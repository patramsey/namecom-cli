package domain

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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
		case r.URL.Path == "/core/v1/accountinfo/balance":
			_, _ = w.Write([]byte(`{"balance":120}`))
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
			// The balance the stub reports, after who pays (#271).
			if want := "Would charge: $39.98 (2 years) · sandbox · balance $120.00"; !strings.Contains(stderr, want) {
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
				DryRun bool            `json:"dryRun"`
				Body   json.RawMessage `json:"body"`
				Quote  *output.Quote   `json:"quote"`
			}
			if err := json.Unmarshal(out.Writer.(*bytes.Buffer).Bytes(), &doc); err != nil {
				t.Fatalf("dry run is not one JSON document: %v\n%s", err, out.Writer)
			}
			want := output.Quote{Total: 39.98, Currency: "USD", Years: 2}
			if !doc.DryRun || len(doc.Body) == 0 || doc.Quote == nil {
				t.Fatalf("dry run = %s, want the body and quote %+v", out.Writer, want)
			}
			// The quote carries the balance too, for a script to compare
			// (#271).
			got := *doc.Quote
			if got.Balance == nil || *got.Balance != 120 {
				t.Errorf("quote balance = %v, want 120:\n%s", got.Balance, out.Writer)
			}
			got.Balance = nil
			if got != want {
				t.Errorf("dry run = %s, want the body and quote %+v", out.Writer, want)
			}
		})
	}
}

func ptr[T any](v T) *T { return &v }

// TestDomainRows_RenewalLockAndRegistrant pins #235: `domain get` left out
// renewalPrice, transferLockExpiresAt and the registrant, all in the JSON.
// Each row appears only when the response carries it; a lock that has
// already lifted is not shown.
func TestDomainRows_RenewalLockAndRegistrant(t *testing.T) {
	now := time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC)
	out := &output.Config{Format: output.FormatTable, Color: output.ColorNever, Writer: &bytes.Buffer{}, EWriter: &bytes.Buffer{}}
	labels := func(rows [][]string) map[string]string {
		m := map[string]string{}
		for _, r := range rows {
			m[r[0]] = r[1]
		}
		return m
	}

	bare := labels(domainRows(out, &coreapigo.DomainResponsePayload{DomainName: "acme.io"}, now))
	for _, k := range []string{"Renews at", "Transfer lock", "Registrant"} {
		if _, ok := bare[k]; ok {
			t.Errorf("%s shown for a response without it", k)
		}
	}

	lock := time.Now().AddDate(0, 0, 90)
	full := labels(domainRows(out, &coreapigo.DomainResponsePayload{
		DomainName:            "acme.io",
		RenewalPrice:          ptr(17.99),
		TransferLockExpiresAt: &lock,
		Contacts: &coreapigo.Contacts{Registrant: &coreapigo.RegistrantContact{
			FirstName: ptr("Jane"), LastName: ptr("Doe"), CompanyName: ptr("Acme Inc"), IsVerified: ptr(false)}},
	}, now))
	if got := full["Renews at"]; got != "$17.99" {
		t.Errorf("Renews at = %q", got)
	}
	if got, want := full["Transfer lock"], "until "+lock.Format("2006-01-02")+" (in 3 months)"; got != want {
		t.Errorf("Transfer lock = %q, want %q", got, want)
	}
	if got := full["Registrant"]; got != "Jane Doe (Acme Inc) · email not verified" {
		t.Errorf("Registrant = %q", got)
	}

	past := now.AddDate(0, 0, -1)
	if _, ok := labels(domainRows(out, &coreapigo.DomainResponsePayload{DomainName: "acme.io", TransferLockExpiresAt: &past}, now))["Transfer lock"]; ok {
		t.Error("a lock that has lifted is still shown")
	}
}

// TestPricing_HeadedAndNoPremiumRow pins #235: `domain pricing` printed an
// unheaded table with "Premium: no" as a row of the PRICE column. The heading
// names the domain, the term, and premium status when it applies.
func TestPricing_HeadedAndNoPremiumRow(t *testing.T) {
	for _, tc := range []struct {
		premium bool
		title   string
	}{
		{false, "example.org — per term (1 year for most TLDs)"},
		{true, "example.org (premium) — per term (1 year for most TLDs)"},
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			if strings.HasSuffix(r.URL.Path, ":checkAvailability") {
				_, _ = w.Write([]byte(`{"results":[{"domainName":"example.org","purchasable":true,"purchasePrice":17.99}]}`))
				return
			}
			_, _ = w.Write([]byte(`{"premium":` + map[bool]string{true: "true", false: "false"}[tc.premium] +
				`,"purchasePrice":17.99,"renewalPrice":17.99,"transferPrice":12.99}`))
		}))
		cmd := cmdForPricing(t, srv)
		if err := runPricing(cmd, []string{"example.org"}); err != nil {
			t.Fatalf("runPricing: %v", err)
		}
		srv.Close()
		stdout := cmdutil.Out(cmd).Writer.(*bytes.Buffer).String()
		if first := strings.SplitN(stdout, "\n", 2)[0]; first != tc.title {
			t.Errorf("heading = %q, want %q\n%s", first, tc.title, stdout)
		}
		if strings.Contains(stdout, "Premium") {
			t.Errorf("premium is not a price, but is a row:\n%s", stdout)
		}
	}
}

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
