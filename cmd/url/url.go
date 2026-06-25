// Package url implements the `namecom url` command group.
package url

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/charmbracelet/huh"
	"github.com/patramsey/namecom-cli/cmd/cmdutil"
	"github.com/patramsey/namecom-cli/internal/api"
	"github.com/patramsey/namecom-cli/internal/api/gen"
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
	Use:   "get <domain> <id>",
	Short: "Get a URL forwarding entry by ID",
	Example: `  namecom url get example.com 12345`,
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
	Use:   "update <domain> <id>",
	Short: "Update a URL forwarding entry",
	Example: `  namecom url update example.com 12345 --to https://other-site.com`,
	Args:              cmdutil.ExactArgs(2),
	RunE:              runUpdate,
	ValidArgsFunction: cmdutil.CompleteDomains,
}

var deleteCmd = &cobra.Command{
	Use:   "delete <domain> <id>",
	Short: "Delete a URL forwarding entry",
	Example: `  namecom url delete example.com 12345`,
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

	Cmd.AddCommand(listCmd, getCmd, createCmd, updateCmd, deleteCmd)
}

func runList(cmd *cobra.Command, args []string) error {
	out := cmdutil.Out(cmd)
	client := cmdutil.APIClient(cmd)
	domain := args[0]

	spin := out.StartSpinner("Fetching URL forwardings…")
	var page int32 = 1
	var all []gen.URLForwardingResponseSchema
	var hasMore bool
	for {
		params := &gen.ListURLForwardingsByDomainParams{Page: &page}
		resp, err := client.Gen().ListURLForwardingsByDomain(cmd.Context(), domain, params)
		if err != nil {
			spin.Stop()
			return err
		}
		var result gen.ListURLForwardingsResponseSchema
		if err := api.Decode(resp, &result); err != nil {
			spin.Stop()
			return err
		}
		all = append(all, result.UrlForwarding...)
		if result.NextPage == nil || *result.NextPage == 0 {
			break
		}
		if !listAll {
			hasMore = true
			break
		}
		page = *result.NextPage
		spin.Update(fmt.Sprintf("Fetching URL forwardings… (page %d, %d so far)", page, len(all)))
	}
	spin.Stop()

	if out.QuietMode {
		ids := make([]string, 0, len(all))
		for _, u := range all {
			if u.Id != nil {
				ids = append(ids, strconv.Itoa(int(*u.Id)))
			}
		}
		out.PrintQuiet(ids)
		return nil
	}

	switch out.Format {
	case output.FormatJSON:
		return out.JSON(all)
	case output.FormatYAML:
		return out.YAML(all)
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
	resp, err := client.Gen().GetURLForwardingById(cmd.Context(), args[0], id)
	stop()
	if err != nil {
		return err
	}
	var entry gen.URLForwardingResponseSchema
	if err := api.Decode(resp, &entry); err != nil {
		return err
	}

	switch out.Format {
	case output.FormatJSON:
		return out.JSON(entry)
	case output.FormatYAML:
		return out.YAML(entry)
	default:
		out.Table(
			[]string{"ID", "HOST", "FORWARDS TO", "TYPE"},
			urlRows([]gen.URLForwardingResponseSchema{entry}),
		)
	}
	return nil
}

func runCreate(cmd *cobra.Command, args []string) error {
	out := cmdutil.Out(cmd)
	client := cmdutil.APIClient(cmd)
	dryRun := cmdutil.IsDryRun(cmd)
	domain := args[0]

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

	fwdType := gen.URLForwardingType(createType)
	body := gen.CreateURLForwardingJSONRequestBody{
		Host:       createHost,
		ForwardsTo: createForwardsTo,
		Type:       fwdType,
	}
	if createTitle != "" {
		body.Title = &createTitle
	}
	if createMeta != "" {
		body.Meta = &createMeta
	}

	if dryRun {
		out.DryRun("POST", fmt.Sprintf("/core/v1/domains/%s/url/forwarding", domain), nil)
		fmt.Fprintf(out.Writer, "  host=%s to=%s type=%s\n", createHost, createForwardsTo, createType)
		return nil
	}

	stop := out.Spin("Creating URL forwarding…")
	resp, err := client.Gen().CreateURLForwarding(cmd.Context(), domain, body)
	stop()
	if err != nil {
		return err
	}
	var entry gen.URLForwardingResponseSchema
	if err := api.Decode(resp, &entry); err != nil {
		return err
	}

	switch out.Format {
	case output.FormatJSON:
		return out.JSON(entry)
	case output.FormatYAML:
		return out.YAML(entry)
	default:
		id := int32(0)
		if entry.Id != nil {
			id = *entry.Id
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
	domain := args[0]

	id, err := parseID(args[1])
	if err != nil {
		return err
	}

	// Fetch current entry so unset flags preserve existing values (type, title, meta).
	getStop := out.Spin("Fetching URL forwarding…")
	getResp, err := client.Gen().GetURLForwardingById(cmd.Context(), domain, id)
	getStop()
	if err != nil {
		return err
	}
	var current gen.URLForwardingResponseSchema
	if err := api.Decode(getResp, &current); err != nil {
		return err
	}

	if updateForwardsTo == "" {
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

	// Preserve current type when --type wasn't explicitly passed and the
	// interactive form didn't run (form always lets the user pick a type).
	fwdTypeStr := string(current.Type)
	if cmd.Flags().Changed("type") || !cmd.Flags().Changed("to") {
		fwdTypeStr = updateType
	}

	body := gen.UpdateURLForwardingByIdJSONRequestBody{
		ForwardsTo: updateForwardsTo,
		Type:       gen.UpdateURLForwardingBodyType(fwdTypeStr),
	}
	if updateTitle != "" {
		body.Title = &updateTitle
	}
	if updateMeta != "" {
		body.Meta = &updateMeta
	}

	if dryRun {
		out.DryRun("PUT", fmt.Sprintf("/core/v1/domains/%s/url/forwarding/%d", domain, id), nil)
		fmt.Fprintf(out.Writer, "  to=%s type=%s\n", updateForwardsTo, updateType)
		return nil
	}

	stop := out.Spin("Updating URL forwarding…")
	resp, err := client.Gen().UpdateURLForwardingById(cmd.Context(), domain, id, body)
	stop()
	if err != nil {
		return err
	}
	var entry gen.URLForwardingResponseSchema
	if err := api.Decode(resp, &entry); err != nil {
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
	domain := args[0]

	id, err := parseID(args[1])
	if err != nil {
		return err
	}

	ok, err := cmdutil.Confirm(out, yes, fmt.Sprintf("Delete URL forwarding %d from %s?", id, domain))
	if err != nil {
		return err
	}
	if !ok {
		out.Warn("aborted")
		return nil
	}

	if dryRun {
		out.DryRun("DELETE", fmt.Sprintf("/core/v1/domains/%s/url/forwarding/%d", domain, id), nil)
		return nil
	}

	stop := out.Spin("Deleting URL forwarding…")
	resp, err := client.Gen().DeleteURLForwardingById(cmd.Context(), domain, id)
	stop()
	if err != nil {
		return err
	}
	if err := api.Decode(resp, nil); err != nil {
		return err
	}
	out.Success(fmt.Sprintf("Deleted URL forwarding %d from %s", id, domain))
	out.Hint(fmt.Sprintf("Run 'namecom url list %s' to see remaining forwardings", domain))
	return nil
}

func urlRows(entries []gen.URLForwardingResponseSchema) [][]string {
	rows := make([][]string, 0, len(entries))
	for _, u := range entries {
		id := ""
		if u.Id != nil {
			id = strconv.Itoa(int(*u.Id))
		}
		rows = append(rows, []string{
			id,
			u.Host,
			u.ForwardsTo,
			string(u.Type),
		})
	}
	return rows
}

func parseID(s string) (int32, error) {
	n, err := strconv.ParseInt(s, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("invalid ID %q: must be a number", s)
	}
	return int32(n), nil
}
