package domain

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	coreapigo "github.com/namedotcom/core-api-go"
	"github.com/patramsey/namecom-cli/cmd/cmdutil"
	"github.com/patramsey/namecom-cli/internal/api"
	"github.com/patramsey/namecom-cli/internal/output"
	"github.com/spf13/cobra"
)

// -- lock --

var lockCmd = &cobra.Command{
	Use:   "lock <on|off> <domain> [<domain>...]",
	Short: "Enable or disable transfer lock",
	Example: `  namecom domain lock on example.com
  namecom domain lock off example.com
  namecom domain lock example.com off   # the domain may come first
  namecom domain lock on example.com example.net
  namecom domain list -q | namecom domain lock on -   # every domain, read from stdin`,
	Args:              cmdutil.MinimumNArgs(2),
	RunE:              runLock,
	ValidArgsFunction: cmdutil.CompleteToggle,
}

// toggle is one of the domain toggles (lock, autorenew, privacy), as
// runToggle needs it.
type toggle struct {
	field string // its name in togglePrompt
	label string // the setting, as success lines name it: "Transfer lock"
	get   func(*coreapigo.DomainResponsePayload) bool
	set   func(req *coreapigo.UpdateDomainRequest, v *bool)
	// after, when set, runs once after every change was made: a warning or
	// hint about the new state.
	after func(out *output.Config, domains []string, enable bool)
}

// runToggle sets one field with UpdateDomain (PATCH) on each domain given.
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
// Several domains, or "-" for a list on stdin, are read first, skipping any
// already in the requested state, and then changed in order after a single
// confirmation that lists them all (#244). The first failure stops the rest;
// the domains already changed are reported before the error. In JSON and YAML
// the report is one document (output.Results), with "changed" false for a
// domain that was already in the requested state.
//
// DomainName is tagged `json:"-"`, so previewing the request previews the body
// alone.
func runToggle(cmd *cobra.Command, args []string, tg toggle) error {
	out := cmdutil.Out(cmd)
	client := cmdutil.APIClient(cmd)
	enable, domains, err := cmdutil.ToggleArgs(cmd, args)
	if err != nil {
		return err
	}

	res := out.Results()
	var pending []string
	for _, d := range domains {
		current, err := toggleCurrent(cmd, d)
		if err != nil {
			return err
		}
		if current != nil && tg.get(current) == enable {
			res.Add(output.ResultItem{Domain: d, Message: fmt.Sprintf("%s is already %s for %s; nothing to change", tg.label, onOff(enable), d)})
			continue
		}
		if tg.field == "lock" && !enable && current != nil {
			warnTransferLocked(out, d, current)
		}
		pending = append(pending, d)
	}
	if len(pending) == 0 {
		res.Print(fmt.Sprintf("%s is already %s for all %s; nothing to change", tg.label, onOff(enable), output.Plural(len(domains), "domain")))
		return nil
	}

	writes := make([]cmdutil.Write[*coreapigo.UpdateDomainRequest], len(pending))
	for i, d := range pending {
		req := &coreapigo.UpdateDomainRequest{DomainName: d}
		tg.set(req, &enable)
		writes[i] = cmdutil.Write[*coreapigo.UpdateDomainRequest]{
			Method: "PATCH",
			Path:   fmt.Sprintf("/core/v1/domains/%s", d),
			Body:   req,
		}
	}
	done, err := cmdutil.RunWrites(cmd, togglePrompts(pending, tg.field, enable), writes,
		func(ctx context.Context, req *coreapigo.UpdateDomainRequest) error {
			_, err := client.SDK().Domains.UpdateDomain(ctx, req)
			return api.FromSDKError(err)
		})
	// A dry run's document is the preview; the domains it would leave alone
	// were reported above in table mode only.
	if cmdutil.IsDryRun(cmd) {
		return err
	}
	verb := "disabled"
	if enable {
		verb = "enabled"
	}
	for _, d := range pending[:done] {
		res.Add(output.ResultItem{Domain: d, Changed: true, Message: fmt.Sprintf("%s %s for %s", tg.label, verb, d)})
	}
	summary := fmt.Sprintf("%s %s for %s", tg.label, verb, output.Plural(done, "domain"))
	if already := len(domains) - len(pending); already > 0 {
		summary += fmt.Sprintf("; already %s for %d", onOff(enable), already)
	}
	res.Print(summary)
	if err != nil {
		if done < len(writes) {
			err = explainUpdateError(err, writes[done].Body)
		}
		if done > 0 {
			err = fmt.Errorf("%w — stopped after changing %d of %d domains", err, done, len(writes))
		}
		return err
	}
	if done > 0 && tg.after != nil {
		tg.after(out, pending, enable)
	}
	return nil
}

// toggleCurrent reads the domain, so a toggle can leave alone one already in
// the requested state.
//
// The PATCH is not idempotent in practice: during the 60-day transfer lock
// after a registration or transfer the API refuses any body carrying `locked`,
// so `lock on` for a domain that is already locked failed with "Domain can not
// be unlocked until …" (#187). One GET avoids sending a change that is not a
// change. It runs under --dry-run too, which then reports the same outcome
// instead of previewing a request that would not be made.
//
// A response with no domain object is not evidence of anything, so it is nil
// and the PATCH goes ahead as before.
func toggleCurrent(cmd *cobra.Command, domainName string) (*coreapigo.DomainResponsePayload, error) {
	d, err := cmdutil.APIClient(cmd).SDK().Domains.GetDomain(cmd.Context(),
		&coreapigo.GetDomainRequest{DomainName: domainName})
	if err != nil {
		return nil, domainError(err, domainName)
	}
	return d, nil
}

// warnTransferLocked warns, before an unlock is previewed or confirmed, that
// the domain d is inside its 60-day transfer lock and the registry will refuse
// it. The GET that read d was made anyway; the date was in it, unused (#287).
func warnTransferLocked(out *output.Config, domain string, d *coreapigo.DomainResponsePayload) {
	if t := d.TransferLockExpiresAt; t != nil && t.After(time.Now()) {
		out.Warn(fmt.Sprintf("%s is in its 60-day transfer lock until %s — the registry will refuse to unlock it before then",
			domain, lockDate(t.Format(time.RFC3339))))
	}
}

// domainError names the domain when fetching it found nothing: the API's
// own "Not Found" does not say what was missing (#234).
func domainError(err error, domainName string) error {
	if cmdutil.IsNotFound(err) {
		return cmdutil.NotFound(err, fmt.Sprintf("domain %q not found — run 'namecom domain list' to see your domains", domainName))
	}
	return api.FromSDKError(err)
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
// lock lifts by itself. The date is shown by lockDate. It unwraps to
// the *api.APIError, so the exit code is unchanged.
type transferLockError struct {
	domain, until string
	locking       bool
	err           error
}

func (e *transferLockError) Error() string {
	until := lockDate(e.until)
	if e.locking {
		return fmt.Sprintf("%s is already transfer-locked: it is in the 60-day lock that follows a registration or transfer, "+
			"and the API refuses any change to the lock, even to restate it, until %s", e.domain, until)
	}
	return fmt.Sprintf("%s cannot be unlocked until %s: it is in the 60-day transfer lock that follows a registration or transfer",
		e.domain, until)
}

// lockDate renders the API's lock-expiry timestamp as a date with a relative
// time, "2026-11-28 (in 2 months)", the way every other date is shown. The
// raw value read "until 2026-11-28T06:37:39Z" (#238). One that does not
// parse is shown as the API sent it.
func lockDate(s string) string {
	for _, layout := range []string{time.RFC3339, "2006-01-02 15:04:05", "2006-01-02T15:04:05", "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.Format("2006-01-02") + " (" + output.Relative(t) + ")"
		}
	}
	return s
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
	return runToggle(cmd, args, toggle{
		field: "lock",
		label: "Transfer lock",
		get:   func(d *coreapigo.DomainResponsePayload) bool { return d.Locked },
		set:   func(req *coreapigo.UpdateDomainRequest, v *bool) { req.Locked = v },
		after: func(out *output.Config, _ []string, enable bool) {
			if !enable {
				out.WarnBox("Lock removed — re-enable after transfers are complete to protect against unauthorized outbound transfers")
			}
		},
	})
}

// -- autorenew --

var autorenewCmd = &cobra.Command{
	Use:   "autorenew <on|off> <domain> [<domain>...]",
	Short: "Enable or disable automatic renewal",
	Example: `  namecom domain autorenew on example.com
  namecom domain autorenew off example.com
  namecom domain autorenew example.com off   # the domain may come first
  namecom domain autorenew on - < domains.txt   # one domain per line`,
	Args:              cmdutil.MinimumNArgs(2),
	RunE:              runAutorenew,
	ValidArgsFunction: cmdutil.CompleteToggle,
}

func runAutorenew(cmd *cobra.Command, args []string) error {
	return runToggle(cmd, args, toggle{
		field: "autorenew",
		label: "Auto-renewal",
		get:   func(d *coreapigo.DomainResponsePayload) bool { return d.AutorenewEnabled },
		set:   func(req *coreapigo.UpdateDomainRequest, v *bool) { req.AutorenewEnabled = v },
		after: func(out *output.Config, domains []string, enable bool) {
			if enable {
				return
			}
			target := "<domain>"
			if len(domains) == 1 {
				target = domains[0]
			}
			out.Hint(fmt.Sprintf("Remember to renew manually before expiry — run 'namecom domain get %s' to check the expiry date", target))
		},
	})
}

// -- privacy --

var privacyCmd = &cobra.Command{
	Use:   "privacy <on|off> <domain> [<domain>...]",
	Short: "Enable or disable WHOIS privacy",
	Example: `  namecom domain privacy on example.com
  namecom domain privacy off example.com
  namecom domain privacy example.com off   # the domain may come first
  namecom domain privacy on example.com example.net`,
	Args:              cmdutil.MinimumNArgs(2),
	RunE:              runPrivacy,
	ValidArgsFunction: cmdutil.CompleteToggle,
}

func runPrivacy(cmd *cobra.Command, args []string) error {
	return runToggle(cmd, args, toggle{
		field: "privacy",
		label: "WHOIS privacy",
		get:   func(d *coreapigo.DomainResponsePayload) bool { return d.PrivacyEnabled },
		set:   func(req *coreapigo.UpdateDomainRequest, v *bool) { req.PrivacyEnabled = v },
	})
}

// -- set-ns --

var setNSCmd = &cobra.Command{
	Use:   "set-ns <domain> --ns ns1.example.com,ns2.example.com",
	Short: "Set nameservers for a domain",
	Long: `Replace a domain's nameservers. DNS records hosted at name.com stop
answering for the domain once it points at other nameservers.`,
	Example: `  namecom domain set-ns example.com --ns ns1.name.com,ns2.name.com
  namecom domain set-ns example.com --ns ns1.example.com,ns2.example.com  # custom nameservers

  # In a script, skip the confirmation:
  namecom domain set-ns example.com --ns ns1.name.com,ns2.name.com --yes`,
	Args:              setNSArgs,
	RunE:              runSetNS,
	ValidArgsFunction: cmdutil.CompleteDomains,
}

var setNSList string

func init() {
	setNSCmd.Flags().StringVar(&setNSList, "ns", "", "comma-separated nameservers (required)")
	_ = setNSCmd.MarkFlagRequired("ns")
	_ = setNSCmd.Flags().SetAnnotation("ns", cmdutil.SuggestFlagFor, []string{"nameservers", "nameserver"})
}

// setNSArgs is ExactArgs(1), except that nameservers passed as arguments get
// the command rewritten with --ns as the hint (#234).
func setNSArgs(cmd *cobra.Command, args []string) error {
	if len(args) > 1 && setNSList == "" {
		return cmdutil.NewUsageErrorHint(fmt.Errorf("too many arguments — set-ns takes the nameservers in --ns, not as arguments"),
			fmt.Sprintf("run '%s %s --ns %s'", cmd.CommandPath(), args[0], strings.Join(args[1:], ",")))
	}
	return cmdutil.ExactArgs(1)(cmd, args)
}

func runSetNS(cmd *cobra.Command, args []string) error {
	out := cmdutil.Out(cmd)
	client := cmdutil.APIClient(cmd)
	domain, err := cmdutil.DomainArg(args, 0)
	if err != nil {
		return err
	}
	// A trailing dot and any case are accepted and dropped, as vanity-ns
	// accepts them: `NS1.Example.org.` was refused here and taken there
	// (#292). A nameserver listed twice is refused rather than sent; it is
	// more likely a typo for a second one than meant.
	ns := strings.Split(setNSList, ",")
	seen := make(map[string]bool, len(ns))
	for i := range ns {
		ns[i] = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(ns[i]), "."))
		if err := cmdutil.ValidNameserver(ns[i], i); err != nil {
			return err
		}
		if seen[ns[i]] {
			return cmdutil.NewUsageError(fmt.Errorf("nameserver %s is listed twice in --ns", ns[i]))
		}
		seen[ns[i]] = true
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
	out.Success(fmt.Sprintf("Set nameservers for %s: %s", domain, strings.Join(ns, ", ")))
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
	Short: "View and update registrant, admin, tech, and billing contacts",
	Long: `View and update a domain's registrant, admin, tech, and billing contacts.

See also: 'namecom contact' to resend or check the ICANN verification email a
new or changed contact is sent.`,
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
	Use:   "set <domain> --contacts-file contacts.json",
	Short: "Set contact information for a domain",
	Long: `Replace a domain's contacts with those in a JSON file, in the shape
'domain contacts get -o json' writes. A changed registrant may be sent an
ICANN verification email, and may start a transfer lock.`,
	Example: `  namecom domain contacts get example.com -o json > contacts.json
  # edit contacts.json, then:
  namecom domain contacts set example.com --contacts-file contacts.json

  # In a script, skip the confirmation:
  namecom domain contacts set example.com --contacts-file contacts.json --yes`,
	Args:              cmdutil.ExactArgs(1),
	RunE:              runContactsSet,
	ValidArgsFunction: cmdutil.CompleteDomains,
}

var contactsFile string

func init() {
	contactsSetCmd.Flags().StringVar(&contactsFile, "contacts-file", "", "JSON file with contact data, as 'contacts get -o json' writes it (required)")
	// --from-file was this command's name for the same file that register and
	// transfer call --contacts-file (#236). It still works, hidden, with a
	// deprecation notice on stderr. Neither is marked required, since cobra
	// would then demand one name specifically; runContactsSet checks instead.
	contactsSetCmd.Flags().StringVar(&contactsFile, "from-file", "", "JSON file with contact data")
	_ = contactsSetCmd.Flags().MarkDeprecated("from-file", "use --contacts-file")
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
		return domainError(err, domain)
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
		err = out.JSON(d.Contacts)
	case output.FormatYAML:
		err = out.YAML(d.Contacts)
	default:
		// A block per role in a table; one table, a row per role, in TSV.
		// Both printed the JSON document, whatever -o said (#289).
		var c coreapigo.Contacts
		if d.Contacts != nil {
			c = *d.Contacts
		}
		roles := contactRoles(c)
		blocks := make([][][]string, len(roles))
		for i, r := range roles {
			blocks[i] = contactRows(out, r.name, r.contact)
		}
		out.KVTables(make([]string, len(roles)), blocks)
	}
	// In every format: in JSON and YAML the warning joins the "warnings" on
	// stderr, where it was left out.
	if err == nil && d.Contacts != nil {
		warnUnverifiedContacts(out, *d.Contacts)
	}
	return err
}

// contactRole is one of a domain's contacts, as contacts get shows it.
type contactRole struct {
	name    string
	contact *coreapigo.Contact
}

// contactRoles returns c's four roles, registrant first, as the owner of
// record. A role the response lacks is there, empty, so the rows are the same
// for every domain. The registrant is the SDK's RegistrantContact, field for
// field a Contact; it is copied into one so the four print alike.
func contactRoles(c coreapigo.Contacts) []contactRole {
	var registrant *coreapigo.Contact
	if r := c.Registrant; r != nil {
		registrant = &coreapigo.Contact{
			FirstName: r.FirstName, LastName: r.LastName, CompanyName: r.CompanyName,
			Address1: r.Address1, Address2: r.Address2, City: r.City, State: r.State,
			Zip: r.Zip, Country: r.Country, Email: r.Email, Phone: r.Phone, Fax: r.Fax,
			IsVerified: r.IsVerified, VerificationID: r.VerificationID,
		}
	}
	return []contactRole{{"registrant", registrant}, {"admin", c.Admin}, {"tech", c.Tech}, {"billing", c.Billing}}
}

// contactRows is one role's detail view: every field, empty when unset, so
// that in TSV, where the roles are one table, each has the same columns.
func contactRows(out *output.Config, name string, c *coreapigo.Contact) [][]string {
	if c == nil {
		c = &coreapigo.Contact{}
	}
	s := func(p *string) string {
		if p == nil {
			return ""
		}
		return *p
	}
	verified, verificationID := "", ""
	if c.IsVerified != nil {
		verified = out.BoolBadge(*c.IsVerified)
	}
	if c.VerificationID != nil {
		verificationID = strconv.FormatInt(*c.VerificationID, 10)
	}
	return [][]string{
		{"Role", name},
		{"First name", s(c.FirstName)},
		{"Last name", s(c.LastName)},
		{"Company", s(c.CompanyName)},
		{"Address 1", s(c.Address1)},
		{"Address 2", s(c.Address2)},
		{"City", s(c.City)},
		{"State", s(c.State)},
		{"Zip", s(c.Zip)},
		{"Country", s(c.Country)},
		{"Email", s(c.Email)},
		{"Phone", s(c.Phone)},
		{"Fax", s(c.Fax)},
		{"Verified", verified},
		{"Verification ID", verificationID},
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
		fmt.Sprintf("Unverified %s: %s", output.PluralNoun(len(pending), "contact"), strings.Join(pending, ", ")),
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

	if contactsFile == "" {
		return cmdutil.RequiredFlags(false, "contacts-file")
	}
	// A bad file is a usage error (exit 2), like the other contacts files.
	contacts, err := cmdutil.ReadContactsFile(contactsFile)
	if err != nil {
		return err
	}

	// Preview the SDK body, not the file's contents: the request wraps them
	// as {"contacts": {...}}.
	body := coreapigo.DomainsSetContactsBody{DomainName: domain, Contacts: contacts}
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
	out.Success(contactsSetLine(body))
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

// updateSummary lists the settings a domain update sent, in the order the
// flags are documented: "auto-renew off, lock on". The success line said only
// "Updated example.com" (#238).
func updateSummary(req *coreapigo.UpdateDomainRequest) string {
	var parts []string
	for _, f := range []struct {
		name string
		v    *bool
	}{
		{"auto-renew", req.AutorenewEnabled},
		{"privacy", req.PrivacyEnabled},
		{"lock", req.Locked},
	} {
		if f.v != nil {
			parts = append(parts, f.name+" "+onOff(*f.v))
		}
	}
	return strings.Join(parts, ", ")
}

// contactsRoles lists the contact roles body sets, in WHOIS order.
func contactsRoles(body coreapigo.DomainsSetContactsBody) []string {
	var roles []string
	if c := body.Contacts; c != nil {
		for _, r := range []struct {
			name string
			set  bool
		}{
			{"registrant", c.Registrant != nil},
			{"admin", c.Admin != nil},
			{"tech", c.Tech != nil},
			{"billing", c.Billing != nil},
		} {
			if r.set {
				roles = append(roles, r.name)
			}
		}
	}
	return roles
}

// contactsSetLine says which contacts were replaced: "Replaced the registrant
// and admin contacts for example.com". It said "Contacts updated" (#238).
func contactsSetLine(body coreapigo.DomainsSetContactsBody) string {
	roles := contactsRoles(body)
	if len(roles) == 0 {
		return fmt.Sprintf("Contacts updated for %s", body.DomainName)
	}
	list := roles[0]
	if n := len(roles); n > 1 {
		list = strings.Join(roles[:n-1], ", ") + " and " + roles[n-1]
	}
	return fmt.Sprintf("Replaced the %s %s for %s", list, output.PluralNoun(len(roles), "contact"), body.DomainName)
}

// contactsSetPrompt asks before replacing contacts, naming the roles the file
// sets. A registrant change gets the stronger warning: the SDK documents that
// it "may" trigger ICANN verification, and that a material registrant change
// is one cause of an ICANN-mandated transfer lock.
func contactsSetPrompt(body coreapigo.DomainsSetContactsBody) string {
	roles := contactsRoles(body)
	registrant := len(roles) > 0 && roles[0] == "registrant"
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
		return output.Money(*p)
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
	// The table had no heading and listed "Premium: no" as a row of the PRICE
	// column (#235). The heading names the domain and the term the prices
	// cover — GetPricingForDomain quotes the TLD's minimum term, which the
	// SDK documents as usually 1 year (2 for .ai) — and says premium there.
	title := domain
	if pricing.Premium {
		title += " (premium)"
	}
	out.Title(title + " — per term (1 year for most TLDs)")
	out.Table([]string{"TYPE", "PRICE"}, [][]string{
		{"Register", register},
		{"Renew", fmtPrice(pricing.RenewalPrice)},
		{"Transfer", fmtPrice(pricing.TransferPrice)},
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
  namecom domain update example.com --autorenew=false   # =false: "--autorenew false" is not the same
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

	// Each flag asks what its toggle command asks (#227): two routes to the
	// same change should not differ in whether they pause. The current state
	// decides whether a flag is a change at all, so it is read when a flag
	// that prompts was passed, or --lock: during the 60-day transfer lock the
	// API refuses any body carrying `locked`, even an unchanged true, and
	// takes the rest of the PATCH down with it (#287). Without the read, a
	// script restating --autorenew=true would need --yes for a change that is
	// not one. With no domain in the response, every flag is treated as a
	// change.
	risky := req.Locked != nil || req.AutorenewEnabled != nil || (req.PrivacyEnabled != nil && !*req.PrivacyEnabled)
	var current *coreapigo.DomainResponsePayload
	if risky {
		current, err = client.SDK().Domains.GetDomain(cmd.Context(),
			&coreapigo.GetDomainRequest{DomainName: domain})
		if err != nil {
			return domainError(err, domain)
		}
	}
	// A field already in the requested state is left out of the body, as the
	// toggles leave out a domain already set (#287), and named in the result.
	// When every field is, nothing is sent.
	var prompts, unchanged []string
	for _, f := range []struct {
		name  string
		label string
		want  **bool
		was   func(*coreapigo.DomainResponsePayload) bool
	}{
		{"lock", "lock", &req.Locked, func(d *coreapigo.DomainResponsePayload) bool { return d.Locked }},
		{"privacy", "privacy", &req.PrivacyEnabled, func(d *coreapigo.DomainResponsePayload) bool { return d.PrivacyEnabled }},
		{"autorenew", "auto-renew", &req.AutorenewEnabled, func(d *coreapigo.DomainResponsePayload) bool { return d.AutorenewEnabled }},
	} {
		want := *f.want
		if want == nil {
			continue
		}
		if current != nil && f.was(current) == *want {
			unchanged = append(unchanged, f.label+" "+onOff(*want))
			*f.want = nil
			continue
		}
		if p := togglePrompt(domain, f.name, *want); p != "" {
			prompts = append(prompts, p)
		}
	}
	if req.AutorenewEnabled == nil && req.PrivacyEnabled == nil && req.Locked == nil {
		out.Unchanged(fmt.Sprintf("%s already has %s; nothing to change", domain, strings.Join(unchanged, ", ")))
		return nil
	}
	if len(unchanged) > 0 {
		out.Note(fmt.Sprintf("%s already has %s; not sending it", domain, strings.Join(unchanged, ", ")))
	}
	unlocking := req.Locked != nil && !*req.Locked
	if unlocking && current != nil {
		warnTransferLocked(out, domain, current)
	}
	prompt := strings.Join(prompts, " ")
	wasLocked := current == nil || current.Locked

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
		out.Success(fmt.Sprintf("Updated %s: %s", domain, updateSummary(req)))
	}
	return nil
}

// togglePrompt is the confirmation for setting one of the UpdateDomain fields
// ("lock", "privacy" or "autorenew") to on, shared by the toggle commands and
// `domain update`. It is "" for the changes that carry no risk worth a pause.
//
// The prompts follow the risk, not the habit (#227). Removing the lock, making
// WHOIS data public and letting a domain lapse each used to run unprompted,
// while turning privacy on, which only enables privacy already purchased and
// never charges (#187), asked first. Turning auto-renewal on asks because it
// commits the account to future charges.
func togglePrompt(domain, field string, on bool) string {
	switch {
	case field == "lock" && !on:
		return fmt.Sprintf("Remove the transfer lock on %s? Anyone with its auth code can then transfer it away.", domain)
	case field == "privacy" && !on:
		return fmt.Sprintf("Turn off WHOIS privacy for %s? Its registrant contact details may then be shown publicly in WHOIS.", domain)
	case field == "autorenew" && !on:
		return fmt.Sprintf("Turn off auto-renewal for %s? It expires on its expiry date unless renewed manually.", domain)
	case field == "autorenew" && on:
		return fmt.Sprintf("Turn on auto-renewal for %s? name.com will renew it before each expiry and charge the renewal price to the account.", domain)
	}
	return ""
}

// togglePrompts is togglePrompt for the domains one toggle command changes:
// for one, its question exactly; for several, the same question asked once,
// followed by every domain it approves, one per line. "" when the change is
// one that does not confirm.
func togglePrompts(domains []string, field string, on bool) string {
	if len(domains) == 1 || togglePrompt(domains[0], field, on) == "" {
		return togglePrompt(domains[0], field, on)
	}
	these := fmt.Sprintf("these %d domains", len(domains))
	var q string
	switch {
	case field == "lock" && !on:
		q = fmt.Sprintf("Remove the transfer lock on %s? Anyone with a domain's auth code can then transfer it away.", these)
	case field == "privacy" && !on:
		q = fmt.Sprintf("Turn off WHOIS privacy for %s? Their registrant contact details may then be shown publicly in WHOIS.", these)
	case field == "autorenew" && !on:
		q = fmt.Sprintf("Turn off auto-renewal for %s? Each expires on its expiry date unless renewed manually.", these)
	case field == "autorenew" && on:
		q = fmt.Sprintf("Turn on auto-renewal for %s? name.com will renew each before its expiry and charge the renewal price to the account.", these)
	}
	return q + "\n  " + strings.Join(domains, "\n  ")
}

func init() {
	updateCmd.Flags().Bool("autorenew", false, "enable/disable auto-renewal")
	updateCmd.Flags().Bool("privacy", false, "enable/disable WHOIS privacy")
	updateCmd.Flags().Bool("lock", false, "enable/disable transfer lock")
	cmdutil.MarkBoolValue(updateCmd.Flags(), "autorenew", "privacy", "lock")
}
