package domain

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/patramsey/namecom-cli/cmd/cmdutil"
	"github.com/patramsey/namecom-cli/internal/api"
	"github.com/patramsey/namecom-cli/internal/api/gen"
	"github.com/patramsey/namecom-cli/internal/output"
	"github.com/spf13/cobra"
)

// -- lock --

var lockCmd = &cobra.Command{
	Use:   "lock <on|off> <domain>",
	Short: "Enable or disable transfer lock",
	Example: `  namecom domain lock on example.com
  namecom domain lock off example.com`,
	Args: cmdutil.ExactArgs(2),
	RunE: runLock,
	ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return []string{"on", "off"}, cobra.ShellCompDirectiveNoFileComp
		}
		return cmdutil.CompleteDomains(cmd, args[1:], toComplete)
	},
}

func runLock(cmd *cobra.Command, args []string) error {
	out := cmdutil.Out(cmd)
	client := cmdutil.APIClient(cmd)
	dryRun := cmdutil.IsDryRun(cmd)
	enable := strings.ToLower(args[0]) == "on"
	domainName := args[1]

	if enable {
		if dryRun {
			out.DryRun("POST", fmt.Sprintf("/core/v1/domains/%s:lock", domainName), nil)
			return nil
		}
		resp, err := client.Gen().LockDomain(cmd.Context(), domainName, &gen.LockDomainParams{})
		if err != nil {
			return err
		}
		if err := api.Decode(resp, nil); err != nil {
			return err
		}
		out.Success(fmt.Sprintf("Transfer lock enabled for %s", domainName))
		out.Hint(fmt.Sprintf("Run 'namecom domain get %s' to confirm status", domainName))
	} else {
		if dryRun {
			out.DryRun("DELETE", fmt.Sprintf("/core/v1/domains/%s:lock", domainName), nil)
			return nil
		}
		resp, err := client.Gen().UnlockDomain(cmd.Context(), domainName, &gen.UnlockDomainParams{})
		if err != nil {
			return err
		}
		if err := api.Decode(resp, nil); err != nil {
			return err
		}
		out.Success(fmt.Sprintf("Transfer lock disabled for %s", domainName))
		out.WarnBox("Lock removed — re-enable after transfers are complete to protect against unauthorized outbound transfers")
	}
	return nil
}

// -- autorenew --

var autorenewCmd = &cobra.Command{
	Use:   "autorenew <on|off> <domain>",
	Short: "Enable or disable automatic renewal",
	Example: `  namecom domain autorenew on example.com
  namecom domain autorenew off example.com`,
	Args:  cmdutil.ExactArgs(2),
	RunE:  runAutorenew,
	ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return []string{"on", "off"}, cobra.ShellCompDirectiveNoFileComp
		}
		return cmdutil.CompleteDomains(cmd, args[1:], toComplete)
	},
}

func runAutorenew(cmd *cobra.Command, args []string) error {
	out := cmdutil.Out(cmd)
	client := cmdutil.APIClient(cmd)
	dryRun := cmdutil.IsDryRun(cmd)
	enable := strings.ToLower(args[0]) == "on"
	domainName := args[1]
	if enable {
		if dryRun {
			out.DryRun("POST", fmt.Sprintf("/core/v1/domains/%s:enable-autorenew", domainName), nil)
			return nil
		}
		r, err := client.Gen().EnableAutorenew(cmd.Context(), domainName, &gen.EnableAutorenewParams{})
		if err != nil {
			return err
		}
		if err := api.Decode(r, nil); err != nil {
			return err
		}
		out.Success(fmt.Sprintf("Auto-renewal enabled for %s", domainName))
		out.Hint(fmt.Sprintf("Run 'namecom domain get %s' to confirm settings", domainName))
	} else {
		if dryRun {
			out.DryRun("POST", fmt.Sprintf("/core/v1/domains/%s:disable-autorenew", domainName), nil)
			return nil
		}
		r, e := client.Gen().DisableAutorenew(cmd.Context(), domainName, &gen.DisableAutorenewParams{})
		if e != nil {
			return e
		}
		if err := api.Decode(r, nil); err != nil {
			return err
		}
		out.Success(fmt.Sprintf("Auto-renewal disabled for %s", domainName))
		out.Hint(fmt.Sprintf("Remember to renew manually before expiry — run 'namecom domain get %s' to check the expiry date", domainName))
	}
	return nil
}

// -- privacy --

var privacyCmd = &cobra.Command{
	Use:   "privacy <on|off> <domain>",
	Short: "Enable or disable WHOIS privacy",
	Example: `  namecom domain privacy on example.com
  namecom domain privacy off example.com`,
	Args:  cmdutil.ExactArgs(2),
	RunE:  runPrivacy,
	ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return []string{"on", "off"}, cobra.ShellCompDirectiveNoFileComp
		}
		return cmdutil.CompleteDomains(cmd, args[1:], toComplete)
	},
}

func runPrivacy(cmd *cobra.Command, args []string) error {
	out := cmdutil.Out(cmd)
	client := cmdutil.APIClient(cmd)
	dryRun := cmdutil.IsDryRun(cmd)
	enable := strings.ToLower(args[0]) == "on"
	domainName := args[1]

	if enable {
		if dryRun {
			out.DryRun("POST", fmt.Sprintf("/core/v1/domains/%s:enable-privacy", domainName), nil)
			return nil
		}
		// PurchasePrivacy may charge money — confirm first.
		yes := cmdutil.IsYes(cmd)
		ok, err := confirm(out, yes, fmt.Sprintf("Purchase WHOIS privacy for %s?", domainName))
		if err != nil {
			return err
		}
		if !ok {
			out.Warn("aborted")
			return nil
		}
		r, err := client.Gen().EnableWhoisPrivacy(cmd.Context(), domainName, &gen.EnableWhoisPrivacyParams{})
		if err != nil {
			return err
		}
		if err := api.Decode(r, nil); err != nil {
			return err
		}
		out.Success(fmt.Sprintf("WHOIS privacy enabled for %s", domainName))
		out.Hint(fmt.Sprintf("Run 'namecom domain get %s' to confirm privacy status", domainName))
	} else {
		if dryRun {
			out.DryRun("POST", fmt.Sprintf("/core/v1/domains/%s:disable-privacy", domainName), nil)
			return nil
		}
		r, err := client.Gen().DisableWhoisPrivacy(cmd.Context(), domainName, &gen.DisableWhoisPrivacyParams{})
		if err != nil {
			return err
		}
		if err := api.Decode(r, nil); err != nil {
			return err
		}
		out.Success(fmt.Sprintf("WHOIS privacy disabled for %s", domainName))
	}
	return nil
}

// -- set-ns --

var setNSCmd = &cobra.Command{
	Use:   "set-ns <domain> --ns ns1.example.com,ns2.example.com",
	Short: "Set nameservers for a domain",
	Example: `  namecom domain set-ns example.com --ns ns1.name.com,ns2.name.com
  namecom domain set-ns example.com --ns ns1.example.com,ns2.example.com  # custom nameservers`,
	Args:              cmdutil.ExactArgs(1),
	RunE:              runSetNS,
	ValidArgsFunction: cmdutil.CompleteDomains,
}

var setNSList string

func init() {
	setNSCmd.Flags().StringVar(&setNSList, "ns", "", "comma-separated nameservers (required)")
	_ = setNSCmd.MarkFlagRequired("ns")
}

func runSetNS(cmd *cobra.Command, args []string) error {
	out := cmdutil.Out(cmd)
	client := cmdutil.APIClient(cmd)
	dryRun := cmdutil.IsDryRun(cmd)
	domain := args[0]
	ns := strings.Split(setNSList, ",")
	for i := range ns {
		ns[i] = strings.TrimSpace(ns[i])
	}
	if dryRun {
		out.DryRun("POST", fmt.Sprintf("/core/v1/domains/%s/nameservers", domain), nil)
		fmt.Fprintf(out.Writer, "  ns=%s\n", setNSList)
		return nil
	}
	stop := out.Spin("Updating nameservers…")
	resp, err := client.Gen().SetNameservers(cmd.Context(), domain, gen.SetNameserversJSONRequestBody{Nameservers: ns})
	stop()
	if err != nil {
		return err
	}
	if err := api.Decode(resp, nil); err != nil {
		return err
	}
	out.Success(fmt.Sprintf("Nameservers updated for %s", domain))
	out.Hint("DNS propagation typically takes a few minutes to a few hours")
	return nil
}

// -- contacts --

var contactsCmd = &cobra.Command{
	Use:   "contacts",
	Short: "View and update registrant, admin, and tech contacts",
}

var contactsGetCmd = &cobra.Command{
	Use:   "get <domain>",
	Short: "Get contact information for a domain",
	Example: `  namecom domain contacts get example.com
  namecom domain contacts get example.com -o json > contacts.json   # save for editing`,
	Args:              cmdutil.ExactArgs(1),
	RunE:              runContactsGet,
	ValidArgsFunction: cmdutil.CompleteDomains,
}

var contactsSetCmd = &cobra.Command{
	Use:   "set <domain> --from-file contacts.json",
	Short: "Set contact information for a domain",
	Example: `  namecom domain contacts get example.com -o json > contacts.json
  # edit contacts.json, then:
  namecom domain contacts set example.com --from-file contacts.json`,
	Args:              cmdutil.ExactArgs(1),
	RunE:              runContactsSet,
	ValidArgsFunction: cmdutil.CompleteDomains,
}

var contactsFile string

func init() {
	contactsSetCmd.Flags().StringVar(&contactsFile, "from-file", "", "JSON file with contact data (required)")
	_ = contactsSetCmd.MarkFlagRequired("from-file")
	contactsCmd.AddCommand(contactsGetCmd, contactsSetCmd)
}

func runContactsGet(cmd *cobra.Command, args []string) error {
	out := cmdutil.Out(cmd)
	client := cmdutil.APIClient(cmd)

	resp, err := client.Gen().GetDomain(cmd.Context(), args[0])
	if err != nil {
		return err
	}
	var d gen.DomainResponsePayload
	if err := api.Decode(resp, &d); err != nil {
		return err
	}

	switch out.Format {
	case output.FormatJSON:
		return out.JSON(d.Contacts)
	case output.FormatYAML:
		return out.YAML(d.Contacts)
	default:
		return out.JSON(d.Contacts)
	}
}

func runContactsSet(cmd *cobra.Command, args []string) error {
	out := cmdutil.Out(cmd)
	client := cmdutil.APIClient(cmd)
	dryRun := cmdutil.IsDryRun(cmd)

	f, err := os.ReadFile(contactsFile)
	if err != nil {
		return fmt.Errorf("reading contacts file: %w", err)
	}
	var contacts gen.ContactsRequest
	if err := json.Unmarshal(f, &contacts); err != nil {
		return fmt.Errorf("parsing contacts file: %w", err)
	}

	if dryRun {
		out.DryRun("PUT", fmt.Sprintf("/core/v1/domains/%s/contacts", args[0]), contacts)
		return nil
	}

	resp, err := client.Gen().SetContacts(cmd.Context(), args[0], gen.SetContactsJSONRequestBody{Contacts: &contacts})
	if err != nil {
		return err
	}
	if err := api.Decode(resp, nil); err != nil {
		return err
	}
	out.Success(fmt.Sprintf("Contacts updated for %s", args[0]))
	return nil
}

// -- auth-code --

var authCodeCmd = &cobra.Command{
	Use:   "auth-code <domain>",
	Short: "Get the EPP/transfer auth code for a domain",
	Example: `  namecom domain auth-code example.com
  namecom domain auth-code example.com -o json | jq -r .authCode   # extract for scripting`,
	Args:              cmdutil.ExactArgs(1),
	RunE:              runAuthCode,
	ValidArgsFunction: cmdutil.CompleteDomains,
}

func runAuthCode(cmd *cobra.Command, args []string) error {
	out := cmdutil.Out(cmd)
	client := cmdutil.APIClient(cmd)

	resp, err := client.Gen().GetAuthCodeForDomain(cmd.Context(), args[0])
	if err != nil {
		return err
	}
	var result gen.AuthCodeResponseSchema
	if err := api.Decode(resp, &result); err != nil {
		return err
	}

	switch out.Format {
	case output.FormatJSON:
		return out.JSON(result)
	case output.FormatYAML:
		return out.YAML(result)
	default:
		out.Table([]string{"DOMAIN", "AUTH CODE"}, [][]string{{args[0], result.AuthCode}})
	}
	return nil
}

// -- pricing --

var pricingCmd = &cobra.Command{
	Use:   "pricing <domain>",
	Short: "Get registration, renewal, and transfer pricing for a domain",
	Example: `  namecom domain pricing example.com
  namecom domain pricing premium.io      # shows premium flag and confirmed price`,
	Args:              cmdutil.ExactArgs(1),
	RunE:              runPricing,
	ValidArgsFunction: cmdutil.CompleteDomains,
}

func runPricing(cmd *cobra.Command, args []string) error {
	out := cmdutil.Out(cmd)
	client := cmdutil.APIClient(cmd)

	resp, err := client.Gen().GetPricingForDomain(cmd.Context(), args[0], &gen.GetPricingForDomainParams{})
	if err != nil {
		return err
	}
	var pricing gen.PricingResponseSchema
	if err := api.Decode(resp, &pricing); err != nil {
		return err
	}

	switch out.Format {
	case output.FormatJSON:
		return out.JSON(pricing)
	case output.FormatYAML:
		return out.YAML(pricing)
	default:
		fmtPrice := func(p *float64) string {
			if p == nil {
				return "N/A"
			}
			return fmt.Sprintf("$%.2f", *p)
		}
		out.Table([]string{"TYPE", "PRICE"}, [][]string{
			{"Register", fmtPrice(pricing.PurchasePrice)},
			{"Renew", fmtPrice(pricing.RenewalPrice)},
			{"Transfer", fmtPrice(pricing.TransferPrice)},
			{"Premium", boolStr(pricing.Premium)},
		})
	}
	return nil
}

// -- update --

var updateCmd = &cobra.Command{
	Use:   "update <domain>",
	Short: "Update domain settings (autorenew, privacy, lock) in one call",
	Example: `  namecom domain update example.com --autorenew=true
  namecom domain update example.com --privacy=true --lock=true`,
	Args:              cmdutil.ExactArgs(1),
	RunE:              runUpdate,
	ValidArgsFunction: cmdutil.CompleteDomains,
}

func init() {
	// no-op: flags added in update_flags.go if we add them; kept for extensibility
}

func runUpdate(cmd *cobra.Command, args []string) error {
	out := cmdutil.Out(cmd)
	client := cmdutil.APIClient(cmd)
	dryRun := cmdutil.IsDryRun(cmd)

	// Read-modify-write: fetch current state first.
	getResp, err := client.Gen().GetDomain(cmd.Context(), args[0])
	if err != nil {
		return err
	}
	var current gen.DomainResponsePayload
	if err := api.Decode(getResp, &current); err != nil {
		return err
	}

	body := gen.UpdateDomainJSONRequestBody{
		AutorenewEnabled: ptr(bool(current.AutorenewEnabled)),
		PrivacyEnabled:   ptr(bool(current.PrivacyEnabled)),
		Locked:           ptr(bool(current.Locked)),
	}

	if cmd.Flags().Changed("autorenew") {
		v, _ := cmd.Flags().GetBool("autorenew")
		body.AutorenewEnabled = ptr(v)
	}
	if cmd.Flags().Changed("privacy") {
		v, _ := cmd.Flags().GetBool("privacy")
		body.PrivacyEnabled = ptr(v)
	}
	if cmd.Flags().Changed("lock") {
		v, _ := cmd.Flags().GetBool("lock")
		body.Locked = ptr(v)
	}

	if dryRun {
		out.DryRun("PUT", fmt.Sprintf("/core/v1/domains/%s", args[0]), body)
		return nil
	}

	resp, err := client.Gen().UpdateDomain(cmd.Context(), args[0], body)
	if err != nil {
		return err
	}
	var updated gen.DomainResponsePayload
	if err := api.Decode(resp, &updated); err != nil {
		return err
	}

	switch out.Format {
	case output.FormatJSON:
		return out.JSON(updated)
	case output.FormatYAML:
		return out.YAML(updated)
	default:
		out.Success(fmt.Sprintf("Updated %s", args[0]))
		out.Hint(fmt.Sprintf("Run 'namecom domain get %s' to confirm the new settings", args[0]))
	}
	return nil
}

func init() {
	updateCmd.Flags().Bool("autorenew", false, "enable/disable auto-renewal")
	updateCmd.Flags().Bool("privacy", false, "enable/disable WHOIS privacy")
	updateCmd.Flags().Bool("lock", false, "enable/disable transfer lock")
}
