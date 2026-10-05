package cmdutil

import (
	"errors"
	"strings"
	"testing"

	"github.com/patramsey/namecom-cli/internal/output"
)

// TestCheckMaxPrice covers the client-side cap #226 asks for: above the cap,
// or with no price to compare, is a usage error (exit 2) naming both numbers.
func TestCheckMaxPrice(t *testing.T) {
	price := func(p float64) *float64 { return &p }
	tests := []struct {
		name  string
		max   string // "" leaves --max-price unset
		price *float64
		want  []string // substrings of the error; nil for no error
	}{
		{"unset", "", price(6250), nil},
		{"under", "100", price(17.99), nil},
		{"equal", "17.99", price(17.99), nil},
		{"above", "100", price(6250), []string{"$6,250.00", "--max-price $100.00", "nothing was sent"}},
		{"a cent above", "17.99", price(18.00), []string{"$18.00", "$17.99"}},
		{"no quote", "100", nil, []string{"no price was quoted", "$100.00"}},
		{"not positive", "0", price(1), []string{"--max-price must be a positive amount"}},
		{"NaN", "NaN", price(1), []string{"--max-price must be a positive amount"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cmd, _, _ := writeCmd(t, false, false)
			var maxPrice float64
			cmd.Flags().Float64Var(&maxPrice, "max-price", 0, "")
			if tc.max != "" {
				if err := cmd.Flags().Set("max-price", tc.max); err != nil {
					t.Fatal(err)
				}
			}
			err := CheckMaxPrice(cmd, maxPrice, "shoes.shop", tc.price)
			if tc.want == nil {
				if err != nil {
					t.Errorf("want no error, got %v", err)
				}
				return
			}
			var usage *UsageError
			if !errors.As(err, &usage) {
				t.Fatalf("want a usage error (exit 2), got %T: %v", err, err)
			}
			for _, w := range tc.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("error %q lacks %q", err, w)
				}
			}
		})
	}
}

// TestRequireAcceptPremium covers the premium gate #226 asks for. Like
// --acknowledge-claim, --yes does not satisfy it: only --accept-premium, or an
// answer to the interactive purchase prompt, does. A dry run buys nothing.
func TestRequireAcceptPremium(t *testing.T) {
	tests := []struct {
		name                          string
		interactive, yes, dry, accept bool
		wantErr                       bool
	}{
		{"script with --yes", false, true, false, false, true},
		{"script without --yes", false, false, false, false, true},
		{"terminal with --yes", true, true, false, false, true},
		{"terminal, prompt decides", true, false, false, false, false},
		{"--accept-premium", false, true, false, true, false},
		{"--dry-run", false, false, true, false, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			defer output.StubInteractive(tc.interactive)()
			cmd, _, _ := writeCmd(t, tc.dry, tc.yes)
			err := RequireAcceptPremium(cmd, tc.accept, "shoes.shop costs $6,250.00 (premium)")
			if !tc.wantErr {
				if err != nil {
					t.Errorf("want no error, got %v", err)
				}
				return
			}
			var usage *UsageError
			if !errors.As(err, &usage) {
				t.Fatalf("want a usage error (exit 2), got %T: %v", err, err)
			}
			for _, w := range []string{"shoes.shop costs $6,250.00", "--accept-premium", "--yes does not cover this"} {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("error %q lacks %q", err, w)
				}
			}
		})
	}
}

// TestCheckMaxPrice_DryRunWarns pins #247: under --dry-run a price above the
// cap is a warning, so the preview still prints, as the premium gate allows.
func TestCheckMaxPrice_DryRunWarns(t *testing.T) {
	price := 6250.0
	for name, p := range map[string]*float64{"above": &price, "no quote": nil} {
		t.Run(name, func(t *testing.T) {
			cmd, _, stderr := writeCmd(t, true, false)
			var maxPrice float64
			cmd.Flags().Float64Var(&maxPrice, "max-price", 0, "")
			if err := cmd.Flags().Set("max-price", "100"); err != nil {
				t.Fatal(err)
			}
			if err := CheckMaxPrice(cmd, maxPrice, "shoes.shop", p); err != nil {
				t.Fatalf("under --dry-run want no error, got %v", err)
			}
			if !strings.Contains(stderr.String(), "$100.00") || !strings.Contains(stderr.String(), "would be refused") {
				t.Errorf("stderr = %q, want a warning that the real run would refuse", stderr)
			}
		})
	}
	// A cap that is not a price is still an error: there is nothing to preview.
	cmd, _, _ := writeCmd(t, true, false)
	var maxPrice float64
	cmd.Flags().Float64Var(&maxPrice, "max-price", 0, "")
	_ = cmd.Flags().Set("max-price", "0")
	if err := CheckMaxPrice(cmd, maxPrice, "shoes.shop", &price); err == nil {
		t.Error("--max-price 0 under --dry-run: want a usage error")
	}
}
