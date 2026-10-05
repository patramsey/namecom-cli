package cmdutil

import (
	"fmt"
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
// Under --dry-run it warns instead and lets the preview print, as the premium
// gate does (#247): a dry run that only says "refused" hides the request the
// user asked to see. The warning says the real run would refuse.
//
// --price was documented as a cap but was only ever sent as purchasePrice,
// unchecked against the quote, so nothing on the client could stop a
// purchase above a figure the user had in mind.
func CheckMaxPrice(cmd *cobra.Command, maxPrice float64, what string, price *float64) error {
	if err := ValidMaxPrice(cmd, maxPrice); err != nil || !cmd.Flags().Changed("max-price") {
		return err
	}
	var refusal string
	switch {
	case price == nil:
		refusal = fmt.Sprintf("no price was quoted for %s, so --max-price %s cannot be checked", what, output.Money(maxPrice))
	// Compared in cents, so a quote of 17.99 is not "above" a cap of 17.99
	// because of how either was parsed.
	case math.Round(*price*100) > math.Round(maxPrice*100):
		refusal = fmt.Sprintf("%s costs %s, above --max-price %s", what, output.Money(*price), output.Money(maxPrice))
	default:
		return nil
	}
	if IsDryRun(cmd) {
		Out(cmd).Warn(refusal + "; without --dry-run this would be refused and nothing sent")
		return nil
	}
	return usagef("%s; nothing was sent", refusal)
}

// ChargeQuote is the Write.Quote of a purchase charging price, in USD, for a
// term of years (0 when the purchase states none). It is nil when there is no
// price to quote, so a dry run never reports a charge of $0.00 it does not
// know.
func ChargeQuote(price *float64, years int, note string) *output.Quote {
	if price == nil {
		return nil
	}
	return &output.Quote{Total: *price, Currency: "USD", Years: years, Note: note}
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
