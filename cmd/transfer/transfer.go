// Package transfer implements the `namecom transfer` command group.
package transfer

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/charmbracelet/huh"
	"github.com/patramsey/namecom-cli/cmd/cmdutil"
	"github.com/patramsey/namecom-cli/internal/api"
	"github.com/patramsey/namecom-cli/internal/api/gen"
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
	var page int32 = 1
	var transfers []gen.Transfer
	var hasMore bool
	var lastResult gen.ListTransfersResponseSchema
	for {
		params := &gen.ListTransfersParams{Page: &page}
		resp, err := client.Gen().ListTransfers(cmd.Context(), params)
		if err != nil {
			spin.Stop()
			return err
		}
		// Fresh variable each iteration: the JSON decoder reuses the existing
		// slice backing array and the pointers inside it, so reusing one target
		// lets page N overwrite values page 1 already appended. It also leaves a
		// stale non-nil NextPage when the final page omits the key, which never
		// terminates. See the same pattern in cmd/dns/dns.go.
		var result gen.ListTransfersResponseSchema
		if err := api.Decode(resp, &result); err != nil {
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
			np = lastResult.NextPage
		}
		return out.JSONList(transfers, np, 0)
	case output.FormatYAML:
		var np *int32
		if hasMore {
			np = lastResult.NextPage
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
	resp, err := client.Gen().GetTransfer(cmd.Context(), domain)
	stop()
	if err != nil {
		if cmdutil.IsNotFound(err) {
			return fmt.Errorf("no transfer found for %q — run 'namecom transfer list' to see active transfers", domain)
		}
		return err
	}
	var t gen.Transfer
	if err := api.Decode(resp, &t); err != nil {
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
			transferRows(out, []gen.Transfer{t}),
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
	if pricingResp, perr := client.Gen().GetPricingForDomain(cmd.Context(), domain, &gen.GetPricingForDomainParams{}); perr == nil {
		var pricing gen.PricingResponseSchema
		if api.Decode(pricingResp, &pricing) == nil && pricing.TransferPrice != nil {
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

	body := gen.CreateTransferJSONRequestBody{
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
		out.DryRun("POST", "/core/v1/transfers", nil)
		fmt.Fprintf(out.Writer, "  domain=%s authCode=[redacted]\n", domain)
		return nil
	}

	stop := out.Spin("Initiating transfer…")
	resp, err := client.Gen().CreateTransfer(cmd.Context(), body)
	stop()
	if err != nil {
		return err
	}
	var result gen.CreateTransferResponseSchema
	if err := api.Decode(resp, &result); err != nil {
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
		if s := string(result.Transfer.Status); s != "" {
			fmt.Fprintf(out.Writer, "  status: %s\n", out.StatusBadge(s))
		}
		// The API flags non-blocking registry statuses that may still stall the
		// transfer. JSON output carried these; table output silently dropped them,
		// so a TTY user saw an unqualified success.
		if w := result.Warnings; w != nil {
			if w.Message != nil && *w.Message != "" {
				out.Warn(*w.Message)
			}
			if w.Statuses != nil && len(*w.Statuses) > 0 {
				out.Warn("registry statuses: " + strings.Join(*w.Statuses, ", "))
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
	switch gen.TransferStatus(status) {
	case gen.TransferStatusCompleted,
		gen.TransferStatusFailed,
		gen.TransferStatusCanceled,
		gen.TransferStatusCanceledPendingRefund:
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
		resp, err := client.Gen().GetTransfer(cmd.Context(), domain)
		stop()
		if err != nil {
			out.Warn(fmt.Sprintf("status check failed: %v", err))
			continue
		}
		var t gen.Transfer
		if err := api.Decode(resp, &t); err != nil {
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

	body := gen.CreateInternalTransferInJSONRequestBody{
		DomainName: domain,
		AuthCode:   internalAuthCode,
	}

	if dryRun {
		out.DryRun("POST", "/core/v1/transfers/internal/in", nil)
		fmt.Fprintf(out.Writer, "  domain=%s authCode=[redacted]\n", domain)
		return nil
	}

	resp, err := client.Gen().CreateInternalTransferIn(cmd.Context(), body)
	if err != nil {
		return err
	}
	var t gen.Transfer
	if err := api.Decode(resp, &t); err != nil {
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
		out.Success(fmt.Sprintf("Internal transfer initiated for %s (status: %s)", domain, t.Status))
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
	resp, err := client.Gen().CancelTransfer(cmd.Context(), domain)
	stop()
	if err != nil {
		return err
	}
	if err := api.Decode(resp, nil); err != nil {
		return err
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

	params := &gen.CancelOutboundTransferParams{
		ContentType: gen.Applicationjson,
	}
	resp, err := client.Gen().CancelOutboundTransfer(cmd.Context(), domain, params)
	if err != nil {
		return err
	}
	var result gen.CancelTransferOutResponseSchema
	if err := api.Decode(resp, &result); err != nil {
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
	resp, err := client.Gen().GetTransferEligibility(cmd.Context(), domain)
	stop()
	if err != nil {
		return err
	}
	var result gen.TransferEligibilityResponseSchema
	if err := api.Decode(resp, &result); err != nil {
		return err
	}

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

func transferRows(out *output.Config, transfers []gen.Transfer) [][]string {
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
