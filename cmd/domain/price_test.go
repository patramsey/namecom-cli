package domain

import (
	"errors"
	"testing"

	"github.com/patramsey/namecom-cli/cmd/cmdutil"
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
