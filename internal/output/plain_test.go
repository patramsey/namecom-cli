package output

import (
	"bytes"
	"testing"
)

// TestPlainTable: with stdout piped, `-o table` kept its box drawing, so
// piping `domain list -o table` into awk or cut handed them borders (#233). A
// Plain config prints whitespace-aligned columns instead, and does not fit
// them to a width.
func TestPlainTable(t *testing.T) {
	var buf bytes.Buffer
	c := &Config{Format: FormatTable, Color: ColorNever, Writer: &buf, EWriter: &bytes.Buffer{}, Plain: true, MaxWidth: 10}
	c.Table([]string{"DOMAIN", "LOCKED"}, [][]string{
		{"example.com", "yes"},
		{"a-much-longer-name.com", "no"},
		{"multi\nline\tcell", "—"},
	})
	want := "DOMAIN                  LOCKED\n" +
		"example.com             yes\n" +
		"a-much-longer-name.com  no\n" +
		"multi line cell         —\n"
	if got := buf.String(); got != want {
		t.Errorf("plain table:\n%s\nwant:\n%s", got, want)
	}

	buf.Reset()
	c.NoHeader = true
	c.Table([]string{"DOMAIN"}, [][]string{{"example.com"}})
	if got := buf.String(); got != "example.com\n" {
		t.Errorf("--no-header plain table = %q", got)
	}

	buf.Reset()
	c.KVTable([][]string{{"Domain", "example.com"}, {"Auto-Renew", "yes"}})
	if got, want := buf.String(), "Domain      example.com\nAuto-Renew  yes\n"; got != want {
		t.Errorf("plain KVTable = %q, want %q", got, want)
	}
}

// Under go test stdout is not a terminal, so DefaultConfig must choose plain
// tables, as it chooses JSON.
func TestDefaultConfig_PlainWhenPiped(t *testing.T) {
	if isStdoutTTY() {
		t.Skip("stdout is a terminal")
	}
	if c := DefaultConfig(); !c.Plain {
		t.Error("DefaultConfig().Plain = false with stdout not a terminal")
	}
}
