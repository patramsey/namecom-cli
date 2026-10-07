// Package email implements the `namecom email` command group.
package email

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/charmbracelet/huh"
	coreapigo "github.com/namedotcom/core-api-go"
	"github.com/patramsey/namecom-cli/cmd/cmdutil"
	"github.com/patramsey/namecom-cli/internal/api"
	"github.com/patramsey/namecom-cli/internal/output"
	"github.com/spf13/cobra"
)

// The DNS records name.com adds to a domain for email forwarding, seen in the
// sandbox (#286). The API adds them itself; the CLI only says so.
const (
	forwardingMX  = "mx3.name.com–mx8.name.com"
	forwardingSPF = "v=spf1 a mx ~all"
)

// Cmd is the `namecom email` parent command.
var Cmd = &cobra.Command{
	Use:   "email",
	Short: "Forward email addresses to external mailboxes",
}

var (
	createEmailTo string
	updateEmailTo string
	listAll       bool
)

var listCmd = &cobra.Command{
	Use:     "list <domain>",
	Aliases: []string{"ls"},
	Short:   "List email forwarding entries",
	Example: `  namecom email list example.com
  namecom email list example.com --all`,
	Args:              cmdutil.ExactArgs(1),
	RunE:              runList,
	ValidArgsFunction: cmdutil.CompleteDomains,
}

var getCmd = &cobra.Command{
	Use:               "get <domain> <mailbox>",
	Short:             "Get an email forwarding entry",
	Example:           `  namecom email get example.com info`,
	Args:              cmdutil.ExactArgs(2),
	RunE:              runGet,
	ValidArgsFunction: cmdutil.CompleteDomains,
}

var createCmd = &cobra.Command{
	Use:     "create <domain> <mailbox>",
	Aliases: []string{"add"},
	Short:   "Create an email forwarding entry",
	Long: `Forward mail sent to <mailbox>@<domain> to another address. <mailbox> is the
part before the @: info, for info@example.com.

A mailbox that already forwards elsewhere is left as it is, and create fails
(exit 1) naming where it forwards; 'namecom email update' changes it.

To deliver forwarded mail, name.com adds DNS records to <domain> when they
are missing: MX records for ` + forwardingMX + ` and the SPF record "` + forwardingSPF + `".
On a domain that already receives mail elsewhere, such as Google Workspace or
Microsoft 365, these compete with its own MX records. Deleting the mailbox
leaves them; 'namecom dns delete' removes them.`,
	Example: `  namecom email create example.com info --to you@gmail.com
  namecom email create example.com support --to team@example.com`,
	Args:              cmdutil.ExactArgs(2),
	RunE:              runCreate,
	ValidArgsFunction: cmdutil.CompleteDomains,
}

var updateCmd = &cobra.Command{
	Use:               "update <domain> <mailbox>",
	Short:             "Update an email forwarding entry",
	Long:              `Change the address mail sent to <mailbox>@<domain> is forwarded to.`,
	Example:           `  namecom email update example.com info --to newemail@gmail.com`,
	Args:              cmdutil.ExactArgs(2),
	RunE:              runUpdate,
	ValidArgsFunction: cmdutil.CompleteDomains,
}

var deleteCmd = &cobra.Command{
	Use:               "delete <domain> <mailbox>",
	Aliases:           []string{"rm"},
	Short:             "Delete an email forwarding entry",
	Long:              `Stop forwarding mail sent to <mailbox>@<domain>.`,
	Example:           `  namecom email delete example.com info`,
	Args:              cmdutil.ExactArgs(2),
	RunE:              runDelete,
	ValidArgsFunction: cmdutil.CompleteDomains,
}

func init() {
	cmdutil.AddPageFlags(listCmd, &listAll, &listPage, &listLimit, "forwardings")

	createCmd.Flags().StringVar(&createEmailTo, "to", "", "destination email address "+cmdutil.PromptedRequired)
	updateCmd.Flags().StringVar(&updateEmailTo, "to", "", "new destination email address "+cmdutil.PromptedRequired)

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

	spin := out.StartSpinner("Fetching email forwardings…")
	page := listPage
	var all []*coreapigo.EmailForwarding
	var hasMore bool
	var nextPage int
	var lastResult *coreapigo.ListEmailForwardingsResponse
	for {
		result, err := client.SDK().EmailForwardings.ListEmailForwardings(cmd.Context(),
			&coreapigo.ListEmailForwardingsRequest{DomainName: domain, Page: &page, PerPage: paging.PerPage})
		if page == listPage && cmdutil.PageOutOfRange(page, err) {
			lastResult = &coreapigo.ListEmailForwardingsResponse{}
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
		if page == listPage && cmdutil.PastLastPage(page, paging.PerPage, len(result.EmailForwarding), 0, result.LastPage) {
			lastResult = &coreapigo.ListEmailForwardingsResponse{}
			break
		}
		all = append(all, cmdutil.NonNil(result.EmailForwarding)...)
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
		spin.Update(fmt.Sprintf("Fetching email forwardings… (page %d, %d so far)", page, len(all)))
	}
	spin.Stop()

	if out.QuietMode {
		boxes := make([]string, 0, len(all))
		for _, e := range all {
			boxes = append(boxes, e.EmailBox)
		}
		out.PrintQuiet(boxes)
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
		headers := []string{"MAILBOX", "FORWARDS TO"}
		if len(all) == 0 {
			cmdutil.EmptyPage(out, listPage, headers, "email forwarding", fmt.Sprintf("Run 'namecom email create %s <mailbox> --to dest@example.com' to add one", domain))
			return nil
		}
		out.Table(
			headers,
			emailRows(all),
		)
		if hasMore {
			out.Count(len(all), "forwarding", cmdutil.MorePages(nextPage))
		} else {
			out.Count(len(all), "forwarding")
		}
	}
	return nil
}

func runGet(cmd *cobra.Command, args []string) error {
	out := cmdutil.Out(cmd)
	client := cmdutil.APIClient(cmd)

	domain, err := cmdutil.DomainArg(args, 0)
	if err != nil {
		return err
	}
	stop := out.Spin("Fetching email forwarding…")
	entry, err := client.SDK().EmailForwardings.GetEmailForwarding(cmd.Context(),
		&coreapigo.GetEmailForwardingRequest{DomainName: domain, EmailBox: args[1]})
	stop()
	if cmdutil.IsNotFound(err) {
		return mailboxNotFound(err, args[1], domain)
	}
	if err != nil {
		return api.FromSDKError(err)
	}
	if err := cmdutil.RequireField("the mailbox", entry.EmailBox); err != nil {
		return err
	}

	// Quiet prints the mailbox, as list -q does.
	if out.QuietMode {
		out.Quiet(entry.EmailBox)
		return nil
	}

	switch out.Format {
	case output.FormatJSON:
		return out.JSON(entry)
	case output.FormatYAML:
		return out.YAML(entry)
	default:
		out.Table(
			[]string{"MAILBOX", "FORWARDS TO"},
			emailRows([]*coreapigo.EmailForwarding{entry}),
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
	mailbox := args[1]

	if err := cmdutil.ValidEmailLocalPart(mailbox, "mailbox"); err != nil {
		return err
	}

	if createEmailTo == "" {
		if !output.IsInteractive() {
			return cmdutil.RequiredFlags(true, "to")
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
			return cmdutil.FormError(err)
		}
	}

	if err := cmdutil.ValidEmail(createEmailTo, "to"); err != nil {
		return err
	}

	body := coreapigo.CreateEmailForwardingRequest{
		DomainName: domain,
		EmailBox:   mailbox,
		EmailTo:    createEmailTo,
	}

	var entry *coreapigo.EmailForwarding
	sent, err := cmdutil.RunWrite(cmd, cmdutil.Write[coreapigo.CreateEmailForwardingRequest]{
		Method: "POST",
		Path:   fmt.Sprintf("/core/v1/domains/%s/email/forwarding", domain),
		Body:   body,
		Spin:   "Creating email forwarding…",
	}, func(ctx context.Context, body coreapigo.CreateEmailForwardingRequest) error {
		var err error
		entry, err = client.SDK().EmailForwardings.CreateEmailForwarding(ctx, &body)
		return api.FromSDKError(err)
	})
	if err != nil || !sent {
		return err
	}

	// The API answers a create for a mailbox that already exists with 200 and
	// the existing entry, unchanged (#283). The response is the only sign, so
	// it is compared with the request rather than spending a GET up front. A
	// mailbox that already forwarded to the same address reads as a success:
	// it is then forwarding where it was asked to.
	box, to := mailbox, createEmailTo
	if entry != nil {
		if entry.EmailBox != "" {
			box = entry.EmailBox
		}
		if entry.EmailTo != "" {
			to = entry.EmailTo
		}
	}
	if !strings.EqualFold(to, createEmailTo) {
		return &cmdutil.ConflictError{
			Err:     fmt.Errorf("mailbox %s@%s already forwards to %s: nothing was changed", box, domain, to),
			Hint:    fmt.Sprintf("run 'namecom email update %s %s --to %s' to forward it to %s instead", domain, box, createEmailTo, createEmailTo),
			Details: entry,
		}
	}

	// The mailbox is what get, update and delete take.
	if out.QuietMode {
		out.Quiet(box)
		return nil
	}

	switch out.Format {
	case output.FormatJSON:
		return out.JSON(entry)
	case output.FormatYAML:
		return out.YAML(entry)
	default:
		// From the response, so the line shows what the API stored.
		out.Success(fmt.Sprintf("Created forwarding %s@%s → %s", box, domain, to))
		// Said rather than checked: a DNS list would be a second request on
		// every create, to report records the help already describes.
		out.Note(fmt.Sprintf("name.com adds MX records (%s) and the SPF record %q to %s if they are missing — run 'namecom dns list %s --host @' to see them",
			forwardingMX, forwardingSPF, domain, domain))
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
	mailbox := args[1]

	if updateEmailTo == "" {
		if !output.IsInteractive() {
			return cmdutil.RequiredFlags(true, "to")
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
			return cmdutil.FormError(err)
		}
	}

	if err := cmdutil.ValidEmail(updateEmailTo, "to"); err != nil {
		return err
	}

	body := coreapigo.EmailForwardingsUpdateEmailForwardingBody{
		DomainName: domain,
		EmailBox:   mailbox,
		EmailTo:    updateEmailTo,
	}

	// The mailbox is escaped as the SDK escapes it, so --dry-run shows the
	// path that is sent (#187); a "/" in it used to preview unescaped.
	var entry *coreapigo.EmailForwarding
	sent, err := cmdutil.RunWrite(cmd, cmdutil.Write[coreapigo.EmailForwardingsUpdateEmailForwardingBody]{
		Method: "PUT",
		Path:   fmt.Sprintf("/core/v1/domains/%s/email/forwarding/%s", domain, url.PathEscape(mailbox)),
		Body:   body,
		Spin:   "Updating email forwarding…",
	}, func(ctx context.Context, body coreapigo.EmailForwardingsUpdateEmailForwardingBody) error {
		var err error
		entry, err = client.SDK().EmailForwardings.UpdateEmailForwarding(ctx, &body)
		return api.FromSDKError(err)
	})
	if cmdutil.IsNotFound(err) {
		return mailboxNotFound(err, mailbox, domain)
	}
	if err != nil || !sent || out.Quiet() {
		return err
	}

	switch out.Format {
	case output.FormatJSON:
		return out.JSON(entry)
	case output.FormatYAML:
		return out.YAML(entry)
	default:
		out.Success(fmt.Sprintf("Updated forwarding %s@%s → %s", mailbox, domain, updateEmailTo))
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
	mailbox := args[1]

	// The prompt says where the mailbox forwards, as url and dns delete say
	// what they remove (#286). That takes a GET, so it is made only when the
	// prompt will be shown: --yes, --dry-run and a script without a terminal
	// send what they did before. A missing mailbox fails here, before asking.
	prompt := fmt.Sprintf("Delete forwarding for %s@%s?", mailbox, domain)
	if !cmdutil.IsYes(cmd) && !cmdutil.IsDryRun(cmd) && output.IsInteractive() {
		stop := out.Spin("Fetching email forwarding…")
		current, err := client.SDK().EmailForwardings.GetEmailForwarding(cmd.Context(),
			&coreapigo.GetEmailForwardingRequest{DomainName: domain, EmailBox: mailbox})
		stop()
		if cmdutil.IsNotFound(err) {
			return mailboxNotFound(err, mailbox, domain)
		}
		if err != nil {
			return api.FromSDKError(err)
		}
		if current != nil && current.EmailTo != "" {
			prompt = fmt.Sprintf("Delete forwarding %s@%s → %s?", mailbox, domain, current.EmailTo)
		}
	}

	// Escaped as the SDK escapes it, so --dry-run shows the path sent (#187).
	sent, err := cmdutil.RunWrite(cmd, cmdutil.Write[cmdutil.NoBody]{
		Method: "DELETE",
		Path:   fmt.Sprintf("/core/v1/domains/%s/email/forwarding/%s", domain, url.PathEscape(mailbox)),
		Prompt: prompt,
		Spin:   "Deleting email forwarding…",
	}, func(ctx context.Context, _ cmdutil.NoBody) error {
		return api.FromSDKError(client.SDK().EmailForwardings.DeleteEmailForwarding(ctx,
			&coreapigo.DeleteEmailForwardingRequest{DomainName: domain, EmailBox: mailbox}))
	})
	if cmdutil.IsNotFound(err) {
		return mailboxNotFound(err, mailbox, domain)
	}
	if err != nil || !sent {
		return err
	}
	out.Success(fmt.Sprintf("Deleted forwarding for %s@%s", mailbox, domain))
	// The API leaves the records it added for forwarding (#286). Whether
	// another mailbox still needs them is not known without a list, so this
	// says what stays and how to remove it rather than offering to.
	out.Note(fmt.Sprintf("the MX and SPF records name.com added for forwarding stay on %s — once no mailbox forwards, remove them with 'namecom dns delete %s <id>' (see 'namecom dns list %s --host @')",
		domain, domain, domain))
	return nil
}

// mailboxNotFound is the not-found error for a mailbox, worded as dns and
// url word theirs (#286). It still exits 4.
func mailboxNotFound(err error, mailbox, domain string) error {
	return cmdutil.NotFound(err, fmt.Sprintf("mailbox %s@%s not found", mailbox, domain),
		fmt.Sprintf("run 'namecom email list %s' to see its mailboxes", domain))
}

func emailRows(entries []*coreapigo.EmailForwarding) [][]string {
	rows := make([][]string, 0, len(entries))
	for _, e := range entries {
		rows = append(rows, []string{
			e.EmailBox + "@" + e.DomainName,
			e.EmailTo,
		})
	}
	return rows
}
