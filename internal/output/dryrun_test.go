package output

import (
	"bytes"
	"encoding/json"
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
		c.DryRun("POST", "/core/v1/domains/example.com/records", body)

		var got map[string]any
		if err := json.Unmarshal(w.Bytes(), &got); err != nil {
			t.Fatalf("dry-run output is not JSON: %v\n%s", err, w.String())
		}
		want := map[string]any{
			"dry_run": true, "method": "POST", "path": "/core/v1/domains/example.com/records",
			"body": map[string]any{"host": "www", "ttl": float64(300)},
		}
		assertEqualJSON(t, got, want)
	})

	t.Run("yaml", func(t *testing.T) {
		var w bytes.Buffer
		c := &Config{Format: FormatYAML, Color: ColorNever, Writer: &w}
		c.DryRun("PUT", "/core/v1/domains/example.com/records/7", body)

		var got map[string]any
		if err := yaml.Unmarshal(w.Bytes(), &got); err != nil {
			t.Fatalf("dry-run output is not YAML: %v\n%s", err, w.String())
		}
		if got["dry_run"] != true || got["method"] != "PUT" || got["path"] != "/core/v1/domains/example.com/records/7" {
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
			c.DryRun("DELETE", "/core/v1/domains/example.com/records/7", nil)
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
		c.DryRun("POST", "/core/v1/x", rawText("host=www"))
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
		c.DryRun("POST", "/core/v1/x", body)
		if !strings.HasPrefix(w.String(), "POST /core/v1/x\n") || strings.Contains(w.String(), "dry_run") {
			t.Errorf("table mode should keep the request line, got: %q", w.String())
		}
	})
}

// TestDryRunAll covers previews of several requests (`dns import`): one JSON
// array in structured modes, so a script can parse the whole plan at once.
func TestDryRunAll(t *testing.T) {
	reqs := []DryRunRequest{
		{Method: "POST", Path: "/a", Body: map[string]any{"n": 1}},
		{Method: "POST", Path: "/b", Body: map[string]any{"n": 2}},
	}

	t.Run("json array", func(t *testing.T) {
		var w bytes.Buffer
		c := &Config{Format: FormatJSON, Color: ColorNever, Writer: &w}
		c.DryRunAll(reqs)
		var got []map[string]any
		if err := json.Unmarshal(w.Bytes(), &got); err != nil {
			t.Fatalf("output is not a JSON array: %v\n%s", err, w.String())
		}
		if len(got) != 2 || got[0]["path"] != "/a" || got[1]["path"] != "/b" || got[1]["dry_run"] != true {
			t.Errorf("unexpected array: %s", w.String())
		}
	})

	t.Run("yaml sequence", func(t *testing.T) {
		var w bytes.Buffer
		c := &Config{Format: FormatYAML, Color: ColorNever, Writer: &w}
		c.DryRunAll(reqs)
		var got []map[string]any
		if err := yaml.Unmarshal(w.Bytes(), &got); err != nil || len(got) != 2 {
			t.Fatalf("output is not a two-item YAML sequence: %v\n%s", err, w.String())
		}
	})

	t.Run("empty is an empty array", func(t *testing.T) {
		var w bytes.Buffer
		c := &Config{Format: FormatJSON, Color: ColorNever, Writer: &w}
		c.DryRunAll(nil)
		if strings.TrimSpace(w.String()) != "[]" {
			t.Errorf("no requests should print [], got %q", w.String())
		}
	})

	t.Run("table prints each request", func(t *testing.T) {
		var w bytes.Buffer
		c := &Config{Format: FormatTable, Color: ColorNever, Writer: &w}
		c.DryRunAll(reqs)
		if strings.Count(w.String(), "POST /") != 2 {
			t.Errorf("want two request lines, got %q", w.String())
		}
	})
}

func assertEqualJSON(t *testing.T, got, want any) {
	t.Helper()
	g, _ := json.Marshal(got)
	w, _ := json.Marshal(want)
	if string(g) != string(w) {
		t.Errorf("got  %s\nwant %s", g, w)
	}
}
