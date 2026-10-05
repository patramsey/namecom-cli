package domain

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	coreapigo "github.com/namedotcom/core-api-go"
	"github.com/patramsey/namecom-cli/cmd/cmdutil"
	"github.com/patramsey/namecom-cli/internal/output"
)

// stubAskRegister replaces the guided form and counts how often it opened,
// recording the price line it was shown.
func stubAskRegister(t *testing.T) (calls *int, quote *string) {
	t.Helper()
	calls, quote = new(int), new(string)
	prev := askRegister
	askRegister = func(_, q string) error {
		*calls++
		*quote = q
		return nil
	}
	t.Cleanup(func() { askRegister = prev })
	return calls, quote
}

// TestRegister_GuidedFormNeverBlocksScriptsOrPreviews guards #239: the form
// opened under --dry-run, and with -o json in a terminal, stopping a preview
// or a script at a prompt. Structured output now fails before any request;
// --dry-run previews the defaults.
func TestRegister_GuidedFormNeverBlocksScriptsOrPreviews(t *testing.T) {
	for _, f := range []output.Format{output.FormatJSON, output.FormatYAML} {
		t.Run("-o "+string(f)+" fails fast", func(t *testing.T) {
			t.Cleanup(output.StubInteractive(true))
			calls, _ := stubAskRegister(t)
			srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				t.Errorf("sent %s %s before failing", r.Method, r.URL)
			}))
			t.Cleanup(srv.Close)

			cmd := cmdForRegister(t, srv)
			cmdutil.Out(cmd).Format = f
			err := runRegister(cmd, []string{"example.com"})
			if _, ok := errors.AsType[*cmdutil.UsageError](err); !ok {
				t.Fatalf("runRegister = %v, want a usage error", err)
			}
			if !strings.Contains(err.Error(), "--years") || !strings.Contains(err.Error(), "--yes") {
				t.Errorf("error does not say how to pass the options: %v", err)
			}
			if *calls != 0 {
				t.Error("the guided form opened")
			}
		})
	}

	t.Run("an option flag skips the form under -o json", func(t *testing.T) {
		t.Cleanup(output.StubInteractive(true))
		calls, _ := stubAskRegister(t)
		var gotCreate map[string]any
		var claims bool
		cmd := cmdForRegister(t, claimsServer(t, unclaimedResponse, &gotCreate, &claims))
		cmdutil.Out(cmd).Format = output.FormatJSON
		if err := cmd.ParseFlags([]string{"--years", "1", "--yes"}); err != nil {
			t.Fatal(err)
		}
		if err := runRegister(cmd, []string{"tiktok.page"}); err != nil {
			t.Fatalf("runRegister: %v", err)
		}
		if *calls != 0 {
			t.Error("the guided form opened")
		}
	})

	t.Run("--dry-run previews without the form", func(t *testing.T) {
		t.Cleanup(output.StubInteractive(true))
		calls, _ := stubAskRegister(t)
		var gotCreate map[string]any
		var claims bool
		cmd := cmdForRegister(t, claimsServer(t, unclaimedResponse, &gotCreate, &claims))
		cmd.PersistentFlags().Bool("dry-run", true, "")
		if err := runRegister(cmd, []string{"tiktok.page"}); err != nil {
			t.Fatalf("runRegister --dry-run: %v", err)
		}
		if *calls != 0 {
			t.Error("the guided form opened under --dry-run")
		}
		if gotCreate != nil {
			t.Error("--dry-run registered the domain")
		}
	})

	t.Run("table output in a terminal still opens it, with the price", func(t *testing.T) {
		t.Cleanup(output.StubInteractive(true))
		calls, quote := stubAskRegister(t)
		var gotCreate map[string]any
		var claims bool
		cmd := cmdForRegister(t, claimsServer(t, unclaimedResponse, &gotCreate, &claims))
		cmd.PersistentFlags().Bool("dry-run", false, "")
		// Stop at the confirmation that follows the form.
		t.Cleanup(cmdutil.StubConfirm(func(string) bool { return false }))
		_ = runRegister(cmd, []string{"tiktok.page"})
		if *calls != 1 {
			t.Fatalf("the guided form opened %d times, want 1", *calls)
		}
		if !strings.Contains(*quote, "$12.99") {
			t.Errorf("the form was shown %q, want the quoted price", *quote)
		}
	})
}

// TestRegister_GuidedFormDefaultsToPrivacyAndAutorenew: the form opens with
// both choices on, and declining nothing sends them in the body.
func TestRegister_GuidedFormDefaultsToPrivacyAndAutorenew(t *testing.T) {
	t.Cleanup(output.StubInteractive(true))
	var privacy, autorenew bool
	prev := askRegister
	askRegister = func(_, _ string) error {
		privacy, autorenew = registerPrivacy, registerAutorenew
		return nil
	}
	t.Cleanup(func() { askRegister = prev })
	registerPrivacy, registerAutorenew = false, false
	t.Cleanup(func() { registerPrivacy, registerAutorenew = false, false })

	var gotCreate map[string]any
	var claims bool
	cmd := cmdForRegister(t, claimsServer(t, unclaimedResponse, &gotCreate, &claims))
	cmd.PersistentFlags().Bool("dry-run", false, "")
	var prompt string
	t.Cleanup(cmdutil.StubConfirm(func(p string) bool { prompt = p; return false }))
	_ = runRegister(cmd, []string{"tiktok.page"})
	if !privacy || !autorenew {
		t.Errorf("form opened with privacy=%v autorenew=%v, want both on", privacy, autorenew)
	}
	if !strings.Contains(prompt, "with WHOIS privacy and auto-renew") {
		t.Errorf("confirmation %q does not state both choices", prompt)
	}
}

// TestRegisterQuote: the price line for each kind of purchase.
func TestRegisterQuote(t *testing.T) {
	p := func(f float64) *float64 { return &f }
	yes := true
	aftermarket := coreapigo.SearchPurchaseType("aftermarket")
	for _, tt := range []struct {
		name string
		r    *coreapigo.SearchResult
		want string
	}{
		{"no price", &coreapigo.SearchResult{}, ""},
		{"standard", &coreapigo.SearchResult{PurchasePrice: p(12.99), RenewalPrice: p(14.99)},
			"$12.99 for the first year, renews at $14.99/yr"},
		{"premium", &coreapigo.SearchResult{PurchasePrice: p(500), RenewalPrice: p(500), Premium: &yes},
			"Premium: $500.00 for the first year, renews at $500.00/yr"},
		{"aftermarket", &coreapigo.SearchResult{PurchasePrice: p(2500), PurchaseType: &aftermarket},
			"aftermarket purchase: $2500.00 (a flat price; more years do not change it)"},
	} {
		if got := registerQuote(tt.r); got != tt.want {
			t.Errorf("%s: registerQuote = %q, want %q", tt.name, got, tt.want)
		}
	}
}
