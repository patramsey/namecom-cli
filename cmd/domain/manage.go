package domain

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
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
//
// DomainName is tagged `json:"-"`, so previewing the request previews the body
// alone. prompt is the confirmation question, or "" for none. sent is false
// under --dry-run or when the user declines.
func applyDomainToggle(cmd *cobra.Command, req *coreapigo.UpdateDomainRequest, prompt string) (sent bool, err error) {
	client := cmdutil.APIClient(cmd)
	sent, err = cmdutil.RunWrite(cmd, cmdutil.Write[*coreapigo.UpdateDomainRequest]{
		Method: "PATCH",
		Path:   fmt.Sprintf("/core/v1/domains/%s", req.DomainName),
		Body:   req,
		Prompt: prompt,
	}, func(ctx context.Context, req *coreapigo.UpdateDomainRequest) error {
		_, err := client.SDK().Domains.UpdateDomain(ctx, req)
		return api.FromSDKError(err)
	})
	return sent, explainUpdateError(err, req)
}

// toggleAlreadySet reads the domain and reports whether the setting get
// returns already equals want, in which case the toggle sends nothing.
//
// The PATCH is not idempotent in practice: during the 60-day transfer lock
// after a registration or transfer the API refuses any body carrying `locked`,
// so `lock on` for a domain that is already locked failed with "Domain can not
// be unlocked until …" (#187). One GET avoids sending a change that is not a
// change. It runs under --dry-run too, which then reports the same outcome
// instead of previewing a request that would not be made.
//
// A response with no domain object is not evidence of anything, so it reports
// false and the PATCH goes ahead as before.
func toggleAlreadySet(cmd *cobra.Command, domainName string, want bool, get func(*coreapigo.DomainResponsePayload) bool) (bool, error) {
	d, err := cmdutil.APIClient(cmd).SDK().Domains.GetDomain(cmd.Context(),
		&coreapigo.GetDomainRequest{DomainName: domainName})
	if err != nil {
		return false, api.FromSDKError(err)
	}
	return d != nil && get(d) == want, nil
}

// onOff is a toggle state in the words its command takes.
func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

// transferLockError is the API's refusal to change `locked` during the 60-day
// transfer lock, restated. The API answers "Invalid Argument (Domain can not
// be unlocked until 2026-11-28 06:37:39)", which says neither why nor that the
// lock lifts by itself. The date is the API's, kept verbatim. It unwraps to
// the *api.APIError, so the exit code is unchanged.
type transferLockError struct {
	domain, until string
	locking       bool
	err           error
}

func (e *transferLockError) Error() string {
	if e.locking {
		return fmt.Sprintf("%s is already transfer-locked: it is in the 60-day lock that follows a registration or transfer, "+
			"and the API refuses any change to the lock, even to restate it, until %s", e.domain, e.until)
	}
	return fmt.Sprintf("%s cannot be unlocked until %s: it is in the 60-day transfer lock that follows a registration or transfer",
		e.domain, e.until)
}

func (e *transferLockError) Unwrap() error { return e.err }

func (e *transferLockError) UserHint() string {
	return "the lock lifts on its own on that date; nothing needs to be done before then"
}

// privacyNotPurchasedError is the API's 409 to enabling WHOIS privacy on a
// domain that has none purchased: "You may need to purchase WHOIS Privacy".
// UpdateDomain only turns on privacy the domain already has, and the CLI has no
// purchase command, so the error says where to buy it. It unwraps to the
// *api.APIError, so the exit code is unchanged.
type privacyNotPurchasedError struct {
	domain string
	err    error
}

func (e *privacyNotPurchasedError) Error() string {
	return fmt.Sprintf("WHOIS privacy is not purchased for %s, and this command can only turn on privacy the domain already has; "+
		"buy it in your name.com account at https://www.name.com/account, then run this again", e.domain)
}

func (e *privacyNotPurchasedError) Unwrap() error { return e.err }

func (e *privacyNotPurchasedError) UserHint() string {
	return fmt.Sprintf("run 'namecom open %s' to open the domain's page on name.com", e.domain)
}

// explainUpdateError restates the UpdateDomain refusals whose API wording is
// unhelpful, given the request that drew them. Any other error is returned
// unchanged.
func explainUpdateError(err error, req *coreapigo.UpdateDomainRequest) error {
	apiErr, ok := errors.AsType[*api.APIError](err)
	if !ok {
		return err
	}
	text := apiErr.Message + " " + apiErr.Details
	if apiErr.StatusCode == http.StatusConflict && req.PrivacyEnabled != nil && *req.PrivacyEnabled &&
		strings.Contains(strings.ToLower(text), "purchase") {
		return &privacyNotPurchasedError{domain: req.DomainName, err: err}
	}
	if req.Locked != nil {
		if _, until, found := strings.Cut(text, "can not be unlocked until "); found && strings.TrimSpace(until) != "" {
			return &transferLockError{domain: req.DomainName, until: strings.TrimSpace(until), locking: *req.Locked, err: err}
		}
	}
	return err
}

func runLock(cmd *cobra.Command, args []string) error {
	out := cmdutil.Out(cmd)
	enable, err := cmdutil.OnOffArg(args[0])
	if err != nil {
		return err
	}
	domainName, err := cmdutil.DomainArg(args, 1)
	if err != nil {
		return err
	}

	already, err := toggleAlreadySet(cmd, domainName, enable,
		func(d *coreapigo.DomainResponsePayload) bool { return d.Locked })
	if err != nil {
		return err
	}
	if already {
		out.Success(fmt.Sprintf("Transfer lock is already %s for %s; nothing to change", onOff(enable), domainName))
		return nil
	}

	req := &coreapigo.UpdateDomainRequest{DomainName: domainName, Locked: &enable}
	if sent, err := applyDomainToggle(cmd, req, ""); err != nil || !sent {
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
	enable, err := cmdutil.OnOffArg(args[0])
	if err != nil {
		return err
	}
	domainName, err := cmdutil.DomainArg(args, 1)
	if err != nil {
		return err
	}
	already, err := toggleAlreadySet(cmd, domainName, enable,
		func(d *coreapigo.DomainResponsePayload) bool { return d.AutorenewEnabled })
	if err != nil {
		return err
	}
	if already {
		out.Success(fmt.Sprintf("Auto-renewal is already %s for %s; nothing to change", onOff(enable), domainName))
		return nil
	}
	req := &coreapigo.UpdateDomainRequest{DomainName: domainName, AutorenewEnabled: &enable}
	if sent, err := applyDomainToggle(cmd, req, ""); err != nil || !sent {
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
	enable, err := cmdutil.OnOffArg(args[0])
	if err != nil {
		return err
	}
	domainName, err := cmdutil.DomainArg(args, 1)
	if err != nil {
		return err
	}

	already, err := toggleAlreadySet(cmd, domainName, enable,
		func(d *coreapigo.DomainResponsePayload) bool { return d.PrivacyEnabled })
	if err != nil {
		return err
	}
	if already {
		out.Success(fmt.Sprintf("WHOIS privacy is already %s for %s; nothing to change", onOff(enable), domainName))
		return nil
	}

	req := &coreapigo.UpdateDomainRequest{DomainName: domainName, PrivacyEnabled: &enable}
	// Confirm before enabling. It never charges: UpdateDomain turns on privacy
	// the domain already has, and fails with a 409 when none was purchased,
	// which explainUpdateError restates (#187). The prompt says so.
	prompt := ""
	if enable {
		prompt = privacyPrompt(domainName)
	}
	if sent, err := applyDomainToggle(cmd, req, prompt); err != nil || !sent {
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
  namecom domain set-ns example.com --ns ns1.example.com,ns2.example.com  # custom nameservers
  namecom domain set-ns example.com --ns ns1.name.com,ns2.name.com --yes  # no prompt, for scripts`,
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
	// DomainName is the path parameter and is not marshaled, so previewing
	// this value previews exactly the body sent.
	body := coreapigo.DomainsSetNameserversBody{DomainName: domain, Nameservers: ns}
	sent, err := cmdutil.RunWrite(cmd, cmdutil.Write[coreapigo.DomainsSetNameserversBody]{
		Method: "POST",
		Path:   fmt.Sprintf("/core/v1/domains/%s:setNameservers", domain),
		Body:   body,
		Prompt: setNSPrompt(body),
		Spin:   "Updating nameservers…",
	}, func(ctx context.Context, body coreapigo.DomainsSetNameserversBody) error {
		_, err := client.SDK().Domains.SetNameservers(ctx, &body)
		return err
	})
	if err != nil || !sent {
		return err
	}
	out.Success(fmt.Sprintf("Nameservers updated for %s", domain))
	out.Hint("DNS propagation typically takes a few minutes to a few hours")
	return nil
}

// setNSPrompt asks before replacing a domain's nameservers, naming the ones
// being sent: a typo here takes the whole domain offline.
func setNSPrompt(body coreapigo.DomainsSetNameserversBody) string {
	return fmt.Sprintf("Set nameservers for %s to %s? The domain stops resolving if these are wrong.",
		body.DomainName, strings.Join(body.Nameservers, ", "))
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
  namecom domain contacts set example.com --from-file contacts.json
  namecom domain contacts set example.com --from-file contacts.json --yes  # no prompt, for scripts`,
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
	if err := cmdutil.RequireField("the domain name", d.DomainName); err != nil {
		return err
	}

	// Quiet prints the registrant's email: the registrant is the owner of
	// record and the contact ICANN verification is sent to. Nothing is
	// printed when the response has none.
	if out.QuietMode {
		if d.Contacts != nil && d.Contacts.Registrant != nil && d.Contacts.Registrant.Email != nil {
			out.Quiet(*d.Contacts.Registrant.Email)
		}
		return nil
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
		if d.Contacts != nil {
			warnUnverifiedContacts(out, *d.Contacts)
		}
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

	// Preview the SDK body, not the file's contents: the request wraps them
	// as {"contacts": {...}}.
	body := coreapigo.DomainsSetContactsBody{DomainName: domain, Contacts: &contacts}
	sent, err := cmdutil.RunWrite(cmd, cmdutil.Write[coreapigo.DomainsSetContactsBody]{
		Method: "POST",
		Path:   fmt.Sprintf("/core/v1/domains/%s:setContacts", domain),
		Body:   body,
		Prompt: contactsSetPrompt(body),
	}, func(ctx context.Context, body coreapigo.DomainsSetContactsBody) error {
		_, err := client.SDK().Domains.SetContacts(ctx, &body)
		return err
	})
	if err != nil || !sent {
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

// contactsSetPrompt asks before replacing contacts, naming the roles the file
// sets. A registrant change gets the stronger warning: the SDK documents that
// it "may" trigger ICANN verification, and that a material registrant change
// is one cause of an ICANN-mandated transfer lock.
func contactsSetPrompt(body coreapigo.DomainsSetContactsBody) string {
	var roles []string
	registrant := false
	if c := body.Contacts; c != nil {
		if c.Registrant != nil {
			roles = append(roles, "registrant")
			registrant = true
		}
		if c.Admin != nil {
			roles = append(roles, "admin")
		}
		if c.Tech != nil {
			roles = append(roles, "tech")
		}
		if c.Billing != nil {
			roles = append(roles, "billing")
		}
	}
	if len(roles) == 0 {
		return fmt.Sprintf("Update contacts for %s? The file sets no contact role.", body.DomainName)
	}
	noun := "contact"
	if len(roles) > 1 {
		noun = "contacts"
	}
	list := roles[0]
	if n := len(roles); n > 1 {
		list = strings.Join(roles[:n-1], ", ") + " and " + roles[n-1]
	}
	msg := fmt.Sprintf("Replace the %s %s for %s?", list, noun, body.DomainName)
	if registrant {
		msg += " A new registrant may need ICANN email verification, and a material" +
			" registrant change can put a transfer lock on the domain."
	}
	return msg
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
	acq, err := acquisition(cmd, domain)
	if err != nil {
		return err
	}

	// Quiet prints the registration price as a bare number ("12.99"), the one
	// a script compares against before registering: the acquisition price
	// when there is one, since that is what `domain register` pays. Nothing
	// when the API quotes none.
	if out.QuietMode {
		price := pricing.PurchasePrice
		if acq != nil {
			price = acq.PurchasePrice
		}
		if price != nil {
			out.Quiet(strconv.FormatFloat(*price, 'f', 2, 64))
		}
		return nil
	}

	// JSON/YAML print the API's pricing as before, with the acquisition added
	// alongside when there is one, so a script reading purchasePrice is
	// unaffected.
	var doc any = pricing
	if acq != nil {
		pt, price := nonDefaultPurchaseType(acq)
		if doc, err = withPurchase(pricing, *pt, price); err != nil {
			return err
		}
	}
	switch out.Format {
	case output.FormatJSON:
		return out.JSON(doc)
	case output.FormatYAML:
		return out.YAML(doc)
	}

	fmtPrice := func(p *float64) string {
		if p == nil {
			return "N/A"
		}
		return fmt.Sprintf("$%.2f", *p)
	}
	register := fmtPrice(pricing.PurchasePrice)
	if acq != nil {
		// Worded by searchPriceLabel, as `domain check` and `search` show it.
		pt, _ := nonDefaultPurchaseType(acq)
		register = fmt.Sprintf("price unknown (%s)", *pt)
		if acq.PurchasePrice != nil {
			register = searchPriceLabel(acq)
		}
		out.Warn(fmt.Sprintf("%s is not a standard registration (purchase type %s): registering it costs %s, not %s",
			domain, *pt, register, fmtPrice(pricing.PurchasePrice)))
	}
	out.Table([]string{"TYPE", "PRICE"}, [][]string{
		{"Register", register},
		{"Renew", fmtPrice(pricing.RenewalPrice)},
		{"Transfer", fmtPrice(pricing.TransferPrice)},
		{"Premium", boolStr(pricing.Premium)},
	})
	return nil
}

// acquisition returns domain's availability result when registering it would
// be an aftermarket, expiring or backorder purchase rather than a plain
// registration, and nil otherwise.
//
// GetPricingForDomain cannot say: the SDK documents its purchasePrice as
// "Does not include aftermarket, expiring, or backorder acquisition prices".
// `domain register` checks availability and pays that price, so `domain
// pricing` showed $17.99 for a name register would buy at $8625 (#187).
func acquisition(cmd *cobra.Command, domain string) (*coreapigo.SearchResult, error) {
	res, err := cmdutil.APIClient(cmd).SDK().Domains.CheckAvailability(cmd.Context(),
		&coreapigo.AvailabilityRequest{DomainNames: []string{domain}})
	if err != nil {
		return nil, fmt.Errorf("checking availability: %w", api.FromSDKError(err))
	}
	for _, r := range cmdutil.NonNil(res.Results) {
		if r == nil || !r.Purchasable {
			continue
		}
		if pt, _ := nonDefaultPurchaseType(r); pt != nil {
			return r, nil
		}
	}
	return nil, nil
}

// withPurchase is pricing's JSON/YAML document with the acquisition added as
// purchaseType and purchaseTypePrice. PricingResponse marshals itself, so the
// fields are added to its encoding rather than by embedding it.
func withPurchase(pricing *coreapigo.PricingResponse, purchaseType string, price *float64) (map[string]any, error) {
	b, err := json.Marshal(pricing)
	if err != nil {
		return nil, err
	}
	doc := map[string]any{}
	if err := json.Unmarshal(b, &doc); err != nil {
		return nil, err
	}
	doc["purchaseType"] = purchaseType
	if price != nil {
		doc["purchaseTypePrice"] = *price
	}
	return doc, nil
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

	domain, err := cmdutil.DomainArg(args, 0)
	if err != nil {
		return err
	}

	// Only the flags that were passed are sent. UpdateDomain is a PATCH that
	// takes "one, or any combination of the parameters" and leaves the rest
	// alone, and since SDK v1.33.5 each field is a flat *bool with omitempty,
	// so nil means "not sent" while a pointer to false still serialises.
	//
	// This used to read the domain and restate all three fields. That breaks
	// during the 60-day transfer lock after registration or transfer, when the
	// API rejects any request containing `locked` — even an unchanged true —
	// so `--autorenew=false` alone failed with "Domain can not be unlocked
	// until …" (#116).
	req := &coreapigo.UpdateDomainRequest{DomainName: domain}
	for flag, field := range map[string]**bool{
		"autorenew": &req.AutorenewEnabled,
		"privacy":   &req.PrivacyEnabled,
		"lock":      &req.Locked,
	} {
		if cmd.Flags().Changed(flag) {
			v, _ := cmd.Flags().GetBool(flag)
			*field = &v
		}
	}
	if req.AutorenewEnabled == nil && req.PrivacyEnabled == nil && req.Locked == nil {
		return cmdutil.NewUsageError(errors.New("nothing to update — pass at least one of --autorenew, --privacy, --lock"))
	}

	enablingPrivacy := req.PrivacyEnabled != nil && *req.PrivacyEnabled
	unlocking := req.Locked != nil && !*req.Locked

	// The current state decides only whether to prompt and whether to warn, so
	// it is read only when one of those is in question. Without the read, a
	// script restating --privacy=true on a domain that already has it would
	// need --yes for a change that costs nothing.
	wasPrivate, wasLocked := false, true
	if enablingPrivacy || unlocking {
		current, err := client.SDK().Domains.GetDomain(cmd.Context(),
			&coreapigo.GetDomainRequest{DomainName: domain})
		if err != nil {
			return api.FromSDKError(err)
		}
		if current != nil {
			wasPrivate, wasLocked = current.PrivacyEnabled, current.Locked
		}
	}

	// `domain privacy on` confirms before enabling privacy, and this command
	// reaches the identical API call, so it asks too — two routes to the same
	// change should not differ in whether they pause. Only turning it ON is
	// gated, and neither route charges (#187).
	prompt := ""
	if enablingPrivacy && !wasPrivate {
		prompt = privacyPrompt(domain)
	}

	var updated *coreapigo.DomainResponsePayload
	sent, err := cmdutil.RunWrite(cmd, cmdutil.Write[*coreapigo.UpdateDomainRequest]{
		Method: "PATCH",
		Path:   fmt.Sprintf("/core/v1/domains/%s", domain),
		Body:   req,
		Prompt: prompt,
	}, func(ctx context.Context, req *coreapigo.UpdateDomainRequest) error {
		var err error
		updated, err = client.SDK().Domains.UpdateDomain(ctx, req)
		return api.FromSDKError(err)
	})
	if err != nil || !sent {
		return explainUpdateError(err, req)
	}
	// Removing the transfer lock has no cost but a real security consequence,
	// so warn for the same reason `domain lock off` does. Only after the API
	// accepted it: during the 60-day transfer lock it refuses (#167).
	if unlocking && wasLocked {
		out.WarnBox("Transfer lock removed — re-enable it after any transfer completes to protect against unauthorized outbound transfers")
	}
	if out.Quiet() {
		return nil
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

// privacyPrompt is the confirmation for turning WHOIS privacy on, shared by
// `domain privacy on` and `domain update --privacy`, which reach the same API
// call. It used to call that call "a billable action", but it never bills: it
// enables privacy already purchased, or fails (#187).
func privacyPrompt(domain string) string {
	return fmt.Sprintf("Enable WHOIS privacy for %s? This turns on privacy already purchased for the domain; it does not charge.", domain)
}

func init() {
	updateCmd.Flags().Bool("autorenew", false, "enable/disable auto-renewal")
	updateCmd.Flags().Bool("privacy", false, "enable/disable WHOIS privacy")
	updateCmd.Flags().Bool("lock", false, "enable/disable transfer lock")
}
