package order

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	coreapigo "github.com/namedotcom/core-api-go"
	"github.com/patramsey/namecom-cli/cmd/cmdutil"
	"github.com/patramsey/namecom-cli/internal/api"
	"github.com/patramsey/namecom-cli/internal/output"
	"github.com/spf13/cobra"
)

// TestOrderTables_NarrowKeepsIDAndRefundable pins #364: at 70 columns
// `order list` hid STATUS and ID — the value `order get` and `order refund`
// take — while TYPE stayed, and at 60 `order get` hid REFUNDABLE.
func TestOrderTables_NarrowKeepsIDAndRefundable(t *testing.T) {
	t.Run("list", func(t *testing.T) {
		for _, width := range []int{60, 70} {
			id, status, created, total := 2142141, "success", "2026-04-06T11:39:11Z", 47.97
			o := &coreapigo.Order{ID: &id, Status: &status, CreateDate: &created, FinalAmount: &total,
				OrderItems: []*coreapigo.OrderItem{
					{ID: 1, Name: strp("namecom-smoke-37de95c1a2b4.com"), Type: "registration"},
					{ID: 2, Name: strp("namecom-smoke-37de95c1a2b4.com"), Type: "whois_privacy"},
				}}
			var stdout, stderr bytes.Buffer
			out := &output.Config{Format: output.FormatTable, Color: output.ColorNever, Writer: &stdout, EWriter: &stderr, MaxWidth: width}
			orderTable(out, []*coreapigo.Order{o})
			header := strings.Split(stdout.String(), "\n")[1]
			if !strings.HasPrefix(strings.TrimLeft(header, "│ "), "ID ") {
				t.Errorf("width %d: ID is not the first column:\n%s", width, stdout.String())
			}
			if !strings.Contains(stdout.String(), "2142141") {
				t.Errorf("width %d: order ID missing:\n%s", width, stdout.String())
			}
			if strings.Contains(header, "TYPE") && !strings.Contains(header, "STATUS") {
				t.Errorf("width %d: TYPE kept over STATUS:\n%s", width, stdout.String())
			}
		}
	})

	t.Run("get", func(t *testing.T) {
		resp := `{"id":12345,"status":"success","createDate":"2026-01-15","finalAmount":19.99,
			"orderItems":[{"id":555,"name":"namecom-smoke-37de95c1a2b4.com","price":19.99,"type":"registration","isRefundable":true}]}`
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(resp))
		}))
		t.Cleanup(srv.Close)
		client, err := api.New(api.Options{BaseURL: srv.URL})
		if err != nil {
			t.Fatal(err)
		}
		var stdout bytes.Buffer
		out := &output.Config{Format: output.FormatTable, Color: output.ColorNever, Writer: &stdout, EWriter: &bytes.Buffer{}, MaxWidth: 60}
		cmd := &cobra.Command{}
		ctx := context.WithValue(context.Background(), cmdutil.KeyOutput, out)
		cmd.SetContext(context.WithValue(ctx, cmdutil.KeyClient, client))
		if err := runGet(cmd, []string{"12345"}); err != nil {
			t.Fatalf("runGet: %v", err)
		}
		if !strings.Contains(stdout.String(), "REFUNDABLE") {
			t.Errorf("REFUNDABLE hidden at 60 columns:\n%s", stdout.String())
		}
		if !strings.Contains(stdout.String(), "555") {
			t.Errorf("item ID missing:\n%s", stdout.String())
		}
	})
}
