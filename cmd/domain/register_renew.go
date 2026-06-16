package domain

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"

	"github.com/charmbracelet/huh"
	"github.com/patramsey/namecom-cli/cmd/cmdutil"
	"github.com/patramsey/namecom-cli/internal/api"
	"github.com/patramsey/namecom-cli/internal/api/gen"
	"github.com/patramsey/namecom-cli/internal/output"
	"github.com/spf13/cobra"
)

var registerCmd = &cobra.Command{
	Use:   "register <domain>",
	Short: "Register a new domain",
	Example: `  namecom domain register example.com
  namecom domain register example.com --years 2 --privacy --autorenew`,
	Args: cmdutil.ExactArgs(1),
	RunE: runRegister,
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
	registerIdemKey      string
	renewYears           int
)

func init() {
	registerCmd.Flags().IntVar(&registerYears, "years", 1, "number of years to register")
	registerCmd.Flags().BoolVar(&registerPrivacy, "privacy", false, "enable WHOIS privacy")
	registerCmd.Flags().BoolVar(&registerAutorenew, "autorenew", false, "enable auto-renewal")
	registerCmd.Flags().StringVar(&registerContactsFile, "contacts-file", "", "JSON file with contact data")
	registerCmd.Flags().Float64Var(&registerPrice, "price", 0, "required for premium domains: confirmed purchase price in USD")
	registerCmd.Flags().StringVar(&registerIdemKey, "idempotency-key", "", "idempotency key for safe retries (auto-generated if omitted)")

	renewCmd.Flags().IntVar(&renewYears, "years", 1, "number of years to renew")
}

func runRegister(cmd *cobra.Command, args []string) error {
	out := cmdutil.Out(cmd)
	client := cmdutil.APIClient(cmd)
	yes := cmdutil.IsYes(cmd)
	dryRun := cmdutil.IsDryRun(cmd)
	domainName := args[0]

	// Guided form when interactive and no customization flags supplied.
	noFlags := !cmd.Flags().Changed("years") && !cmd.Flags().Changed("privacy") && !cmd.Flags().Changed("autorenew")
	if output.IsInteractive() && noFlags && !yes {
		if err := registerForm(); err != nil {
			return err
		}
	}

	// Fetch pricing first to show cost before charging.
	out.Step("Checking pricing for " + domainName + "…")
	pricingResp, err := client.Gen().GetPricingForDomain(cmd.Context(), domainName, &gen.GetPricingForDomainParams{})
	if err != nil {
		return fmt.Errorf("fetching pricing: %w", err)
	}
	var pricing gen.PricingResponseSchema
	if err := api.Decode(pricingResp, &pricing); err != nil {
		return err
	}

	regPrice := ""
	if pricing.PurchasePrice != nil {
		regPrice = fmt.Sprintf("$%.2f/yr", *pricing.PurchasePrice)
	}
	promptMsg := fmt.Sprintf("Register %s for %d year(s) at %s?", domainName, registerYears, regPrice)
	ok, err := confirm(out, yes, promptMsg)
	if err != nil {
		return err
	}
	if !ok {
		out.Warn("aborted")
		return nil
	}

	years := int32(registerYears)
	payload := gen.DomainCreatePayload{
		DomainName:       domainName,
		AutorenewEnabled: ptr(registerAutorenew),
		PrivacyEnabled:   ptr(registerPrivacy),
	}

	body := gen.CreateDomainJSONRequestBody{
		Domain: payload,
		Years:  &years,
	}
	if registerContactsFile != "" {
		f, err := os.ReadFile(registerContactsFile)
		if err != nil {
			return fmt.Errorf("reading contacts file: %w", err)
		}
		var contacts gen.ContactsRequest
		if err := json.Unmarshal(f, &contacts); err != nil {
			return fmt.Errorf("parsing contacts file: %w", err)
		}
		body.Domain.Contacts = &contacts
	}
	if registerPrice > 0 {
		body.PurchasePrice = ptr(registerPrice)
	} else if pricing.Premium && pricing.PurchasePrice != nil {
		// Premium domains require the confirmed price in the body. Use the price
		// we already fetched so the user isn't forced to pass --price manually.
		body.PurchasePrice = pricing.PurchasePrice
	}

	if dryRun {
		out.DryRun("POST", "/core/v1/domains", body)
		return nil
	}

	out.Step("Registering " + domainName + "…")
	params := &gen.CreateDomainParams{}
	if registerIdemKey != "" {
		params.XIdempotencyKey = ptr(registerIdemKey)
	}
	resp, err := client.Gen().CreateDomain(cmd.Context(), params, body)
	if err != nil {
		return err
	}
	var created gen.CreateDomainResponseSchema
	if err := api.Decode(resp, &created); err != nil {
		return err
	}

	switch out.Format {
	case output.FormatJSON:
		return out.JSON(created)
	case output.FormatYAML:
		return out.YAML(created)
	default:
		out.Success(fmt.Sprintf("Registered %s (order #%d, total $%.2f)", created.Domain.DomainName, created.Order, created.TotalPaid))
		out.Hint(fmt.Sprintf("Run 'namecom dns list %s' to add DNS records", created.Domain.DomainName))
		if !registerAutorenew {
			out.Hint(fmt.Sprintf("Run 'namecom domain autorenew %s on' to enable auto-renewal", created.Domain.DomainName))
		}
	}
	return nil
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
	yes := cmdutil.IsYes(cmd)
	dryRun := cmdutil.IsDryRun(cmd)
	domainName := args[0]

	// Fetch pricing to show renewal cost before charging.
	out.Step("Checking renewal pricing for " + domainName + "…")
	pricingResp, err := client.Gen().GetPricingForDomain(cmd.Context(), domainName, &gen.GetPricingForDomainParams{})
	if err != nil {
		return fmt.Errorf("fetching pricing: %w", err)
	}
	var pricing gen.PricingResponseSchema
	if err := api.Decode(pricingResp, &pricing); err != nil {
		return err
	}

	renewPrice := ""
	if pricing.RenewalPrice != nil {
		renewPrice = fmt.Sprintf("$%.2f/yr", *pricing.RenewalPrice)
	}
	promptMsg := fmt.Sprintf("Renew %s for %d year(s) at %s?", domainName, renewYears, renewPrice)
	ok, err := confirm(out, yes, promptMsg)
	if err != nil {
		return err
	}
	if !ok {
		out.Warn("aborted")
		return nil
	}

	years := int32(renewYears)
	body := gen.RenewDomainJSONRequestBody{Years: &years}

	if dryRun {
		out.DryRun("POST", fmt.Sprintf("/core/v1/domains/%s:renew", domainName), body)
		return nil
	}

	resp, err := client.Gen().RenewDomain(cmd.Context(), domainName, body)
	if err != nil {
		return err
	}
	var renewed gen.RenewDomainResponseSchema
	if err := api.Decode(resp, &renewed); err != nil {
		return err
	}

	switch out.Format {
	case output.FormatJSON:
		return out.JSON(renewed)
	case output.FormatYAML:
		return out.YAML(renewed)
	default:
		orderNum := int32(0)
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
