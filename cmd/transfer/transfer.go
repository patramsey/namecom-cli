// Package transfer implements the `namecom transfer` command group.
package transfer

import (
	"errors"
	"fmt"
	"net/http"
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
	createWatch    bool

	internalAuthCode string
)

var listAll bool

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List transfers",
	Example: `  namecom transfer list         # active/recent transfers (first page)
  namecom transfer list --all   # full transfer history`,
	Args: cobra.NoArgs,
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
	Short: "Initiate a transfer in from another registrar",
	Example: `  namecom transfer create example.com --auth-code XXXXXX
  namecom transfer create example.com --auth-code XXXXXX --privacy`,
	Args: cmdutil.ExactArgs(1),
	RunE: runCreate,
}

var internalCmd = &cobra.Command{
	Use:   "internal-in <domain>",
	Short: "Move a domain between name.com accounts (enterprise resellers only)",
	Long: `Move a domain between name.com accounts without the usual EPP transfer wait.

Requires an approved enterprise reseller account: the spec states "Restricted to
approved enterprise resellers; other callers receive 403 Forbidden." Contact
name.com support to request access.

The losing account must unlock the domain and supply the authorization code from
the name.com dashboard first — this command cannot do either.`,
	Example: `  namecom transfer internal-in example.com --auth-code XXXXXX`,
	Args:    cmdutil.ExactArgs(1),
	RunE:    runInternalIn,
}

var cancelCmd = &cobra.Command{
	Use:               "cancel <domain>",
	Short:             "Cancel an in-progress transfer",
	Example:           `  namecom transfer cancel example.com`,
	Args:              cmdutil.ExactArgs(1),
	RunE:              runCancel,
	ValidArgsFunction: cmdutil.CompleteDomains,
}

var cancelOutboundCmd = &cobra.Command{
	Use:               "cancel-outbound <domain>",
	Short:             "Cancel an outbound transfer-out",
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
	createCmd.Flags().StringVar(&createAuthCode, "auth-code", "", "transfer authorization code")
	createCmd.Flags().BoolVar(&createPrivacy, "privacy", false, "purchase WHOIS privacy with transfer")
	createCmd.Flags().Float64Var(&createPrice, "price", 0, "purchase price for premium domain transfers")
	createCmd.Flags().BoolVar(&createWatch, "watch", false, "poll transfer status every 5 minutes until complete or failed")

	internalCmd.Flags().StringVar(&internalAuthCode, "auth-code", "", "transfer authorization code")

	listCmd.Flags().BoolVar(&listAll, "all", false, "fetch all pages (full transfer history)")

	cmdutil.GroupCmd(Cmd)
	Cmd.AddCommand(listCmd, getCmd, createCmd, internalCmd, cancelCmd, cancelOutboundCmd, eligibilityCmd)
}

func runList(cmd *cobra.Command, _ []string) error {
	out := cmdutil.Out(cmd)
	client := cmdutil.APIClient(cmd)

	spin := out.StartSpinner("Fetching transfers…")
	page := 1
	var transfers []*coreapigo.Transfer
	var hasMore bool
	var lastResult *coreapigo.ListTransfersResponse
	for {
		result, err := client.SDK().Transfers.ListTransfers(cmd.Context(),
			&coreapigo.ListTransfersRequest{Page: &page})
		if err != nil {
			spin.Stop()
			return err
		}
		transfers = append(transfers, result.Transfers...)
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
		out.Count(len(transfers), "transfer")
		if hasMore {
			out.Hint("Showing first page — pass --all for full transfer history")
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
			return fmt.Errorf("no transfer found for %q — run 'namecom transfer list' to see active transfers", domain)
		}
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
	yes := cmdutil.IsYes(cmd)
	dryRun := cmdutil.IsDryRun(cmd)
	domain, err := cmdutil.DomainArg(args, 0)
	if err != nil {
		return err
	}

	// If --auth-code not supplied and we're interactive, prompt for it via form.
	if createAuthCode == "" {
		if !output.IsInteractive() {
			return fmt.Errorf("--auth-code is required (or set interactively in a TTY)")
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
			if errors.Is(err, huh.ErrUserAborted) {
				out.Warn("aborted")
				return nil
			}
			return err
		}
	}

	if err := cmdutil.ValidAuthCode(createAuthCode); err != nil {
		return err
	}

	// Quote the transfer before asking. register/renew both show the amount in
	// their prompt; transfer asked only "Initiate transfer of X?", so the user
	// approved a charge they had never seen. A pricing failure must not block
	// the transfer — fall back to an unpriced prompt.
	priceMsg := ""
	if pricing, perr := client.SDK().Domains.GetPricingForDomain(cmd.Context(),
		&coreapigo.GetPricingForDomainRequest{DomainName: domain}); perr == nil {
		if pricing.TransferPrice != nil {
			priceMsg = fmt.Sprintf(" for $%.2f", *pricing.TransferPrice)
			if createPrivacy {
				priceMsg += " plus WHOIS privacy"
			}
		}
	}

	ok, err := confirm(out, yes, fmt.Sprintf("Initiate transfer of %s%s?", domain, priceMsg))
	if err != nil {
		return err
	}
	if !ok {
		out.Warn("aborted")
		return nil
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

	if dryRun {
		// Preview the real body rather than a nil, but never the auth code: it
		// is the secret that authorises moving the domain, and --dry-run output
		// lands in terminal scrollback and CI logs. Everything else is worth
		// seeing — purchasePrice especially, since this command spends money.
		preview := body
		preview.AuthCode = "[redacted]"
		out.DryRun("POST", "/core/v1/transfers", preview)
		return nil
	}

	stop := out.Spin("Initiating transfer…")
	result, err := client.SDK().Transfers.CreateTransfer(cmd.Context(), &body)
	stop()
	if err != nil {
		return err
	}

	// Render the result, then fall through to --watch. These branches used to
	// `return` directly, which made --watch unreachable in JSON/YAML mode — i.e.
	// in every pipe, since JSON is the default for non-TTY stdout. The flag
	// exists for automation and did nothing in exactly the automation case.
	switch out.Format {
	case output.FormatJSON:
		if err := out.JSON(result); err != nil {
			return err
		}
	case output.FormatYAML:
		if err := out.YAML(result); err != nil {
			return err
		}
	default:
		out.Success(fmt.Sprintf("Transfer initiated for %s (order #%d, total $%.2f)",
			domain, result.Order, result.TotalPaid))
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
	progress := out.Writer
	if out.Format != output.FormatTable {
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
	yes := cmdutil.IsYes(cmd)
	dryRun := cmdutil.IsDryRun(cmd)
	domain, err := cmdutil.DomainArg(args, 0)
	if err != nil {
		return err
	}

	if internalAuthCode == "" {
		if !output.IsInteractive() {
			return fmt.Errorf("--auth-code is required (or set interactively in a TTY)")
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
			if errors.Is(err, huh.ErrUserAborted) {
				out.Warn("aborted")
				return nil
			}
			return err
		}
	}

	if err := cmdutil.ValidAuthCode(internalAuthCode); err != nil {
		return err
	}

	ok, err := confirm(out, yes, fmt.Sprintf("Transfer %s from another name.com account?", domain))
	if err != nil {
		return err
	}
	if !ok {
		out.Warn("aborted")
		return nil
	}

	body := coreapigo.CreateInternalTransferInRequest{
		DomainName: domain,
		AuthCode:   internalAuthCode,
	}

	if dryRun {
		// Same redaction as the external transfer above.
		preview := body
		preview.AuthCode = "[redacted]"
		out.DryRun("POST", "/core/v1/transfers/internal/in", preview)
		return nil
	}

	t, err := client.SDK().Transfers.CreateInternalTransferIn(cmd.Context(), &body)
	if err != nil {
		err = api.FromSDKError(err)
		// A 403 here almost always means the account is not on the enterprise
		// allowlist rather than that the credentials are wrong. Say so, instead
		// of letting the generic "check your credentials" hint send the user off
		// to re-enter a token that was fine.
		var apiErr *api.APIError
		if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusForbidden {
			return fmt.Errorf("internal transfer-in requires an approved enterprise reseller account "+
				"— contact name.com support to request access (original error: %w)", err)
		}
		return err
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
	yes := cmdutil.IsYes(cmd)
	dryRun := cmdutil.IsDryRun(cmd)
	domain, err := cmdutil.DomainArg(args, 0)
	if err != nil {
		return err
	}

	if dryRun {
		out.DryRun("POST", fmt.Sprintf("/core/v1/transfers/%s:cancel", domain), nil)
		return nil
	}

	ok, err := confirm(out, yes, fmt.Sprintf("Cancel transfer of %s?", domain))
	if err != nil {
		return err
	}
	if !ok {
		out.Warn("aborted")
		return nil
	}

	stop := out.Spin("Cancelling transfer…")
	_, err = client.SDK().Transfers.CancelTransfer(cmd.Context(),
		&coreapigo.CancelTransferRequest{DomainName: domain, Body: &coreapigo.EmptyObject{}})
	stop()
	if err != nil {
		return api.FromSDKError(err)
	}
	out.Success(fmt.Sprintf("Cancelled transfer of %s", domain))
	out.Hint("Run 'namecom transfer list' to see remaining active transfers")
	return nil
}

func runCancelOutbound(cmd *cobra.Command, args []string) error {
	out := cmdutil.Out(cmd)
	client := cmdutil.APIClient(cmd)
	yes := cmdutil.IsYes(cmd)
	dryRun := cmdutil.IsDryRun(cmd)
	domain, err := cmdutil.DomainArg(args, 0)
	if err != nil {
		return err
	}

	if dryRun {
		out.DryRun("POST", fmt.Sprintf("/core/v1/transfers/external/out/%s:cancel", domain), nil)
		return nil
	}

	ok, err := confirm(out, yes, fmt.Sprintf("Cancel outbound transfer of %s?", domain))
	if err != nil {
		return err
	}
	if !ok {
		out.Warn("aborted")
		return nil
	}

	result, err := client.SDK().Transfers.CancelOutboundTransfer(cmd.Context(),
		&coreapigo.CancelOutboundTransferRequest{DomainName: domain, Body: &coreapigo.EmptyObject{}})
	if err != nil {
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

	switch out.Format {
	case output.FormatJSON:
		return out.JSON(result)
	case output.FormatYAML:
		return out.YAML(result)
	default:
		out.Table([]string{"DOMAIN", "AT NAME.COM", "SUPPORTS INTERNAL"}, [][]string{
			{result.DomainName, out.BoolBadge(result.AtName), out.BoolBadge(result.SupportsInternalTransfer)},
		})
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

func confirm(out *output.Config, yes bool, msg string) (bool, error) {
	return cmdutil.Confirm(out, yes, msg)
}
