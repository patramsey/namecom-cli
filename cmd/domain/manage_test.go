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

// ---- register years ---------------------------------------------------------

func cmdForRegister(t *testing.T, srv *httptest.Server) *cobra.Command {
	t.Helper()
	cmd := baseCmd(t, srv)
	cmd.Flags().IntVar(&registerYears, "years", 1, "")
	cmd.Flags().BoolVar(&registerPrivacy, "privacy", false, "")
	cmd.Flags().BoolVar(&registerAutorenew, "autorenew", false, "")
	cmd.Flags().StringVar(&registerContactsFile, "contacts-file", "", "")
	cmd.Flags().Float64Var(&registerPrice, "price", 0, "")
	cmd.Flags().StringVar(&registerIdemKey, "idempotency-key", "", "")
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
	var yes bool
	cmd.PersistentFlags().BoolVarP(&yes, "yes", "y", false, "")
	if err := cmd.PersistentFlags().Set("yes", "true"); err != nil {
		t.Fatalf("setting yes flag: %v", err)
	}
	t.Cleanup(func() { renewYears = 1 })
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
