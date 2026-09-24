package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	coreapigo "github.com/namedotcom/core-api-go"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/patramsey/namecom-cli/cmd/cmdutil"
	"github.com/patramsey/namecom-cli/internal/api"
	"github.com/patramsey/namecom-cli/internal/config"
	"github.com/patramsey/namecom-cli/internal/output"
	"github.com/spf13/cobra"
)

// statusServer answers the calls runStatus makes. balanceStatus lets a test
// make just the balance endpoint fail while everything else succeeds.
func statusServer(t *testing.T, balanceStatus int, balanceBody string) *httptest.Server {
	return statusServerWith(t, balanceStatus, balanceBody, http.StatusOK)
}

func statusServerWith(t *testing.T, balanceStatus int, balanceBody string, transferStatus int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "balance"):
			w.WriteHeader(balanceStatus)
			_, _ = w.Write([]byte(balanceBody))
		case strings.Contains(r.URL.Path, "transfers"):
			if transferStatus != http.StatusOK {
				w.WriteHeader(transferStatus)
				_, _ = w.Write([]byte(`{"message":"Permission Denied"}`))
				return
			}
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

// TestStatus_DistinguishesZeroTransfersFromUnavailable applies the same
// reasoning as the balance handling to the pre-existing transfers fetch, which
// swallowed its error and left PendingTransfers at 0.
//
// In table output that is merely invisible, but `status -o json` emitted
// "pending_transfers": 0 — a positive claim that no transfers are pending, made
// on the basis of a request that failed. A script gating on that value acts on
// a fact the CLI never established.
func TestStatus_DistinguishesZeroTransfersFromUnavailable(t *testing.T) {
	t.Run("genuinely none reports zero", func(t *testing.T) {
		srv := statusServerWith(t, http.StatusOK, `{"balance":10}`, http.StatusOK)
		cmd, buf := statusCmdFor(t, srv)
		cmdutil.Out(cmd).Format = output.FormatJSON

		if err := runStatus(cmd, nil); err != nil {
			t.Fatalf("runStatus: %v", err)
		}
		var got map[string]any
		if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
			t.Fatalf("output is not valid JSON: %v\n%s", err, buf.String())
		}
		v, present := got["pending_transfers"]
		if !present {
			t.Fatalf("a successful fetch of zero transfers must report 0, got: %s", buf.String())
		}
		if v != float64(0) {
			t.Errorf("expected 0 pending transfers, got %#v", v)
		}
	})

	t.Run("unavailable is not reported as zero", func(t *testing.T) {
		srv := statusServerWith(t, http.StatusOK, `{"balance":10}`, http.StatusForbidden)
		cmd, buf := statusCmdFor(t, srv)
		cmdutil.Out(cmd).Format = output.FormatJSON

		if err := runStatus(cmd, nil); err != nil {
			t.Fatalf("a transfers failure should not fail status: %v", err)
		}
		var got map[string]any
		if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
			t.Fatalf("output is not valid JSON: %v\n%s", err, buf.String())
		}
		if v, present := got["pending_transfers"]; present {
			t.Errorf("a failed transfers lookup must not claim a count, got %#v", v)
		}
	})
}

// TestClassifyExpiry_SeparatesExpiredFromExpiring pins that a domain already
// past its expiry date is counted as expired, not as "expiring within 7 days".
//
// status asks for domains expiring before now+30d with no lower bound, so
// every expired domain comes back too, and the classifier bucketed on
// `days < 7` — which a negative day count always satisfies. A sandbox domain
// that expired 793 days earlier was reported as "1 expiring within 7 days",
// a red alarm that could never clear.
func TestClassifyExpiry_SeparatesExpiredFromExpiring(t *testing.T) {
	now := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	at := func(d time.Duration) *time.Time { t := now.Add(d); return &t }
	day := 24 * time.Hour

	domains := []*coreapigo.DomainResponsePayload{
		{DomainName: "long-dead.com", ExpireDate: at(-793 * day)},
		{DomainName: "just-lapsed.com", ExpireDate: at(-2 * time.Hour)}, // under a day: days rounds to 0
		{DomainName: "this-week.com", ExpireDate: at(3 * day)},
		{DomainName: "this-month.com", ExpireDate: at(20 * day)},
		{DomainName: "no-date.com"},
	}
	got := classifyExpiry(domains, now)

	if got.expired != 2 {
		t.Errorf("expired = %d, want 2 (long-dead and just-lapsed)", got.expired)
	}
	if got.critical != 1 {
		t.Errorf("expiring within 7 days = %d, want 1 (this-week only; expired domains are not 'expiring')", got.critical)
	}
	if got.soon != 1 {
		t.Errorf("expiring within 30 days = %d, want 1", got.soon)
	}
	for _, it := range got.items {
		wantExpired := it.Domain == "long-dead.com" || it.Domain == "just-lapsed.com"
		if it.Expired != wantExpired {
			t.Errorf("%s: Expired = %v, want %v", it.Domain, it.Expired, wantExpired)
		}
	}
}

// TestStatus_RendersExpiredAsExpired pins the text: an expired domain reads
// "expired N days ago", not "(-793 days)" under an "Expiring soon" heading.
func TestStatus_RendersExpiredAsExpired(t *testing.T) {
	var buf bytes.Buffer
	out := &output.Config{Format: output.FormatTable, Color: output.ColorNever, Writer: &buf, EWriter: &bytes.Buffer{}}
	renderStatus(out, statusSummary{
		DomainsTotal: 2, Expired: 1, ExpiringCritical: 1,
		ExpiringDomains: []expiryItem{
			{Domain: "long-dead.com", Expires: "2024-07-22", Days: -793, Expired: true},
			{Domain: "this-week.com", Expires: "2026-09-27", Days: 3},
		},
	})
	got := buf.String()

	for _, want := range []string{"1 expired", "1 expiring within 7 days", "expired 793 days ago", "(3 days)"} {
		if !strings.Contains(got, want) {
			t.Errorf("status output missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "-793") {
		t.Errorf("status shows a negative day count instead of saying the domain expired:\n%s", got)
	}
}
