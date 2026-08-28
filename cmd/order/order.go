// Package order implements the `namecom order` command group.
package order

import (
	"fmt"
	"strconv"
	"strings"

	coreapigo "github.com/namedotcom/core-api-go"
	"github.com/patramsey/namecom-cli/cmd/cmdutil"
	"github.com/patramsey/namecom-cli/internal/api"
	"github.com/patramsey/namecom-cli/internal/output"
	"github.com/spf13/cobra"
)

func confirmRefund(out *output.Config, yes bool, orderID int32, itemIDs []int32) (bool, error) {
	return cmdutil.Confirm(out, yes, fmt.Sprintf("Refund order %d, items %v? This cannot be undone.", orderID, itemIDs))
}

// Cmd is the `namecom order` parent command.
var Cmd = &cobra.Command{
	Use:   "order",
	Short: "View purchase history and request refunds",
}

var (
	refundOrderID int32
	refundItemIDs []int32

	listAll    bool
	listDomain string
	listSince  string
	listUntil  string
	listStatus string
)

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List orders",
	Example: `  namecom order list                                   # most recent page
  namecom order list --all                             # full history (can be slow)
  namecom order list --since 2026-01-01                # orders from this year
  namecom order list --domain acme.io                  # orders for one domain
  namecom order list --status success
  namecom order list --all -o json | jq '.data[].id'   # JSON output is wrapped in a "data" envelope`,
	Args: cobra.NoArgs,
	RunE: runList,
}

var getCmd = &cobra.Command{
	Use:     "get <id>",
	Short:   "Get an order by ID",
	Example: `  namecom order get 12345`,
	Args:    cmdutil.ExactArgs(1),
	RunE:    runGet,
}

var refundCmd = &cobra.Command{
	Use:     "refund",
	Short:   "Process a refund for order items",
	Example: `  namecom order refund --order-id 12345 --item-ids 67890 --yes`,
	Args:    cobra.NoArgs,
	RunE:    runRefund,
}

func init() {
	listCmd.Flags().BoolVar(&listAll, "all", false, "fetch all pages (full history — can be slow)")
	listCmd.Flags().StringVar(&listDomain, "domain", "", "filter by domain name (supports * wildcard)")
	listCmd.Flags().StringVar(&listSince, "since", "", "filter orders created on or after this date (YYYY-MM-DD)")
	listCmd.Flags().StringVar(&listUntil, "until", "", "filter orders created on or before this date (YYYY-MM-DD)")
	listCmd.Flags().StringVar(&listStatus, "status", "", "filter by status: success, failed, initialized, started, review")

	refundCmd.Flags().Int32Var(&refundOrderID, "order-id", 0, "order ID (required)")
	refundCmd.Flags().Int32SliceVar(&refundItemIDs, "item-ids", nil, "comma-separated order item IDs (required)")
	_ = refundCmd.MarkFlagRequired("order-id")
	_ = refundCmd.MarkFlagRequired("item-ids")

	cmdutil.GroupCmd(Cmd)
	Cmd.AddCommand(listCmd, getCmd, refundCmd)
}

func runList(cmd *cobra.Command, _ []string) error {
	out := cmdutil.Out(cmd)
	client := cmdutil.APIClient(cmd)

	if listSince != "" {
		if err := cmdutil.ValidDate(listSince, "since"); err != nil {
			return err
		}
	}
	if listUntil != "" {
		if err := cmdutil.ValidDate(listUntil, "until"); err != nil {
			return err
		}
	}

	// Auto-paginate when any filter is active — results will be small.
	filtered := cmd.Flags().Changed("domain") || cmd.Flags().Changed("since") ||
		cmd.Flags().Changed("until") || cmd.Flags().Changed("status")
	autoPage := listAll || filtered

	spin := out.StartSpinner("Fetching orders…")
	page := 1
	var orders []*coreapigo.Order
	var hasMore bool
	var lastResult *coreapigo.ListOrdersResponse
	for {
		req := &coreapigo.ListOrdersRequest{Page: &page}
		if listDomain != "" {
			req.DomainName = &listDomain
		}
		if listSince != "" {
			req.CreateDateStart = &listSince
		}
		if listUntil != "" {
			req.CreateDateEnd = &listUntil
		}
		if listStatus != "" {
			s := coreapigo.ListOrdersRequestOrderStatus(listStatus)
			req.OrderStatus = &s
		}
		result, err := client.SDK().Orders.ListOrders(cmd.Context(), req)
		if err != nil {
			spin.Stop()
			return api.FromSDKError(err)
		}
		orders = append(orders, result.Orders...)
		lastResult = result
		next, ok := cmdutil.NextPage(page, result.NextPage, result.LastPage)
		if !ok {
			break
		}
		if !autoPage {
			hasMore = true
			break
		}
		page = next
		spin.Update(fmt.Sprintf("Fetching orders… (page %d, %d so far)", page, len(orders)))
	}
	spin.Stop()

	if out.QuietMode {
		ids := make([]string, 0, len(orders))
		for _, o := range orders {
			if o.ID != nil {
				ids = append(ids, strconv.Itoa(*o.ID))
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
		return out.JSONList(orders, np, cmdutil.Int32Count(lastResult.TotalCount))
	case output.FormatYAML:
		var np *int32
		if hasMore {
			np = cmdutil.Int32Page(lastResult.NextPage)
		}
		return out.YAMLList(orders, np, cmdutil.Int32Count(lastResult.TotalCount))
	default:
		if len(orders) == 0 {
			out.Empty("order", "")
			return nil
		}
		out.Table(
			[]string{"ID", "STATUS", "DATE", "TOTAL"},
			orderRows(out, orders),
		)
		out.Count(len(orders), "order")
		if hasMore {
			out.Hint("Showing first page — use --since, --domain, or --status to narrow results; --all for full history")
		}
	}
	return nil
}

func runGet(cmd *cobra.Command, args []string) error {
	out := cmdutil.Out(cmd)
	client := cmdutil.APIClient(cmd)

	id, err := parseID(args[0])
	if err != nil {
		return err
	}

	stop := out.Spin("Fetching order…")
	o, err := client.SDK().Orders.GetOrder(cmd.Context(), &coreapigo.GetOrderRequest{OrderID: int(id)})
	stop()
	if err != nil {
		return err
	}

	switch out.Format {
	case output.FormatJSON:
		return out.JSON(o)
	case output.FormatYAML:
		return out.YAML(o)
	default:
		out.Table(
			[]string{"ID", "STATUS", "DATE", "TOTAL"},
			orderRows(out, []*coreapigo.Order{o}),
		)
		// Show the line items. Their IDs are the required input to
		// `order refund --item-ids`, and sharing list's renderer meant a single
		// order rendered exactly like a list row — leaving no way to discover
		// them without dropping to `-o json | jq`.
		if len(o.OrderItems) > 0 {
			out.Table(
				[]string{"ITEM ID", "NAME", "TYPE", "PRICE", "REFUNDABLE"},
				orderItemRows(out, o.OrderItems, o.Currency),
			)
			out.Hint("Run 'namecom order refund --order-id " +
				strconv.Itoa(derefInt(o.ID)) + " --item-ids <ITEM ID>' to refund a refundable item")
		}
		out.Hint("Run 'namecom order list' to see all orders")
	}
	return nil
}

func runRefund(cmd *cobra.Command, _ []string) error {
	out := cmdutil.Out(cmd)
	client := cmdutil.APIClient(cmd)
	yes := cmdutil.IsYes(cmd)
	dryRun := cmdutil.IsDryRun(cmd)

	itemIDs := make([]int, 0, len(refundItemIDs))
	for _, id := range refundItemIDs {
		itemIDs = append(itemIDs, int(id))
	}

	body := coreapigo.RefundRequest{
		OrderID:      int(refundOrderID),
		OrderItemIDs: itemIDs,
	}

	if dryRun {
		// Previously printed a hand-rolled "orderId=… itemIds=…" line beside a
		// nil body, so the preview was a paraphrase of the request rather than
		// the request. Nothing here is secret, and a refund is worth seeing
		// exactly as it will be sent.
		out.DryRun("POST", "/core/v1/refund", body)
		return nil
	}

	ok, err := confirmRefund(out, yes, refundOrderID, refundItemIDs)
	if err != nil {
		return err
	}
	if !ok {
		out.Warn("aborted")
		return nil
	}

	// The root --idempotency-key (or an auto-generated one) is applied by the
	// shared transport, not per call; no per-command flag is needed or wanted
	// here.
	result, err := client.SDK().Refunds.ProcessRefund(cmd.Context(), &body)
	if err != nil {
		return api.FromSDKError(err)
	}

	switch out.Format {
	case output.FormatJSON:
		return out.JSON(result)
	case output.FormatYAML:
		return out.YAML(result)
	default:
		out.Success(fmt.Sprintf("Refunded $%.2f for %d item(s)", result.TotalRefundAmount, len(result.Results)))
		out.Hint("Run 'namecom order list' to see updated order status")
	}
	return nil
}

// formatAmount renders a monetary value in the order's currency. Orders can be
// placed in non-USD currencies ('USD', 'CNY'), so a bare "$" would misreport
// them. USD keeps the familiar symbol; anything else is suffixed with its code
// rather than guessing at a symbol we may not have.
func formatAmount(amount float64, currency *string) string {
	if currency == nil || *currency == "" || strings.EqualFold(*currency, "USD") {
		return fmt.Sprintf("$%.2f", amount)
	}
	return fmt.Sprintf("%.2f %s", amount, strings.ToUpper(*currency))
}

func derefInt(n *int) int {
	if n == nil {
		return 0
	}
	return *n
}

// orderItemRows renders an order's line items. Item IDs are what
// `order refund --item-ids` consumes, and IsRefundable says whether a refund
// is even possible — so both belong in the default view.
func orderItemRows(out *output.Config, items []*coreapigo.OrderItem, currency *string) [][]string {
	rows := make([][]string, 0, len(items))
	for _, it := range items {
		name := ""
		if it.Name != nil {
			name = *it.Name
		}
		refundable := out.Dim("—")
		if it.IsRefundable {
			refundable = out.BoolBadge(true)
		}
		rows = append(rows, []string{
			strconv.Itoa(it.ID),
			name,
			it.Type,
			formatAmount(it.Price, currency),
			refundable,
		})
	}
	return rows
}

func orderRows(out *output.Config, orders []*coreapigo.Order) [][]string {
	rows := make([][]string, 0, len(orders))
	for _, o := range orders {
		id := ""
		if o.ID != nil {
			id = out.Dim(strconv.Itoa(*o.ID))
		}
		status := ""
		if o.Status != nil {
			status = out.StatusBadge(*o.Status)
		}
		date := ""
		if o.CreateDate != nil {
			date = out.Dim(*o.CreateDate)
		}
		total := ""
		if o.FinalAmount != nil {
			total = formatAmount(*o.FinalAmount, o.Currency)
		}
		rows = append(rows, []string{id, status, date, total})
	}
	return rows
}

func parseID(s string) (int32, error) {
	n, err := strconv.ParseInt(s, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("invalid order ID %q: must be a number", s)
	}
	return int32(n), nil
}
