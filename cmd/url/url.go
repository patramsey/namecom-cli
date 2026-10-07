// Package url implements the `namecom url` command group.
package url

import (
	"context"
	"errors"
	"fmt"
	neturl "net/url"
	"strconv"
	"strings"

	"github.com/charmbracelet/huh"
	coreapigo "github.com/namedotcom/core-api-go"
	"github.com/patramsey/namecom-cli/cmd/cmdutil"
	"github.com/patramsey/namecom-cli/internal/api"
	"github.com/patramsey/namecom-cli/internal/output"
	"github.com/spf13/cobra"
)

// Cmd is the `namecom url` parent command.
var Cmd = &cobra.Command{
	Use:   "url",
	Short: "Create and manage URL forwarding for your domains",
}

var (
	createHost       string
	createForwardsTo string
	createType       string
	createTitle      string
	createMeta       string

	updateForwardsTo string
	updateType       string
	updateTitle      string
	updateMeta       string

	listAll bool
)

// urlTypes describes the --type values. The API's names are kept as the
// values, so the help says that "redirect" is the 301 (#237). Elsewhere the
// feature is "URL forwarding", name.com's own name for it; "redirect" only
// describes what a 301 or 302 forwarding does.
const urlTypes = "redirect (301, permanent), 302 (temporary), masked (destination shown in a frame)"

var listCmd = &cobra.Command{
	Use:     "list <domain>",
	Aliases: []string{"ls"},
	Short:   "List URL forwarding entries for a domain",
	Example: `  namecom url list example.com
  namecom url list example.com --all`,
	Args:              cmdutil.ExactArgs(1),
	RunE:              runList,
	ValidArgsFunction: cmdutil.CompleteDomains,
}

var getCmd = &cobra.Command{
	Use:               "get <domain> <id>",
	Short:             "Get a URL forwarding entry by ID",
	Example:           `  namecom url get example.com 12345`,
	Args:              cmdutil.ExactArgs(2),
	RunE:              runGet,
	ValidArgsFunction: cmdutil.CompleteDomains,
}

var createCmd = &cobra.Command{
	Use:     "create <domain>",
	Aliases: []string{"add"},
	Short:   "Create a URL forwarding entry",
	Long: `Send visitors to a domain, or to one host on it, to another URL.

The default type, redirect, is a 301 (permanent) redirect; 302 is a temporary
one. masked keeps the domain in the address bar and shows the destination in
a frame, with --title and --meta for the page around it.

Forwarding a subdomain replaces that host's A records, and deleting the
forwarding removes them. Forwarding the apex adds an A record for it, which
deleting the forwarding leaves in place: remove it with 'namecom dns delete'.

--title and --meta apply only to masked forwarding; with another type they
are a usage error.`,
	Example: `  namecom url create example.com --to https://new-site.com
  namecom url create example.com --host www --to https://new-site.com --type 302
  namecom url create example.com --to https://new-site.com --type masked --title "My Site"`,
	Args:              cmdutil.ExactArgs(1),
	RunE:              runCreate,
	ValidArgsFunction: cmdutil.CompleteDomains,
}

var updateCmd = &cobra.Command{
	Use:   "update <domain> <id>",
	Short: "Update a URL forwarding entry",
	Long: `Change a forwarding entry's destination, type, title or meta tags. Only the flags
passed change; the rest are kept. The host is never changed: to move a
forwarding entry to another host, delete it and create a new one.`,
	Example: `  namecom url update example.com 12345 --to https://other-site.com
  namecom url update example.com 12345 --type 302`,
	Args:              cmdutil.ExactArgs(2),
	RunE:              runUpdate,
	ValidArgsFunction: cmdutil.CompleteDomains,
}

var deleteCmd = &cobra.Command{
	Use:               "delete <domain> <id>",
	Aliases:           []string{"rm"},
	Short:             "Delete a URL forwarding entry",
	Example:           `  namecom url delete example.com 12345`,
	Args:              cmdutil.ExactArgs(2),
	RunE:              runDelete,
	ValidArgsFunction: cmdutil.CompleteDomains,
}

func init() {
	cmdutil.AddPageFlags(listCmd, &listAll, &listPage, &listLimit, "forwarding entries")

	createCmd.Flags().StringVar(&createHost, "host", "@", "host to forward: www, www.example.com, or @ for the apex; forwarding a subdomain replaces its A records")
	createCmd.Flags().StringVar(&createForwardsTo, "to", "", "destination URL "+cmdutil.PromptedRequired)
	createCmd.Flags().StringVar(&createType, "type", "redirect", "forwarding type: "+urlTypes)
	createCmd.Flags().StringVar(&createTitle, "title", "", "page title (masked only)")
	createCmd.Flags().StringVar(&createMeta, "meta", "", "meta tags (masked only)")

	updateCmd.Flags().StringVar(&updateForwardsTo, "to", "", "new destination URL")
	// No default: an unset --type keeps the forwarding's current type, and a
	// default of "redirect" in --help read as though a masked forwarding would
	// be reset to a redirect.
	updateCmd.Flags().StringVar(&updateType, "type", "", "forwarding type: "+urlTypes+" (default: keep the current type)")
	updateCmd.Flags().StringVar(&updateTitle, "title", "", "page title (masked only)")
	updateCmd.Flags().StringVar(&updateMeta, "meta", "", "meta tags (masked only)")
	cmdutil.CompleteFlagValues(createCmd, "type", cmdutil.URLForwardingTypes)
	cmdutil.CompleteFlagValues(updateCmd, "type", cmdutil.URLForwardingTypes)

	cmdutil.GroupCmd(Cmd)
	cmdutil.MarkWrite(createCmd, updateCmd, deleteCmd)
	Cmd.AddCommand(listCmd, getCmd, createCmd, updateCmd, deleteCmd)
}

var listPage, listLimit int

func runList(cmd *cobra.Command, args []string) error {
	out := cmdutil.Out(cmd)
	client := cmdutil.APIClient(cmd)
	domain, err := cmdutil.DomainArg(args, 0)
	if err != nil {
		return err
	}

	paging, err := cmdutil.ListPaging(cmd, listAll, listPage, listLimit)
	if err != nil {
		return err
	}

	spin := out.StartSpinner("Fetching URL forwardings…")
	page := listPage
	var all []*coreapigo.URLForwardingResponse
	var hasMore bool
	var nextPage int
	var lastResult *coreapigo.ListURLForwardingsResponse
	for {
		result, err := client.SDK().URLForwardings.ListURLForwardingsByDomain(cmd.Context(),
			&coreapigo.ListURLForwardingsByDomainRequest{DomainName: domain, Page: &page, PerPage: paging.PerPage})
		if page == listPage && cmdutil.PageOutOfRange(page, err) {
			lastResult = &coreapigo.ListURLForwardingsResponse{}
			break
		}
		if err != nil {
			spin.Stop()
			// The API's own "Domain not found." does not name it (#291).
			if cmdutil.IsNotFound(err) {
				return cmdutil.DomainNotFound(err, domain)
			}
			return api.FromSDKError(err)
		}
		// The reply carries no totalCount, so only lastPage can say.
		if page == listPage && cmdutil.PastLastPage(page, paging.PerPage, len(result.URLForwarding), 0, result.LastPage) {
			lastResult = &coreapigo.ListURLForwardingsResponse{}
			break
		}
		all = append(all, cmdutil.NonNil(result.URLForwarding)...)
		lastResult = result
		next, ok := cmdutil.NextPage(page, result.NextPage, result.LastPage)
		if !ok {
			break
		}
		// cmdutil.ListPaging: --all, or --quiet without --page or --limit.
		if !paging.All {
			hasMore, nextPage = true, next
			break
		}
		page = next
		spin.Update(fmt.Sprintf("Fetching URL forwardings… (page %d, %d so far)", page, len(all)))
	}
	spin.Stop()

	if out.QuietMode {
		ids := make([]string, 0, len(all))
		for _, u := range all {
			if u.ID != nil {
				ids = append(ids, strconv.Itoa(*u.ID))
			}
		}
		out.PrintQuiet(ids)
		if hasMore {
			cmdutil.QuietMorePages(out, nextPage)
		}
		return nil
	}

	switch out.Format {
	case output.FormatJSON:
		var np *int32
		if hasMore {
			np = cmdutil.Int32Page(lastResult.NextPage)
		}
		return out.JSONList(all, np, 0)
	case output.FormatYAML:
		var np *int32
		if hasMore {
			np = cmdutil.Int32Page(lastResult.NextPage)
		}
		return out.YAMLList(all, np, 0)
	default:
		headers := []string{"ID", "HOST", "FORWARDS TO", "TYPE"}
		if len(all) == 0 {
			cmdutil.EmptyPage(out, listPage, headers, "URL forwarding", fmt.Sprintf("Run 'namecom url create %s --to https://example.com' to add one", domain))
			return nil
		}
		out.Table(
			headers,
			urlRows(all),
		)
		if hasMore {
			out.Count(len(all), "URL forwarding", cmdutil.MorePages(nextPage))
		} else {
			out.Count(len(all), "URL forwarding")
		}
	}
	return nil
}

func runGet(cmd *cobra.Command, args []string) error {
	out := cmdutil.Out(cmd)
	client := cmdutil.APIClient(cmd)

	id, err := parseID(args[1])
	if err != nil {
		return err
	}

	domain, err := cmdutil.DomainArg(args, 0)
	if err != nil {
		return err
	}
	stop := out.Spin("Fetching URL forwarding…")
	entry, err := client.SDK().URLForwardings.GetURLForwardingByID(cmd.Context(),
		&coreapigo.GetURLForwardingByIDRequest{DomainName: domain, ID: id})
	stop()
	if cmdutil.IsNotFound(err) {
		return forwardingNotFound(err, id, domain)
	}
	if err != nil {
		return err
	}
	if err := cmdutil.RequireField("the forwarding's ID", entry.ID); err != nil {
		return err
	}

	// --quiet prints the identifying value only, matching list commands.
	if out.QuietMode {
		out.PrintQuiet([]string{strconv.Itoa(*entry.ID)})
		return nil
	}

	switch out.Format {
	case output.FormatJSON:
		return out.JSON(entry)
	case output.FormatYAML:
		return out.YAML(entry)
	default:
		out.Table(
			[]string{"ID", "HOST", "FORWARDS TO", "TYPE"},
			urlRows([]*coreapigo.URLForwardingResponse{entry}),
		)
	}
	return nil
}

func runCreate(cmd *cobra.Command, args []string) error {
	out := cmdutil.Out(cmd)
	client := cmdutil.APIClient(cmd)
	domain, err := cmdutil.DomainArg(args, 0)
	if err != nil {
		return err
	}

	// The API treats host "" as distinct from "@": a forwarding on "" replaces
	// every apex A record and adds a "*" wildcard, and deleting it removes
	// every apex A record. Refuse it the way `dns create` does, before the
	// form asks for anything else. A fully qualified host is made relative,
	// as `dns create` makes it: sent as typed, www.example.com became a
	// forwarding for www.example.com.example.com.
	host, err := cmdutil.ZoneHost(createHost, domain)
	if err != nil {
		return err
	}

	if createForwardsTo == "" {
		if !output.IsInteractive() {
			return cmdutil.RequiredFlags(true, "to")
		}
		typeOptions := []huh.Option[string]{
			huh.NewOption("redirect (301 permanent)", "redirect"),
			huh.NewOption("302 temporary redirect", "302"),
			huh.NewOption("masked (iframe)", "masked"),
		}
		form := huh.NewForm(
			huh.NewGroup(
				huh.NewInput().
					Title("Destination URL").
					Description(fmt.Sprintf("Where should %s forward to?", forwardingName(domain, host))).
					Placeholder("https://example.com").
					Value(&createForwardsTo).
					Validate(validateDestination),
				huh.NewSelect[string]().
					Title("Forwarding Type").
					Options(typeOptions...).
					Value(&createType),
			),
		)
		if err := form.Run(); err != nil {
			return cmdutil.FormError(err)
		}
		createForwardsTo = strings.TrimSpace(createForwardsTo)
	}

	if err := cmdutil.ValidURL(createForwardsTo, "to"); err != nil {
		return err
	}
	if err := cmdutil.ValidURLForwardingType(createType, "type"); err != nil {
		return err
	}
	if err := maskedOnly(createType, createTitle, createMeta); err != nil {
		return err
	}

	// DomainName is the path parameter, not a body field — it is tagged
	// `json:"-"`. SDK v1.33.5 dropped the CreateURLForwardingRequest wrapper
	// that used to carry it, so it has to be set here or the request POSTs to
	// /core/v1/domains//url/forwarding with an empty segment.
	body := coreapigo.URLForwardingInput{
		DomainName: domain,
		Host:       host,
		ForwardsTo: createForwardsTo,
		Type:       coreapigo.URLForwardingInputType(createType),
	}
	if createTitle != "" {
		body.Title = &createTitle
	}
	if createMeta != "" {
		body.Meta = &createMeta
	}

	var entry *coreapigo.URLForwardingResponse
	sent, err := cmdutil.RunWrite(cmd, cmdutil.Write[coreapigo.URLForwardingInput]{
		Method: "POST",
		Path:   fmt.Sprintf("/core/v1/domains/%s/url/forwarding", domain),
		Body:   body,
		Spin:   "Creating URL forwarding…",
	}, func(ctx context.Context, body coreapigo.URLForwardingInput) error {
		var err error
		entry, err = client.SDK().URLForwardings.CreateURLForwarding(ctx, &body)
		if err == nil && (entry == nil || entry.ID == nil || *entry.ID <= 0) {
			// Every other url command addresses a forwarding by this ID, so a
			// 2xx without one is not a success to report as "id 0" (#187).
			return &api.UnexpectedResponseError{Reason: "the response did not include the new forwarding's ID"}
		}
		return err
	})
	recovered := false
	if err != nil && host == "@" && isDuplicateRecord(err) {
		entry, err = createdAnyway(cmd, body, err)
		recovered = err == nil
	}
	if err != nil {
		return err
	}
	if !sent {
		return nil
	}
	if recovered {
		out.Warn(fmt.Sprintf("the API answered 400 Duplicate Record, but the forwarding was created (id %d). "+
			"The apex of %s likely has A records left by deleted forwardings: see 'namecom dns list %s --host @ --type A', "+
			"and remove the ones you don't need with 'namecom dns delete %s <id>'", *entry.ID, domain, domain, domain))
	}

	if out.QuietMode {
		if entry == nil || entry.ID == nil {
			out.Warn("the API did not return the new URL forwarding's ID")
			return nil
		}
		out.Quiet(strconv.Itoa(*entry.ID))
		return nil
	}

	// A recovered create says "changed": true, since the error it recovered
	// from could read as nothing having been made.
	var doc any = entry
	if recovered && (out.Format == output.FormatJSON || out.Format == output.FormatYAML) {
		withChanged, err := output.WithChanged(entry, true)
		if err != nil {
			return err
		}
		doc = withChanged
	}
	switch out.Format {
	case output.FormatJSON:
		return out.JSON(doc)
	case output.FormatYAML:
		return out.YAML(doc)
	default:
		out.Success(fmt.Sprintf("Created URL forwarding (id %d): %s → %s", *entry.ID, host, createForwardsTo))
	}
	return nil
}

func runUpdate(cmd *cobra.Command, args []string) error {
	out := cmdutil.Out(cmd)
	client := cmdutil.APIClient(cmd)
	domain, err := cmdutil.DomainArg(args, 0)
	if err != nil {
		return err
	}

	id, err := parseID(args[1])
	if err != nil {
		return err
	}

	// A --type that is not masked rules out --title and --meta before the
	// GET; without --type, the current type decides, below.
	if cmd.Flags().Changed("type") {
		if err := maskedOnly(updateType, updateTitle, updateMeta); err != nil {
			return err
		}
	}

	// Fetch current entry so unset flags preserve existing values (type, title, meta).
	getStop := out.Spin("Fetching URL forwarding…")
	current, err := client.SDK().URLForwardings.GetURLForwardingByID(cmd.Context(),
		&coreapigo.GetURLForwardingByIDRequest{DomainName: domain, ID: id})
	getStop()
	if cmdutil.IsNotFound(err) {
		return forwardingNotFound(err, id, domain)
	}
	if err != nil {
		return err
	}

	if updateForwardsTo == "" {
		// This is a read-modify-write: an unset --to means "keep the current
		// destination", exactly as unset --type/--title/--meta do. Demanding it
		// made `url update <domain> <id> --type masked` impossible in a script,
		// even though every other field is already preserved from `current`.
		if cmd.Flags().Changed("type") || cmd.Flags().Changed("title") || cmd.Flags().Changed("meta") {
			updateForwardsTo = current.ForwardsTo
		} else if !output.IsInteractive() {
			return cmdutil.NewUsageErrorHint(errors.New("--to is required unless --type, --title or --meta is passed"),
				"pass --to, or --type/--title/--meta to change only those; a terminal prompts for --to")
		}
	}

	formRan := false
	if updateForwardsTo == "" {
		if !output.IsInteractive() {
			return cmdutil.RequiredFlags(true, "to")
		}
		formRan = true
		typeOptions := []huh.Option[string]{
			huh.NewOption("redirect (301 permanent)", "redirect"),
			huh.NewOption("302 temporary redirect", "302"),
			huh.NewOption("masked (iframe)", "masked"),
		}
		form := huh.NewForm(
			huh.NewGroup(
				huh.NewInput().
					Title("New Destination URL").
					Placeholder("https://example.com").
					Value(&updateForwardsTo).
					Validate(validateDestination),
				huh.NewSelect[string]().
					Title("Forwarding Type").
					Options(typeOptions...).
					Value(&updateType),
			),
		)
		if err := form.Run(); err != nil {
			return cmdutil.FormError(err)
		}
		updateForwardsTo = strings.TrimSpace(updateForwardsTo)
	}

	if err := cmdutil.ValidURL(updateForwardsTo, "to"); err != nil {
		return err
	}
	if cmd.Flags().Changed("type") {
		if err := cmdutil.ValidURLForwardingType(updateType, "type"); err != nil {
			return err
		}
	}

	// Preserve the current type unless the user actually chose one: either via
	// --type, or through the interactive form (which always asks).
	//
	// This previously keyed off `!cmd.Flags().Changed("to")` as a stand-in for
	// "the form ran". That inference was wrong for any invocation that set
	// neither --to nor --type — e.g. `--title X` — which then fell through to
	// updateType's DEFAULT of "redirect" and silently converted a masked
	// forwarding into a 301.
	fwdTypeStr := string(current.Type)
	if cmd.Flags().Changed("type") || formRan {
		fwdTypeStr = updateType
	}
	if err := maskedOnly(fwdTypeStr, updateTitle, updateMeta); err != nil {
		return err
	}

	// No host key is sent. SDK v1.33.5 introduced URLForwardingUpdate, where
	// Host is *string with omitempty and documented as "Omit this field to keep
	// the existing host. Send an empty string to move the forwarding to the
	// apex."
	//
	// Update previously shared URLForwardingInput with create, whose Host had no
	// omitempty, so the key was always serialised and there was no way to say
	// "leave the host alone". Sending back the host fetched a moment earlier was
	// the safe workaround — a no-op restatement, since a literal "" is the apex
	// rather than "unchanged" and would have moved the forwarding. Omitting the
	// field says that directly, and drops the read-modify-write race the
	// restatement carried.
	//
	// Title and Meta are still seeded from the current entry so an unset flag
	// preserves what is already there. A passed flag replaces it even when
	// empty, so `--title ""` clears the title: gating on the value instead
	// resent the old one. The SDK's omitempty on these *string fields drops
	// only a nil pointer, so a pointer to "" goes out as "title":"".
	fwdType := coreapigo.URLForwardingUpdateType(fwdTypeStr)
	body := coreapigo.URLForwardingUpdate{
		ForwardsTo: &updateForwardsTo,
		Type:       &fwdType,
		Title:      current.Title,
		Meta:       current.Meta,
	}
	if cmd.Flags().Changed("title") {
		body.Title = &updateTitle
	}
	if cmd.Flags().Changed("meta") {
		body.Meta = &updateMeta
	}

	// Leaving masked keeps the stored title and meta, so switching back
	// restores them; a redirect does not use them (#286).
	if string(current.Type) == "masked" && fwdTypeStr != "masked" &&
		(derefStr(body.Title) != "" || derefStr(body.Meta) != "") {
		out.Warn(fmt.Sprintf(`the forwarding keeps its title and meta, which %s does not use — pass --title "" --meta "" to clear them`, fwdTypeStr))
	}

	// The flags ask for what the forwarding already is: nothing is sent,
	// under --dry-run too, as `dns update` does. The PATCH used to be sent
	// anyway and reported as "no values changed", with nothing in JSON to
	// tell it from a change. The GET above is all it takes.
	changes := urlChanges(current, body)
	if len(changes) == 0 {
		return printUpdated(out, current, false,
			fmt.Sprintf("URL forwarding %d (%s) already has these values: nothing to change", id, displayHost(current.Host)))
	}

	var entry *coreapigo.URLForwardingResponse
	sent, err := cmdutil.RunWrite(cmd, cmdutil.Write[coreapigo.URLForwardingUpdate]{
		Method: "PATCH",
		Path:   fmt.Sprintf("/core/v1/urlforwarding/%s/%d", domain, id),
		Body:   body,
		Spin:   "Updating URL forwarding…",
	}, func(ctx context.Context, body coreapigo.URLForwardingUpdate) error {
		var err error
		entry, err = client.SDK().URLForwardings.UpdateURLForwardingByID(ctx,
			&coreapigo.UpdateURLForwardingByIDRequest{DomainName: domain, ID: id, Body: &body})
		return err
	})
	if err != nil || !sent {
		return err
	}
	return printUpdated(out, entry, true,
		fmt.Sprintf("Updated URL forwarding %d (%s): %s", id, displayHost(current.Host), strings.Join(changes, ", ")))
}

// printUpdated prints the forwarding `url update` changed, or found already
// as asked: in JSON and YAML the entry with "changed", so a script can tell
// a no-op from a change, and otherwise msg. --quiet prints nothing.
func printUpdated(out *output.Config, entry *coreapigo.URLForwardingResponse, changed bool, msg string) error {
	if out.Quiet() {
		return nil
	}
	switch out.Format {
	case output.FormatJSON, output.FormatYAML:
		doc, err := output.WithChanged(entry, changed)
		if err != nil {
			return err
		}
		if out.Format == output.FormatYAML {
			return out.YAML(doc)
		}
		return out.JSON(doc)
	}
	out.Success(msg)
	return nil
}

func runDelete(cmd *cobra.Command, args []string) error {
	out := cmdutil.Out(cmd)
	client := cmdutil.APIClient(cmd)
	domain, err := cmdutil.DomainArg(args, 0)
	if err != nil {
		return err
	}

	id, err := parseID(args[1])
	if err != nil {
		return err
	}

	// Fetch the forwarding so the prompt can show it. "Delete URL forwarding
	// 7 from D?" named only an ID, and was asked even for one that did not
	// exist (#235). A missing one now fails here, before any prompt.
	stop := out.Spin("Fetching URL forwarding…")
	current, err := client.SDK().URLForwardings.GetURLForwardingByID(cmd.Context(),
		&coreapigo.GetURLForwardingByIDRequest{DomainName: domain, ID: id})
	stop()
	if cmdutil.IsNotFound(err) {
		return forwardingNotFound(err, id, domain)
	}
	if err != nil {
		return err
	}
	prompt := fmt.Sprintf("Delete URL forwarding %d from %s?", id, domain)
	if current != nil {
		prompt = fmt.Sprintf("Delete URL forwarding %s → %s (%s) from %s?",
			displayHost(current.Host), current.ForwardsTo, current.Type, domain)
	}

	sent, err := cmdutil.RunWrite(cmd, cmdutil.Write[cmdutil.NoBody]{
		Method: "DELETE",
		Path:   fmt.Sprintf("/core/v1/urlforwarding/%s/%d", domain, id),
		Prompt: prompt,
		Spin:   "Deleting URL forwarding…",
	}, func(ctx context.Context, _ cmdutil.NoBody) error {
		return client.SDK().URLForwardings.DeleteURLForwardingByID(ctx,
			&coreapigo.DeleteURLForwardingByIDRequest{DomainName: domain, ID: id})
	})
	if err != nil || !sent {
		return err
	}
	out.Success(fmt.Sprintf("Deleted URL forwarding %d from %s", id, domain))
	// The API removes a subdomain forwarding's A records with it, but not the
	// one it added at the apex (#286). Left behind, such records are what makes
	// a later apex create answer 400 Duplicate Record, so JSON says so too.
	if current != nil && displayHost(current.Host) == "@" {
		cmdutil.SideEffectNote(out, fmt.Sprintf("the A record name.com added at the apex of %s for this forwarding stays — remove it with 'namecom dns delete %s <id>' (see 'namecom dns list %s --host @')",
			domain, domain, domain))
	}
	return nil
}

// forwardingNotFound is the not-found error for a forwarding ID, the same
// for get, update and delete. update showed the API's own "URL forwarding
// entry not found.", which names neither the ID nor the domain.
func forwardingNotFound(err error, id int, domain string) error {
	return cmdutil.NotFound(err, fmt.Sprintf("URL forwarding %d not found on %s", id, domain),
		fmt.Sprintf("run 'namecom url list %s' to see its forwarding IDs", domain))
}

// isDuplicateRecord reports whether err is the API's 400 "Parameter Value
// Error - Duplicate Record".
func isDuplicateRecord(err error) bool {
	apiErr, ok := errors.AsType[*api.APIError](err)
	return ok && apiErr.StatusCode == 400 &&
		strings.Contains(apiErr.Details+" "+apiErr.Message, "Duplicate Record")
}

// createdAnyway is an apex create that the API answered with 400 Duplicate
// Record. It can store the forwarding and still answer that: name.com picks
// an A record for the apex, and when its pick matches an A record left by an
// earlier, deleted forwarding, the forwarding is created and the 400 sent
// anyway. Reported as a failure, a script retried a write that had landed,
// or cleaned up one it believed it never made.
//
// One list of the domain's forwardings decides it, and only on this error,
// so a create that succeeds still costs one request. A forwarding on the
// apex with the destination and type sent is returned as the result. When
// there is none, cause is returned as a conflict naming the likely culprit;
// when the list fails, as one saying the outcome is unknown.
func createdAnyway(cmd *cobra.Command, body coreapigo.URLForwardingInput, cause error) (*coreapigo.URLForwardingResponse, error) {
	out := cmdutil.Out(cmd)
	client := cmdutil.APIClient(cmd)
	domain := body.DomainName

	stop := out.Spin("Checking whether the forwarding was created…")
	page, perPage := 1, cmdutil.MaxPerPage
	list, err := client.SDK().URLForwardings.ListURLForwardingsByDomain(cmd.Context(),
		&coreapigo.ListURLForwardingsByDomainRequest{DomainName: domain, Page: &page, PerPage: &perPage})
	stop()
	if err != nil {
		return nil, &cmdutil.ConflictError{Err: cause,
			Hint: fmt.Sprintf("the forwarding may have been created anyway: run 'namecom url list %s' to check", domain)}
	}
	var found *coreapigo.URLForwardingResponse
	for _, f := range cmdutil.NonNil(list.URLForwarding) {
		if f.ID != nil && displayHost(f.Host) == "@" && f.ForwardsTo == body.ForwardsTo &&
			string(f.Type) == string(body.Type) && (found == nil || *f.ID > *found.ID) {
			found = f
		}
	}
	if found == nil {
		return nil, &cmdutil.ConflictError{Err: cause,
			Hint: fmt.Sprintf("the apex of %s likely has A records left by deleted forwardings: see 'namecom dns list %s --host @ --type A', and remove the ones you don't need with 'namecom dns delete %s <id>'",
				domain, domain, domain)}
	}
	return found, nil
}

// maskedOnly is the usage error for a non-empty --title or --meta on a
// forwarding that is not masked: they were sent and stored on a redirect,
// where they do nothing (#286). An empty value is allowed, so a title left
// on a redirect can still be cleared.
func maskedOnly(fwdType, title, meta string) error {
	if fwdType == "masked" || (title == "" && meta == "") {
		return nil
	}
	return cmdutil.NewUsageErrorHint(fmt.Errorf("--title and --meta apply only to masked forwarding, not %s", fwdType),
		"add --type masked, or leave out --title and --meta")
}

func derefStr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// urlChanges lists what an update changes on the forwarding, as "forwards
// to https://a → https://b", for the line that names it (#238). An empty
// list means the update would change nothing.
func urlChanges(current *coreapigo.URLForwardingResponse, body coreapigo.URLForwardingUpdate) []string {
	var changes []string
	add := func(field, was, now string) {
		if was != now {
			if was == "" {
				was = output.None
			}
			if now == "" {
				now = output.None
			}
			changes = append(changes, fmt.Sprintf("%s %s → %s", field, was, now))
		}
	}
	add("forwards to", current.ForwardsTo, derefStr(body.ForwardsTo))
	if body.Type != nil {
		add("type", string(current.Type), string(*body.Type))
	}
	add("title", derefStr(current.Title), derefStr(body.Title))
	add("meta", derefStr(current.Meta), derefStr(body.Meta))
	return changes
}

func urlRows(entries []*coreapigo.URLForwardingResponse) [][]string {
	rows := make([][]string, 0, len(entries))
	for _, u := range entries {
		id := ""
		if u.ID != nil {
			id = strconv.Itoa(*u.ID)
		}
		rows = append(rows, []string{
			id,
			displayHost(u.Host),
			u.ForwardsTo,
			string(u.Type),
		})
	}
	return rows
}

func parseID(s string) (int, error) {
	n, ok := cmdutil.PositiveID(s)
	if !ok {
		return 0, cmdutil.NewUsageError(fmt.Errorf("invalid ID %q: must be a positive whole number", s))
	}
	return int(n), nil
}

// validateDestination checks a destination typed into the create or update
// form, as it is typed. The form used to accept anything non-empty, so
// "example dot com" got through, the typing was lost, and the error named a
// --to flag the user never passed (#239).
func validateDestination(s string) error {
	s = strings.TrimSpace(s)
	if s == "" {
		return errors.New("destination URL is required")
	}
	lower := strings.ToLower(s)
	if !strings.HasPrefix(lower, "http://") && !strings.HasPrefix(lower, "https://") {
		return errors.New("must start with http:// or https://, e.g. https://example.com")
	}
	u, err := neturl.Parse(s)
	if err != nil || u.Host == "" || strings.ContainsAny(s, " \t") {
		return errors.New("not a valid URL, e.g. https://example.com/path")
	}
	return nil
}

// forwardingName is the name a forwarding answers on: the bare domain for the
// apex, host.domain otherwise. The form asked where "example.com/@" should
// forward to.
func forwardingName(domain, host string) string {
	if host == "" || host == "@" {
		return domain
	}
	return host + "." + domain
}

// displayHost renders a record's host for a table. The API returns the apex as
// an empty string, which printed as an empty cell — indistinguishable from a
// column that failed to render. "@" is the spelling --host takes for the apex,
// so input and output now agree. JSON and YAML keep the API's value.
func displayHost(h *string) string {
	if h == nil || *h == "" {
		return "@"
	}
	return *h
}
