package domain

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	coreapigo "github.com/namedotcom/core-api-go"

	"github.com/patramsey/namecom-cli/cmd/cmdutil"
	"github.com/patramsey/namecom-cli/internal/api"
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

// TestToggle_NotFoundNamesTheDomain pins #234: `domain lock on nope.com`
// printed the API's bare "Not Found".
func TestToggle_NotFoundNamesTheDomain(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"Not Found"}`))
	}))
	t.Cleanup(srv.Close)

	err := runLock(cmdForToggle(t, srv), []string{"on", "nope.com"})
	if err == nil || !strings.HasPrefix(err.Error(), `domain "nope.com" not found`) {
		t.Fatalf("runLock = %v, want it to name the domain", err)
	}
	if !cmdutil.IsNotFound(err) {
		t.Error("the named error must still be a 404, so it exits 4")
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

// TestSetNS_PositionalNameserversSuggestNS pins #234: `set-ns D ns1 ns2` said
// only "too many arguments", without mentioning --ns.
func TestSetNS_PositionalNameserversSuggestNS(t *testing.T) {
	prev := setNSList
	t.Cleanup(func() { setNSList = prev })
	setNSList = ""

	err := setNSArgs(setNSCmd, []string{"example.com", "ns1.a.com", "ns2.a.com"})
	u, ok := errors.AsType[*cmdutil.UsageError](err)
	if !ok {
		t.Fatalf("setNSArgs = %v, want a usage error", err)
	}
	// The package's command tree has no root here, so the path starts at "domain".
	if want := "run '" + setNSCmd.CommandPath() + " example.com --ns ns1.a.com,ns2.a.com'"; u.UserHint() != want {
		t.Errorf("hint = %q, want %q", u.UserHint(), want)
	}
	if err := setNSArgs(setNSCmd, []string{"example.com"}); err != nil {
		t.Errorf("one domain rejected: %v", err)
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
	setYes(t, cmd, true)
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
		_ = json.NewEncoder(w).Encode(coreapigo.PricingResponse{
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
	cmd.Flags().Float64Var(&registerMaxPrice, "max-price", 0, "")
	cmd.Flags().BoolVar(&registerAccept, "accept-premium", false, "")
	t.Cleanup(func() {
		registerAckClaim, registerTLDReqs, registerMaxPrice, registerAccept = false, nil, 0, false
	})
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
		results := []*coreapigo.SearchResult{{DomainName: domain, Purchasable: purchasable, PurchasePrice: &price}}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(coreapigo.SearchResponse{Results: results})
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
			results := []*coreapigo.SearchResult{{DomainName: "free.com", Purchasable: true, PurchasePrice: &price}}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(coreapigo.SearchResponse{Results: results})
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
			results := []*coreapigo.SearchResult{{DomainName: "example.com", Purchasable: true, PurchasePrice: &price}}
			_ = json.NewEncoder(w).Encode(coreapigo.SearchResponse{Results: results})
		case strings.Contains(r.URL.Path, "getPricing"):
			pricingChecked = true
			_ = json.NewEncoder(w).Encode(coreapigo.PricingResponse{PurchasePrice: &price})
		case strings.Contains(r.URL.Path, "claims"):
			claimsChecked = true
			_, _ = w.Write([]byte(`{"domain":"example.com","claimsProcessActive":false,"claimId":null,"claims":[]}`))
		case r.URL.Path == "/core/v1/accountinfo/balance":
			_, _ = w.Write([]byte(`{"balance":120}`)) // for the dry run's quote (#271)
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
		_ = json.NewEncoder(w).Encode(coreapigo.DomainResponsePayload{DomainName: "example.com"})
	}))
	t.Cleanup(srv.Close)

	cmd := cmdForUpdate(t, srv)
	// --privacy=true, because it neither prompts nor reads the domain first.
	if err := cmd.Flags().Set("privacy", "true"); err != nil {
		t.Fatalf("setting privacy flag: %v", err)
	}
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
	cmd.Flags().Float64Var(&renewMaxPrice, "max-price", 0, "")
	cmd.Flags().BoolVar(&renewAccept, "accept-premium", false, "")
	var yes bool
	cmd.PersistentFlags().BoolVarP(&yes, "yes", "y", false, "")
	if err := cmd.PersistentFlags().Set("yes", "true"); err != nil {
		t.Fatalf("setting yes flag: %v", err)
	}
	t.Cleanup(func() { renewYears, renewPrice, renewMaxPrice, renewAccept = 1, 0, 0, false })
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
func renewServer(t *testing.T, pricing coreapigo.PricingResponse, got *map[string]any) *httptest.Server {
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
		_ = json.NewEncoder(w).Encode(coreapigo.RenewDomainResponse{})
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
	srv := renewServer(t, coreapigo.PricingResponse{Premium: true, RenewalPrice: &renewal}, &gotBody)

	cmd := cmdForRenew(t, srv)
	renewAccept = true // a premium renewal needs --accept-premium (#226)
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
	srv := renewServer(t, coreapigo.PricingResponse{Premium: false, RenewalPrice: &renewal}, &gotBody)

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
	srv := renewServer(t, coreapigo.PricingResponse{Premium: true, RenewalPrice: &quoted}, &gotBody)

	cmd := cmdForRenew(t, srv)
	if err := cmd.ParseFlags([]string{"--price", "1800", "--accept-premium"}); err != nil {
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

// TestRenew_PromptQuotesThePriceSent is the renew side of #83: with --price,
// the prompt quoted the standard renewal price while the body carried the
// override. Without --yes in a non-interactive test, Confirm's error carries
// the prompt.
func TestRenew_PromptQuotesThePriceSent(t *testing.T) {
	defer output.StubInteractive(false)()
	quoted := 2500.00
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if !strings.Contains(r.URL.Path, "getPricing") {
			t.Errorf("renewed without confirmation: %s %s", r.Method, r.URL)
		}
		_ = json.NewEncoder(w).Encode(coreapigo.PricingResponse{Premium: true, RenewalPrice: &quoted})
	}))
	t.Cleanup(srv.Close)

	cmd := cmdForRenew(t, srv)
	if err := cmd.PersistentFlags().Set("yes", "false"); err != nil {
		t.Fatal(err)
	}
	if err := cmd.ParseFlags([]string{"--price", "1800", "--accept-premium"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	err := runRenew(cmd, []string{"premium.io"})
	if err == nil {
		t.Fatal("expected the non-interactive confirm error")
	}
	if !strings.Contains(err.Error(), "$1,800.00") {
		t.Errorf("prompt does not quote the price sent ($1,800.00):\n%v", err)
	}
	if strings.Contains(err.Error(), "2500") {
		t.Errorf("prompt quotes the standard renewal price, which is not sent:\n%v", err)
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
					_ = json.NewEncoder(w).Encode(coreapigo.PricingResponse{
						PurchasePrice: &price, RenewalPrice: &price,
					})
				case strings.Contains(r.URL.Path, "checkAvailability"):
					results := []*coreapigo.SearchResult{{DomainName: "example.com", Purchasable: true, PurchasePrice: &price}}
					_ = json.NewEncoder(w).Encode(coreapigo.SearchResponse{Results: results})
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

// TestYearsReachesTheWriteBody pins the term length on the two commands that
// spend money.
//
// TestPricingQuoteUsesRequestedYears above covers the *quote* — that --years 3
// is passed to getPricing. This covers the other half: that the same 3 reaches
// the register/renew body. Drop Years there and the API applies its default of
// one year, so the user is quoted, shown, and confirms a three-year price, then
// receives a one-year term. Nothing errors and nothing looks wrong.
//
// --years is deliberately 3 here. Its flag default is 1, so a fixture at 1
// cannot tell "sent the requested term" apart from "sent nothing and let the
// API default", which is exactly the bug.
func TestYearsReachesTheWriteBody(t *testing.T) {
	tests := []struct {
		name     string
		pathPart string
		run      func(*testing.T, *httptest.Server) error
	}{
		{
			name:     "renew",
			pathPart: "renew",
			run: func(t *testing.T, srv *httptest.Server) error {
				cmd := cmdForRenew(t, srv)
				if err := cmd.ParseFlags([]string{"--years", "3"}); err != nil {
					t.Fatalf("ParseFlags: %v", err)
				}
				return runRenew(cmd, []string{"example.com"})
			},
		},
		{
			name:     "register",
			pathPart: "domains",
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
			var writeBody map[string]any
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch {
				case strings.Contains(r.URL.Path, "getPricing"):
					_ = json.NewEncoder(w).Encode(coreapigo.PricingResponse{
						PurchasePrice: &price, RenewalPrice: &price,
					})
				case strings.Contains(r.URL.Path, "checkAvailability"):
					results := []*coreapigo.SearchResult{{DomainName: "example.com", Purchasable: true, PurchasePrice: &price}}
					_ = json.NewEncoder(w).Encode(coreapigo.SearchResponse{Results: results})
				case r.Method == http.MethodPost:
					_ = json.NewDecoder(r.Body).Decode(&writeBody)
					_, _ = w.Write([]byte(`{}`))
				default:
					_, _ = w.Write([]byte(`{}`))
				}
			}))
			t.Cleanup(srv.Close)

			if err := tc.run(t, srv); err != nil {
				t.Fatalf("run: %v", err)
			}
			if writeBody == nil {
				t.Fatal("no write request was sent")
			}
			got, ok := writeBody["years"]
			if !ok {
				t.Fatalf("body omits years — the API will default to 1 while the user "+
					"was quoted a 3-year price; body was: %#v", writeBody)
			}
			if n, isNum := got.(float64); !isNum || int(n) != 3 {
				t.Errorf("body should carry the requested term years=3, got %#v", got)
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
			_ = json.NewEncoder(w).Encode(coreapigo.PricingResponse{RenewalPrice: &price})
		default:
			renewPath = r.URL.Path
			_ = json.NewEncoder(w).Encode(coreapigo.RenewDomainResponse{})
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
	cmd.Flags().StringVar(&contactsFile, "contacts-file", f, "")
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
	setYes(t, cmd, true)
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
	cmd.Flags().StringVar(&contactsFile, "contacts-file", contactsFile, "")

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

// ---- confirmation (#136) ----------------------------------------------------

// setYes gives a standalone test command the root --yes flag, set as asked.
func setYes(t *testing.T, cmd *cobra.Command, yes bool) {
	t.Helper()
	var v bool
	cmd.PersistentFlags().BoolVarP(&v, "yes", "y", false, "")
	if yes {
		if err := cmd.PersistentFlags().Set("yes", "true"); err != nil {
			t.Fatalf("setting yes flag: %v", err)
		}
	}
}

func TestSetNSPrompt(t *testing.T) {
	body := coreapigo.DomainsSetNameserversBody{
		DomainName:  "example.com",
		Nameservers: []string{"ns1.x.com", "ns2.x.com"},
	}
	want := "Set nameservers for example.com to ns1.x.com, ns2.x.com? " +
		"The domain stops resolving if these are wrong."
	if got := setNSPrompt(body); got != want {
		t.Errorf("setNSPrompt:\n got: %s\nwant: %s", got, want)
	}
}

func TestContactsSetPrompt(t *testing.T) {
	const registrantWarning = "ICANN email verification"
	tests := []struct {
		name, file, roles string
		registrant        bool
	}{
		{"all four", `{"registrant":{},"admin":{},"tech":{},"billing":{}}`,
			"registrant, admin, tech and billing", true},
		{"registrant only", `{"registrant":{}}`, "registrant", true},
		{"no registrant", `{"admin":{},"billing":{}}`, "admin and billing", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var c coreapigo.ContactsRequest
			if err := json.Unmarshal([]byte(tc.file), &c); err != nil {
				t.Fatalf("Unmarshal: %v", err)
			}
			got := contactsSetPrompt(coreapigo.DomainsSetContactsBody{DomainName: "example.com", Contacts: &c})
			if want := "Replace the " + tc.roles + " contact"; !strings.HasPrefix(got, want) {
				t.Errorf("prompt %q should start %q", got, want)
			}
			if !strings.Contains(got, "example.com") {
				t.Errorf("prompt %q does not name the domain", got)
			}
			if has := strings.Contains(got, registrantWarning); has != tc.registrant {
				t.Errorf("prompt %q: registrant warning present=%v, want %v", got, has, tc.registrant)
			}
			if tc.registrant && !strings.Contains(got, "transfer lock") {
				t.Errorf("registrant prompt %q should mention the transfer lock", got)
			}
		})
	}
}

// TestSetNSAndContactsSet_Confirm pins #136: both writes now confirm like
// other destructive ones. A decline exits 0 and sends nothing, --yes sends
// without asking, and a script without --yes gets an error carrying the
// question rather than a silent change.
func TestSetNSAndContactsSet_Confirm(t *testing.T) {
	const contacts = `{"registrant":{"firstName":"Ada","lastName":"Lovelace","email":"ada@example.com"}}`
	commands := []struct {
		name       string
		build      func(*testing.T, *httptest.Server) *cobra.Command
		run        func(*cobra.Command, []string) error
		wantPrompt string
	}{
		{"set-ns", func(t *testing.T, srv *httptest.Server) *cobra.Command {
			cmd := cmdForSetNS(t, srv)
			if err := cmd.ParseFlags([]string{"--ns", "ns1.x.com,ns2.x.com"}); err != nil {
				t.Fatalf("ParseFlags: %v", err)
			}
			return cmd
		}, runSetNS, "Set nameservers for example.com to ns1.x.com, ns2.x.com?"},
		{"contacts set", func(t *testing.T, srv *httptest.Server) *cobra.Command {
			return cmdForContactsSet(t, srv, contacts)
		}, runContactsSet, "Replace the registrant contact for example.com?"},
	}
	for _, c := range commands {
		serve := func(t *testing.T) (*httptest.Server, *int) {
			var hits int
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				hits++
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{}`))
			}))
			t.Cleanup(srv.Close)
			return srv, &hits
		}

		t.Run(c.name+"/decline", func(t *testing.T) {
			var asked string
			defer cmdutil.StubConfirm(func(p string) bool { asked = p; return false })()
			srv, hits := serve(t)
			cmd := c.build(t, srv)
			setYes(t, cmd, false)
			if err := c.run(cmd, []string{"example.com"}); !errors.Is(err, cmdutil.ErrAborted) {
				t.Fatalf("a decline must fail with cmdutil.ErrAborted (exit 1), got: %v", err)
			}
			if *hits != 0 {
				t.Errorf("declined, but %d request(s) were sent", *hits)
			}
			if !strings.HasPrefix(asked, c.wantPrompt) {
				t.Errorf("prompt %q should start %q", asked, c.wantPrompt)
			}
		})

		t.Run(c.name+"/yes sends", func(t *testing.T) {
			defer output.StubInteractive(false)()
			srv, hits := serve(t)
			cmd := c.build(t, srv)
			setYes(t, cmd, true)
			if err := c.run(cmd, []string{"example.com"}); err != nil {
				t.Fatalf("with --yes: %v", err)
			}
			if *hits != 1 {
				t.Errorf("with --yes, want 1 request, got %d", *hits)
			}
		})

		t.Run(c.name+"/non-interactive without yes", func(t *testing.T) {
			defer output.StubInteractive(false)()
			srv, hits := serve(t)
			cmd := c.build(t, srv)
			setYes(t, cmd, false)
			err := c.run(cmd, []string{"example.com"})
			if err == nil {
				t.Fatal("expected an error without --yes in non-interactive mode")
			}
			if !strings.Contains(err.Error(), c.wantPrompt) || !strings.Contains(err.Error(), "--yes") {
				t.Errorf("error should carry the prompt and name --yes, got: %v", err)
			}
			if *hits != 0 {
				t.Errorf("unconfirmed, but %d request(s) were sent", *hits)
			}
		})
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
		_ = json.NewEncoder(w).Encode(coreapigo.AuthCodeResponse{AuthCode: "SECRET123"})
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
func checkThenCreateServer(t *testing.T, result coreapigo.SearchResult, got *map[string]any) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "checkAvailability"):
			results := []*coreapigo.SearchResult{&result}
			_ = json.NewEncoder(w).Encode(coreapigo.SearchResponse{Results: results})
		case strings.Contains(r.URL.Path, "getPricing"):
			_ = json.NewEncoder(w).Encode(coreapigo.PricingResponse{PurchasePrice: result.PurchasePrice})
		case strings.Contains(r.URL.Path, "claims"):
			// register now always checks for trademark claims; no claim here.
			_, _ = w.Write([]byte(`{"domain":"example.com","claimsProcessActive":false,"claimId":null,"claims":[]}`))
		default:
			if err := json.NewDecoder(r.Body).Decode(got); err != nil {
				t.Errorf("decoding create body: %v", err)
			}
			_ = json.NewEncoder(w).Encode(coreapigo.CreateDomainResponse{})
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
	ptype := coreapigo.SearchPurchaseType("aftermarket_b")
	var gotBody map[string]any
	srv := checkThenCreateServer(t, coreapigo.SearchResult{
		DomainName: "example.com", Purchasable: true,
		PurchasePrice: &price, PurchaseType: &ptype,
	}, &gotBody)

	cmd := cmdForRegister(t, srv)
	if err := cmd.PersistentFlags().Set("yes", "true"); err != nil {
		t.Fatalf("setting yes flag: %v", err)
	}
	registerAccept = true // a non-standard price needs --accept-premium (#226)
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
	ptype := coreapigo.SearchPurchaseType("registration")
	var gotBody map[string]any
	srv := checkThenCreateServer(t, coreapigo.SearchResult{
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
			// update may GET before it PATCHes (to decide whether to prompt or
			// warn); the live server keeps the last request, the mutation.
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
			// resp has every toggle off, so an "off" toggle would be a no-op
			// that sends nothing (#187); give those a domain where it is on.
			resp := resp
			if tc.args[0] == "off" {
				resp = toggleStub(false)
			}
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
	cmd.Flags().IntVar(&listPage, "page", 1, "")
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
		cmd.Flags().IntVar(&listPage, "page", 1, "")
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
		cmd.Flags().IntVar(&listPage, "page", 1, "")
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
				// The opposite state, so the toggle is a change and is sent.
				_, _ = w.Write([]byte(toggleStub(tc.wantValue)))
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

// Contacts is a pointer with omitempty in the SDK. The table path dereferenced
// it for the verification warning, so a GetDomain response without "contacts"
// panicked. It must render as empty instead.
func TestContactsGet_OmittedContactsDoesNotPanic(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"domainName":"example.com"}`))
	}))
	t.Cleanup(srv.Close)

	cmd := cmdForContactsGet(t, srv)
	if err := runContactsGet(cmd, []string{"example.com"}); err != nil {
		t.Fatalf("runContactsGet: %v", err)
	}
	if got := cmdutil.Out(cmd).EWriter.(*bytes.Buffer).String(); strings.Contains(strings.ToLower(got), "verif") {
		t.Errorf("no contacts means nothing to warn about, got:\n%s", got)
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
					_ = json.NewEncoder(w).Encode(coreapigo.PricingResponse{
						PurchasePrice: &price, RenewalPrice: &price,
					})
				case strings.Contains(r.URL.Path, "checkAvailability"):
					results := []*coreapigo.SearchResult{{DomainName: "example.com", Purchasable: true, PurchasePrice: &price}}
					_ = json.NewEncoder(w).Encode(coreapigo.SearchResponse{Results: results})
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
			// The term price is what must not read "/yr"; register's prompt
			// also states the yearly renewal rate, which is a different figure
			// for a multi-year term (#235).
			if hasYr := strings.Contains(prompt, "$699.90/yr"); hasYr != tc.wantYr {
				t.Errorf("years=%s prompt %q: $699.90/yr present=%v, want %v", tc.years, prompt, hasYr, tc.wantYr)
			}
			if !tc.wantYr && !strings.Contains(prompt, "total for "+tc.years) &&
				!strings.Contains(prompt, tc.years+" years: $699.90 total") {
				t.Errorf("multi-year prompt should state the term total, got: %s", prompt)
			}
		})
	}
}

// TestUpdate_PrivacyOnDoesNotPrompt: `domain update --privacy=true` used to
// confirm, like `domain privacy on`, but neither charges — they turn on privacy
// already purchased, or fail (#187). Both dropped the prompt (#227), and
// update no longer reads the domain for it.
func TestUpdate_PrivacyOnDoesNotPrompt(t *testing.T) {
	defer output.StubInteractive(false)()

	var requests []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"domainName":"example.com","privacyEnabled":false}`))
	}))
	t.Cleanup(srv.Close)

	// No --yes: a prompt would fail non-interactively.
	cmd := withRootFlags(t, cmdForUpdate(t, srv))
	if err := cmd.Flags().Set("privacy", "true"); err != nil {
		t.Fatalf("setting privacy flag: %v", err)
	}
	if err := runUpdate(cmd, []string{"example.com"}); err != nil {
		t.Fatalf("enabling privacy must not need --yes: %v", err)
	}
	if strings.Join(requests, " ") != http.MethodPatch {
		t.Errorf("want a single PATCH, got %v", requests)
	}
}

// TestUpdate_NonBillableChangesDoNotPrompt is the counterweight: locking a
// domain carries no risk, so it must stay frictionless.
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
	if err := cmd.Flags().Set("lock", "true"); err != nil {
		t.Fatalf("setting lock flag: %v", err)
	}

	if err := runUpdate(cmd, []string{"example.com"}); err != nil {
		t.Fatalf("locking must not require confirmation: %v", err)
	}
	if !patched {
		t.Error("the update was not sent")
	}
}

// TestUpdate_PreservesUnmentionedSettings guards the one command that can turn
// three separate protections off by accident.
//
// UpdateDomain is a PATCH, and the CLI sends only the flags the user passed.
// Send an unpassed flag at its zero value instead of leaving it out and
// `domain update --autorenew=true` would ALSO unlock the domain and switch off
// WHOIS privacy without printing a word about either. Unlocking is what makes
// an unauthorized transfer possible, and dropping privacy republishes the
// registrant's name, address, and phone number in public WHOIS.
//
// Restating the current value instead is not safe either: it is what #116
// broke on — see TestUpdate_TransferLockedDomainDoesNotSendLocked. So the
// unpassed fields must be absent, not merely true.
//
// The fixture sets them to true on purpose, so a body that restated current
// state would show up here rather than pass as "false, the zero value".
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
	if err := root.PersistentFlags().Set("yes", "true"); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Flags().Set("autorenew", "true"); err != nil {
		t.Fatalf("setting autorenew flag: %v", err)
	}

	if err := runUpdate(cmd, []string{"example.com"}); err != nil {
		t.Fatalf("runUpdate: %v", err)
	}
	if patchBody == nil {
		t.Fatal("the update was never sent")
	}

	var sent map[string]*bool
	if err := json.Unmarshal(patchBody, &sent); err != nil {
		t.Fatalf("PATCH body was not JSON: %v (%s)", err, patchBody)
	}
	if v := sent["autorenewEnabled"]; v == nil || !*v {
		t.Errorf("--autorenew=true must reach the wire, got %s", patchBody)
	}
	if _, ok := sent["locked"]; ok {
		t.Errorf("--lock was not passed, so locked must be absent (false unlocks the domain): %s", patchBody)
	}
	if _, ok := sent["privacyEnabled"]; ok {
		t.Errorf("--privacy was not passed, so privacyEnabled must be absent (false exposes WHOIS data): %s", patchBody)
	}
}

// updateTransferLockedDomain is GET /core/v1/domains/{name} for a domain inside
// its 60-day transfer lock, cut down from a sandbox capture to the fields
// `domain update` could read. The contacts are dropped as irrelevant.
const updateTransferLockedDomain = `{"domainName":"example.com","createDate":"2026-09-29T05:37:38Z",` +
	`"expireDate":"2027-09-29T05:37:38Z","autorenewEnabled":false,"locked":true,` +
	`"locks":["clientTransferProhibited"],"transferLockExpiresAt":"2026-11-28T06:37:39Z",` +
	`"privacyEnabled":false,"nameservers":["ns1vwx.name.com","ns2gtx.name.com"],"renewalPrice":19.99}`

// TestUpdate_TransferLockedDomainDoesNotSendLocked reproduces #116. During the
// transfer lock the API rejects any UpdateDomain carrying `locked`, even an
// unchanged true, so `domain update --autorenew=true` failed because the CLI
// restated the lock it had just read. The stub rejects `locked` the way the
// sandbox does.
//
// The GET it makes is for the auto-renewal prompt (#227); what it read must
// still not leak into the body.
func TestUpdate_TransferLockedDomainDoesNotSendLocked(t *testing.T) {
	defer output.StubInteractive(false)()

	var requests []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method)
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPatch {
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if _, ok := body["locked"]; ok {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"message":"Invalid Argument","details":"Domain can not be unlocked until 2026-11-28 06:37:39"}`))
				return
			}
		}
		_, _ = w.Write([]byte(updateTransferLockedDomain))
	}))
	t.Cleanup(srv.Close)

	cmd := withRootFlags(t, cmdForUpdate(t, srv))
	if err := cmd.Root().PersistentFlags().Set("yes", "true"); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Flags().Set("autorenew", "true"); err != nil {
		t.Fatalf("setting autorenew flag: %v", err)
	}
	if err := runUpdate(cmd, []string{"example.com"}); err != nil {
		t.Fatalf("--autorenew alone must not touch the transfer lock: %v", err)
	}
	if strings.Join(requests, " ") != "GET PATCH" {
		t.Errorf("want GET then PATCH, got %v", requests)
	}
}

// TestUpdate_ReadsStateOnlyToPromptOrWarn pins why update GETs the domain: the
// prompts (#227) and the unlock warning depend on the current value. A flag
// restating the current state must not need --yes, and unlocking an unlocked
// domain has no consequence to warn about. --privacy=true never prompts, so
// it reads nothing.
func TestUpdate_ReadsStateOnlyToPromptOrWarn(t *testing.T) {
	defer output.StubInteractive(false)()

	for _, tc := range []struct {
		name, flag, current string
		yes, warn           bool
		want                string
	}{
		{"privacy on", "privacy=true", `"privacyEnabled":false`, false, false, "PATCH"},
		{"autorenew already on", "autorenew=true", `"autorenewEnabled":true`, false, false, "GET"},
		{"unlock a locked domain", "lock=false", `"locked":true`, true, true, "GET PATCH"},
		{"unlock an unlocked domain", "lock=false", `"locked":false`, false, false, "GET"},
		{"lock an unlocked domain", "lock=true", `"locked":false`, false, false, "GET PATCH"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var requests []string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests = append(requests, r.Method)
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"domainName":"example.com",` + tc.current + `}`))
			}))
			t.Cleanup(srv.Close)

			cmd := withRootFlags(t, cmdForUpdate(t, srv))
			name, value, _ := strings.Cut(tc.flag, "=")
			if err := cmd.Flags().Set(name, value); err != nil {
				t.Fatalf("setting %s: %v", name, err)
			}
			// Without --yes, a prompt here fails non-interactively.
			if tc.yes {
				if err := cmd.Root().PersistentFlags().Set("yes", "true"); err != nil {
					t.Fatal(err)
				}
			}
			if err := runUpdate(cmd, []string{"example.com"}); err != nil {
				t.Fatalf("runUpdate: %v", err)
			}
			if got := strings.Join(requests, " "); got != tc.want {
				t.Errorf("want %s, got %s", tc.want, got)
			}
			stderr := cmdutil.Out(cmd).EWriter.(*bytes.Buffer).String()
			if got := strings.Contains(stderr, "Transfer lock removed"); got != tc.warn {
				t.Errorf("unlock warning shown = %v, want %v; stderr: %q", got, tc.warn, stderr)
			}
		})
	}
}

// TestUpdate_DropsFieldsAlreadySet reproduces #287. During the 60-day
// transfer lock the API refuses any body carrying `locked`, even an unchanged
// true, so `--lock=true --autorenew=true` on a locked domain failed and took
// the auto-renewal change with it. A field already in the requested state is
// left out; when every field is, nothing is sent, and the result says so.
// The stub refuses `locked` the way the sandbox does.
func TestUpdate_DropsFieldsAlreadySet(t *testing.T) {
	defer output.StubInteractive(false)()

	for _, tc := range []struct {
		name     string
		flags    []string
		dryRun   bool
		requests string
		body     string // the PATCH body, or the previewed one under dryRun
		stdout   string
	}{
		{"lock and autorenew", []string{"--lock=true", "--autorenew=true"}, false, "GET PATCH", `{"autorenewEnabled":true}`, ""},
		{"lock and autorenew, dry run", []string{"--lock=true", "--autorenew=true"}, true, "GET", `{"autorenewEnabled":true}`, ""},
		{"lock alone", []string{"--lock=true"}, false, "GET", "", `"changed":false`},
		{"lock alone, dry run", []string{"--lock=true"}, true, "GET", "", `"changed":false`},
		{"every flag unchanged", []string{"--lock=true", "--privacy=false", "--autorenew=false"}, false, "GET", "", `"changed":false`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var requests []string
			var body string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests = append(requests, r.Method)
				w.Header().Set("Content-Type", "application/json")
				if r.Method == http.MethodPatch {
					b, _ := io.ReadAll(r.Body)
					body = string(b)
					if strings.Contains(body, "locked") {
						w.WriteHeader(http.StatusBadRequest)
						_, _ = w.Write([]byte(`{"message":"Invalid Argument","details":"Domain can not be unlocked until 2026-11-28 06:37:39"}`))
						return
					}
				}
				_, _ = w.Write([]byte(updateTransferLockedDomain))
			}))
			t.Cleanup(srv.Close)

			cmd := withRootFlags(t, cmdForUpdate(t, srv))
			out := cmdutil.Out(cmd)
			out.Format = output.FormatJSON
			if err := cmd.Root().PersistentFlags().Set("yes", "true"); err != nil {
				t.Fatal(err)
			}
			if tc.dryRun {
				if err := cmd.Root().PersistentFlags().Set("dry-run", "true"); err != nil {
					t.Fatal(err)
				}
			}
			if err := cmd.ParseFlags(tc.flags); err != nil {
				t.Fatal(err)
			}
			if err := runUpdate(cmd, []string{"example.com"}); err != nil {
				t.Fatalf("runUpdate: %v", err)
			}
			if got := strings.Join(requests, " "); got != tc.requests {
				t.Errorf("requests = %s, want %s", got, tc.requests)
			}
			stdout := out.Writer.(*bytes.Buffer).String()
			if tc.dryRun {
				body = stdout
			}
			squash := strings.NewReplacer(" ", "", "\n", "").Replace
			if tc.body != "" && !strings.Contains(squash(body), tc.body) {
				t.Errorf("body = %s, want %s", body, tc.body)
			}
			if strings.Contains(body, "locked") {
				t.Errorf("an unchanged lock must not be sent: %s", body)
			}
			if tc.stdout != "" && !strings.Contains(squash(stdout), tc.stdout) {
				t.Errorf("stdout = %s, want it to contain %s", stdout, tc.stdout)
			}
		})
	}
}

// TestUnlock_WarnsOfTransferLock: unlocking a domain in its 60-day transfer
// lock will be refused, and the GET each command already makes says until
// when (#287). The dry run says so instead of previewing a clean unlock.
func TestUnlock_WarnsOfTransferLock(t *testing.T) {
	defer output.StubInteractive(false)()

	for _, tc := range []struct {
		name string
		run  func(*cobra.Command) error
	}{
		{"domain update --lock=false", func(cmd *cobra.Command) error {
			if err := cmd.Flags().Set("lock", "false"); err != nil {
				return err
			}
			return runUpdate(cmd, []string{"example.com"})
		}},
		{"domain lock off", func(cmd *cobra.Command) error { return runLock(cmd, []string{"off", "example.com"}) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var requests []string
			future := time.Now().Add(30 * 24 * time.Hour).UTC().Format(time.RFC3339)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests = append(requests, r.Method)
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"domainName":"example.com","locked":true,"transferLockExpiresAt":"` + future + `"}`))
			}))
			t.Cleanup(srv.Close)

			cmd := withRootFlags(t, cmdForUpdate(t, srv))
			if err := cmd.Root().PersistentFlags().Set("dry-run", "true"); err != nil {
				t.Fatal(err)
			}
			if err := tc.run(cmd); err != nil {
				t.Fatalf("dry run: %v", err)
			}
			if got := strings.Join(requests, " "); got != "GET" {
				t.Errorf("requests = %s, want GET", got)
			}
			stderr := cmdutil.Out(cmd).EWriter.(*bytes.Buffer).String()
			if !strings.Contains(stderr, "transfer lock until "+future[:10]) || !strings.Contains(stderr, "refuse") {
				t.Errorf("want the transfer-lock warning, stderr: %q", stderr)
			}
		})
	}
}

// TestUpdate_UnlockWarningOnlyAfterSuccess pins #167: during the 60-day
// transfer lock the API refuses --lock=false, and the warning that the lock
// was removed used to print before that refusal, claiming a change that never
// happened.
func TestUpdate_UnlockWarningOnlyAfterSuccess(t *testing.T) {
	defer output.StubInteractive(false)()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPatch {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"message":"Invalid Argument","details":"Domain can not be unlocked until 2026-11-28 06:37:39"}`))
			return
		}
		_, _ = w.Write([]byte(`{"domainName":"example.com","locked":true}`))
	}))
	t.Cleanup(srv.Close)

	cmd := withRootFlags(t, cmdForUpdate(t, srv))
	if err := cmd.Flags().Set("lock", "false"); err != nil {
		t.Fatalf("setting lock: %v", err)
	}
	if err := runUpdate(cmd, []string{"example.com"}); err == nil {
		t.Fatal("want the API's refusal as an error")
	}
	if stderr := cmdutil.Out(cmd).EWriter.(*bytes.Buffer).String(); strings.Contains(stderr, "lock removed") {
		t.Errorf("a refused unlock must not report the lock removed; stderr: %q", stderr)
	}
}

// TestUpdate_NoFlagsIsAUsageError:with only changed fields sent, an update
// with no flags would PATCH `{}`, which the API rejects — it requires at least
// one field. It used to restate the current values, a write that changed
// nothing and printed "Updated". Neither is useful, so it is refused before
// any request.
func TestUpdate_NoFlagsIsAUsageError(t *testing.T) {
	cmd := withRootFlags(t, cmdForUpdate(t, neverCalledServer(t)))
	err := runUpdate(cmd, []string{"example.com"})
	if _, ok := errors.AsType[*cmdutil.UsageError](err); !ok {
		t.Fatalf("want a usage error, got %v", err)
	}
}

// withRootFlags parents cmd under a root carrying the persistent --dry-run and
// --yes flags, both unset, as runUpdate reads them.
func withRootFlags(t *testing.T, cmd *cobra.Command) *cobra.Command {
	t.Helper()
	root := &cobra.Command{Use: "namecom"}
	var dr, yes bool
	root.PersistentFlags().BoolVar(&dr, "dry-run", false, "")
	root.PersistentFlags().BoolVarP(&yes, "yes", "y", false, "")
	root.AddCommand(cmd)
	return cmd
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
			ptype := coreapigo.SearchPurchaseType("aftermarket_b")
			results := []*coreapigo.SearchResult{{
				DomainName: "example.com", Purchasable: true,
				PurchasePrice: &price, PurchaseType: &ptype,
			}}
			_ = json.NewEncoder(w).Encode(coreapigo.SearchResponse{Results: results})
		case strings.Contains(r.URL.Path, "claims"):
			_, _ = w.Write([]byte(`{"domain":"example.com","claimsProcessActive":false,"claimId":null,"claims":[]}`))
		case strings.Contains(r.URL.Path, "getPricing"):
			_ = json.NewEncoder(w).Encode(coreapigo.PricingResponse{PurchasePrice: &price})
		case r.URL.Path == "/core/v1/accountinfo/balance":
			_, _ = w.Write([]byte(`{"balance":120}`)) // for the dry run's quote (#271)
		default:
			created = true
			_ = json.NewEncoder(w).Encode(coreapigo.CreateDomainResponse{})
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

// TestRegister_PromptQuotesThePriceSent guards issue #83: the confirmation
// prompt quoted GetPricingForDomain's standard registration price, while the
// body sent the availability check's acquisition price or --price. Answering
// yes to "$12.99/yr" submitted a $2500 purchase.
//
// The two endpoints return different prices here on purpose — with identical
// prices, as in TestRegister_DryRunPreviewsTheRealBody, a mismatch can't show.
// Without --yes in a non-interactive test, Confirm's error carries the prompt.
func TestRegister_PromptQuotesThePriceSent(t *testing.T) {
	tests := []struct {
		name  string
		price string // --price, empty for none
		want  string
	}{
		{"aftermarket check price", "", "$2,500.00"},
		{"--price override", "3000", "$3,000.00"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checkPrice, pricingPrice := 2500.00, 12.99
			var created bool
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch {
				case strings.Contains(r.URL.Path, "checkAvailability"):
					ptype := coreapigo.SearchPurchaseType("aftermarket_b")
					results := []*coreapigo.SearchResult{{
						DomainName: "example.com", Purchasable: true,
						PurchasePrice: &checkPrice, PurchaseType: &ptype,
					}}
					_ = json.NewEncoder(w).Encode(coreapigo.SearchResponse{Results: results})
				case strings.Contains(r.URL.Path, "getPricing"):
					_ = json.NewEncoder(w).Encode(coreapigo.PricingResponse{PurchasePrice: &pricingPrice})
				case strings.Contains(r.URL.Path, "claims"):
					// The claims check runs before the prompt, so the body
					// is complete when it is confirmed.
					_, _ = w.Write([]byte(`{"domain":"example.com","claimsProcessActive":false,"claimId":null,"claims":[]}`))
				default:
					created = true
					_ = json.NewEncoder(w).Encode(coreapigo.CreateDomainResponse{})
				}
			}))
			t.Cleanup(srv.Close)

			cmd := cmdForRegister(t, srv)
			if tt.price != "" {
				if err := cmd.Flags().Set("price", tt.price); err != nil {
					t.Fatal(err)
				}
			}
			err := runRegister(cmd, []string{"example.com"})
			if err == nil {
				t.Fatal("expected the non-interactive confirm error")
			}
			if created {
				t.Fatal("registered without confirmation")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("prompt does not quote the price sent (%s):\n%v", tt.want, err)
			}
			if strings.Contains(err.Error(), "12.99") {
				t.Errorf("prompt quotes the standard registration price, which is not sent:\n%v", err)
			}
		})
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
			ptype := coreapigo.SearchPurchaseType("landrush_eap")
			results := []*coreapigo.SearchResult{{
				DomainName: "example.com", Purchasable: true,
				PurchasePrice: &price, PurchaseType: &ptype,
			}}
			_ = json.NewEncoder(w).Encode(coreapigo.SearchResponse{Results: results})
		case strings.Contains(r.URL.Path, "claims"):
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if v, ok := body["purchaseType"].(string); ok {
				claimsPurchaseTypeSent = v
			}
			_, _ = w.Write([]byte(`{"domain":"example.com","claimsProcessActive":false,"claimId":null,"claims":[]}`))
		case strings.Contains(r.URL.Path, "getPricing"):
			_ = json.NewEncoder(w).Encode(coreapigo.PricingResponse{PurchasePrice: &price})
		default:
			_ = json.NewEncoder(w).Encode(coreapigo.CreateDomainResponse{})
		}
	}))
	t.Cleanup(srv.Close)

	cmd := cmdForRegister(t, srv)
	if err := cmd.PersistentFlags().Set("yes", "true"); err != nil {
		t.Fatalf("setting yes flag: %v", err)
	}
	registerAccept = true // a non-standard price needs --accept-premium (#226)
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

// TestRegister_UnreadableContactsFileFailsBeforeThePrompt guards the same
// ordering for --contacts-file.
//
// The file was read after the price confirmation, so a typo in its path, or a
// malformed file, made the user approve a charge before being told the request
// could not be built. The body is now complete before anything is confirmed.
func TestRegister_UnreadableContactsFileFailsBeforeThePrompt(t *testing.T) {
	defer output.StubInteractive(false)()
	price := 12.99
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "checkAvailability"):
			results := []*coreapigo.SearchResult{{DomainName: "example.com", Purchasable: true, PurchasePrice: &price}}
			_ = json.NewEncoder(w).Encode(coreapigo.SearchResponse{Results: results})
		case strings.Contains(r.URL.Path, "getPricing"):
			_ = json.NewEncoder(w).Encode(coreapigo.PricingResponse{PurchasePrice: &price})
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
			http.Error(w, "unexpected", http.StatusInternalServerError)
		}
	}))
	t.Cleanup(srv.Close)

	cmd := cmdForRegister(t, srv)
	t.Cleanup(func() { registerContactsFile = "" })
	if err := cmd.Flags().Set("contacts-file", filepath.Join(t.TempDir(), "missing.json")); err != nil {
		t.Fatal(err)
	}

	// No --yes and no TTY: had the prompt come first, this would fail with
	// "pass --yes to confirm" instead.
	err := runRegister(cmd, []string{"example.com"})
	if err == nil || !strings.Contains(err.Error(), "reading contacts file") {
		t.Fatalf("want the contacts file error before any prompt, got: %v", err)
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

// TestRegister_PromptWordingByPurchaseKind guards issue #132. With the price
// right (#83), the words around it still misled:
//
//   - A registry premium name read "at $1,000.00/yr" for shoe.luxe, whose
//     premium applies to the purchase while it renews at $24.99 (sandbox
//     pricing, pricing_premium_shoe_luxe). "/yr" says $1000 every year.
//   - An aftermarket name read "for 3 year(s)", which the API does not
//     guarantee for acquisition purchase types.
//
// The pricing shapes are the sandbox's, not hand-invented ones.
func TestRegister_PromptWordingByPurchaseKind(t *testing.T) {
	defer output.StubInteractive(false)()

	tests := []struct {
		name         string
		years        string
		pricing      string // GetPricingForDomain response
		purchaseType string // availability check purchaseType
		checkPrice   float64
		want         []string
		notWant      []string
	}{
		{
			name:         "premium with renewal price",
			years:        "1",
			pricing:      `{"premium":true,"purchasePrice":1000,"renewalPrice":24.99,"transferPrice":24.99}`,
			purchaseType: "registration", checkPrice: 1000,
			want:    []string{"Register shoe.luxe for 1 year: $1,000.00 (premium; renews at $24.99/yr), without WHOIS privacy or auto-renew?"},
			notWant: []string{"$1,000.00/yr"},
		},
		{
			name:         "premium without renewal price",
			years:        "1",
			pricing:      `{"premium":true,"purchasePrice":1000}`,
			purchaseType: "registration", checkPrice: 1000,
			want:    []string{": $1,000.00 (premium), without"},
			notWant: []string{"/yr", "renews"},
		},
		{
			name: "premium multi-year",
			// The years:2 figures are totals for the term, renewal included:
			// PricingResponse.RenewalPrice is "the total renewal cost for the
			// requested years".
			years:        "2",
			pricing:      `{"premium":true,"purchasePrice":1523.08,"renewalPrice":1523.08,"transferPrice":761.54}`,
			purchaseType: "registration", checkPrice: 761.54,
			want:    []string{"for 2 years: $1,523.08 total (premium; renews at $761.54/yr), without"},
			notWant: []string{"$1,523.08/yr"},
		},
		{
			name:         "aftermarket drops the year count",
			years:        "3",
			pricing:      `{"premium":false,"purchasePrice":12.99,"renewalPrice":12.99}`,
			purchaseType: "aftermarket_b", checkPrice: 2500,
			want:    []string{"Register shoe.luxe at $2,500.00 flat (aftermarket_b, not per year", "--years 3"},
			notWant: []string{"year(s)", "/yr", "12.99"},
		},
		{
			name:         "aftermarket single year says nothing of years",
			years:        "1",
			pricing:      `{"premium":false,"purchasePrice":12.99,"renewalPrice":12.99}`,
			purchaseType: "aftermarket_b", checkPrice: 2500,
			want:    []string{"Register shoe.luxe at $2,500.00 flat (aftermarket_b, not per year), without WHOIS privacy or auto-renew?"},
			notWant: []string{"year(s)", "--years"},
		},
		{
			name:         "standard unchanged",
			years:        "1",
			pricing:      `{"premium":false,"purchasePrice":12.99,"renewalPrice":12.99,"transferPrice":12.99}`,
			purchaseType: "registration", checkPrice: 12.99,
			want:    []string{"Register shoe.luxe for 1 year: $12.99 (renews at $12.99/yr), without WHOIS privacy or auto-renew?"},
			notWant: []string{"premium"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch {
				case strings.Contains(r.URL.Path, "checkAvailability"):
					ptype := coreapigo.SearchPurchaseType(tt.purchaseType)
					price := tt.checkPrice
					results := []*coreapigo.SearchResult{{
						DomainName: "shoe.luxe", Purchasable: true,
						PurchasePrice: &price, PurchaseType: &ptype,
					}}
					_ = json.NewEncoder(w).Encode(coreapigo.SearchResponse{Results: results})
				case strings.Contains(r.URL.Path, "getPricing"):
					_, _ = w.Write([]byte(tt.pricing))
				case strings.Contains(r.URL.Path, "claims"):
					_, _ = w.Write([]byte(`{"domain":"shoe.luxe","claims":[],"claimsProcessActive":false,"claimId":null,"notBefore":null,"notAfter":null,"claimsNotice":""}`))
				default:
					t.Errorf("registered without confirmation: %s %s", r.Method, r.URL)
					http.Error(w, "unexpected", http.StatusInternalServerError)
				}
			}))
			t.Cleanup(srv.Close)

			cmd := cmdForRegister(t, srv)
			if err := cmd.Flags().Set("years", tt.years); err != nil {
				t.Fatal(err)
			}
			// Past the premium gate, so the refusal is the purchase prompt's.
			registerAccept = true
			err := runRegister(cmd, []string{"shoe.luxe"})
			if err == nil {
				t.Fatal("expected the non-interactive confirm error")
			}
			for _, w := range tt.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("prompt lacks %q:\n%v", w, err)
				}
			}
			for _, nw := range tt.notWant {
				if strings.Contains(err.Error(), nw) {
					t.Errorf("prompt should not contain %q:\n%v", nw, err)
				}
			}
		})
	}
}

// TestContactFiles_BadFileIsUsageError pins that `domain register
// --contacts-file` and `domain contacts set --contacts-file` reject a missing or
// invalid file before any request or prompt, with exit 2, as `transfer create`
// does. Register used to read the file only after the availability check,
// the guided form and the pricing lookup, and both exited 1.
func TestContactFiles_BadFileIsUsageError(t *testing.T) {
	t.Cleanup(output.StubInteractive(false))
	files := map[string]func(t *testing.T) string{
		"missing": func(t *testing.T) string { return filepath.Join(t.TempDir(), "missing.json") },
		"invalid JSON": func(t *testing.T) string {
			p := filepath.Join(t.TempDir(), "contacts.json")
			if err := os.WriteFile(p, []byte(`not json`), 0o600); err != nil {
				t.Fatalf("writing contacts file: %v", err)
			}
			return p
		},
	}
	cmds := map[string]func(t *testing.T, path string) error{
		"register": func(t *testing.T, path string) error {
			cmd := cmdForRegister(t, neverCalledServer(t))
			t.Cleanup(func() { registerContactsFile = "" })
			if err := cmd.ParseFlags([]string{"--contacts-file", path}); err != nil {
				t.Fatalf("ParseFlags: %v", err)
			}
			return runRegister(cmd, []string{"example.com"})
		},
		"contacts set": func(t *testing.T, path string) error {
			cmd := baseCmd(t, neverCalledServer(t))
			t.Cleanup(func() { contactsFile = "" })
			cmd.Flags().StringVar(&contactsFile, "contacts-file", path, "")
			return runContactsSet(cmd, []string{"example.com"})
		},
	}
	for cname, run := range cmds {
		for fname, file := range files {
			t.Run(cname+"/"+fname, func(t *testing.T) {
				err := run(t, file(t))
				var usage *cmdutil.UsageError
				if !errors.As(err, &usage) {
					t.Fatalf("bad contacts file should be a usage error (exit 2), got %T: %v", err, err)
				}
				if !strings.Contains(err.Error(), "contacts file") {
					t.Errorf("error should name the contacts file, got: %v", err)
				}
			})
		}
	}
}

// TestContactsSet_FromFileIsADeprecatedAlias pins #236: the contacts file is
// --contacts-file everywhere, and contacts set's old --from-file still fills
// the same variable, hidden from help.
func TestContactsSet_FromFileIsADeprecatedAlias(t *testing.T) {
	t.Cleanup(func() {
		contactsFile = ""
		for _, name := range []string{"contacts-file", "from-file"} {
			if f := contactsSetCmd.Flags().Lookup(name); f != nil {
				f.Changed = false
			}
		}
	})
	if err := contactsSetCmd.Flags().Set("from-file", "old.json"); err != nil {
		t.Fatalf("setting --from-file: %v", err)
	}
	if contactsFile != "old.json" {
		t.Errorf("--from-file set contactsFile to %q, want old.json", contactsFile)
	}
	f := contactsSetCmd.Flags().Lookup("from-file")
	if !f.Hidden || f.Deprecated == "" {
		t.Errorf("--from-file hidden=%v deprecated=%q, want hidden and deprecated", f.Hidden, f.Deprecated)
	}
	if contactsSetCmd.Flags().Lookup("contacts-file") == nil {
		t.Error("contacts set has no --contacts-file")
	}
}
