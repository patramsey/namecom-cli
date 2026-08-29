package domain

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	coreapigo "github.com/namedotcom/core-api-go"
	"github.com/patramsey/namecom-cli/cmd/cmdutil"
	"github.com/patramsey/namecom-cli/internal/api"
	"github.com/patramsey/namecom-cli/internal/output"
	"github.com/spf13/cobra"
)

// ---- search -----------------------------------------------------------------

func TestSearch_ShowsResults(t *testing.T) {
	avail := true
	price := 12.99
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		results := []*coreapigo.SearchResult{
			{DomainName: "acme.com", Purchasable: avail, PurchasePrice: &price},
			{DomainName: "acme.io", Purchasable: false},
		}
		_ = json.NewEncoder(w).Encode(coreapigo.SearchResponse{Results: results})
	}))
	t.Cleanup(srv.Close)

	cmd := baseCmd(t, srv)
	if err := runSearch(cmd, []string{"acme"}); err != nil {
		t.Fatalf("runSearch: %v", err)
	}
	// Assert what was rendered. err == nil passes while the renderer emits an
	// empty table, so the data the command exists to show can vanish silently.
	if buf, ok := cmdutil.Out(cmd).Writer.(*bytes.Buffer); ok {
		got := buf.String()
		if !strings.Contains(got, "acme.com") {
			t.Errorf("output is missing %q (both results and the price):\n%s", "acme.com", got)
		}
		if !strings.Contains(got, "acme.io") {
			t.Errorf("output is missing %q (both results and the price):\n%s", "acme.io", got)
		}
		if !strings.Contains(got, "12.99") {
			t.Errorf("output is missing %q (both results and the price):\n%s", "12.99", got)
		}
	} else {
		t.Fatal("output writer is not a *bytes.Buffer")
	}
}

func TestSearch_Empty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		var empty []*coreapigo.SearchResult
		_ = json.NewEncoder(w).Encode(coreapigo.SearchResponse{Results: empty})
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
			results := []*coreapigo.ZoneCheckResult{
				{DomainName: "free.com", Available: &free},
				{DomainName: "taken.co.uk", Available: &taken},
			}
			_ = json.NewEncoder(w).Encode(coreapigo.ZoneCheckResponse{Results: results, Total: 2})
		case "/core/v1/domains/free.com:getPricing":
			_ = json.NewEncoder(w).Encode(coreapigo.PricingResponse{PurchasePrice: &price})
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

	var got []*coreapigo.SearchResult
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
			results := []*coreapigo.ZoneCheckResult{{DomainName: "free.com", Available: &avail}}
			_ = json.NewEncoder(w).Encode(coreapigo.ZoneCheckResponse{Results: results, Total: 1})
		case "/core/v1/domains/free.com:getPricing":
			pricingCalled = true
			_ = json.NewEncoder(w).Encode(coreapigo.PricingResponse{PurchasePrice: &price})
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
			results := []*coreapigo.ZoneCheckResult{{DomainName: "taken.com", Available: &avail}}
			_ = json.NewEncoder(w).Encode(coreapigo.ZoneCheckResponse{Results: results, Total: 1})
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
			results := []*coreapigo.ZoneCheckResult{{DomainName: "example.xyz", Available: nil}}
			_ = json.NewEncoder(w).Encode(coreapigo.ZoneCheckResponse{Results: results, Total: 1})
		case "/core/v1/domains:checkAvailability":
			checkAvailCalled = true
			results := []*coreapigo.SearchResult{{DomainName: "example.xyz", Purchasable: true, PurchasePrice: &price}}
			_ = json.NewEncoder(w).Encode(coreapigo.SearchResponse{Results: results})
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
			results := []*coreapigo.SearchResult{{DomainName: "free.com", Purchasable: true, PurchasePrice: &price}}
			_ = json.NewEncoder(w).Encode(coreapigo.SearchResponse{Results: results})
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
	results := []*coreapigo.SearchResult{}
	if err := renderSearchResults(out, results); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRenderSearchResults_JSONOutput(t *testing.T) {
	price := 12.99
	results := []*coreapigo.SearchResult{
		{DomainName: "free.com", Purchasable: true, PurchasePrice: &price},
		{DomainName: "taken.com", Purchasable: false},
	}
	var buf bytes.Buffer
	out := outWithFormat(output.FormatJSON, &buf)
	if err := renderSearchResults(out, results); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var decoded []*coreapigo.SearchResult
	if err := json.Unmarshal(buf.Bytes(), &decoded); err != nil {
		t.Fatalf("output is not valid JSON: %v\noutput: %s", err, buf.String())
	}
	if len(decoded) != 2 {
		t.Errorf("expected 2 results in JSON, got %d", len(decoded))
	}
}

func TestRenderSearchResults_QuietMode(t *testing.T) {
	price := 9.99
	results := []*coreapigo.SearchResult{
		{DomainName: "available.com", Purchasable: true, PurchasePrice: &price},
		{DomainName: "taken.com", Purchasable: false},
	}
	var buf bytes.Buffer
	out := outWithFormat(output.FormatTable, &buf)
	out.QuietMode = true
	if err := renderSearchResults(out, results); err != nil {
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
			results := []*coreapigo.ZoneCheckResult{
				{DomainName: "free.com", Available: &avail},
				{DomainName: "unexpected.com", Available: &avail},
			}
			_ = json.NewEncoder(w).Encode(coreapigo.ZoneCheckResponse{Results: results, Total: 2})
		case "/core/v1/domains/free.com:getPricing":
			pricingCalls = append(pricingCalls, r.URL.Path)
			_ = json.NewEncoder(w).Encode(coreapigo.PricingResponse{PurchasePrice: &price})
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
			results := []*coreapigo.ZoneCheckResult{{DomainName: "free.com", Available: &avail}}
			_ = json.NewEncoder(w).Encode(coreapigo.ZoneCheckResponse{Results: results, Total: 1})
		case "/core/v1/domains/free.com:getPricing":
			pricingPrice = price
			_ = json.NewEncoder(w).Encode(coreapigo.PricingResponse{PurchasePrice: &price})
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
			results := []*coreapigo.SearchResult{
				{DomainName: "example.com", Purchasable: avail, PurchasePrice: &price},
			}
			_ = json.NewEncoder(w).Encode(coreapigo.SearchResponse{Results: results})
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

// TestInlineRegister_ForwardsPurchaseType covers the second registration path.
// runCheck's --authoritative branch holds a real coreapigo.SearchResult with
// PurchaseType populated, but handed inlineRegister only the domain name and
// price, so aftermarket results were registered as plain registrations. This
// path can't be driven end-to-end (it's gated on an interactive TTY), so drive
// inlineRegister directly.
func TestInlineRegister_ForwardsPurchaseType(t *testing.T) {
	price := 450.00
	ptype := coreapigo.SearchPurchaseType("aftermarket_b")

	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// inlineRegister now runs the same trademark check as `domain register`.
		if strings.Contains(r.URL.Path, "claims") {
			_, _ = w.Write([]byte(`{"domain":"example.com","claimsProcessActive":false,"claimId":null,"claims":[]}`))
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Errorf("decoding create body: %v", err)
		}
		_ = json.NewEncoder(w).Encode(coreapigo.CreateDomainResponse{})
	}))
	t.Cleanup(srv.Close)

	cmd := cmdForCheck(t, srv)
	result := &coreapigo.SearchResult{
		DomainName: "example.com", Purchasable: true,
		PurchasePrice: &price, PurchaseType: &ptype,
	}
	if err := inlineRegister(cmd, result); err != nil {
		t.Fatalf("inlineRegister: %v", err)
	}
	if gotBody == nil {
		t.Fatal("create request was never sent")
	}
	if got := gotBody["purchaseType"]; got != "aftermarket_b" {
		t.Errorf("expected purchaseType forwarded, got %#v", got)
	}
	if got := gotBody["purchasePrice"]; got != price {
		t.Errorf("expected purchasePrice %.2f, got %#v", price, got)
	}
}

// checkRegisterServer answers a ZoneCheck+pricing check for one available
// domain and records whether CreateDomain was ever called.
func checkRegisterServer(t *testing.T, registered *bool) *httptest.Server {
	t.Helper()
	avail := true
	price := 12.99
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "zonecheck"):
			results := []*coreapigo.ZoneCheckResult{{DomainName: "free.com", Available: &avail}}
			_ = json.NewEncoder(w).Encode(coreapigo.ZoneCheckResponse{Results: results, Total: 1})
		case strings.Contains(r.URL.Path, "getPricing"):
			_ = json.NewEncoder(w).Encode(coreapigo.PricingResponse{PurchasePrice: &price})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/domains"):
			*registered = true
			_ = json.NewEncoder(w).Encode(coreapigo.CreateDomainResponse{})
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
			http.Error(w, "unexpected", http.StatusInternalServerError)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestCheck_YesDoesNotAutoPurchase is a money guard. `domain check` ends with an
// interactive "offer to register" flow that passed the GLOBAL --yes straight
// into confirm(), so confirm returned true with no prompt and the domain was
// bought. --yes is exactly the flag people bake into aliases and CI wrappers
// because it is supposed to be safe on a read-only command; it must never
// convert `check` into `register`.
func TestCheck_YesDoesNotAutoPurchase(t *testing.T) {
	defer output.StubInteractive(true)()

	var registered bool
	srv := checkRegisterServer(t, &registered)

	cmd := cmdForCheck(t, srv)
	if err := cmd.PersistentFlags().Set("yes", "true"); err != nil {
		t.Fatalf("setting yes flag: %v", err)
	}
	// confirm() with yes=false and no TTY to read from must not silently succeed.
	_ = runCheck(cmd, []string{"free.com"})

	if registered {
		t.Error("MONEY BUG: `domain check --yes` registered the domain without an explicit answer")
	}
}

// TestCheck_DryRunDoesNotPurchase guards the other half: runCheck never
// consulted cmdutil.IsDryRun at all, so --dry-run performed a real purchase.
func TestCheck_DryRunDoesNotPurchase(t *testing.T) {
	defer output.StubInteractive(true)()

	var registered bool
	srv := checkRegisterServer(t, &registered)

	child := cmdForCheck(t, srv)
	root := &cobra.Command{Use: "namecom"}
	var dr, yes bool
	root.PersistentFlags().BoolVar(&dr, "dry-run", false, "")
	root.PersistentFlags().BoolVarP(&yes, "yes", "y", false, "")
	for _, f := range []string{"dry-run", "yes"} {
		if err := root.PersistentFlags().Set(f, "true"); err != nil {
			t.Fatalf("setting %s flag: %v", f, err)
		}
	}
	root.AddCommand(child)

	if err := runCheck(child, []string{"free.com"}); err != nil {
		t.Fatalf("runCheck: %v", err)
	}
	if registered {
		t.Error("MONEY BUG: `domain check --dry-run --yes` performed a real purchase")
	}
}

// TestCheck_SandboxByProfileBypassesZoneCheck guards a hole in the sandbox fix.
// cmdutil.IsSandbox only inspected the --sandbox FLAG, but sandbox mode is also
// reachable via a profile's `sandbox: true` or NAMECOM_SANDBOX. Those users got
// a sandbox client while runCheck still took the production-only ZoneCheck path
// — producing exactly the contradictory results the fix exists to prevent.
// out.Sandbox is the resolved value root.go computes from all three sources.
func TestCheck_SandboxByProfileBypassesZoneCheck(t *testing.T) {
	avail := true
	price := 9.99
	var zoneCheckCalled bool

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "zonecheck"):
			zoneCheckCalled = true
			t.Error("ZoneCheck must not run when the resolved profile is sandbox")
			http.Error(w, "unexpected", http.StatusInternalServerError)
		case strings.Contains(r.URL.Path, "checkAvailability"):
			results := []*coreapigo.SearchResult{{DomainName: "example.com", Purchasable: avail, PurchasePrice: &price}}
			_ = json.NewEncoder(w).Encode(coreapigo.SearchResponse{Results: results})
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
			http.Error(w, "unexpected", http.StatusInternalServerError)
		}
	}))
	t.Cleanup(srv.Close)

	cmd := cmdForCheck(t, srv)
	// No --sandbox flag; sandbox came from the profile, as root.go resolves it.
	cmdutil.Out(cmd).Sandbox = true

	if err := runCheck(cmd, []string{"example.com"}); err != nil {
		t.Fatalf("runCheck: %v", err)
	}
	if zoneCheckCalled {
		t.Error("ZoneCheck ran despite the resolved credentials being sandbox")
	}
}

// TestCheck_NormalizesDomainArgs guards a silent wrong-answer bug. runCheck is
// the only command that never runs its arguments through cmdutil.DomainArg,
// which lowercases them. It keys argIdx on the raw argv string, so when the API
// echoes the canonical form the lookup misses and the pre-sized result slot
// stays zero-valued — rendering a blank row that reads as "taken", with exit 0.
//
// `namecom domain check Example.COM` is a completely ordinary thing to type.
func TestCheck_NormalizesDomainArgs(t *testing.T) {
	avail := true
	price := 12.99

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "zonecheck"):
			// The API answers with the canonical lowercase name.
			results := []*coreapigo.ZoneCheckResult{{DomainName: "example.com", Available: &avail}}
			_ = json.NewEncoder(w).Encode(coreapigo.ZoneCheckResponse{Results: results, Total: 1})
		case strings.Contains(r.URL.Path, "getPricing"):
			_ = json.NewEncoder(w).Encode(coreapigo.PricingResponse{PurchasePrice: &price})
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
			http.Error(w, "unexpected", http.StatusInternalServerError)
		}
	}))
	t.Cleanup(srv.Close)

	cmd, buf := cmdForCheckJSON(t, srv)
	if err := runCheck(cmd, []string{"Example.COM"}); err != nil {
		t.Fatalf("runCheck: %v", err)
	}

	var got []*coreapigo.SearchResult
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, buf.String())
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 result, got %d: %s", len(got), buf.String())
	}
	if got[0].DomainName == "" {
		t.Fatalf("blank result row — the mixed-case argument never matched the API's canonical name: %s", buf.String())
	}
	if !got[0].Purchasable {
		t.Errorf("Example.COM is available but was reported as taken: %s", buf.String())
	}
}

// TestCheck_MatchesPunycodeResponse extends the normalization guard to IDNs.
// The spec says punycode "is normalized server-side, so either ASCII or UTF-8
// is accepted" on input, but results come back "in its canonical (ASCII /
// punycode) form". runCheck keys its result slots by the argument string, so a
// Unicode argument never matches the punycode reply — the same blank-row,
// exit-0 failure as the mixed-case bug, and lowercasing alone does not fix it.
func TestCheck_MatchesPunycodeResponse(t *testing.T) {
	avail := true
	price := 29.99

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "zonecheck"):
			// Canonical punycode reply for the Unicode name we asked about.
			results := []*coreapigo.ZoneCheckResult{{DomainName: "xn--caf-dma.com", Available: &avail}}
			_ = json.NewEncoder(w).Encode(coreapigo.ZoneCheckResponse{Results: results, Total: 1})
		case strings.Contains(r.URL.Path, "getPricing"):
			_ = json.NewEncoder(w).Encode(coreapigo.PricingResponse{PurchasePrice: &price})
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
			http.Error(w, "unexpected", http.StatusInternalServerError)
		}
	}))
	t.Cleanup(srv.Close)

	cmd, buf := cmdForCheckJSON(t, srv)
	if err := runCheck(cmd, []string{"café.com"}); err != nil {
		t.Fatalf("runCheck: %v", err)
	}

	var got []*coreapigo.SearchResult
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, buf.String())
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 result, got %d: %s", len(got), buf.String())
	}
	if got[0].DomainName == "" {
		t.Fatalf("blank row — the Unicode argument never matched the punycode reply: %s", buf.String())
	}
	if !got[0].Purchasable {
		t.Errorf("café.com is available but was reported as taken: %s", buf.String())
	}
}

// TestArgMatcher covers the resolver that replaced local punycode encoding.
// The API replies in canonical form, which may not be spelled the way the user
// typed it; rather than depend on golang.org/x/net/idna to re-encode locally,
// unrecognized replies are resolved by elimination.
//
// The conservative half matters most: pairing is only safe when exactly one
// argument is outstanding. Guessing among several would report one domain's
// availability under another's name — strictly worse than the blank row it
// would be replacing.
func TestArgMatcher(t *testing.T) {
	t.Run("exact names match", func(t *testing.T) {
		m := newArgMatcher([]string{"a.com", "b.com"})
		if i, ok := m.match("b.com"); !ok || i != 1 {
			t.Errorf("expected index 1, got %d ok=%v", i, ok)
		}
		if i, ok := m.match("a.com"); !ok || i != 0 {
			t.Errorf("expected index 0, got %d ok=%v", i, ok)
		}
	})

	t.Run("single unrecognized reply resolves by elimination", func(t *testing.T) {
		// café.com was sent as UTF-8; the API replies in punycode.
		m := newArgMatcher([]string{"café.com"})
		i, ok := m.match("xn--caf-dma.com")
		if !ok || i != 0 {
			t.Fatalf("a lone outstanding argument should absorb the reply, got %d ok=%v", i, ok)
		}
		// Idempotent: the pricing pass looks the same name up again.
		if j, ok2 := m.match("xn--caf-dma.com"); !ok2 || j != i {
			t.Errorf("second lookup must resolve identically, got %d ok=%v", j, ok2)
		}
	})

	t.Run("mixed: exact matches narrow it down to one", func(t *testing.T) {
		m := newArgMatcher([]string{"a.com", "café.com"})
		if _, ok := m.match("a.com"); !ok {
			t.Fatal("exact match failed")
		}
		// Only café.com is left, so the punycode reply is unambiguous.
		if i, ok := m.match("xn--caf-dma.com"); !ok || i != 1 {
			t.Errorf("expected index 1 after elimination, got %d ok=%v", i, ok)
		}
	})

	t.Run("ambiguous replies are refused, not guessed", func(t *testing.T) {
		m := newArgMatcher([]string{"café.com", "münchen.de"})
		if _, ok := m.match("xn--caf-dma.com"); ok {
			t.Error("with two outstanding arguments the pairing is ambiguous and must be refused")
		}
	})

	t.Run("unrequested domain is rejected once all are claimed", func(t *testing.T) {
		m := newArgMatcher([]string{"a.com"})
		if _, ok := m.match("a.com"); !ok {
			t.Fatal("exact match failed")
		}
		if _, ok := m.match("unrelated.com"); ok {
			t.Error("a reply about a domain we never asked about must not claim a slot")
		}
	})
}

// TestInlineRegister_ChecksTrademarkClaims closes a bypass of the legal gate.
// `domain register` refuses to register a TMCH-matched name without an explicit
// acknowledgement — and deliberately will not let --yes satisfy it. But
// `domain check <domain>` offers to register the domain it just found available
// and calls inlineRegister, which POSTed to /core/v1/domains with no claims
// check and no Claims field at all.
//
// Same purchase, same legal notice requirement, no gate.
func TestInlineRegister_ChecksTrademarkClaims(t *testing.T) {
	defer output.StubInteractive(false)()

	var created bool
	var claimsChecked bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "claims"):
			claimsChecked = true
			_, _ = w.Write([]byte(`{
			  "domain":"tiktok.page","claimsProcessActive":true,
			  "claimId":"abc123","notBefore":"2026-01-01T00:00:00Z","notAfter":"2026-12-31T00:00:00Z",
			  "claimsNotice":"**This domain may infringe on a trademark claim.**",
			  "claims":[{"trademark":"TIKTOK","jurisdiction":"US"}]
			}`))
		default:
			created = true
			_ = json.NewEncoder(w).Encode(coreapigo.CreateDomainResponse{})
		}
	}))
	t.Cleanup(srv.Close)

	cmd := cmdForCheck(t, srv)
	price := 12.99
	err := inlineRegister(cmd, &coreapigo.SearchResult{
		DomainName: "tiktok.page", Purchasable: true, PurchasePrice: &price,
	})

	if !claimsChecked {
		t.Error("the inline register path must check for trademark claims like 'domain register' does")
	}
	if err == nil {
		t.Error("a claimed domain must not be registered without an acknowledgement")
	}
	if created {
		t.Error("BYPASS: a TMCH-matched domain was purchased with no claim notice and no acknowledgement")
	}
}

// TestArgMatcher_DefectsFoundInReview covers four failure modes an adversarial
// review found in the elimination heuristic. Each ends in a silently wrong
// result with exit 0, which is the same class as the blank-row bug the matcher
// was written to fix.
func TestArgMatcher_DefectsFoundInReview(t *testing.T) {
	t.Run("duplicate arguments leave the extra slot unclaimed, not mis-claimable", func(t *testing.T) {
		// `check a.com a.com` is degenerate input. Idempotency wins over
		// distributing duplicates: the zone pass and the pricing pass both look
		// up the same API name and MUST resolve to the same slot, so a repeated
		// lookup returns the cached index rather than advancing to the next
		// duplicate. The leftover slot must then stay unclaimed — if elimination
		// could later hand it to some unrelated reply, that reply's result would
		// be rendered as though it answered the user's second argument.
		m := newArgMatcher([]string{"a.com", "a.com"})
		i, ok := m.match("a.com")
		if !ok || i != 0 {
			t.Fatalf("first lookup should claim slot 0, got %d ok=%v", i, ok)
		}
		if j, ok2 := m.match("a.com"); !ok2 || j != i {
			t.Errorf("repeated lookup must be idempotent, got %d want %d", j, i)
		}
		// The duplicate slot is reported so runCheck can name it rather than
		// emitting a blank row.
		if got := m.unclaimed(); len(got) != 1 || got[0] != 1 {
			t.Errorf("expected slot 1 to be reported unclaimed, got %v", got)
		}
		// And it must not be claimable by an unrelated ASCII reply.
		if _, ok := m.match("something-else.com"); ok {
			t.Error("an unrelated reply claimed the leftover duplicate slot")
		}
	})

	t.Run("an unrequested reply never claims an ASCII argument", func(t *testing.T) {
		// The API returning a name we did not ask about must not be assumed to
		// be one of ours. b.com is plain ASCII: if it had been normalized we
		// would have matched it exactly, so an unmatched reply is an anomaly,
		// not a canonicalization.
		m := newArgMatcher([]string{"a.com", "b.com"})
		if _, ok := m.match("a.com"); !ok {
			t.Fatal("exact match failed")
		}
		if _, ok := m.match("a.co"); ok {
			t.Error("an unrequested reply claimed the slot of a domain the user asked about")
		}
	})

	t.Run("elimination still works for a non-ASCII argument", func(t *testing.T) {
		// The case the heuristic exists for: we cannot compute café.com's
		// punycode locally, so an unmatched reply plausibly IS its canonical form.
		m := newArgMatcher([]string{"a.com", "café.com"})
		if _, ok := m.match("a.com"); !ok {
			t.Fatal("exact match failed")
		}
		if i, ok := m.match("xn--caf-dma.com"); !ok || i != 1 {
			t.Errorf("expected the IDN slot, got %d ok=%v", i, ok)
		}
	})

	t.Run("two unresolvable IDNs are refused rather than swapped", func(t *testing.T) {
		m := newArgMatcher([]string{"café.com", "résumé.com"})
		if _, ok := m.match("xn--caf-dma.com"); ok {
			t.Error("with two IDN arguments outstanding the pairing is ambiguous and must be refused")
		}
	})
}

// TestCheck_UnverifiedDomainIsNotReportedAsTaken is the safety net that makes
// the above survivable. Whatever the matcher can or cannot resolve, a domain
// the CLI never got an answer for must never render as "taken" — that is the
// original bug, and reporting an available domain as unavailable is the
// expensive direction to be wrong in.
func TestCheck_UnverifiedDomainIsNotReportedAsTaken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "zonecheck"):
			// Two IDN args, replies in canonical form: unresolvable by elimination.
			_, _ = w.Write([]byte(`{"results":[
			  {"domainName":"xn--caf-dma.com","available":true},
			  {"domainName":"xn--rsum-bpad.com","available":true}
			],"total":2}`))
		case strings.Contains(r.URL.Path, "checkAvailability"):
			_, _ = w.Write([]byte(`{"results":[]}`))
		default:
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	t.Cleanup(srv.Close)

	cmd, buf := cmdForCheckJSON(t, srv)
	if err := runCheck(cmd, []string{"café.com", "résumé.com"}); err != nil {
		t.Fatalf("runCheck: %v", err)
	}

	var got []*coreapigo.SearchResult
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, buf.String())
	}
	for i, r := range got {
		if r.DomainName == "" {
			t.Errorf("result %d is a blank row — an unchecked domain must still be named: %s", i, buf.String())
		}
	}
}

// TestRenderSearchResults_PremiumColumnAlwaysAnswers pins that the PREMIUM
// column states an answer rather than going blank.
//
// The column rendered "" for every non-premium domain, so `domain check` on an
// ordinary name printed an empty cell — indistinguishable from a column that
// had failed to render, and the only boolean in the CLI that did not use
// BoolBadge. Premium is not cosmetic: the SDK documents that when it is true,
// purchasePrice must be passed on Create Domain, so "we didn't say" and "no"
// are different answers to the user.
//
// SearchResult.Premium is a *bool with three states, and the SDK documents the
// field as "only returned for purchasable domains" with `omitempty` on the
// wire. So for a purchasable domain an absent premium means false, while for
// an unpurchasable one the question does not arise.
func TestRenderSearchResults_PremiumColumnAlwaysAnswers(t *testing.T) {
	premium := true
	notPremium := false
	price := 17.99

	tests := []struct {
		name    string
		result  *coreapigo.SearchResult
		want    string
		notWant string
	}{
		{
			name:   "premium true says yes",
			result: &coreapigo.SearchResult{DomainName: "gold.com", Purchasable: true, PurchasePrice: &price, Premium: &premium},
			want:   "yes",
		},
		{
			name:    "premium false says no",
			result:  &coreapigo.SearchResult{DomainName: "plain.com", Purchasable: true, PurchasePrice: &price, Premium: &notPremium},
			want:    "no",
			notWant: "yes",
		},
		{
			// The reported case: CheckAvailability omits premium entirely for
			// an ordinary purchasable domain.
			name:    "premium omitted on a purchasable domain says no",
			result:  &coreapigo.SearchResult{DomainName: "beeeers.com", Purchasable: true, PurchasePrice: &price},
			want:    "no",
			notWant: "yes",
		},
		{
			// Premium is a property of a purchase that cannot be made, so
			// neither yes nor no is true. Matches the PRICE column, which
			// already renders an em dash for an unpurchasable domain.
			name:    "unpurchasable domain says neither",
			result:  &coreapigo.SearchResult{DomainName: "taken.com", Purchasable: false},
			want:    "—",
			notWant: "yes",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			out := outWithFormat(output.FormatTable, &buf)
			if err := renderSearchResults(out, []*coreapigo.SearchResult{tc.result}); err != nil {
				t.Fatalf("renderSearchResults: %v", err)
			}
			got := buf.String()

			// The PREMIUM cell is the last column; isolate the data row so the
			// assertion cannot be satisfied by the header or the hint line.
			var row string
			for _, line := range strings.Split(got, "\n") {
				if strings.Contains(line, tc.result.DomainName) {
					row = line
					break
				}
			}
			if row == "" {
				t.Fatalf("no row rendered for %s:\n%s", tc.result.DomainName, got)
			}
			premiumCell := strings.TrimSpace(lastCell(row))
			if premiumCell != tc.want {
				t.Errorf("PREMIUM cell = %q, want %q\nfull row: %s", premiumCell, tc.want, row)
			}
			if tc.notWant != "" && premiumCell == tc.notWant {
				t.Errorf("PREMIUM cell must not be %q", tc.notWant)
			}
		})
	}
}

// lastCell returns the final table cell of a rendered row.
func lastCell(row string) string {
	cells := strings.Split(strings.Trim(strings.TrimSpace(row), "│"), "│")
	if len(cells) == 0 {
		return ""
	}
	return cells[len(cells)-1]
}
