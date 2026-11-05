package contact

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/patramsey/namecom-cli/cmd/cmdutil"
	"github.com/patramsey/namecom-cli/internal/output"
)

// TestUnverified_NarrowKeepsDeadline pins #364: the help says "always read
// the DEADLINE column", and a narrow terminal hid it.
func TestUnverified_NarrowKeepsDeadline(t *testing.T) {
	const resp = `{"unverifiedContacts":[
	  {"verificationId": 9911, "email": "a-rather-long-registrant-address@example.com",
	   "verifyBy": "2026-08-20T00:00:00Z", "domains": ["namecom-smoke-37de95c1a2b4.com","example.net"]}
	],"totalCount":1}`
	for _, width := range []int{60, 70} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(resp))
		}))
		cmd, stdout, _ := contactCmd(t, srv, output.FormatTable)
		cmdutil.Out(cmd).MaxWidth = width
		if err := runUnverified(cmd, nil); err != nil {
			t.Fatalf("runUnverified: %v", err)
		}
		srv.Close()
		for _, want := range []string{"DEADLINE", "2026-08-20", "9911"} {
			if !strings.Contains(stdout.String(), want) {
				t.Errorf("width %d: %q missing:\n%s", width, want, stdout.String())
			}
		}
	}
}
