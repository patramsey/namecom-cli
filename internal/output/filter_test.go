package output

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

// filtered runs raw through --fields fields and, if jq is set, --jq, as
// EndFilter does, in format f, and returns what it printed.
func filtered(t *testing.T, f Format, raw string, fields []string, jq string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	c := &Config{Format: f, Writer: &out, EWriter: &out, Plain: true}
	filter := &Filter{Fields: fields}
	if jq != "" {
		code, err := CompileJQ(jq)
		if err != nil {
			t.Fatal(err)
		}
		filter.JQ = code
	}
	c.BeginFilter(filter)
	_, _ = c.Writer.Write([]byte(raw))
	err := c.EndFilter(false)
	return out.String(), err
}

// TestProject_MissingInSomeItems: the API leaves out empty values, so a field
// absent from some items, even the first, is null there, not an error.
func TestProject_MissingInSomeItems(t *testing.T) {
	got, err := filtered(t, FormatJSON, `{"data":[{"a":1},{"a":2,"b":"x"}],"total":2}`, []string{"b", "a"}, "")
	if err != nil {
		t.Fatal(err)
	}
	want := "{\n  \"data\": [\n    {\n      \"b\": null,\n      \"a\": 1\n    },\n    {\n      \"b\": \"x\",\n      \"a\": 2\n    }\n  ],\n  \"total\": 2\n}\n"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

// TestProject_Unknown names every field that no item has, and lists what the
// items do have, from all of them.
func TestProject_Unknown(t *testing.T) {
	_, err := filtered(t, FormatJSON, `{"data":[{"a":1},{"b":2}]}`, []string{"a", "x", "y"}, "")
	var fe *FilterError
	if !errors.As(err, &fe) {
		t.Fatalf("want a *FilterError, got %v", err)
	}
	if msg := err.Error(); !strings.Contains(msg, `fields "x", "y"`) || !strings.HasSuffix(msg, "has: a, b") {
		t.Errorf("message: %s", msg)
	}
}

func TestProject_Shapes(t *testing.T) {
	// A bare array is a list too.
	if got, err := filtered(t, FormatTSV, `[{"a":1,"b":true},{"a":2}]`, []string{"b", "a"}, ""); err != nil || got != "b\ta\ntrue\t1\n\t2\n" {
		t.Errorf("bare array: %q, %v", got, err)
	}
	// Items that are not objects cannot be projected.
	if _, err := filtered(t, FormatJSON, `{"data":["a","b"]}`, []string{"a"}, ""); err == nil {
		t.Error("want an error for a list of strings")
	}
	// A nested value is compact JSON in a cell, and one object is
	// field<TAB>value rows.
	if got, err := filtered(t, FormatTSV, `{"n":{"x":[1,"a<b"]}}`, []string{"n"}, ""); err != nil || got != "n\t{\"x\":[1,\"a<b\"]}\n" {
		t.Errorf("nested: %q, %v", got, err)
	}
	// Numbers are not rounded on the way through.
	if got, err := filtered(t, FormatJSON, `{"id":12345678901234567890}`, []string{"id"}, ""); err != nil || !strings.Contains(got, "12345678901234567890") {
		t.Errorf("big number: %q, %v", got, err)
	}
}

// TestTSVEscaping: a cell's backslash, tab and line breaks are escaped, so a
// row is one line and a tab always separates cells.
func TestTSVEscaping(t *testing.T) {
	var out bytes.Buffer
	c := &Config{Format: FormatTSV, Writer: &out}
	c.Table([]string{"A", "B"}, [][]string{{"x\ty", "1\n2\r3\\4"}, {"", None}})
	want := "A\tB\nx\\ty\t1\\n2\\r3\\\\4\n\t\n"
	if out.String() != want {
		t.Errorf("got %q, want %q", out.String(), want)
	}
}

// TestJQ_Results: strings raw, other values compact, halt stops quietly, an
// error is a *FilterError and leaves nothing printed.
func TestJQ_Results(t *testing.T) {
	if got, err := filtered(t, FormatJSON, `{"a":"x","b":[1,2]}`, nil, `.a, .b, null, true`); err != nil || got != "x\n[1,2]\nnull\ntrue\n" {
		t.Errorf("got %q, %v", got, err)
	}
	if got, err := filtered(t, FormatJSON, `{"a":1}`, nil, `.a, halt, 2`); err != nil || got != "1\n" {
		t.Errorf("halt: %q, %v", got, err)
	}
	got, err := filtered(t, FormatJSON, `{"a":1}`, nil, `.a, error("boom")`)
	var fe *FilterError
	if !errors.As(err, &fe) || got != "" || !strings.Contains(err.Error(), "boom") {
		t.Errorf("error: %q, %v", got, err)
	}
}

// TestEndFilter_AfterWriteKeepsFormat: a --fields that does not fit a write's
// result is warned about, the change having been made, and the result is
// printed unfiltered — in the format asked for. It came out as indented JSON
// under -o tsv and -o table (#325).
func TestEndFilter_AfterWriteKeepsFormat(t *testing.T) {
	raw := `{"id":7,"host":"www","ttl":300}`
	for _, tc := range []struct {
		f    Format
		want string
	}{
		{FormatTSV, "id\t7\nhost\twww\nttl\t300\n"},
		{FormatYAML, "id: 7\nhost: www\nttl: 300\n"},
		{FormatJSON, raw},
	} {
		var out, errOut bytes.Buffer
		c := &Config{Format: tc.f, Writer: &out, EWriter: &errOut, Plain: true}
		c.BeginFilter(&Filter{Fields: []string{"bogus"}})
		_, _ = c.Writer.Write([]byte(raw))
		if err := c.EndFilter(true); err != nil {
			t.Fatalf("%s: %v", tc.f, err)
		}
		if out.String() != tc.want {
			t.Errorf("%s: got %q, want %q", tc.f, out.String(), tc.want)
		}
		if tc.f == FormatTSV && !strings.Contains(errOut.String(), `unknown field "bogus"`) {
			t.Errorf("%s: no warning: %q", tc.f, errOut.String())
		}
	}

	// A list keeps its rows, every item's keys the columns.
	var out bytes.Buffer
	c := &Config{Format: FormatTSV, Writer: &out, EWriter: &bytes.Buffer{}, Plain: true}
	c.BeginFilter(&Filter{Fields: []string{"bogus"}})
	_, _ = c.Writer.Write([]byte(`{"success":true,"data":[{"domain":"a.com","changed":true},{"domain":"b.com","id":3,"changed":false}]}`))
	if err := c.EndFilter(true); err != nil {
		t.Fatal(err)
	}
	if want := "domain\tchanged\tid\na.com\ttrue\t\nb.com\tfalse\t3\n"; out.String() != want {
		t.Errorf("list: got %q, want %q", out.String(), want)
	}
}
