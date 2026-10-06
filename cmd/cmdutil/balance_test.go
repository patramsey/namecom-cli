package cmdutil

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/patramsey/namecom-cli/internal/api"
	"github.com/patramsey/namecom-cli/internal/output"
	"github.com/spf13/cobra"
)

// balanceServer answers the balance lookup with status and body, and counts
// the lookups.
func balanceServer(t *testing.T, status int, body string, calls *atomic.Int32) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/core/v1/accountinfo/balance" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
		}
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// withClient adds an API client for srv to cmd's context.
func withClient(t *testing.T, cmd *cobra.Command, srv *httptest.Server) {
	t.Helper()
	client, err := api.New(api.Options{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("api.New: %v", err)
	}
	cmd.SetContext(context.WithValue(cmd.Context(), KeyClient, client))
}

// purchase runs a $39.98 purchase through RunWrite with a balance lookup
// against srv, answering its prompt yes, and returns the line under the
// prompt.
func purchase(t *testing.T, cmd *cobra.Command) (detail string) {
	t.Helper()
	prev := confirmFunc
	confirmFunc = func(_ *output.Config, _ bool, _, d string) (bool, error) {
		detail = d
		return true, nil
	}
	t.Cleanup(func() { confirmFunc = prev })

	sent, err := RunWrite(cmd, Write[testBody]{
		Method: "POST", Path: "/core/v1/domains",
		Body:    testBody{Name: "acme.io"},
		Prompt:  "Register acme.io for $39.98?",
		Quote:   &output.Quote{Total: 39.98, Currency: "USD", Years: 2},
		Balance: StartBalance(cmd),
	}, func(context.Context, testBody) error { return nil })
	if err != nil || !sent {
		t.Fatalf("RunWrite = (%v, %v), want (true, nil)", sent, err)
	}
	return detail
}

// TestPurchase_ShowsBalance pins #271: a purchase prompt's context line ends
// with the account balance, and a balance below the total is warned about
// before the prompt, without refusing.
func TestPurchase_ShowsBalance(t *testing.T) {
	defer output.StubInteractive(true)()
	for _, tc := range []struct {
		name, body string
		warn       bool
	}{
		{"covers", `{"balance":120}`, false},
		{"short", `{"balance":12.5}`, true},
		// The same in cents is enough: comparing floats directly would call
		// 39.98 short of 39.98 parsed another way.
		{"exact", `{"balance":39.98}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			cmd, _, stderr := writeCmd(t, false, false)
			withClient(t, cmd, balanceServer(t, http.StatusOK, tc.body, &calls))

			detail := purchase(t, cmd)
			if !strings.Contains(detail, " · balance $") {
				t.Errorf("prompt context = %q, want it to end with the balance", detail)
			}
			if got := strings.Contains(stderr.String(), "is less than this purchase's $39.98"); got != tc.warn {
				t.Errorf("warned = %v, want %v; stderr:\n%s", got, tc.warn, stderr)
			}
		})
	}
}

// TestPurchase_BalanceLookupFailureIsSilent: a failed lookup must never
// block the purchase or say anything; the line is what it was before #271.
func TestPurchase_BalanceLookupFailureIsSilent(t *testing.T) {
	defer output.StubInteractive(true)()
	var calls atomic.Int32
	cmd, _, stderr := writeCmd(t, false, false)
	withClient(t, cmd, balanceServer(t, http.StatusForbidden, `{"message":"Permission Denied"}`, &calls))

	if detail := purchase(t, cmd); strings.Contains(detail, "balance") {
		t.Errorf("prompt context = %q, want no balance", detail)
	}
	if calls.Load() == 0 {
		t.Error("the balance was never looked up, so the failure path was not exercised")
	}
	if stderr.Len() != 0 {
		t.Errorf("a failed balance lookup printed:\n%s", stderr)
	}
}

// TestStartBalance_NoRequestWithoutAPrompt: --yes, or no terminal, shows no
// prompt, so the balance would be fetched for nothing.
func TestStartBalance_NoRequestWithoutAPrompt(t *testing.T) {
	for _, tc := range []struct {
		name             string
		yes, interactive bool
	}{
		{"yes", true, true},
		{"no terminal", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defer output.StubInteractive(tc.interactive)()
			var calls atomic.Int32
			cmd, _, _ := writeCmd(t, false, tc.yes)
			withClient(t, cmd, balanceServer(t, http.StatusOK, `{"balance":1}`, &calls))
			if b := StartBalance(cmd); b != nil {
				b.Wait()
				t.Error("StartBalance started a lookup")
			}
			if calls.Load() != 0 {
				t.Errorf("%d balance requests, want 0", calls.Load())
			}
		})
	}
}

// TestDryRun_QuoteCarriesBalance: --dry-run has no prompt, but its quote
// reports the balance, in the JSON object for a script and on the "Would
// charge" line, even without a terminal.
func TestDryRun_QuoteCarriesBalance(t *testing.T) {
	t.Setenv("NAMECOM_USERNAME", "")
	t.Setenv("NAMECOM_TOKEN", "")
	defer output.StubInteractive(false)()
	var calls atomic.Int32
	cmd, stdout, stderr := writeCmd(t, true, false)
	withClient(t, cmd, balanceServer(t, http.StatusOK, `{"balance":12.5}`, &calls))
	failIfConfirmed(t)

	if _, err := RunWrite(cmd, Write[testBody]{
		Method: "POST", Path: "/core/v1/domains",
		Body:    testBody{Name: "acme.io"},
		Prompt:  "Register acme.io?",
		Quote:   &output.Quote{Total: 39.98, Currency: "USD"},
		Balance: StartBalance(cmd),
	}, failIfSent(t)); err != nil {
		t.Fatalf("RunWrite: %v", err)
	}
	if !strings.Contains(stderr.String(), "Would charge: $39.98 · production · balance $12.50") {
		t.Errorf("stderr lacks the balance on the charge line:\n%s", stderr)
	}
	if !strings.Contains(stderr.String(), "is less than this purchase's $39.98") {
		t.Errorf("stderr lacks the short-balance warning:\n%s", stderr)
	}

	out := Out(cmd)
	out.Format = output.FormatJSON
	stdout.Reset()
	if _, err := RunWrite(cmd, Write[testBody]{
		Method: "POST", Path: "/core/v1/domains",
		Body:    testBody{Name: "acme.io"},
		Quote:   &output.Quote{Total: 39.98, Currency: "USD"},
		Balance: StartBalance(cmd),
	}, failIfSent(t)); err != nil {
		t.Fatalf("RunWrite: %v", err)
	}
	if !strings.Contains(stdout.String(), `"balance": 12.5`) {
		t.Errorf("dry-run quote lacks the balance:\n%s", stdout)
	}
}
