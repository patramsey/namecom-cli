// Package order implements the `namecom order` command group.
package order

import (
	"fmt"
	"strconv"

	"github.com/namedotcom/namecom-cli/cmd/cmdutil"
	"github.com/namedotcom/namecom-cli/internal/api"
	"github.com/namedotcom/namecom-cli/internal/api/gen"
	"github.com/namedotcom/namecom-cli/internal/output"
	"github.com/spf13/cobra"
)

func confirmRefund(yes bool, orderID int32, itemIDs []int32) (bool, error) {
	return cmdutil.Confirm(yes, fmt.Sprintf("Refund order %d, items %v? This cannot be undone.", orderID, itemIDs))
}

// Cmd is the `namecom order` parent command.
var Cmd = &cobra.Command{
	Use:   "order",
	Short: "View purchase history and request refunds",
}

var (
	refundOrderID   int32
	refundItemIDs   []int32
	refundIdemKey   string
)

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List orders",
	Example: `  namecom order list
  namecom order list -o json | jq '.[] | select(.status=="completed")'`,
	Args:  cobra.NoArgs,
	RunE:  runList,
}

var getCmd = &cobra.Command{
	Use:   "get <id>",
	Short: "Get an order by ID",
	Example: `  namecom order get 12345`,
	Args:  cmdutil.ExactArgs(1),
	RunE:  runGet,
}

var refundCmd = &cobra.Command{
	Use:   "refund",
	Short: "Process a refund for order items",
	Example: `  namecom order refund --order-id 12345 --item-ids 67890 --yes`,
	Args:  cobra.NoArgs,
	RunE:  runRefund,
}

func init() {
	refundCmd.Flags().Int32Var(&refundOrderID, "order-id", 0, "order ID (required)")
	refundCmd.Flags().Int32SliceVar(&refundItemIDs, "item-ids", nil, "comma-separated order item IDs (required)")
	refundCmd.Flags().StringVar(&refundIdemKey, "idempotency-key", "", "idempotency key for safe retries")
	_ = refundCmd.MarkFlagRequired("order-id")
	_ = refundCmd.MarkFlagRequired("item-ids")

	Cmd.AddCommand(listCmd, getCmd, refundCmd)
}

func runList(cmd *cobra.Command, args []string) error {
	out := cmdutil.Out(cmd)
	client := cmdutil.APIClient(cmd)

	stop := out.Spin("Fetching orders…")
	var page int32 = 1
	var all []gen.Order
	for {
		params := &gen.ListOrdersParams{Page: &page}
		resp, err := client.Gen().ListOrders(cmd.Context(), params)
		if err != nil {
			stop()
			return err
		}
		var result gen.ListOrdersResponseSchema
		if err := api.Decode(resp, &result); err != nil {
			stop()
			return err
		}
		all = append(all, result.Orders...)
		if result.NextPage == nil || *result.NextPage == 0 {
			break
		}
		page = *result.NextPage
	}
	stop()

	if out.QuietMode {
		ids := make([]string, 0, len(all))
		for _, o := range all {
			if o.Id != nil {
				ids = append(ids, strconv.Itoa(int(*o.Id)))
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
			out.Empty("order", "")
			return nil
		}
		out.Table(
			[]string{"ID", "STATUS", "DATE", "TOTAL"},
			orderRows(out, all),
		)
		out.Count(len(all), "order")
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
	resp, err := client.Gen().GetOrder(cmd.Context(), id)
	stop()
	if err != nil {
		return err
	}
	var o gen.Order
	if err := api.Decode(resp, &o); err != nil {
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
			orderRows(out, []gen.Order{o}),
		)
		out.Hint("Run 'namecom order list' to see all orders")
	}
	return nil
}

func runRefund(cmd *cobra.Command, args []string) error {
	out := cmdutil.Out(cmd)
	client := cmdutil.APIClient(cmd)
	yes := cmdutil.IsYes(cmd)
	dryRun := cmdutil.IsDryRun(cmd)

	ok, err := confirmRefund(yes, refundOrderID, refundItemIDs)
	if err != nil {
		return err
	}
	if !ok {
		out.Warn("aborted")
		return nil
	}

	itemIDs := make([]int32, len(refundItemIDs))
	copy(itemIDs, refundItemIDs)

	body := gen.ProcessRefundJSONRequestBody{
		OrderId:      refundOrderID,
		OrderItemIds: itemIDs,
	}

	params := &gen.ProcessRefundParams{}
	if refundIdemKey != "" {
		params.XIdempotencyKey = &refundIdemKey
	}

	if dryRun {
		out.DryRun("POST", "/core/v1/orders:refund", nil)
		fmt.Fprintf(out.Writer, "  orderId=%d itemIds=%v\n", refundOrderID, refundItemIDs)
		return nil
	}

	resp, err := client.Gen().ProcessRefund(cmd.Context(), params, body)
	if err != nil {
		return err
	}
	var result gen.RefundResponseSchema
	if err := api.Decode(resp, &result); err != nil {
		return err
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

func orderRows(out *output.Config, orders []gen.Order) [][]string {
	rows := make([][]string, 0, len(orders))
	for _, o := range orders {
		id := ""
		if o.Id != nil {
			id = out.Dim(strconv.Itoa(int(*o.Id)))
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
			total = fmt.Sprintf("$%.2f", *o.FinalAmount)
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
