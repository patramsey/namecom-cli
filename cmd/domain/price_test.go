package domain

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/patramsey/namecom-cli/cmd/cmdutil"
	"github.com/patramsey/namecom-cli/internal/output"
	"github.com/spf13/cobra"
)

// TestPriceFlag_RejectsNonPositive guards issue #168: --price Inf, NaN or a
// negative value is a usage error (exit 2) before any request or prompt. NaN
// and negatives were silently ignored, and Inf was quoted as "$+Inf" and then
// failed to marshal on send.
func TestPriceFlag_RejectsNonPositive(t *testing.T) {
	for _, price := range []string{"Inf", "-Inf", "NaN", "-5", "0"} {
		for name, run := range map[string]func(string) error{
			"register": func(p string) error {
				cmd := cmdForRegister(t, neverCalledServer(t))
				if err := cmd.ParseFlags([]string{"--price", p}); err != nil {
					t.Fatalf("ParseFlags: %v", err)
				}
				t.Cleanup(func() { registerPrice = 0 })
				return runRegister(cmd, []string{"example.com"})
			},
			"renew": func(p string) error {
				cmd := cmdForRenew(t, neverCalledServer(t))
				if err := cmd.ParseFlags([]string{"--price", p}); err != nil {
					t.Fatalf("ParseFlags: %v", err)
				}
				return runRenew(cmd, []string{"example.com"})
			},
		} {
			t.Run(name+" --price "+price, func(t *testing.T) {
				err := run(price)
				var usage *cmdutil.UsageError
				if !errors.As(err, &usage) {
					t.Errorf("want a usage error, got %T: %v", err, err)
				}
			})
		}
	}
}

// purchaseServer answers every read with stub, which carries the fields of
// the availability, pricing and claims responses at once, and counts the
// purchases: the register POST and the renew POST.
func purchaseServer(t *testing.T, stub string) (*httptest.Server, *int) {
	t.Helper()
	var writes int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/core/v1/domains" || strings.HasSuffix(r.URL.Path, ":renew") {
			writes++
		}
		_, _ = w.Write([]byte(stub))
	}))
	t.Cleanup(srv.Close)
	return srv, &writes
}

// standardStub is a $25 registration and renewal; premiumStub is a $6,250
// registry premium that renews at the same price, like shoes.shop in #226.
const (
	standardStub = `{"results":[{"domainName":"example.com","purchasable":true,"purchasePrice":25}],` +
		`"purchasePrice":25,"renewalPrice":25,"premium":false,"claimsProcessActive":false,"claims":[],"order":1,"totalPaid":25}`
	premiumStub = `{"results":[{"domainName":"example.com","purchasable":true,"purchasePrice":6250}],` +
		`"purchasePrice":6250,"renewalPrice":6250,"premium":true,"claimsProcessActive":false,"claims":[],"order":1,"totalPaid":6250}`
)

// purchaseRun runs register or renew against srv with flags, non-interactively.
// dryRun adds --dry-run; otherwise --yes is set.
type purchaseRun func(t *testing.T, srv *httptest.Server, dryRun bool, flags ...string) error

var purchaseRuns = map[string]purchaseRun{
	"register": func(t *testing.T, srv *httptest.Server, dryRun bool, flags ...string) error {
		cmd := cmdForRegister(t, srv)
		setPurchaseMode(t, cmd, dryRun)
		if err := cmd.ParseFlags(flags); err != nil {
			t.Fatalf("ParseFlags: %v", err)
		}
		t.Cleanup(func() { registerPrice = 0 })
		return runRegister(cmd, []string{"example.com"})
	},
	"renew": func(t *testing.T, srv *httptest.Server, dryRun bool, flags ...string) error {
		cmd := cmdForRenew(t, srv)
		setPurchaseMode(t, cmd, dryRun)
		if err := cmd.ParseFlags(flags); err != nil {
			t.Fatalf("ParseFlags: %v", err)
		}
		return runRenew(cmd, []string{"example.com"})
	},
}

// setPurchaseMode gives cmd a --dry-run flag and sets exactly one of --dry-run
// and --yes.
func setPurchaseMode(t *testing.T, cmd *cobra.Command, dryRun bool) {
	t.Helper()
	var dr bool
	cmd.PersistentFlags().BoolVar(&dr, "dry-run", false, "")
	flag, value := "yes", "true"
	if dryRun {
		flag = "dry-run"
		if err := cmd.PersistentFlags().Set("yes", "false"); err != nil {
			t.Fatal(err)
		}
	}
	if err := cmd.PersistentFlags().Set(flag, value); err != nil {
		t.Fatal(err)
	}
}

// TestPurchase_MaxPrice covers #226: --max-price refuses, before anything is
// bought, a price above it — exit 2, both numbers in the message — and does
// so under --dry-run too, since that is what the real run would do.
func TestPurchase_MaxPrice(t *testing.T) {
	defer output.StubInteractive(false)()
	for name, run := range purchaseRuns {
		t.Run(name+" above the cap", func(t *testing.T) {
			for _, dry := range []bool{false, true} {
				srv, writes := purchaseServer(t, standardStub)
				err := run(t, srv, dry, "--max-price", "20")
				var usage *cmdutil.UsageError
				if !errors.As(err, &usage) {
					t.Fatalf("dry-run=%v: want a usage error (exit 2), got %T: %v", dry, err, err)
				}
				for _, w := range []string{"$25.00", "--max-price $20.00"} {
					if !strings.Contains(err.Error(), w) {
						t.Errorf("dry-run=%v: error %q lacks %q", dry, err, w)
					}
				}
				if *writes != 0 {
					t.Errorf("dry-run=%v: refused, but %d purchase(s) were sent", dry, *writes)
				}
			}
		})
		t.Run(name+" within the cap", func(t *testing.T) {
			srv, writes := purchaseServer(t, standardStub)
			if err := run(t, srv, false, "--max-price", "25"); err != nil || *writes != 1 {
				t.Errorf("want one purchase and no error, got %d and %v", *writes, err)
			}
		})
		t.Run(name+" --price above the cap", func(t *testing.T) {
			// --price is what is sent, so it is what the cap is checked against.
			srv, writes := purchaseServer(t, standardStub)
			err := run(t, srv, false, "--price", "30", "--max-price", "25")
			if err == nil || !strings.Contains(err.Error(), "$30.00") || *writes != 0 {
				t.Errorf("want a refusal quoting $30.00 and no purchase, got %d purchase(s) and %v", *writes, err)
			}
		})
	}
}

// TestPurchase_PremiumNeedsAcceptPremium covers #226: --yes alone bought a
// $6,250-a-year name. A premium price now needs --accept-premium when there is
// no interactive prompt, and a dry run says so without failing.
func TestPurchase_PremiumNeedsAcceptPremium(t *testing.T) {
	defer output.StubInteractive(false)()
	for name, run := range purchaseRuns {
		t.Run(name+" --yes alone", func(t *testing.T) {
			srv, writes := purchaseServer(t, premiumStub)
			err := run(t, srv, false)
			var usage *cmdutil.UsageError
			if !errors.As(err, &usage) {
				t.Fatalf("want a usage error (exit 2), got %T: %v", err, err)
			}
			for _, w := range []string{"$6,250.00", "premium", "--accept-premium"} {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("error %q lacks %q", err, w)
				}
			}
			if *writes != 0 {
				t.Errorf("refused, but %d purchase(s) were sent", *writes)
			}
		})
		t.Run(name+" --accept-premium", func(t *testing.T) {
			srv, writes := purchaseServer(t, premiumStub)
			if err := run(t, srv, false, "--accept-premium"); err != nil || *writes != 1 {
				t.Errorf("want one purchase and no error, got %d and %v", *writes, err)
			}
		})
		t.Run(name+" --dry-run", func(t *testing.T) {
			srv, writes := purchaseServer(t, premiumStub)
			if err := run(t, srv, true); err != nil || *writes != 0 {
				t.Errorf("want a preview and no purchase, got %d purchase(s) and %v", *writes, err)
			}
		})
		t.Run(name+" standard price needs nothing", func(t *testing.T) {
			srv, writes := purchaseServer(t, standardStub)
			if err := run(t, srv, false); err != nil || *writes != 1 {
				t.Errorf("want one purchase and no error, got %d and %v", *writes, err)
			}
		})
	}
}
