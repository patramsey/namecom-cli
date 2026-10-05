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
	Short: "Create and manage URL redirects for your domains",
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

var listCmd = &cobra.Command{
	Use:     "list <domain>",
	Aliases: []string{"ls"},
	Short:   "List URL forwarding entries",
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
	Example: `  namecom url create example.com --to https://new-site.com
  namecom url create example.com --host www --to https://new-site.com --type redirect
  namecom url create example.com --to https://new-site.com --type masked --title "My Site"`,
	Args:              cmdutil.ExactArgs(1),
	RunE:              runCreate,
	ValidArgsFunction: cmdutil.CompleteDomains,
}

var updateCmd = &cobra.Command{
	Use:               "update <domain> <id>",
	Short:             "Update a URL forwarding entry",
	Example:           `  namecom url update example.com 12345 --to https://other-site.com`,
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
	cmdutil.AddPageFlags(listCmd, &listAll, &listPage, &listLimit, "forwarding")

	createCmd.Flags().StringVar(&createHost, "host", "@", "subdomain host (@ for apex); a forwarding on a subdomain replaces its existing A records")
	createCmd.Flags().StringVar(&createForwardsTo, "to", "", "destination URL "+cmdutil.PromptedRequired)
	createCmd.Flags().StringVar(&createType, "type", "redirect", "forwarding type: redirect, 302, masked")
	createCmd.Flags().StringVar(&createTitle, "title", "", "page title (masked only)")
	createCmd.Flags().StringVar(&createMeta, "meta", "", "meta tags (masked only)")

	updateCmd.Flags().StringVar(&updateForwardsTo, "to", "", "new destination URL")
	// No default: an unset --type keeps the forwarding's current type, and a
	// default of "redirect" in --help read as though a masked forwarding would
	// be reset to a redirect.
	updateCmd.Flags().StringVar(&updateType, "type", "", "forwarding type: redirect, 302, masked (default: keep the current type)")
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

	if err := cmdutil.ValidPage(listPage, listLimit); err != nil {
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
			&coreapigo.ListURLForwardingsByDomainRequest{DomainName: domain, Page: &page, PerPage: cmdutil.PerPage(listLimit)})
		if err != nil {
			spin.Stop()
			return api.FromSDKError(err)
		}
		all = append(all, cmdutil.NonNil(result.URLForwarding)...)
		lastResult = result
		next, ok := cmdutil.NextPage(page, result.NextPage, result.LastPage)
		if !ok {
			break
		}
		// --quiet returns before the "showing first page" hint, so stopping
		// early would truncate silently. Page fully whenever the caller cannot
		// be told there is more — see cmd/contact/contact.go.
		if !listAll && !out.QuietMode {
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
		if len(all) == 0 {
			out.Empty("URL forwarding", fmt.Sprintf("Run 'namecom url create %s --to https://example.com' to add one", domain))
			return nil
		}
		out.Table(
			[]string{"ID", "HOST", "FORWARDS TO", "TYPE"},
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
		return cmdutil.NotFound(err, fmt.Sprintf("URL forwarding %d not found on %s — run 'namecom url list %s' to see forwarding IDs", id, domain, domain))
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
	// form asks for anything else.
	if err := cmdutil.ValidDNSHost(createHost); err != nil {
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
					Description(fmt.Sprintf("Where should %s forward to?", forwardingName(domain, createHost))).
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

	// DomainName is the path parameter, not a body field — it is tagged
	// `json:"-"`. SDK v1.33.5 dropped the CreateURLForwardingRequest wrapper
	// that used to carry it, so it has to be set here or the request POSTs to
	// /core/v1/domains//url/forwarding with an empty segment.
	body := coreapigo.URLForwardingInput{
		DomainName: domain,
		Host:       createHost,
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
	if err != nil {
		return err
	}
	if !sent {
		return nil
	}

	if out.QuietMode {
		if entry == nil || entry.ID == nil {
			out.Warn("the API did not return the new URL forwarding's ID")
			return nil
		}
		out.Quiet(strconv.Itoa(*entry.ID))
		return nil
	}

	switch out.Format {
	case output.FormatJSON:
		return out.JSON(entry)
	case output.FormatYAML:
		return out.YAML(entry)
	default:
		out.Success(fmt.Sprintf("Created URL forwarding (id %d): %s → %s", *entry.ID, createHost, createForwardsTo))
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

	// Fetch current entry so unset flags preserve existing values (type, title, meta).
	getStop := out.Spin("Fetching URL forwarding…")
	current, err := client.SDK().URLForwardings.GetURLForwardingByID(cmd.Context(),
		&coreapigo.GetURLForwardingByIDRequest{DomainName: domain, ID: id})
	getStop()
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
	if err != nil {
		return err
	}
	if !sent || out.Quiet() {
		return nil
	}

	switch out.Format {
	case output.FormatJSON:
		return out.JSON(entry)
	case output.FormatYAML:
		return out.YAML(entry)
	default:
		out.Success(urlUpdateLine(id, current, body))
	}
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
		return cmdutil.NotFound(err, fmt.Sprintf("URL forwarding %d not found on %s — run 'namecom url list %s' to see forwarding IDs", id, domain, domain))
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
	return nil
}

// urlUpdateLine names the forwarding an update changed and what changed:
// "Updated URL forwarding 7 (go.example.com): forwards to https://a → https://b".
// It said only "Updated URL forwarding 7" (#238).
func urlUpdateLine(id int, current *coreapigo.URLForwardingResponse, body coreapigo.URLForwardingUpdate) string {
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
	str := func(p *string) string {
		if p == nil {
			return ""
		}
		return *p
	}
	add("forwards to", current.ForwardsTo, str(body.ForwardsTo))
	if body.Type != nil {
		add("type", string(current.Type), string(*body.Type))
	}
	add("title", str(current.Title), str(body.Title))
	add("meta", str(current.Meta), str(body.Meta))
	line := fmt.Sprintf("Updated URL forwarding %d (%s)", id, displayHost(current.Host))
	if len(changes) == 0 {
		return line + ": no values changed"
	}
	return line + ": " + strings.Join(changes, ", ")
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
