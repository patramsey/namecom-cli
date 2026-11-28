package email

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/patramsey/namecom-cli/cmd/cmdutil"
	"github.com/patramsey/namecom-cli/internal/output"
)

// TestEmailDelete_IfExists pins #382: `email delete` had no --if-exists,
// unlike `dns delete` and `url delete`, so a retry of one that worked exited
// 4. With it, a mailbox with no forwarding is "changed": false, read from
// one list of the domain's forwardings, which also shows the domain is
// there: a domain not in the account still fails.
func TestEmailDelete_IfExists(t *testing.T) {
	const list = "GET /core/v1/domains/example.com/email/forwarding"
	const del = "DELETE /core/v1/domains/example.com/email/forwarding/info"
	present := `{"emailForwarding":[{"domainName":"example.com","emailBox":"info","emailTo":"you@example.org"}],"lastPage":1}`

	for name, tc := range map[string]struct {
		routes      map[string]string // request → body; a missing one is a 404
		wantSent    []string
		wantChanged bool
		wantErr     string
	}{
		"there":       {map[string]string{list: present, del: `{}`}, []string{list, del}, true, ""},
		"gone":        {map[string]string{list: `{"emailForwarding":[],"lastPage":1}`}, []string{list}, false, ""},
		"gone since":  {map[string]string{list: present}, []string{list, del}, false, ""},
		"domain gone": {map[string]string{}, []string{list}, false, "example.com"},
	} {
		t.Run(name, func(t *testing.T) {
			deleteIfExists = true
			t.Cleanup(func() { deleteIfExists = false })
			var sent []string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				key := r.Method + " " + r.URL.Path
				sent = append(sent, key)
				w.Header().Set("Content-Type", "application/json")
				body, ok := tc.routes[key]
				if !ok {
					w.WriteHeader(http.StatusNotFound)
					body = `{"message":"Not Found"}`
				}
				_, _ = w.Write([]byte(body))
			}))
			t.Cleanup(srv.Close)
			cmd := cmdForEmailDelete(t, srv)
			out := cmdutil.Out(cmd)
			out.Format = output.FormatJSON

			err := runDelete(cmd, []string{"example.com", "info"})
			if strings.Join(sent, ", ") != strings.Join(tc.wantSent, ", ") {
				t.Errorf("sent %q, want %q", sent, tc.wantSent)
			}
			if tc.wantErr != "" {
				if !cmdutil.IsNotFound(err) || !strings.Contains(err.Error(), tc.wantErr) {
					t.Errorf("runDelete = %v, want the domain not found", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("runDelete: %v", err)
			}
			var doc struct {
				Changed bool `json:"changed"`
			}
			if err := json.Unmarshal(out.Writer.(*bytes.Buffer).Bytes(), &doc); err != nil {
				t.Fatalf("stdout: %v\n%s", err, out.Writer)
			}
			if doc.Changed != tc.wantChanged {
				t.Errorf("changed = %v, want %v\n%s", doc.Changed, tc.wantChanged, out.Writer)
			}
		})
	}
}
