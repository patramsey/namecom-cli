package domain

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/charmbracelet/huh"
	coreapigo "github.com/namedotcom/core-api-go"
	"github.com/patramsey/namecom-cli/cmd/cmdutil"
	"github.com/patramsey/namecom-cli/internal/api"
	"github.com/patramsey/namecom-cli/internal/output"
	"github.com/spf13/cobra"
)

var registerCmd = &cobra.Command{
	Use:   "register <domain>",
	Short: "Register a new domain",
	Example: `  namecom domain register example.com
  namecom domain register example.com --years 2 --privacy --autorenew`,
	Args: cmdutil.ExactArgs(1),
	// A name to register is not one you own, nor a file (#187).
	ValidArgsFunction: cobra.NoFileCompletions,
	RunE:              runRegister,
}

var renewCmd = &cobra.Command{
	Use:   "renew <domain>",
	Short: "Renew a domain registration",
	Example: `  namecom domain renew example.com
  namecom domain renew example.com --years 2`,
	Args:              cmdutil.ExactArgs(1),
	RunE:              runRenew,
	ValidArgsFunction: cmdutil.CompleteDomains,
}

var (
	registerYears        int
	registerPrivacy      bool
	registerAutorenew    bool
	registerContactsFile string
	registerPrice        float64
	registerAckClaim     bool
	registerTLDReqs      []string
	renewYears           int
	renewPrice           float64
)

func init() {
	registerCmd.Flags().IntVar(&registerYears, "years", 1, "number of years to register")
	registerCmd.Flags().BoolVar(&registerPrivacy, "privacy", false, "enable WHOIS privacy")
	registerCmd.Flags().BoolVar(&registerAutorenew, "autorenew", false, "enable auto-renewal")
	registerCmd.Flags().StringVar(&registerContactsFile, "contacts-file", "", "JSON file with contact data")
	registerCmd.Flags().Float64Var(&registerPrice, "price", 0, "override the purchase price in USD (premium prices are filled in automatically; use this only to cap what you will pay)")
	registerCmd.Flags().BoolVar(&registerAckClaim, "acknowledge-claim", false,
		"acknowledge a trademark claim on this domain (required to register a claimed domain non-interactively; --yes does NOT cover this)")
	registerCmd.Flags().StringArrayVar(&registerTLDReqs, "tld-requirement", nil,
		"registry-required field as key=value; repeatable (see 'namecom domain requirements <tld>')")

	renewCmd.Flags().IntVar(&renewYears, "years", 1, "number of years to renew")
	renewCmd.Flags().Float64Var(&renewPrice, "price", 0, "override the renewal price in USD (premium prices are filled in automatically; use this only to cap what you will pay)")
}

// formatTermPrice renders a price for the term it actually covers.
//
// GetPricingForDomain returns the price for the REQUESTED period, not a
// per-year rate: the spec's examples show purchasePrice 349.95 for one year and
// 699.9 with years:2. Labelling a multi-year figure "/yr" therefore states
// double (or triple) what the user will be charged, in the one message whose
// job is to state the amount correctly.
func formatTermPrice(price float64, years int) string {
	if years <= 1 {
		return fmt.Sprintf("$%.2f/yr", price)
	}
	return fmt.Sprintf("$%.2f total for %d years", price, years)
}

func runRegister(cmd *cobra.Command, args []string) error {
	out := cmdutil.Out(cmd)
	client := cmdutil.APIClient(cmd)
	yes := cmdutil.IsYes(cmd)
	dryRun := cmdutil.IsDryRun(cmd)
	domainName, err := cmdutil.DomainArg(args, 0)
	if err != nil {
		return err
	}

	if cmd.Flags().Changed("years") {
		if err := cmdutil.ValidYears(registerYears); err != nil {
			return err
		}
	}
	if cmd.Flags().Changed("price") {
		if err := cmdutil.ValidPrice(registerPrice); err != nil {
			return err
		}
	}

	// Parse --tld-requirement up front. It touches nothing but argv, and running
	// it late meant a typo surfaced only AFTER the user had confirmed a purchase
	// and possibly acknowledged a trademark notice.
	tldReqs, err := parseTLDRequirements(registerTLDReqs)
	if err != nil {
		return err
	}

	// Check availability before collecting any further input. CheckAvailability
	// is used here (not ZoneCheck) because it's authoritative — it includes
	// pricing, premium status, and the reason a domain isn't available, all of
	// which we can cross-check against the subsequent GetPricingForDomain call.
	// Carried out of the availability check below: purchaseType tells the API
	// this is an aftermarket/expiring/backorder purchase rather than a plain
	// registration, and checkPrice is the price quoted for that specific
	// purchase type — which can differ from standard registration pricing.
	var checkPurchaseType *string
	var checkPrice *float64

	{
		stop := out.Spin("Checking availability of " + domainName + "…")
		checkResult, err := client.SDK().Domains.CheckAvailability(cmd.Context(),
			&coreapigo.AvailabilityRequest{DomainNames: []string{domainName}})
		stop()
		if err != nil {
			return fmt.Errorf("checking availability: %w", err)
		}
		checkResult.Results = cmdutil.NonNil(checkResult.Results)
		if len(checkResult.Results) == 0 {
			return fmt.Errorf("%s is not available for registration", domainName)
		}
		r := (checkResult.Results)[0]
		if !r.Purchasable {
			msg := domainName + " is not available for registration"
			if r.Reason != nil && *r.Reason != "" {
				msg += ": " + *r.Reason
			}
			return fmt.Errorf("%s", msg)
		}
		checkPurchaseType, checkPrice = nonDefaultPurchaseType(r)
	}

	// Guided form when interactive and no customization flags supplied.
	noFlags := !cmd.Flags().Changed("years") && !cmd.Flags().Changed("privacy") && !cmd.Flags().Changed("autorenew")
	if output.IsInteractive() && noFlags && !yes {
		if err := registerForm(); err != nil {
			return err
		}
	}

	// Fetch pricing first to show cost before charging. Quote the same term the
	// request body will carry — otherwise a multi-year registration quotes the
	// 1-year price and then sends it alongside years:N, which CreateDomainRequest
	// explicitly warns against ("If passing purchasePrice make sure to adjust it
	// accordingly").
	out.Step("Checking pricing for " + domainName + "…")
	pricingYears := registerYears
	pricing, err := client.SDK().Domains.GetPricingForDomain(cmd.Context(),
		&coreapigo.GetPricingForDomainRequest{DomainName: domainName, Years: &pricingYears})
	if err != nil {
		return fmt.Errorf("fetching pricing: %w", err)
	}

	// Decide the price the body will carry BEFORE prompting, and quote that
	// figure. Quoting GetPricingForDomain unconditionally meant an aftermarket
	// name or a --price override confirmed one amount and submitted another.
	var purchasePrice *float64
	if registerPrice > 0 {
		purchasePrice = &registerPrice
	} else if checkPrice != nil {
		// Non-registration purchases (aftermarket, expiring, backorder) require a
		// price, and it's the check result's price that applies — the separate
		// GetPricingForDomain call quotes standard registration pricing.
		purchasePrice = checkPrice
	} else if pricing.Premium && pricing.PurchasePrice != nil {
		// Premium domains require the confirmed price in the body. Use the price
		// we already fetched so the user isn't forced to pass --price manually.
		purchasePrice = pricing.PurchasePrice
	}

	// No narrowing conversion any more: the SDK takes years as int.
	years := registerYears
	payload := coreapigo.DomainCreatePayload{
		DomainName:       &domainName,
		AutorenewEnabled: &registerAutorenew,
		PrivacyEnabled:   &registerPrivacy,
	}

	body := coreapigo.CreateDomainRequest{
		Domain: &payload,
		Years:  &years,
	}
	if registerContactsFile != "" {
		f, err := os.ReadFile(registerContactsFile) //nolint:gosec // G304: --contacts-file names the file to read; that is the flag's purpose
		if err != nil {
			return fmt.Errorf("reading contacts file: %w", err)
		}
		var contacts coreapigo.ContactsRequest
		if err := json.Unmarshal(f, &contacts); err != nil {
			return fmt.Errorf("parsing contacts file: %w", err)
		}
		body.Domain.Contacts = &contacts
	}
	if len(tldReqs) > 0 {
		body.TldRequirements = tldReqs
	}
	body.PurchaseType = checkPurchaseType
	body.PurchasePrice = purchasePrice

	// Trademark claims. CreateDomainRequest: "When a domain has trademark
	// claims (as determined by the Domain Claims Check endpoint), you must
	// include the claims acknowledgment data in the domain creation request."
	// Without this the CLI simply could not register a TMCH-matched name.
	//
	// The claim is looked up here so the body is complete before it is
	// previewed or confirmed. Showing the notice and collecting the
	// acknowledgement stay after the price confirmation, in the send callback
	// below — they add nothing to the body, which already carries the claim.
	claim, err := lookupClaims(cmd, domainName, checkPurchaseType)
	if err != nil {
		return err
	}
	body.Claims = claimsInfo(claim)
	if dryRun && claim != nil {
		// Nothing is purchased, so there is nothing to acknowledge — but the
		// preview still shows the claims block the real request would carry,
		// which is the whole point of inspecting it first.
		renderClaimsNotice(out, claim)
		out.Hint("This domain has a trademark claim; registering it will require --acknowledge-claim")
	}

	// RunWrite skips the prompt entirely under --dry-run. Asking a human to
	// confirm an action that will not happen is noise, and in a script Confirm
	// hard-errors ("pass --yes to confirm in non-interactive mode"), which made
	// --dry-run unusable in CI.
	var created *coreapigo.CreateDomainResponse
	sent, err := cmdutil.RunWrite(cmd, cmdutil.Write[coreapigo.CreateDomainRequest]{
		Method: "POST",
		Path:   "/core/v1/domains",
		Body:   body,
		Prompt: registerPrompt(domainName, body, pricing),
	}, func(ctx context.Context, body coreapigo.CreateDomainRequest) error {
		if claim != nil {
			renderClaimsNotice(out, claim)
			if err := acknowledgeClaim(out, domainName); err != nil {
				return err
			}
		}
		out.Step("Registering " + domainName + "…")
		// The root --idempotency-key (or an auto-generated one) is applied by
		// the client request editor; no per-command flag is needed or wanted
		// here.
		var err error
		created, err = client.SDK().Domains.CreateDomain(ctx, &body)
		return err
	})
	if err != nil || !sent {
		return err
	}

	if out.QuietMode {
		registered := domainName
		if created != nil && created.Domain != nil && created.Domain.DomainName != "" {
			registered = created.Domain.DomainName
		}
		out.Quiet(registered)
		return nil
	}

	switch out.Format {
	case output.FormatJSON:
		return out.JSON(created)
	case output.FormatYAML:
		return out.YAML(created)
	default:
		// created.Domain is a pointer in the SDK where the generated client used
		// a value, so a response without a "domain" object panics rather than
		// printing an empty name. Fall back to the name we asked to register,
		// which is what the user typed and cannot be wrong here.
		registered := domainName
		if created.Domain != nil && created.Domain.DomainName != "" {
			registered = created.Domain.DomainName
		}
		out.Success(fmt.Sprintf("Registered %s (order #%d, total $%.2f)", registered, created.Order, created.TotalPaid))
		// A new registration can trigger ICANN contact verification, and an
		// unverified contact can get the domain registry-locked (typically 15
		// days). The verification record is not queryable for ~10 minutes after
		// creation, so point at the check rather than performing it here.
		out.Hint(fmt.Sprintf("ICANN may require contact email verification — run "+
			"'namecom domain contacts get %s' in a few minutes to check", registered))
		out.Hint(fmt.Sprintf("Run 'namecom dns list %s' to add DNS records", registered))
		if !registerAutorenew {
			out.Hint(fmt.Sprintf("Run 'namecom domain autorenew on %s' to enable auto-renewal", registered))
		}
	}
	return nil
}

// registerPrompt is the purchase confirmation for body. The price it quotes
// is the one body carries, so the user approves the amount actually
// submitted; quoting GetPricingForDomain unconditionally meant an aftermarket
// name or a --price override confirmed one amount and submitted another
// (#83). pricing's registration price is quoted only when the body carries
// none — the API then charges exactly that.
func registerPrompt(domainName string, body coreapigo.CreateDomainRequest, pricing *coreapigo.PricingResponse) string {
	years := 1
	if body.Years != nil {
		years = *body.Years
	}
	price := ""
	switch {
	case body.PurchasePrice != nil && body.PurchaseType != nil:
		// An acquisition price is a flat fee: the API documents that years on
		// create does not multiply it, so "/yr" or "total for N years" would
		// both misstate it. Nor does it guarantee the term for these purchase
		// types, so the prompt states none (#132); --years is still sent, so a
		// non-default one is flagged rather than silently confirmed.
		note := ""
		if years != 1 {
			note = fmt.Sprintf("; --years %d may not apply", years)
		}
		return fmt.Sprintf("Register %s at $%.2f flat (%s, not per year%s)?",
			domainName, *body.PurchasePrice, *body.PurchaseType, note)
	case body.PurchasePrice != nil && pricing.GetPremium():
		// A registry premium is charged on this purchase, and the renewal price
		// is often far lower — shoe.luxe is $1000.00 to register and $24.99 to
		// renew. "/yr" on the purchase price read as $1000 every year (#132).
		// Both figures are totals for the requested term (PricingResponse), so
		// a multi-year term reuses formatTermPrice's "total for N years".
		price = fmt.Sprintf("$%.2f", *body.PurchasePrice)
		if years > 1 {
			price += fmt.Sprintf(" total for %d years", years)
		}
		price += " (premium"
		if pricing.RenewalPrice != nil {
			price += "; renews at " + formatTermPrice(*pricing.RenewalPrice, years)
		}
		price += ")"
	case body.PurchasePrice != nil:
		price = formatTermPrice(*body.PurchasePrice, years)
	case pricing.GetPurchasePrice() != nil:
		price = formatTermPrice(*pricing.PurchasePrice, years)
	}
	return fmt.Sprintf("Register %s for %d year(s) at %s?", domainName, years, price)
}

// renewPrompt is the renewal confirmation for body, quoting the price body
// carries — --price, or the premium price merged from the quote — and the
// standard renewal price only when it carries none. Quoting the standard price
// unconditionally meant `renew --price 1800` asked "at $2500.00/yr?" and then
// submitted 1800, the renew side of #83.
func renewPrompt(domainName string, body coreapigo.DomainsRenewDomainBody, standard *float64) string {
	years := 1
	if body.Years != nil {
		years = *body.Years
	}
	price := ""
	switch {
	case body.PurchasePrice != nil:
		price = formatTermPrice(*body.PurchasePrice, years)
	case standard != nil:
		price = formatTermPrice(*standard, years)
	}
	return fmt.Sprintf("Renew %s for %d year(s) at %s?", domainName, years, price)
}

func registerForm() error {
	yearsStr := "1"
	form := huh.NewForm(
		huh.NewGroup(
			huh.NewInput().
				Title("Years to register").
				Value(&yearsStr).
				Validate(func(s string) error {
					n, err := strconv.Atoi(s)
					if err != nil || n < 1 || n > 10 {
						return fmt.Errorf("enter a number between 1 and 10")
					}
					return nil
				}),
			huh.NewConfirm().
				Title("Enable WHOIS privacy?").
				Value(&registerPrivacy),
			huh.NewConfirm().
				Title("Enable auto-renewal?").
				Value(&registerAutorenew),
		),
	)
	if err := form.Run(); err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			return fmt.Errorf("aborted")
		}
		return err
	}
	if n, err := strconv.Atoi(yearsStr); err == nil {
		registerYears = n
	}
	return nil
}

func runRenew(cmd *cobra.Command, args []string) error {
	out := cmdutil.Out(cmd)
	client := cmdutil.APIClient(cmd)
	domainName, err := cmdutil.DomainArg(args, 0)
	if err != nil {
		return err
	}

	if cmd.Flags().Changed("years") {
		if err := cmdutil.ValidYears(renewYears); err != nil {
			return err
		}
	}
	if cmd.Flags().Changed("price") {
		if err := cmdutil.ValidPrice(renewPrice); err != nil {
			return err
		}
	}

	// Fetch pricing to show renewal cost before charging. Quote the same term
	// the request body will carry, so the price we show and the price we send
	// can't diverge on multi-year renewals.
	out.Step("Checking renewal pricing for " + domainName + "…")
	pricingYears := renewYears
	pricing, err := client.SDK().Domains.GetPricingForDomain(cmd.Context(),
		&coreapigo.GetPricingForDomainRequest{DomainName: domainName, Years: &pricingYears})
	if err != nil {
		return fmt.Errorf("fetching pricing: %w", err)
	}

	years := renewYears
	body := coreapigo.DomainsRenewDomainBody{DomainName: domainName, Years: &years}
	if renewPrice > 0 {
		body.PurchasePrice = &renewPrice
	} else if pricing.Premium && pricing.RenewalPrice != nil {
		// Premium domains require the confirmed price in the body. Use the price
		// we already quoted above so the user isn't forced to pass --price
		// manually. Mirrors the same merge in runRegister.
		body.PurchasePrice = pricing.RenewalPrice
	}

	// See runRegister: --dry-run must not prompt, and must not hard-error in a
	// non-interactive shell for an action it will never perform.
	var renewed *coreapigo.RenewDomainResponse
	sent, err := cmdutil.RunWrite(cmd, cmdutil.Write[coreapigo.DomainsRenewDomainBody]{
		Method: "POST",
		Path:   fmt.Sprintf("/core/v1/domains/%s:renew", domainName),
		Body:   body,
		Prompt: renewPrompt(domainName, body, pricing.RenewalPrice),
	}, func(ctx context.Context, body coreapigo.DomainsRenewDomainBody) error {
		var err error
		renewed, err = client.SDK().Domains.RenewDomain(ctx, &body)
		return err
	})
	if err != nil || !sent || out.Quiet() {
		return err
	}

	switch out.Format {
	case output.FormatJSON:
		return out.JSON(renewed)
	case output.FormatYAML:
		return out.YAML(renewed)
	default:
		orderNum := 0
		if renewed.Order != nil {
			orderNum = *renewed.Order
		}
		totalPaid := 0.0
		if renewed.TotalPaid != nil {
			totalPaid = *renewed.TotalPaid
		}
		out.Success(fmt.Sprintf("Renewed %s (order #%d, total $%.2f)", domainName, orderNum, totalPaid))
		out.Hint(fmt.Sprintf("Run 'namecom domain get %s' to see the new expiry date", domainName))
	}
	return nil
}

// resolveClaims checks a domain for trademark claims and, when any exist,
// shows the notice and obtains the user's acknowledgement.
//
// The claims process is TMCH's: during a gTLD's claims period, registering a
// name matching a registered trademark requires the registrant to be shown a
// notice and to acknowledge it. The API mirrors that — the acknowledgement
// triple (claimId, notBefore, notAfter) must accompany the create request.
//
// Returns nil when the domain has no claims, which is the overwhelmingly common
// case and leaves the request body untouched. runRegister uses the three
// pieces separately so it can complete the body before confirming.
func resolveClaims(cmd *cobra.Command, out *output.Config, domainName string, purchaseType *string) (*coreapigo.DomainClaimsInfo, error) {
	claim, err := lookupClaims(cmd, domainName, purchaseType)
	if err != nil || claim == nil {
		return nil, err
	}
	renderClaimsNotice(out, claim)
	if err := acknowledgeClaim(out, domainName); err != nil {
		return nil, err
	}
	return claimsInfo(claim), nil
}

// lookupClaims returns the trademark claim on domainName for the purchase
// being made, or nil when there is none.
func lookupClaims(cmd *cobra.Command, domainName string, purchaseType *string) (*coreapigo.DomainClaimsCheckResponse, error) {
	client := cmdutil.APIClient(cmd)

	// Claims applicability is per-purchase-type: ResellerTldInfo.claimsCheckRequired
	// is "Array of valid purchase types if claims check is required for the TLD".
	// Sending an empty body defaults the API to "registration", so a landrush or
	// aftermarket acquisition of a trademarked name could report no claim — and
	// the gate would silently not fire for the transaction actually being made.
	body := coreapigo.DomainClaimsCheckRequest{Domain: domainName}
	if purchaseType != nil && *purchaseType != "" {
		pt := coreapigo.DomainClaimsCheckRequestPurchaseType(*purchaseType)
		body.PurchaseType = &pt
	}

	result, err := client.SDK().DomainInfo.CheckDomainClaims(cmd.Context(), &body)
	if err != nil {
		return nil, fmt.Errorf("checking trademark claims: %w", api.FromSDKError(err))
	}

	// No claim on this name: nothing to show, nothing to send.
	if result.ClaimID == nil || *result.ClaimID == "" {
		return nil, nil
	}
	return result, nil
}

// claimsInfo is the acknowledgement triple the create request carries for
// claim, or nil when there is no claim.
func claimsInfo(claim *coreapigo.DomainClaimsCheckResponse) *coreapigo.DomainClaimsInfo {
	if claim == nil {
		return nil
	}
	return &coreapigo.DomainClaimsInfo{
		ClaimID:   claim.ClaimID,
		NotBefore: claim.NotBefore,
		NotAfter:  claim.NotAfter,
	}
}

// acknowledgeClaim obtains the user's acknowledgement of a claim notice that
// has just been shown.
//
// The acknowledgement is deliberately NOT satisfied by --yes. --yes is a
// general-purpose "skip prompts" flag that people set globally in wrappers and
// aliases; letting it accept a legal notice on the user's behalf would mean
// acknowledging something nobody read. --acknowledge-claim has to be passed
// explicitly, and it exists only on this command.
func acknowledgeClaim(out *output.Config, domainName string) error {
	if registerAckClaim {
		return nil
	}
	if !output.IsInteractive() {
		return fmt.Errorf(
			"%s has a trademark claim against it — pass --acknowledge-claim to confirm you have "+
				"read the notice above and still want to register it (--yes does not cover this)", domainName)
	}
	// Interactive: the notice is on screen, so an explicit answer is a
	// genuine acknowledgement. Pass false for `yes` deliberately.
	ok, err := confirm(out, false, "Acknowledge this trademark claim and continue?")
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("aborted: trademark claim not acknowledged")
	}
	return nil
}

// renderClaimsNotice displays the registry's own claim notice plus the matching
// trademarks. The notice text is supplied by the API and exists precisely to be
// shown to the registrant, so it is printed verbatim.
func renderClaimsNotice(out *output.Config, r *coreapigo.DomainClaimsCheckResponse) {
	lines := []string{"TRADEMARK CLAIM on " + r.Domain}
	if r.ClaimsNotice != nil && *r.ClaimsNotice != "" {
		lines = append(lines, *r.ClaimsNotice)
	} else {
		// The API's own documented claims_found example returns a claimId with
		// an empty claims array and no claimsNotice key at all. Without a
		// fallback the box would read only "TRADEMARK CLAIM on <domain>" while
		// the prompt asks the user to confirm they have read "the notice above".
		lines = append(lines,
			"This domain matches a registered trademark. Proceeding with registration",
			"acknowledges that you have received notice of this claim.")
	}
	for _, c := range cmdutil.NonNil(r.Claims) {
		desc := c.Trademark
		// Holder is a spec-required field and the most useful item in a
		// trademark notice — whose mark this is.
		if c.Holder != "" {
			desc += " — " + c.Holder
		}
		if c.Jurisdiction != nil && *c.Jurisdiction != "" {
			desc += " (" + *c.Jurisdiction
			if c.RegistrationNumber != nil && *c.RegistrationNumber != "" {
				desc += ", reg " + *c.RegistrationNumber
			}
			desc += ")"
		}
		lines = append(lines, "  • "+desc)
	}
	out.WarnBox(lines...)
}

// parseTLDRequirements turns repeated --tld-requirement key=value flags into
// the map the API expects. Some registries require extra fields, and IDN
// registrations require the character-set code; the spec declines to document
// them ("they vary wildly between registries and TLDs"), so the values are
// passed through verbatim and validated server-side.
func parseTLDRequirements(pairs []string) (map[string]string, error) {
	if len(pairs) == 0 {
		return nil, nil
	}
	reqs := make(map[string]string, len(pairs))
	for _, p := range pairs {
		k, v, found := strings.Cut(p, "=")
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		if !found || k == "" {
			return nil, fmt.Errorf("invalid --tld-requirement %q: expected key=value", p)
		}
		reqs[k] = v
	}
	return reqs, nil
}
