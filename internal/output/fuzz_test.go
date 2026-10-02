package output

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"
	"gopkg.in/yaml.v3"
)

var errSkip = errors.New("skip")

// decodeJSONStrict decodes one JSON value into plain Go values with numbers
// as float64. It returns errSkip for inputs Go's own encoder can never hand to
// writeYAML: duplicate object keys, and numbers outside float64's range.
func decodeJSONStrict(dec *json.Decoder) (any, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	switch t := tok.(type) {
	case json.Delim:
		if t == '[' {
			arr := []any{}
			for dec.More() {
				v, err := decodeJSONStrict(dec)
				if err != nil {
					return nil, err
				}
				arr = append(arr, v)
			}
			_, err := dec.Token()
			return arr, err
		}
		obj := map[string]any{}
		for dec.More() {
			k, err := dec.Token()
			if err != nil {
				return nil, err
			}
			ks := k.(string)
			if _, dup := obj[ks]; dup {
				return nil, errSkip
			}
			v, err := decodeJSONStrict(dec)
			if err != nil {
				return nil, err
			}
			obj[ks] = v
		}
		_, err := dec.Token()
		return obj, err
	case json.Number:
		f, err := strconv.ParseFloat(t.String(), 64)
		if err != nil || math.IsInf(f, 0) {
			return nil, errSkip
		}
		// An integer beyond uint64 is tagged !!int and yaml.v3 refuses it;
		// no Go value the CLI encodes can produce one.
		if !strings.ContainsAny(t.String(), ".eE") {
			if _, err := strconv.ParseInt(t.String(), 10, 64); err != nil {
				if _, err := strconv.ParseUint(t.String(), 10, 64); err != nil {
					return nil, errSkip
				}
			}
		}
		return f, nil
	case string:
		return t, nil
	default:
		return t, nil
	}
}

// normalizeYAML converts yaml.v3's decoded value into the shape
// decodeJSONStrict produces: numbers as float64, maps keyed by string.
func normalizeYAML(v any) (any, error) {
	switch t := v.(type) {
	case map[string]any:
		out := map[string]any{}
		for k, e := range t {
			n, err := normalizeYAML(e)
			if err != nil {
				return nil, err
			}
			out[k] = n
		}
		return out, nil
	case map[any]any:
		return nil, fmt.Errorf("non-string-keyed map %v", t)
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			n, err := normalizeYAML(e)
			if err != nil {
				return nil, err
			}
			out[i] = n
		}
		return out, nil
	case int:
		return float64(t), nil
	case int64:
		return float64(t), nil
	case uint64:
		return float64(t), nil
	case float64:
		return t, nil
	default:
		return t, nil
	}
}

// FuzzYAMLMatchesJSON checks the property output.YAML is built on: the YAML
// it writes for a value parses back to the same data as that value's JSON.
func FuzzYAMLMatchesJSON(f *testing.F) {
	for _, s := range []string{
		`{"a":1,"b":"2","c":true,"d":null,"e":[1.5,"x"]}`,
		`["true","null","~","123","0x1F","1e3",".inf","-.nan","yes","no","on"]`,
		`{"<<":{"a":1},"":"empty","- x":"y","? q":":"}`,
		`"line1\nline2\n"`, `"  lead"`, `"trail  "`, `"\u0085"`, `"\ufeff"`, "\"\u2028\"",
		`"\t"`, `"#"`, `"a: b"`, `"'q'"`, `"!tag"`, `"&anchor"`, `"*alias"`,
		`-0`, `1E5`, `0.0000001`, `12345678901234567890`, `{"key\u0000":1}`,
		`"2001-01-01"`, `"1_000"`, `"0o17"`, `"+1"`, `".5"`,
	} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		if !json.Valid(data) {
			return
		}
		dec := json.NewDecoder(bytes.NewReader(data))
		dec.UseNumber()
		want, err := decodeJSONStrict(dec)
		if errors.Is(err, errSkip) {
			return
		}
		if err != nil {
			t.Fatalf("decoding valid JSON: %v", err)
		}

		var buf bytes.Buffer
		if err := writeYAML(&buf, json.RawMessage(data)); err != nil {
			t.Fatalf("writeYAML(%s): %v", data, err)
		}
		var raw any
		if err := yaml.Unmarshal(buf.Bytes(), &raw); err != nil {
			t.Fatalf("YAML for %s does not parse: %v\n%s", data, err, buf.String())
		}
		got, err := normalizeYAML(raw)
		if err != nil {
			t.Fatalf("YAML for %s: %v\n%s", data, err, buf.String())
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("YAML does not round-trip JSON %s\n yaml: %s\n want: %#v\n  got: %#v",
				data, buf.String(), want, got)
		}
	})
}

// FuzzTableFits checks Table's width handling: when more than one column
// survives, every rendered line fits MaxWidth, and the surviving columns are a
// prefix of the originals.
func FuzzTableFits(f *testing.F) {
	f.Add("NAME", "VALUE", "NOTE", "example.com", "1.2.3.4", "a note", 40)
	f.Add("名前", "値", "注", "例え.com", "一二三", "ノート", 20)
	f.Add("A", "B", "C", "\x1b[31mred\x1b[0m", "x", "y", 15)
	f.Add("A", "B", "C", "tab\there", "x", "y", 12)
	f.Add("A", "B", "C", "multi\nline", "x", "y", 12)
	f.Add("A", "B", "C", "👍🏽", "e\u0301", "\u200b", 12)
	f.Fuzz(func(t *testing.T, h1, h2, h3, c1, c2, c3 string, width int) {
		if width < 1 || width > 300 {
			return
		}
		// Cells reach Table from decoded JSON, where invalid UTF-8 has
		// already become U+FFFD.
		for _, s := range []string{h1, h2, h3, c1, c2, c3} {
			if !utf8.ValidString(s) {
				return
			}
		}
		var buf bytes.Buffer
		c := &Config{Format: FormatTable, Color: ColorNever, Writer: &buf, EWriter: &buf, MaxWidth: width}
		headers := []string{h1, h2, h3}
		rows := [][]string{{c1, c2, c3}}
		kept, _, dropped := c.fitColumns(headers, rows)
		if len(kept)+len(dropped) != len(headers) {
			t.Fatalf("kept %d + dropped %d != %d", len(kept), len(dropped), len(headers))
		}
		c.Table(headers, rows)
		if len(kept) < 2 {
			return // the first column is never dropped, so it may overflow
		}
		table := buf.String()
		if i := strings.LastIndex(table, "column"); len(dropped) > 0 && i >= 0 {
			table = table[:strings.LastIndex(table[:i], "\n")+1]
		}
		for line := range strings.SplitSeq(strings.TrimRight(table, "\n"), "\n") {
			if w := lipgloss.Width(line); w > width {
				t.Fatalf("line is %d cells wide, MaxWidth %d (kept %d columns):\n%s", w, width, len(kept), buf.String())
			}
		}
	})
}
