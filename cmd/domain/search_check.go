package domain

import (
	"errors"
	"fmt"
	"strings"
	"sync"

	coreapigo "github.com/namedotcom/core-api-go"
	"github.com/patramsey/namecom-cli/cmd/cmdutil"
	"github.com/patramsey/namecom-cli/internal/api"
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
func nonDefaultPurchaseType(r *coreapigo.SearchResult) (*string, *float64) {
	if r.PurchaseType == nil {
		return nil, nil
	}
	pt := string(*r.PurchaseType)
	if pt == "" || pt == string(coreapigo.SearchPurchaseTypeRegistration) {
		return nil, nil
	}
	return &pt, r.PurchasePrice
}

// inlineRegister performs a quick single-domain registration from a result the
// check/search endpoint already returned — no extra pricing API call. It takes
// the whole SearchResult rather than a name and price so that purchaseType
// travels with them; the API requires it for non-registration purchases.
func inlineRegister(cmd *cobra.Command, r *coreapigo.SearchResult) error {
	out := cmdutil.Out(cmd)
	client := cmdutil.APIClient(cmd)

	domainName := r.DomainName
	body := checkRegisterBody(r)

	// Same trademark gate as `domain register`. This path offers to buy a domain
	// too, so skipping the check here would let `domain check <name>` acquire a
	// TMCH-matched name with no notice shown and no acknowledgement sent —
	// walking straight around the gate the register command enforces.
	// Pass the purchase type through so the claims check matches the transaction
	// actually being made — an aftermarket or landrush acquisition can have
	// different claims applicability than a plain registration.
	claimsPT, _ := nonDefaultPurchaseType(r)
	claims, err := resolveClaims(cmd, out, domainName, claimsPT)
	if err != nil {
		return err
	}
	body.Claims = claims

	stop := out.Spin(fmt.Sprintf("Registering %s…", domainName))
	created, err := client.SDK().Domains.CreateDomain(cmd.Context(), &body)
	stop()
	if err != nil {
		return api.MarkWrite(err) // not sent through RunWrite, so marked here
	}
	// Same nil guard as `domain register`: created.Domain is a pointer in the
	// SDK, so a response without a domain object would panic here rather than
	// print an empty name. Fall back to the name we asked to register.
	registered := domainName
	if created.Domain != nil && created.Domain.DomainName != "" {
		registered = created.Domain.DomainName
	}
	out.Success(fmt.Sprintf("Registered %s (order #%d, total %s)", registered, created.Order, output.Money(created.TotalPaid)))
	out.Hint(fmt.Sprintf("Run 'namecom dns list %s' to add DNS records", registered))
	out.Hint(fmt.Sprintf("Run 'namecom domain autorenew on %s' to enable auto-renewal", registered))
	return nil
}

// checkRegisterBody is the request inlineRegister sends for r, before any
// trademark claim is added. checkRegisterPrompt quotes from the same body, so
// the offer states the price actually submitted.
func checkRegisterBody(r *coreapigo.SearchResult) coreapigo.CreateDomainRequest {
	domainName := r.DomainName
	years := 1
	body := coreapigo.CreateDomainRequest{
		Domain: &coreapigo.DomainCreatePayload{
			DomainName:       &domainName,
			AutorenewEnabled: new(bool),
			PrivacyEnabled:   new(bool),
		},
		Years: &years,
	}
	if r.PurchasePrice != nil {
		body.PurchasePrice = r.PurchasePrice
	}
	body.PurchaseType, _ = nonDefaultPurchaseType(r)
	return body
}

// checkRegisterPrompt is `domain check`'s register offer, worded by
// registerPrompt so it describes the purchase kind the way `domain register`
// does: a flat fee for an aftermarket, expiring or backorder name, and a
// premium price with its renewal price (#171). The search result stands in
// for the pricing lookup `domain register` makes.
func checkRegisterPrompt(r *coreapigo.SearchResult) string {
	if r.PurchasePrice == nil {
		return fmt.Sprintf("Register %s?", r.DomainName)
	}
	return registerPrompt(r.DomainName, checkRegisterBody(r), &coreapigo.PricingResponse{
		Premium:       derefBool(r.Premium),
		PurchasePrice: r.PurchasePrice,
		RenewalPrice:  r.RenewalPrice,
	})
}

// searchPriceLabel is the PRICE cell for a purchasable result, in the same
// terms as checkRegisterPrompt. Only an ordinary registration is a yearly
// price; an acquisition is a one-off fee, and a premium name usually renews
// far below its purchase price.
func searchPriceLabel(r *coreapigo.SearchResult) string {
	price := *r.PurchasePrice
	if pt, _ := nonDefaultPurchaseType(r); pt != nil {
		return fmt.Sprintf("%s flat (%s)", output.Money(price), *pt)
	}
	if derefBool(r.Premium) {
		// Its renewal price, often far lower, is in the RENEWS column.
		return output.Money(price)
	}
	return output.Money(price) + "/yr"
}

// searchRenewLabel is the RENEWS cell: what the name costs a year after it is
// bought. A cheap first year that renews at several times the price is a
// registrar's best-known surprise, and the JSON carried renewalPrice while
// the table left it out (#235).
func searchRenewLabel(out *output.Config, r *coreapigo.SearchResult) string {
	if !r.Purchasable || r.RenewalPrice == nil {
		return out.Dim("—")
	}
	return output.Money(*r.RenewalPrice) + "/yr"
}

var searchCmd = &cobra.Command{
	Use:   "search <term>",
	Short: "Search for available domains matching a keyword",
	Example: `  namecom domain search mystartup
  namecom domain search myidea -q  # print only available domains`,
	Args:              cmdutil.ExactArgs(1),
	ValidArgsFunction: cobra.NoFileCompletions,
	RunE:              runSearch,
}

var checkAuthoritative bool

var checkCmd = &cobra.Command{
	Use:   "check <domain> [<domain>...]",
	Short: "Check exact availability and price for one or more domains",
	Long: `Check exact availability and price for one or more domains. Any number
of names may be given; the API answers 50 per request, so a longer list is
sent 50 at a time. '-' reads names from stdin, one per line; blank lines and
# comments are skipped.`,
	Example: `  namecom domain check example.com
  namecom domain check example.com myidea.io coolname.dev
  namecom domain check - < names.txt                # one name per line
  namecom domain check --authoritative example.com  # skip ZoneCheck, hit registry directly
  namecom domain check --sandbox example.com        # sandbox: registry check used automatically`,
	Args: cmdutil.MinimumNArgs(1),
	// Names to check are not ones you own, nor files (#187).
	ValidArgsFunction: cobra.NoFileCompletions,
	RunE:              runCheck,
}

func init() {
	checkCmd.Flags().BoolVar(&checkAuthoritative, "authoritative", false, "use registry check instead of DNS zone check (slower but authoritative)")
}

func runSearch(cmd *cobra.Command, args []string) error {
	out := cmdutil.Out(cmd)
	client := cmdutil.APIClient(cmd)
	// An empty term printed an empty table and exited 0 (#187), which a
	// script cannot tell from "nothing matched".
	if strings.TrimSpace(args[0]) == "" {
		return cmdutil.NewUsageError(errors.New("search term must not be empty"))
	}

	stop := out.Spin("Searching domains…")
	result, err := client.SDK().Domains.Search(cmd.Context(),
		&coreapigo.SearchRequest{Keyword: args[0]})
	stop()
	if err != nil {
		return err
	}
	return renderSearchResults(out, cmdutil.NonNil(result.Results))
}

// maxCheckNames is the most names the API accepts in one ZoneCheck or
// CheckAvailability request. More used to fail with the SDK's "number of
// items must be less than or equal to 50" (#244); runCheck now splits them.
const maxCheckNames = 50

func runCheck(cmd *cobra.Command, args []string) error {
	out := cmdutil.Out(cmd)

	// Not DomainArgs: a name given twice is checked, and shown, twice.
	args, err := cmdutil.ExpandStdinArgs(cmd, args)
	if err != nil {
		return err
	}

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
		out.Note("Sandbox mode: using registry check (ZoneCheck is production-only)")
	}

	// --authoritative (or sandbox mode) skips ZoneCheck and hits the registry directly.
	check, warning := checkZone, "could not determine availability for %[1]s — run "+
		"'namecom domain check --authoritative %[1]s' to query the registry directly"
	if checkAuthoritative || cmdutil.IsSandbox(cmd) {
		check, warning = checkRegistry, "the registry returned no result for %s — check the name and its TLD"
	}

	// One chunk at a time, each finished — pricing included — before the next
	// starts. Every request still goes through the client's rate limiter; this
	// bounds how many wait on it at once, since the pricing lookups for 500
	// names started together would queue for 50 seconds, past the per-request
	// timeout. Each chunk has its own argMatcher, and finishCheck sees the
	// whole list, so a name no reply answered is caught in any chunk.
	results := make([]*coreapigo.SearchResult, len(args))
	for start := 0; start < len(args); start += maxCheckNames {
		end := min(start+maxCheckNames, len(args))
		chunk, err := check(cmd, args[start:end])
		if err != nil {
			return err
		}
		copy(results[start:end], chunk)
	}
	return finishCheck(cmd, out, args, results, warning)
}

// checkRegistry answers at most maxCheckNames names with one
// CheckAvailability request: a result slot per name, in order, left nil
// for a name no reply was matched to.
func checkRegistry(cmd *cobra.Command, args []string) ([]*coreapigo.SearchResult, error) {
	client := cmdutil.APIClient(cmd)
	stop := cmdutil.Out(cmd).Spin("Checking availability…")
	result, err := client.SDK().Domains.CheckAvailability(cmd.Context(),
		&coreapigo.AvailabilityRequest{DomainNames: args})
	stop()
	if err != nil {
		return nil, err
	}
	// Key the replies to the arguments as the ZoneCheck path does, so a
	// name the registry left out (an unknown TLD, say) still gets a row and
	// fails the command instead of vanishing (#170).
	results := make([]*coreapigo.SearchResult, len(args))
	matcher := newArgMatcher(args)
	if result != nil {
		for _, r := range cmdutil.NonNil(result.Results) {
			if idx, ok := matcher.match(r.DomainName); ok {
				results[idx] = r
			}
		}
	}
	return results, nil
}

// checkZone is checkRegistry by way of ZoneCheck, a pricing lookup for each
// available name, and CheckAvailability for the TLDs ZoneCheck does not cover.
func checkZone(cmd *cobra.Command, args []string) ([]*coreapigo.SearchResult, error) {
	client := cmdutil.APIClient(cmd)

	// Step 1: ZoneCheck — fast DNS zone file lookup for all domains at once.
	// Available==true: available; Available==false: taken; Available==nil: TLD
	// not supported by ZoneCheck, fall back to CheckAvailability for those.
	stop := cmdutil.Out(cmd).Spin("Checking availability…")
	zoneResult, err := client.SDK().Domains.ZoneCheck(cmd.Context(),
		&coreapigo.ZoneCheckRequest{DomainNames: args})
	stop()
	if err != nil {
		return nil, err
	}
	zoneResult.Results = cmdutil.NonNil(zoneResult.Results)

	// Preserve input order in the final result slice.
	finalResults := make([]*coreapigo.SearchResult, len(args))
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
		finalResults[idx] = &coreapigo.SearchResult{
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
			pricing, err := client.SDK().Domains.GetPricingForDomain(cmd.Context(),
				&coreapigo.GetPricingForDomainRequest{DomainName: domainName})
			if err != nil {
				mu.Lock()
				pricingErr = api.FromSDKError(err)
				mu.Unlock()
				return
			}
			premium := pricing.Premium
			sld, tld, _ := strings.Cut(domainName, ".")
			mu.Lock()
			finalResults[idx] = &coreapigo.SearchResult{
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
		return nil, fmt.Errorf("fetching pricing: %w", pricingErr)
	}

	// Step 3: CheckAvailability for TLDs ZoneCheck returned null for.
	if len(unsupported) > 0 {
		checkResult, err := client.SDK().Domains.CheckAvailability(cmd.Context(),
			&coreapigo.AvailabilityRequest{DomainNames: unsupported})
		if err != nil {
			return nil, fmt.Errorf("checking availability: %w", api.FromSDKError(err))
		}
		if checkResult.Results != nil {
			for _, r := range cmdutil.NonNil(checkResult.Results) {
				if idx, ok := matcher.match(r.DomainName); ok {
					finalResults[idx] = r
				}
			}
		}
	}
	return finalResults, nil
}

// finishCheck renders one row per argument and fails the command when any
// argument got no answer.
//
// Safety net: any argument no reply resolved to would otherwise render as a
// zero-valued row — blank name, Purchasable false — which reads as "taken".
// Reporting an available domain as unavailable is the expensive direction to
// be wrong in, and it is the exact bug the ZoneCheck path was fixed for. Name
// the domain the user asked about and leave it explicitly unpurchasable
// rather than silently asserting it is gone. warning is a format taking the
// domain name.
//
// Every slot is checked, not only the arguments argMatcher left unclaimed: a
// claimed name can still be unanswered, as when ZoneCheck defers it to
// CheckAvailability and that reply leaves it out.
func finishCheck(cmd *cobra.Command, out *output.Config, args []string,
	results []*coreapigo.SearchResult, warning string) error {
	var unknown []string
	for i := range results {
		// nil, not just zero-valued: the SDK returns []*SearchResult, so a slot
		// no reply filled is a nil pointer rather than an empty struct. Reading
		// through it panics, which would take out the very safety net this loop
		// is.
		if results[i] == nil || results[i].DomainName == "" {
			sld, tld, _ := strings.Cut(args[i], ".")
			results[i] = &coreapigo.SearchResult{DomainName: args[i], Sld: sld, Tld: tld}
			out.Warn(fmt.Sprintf(warning, args[i]))
			unknown = append(unknown, args[i])
		}
	}

	if err := renderSearchResults(out, results); err != nil {
		return err
	}
	// A row that answers nothing is not a successful check: exit non-zero so a
	// script does not read the placeholder as "taken".
	if len(unknown) > 0 {
		return fmt.Errorf("availability unknown for %s", strings.Join(unknown, ", "))
	}
	return maybeOfferRegister(cmd, out, results)
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
func maybeOfferRegister(cmd *cobra.Command, out *output.Config, results []*coreapigo.SearchResult) error {
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
	// Deliberately passing false, not cmdutil.IsYes(cmd) — see the doc comment.
	ok, err := confirm(out, false, checkRegisterPrompt(r), cmdutil.PromptContext(cmd))
	if err != nil {
		return err
	}
	// Declining is not a failure here, unlike every other prompt: the check
	// the user asked for succeeded, and the offer was the CLI's idea.
	if !ok {
		out.Note(r.DomainName + " was not registered")
		return nil
	}
	return inlineRegister(cmd, r)
}

func renderSearchResults(out *output.Config, results []*coreapigo.SearchResult) error {
	if results == nil {
		results = []*coreapigo.SearchResult{}
	}

	if out.QuietMode {
		names := make([]string, 0, len(results))
		for _, r := range results {
			if r.Purchasable {
				names = append(names, r.DomainName)
			}
		}
		out.PrintQuiet(names)
		return nil
	}

	switch out.Format {
	case output.FormatJSON:
		return out.JSON(results)
	case output.FormatYAML:
		return out.YAML(results)
	default:
		headers := []string{"DOMAIN", "AVAILABILITY", "PRICE", "RENEWS", "PREMIUM"}
		rows := make([][]string, 0, len(results))
		for _, r := range results {
			price := out.Dim("—")
			if r.Purchasable && r.PurchasePrice != nil {
				price = searchPriceLabel(r)
			}
			// SearchResult.Premium is a *bool, and the SDK documents it as
			// "only returned for purchasable domains" with omitempty on the
			// wire. So an absent value is not "unknown": for a purchasable
			// domain it means false, and for an unpurchasable one the question
			// does not arise. Rendering "" for both left every ordinary domain
			// with an empty cell that read as a broken column — and this was
			// the only boolean in the CLI not using BoolBadge. Premium is not
			// cosmetic: when true, purchasePrice must be sent on register.
			premium := out.Dim("—")
			if r.Purchasable {
				premium = out.BoolBadge(derefBool(r.Premium))
			}
			rows = append(rows, []string{
				r.DomainName,
				out.AvailabilityBadge(r.Purchasable),
				price,
				searchRenewLabel(out, r),
				premium,
			})
		}
		out.Table(headers, rows)
		out.Count(len(results), "result")
		var available []string
		for _, r := range results {
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
// Arguments are lowercased and punycode-encoded (cmdutil.CanonicalDomain)
// before we get here, so an exact match is the normal case. Should the API's
// canonical form still differ, an unrecognized reply is resolved by
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
