package domain

import (
	"fmt"
	"strings"

	coreapigo "github.com/namedotcom/core-api-go"
	"github.com/patramsey/namecom-cli/cmd/cmdutil"
	"github.com/patramsey/namecom-cli/internal/api"
	"github.com/patramsey/namecom-cli/internal/output"
	"github.com/spf13/cobra"
)

// -- tld-pricing --
//
// Under `domain`, beside `domain pricing <domain>` and `domain requirements
// <tld>`, the other command about a TLD rather than a domain in the account:
// `domain pricing` prices one name, this one prices the TLDs.

var tldPricingCmd = &cobra.Command{
	Use:   "tld-pricing",
	Short: "List what each TLD costs to register, renew, transfer, and restore",
	Long: `List name.com's prices for each TLD it sells, alphabetically: your
account's price to register, renew, transfer in, and restore a domain, in
USD, for the standard (not premium) names in the TLD.

--tld names the TLDs to price, up to 25; name.com leaves out any it does not
sell, and the CLI warns about them. --years prices a longer term, 1 to 10:
each price is then the total for that many years. A "—" means name.com does
not offer that for the TLD. A promo code is not reflected, and a premium
name costs more: 'namecom domain pricing <domain>' prices one name.

One page at a time (the API's page size is 25) unless --all or --limit says
otherwise; -o json has every price, the retail and original (MSRP) ones
too.`,
	Example: `  namecom domain tld-pricing --tld io                # what does .io cost?
  namecom domain tld-pricing --tld com,net,org --years 2
  namecom domain tld-pricing --all -o json | jq '.data[] | {tld, registrationPrice}'`,
	Args: cmdutil.NoArgs,
	RunE: runTLDPricing,
}

var (
	tldPricingTLDs  []string
	tldPricingYears int
	tldPricingAll   bool
	tldPricingPage  int
	tldPricingLimit int
)

// maxPricedTLDs is the most TLDs one TLD pricing request takes: "Maximum of
// 25 TLDs can be requested at a time".
const maxPricedTLDs = 25

func init() {
	tldPricingCmd.Flags().StringArrayVar(&tldPricingTLDs, "tld", nil, "price only these TLDs (e.g. com,io); repeatable, up to 25")
	tldPricingCmd.Flags().IntVar(&tldPricingYears, "years", 0, "price this many years, 1 to 10 (default 1)")
	cmdutil.AddPageFlags(tldPricingCmd, &tldPricingAll, &tldPricingPage, &tldPricingLimit, "TLDs")
}

// tldPricingHeaders are the table's columns, most important first, as
// output.Table drops from the right on a narrow terminal.
var tldPricingHeaders = []string{"TLD", "REGISTER", "RENEW", "TRANSFER", "RESTORE"}

func runTLDPricing(cmd *cobra.Command, _ []string) error {
	out := cmdutil.Out(cmd)
	client := cmdutil.APIClient(cmd)

	tlds, err := tldList(tldPricingTLDs, "tld", maxPricedTLDs)
	if err != nil {
		return err
	}
	var duration *int
	if cmd.Flags().Changed("years") {
		if err := cmdutil.ValidYears(tldPricingYears); err != nil {
			return err
		}
		years := tldPricingYears
		duration = &years
	}
	paging, err := cmdutil.ListPaging(cmd, tldPricingAll, tldPricingPage, tldPricingLimit)
	if err != nil {
		return err
	}
	tldParams := make([]*string, len(tlds))
	for i := range tlds {
		tldParams[i] = &tlds[i]
	}

	spin := out.StartSpinner("Fetching TLD prices…")
	page := tldPricingPage
	var prices []*coreapigo.TldPriceListEntry
	var hasMore bool
	var nextPage int
	var lastResult *coreapigo.TldPriceListResponse
	for {
		req := &coreapigo.TldPriceListRequest{Page: &page, PerPage: paging.PerPage, Duration: duration, Tlds: tldParams}
		result, err := client.SDK().TldPricing.TldPriceList(cmd.Context(), req)
		if page == tldPricingPage && cmdutil.PageOutOfRange(page, err) {
			lastResult = &coreapigo.TldPriceListResponse{}
			break
		}
		if err != nil {
			spin.Stop()
			return api.FromSDKError(err)
		}
		// Past the end, an empty page, as every list shows it.
		if page == tldPricingPage && cmdutil.PastLastPage(page, paging.PerPage, len(result.Pricing), result.TotalCount, &result.LastPage) {
			lastResult = &coreapigo.TldPriceListResponse{}
			break
		}
		prices = append(prices, cmdutil.NonNil(result.Pricing)...)
		lastResult = result
		next, ok := cmdutil.NextPage(page, result.NextPage, &result.LastPage)
		if !ok {
			break
		}
		if !paging.All {
			hasMore, nextPage = true, next
			break
		}
		page = next
		spin.Update(fmt.Sprintf("Fetching TLD prices… (page %d, %d so far)", page, len(prices)))
	}
	spin.Stop()

	// The API drops a TLD it does not sell from the list rather than
	// failing (unless it drops all of them), so say which.
	if missing := unpricedTLDs(tlds, prices); len(missing) > 0 && tldPricingPage == 1 && !hasMore {
		why := "name.com does not sell it, or it is not a TLD"
		if len(missing) > 1 {
			why = "name.com does not sell them, or they are not TLDs"
		}
		out.Warn(fmt.Sprintf("no price for %s: %s", strings.Join(missing, ", "), why))
	}

	if out.QuietMode {
		names := make([]string, len(prices))
		for i, p := range prices {
			names[i] = p.Tld
		}
		out.PrintQuiet(names)
		if hasMore {
			cmdutil.QuietMorePages(out, nextPage)
		}
		return nil
	}

	foot := cmdutil.Page("TLD", tldPricingPage, len(prices), paging.All, paging.PerPage, lastResult.From, lastResult.To, lastResult.TotalCount, nextPage)
	years := 1
	if duration != nil {
		years = *duration
	}
	foot.Notes = []string{"prices for " + output.Plural(years, "year")}
	var np *int32
	if hasMore {
		np = cmdutil.Int32Page(lastResult.NextPage)
	}
	switch out.Format {
	case output.FormatJSON:
		out.ListFooter(foot) // for a table --fields prints
		return out.JSONList(prices, np, cmdutil.ListTotal(tldPricingPage, lastResult.TotalCount))
	case output.FormatYAML:
		return out.YAMLList(prices, np, cmdutil.ListTotal(tldPricingPage, lastResult.TotalCount))
	}
	if len(prices) == 0 {
		cmdutil.EmptyPage(out, tldPricingPage, tldPricingHeaders, "TLD", "")
		return nil
	}
	price := func(p *float64) string {
		if p == nil {
			return "" // the table prints an empty cell as "—"
		}
		return out.Price(*p)
	}
	rows := make([][]string, len(prices))
	for i, p := range prices {
		rows[i] = []string{p.Tld, price(p.RegistrationPrice), price(p.RenewalPrice),
			price(p.TransferInPrice), price(p.DomainRestorationPrice)}
	}
	out.Table(tldPricingHeaders, rows, output.Identifier("TLD"))
	out.ListFooter(foot)
	return nil
}

// unpricedTLDs is the TLDs asked for that prices has no entry for. The API
// names an IDN TLD in Unicode while the request carries punycode, so each
// entry is compared in the form it was sent.
func unpricedTLDs(asked []string, prices []*coreapigo.TldPriceListEntry) []string {
	got := make(map[string]bool, len(prices))
	for _, p := range prices {
		got[strings.TrimPrefix(cmdutil.CanonicalDomain(p.Tld), ".")] = true
	}
	var missing []string
	for _, t := range asked {
		if !got[t] {
			missing = append(missing, t)
		}
	}
	return missing
}
