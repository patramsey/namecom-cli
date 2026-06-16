package domain

import (
	"fmt"

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
			AutorenewEnabled: ptr(false),
			PrivacyEnabled:   ptr(false),
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

var checkCmd = &cobra.Command{
	Use:   "check <domain> [<domain>...]",
	Short: "Check exact availability and price for one or more domains",
	Example: `  namecom domain check example.com
  namecom domain check example.com myidea.io coolname.dev`,
	Args: cmdutil.MinimumNArgs(1),
	RunE: runCheck,
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

	// When checking a single domain in interactive table mode, offer to register it immediately
	// if it's available — the price is already in the response so no extra API call is needed.
	if out.Format != output.FormatTable || out.QuietMode || !output.IsInteractive() {
		return nil
	}
	results := result.Results
	if results == nil || len(*results) != 1 {
		return nil
	}
	r := (*results)[0]
	if !r.Purchasable {
		return nil
	}
	price := ""
	if r.PurchasePrice != nil {
		price = fmt.Sprintf(" for $%.2f/yr", *r.PurchasePrice)
	}
	ok, err := confirm(cmdutil.IsYes(cmd), fmt.Sprintf("Register %s%s?", r.DomainName, price))
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
