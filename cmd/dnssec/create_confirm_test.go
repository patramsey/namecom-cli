package dnssec

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/patramsey/namecom-cli/cmd/cmdutil"
	"github.com/patramsey/namecom-cli/internal/output"
)

// TestDNSSECCreate_Confirms pins #382: a DS record that matches none of the
// zone's keys stops the domain resolving, as wrong nameservers do, but
// `dnssec create` sent it without asking, where `domain set-ns` asks. It now
// asks, naming the record; without a terminal it needs --yes.
func TestDNSSECCreate_Confirms(t *testing.T) {
	for name, tc := range map[string]struct {
		interactive bool
		answer      bool
		wantPOST    bool
		wantErr     func(error) bool
	}{
		"no terminal": {false, false, false, func(err error) bool {
			_, ok := errors.AsType[*cmdutil.ConfirmationRequiredError](err)
			return ok
		}},
		"declined": {true, false, false, func(err error) bool { return errors.Is(err, cmdutil.ErrAborted) }},
		"accepted": {true, true, true, func(err error) bool { return err == nil }},
	} {
		t.Run(name, func(t *testing.T) {
			defer output.StubInteractive(tc.interactive)()
			var prompts []string
			if tc.interactive {
				defer cmdutil.StubConfirm(func(p string) bool { prompts = append(prompts, p); return tc.answer })()
			}
			var posted bool
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				posted = posted || r.Method == http.MethodPost
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"domainName":"example.com","keyTag":12345,"algorithm":8,"digestType":2}`))
			}))
			t.Cleanup(srv.Close)
			cmd := withDryRun(t, cmdForCreate(t, srv), false)
			if err := cmd.Root().PersistentFlags().Set("yes", "false"); err != nil {
				t.Fatal(err)
			}
			createAlgorithm, createDigest, createDigestType, createKeyTag = 8, sha256Digest, 2, 12345

			err := runCreate(cmd, []string{"example.com"})
			if !tc.wantErr(err) {
				t.Errorf("runCreate = %v", err)
			}
			if posted != tc.wantPOST {
				t.Errorf("POST sent: %v, want %v", posted, tc.wantPOST)
			}
			if tc.interactive && (len(prompts) != 1 || !strings.HasPrefix(prompts[0], "Add DS record with key tag 12345 (algorithm 8, digest type 2) to example.com?")) {
				t.Errorf("prompts = %q", prompts)
			}
		})
	}
}
