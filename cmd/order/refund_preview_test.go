package order

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/patramsey/namecom-cli/cmd/cmdutil"
	"github.com/patramsey/namecom-cli/internal/output"
)

const previewOrder = `{"id":42,"currency":"USD","orderItems":[
	{"id":7,"name":"acme.io","price":35.98,"type":"registration","isRefundable":true},
	{"id":8,"name":"acme.io","price":11.99,"type":"whois_privacy","isRefundable":false}]}`

// previewServer answers every GET with previewOrder and records each request.
func previewServer(t *testing.T, seen *[]string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*seen = append(*seen, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(previewOrder))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestRefundDryRun_ShowsWhatIsRefunded pins #382: the dry run read the order
// but previewed only the body's IDs. It now says what is refunded and for
// how much — a "Would refund" line on stderr in table mode, a "refund" key
// beside the body in JSON — from the one GET it already made.
func TestRefundDryRun_ShowsWhatIsRefunded(t *testing.T) {
	t.Run("table", func(t *testing.T) {
		var seen []string
		cmd := cmdForRefund(t, previewServer(t, &seen), true)
		if err := runRefund(cmd, nil); err != nil {
			t.Fatal(err)
		}
		out := cmdutil.Out(cmd)
		stderr := out.EWriter.(*bytes.Buffer).String()
		want := "Would refund: $35.98 for acme.io registration (order 42, item 7)\n"
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr = %q, want %q", stderr, want)
		}
		if !strings.Contains(out.Writer.(*bytes.Buffer).String(), "POST /core/v1/refund") {
			t.Errorf("no request previewed: %q", out.Writer.(*bytes.Buffer).String())
		}
		if got := strings.Join(seen, ","); got != "GET /core/v1/orders/42" {
			t.Errorf("sent %q, want only the order's GET", got)
		}
	})

	t.Run("json", func(t *testing.T) {
		var seen []string
		cmd := cmdForRefund(t, previewServer(t, &seen), true)
		cmdutil.Out(cmd).Format = output.FormatJSON
		if err := runRefund(cmd, nil); err != nil {
			t.Fatal(err)
		}
		var doc map[string]any
		if err := json.Unmarshal(cmdutil.Out(cmd).Writer.(*bytes.Buffer).Bytes(), &doc); err != nil {
			t.Fatal(err)
		}
		for _, k := range []string{"dryRun", "method", "path", "body"} {
			if _, ok := doc[k]; !ok {
				t.Errorf("missing %q: %v", k, doc)
			}
		}
		raw, _ := json.Marshal(doc["refund"])
		if want := `{"currency":"USD","items":[{"amount":35.98,"id":7,"name":"acme.io","type":"registration"}],"total":35.98}`; string(raw) != want {
			t.Errorf("refund = %s\nwant     %s", raw, want)
		}
	})
}

// TestRefund_RefusesANonRefundableItem pins #382: an item the order marks
// isRefundable false was previewed, and asked about, as a refund. It is now
// a usage error naming the item, before any prompt and before the POST.
func TestRefund_RefusesANonRefundableItem(t *testing.T) {
	for _, dryRun := range []bool{true, false} {
		name := "prompted"
		if dryRun {
			name = "dry run"
		}
		t.Run(name, func(t *testing.T) {
			var prompts []string
			defer cmdutil.StubConfirm(func(p string) bool { prompts = append(prompts, p); return true })()
			defer output.StubInteractive(true)()
			var seen []string
			cmd := cmdForRefund(t, previewServer(t, &seen), dryRun)
			refundOrderID, refundItemIDs = 0, nil
			if err := cmd.ParseFlags([]string{"--order-id", "42", "--item-ids", "7,8"}); err != nil {
				t.Fatal(err)
			}
			if err := cmd.Root().PersistentFlags().Set("yes", "false"); err != nil {
				t.Fatal(err)
			}
			err := runRefund(cmd, nil)
			if _, ok := errors.AsType[*cmdutil.UsageError](err); !ok || !strings.Contains(err.Error(), "item 8 of order 42 (acme.io whois privacy) is not refundable") {
				t.Errorf("runRefund = %v, want a usage error naming item 8", err)
			}
			if len(prompts) != 0 {
				t.Errorf("prompted %q before refusing", prompts)
			}
			if got := strings.Join(seen, ","); got != "GET /core/v1/orders/42" {
				t.Errorf("sent %q, want only the order's GET", got)
			}
			if s := cmdutil.Out(cmd).Writer.(*bytes.Buffer).String(); s != "" {
				t.Errorf("stdout = %q, want nothing previewed", s)
			}
		})
	}
}
