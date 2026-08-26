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
	t.Run("cancel", func(t *testing.T) {
		drifttest.AssertRequest(t, drifttest.Request{
			Method: "POST",
			Path:   "/core/v1/transfers/example.com:cancel",
		}, cmdForTransferGet, runCancel, []string{"example.com"}, transferStub)
	})

	t.Run("cancel-outbound", func(t *testing.T) {
		drifttest.AssertRequest(t, drifttest.Request{
			Method: "POST",
			Path:   "/core/v1/transfers/external/out/example.com:cancel",
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
