package order

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/patramsey/namecom-cli/cmd/cmdutil"
)

// TestRefundDryRun_ChecksTheOrder pins #326: `order refund --dry-run`
// previewed a refund of an order or item that did not exist, and exited 0,
// where the real refund fails. The dry run now reads the order, as a
// prompted refund already does to word its question, and fails as the
// prompted refund would; it still sends nothing else.
func TestRefundDryRun_ChecksTheOrder(t *testing.T) {
	for name, tc := range map[string]struct {
		status  int
		order   string
		check   func(error) bool
		preview bool
	}{
		"missing order": {http.StatusNotFound, `{"message":"Not Found"}`, func(err error) bool {
			return cmdutil.IsNotFound(err) && strings.Contains(err.Error(), "order 42 not found")
		}, false},
		"missing item": {http.StatusOK, `{"id":42,"orderItems":[{"id":8}]}`, func(err error) bool {
			_, ok := errors.AsType[*cmdutil.UsageError](err)
			return ok && strings.Contains(err.Error(), "order 42 has no item 7")
		}, false},
		"order and item there": {http.StatusOK, `{"id":42,"orderItems":[{"id":7,"price":10}]}`, func(err error) bool { return err == nil }, true},
	} {
		t.Run(name, func(t *testing.T) {
			var seen []string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				seen = append(seen, r.Method+" "+r.URL.Path)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.order))
			}))
			t.Cleanup(srv.Close)
			cmd := cmdForRefund(t, srv, true)
			err := runRefund(cmd, nil)
			if !tc.check(err) {
				t.Errorf("dry run = %v", err)
			}
			if got := strings.Join(seen, ","); got != "GET /core/v1/orders/42" {
				t.Errorf("sent %q, want only the order's GET", got)
			}
			stdout := cmdutil.Out(cmd).Writer.(*bytes.Buffer).String()
			if got := strings.Contains(stdout, "POST /core/v1/refund"); got != tc.preview {
				t.Errorf("stdout = %q, want the refund previewed: %v", stdout, tc.preview)
			}
		})
	}
}
