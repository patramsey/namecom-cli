// Package email implements the `namecom email` command group.
package email

import (
	"errors"
	"fmt"
	"strings"

	"github.com/charmbracelet/huh"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/patramsey/namecom-cli/cmd/cmdutil"
	"github.com/patramsey/namecom-cli/internal/api"
	"github.com/patramsey/namecom-cli/internal/api/gen"
	"github.com/patramsey/namecom-cli/internal/output"
	"github.com/spf13/cobra"
)

// Cmd is the `namecom email` parent command.
var Cmd = &cobra.Command{
	Use:   "email",
	Short: "Forward email addresses to external mailboxes",
}

var (
	createEmailTo string
	updateEmailTo string
)

var listCmd = &cobra.Command{
	Use:   "list <domain>",
	Short: "List email forwarding entries",
	Example: `  namecom email list example.com`,
	Args:              cmdutil.ExactArgs(1),
	RunE:              runList,
	ValidArgsFunction: cmdutil.CompleteDomains,
}

var getCmd = &cobra.Command{
	Use:   "get <domain> <mailbox>",
	Short: "Get an email forwarding entry",
	Example: `  namecom email get example.com info`,
	Args:              cmdutil.ExactArgs(2),
	RunE:              runGet,
	ValidArgsFunction: cmdutil.CompleteDomains,
}

var createCmd = &cobra.Command{
	Use:   "create <domain> <mailbox>",
	Short: "Create an email forwarding entry",
	Example: `  namecom email create example.com info --to you@gmail.com
  namecom email create example.com support --to team@example.com`,
	Args:              cmdutil.ExactArgs(2),
	RunE:              runCreate,
	ValidArgsFunction: cmdutil.CompleteDomains,
}

var updateCmd = &cobra.Command{
	Use:   "update <domain> <mailbox>",
	Short: "Update an email forwarding entry",
	Example: `  namecom email update example.com info --to newemail@gmail.com`,
	Args:              cmdutil.ExactArgs(2),
	RunE:              runUpdate,
	ValidArgsFunction: cmdutil.CompleteDomains,
}

var deleteCmd = &cobra.Command{
	Use:   "delete <domain> <mailbox>",
	Short: "Delete an email forwarding entry",
	Example: `  namecom email delete example.com info`,
	Args:              cmdutil.ExactArgs(2),
	RunE:              runDelete,
	ValidArgsFunction: cmdutil.CompleteDomains,
}

func init() {
	createCmd.Flags().StringVar(&createEmailTo, "to", "", "destination email address (required)")
	updateCmd.Flags().StringVar(&updateEmailTo, "to", "", "new destination email address")

	Cmd.AddCommand(listCmd, getCmd, createCmd, updateCmd, deleteCmd)
}

func runList(cmd *cobra.Command, args []string) error {
	out := cmdutil.Out(cmd)
	client := cmdutil.APIClient(cmd)
	domain := args[0]

	stop := out.Spin("Fetching email forwardings…")
	var page int32 = 1
	var all []gen.EmailForwarding
	for {
		params := &gen.ListEmailForwardingsParams{Page: &page}
		resp, err := client.Gen().ListEmailForwardings(cmd.Context(), domain, params)
		if err != nil {
			stop()
			return err
		}
		var result gen.ListEmailForwardingsResponseSchema
		if err := api.Decode(resp, &result); err != nil {
			stop()
			return err
		}
		all = append(all, result.EmailForwarding...)
		if result.NextPage == nil || *result.NextPage == 0 {
			break
		}
		page = *result.NextPage
	}
	stop()

	if out.QuietMode {
		boxes := make([]string, 0, len(all))
		for _, e := range all {
			boxes = append(boxes, e.EmailBox)
		}
		out.PrintQuiet(boxes)
		return nil
	}

	switch out.Format {
	case output.FormatJSON:
		return out.JSON(all)
	case output.FormatYAML:
		return out.YAML(all)
	default:
		if len(all) == 0 {
			out.Empty("email forwarding", fmt.Sprintf("Run 'namecom email create %s <mailbox> --to dest@example.com' to add one", domain))
			return nil
		}
		out.Table(
			[]string{"MAILBOX", "FORWARDS TO"},
			emailRows(all),
		)
		out.Count(len(all), "forwarding")
	}
	return nil
}

func runGet(cmd *cobra.Command, args []string) error {
	out := cmdutil.Out(cmd)
	client := cmdutil.APIClient(cmd)

	stop := out.Spin("Fetching email forwarding…")
	resp, err := client.Gen().GetEmailForwarding(cmd.Context(), args[0], args[1])
	stop()
	if err != nil {
		return err
	}
	var entry gen.EmailForwarding
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
			[]string{"MAILBOX", "FORWARDS TO"},
			emailRows([]gen.EmailForwarding{entry}),
		)
	}
	return nil
}

func runCreate(cmd *cobra.Command, args []string) error {
	out := cmdutil.Out(cmd)
	client := cmdutil.APIClient(cmd)
	dryRun := cmdutil.IsDryRun(cmd)
	domain, mailbox := args[0], args[1]

	if createEmailTo == "" {
		if !output.IsInteractive() {
			return fmt.Errorf("--to is required")
		}
		form := huh.NewForm(
			huh.NewGroup(
				huh.NewInput().
					Title("Forward To").
					Description(fmt.Sprintf("Email address that %s@%s should forward to", mailbox, domain)).
					Placeholder("you@example.com").
					Value(&createEmailTo).
					Validate(func(s string) error {
						if s == "" {
							return errors.New("destination email is required")
						}
						if !strings.Contains(s, "@") {
							return errors.New("enter a valid email address")
						}
						return nil
					}),
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

	body := gen.CreateEmailForwardingJSONRequestBody{
		EmailBox: mailbox,
		EmailTo:  openapi_types.Email(createEmailTo),
	}

	if dryRun {
		out.DryRun("POST", fmt.Sprintf("/core/v1/domains/%s/email/forwarding", domain), nil)
		fmt.Fprintf(out.Writer, "  emailBox=%s emailTo=%s\n", mailbox, createEmailTo)
		return nil
	}

	stop := out.Spin("Creating email forwarding…")
	resp, err := client.Gen().CreateEmailForwarding(cmd.Context(), domain, body)
	stop()
	if err != nil {
		return err
	}
	var entry gen.EmailForwarding
	if err := api.Decode(resp, &entry); err != nil {
		return err
	}

	switch out.Format {
	case output.FormatJSON:
		return out.JSON(entry)
	case output.FormatYAML:
		return out.YAML(entry)
	default:
		out.Success(fmt.Sprintf("Created forwarding %s@%s → %s", mailbox, domain, createEmailTo))
		out.Hint(fmt.Sprintf("Run 'namecom email list %s' to see all forwardings", domain))
	}
	return nil
}

func runUpdate(cmd *cobra.Command, args []string) error {
	out := cmdutil.Out(cmd)
	client := cmdutil.APIClient(cmd)
	dryRun := cmdutil.IsDryRun(cmd)
	domain, mailbox := args[0], args[1]

	if updateEmailTo == "" {
		if !output.IsInteractive() {
			return fmt.Errorf("--to is required")
		}
		form := huh.NewForm(
			huh.NewGroup(
				huh.NewInput().
					Title("New Forward-To Address").
					Description(fmt.Sprintf("New destination for %s@%s", mailbox, domain)).
					Placeholder("you@example.com").
					Value(&updateEmailTo).
					Validate(func(s string) error {
						if s == "" {
							return errors.New("destination email is required")
						}
						if !strings.Contains(s, "@") {
							return errors.New("enter a valid email address")
						}
						return nil
					}),
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

	body := gen.UpdateEmailForwardingJSONRequestBody{
		EmailTo: &updateEmailTo,
	}

	if dryRun {
		out.DryRun("PUT", fmt.Sprintf("/core/v1/domains/%s/email/forwarding/%s", domain, mailbox), nil)
		fmt.Fprintf(out.Writer, "  emailTo=%s\n", updateEmailTo)
		return nil
	}

	stop := out.Spin("Updating email forwarding…")
	resp, err := client.Gen().UpdateEmailForwarding(cmd.Context(), domain, mailbox, body)
	stop()
	if err != nil {
		return err
	}
	var entry gen.EmailForwarding
	if err := api.Decode(resp, &entry); err != nil {
		return err
	}

	switch out.Format {
	case output.FormatJSON:
		return out.JSON(entry)
	case output.FormatYAML:
		return out.YAML(entry)
	default:
		out.Success(fmt.Sprintf("Updated forwarding %s@%s → %s", mailbox, domain, updateEmailTo))
		out.Hint(fmt.Sprintf("Run 'namecom email list %s' to see all forwardings", domain))
	}
	return nil
}

func runDelete(cmd *cobra.Command, args []string) error {
	out := cmdutil.Out(cmd)
	client := cmdutil.APIClient(cmd)
	yes := cmdutil.IsYes(cmd)
	dryRun := cmdutil.IsDryRun(cmd)
	domain, mailbox := args[0], args[1]

	ok, err := cmdutil.Confirm(yes, fmt.Sprintf("Delete forwarding for %s@%s?", mailbox, domain))
	if err != nil {
		return err
	}
	if !ok {
		out.Warn("aborted")
		return nil
	}

	if dryRun {
		out.DryRun("DELETE", fmt.Sprintf("/core/v1/domains/%s/email/forwarding/%s", domain, mailbox), nil)
		return nil
	}

	stop := out.Spin("Deleting email forwarding…")
	resp, err := client.Gen().DeleteEmailForwarding(cmd.Context(), domain, mailbox)
	stop()
	if err != nil {
		return err
	}
	if err := api.Decode(resp, nil); err != nil {
		return err
	}
	out.Success(fmt.Sprintf("Deleted forwarding for %s@%s", mailbox, domain))
	out.Hint(fmt.Sprintf("Run 'namecom email list %s' to see remaining forwardings", domain))
	return nil
}

func emailRows(entries []gen.EmailForwarding) [][]string {
	rows := make([][]string, 0, len(entries))
	for _, e := range entries {
		rows = append(rows, []string{
			e.EmailBox + "@" + e.DomainName,
			string(e.EmailTo),
		})
	}
	return rows
}
