package url

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/patramsey/namecom-cli/cmd/cmdutil"
	"github.com/patramsey/namecom-cli/internal/output"
)

// TestURLCreate_SaysChanged pins #326: a plain `url create -o json` printed
// the forwarding with no "changed" key, which only the create recovered from
// a Duplicate Record carried. It is one POST, and says "changed": true.
func TestURLCreate_SaysChanged(t *testing.T) {
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		if r.Method != http.MethodPost {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":7,"host":"www","forwardsTo":"https://example.org","type":"redirect"}`))
	}))
	t.Cleanup(srv.Close)
	cmd := withDryRun(t, cmdForURLCreate(t, srv), false)
	out := cmdutil.Out(cmd)
	out.Format = output.FormatJSON
	if err := cmd.ParseFlags([]string{"--to", "https://example.org", "--host", "www"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	if err := runCreate(cmd, []string{"example.com"}); err != nil {
		t.Fatalf("runCreate: %v", err)
	}
	stdout := out.Writer.(*bytes.Buffer).String()
	var doc map[string]any
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil || doc["changed"] != true || doc["id"] != float64(7) {
		t.Errorf("stdout = %q (%v), want forwarding 7 with \"changed\": true", stdout, err)
	}
	if got := n.Load(); got != 1 {
		t.Errorf("requests = %d, want 1", got)
	}
}
