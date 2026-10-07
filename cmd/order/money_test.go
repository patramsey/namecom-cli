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

func strp(s string) *string { return &s }

// TestOrderRows_SayWhatWasBought pins #235: `order list` showed only ID,
// STATUS, DATE and TOTAL, though each order carries its items' names and
// types. A name repeated across items (a registration and its privacy) counts
// once, and the other names are counted rather than listed.
func TestOrderRows_SayWhatWasBought(t *testing.T) {
	id, status, created, total := 2142141, "success", "2026-04-06T11:39:11Z", 47.97
	o := &coreapigo.Order{ID: &id, Status: &status, CreateDate: &created, FinalAmount: &total,
		OrderItems: []*coreapigo.OrderItem{
			{ID: 1, Name: strp("acme.io"), Type: "registration"},
			{ID: 2, Name: strp("acme.io"), Type: "whois_privacy"},
			{ID: 3, Name: strp("acme.dev"), Type: "registration"},
			{ID: 4, Name: strp("acme.app"), Type: "registration"},
		}}
	out := &output.Config{Format: output.FormatTable, Color: output.ColorNever, Writer: &bytes.Buffer{}, EWriter: &bytes.Buffer{}}
	got := orderRows(out, []*coreapigo.Order{o})[0]
	want := []string{"2026-04-06", "acme.io +2", "registration, whois_privacy", "$47.97", "success", "2142141"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("row = %q\nwant  %q", got, want)
	}
	if strings.Join(orderHeaders, "|") != "DATE|DOMAIN(S)|TYPE|TOTAL|STATUS|ID" {
		t.Errorf("headers = %q", orderHeaders)
	}
}

// TestRefundPrompt_NamesItemsAndAmount pins #235: the refund prompt read
// "Refund order 2142141, items [1]? This cannot be undone." — a Go slice, with
// no product or amount. It now fetches the order and names both; a missing
// order or item fails before the question, and no refund is sent.
func TestRefundPrompt_NamesItemsAndAmount(t *testing.T) {
	const order = `{"id":2142141,"status":"success","finalAmount":47.97,"orderItems":[
		{"id":1,"name":"acme.io","price":35.98,"type":"registration","isRefundable":true},
		{"id":2,"name":"acme.io","price":11.99,"type":"whois_privacy","isRefundable":true}]}`

	for _, tc := range []struct {
		name, items string
		status      int
		wantPrompt  string
		wantErr     string
	}{
		{"one item", "1", http.StatusOK,
			"Refund $35.98 for acme.io registration (order 2142141, item 1)? This cannot be undone.", ""},
		{"two items", "1,2", http.StatusOK,
			"Refund $47.97 for acme.io registration, acme.io whois privacy (order 2142141, items 1, 2)? This cannot be undone.", ""},
		{"item not on the order", "9", http.StatusOK, "", "order 2142141 has no item 9"},
		{"order not found", "1", http.StatusNotFound, "", "order 2142141 not found"},
		{"lookup fails", "1,2", http.StatusBadRequest,
			"Refund order 2142141, items 1, 2? This cannot be undone.", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var prompts []string
			defer cmdutil.StubConfirm(func(p string) bool { prompts = append(prompts, p); return false })()
			// The order is read only when the prompt can be shown.
			defer output.StubInteractive(true)()
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					t.Errorf("refund sent without a yes: %s %s", r.Method, r.URL)
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				if tc.status == http.StatusOK {
					_, _ = w.Write([]byte(order))
				} else {
					_, _ = w.Write([]byte(`{"message":"nope"}`))
				}
			}))
			t.Cleanup(srv.Close)

			cmd := cmdForRefund(t, srv, false)
			refundOrderID, refundItemIDs = 0, nil
			if err := cmd.ParseFlags([]string{"--order-id", "2142141", "--item-ids", tc.items}); err != nil {
				t.Fatal(err)
			}
			if err := cmd.Root().PersistentFlags().Set("yes", "false"); err != nil {
				t.Fatal(err)
			}
			err := runRefund(cmd, nil)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Errorf("runRefund = %v, want an error containing %q", err, tc.wantErr)
				}
				if len(prompts) != 0 {
					t.Errorf("prompted %q before failing", prompts)
				}
				return
			}
			if len(prompts) != 1 || prompts[0] != tc.wantPrompt {
				t.Errorf("prompts = %q\nwant      [%q]", prompts, tc.wantPrompt)
			}
		})
	}
}

// TestOrderGet_RefundHintOnlyForRefundableItems pins #235: `order get`
// suggested `order refund` even when no item could be refunded.
func TestOrderGet_RefundHintOnlyForRefundableItems(t *testing.T) {
	for _, tc := range []struct {
		name       string
		refundable bool
		wantHint   string
	}{
		{"refundable", true, "--item-ids 555' to refund the refundable item"},
		{"not refundable", false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp := `{"id":12345,"status":"success","createDate":"2026-01-15","finalAmount":19.99,
				"orderItems":[{"id":555,"name":"acme.io","price":19.99,"type":"registration","isRefundable":` +
				map[bool]string{true: "true", false: "false"}[tc.refundable] + `}]}`
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(resp))
			}))
			t.Cleanup(srv.Close)
			client, err := api.New(api.Options{BaseURL: srv.URL})
			if err != nil {
				t.Fatal(err)
			}
			var stderr bytes.Buffer
			out := &output.Config{Format: output.FormatTable, Color: output.ColorNever, Writer: &bytes.Buffer{}, EWriter: &stderr}
			cmd := &cobra.Command{}
			ctx := context.WithValue(context.Background(), cmdutil.KeyOutput, out)
			cmd.SetContext(context.WithValue(ctx, cmdutil.KeyClient, client))
			if err := runGet(cmd, []string{"12345"}); err != nil {
				t.Fatalf("runGet: %v", err)
			}
			hinted := strings.Contains(stderr.String(), "order refund")
			if tc.wantHint == "" && hinted {
				t.Errorf("suggested a refund with nothing refundable:\n%s", stderr.String())
			}
			if tc.wantHint != "" && !strings.Contains(stderr.String(), tc.wantHint) {
				t.Errorf("stderr lacks %q:\n%s", tc.wantHint, stderr.String())
			}
		})
	}
}
