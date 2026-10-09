package dns

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/patramsey/namecom-cli/internal/output"
)

// TestDNSCreate_SaysChanged pins #326: a plain `dns create -o json` printed
// the record with no "changed" key, which `--if-not-exists` and every update
// carry. It is one POST, and says "changed": true in JSON and YAML.
func TestDNSCreate_SaysChanged(t *testing.T) {
	for _, format := range []output.Format{output.FormatJSON, output.FormatYAML} {
		t.Run(string(format), func(t *testing.T) {
			var n atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				n.Add(1)
				if r.Method != http.MethodPost {
					t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"id":9,"type":"A","host":"api","answer":"192.0.2.9","ttl":300}`))
			}))
			t.Cleanup(srv.Close)
			cmd, captured := createFor(t, srv, runOpts{yes: true, format: format}, "--type", "A", "--host", "api", "--answer", "192.0.2.9")
			if err := runCreate(cmd, []string{"example.com"}); err != nil {
				t.Fatalf("runCreate: %v", err)
			}
			stdout, _ := captured()
			if format == output.FormatYAML {
				if want := "changed: true\n"; len(stdout) < len(want) || stdout[len(stdout)-len(want):] != want {
					t.Errorf("stdout = %q, want it to end %q", stdout, want)
				}
			} else {
				var doc map[string]any
				if err := json.Unmarshal([]byte(stdout), &doc); err != nil || doc["changed"] != true || doc["id"] != float64(9) {
					t.Errorf("stdout = %q (%v), want record 9 with \"changed\": true", stdout, err)
				}
			}
			if got := n.Load(); got != 1 {
				t.Errorf("requests = %d, want 1", got)
			}
		})
	}
}
