// Package transfer implements the `namecom transfer` command group.
package transfer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/charmbracelet/huh"
	coreapigo "github.com/namedotcom/core-api-go"
	"github.com/patramsey/namecom-cli/cmd/cmdutil"
	"github.com/patramsey/namecom-cli/internal/api"
	"github.com/patramsey/namecom-cli/internal/output"
	"github.com/spf13/cobra"
)

// Cmd is the `namecom transfer` parent command.
var Cmd = &cobra.Command{
	Use:   "transfer",
	Short: "Transfer domains in from or out to other registrars",
}

var (
	createAuthCode string
	createPrivacy  bool
	createPrice    float64
	createMaxPrice float64
	createAccept   bool
	createWatch    bool
	// createContactsFile and internalContactsFile name a ContactsRequest JSON
	// file, the format `domain register --contacts-file` takes.
	createContactsFile string

	internalAuthCode     string
	internalContactsFile string
)

var listAll bool

var listCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"ls"},
	Short:   "List transfers",
	Example: `  namecom transfer list         # active/recent transfers (first page)
  namecom transfer list --all   # full transfer history`,
	Args: cmdutil.NoArgs,
	RunE: runList,
}

var getCmd = &cobra.Command{
	Use:               "get <domain>",
	Short:             "Get a transfer's status",
	Example:           `  namecom transfer get example.com`,
	Args:              cmdutil.ExactArgs(1),
	RunE:              runGet,
	ValidArgsFunction: cmdutil.CompleteDomains,
}

var createCmd = &cobra.Command{
	Use:   "create <domain>",
	Short: "Start a transfer in from another registrar",
	Example: `  namecom transfer create example.com --auth-code XXXXXX
  namecom transfer create example.com --auth-code XXXXXX --privacy

  # Apply WHOIS contacts on arrival instead of the account defaults. Changing
  # contacts may start a registrar transfer lock, per account settings.
  namecom transfer create example.com --auth-code XXXXXX --contacts-file contacts.json`,
	Args: cmdutil.ExactArgs(1),
	// A domain to transfer in is held elsewhere, not in this account (#187).
	ValidArgsFunction: cobra.NoFileCompletions,
	RunE:              runCreate,
}

var internalCmd = &cobra.Command{
	Use:   "internal-in <domain>",
	Short: "Move a domain from another name.com account into this one (enterprise resellers only)",
	Long: `Move a domain from another name.com account into this one, without the usual
EPP transfer wait.

Requires an approved enterprise reseller account: the spec states "Restricted to
approved enterprise resellers; other callers receive 403 Forbidden." Contact
name.com support to request access.

The losing account must unlock the domain and supply the authorization code from
the name.com dashboard first — this command cannot do either.`,
	Example: `  namecom transfer internal-in example.com --auth-code XXXXXX
  namecom transfer internal-in example.com --auth-code XXXXXX --contacts-file contacts.json`,
	Args: cmdutil.ExactArgs(1),
	RunE: runInternalIn,
}

var cancelCmd = &cobra.Command{
	Use:   "cancel <domain>",
	Short: "Cancel a transfer in to name.com that is still in progress",
	Long: `Cancel a transfer in to name.com started with 'transfer create' that has not
completed. To stop a domain leaving name.com for another registrar, use
'transfer cancel-outbound'.`,
	Example:           `  namecom transfer cancel example.com`,
	Args:              cmdutil.ExactArgs(1),
	RunE:              runCancel,
	ValidArgsFunction: cmdutil.CompleteDomains,
}

var cancelOutboundCmd = &cobra.Command{
	Use:   "cancel-outbound <domain>",
	Short: "Stop a domain transferring out to another registrar",
	Long: `Cancel a pending transfer of a domain from name.com to another registrar, so
the domain stays here. To cancel a transfer in to name.com, use
'transfer cancel'.`,
	Example:           `  namecom transfer cancel-outbound example.com`,
	Args:              cmdutil.ExactArgs(1),
	RunE:              runCancelOutbound,
	ValidArgsFunction: cmdutil.CompleteDomains,
}

var eligibilityCmd = &cobra.Command{
	Use:               "eligibility <domain>",
	Short:             "Check if a domain is eligible for transfer",
	Example:           `  namecom transfer eligibility example.com`,
	Args:              cmdutil.ExactArgs(1),
	RunE:              runEligibility,
	ValidArgsFunction: cmdutil.CompleteDomains,
}

func init() {
	createCmd.Flags().StringVar(&createAuthCode, "auth-code", "", "transfer authorization code "+cmdutil.PromptedRequired)
	createCmd.Flags().BoolVar(&createPrivacy, "privacy", false, "include WHOIS privacy (free) with the transfer")
	createCmd.Flags().Float64Var(&createPrice, "price", 0, "purchase price in USD to send as purchasePrice, "+
		"which a premium domain's transfer requires; not a cap, see --max-price")
	createCmd.Flags().Float64Var(&createMaxPrice, "max-price", 0, cmdutil.MaxPriceUsage)
	createCmd.Flags().BoolVar(&createAccept, "accept-premium", false, cmdutil.AcceptPremiumUsage)
	createCmd.Flags().BoolVar(&createWatch, "watch", false, "poll transfer status every 5 minutes until complete or failed")
	createCmd.Flags().StringVar(&createContactsFile, "contacts-file", "", contactsFileUsage)

	internalCmd.Flags().StringVar(&internalAuthCode, "auth-code", "", "transfer authorization code "+cmdutil.PromptedRequired)
	internalCmd.Flags().StringVar(&internalContactsFile, "contacts-file", "", contactsFileUsage)

	cmdutil.AddPageFlags(listCmd, &listAll, &listPage, &listLimit, "transfer")

	cmdutil.GroupCmd(Cmd)
	cmdutil.MarkWrite(createCmd, internalCmd, cancelCmd, cancelOutboundCmd)
	Cmd.AddCommand(listCmd, getCmd, createCmd, internalCmd, cancelCmd, cancelOutboundCmd, eligibilityCmd)
}

var listPage, listLimit int

func runList(cmd *cobra.Command, _ []string) error {
	out := cmdutil.Out(cmd)
	client := cmdutil.APIClient(cmd)

	if err := cmdutil.ValidPage(listPage, listLimit); err != nil {
		return err
	}

	spin := out.StartSpinner("Fetching transfers…")
	page := listPage
	var transfers []*coreapigo.Transfer
	var hasMore bool
	var nextPage int
	var lastResult *coreapigo.ListTransfersResponse
	for {
		result, err := client.SDK().Transfers.ListTransfers(cmd.Context(),
			&coreapigo.ListTransfersRequest{Page: &page, PerPage: cmdutil.PerPage(listLimit)})
		if err != nil {
			spin.Stop()
			return err
		}
		transfers = append(transfers, cmdutil.NonNil(result.Transfers)...)
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
		spin.Update(fmt.Sprintf("Fetching transfers… (page %d, %d so far)", page, len(transfers)))
	}
	spin.Stop()

	if out.QuietMode {
		names := make([]string, 0, len(transfers))
		for _, t := range transfers {
			names = append(names, t.DomainName)
		}
		out.PrintQuiet(names)
		return nil
	}

	switch out.Format {
	case output.FormatJSON:
		var np *int32
		if hasMore {
			np = cmdutil.Int32Page(lastResult.NextPage)
		}
		return out.JSONList(transfers, np, 0)
	case output.FormatYAML:
		var np *int32
		if hasMore {
			np = cmdutil.Int32Page(lastResult.NextPage)
		}
		return out.YAMLList(transfers, np, 0)
	default:
		if len(transfers) == 0 {
			out.Empty("transfer", "Run 'namecom transfer create <domain> --auth-code XXXXXX' to initiate a transfer")
			return nil
		}
		out.Table(
			[]string{"DOMAIN", "STATUS"},
			transferRows(out, transfers),
		)
		if hasMore {
			out.Count(len(transfers), "transfer", cmdutil.MorePages(nextPage))
		} else {
			out.Count(len(transfers), "transfer")
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
	stop := out.Spin("Fetching transfer…")
	t, err := client.SDK().Transfers.GetTransfer(cmd.Context(),
		&coreapigo.GetTransferRequest{DomainName: domain})
	stop()
	if err != nil {
		if cmdutil.IsNotFound(err) {
			return cmdutil.NotFound(err, fmt.Sprintf("no transfer found for %q — run 'namecom transfer list' to see active transfers", domain))
		}
		return err
	}
	if err := cmdutil.RequireField("the domain name", t.DomainName); err != nil {
		return err
	}

	// --quiet prints the identifying value only, matching list commands.
	if out.QuietMode {
		out.PrintQuiet([]string{t.DomainName})
		return nil
	}

	switch out.Format {
	case output.FormatJSON:
		return out.JSON(t)
	case output.FormatYAML:
		return out.YAML(t)
	default:
		out.Table(
			[]string{"DOMAIN", "STATUS"},
			transferRows(out, []*coreapigo.Transfer{t}),
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

	if cmd.Flags().Changed("price") {
		if err := cmdutil.ValidPrice(createPrice); err != nil {
			return err
		}
	}
	if err := cmdutil.ValidMaxPrice(cmd, createMaxPrice); err != nil {
		return err
	}

	contacts, err := readContactsFlag(createContactsFile)
	if err != nil {
		return err
	}

	// If --auth-code not supplied and we're interactive, prompt for it via form.
	if createAuthCode == "" {
		if !output.IsInteractive() {
			return cmdutil.RequiredFlags(true, "auth-code")
		}
		form := huh.NewForm(
			huh.NewGroup(
				huh.NewInput().
					Title("Transfer Auth Code").
					Description("The EPP authorization code from your current registrar — kept out of shell history").
					EchoMode(huh.EchoModePassword).
					Value(&createAuthCode).
					Validate(func(s string) error {
						if s == "" {
							return errors.New("auth code is required")
						}
						return nil
					}),
			),
		)
		if err := form.Run(); err != nil {
			return cmdutil.FormError(err)
		}
	}

	if err := cmdutil.ValidAuthCode(createAuthCode); err != nil {
		return err
	}

	// Quote the transfer before asking. register/renew both show the amount in
	// their prompt; transfer asked only "Initiate transfer of X?", so the user
	// approved a charge they had never seen. A pricing failure must not block
	// the transfer — fall back to an unpriced prompt.
	var quoted *float64
	premium := false
	if pricing, perr := client.SDK().Domains.GetPricingForDomain(cmd.Context(),
		&coreapigo.GetPricingForDomainRequest{DomainName: domain}); perr == nil {
		quoted = pricing.TransferPrice
		premium = pricing.GetPremium()
	}

	body := coreapigo.CreateTransferRequest{
		DomainName: domain,
		AuthCode:   createAuthCode,
	}
	if createPrivacy {
		body.PrivacyEnabled = &createPrivacy
	}
	if createPrice > 0 {
		body.PurchasePrice = &createPrice
	}
	body.Contacts = contacts

	// The spending gates (#226). Without a quote, --max-price has nothing to
	// compare unless --price set the amount, and refuses; a transfer whose
	// premium status is unknown is not gated, as before.
	charged := quoted
	if body.PurchasePrice != nil {
		charged = body.PurchasePrice
	}
	if err := cmdutil.CheckMaxPrice(cmd, createMaxPrice, "transferring "+domain, charged); err != nil {
		return err
	}
	if premium {
		desc := fmt.Sprintf("transferring %s costs an unquoted price (premium)", domain)
		if charged != nil {
			desc = fmt.Sprintf("transferring %s costs %s (premium)", domain, output.Money(*charged))
		}
		if cmdutil.IsDryRun(cmd) && !createAccept {
			out.Hint(desc + "; transferring it without the interactive prompt will require --accept-premium")
		}
		if err := cmdutil.RequireAcceptPremium(cmd, createAccept, desc); err != nil {
			return err
		}
	}

	// RunWrite skips the prompt under --dry-run: nothing will be sent, and in
	// a script Confirm hard-errors without --yes, which made --dry-run
	// unusable in CI.
	var result *coreapigo.CreateTransferResponse
	sent, err := cmdutil.RunWrite(cmd, cmdutil.Write[coreapigo.CreateTransferRequest]{
		Method:  "POST",
		Path:    "/core/v1/transfers",
		Body:    body,
		Preview: redactTransferAuthCode,
		Prompt:  transferPrompt(domain, body, quoted),
		Quote:   cmdutil.ChargeQuote(charged, 0, transferQuoteNote(premium)),
		Spin:    "Initiating transfer…",
	}, func(ctx context.Context, body coreapigo.CreateTransferRequest) error {
		var err error
		result, err = client.SDK().Transfers.CreateTransfer(ctx, &body)
		return err
	})
	if err != nil || !sent {
		return err
	}

	// Render the result, then fall through to --watch. These branches used to
	// `return` directly, which made --watch unreachable in JSON/YAML mode — i.e.
	// in every pipe, since JSON is the default for non-TTY stdout. The flag
	// exists for automation and did nothing in exactly the automation case.
	// Quiet prints the domain: it is what transfer get and cancel take.
	switch {
	case out.Quiet(domain):
	case out.Format == output.FormatJSON:
		if err := out.JSON(result); err != nil {
			return err
		}
	case out.Format == output.FormatYAML:
		if err := out.YAML(result); err != nil {
			return err
		}
	default:
		out.Success(fmt.Sprintf("Transfer initiated for %s (order #%d, total %s)",
			domain, result.Order, output.Money(result.TotalPaid)))
		// Nil-checked because the SDK types this as *Transfer where the
		// generated client used a value. A response without a "transfer" key
		// used to yield an empty status and no status line; unguarded here it
		// is a nil dereference and the command panics instead of printing a
		// successful transfer. Caught by TestRequestShape_Transfer, whose stub
		// omits the key.
		if result.Transfer != nil {
			if s := string(result.Transfer.Status); s != "" {
				fmt.Fprintf(out.Writer, "  status: %s\n", out.StatusBadge(s))
			}
		}
		// The API flags non-blocking registry statuses that may still stall the
		// transfer. JSON output carried these; table output silently dropped them,
		// so a TTY user saw an unqualified success.
		if w := result.Warnings; w != nil {
			if w.Message != nil && *w.Message != "" {
				out.Warn(*w.Message)
			}
			if len(w.Statuses) > 0 {
				out.Warn("registry statuses: " + strings.Join(w.Statuses, ", "))
			}
		}
		out.Hint("Transfers typically take 3–5 days — the gaining registrar and current owner must approve")
		out.Hint(fmt.Sprintf("Run 'namecom transfer get %s' to check status", domain))
	}
	if createWatch {
		return watchTransfer(cmd, out, client, domain)
	}
	return nil
}

// isTerminalTransferStatus reports whether a transfer status is final, using
// the split the spec defines on TransferStatus. Two are counterintuitive and
// worth stating explicitly: `rejected` is NON-terminal (the losing registrar
// rejected it, but the transfer may still progress), while
// `canceled_pending_refund` IS terminal (canceled; only the refund is
// outstanding).
func isTerminalTransferStatus(status string) bool {
	switch coreapigo.TransferStatus(status) {
	case coreapigo.TransferStatusCompleted,
		coreapigo.TransferStatusFailed,
		coreapigo.TransferStatusCanceled,
		coreapigo.TransferStatusCanceledPendingRefund:
		return true
	}
	return false
}

// watchTransfer polls GetTransfer every 5 minutes until it reaches a terminal
// state. Useful in CI/automation — for interactive use the hint is enough.
func watchTransfer(cmd *cobra.Command, out *output.Config, client *api.Client, domain string) error {
	// Progress commentary goes to stderr, not stdout. --watch is reachable in
	// JSON/YAML mode (that is the automation case it exists for), and stdout
	// already carries the create response as a structured document — writing
	// human progress lines into that stream makes it unparseable.
	// Quiet mode keeps it off both: stdout holds only the domain, and the
	// outcome is the exit code plus the warning below for a failed transfer.
	progress := out.Writer
	switch {
	case out.QuietMode:
		progress = io.Discard
	case out.Format != output.FormatTable:
		progress = out.EWriter
	}
	fmt.Fprintf(progress, "\nWatching transfer status — checking every 5 minutes (Ctrl+C to stop)\n")

	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-cmd.Context().Done():
			return nil
		case <-ticker.C:
		}
		stop := out.Spin("Checking transfer status…")
		t, err := client.SDK().Transfers.GetTransfer(cmd.Context(),
			&coreapigo.GetTransferRequest{DomainName: domain})
		stop()
		if err != nil {
			out.Warn(fmt.Sprintf("status check failed: %v", err))
			continue
		}
		status := string(t.Status)
		fmt.Fprintf(progress, "  %s  %s\n", time.Now().Format("15:04"), out.StatusBadge(status))
		if isTerminalTransferStatus(status) {
			if status == "completed" {
				out.Success(fmt.Sprintf("Transfer of %s completed", domain))
			} else {
				out.Warn(fmt.Sprintf("Transfer of %s ended with status: %s", domain, status))
			}
			return nil
		}
	}
}

func runInternalIn(cmd *cobra.Command, args []string) error {
	out := cmdutil.Out(cmd)
	client := cmdutil.APIClient(cmd)
	domain, err := cmdutil.DomainArg(args, 0)
	if err != nil {
		return err
	}

	contacts, err := readContactsFlag(internalContactsFile)
	if err != nil {
		return err
	}

	if internalAuthCode == "" {
		if !output.IsInteractive() {
			return cmdutil.RequiredFlags(true, "auth-code")
		}
		form := huh.NewForm(
			huh.NewGroup(
				huh.NewInput().
					Title("Transfer Auth Code").
					Description("The authorization code from the source name.com account").
					EchoMode(huh.EchoModePassword).
					Value(&internalAuthCode).
					Validate(func(s string) error {
						if s == "" {
							return errors.New("auth code is required")
						}
						return nil
					}),
			),
		)
		if err := form.Run(); err != nil {
			return cmdutil.FormError(err)
		}
	}

	if err := cmdutil.ValidAuthCode(internalAuthCode); err != nil {
		return err
	}

	body := coreapigo.CreateInternalTransferInRequest{
		DomainName: domain,
		AuthCode:   internalAuthCode,
		Contacts:   contacts,
	}

	var t *coreapigo.DomainResponsePayload
	sent, err := cmdutil.RunWrite(cmd, cmdutil.Write[coreapigo.CreateInternalTransferInRequest]{
		Method: "POST",
		Path:   "/core/v1/transfers/internal/in",
		Body:   body,
		// Same redaction as the external transfer above.
		Preview: func(b coreapigo.CreateInternalTransferInRequest) any {
			b.AuthCode = redactedAuthCode
			return b
		},
		Prompt: fmt.Sprintf("Transfer %s from another name.com account%s?", domain, contactsPromptNote(body.Contacts)),
	}, func(ctx context.Context, body coreapigo.CreateInternalTransferInRequest) error {
		var err error
		t, err = client.SDK().Transfers.CreateInternalTransferIn(ctx, &body)
		return err
	})
	if !sent {
		return err
	}
	if err != nil {
		// A 403 here almost always means the account is not on the enterprise
		// allowlist rather than that the credentials are wrong. AsRestricted
		// says so and replaces the "check your credentials" hint, which a
		// plain %w wrap kept (#161).
		return cmdutil.AsRestricted(api.FromSDKError(err), "internal transfer-in", "approved enterprise reseller")
	}

	if out.Quiet(domain) {
		return nil
	}

	switch out.Format {
	case output.FormatJSON:
		return out.JSON(t)
	case output.FormatYAML:
		return out.YAML(t)
	default:
		// No status is reported here, and that is a fix rather than a
		// simplification. This endpoint returns a DomainResponsePayload — the
		// spec says so and the SDK types it that way — which has no status
		// field. The generated client decoded that payload into a Transfer
		// struct, so Status silently stayed empty and the line has always read
		// "(status: )". The hint below is where a caller gets the real answer.
		out.Success(fmt.Sprintf("Internal transfer initiated for %s", domain))
		out.Hint(fmt.Sprintf("Run 'namecom transfer get %s' to check status", domain))
	}
	return nil
}

func runCancel(cmd *cobra.Command, args []string) error {
	out := cmdutil.Out(cmd)
	client := cmdutil.APIClient(cmd)
	domain, err := cmdutil.DomainArg(args, 0)
	if err != nil {
		return err
	}

	// Look the transfer up before asking. The prompt was shown for any name,
	// so "Cancel transfer of typo.com?" could be answered yes before the API
	// said there was nothing to cancel (#235); `domain lock` already checks
	// first. Its status goes in the prompt, so the user sees what they cancel.
	stop := out.Spin("Fetching transfer…")
	t, err := client.SDK().Transfers.GetTransfer(cmd.Context(),
		&coreapigo.GetTransferRequest{DomainName: domain})
	stop()
	if err != nil {
		if cmdutil.IsNotFound(err) {
			return cmdutil.NotFound(err, fmt.Sprintf("no transfer found for %q — run 'namecom transfer list' to see active transfers", domain))
		}
		return err
	}
	prompt := fmt.Sprintf("Cancel transfer of %s?", domain)
	if t != nil && t.Status != "" {
		prompt = fmt.Sprintf("Cancel transfer of %s (status: %s)?", domain, t.Status)
	}

	// NoBody: the {} sent is the SDK's EmptyObject placeholder
	// (namedotcom/core-api-go#8), not a body the user supplies.
	sent, err := cmdutil.RunWrite(cmd, cmdutil.Write[cmdutil.NoBody]{
		Method: "POST",
		Path:   fmt.Sprintf("/core/v1/transfers/%s:cancel", domain),
		Prompt: prompt,
		Spin:   "Cancelling transfer…",
	}, func(ctx context.Context, _ cmdutil.NoBody) error {
		_, err := client.SDK().Transfers.CancelTransfer(ctx,
			&coreapigo.CancelTransferRequest{DomainName: domain, Body: &coreapigo.EmptyObject{}})
		return api.FromSDKError(err)
	})
	if err != nil || !sent {
		return err
	}
	out.Success(fmt.Sprintf("Cancelled transfer of %s", domain))
	return nil
}

func runCancelOutbound(cmd *cobra.Command, args []string) error {
	out := cmdutil.Out(cmd)
	client := cmdutil.APIClient(cmd)
	domain, err := cmdutil.DomainArg(args, 0)
	if err != nil {
		return err
	}

	var result *coreapigo.CancelTransferOutResponse
	sent, err := cmdutil.RunWrite(cmd, cmdutil.Write[cmdutil.NoBody]{
		Method: "POST",
		Path:   fmt.Sprintf("/core/v1/transfers/external/out/%s:cancel", domain),
		Prompt: fmt.Sprintf("Cancel outbound transfer of %s?", domain),
	}, func(ctx context.Context, _ cmdutil.NoBody) error {
		var err error
		result, err = client.SDK().Transfers.CancelOutboundTransfer(ctx,
			&coreapigo.CancelOutboundTransferRequest{DomainName: domain, Body: &coreapigo.EmptyObject{}})
		return err
	})
	if err != nil || !sent || out.Quiet() {
		return err
	}

	switch out.Format {
	case output.FormatJSON:
		return out.JSON(result)
	case output.FormatYAML:
		return out.YAML(result)
	default:
		out.Success(fmt.Sprintf("Cancelled outbound transfer of %s (status: %s)", domain, result.Status))
	}
	return nil
}

func runEligibility(cmd *cobra.Command, args []string) error {
	out := cmdutil.Out(cmd)
	client := cmdutil.APIClient(cmd)
	domain, err := cmdutil.DomainArg(args, 0)
	if err != nil {
		return err
	}

	stop := out.Spin("Checking transfer eligibility…")
	elig, err := client.SDK().Transfers.GetTransferEligibility(cmd.Context(),
		&coreapigo.GetTransferEligibilityRequest{DomainName: domain})
	stop()
	if err != nil {
		return err
	}
	result := elig

	// Quiet prints the domain only when it is at name.com and its TLD supports
	// internal transfer — the case this command exists to find — and nothing
	// otherwise, so `[ -n "$(namecom transfer eligibility d.com -q)" ]` is the
	// test. Echoing the domain unconditionally would tell a script nothing.
	if out.QuietMode {
		if result.AtName && result.SupportsInternalTransfer {
			out.Quiet(result.DomainName)
		}
		return nil
	}

	switch out.Format {
	case output.FormatJSON:
		return out.JSON(result)
	case output.FormatYAML:
		return out.YAML(result)
	default:
		out.Table(eligibilityTable(result))
		if result.AtName {
			// supportsInternalTransfer is a TLD-level flag. The spec is explicit
			// that it "does not reflect per-account allowlist eligibility" — so
			// recommending this unconditionally sent ordinary users to a command
			// that returns 403 for everyone outside the enterprise allowlist.
			out.Hint(fmt.Sprintf("Run 'namecom transfer internal-in %s --auth-code XXXXXX' to transfer "+
				"(requires enterprise reseller approval)", domain))
		} else {
			out.Hint(fmt.Sprintf("Run 'namecom transfer create %s --auth-code XXXXXX' to initiate transfer", domain))
		}
	}
	return nil
}

// eligibilityTable lays out an eligibility result. It showed "AT NAME.COM no"
// beside "SUPPORTS INTERNAL yes", which read as a contradiction (#238): the
// second is a TLD-level flag that matters only for a domain already at
// name.com. REGISTERED AT says where the domain is, and the TLD column
// appears only when it applies.
func eligibilityTable(r *coreapigo.TransferEligibilityResponse) ([]string, [][]string) {
	if !r.AtName {
		return []string{"DOMAIN", "REGISTERED AT"}, [][]string{{r.DomainName, "another registrar"}}
	}
	internal := "yes"
	if !r.SupportsInternalTransfer {
		internal = "no"
	}
	return []string{"DOMAIN", "REGISTERED AT", "TLD ALLOWS INTERNAL TRANSFER"},
		[][]string{{r.DomainName, "name.com (an account)", internal}}
}

func transferRows(out *output.Config, transfers []*coreapigo.Transfer) [][]string {
	rows := make([][]string, 0, len(transfers))
	for _, t := range transfers {
		rows = append(rows, []string{
			t.DomainName,
			out.StatusBadge(string(t.Status)),
		})
	}
	return rows
}

// transferPrompt is the confirmation for a transfer create. It quotes the
// price body carries when --price set one, and the standard transfer price
// otherwise; quoting the standard price unconditionally meant the user
// approved one amount while the request carried another.
//
// It said "plus WHOIS privacy" with no price, which read as an extra charge,
// and never said what the price buys (#235). The SDK documents privacy on a
// transfer as free, and transferPrice as covering the TLD's minimum term; it
// does not promise that term is added to the current expiry, so the prompt
// states only what is documented.
func transferPrompt(domain string, body coreapigo.CreateTransferRequest, quoted *float64) string {
	price := quoted
	if body.PurchasePrice != nil {
		price = body.PurchasePrice
	}
	priceMsg := ""
	if price != nil {
		priceMsg = fmt.Sprintf(" for %s (covers %s)", output.Money(*price), transferTerm)
	}
	if body.PrivacyEnabled != nil && *body.PrivacyEnabled {
		priceMsg += ", with WHOIS privacy at no charge"
	}
	return fmt.Sprintf("Transfer %s in%s%s?", domain, priceMsg, contactsPromptNote(body.Contacts))
}

// transferTerm says what a transfer price covers, as PricingResponse
// documents transferPrice: the TLD's minimum transfer/registration term,
// "typically 1 year". The API does not promise how that term combines with
// the current expiry, so neither does the CLI.
const transferTerm = "the TLD's minimum term, typically 1 year"

// transferQuoteNote is the quote note for a transfer's price.
func transferQuoteNote(premium bool) string {
	if premium {
		return "premium; covers " + transferTerm
	}
	return "covers " + transferTerm
}

// contactsFileUsage is the --contacts-file help for both transfer writes.
const contactsFileUsage = "JSON file of WHOIS contacts to apply, as for 'domain register'; " +
	"omitted roles get account defaults (may start a transfer lock)"

// readContactsFlag reads --contacts-file, or returns nil when it is unset. It
// runs before the auth-code prompt and any request, so a bad file (a usage
// error, exit 2) is reported before the user has typed a secret.
func readContactsFlag(path string) (*coreapigo.ContactsRequest, error) {
	if path == "" {
		return nil, nil
	}
	return cmdutil.ReadContactsFile(path)
}

// contactsPromptNote is appended to a transfer prompt when the body carries
// contacts. The SDK documents that applying them may start a registrar
// contact-change transfer lock, which is worth knowing before approving.
func contactsPromptNote(c *coreapigo.ContactsRequest) string {
	if c == nil {
		return ""
	}
	return " and apply the contacts file (may start a transfer lock)"
}

// redactedAuthCode replaces the auth code in a --dry-run preview. It is the
// placeholder the --debug log uses too, so a secret looks the same in both.
const redactedAuthCode = api.Redacted

// redactTransferAuthCode is the preview of a transfer create: the real body,
// but never the auth code. It is the secret that authorises moving the domain,
// and --dry-run output lands in terminal scrollback and CI logs. Everything
// else is worth seeing — purchasePrice especially, since this command spends
// money.
func redactTransferAuthCode(b coreapigo.CreateTransferRequest) any {
	b.AuthCode = redactedAuthCode
	return b
}
