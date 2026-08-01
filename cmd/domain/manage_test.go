package domain

import (
	"bytes"
	"context"
	"encoding/json"
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

func TestRegister_DryRunSkipsAvailabilityCheck(t *testing.T) {
	// Dry-run skips CheckAvailability and CreateDomain but still fetches pricing
	// to show the user what they would be charged.
	var checkAvailCalled bool
	var createCalled bool
	price := 12.99

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/core/v1/domains:checkAvailability":
			checkAvailCalled = true
			t.Error("CheckAvailability should not be called in dry-run mode")
			http.Error(w, "unexpected", http.StatusInternalServerError)
		case "/core/v1/domains/example.com:getPricing":
			_ = json.NewEncoder(w).Encode(gen.PricingResponseSchema{PurchasePrice: &price})
		case "/core/v1/domains":
			createCalled = true
			t.Error("CreateDomain should not be called in dry-run mode")
			http.Error(w, "unexpected", http.StatusInternalServerError)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
			http.Error(w, "unexpected", http.StatusInternalServerError)
		}
	}))
	t.Cleanup(srv.Close)

	cmd := cmdForRegister(t, srv)
	var dryRun bool
	cmd.PersistentFlags().BoolVar(&dryRun, "dry-run", false, "")
	if err := cmd.PersistentFlags().Set("dry-run", "true"); err != nil {
		t.Fatalf("setting dry-run flag: %v", err)
	}
	// --yes prevents confirm() from erroring in non-interactive mode.
	if err := cmd.PersistentFlags().Set("yes", "true"); err != nil {
		t.Fatalf("setting yes flag: %v", err)
	}
	if err := runRegister(cmd, []string{"example.com"}); err != nil {
		t.Fatalf("expected no error in dry-run, got: %v", err)
	}
	if checkAvailCalled {
		t.Error("CheckAvailability was called in dry-run mode")
	}
	if createCalled {
		t.Error("CreateDomain was called in dry-run mode")
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

func TestContactsGet_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(gen.DomainResponsePayload{DomainName: "example.com"})
	}))
	t.Cleanup(srv.Close)

	cmd := cmdForContactsGet(t, srv)
	if err := runContactsGet(cmd, []string{"example.com"}); err != nil {
		t.Fatalf("runContactsGet: %v", err)
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

// TestList_SortValidated guards the one enum flag that wasn't checked
// client-side. Every other enum (--type, --status, --output, forwarding type)
// validates before the request; --sort passed a typo straight through and the
// user got a raw API error instead of the list of valid fields.
func TestList_SortValidated(t *testing.T) {
	srv := neverCalledServer(t)
	cmd := baseCmd(t, srv)
	cmd.Flags().StringVar(&listSort, "sort", "", "")
	cmd.Flags().Int32Var(&listPage, "page", 1, "")
	t.Cleanup(func() { listSort = ""; listPage = 1 })
	if err := cmd.ParseFlags([]string{"--sort", "expiryDate"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}

	err := runList(cmd, nil)
	if err == nil {
		t.Fatal("expected a client-side error for an invalid --sort field")
	}
	if !strings.Contains(err.Error(), "expireDate") {
		t.Errorf("error should list the valid sort fields, got: %v", err)
	}
}
