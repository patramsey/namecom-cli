package output

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// evil is what a hostile TXT answer or error message carries: an OSC that
// sets the window title, a screen clear, colour, a C1 CSI, DEL, a line
// separator and a carriage return that would overwrite the line.
const evil = "a\x1b]0;PWNED\a\x1b[2J\x1b[31mred\x1b[0m\u009b2J\x7f\xe2\x80\xa8\rb"

const evilEscaped = `a\x1b]0;PWNED\x07\x1b[2J\x1b[31mred\x1b[0m\u009b2J\x7f` + "\\u2028" + `\rb`

func TestSanitize(t *testing.T) {
	for in, want := range map[string]string{
		"plain":             "plain",
		"tab\tand\nnewline": "tab\tand\nnewline",
		"wide 名前 👍🏽":        "wide 名前 👍🏽",
		evil:                evilEscaped,
		"bad\xffbyte":       `bad\xffbyte`,
		"nul\x00":           `nul\x00`,
		// A right-to-left override and a zero-width space.
		"evil\xe2\x80\xaetxt.exe\xe2\x80\x8b": "evil\\u202etxt.exe\\u200b",
	} {
		if got := Sanitize(in); got != want {
			t.Errorf("Sanitize(%q) = %q, want %q", in, got, want)
		}
		if got := Sanitize(Sanitize(in)); got != want {
			t.Errorf("Sanitize is not idempotent on %q: %q", in, got)
		}
	}
}

func TestTSVEscape(t *testing.T) {
	in := "a\\b\tc\nd\re\x1b[2Jf\u0085"
	want := `a\\b\tc\nd\re\x1b[2Jf\u0085`
	if got := tsvEscape(in); got != want {
		t.Errorf("tsvEscape(%q) = %q, want %q", in, got, want)
	}
	// Bidi and zero-width characters are text to a program reading TSV.
	if in := "x\xe2\x80\xaey\xe2\x80\x8b"; tsvEscape(in) != in {
		t.Errorf("tsvEscape(%q) = %q, want it unchanged", in, tsvEscape(in))
	}
}

// TestHumanOutput_EscapesControlCharacters pins #337: every path that prints
// for people — table cells, detail views, success, warning, hint and error
// lines, titles and footers — shows a control character as an escape rather
// than sending it to the terminal.
func TestHumanOutput_EscapesControlCharacters(t *testing.T) {
	for _, plain := range []bool{false, true} {
		var stdout, stderr bytes.Buffer
		c := &Config{Format: FormatTable, Color: ColorNever, Writer: &stdout, EWriter: &stderr, Plain: plain}
		c.Table([]string{"ID", "ANSWER"}, [][]string{{"1", evil}})
		c.KVTable([][]string{{"Answer", evil}})
		c.Title(evil)
		c.Success(evil)
		c.Warn(evil)
		c.Hint(evil)
		c.Note(evil)
		c.Footer(evil)
		c.WarnBox(evil)
		c.Error(errors.New(evil))
		c.QuietMode = true
		c.Quiet(evil)
		for name, got := range map[string]string{"stdout": stdout.String(), "stderr": stderr.String()} {
			if strings.ContainsAny(got, "\x1b\a\x7f\r\u009b\u2028") {
				t.Errorf("plain=%v: %s holds a raw control character:\n%q", plain, name, got)
			}
			if !strings.Contains(got, `\x1b]0;PWNED`) {
				t.Errorf("plain=%v: %s does not show the escape:\n%q", plain, name, got)
			}
		}
	}
}

// TestHumanOutput_KeepsOwnStyling: the CLI's own colours are escape sequences
// too, made before the cell or message reaches the sink that sanitizes it.
// They must still render, while the data's sequences beside them do not.
func TestHumanOutput_KeepsOwnStyling(t *testing.T) {
	prev := lipgloss.ColorProfile()
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })
	lipgloss.SetColorProfile(termenv.ANSI256)

	var stdout, stderr bytes.Buffer
	c := &Config{Format: FormatTable, Color: ColorAlways, Writer: &stdout, EWriter: &stderr}
	badge := c.StatusBadge("failed")
	dim := c.Dim("note " + evil)
	if !strings.Contains(badge, "\x1b[") {
		t.Fatalf("StatusBadge(failed) = %q, want it styled", badge)
	}
	c.Table([]string{"STATUS", "ANSWER"}, [][]string{{badge + " " + evil, dim}})
	c.Success("done " + c.Amber("careful") + " " + evil)

	for name, got := range map[string]string{"stdout": stdout.String(), "stderr": stderr.String()} {
		if strings.Contains(got, "\x1b[2J") || strings.Contains(got, "\x1b]0;") {
			t.Errorf("%s passes the data's escape sequences:\n%q", name, got)
		}
	}
	out := stdout.String()
	for _, styled := range []string{badge, dim, c.Amber("careful")} {
		if !strings.Contains(out, styled) {
			t.Errorf("styled text %q did not render intact:\n%q", styled, out)
		}
	}
	if !strings.Contains(out, `\x1b]0;PWNED`) {
		t.Errorf("data's escape not shown:\n%q", out)
	}
}

// TestTSV_EscapesControlCharacters: TSV escapes control characters beyond the
// tab, line break and backslash it always escaped, in every TSV writer.
func TestTSV_EscapesControlCharacters(t *testing.T) {
	var stdout bytes.Buffer
	c := &Config{Format: FormatTSV, Writer: &stdout, EWriter: &bytes.Buffer{}}
	c.Table([]string{"ID", "ANSWER"}, [][]string{{"1", c.Dim(evil)}})
	c.KVTable([][]string{{"answer", evil}})
	c.Success(evil)
	got := stdout.String()
	if strings.ContainsAny(got, "\x1b\a\x7f\r\u009b\u2028") {
		t.Errorf("TSV holds a raw control character:\n%q", got)
	}
	if n := strings.Count(got, `\x1b]0;PWNED`); n != 3 {
		t.Errorf("TSV shows the escape %d times, want 3:\n%q", n, got)
	}
	if strings.Contains(got, `\\x1b`) {
		t.Errorf("TSV escaped a value twice:\n%q", got)
	}
}

func TestSanitizeLine(t *testing.T) {
	for in, want := range map[string]string{
		"plain":               "plain",
		"tab\tand\nnewline":   `tab\tand\nnewline`,
		evil:                  evilEscaped,
		"evil\xe2\x80\xaetxt": "evil\\u202etxt",
	} {
		if got := SanitizeLine(in); got != want {
			t.Errorf("SanitizeLine(%q) = %q, want %q", in, got, want)
		}
		if got := SanitizeLine(SanitizeLine(in)); got != want {
			t.Errorf("SanitizeLine is not idempotent on %q: %q", in, got)
		}
	}
}

func TestUnprintable(t *testing.T) {
	for in, want := range map[string]bool{
		"example.com":         false,
		"名前.com":              false,
		"a\tb":                true,
		"a\nb":                true,
		"h1\x1b[2Jx.com":      true,
		"x\u202e.com":         true,
		"zero\u200bwidth.com": true,
		"bad\xff.com":         true,
		"del\x7f.com":         true,
	} {
		if got := Unprintable(in); got != want {
			t.Errorf("Unprintable(%q) = %v, want %v", in, got, want)
		}
	}
}

// TestTable_OneLineCells pins #359: a line feed in a cell printed a line of
// its own, which read as another row, and a tab hid the text after it, since
// the width code counted it as nothing. Both now show as escapes, measured
// as they print, in the bordered and plain tables and the detail view.
func TestTable_OneLineCells(t *testing.T) {
	for _, plain := range []bool{false, true} {
		var stdout bytes.Buffer
		c := &Config{Format: FormatTable, Color: ColorNever, Writer: &stdout, EWriter: &bytes.Buffer{}, Plain: plain}
		c.Table([]string{"ID", "ANSWER"}, [][]string{{"1", "a\tb\tc"}, {"2", "x\nFAKE"}})
		c.KVTable([][]string{{"Answer", "y\nFAKE\tz"}})
		got := stdout.String()
		if strings.Contains(got, "\t") || strings.Contains(got, "\nFAKE") {
			t.Errorf("plain=%v: a tab or line feed reached the table:\n%s", plain, got)
		}
		for _, want := range []string{`a\tb\tc`, `x\nFAKE`, `y\nFAKE\tz`} {
			if !strings.Contains(got, want) {
				t.Errorf("plain=%v: table does not show %q:\n%s", plain, want, got)
			}
		}
	}

	// Measured as printed: at a width the escaped cell just fits, nothing
	// is hidden or cut short.
	var stdout, stderr bytes.Buffer
	c := &Config{Format: FormatTable, Color: ColorNever, Writer: &stdout, EWriter: &stderr, MaxWidth: 40}
	c.Table([]string{"ID", "ANSWER"}, [][]string{{"1", "a\tb\tc"}})
	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	for _, l := range lines {
		if lipgloss.Width(l) != lipgloss.Width(lines[0]) {
			t.Errorf("line %q is not as wide as the border %q", l, lines[0])
		}
	}
	if stderr.Len() > 0 {
		t.Errorf("table fits but printed a footer: %q", stderr.String())
	}
}

// TestSanitizeLine_StyledChunk: a cell styled by the CLI (out.Dim(answer))
// carries the data's line feed inside the chunk render remembered, which
// Sanitize passes through. SanitizeLine escapes it there too.
func TestSanitizeLine_StyledChunk(t *testing.T) {
	prev := lipgloss.ColorProfile()
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })
	lipgloss.SetColorProfile(termenv.ANSI256)
	c := &Config{Format: FormatTable, Color: ColorAlways}
	if got := SanitizeLine(c.Dim("x\nFAKE")); strings.Contains(got, "\n") {
		t.Errorf("SanitizeLine(Dim) = %q, holds a line feed", got)
	}
}
