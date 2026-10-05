// Package order implements the `namecom order` command group.
package order

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	coreapigo "github.com/namedotcom/core-api-go"
	"github.com/patramsey/namecom-cli/cmd/cmdutil"
	"github.com/patramsey/namecom-cli/internal/api"
	"github.com/patramsey/namecom-cli/internal/output"
	"github.com/spf13/cobra"
)

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

// timestampNote is shared by `order list` and `order get`. name.com's order
// API returns timestamps about 6h behind real UTC (#134), most likely US
// Mountain local time, while labelling them Z. Domain timestamps are correct.
// The CLI does not shift them: the offset appears to follow daylight saving
// and could be fixed upstream at any time.
const timestampNote = `Note: name.com's order timestamps currently run several hours behind UTC
despite the "Z" suffix, and the --since/--until filters use the same clock.
An order placed near midnight UTC may show the previous day's date and fall
outside a date filter you would expect to include it.`

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List orders",
	Long:  "List orders, newest first.\n\n" + timestampNote,
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
	Long:    "Get an order by ID.\n\n" + timestampNote,
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
	listCmd.Flags().StringVar(&listSince, "since", "", "filter orders created on or after this date (YYYY-MM-DD); name.com's order clock runs hours behind UTC")
	listCmd.Flags().StringVar(&listUntil, "until", "", "filter orders created on or before this date (YYYY-MM-DD); name.com's order clock runs hours behind UTC")
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
	// The API treats createDateEnd as exclusive — midnight at the start of the
	// date — so --until, documented as "on or before", sends the next day.
	// ValidDate accepts only YYYY-MM-DD, so there is no time of day to keep.
	var until string
	if listUntil != "" {
		if err := cmdutil.ValidDate(listUntil, "until"); err != nil {
			return err
		}
		d, _ := time.Parse("2006-01-02", listUntil)
		until = d.AddDate(0, 0, 1).Format("2006-01-02")
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
	// Newest first. The API defaults to ascending, so without this the first
	// page of a long history was its oldest orders and anything recent — the
	// orders people look for, and the ones `order refund` can still act on —
	// sat behind every other page.
	dir := "desc"
	for {
		req := &coreapigo.ListOrdersRequest{Page: &page, Dir: &dir}
		if listDomain != "" {
			req.DomainName = &listDomain
		}
		if listSince != "" {
			req.CreateDateStart = &listSince
		}
		if until != "" {
			req.CreateDateEnd = &until
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
		orders = append(orders, cmdutil.NonNil(result.Orders)...)
		lastResult = result
		next, ok := cmdutil.NextPage(page, result.NextPage, result.LastPage)
		if !ok {
			break
		}
		// --quiet returns before the "showing the newest orders" hint, so
		// stopping early would truncate silently. Page fully whenever the
		// caller cannot be told there is more — see cmd/contact/contact.go.
		if !autoPage && !out.QuietMode {
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
			out.Hint("Showing the newest orders — use --since, --domain, or --status to narrow results; --all for full history")
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
	if cmdutil.IsNotFound(err) {
		return cmdutil.NotFound(err, fmt.Sprintf("order %d not found — run 'namecom order list' to see your orders", id))
	}
	if err != nil {
		return err
	}
	if err := cmdutil.RequireField("the order ID", o.ID); err != nil {
		return err
	}
	o.OrderItems = cmdutil.NonNil(o.OrderItems)

	// Quiet prints the order ID from the response.
	if out.QuietMode {
		out.Quiet(strconv.Itoa(*o.ID))
		return nil
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
	// Repeated IDs are dropped, keeping first-seen order. Sent as given, the
	// API refunds the first copy and reports the second "failed" (already
	// refunded), so a refund that worked exited 1. Done before the body is
	// built, so the --dry-run preview and the prompt show what is sent.
	itemIDs := make([]int, 0, len(refundItemIDs))
	seen := make(map[int]bool, len(refundItemIDs))
	var dropped []string
	for _, id := range refundItemIDs {
		n := int(id)
		if seen[n] {
			if !slices.Contains(dropped, strconv.Itoa(n)) {
				dropped = append(dropped, strconv.Itoa(n))
			}
			continue
		}
		seen[n] = true
		itemIDs = append(itemIDs, n)
	}
	if len(dropped) > 0 {
		out.Warn("ignoring duplicate item ID(s): " + strings.Join(dropped, ", "))
	}

	body := coreapigo.RefundRequest{
		OrderID:      int(refundOrderID),
		OrderItemIDs: itemIDs,
	}

	// The preview is the body itself. It previously printed a hand-rolled
	// "orderId=… itemIds=…" line beside a nil body, so the preview was a
	// paraphrase of the request rather than the request. Nothing here is
	// secret, and a refund is worth seeing exactly as it will be sent.
	//
	// The root --idempotency-key (or an auto-generated one) is applied by the
	// shared transport, not per call; no per-command flag is needed or wanted
	// here.
	var result *coreapigo.RefundResponse
	sent, err := cmdutil.RunWrite(cmd, cmdutil.Write[coreapigo.RefundRequest]{
		Method: "POST",
		Path:   "/core/v1/refund",
		Body:   body,
		Prompt: fmt.Sprintf("Refund order %d, items %v? This cannot be undone.", body.OrderID, body.OrderItemIDs),
	}, func(ctx context.Context, body coreapigo.RefundRequest) error {
		var err error
		result, err = client.SDK().Refunds.ProcessRefund(ctx, &body)
		if r := conflictRefundResult(err); r != nil {
			result = r
			return nil
		}
		return api.FromSDKError(err)
	})
	if err != nil || !sent {
		return err
	}

	// The call succeeding says nothing about each item: the API returns 200
	// with per-item "failed" or "canceled" results (outside the grace period,
	// for one), and counting len(Results) reported those as refunded.
	var refunded, failed int
	var problems []string
	for _, r := range result.Results {
		if r == nil {
			continue
		}
		switch r.OrderItemStatus {
		case coreapigo.RefundItemResultOrderItemStatusRefunded:
			refunded++
			continue
		case coreapigo.RefundItemResultOrderItemStatusFailed, coreapigo.RefundItemResultOrderItemStatusCanceled:
			failed++
		}
		// Anything else ("initialized", or a status this build does not know)
		// is not confirmed as refunded, so it is reported but not counted as a
		// failure.
		msg := fmt.Sprintf("item %d: refund %s", r.OrderItemID, r.OrderItemStatus)
		if r.OrderItemStatus == "" {
			msg = fmt.Sprintf("item %d: refund status not reported", r.OrderItemID)
		}
		if r.Message != nil && *r.Message != "" {
			msg += " — " + *r.Message
		}
		problems = append(problems, msg)
	}

	switch {
	case out.QuietMode:
		// Nothing on stdout, but which items failed is still worth saying.
		for _, p := range problems {
			out.Warn(p)
		}
	case out.Format == output.FormatJSON:
		if err := out.JSON(result); err != nil {
			return err
		}
	case out.Format == output.FormatYAML:
		if err := out.YAML(result); err != nil {
			return err
		}
	default:
		if refunded > 0 {
			out.Success(fmt.Sprintf("Refunded $%.2f for %d item(s)", result.TotalRefundAmount, refunded))
		}
		for _, p := range problems {
			out.Warn(p)
		}
		out.Hint("Run 'namecom order list' to see updated order status")
	}
	if failed > 0 {
		// Exit 1: the request was valid and authorized, the API declined part
		// of it — a runtime outcome, not a usage or credential problem.
		return fmt.Errorf("%d of %d item(s) were not refunded", failed, len(result.Results))
	}
	return nil
}

// conflictRefundResult recovers the refund result from a 409. When every item
// fails the API answers 409 rather than 200, with the same per-item body, and
// treating that as a plain API error printed the raw JSON instead of each
// item's reason. A 409 without results — an idempotency-key conflict, say —
// returns nil and stays an ordinary API error.
func conflictRefundResult(err error) *coreapigo.RefundResponse {
	conflict, ok := errors.AsType[*coreapigo.ConflictError](err)
	if !ok || conflict.Body == nil {
		return nil
	}
	raw, err := json.Marshal(conflict.Body)
	if err != nil {
		return nil
	}
	var r coreapigo.RefundResponse
	if err := json.Unmarshal(raw, &r); err != nil || len(r.Results) == 0 {
		return nil
	}
	return &r
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
			date = out.Dim(orderDate(*o.CreateDate))
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
	n, ok := cmdutil.PositiveID(s)
	if !ok {
		return 0, cmdutil.NewUsageError(fmt.Errorf("invalid order ID %q: must be a positive whole number", s))
	}
	return n, nil
}

// orderDate prints an order's creation time as YYYY-MM-DD, the form every other
// command uses, rather than the API's raw RFC 3339 timestamp. A value that does
// not parse is shown as-is rather than hidden.
func orderDate(s string) string {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.Format("2006-01-02")
	}
	return s
}
