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
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
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
	Format    Format
	QuietMode bool      // -q: print IDs/names only, one per line
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
}

// DefaultConfig returns an output config with defaults resolved from the
// current environment (TTY detection, NO_COLOR, CLICOLOR_FORCE).
func DefaultConfig() *Config {
	f := FormatJSON
	if isStdoutTTY() {
		f = FormatTable
	}
	c := &Config{
		Format:  f,
		Color:   ColorAuto,
		Writer:  os.Stdout,
		EWriter: os.Stderr,
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
	// ColorAuto: respect NO_COLOR (presence-based) and CLICOLOR_FORCE.
	if _, set := os.LookupEnv("NO_COLOR"); set {
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
// the 256-color palette entries we use for badges and status dots.
var (
	acGreen  = lipgloss.AdaptiveColor{Dark: "82", Light: "28"}
	acRed    = lipgloss.AdaptiveColor{Dark: "196", Light: "160"}
	acAmber  = lipgloss.AdaptiveColor{Dark: "220", Light: "136"}
	acGray   = lipgloss.AdaptiveColor{Dark: "8", Light: "244"}
	acBlue   = lipgloss.AdaptiveColor{Dark: "75", Light: "26"}
	acPurple = lipgloss.AdaptiveColor{Dark: "141", Light: "90"}
	acPink   = lipgloss.AdaptiveColor{Dark: "213", Light: "125"}
	acOrange = lipgloss.AdaptiveColor{Dark: "208", Light: "166"}
	acSky    = lipgloss.AdaptiveColor{Dark: "39", Light: "25"}
	acSpring = lipgloss.AdaptiveColor{Dark: "48", Light: "29"}
	acSalmon = lipgloss.AdaptiveColor{Dark: "203", Light: "160"}
	acCyan   = lipgloss.AdaptiveColor{Dark: "6", Light: "6"} // ANSI — terminal-theme safe

	// Lip Gloss styles. Initialized once; use .Render() (not .String()) so
	// color is applied lazily and tests can disable it via NO_COLOR.
	styleSuccess = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("2"))
	styleError   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("1"))
	styleWarning = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("3"))
	styleDim     = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	styleBorder  = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	styleStep    = lipgloss.NewStyle().Bold(true).Foreground(acCyan)
	styleTitle   = lipgloss.NewStyle().Bold(true)

	// DNS record type badge colors — background with black foreground.
	dnsTypeColors = map[string]lipgloss.AdaptiveColor{
		"A":     acGreen,
		"AAAA":  acSky,
		"CNAME": acPink,
		"MX":    acAmber,
		"TXT":   acPurple,
		"NS":    acBlue,
		"SRV":   acOrange,
		"ANAME": acSpring,
		"CAA":   acSalmon,
	}

	// Domain / transfer status dot colors — foreground only.
	statusColors = map[string]lipgloss.AdaptiveColor{
		"active":    acGreen,
		"locked":    acSalmon,
		"expired":   acRed,
		"suspended": acRed,
		"pending":   acAmber,
		"completed": acGreen,
		"failed":    acRed,
		"canceled":  acGray,
		"rejected":  acRed,
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

// Table renders rows as a styled table. headers is the column header row.
// Table renders headers and rows as a bordered table, dropping trailing
// columns that do not fit the terminal.
//
// Tables were rendered at their natural width with no regard for the terminal:
// `domain list` came to 113 columns, `order list` 99, `dns list` 87. In an
// 80-column terminal — an SSH session, a split pane — every one of them wrapped
// and the rounded borders came apart into unreadable fragments. Columns are
// ordered most- to least-important by their callers, so the ones that go are
// the ones from the right, and a footer names them rather than letting them
// vanish. --wide opts out; so does any non-terminal writer, since a pipe has no
// width to fit and its consumer wants the whole row.
func (c *Config) Table(headers []string, rows [][]string) {
	color := c.ColorEnabled()
	headers, rows, dropped := c.fitColumns(headers, rows)

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

	if len(dropped) > 0 {
		fmt.Fprintln(c.Writer, c.Dim(fmt.Sprintf(
			"%d column%s hidden (%s) — widen the terminal, pass --wide, or use -o json",
			len(dropped), plural("", len(dropped)), strings.Join(dropped, ", "))))
	}
}

// fitColumns drops trailing columns until the rendered table fits MaxWidth,
// returning the surviving headers and rows plus the names of what went.
//
// The first column is never dropped: a table of nothing but a row count is
// worse than one that overflows, and callers put the identifying column first.
func (c *Config) fitColumns(headers []string, rows [][]string) ([]string, [][]string, []string) {
	if c.Wide || c.MaxWidth <= 0 || len(headers) == 0 {
		return headers, rows, nil
	}

	keep := len(headers)
	for keep > 1 && tableWidth(colWidths(headers, rows, keep)) > c.MaxWidth {
		keep--
	}
	if keep == len(headers) {
		return headers, rows, nil
	}

	dropped := make([]string, 0, len(headers)-keep)
	for _, h := range headers[keep:] {
		dropped = append(dropped, strings.ToLower(h))
	}
	trimmed := make([][]string, 0, len(rows))
	for _, r := range rows {
		if len(r) > keep {
			r = r[:keep]
		}
		trimmed = append(trimmed, r)
	}
	return headers[:keep], trimmed, dropped
}

// colWidths measures the first n columns at their natural (widest-cell) width.
// lipgloss.Width is used rather than len because cells arrive pre-styled —
// ExpiryDate returns ANSI escapes — and because a domain may hold wide runes.
func colWidths(headers []string, rows [][]string, n int) []int {
	w := make([]int, n)
	for i := 0; i < n && i < len(headers); i++ {
		w[i] = lipgloss.Width(headers[i])
	}
	for _, r := range rows {
		for i := 0; i < n && i < len(r); i++ {
			if cw := lipgloss.Width(r[i]); cw > w[i] {
				w[i] = cw
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
	color := c.ColorEnabled()

	valueStyle := lipgloss.NewStyle().Padding(0, 1)
	fieldStyle := lipgloss.NewStyle().Padding(0, 1)
	if color {
		fieldStyle = fieldStyle.Foreground(lipgloss.Color("111")).Bold(true)
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

	fmt.Fprintln(c.Writer, t.Render())
}

// PrintQuiet prints each value on its own line — used for -q/--quiet mode so
// scripts can pipe IDs: `namecom dns list d.com -q | xargs ...`
func (c *Config) PrintQuiet(vals []string) {
	for _, v := range vals {
		fmt.Fprintln(c.Writer, v)
	}
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

// Hint prints a dimmed suggestion line to stdout — shown only in table mode.
func (c *Config) Hint(msg string) {
	if c.Format != FormatTable {
		return
	}
	if c.ColorEnabled() {
		arrow := styleDim.Render("→")
		fmt.Fprintln(c.Writer, arrow+" "+styleDim.Render(msg))
	} else {
		fmt.Fprintln(c.Writer, "→ "+msg)
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

func errorHint(err error) string {
	if h, ok := err.(hintable); ok {
		if hint := h.UserHint(); hint != "" {
			return hint
		}
	}
	// Unwrap to find a hintable cause (e.g. fmt.Errorf("fetching: %w", apiErr)).
	// The loop stops at a nil cause: *net.DNSError and *json.UnmarshalTypeError
	// both have an Unwrap that returns nil, and assigning that to err crashed
	// the err.Error() below on every DNS failure and undecodable response.
	for cause := errors.Unwrap(err); cause != nil; cause = errors.Unwrap(cause) {
		if h, ok := cause.(hintable); ok {
			if hint := h.UserHint(); hint != "" {
				return hint
			}
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

// StatusBadge returns a styled label for domain/transfer status values.
func (c *Config) StatusBadge(status string) string {
	if !c.ColorEnabled() {
		return status
	}
	key := strings.ToLower(status)
	// Match prefix for compound statuses like "pending_transfer".
	color := lipgloss.AdaptiveColor{Dark: "7", Light: "245"} // default: gray
	for k, v := range statusColors {
		if key == k || strings.HasPrefix(key, k) {
			color = v
			break
		}
	}
	dot := lipgloss.NewStyle().Foreground(color).Render("●")
	text := lipgloss.NewStyle().Bold(true).Foreground(color).Render(status)
	return dot + " " + text
}

// TypeBadge returns a styled, colored label for DNS record types and similar
// short categorical values. Falls back to StatusBadge color logic if the type
// isn't in the DNS palette.
func (c *Config) TypeBadge(typ string) string {
	if !c.ColorEnabled() {
		return typ
	}
	upper := strings.ToUpper(typ)
	color, ok := dnsTypeColors[upper]
	if !ok {
		return c.StatusBadge(typ)
	}
	return lipgloss.NewStyle().
		Background(color).
		Foreground(lipgloss.Color("0")).
		Padding(0, 1).
		Bold(true).
		Render(upper)
}

// AvailabilityBadge returns a visually distinct ✓ available / ✗ taken badge.
func (c *Config) AvailabilityBadge(purchasable bool) string {
	if !c.ColorEnabled() {
		if purchasable {
			return "✓ available"
		}
		return "✗ taken"
	}
	if purchasable {
		return styleSuccess.Render("✓") + " " + lipgloss.NewStyle().Foreground(acGreen).Render("available")
	}
	return styleError.Render("✗") + " " + lipgloss.NewStyle().Foreground(acRed).Render("taken")
}

// BoolBadge returns a styled ✓ yes (true) or ✗ no (false).
func (c *Config) BoolBadge(b bool) string {
	if !c.ColorEnabled() {
		if b {
			return "yes"
		}
		return "no"
	}
	if b {
		return styleSuccess.Render("✓ yes")
	}
	return styleError.Render("✗ no")
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

// spinFrames are the animation frames for the spinner.
var spinFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// spinnerTTY is the terminal check Spin and StartSpinner make. It is a
// variable, like IsInteractive, because under go test stderr is never a
// terminal: every spinner was a no-op there, so nothing could show that a
// command stops the spinner it starts. See RecordSpinners.
var spinnerTTY = isStderrTTY

// spinRecorder, when set by RecordSpinners, stands in for the animation.
var spinRecorder *SpinRecorder

// SpinRecorder records the spinners Spin and StartSpinner start and stop, so a
// test can assert that a command leaves none running. Obtain one from
// RecordSpinners.
type SpinRecorder struct {
	mu      sync.Mutex
	started []string
	running map[int]string
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

// start records a spinner and returns the function that records its stop.
func (r *SpinRecorder) start(msg string) func() {
	r.mu.Lock()
	id := len(r.started)
	r.started = append(r.started, msg)
	r.running[id] = msg
	r.mu.Unlock()
	return func() {
		r.mu.Lock()
		delete(r.running, id)
		r.mu.Unlock()
	}
}

// Spin starts a spinner on stderr with the given message and returns a stop
// function. Call the returned function when the operation completes.
// In non-TTY or non-table mode it's a no-op (no spinner, no output).
func (c *Config) Spin(msg string) func() {
	if !spinnerTTY() || c.Format != FormatTable || c.QuietMode {
		return func() {}
	}
	if spinRecorder != nil {
		var once sync.Once
		stop := spinRecorder.start(msg)
		return func() { once.Do(stop) }
	}

	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)

	go func() {
		defer wg.Done()
		ticker := time.NewTicker(80 * time.Millisecond)
		defer ticker.Stop()
		i := 0
		for {
			select {
			case <-done:
				fmt.Fprintf(c.EWriter, "\r\033[K") // clear line
				return
			case <-ticker.C:
				frame := spinFrames[i%len(spinFrames)]
				if c.ColorEnabled() {
					frame = styleDim.Render(frame)
				}
				fmt.Fprintf(c.EWriter, "\r%s %s", frame, msg)
				i++
			}
		}
	}()

	var once sync.Once
	return func() {
		once.Do(func() {
			close(done)
			wg.Wait()
		})
	}
}

// Spinner is a running spinner whose message can be updated mid-operation.
// Obtain one via StartSpinner; call Stop when the operation completes.
type Spinner struct {
	update chan string
	stop   chan struct{}
	wg     sync.WaitGroup
	once   sync.Once
	onStop func() // set when a SpinRecorder is recording this spinner
}

// Stop halts the spinner and clears the line.
func (s *Spinner) Stop() {
	s.once.Do(func() {
		close(s.stop)
		s.wg.Wait()
		if s.onStop != nil {
			s.onStop()
		}
	})
}

// Update changes the message shown next to the spinner frame.
func (s *Spinner) Update(msg string) {
	select {
	case s.update <- msg:
	default:
	}
}

// StartSpinner starts a spinner that can be updated via Update while running.
// In non-TTY or non-table mode it is a no-op (Stop/Update are still safe to call).
func (c *Config) StartSpinner(msg string) *Spinner {
	s := &Spinner{
		update: make(chan string, 1),
		stop:   make(chan struct{}),
	}
	if !spinnerTTY() || c.Format != FormatTable || c.QuietMode {
		return s
	}
	if spinRecorder != nil {
		s.onStop = spinRecorder.start(msg)
		return s
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		ticker := time.NewTicker(80 * time.Millisecond)
		defer ticker.Stop()
		current := msg
		i := 0
		for {
			select {
			case <-s.stop:
				fmt.Fprintf(c.EWriter, "\r\033[K")
				return
			case m := <-s.update:
				current = m
			case <-ticker.C:
				frame := spinFrames[i%len(spinFrames)]
				if c.ColorEnabled() {
					frame = styleDim.Render(frame)
				}
				fmt.Fprintf(c.EWriter, "\r%s %s", frame, current)
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

// Count prints a dim result count footer — only in table mode, skipped in quiet mode.
func (c *Config) Count(n int, noun string) {
	if c.Format != FormatTable || c.QuietMode {
		return
	}
	label := noun + "s"
	if n == 1 {
		label = noun
	}
	fmt.Fprintln(c.Writer, c.Dim(fmt.Sprintf("(%d %s)", n, label)))
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

// Step prints a flyctl-style phase header ("==> Checking availability…") to
// stdout. Only emitted in table/interactive mode, skipped when quiet or piped.
func (c *Config) Step(msg string) {
	if c.Format != FormatTable || c.QuietMode {
		return
	}
	if c.ColorEnabled() {
		fmt.Fprintln(c.Writer, styleStep.Render("==>")+" "+msg)
	} else {
		fmt.Fprintln(c.Writer, "==> "+msg)
	}
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

// Empty prints an empty-state message when a list returns zero results.
// noun should be singular ("domain", "record"). hint is shown as a dim hint.
func (c *Config) Empty(noun, hint string) {
	if c.Format != FormatTable || c.QuietMode {
		return
	}
	fmt.Fprintln(c.Writer, c.Dim("No "+noun+"s found."))
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
