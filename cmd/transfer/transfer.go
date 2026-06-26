// Package transfer implements the `namecom transfer` command group.
package transfer

import (
	"errors"
	"fmt"
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
	Example: `  namecom transfer get example.com`,
	Args:              cmdutil.ExactArgs(1),
	RunE:              runGet,
	ValidArgsFunction: cmdutil.CompleteDomains,
}

var createCmd = &cobra.Command{
	Use:   "create <domain>",
	Short: "Initiate a transfer in from another registrar",
	Example: `  namecom transfer create example.com --auth-code XXXXXX
  namecom transfer create example.com --auth-code XXXXXX --privacy`,
	Args:  cmdutil.ExactArgs(1),
	RunE:  runCreate,
}

var internalCmd = &cobra.Command{
	Use:   "internal-in <domain>",
	Short: "Move a domain between name.com accounts (no EPP wait required)",
	Example: `  namecom transfer internal-in example.com --auth-code XXXXXX`,
	Args:  cmdutil.ExactArgs(1),
	RunE:  runInternalIn,
}

var cancelCmd = &cobra.Command{
	Use:               "cancel <domain>",
	Short:             "Cancel an in-progress transfer",
	Example: `  namecom transfer cancel example.com`,
	Args:              cmdutil.ExactArgs(1),
	RunE:              runCancel,
	ValidArgsFunction: cmdutil.CompleteDomains,
}

var cancelOutboundCmd = &cobra.Command{
	Use:               "cancel-outbound <domain>",
	Short:             "Cancel an outbound transfer-out",
	Example: `  namecom transfer cancel-outbound example.com`,
	Args:              cmdutil.ExactArgs(1),
	RunE:              runCancelOutbound,
	ValidArgsFunction: cmdutil.CompleteDomains,
}

var eligibilityCmd = &cobra.Command{
	Use:               "eligibility <domain>",
	Short:             "Check if a domain is eligible for transfer",
	Example: `  namecom transfer eligibility example.com`,
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

	Cmd.AddCommand(listCmd, getCmd, createCmd, internalCmd, cancelCmd, cancelOutboundCmd, eligibilityCmd)
}

func runList(cmd *cobra.Command, args []string) error {
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
		if err := api.Decode(resp, &lastResult); err != nil {
			spin.Stop()
			return err
		}
		transfers = append(transfers, lastResult.Transfers...)
		if lastResult.NextPage == nil || *lastResult.NextPage == 0 {
			break
		}
		if !listAll {
			hasMore = true
			break
		}
		page = *lastResult.NextPage
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

	stop := out.Spin("Fetching transfer…")
	resp, err := client.Gen().GetTransfer(cmd.Context(), args[0])
	stop()
	if err != nil {
		if cmdutil.IsNotFound(err) {
			return fmt.Errorf("no transfer found for %q — run 'namecom transfer list' to see active transfers", args[0])
		}
		return err
	}
	var t gen.Transfer
	if err := api.Decode(resp, &t); err != nil {
		return err
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
	domain := args[0]

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

	ok, err := confirm(out, yes, fmt.Sprintf("Initiate transfer of %s?", domain))
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
		body.PrivacyEnabled = ptr(createPrivacy)
	}
	if createPrice > 0 {
		body.PurchasePrice = ptr(createPrice)
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

	switch out.Format {
	case output.FormatJSON:
		return out.JSON(result)
	case output.FormatYAML:
		return out.YAML(result)
	default:
		out.Success(fmt.Sprintf("Transfer initiated for %s (order #%d, total $%.2f)",
			domain, result.Order, result.TotalPaid))
		out.Hint("Transfers typically take 3–5 days — the gaining registrar and current owner must approve")
		out.Hint(fmt.Sprintf("Run 'namecom transfer get %s' to check status", domain))
	}
	if createWatch {
		return watchTransfer(cmd, out, client, domain)
	}
	return nil
}

// watchTransfer polls GetTransfer every 5 minutes until it reaches a terminal
// state. Useful in CI/automation — for interactive use the hint is enough.
func watchTransfer(cmd *cobra.Command, out *output.Config, client *api.Client, domain string) error {
	terminalStates := map[string]bool{
		"completed": true, "canceled": true, "failed": true, "rejected": true,
	}
	fmt.Fprintf(out.Writer, "\nWatching transfer status — checking every 5 minutes (Ctrl+C to stop)\n")

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
		fmt.Fprintf(out.Writer, "  %s  %s\n", time.Now().Format("15:04"), out.StatusBadge(status))
		if terminalStates[status] {
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
	domain := args[0]

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
		out.DryRun("POST", "/core/v1/transfers:internal-in", nil)
		fmt.Fprintf(out.Writer, "  domain=%s authCode=[redacted]\n", domain)
		return nil
	}

	resp, err := client.Gen().CreateInternalTransferIn(cmd.Context(), body)
	if err != nil {
		return err
	}
	var t gen.Transfer
	if err := api.Decode(resp, &t); err != nil {
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
	domain := args[0]

	ok, err := confirm(out, yes, fmt.Sprintf("Cancel transfer of %s?", domain))
	if err != nil {
		return err
	}
	if !ok {
		out.Warn("aborted")
		return nil
	}

	if dryRun {
		out.DryRun("DELETE", fmt.Sprintf("/core/v1/transfers/%s", domain), nil)
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
	domain := args[0]

	ok, err := confirm(out, yes, fmt.Sprintf("Cancel outbound transfer of %s?", domain))
	if err != nil {
		return err
	}
	if !ok {
		out.Warn("aborted")
		return nil
	}

	if dryRun {
		out.DryRun("POST", fmt.Sprintf("/core/v1/domains/%s:cancel-transfer-out", domain), nil)
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
	domain := args[0]

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
			out.Hint(fmt.Sprintf("Run 'namecom transfer internal-in %s --auth-code XXXXXX' to transfer", domain))
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

func ptr[T any](v T) *T { return &v }
