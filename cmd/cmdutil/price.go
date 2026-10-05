package cmdutil

import (
	"math"

	"github.com/patramsey/namecom-cli/internal/output"
	"github.com/spf13/cobra"
)

// MaxPriceUsage and AcceptPremiumUsage are the help for the two spending
// gates, shared by every command that buys something (#226).
const (
	MaxPriceUsage = "refuse, before sending anything, if the price charged for this order " +
		"(the total for the term, in USD) is above this amount"
	AcceptPremiumUsage = "allow a premium, aftermarket, expiring or backorder price; required " +
		"to buy at one without an interactive prompt (--yes does NOT cover this)"
)

// ValidMaxPrice checks a --max-price value, when one was passed. Commands call
// it with their other flag checks, before any request.
func ValidMaxPrice(cmd *cobra.Command, maxPrice float64) error {
	if !cmd.Flags().Changed("max-price") {
		return nil
	}
	if math.IsNaN(maxPrice) || math.IsInf(maxPrice, 0) || maxPrice <= 0 {
		return usagef("--max-price must be a positive amount in USD (got %v)", maxPrice)
	}
	return nil
}

// CheckMaxPrice enforces --max-price against price, the amount the request
// will charge. It does nothing when the flag was not passed. A price above
// the cap, or no price to compare, is a usage error (exit 2) naming both
// numbers, and the caller sends nothing.
//
// It runs under --dry-run too: like any other check of the invocation, a
// refusal is what the real run would do, and a dry run should say so.
//
// --price was documented as a cap but was only ever sent as purchasePrice,
// unchecked against the quote, so nothing on the client could stop a
// purchase above a figure the user had in mind.
func CheckMaxPrice(cmd *cobra.Command, maxPrice float64, what string, price *float64) error {
	if err := ValidMaxPrice(cmd, maxPrice); err != nil || !cmd.Flags().Changed("max-price") {
		return err
	}
	if price == nil {
		return usagef("no price was quoted for %s, so --max-price %s cannot be checked; nothing was sent", what, output.Money(maxPrice))
	}
	// Compared in cents, so a quote of 17.99 is not "above" a cap of 17.99
	// because of how either was parsed.
	if math.Round(*price*100) > math.Round(maxPrice*100) {
		return usagef("%s costs %s, above --max-price %s; nothing was sent", what, output.Money(*price), output.Money(maxPrice))
	}
	return nil
}

// RequireAcceptPremium gates a purchase at a premium, aftermarket, expiring or
// backorder price. desc says what is being bought and for how much; it opens
// the error.
//
// Like --acknowledge-claim, it is deliberately NOT satisfied by --yes, which
// people set in wrappers and aliases: `domain register shoes.shop --yes` bought
// a $6,250-a-year name with no more friction than a $10 one. The acceptance is
// either --accept-premium or an answer to the interactive purchase prompt,
// which quotes the price. Under --dry-run nothing is bought, so nothing needs
// accepting; the caller shows a hint instead.
func RequireAcceptPremium(cmd *cobra.Command, accepted bool, desc string) error {
	if accepted || IsDryRun(cmd) {
		return nil
	}
	if output.IsInteractive() && !IsYes(cmd) {
		return nil
	}
	return usagef("%s — pass --accept-premium to buy at this price (--yes does not cover this)", desc)
}
