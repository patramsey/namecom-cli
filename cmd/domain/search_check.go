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

// inlineRegister performs a quick single-domain registration using a price
// already returned by the check/search endpoint — no extra pricing API call.
func inlineRegister(cmd *cobra.Command, domainName string, purchasePrice *float64) error {
	out := cmdutil.Out(cmd)
	client := cmdutil.APIClient(cmd)

	years := int32(1)
	body := gen.CreateDomainJSONRequestBody{
		Domain: gen.DomainCreatePayload{
			DomainName:       domainName,
			AutorenewEnabled: new(bool),
			PrivacyEnabled:   new(bool),
		},
		Years: &years,
	}
	if purchasePrice != nil {
		body.PurchasePrice = purchasePrice
	}

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
		if result.Results == nil || len(*result.Results) != 1 ||
			!(*result.Results)[0].Purchasable ||
			out.Format != output.FormatTable || out.QuietMode || !output.IsInteractive() {
			return nil
		}
		r := (*result.Results)[0]
		price := ""
		if r.PurchasePrice != nil {
			price = fmt.Sprintf(" for $%.2f/yr", *r.PurchasePrice)
		}
		ok, err := confirm(out, cmdutil.IsYes(cmd), fmt.Sprintf("Register %s%s?", r.DomainName, price))
		if err != nil {
			return err
		}
		if !ok {
			out.Warn("aborted")
			return nil
		}
		return inlineRegister(cmd, r.DomainName, r.PurchasePrice)
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
	argIdx := make(map[string]int, len(args))
	for i, name := range args {
		argIdx[name] = i
	}

	var unsupported []string
	seen := make(map[string]bool, len(args))
	for _, r := range zoneResult.Results {
		idx, ok := argIdx[r.DomainName]
		if !ok {
			continue // API returned a domain we didn't request; ignore
		}
		seen[r.DomainName] = true
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
		idx, ok := argIdx[r.DomainName]
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
				if idx, ok := argIdx[r.DomainName]; ok {
					finalResults[idx] = r
				}
			}
		}
	}

	if err := renderSearchResults(out, &finalResults); err != nil {
		return err
	}

	// When checking a single domain in interactive table mode, offer to register
	// it immediately if it's available.
	if out.Format != output.FormatTable || out.QuietMode || !output.IsInteractive() {
		return nil
	}
	if len(finalResults) != 1 || !finalResults[0].Purchasable {
		return nil
	}
	r := finalResults[0]
	price := ""
	if r.PurchasePrice != nil {
		price = fmt.Sprintf(" for $%.2f/yr", *r.PurchasePrice)
	}
	ok, err := confirm(out, cmdutil.IsYes(cmd), fmt.Sprintf("Register %s%s?", r.DomainName, price))
	if err != nil {
		return err
	}
	if !ok {
		out.Warn("aborted")
		return nil
	}
	return inlineRegister(cmd, r.DomainName, r.PurchasePrice)
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
