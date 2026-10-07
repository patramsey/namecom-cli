package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	coreapigo "github.com/namedotcom/core-api-go"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/patramsey/namecom-cli/cmd/cmdutil"
	"github.com/patramsey/namecom-cli/internal/api"
	"github.com/patramsey/namecom-cli/internal/config"
	"github.com/patramsey/namecom-cli/internal/output"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
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

// TestStatus_YAMLKeysMatchJSON pins #111 for the one struct this package
// defines itself. Its json tags were snake_case then, so YAML encoded from the Go
// value said `domainstotal` where JSON said `domains_total`, and the omitted
// balance came out as `balance: null`.
func TestStatus_YAMLKeysMatchJSON(t *testing.T) {
	srv := statusServer(t, http.StatusForbidden, `{"message":"Permission Denied"}`)
	keys := map[output.Format][]string{}
	for _, f := range []output.Format{output.FormatJSON, output.FormatYAML} {
		cmd, buf := statusCmdFor(t, srv)
		cmdutil.Out(cmd).Format = f
		if err := runStatus(cmd, nil); err != nil {
			t.Fatalf("runStatus(%s): %v", f, err)
		}
		var doc yaml.Node // JSON is YAML, so one parser reads both
		if err := yaml.Unmarshal(buf.Bytes(), &doc); err != nil {
			t.Fatalf("parsing %s output: %v\n%s", f, err, buf.String())
		}
		m := doc.Content[0]
		for i := 0; i < len(m.Content); i += 2 {
			keys[f] = append(keys[f], m.Content[i].Value)
		}
	}
	if !slices.Equal(keys[output.FormatJSON], keys[output.FormatYAML]) {
		t.Errorf("YAML keys differ from JSON keys\njson: %v\nyaml: %v", keys[output.FormatJSON], keys[output.FormatYAML])
	}
	if !slices.Contains(keys[output.FormatYAML], "domainsTotal") || slices.Contains(keys[output.FormatYAML], "balance") {
		t.Errorf("YAML should use json tag names and omit the unavailable balance, got %v", keys[output.FormatYAML])
	}
}

// TestStatus_DistinguishesZeroTransfersFromUnavailable applies the same
// reasoning as the balance handling to the pre-existing transfers fetch, which
// swallowed its error and left PendingTransfers at 0.
//
// In table output that is merely invisible, but `status -o json` emitted
// "pendingTransfers": 0 — a positive claim that no transfers are pending, made
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
		v, present := got["pendingTransfers"]
		if !present {
			t.Fatalf("a successful fetch of zero transfers must report 0, got: %s", buf.String())
		}
		if v != float64(0) {
			t.Errorf("expected 0 pending transfers, got %#v", v)
		}
		// Empty lists are [], not left out: `--jq '.pendingTransferDomains[]'`
		// failed with "cannot iterate over: null" on an account with none.
		for _, k := range []string{"expiringDomains", "pendingTransferDomains"} {
			if l, ok := got[k].([]any); !ok || len(l) != 0 {
				t.Errorf("%s = %#v, want []", k, got[k])
			}
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
		if v, present := got["pendingTransfers"]; present {
			t.Errorf("a failed transfers lookup must not claim a count, got %#v", v)
		}
		// Nor an empty list: null says the domains are unknown.
		if v, present := got["pendingTransferDomains"]; !present || v != nil {
			t.Errorf("pendingTransferDomains = %#v (present %v), want null", v, present)
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
// "expired 2 years ago" — the units `domain list` uses for the same date
// (#238) — not "(-793 days)" under an "Expiring soon" heading.
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

	for _, want := range []string{"1 expired", "1 expiring within 7 days", "(expired 2 years ago)", "(in 3 days)"} {
		if !strings.Contains(got, want) {
			t.Errorf("status output missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "-793") {
		t.Errorf("status shows a negative day count instead of saying the domain expired:\n%s", got)
	}
}

// TestStatus_RenewHintNamesWhatIsListed pins #293: the footer said "renew
// expiring domains" under a list of domains that had already expired.
func TestStatus_RenewHintNamesWhatIsListed(t *testing.T) {
	for _, tc := range []struct {
		name string
		s    statusSummary
		want string
	}{
		{"expired only", statusSummary{DomainsTotal: 1, Expired: 1}, "to renew expired domains"},
		{"expiring only", statusSummary{DomainsTotal: 1, ExpiringSoon: 1}, "to renew expiring domains"},
		{"both", statusSummary{DomainsTotal: 2, Expired: 1, ExpiringCritical: 1}, "to renew expired and expiring domains"},
		{"neither", statusSummary{DomainsTotal: 1}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stderr bytes.Buffer
			out := &output.Config{Format: output.FormatTable, Color: output.ColorNever, Writer: &bytes.Buffer{}, EWriter: &stderr}
			renderStatus(out, tc.s)
			got := ""
			for _, l := range strings.Split(stderr.String(), "\n") {
				if strings.Contains(l, "domain renew") {
					got = l
				}
			}
			if tc.want == "" && got != "" || tc.want != "" && !strings.HasSuffix(got, tc.want) {
				t.Errorf("renew hint = %q, want one ending %q", got, tc.want)
			}
		})
	}
}

// TestStatus_SummaryLine pins #211: a domain due within 7 days hid the count
// due in 7–30 days from the summary, and counts other than 1 read as
// "2 transfer pending".
func TestStatus_SummaryLine(t *testing.T) {
	for _, tc := range []struct {
		name string
		s    statusSummary
		want string
	}{
		{"both expiry counts", statusSummary{DomainsTotal: 5, ExpiringCritical: 1, ExpiringSoon: 2},
			"5 domains  1 expiring within 7 days  2 more within 30 days"},
		{"only the 30-day count", statusSummary{DomainsTotal: 5, ExpiringSoon: 2},
			"5 domains  2 expiring within 30 days"},
		{"singular", statusSummary{DomainsTotal: 1, PendingTransfers: ptrInt(1)},
			"1 domain  1 transfer pending"},
		{"plural transfers", statusSummary{DomainsTotal: 3, PendingTransfers: ptrInt(2)},
			"3 domains  2 transfers pending"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			out := &output.Config{Format: output.FormatTable, Color: output.ColorNever, Writer: &buf, EWriter: &bytes.Buffer{}}
			renderStatus(out, tc.s)
			// The summary is the line after the profile header.
			_, rest, _ := strings.Cut(buf.String(), "\n")
			if got, _, _ := strings.Cut(rest, "\n"); got != tc.want {
				t.Errorf("summary line = %q, want %q\n%s", got, tc.want, buf.String())
			}
		})
	}
}

// TestStatus_NullBodyIsAnError pins #157: a 200 whose body is `null` made the
// SDK return a nil response with a nil error, and status dereferenced it inside
// an errgroup goroutine — a panic that bypassed Execute and took the process
// down. It must be an ordinary error that exits 1.
func TestStatus_NullBodyIsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("null"))
	}))
	t.Cleanup(srv.Close)
	cmd, _ := statusCmdFor(t, srv)

	err := runStatus(cmd, nil)
	if err == nil || !strings.Contains(err.Error(), "unexpected response from the API") {
		t.Fatalf("runStatus = %v, want an unexpected-response error", err)
	}
	if got := exitCode(err); got != 1 {
		t.Errorf("exit code = %d, want 1", got)
	}
}

// TestStatus_NullListElementsAreSkipped: a null element in the expiring-domain
// or transfer list crashed classifyExpiry and the pending-transfer count
// (#157). Each is skipped.
func TestStatus_NullListElementsAreSkipped(t *testing.T) {
	soon := time.Now().AddDate(0, 0, 10).UTC().Format(time.RFC3339)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "balance"):
			_, _ = w.Write([]byte(`{"balance":1}`))
		case strings.Contains(r.URL.Path, "transfers"):
			_, _ = w.Write([]byte(`{"transfers":[null,{"domainName":"moving.com","status":"pending"}]}`))
		default:
			_, _ = w.Write([]byte(`{"domains":[null,{"domainName":"soon.com","expireDate":"` + soon + `"}],"totalCount":1}`))
		}
	}))
	t.Cleanup(srv.Close)
	cmd, buf := statusCmdFor(t, srv)

	if err := runStatus(cmd, nil); err != nil {
		t.Fatalf("runStatus: %v", err)
	}
	for _, want := range []string{"soon.com", "moving.com", "1 transfer pending"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("status output missing %q:\n%s", want, buf.String())
		}
	}
}
