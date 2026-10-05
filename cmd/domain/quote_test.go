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
