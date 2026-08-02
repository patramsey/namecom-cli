package domain

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/patramsey/namecom-cli/cmd/cmdutil"
	"github.com/patramsey/namecom-cli/internal/api"
	"github.com/patramsey/namecom-cli/internal/api/gen"
	"github.com/patramsey/namecom-cli/internal/output"
	"github.com/spf13/cobra"
)

func neverCalledServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("API should not be called for pre-flight validation failure: %s %s", r.Method, r.URL)
		http.Error(w, "unexpected call", http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func baseCmd(t *testing.T, srv *httptest.Server) *cobra.Command {
	t.Helper()
	client, err := api.New(api.Options{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("api.New: %v", err)
	}
	out := &output.Config{
		Format:  output.FormatTable,
		Color:   output.ColorNever,
		Writer:  &bytes.Buffer{},
		EWriter: &bytes.Buffer{},
	}
	cmd := &cobra.Command{}
	ctx := context.WithValue(context.Background(), cmdutil.KeyOutput, out)
	ctx = context.WithValue(ctx, cmdutil.KeyClient, client)
	cmd.SetContext(ctx)
	return cmd
}

// ---- set-ns -----------------------------------------------------------------

func cmdForSetNS(t *testing.T, srv *httptest.Server) *cobra.Command {
	cmd := baseCmd(t, srv)
	cmd.Flags().StringVar(&setNSList, "ns", "", "")
	return cmd
}

// contentTypeServer returns a server that asserts Content-Type: application/json
// on every request and returns a minimal 200 response.
func contentTypeServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("expected Content-Type: application/json, got %q for %s %s", ct, r.Method, r.URL)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func cmdForToggle(t *testing.T, srv *httptest.Server) *cobra.Command {
	t.Helper()
	cmd := baseCmd(t, srv)
	var yes bool
	cmd.PersistentFlags().BoolVarP(&yes, "yes", "y", false, "")
	if err := cmd.PersistentFlags().Set("yes", "true"); err != nil {
		t.Fatalf("setting yes flag: %v", err)
	}
	return cmd
}

func TestContentTypeHeader_AllToggleCommands(t *testing.T) {
	tests := []struct {
		name string
		run  func(*cobra.Command) error
	}{
		{"lock on", func(cmd *cobra.Command) error { return runLock(cmd, []string{"on", "example.com"}) }},
		{"lock off", func(cmd *cobra.Command) error { return runLock(cmd, []string{"off", "example.com"}) }},
		{"autorenew on", func(cmd *cobra.Command) error { return runAutorenew(cmd, []string{"on", "example.com"}) }},
		{"autorenew off", func(cmd *cobra.Command) error { return runAutorenew(cmd, []string{"off", "example.com"}) }},
		{"privacy on", func(cmd *cobra.Command) error { return runPrivacy(cmd, []string{"on", "example.com"}) }},
		{"privacy off", func(cmd *cobra.Command) error { return runPrivacy(cmd, []string{"off", "example.com"}) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := cmdForToggle(t, contentTypeServer(t))
			if err := tt.run(cmd); err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestSetNS_InvalidNameserver(t *testing.T) {
	tests := []struct {
		desc, ns    string
		errContains string
	}{
		{"no dot", "ns1nodot", "fully-qualified"},
		{"empty entry", "ns1.example.com,", "empty"},
		{"leading hyphen", "-ns1.example.com", "hyphen"},
		{"leading dot", ".ns1.example.com", "dot"},
	}
	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			srv := neverCalledServer(t)
			cmd := cmdForSetNS(t, srv)
			if err := cmd.ParseFlags([]string{"--ns", tt.ns}); err != nil {
				t.Fatalf("ParseFlags: %v", err)
			}
			err := runSetNS(cmd, []string{"example.com"})
			if err == nil {
				t.Fatalf("expected error for NS %q, got nil", tt.ns)
			}
			if tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
				t.Errorf("expected %q in error, got: %v", tt.errContains, err)
			}
		})
	}
}

func TestSetNS_BadDomainArg(t *testing.T) {
	srv := neverCalledServer(t)
	cmd := cmdForSetNS(t, srv)
	if err := cmd.ParseFlags([]string{"--ns", "ns1.example.com"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	err := runSetNS(cmd, []string{"nodot"})
	if err == nil {
		t.Fatal("expected error for domain without dot, got nil")
	}
}

func TestSetNS_Success(t *testing.T) {
	var receivedPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)

	cmd := cmdForSetNS(t, srv)
	if err := cmd.ParseFlags([]string{"--ns", "ns1.example.com,ns2.example.com"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	if err := runSetNS(cmd, []string{"example.com"}); err != nil {
		t.Fatalf("runSetNS: %v", err)
	}
	if !strings.Contains(receivedPath, "example.com") {
		t.Errorf("expected 'example.com' in request path, got: %q", receivedPath)
	}
}

// ---- pricing ----------------------------------------------------------------

func cmdForPricing(t *testing.T, srv *httptest.Server) *cobra.Command {
	t.Helper()
	client, err := api.New(api.Options{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("api.New: %v", err)
	}
	out := &output.Config{Format: output.FormatTable, Color: output.ColorNever, Writer: &bytes.Buffer{}, EWriter: &bytes.Buffer{}}
	cmd := &cobra.Command{}
	ctx := context.WithValue(context.Background(), cmdutil.KeyOutput, out)
	ctx = context.WithValue(ctx, cmdutil.KeyClient, client)
	cmd.SetContext(ctx)
	return cmd
}

func TestPricing_BadDomain(t *testing.T) {
	srv := neverCalledServer(t)
	cmd := cmdForPricing(t, srv)
	if err := runPricing(cmd, []string{"nodot"}); err == nil {
		t.Fatal("expected error for domain without dot, got nil")
	}
}

func TestPricing_Success(t *testing.T) {
	purchase, renewal, transfer := 12.99, 14.99, 9.99
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(gen.PricingResponseSchema{
			PurchasePrice: &purchase,
			RenewalPrice:  &renewal,
			TransferPrice: &transfer,
		})
	}))
	t.Cleanup(srv.Close)

	var stdout bytes.Buffer
	client, _ := api.New(api.Options{BaseURL: srv.URL})
	out := &output.Config{Format: output.FormatTable, Color: output.ColorNever, Writer: &stdout, EWriter: &bytes.Buffer{}}
	cmd := &cobra.Command{}
	ctx := context.WithValue(context.Background(), cmdutil.KeyOutput, out)
	ctx = context.WithValue(ctx, cmdutil.KeyClient, client)
	cmd.SetContext(ctx)

	if err := runPricing(cmd, []string{"example.com"}); err != nil {
		t.Fatalf("runPricing: %v", err)
	}
	if !strings.Contains(stdout.String(), "12.99") {
		t.Errorf("expected purchase price in output, got: %q", stdout.String())
	}
}

// ---- register years ---------------------------------------------------------

func cmdForRegister(t *testing.T, srv *httptest.Server) *cobra.Command {
	t.Helper()
	cmd := baseCmd(t, srv)
	cmd.Flags().IntVar(&registerYears, "years", 1, "")
	cmd.Flags().BoolVar(&registerPrivacy, "privacy", false, "")
	cmd.Flags().BoolVar(&registerAutorenew, "autorenew", false, "")
	cmd.Flags().StringVar(&registerContactsFile, "contacts-file", "", "")
	cmd.Flags().Float64Var(&registerPrice, "price", 0, "")
	cmd.Flags().BoolVar(&registerAckClaim, "acknowledge-claim", false, "")
	cmd.Flags().StringArrayVar(&registerTLDReqs, "tld-requirement", nil, "")
	t.Cleanup(func() { registerAckClaim = false; registerTLDReqs = nil })
	var yes bool
	cmd.PersistentFlags().BoolVarP(&yes, "yes", "y", false, "")
	return cmd
}

// availabilityServer returns a server that responds to the CheckAvailability
// endpoint with a single result whose Purchasable field matches the given value.
// Any other request causes the test to fail.
func availabilityServer(t *testing.T, domain string, purchasable bool) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/core/v1/domains:checkAvailability" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
			http.Error(w, "unexpected", http.StatusInternalServerError)
			return
		}
		price := 12.99
		results := []gen.SearchResult{{DomainName: domain, Purchasable: purchasable, PurchasePrice: &price}}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(gen.SearchResponseSchema{Results: &results})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestRegister_UnavailableDomain(t *testing.T) {
	srv := availabilityServer(t, "taken.com", false)
	cmd := cmdForRegister(t, srv)
	err := runRegister(cmd, []string{"taken.com"})
	if err == nil {
		t.Fatal("expected error for unavailable domain, got nil")
	}
	if !strings.Contains(err.Error(), "not available") {
		t.Errorf("expected 'not available' in error, got: %v", err)
	}
}

func TestRegister_AvailabilityCheckedBeforeForm(t *testing.T) {
	// When the domain is available the flow continues past the availability check
	// and reaches the pricing endpoint. We return 500 there to stop execution.
	// Assertions: CheckAvailability was called, and the error is the expected
	// pricing failure — not a false "not available" rejection.
	var checkCalled bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/core/v1/domains:checkAvailability" {
			checkCalled = true
			price := 12.99
			results := []gen.SearchResult{{DomainName: "free.com", Purchasable: true, PurchasePrice: &price}}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(gen.SearchResponseSchema{Results: &results})
			return
		}
		// Stop at pricing — we don't need to simulate the full flow.
		http.Error(w, `{"message":"stop"}`, http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	cmd := cmdForRegister(t, srv)
	err := runRegister(cmd, []string{"free.com"})
	if !checkCalled {
		t.Error("CheckAvailability endpoint was never called")
	}
	if err == nil {
		t.Error("expected error from pricing stub, got nil")
	}
	if strings.Contains(err.Error(), "not available") {
		t.Errorf("available domain incorrectly rejected: %v", err)
	}
	// "stop" is the sentinel message our stub returns for post-check endpoints,
	// confirming execution passed the availability gate and reached pricing.
	if !strings.Contains(err.Error(), "stop") {
		t.Errorf("expected sentinel error from pricing stub, got: %v", err)
	}
}

// TestRegister_DryRunMakesReadsButNeverWrites pins the rule --dry-run follows:
// perform the read-only lookups, never the write.
//
// This replaces TestRegister_DryRunSkipsAvailabilityCheck, which asserted that
// dry-run skipped CheckAvailability — while its own comment noted that pricing
// WAS still fetched. Both are read-only; skipping one and not the other was
// arbitrary, and the consequence was a preview missing purchaseType and the
// aftermarket price, the very fields a dry run exists to reveal.
func TestRegister_DryRunMakesReadsButNeverWrites(t *testing.T) {
	var availChecked, pricingChecked, claimsChecked, created bool
	price := 12.99

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "checkAvailability"):
			availChecked = true
			results := []gen.SearchResult{{DomainName: "example.com", Purchasable: true, PurchasePrice: &price}}
			_ = json.NewEncoder(w).Encode(gen.SearchResponseSchema{Results: &results})
		case strings.Contains(r.URL.Path, "getPricing"):
			pricingChecked = true
			_ = json.NewEncoder(w).Encode(gen.PricingResponseSchema{PurchasePrice: &price})
		case strings.Contains(r.URL.Path, "claims"):
			claimsChecked = true
			_, _ = w.Write([]byte(`{"domain":"example.com","claimsProcessActive":false,"claimId":null,"claims":[]}`))
		default:
			created = true
			t.Error("CreateDomain must never be called in dry-run mode")
			http.Error(w, "unexpected", http.StatusInternalServerError)
		}
	}))
	t.Cleanup(srv.Close)

	cmd := cmdForRegister(t, srv)
	root := &cobra.Command{Use: "namecom"}
	var dr, yes bool
	root.PersistentFlags().BoolVar(&dr, "dry-run", false, "")
	root.PersistentFlags().BoolVarP(&yes, "yes", "y", false, "")
	for _, f := range []string{"dry-run", "yes"} {
		if err := root.PersistentFlags().Set(f, "true"); err != nil {
			t.Fatalf("setting %s: %v", f, err)
		}
	}
	root.AddCommand(cmd)

	if err := runRegister(cmd, []string{"example.com"}); err != nil {
		t.Fatalf("runRegister: %v", err)
	}

	if created {
		t.Error("dry-run performed a real registration")
	}
	for name, done := range map[string]bool{
		"availability": availChecked,
		"pricing":      pricingChecked,
		"claims":       claimsChecked,
	} {
		if !done {
			t.Errorf("dry-run skipped the read-only %s lookup, so the preview cannot be accurate", name)
		}
	}
}

// ---- toggle commands (lock / autorenew / privacy) ---------------------------

func TestToggle_BadValue(t *testing.T) {
	tests := []struct {
		name string
		run  func(*cobra.Command) error
	}{
		{"lock", func(cmd *cobra.Command) error { return runLock(cmd, []string{"yes", "example.com"}) }},
		{"autorenew", func(cmd *cobra.Command) error { return runAutorenew(cmd, []string{"true", "example.com"}) }},
		{"privacy", func(cmd *cobra.Command) error { return runPrivacy(cmd, []string{"enable", "example.com"}) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := neverCalledServer(t)
			cmd := cmdForToggle(t, srv)
			err := tt.run(cmd)
			if err == nil {
				t.Fatalf("expected error for non-on/off toggle value, got nil")
			}
			if !strings.Contains(err.Error(), "on") || !strings.Contains(err.Error(), "off") {
				t.Errorf("expected error to mention 'on' and 'off', got: %v", err)
			}
		})
	}
}

func TestToggle_BadDomain(t *testing.T) {
	tests := []struct {
		name string
		run  func(*cobra.Command) error
	}{
		{"lock", func(cmd *cobra.Command) error { return runLock(cmd, []string{"on", "nodot"}) }},
		{"autorenew", func(cmd *cobra.Command) error { return runAutorenew(cmd, []string{"on", "nodot"}) }},
		{"privacy", func(cmd *cobra.Command) error { return runPrivacy(cmd, []string{"on", "nodot"}) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := neverCalledServer(t)
			cmd := cmdForToggle(t, srv)
			err := tt.run(cmd)
			if err == nil {
				t.Fatalf("expected error for domain without dot, got nil")
			}
		})
	}
}

func TestToggle_DomainNormalized(t *testing.T) {
	tests := []struct {
		name string
		run  func(*cobra.Command, string) error
	}{
		{"lock on", func(cmd *cobra.Command, domain string) error { return runLock(cmd, []string{"on", domain}) }},
		{"autorenew on", func(cmd *cobra.Command, domain string) error { return runAutorenew(cmd, []string{"on", domain}) }},
		{"privacy on", func(cmd *cobra.Command, domain string) error { return runPrivacy(cmd, []string{"on", domain}) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var receivedPath string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				receivedPath = r.URL.Path
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{}`))
			}))
			t.Cleanup(srv.Close)

			cmd := cmdForToggle(t, srv)
			if err := tt.run(cmd, "EXAMPLE.COM"); err != nil {
				t.Fatalf("%s: %v", tt.name, err)
			}
			if strings.Contains(receivedPath, "EXAMPLE") {
				t.Errorf("%s: domain not normalized in path: %q", tt.name, receivedPath)
			}
			if !strings.Contains(receivedPath, "example.com") {
				t.Errorf("%s: expected 'example.com' in path, got: %q", tt.name, receivedPath)
			}
		})
	}
}

// ---- domain update ----------------------------------------------------------

func cmdForUpdate(t *testing.T, srv *httptest.Server) *cobra.Command {
	t.Helper()
	cmd := baseCmd(t, srv)
	cmd.Flags().Bool("autorenew", false, "")
	cmd.Flags().Bool("privacy", false, "")
	cmd.Flags().Bool("lock", false, "")
	return cmd
}

func TestDomainUpdate_NormalizesDomain(t *testing.T) {
	var writePath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPatch || r.Method == http.MethodPut {
			writePath = r.URL.Path
		}
		_ = json.NewEncoder(w).Encode(gen.DomainResponsePayload{DomainName: "example.com"})
	}))
	t.Cleanup(srv.Close)

	cmd := cmdForUpdate(t, srv)
	if err := runUpdate(cmd, []string{"EXAMPLE.COM"}); err != nil {
		t.Fatalf("runUpdate: %v", err)
	}
	if writePath == "" {
		t.Fatal("update request (PUT/PATCH) was never made")
	}
	if strings.Contains(writePath, "EXAMPLE") {
		t.Errorf("update path used raw args[0] instead of normalized domain: %q", writePath)
	}
	if !strings.Contains(writePath, "example.com") {
		t.Errorf("expected normalized domain in update path, got: %q", writePath)
	}
}

// ---- renew ------------------------------------------------------------------

func cmdForRenew(t *testing.T, srv *httptest.Server) *cobra.Command {
	t.Helper()
	cmd := baseCmd(t, srv)
	cmd.Flags().IntVar(&renewYears, "years", 1, "")
	cmd.Flags().Float64Var(&renewPrice, "price", 0, "")
	var yes bool
	cmd.PersistentFlags().BoolVarP(&yes, "yes", "y", false, "")
	if err := cmd.PersistentFlags().Set("yes", "true"); err != nil {
		t.Fatalf("setting yes flag: %v", err)
	}
	t.Cleanup(func() { renewYears = 1; renewPrice = 0 })
	return cmd
}

func TestRenew_BadDomain(t *testing.T) {
	srv := neverCalledServer(t)
	cmd := cmdForRenew(t, srv)
	err := runRenew(cmd, []string{"nodot"})
	if err == nil {
		t.Fatal("expected error for domain without dot, got nil")
	}
}

func TestRenew_YearsOutOfRange(t *testing.T) {
	for _, years := range []string{"0", "11"} {
		t.Run("years="+years, func(t *testing.T) {
			srv := neverCalledServer(t)
			cmd := cmdForRenew(t, srv)
			if err := cmd.ParseFlags([]string{"--years", years}); err != nil {
				t.Fatalf("ParseFlags: %v", err)
			}
			err := runRenew(cmd, []string{"example.com"})
			if err == nil {
				t.Fatalf("expected error for --years %s, got nil", years)
			}
			if !strings.Contains(err.Error(), "years") {
				t.Errorf("expected 'years' in error, got: %v", err)
			}
		})
	}
}

// renewServer serves the given pricing for the getPricing call and records the
// decoded body of the subsequent renew POST, so tests can assert on what we
// actually send rather than only that no error came back.
func renewServer(t *testing.T, pricing gen.PricingResponseSchema, got *map[string]any) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Path, "getPricing") {
			_ = json.NewEncoder(w).Encode(pricing)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(got); err != nil {
			t.Errorf("decoding renew body: %v", err)
		}
		_ = json.NewEncoder(w).Encode(gen.RenewDomainResponseSchema{})
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestRenew_PremiumSendsPurchasePrice guards a regression where runRenew built
// the body as {Years: &years} only. DomainsRenewDomainBody also carries
// PurchasePrice, documented "required if this is a premium domain" — so a
// premium renewal was quoted to the user at the right price, confirmed, then
// rejected by the API. runRegister already merged the price correctly; renew
// was the asymmetric path.
func TestRenew_PremiumSendsPurchasePrice(t *testing.T) {
	renewal := 2500.00
	var gotBody map[string]any
	srv := renewServer(t, gen.PricingResponseSchema{Premium: true, RenewalPrice: &renewal}, &gotBody)

	cmd := cmdForRenew(t, srv)
	if err := runRenew(cmd, []string{"premium.io"}); err != nil {
		t.Fatalf("runRenew: %v", err)
	}
	if gotBody == nil {
		t.Fatal("renew request was never sent")
	}
	got, ok := gotBody["purchasePrice"]
	if !ok {
		t.Fatalf("premium renewal must send purchasePrice, body was: %#v", gotBody)
	}
	if got != renewal {
		t.Errorf("expected purchasePrice %.2f, got %#v", renewal, got)
	}
}

// TestRenew_NonPremiumOmitsPurchasePrice pins the other side: standard renewals
// should not pin a price the user never confirmed.
func TestRenew_NonPremiumOmitsPurchasePrice(t *testing.T) {
	renewal := 19.99
	var gotBody map[string]any
	srv := renewServer(t, gen.PricingResponseSchema{Premium: false, RenewalPrice: &renewal}, &gotBody)

	cmd := cmdForRenew(t, srv)
	if err := runRenew(cmd, []string{"example.com"}); err != nil {
		t.Fatalf("runRenew: %v", err)
	}
	if gotBody == nil {
		t.Fatal("renew request was never sent")
	}
	if _, ok := gotBody["purchasePrice"]; ok {
		t.Errorf("standard renewal should omit purchasePrice, got: %#v", gotBody)
	}
}

// TestRenew_ExplicitPriceOverridesQuote covers the --price escape hatch, which
// renewCmd lacked entirely while registerCmd had one. It also matters when the
// quoted price and the price the user is willing to pay disagree.
func TestRenew_ExplicitPriceOverridesQuote(t *testing.T) {
	quoted := 2500.00
	confirmed := 1800.00
	var gotBody map[string]any
	srv := renewServer(t, gen.PricingResponseSchema{Premium: true, RenewalPrice: &quoted}, &gotBody)

	cmd := cmdForRenew(t, srv)
	if err := cmd.ParseFlags([]string{"--price", "1800"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	if err := runRenew(cmd, []string{"premium.io"}); err != nil {
		t.Fatalf("runRenew: %v", err)
	}
	if gotBody == nil {
		t.Fatal("renew request was never sent")
	}
	if got := gotBody["purchasePrice"]; got != confirmed {
		t.Errorf("--price should win over the quote: expected %.2f, got %#v", confirmed, got)
	}
}

// TestPricingQuoteUsesRequestedYears guards a regression where both runRegister
// and runRenew passed an empty GetPricingForDomainParams{}, ignoring --years.
// The API defaults to the minimum period, so `--years 3` quoted the 1-year
// price to the user and then sent that price alongside years:3 —
// CreateDomainRequest.Years explicitly warns "If passing purchasePrice make
// sure to adjust it accordingly." It is also simply wrong for TLDs whose
// minimum period isn't 1 year (.ai requires 2).
func TestPricingQuoteUsesRequestedYears(t *testing.T) {
	tests := []struct {
		name string
		run  func(*testing.T, *httptest.Server) error
	}{
		{
			name: "renew",
			run: func(t *testing.T, srv *httptest.Server) error {
				cmd := cmdForRenew(t, srv)
				if err := cmd.ParseFlags([]string{"--years", "3"}); err != nil {
					t.Fatalf("ParseFlags: %v", err)
				}
				return runRenew(cmd, []string{"example.com"})
			},
		},
		{
			name: "register",
			run: func(t *testing.T, srv *httptest.Server) error {
				cmd := cmdForRegister(t, srv)
				if err := cmd.PersistentFlags().Set("yes", "true"); err != nil {
					t.Fatalf("setting yes flag: %v", err)
				}
				if err := cmd.ParseFlags([]string{"--years", "3"}); err != nil {
					t.Fatalf("ParseFlags: %v", err)
				}
				return runRegister(cmd, []string{"example.com"})
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			price := 12.99
			var pricingYears string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch {
				case strings.Contains(r.URL.Path, "getPricing"):
					pricingYears = r.URL.Query().Get("years")
					_ = json.NewEncoder(w).Encode(gen.PricingResponseSchema{
						PurchasePrice: &price, RenewalPrice: &price,
					})
				case strings.Contains(r.URL.Path, "checkAvailability"):
					results := []gen.SearchResult{{DomainName: "example.com", Purchasable: true, PurchasePrice: &price}}
					_ = json.NewEncoder(w).Encode(gen.SearchResponseSchema{Results: &results})
				default:
					_, _ = w.Write([]byte(`{}`))
				}
			}))
			t.Cleanup(srv.Close)

			if err := tc.run(t, srv); err != nil {
				t.Fatalf("run: %v", err)
			}
			if pricingYears != "3" {
				t.Errorf("pricing call should request years=3, got %q", pricingYears)
			}
		})
	}
}

func TestRenew_DomainNormalized(t *testing.T) {
	price := 12.99
	var renewPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "getPricing"):
			_ = json.NewEncoder(w).Encode(gen.PricingResponseSchema{RenewalPrice: &price})
		default:
			renewPath = r.URL.Path
			_ = json.NewEncoder(w).Encode(gen.RenewDomainResponseSchema{})
		}
	}))
	t.Cleanup(srv.Close)

	cmd := cmdForRenew(t, srv)
	if err := runRenew(cmd, []string{"EXAMPLE.COM"}); err != nil {
		t.Fatalf("runRenew: %v", err)
	}
	if strings.Contains(renewPath, "EXAMPLE") {
		t.Errorf("domain not normalized in renew path: %q", renewPath)
	}
	if !strings.Contains(renewPath, "example.com") {
		t.Errorf("expected 'example.com' in renew path, got: %q", renewPath)
	}
}

// ---- contacts set -----------------------------------------------------------

func cmdForContactsSet(t *testing.T, srv *httptest.Server, contactsJSON string) *cobra.Command {
	t.Helper()
	dir := t.TempDir()
	f := filepath.Join(dir, "contacts.json")
	if err := os.WriteFile(f, []byte(contactsJSON), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	contactsFile = f
	t.Cleanup(func() { contactsFile = "" })

	cmd := baseCmd(t, srv)
	cmd.Flags().StringVar(&contactsFile, "from-file", f, "")
	return cmd
}

func TestContactsSet_BadDomain(t *testing.T) {
	srv := neverCalledServer(t)
	cmd := cmdForContactsSet(t, srv, `{}`)
	err := runContactsSet(cmd, []string{"nodot"})
	if err == nil {
		t.Fatal("expected error for domain without dot, got nil")
	}
}

func TestContactsSet_DomainNormalized(t *testing.T) {
	var receivedPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)

	cmd := cmdForContactsSet(t, srv, `{}`)
	if err := runContactsSet(cmd, []string{"EXAMPLE.COM"}); err != nil {
		t.Fatalf("runContactsSet: %v", err)
	}
	if strings.Contains(receivedPath, "EXAMPLE") {
		t.Errorf("domain not normalized in contacts path: %q", receivedPath)
	}
	if !strings.Contains(receivedPath, "example.com") {
		t.Errorf("expected 'example.com' in contacts path, got: %q", receivedPath)
	}
}

func TestContactsSet_BadFile(t *testing.T) {
	srv := neverCalledServer(t)
	cmd := baseCmd(t, srv)
	contactsFile = "/nonexistent/path/contacts.json"
	t.Cleanup(func() { contactsFile = "" })
	cmd.Flags().StringVar(&contactsFile, "from-file", contactsFile, "")

	err := runContactsSet(cmd, []string{"example.com"})
	if err == nil {
		t.Fatal("expected error for missing contacts file, got nil")
	}
}

func TestContactsSet_InvalidJSON(t *testing.T) {
	srv := neverCalledServer(t)
	cmd := cmdForContactsSet(t, srv, `not json`)
	err := runContactsSet(cmd, []string{"example.com"})
	if err == nil {
		t.Fatal("expected error for invalid JSON contacts file, got nil")
	}
	if !strings.Contains(err.Error(), "parsing") {
		t.Errorf("expected 'parsing' in error, got: %v", err)
	}
}

// ---- auth-code --------------------------------------------------------------

func TestAuthCode_BadDomain(t *testing.T) {
	srv := neverCalledServer(t)
	cmd := baseCmd(t, srv)
	err := runAuthCode(cmd, []string{"nodot"})
	if err == nil {
		t.Fatal("expected error for domain without dot, got nil")
	}
}

func TestAuthCode_DomainNormalized(t *testing.T) {
	var receivedPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(gen.AuthCodeResponseSchema{AuthCode: "SECRET123"})
	}))
	t.Cleanup(srv.Close)

	cmd := baseCmd(t, srv)
	if err := runAuthCode(cmd, []string{"EXAMPLE.COM"}); err != nil {
		t.Fatalf("runAuthCode: %v", err)
	}
	if strings.Contains(receivedPath, "EXAMPLE") {
		t.Errorf("domain not normalized in auth-code path: %q", receivedPath)
	}
	if !strings.Contains(receivedPath, "example.com") {
		t.Errorf("expected 'example.com' in auth-code path, got: %q", receivedPath)
	}
}

// ---- contacts get -----------------------------------------------------------

func cmdForContactsGet(t *testing.T, srv *httptest.Server) *cobra.Command {
	t.Helper()
	client, err := api.New(api.Options{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("api.New: %v", err)
	}
	out := &output.Config{Format: output.FormatTable, Color: output.ColorNever, Writer: &bytes.Buffer{}, EWriter: &bytes.Buffer{}}
	cmd := &cobra.Command{}
	ctx := context.WithValue(context.Background(), cmdutil.KeyOutput, out)
	ctx = context.WithValue(ctx, cmdutil.KeyClient, client)
	cmd.SetContext(ctx)
	return cmd
}

func TestContactsGet_BadDomain(t *testing.T) {
	srv := neverCalledServer(t)
	cmd := cmdForContactsGet(t, srv)
	if err := runContactsGet(cmd, []string{"nodot"}); err == nil {
		t.Fatal("expected error for domain without dot, got nil")
	}
}

// TestContactsGet_Success pins that the contacts are actually rendered. The
// fixture must carry real contacts: with an empty payload the command prints
// "null" and passes, which is indistinguishable from rendering nothing at all.
//
// It also pins the quiet side of warnUnverifiedContacts — a fully verified
// domain must NOT be shown the registry-lock warning, or the warning stops
// meaning anything on the domains where it matters.
func TestContactsGet_Success(t *testing.T) {
	const payload = `{
	  "domainName": "example.com",
	  "contacts": {
	    "registrant": {"firstName":"Ada","lastName":"Lovelace","email":"ada@example.com","isVerified":true},
	    "admin": {"firstName":"Grace","lastName":"Hopper","email":"grace@example.com","isVerified":true}
	  }
	}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(payload))
	}))
	t.Cleanup(srv.Close)

	cmd := cmdForContactsGet(t, srv)
	if err := runContactsGet(cmd, []string{"example.com"}); err != nil {
		t.Fatalf("runContactsGet: %v", err)
	}

	buf, ok := cmdutil.Out(cmd).Writer.(*bytes.Buffer)
	if !ok {
		t.Fatal("output writer is not a *bytes.Buffer")
	}
	got := buf.String()
	for _, want := range []string{"ada@example.com", "Lovelace", "grace@example.com"} {
		if !strings.Contains(got, want) {
			t.Errorf("contacts output missing %q:\n%s", want, got)
		}
	}
	// The warning goes to stderr via WarnBox, so this has to read EWriter —
	// asserting its absence on stdout would pass no matter what was warned.
	ebuf, ok := cmdutil.Out(cmd).EWriter.(*bytes.Buffer)
	if !ok {
		t.Fatal("error writer is not a *bytes.Buffer")
	}
	if stderr := ebuf.String(); strings.Contains(strings.ToLower(stderr), "unverified") {
		t.Errorf("every contact is verified — no registry-lock warning should appear:\n%s", stderr)
	}
}

func TestRegister_YearsOutOfRange(t *testing.T) {
	for _, years := range []string{"0", "11", "100"} {
		t.Run("years="+years, func(t *testing.T) {
			srv := neverCalledServer(t)
			cmd := cmdForRegister(t, srv)
			// ParseFlags marks --years as Changed, triggering ValidYears.
			if err := cmd.ParseFlags([]string{"--years", years}); err != nil {
				t.Fatalf("ParseFlags: %v", err)
			}
			err := runRegister(cmd, []string{"example.com"})
			if err == nil {
				t.Fatalf("expected error for --years %s, got nil", years)
			}
			if !strings.Contains(err.Error(), "years") {
				t.Errorf("expected 'years' in error, got: %v", err)
			}
		})
	}
}

// checkThenCreateServer answers CheckAvailability with the given result and
// records the body of the subsequent CreateDomain POST.
func checkThenCreateServer(t *testing.T, result gen.SearchResult, got *map[string]any) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "checkAvailability"):
			results := []gen.SearchResult{result}
			_ = json.NewEncoder(w).Encode(gen.SearchResponseSchema{Results: &results})
		case strings.Contains(r.URL.Path, "getPricing"):
			_ = json.NewEncoder(w).Encode(gen.PricingResponseSchema{PurchasePrice: result.PurchasePrice})
		case strings.Contains(r.URL.Path, "claims"):
			// register now always checks for trademark claims; no claim here.
			_, _ = w.Write([]byte(`{"domain":"example.com","claimsProcessActive":false,"claimId":null,"claims":[]}`))
		default:
			if err := json.NewDecoder(r.Body).Decode(got); err != nil {
				t.Errorf("decoding create body: %v", err)
			}
			_ = json.NewEncoder(w).Encode(gen.CreateDomainResponseSchema{})
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestRegister_ForwardsPurchaseTypeAndPrice guards a gap where PurchaseType
// appeared nowhere in cmd/ at all, despite CreateDomainRequest documenting it
// as "should be copied from the result of either a Search or checkAvailability
// request". Aftermarket, expiring and backorder results were all submitted as
// plain registrations. PurchasePrice is documented as required when
// purchaseType is not "registration", so the two must travel together.
func TestRegister_ForwardsPurchaseTypeAndPrice(t *testing.T) {
	price := 450.00
	ptype := gen.SearchPurchaseType("aftermarket_b")
	var gotBody map[string]any
	srv := checkThenCreateServer(t, gen.SearchResult{
		DomainName: "example.com", Purchasable: true,
		PurchasePrice: &price, PurchaseType: &ptype,
	}, &gotBody)

	cmd := cmdForRegister(t, srv)
	if err := cmd.PersistentFlags().Set("yes", "true"); err != nil {
		t.Fatalf("setting yes flag: %v", err)
	}
	if err := runRegister(cmd, []string{"example.com"}); err != nil {
		t.Fatalf("runRegister: %v", err)
	}
	if gotBody == nil {
		t.Fatal("create request was never sent")
	}
	if got := gotBody["purchaseType"]; got != "aftermarket_b" {
		t.Errorf("expected purchaseType %q forwarded, got %#v", "aftermarket_b", got)
	}
	if got := gotBody["purchasePrice"]; got != price {
		t.Errorf("non-registration purchase requires a price: expected %.2f, got %#v", price, got)
	}
}

// TestRegister_PlainRegistrationOmitsPurchaseType pins the common case: a
// standard registration shouldn't start sending a redundant field, and a
// non-premium registration shouldn't pin a price.
func TestRegister_PlainRegistrationOmitsPurchaseType(t *testing.T) {
	price := 12.99
	ptype := gen.SearchPurchaseType("registration")
	var gotBody map[string]any
	srv := checkThenCreateServer(t, gen.SearchResult{
		DomainName: "example.com", Purchasable: true,
		PurchasePrice: &price, PurchaseType: &ptype,
	}, &gotBody)

	cmd := cmdForRegister(t, srv)
	if err := cmd.PersistentFlags().Set("yes", "true"); err != nil {
		t.Fatalf("setting yes flag: %v", err)
	}
	if err := runRegister(cmd, []string{"example.com"}); err != nil {
		t.Fatalf("runRegister: %v", err)
	}
	if _, ok := gotBody["purchaseType"]; ok {
		t.Errorf("plain registration should omit purchaseType, got: %#v", gotBody)
	}
	if _, ok := gotBody["purchasePrice"]; ok {
		t.Errorf("non-premium registration should omit purchasePrice, got: %#v", gotBody)
	}
}

// TestIdempotencyKeyFlagIsNotShadowed guards the second half of the
// double-charge bug. registerCmd defined its own --idempotency-key, which
// shadows the root persistent flag of the same name: pflag keeps the local one,
// so the root variable stayed empty and root.go minted a fresh uuid.New() per
// invocation. The user's key was therefore ignored no matter where it appeared
// on the command line. The root flag already applies to every subcommand, so a
// local redefinition can only break it.
func TestIdempotencyKeyFlagIsNotShadowed(t *testing.T) {
	for _, c := range []struct {
		name string
		cmd  *cobra.Command
	}{
		{"domain register", registerCmd},
		{"domain renew", renewCmd},
	} {
		t.Run(c.name, func(t *testing.T) {
			if f := c.cmd.Flags().Lookup("idempotency-key"); f != nil {
				t.Errorf("%s defines a local --idempotency-key that shadows the root persistent flag", c.name)
			}
		})
	}
}

// ---- dry-run / real request drift -------------------------------------------

var httpMethods = map[string]bool{
	"GET": true, "POST": true, "PUT": true, "PATCH": true, "DELETE": true,
}

func dryRunLine(t *testing.T, s string) string {
	t.Helper()
	for _, line := range strings.Split(s, "\n") {
		f := strings.Fields(line)
		if len(f) >= 2 && httpMethods[f[0]] && strings.HasPrefix(f[1], "/") {
			return f[0] + " " + f[1]
		}
	}
	t.Fatalf("no dry-run METHOD/path line found in output: %q", s)
	return ""
}

// domainDryRunCmd builds a command wired to srv, with a root carrying --yes and
// optionally --dry-run, plus whatever local flags the target command reads.
func domainDryRunCmd(t *testing.T, srv *httptest.Server, dryRun bool, register func(*cobra.Command)) *cobra.Command {
	t.Helper()
	cmd := baseCmd(t, srv)
	if register != nil {
		register(cmd)
	}
	root := &cobra.Command{Use: "namecom"}
	var dr, yes bool
	root.PersistentFlags().BoolVar(&dr, "dry-run", false, "")
	root.PersistentFlags().BoolVarP(&yes, "yes", "y", false, "")
	if err := root.PersistentFlags().Set("yes", "true"); err != nil {
		t.Fatalf("setting yes flag: %v", err)
	}
	if dryRun {
		if err := root.PersistentFlags().Set("dry-run", "true"); err != nil {
			t.Fatalf("setting dry-run flag: %v", err)
		}
	}
	root.AddCommand(cmd)
	return cmd
}

// TestDryRunMatchesRealRequest_Domain runs each mutating domain command twice —
// once with --dry-run to capture what we PRINT, once live to capture what we
// SEND — and asserts they agree.
//
// Eight of these nine had drifted. The name.com API uses `:verb` action suffixes
// (`:unlock`, `:enableAutorenew`) that are easy to guess wrong, and the
// hand-written dry-run strings had guessed wrong in every case except `lock on`.
// --dry-run exists so users can learn the API before scripting it with
// `namecom api`; every wrong line sent someone to a 404.
func TestDryRunMatchesRealRequest_Domain(t *testing.T) {
	const resp = `{"domainName":"example.com","locked":false,"autorenewEnabled":false,"privacyEnabled":false,"nameservers":["ns1.example.com"],"contacts":{}}`

	tests := []struct {
		name     string
		register func(*cobra.Command)
		args     []string
		run      func(*cobra.Command, []string) error
	}{
		{"lock on", nil, []string{"on", "example.com"}, runLock},
		{"lock off", nil, []string{"off", "example.com"}, runLock},
		{"autorenew on", nil, []string{"on", "example.com"}, runAutorenew},
		{"autorenew off", nil, []string{"off", "example.com"}, runAutorenew},
		{"privacy on", nil, []string{"on", "example.com"}, runPrivacy},
		{"privacy off", nil, []string{"off", "example.com"}, runPrivacy},
		{
			name:     "set-ns",
			register: func(c *cobra.Command) { c.Flags().StringVar(&setNSList, "ns", "ns1.example.com,ns2.example.com", "") },
			args:     []string{"example.com"},
			run:      runSetNS,
		},
		{
			// update is read-modify-write, so the live run issues GET then PATCH;
			// captureRealRequest keeps the last request, which is the mutation.
			name: "update",
			register: func(c *cobra.Command) {
				c.Flags().Bool("autorenew", false, "")
				c.Flags().Bool("privacy", false, "")
				c.Flags().Bool("lock", false, "")
				if err := c.Flags().Set("autorenew", "true"); err != nil {
					t.Fatalf("setting autorenew flag: %v", err)
				}
			},
			args: []string{"example.com"},
			run:  runUpdate,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dsrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(resp))
			}))
			t.Cleanup(dsrv.Close)
			dcmd := domainDryRunCmd(t, dsrv, true, tc.register)
			if err := tc.run(dcmd, tc.args); err != nil {
				t.Fatalf("dry-run invocation: %v", err)
			}
			buf, ok := cmdutil.Out(dcmd).Writer.(*bytes.Buffer)
			if !ok {
				t.Fatal("output writer is not a *bytes.Buffer")
			}
			printed := dryRunLine(t, buf.String())

			var last string
			lsrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				last = r.Method + " " + r.URL.Path
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(resp))
			}))
			t.Cleanup(lsrv.Close)
			lcmd := domainDryRunCmd(t, lsrv, false, tc.register)
			if err := tc.run(lcmd, tc.args); err != nil {
				t.Fatalf("live invocation: %v", err)
			}
			if last == "" {
				t.Fatal("no request was made")
			}

			if printed != last {
				t.Errorf("--dry-run reports %q but the command actually sends %q", printed, last)
			}
		})
	}
}

// TestQuietMode_DetailCommands guards a scripting gap: --quiet was implemented
// only in list commands. Every detail command ignored it and printed a full
// table, so the obvious scripting invocations did not work.
//
// `domain auth-code <domain> -q` is the clearest case — the whole point is to
// capture the code into a variable for a transfer:
//
//	CODE=$(namecom domain auth-code example.com -q)
//
// Instead it emitted a bordered table.
func TestQuietMode_DetailCommands(t *testing.T) {
	tests := []struct {
		name string
		resp string
		run  func(*cobra.Command, []string) error
		args []string
		want string
	}{
		{
			name: "auth-code prints just the code",
			resp: `{"authCode":"SECRET-EPP-CODE"}`,
			run:  runAuthCode,
			args: []string{"example.com"},
			want: "SECRET-EPP-CODE",
		},
		{
			name: "domain get prints just the name",
			resp: `{"domainName":"example.com","locked":true}`,
			run:  runGet,
			args: []string{"example.com"},
			want: "example.com",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(tc.resp))
			}))
			t.Cleanup(srv.Close)

			client, err := api.New(api.Options{BaseURL: srv.URL})
			if err != nil {
				t.Fatalf("api.New: %v", err)
			}
			var buf bytes.Buffer
			out := &output.Config{
				Format: output.FormatTable, Color: output.ColorNever,
				QuietMode: true, Writer: &buf, EWriter: &bytes.Buffer{},
			}
			cmd := &cobra.Command{}
			ctx := context.WithValue(context.Background(), cmdutil.KeyOutput, out)
			ctx = context.WithValue(ctx, cmdutil.KeyClient, client)
			cmd.SetContext(ctx)

			if err := tc.run(cmd, tc.args); err != nil {
				t.Fatalf("run: %v", err)
			}
			got := strings.TrimSpace(buf.String())
			if got != tc.want {
				t.Errorf("--quiet should print exactly %q, got %q", tc.want, got)
			}
		})
	}
}

// TestList_SortAcceptsAnyServerSideField reverts a client-side allowlist that
// was invented rather than derived.
//
// namecom.api.yaml:349-353 declares `sort` as a bare `type: string` with no
// enum — "Sort specifies which domain property to order by" — and no list of
// permitted fields appears anywhere in the spec. The CLI had been rejecting
// everything outside a guessed set of three, so any other field the server
// supports was blocked by the client with an error asserting it was invalid.
// The server is the authority here.
func TestList_SortAcceptsAnyServerSideField(t *testing.T) {
	var gotSort string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotSort = r.URL.Query().Get("sort")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"domains":[],"totalCount":0,"nextPage":0}`))
	}))
	t.Cleanup(srv.Close)

	cmd := baseCmd(t, srv)
	cmd.Flags().StringVar(&listSort, "sort", "", "")
	cmd.Flags().StringVar(&listSortDir, "sort-dir", "", "")
	cmd.Flags().Int32Var(&listPage, "page", 1, "")
	t.Cleanup(func() { listSort = ""; listSortDir = ""; listPage = 1 })
	if err := cmd.ParseFlags([]string{"--sort", "renewalPrice"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}

	if err := runList(cmd, nil); err != nil {
		t.Fatalf("a sort field outside the old guessed set must reach the server: %v", err)
	}
	if gotSort != "renewalPrice" {
		t.Errorf("sort field should be forwarded verbatim, got %q", gotSort)
	}
}

// TestList_SortDirection covers the `dir` query parameter, which the spec
// documents ("Possible values are 'asc' (default) or 'desc'") but the CLI never
// exposed — making "soonest expiring first" unreachable.
func TestList_SortDirection(t *testing.T) {
	t.Run("forwarded when set", func(t *testing.T) {
		var gotDir string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotDir = r.URL.Query().Get("dir")
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"domains":[],"totalCount":0,"nextPage":0}`))
		}))
		t.Cleanup(srv.Close)

		cmd := baseCmd(t, srv)
		cmd.Flags().StringVar(&listSort, "sort", "", "")
		cmd.Flags().StringVar(&listSortDir, "sort-dir", "", "")
		cmd.Flags().Int32Var(&listPage, "page", 1, "")
		t.Cleanup(func() { listSort = ""; listSortDir = ""; listPage = 1 })
		if err := cmd.ParseFlags([]string{"--sort", "expireDate", "--sort-dir", "desc"}); err != nil {
			t.Fatalf("ParseFlags: %v", err)
		}
		if err := runList(cmd, nil); err != nil {
			t.Fatalf("runList: %v", err)
		}
		if gotDir != "desc" {
			t.Errorf("expected dir=desc, got %q", gotDir)
		}
	})

	t.Run("rejected when not asc or desc", func(t *testing.T) {
		// Unlike sort, dir DOES have documented values, so a typo is worth
		// catching before the round trip.
		srv := neverCalledServer(t)
		cmd := baseCmd(t, srv)
		cmd.Flags().StringVar(&listSort, "sort", "", "")
		cmd.Flags().StringVar(&listSortDir, "sort-dir", "", "")
		cmd.Flags().Int32Var(&listPage, "page", 1, "")
		t.Cleanup(func() { listSort = ""; listSortDir = ""; listPage = 1 })
		if err := cmd.ParseFlags([]string{"--sort-dir", "descending"}); err != nil {
			t.Fatalf("ParseFlags: %v", err)
		}
		err := runList(cmd, nil)
		if err == nil {
			t.Fatal("expected an error for an invalid --sort-dir")
		}
		if !strings.Contains(err.Error(), "desc") {
			t.Errorf("error should name the valid values, got: %v", err)
		}
	})
}

// TestToggleCommands_UseUpdateDomain guards a deprecation migration. The spec
// marks LockDomain, UnlockDomain, EnableAutorenew, DisableAutorenew,
// EnableWhoisPrivacy and DisableWhoisPrivacy as `deprecated: true`, each saying
// "deprecated in favor of the new UpdateDomain API. This will be removed in a
// future release."
//
// All six back a user-facing command, so their removal breaks the CLI. The
// replacement — PATCH /core/v1/domains/{name} — is already used by
// `domain update` in this same package.
//
// Note PurchasePrivacy is deliberately NOT the migration target for
// `privacy on`: it is a separate operation the spec describes as "a billable
// action", whereas UpdateDomain carries no billing language and is the stated
// successor to the deprecated toggles.
func TestToggleCommands_UseUpdateDomain(t *testing.T) {
	tests := []struct {
		name      string
		args      []string
		run       func(*cobra.Command, []string) error
		wantField string
		wantValue bool
	}{
		{"lock on", []string{"on", "example.com"}, runLock, "locked", true},
		{"lock off", []string{"off", "example.com"}, runLock, "locked", false},
		{"autorenew on", []string{"on", "example.com"}, runAutorenew, "autorenewEnabled", true},
		{"autorenew off", []string{"off", "example.com"}, runAutorenew, "autorenewEnabled", false},
		{"privacy on", []string{"on", "example.com"}, runPrivacy, "privacyEnabled", true},
		{"privacy off", []string{"off", "example.com"}, runPrivacy, "privacyEnabled", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var method, path string
			var body map[string]any
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				method, path = r.Method, r.URL.Path
				_ = json.NewDecoder(r.Body).Decode(&body)
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"domainName":"example.com"}`))
			}))
			t.Cleanup(srv.Close)

			cmd := cmdForToggle(t, srv)
			if err := tc.run(cmd, tc.args); err != nil {
				t.Fatalf("run: %v", err)
			}

			if method != http.MethodPatch {
				t.Errorf("expected PATCH (UpdateDomain), got %s — still on a deprecated endpoint", method)
			}
			if path != "/core/v1/domains/example.com" {
				t.Errorf("expected the UpdateDomain path, got %q", path)
			}
			if got, ok := body[tc.wantField]; !ok || got != tc.wantValue {
				t.Errorf("expected body %s=%v, got %#v", tc.wantField, tc.wantValue, body)
			}
		})
	}
}

// TestContactsGet_SurfacesVerificationStatus guards the highest-consequence gap
// found in the API coverage audit.
//
// ICANN requires registrant contact verification, and the spec is explicit
// about what happens if it lapses (UnverifiedContact.verifyBy): "If the contact
// record is not verified by this date, the domain may become locked by the
// registry. This is typically 15 days from the creation date."
//
// Both `domain register` and `domain contacts set` can trigger verification —
// the spec says validation "is required by ICANN for all TLDs except ccTLDs" on
// contact update. Yet the CLI had no verification awareness anywhere: a grep for
// "verif" across cmd/ returned only unrelated hits.
//
// GetDomain already returns isVerified and verificationId on every contact
// role, so no extra API call is needed — `contacts get` just dumped raw JSON and
// never called attention to it.
func TestContactsGet_SurfacesVerificationStatus(t *testing.T) {
	const resp = `{
	  "domainName": "example.com",
	  "contacts": {
	    "registrant": {"firstName":"Alice","lastName":"A","email":"alice@example.com","isVerified":false,"verificationId":9911},
	    "admin":      {"firstName":"Bob","lastName":"B","email":"bob@example.com","isVerified":true},
	    "tech":       {"firstName":"Cal","lastName":"C","email":"cal@example.com","isVerified":true},
	    "billing":    {"firstName":"Dee","lastName":"D","email":"dee@example.com","isVerified":true}
	  }
	}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(resp))
	}))
	t.Cleanup(srv.Close)

	client, err := api.New(api.Options{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("api.New: %v", err)
	}
	var stdout, stderr bytes.Buffer
	out := &output.Config{Format: output.FormatTable, Color: output.ColorNever, Writer: &stdout, EWriter: &stderr}
	cmd := &cobra.Command{}
	ctx := context.WithValue(context.Background(), cmdutil.KeyOutput, out)
	ctx = context.WithValue(ctx, cmdutil.KeyClient, client)
	cmd.SetContext(ctx)

	if err := runContactsGet(cmd, []string{"example.com"}); err != nil {
		t.Fatalf("runContactsGet: %v", err)
	}
	combined := stdout.String() + stderr.String()

	// An unverified registrant is the case that can cost the user the domain.
	if !strings.Contains(strings.ToLower(combined), "unverified") &&
		!strings.Contains(strings.ToLower(combined), "not verified") {
		t.Errorf("an unverified registrant contact must be called out, got:\n%s", combined)
	}
	// The registry-lock consequence is the reason it matters.
	if !strings.Contains(strings.ToLower(combined), "lock") {
		t.Errorf("output should explain the registry-lock consequence, got:\n%s", combined)
	}
}

// claimsServer answers the full register flow. claimsBody is the
// CheckDomainClaims response; the create body is recorded for assertions.
func claimsServer(t *testing.T, claimsBody string, gotCreate *map[string]any, claimsCalled *bool) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "claims"):
			*claimsCalled = true
			_, _ = w.Write([]byte(claimsBody))
		case strings.Contains(r.URL.Path, "checkAvailability"):
			_, _ = w.Write([]byte(`{"results":[{"domainName":"tiktok.page","purchasable":true,"purchasePrice":12.99}]}`))
		case strings.Contains(r.URL.Path, "getPricing"):
			_, _ = w.Write([]byte(`{"purchasePrice":12.99}`))
		default:
			_ = json.NewDecoder(r.Body).Decode(gotCreate)
			_, _ = w.Write([]byte(`{"order":1,"totalPaid":12.99,"domain":{"domainName":"tiktok.page"}}`))
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

const claimedResponse = `{
  "domain": "tiktok.page",
  "claimsProcessActive": true,
  "claimId": "2013041500/2/6/9/rJ1NrDO92vDsAzf7EQzgjX4R0000000001",
  "notBefore": "2026-01-01T00:00:00Z",
  "notAfter":  "2026-12-31T00:00:00Z",
  "claimsNotice": "**This domain may infringe on a trademark claim. Proceeding with registration acknowledges that you have received notice of this claim.**",
  "claims": [{"trademark":"TIKTOK","jurisdiction":"US","registrationNumber":"5653614"}]
}`

const unclaimedResponse = `{"domain":"example.com","claimsProcessActive":false,"claimId":null,"claims":[]}`

// TestRegister_ClaimedDomainRequiresExplicitAcknowledgement is the core guard.
// CreateDomainRequest documents that "When a domain has trademark claims (as
// determined by the Domain Claims Check endpoint), you must include the claims
// acknowledgment data in the domain creation request" — and `domain register`
// never called that endpoint nor set the field, so registering any
// TMCH-matched name was impossible through the CLI.
//
// The acknowledgement is a legal notice ("Proceeding with registration
// acknowledges that you have received notice of this claim"), so --yes must NOT
// satisfy it: --yes is a general-purpose flag people set in wrappers, and
// accepting a trademark notice nobody read is exactly the failure mode the
// `domain check --yes` money bug had.
func TestRegister_ClaimedDomainRequiresExplicitAcknowledgement(t *testing.T) {
	defer output.StubInteractive(false)()

	var gotCreate map[string]any
	var claimsCalled bool
	srv := claimsServer(t, claimedResponse, &gotCreate, &claimsCalled)

	cmd := cmdForRegister(t, srv)
	if err := cmd.PersistentFlags().Set("yes", "true"); err != nil {
		t.Fatalf("setting yes flag: %v", err)
	}

	err := runRegister(cmd, []string{"tiktok.page"})
	if err == nil {
		t.Fatal("--yes alone must not acknowledge a trademark claim")
	}
	if !strings.Contains(err.Error(), "acknowledge-claim") {
		t.Errorf("error should name the flag that acknowledges the claim, got: %v", err)
	}
	if gotCreate != nil {
		t.Error("registration must not proceed without an explicit acknowledgement")
	}
	if !claimsCalled {
		t.Error("register must check for trademark claims before registering")
	}
}

// TestRegister_AcknowledgedClaimIsForwarded pins the data actually reaching the
// API: claimId plus the validity window, all three of which the create request
// requires when claims exist.
func TestRegister_AcknowledgedClaimIsForwarded(t *testing.T) {
	defer output.StubInteractive(false)()

	var gotCreate map[string]any
	var claimsCalled bool
	srv := claimsServer(t, claimedResponse, &gotCreate, &claimsCalled)

	cmd := cmdForRegister(t, srv)
	if err := cmd.PersistentFlags().Set("yes", "true"); err != nil {
		t.Fatalf("setting yes flag: %v", err)
	}
	if err := cmd.Flags().Set("acknowledge-claim", "true"); err != nil {
		t.Fatalf("setting acknowledge-claim flag: %v", err)
	}
	if err := runRegister(cmd, []string{"tiktok.page"}); err != nil {
		t.Fatalf("runRegister: %v", err)
	}

	claims, ok := gotCreate["claims"].(map[string]any)
	if !ok {
		t.Fatalf("create body must carry the claims acknowledgement, got: %#v", gotCreate)
	}
	if claims["claimId"] != "2013041500/2/6/9/rJ1NrDO92vDsAzf7EQzgjX4R0000000001" {
		t.Errorf("claimId not forwarded, got %#v", claims["claimId"])
	}
	for _, k := range []string{"notBefore", "notAfter"} {
		if claims[k] == nil || claims[k] == "" {
			t.Errorf("%s must be forwarded with the acknowledgement, got %#v", k, claims[k])
		}
	}
}

// TestRegister_UnclaimedDomainIsUnaffected guards against a regression in the
// overwhelmingly common path: no claims means no prompt, no extra flag, and no
// claims field on the request.
func TestRegister_UnclaimedDomainIsUnaffected(t *testing.T) {
	defer output.StubInteractive(false)()

	var gotCreate map[string]any
	var claimsCalled bool
	srv := claimsServer(t, unclaimedResponse, &gotCreate, &claimsCalled)

	cmd := cmdForRegister(t, srv)
	if err := cmd.PersistentFlags().Set("yes", "true"); err != nil {
		t.Fatalf("setting yes flag: %v", err)
	}
	if err := runRegister(cmd, []string{"example.com"}); err != nil {
		t.Fatalf("an unclaimed domain must register without extra flags, got: %v", err)
	}
	if gotCreate == nil {
		t.Fatal("registration did not happen")
	}
	if _, present := gotCreate["claims"]; present {
		t.Errorf("unclaimed registration must not send a claims field, got: %#v", gotCreate)
	}
}

// TestRegisterRenew_MultiYearPromptIsNotLabelledPerYear guards a regression
// introduced by wiring --years into GetPricingForDomain.
//
// The pricing endpoint returns the price for the REQUESTED TERM, not a per-year
// rate — namecom.api.yaml:626: "You will need to get the single year pricing,
// and then multiply the single year pricing by the number of years… 1 year
// pricing = 349.95. 2 year pricing = 699.90". So labelling a multi-year figure
// "/yr" shows the user several times what they will actually be charged, in the
// one message whose job is to state the amount.
//
// This test drives runRegister/runRenew for real. An earlier version called the
// formatTermPrice helper directly and discarded the run function — it passed
// while runRegister still used the raw "/yr" format, which is precisely the
// bug it was named after. The prompt text is asserted via the error returned by
// cmdutil.Confirm in a non-interactive shell without --yes, which embeds the
// full prompt string.
func TestRegisterRenew_MultiYearPromptIsNotLabelledPerYear(t *testing.T) {
	defer output.StubInteractive(false)()

	tests := []struct {
		name     string
		years    string
		wantYr   bool // "/yr" is correct only for a single-year term
		register bool
	}{
		{"register single year keeps /yr", "1", true, true},
		{"register multi-year must not say /yr", "3", false, true},
		{"renew single year keeps /yr", "1", true, false},
		{"renew multi-year must not say /yr", "2", false, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			price := 699.90
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch {
				case strings.Contains(r.URL.Path, "getPricing"):
					_ = json.NewEncoder(w).Encode(gen.PricingResponseSchema{
						PurchasePrice: &price, RenewalPrice: &price,
					})
				case strings.Contains(r.URL.Path, "checkAvailability"):
					results := []gen.SearchResult{{DomainName: "example.com", Purchasable: true, PurchasePrice: &price}}
					_ = json.NewEncoder(w).Encode(gen.SearchResponseSchema{Results: &results})
				case strings.Contains(r.URL.Path, "claims"):
					_, _ = w.Write([]byte(`{"domain":"example.com","claimsProcessActive":false,"claimId":null,"claims":[]}`))
				default:
					t.Errorf("no purchase should be made: %s %s", r.Method, r.URL)
					http.Error(w, "unexpected", http.StatusInternalServerError)
				}
			}))
			t.Cleanup(srv.Close)

			var cmd *cobra.Command
			var run func(*cobra.Command, []string) error
			if tc.register {
				cmd, run = cmdForRegister(t, srv), runRegister
			} else {
				cmd, run = cmdForRenew(t, srv), runRenew
			}
			// Deliberately NOT setting --yes: non-interactively, Confirm returns
			// an error carrying the prompt it would have shown, which is the only
			// way to inspect the message without a TTY.
			if err := cmd.PersistentFlags().Set("yes", "false"); err != nil {
				t.Fatalf("setting yes flag: %v", err)
			}
			if err := cmd.Flags().Set("years", tc.years); err != nil {
				t.Fatalf("setting years flag: %v", err)
			}

			err := run(cmd, []string{"example.com"})
			if err == nil {
				t.Fatal("expected the confirmation to fail non-interactively, exposing the prompt")
			}
			prompt := err.Error()

			if !strings.Contains(prompt, "699.90") {
				t.Fatalf("prompt should quote the price, got: %s", prompt)
			}
			if hasYr := strings.Contains(prompt, "/yr"); hasYr != tc.wantYr {
				t.Errorf("years=%s prompt %q: /yr present=%v, want %v", tc.years, prompt, hasYr, tc.wantYr)
			}
			if !tc.wantYr && !strings.Contains(prompt, "total for "+tc.years) {
				t.Errorf("multi-year prompt should state the term total, got: %s", prompt)
			}
		})
	}
}

// TestUpdate_PrivacyPurchaseIsConfirmed guards a consistency hole with real
// money behind it. `domain privacy on` deliberately confirms first, because
// enabling WHOIS privacy can be billable on accounts without a bundled plan.
// `domain update --privacy=true` reaches the identical API call — both now PATCH
// /core/v1/domains/{name} with privacyEnabled — but asked nothing.
//
// Two ways to the same charge, only one of which paused.
func TestUpdate_PrivacyPurchaseIsConfirmed(t *testing.T) {
	defer output.StubInteractive(false)()

	var patched bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPatch {
			patched = true
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"domainName":"example.com","privacyEnabled":false}`))
	}))
	t.Cleanup(srv.Close)

	// No --yes: non-interactively, a billable change must not proceed silently.
	cmd := baseCmd(t, srv)
	cmd.Flags().Bool("autorenew", false, "")
	cmd.Flags().Bool("privacy", false, "")
	cmd.Flags().Bool("lock", false, "")
	root := &cobra.Command{Use: "namecom"}
	var dr, yes bool
	root.PersistentFlags().BoolVar(&dr, "dry-run", false, "")
	root.PersistentFlags().BoolVarP(&yes, "yes", "y", false, "")
	root.AddCommand(cmd)
	if err := cmd.Flags().Set("privacy", "true"); err != nil {
		t.Fatalf("setting privacy flag: %v", err)
	}

	err := runUpdate(cmd, []string{"example.com"})
	if err == nil {
		t.Fatal("enabling privacy is billable and must be confirmed, like 'domain privacy on'")
	}
	if patched {
		t.Error("the update was sent despite no confirmation")
	}
}

// TestUpdate_NonBillableChangesDoNotPrompt is the counterweight: only the
// billable field should gate. Turning autorenew on must stay frictionless.
func TestUpdate_NonBillableChangesDoNotPrompt(t *testing.T) {
	defer output.StubInteractive(false)()

	var patched bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPatch {
			patched = true
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"domainName":"example.com"}`))
	}))
	t.Cleanup(srv.Close)

	cmd := baseCmd(t, srv)
	cmd.Flags().Bool("autorenew", false, "")
	cmd.Flags().Bool("privacy", false, "")
	cmd.Flags().Bool("lock", false, "")
	root := &cobra.Command{Use: "namecom"}
	var dr, yes bool
	root.PersistentFlags().BoolVar(&dr, "dry-run", false, "")
	root.PersistentFlags().BoolVarP(&yes, "yes", "y", false, "")
	root.AddCommand(cmd)
	if err := cmd.Flags().Set("autorenew", "true"); err != nil {
		t.Fatalf("setting autorenew flag: %v", err)
	}

	if err := runUpdate(cmd, []string{"example.com"}); err != nil {
		t.Fatalf("a non-billable update must not require confirmation: %v", err)
	}
	if !patched {
		t.Error("the update was not sent")
	}
}

// TestUpdate_PreservesUnmentionedSettings pins the read-modify-write contract on
// the one command that can turn three separate protections off by accident.
//
// This is a PATCH, but the CLI sends all three booleans every time — it seeds
// them from the current domain and overrides only the flags the user actually
// passed. Drop that seeding and every field defaults to false, so
// `domain update --autorenew=true` would ALSO unlock the domain and switch off
// WHOIS privacy without printing a word about either. Unlocking is what makes
// an unauthorized transfer possible, and dropping privacy republishes the
// registrant's name, address, and phone number in public WHOIS.
//
// The fixture sets the preserved fields to true on purpose: false is both the
// zero value and a legitimate state, so a fixture full of false cannot tell
// "preserved correctly" apart from "dropped to the zero value".
func TestUpdate_PreservesUnmentionedSettings(t *testing.T) {
	defer output.StubInteractive(false)()

	var patchBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPatch {
			patchBody, _ = io.ReadAll(r.Body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"domainName":"example.com","locked":true,` +
			`"privacyEnabled":true,"autorenewEnabled":false}`))
	}))
	t.Cleanup(srv.Close)

	cmd := baseCmd(t, srv)
	cmd.Flags().Bool("autorenew", false, "")
	cmd.Flags().Bool("privacy", false, "")
	cmd.Flags().Bool("lock", false, "")
	root := &cobra.Command{Use: "namecom"}
	var dr, yes bool
	root.PersistentFlags().BoolVar(&dr, "dry-run", false, "")
	root.PersistentFlags().BoolVarP(&yes, "yes", "y", false, "")
	root.AddCommand(cmd)
	if err := cmd.Flags().Set("autorenew", "true"); err != nil {
		t.Fatalf("setting autorenew flag: %v", err)
	}

	if err := runUpdate(cmd, []string{"example.com"}); err != nil {
		t.Fatalf("runUpdate: %v", err)
	}
	if patchBody == nil {
		t.Fatal("the update was never sent")
	}

	var sent struct {
		AutorenewEnabled *bool `json:"autorenewEnabled"`
		PrivacyEnabled   *bool `json:"privacyEnabled"`
		Locked           *bool `json:"locked"`
	}
	if err := json.Unmarshal(patchBody, &sent); err != nil {
		t.Fatalf("PATCH body was not JSON: %v (%s)", err, patchBody)
	}
	if sent.AutorenewEnabled == nil || !*sent.AutorenewEnabled {
		t.Errorf("--autorenew=true must reach the wire, got %v", sent.AutorenewEnabled)
	}
	if sent.Locked == nil || !*sent.Locked {
		t.Errorf("--lock was not passed: the domain must stay locked, got %v (this unlocks it)", sent.Locked)
	}
	if sent.PrivacyEnabled == nil || !*sent.PrivacyEnabled {
		t.Errorf("--privacy was not passed: privacy must stay on, got %v (this exposes WHOIS data)", sent.PrivacyEnabled)
	}
}

// TestRegister_DryRunPreviewsTheRealBody guards what --dry-run is for.
//
// runRegister wrapped its availability check in `if !dryRun`, and resolveClaims
// returned early on dry-run — so the previewed body omitted purchaseType, the
// aftermarket purchasePrice, and claims. Those are precisely the fields someone
// runs --dry-run to inspect on a premium or trademark-claimed name.
//
// Skipping the CHARGE is the point; skipping the two read-only lookups that
// determine the body is not.
func TestRegister_DryRunPreviewsTheRealBody(t *testing.T) {
	price := 450.00
	var created bool

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "checkAvailability"):
			ptype := gen.SearchPurchaseType("aftermarket_b")
			results := []gen.SearchResult{{
				DomainName: "example.com", Purchasable: true,
				PurchasePrice: &price, PurchaseType: &ptype,
			}}
			_ = json.NewEncoder(w).Encode(gen.SearchResponseSchema{Results: &results})
		case strings.Contains(r.URL.Path, "claims"):
			_, _ = w.Write([]byte(`{"domain":"example.com","claimsProcessActive":false,"claimId":null,"claims":[]}`))
		case strings.Contains(r.URL.Path, "getPricing"):
			_ = json.NewEncoder(w).Encode(gen.PricingResponseSchema{PurchasePrice: &price})
		default:
			created = true
			_ = json.NewEncoder(w).Encode(gen.CreateDomainResponseSchema{})
		}
	}))
	t.Cleanup(srv.Close)

	cmd := cmdForRegister(t, srv)
	root := &cobra.Command{Use: "namecom"}
	var dr, yes bool
	root.PersistentFlags().BoolVar(&dr, "dry-run", false, "")
	root.PersistentFlags().BoolVarP(&yes, "yes", "y", false, "")
	for _, f := range []string{"dry-run", "yes"} {
		if err := root.PersistentFlags().Set(f, "true"); err != nil {
			t.Fatalf("setting %s: %v", f, err)
		}
	}
	root.AddCommand(cmd)

	buf, ok := cmdutil.Out(cmd).Writer.(*bytes.Buffer)
	if !ok {
		t.Fatal("output writer is not a *bytes.Buffer")
	}
	if err := runRegister(cmd, []string{"example.com"}); err != nil {
		t.Fatalf("runRegister: %v", err)
	}

	if created {
		t.Fatal("--dry-run performed a real registration")
	}
	preview := buf.String()
	if !strings.Contains(preview, "aftermarket_b") {
		t.Errorf("dry-run body omits purchaseType — the field that decides what kind of purchase this is:\n%s", preview)
	}
	if !strings.Contains(preview, "450") {
		t.Errorf("dry-run body omits the aftermarket purchasePrice:\n%s", preview)
	}
}

// TestRegister_ClaimsCheckedForTheActualPurchaseType guards a case where the
// trademark gate silently does not fire.
//
// resolveClaims sent an empty body, which the API defaults to
// purchaseType "registration". But claims applicability is per-purchase-type —
// ResellerTldInfo.claimsCheckRequired is documented as "Array of valid purchase
// types if claims check is required" — and runRegister already knows the real
// type from the availability check. For a landrush or aftermarket acquisition
// of a trademarked name, checking the wrong type can report no claim, so no
// notice is shown and no acknowledgement is collected.
func TestRegister_ClaimsCheckedForTheActualPurchaseType(t *testing.T) {
	price := 450.00
	var claimsPurchaseTypeSent string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "checkAvailability"):
			ptype := gen.SearchPurchaseType("landrush_eap")
			results := []gen.SearchResult{{
				DomainName: "example.com", Purchasable: true,
				PurchasePrice: &price, PurchaseType: &ptype,
			}}
			_ = json.NewEncoder(w).Encode(gen.SearchResponseSchema{Results: &results})
		case strings.Contains(r.URL.Path, "claims"):
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if v, ok := body["purchaseType"].(string); ok {
				claimsPurchaseTypeSent = v
			}
			_, _ = w.Write([]byte(`{"domain":"example.com","claimsProcessActive":false,"claimId":null,"claims":[]}`))
		case strings.Contains(r.URL.Path, "getPricing"):
			_ = json.NewEncoder(w).Encode(gen.PricingResponseSchema{PurchasePrice: &price})
		default:
			_ = json.NewEncoder(w).Encode(gen.CreateDomainResponseSchema{})
		}
	}))
	t.Cleanup(srv.Close)

	cmd := cmdForRegister(t, srv)
	if err := cmd.PersistentFlags().Set("yes", "true"); err != nil {
		t.Fatalf("setting yes flag: %v", err)
	}
	if err := runRegister(cmd, []string{"example.com"}); err != nil {
		t.Fatalf("runRegister: %v", err)
	}

	if claimsPurchaseTypeSent != "landrush_eap" {
		t.Errorf("claims check used purchaseType %q, but the purchase is landrush_eap — "+
			"the gate may not fire for the transaction actually being made", claimsPurchaseTypeSent)
	}
}

// TestRegister_MalformedTLDRequirementFailsBeforeAnyPrompt guards the ordering
// of a pure string parse.
//
// parseTLDRequirements ran after the confirmation prompt and after
// resolveClaims — so a typo'd --tld-requirement made the user approve a charge,
// and potentially acknowledge a trademark notice, before being told the flag
// was malformed. It touches nothing but argv; it belongs before any prompt or
// request.
func TestRegister_MalformedTLDRequirementFailsBeforeAnyPrompt(t *testing.T) {
	srv := neverCalledServer(t)

	cmd := cmdForRegister(t, srv)
	if err := cmd.PersistentFlags().Set("yes", "true"); err != nil {
		t.Fatalf("setting yes flag: %v", err)
	}
	if err := cmd.Flags().Set("tld-requirement", "legal-type"); err != nil { // missing =value
		t.Fatalf("setting tld-requirement: %v", err)
	}

	err := runRegister(cmd, []string{"example.com"})
	if err == nil {
		t.Fatal("expected an error for a malformed --tld-requirement")
	}
	if !strings.Contains(err.Error(), "key=value") {
		t.Errorf("error should show the expected form, got: %v", err)
	}
}

// TestRequirements_QuietListsRequiredFields guards that -q returns API data
// rather than the caller's own argument.
//
// It previously echoed back the TLD that was typed in, which tells a script
// nothing. The useful scriptable answer is the field names, one per line, so a
// caller can build the matching --tld-requirement flags.
func TestRequirements_QuietListsRequiredFields(t *testing.T) {
	const resp = `{
	  "tldInfo": {"allowedRegistrationYears":[1,2],"supportsDnssec":true,"supportsPrivacy":false,
	              "supportsTransferLock":true,"supportsPremium":false,"supportsInternalTransfer":false},
	  "requirements": {"fields": {"legal-type": {}, "birth-country": {}}},
	  "contacts": {}
	}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(resp))
	}))
	t.Cleanup(srv.Close)

	client, err := api.New(api.Options{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("api.New: %v", err)
	}
	var buf bytes.Buffer
	out := &output.Config{Format: output.FormatTable, Color: output.ColorNever,
		QuietMode: true, Writer: &buf, EWriter: &bytes.Buffer{}}
	cmd := &cobra.Command{}
	ctx := context.WithValue(context.Background(), cmdutil.KeyOutput, out)
	ctx = context.WithValue(ctx, cmdutil.KeyClient, client)
	cmd.SetContext(ctx)

	if err := runRequirements(cmd, []string{"fr"}); err != nil {
		t.Fatalf("runRequirements: %v", err)
	}
	got := strings.TrimSpace(buf.String())
	if got == "fr" {
		t.Fatal("--quiet echoed the argument back instead of returning API data")
	}
	for _, want := range []string{"birth-country", "legal-type"} {
		if !strings.Contains(got, want) {
			t.Errorf("expected required field %q in quiet output, got: %q", want, got)
		}
	}
}
