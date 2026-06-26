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
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
	"golang.org/x/term"
	"gopkg.in/yaml.v3"
)

// Format is the output format selected by --output / -o.
type Format string

const (
	FormatTable Format = "table"
	FormatJSON  Format = "json"
	FormatYAML  Format = "yaml"
)

// ColorMode maps to --color flag values.
type ColorMode string

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
}

// DefaultConfig returns an output config with defaults resolved from the
// current environment (TTY detection, NO_COLOR, CLICOLOR_FORCE).
func DefaultConfig() *Config {
	f := FormatJSON
	if isStdoutTTY() {
		f = FormatTable
	}
	return &Config{
		Format:  f,
		Color:   ColorAuto,
		Writer:  os.Stdout,
		EWriter: os.Stderr,
	}
}

// IsInteractive reports whether stdin is a TTY — i.e., a human is present.
// Command code uses this to decide whether to show prompts.
func IsInteractive() bool {
	return isStdinTTY()
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
func (c *Config) YAML(v any) error {
	return yaml.NewEncoder(c.Writer).Encode(v)
}

// listEnvelope wraps paginated list results with metadata for agent consumers.
// nextPage and total are omitted when zero/nil.
type listEnvelope struct {
	Data     any    `json:"data" yaml:"data"`
	NextPage *int32 `json:"nextPage,omitempty" yaml:"nextPage,omitempty"`
	Total    int32  `json:"total,omitempty" yaml:"total,omitempty"`
}

// JSONList encodes data as a pagination envelope: {"data":[…],"nextPage":N,"total":N}.
// nextPage is omitted when nil or zero; total is omitted when zero.
func (c *Config) JSONList(data any, nextPage *int32, total int32) error {
	env := listEnvelope{Data: data, Total: total}
	if nextPage != nil && *nextPage != 0 {
		env.NextPage = nextPage
	}
	return c.JSON(env)
}

// YAMLList encodes data as a pagination envelope in YAML.
func (c *Config) YAMLList(data any, nextPage *int32, total int32) error {
	env := listEnvelope{Data: data, Total: total}
	if nextPage != nil && *nextPage != 0 {
		env.NextPage = nextPage
	}
	return c.YAML(env)
}

// Table renders rows as a styled table. headers is the column header row.
func (c *Config) Table(headers []string, rows [][]string) {
	color := c.ColorEnabled()

	cell := lipgloss.NewStyle().Padding(0, 1)
	header := cell.Bold(color)
	if color {
		header = header.Foreground(lipgloss.Color("15")) // bright white
	}

	styleFunc := func(row, col int) lipgloss.Style {
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
}

// KVTable renders a headerless two-column key-value table with styled field names.
func (c *Config) KVTable(rows [][]string) {
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

// Success prints a ✓ success message to stdout.
func (c *Config) Success(msg string) {
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

// Error prints a user-facing error to stderr. In JSON output mode the error is
// emitted as a structured envelope so agents can parse failures.
func (c *Config) Error(err error) {
	hint := errorHint(err)

	if c.Format == FormatJSON {
		env := map[string]any{"error": map[string]string{"message": err.Error()}}
		if hint != "" {
			env["hint"] = hint
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
	type unwrapper interface{ Unwrap() error }
	for u, ok := err.(unwrapper); ok; u, ok = err.(unwrapper) {
		err = u.Unwrap()
		if h, ok2 := err.(hintable); ok2 {
			if hint := h.UserHint(); hint != "" {
				return hint
			}
		}
	}
	// Network-level failures.
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
	switch {
	case days < 0:
		return lipgloss.NewStyle().Bold(true).Foreground(acRed).Render(label)
	case days < 7:
		return lipgloss.NewStyle().Bold(true).Foreground(acRed).Render(label)
	case days < 30:
		return lipgloss.NewStyle().Bold(true).Foreground(acAmber).Render(label)
	default:
		return styleDim.Render(label)
	}
}

// relativeTime converts a floating-point day count into a human-readable string.
func relativeTime(days float64) string {
	abs := days
	if abs < 0 {
		abs = -abs
	}
	switch {
	case days < 0 && abs < 1:
		return "expired today"
	case days < 0:
		n := int(abs + 0.5)
		if n == 1 {
			return "1 day ago"
		}
		return fmt.Sprintf("%d days ago", n)
	case days < 1:
		return "today"
	default:
		n := int(days + 0.5)
		if n == 1 {
			return "in 1 day"
		}
		return fmt.Sprintf("in %d days", n)
	}
}

// spinFrames are the animation frames for the spinner.
var spinFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// Spin starts a spinner on stderr with the given message and returns a stop
// function. Call the returned function when the operation completes.
// In non-TTY or non-table mode it's a no-op (no spinner, no output).
func (c *Config) Spin(msg string) func() {
	if !isStderrTTY() || c.Format != FormatTable || c.QuietMode {
		return func() {}
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
}

// Stop halts the spinner and clears the line.
func (s *Spinner) Stop() {
	s.once.Do(func() {
		close(s.stop)
		s.wg.Wait()
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
	if !isStderrTTY() || c.Format != FormatTable || c.QuietMode {
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

// DryRun prints a styled mock-request line for --dry-run mode.
// Pass a struct or map as body to pretty-print it as indented JSON; pass nil for no body.
func (c *Config) DryRun(method, path string, body any) {
	if c.ColorEnabled() {
		tag := lipgloss.NewStyle().Foreground(lipgloss.Color("220")).Bold(true).Render("dry-run")
		m := lipgloss.NewStyle().Foreground(lipgloss.Color("111")).Bold(true).Render(method)
		p := styleDim.Render(path)
		fmt.Fprintf(c.Writer, "  [%s]  %s %s\n", tag, m, p)
	} else {
		fmt.Fprintf(c.Writer, "%s %s\n", method, path)
	}
	if body != nil {
		b, _ := json.MarshalIndent(body, "", "  ")
		indented := "  " + strings.ReplaceAll(string(b), "\n", "\n  ")
		fmt.Fprintln(c.Writer, indented)
	}
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
