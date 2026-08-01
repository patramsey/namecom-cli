package domain

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/patramsey/namecom-cli/cmd/cmdutil"
	"github.com/patramsey/namecom-cli/internal/api"
	"github.com/patramsey/namecom-cli/internal/api/gen"
	"github.com/patramsey/namecom-cli/internal/output"
	"github.com/spf13/cobra"
)

// ---- search -----------------------------------------------------------------

func TestSearch_ShowsResults(t *testing.T) {
	avail := true
	price := 12.99
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		results := []gen.SearchResult{
			{DomainName: "acme.com", Purchasable: avail, PurchasePrice: &price},
			{DomainName: "acme.io", Purchasable: false},
		}
		_ = json.NewEncoder(w).Encode(gen.SearchResponseSchema{Results: &results})
	}))
	t.Cleanup(srv.Close)

	cmd := baseCmd(t, srv)
	if err := runSearch(cmd, []string{"acme"}); err != nil {
		t.Fatalf("runSearch: %v", err)
	}
}

func TestSearch_Empty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		var empty []gen.SearchResult
		_ = json.NewEncoder(w).Encode(gen.SearchResponseSchema{Results: &empty})
	}))
	t.Cleanup(srv.Close)

	cmd := baseCmd(t, srv)
	if err := runSearch(cmd, []string{"zzznomatch"}); err != nil {
		t.Fatalf("runSearch: %v", err)
	}
}

func cmdForCheck(t *testing.T, srv *httptest.Server) *cobra.Command {
	t.Helper()
	cmd := baseCmd(t, srv)
	cmd.Flags().BoolVar(&checkAuthoritative, "authoritative", false, "")
	var yes bool
	cmd.PersistentFlags().BoolVarP(&yes, "yes", "y", false, "")
	t.Cleanup(func() { checkAuthoritative = false })
	return cmd
}

// cmdForCheckSandbox builds a check command whose root has --sandbox=true,
// so IsSandbox(cmd) returns true and ZoneCheck is bypassed.
func cmdForCheckSandbox(t *testing.T, srv *httptest.Server) *cobra.Command {
	t.Helper()
	child := cmdForCheck(t, srv)
	root := &cobra.Command{Use: "namecom"}
	var sandbox bool
	root.PersistentFlags().BoolVar(&sandbox, "sandbox", false, "")
	if err := root.PersistentFlags().Set("sandbox", "true"); err != nil {
		t.Fatalf("setting sandbox flag: %v", err)
	}
	root.AddCommand(child)
	return child
}

// cmdForCheckJSON builds a check command that emits JSON into the returned
// buffer, so tests can assert on the exact serialized fields. baseCmd keeps
// its buffer private and renders tables, which hides field-level regressions.
func cmdForCheckJSON(t *testing.T, srv *httptest.Server) (*cobra.Command, *bytes.Buffer) {
	t.Helper()
	client, err := api.New(api.Options{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("api.New: %v", err)
	}
	var buf bytes.Buffer
	out := &output.Config{
		Format:  output.FormatJSON,
		Color:   output.ColorNever,
		Writer:  &buf,
		EWriter: &bytes.Buffer{},
	}
	cmd := &cobra.Command{}
	ctx := context.WithValue(context.Background(), cmdutil.KeyOutput, out)
	ctx = context.WithValue(ctx, cmdutil.KeyClient, client)
	cmd.SetContext(ctx)
	cmd.Flags().BoolVar(&checkAuthoritative, "authoritative", false, "")
	t.Cleanup(func() { checkAuthoritative = false })
	return cmd, &buf
}

// TestCheck_ZoneCheckPathPopulatesSldTld guards a regression where results
// synthesized from ZoneCheck (which returns only domainName + available) were
// missing the Sld and Tld fields that the CheckAvailability path populates —
// so `domain check <domain> -o json` emitted `"sld": "", "tld": ""` depending
// on which code path served the request.
func TestCheck_ZoneCheckPathPopulatesSldTld(t *testing.T) {
	price := 12.99
	free, taken := true, false

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/core/v1/zonecheck":
			results := []gen.ZoneCheckResult{
				{DomainName: "free.com", Available: &free},
				{DomainName: "taken.co.uk", Available: &taken},
			}
			_ = json.NewEncoder(w).Encode(gen.ZoneCheckResponseSchema{Results: results, Total: 2})
		case "/core/v1/domains/free.com:getPricing":
			_ = json.NewEncoder(w).Encode(gen.PricingResponseSchema{PurchasePrice: &price})
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
			http.Error(w, "unexpected", http.StatusInternalServerError)
		}
	}))
	t.Cleanup(srv.Close)

	cmd, buf := cmdForCheckJSON(t, srv)
	if err := runCheck(cmd, []string{"free.com", "taken.co.uk"}); err != nil {
		t.Fatalf("runCheck: %v", err)
	}

	var got []gen.SearchResult
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("output is not valid JSON: %v\noutput: %s", err, buf.String())
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 results, got %d: %s", len(got), buf.String())
	}

	// free.com is filled in by the pricing goroutine, taken.co.uk by the plain
	// unavailable branch — the two branches build SearchResult separately, so
	// both need covering. The split is at the FIRST dot, per the spec ("TLD is
	// the rest of the domain_name after the SLD"), keeping co.uk intact.
	want := []struct{ domain, sld, tld string }{
		{"free.com", "free", "com"},
		{"taken.co.uk", "taken", "co.uk"},
	}
	for i, w := range want {
		if got[i].DomainName != w.domain {
			t.Fatalf("result %d: expected domain %q, got %q", i, w.domain, got[i].DomainName)
		}
		if got[i].Sld != w.sld {
			t.Errorf("%s: expected sld %q, got %q", w.domain, w.sld, got[i].Sld)
		}
		if got[i].Tld != w.tld {
			t.Errorf("%s: expected tld %q, got %q", w.domain, w.tld, got[i].Tld)
		}
	}
}

func TestCheck_ZoneCheckAvailableGetsPricing(t *testing.T) {
	avail := true
	price := 12.99
	var pricingCalled bool

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/core/v1/zonecheck":
			results := []gen.ZoneCheckResult{{DomainName: "free.com", Available: &avail}}
			_ = json.NewEncoder(w).Encode(gen.ZoneCheckResponseSchema{Results: results, Total: 1})
		case "/core/v1/domains/free.com:getPricing":
			pricingCalled = true
			_ = json.NewEncoder(w).Encode(gen.PricingResponseSchema{PurchasePrice: &price})
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
			http.Error(w, "unexpected", http.StatusInternalServerError)
		}
	}))
	t.Cleanup(srv.Close)

	cmd := cmdForCheck(t, srv)
	if err := runCheck(cmd, []string{"free.com"}); err != nil {
		t.Fatalf("runCheck: %v", err)
	}
	if !pricingCalled {
		t.Error("expected GetPricingForDomain to be called for available domain")
	}
}

func TestCheck_ZoneCheckUnavailableSkipsPricing(t *testing.T) {
	avail := false
	var pricingCalled bool

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/core/v1/zonecheck":
			results := []gen.ZoneCheckResult{{DomainName: "taken.com", Available: &avail}}
			_ = json.NewEncoder(w).Encode(gen.ZoneCheckResponseSchema{Results: results, Total: 1})
		default:
			pricingCalled = true
			t.Errorf("unexpected request for unavailable domain: %s %s", r.Method, r.URL)
			http.Error(w, "unexpected", http.StatusInternalServerError)
		}
	}))
	t.Cleanup(srv.Close)

	cmd := cmdForCheck(t, srv)
	if err := runCheck(cmd, []string{"taken.com"}); err != nil {
		t.Fatalf("runCheck: %v", err)
	}
	if pricingCalled {
		t.Error("expected no pricing call for unavailable domain")
	}
}

func TestCheck_ZoneCheckNullFallsBackToCheckAvailability(t *testing.T) {
	price := 29.99
	var checkAvailCalled bool

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/core/v1/zonecheck":
			// null Available signals unsupported TLD.
			results := []gen.ZoneCheckResult{{DomainName: "example.xyz", Available: nil}}
			_ = json.NewEncoder(w).Encode(gen.ZoneCheckResponseSchema{Results: results, Total: 1})
		case "/core/v1/domains:checkAvailability":
			checkAvailCalled = true
			results := []gen.SearchResult{{DomainName: "example.xyz", Purchasable: true, PurchasePrice: &price}}
			_ = json.NewEncoder(w).Encode(gen.SearchResponseSchema{Results: &results})
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
			http.Error(w, "unexpected", http.StatusInternalServerError)
		}
	}))
	t.Cleanup(srv.Close)

	cmd := cmdForCheck(t, srv)
	if err := runCheck(cmd, []string{"example.xyz"}); err != nil {
		t.Fatalf("runCheck: %v", err)
	}
	if !checkAvailCalled {
		t.Error("expected CheckAvailability fallback for unsupported TLD (null Available)")
	}
}

func TestCheck_AuthoritativeBypassesZoneCheck(t *testing.T) {
	price := 12.99
	var zoneCheckCalled bool

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/core/v1/zonecheck":
			zoneCheckCalled = true
			t.Error("ZoneCheck should not be called with --authoritative")
			http.Error(w, "unexpected", http.StatusInternalServerError)
		case "/core/v1/domains:checkAvailability":
			results := []gen.SearchResult{{DomainName: "free.com", Purchasable: true, PurchasePrice: &price}}
			_ = json.NewEncoder(w).Encode(gen.SearchResponseSchema{Results: &results})
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
			http.Error(w, "unexpected", http.StatusInternalServerError)
		}
	}))
	t.Cleanup(srv.Close)

	cmd := cmdForCheck(t, srv)
	if err := cmd.ParseFlags([]string{"--authoritative"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	if err := runCheck(cmd, []string{"free.com"}); err != nil {
		t.Fatalf("runCheck: %v", err)
	}
	if zoneCheckCalled {
		t.Error("ZoneCheck was called despite --authoritative flag")
	}
}

// ---- renderSearchResults ----------------------------------------------------

func outWithFormat(format output.Format, buf *bytes.Buffer) *output.Config {
	return &output.Config{Format: format, Color: output.ColorNever, Writer: buf, EWriter: &bytes.Buffer{}}
}

func TestRenderSearchResults_EmptyResults(t *testing.T) {
	var buf bytes.Buffer
	out := outWithFormat(output.FormatTable, &buf)
	results := []gen.SearchResult{}
	if err := renderSearchResults(out, &results); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRenderSearchResults_JSONOutput(t *testing.T) {
	price := 12.99
	results := []gen.SearchResult{
		{DomainName: "free.com", Purchasable: true, PurchasePrice: &price},
		{DomainName: "taken.com", Purchasable: false},
	}
	var buf bytes.Buffer
	out := outWithFormat(output.FormatJSON, &buf)
	if err := renderSearchResults(out, &results); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var decoded []gen.SearchResult
	if err := json.Unmarshal(buf.Bytes(), &decoded); err != nil {
		t.Fatalf("output is not valid JSON: %v\noutput: %s", err, buf.String())
	}
	if len(decoded) != 2 {
		t.Errorf("expected 2 results in JSON, got %d", len(decoded))
	}
}

func TestRenderSearchResults_QuietMode(t *testing.T) {
	price := 9.99
	results := []gen.SearchResult{
		{DomainName: "available.com", Purchasable: true, PurchasePrice: &price},
		{DomainName: "taken.com", Purchasable: false},
	}
	var buf bytes.Buffer
	out := outWithFormat(output.FormatTable, &buf)
	out.QuietMode = true
	if err := renderSearchResults(out, &results); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := buf.String()
	if !strings.Contains(got, "available.com") {
		t.Errorf("quiet output missing available domain: %q", got)
	}
	if strings.Contains(got, "taken.com") {
		t.Errorf("quiet output should exclude unavailable domains, got: %q", got)
	}
}

func TestCheck_UnexpectedZoneCheckDomainSkippedForPricing(t *testing.T) {
	avail := true
	price := 12.99
	var pricingCalls []string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/core/v1/zonecheck":
			// API returns the requested domain AND an extra one we didn't ask for.
			results := []gen.ZoneCheckResult{
				{DomainName: "free.com", Available: &avail},
				{DomainName: "unexpected.com", Available: &avail},
			}
			_ = json.NewEncoder(w).Encode(gen.ZoneCheckResponseSchema{Results: results, Total: 2})
		case "/core/v1/domains/free.com:getPricing":
			pricingCalls = append(pricingCalls, r.URL.Path)
			_ = json.NewEncoder(w).Encode(gen.PricingResponseSchema{PurchasePrice: &price})
		default:
			// Any request for "unexpected.com" pricing would land here.
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
			http.Error(w, "unexpected", http.StatusInternalServerError)
		}
	}))
	t.Cleanup(srv.Close)

	cmd := cmdForCheck(t, srv)
	if err := runCheck(cmd, []string{"free.com"}); err != nil {
		t.Fatalf("runCheck: %v", err)
	}
	if len(pricingCalls) != 1 {
		t.Errorf("expected exactly 1 pricing call (for free.com), got %d: %v", len(pricingCalls), pricingCalls)
	}
}

func TestCheck_PricingPopulatedFromGetPricing(t *testing.T) {
	avail := true
	price := 12.99
	var pricingPrice float64

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/core/v1/zonecheck":
			results := []gen.ZoneCheckResult{{DomainName: "free.com", Available: &avail}}
			_ = json.NewEncoder(w).Encode(gen.ZoneCheckResponseSchema{Results: results, Total: 1})
		case "/core/v1/domains/free.com:getPricing":
			pricingPrice = price
			_ = json.NewEncoder(w).Encode(gen.PricingResponseSchema{PurchasePrice: &price})
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
			http.Error(w, "unexpected", http.StatusInternalServerError)
		}
	}))
	t.Cleanup(srv.Close)

	cmd := cmdForCheck(t, srv)
	if err := runCheck(cmd, []string{"free.com"}); err != nil {
		t.Fatalf("runCheck: %v", err)
	}
	if pricingPrice != price {
		t.Errorf("expected GetPricing to be called and return %.2f, got %.2f", price, pricingPrice)
	}
}

// ---- sandbox bypass ---------------------------------------------------------

// TestCheck_SandboxBypassesZoneCheck verifies that --sandbox routes directly
// to CheckAvailability (EPP) without calling ZoneCheck. ZoneCheck always
// queries production DNS zone files and has no sandbox equivalent, so mixing
// it with the sandbox EPP registry produces contradictory results.
func TestCheck_SandboxBypassesZoneCheck(t *testing.T) {
	avail := true
	price := 9.99
	var zoneCheckCalled bool

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/core/v1/zonecheck":
			zoneCheckCalled = true
			t.Error("ZoneCheck should not be called in sandbox mode")
			http.Error(w, "unexpected zonecheck call", http.StatusInternalServerError)
		case "/core/v1/domains:checkAvailability":
			results := []gen.SearchResult{
				{DomainName: "example.com", Purchasable: avail, PurchasePrice: &price},
			}
			_ = json.NewEncoder(w).Encode(gen.SearchResponseSchema{Results: &results})
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
			http.Error(w, "unexpected", http.StatusInternalServerError)
		}
	}))
	t.Cleanup(srv.Close)

	cmd := cmdForCheckSandbox(t, srv)
	if err := runCheck(cmd, []string{"example.com"}); err != nil {
		t.Fatalf("runCheck in sandbox mode: %v", err)
	}
	if zoneCheckCalled {
		t.Error("ZoneCheck was called in sandbox mode — should have been bypassed")
	}
}
