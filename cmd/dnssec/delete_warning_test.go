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

// TestDNSSECDelete_WarnsWhatItCanBreak pins #235: removing a DS record gave
// no warning, though leaving DS records that match none of the zone's signing
// keys makes validating resolvers fail the domain. The warning goes to stderr
// in table mode, before the question, and stays out of structured output.
func TestDNSSECDelete_WarnsWhatItCanBreak(t *testing.T) {
	for _, format := range []output.Format{output.FormatTable, output.FormatJSON} {
		t.Run(string(format), func(t *testing.T) {
			var warnedFirst bool
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{}`))
			}))
			t.Cleanup(srv.Close)
			cmd := cmdWithYes(t, srv)
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
			if !strings.Contains(got, "validating resolvers will fail to resolve it") || !strings.Contains(got, "example.com") {
				t.Errorf("stderr lacks the warning:\n%s", got)
			}
			if !warnedFirst {
				t.Error("the warning must be shown before the question")
			}
		})
	}
}
