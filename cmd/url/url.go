// Package url implements the `namecom url` command group.
package url

import (
	"errors"
	"fmt"
	"math"
	"strconv"

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
	Use:   "list <domain>",
	Short: "List URL forwarding entries",
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
	Use:   "create <domain>",
	Short: "Create a URL forwarding entry",
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
	Short:             "Delete a URL forwarding entry",
	Example:           `  namecom url delete example.com 12345`,
	Args:              cmdutil.ExactArgs(2),
	RunE:              runDelete,
	ValidArgsFunction: cmdutil.CompleteDomains,
}

func init() {
	listCmd.Flags().BoolVar(&listAll, "all", false, "fetch all pages")

	createCmd.Flags().StringVar(&createHost, "host", "@", "subdomain host (@ for apex)")
	createCmd.Flags().StringVar(&createForwardsTo, "to", "", "destination URL")
	createCmd.Flags().StringVar(&createType, "type", "redirect", "forwarding type: redirect, 302, masked")
	createCmd.Flags().StringVar(&createTitle, "title", "", "page title (masked only)")
	createCmd.Flags().StringVar(&createMeta, "meta", "", "meta tags (masked only)")

	updateCmd.Flags().StringVar(&updateForwardsTo, "to", "", "new destination URL")
	updateCmd.Flags().StringVar(&updateType, "type", "redirect", "forwarding type: redirect, 302, masked")
	updateCmd.Flags().StringVar(&updateTitle, "title", "", "page title (masked only)")
	updateCmd.Flags().StringVar(&updateMeta, "meta", "", "meta tags (masked only)")

	cmdutil.GroupCmd(Cmd)
	Cmd.AddCommand(listCmd, getCmd, createCmd, updateCmd, deleteCmd)
}

func runList(cmd *cobra.Command, args []string) error {
	out := cmdutil.Out(cmd)
	client := cmdutil.APIClient(cmd)
	domain, err := cmdutil.DomainArg(args, 0)
	if err != nil {
		return err
	}

	spin := out.StartSpinner("Fetching URL forwardings…")
	page := 1
	var all []*coreapigo.URLForwardingResponse
	var hasMore bool
	var lastResult *coreapigo.ListURLForwardingsResponse
	for {
		result, err := client.SDK().URLForwardings.ListURLForwardingsByDomain(cmd.Context(),
			&coreapigo.ListURLForwardingsByDomainRequest{DomainName: domain, Page: &page})
		if err != nil {
			spin.Stop()
			return api.FromSDKError(err)
		}
		all = append(all, result.URLForwarding...)
		lastResult = result
		next, ok := cmdutil.NextPage(page, result.NextPage, result.LastPage)
		if !ok {
			break
		}
		if !listAll {
			hasMore = true
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
			np = int32Page(lastResult.NextPage)
		}
		return out.JSONList(all, np, 0)
	case output.FormatYAML:
		var np *int32
		if hasMore {
			np = int32Page(lastResult.NextPage)
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
		out.Count(len(all), "URL forwarding")
		if hasMore {
			out.Hint("Showing first page — pass --all to fetch all entries")
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

	stop := out.Spin("Fetching URL forwarding…")
	domain, err := cmdutil.DomainArg(args, 0)
	if err != nil {
		return err
	}
	entry, err := client.SDK().URLForwardings.GetURLForwardingByID(cmd.Context(),
		&coreapigo.GetURLForwardingByIDRequest{DomainName: domain, ID: id})
	stop()
	if err != nil {
		return err
	}

	// --quiet prints the identifying value only, matching list commands.
	if out.QuietMode {
		id := ""
		if entry.ID != nil {
			id = strconv.Itoa(*entry.ID)
		}
		out.PrintQuiet([]string{id})
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
	dryRun := cmdutil.IsDryRun(cmd)
	domain, err := cmdutil.DomainArg(args, 0)
	if err != nil {
		return err
	}

	if createForwardsTo == "" {
		if !output.IsInteractive() {
			return fmt.Errorf("--to is required")
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
					Description(fmt.Sprintf("Where should %s/%s forward to?", domain, createHost)).
					Placeholder("https://example.com").
					Value(&createForwardsTo).
					Validate(func(s string) error {
						if s == "" {
							return errors.New("destination URL is required")
						}
						return nil
					}),
				huh.NewSelect[string]().
					Title("Forwarding Type").
					Options(typeOptions...).
					Value(&createType),
			),
		)
		if err := form.Run(); err != nil {
			if errors.Is(err, huh.ErrUserAborted) {
				out.Warn("aborted")
				return nil
			}
			return err
		}
	}

	if err := cmdutil.ValidURL(createForwardsTo, "to"); err != nil {
		return err
	}
	if err := cmdutil.ValidURLForwardingType(createType, "type"); err != nil {
		return err
	}

	body := coreapigo.URLForwardingInput{
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

	if dryRun {
		out.DryRun("POST", fmt.Sprintf("/core/v1/domains/%s/url/forwarding", domain), body)
		fmt.Fprintf(out.Writer, "  host=%s to=%s type=%s\n", createHost, createForwardsTo, createType)
		return nil
	}

	stop := out.Spin("Creating URL forwarding…")
	entry, err := client.SDK().URLForwardings.CreateURLForwarding(cmd.Context(),
		&coreapigo.CreateURLForwardingRequest{DomainName: domain, Body: &body})
	stop()
	if err != nil {
		return err
	}

	switch out.Format {
	case output.FormatJSON:
		return out.JSON(entry)
	case output.FormatYAML:
		return out.YAML(entry)
	default:
		id := 0
		if entry.ID != nil {
			id = *entry.ID
		}
		out.Success(fmt.Sprintf("Created URL forwarding (id %d): %s → %s", id, createHost, createForwardsTo))
		out.Hint(fmt.Sprintf("Run 'namecom url list %s' to see all forwardings", domain))
	}
	return nil
}

func runUpdate(cmd *cobra.Command, args []string) error {
	out := cmdutil.Out(cmd)
	client := cmdutil.APIClient(cmd)
	dryRun := cmdutil.IsDryRun(cmd)
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
			return fmt.Errorf("--to is required (or pass --type/--title/--meta to change those instead)")
		}
	}

	formRan := false
	if updateForwardsTo == "" {
		if !output.IsInteractive() {
			return fmt.Errorf("--to is required")
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
					Validate(func(s string) error {
						if s == "" {
							return errors.New("destination URL is required")
						}
						return nil
					}),
				huh.NewSelect[string]().
					Title("Forwarding Type").
					Options(typeOptions...).
					Value(&updateType),
			),
		)
		if err := form.Run(); err != nil {
			if errors.Is(err, huh.ErrUserAborted) {
				out.Warn("aborted")
				return nil
			}
			return err
		}
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

	// Title and Meta have no `omitempty` in the request body, so leaving them
	// nil transmits an explicit `"title":null` — the server reads that as a
	// deliberate clear, not an omission. Seed both from the current entry so
	// an unset flag preserves what's already there.
	// This request carries a "host" key that the generated client did not send,
	// and that is a deliberate, unavoidable change rather than an oversight.
	//
	// The SDK models create and update with a single URLForwardingInput whose
	// Host has no omitempty, so the key is always serialised. Its explicit-field
	// machinery does not help: HandleExplicitFields only strips omitempty from
	// fields that were set, it does not omit unset ones. There is no way to
	// express "leave the host alone" through this type.
	//
	// Sending the host fetched a moment ago is the safe form of that. A literal
	// would send "host":"" — and an empty host on a URL forwarding is the apex,
	// not "unchanged", so it could silently move the forward. Seeding from the
	// current record makes the field a no-op restatement of what is already
	// there. See docs/upstream/core-api-go-urlforwarding-host-required.md.
	body := coreapigo.URLForwardingInput{
		Host:       derefStr(current.Host),
		ForwardsTo: updateForwardsTo,
		Type:       coreapigo.URLForwardingInputType(fwdTypeStr),
		Title:      current.Title,
		Meta:       current.Meta,
	}
	if updateTitle != "" {
		body.Title = &updateTitle
	}
	if updateMeta != "" {
		body.Meta = &updateMeta
	}

	if dryRun {
		out.DryRun("PATCH", fmt.Sprintf("/core/v1/urlforwarding/%s/%d", domain, id), body)
		fmt.Fprintf(out.Writer, "  to=%s type=%s\n", updateForwardsTo, updateType)
		return nil
	}

	stop := out.Spin("Updating URL forwarding…")
	entry, err := client.SDK().URLForwardings.UpdateURLForwardingByID(cmd.Context(),
		&coreapigo.UpdateURLForwardingByIDRequest{DomainName: domain, ID: id, Body: &body})
	stop()
	if err != nil {
		return err
	}

	switch out.Format {
	case output.FormatJSON:
		return out.JSON(entry)
	case output.FormatYAML:
		return out.YAML(entry)
	default:
		out.Success(fmt.Sprintf("Updated URL forwarding %d", id))
		out.Hint(fmt.Sprintf("Run 'namecom url list %s' to see all forwardings", domain))
	}
	return nil
}

func runDelete(cmd *cobra.Command, args []string) error {
	out := cmdutil.Out(cmd)
	client := cmdutil.APIClient(cmd)
	yes := cmdutil.IsYes(cmd)
	dryRun := cmdutil.IsDryRun(cmd)
	domain, err := cmdutil.DomainArg(args, 0)
	if err != nil {
		return err
	}

	id, err := parseID(args[1])
	if err != nil {
		return err
	}

	if dryRun {
		out.DryRun("DELETE", fmt.Sprintf("/core/v1/urlforwarding/%s/%d", domain, id), nil)
		return nil
	}

	ok, err := cmdutil.Confirm(out, yes, fmt.Sprintf("Delete URL forwarding %d from %s?", id, domain))
	if err != nil {
		return err
	}
	if !ok {
		out.Warn("aborted")
		return nil
	}

	stop := out.Spin("Deleting URL forwarding…")
	err = client.SDK().URLForwardings.DeleteURLForwardingByID(cmd.Context(),
		&coreapigo.DeleteURLForwardingByIDRequest{DomainName: domain, ID: id})
	stop()
	if err != nil {
		return err
	}
	out.Success(fmt.Sprintf("Deleted URL forwarding %d from %s", id, domain))
	out.Hint(fmt.Sprintf("Run 'namecom url list %s' to see remaining forwardings", domain))
	return nil
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
			derefStr(u.Host),
			u.ForwardsTo,
			string(u.Type),
		})
	}
	return rows
}

func parseID(s string) (int, error) {
	n, err := strconv.ParseInt(s, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("invalid ID %q: must be a number", s)
	}
	return int(n), nil
}

// int32Page narrows the SDK's *int page number to the *int32 the output
// envelope uses. Same rationale as cmd/dns.
func int32Page(p *int) *int32 {
	if p == nil || *p > math.MaxInt32 || *p < math.MinInt32 {
		return nil
	}
	v := int32(*p)
	return &v
}

// derefStr returns the value behind a *string, or "" when it is nil.
func derefStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
