package dns

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/patramsey/namecom-cli/cmd/cmdutil"
)

// TestDNSDelete_ShowsTheRecord pins #235: `dns delete D 12345` asked "Delete
// DNS record 12345 from D?" — an ID and nothing else — even for a record that
// did not exist. It now fetches the record first: the prompt shows it, and a
// missing one fails with not-found before any prompt or DELETE.
func TestDNSDelete_ShowsTheRecord(t *testing.T) {
	for _, tc := range []struct {
		name, record string
		status       int
		wantPrompt   string
	}{
		{"A record", `{"id":12345,"host":"www","type":"A","answer":"1.2.3.4","ttl":300}`, http.StatusOK,
			"Delete A www → 1.2.3.4 (TTL 300) from example.com?"},
		{"MX at the apex", `{"id":12345,"type":"MX","answer":"mail.example.com","ttl":3600,"priority":10}`, http.StatusOK,
			"Delete MX @ → mail.example.com (priority 10, TTL 3600) from example.com?"},
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
				_, _ = w.Write([]byte(tc.record))
			}))
			t.Cleanup(srv.Close)

			err := runDelete(cmdForDelete(t, srv), []string{"example.com", "12345"})
			if tc.wantPrompt == "" {
				if !cmdutil.IsNotFound(err) || !strings.Contains(err.Error(), "record 12345 not found on example.com") {
					t.Errorf("runDelete = %v, want not-found naming the record", err)
				}
				if len(prompts) != 0 {
					t.Errorf("prompted %q for a record that does not exist", prompts)
				}
				return
			}
			if len(prompts) != 1 || prompts[0] != tc.wantPrompt {
				t.Errorf("prompts = %q\nwant      [%q]", prompts, tc.wantPrompt)
			}
		})
	}
}
