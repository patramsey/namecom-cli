package output

import (
	"bytes"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// TestListPage_Parts: every page of a list the API counts says where it
// sits, the last one included, which said "1 domain" where page 1 said
// "Showing 1–2 of 5 domains".
func TestListPage_Parts(t *testing.T) {
	tests := []struct {
		name string
		p    ListPage
		want string
	}{
		{"first page", ListPage{Noun: "domain", Count: 2, From: 1, To: 2, Total: 5, Next: 2},
			"Showing 1–2 of 5 domains · --page 2 for more, --all for everything"},
		{"last page", ListPage{Noun: "domain", Count: 1, From: 5, To: 5, Total: 5},
			"Showing 5–5 of 5 domains"},
		{"the whole list", ListPage{Noun: "domain", Count: 5, From: 1, To: 5, Total: 5}, "5 domains"},
		{"position unknown", ListPage{Noun: "order", Count: 2, Total: 9122, Next: 2},
			"2 of 9,122 orders · --page 2 for more, --all for everything"},
		{"no total", ListPage{Noun: "transfer", Count: 2, Next: 3}, "2 transfers · --page 3 for more, --all for everything"},
		{"past the end", ListPage{Noun: "domain", Page: 4},
			"No domains on page 4 · that is past the last page; leave out --page to start at the first"},
		{"empty first page", ListPage{Noun: "domain", Page: 1}, "0 domains"},
		{"notes and narrowing", ListPage{Noun: "order", Count: 2, From: 1, To: 2, Total: 10, Notes: []string{"newest first"}, Next: 2, Narrow: "--since"},
			"Showing 1–2 of 10 orders · newest first · --page 2 for more, --all for everything, or narrow with --since"},
	}
	for _, tc := range tests {
		if got := strings.Join(tc.p.parts(), " · "); got != tc.want {
			t.Errorf("%s: %q, want %q", tc.name, got, tc.want)
		}
	}
}

// TestHint_WrappedLinesNotPadded: a hint wrapped in a colour terminal has no
// trailing spaces. The wrapped block was styled as one, and lipgloss pads
// every line of a block to the widest, so a continuation line ended in
// dozens of spaces after the colour reset (#325).
func TestHint_WrappedLinesNotPadded(t *testing.T) {
	var buf bytes.Buffer
	c := &Config{Format: FormatTable, Color: ColorAlways, MaxWidth: 50, Writer: &bytes.Buffer{}, EWriter: &buf}
	c.Hint("Run 'namecom dns create example.com --type A --answer 1.2.3.4' to add the first record")
	lines := strings.Split(strings.TrimSuffix(buf.String(), "\n"), "\n")
	if len(lines) < 2 {
		t.Fatalf("want the hint wrapped, got %q", buf.String())
	}
	for _, l := range lines {
		if strings.HasSuffix(l, " ") || ansi.StringWidth(l) > 50 {
			t.Errorf("line %q is padded or wider than 50", l)
		}
	}
	if got := ansi.Strip(lines[1]); !strings.HasPrefix(got, "  ") || strings.HasPrefix(got, "   ") {
		t.Errorf("continuation %q, want it under the text, two spaces in", got)
	}
}

// TestTable_FitNoteWraps: the note under a table cut to fit the terminal
// wraps to it, as the footer under it does. It was one line of up to 105
// columns at 60 (#325).
func TestTable_FitNoteWraps(t *testing.T) {
	var out, errOut bytes.Buffer
	c := &Config{Format: FormatTable, Color: ColorNever, MaxWidth: 60, Writer: &out, EWriter: &errOut}
	c.Table([]string{"DOMAIN", "EXPIRES", "AUTO-RENEW", "LOCKED", "PRIVACY"},
		[][]string{{"a-rather-long-domain-name-for-the-terminal.com", "2027-01-01 (in 3 months)", "yes", "no", "yes"}})
	if !strings.Contains(errOut.String(), "hidden") {
		t.Fatalf("want a fitting note, got %q", errOut.String())
	}
	for l := range strings.SplitSeq(strings.TrimSuffix(errOut.String(), "\n"), "\n") {
		if w := ansi.StringWidth(l); w > 60 {
			t.Errorf("note line is %d columns, wider than 60: %q", w, l)
		}
	}
}

// TestFooter_WrapsToTerminal: a footer wider than the terminal breaks
// between its parts. The order list footer was 111 columns at 80.
func TestFooter_WrapsToTerminal(t *testing.T) {
	var errw bytes.Buffer
	c := &Config{Format: FormatTable, Color: ColorNever, Writer: &bytes.Buffer{}, EWriter: &errw, MaxWidth: 80}
	c.ListFooter(ListPage{Noun: "order", Count: 2, From: 1, To: 2, Total: 9122, Notes: []string{"newest first"},
		Next: 2, Narrow: "--since, --domain or --status"})
	want := "Showing 1–2 of 9,122 orders · newest first\n" +
		"--page 2 for more, --all for everything, or narrow with --since, --domain or\n--status\n"
	if errw.String() != want {
		t.Errorf("got:\n%s\nwant:\n%s", errw.String(), want)
	}
	for line := range strings.SplitSeq(strings.TrimSuffix(errw.String(), "\n"), "\n") {
		if len([]rune(line)) > 80 {
			t.Errorf("line wider than 80: %q", line)
		}
	}

	errw.Reset()
	c.Hint(strings.Repeat("word ", 30))
	for i, line := range strings.Split(strings.TrimSuffix(errw.String(), "\n"), "\n") {
		if len([]rune(line)) > 80 {
			t.Errorf("hint line wider than 80: %q", line)
		}
		if i > 0 && !strings.HasPrefix(line, "  word") {
			t.Errorf("continuation %q is not indented under the text", line)
		}
	}
}

// TestFields_ListFooterAfterTable: with --fields in a table, a list's footer
// is the one the command gives ListFooter, worded as it is without --fields,
// and printed after the table. It printed first, with "results" for the
// noun, because the table is printed only once the command has returned.
func TestFields_ListFooterAfterTable(t *testing.T) {
	var both bytes.Buffer
	c := &Config{Format: FormatTable, Color: ColorNever, Writer: &both, EWriter: &both, Plain: true}
	c.BeginFilter(&Filter{Fields: []string{"domainName"}})
	c.ListFooter(ListPage{Noun: "domain", Count: 2, From: 1, To: 2, Total: 6522, Next: 2})
	if err := c.JSONList([]map[string]string{{"domainName": "a.com"}, {"domainName": "b.com"}}, nil, int32Ptr(6522)); err != nil {
		t.Fatal(err)
	}
	if err := c.EndFilter(false); err != nil {
		t.Fatal(err)
	}
	want := "domainName\na.com\nb.com\nShowing 1–2 of 6,522 domains · --page 2 for more, --all for everything\n"
	if both.String() != want {
		t.Errorf("got:\n%s\nwant:\n%s", both.String(), want)
	}

	// Other formats print no footer.
	both.Reset()
	c.Format = FormatTSV
	c.BeginFilter(&Filter{Fields: []string{"domainName"}})
	c.ListFooter(ListPage{Noun: "domain", Count: 1, Next: 2})
	if err := c.JSONList([]map[string]string{{"domainName": "a.com"}}, nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := c.EndFilter(false); err != nil {
		t.Fatal(err)
	}
	if both.String() != "domainName\na.com\n" {
		t.Errorf("tsv: %q", both.String())
	}
}
