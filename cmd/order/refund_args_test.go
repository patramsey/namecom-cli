package order

import (
	"errors"
	"strings"
	"testing"

	"github.com/patramsey/namecom-cli/cmd/cmdutil"
	"github.com/spf13/cobra"
)

// TestRefundArgs_OrderIDAsArgument pins #292: `order refund 12345`, the way
// `order get 12345` takes an ID, was reported as an unknown command "12345".
// It is a usage error whose hint is the command with --order-id.
func TestRefundArgs_OrderIDAsArgument(t *testing.T) {
	build := func(t *testing.T, flags ...string) *cobra.Command {
		cmd := &cobra.Command{Use: "refund"}
		cmd.Flags().Int32Var(&refundOrderID, "order-id", 0, "")
		cmd.Flags().Int32SliceVar(&refundItemIDs, "item-ids", nil, "")
		t.Cleanup(func() { refundOrderID, refundItemIDs = 0, nil })
		if err := cmd.ParseFlags(flags); err != nil {
			t.Fatal(err)
		}
		return cmd
	}

	for _, tc := range []struct {
		name  string
		flags []string
		args  []string
		hint  string // "" for no hint about --order-id
	}{
		{"ID alone", nil, []string{"12345"}, "refund --order-id 12345 --item-ids <item-ids>"},
		{"ID with item IDs", []string{"--item-ids", "7,8"}, []string{"12345"}, "refund --order-id 12345 --item-ids 7,8"},
		{"not a number", nil, []string{"abc"}, ""},
		{"a number and not", nil, []string{"12345", "abc"}, ""},
		// #313: two IDs fell back to the generic "takes no arguments".
		{"order and item IDs", nil, []string{"12345", "6789"}, "refund --order-id 12345 --item-ids 6789"},
		{"order and item IDs, more items in the flag", []string{"--item-ids", "7"}, []string{"12345", "6789"}, "refund --order-id 12345 --item-ids 6789,7"},
		{"--order-id given too", []string{"--order-id", "1"}, []string{"12345"}, "refund --order-id 1 --item-ids 12345"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := refundArgs(build(t, tc.flags...), tc.args)
			uerr, ok := errors.AsType[*cmdutil.UsageError](err)
			if !ok {
				t.Fatalf("want a usage error, got %v", err)
			}
			hint := uerr.UserHint()
			if tc.hint != "" && !strings.Contains(hint, tc.hint) {
				t.Errorf("hint = %q, want it to contain %q", hint, tc.hint)
			}
			if tc.hint == "" && strings.Contains(hint, "--order-id") {
				t.Errorf("hint = %q, want no --order-id rewrite", hint)
			}
		})
	}
	if err := refundArgs(build(t), nil); err != nil {
		t.Errorf("no arguments is fine, got %v", err)
	}
}
