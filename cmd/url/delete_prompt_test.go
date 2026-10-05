package url

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/patramsey/namecom-cli/cmd/cmdutil"
)

// TestURLDelete_ShowsTheForwarding pins #235: `url delete D 7` asked "Delete
// URL forwarding 7 from D?" even for a forwarding that did not exist. It now
// fetches it first: the prompt shows where it forwards, and a missing one
// fails with not-found before any prompt or DELETE.
func TestURLDelete_ShowsTheForwarding(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
		wantPrompt string
	}{
		{"present", `{"id":7,"host":"go.example.com","forwardsTo":"https://acme.io","type":"redirect"}`, http.StatusOK,
			"Delete URL forwarding go.example.com → https://acme.io (redirect) from example.com?"},
		{"missing", `{"message":"Not Found"}`, http.StatusNotFound, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var prompts []string
			defer cmdutil.StubConfirm(func(p string) bool { prompts = append(prompts, p); return false })()
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					t.Errorf("sent %s %s without a yes", r.Method, r.URL)
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			t.Cleanup(srv.Close)

			err := runDelete(cmdForURLDelete(t, srv), []string{"example.com", "7"})
			if tc.wantPrompt == "" {
				if !cmdutil.IsNotFound(err) || !strings.Contains(err.Error(), "URL forwarding 7 not found on example.com") {
					t.Errorf("runDelete = %v, want not-found naming the forwarding", err)
				}
				if len(prompts) != 0 {
					t.Errorf("prompted %q for a forwarding that does not exist", prompts)
				}
				return
			}
			if len(prompts) != 1 || prompts[0] != tc.wantPrompt {
				t.Errorf("prompts = %q\nwant      [%q]", prompts, tc.wantPrompt)
			}
		})
	}
}
