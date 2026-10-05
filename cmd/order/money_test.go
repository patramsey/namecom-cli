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
