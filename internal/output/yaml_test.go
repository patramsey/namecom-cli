package output

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"

	"gopkg.in/yaml.v3"
)

// yamlNastyStrings are strings yaml.v3 does not round-trip when left to choose
// a style itself, plus their neighbours. Most came from fuzzing
// writeYAML against json.Marshal (#190): a leading line break was dropped by
// the block scalar yaml.v3 picked, a tab before a line break produced YAML
// that yaml.v3 could not read back, and "<<" became a merge key.
var yamlNastyStrings = []string{
	"", " ", "  ", "a", "plain text",
	"\n", "\n0", "\n\n", "a\n", "a\nb", "a\nb\n", "a\n\n", " a\nb", "a\nb ",
	"\t", "\t\n", "\ta\nb", "a\tb", "a\t\nb", "\ta", "a\t",
	"\r", "\r\n", "a\r\nb", "\x00", "\x1b[31m", "\x7f",
	"\u0085", "\u0085a", "a\u0085b", "\u2028", "\u2028a", "a\u2028b", "\u2029", "\u2029a",
	"\u0085\n", "\u2028\n", "\u2029\n", "\u0085\na", "\u2028 a\nb", "\u2029\t\n",
	"\ufeff", "\ufeffa",
	" leading", "trailing ", "#comment", "a #b", "a: b", "- a", "? a", "*a", "&a", "!a", "|", ">", "%a", "@a", "`a",
	"'", "\"", "[a]", "{a}", ",", "---", "...", "--- a",
	"<<", "<< ", "=", "~", "null", "Null", "NULL", "true", "True", "yes", "no", "on", "off", "y", "n",
	"0", "-1", "0x10", "0o7", "010", "1e3", ".5", "1_000", ".inf", "-.Inf", ".nan", "2026-10-01",
}

func TestWriteYAML_RoundTripsLikeJSON(t *testing.T) {
	check := func(t *testing.T, v any) {
		t.Helper()
		j, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		var want any
		if err := json.Unmarshal(j, &want); err != nil {
			t.Fatal(err)
		}
		var buf bytes.Buffer
		if err := writeYAML(&buf, v); err != nil {
			t.Errorf("writeYAML(%s): %v", j, err)
			return
		}
		var got any
		if err := yaml.Unmarshal(buf.Bytes(), &got); err != nil {
			t.Errorf("YAML for %s does not parse: %v\n%s", j, err, buf.String())
			return
		}
		if !reflect.DeepEqual(yamlAsJSON(got), want) {
			t.Errorf("YAML for %s reads back as %#v, want %#v\n%s", j, got, want, buf.String())
		}
	}
	for _, s := range yamlNastyStrings {
		check(t, map[string]any{"a": s})
		check(t, map[string]any{s: "a"})
		check(t, map[string]any{s: map[string]any{"a": 1}})
		check(t, []any{s, map[string]any{"k": []any{s}}})
	}
}

// TestWriteYAML_QuotesYAML11Scalars pins that a string a YAML 1.1 parser —
// Ruby's YAML.load, PyYAML — reads as a boolean or null is quoted. yaml.v3
// quotes what YAML 1.2 would mistype, but wrote `value: NO` for Norway's
// country code and `value: yes` for a requirements option, which those
// parsers read back as false and true.
func TestWriteYAML_QuotesYAML11Scalars(t *testing.T) {
	for _, s := range []string{
		"y", "Y", "yes", "Yes", "YES", "yEs", "n", "N", "no", "No", "NO",
		"on", "On", "ON", "off", "Off", "OFF", "true", "True", "TRUE", "false", "FALSE",
		"null", "Null", "NULL", "~",
	} {
		var buf bytes.Buffer
		if err := writeYAML(&buf, map[string]any{s: s}); err != nil {
			t.Fatal(err)
		}
		if want := `"` + s + `": "` + s + `"` + "\n"; buf.String() != want {
			t.Errorf("%q: got %q, want %q", s, buf.String(), want)
		}
	}
	// Strings that only look like one stay plain.
	for _, s := range []string{"yess", "nope", "onto", "Norway", "nul"} {
		var buf bytes.Buffer
		if err := writeYAML(&buf, map[string]any{"a": s}); err != nil {
			t.Fatal(err)
		}
		if want := "a: " + s + "\n"; buf.String() != want {
			t.Errorf("%q: got %q, want %q", s, buf.String(), want)
		}
	}
}

// yamlAsJSON turns what yaml.Unmarshal decodes into what json.Unmarshal
// would: map[string]any and float64 numbers.
func yamlAsJSON(v any) any {
	switch t := v.(type) {
	case map[string]any:
		m := make(map[string]any, len(t))
		for k, e := range t {
			m[k] = yamlAsJSON(e)
		}
		return m
	case []any:
		s := make([]any, len(t))
		for i, e := range t {
			s[i] = yamlAsJSON(e)
		}
		return s
	case int:
		return float64(t)
	default:
		return v
	}
}
