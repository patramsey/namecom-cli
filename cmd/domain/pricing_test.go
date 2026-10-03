package domain

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/patramsey/namecom-cli/cmd/cmdutil"
	"github.com/patramsey/namecom-cli/internal/output"
)

// pricingServer answers GetPricingForDomain with the standard $17.99 and
// checkAvailability with purchaseType at $8625, as the API does for an
// aftermarket name.
func pricingServer(t *testing.T, purchaseType string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, ":checkAvailability") {
			_, _ = w.Write([]byte(`{"results":[{"domainName":"example.org","purchasable":true,` +
				`"purchasePrice":8625,"purchaseType":"` + purchaseType + `"}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"premium":false,"purchasePrice":17.99,"renewalPrice":17.99,"transferPrice":12.99}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestPricing_ReportsNonStandardPurchase covers #187: `domain pricing
// example.org` showed the standard $17.99 while `check`, `search` and
// `register` quoted an aftermarket purchase at $8625. GetPricingForDomain does
// not include acquisition prices, so pricing also checks availability and
// reports the price a registration would actually cost.
func TestPricing_ReportsNonStandardPurchase(t *testing.T) {
	run := func(t *testing.T, purchaseType string, set func(*output.Config)) (stdout, stderr string) {
		t.Helper()
		cmd := cmdForPricing(t, pricingServer(t, purchaseType))
		out := cmdutil.Out(cmd)
		set(out)
		if err := runPricing(cmd, []string{"example.org"}); err != nil {
			t.Fatalf("runPricing: %v", err)
		}
		return out.Writer.(*bytes.Buffer).String(), out.EWriter.(*bytes.Buffer).String()
	}

	t.Run("table", func(t *testing.T) {
		stdout, stderr := run(t, "aftermarket_s", func(*output.Config) {})
		if !strings.Contains(stdout, "$8625.00 flat (aftermarket_s)") {
			t.Errorf("the Register row must quote the acquisition price, got:\n%s", stdout)
		}
		if !strings.Contains(stderr, "$8625.00") || !strings.Contains(stderr, "$17.99") {
			t.Errorf("want a warning naming both prices on stderr, got %q", stderr)
		}
	})

	t.Run("json keeps the pricing fields and adds the purchase", func(t *testing.T) {
		stdout, _ := run(t, "aftermarket_s", func(o *output.Config) { o.Format = output.FormatJSON })
		var got map[string]any
		if err := json.Unmarshal([]byte(stdout), &got); err != nil {
			t.Fatalf("not JSON: %v (%s)", err, stdout)
		}
		if got["purchasePrice"] != 17.99 || got["renewalPrice"] != 17.99 {
			t.Errorf("the existing fields must be unchanged, got %v", got)
		}
		if got["purchaseType"] != "aftermarket_s" || got["purchaseTypePrice"] != 8625.0 {
			t.Errorf("want purchaseType and purchaseTypePrice added, got %v", got)
		}
	})

	t.Run("quiet prints what a registration costs", func(t *testing.T) {
		stdout, _ := run(t, "expiring", func(o *output.Config) { o.QuietMode = true })
		if strings.TrimSpace(stdout) != "8625.00" {
			t.Errorf("want 8625.00, got %q", stdout)
		}
	})

	t.Run("a plain registration is unchanged", func(t *testing.T) {
		stdout, stderr := run(t, "registration", func(o *output.Config) { o.Format = output.FormatJSON })
		if strings.Contains(stdout, "purchaseType") || stderr != "" {
			t.Errorf("a standard registration must print as before, got %s / %q", stdout, stderr)
		}
	})
}
