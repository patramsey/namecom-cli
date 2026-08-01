package domain

import (
	"fmt"
	"strings"
	"sync"

	"github.com/patramsey/namecom-cli/cmd/cmdutil"
	"github.com/patramsey/namecom-cli/internal/api"
	"github.com/patramsey/namecom-cli/internal/api/gen"
	"github.com/patramsey/namecom-cli/internal/output"
	"github.com/spf13/cobra"
)

// nonDefaultPurchaseType returns the purchaseType and price to forward to
// CreateDomain, or (nil, nil) for an ordinary registration.
//
// CreateDomainRequest.PurchaseType is documented as "should be copied from the
// result of either a Search or checkAvailability request", and PurchasePrice is
// "required if purchaseType is not 'registration'" — so the two travel
// together. "registration" is the API's own default, so we omit it rather than
// send a redundant field.
//
// Note this is only ever populated from a real SearchResult. The ZoneCheck path
// in runCheck cannot supply one: neither ZoneCheck nor GetPricingForDomain
// returns a purchaseType, so results synthesized there can structurally only
// represent a plain registration.
func nonDefaultPurchaseType(r gen.SearchResult) (*string, *float64) {
	if r.PurchaseType == nil {
		return nil, nil
	}
	pt := string(*r.PurchaseType)
	if pt == "" || pt == string(gen.Registration) {
		return nil, nil
	}
	return &pt, r.PurchasePrice
}

// inlineRegister performs a quick single-domain registration from a result the
// check/search endpoint already returned — no extra pricing API call. It takes
// the whole SearchResult rather than a name and price so that purchaseType
// travels with them; the API requires it for non-registration purchases.
func inlineRegister(cmd *cobra.Command, r gen.SearchResult) error {
	out := cmdutil.Out(cmd)
	client := cmdutil.APIClient(cmd)

	domainName := r.DomainName
	years := int32(1)
	body := gen.CreateDomainJSONRequestBody{
		Domain: gen.DomainCreatePayload{
			DomainName:       domainName,
			AutorenewEnabled: new(bool),
			PrivacyEnabled:   new(bool),
		},
		Years: &years,
	}
	if r.PurchasePrice != nil {
		body.PurchasePrice = r.PurchasePrice
	}
	body.PurchaseType, _ = nonDefaultPurchaseType(r)

	// Same trademark gate as `domain register`. This path offers to buy a domain
	// too, so skipping the check here would let `domain check <name>` acquire a
	// TMCH-matched name with no notice shown and no acknowledgement sent —
	// walking straight around the gate the register command enforces.
	// Pass the purchase type through so the claims check matches the transaction
	// actually being made — an aftermarket or landrush acquisition can have
	// different claims applicability than a plain registration.
	claimsPT, _ := nonDefaultPurchaseType(r)
	claims, err := resolveClaims(cmd, out, domainName, claimsPT, false)
	if err != nil {
		return err
	}
	body.Claims = claims

	stop := out.Spin(fmt.Sprintf("Registering %s…", domainName))
	resp, err := client.Gen().CreateDomain(cmd.Context(), &gen.CreateDomainParams{}, body)
	stop()
	if err != nil {
		return err
	}
	var created gen.CreateDomainResponseSchema
	if err := api.Decode(resp, &created); err != nil {
		return err
	}
	out.Success(fmt.Sprintf("Registered %s (order #%d, total $%.2f)", created.Domain.DomainName, created.Order, created.TotalPaid))
	out.Hint(fmt.Sprintf("Run 'namecom dns list %s' to add DNS records", created.Domain.DomainName))
	out.Hint(fmt.Sprintf("Run 'namecom domain autorenew on %s' to enable auto-renewal", created.Domain.DomainName))
	return nil
}

var searchCmd = &cobra.Command{
	Use:   "search <term>",
	Short: "Search for available domains matching a keyword",
	Example: `  namecom domain search mystartup
  namecom domain search myidea -q  # print only available domains`,
	Args: cmdutil.ExactArgs(1),
	RunE: runSearch,
}

var checkAuthoritative bool

var checkCmd = &cobra.Command{
	Use:   "check <domain> [<domain>...]",
	Short: "Check exact availability and price for one or more domains",
	Example: `  namecom domain check example.com
  namecom domain check example.com myidea.io coolname.dev
  namecom domain check --authoritative example.com  # skip ZoneCheck, hit registry directly
  namecom domain check --sandbox example.com        # sandbox: registry check used automatically`,
	Args: cmdutil.MinimumNArgs(1),
	RunE: runCheck,
}

func init() {
	checkCmd.Flags().BoolVar(&checkAuthoritative, "authoritative", false, "use registry check instead of DNS zone check (slower but authoritative)")
}

func runSearch(cmd *cobra.Command, args []string) error {
	out := cmdutil.Out(cmd)
	client := cmdutil.APIClient(cmd)

	stop := out.Spin("Searching domains…")
	resp, err := client.Gen().Search(cmd.Context(), gen.SearchJSONRequestBody{Keyword: args[0]})
	stop()
	if err != nil {
		return err
	}
	var result gen.SearchResponseSchema
	if err := api.Decode(resp, &result); err != nil {
		return err
	}
	return renderSearchResults(out, result.Results)
}

func runCheck(cmd *cobra.Command, args []string) error {
	out := cmdutil.Out(cmd)
	client := cmdutil.APIClient(cmd)

	// Normalize (and validate) every argument up front. This was the only
	// command that skipped cmdutil.DomainArg, and the results below are keyed by
	// domain name: with a mixed-case argument like "Example.COM" the API's
	// canonical lowercase reply never matched, leaving a zero-valued slot that
	// renders as a blank "taken" row — and exits 0. Same failure for an IDN
	// entered as Unicode and returned as punycode.
	for i := range args {
		normalized, err := cmdutil.DomainArg(args, i)
		if err != nil {
			return err
		}
		args[i] = normalized
	}

	// ZoneCheck queries production DNS zone files and has no sandbox equivalent —
	// the sandbox EPP registry is isolated from production zone data. Using both
	// in the same check produces contradictory results in sandbox mode. Skip
	// ZoneCheck and go straight to the EPP registry when --sandbox is active.
	if cmdutil.IsSandbox(cmd) && !checkAuthoritative {
		out.Hint("Sandbox mode: using registry check (ZoneCheck is production-only)")
	}

	// --authoritative (or sandbox mode) skips ZoneCheck and hits the registry directly.
	if checkAuthoritative || cmdutil.IsSandbox(cmd) {
		stop := out.Spin("Checking availability…")
		resp, err := client.Gen().CheckAvailability(cmd.Context(), gen.CheckAvailabilityJSONRequestBody{DomainNames: args})
		stop()
		if err != nil {
			return err
		}
		var result gen.SearchResponseSchema
		if err := api.Decode(resp, &result); err != nil {
			return err
		}
		if err := renderSearchResults(out, result.Results); err != nil {
			return err
		}
		if result.Results == nil {
			return nil
		}
		return maybeOfferRegister(cmd, out, *result.Results)
	}

	// Step 1: ZoneCheck — fast DNS zone file lookup for all domains at once.
	// Available==true: available; Available==false: taken; Available==nil: TLD
	// not supported by ZoneCheck, fall back to CheckAvailability for those.
	stop := out.Spin("Checking availability…")
	zoneResp, err := client.Gen().ZoneCheck(cmd.Context(), gen.ZoneCheckJSONRequestBody{DomainNames: args})
	stop()
	if err != nil {
		return err
	}
	var zoneResult gen.ZoneCheckResponseSchema
	if err := api.Decode(zoneResp, &zoneResult); err != nil {
		return err
	}

	// Preserve input order in the final result slice.
	finalResults := make([]gen.SearchResult, len(args))
	matcher := newArgMatcher(args)

	var unsupported []string
	seen := make(map[string]bool, len(args))
	for _, r := range zoneResult.Results {
		idx, ok := matcher.match(r.DomainName)
		if !ok {
			continue // API returned a domain we didn't request; ignore
		}
		seen[args[idx]] = true
		if r.Available == nil {
			// Null means this TLD isn't supported by ZoneCheck.
			unsupported = append(unsupported, r.DomainName)
			continue
		}
		sld, tld, _ := strings.Cut(r.DomainName, ".")
		finalResults[idx] = gen.SearchResult{
			DomainName:  r.DomainName,
			Purchasable: *r.Available,
			Sld:         sld,
			Tld:         tld,
		}
	}
	// Domains absent from ZoneCheck results entirely (pre-validation failure)
	// fall back to CheckAvailability rather than rendering as a blank row.
	for _, name := range args {
		if !seen[name] {
			unsupported = append(unsupported, name)
		}
	}

	// Step 2: Fetch pricing in parallel for domains ZoneCheck says are available.
	var mu sync.Mutex
	var wg sync.WaitGroup
	var pricingErr error

	for _, r := range zoneResult.Results {
		if r.Available == nil || !*r.Available {
			continue
		}
		idx, ok := matcher.match(r.DomainName)
		if !ok {
			continue // unexpected domain from API; skip
		}
		wg.Add(1)
		go func(domainName string, idx int) {
			defer wg.Done()
			pResp, err := client.Gen().GetPricingForDomain(cmd.Context(), domainName, &gen.GetPricingForDomainParams{})
			if err != nil {
				mu.Lock()
				pricingErr = err
				mu.Unlock()
				return
			}
			var pricing gen.PricingResponseSchema
			if err := api.Decode(pResp, &pricing); err != nil {
				mu.Lock()
				pricingErr = err
				mu.Unlock()
				return
			}
			premium := pricing.Premium
			sld, tld, _ := strings.Cut(domainName, ".")
			mu.Lock()
			finalResults[idx] = gen.SearchResult{
				DomainName:    domainName,
				Purchasable:   true,
				PurchasePrice: pricing.PurchasePrice,
				RenewalPrice:  pricing.RenewalPrice,
				Premium:       &premium,
				Sld:           sld,
				Tld:           tld,
			}
			mu.Unlock()
		}(r.DomainName, idx)
	}
	wg.Wait()
	if pricingErr != nil {
		return fmt.Errorf("fetching pricing: %w", pricingErr)
	}

	// Step 3: CheckAvailability for TLDs ZoneCheck returned null for.
	if len(unsupported) > 0 {
		checkResp, err := client.Gen().CheckAvailability(cmd.Context(), gen.CheckAvailabilityJSONRequestBody{DomainNames: unsupported})
		if err != nil {
			return fmt.Errorf("checking availability: %w", err)
		}
		var checkResult gen.SearchResponseSchema
		if err := api.Decode(checkResp, &checkResult); err != nil {
			return err
		}
		if checkResult.Results != nil {
			for _, r := range *checkResult.Results {
				if idx, ok := matcher.match(r.DomainName); ok {
					finalResults[idx] = r
				}
			}
		}
	}

	// Safety net: any argument no reply resolved to would otherwise render as a
	// zero-valued row — blank name, Purchasable false — which reads as "taken".
	// Reporting an available domain as unavailable is the expensive direction to
	// be wrong in, and it is the exact bug this whole path was fixed for. Name
	// the domain the user asked about and leave it explicitly unpurchasable
	// rather than silently asserting it is gone.
	for _, i := range matcher.unclaimed() {
		if finalResults[i].DomainName == "" {
			sld, tld, _ := strings.Cut(args[i], ".")
			finalResults[i] = gen.SearchResult{DomainName: args[i], Sld: sld, Tld: tld}
			out.Warn(fmt.Sprintf("could not determine availability for %s — run "+
				"'namecom domain check --authoritative %s' to query the registry directly", args[i], args[i]))
		}
	}

	if err := renderSearchResults(out, &finalResults); err != nil {
		return err
	}

	return maybeOfferRegister(cmd, out, finalResults)
}

// maybeOfferRegister offers to register a domain that `check` just found
// available, when exactly one was checked and a human is at the terminal.
//
// Two safety rules, both learned the hard way:
//
//   - The answer is NEVER auto-supplied from --yes. `check` is a read-only
//     command; --yes is a global flag people put in aliases and CI wrappers
//     precisely because it is safe there. Honoring it here silently turned
//     `check` into `register` and charged the user.
//   - --dry-run suppresses the offer entirely. There is no meaningful "preview"
//     of an interactive purchase prompt, and the old code ignored --dry-run
//     outright, so a dry run really bought the domain.
func maybeOfferRegister(cmd *cobra.Command, out *output.Config, results []gen.SearchResult) error {
	if cmdutil.IsDryRun(cmd) {
		return nil
	}
	if out.Format != output.FormatTable || out.QuietMode || !output.IsInteractive() {
		return nil
	}
	if len(results) != 1 || !results[0].Purchasable {
		return nil
	}
	r := results[0]
	price := ""
	if r.PurchasePrice != nil {
		price = fmt.Sprintf(" for $%.2f/yr", *r.PurchasePrice)
	}
	// Deliberately passing false, not cmdutil.IsYes(cmd) — see the doc comment.
	ok, err := confirm(out, false, fmt.Sprintf("Register %s%s?", r.DomainName, price))
	if err != nil {
		return err
	}
	if !ok {
		out.Warn("aborted")
		return nil
	}
	return inlineRegister(cmd, r)
}

func renderSearchResults(out *output.Config, results *[]gen.SearchResult) error {
	if results == nil {
		results = &[]gen.SearchResult{}
	}

	if out.QuietMode {
		names := make([]string, 0, len(*results))
		for _, r := range *results {
			if r.Purchasable {
				names = append(names, r.DomainName)
			}
		}
		out.PrintQuiet(names)
		return nil
	}

	switch out.Format {
	case output.FormatJSON:
		return out.JSON(*results)
	case output.FormatYAML:
		return out.YAML(*results)
	default:
		headers := []string{"DOMAIN", "AVAILABILITY", "PRICE", "PREMIUM"}
		rows := make([][]string, 0, len(*results))
		for _, r := range *results {
			price := out.Dim("—")
			if r.Purchasable && r.PurchasePrice != nil {
				price = fmt.Sprintf("$%.2f/yr", *r.PurchasePrice)
			}
			premium := ""
			if derefBool(r.Premium) {
				premium = out.BoolBadge(true)
			}
			rows = append(rows, []string{
				r.DomainName,
				out.AvailabilityBadge(r.Purchasable),
				price,
				premium,
			})
		}
		out.Table(headers, rows)
		out.Count(len(*results), "result")
		var available []string
		for _, r := range *results {
			if r.Purchasable {
				available = append(available, r.DomainName)
			}
		}
		if len(available) == 1 {
			out.Hint(fmt.Sprintf("Run 'namecom domain register %s' to register it", available[0]))
		} else if len(available) > 1 {
			out.Hint("Run 'namecom domain register <domain>' to register an available domain")
		}
	}
	return nil
}

// argMatcher maps a domain name returned by the API back to the position of the
// argument that asked about it.
//
// The API normalizes names server-side and replies "in its canonical (ASCII /
// punycode) form", so a reply is not always spelled the way the user typed it.
// Arguments are lowercased before we get here, which covers case; what remains
// is punycode. Encoding it locally would mean depending on
// golang.org/x/net/idna, so an unrecognized reply is instead resolved by
// elimination — but only under conditions that make the pairing safe:
//
//   - exactly one argument may be outstanding, so the pairing is unambiguous;
//   - that argument must contain non-ASCII characters. An all-ASCII argument
//     would have matched exactly if it were ours, so an unmatched reply is an
//     anomaly (a domain we never asked about), not a canonicalization — and
//     claiming a slot for it would report a result under the wrong name.
//
// Anything else is refused. runCheck then names the unresolved argument itself
// rather than emitting a blank row, so an unchecked domain is never presented
// as taken.
type argMatcher struct {
	args    []string
	claimed []bool
	// alias remembers resolutions so repeated lookups (the zone pass and the
	// pricing pass both ask) return the same slot.
	alias map[string]int
}

func newArgMatcher(args []string) *argMatcher {
	return &argMatcher{args: args, claimed: make([]bool, len(args)), alias: map[string]int{}}
}

// match resolves an API-returned name to an argument index.
func (m *argMatcher) match(apiName string) (int, bool) {
	if i, ok := m.alias[apiName]; ok {
		return i, true
	}
	// First unclaimed argument with this exact name. Scanning rather than using
	// a name->index map keeps duplicate arguments (`check a.com a.com`) from
	// collapsing onto one slot and orphaning the other.
	for i, a := range m.args {
		if !m.claimed[i] && a == apiName {
			m.claimed[i] = true
			m.alias[apiName] = i
			return i, true
		}
	}

	// Unrecognized spelling: eliminate only if exactly one argument is pending
	// AND it is one whose canonical form we could not have computed.
	pending := -1
	for i, done := range m.claimed {
		if done {
			continue
		}
		if pending != -1 {
			return 0, false // ambiguous
		}
		pending = i
	}
	if pending == -1 || isASCII(m.args[pending]) {
		return 0, false
	}
	m.claimed[pending] = true
	m.alias[apiName] = pending
	return pending, true
}

// unclaimed returns the indexes of arguments no reply was matched to.
func (m *argMatcher) unclaimed() []int {
	var out []int
	for i, done := range m.claimed {
		if !done {
			out = append(out, i)
		}
	}
	return out
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] > 127 {
			return false
		}
	}
	return true
}
