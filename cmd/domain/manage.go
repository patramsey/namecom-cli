package domain

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	coreapigo "github.com/namedotcom/core-api-go"
	"github.com/patramsey/namecom-cli/cmd/cmdutil"
	"github.com/patramsey/namecom-cli/internal/api"
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

// applyDomainToggle performs a single-field UpdateDomain (PATCH).
//
// It replaces the deprecated :lock, :unlock, :enableAutorenew,
// :disableAutorenew, :enableWhoisPrivacy and :disableWhoisPrivacy endpoints,
// each of which the spec marks `deprecated: true` with "deprecated in favor of
// the new UpdateDomain API. This will be removed in a future release."
//
// Only the field being changed is sent. All three fields are *bool with
// omitempty, and the schema combines them with anyOf, so a partial body is
// valid and leaves the other two untouched — no read-modify-write needed.
//
// Note PurchasePrivacy is deliberately not used for `privacy on`: the spec
// describes it as "a billable action" that purchases and enables, whereas
// UpdateDomain is the documented successor to the deprecated toggle.
func applyDomainToggle(cmd *cobra.Command, req *coreapigo.UpdateDomainRequest) error {
	client := cmdutil.APIClient(cmd)
	_, err := client.SDK().Domains.UpdateDomain(cmd.Context(), req)
	return api.FromSDKError(err)
}

// toggleDryRun prints the UpdateDomain request a toggle would send. DomainName
// is tagged `json:"-"`, so marshalling the request yields the body alone.
func toggleDryRun(out *output.Config, req *coreapigo.UpdateDomainRequest) {
	out.DryRun("PATCH", fmt.Sprintf("/core/v1/domains/%s", req.DomainName), req)
}

func runLock(cmd *cobra.Command, args []string) error {
	out := cmdutil.Out(cmd)
	dryRun := cmdutil.IsDryRun(cmd)
	toggle := strings.ToLower(args[0])
	if toggle != "on" && toggle != "off" {
		return fmt.Errorf("expected 'on' or 'off', got %q", args[0])
	}
	enable := toggle == "on"
	domainName, err := cmdutil.DomainArg(args, 1)
	if err != nil {
		return err
	}

	req := &coreapigo.UpdateDomainRequest{DomainName: domainName, Locked: &enable}
	if dryRun {
		toggleDryRun(out, req)
		return nil
	}
	if err := applyDomainToggle(cmd, req); err != nil {
		return err
	}
	if enable {
		out.Success(fmt.Sprintf("Transfer lock enabled for %s", domainName))
		out.Hint(fmt.Sprintf("Run 'namecom domain get %s' to confirm status", domainName))
	} else {
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
	Args: cmdutil.ExactArgs(2),
	RunE: runAutorenew,
	ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return []string{"on", "off"}, cobra.ShellCompDirectiveNoFileComp
		}
		return cmdutil.CompleteDomains(cmd, args[1:], toComplete)
	},
}

func runAutorenew(cmd *cobra.Command, args []string) error {
	out := cmdutil.Out(cmd)
	dryRun := cmdutil.IsDryRun(cmd)
	toggle := strings.ToLower(args[0])
	if toggle != "on" && toggle != "off" {
		return fmt.Errorf("expected 'on' or 'off', got %q", args[0])
	}
	enable := toggle == "on"
	domainName, err := cmdutil.DomainArg(args, 1)
	if err != nil {
		return err
	}
	req := &coreapigo.UpdateDomainRequest{DomainName: domainName, AutorenewEnabled: &enable}
	if dryRun {
		toggleDryRun(out, req)
		return nil
	}
	if err := applyDomainToggle(cmd, req); err != nil {
		return err
	}
	if enable {
		out.Success(fmt.Sprintf("Auto-renewal enabled for %s", domainName))
		out.Hint(fmt.Sprintf("Run 'namecom domain get %s' to confirm settings", domainName))
	} else {
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
	Args: cmdutil.ExactArgs(2),
	RunE: runPrivacy,
	ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return []string{"on", "off"}, cobra.ShellCompDirectiveNoFileComp
		}
		return cmdutil.CompleteDomains(cmd, args[1:], toComplete)
	},
}

func runPrivacy(cmd *cobra.Command, args []string) error {
	out := cmdutil.Out(cmd)
	dryRun := cmdutil.IsDryRun(cmd)
	toggle := strings.ToLower(args[0])
	if toggle != "on" && toggle != "off" {
		return fmt.Errorf("expected 'on' or 'off', got %q", args[0])
	}
	enable := toggle == "on"
	domainName, err := cmdutil.DomainArg(args, 1)
	if err != nil {
		return err
	}

	req := &coreapigo.UpdateDomainRequest{DomainName: domainName, PrivacyEnabled: &enable}
	if dryRun {
		toggleDryRun(out, req)
		return nil
	}
	// Enabling privacy can incur a charge on accounts without a bundled privacy
	// plan, so confirm before doing it. Disabling never charges.
	if enable {
		ok, err := confirm(out, cmdutil.IsYes(cmd), fmt.Sprintf("Enable WHOIS privacy for %s? This may be a billable action.", domainName))
		if err != nil {
			return err
		}
		if !ok {
			out.Warn("aborted")
			return nil
		}
	}
	if err := applyDomainToggle(cmd, req); err != nil {
		return err
	}
	if enable {
		out.Success(fmt.Sprintf("WHOIS privacy enabled for %s", domainName))
		out.Hint(fmt.Sprintf("Run 'namecom domain get %s' to confirm privacy status", domainName))
	} else {
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
	domain, err := cmdutil.DomainArg(args, 0)
	if err != nil {
		return err
	}
	ns := strings.Split(setNSList, ",")
	for i := range ns {
		ns[i] = strings.TrimSpace(ns[i])
	}
	for i, n := range ns {
		if err := cmdutil.ValidNameserver(n, i); err != nil {
			return err
		}
	}
	if dryRun {
		out.DryRun("POST", fmt.Sprintf("/core/v1/domains/%s:setNameservers", domain), nil)
		fmt.Fprintf(out.Writer, "  ns=%s\n", setNSList)
		return nil
	}
	stop := out.Spin("Updating nameservers…")
	_, err = client.SDK().Domains.SetNameservers(cmd.Context(),
		&coreapigo.DomainsSetNameserversBody{DomainName: domain, Nameservers: ns})
	stop()
	if err != nil {
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
	cmdutil.GroupCmd(contactsCmd)
	contactsCmd.AddCommand(contactsGetCmd, contactsSetCmd)
}

func runContactsGet(cmd *cobra.Command, args []string) error {
	out := cmdutil.Out(cmd)
	client := cmdutil.APIClient(cmd)

	domain, err := cmdutil.DomainArg(args, 0)
	if err != nil {
		return err
	}
	d, err := client.SDK().Domains.GetDomain(cmd.Context(),
		&coreapigo.GetDomainRequest{DomainName: domain})
	if err != nil {
		return err
	}

	switch out.Format {
	case output.FormatJSON:
		return out.JSON(d.Contacts)
	case output.FormatYAML:
		return out.YAML(d.Contacts)
	default:
		if err := out.JSON(d.Contacts); err != nil {
			return err
		}
		warnUnverifiedContacts(out, *d.Contacts)
		return nil
	}
}

// warnUnverifiedContacts calls out contacts pending ICANN verification.
//
// The consequence is severe and time-boxed: the spec states that if a contact
// record is not verified by its deadline "the domain may become locked by the
// registry", typically 15 days from creation. Both `domain register` and
// `domain contacts set` can trigger verification, and until now the CLI never
// mentioned it — the isVerified flag was buried in a raw JSON dump.
//
// GetDomain already returns this, so no extra request is made.
func warnUnverifiedContacts(out *output.Config, c coreapigo.Contacts) {
	var pending []string
	check := func(role string, verified *bool) {
		if verified != nil && !*verified {
			pending = append(pending, role)
		}
	}
	if c.Registrant != nil {
		check("registrant", c.Registrant.IsVerified)
	}
	if c.Admin != nil {
		check("admin", c.Admin.IsVerified)
	}
	if c.Tech != nil {
		check("tech", c.Tech.IsVerified)
	}
	if c.Billing != nil {
		check("billing", c.Billing.IsVerified)
	}
	if len(pending) == 0 {
		return
	}
	out.WarnBox(
		fmt.Sprintf("Unverified contact(s): %s", strings.Join(pending, ", ")),
		"ICANN requires contact verification. If it is not completed by the deadline",
		"(typically 15 days from when it was triggered) the registry may LOCK the domain.",
		"Check the inbox for the verification email — name.com can resend it.",
	)
}

func runContactsSet(cmd *cobra.Command, args []string) error {
	out := cmdutil.Out(cmd)
	client := cmdutil.APIClient(cmd)
	dryRun := cmdutil.IsDryRun(cmd)

	domain, err := cmdutil.DomainArg(args, 0)
	if err != nil {
		return err
	}

	f, err := os.ReadFile(contactsFile) //nolint:gosec // G304: --contacts-file names the file to read; that is the flag's purpose
	if err != nil {
		return fmt.Errorf("reading contacts file: %w", err)
	}
	var contacts coreapigo.ContactsRequest
	if err := json.Unmarshal(f, &contacts); err != nil {
		return fmt.Errorf("parsing contacts file: %w", err)
	}

	if dryRun {
		out.DryRun("POST", fmt.Sprintf("/core/v1/domains/%s:setContacts", domain), contacts)
		return nil
	}

	_, err = client.SDK().Domains.SetContacts(cmd.Context(),
		&coreapigo.DomainsSetContactsBody{DomainName: domain, Contacts: &contacts})
	if err != nil {
		return err
	}
	out.Success(fmt.Sprintf("Contacts updated for %s", domain))
	// Updating the registrant can start an ICANN verification clock. The spec:
	// "When registrant contact information is updated, validation may be
	// triggered if the new contact information has not been previously
	// validated. This validation is required by ICANN for all TLDs except
	// country-code TLDs (ccTLDs)." Missing the deadline can get the domain
	// registry-locked, so say so at the moment the clock may have started.
	out.Hint("If this changed the registrant, ICANN may require email verification — " +
		fmt.Sprintf("run 'namecom domain contacts get %s' to check", domain))
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

	domain, err := cmdutil.DomainArg(args, 0)
	if err != nil {
		return err
	}
	result, err := client.SDK().Domains.GetAuthCodeForDomain(cmd.Context(),
		&coreapigo.GetAuthCodeForDomainRequest{DomainName: domain})
	if err != nil {
		return err
	}

	// --quiet prints just the code, so it can be captured directly:
	//   CODE=$(namecom domain auth-code example.com -q)
	// Detail commands ignored --quiet entirely and printed a bordered table.
	if out.QuietMode {
		out.PrintQuiet([]string{result.AuthCode})
		return nil
	}

	switch out.Format {
	case output.FormatJSON:
		return out.JSON(result)
	case output.FormatYAML:
		return out.YAML(result)
	default:
		out.Table([]string{"DOMAIN", "AUTH CODE"}, [][]string{{domain, result.AuthCode}})
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

	domain, err := cmdutil.DomainArg(args, 0)
	if err != nil {
		return err
	}
	pricing, err := client.SDK().Domains.GetPricingForDomain(cmd.Context(),
		&coreapigo.GetPricingForDomainRequest{DomainName: domain})
	if err != nil {
		return api.FromSDKError(err)
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
	domain, err := cmdutil.DomainArg(args, 0)
	if err != nil {
		return err
	}
	current, err := client.SDK().Domains.GetDomain(cmd.Context(),
		&coreapigo.GetDomainRequest{DomainName: domain})
	if err != nil {
		return api.FromSDKError(err)
	}

	autorenew := current.AutorenewEnabled
	privacy := current.PrivacyEnabled
	locked := current.Locked
	// All three fields are sent on every call: this is a read-modify-write that
	// restates the current values, so an unset flag preserves what is already
	// there rather than clearing it.
	//
	// Until SDK v1.33.5 that could not be expressed with the typed request.
	// The spec combines the three with anyOf, meaning "at least one", but Fern
	// modelled it as an exclusive union whose MarshalJSON returned on the FIRST
	// non-nil variant — so setting all three silently transmitted only
	// autorenewEnabled and dropped locked and privacyEnabled, on a command
	// where "locked" is the transfer lock. The workaround was a raw
	// map[string]any passed through option.WithBodyProperties.
	//
	// v1.33.5 removed the union: the fields are now flat on the request, each
	// *bool with omitempty, and a non-nil pointer to false still serialises.
	// The bytes on the wire are unchanged, which TestRequestShape_Domain pins.
	if cmd.Flags().Changed("autorenew") {
		autorenew, _ = cmd.Flags().GetBool("autorenew")
	}
	if cmd.Flags().Changed("privacy") {
		privacy, _ = cmd.Flags().GetBool("privacy")
	}
	if cmd.Flags().Changed("lock") {
		locked, _ = cmd.Flags().GetBool("lock")
	}

	req := &coreapigo.UpdateDomainRequest{
		DomainName:       domain,
		AutorenewEnabled: &autorenew,
		PrivacyEnabled:   &privacy,
		Locked:           &locked,
	}

	if dryRun {
		out.DryRun("PATCH", fmt.Sprintf("/core/v1/domains/%s", domain), req)
		return nil
	}

	// Enabling privacy can be billable, and `domain privacy on` confirms before
	// doing it. This command reaches the identical API call, so it has to ask
	// too — otherwise there are two routes to the same charge and only one of
	// them pauses. Only gate on turning it ON: disabling never costs anything.
	if cmd.Flags().Changed("privacy") {
		if v, _ := cmd.Flags().GetBool("privacy"); v && !current.PrivacyEnabled {
			ok, cerr := confirm(out, cmdutil.IsYes(cmd),
				fmt.Sprintf("Enable WHOIS privacy for %s? This may be a billable action.", domain))
			if cerr != nil {
				return cerr
			}
			if !ok {
				out.Warn("aborted")
				return nil
			}
		}
	}
	// Removing the transfer lock has no cost but a real security consequence,
	// so warn for the same reason `domain lock off` does.
	if cmd.Flags().Changed("lock") {
		if v, _ := cmd.Flags().GetBool("lock"); !v && current.Locked {
			out.WarnBox("Transfer lock removed — re-enable it after any transfer completes to protect against unauthorized outbound transfers")
		}
	}

	updated, err := client.SDK().Domains.UpdateDomain(cmd.Context(), req)
	if err != nil {
		return api.FromSDKError(err)
	}

	switch out.Format {
	case output.FormatJSON:
		return out.JSON(updated)
	case output.FormatYAML:
		return out.YAML(updated)
	default:
		out.Success(fmt.Sprintf("Updated %s", domain))
		out.Hint(fmt.Sprintf("Run 'namecom domain get %s' to confirm the new settings", domain))
	}
	return nil
}

func init() {
	updateCmd.Flags().Bool("autorenew", false, "enable/disable auto-renewal")
	updateCmd.Flags().Bool("privacy", false, "enable/disable WHOIS privacy")
	updateCmd.Flags().Bool("lock", false, "enable/disable transfer lock")
}
