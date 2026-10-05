package domain

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	coreapigo "github.com/namedotcom/core-api-go"
	"github.com/patramsey/namecom-cli/cmd/cmdutil"
	"github.com/patramsey/namecom-cli/internal/output"
	"github.com/spf13/cobra"
)

// purchaseQuoteServer answers the reads register and renew make before they
// buy, quoting price for the term, and fails the test on anything else: a
// dry run must not purchase.
func purchaseQuoteServer(t *testing.T, pricing string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "checkAvailability"):
			_, _ = w.Write([]byte(`{"results":[{"domainName":"acme.io","purchasable":true,"purchasePrice":19.99,"renewalPrice":19.99}]}`))
		case strings.Contains(r.URL.Path, "getPricing"):
			_, _ = w.Write([]byte(pricing))
		case strings.Contains(r.URL.Path, "claims"):
			_, _ = w.Write([]byte(`{"domain":"acme.io","claimsProcessActive":false,"claimId":null,"claims":[]}`))
		default:
			t.Errorf("a dry run sent %s %s", r.Method, r.URL)
			http.Error(w, "unexpected", http.StatusInternalServerError)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestPurchaseDryRun_StatesTheCharge pins #235: `domain renew X --years 2
// --dry-run` printed only {"years": 2}, and a standard registration's body
// carries no price at all, so a dry run never said what the real run would
// charge. Table mode now ends with a "Would charge" line naming who pays, and
// JSON carries a quote field beside the unchanged body.
func TestPurchaseDryRun_StatesTheCharge(t *testing.T) {
	defer output.StubInteractive(false)()
	t.Setenv("NAMECOM_USERNAME", "")
	t.Setenv("NAMECOM_TOKEN", "")
	const pricing = `{"premium":false,"purchasePrice":39.98,"renewalPrice":39.98,"transferPrice":19.99}`

	type run struct {
		name     string
		register func(*cobra.Command)
		run      func(*cobra.Command, []string) error
	}
	runs := []run{
		{"register", func(c *cobra.Command) {
			c.Flags().IntVar(&registerYears, "years", 1, "")
			c.Flags().BoolVar(&registerPrivacy, "privacy", false, "")
			c.Flags().BoolVar(&registerAutorenew, "autorenew", false, "")
			c.Flags().StringVar(&registerContactsFile, "contacts-file", "", "")
			c.Flags().Float64Var(&registerPrice, "price", 0, "")
			c.Flags().BoolVar(&registerAckClaim, "acknowledge-claim", false, "")
			c.Flags().StringArrayVar(&registerTLDReqs, "tld-requirement", nil, "")
			c.Flags().Float64Var(&registerMaxPrice, "max-price", 0, "")
			c.Flags().BoolVar(&registerAccept, "accept-premium", false, "")
		}, runRegister},
		{"renew", func(c *cobra.Command) {
			c.Flags().IntVar(&renewYears, "years", 1, "")
			c.Flags().Float64Var(&renewPrice, "price", 0, "")
			c.Flags().Float64Var(&renewMaxPrice, "max-price", 0, "")
			c.Flags().BoolVar(&renewAccept, "accept-premium", false, "")
		}, runRenew},
	}
	t.Cleanup(func() { registerYears, renewYears = 1, 1 })

	for _, r := range runs {
		t.Run(r.name+"/table", func(t *testing.T) {
			cmd := domainDryRunCmd(t, purchaseQuoteServer(t, pricing), true, r.register)
			out := cmdutil.Out(cmd)
			out.Sandbox = true
			if err := cmd.Flags().Set("years", "2"); err != nil {
				t.Fatal(err)
			}
			if err := r.run(cmd, []string{"acme.io"}); err != nil {
				t.Fatalf("dry run: %v", err)
			}
			stderr := out.EWriter.(*bytes.Buffer).String()
			if want := "Would charge: $39.98 (2 years) · sandbox"; !strings.Contains(stderr, want) {
				t.Errorf("stderr lacks %q:\n%s", want, stderr)
			}
			if stdout := out.Writer.(*bytes.Buffer).String(); strings.Contains(stdout, "Would charge") {
				t.Errorf("the charge line belongs on stderr, not with the request on stdout:\n%s", stdout)
			}
		})

		t.Run(r.name+"/json", func(t *testing.T) {
			cmd := domainDryRunCmd(t, purchaseQuoteServer(t, pricing), true, r.register)
			out := cmdutil.Out(cmd)
			out.Format = output.FormatJSON
			if err := cmd.Flags().Set("years", "2"); err != nil {
				t.Fatal(err)
			}
			if err := r.run(cmd, []string{"acme.io"}); err != nil {
				t.Fatalf("dry run: %v", err)
			}
			var doc struct {
				DryRun bool            `json:"dry_run"`
				Body   json.RawMessage `json:"body"`
				Quote  *output.Quote   `json:"quote"`
			}
			if err := json.Unmarshal(out.Writer.(*bytes.Buffer).Bytes(), &doc); err != nil {
				t.Fatalf("dry run is not one JSON document: %v\n%s", err, out.Writer)
			}
			want := output.Quote{Total: 39.98, Currency: "USD", Years: 2}
			if !doc.DryRun || len(doc.Body) == 0 || doc.Quote == nil || *doc.Quote != want {
				t.Errorf("dry run = %s, want the body and quote %+v", out.Writer, want)
			}
		})
	}
}

func ptr[T any](v T) *T { return &v }

// TestSearchResults_ShowRenewalPrice pins #235: search and check showed the
// first-year price only, though the results carry renewalPrice — $3.99 now,
// renewing at several times that, is the surprise a registrar is known for.
func TestSearchResults_ShowRenewalPrice(t *testing.T) {
	var buf bytes.Buffer
	out := &output.Config{Format: output.FormatTable, Color: output.ColorNever, Writer: &buf, EWriter: &bytes.Buffer{}, Wide: true, Plain: true}
	results := []*coreapigo.SearchResult{
		{DomainName: "cheap.xyz", Purchasable: true, PurchasePrice: ptr(3.99), RenewalPrice: ptr(14.99)},
		{DomainName: "taken.com", Purchasable: false},
	}
	if err := renderSearchResults(out, results); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("want a header and two rows, got:\n%s", buf.String())
	}
	for i, want := range [][]string{
		{"DOMAIN", "AVAILABILITY", "PRICE", "RENEWS", "PREMIUM"},
		{"cheap.xyz", "✓", "available", "$3.99/yr", "$14.99/yr", "no"},
		{"taken.com", "taken", "—", "—", "—"},
	} {
		if got := strings.Fields(lines[i]); strings.Join(got, "|") != strings.Join(want, "|") {
			t.Errorf("line %d = %q, want %q", i, got, want)
		}
	}
}

// registerBody is a CreateDomainRequest for years, with purchaseType and
// purchasePrice set when purchaseType is not empty.
func registerBody(years int, purchaseType string, price float64) coreapigo.CreateDomainRequest {
	b := coreapigo.CreateDomainRequest{Years: &years}
	if purchaseType != "" {
		b.PurchaseType, b.PurchasePrice = &purchaseType, &price
	}
	return b
}

// TestRegisterDryRun_FlatPriceQuotesNoTerm: an aftermarket price is a flat
// fee with no guaranteed term (#132), so its quote states no years either.
func TestRegisterDryRun_FlatPriceQuotesNoTerm(t *testing.T) {
	q := registerChargeQuote(registerBody(2, "aftermarket_b", 2500), nil, ptr(2500.0))
	if q == nil || q.Years != 0 || q.Note != "aftermarket_b, flat price" || q.Summary() != "$2,500.00 (aftermarket_b, flat price)" {
		t.Errorf("quote = %+v", q)
	}
	if q := registerChargeQuote(registerBody(1, "", 0), nil, nil); q != nil {
		t.Errorf("no price must mean no quote, got %+v", q)
	}
}

// TestRegisterPrompt_StatesTermRenewalAndChoices pins #235's register prompt:
// it read "for 2 year(s) at $35.98 total for 2 years" and said nothing of the
// privacy and auto-renew settings the body carries.
func TestRegisterPrompt_StatesTermRenewalAndChoices(t *testing.T) {
	pricing := &coreapigo.PricingResponse{PurchasePrice: ptr(35.98), RenewalPrice: ptr(35.98)}
	for _, tc := range []struct {
		privacy, autorenew bool
		want               string
	}{
		{true, false, "Register acme.io for 2 years: $35.98 total (renews at $17.99/yr), with WHOIS privacy, without auto-renew?"},
		{true, true, "Register acme.io for 2 years: $35.98 total (renews at $17.99/yr), with WHOIS privacy and auto-renew?"},
		{false, true, "Register acme.io for 2 years: $35.98 total (renews at $17.99/yr), with auto-renew, without WHOIS privacy?"},
		{false, false, "Register acme.io for 2 years: $35.98 total (renews at $17.99/yr), without WHOIS privacy or auto-renew?"},
	} {
		body := registerBody(2, "", 0)
		body.Domain = &coreapigo.DomainCreatePayload{PrivacyEnabled: &tc.privacy, AutorenewEnabled: &tc.autorenew}
		if got := registerPrompt("acme.io", body, pricing); got != tc.want {
			t.Errorf("prompt = %q\nwant     %q", got, tc.want)
		}
	}
}
