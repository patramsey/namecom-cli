package order

import (
	"net/http/httptest"
	"testing"

	"github.com/spf13/cobra"

	"github.com/patramsey/namecom-cli/internal/drifttest"
)

// TestRequestShape_Order pins the wire request for `order refund`, the only
// mutating command in this group and the one that moves money.
//
// Captured from the generated client before the port to the Core SDK, so it
// asserts equivalence rather than describing whatever the SDK produces — see
// #40.
func TestRequestShape_Order(t *testing.T) {
	const stub = `{"orderId":88,"results":[{"orderItemId":5,"success":true}]}`

	build := func(t *testing.T, srv *httptest.Server) *cobra.Command {
		// cmdForRefund takes its own dryRun flag; drifttest supplies the real
		// one through the root command, so this always builds the live form.
		cmd := cmdForRefund(t, srv, false)
		// refundItemIDs is package-level and Int32SliceVar appends rather than
		// replaces, so a value left by an earlier test rides along into this
		// request. Reset it, or the assertion depends on test order.
		refundOrderID, refundItemIDs = 0, nil
		if err := cmd.ParseFlags([]string{"--order-id", "88", "--item-ids", "5,6"}); err != nil {
			t.Fatalf("ParseFlags: %v", err)
		}
		return cmd
	}
	drifttest.AssertRequest(t, drifttest.Request{
		Method: "POST",
		Path:   "/core/v1/refund",
		Body:   `{"orderId":88,"orderItemIds":[5,6]}`,
	}, build, runRefund, nil, stub)
}
