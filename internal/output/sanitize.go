package output

import (
	"fmt"
	"io"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// Values the API returns — a TXT answer, a forwarding target, an error
// message — were written to the terminal as they came, so an escape sequence
// in one was obeyed: it could set the window title, clear the screen, rewrite
// the lines above (a confirmation's summary among them), or, in terminals
// that honour OSC 52, write the clipboard (#337). Anyone who can write to the
// account, or a --base-url endpoint, chooses those values.
//
// So every string this package prints for people goes through Sanitize, which
// replaces each control character with a visible escape: C0 other than tab
// and line feed, DEL, C1, the line and paragraph separators, and any byte
// that is not UTF-8. ESC prints as \x1b. It also escapes the bidi and
// zero-width characters that make text read as something else (escapeHuman).
// JSON and YAML escape control characters themselves and are left alone; TSV
// escapes them as well as its own tab, line break and backslash (tsvEscape);
// --jq prints a string raw, as jq -r does.
//
// The CLI's own styling is escape sequences too, and it reaches the sinks
// inside the same strings as the data: a cell is often out.Dim(x) or a badge.
// It survives because it is made by render, which sanitizes the text before
// styling it and remembers the styled result as the CLI's own. Sanitize
// passes a remembered sequence through and escapes every other one. Data can
// at most repeat, exactly, styled text the CLI itself printed.

// trusted holds the styled chunks render produced: each run from an ESC to
// the SGR reset that ends it.
var trusted sync.Map // string → struct{}

// sgrReset ends every sequence lipgloss renders.
const sgrReset = "\x1b[0m"

// render is st.Render(s) for an s that may hold data. Every Render in this
// package goes through it: a styled string that did not would have its
// escapes shown as text wherever it is sanitized.
func render(st lipgloss.Style, s string) string {
	out := st.Render(Sanitize(s))
	for rest := out; ; {
		i := strings.IndexByte(rest, 0x1b)
		if i < 0 {
			break
		}
		j := strings.Index(rest[i:], sgrReset)
		if j < 0 {
			break
		}
		end := i + j + len(sgrReset)
		trusted.Store(rest[i:end], struct{}{})
		rest = rest[end:]
	}
	return out
}

// trustedLen returns the length of the styled chunk render made that s
// begins with, or 0 when s does not begin with one.
func trustedLen(s string) int {
	j := strings.Index(s, sgrReset)
	if j < 0 {
		return 0
	}
	n := j + len(sgrReset)
	if _, ok := trusted.Load(s[:n]); ok {
		return n
	}
	return 0
}

// Sanitize returns s for printing to a terminal: every control character and
// every bidi or zero-width character replaced by a visible escape (\x1b,
// \x7f, \u009b; a carriage return as \r), except tab and line feed, and
// except the CLI's own styling. See the comment at the top of this file. Use
// it for any value from the API that reaches the terminal other than through
// this package.
func Sanitize(s string) string {
	return clean(s, escapeHuman, func(span string) string { return span })
}

// SanitizeWriter returns a writer that sanitizes what is written to it, for
// a command that prints API values with fmt.Fprintf rather than through
// Config. Each Write is sanitized on its own, so styling must arrive whole in
// one Write, as one fmt.Fprintf call's output does.
func SanitizeWriter(w io.Writer) io.Writer { return sanitizeWriter{w} }

type sanitizeWriter struct{ w io.Writer }

func (s sanitizeWriter) Write(p []byte) (int, error) {
	if _, err := io.WriteString(s.w, Sanitize(string(p))); err != nil {
		return 0, err
	}
	return len(p), nil
}

// tsvEscape escapes a TSV cell: tab, line feed, carriage return and
// backslash as \t, \n, \r and \\ — the "linear TSV" rules mlr and
// PostgreSQL's text COPY read — so every row is one line and every tab
// separates two cells, and every other control character as Sanitize does.
// Styling is removed, not escaped: TSV has none of its own, and a remembered
// sequence in a cell is a value the CLI styled.
func tsvEscape(s string) string {
	return clean(s, func(r rune) string {
		switch r {
		case '\\':
			return `\\`
		case '\t':
			return `\t`
		case '\n':
			return `\n`
		}
		return escapeControl(r)
	}, func(span string) string { return tsvEscape(ansi.Strip(span)) })
}

// escapeControl returns the visible escape for r if it is a control
// character — C0 other than tab and line feed, DEL, C1, or the line and
// paragraph separators U+2028 and U+2029 — or "" to print r itself.
func escapeControl(r rune) string {
	switch {
	case r == '\t' || r == '\n':
		return ""
	case r == '\r':
		return `\r`
	case r < 0x20 || r == 0x7f:
		return fmt.Sprintf(`\x%02x`, r)
	case (r >= 0x80 && r <= 0x9f) || r == 0x2028 || r == 0x2029:
		return fmt.Sprintf(`\u%04x`, r)
	}
	return ""
}

// escapeHuman is escapeControl plus the invisible characters that make text
// read as something it is not: the bidirectional marks, embeddings,
// overrides and isolates (U+200E–U+200F, U+202A–U+202E, U+2066–U+2069),
// which reorder what is shown, and the zero-width characters (U+200B–U+200D,
// U+FEFF). TSV leaves these alone: they are text, a program reading the
// value wants them, and a terminal does not act on them.
func escapeHuman(r rune) string {
	if (r >= 0x200b && r <= 0x200f) || (r >= 0x202a && r <= 0x202e) ||
		(r >= 0x2066 && r <= 0x2069) || r == 0xfeff {
		return fmt.Sprintf(`\u%04x`, r)
	}
	return escapeControl(r)
}

// clean returns s with each rune escape names replaced, each byte that is not
// UTF-8 written as \xNN, and each styled chunk render made handed to span.
// It allocates only when there is something to replace.
func clean(s string, escape func(rune) string, span func(string) string) string {
	dirty := false
	for _, r := range s {
		if r == utf8.RuneError || escape(r) != "" {
			dirty = true
			break
		}
	}
	if !dirty {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] == 0x1b {
			if n := trustedLen(s[i:]); n > 0 {
				b.WriteString(span(s[i : i+n]))
				i += n
				continue
			}
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		switch e := escape(r); {
		case r == utf8.RuneError && size == 1:
			fmt.Fprintf(&b, `\x%02x`, s[i])
		case e != "":
			b.WriteString(e)
		default:
			b.WriteString(s[i : i+size])
		}
		i += size
	}
	return b.String()
}

// sanitizeCells returns cells with each one sanitized.
func sanitizeCells(cells []string) []string {
	out := make([]string, len(cells))
	for i, v := range cells {
		out[i] = Sanitize(v)
	}
	return out
}

// sanitizeRows returns rows with every cell sanitized. rows itself is not
// modified: callers may hold on to it.
func sanitizeRows(rows [][]string) [][]string {
	out := make([][]string, len(rows))
	for i, r := range rows {
		out[i] = sanitizeCells(r)
	}
	return out
}

// unstyled is what a styling helper returns when colour is off: s sanitized,
// or in TSV s as it is, since writeTSV escapes every cell its own way and
// would escape a sanitized value's backslashes a second time.
func (c *Config) unstyled(s string) string {
	if c.Format == FormatTSV {
		return s
	}
	return Sanitize(s)
}

// unstyle removes the styling render added to s and leaves every other byte
// as it is, for the writer to escape.
func unstyle(s string) string {
	if !strings.Contains(s, sgrReset) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] == 0x1b {
			if n := trustedLen(s[i:]); n > 0 {
				b.WriteString(ansi.Strip(s[i : i+n]))
				i += n
				continue
			}
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}
