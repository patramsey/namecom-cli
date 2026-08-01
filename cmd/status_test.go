package cmd

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/patramsey/namecom-cli/cmd/cmdutil"
	"github.com/patramsey/namecom-cli/internal/api"
	"github.com/patramsey/namecom-cli/internal/config"
	"github.com/patramsey/namecom-cli/internal/output"
	"github.com/spf13/cobra"
)

// statusServer answers the calls runStatus makes. balanceStatus lets a test
// make just the balance endpoint fail while everything else succeeds.
func statusServer(t *testing.T, balanceStatus int, balanceBody string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "balance"):
			w.WriteHeader(balanceStatus)
			_, _ = w.Write([]byte(balanceBody))
		case strings.Contains(r.URL.Path, "transfers"):
			_, _ = w.Write([]byte(`{"transfers":[],"nextPage":0}`))
		default:
			_, _ = w.Write([]byte(`{"domains":[],"totalCount":3,"nextPage":0}`))
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func statusCmdFor(t *testing.T, srv *httptest.Server) (*cobra.Command, *bytes.Buffer) {
	t.Helper()
	client, err := api.New(api.Options{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("api.New: %v", err)
	}
	var buf bytes.Buffer
	out := &output.Config{Format: output.FormatTable, Color: output.ColorNever, Writer: &buf, EWriter: &bytes.Buffer{}}
	cmd := &cobra.Command{}
	ctx := context.WithValue(context.Background(), cmdutil.KeyOutput, out)
	ctx = context.WithValue(ctx, cmdutil.KeyClient, client)
	ctx = context.WithValue(ctx, cmdutil.KeyConfig, &config.File{Profiles: map[string]config.Profile{}})
	cmd.SetContext(ctx)
	return cmd, &buf
}

// TestStatus_ShowsAccountBalance covers the practical reason to want it: every
// purchasing operation (register, renew, transfer, privacy) can fail with a 402
// whose canonical detail is "Insufficient Funds", and there was no way to see
// the balance before running one.
func TestStatus_ShowsAccountBalance(t *testing.T) {
	srv := statusServer(t, http.StatusOK, `{"balance":142.50}`)
	cmd, buf := statusCmdFor(t, srv)

	if err := runStatus(cmd, nil); err != nil {
		t.Fatalf("runStatus: %v", err)
	}
	if !strings.Contains(buf.String(), "142.50") {
		t.Errorf("status should show the account balance, got:\n%s", buf.String())
	}
}

// TestStatus_OmitsBalanceWhenUnavailable is the important half. A failed
// balance lookup must not render as $0.00 — that reads as "your account is
// empty", which is worse than saying nothing at all. Showing a wrong balance is
// more harmful than showing none.
//
// This is the same shape as the pre-existing transfers handling, which swallows
// errors and leaves PendingTransfers at 0 — indistinguishable from genuinely
// having none.
func TestStatus_OmitsBalanceWhenUnavailable(t *testing.T) {
	srv := statusServer(t, http.StatusForbidden, `{"message":"Permission Denied"}`)
	cmd, buf := statusCmdFor(t, srv)

	// A balance failure must not fail the whole status view.
	if err := runStatus(cmd, nil); err != nil {
		t.Fatalf("a balance failure should not fail status: %v", err)
	}
	got := buf.String()
	if strings.Contains(got, "$0.00") {
		t.Errorf("an unavailable balance must not render as $0.00:\n%s", got)
	}
	if strings.Contains(strings.ToLower(got), "balance") &&
		!strings.Contains(strings.ToLower(got), "unavailable") {
		t.Errorf("if balance is mentioned at all it must be marked unavailable:\n%s", got)
	}
}
