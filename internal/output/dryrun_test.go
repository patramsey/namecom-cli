package output

import (
	"bytes"
	"encoding/json"
	"math"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// rawText marshals as a JSON string, the way `namecom api` previews a --data
// body that is not JSON.
type rawText string

func (r rawText) MarshalJSON() ([]byte, error) { return json.Marshal(string(r)) }

// TestDryRun_StructuredFormats pins issue #137: --dry-run printed the human
// "METHOD /path" form in every format, so a script asking for -o json had to
// parse text to see the planned request.
func TestDryRun_StructuredFormats(t *testing.T) {
	body := map[string]any{"host": "www", "ttl": 300}

	t.Run("json", func(t *testing.T) {
		var w bytes.Buffer
		c := &Config{Format: FormatJSON, Color: ColorNever, Writer: &w}
		must(t, c.DryRun("POST", "/core/v1/domains/example.com/records", body))

		var got map[string]any
		if err := json.Unmarshal(w.Bytes(), &got); err != nil {
			t.Fatalf("dry-run output is not JSON: %v\n%s", err, w.String())
		}
		want := map[string]any{
			"dryRun": true, "method": "POST", "path": "/core/v1/domains/example.com/records",
			"body": map[string]any{"host": "www", "ttl": float64(300)},
		}
		assertEqualJSON(t, got, want)
	})

	t.Run("yaml", func(t *testing.T) {
		var w bytes.Buffer
		c := &Config{Format: FormatYAML, Color: ColorNever, Writer: &w}
		must(t, c.DryRun("PUT", "/core/v1/domains/example.com/records/7", body))

		var got map[string]any
		if err := yaml.Unmarshal(w.Bytes(), &got); err != nil {
			t.Fatalf("dry-run output is not YAML: %v\n%s", err, w.String())
		}
		if got["dryRun"] != true || got["method"] != "PUT" || got["path"] != "/core/v1/domains/example.com/records/7" {
			t.Errorf("unexpected YAML document:\n%s", w.String())
		}
		b, ok := got["body"].(map[string]any)
		if !ok || b["host"] != "www" || b["ttl"] != 300 {
			t.Errorf("body not carried through, got:\n%s", w.String())
		}
	})

	t.Run("nil body is omitted", func(t *testing.T) {
		for _, f := range []Format{FormatJSON, FormatYAML} {
			var w bytes.Buffer
			c := &Config{Format: f, Color: ColorNever, Writer: &w}
			must(t, c.DryRun("DELETE", "/core/v1/domains/example.com/records/7", nil))
			if strings.Contains(w.String(), "body") {
				t.Errorf("%s: a request without a body should have no body key, got:\n%s", f, w.String())
			}
			if !strings.Contains(w.String(), "DELETE") {
				t.Errorf("%s: method missing, got:\n%s", f, w.String())
			}
		}
	})

	t.Run("non-JSON raw body stays a string", func(t *testing.T) {
		var w bytes.Buffer
		c := &Config{Format: FormatJSON, Color: ColorNever, Writer: &w}
		must(t, c.DryRun("POST", "/core/v1/x", rawText("host=www")))
		var got map[string]any
		if err := json.Unmarshal(w.Bytes(), &got); err != nil {
			t.Fatalf("dry-run output is not JSON: %v\n%s", err, w.String())
		}
		if got["body"] != "host=www" {
			t.Errorf(`body = %v, want the raw string "host=www"`, got["body"])
		}
	})

	t.Run("table keeps the human form", func(t *testing.T) {
		var w bytes.Buffer
		c := &Config{Format: FormatTable, Color: ColorNever, Writer: &w}
		must(t, c.DryRun("POST", "/core/v1/x", body))
		if !strings.HasPrefix(w.String(), "POST /core/v1/x\n") || strings.Contains(w.String(), "dryRun") {
			t.Errorf("table mode should keep the request line, got: %q", w.String())
		}
	})
}

// TestDryRunAll covers previews of several requests (`dns import`): one
// {"dryRun": true, "data": [...]} document in structured modes, so a script
// can parse the whole plan at once, wrapped like every other list (#240).
func TestDryRunAll(t *testing.T) {
	reqs := []DryRunRequest{
		{Method: "POST", Path: "/a", Body: map[string]any{"n": 1}},
		{Method: "POST", Path: "/b", Body: map[string]any{"n": 2}},
	}
	type plan struct {
		DryRun bool             `json:"dryRun" yaml:"dryRun"`
		Data   []map[string]any `json:"data" yaml:"data"`
	}

	t.Run("json envelope", func(t *testing.T) {
		var w bytes.Buffer
		c := &Config{Format: FormatJSON, Color: ColorNever, Writer: &w}
		must(t, c.DryRunAll(reqs))
		var got plan
		if err := json.Unmarshal(w.Bytes(), &got); err != nil {
			t.Fatalf("output is not a JSON object: %v\n%s", err, w.String())
		}
		if !got.DryRun || len(got.Data) != 2 || got.Data[0]["path"] != "/a" || got.Data[1]["path"] != "/b" || got.Data[1]["dryRun"] != true {
			t.Errorf("unexpected plan: %s", w.String())
		}
	})

	t.Run("yaml envelope", func(t *testing.T) {
		var w bytes.Buffer
		c := &Config{Format: FormatYAML, Color: ColorNever, Writer: &w}
		must(t, c.DryRunAll(reqs))
		var got plan
		if err := yaml.Unmarshal(w.Bytes(), &got); err != nil || len(got.Data) != 2 {
			t.Fatalf("output is not a two-item YAML plan: %v\n%s", err, w.String())
		}
	})

	t.Run("empty is an empty data array", func(t *testing.T) {
		var w bytes.Buffer
		c := &Config{Format: FormatJSON, Color: ColorNever, Writer: &w}
		must(t, c.DryRunAll(nil))
		if !strings.Contains(w.String(), `"data": []`) {
			t.Errorf(`no requests should print "data": [], got %q`, w.String())
		}
	})

	t.Run("table prints each request", func(t *testing.T) {
		var w bytes.Buffer
		c := &Config{Format: FormatTable, Color: ColorNever, Writer: &w}
		must(t, c.DryRunAll(reqs))
		if strings.Count(w.String(), "POST /") != 2 {
			t.Errorf("want two request lines, got %q", w.String())
		}
	})
}

// TestDryRun_UnmarshalableBodyIsAnError pins the output half of issue #168.
// DryRun discarded the marshal error, so a body JSON cannot encode — a
// --price of +Inf — printed nothing in JSON and YAML modes, and a bare request
// line in table mode, and the command exited 0 as though it had previewed.
func TestDryRun_UnmarshalableBodyIsAnError(t *testing.T) {
	body := map[string]any{"purchasePrice": math.Inf(1)}
	for _, f := range []Format{FormatJSON, FormatYAML, FormatTable} {
		t.Run(string(f), func(t *testing.T) {
			var w bytes.Buffer
			c := &Config{Format: f, Color: ColorNever, Writer: &w}
			if err := c.DryRun("POST", "/core/v1/domains", body); err == nil {
				t.Error("DryRun: want an error for a body that cannot be encoded")
			}
			if err := c.DryRunAll([]DryRunRequest{{Method: "POST", Path: "/a", Body: body}}); err == nil {
				t.Error("DryRunAll: want an error for a body that cannot be encoded")
			}
			// Nothing half-printed: a script must not mistake a request line
			// without its body for the preview.
			if w.Len() != 0 {
				t.Errorf("want no output, got:\n%s", w.String())
			}
		})
	}
}

// TestDryRunQuote pins #235's dry-run cost: JSON gains a "quote" field beside
// the unchanged request, and table mode one stderr line after it. A nil quote
// is plain DryRun, with no "quote" key at all.
func TestDryRunQuote(t *testing.T) {
	q := &Quote{Total: 39.98, Currency: "USD", Years: 2}

	t.Run("json", func(t *testing.T) {
		var w bytes.Buffer
		c := &Config{Format: FormatJSON, Color: ColorNever, Writer: &w}
		must(t, c.DryRunQuote("POST", "/core/v1/domains/a.io:renew", map[string]any{"years": 2}, q, "sandbox"))
		var got map[string]any
		if err := json.Unmarshal(w.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		assertEqualJSON(t, got, map[string]any{
			"dryRun": true, "method": "POST", "path": "/core/v1/domains/a.io:renew",
			"body":  map[string]any{"years": float64(2)},
			"quote": map[string]any{"total": 39.98, "currency": "USD", "years": float64(2)},
		})
	})

	t.Run("table", func(t *testing.T) {
		var w, e bytes.Buffer
		c := &Config{Format: FormatTable, Color: ColorNever, Writer: &w, EWriter: &e}
		must(t, c.DryRunQuote("POST", "/core/v1/domains/a.io:renew", nil, q, "sandbox · profile default"))
		if got, want := e.String(), "Would charge: $39.98 (2 years) · sandbox · profile default\n"; got != want {
			t.Errorf("stderr = %q, want %q", got, want)
		}
		if strings.Contains(w.String(), "charge") {
			t.Errorf("stdout carries the charge line:\n%s", w.String())
		}
	})

	t.Run("nil quote", func(t *testing.T) {
		var w, e bytes.Buffer
		c := &Config{Format: FormatJSON, Color: ColorNever, Writer: &w, EWriter: &e}
		must(t, c.DryRunQuote("DELETE", "/x", nil, nil, "sandbox"))
		if strings.Contains(w.String(), "quote") || e.Len() != 0 {
			t.Errorf("want no quote, got stdout %q stderr %q", w.String(), e.String())
		}
	})
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func assertEqualJSON(t *testing.T, got, want any) {
	t.Helper()
	g, _ := json.Marshal(got)
	w, _ := json.Marshal(want)
	if string(g) != string(w) {
		t.Errorf("got  %s\nwant %s", g, w)
	}
}
