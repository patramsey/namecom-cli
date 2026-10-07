package transfer

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	coreapigo "github.com/namedotcom/core-api-go"
	"github.com/patramsey/namecom-cli/cmd/cmdutil"
	"github.com/patramsey/namecom-cli/internal/drifttest"
	"github.com/patramsey/namecom-cli/internal/output"
)

// pricingOnlyServer quotes pricing for GetPricingForDomain, and a balance of
// $120.00 for the balance lookup, and fails the test on any other request.
func pricingOnlyServer(t *testing.T, pricing string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/core/v1/accountinfo/balance" {
			_, _ = w.Write([]byte(`{"balance":120}`))
			return
		}
		if !strings.Contains(r.URL.Path, "getPricing") {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
			http.Error(w, "unexpected", http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte(pricing))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestTransferDryRun_StatesTheCharge pins the transfer half of #235: a
// standard transfer's body carries no price, so its dry run never said what
// the transfer would cost.
func TestTransferDryRun_StatesTheCharge(t *testing.T) {
	t.Setenv("NAMECOM_USERNAME", "")
	t.Setenv("NAMECOM_TOKEN", "")
	for _, format := range []output.Format{output.FormatTable, output.FormatJSON} {
		t.Run(string(format), func(t *testing.T) {
			cmd := cmdForTransferCreate(t, pricingOnlyServer(t, `{"transferPrice":12.99}`))
			if err := cmd.ParseFlags([]string{"--auth-code", "AUTH123"}); err != nil {
				t.Fatal(err)
			}
			cmd = drifttest.WithDryRun(t, cmd, true)
			out := cmdutil.Out(cmd)
			out.Format = format
			if err := runCreate(cmd, []string{"example.com"}); err != nil {
				t.Fatalf("dry run: %v", err)
			}
			stdout := out.Writer.(*bytes.Buffer).String()
			if format == output.FormatTable {
				stderr := out.EWriter.(*bytes.Buffer).String()
				want := "Would charge: $12.99 (covers the TLD's minimum term, typically 1 year) · production · balance $120.00"
				if !strings.Contains(stderr, want) {
					t.Errorf("stderr lacks %q:\n%s", want, stderr)
				}
				return
			}
			var doc struct {
				Quote *output.Quote `json:"quote"`
			}
			if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
				t.Fatalf("not one JSON document: %v\n%s", err, stdout)
			}
			if doc.Quote == nil || doc.Quote.Total != 12.99 || doc.Quote.Currency != "USD" || doc.Quote.Years != 0 ||
				doc.Quote.Balance == nil || *doc.Quote.Balance != 120 {
				t.Errorf("quote = %+v, want $12.99 USD with no year count and a $120 balance:\n%s", doc.Quote, stdout)
			}
		})
	}
}

// TestTransferPrompt_SaysWhatThePriceBuys pins #235: the prompt said "plus
// WHOIS privacy" with no price, which read as an extra charge, and never said
// what the transfer price covers. Both are now stated as the SDK documents
// them: privacy is free, and the price covers the TLD's minimum term.
func TestTransferPrompt_SaysWhatThePriceBuys(t *testing.T) {
	on := true
	body := coreapigo.CreateTransferRequest{DomainName: "acme.io", PrivacyEnabled: &on}
	want := "Transfer acme.io in for $12.99 (covers the TLD's minimum term, typically 1 year), with WHOIS privacy at no charge?"
	if got := transferPrompt("acme.io", body, ptr(12.99)); got != want {
		t.Errorf("prompt = %q\nwant     %q", got, want)
	}
	if got := transferPrompt("acme.io", coreapigo.CreateTransferRequest{DomainName: "acme.io"}, nil); got != "Transfer acme.io in?" {
		t.Errorf("unpriced prompt = %q", got)
	}
}

func ptr[T any](v T) *T { return &v }

// TestTransferCancel_ChecksTheTransferFirst pins #235: `transfer cancel`
// asked "Cancel transfer of X?" for any name, and only the API's reply to the
// POST said there was no transfer. It now looks the transfer up first: a
// missing one fails with not-found before any prompt, and a real one's status
// is in the question.
func TestTransferCancel_ChecksTheTransferFirst(t *testing.T) {
	var prompts []string
	defer cmdutil.StubConfirm(func(p string) bool { prompts = append(prompts, p); return false })()

	t.Run("missing", func(t *testing.T) {
		prompts = nil
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet {
				t.Errorf("sent %s %s for a transfer that does not exist", r.Method, r.URL)
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"Not Found"}`))
		}))
		t.Cleanup(srv.Close)
		err := runCancel(cmdForTransferGet(t, srv), []string{"typo.com"})
		if !cmdutil.IsNotFound(err) || !strings.Contains(err.Error(), `transfer of "typo.com" not found`) {
			t.Errorf("runCancel = %v, want a not-found error naming the domain", err)
		}
		if len(prompts) != 0 {
			t.Errorf("prompted %q before finding there was nothing to cancel", prompts)
		}
	})

	t.Run("present", func(t *testing.T) {
		prompts = nil
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet {
				t.Errorf("declined prompt still sent %s %s", r.Method, r.URL)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"domainName":"acme.io","status":"pending_transfer"}`))
		}))
		t.Cleanup(srv.Close)
		// The decline's own result is #236's to define; only the question
		// matters here.
		_ = runCancel(cmdForTransferGet(t, srv), []string{"acme.io"})
		if want := "Cancel transfer of acme.io (status: pending_transfer)?"; len(prompts) != 1 || prompts[0] != want {
			t.Errorf("prompts = %q, want [%q]", prompts, want)
		}
	})
}

// TestTransferDryRun_NoQuoteWithoutAPrice: when pricing fails the transfer is
// still allowed, unpriced, so the dry run reports no charge rather than $0.00.
func TestTransferDryRun_NoQuoteWithoutAPrice(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"message":"unavailable"}`, http.StatusBadRequest)
	}))
	t.Cleanup(srv.Close)
	cmd := cmdForTransferCreate(t, srv)
	if err := cmd.ParseFlags([]string{"--auth-code", "AUTH123"}); err != nil {
		t.Fatal(err)
	}
	cmd = drifttest.WithDryRun(t, cmd, true)
	if err := runCreate(cmd, []string{"example.com"}); err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if stderr := cmdutil.Out(cmd).EWriter.(*bytes.Buffer).String(); strings.Contains(stderr, "Would charge") {
		t.Errorf("an unpriced transfer must not claim a charge:\n%s", stderr)
	}
}
