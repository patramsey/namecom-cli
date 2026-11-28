package dnssec

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/patramsey/namecom-cli/cmd/cmdutil"
	"github.com/patramsey/namecom-cli/internal/output"
)

const dsWarning = "validating resolvers will fail to resolve it"

// TestDNSSECDelete_WarnsWhatItCanBreak pins #235: removing a DS record gave
// no warning, though leaving DS records that match none of the zone's signing
// keys makes validating resolvers fail the domain. The warning goes to stderr
// in table mode, before the question, and stays out of structured output.
func TestDNSSECDelete_WarnsWhatItCanBreak(t *testing.T) {
	defer output.StubInteractive(true)()
	for _, format := range []output.Format{output.FormatTable, output.FormatJSON} {
		t.Run(string(format), func(t *testing.T) {
			var warnedFirst bool
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{}`))
			}))
			t.Cleanup(srv.Close)
			cmd := cmdWithYes(t, srv)
			if err := cmd.PersistentFlags().Set("yes", "false"); err != nil {
				t.Fatal(err)
			}
			out := cmdutil.Out(cmd)
			out.Format = format
			stderr := out.EWriter.(*bytes.Buffer)
			defer cmdutil.StubConfirm(func(string) bool { warnedFirst = stderr.Len() > 0; return false })()

			_ = runDelete(cmd, []string{"example.com", "abc123"})
			got := stderr.String()
			if format == output.FormatJSON {
				if strings.Contains(got, "DS record") {
					t.Errorf("structured output got the table-mode warning:\n%s", got)
				}
				return
			}
			if !strings.Contains(got, dsWarning) || !strings.Contains(got, "example.com") {
				t.Errorf("stderr lacks the warning:\n%s", got)
			}
			if !warnedFirst {
				t.Error("the warning must be shown before the question")
			}
		})
	}
}

// TestDNSSECDelete_MissingDigest pins #382: a delete of a digest the domain
// does not have said only the API's "Not Found", where get and --dry-run
// name the record, and printed the what-remains warning before failing.
// With no question to ask, the warning now follows a delete that worked.
func TestDNSSECDelete_MissingDigest(t *testing.T) {
	for name, tc := range map[string]struct {
		status   int
		wantErr  string
		wantWarn bool
	}{
		"missing": {http.StatusNotFound, "DS record abc123 not found on example.com", false},
		"deleted": {http.StatusOK, "", true},
	} {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodDelete {
					t.Errorf("sent %s, want only the DELETE", r.Method)
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(`{"message":"Not Found"}`))
			}))
			t.Cleanup(srv.Close)
			cmd := cmdWithYes(t, srv)
			err := runDelete(cmd, []string{"example.com", "abc123"})
			if tc.wantErr == "" && err != nil {
				t.Fatalf("runDelete = %v", err)
			}
			if tc.wantErr != "" && (!cmdutil.IsNotFound(err) || !strings.Contains(err.Error(), tc.wantErr)) {
				t.Errorf("runDelete = %v, want not_found %q", err, tc.wantErr)
			}
			stderr := cmdutil.Out(cmd).EWriter.(*bytes.Buffer).String()
			if got := strings.Contains(stderr, dsWarning); got != tc.wantWarn {
				t.Errorf("warning shown: %v, want %v\n%s", got, tc.wantWarn, stderr)
			}
			stdout := cmdutil.Out(cmd).Writer.(*bytes.Buffer).String()
			if got := strings.Contains(stdout, "Removed DS record"); got != tc.wantWarn {
				t.Errorf("stdout = %q, want the success line: %v", stdout, tc.wantWarn)
			}
		})
	}
}
