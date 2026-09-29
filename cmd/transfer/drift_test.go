package transfer

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/patramsey/namecom-cli/cmd/cmdutil"
	"github.com/patramsey/namecom-cli/internal/drifttest"
	"github.com/patramsey/namecom-cli/internal/output"
)

const transferStub = `{"domainName":"example.com","status":"pending"}`

// TestDryRunNeverPrintsAuthCode is the reason `transfer` previews a redacted
// copy of the body rather than the body.
//
// The auth code is the secret that authorises moving a domain between
// registrars. --dry-run output goes to a terminal, into scrollback, and into CI
// logs, so printing it there would put a transfer credential somewhere it is
// never cleaned up. The previous code avoided this by printing no body at all;
// this asserts the narrower property directly, so the body can be shown.
func TestDryRunNeverPrintsAuthCode(t *testing.T) {
	const secret = "SUPERSECRET-AUTH-9911"

	cases := []struct {
		name  string
		build drifttest.Build
		run   drifttest.Run
		args  []string
	}{
		{
			name: "create",
			build: func(t *testing.T, srv *httptest.Server) *cobra.Command {
				cmd := cmdForTransferCreate(t, srv)
				if err := cmd.ParseFlags([]string{"--auth-code", secret, "--price", "9.99"}); err != nil {
					t.Fatalf("ParseFlags: %v", err)
				}
				return cmd
			},
			run:  runCreate,
			args: []string{"example.com"},
		},
		{
			name: "internal-in",
			build: func(t *testing.T, srv *httptest.Server) *cobra.Command {
				cmd := cmdForInternalIn(t, srv)
				if err := cmd.ParseFlags([]string{"--auth-code", secret}); err != nil {
					t.Fatalf("ParseFlags: %v", err)
				}
				return cmd
			},
			run:  runInternalIn,
			args: []string{"example.com"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(failIfWritten(t))
			t.Cleanup(srv.Close)

			cmd := drifttest.WithDryRun(t, tc.build(t, srv), true)
			if err := tc.run(cmd, tc.args); err != nil {
				t.Fatalf("dry-run invocation failed: %v", err)
			}
			buf, ok := cmdutil.Out(cmd).Writer.(*bytes.Buffer)
			if !ok {
				t.Fatal("output writer is not a *bytes.Buffer")
			}
			out := buf.String()

			if strings.Contains(out, secret) {
				t.Errorf("--dry-run printed the transfer auth code:\n%s", out)
			}
			if !strings.Contains(out, "[redacted]") {
				t.Errorf("expected the auth code to be shown as [redacted]; got:\n%s", out)
			}
			// The redaction is only worth having if the rest of the body is
			// actually previewed — otherwise this passes trivially against the
			// old nil-body behaviour it replaced.
			if !strings.Contains(out, "example.com") {
				t.Errorf("expected the previewed body to name the domain; got:\n%s", out)
			}
		})
	}
}

// TestDryRunDoesNotPrompt pins that --dry-run works without --yes when stdin is
// not a TTY, as in CI. Both commands used to confirm before checking
// --dry-run, so they failed with "pass --yes to confirm in non-interactive
// mode" — or, in a terminal, asked the user to approve a transfer that would
// not be sent. drifttest.WithDryRun used to set --yes on the dry-run half too,
// which is why the tests above did not notice.
func TestDryRunDoesNotPrompt(t *testing.T) {
	for _, tc := range []struct {
		name  string
		build func(*testing.T, *httptest.Server) *cobra.Command
		run   drifttest.Run
	}{
		{"create", cmdForTransferCreate, runCreate},
		{"internal-in", cmdForInternalIn, runInternalIn},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(failIfWritten(t))
			t.Cleanup(srv.Close)

			cmd := tc.build(t, srv)
			if err := cmd.ParseFlags([]string{"--auth-code", "AUTH123"}); err != nil {
				t.Fatalf("ParseFlags: %v", err)
			}
			cmd = drifttest.WithDryRun(t, cmd, true)
			if cmdutil.IsYes(cmd) {
				t.Fatal("test setup: --yes must be unset for this to prove anything")
			}
			if err := tc.run(cmd, []string{"example.com"}); err != nil {
				t.Fatalf("--dry-run without --yes should not prompt or fail: %v", err)
			}
		})
	}
}

// TestDryRunPreviewsPrice pins the payload half that motivated showing the body
// at all: `transfer create --price` spends money, and the preview used to name
// only the method and path.
func TestDryRunPreviewsPrice(t *testing.T) {
	srv := httptest.NewServer(failIfWritten(t))
	t.Cleanup(srv.Close)

	cmd := cmdForTransferCreate(t, srv)
	if err := cmd.ParseFlags([]string{"--auth-code", "AUTH123", "--price", "42.5"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	cmd = drifttest.WithDryRun(t, cmd, true)
	if err := runCreate(cmd, []string{"example.com"}); err != nil {
		t.Fatalf("dry-run invocation failed: %v", err)
	}
	out := cmdutil.Out(cmd).Writer.(*bytes.Buffer).String()
	if !strings.Contains(out, "42.5") {
		t.Errorf("--dry-run did not preview the purchase price it is about to spend:\n%s", out)
	}
}

// TestTransferCreate_PromptQuotesThePriceSent guards the transfer side of
// #83: with --price, the prompt quoted the standard transfer price while the
// body carried the override. Without --yes in a non-interactive test,
// Confirm's error carries the prompt.
func TestTransferCreate_PromptQuotesThePriceSent(t *testing.T) {
	defer output.StubInteractive(false)()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if !strings.Contains(r.URL.Path, "getPricing") {
			t.Errorf("transferred without confirmation: %s %s", r.Method, r.URL)
		}
		_, _ = w.Write([]byte(`{"transferPrice":12.99}`))
	}))
	t.Cleanup(srv.Close)

	cmd := cmdForTransferCreate(t, srv)
	if err := cmd.ParseFlags([]string{"--auth-code", "AUTH123", "--price", "42.5"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	t.Cleanup(func() { createPrice = 0 })
	err := runCreate(cmd, []string{"example.com"})
	if err == nil {
		t.Fatal("expected the non-interactive confirm error")
	}
	if !strings.Contains(err.Error(), "$42.50") {
		t.Errorf("prompt does not quote the price sent ($42.50):\n%v", err)
	}
	if strings.Contains(err.Error(), "12.99") {
		t.Errorf("prompt quotes the standard transfer price, which is not sent:\n%v", err)
	}
}

// TestRequestShape_TransferCancels pins the two cancel operations, which are
// POSTs the API accepts with no request body at all.
//
// Added before the SDK port deliberately. The equivalent contact endpoints
// turned out to be modelled with a body the SDK marshals regardless, so leaving
// it unset sent a literal `null` — see
// docs/upstream/core-api-go-forced-request-bodies.md. These are the same shape
// of endpoint, so whatever the port does to them should be visible rather than
// discovered from a 400 later.
func TestRequestShape_TransferCancels(t *testing.T) {
	// Both now send {} where they previously sent no body — the same forced-body
	// limitation as the contact endpoints, and the reason this test was written
	// before the port rather than after. See
	// docs/upstream/core-api-go-forced-request-bodies.md.
	t.Run("cancel", func(t *testing.T) {
		drifttest.AssertRequest(t, drifttest.Request{
			Method: "POST",
			Path:   "/core/v1/transfers/example.com:cancel",
			Body:   `{}`,
		}, cmdForTransferGet, runCancel, []string{"example.com"}, transferStub)
	})

	t.Run("cancel-outbound", func(t *testing.T) {
		drifttest.AssertRequest(t, drifttest.Request{
			Method: "POST",
			Path:   "/core/v1/transfers/external/out/example.com:cancel",
			Body:   `{}`,
		}, cmdForTransferGet, runCancelOutbound, []string{"example.com"}, transferStub)
	})
}

// TestRequestShape_Transfer pins the wire request for the two transfer writes
// that carry a body. The auth code is asserted present here — redaction is a
// property of the preview, not of what is sent.
func TestRequestShape_Transfer(t *testing.T) {
	t.Run("create", func(t *testing.T) {
		build := func(t *testing.T, srv *httptest.Server) *cobra.Command {
			cmd := cmdForTransferCreate(t, srv)
			if err := cmd.ParseFlags([]string{"--auth-code", "AUTH123"}); err != nil {
				t.Fatalf("ParseFlags: %v", err)
			}
			return cmd
		}
		drifttest.AssertRequest(t, drifttest.Request{
			Method: "POST",
			Path:   "/core/v1/transfers",
			Body:   `{"domainName":"example.com","authCode":"AUTH123"}`,
		}, build, runCreate, []string{"example.com"}, transferStub)
	})

	t.Run("internal-in", func(t *testing.T) {
		build := func(t *testing.T, srv *httptest.Server) *cobra.Command {
			cmd := cmdForInternalIn(t, srv)
			if err := cmd.ParseFlags([]string{"--auth-code", "AUTH123"}); err != nil {
				t.Fatalf("ParseFlags: %v", err)
			}
			return cmd
		}
		drifttest.AssertRequest(t, drifttest.Request{
			Method: "POST",
			Path:   "/core/v1/transfers/internal/in",
			Body:   `{"domainName":"example.com","authCode":"AUTH123"}`,
		}, build, runInternalIn, []string{"example.com"}, transferStub)
	})
}

// failIfWritten is the stub handler for dry-run tests. Reads are expected —
// `transfer create --dry-run` fetches pricing so the confirm line can quote a
// cost — so only a write means the flag was ignored.
func failIfWritten(t *testing.T) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			t.Errorf("--dry-run performed a write: %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(transferStub))
	}
}

// TestDryRunMatchesRealRequest_TransferBody asserts the body --dry-run prints
// is the body sent, for every transfer write. The two creates redact the auth
// code and nothing else; the cancels preview no body and send the SDK's {}
// placeholder.
func TestDryRunMatchesRealRequest_TransferBody(t *testing.T) {
	redacted := map[string]string{"authCode": "[redacted]"}

	t.Run("create", func(t *testing.T) {
		build := func(t *testing.T, srv *httptest.Server) *cobra.Command {
			cmd := cmdForTransferCreate(t, srv)
			if err := cmd.ParseFlags([]string{"--auth-code", "AUTH123", "--price", "42.5", "--privacy"}); err != nil {
				t.Fatalf("ParseFlags: %v", err)
			}
			t.Cleanup(func() { createAuthCode, createPrice, createPrivacy = "", 0, false })
			return cmd
		}
		drifttest.AssertDryRunBodyMatchesRedacted(t, build, runCreate, []string{"example.com"}, transferStub, redacted)
	})

	t.Run("internal-in", func(t *testing.T) {
		build := func(t *testing.T, srv *httptest.Server) *cobra.Command {
			cmd := cmdForInternalIn(t, srv)
			if err := cmd.ParseFlags([]string{"--auth-code", "ABC123"}); err != nil {
				t.Fatalf("ParseFlags: %v", err)
			}
			return cmd
		}
		drifttest.AssertDryRunBodyMatchesRedacted(t, build, runInternalIn, []string{"example.com"}, transferStub, redacted)
	})

	t.Run("cancel", func(t *testing.T) {
		drifttest.AssertDryRunBodyMatches(t, cmdForTransferGet, runCancel, []string{"example.com"}, transferStub)
	})

	t.Run("cancel-outbound", func(t *testing.T) {
		drifttest.AssertDryRunBodyMatches(t, cmdForTransferGet, runCancelOutbound, []string{"example.com"}, transferStub)
	})
}
