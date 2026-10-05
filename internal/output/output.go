// Package output handles TTY detection and rendering for the namecom CLI.
//
// Mode selection (checked independently):
//   - Prompt suppression: based on stdin TTY (suppress prompts when no human is typing)
//   - Output format: based on stdout TTY (default json when piped/redirected)
//
// Color is disabled when NO_COLOR is set (presence-based per spec), when
// stdout is not a TTY, or when --color=never. CLICOLOR_FORCE=1 re-enables
// color even when stdout is not a TTY.
package output

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
	"golang.org/x/term"
	"gopkg.in/yaml.v3"
)

// Format is the output format selected by --output / -o.
type Format string

// Output format constants for the --output / -o flag.
const (
	FormatTable Format = "table"
	FormatJSON  Format = "json"
	FormatYAML  Format = "yaml"
)

// ColorMode maps to --color flag values.
type ColorMode string

// Color mode constants for the --color flag.
const (
	ColorAuto   ColorMode = "auto"
	ColorAlways ColorMode = "always"
	ColorNever  ColorMode = "never"
)

// Config holds the resolved output configuration, built once from global flags
// and stored on the cobra command context.
type Config struct {
	Format Format
	// QuietMode is -q/--quiet. It is a contract for scripts, and it wins over
	// --output: a quiet command prints the same thing in every format.
	//
	//   - list commands print one identifier per line;
	//   - create commands print only the new resource's identifier (an ID, or
	//     the name where the name is the identifier: a domain, a vanity
	//     nameserver hostname, an email mailbox);
	//   - update, delete and other writes print nothing — the exit code is
	//     the result;
	//   - read commands that show one object print its most useful
	//     identifying value, one per line, chosen per command (a comment at
	//     its quiet branch says which and why).
	//
	// Hints, success lines, counts and spinners never print in quiet mode.
	// Warnings and errors still go to stderr. Quiet() is the helper commands
	// use to honour this. A --dry-run preview, `dns export` and `api` print
	// their document regardless: that document is what was asked for.
	QuietMode bool
	NoHeader  bool      // --no-header: omit header row from table output
	Color     ColorMode // --color flag value
	Writer    io.Writer // defaults to os.Stdout
	EWriter   io.Writer // defaults to os.Stderr
	Sandbox   bool      // true when targeting the sandbox API (--sandbox / profile)
	Wide      bool      // --wide: never drop table columns, even if they overflow
	// MaxWidth is the terminal width tables must fit inside. Zero means
	// unconstrained, which is what a pipe or a redirect gets: a consumer that
	// is not a terminal has no width to respect and wants every column.
	MaxWidth int
	// Plain renders tables without borders, as whitespace-aligned columns.
	// DefaultConfig sets it when stdout is not a terminal, so `-o table` into
	// a pipe gives `awk` and `cut` rows they can split, not box-drawing.
	Plain bool
}

// DefaultConfig returns an output config with defaults resolved from the
// current environment (TTY detection, NO_COLOR, CLICOLOR_FORCE).
func DefaultConfig() *Config {
	tty := isStdoutTTY()
	f := FormatJSON
	if tty {
		f = FormatTable
	}
	c := &Config{
		Format:  f,
		Color:   ColorAuto,
		Writer:  os.Stdout,
		EWriter: os.Stderr,
		Plain:   !tty,
	}
	if w, _, err := term.GetSize(int(os.Stdout.Fd())); err == nil && w > 0 {
		c.MaxWidth = w
	}
	return c
}

// IsInteractive reports whether stdin is a TTY — i.e., a human is present.
// Command code uses this to decide whether to show prompts.
//
// It is a variable rather than a plain function so tests can simulate a human
// at the terminal. Several money-spending code paths are reachable only when
// this returns true, and they were untestable — and consequently untested —
// while it read the real TTY unconditionally. Tests must restore the original
// value; see output.StubInteractive.
var IsInteractive = func() bool {
	return isStdinTTY()
}

// StubInteractive forces IsInteractive to return v and returns a function that
// restores the previous implementation. Intended for tests:
//
//	defer output.StubInteractive(true)()
func StubInteractive(v bool) func() {
	prev := IsInteractive
	IsInteractive = func() bool { return v }
	return func() { IsInteractive = prev }
}

// ColorEnabled reports whether ANSI colors should be emitted for this config.
func (c *Config) ColorEnabled() bool {
	switch c.Color {
	case ColorAlways:
		return true
	case ColorNever:
		return false
	}
	// ColorAuto: respect NO_COLOR (presence-based) and CLICOLOR_FORCE, and
	// stay plain on a Windows console that cannot interpret escape codes.
	if _, set := os.LookupEnv("NO_COLOR"); set || !vtSupported {
		return false
	}
	if os.Getenv("CLICOLOR_FORCE") == "1" {
		return true
	}
	return isStdoutTTY()
}

// ApplyColorProfile makes lipgloss's renderer agree with an explicit --color.
//
// ColorEnabled decides whether this package styles a string, but lipgloss
// makes its own terminal check when it renders one: with stdout piped it
// detects no colour support and drops every escape, so `--color always | cat`
// printed plain text. always therefore forces a colour profile when none was
// detected, and never forces plain text. auto leaves lipgloss's detection,
// which already honours NO_COLOR and CLICOLOR_FORCE, alone.
//
// The renderer is process-global, so this is called once, from the root
// command, when the flags are applied.
func (c *Config) ApplyColorProfile() {
	switch c.Color {
	case ColorAlways:
		if lipgloss.ColorProfile() == termenv.Ascii {
			lipgloss.SetColorProfile(termenv.ANSI256)
		}
	case ColorNever:
		lipgloss.SetColorProfile(termenv.Ascii)
	}
}

// Adaptive color tokens — Dark values are vivid for dark terminals; Light
// values are darker variants that stay readable on cream/white backgrounds.
// ANSI 1–8 are terminal-theme-defined and adapt automatically; these cover
// the 256-color palette entries we use for the states that need attention.
var (
	acGreen = lipgloss.AdaptiveColor{Dark: "82", Light: "28"}
	acRed   = lipgloss.AdaptiveColor{Dark: "196", Light: "160"}
	acAmber = lipgloss.AdaptiveColor{Dark: "220", Light: "136"}

	// Lip Gloss styles. Initialized once; use .Render() (not .String()) so
	// color is applied lazily and tests can disable it via NO_COLOR.
	styleSuccess = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("2"))
	styleError   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("1"))
	styleWarning = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("3"))
	styleDim     = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	styleBorder  = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	styleTitle   = lipgloss.NewStyle().Bold(true)

	// Status colors, for the statuses that ask something of the reader: a
	// failure to look into, or something still in progress. Every other
	// status — active, completed, canceled — is plain text (#238).
	statusColors = map[string]lipgloss.AdaptiveColor{
		"expired":   acRed,
		"suspended": acRed,
		"failed":    acRed,
		"rejected":  acRed,
		"pending":   acAmber,
	}
)

// JSON encodes v as indented JSON to the configured writer.
func (c *Config) JSON(v any) error {
	enc := json.NewEncoder(c.Writer)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// YAML encodes v as YAML to the configured writer.
//
// v is converted through its JSON encoding rather than handed to yaml.v3
// directly. yaml.v3 ignores `json` tags, and the SDK types carry no `yaml`
// ones, so every key came out as the lowercased Go field name (`domainname`
// for `domainName`, `domainstotal` for `domains_total`) and every omitempty
// field as `null`. Going through JSON makes the json tags — and any
// MarshalJSON — decide the keys for both formats, so YAML says exactly what
// JSON says.
func (c *Config) YAML(v any) error {
	return writeYAML(c.Writer, v)
}

func writeYAML(w io.Writer, v any) error {
	node, err := yamlNode(v)
	if err != nil {
		return err
	}
	enc := yaml.NewEncoder(w)
	if err := enc.Encode(node); err != nil {
		return err
	}
	return enc.Close()
}

// yamlNode builds a YAML tree from v's JSON encoding. A yaml.Node rather than
// a map[string]any, because yaml.v3 sorts map keys and JSON output keeps
// struct field order: the tree preserves it.
func yamlNode(v any) (*yaml.Node, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	return jsonToNode(dec)
}

// yamlString returns s as a string node, for a value or a key.
//
// The explicit !!str tag makes the encoder quote a string that would otherwise
// read back as another type ("123", "true", "null"). That is not enough for
// every string: left to choose a style, yaml.v3 writes some in a form that
// reads back differently or not at all. A leading line break is lost by the
// block scalar it picks ("\n" came back as ""), a tab before a line break
// produces YAML it cannot parse, and a plain "<<" key is a merge key. Rather
// than track each case, any string with a control character, a Unicode line
// separator, a byte order mark or surrounding whitespace is double-quoted,
// where everything is escaped and nothing is folded or trimmed.
func yamlString(s string) *yaml.Node {
	n := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: s}
	if s == "<<" || strings.TrimSpace(s) != s || strings.ContainsFunc(s, func(r rune) bool {
		return unicode.IsControl(r) || r == '\u2028' || r == '\u2029' || r == '\ufeff'
	}) {
		n.Style = yaml.DoubleQuotedStyle
	}
	return n
}

// jsonToNode consumes one JSON value from dec and returns it as a node.
func jsonToNode(dec *json.Decoder) (*yaml.Node, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	switch t := tok.(type) {
	case json.Delim:
		n := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		if t == '{' {
			n.Kind, n.Tag = yaml.MappingNode, "!!map"
		}
		for dec.More() {
			if n.Kind == yaml.MappingNode {
				key, err := dec.Token()
				if err != nil {
					return nil, err
				}
				n.Content = append(n.Content, yamlString(key.(string)))
			}
			child, err := jsonToNode(dec)
			if err != nil {
				return nil, err
			}
			n.Content = append(n.Content, child)
		}
		if _, err := dec.Token(); err != nil { // closing delimiter
			return nil, err
		}
		return n, nil
	case string:
		return yamlString(t), nil
	case json.Number:
		tag := "!!int"
		if strings.ContainsAny(t.String(), ".eE") {
			tag = "!!float"
		}
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: tag, Value: t.String()}, nil
	case bool:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: fmt.Sprint(t)}, nil
	default: // nil
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!null", Value: "null"}, nil
	}
}

// listEnvelope wraps paginated list results with metadata for agent consumers.
// nextPage and total are omitted when zero/nil.
type listEnvelope struct {
	Data     any    `json:"data" yaml:"data"`
	NextPage *int32 `json:"nextPage,omitempty" yaml:"nextPage,omitempty"`
	Total    int32  `json:"total,omitempty" yaml:"total,omitempty"`
}

// newListEnvelope builds the envelope both list encoders share.
//
// A nil slice becomes an empty one. List commands accumulate pages with
// append, and appending an empty page to a nil slice leaves it nil, so an
// empty list encoded as `"data": null` — which `jq '.data[]'` refuses to
// iterate — although the API itself had returned `[]`.
func newListEnvelope(data any, nextPage *int32, total int32) listEnvelope {
	if v := reflect.ValueOf(data); v.Kind() == reflect.Slice && v.IsNil() {
		data = reflect.MakeSlice(v.Type(), 0, 0).Interface()
	}
	env := listEnvelope{Data: data, Total: total}
	if nextPage != nil && *nextPage != 0 {
		env.NextPage = nextPage
	}
	return env
}

// JSONList encodes data as a pagination envelope: {"data":[…],"nextPage":N,"total":N}.
// nextPage is omitted when nil or zero; total is omitted when zero.
func (c *Config) JSONList(data any, nextPage *int32, total int32) error {
	return c.JSON(newListEnvelope(data, nextPage, total))
}

// YAMLList encodes data as a pagination envelope in YAML.
func (c *Config) YAMLList(data any, nextPage *int32, total int32) error {
	return c.YAML(newListEnvelope(data, nextPage, total))
}

// TableOption adjusts how one Table call fits the terminal.
type TableOption func(*tableOpts)

type tableOpts struct {
	essential map[string]bool
}

// Essential marks columns, named by header, that are never dropped to fit
// the terminal. They can still be shortened with "…". It is for the column
// that carries the point of the table — the DNS answer, the domains an
// unverified contact puts at risk — which, being the widest, used to be the
// first to go.
func Essential(headers ...string) TableOption {
	return func(o *tableOpts) {
		if o.essential == nil {
			o.essential = map[string]bool{}
		}
		for _, h := range headers {
			o.essential[h] = true
		}
	}
}

// minColWidth is how narrow fitColumns shortens a column before it starts
// dropping columns instead. Twenty characters keeps enough of a hostname, a
// TXT value or a domain name to tell rows apart.
const minColWidth = 20

// Table renders headers and rows as a bordered table that fits the terminal.
//
// Tables were rendered at their natural width with no regard for the terminal:
// `domain list` came to 113 columns, `order list` 99, `dns list` 87. In an
// 80-column terminal every one of them wrapped and the rounded borders came
// apart. Trailing columns were then dropped to fit — but one long value was
// enough to drop everything after it: an SPF record left `dns list` showing
// only ID and HOST, the answer itself gone, even at 120 columns (#233).
//
// So the widest cells are shortened with "…" first, down to minColWidth, and
// only then are columns dropped: from the right, never the first column, and
// never one marked Essential. A footer says what was hidden or shortened.
// --wide opts out; so does any non-terminal writer, since a pipe has no width
// to fit and its consumer wants the whole row.
func (c *Config) Table(headers []string, rows [][]string, opts ...TableOption) {
	var o tableOpts
	for _, opt := range opts {
		opt(&o)
	}
	rows = dashEmpty(rows, 0)
	if c.Plain {
		if c.NoHeader {
			headers = nil
		}
		c.plainTable(headers, rows)
		return
	}
	color := c.ColorEnabled()
	headers, rows, dropped, truncated := c.fitColumns(headers, rows, o.essential)

	cell := lipgloss.NewStyle().Padding(0, 1)
	header := cell.Bold(color)
	if color {
		header = header.Foreground(lipgloss.Color("15")) // bright white
	}

	styleFunc := func(row, _ int) lipgloss.Style {
		if row == table.HeaderRow {
			return header
		}
		return cell
	}

	t := table.New().
		StyleFunc(styleFunc).
		Border(lipgloss.RoundedBorder()).
		BorderColumn(true)
	if !c.NoHeader {
		t = t.Headers(headers...)
	}

	if color {
		t = t.BorderStyle(styleBorder)
	}

	for _, row := range rows {
		t.Row(row...)
	}

	fmt.Fprintln(c.Writer, t.Render())

	var notes []string
	if len(dropped) > 0 {
		notes = append(notes, fmt.Sprintf("%s hidden (%s)",
			Plural(len(dropped), "column"), strings.Join(dropped, ", ")))
	}
	if truncated {
		notes = append(notes, "long values cut short with …")
	}
	if len(notes) > 0 {
		fmt.Fprintln(c.EWriter, c.Dim(strings.Join(notes, "; ")+
			" — widen the terminal, pass --wide, or use -o json"))
	}
}

// None is what a table shows for a missing value.
const None = "—"

// dashEmpty returns rows with each empty cell, from column from onward,
// replaced by None. rows itself is not modified.
//
// A missing value was a blank cell in some tables and "—" in others — a
// missing DNS priority was blank, a missing price in `domain check` a dash
// (#238). A blank cell also reads as a rendering failure, and in a plain
// table it vanishes under awk's whitespace splitting, shifting every later
// field left. Doing it here covers every table without each caller having to
// remember.
func dashEmpty(rows [][]string, from int) [][]string {
	out := make([][]string, len(rows))
	for i, r := range rows {
		out[i] = r
		for j := from; j < len(r); j++ {
			if strings.TrimSpace(ansi.Strip(r[j])) == "" {
				if sameBacking(out[i], r) {
					out[i] = append([]string(nil), r...)
				}
				out[i][j] = None
			}
		}
	}
	return out
}

// sameBacking reports whether a and b share their first element's storage.
func sameBacking(a, b []string) bool {
	return len(a) > 0 && len(b) > 0 && &a[0] == &b[0]
}

// plainTable prints headers (when non-nil) and rows as columns aligned with
// spaces, two between each, with no borders and no trailing whitespace —
// the shape `gh` prints into a pipe (#233). The bordered table kept its box
// drawing when piped, so `namecom domain list -o table | awk '{print $1}'`
// printed "╭───" and "│". Line breaks and tabs in a cell become spaces, so
// every row stays one line.
func (c *Config) plainTable(headers []string, rows [][]string) {
	all := rows
	if headers != nil {
		all = append([][]string{headers}, rows...)
	}
	n := 0
	for _, r := range all {
		n = max(n, len(r))
	}
	clean := make([][]string, len(all))
	widths := make([]int, n)
	for i, r := range all {
		clean[i] = make([]string, len(r))
		for j, v := range r {
			v = strings.Map(func(r rune) rune {
				if r == '\n' || r == '\r' || r == '\t' {
					return ' '
				}
				return r
			}, v)
			clean[i][j] = v
			widths[j] = max(widths[j], lipgloss.Width(v))
		}
	}
	for _, r := range clean {
		var b strings.Builder
		for j, v := range r {
			if j > 0 {
				b.WriteString("  ")
			}
			b.WriteString(v)
			if j < len(r)-1 {
				b.WriteString(strings.Repeat(" ", widths[j]-lipgloss.Width(v)))
			}
		}
		fmt.Fprintln(c.Writer, strings.TrimRight(b.String(), " "))
	}
}

// fitColumns makes a table fit MaxWidth. It shortens the widest columns, and
// if that is not enough, drops trailing columns that are not essential. It
// returns the surviving headers and rows, the names of the columns dropped,
// and whether any cell was shortened.
//
// The first column is never dropped: a table of nothing but a row count is
// worse than one that overflows, and callers put the identifying column first.
func (c *Config) fitColumns(headers []string, rows [][]string, essential map[string]bool) ([]string, [][]string, []string, bool) {
	if c.Wide || c.MaxWidth <= 0 || len(headers) == 0 {
		return headers, rows, nil, false
	}

	keep := make([]int, len(headers)) // indexes of the surviving columns
	for i := range keep {
		keep[i] = i
	}
	var natural, widths []int
	for {
		natural = colWidths(headers, rows, keep)
		var fits bool
		widths, fits = shrinkToFit(headers, keep, natural, c.MaxWidth)
		if fits {
			break
		}
		drop := -1
		for k := len(keep) - 1; k > 0; k-- {
			if !essential[headers[keep[k]]] {
				drop = k
				break
			}
		}
		if drop < 0 {
			break // nothing left to drop: overflow at the narrowest widths
		}
		keep = append(keep[:drop:drop], keep[drop+1:]...)
	}
	if len(keep) == len(headers) && slices.Equal(widths, natural) {
		return headers, rows, nil, false
	}

	kept := make(map[int]bool, len(keep))
	for _, i := range keep {
		kept[i] = true
	}
	var dropped []string
	for i, h := range headers {
		if !kept[i] {
			dropped = append(dropped, strings.ToLower(h))
		}
	}

	outHeaders := make([]string, len(keep))
	for k, i := range keep {
		outHeaders[k] = headers[i]
	}
	truncated := false
	outRows := make([][]string, 0, len(rows))
	for _, r := range rows {
		nr := make([]string, 0, len(keep))
		for k, i := range keep {
			if i >= len(r) {
				break
			}
			v := r[i]
			if lipgloss.Width(v) > widths[k] {
				v = ansi.Truncate(v, widths[k], "…")
				truncated = true
			}
			nr = append(nr, v)
		}
		outRows = append(outRows, nr)
	}
	return outHeaders, outRows, dropped, truncated
}

// shrinkToFit narrows the widest column a character at a time until the table
// fits maxWidth, taking no column below minColWidth or its header's width. It
// returns the widths and whether they fit.
func shrinkToFit(headers []string, keep, natural []int, maxWidth int) ([]int, bool) {
	widths := append([]int(nil), natural...)
	floor := make([]int, len(widths))
	for k, i := range keep {
		floor[k] = min(natural[k], max(minColWidth, lipgloss.Width(headers[i])))
	}
	for tableWidth(widths) > maxWidth {
		widest := -1
		for k, w := range widths {
			if w > floor[k] && (widest < 0 || w > widths[widest]) {
				widest = k
			}
		}
		if widest < 0 {
			return widths, false
		}
		widths[widest]--
	}
	return widths, true
}

// colWidths measures the columns at the given indexes at their natural
// (widest-cell) width. lipgloss.Width is used rather than len because cells
// arrive pre-styled — ExpiryDate returns ANSI escapes — and because a domain
// may hold wide runes.
func colWidths(headers []string, rows [][]string, cols []int) []int {
	w := make([]int, len(cols))
	for k, i := range cols {
		if i < len(headers) {
			w[k] = lipgloss.Width(headers[i])
		}
	}
	for _, r := range rows {
		for k, i := range cols {
			if i < len(r) {
				if cw := lipgloss.Width(r[i]); cw > w[k] {
					w[k] = cw
				}
			}
		}
	}
	return w
}

// tableWidth is the rendered width of a table with the given column widths:
// each column carries one space of padding on each side, and there is a border
// rune before the first column, between every pair, and after the last.
func tableWidth(widths []int) int {
	total := len(widths) + 1
	for _, w := range widths {
		total += w + 2
	}
	return total
}

// KVTable renders a headerless two-column key-value table with styled field names.
func (c *Config) KVTable(rows [][]string) {
	// Structured modes get their data from the caller's own JSON/YAML encoding;
	// emitting an ASCII table here would append a second, unparseable document.
	// Every sibling renderer (Success, Hint, Step, Count, Empty, Title) already
	// gates on format — this one did not, which is how `auth status -o json`
	// came to print an envelope followed by a table.
	if c.Format == FormatJSON || c.Format == FormatYAML || c.QuietMode {
		return
	}
	rows = dashEmpty(rows, 1)
	if c.Plain {
		c.plainTable(nil, rows)
		return
	}
	color := c.ColorEnabled()

	valueStyle := lipgloss.NewStyle().Padding(0, 1)
	fieldStyle := lipgloss.NewStyle().Padding(0, 1)
	if color {
		fieldStyle = fieldStyle.Bold(true) // bold, not blue: colour is for what needs action
	}

	styleFunc := func(_, col int) lipgloss.Style {
		if col == 0 {
			return fieldStyle
		}
		return valueStyle
	}

	t := table.New().
		StyleFunc(styleFunc).
		Border(lipgloss.RoundedBorder()).
		BorderColumn(true)

	if color {
		t = t.BorderStyle(styleBorder)
	}

	for _, row := range rows {
		t.Row(row...)
	}

	// Wrap values to the terminal, as Table drops columns to fit it: a long
	// value — a nameserver list, a forwarding URL — otherwise ran past the
	// edge and the borders came apart. Width is set only when the table is too
	// wide, because lipgloss also stretches a narrower table to fill it.
	// --wide and a non-terminal writer keep the natural width.
	if !c.Wide && c.MaxWidth > 0 && tableWidth(colWidths(nil, rows, []int{0, 1})) > c.MaxWidth {
		t = t.Width(c.MaxWidth)
	}

	fmt.Fprintln(c.Writer, t.Render())
}

// PrintQuiet prints each value on its own line — used for -q/--quiet mode so
// scripts can pipe IDs: `namecom dns list d.com -q | xargs ...`
func (c *Config) PrintQuiet(vals []string) {
	for _, v := range vals {
		fmt.Fprintln(c.Writer, v)
	}
}

// Quiet reports whether quiet mode is on, and when it is, prints vals one per
// line first, skipping empty ones. Commands call it ahead of their format
// switch so -q overrides -o json as well as the table:
//
//	if out.Quiet(id) {
//		return nil
//	}
//
// Called with no values it prints nothing, which is the quiet output of every
// update and delete.
func (c *Config) Quiet(vals ...string) bool {
	if !c.QuietMode {
		return false
	}
	for _, v := range vals {
		if v != "" {
			fmt.Fprintln(c.Writer, v)
		}
	}
	return true
}

// SandboxTag returns a styled "[sandbox]" badge when c.Sandbox is set, or ""
// otherwise. Used to flag output that came from the sandbox API so it's never
// mistaken for a production result.
func (c *Config) SandboxTag() string {
	if !c.Sandbox {
		return ""
	}
	if c.ColorEnabled() {
		return lipgloss.NewStyle().Bold(true).Foreground(acAmber).Render("[sandbox]") + " "
	}
	return "[sandbox] "
}

// Success reports that a mutating command completed.
//
// Many mutating commands (deletes, toggles, imports) have no format switch of
// their own and call only this. Printing "✓ <msg>" unconditionally meant
// `namecom dns delete … -o json | jq .` received a checkmark line and failed to
// parse — and since DefaultConfig() selects JSON whenever stdout is not a TTY,
// that was the default in every pipe. So structured modes get a structured
// envelope here, matching Hint/Step/Count/Empty, which already gate on format.
func (c *Config) Success(msg string) {
	if c.QuietMode {
		return
	}
	switch c.Format {
	case FormatJSON:
		enc := json.NewEncoder(c.Writer)
		enc.SetIndent("", "  ")
		_ = enc.Encode(map[string]any{"success": true, "message": msg})
		return
	case FormatYAML:
		_ = writeYAML(c.Writer, map[string]any{"success": true, "message": msg})
		return
	}
	if c.ColorEnabled() {
		fmt.Fprintln(c.Writer, styleSuccess.Render("✓")+" "+c.SandboxTag()+msg)
	} else {
		fmt.Fprintln(c.Writer, "✓ "+c.SandboxTag()+msg)
	}
}

// Note prints a dim line of information to stderr — something worth knowing
// that is neither a warning nor a next step, such as which check sandbox mode
// uses. Shown only in table mode, and never in quiet mode.
//
// The CLI has four status symbols: ✓ success, ! warning, ✗ error and → next
// step (#238). A note used to borrow →, as in "→ Sandbox mode: …", which made
// it read as an instruction; it carries no symbol now.
func (c *Config) Note(msg string) {
	if c.Format != FormatTable || c.QuietMode {
		return
	}
	fmt.Fprintln(c.EWriter, c.Dim(msg))
}

// Hint prints a dimmed next-step suggestion to stderr — shown only in table
// mode, and never in quiet mode.
//
// It went to stdout, so `domain list -o table > domains.txt` saved the
// "→ Run …" lines with the table (#233). stdout is for the result; anything
// said about the result goes to stderr, as gh does.
func (c *Config) Hint(msg string) {
	if c.Format != FormatTable || c.QuietMode {
		return
	}
	if c.ColorEnabled() {
		arrow := styleDim.Render("→")
		fmt.Fprintln(c.EWriter, arrow+" "+styleDim.Render(msg))
	} else {
		fmt.Fprintln(c.EWriter, "→ "+msg)
	}
}

// Warn prints a warning message to stderr.
func (c *Config) Warn(msg string) {
	if c.ColorEnabled() {
		fmt.Fprintln(c.EWriter, styleWarning.Render("!")+" "+msg)
	} else {
		fmt.Fprintln(c.EWriter, "! "+msg)
	}
}

// hintable is implemented by errors that carry an actionable suggestion.
type hintable interface {
	UserHint() string
}

// detailer is implemented by errors that carry structured detail for the
// JSON/YAML error envelope, such as a raw API response body.
type detailer interface {
	ErrorDetails() any
}

// Error prints a user-facing error to stderr. In JSON output mode the error is
// emitted as a structured envelope so agents can parse failures.
func (c *Config) Error(err error) {
	hint := errorHint(err)

	// Structured modes get a machine-readable envelope. The plain-text fallback
	// emits "error: <msg>", which looks like YAML but stops parsing as soon as
	// the message contains ": " — which nearly every wrapped error does.
	if c.Format == FormatJSON || c.Format == FormatYAML {
		e := map[string]any{"message": err.Error()}
		if d := detailer(nil); errors.As(err, &d) {
			if details := d.ErrorDetails(); details != nil {
				e["details"] = details
			}
		}
		env := map[string]any{"error": e}
		if hint != "" {
			env["hint"] = hint
		}
		if c.Format == FormatYAML {
			_ = writeYAML(c.EWriter, env)
			return
		}
		enc := json.NewEncoder(c.EWriter)
		enc.SetIndent("", "  ")
		_ = enc.Encode(env)
		return
	}
	msg := err.Error()
	if c.ColorEnabled() {
		fmt.Fprintln(c.EWriter, styleError.Render("✗")+" "+msg)
		if hint != "" {
			fmt.Fprintln(c.EWriter, styleDim.Render("  hint: "+hint))
		}
	} else {
		fmt.Fprintln(c.EWriter, "error: "+msg)
		if hint != "" {
			fmt.Fprintln(c.EWriter, "  hint: "+hint)
		}
	}
}

// errorHint returns the "what to do" line for err, or "" when there is none.
//
// The outermost error in the chain that has a UserHint decides, even when it
// returns "". A wrapper that rewrites the message rewrites the advice with it,
// and an empty hint is how it says the message already carries it:
// `domain "x" not found — run 'namecom domain list' …` used to be followed by
// the 404's generic "check the domain name or ID", saying it twice (#234).
func errorHint(err error) string {
	// Walk to the first hintable cause (e.g. fmt.Errorf("fetching: %w", apiErr)).
	// The loop stops at a nil cause: *net.DNSError and *json.UnmarshalTypeError
	// both have an Unwrap that returns nil, and assigning that to err crashed
	// the err.Error() below on every DNS failure and undecodable response.
	for cause := err; cause != nil; cause = errors.Unwrap(cause) {
		if h, ok := cause.(hintable); ok {
			return h.UserHint()
		}
	}
	// Network-level failures. Any DNS failure counts, not only "no such host":
	// a resolver that times out or misbehaves is still the network.
	if _, ok := errors.AsType[*net.DNSError](err); ok {
		return "could not reach the API — check your network connection"
	}
	msg := err.Error()
	if strings.Contains(msg, "no such host") ||
		strings.Contains(msg, "connection refused") ||
		strings.Contains(msg, "dial tcp") ||
		strings.Contains(msg, "i/o timeout") {
		return "could not reach the API — check your network connection"
	}
	return ""
}

// statusColor returns the colour for status, and whether it has one. A
// prefix matches, for compound statuses like "pending_transfer".
func statusColor(status string) (lipgloss.AdaptiveColor, bool) {
	key := strings.ToLower(status)
	for k, v := range statusColors {
		if key == k || strings.HasPrefix(key, k) {
			return v, true
		}
	}
	return lipgloss.AdaptiveColor{}, false
}

// StatusBadge returns a domain, transfer or order status, coloured only when
// it needs attention: red for a failure, amber for pending. Every status had
// a coloured dot, so a list of healthy domains was a column of green (#238).
func (c *Config) StatusBadge(status string) string {
	color, ok := statusColor(status)
	if !c.ColorEnabled() || !ok {
		return status
	}
	dot := lipgloss.NewStyle().Foreground(color).Render("●")
	text := lipgloss.NewStyle().Bold(true).Foreground(color).Render(status)
	return dot + " " + text
}

// TypeBadge returns a DNS record type, upper-cased and bold. It was drawn on a
// coloured background, one colour per type, which put nine colours on a
// `dns list` that asked nothing of the reader (#238).
func (c *Config) TypeBadge(typ string) string {
	upper := strings.ToUpper(typ)
	if !c.ColorEnabled() {
		return upper
	}
	return styleTitle.Render(upper)
}

// AvailabilityBadge returns "✓ available" or "taken". A taken name is not an
// error, so it no longer carries ✗ or red; an available one is what the
// reader can act on, and keeps the check and the colour.
func (c *Config) AvailabilityBadge(purchasable bool) string {
	if !purchasable {
		return "taken"
	}
	if !c.ColorEnabled() {
		return "✓ available"
	}
	return styleSuccess.Render("✓") + " " + lipgloss.NewStyle().Foreground(acGreen).Render("available")
}

// BoolBadge returns "yes" or "no", in plain text.
//
// It returned a bold green "✓ yes" or red "✗ no", so a 250-row domain list
// was mostly green, and red landed on harmless values — Premium "no", Privacy
// "no", a TLD without DNSSEC (#238). Colour is for values that need action;
// use BoolAlert for those.
func (c *Config) BoolBadge(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// BoolAlert returns "yes" or "no" like BoolBadge, in amber when b equals
// alertOn: the value that needs the reader's attention, such as a domain that
// is not locked against transfer.
func (c *Config) BoolAlert(b, alertOn bool) string {
	s := c.BoolBadge(b)
	if b == alertOn {
		return c.Amber(s)
	}
	return s
}

// ExpiryDate formats a domain expiry date with color urgency indicators and a
// human-readable relative time suffix ("in 14 days", "12 days ago").
func (c *Config) ExpiryDate(t *time.Time) string {
	if t == nil {
		return ""
	}
	date := t.Format("2006-01-02")
	days := time.Until(*t).Hours() / 24
	rel := relativeTime(days)
	label := date + " (" + rel + ")"
	if !c.ColorEnabled() {
		return label
	}
	return expiryStyle(days).Render(label)
}

// expiryStyle picks the urgency styling for a date this many days away.
//
// Split out from ExpiryDate so the thresholds can be asserted directly: off a
// TTY lipgloss degrades every style to a no-op, so a test comparing rendered
// output cannot tell red from dim and passes whatever the code does.
func expiryStyle(days float64) lipgloss.Style {
	switch {
	case days < 7: // expired, or expiring inside a week
		return lipgloss.NewStyle().Bold(true).Foreground(acRed)
	case days < 30:
		return lipgloss.NewStyle().Bold(true).Foreground(acAmber)
	default:
		return styleDim
	}
}

// relativeTime converts a floating-point day count into a human-readable
// string, widening the unit as the distance grows.
//
// It used to speak only days, which is right near an expiry and useless far
// from one: a domain paid through 2034 rendered as "in 2750 days", a number no
// reader converts to anything meaningful. Days stay exact inside a quarter,
// where renewal decisions actually happen; past that the unit widens. The
// absolute date sits immediately before this string in every caller, so the
// parenthetical only has to convey magnitude.
func relativeTime(days float64) string {
	abs := days
	if abs < 0 {
		abs = -abs
	}
	switch {
	case days < 0 && abs < 1:
		return "expired today"
	case days < 1 && days >= 0:
		return "today"
	}
	unit, n := humanizeDays(abs)
	if days < 0 {
		return fmt.Sprintf("%d %s ago", n, plural(unit, n))
	}
	return fmt.Sprintf("in %d %s", n, plural(unit, n))
}

// humanizeDays picks the coarsest unit that still says something useful about a
// span, and returns the count in that unit.
func humanizeDays(abs float64) (unit string, n int) {
	switch {
	case abs <= 90:
		return "day", int(abs + 0.5)
	case abs < 730:
		return "month", int(abs/30.44 + 0.5)
	default:
		return "year", int(abs/365.25 + 0.5)
	}
}

func plural(unit string, n int) string {
	if n == 1 {
		return unit
	}
	return unit + "s"
}

// Plural returns n and noun together, with thousands separators and the noun
// pluralised: "1 year", "2 years", "6,522 domains". A noun ending in a
// consonant and y takes "ies" ("2 entries"); one ending in s, x, sh or ch
// takes "es"; anything else takes "s".
func Plural(n int, noun string) string {
	return Thousands(n) + " " + PluralNoun(n, noun)
}

// PluralNoun returns noun pluralised for n, without the number.
func PluralNoun(n int, noun string) string {
	if n == 1 || n == -1 || noun == "" {
		return noun
	}
	if len(noun) > 1 && strings.HasSuffix(noun, "y") && !strings.ContainsRune("aeiou", rune(noun[len(noun)-2])) {
		return noun[:len(noun)-1] + "ies"
	}
	for _, suf := range []string{"s", "x", "sh", "ch"} {
		if strings.HasSuffix(noun, suf) {
			return noun + "es"
		}
	}
	return noun + "s"
}

// Thousands formats n with comma thousands separators: 6522 → "6,522".
func Thousands(n int) string {
	s := strconv.Itoa(n)
	sign := ""
	if strings.HasPrefix(s, "-") {
		sign, s = "-", s[1:]
	}
	var b strings.Builder
	for i, r := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	return sign + b.String()
}

// spinFrames are the animation frames for the spinner.
var spinFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// spinnerTTY is the terminal check Spin and StartSpinner make. It is a
// variable, like IsInteractive, because under go test stderr is never a
// terminal: every spinner was a no-op there, so nothing could show that a
// command stops the spinner it starts. See RecordSpinners.
//
// A Windows console without virtual terminal processing counts as no
// terminal: the spinner's `\r\033[K` would print as text there.
var spinnerTTY = func() bool { return vtSupported && isStderrTTY() }

// spinRecorder, when set by RecordSpinners, stands in for the animation.
var spinRecorder *SpinRecorder

// SpinRecorder records the spinners Spin and StartSpinner start and stop, so a
// test can assert that a command leaves none running. Obtain one from
// RecordSpinners.
type SpinRecorder struct {
	mu      sync.Mutex
	started []string
	running map[int]string
	shown   []string
}

// RecordSpinners makes Spin and StartSpinner behave as they do on a terminal —
// subject to the same format and --quiet checks — but record each spinner in
// the returned SpinRecorder instead of drawing it. The returned function
// restores the previous behaviour. Intended for tests:
//
//	rec, restore := output.RecordSpinners()
//	defer restore()
func RecordSpinners() (*SpinRecorder, func()) {
	prevTTY, prevRec := spinnerTTY, spinRecorder
	r := &SpinRecorder{running: map[int]string{}}
	spinnerTTY, spinRecorder = func() bool { return true }, r
	return r, func() { spinnerTTY, spinRecorder = prevTTY, prevRec }
}

// Started returns the message of every spinner started, in order.
func (r *SpinRecorder) Started() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.started...)
}

// Running returns the messages of spinners started and not yet stopped, in
// the order they were started.
func (r *SpinRecorder) Running() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var msgs []string
	for i, m := range r.started {
		if _, ok := r.running[i]; ok {
			msgs = append(msgs, m)
		}
	}
	return msgs
}

// Shown returns every text a spinner would have drawn, in order: each
// spinner's starting message, then each change to it from Update or
// SpinnerNote.
func (r *SpinRecorder) Shown() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.shown...)
}

// start records a spinner and returns the function that records its stop.
func (r *SpinRecorder) start(msg string) func() {
	r.mu.Lock()
	id := len(r.started)
	r.started = append(r.started, msg)
	r.running[id] = msg
	r.shown = append(r.shown, msg)
	r.mu.Unlock()
	return func() {
		r.mu.Lock()
		delete(r.running, id)
		r.mu.Unlock()
	}
}

// show records a change to a running spinner's text.
func (r *SpinRecorder) show(text string) {
	r.mu.Lock()
	r.shown = append(r.shown, text)
	r.mu.Unlock()
}

// spinLine is the text one spinner draws: its message, then the note
// SpinnerNote last gave it, if any.
type spinLine struct {
	mu        sync.Mutex
	msg, note string
	rec       *SpinRecorder // set when recording instead of drawing
}

func (l *spinLine) text() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.note == "" {
		return l.msg
	}
	return l.msg + " " + l.note
}

// setMsg replaces the message and drops the note, which was about the step
// the old message described.
func (l *spinLine) setMsg(msg string) { l.change(func() { l.msg, l.note = msg, "" }) }

// setNote replaces the note.
func (l *spinLine) setNote(note string) { l.change(func() { l.note = note }) }

func (l *spinLine) change(f func()) {
	l.mu.Lock()
	f()
	l.mu.Unlock()
	if l.rec != nil {
		l.rec.show(l.text())
	}
}

// spinning is the spinners being drawn now, oldest first. It is package
// state, like spinnerTTY, because there is one stderr to draw on, and the
// code that learns of a retry — the API client's hook — holds no spinner.
var spinning struct {
	mu    sync.Mutex
	lines []*spinLine
}

// startLine registers a spinner that is being drawn.
func startLine(msg string, rec *SpinRecorder) *spinLine {
	l := &spinLine{msg: msg, rec: rec}
	spinning.mu.Lock()
	spinning.lines = append(spinning.lines, l)
	spinning.mu.Unlock()
	return l
}

// end unregisters l. It is safe to call on a line never registered.
func (l *spinLine) end() {
	spinning.mu.Lock()
	defer spinning.mu.Unlock()
	for i, s := range spinning.lines {
		if s == l {
			spinning.lines = append(spinning.lines[:i], spinning.lines[i+1:]...)
			return
		}
	}
}

// SpinnerNote shows note after the message of the spinner on screen — the
// one started last — and reports whether there was one. A line printed to
// stderr while a spinner runs lands on top of its frame (#232), so a caller
// with something to say mid-operation, such as a retry, tries this first and
// prints a line only when it returns false. The note stays until the spinner
// stops, its message is updated, or another note replaces it.
func SpinnerNote(note string) bool {
	spinning.mu.Lock()
	var l *spinLine
	if n := len(spinning.lines); n > 0 {
		l = spinning.lines[n-1]
	}
	spinning.mu.Unlock()
	if l == nil {
		return false
	}
	l.setNote(note)
	return true
}

// Spin starts a spinner on stderr with the given message and returns a stop
// function. Call the returned function when the operation completes.
// In non-TTY or non-table mode it's a no-op (no spinner, no output).
func (c *Config) Spin(msg string) func() {
	return c.StartSpinner(msg).Stop
}

// Spinner is a running spinner whose message can be updated mid-operation.
// Obtain one via StartSpinner; call Stop when the operation completes.
type Spinner struct {
	line   *spinLine // nil when nothing is drawn
	stop   chan struct{}
	wg     sync.WaitGroup
	once   sync.Once
	onStop func() // set when a SpinRecorder is recording this spinner
}

// Stop halts the spinner and clears the line.
func (s *Spinner) Stop() {
	s.once.Do(func() {
		if s.line != nil {
			s.line.end()
		}
		close(s.stop)
		s.wg.Wait()
		if s.onStop != nil {
			s.onStop()
		}
	})
}

// Update changes the message shown next to the spinner frame, dropping any
// note SpinnerNote added to the old one.
func (s *Spinner) Update(msg string) {
	if s.line != nil {
		s.line.setMsg(msg)
	}
}

// StartSpinner starts a spinner that can be updated via Update while running.
// In non-TTY or non-table mode it is a no-op (Stop/Update are still safe to call).
func (c *Config) StartSpinner(msg string) *Spinner {
	s := &Spinner{stop: make(chan struct{})}
	if !spinnerTTY() || c.Format != FormatTable || c.QuietMode {
		return s
	}
	if spinRecorder != nil {
		s.onStop = spinRecorder.start(msg)
		s.line = startLine(msg, spinRecorder)
		return s
	}
	s.line = startLine(msg, nil)
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		ticker := time.NewTicker(80 * time.Millisecond)
		defer ticker.Stop()
		i := 0
		for {
			select {
			case <-s.stop:
				fmt.Fprintf(c.EWriter, "\r\033[K")
				return
			case <-ticker.C:
				frame := spinFrames[i%len(spinFrames)]
				if c.ColorEnabled() {
					frame = styleDim.Render(frame)
				}
				// Clear to the end of the line: the text shrinks when a note
				// is dropped or the message is updated to a shorter one.
				fmt.Fprintf(c.EWriter, "\r%s %s\033[K", frame, s.line.text())
				i++
			}
		}
	}()
	return s
}

// DryRunRequest is one request a --dry-run would have sent, as the structured
// output formats print it. Body is omitted when nil.
type DryRunRequest struct {
	DryRun bool   `json:"dry_run"`
	Method string `json:"method"`
	Path   string `json:"path"`
	Body   any    `json:"body,omitempty"`
}

// DryRun prints the request a --dry-run would have sent.
// Pass a struct or map as body to pretty-print it as indented JSON; pass nil for no body.
//
// JSON and YAML modes get a {"dry_run": true, "method", "path", "body"}
// document rather than the request line: a script that asked for -o json had
// to parse text to inspect the planned request. That includes the non-TTY
// JSON default, as it does for Success — the text form in a pipe was the one
// thing a `| jq` could not read.
//
// A body that cannot be encoded is an error, and nothing is printed. The error
// used to be discarded, so a --price of +Inf previewed as no output at all in
// JSON and YAML, and as a request line with no body in table mode — and the
// command exited 0 as though the preview had worked.
func (c *Config) DryRun(method, path string, body any) error {
	req := DryRunRequest{DryRun: true, Method: method, Path: path, Body: body}
	switch c.Format {
	case FormatJSON:
		return dryRunErr(c.JSON(req))
	case FormatYAML:
		return dryRunErr(c.YAML(req))
	}
	return c.dryRunText([]DryRunRequest{req})
}

// DryRunAll prints several previewed requests: one array in JSON and YAML
// modes, so the plan parses as a single document, and one request line each
// in table mode. The DryRun field of each request is set here. As with
// DryRun, a body that cannot be encoded fails the whole preview.
func (c *Config) DryRunAll(reqs []DryRunRequest) error {
	all := make([]DryRunRequest, len(reqs))
	for i, r := range reqs {
		r.DryRun = true
		all[i] = r
	}
	switch c.Format {
	case FormatJSON:
		return dryRunErr(c.JSON(all))
	case FormatYAML:
		return dryRunErr(c.YAML(all))
	}
	return c.dryRunText(all)
}

func dryRunErr(err error) error {
	if err != nil {
		return fmt.Errorf("previewing request: %w", err)
	}
	return nil
}

// dryRunText encodes every body before printing anything, so a failure
// leaves no request line behind without its body.
func (c *Config) dryRunText(reqs []DryRunRequest) error {
	bodies := make([][]byte, len(reqs))
	for i, r := range reqs {
		if r.Body == nil {
			continue
		}
		b, err := json.MarshalIndent(r.Body, "", "  ")
		if err != nil {
			return dryRunErr(err)
		}
		bodies[i] = b
	}
	for i, r := range reqs {
		if c.ColorEnabled() {
			tag := lipgloss.NewStyle().Foreground(lipgloss.Color("220")).Bold(true).Render("dry-run")
			m := lipgloss.NewStyle().Foreground(lipgloss.Color("111")).Bold(true).Render(r.Method)
			p := styleDim.Render(r.Path)
			fmt.Fprintf(c.Writer, "  [%s]  %s %s\n", tag, m, p)
		} else {
			fmt.Fprintf(c.Writer, "%s %s\n", r.Method, r.Path)
		}
		if bodies[i] != nil {
			indented := "  " + strings.ReplaceAll(string(bodies[i]), "\n", "\n  ")
			fmt.Fprintln(c.Writer, indented)
		}
	}
	return nil
}

// Count prints the footer under a list — "3 domains" — with any notes after
// it on the same line: "25 transfers · first page — pass --all for the rest".
//
// A paginated list used to print two footers, "(250 domains)" and then
// "Showing 1–250 of 6522 — …" as a hint, saying the count twice and wrapping
// at 80 columns (#233). A list that has more to say passes it as a note.
func (c *Config) Count(n int, noun string, notes ...string) {
	c.Footer(append([]string{Plural(n, noun)}, notes...)...)
}

// Footer prints parts as one dim line on stderr, joined with " · ". Only in
// table mode, and not in quiet mode. Count is the usual caller; a list whose
// count reads differently ("Showing 1–250 of 6,522 domains") calls it
// directly. It goes to stderr for the reason Hint does.
func (c *Config) Footer(parts ...string) {
	if c.Format != FormatTable || c.QuietMode {
		return
	}
	var kept []string
	for _, p := range parts {
		if p != "" {
			kept = append(kept, p)
		}
	}
	if len(kept) > 0 {
		fmt.Fprintln(c.EWriter, c.Dim(strings.Join(kept, " · ")))
	}
}

// Dim returns text rendered in a muted/gray style.
func (c *Config) Dim(s string) string {
	if !c.ColorEnabled() {
		return s
	}
	return styleDim.Render(s)
}

// ParseFormat validates and returns the Format value from a flag string.
func ParseFormat(s string) (Format, error) {
	switch Format(strings.ToLower(s)) {
	case FormatTable:
		return FormatTable, nil
	case FormatJSON:
		return FormatJSON, nil
	case FormatYAML:
		return FormatYAML, nil
	}
	return "", fmt.Errorf("unknown output format %q; choose table, json, or yaml", s)
}

// ParseColorMode validates the --color flag value.
func ParseColorMode(s string) (ColorMode, error) {
	switch ColorMode(strings.ToLower(s)) {
	case ColorAuto:
		return ColorAuto, nil
	case ColorAlways:
		return ColorAlways, nil
	case ColorNever:
		return ColorNever, nil
	}
	return "", fmt.Errorf("unknown color mode %q; choose auto, always, or never", s)
}

// Title prints a bold resource-name header above a detail view. Only emitted
// in table/interactive mode.
func (c *Config) Title(name string) {
	if c.Format != FormatTable || c.QuietMode {
		return
	}
	if c.ColorEnabled() {
		fmt.Fprintln(c.Writer, c.SandboxTag()+styleTitle.Render(name))
	} else {
		fmt.Fprintln(c.Writer, c.SandboxTag()+name)
	}
}

// Empty prints an empty-state message to stderr when a list returns zero
// results, so an empty list leaves stdout empty. noun should be singular
// ("domain", "record"). hint is shown as a dim hint.
func (c *Config) Empty(noun, hint string) {
	if c.Format != FormatTable || c.QuietMode {
		return
	}
	fmt.Fprintln(c.EWriter, c.Dim("No "+PluralNoun(2, noun)+" found."))
	if hint != "" {
		c.Hint(hint)
	}
}

// WarnBox prints a bordered warning box to stderr. Use for important notices
// that warrant more visual weight than a single Warn line.
func (c *Config) WarnBox(lines ...string) {
	if c.Format != FormatTable {
		for _, l := range lines {
			fmt.Fprintln(c.EWriter, "WARNING: "+l)
		}
		return
	}
	body := strings.Join(lines, "\n")
	if c.ColorEnabled() {
		box := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(acAmber).
			Foreground(acAmber).
			Padding(0, 1).
			Render(body)
		fmt.Fprintln(c.EWriter, box)
	} else {
		fmt.Fprintln(c.EWriter, "WARNING: "+body)
	}
}

// Red returns text styled in the error/red adaptive color.
func (c *Config) Red(s string) string {
	if !c.ColorEnabled() {
		return s
	}
	return lipgloss.NewStyle().Foreground(acRed).Render(s)
}

// Amber returns text styled in the warning/amber adaptive color.
func (c *Config) Amber(s string) string {
	if !c.ColorEnabled() {
		return s
	}
	return lipgloss.NewStyle().Foreground(acAmber).Render(s)
}

// IsStderrTTY reports whether stderr is a terminal.
func IsStderrTTY() bool { return isStderrTTY() }
func isStdoutTTY() bool { return term.IsTerminal(int(os.Stdout.Fd())) }
func isStdinTTY() bool  { return term.IsTerminal(int(os.Stdin.Fd())) }
func isStderrTTY() bool { return term.IsTerminal(int(os.Stderr.Fd())) }
