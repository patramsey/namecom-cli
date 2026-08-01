package order

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/patramsey/namecom-cli/cmd/cmdutil"
	"github.com/patramsey/namecom-cli/internal/api"
	"github.com/patramsey/namecom-cli/internal/api/gen"
	"github.com/patramsey/namecom-cli/internal/output"
	"github.com/spf13/cobra"
)

// orderServer builds an httptest.Server that serves paginated order list
// responses. pages is a slice of order-ID slices; page i returns orders with
// those IDs and sets NextPage accordingly. It records all request URLs.
func orderServer(t *testing.T, pages [][]int32) (*httptest.Server, *[]string) {
	t.Helper()
	var received []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received = append(received, r.URL.String())
		pageQ := r.URL.Query().Get("page")
		pageNum := 1
		if n, err := strconv.Atoi(pageQ); pageQ != "" && err == nil {
			pageNum = n
		}
		idx := pageNum - 1
		if idx < 0 || idx >= len(pages) {
			http.Error(w, "page out of range", http.StatusNotFound)
			return
		}
		var orders []gen.Order
		for i := range pages[idx] {
			orders = append(orders, gen.Order{Id: &pages[idx][i]})
		}
		var nextPage int32
		if idx+1 < len(pages) {
			nextPage = int32(idx + 2)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(gen.ListOrdersResponseSchema{
			Orders:   orders,
			NextPage: &nextPage,
		})
	}))
	t.Cleanup(srv.Close)
	return srv, &received
}

// cmdForOrderList builds a cobra.Command wired with a test API client,
// stdout and stderr buffers, and the same flags as init() so Changed() works.
func cmdForOrderList(t *testing.T, srv *httptest.Server, stdout, stderr *bytes.Buffer) *cobra.Command {
	t.Helper()
	client, err := api.New(api.Options{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("api.New: %v", err)
	}
	out := &output.Config{Format: output.FormatTable, Color: output.ColorNever, Writer: stdout, EWriter: stderr}

	cmd := &cobra.Command{}
	ctx := context.WithValue(context.Background(), cmdutil.KeyOutput, out)
	ctx = context.WithValue(ctx, cmdutil.KeyClient, client)
	cmd.SetContext(ctx)

	cmd.Flags().BoolVar(&listAll, "all", false, "")
	cmd.Flags().StringVar(&listDomain, "domain", "", "")
	cmd.Flags().StringVar(&listSince, "since", "", "")
	cmd.Flags().StringVar(&listUntil, "until", "", "")
	cmd.Flags().StringVar(&listStatus, "status", "", "")
	return cmd
}

func TestOrderList_PaginationStopsAtFirstPage(t *testing.T) {
	srv, requests := orderServer(t, [][]int32{
		{101, 102}, // page 1 — NextPage=2 set
		{103},      // page 2 — should NOT be fetched
	})
	var stdout, stderr bytes.Buffer
	cmd := cmdForOrderList(t, srv, &stdout, &stderr)
	listAll, listDomain, listSince, listUntil, listStatus = false, "", "", "", ""

	if err := runList(cmd, nil); err != nil {
		t.Fatalf("runList: %v", err)
	}
	if len(*requests) != 1 {
		t.Errorf("expected 1 request (first page only), got %d: %v", len(*requests), *requests)
	}
	if !contains(stdout.String(), "Showing first page") {
		t.Errorf("expected pagination hint in output: %q", stdout.String())
	}
}

func TestOrderList_AllFetchesAllPages(t *testing.T) {
	srv, requests := orderServer(t, [][]int32{
		{101}, // page 1
		{102}, // page 2
		{103}, // page 3 — no NextPage
	})
	var stdout, stderr bytes.Buffer
	cmd := cmdForOrderList(t, srv, &stdout, &stderr)
	listAll, listDomain, listSince, listUntil, listStatus = false, "", "", "", ""
	if err := cmd.ParseFlags([]string{"--all"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}

	if err := runList(cmd, nil); err != nil {
		t.Fatalf("runList: %v", err)
	}
	if len(*requests) != 3 {
		t.Errorf("expected 3 requests (all pages), got %d: %v", len(*requests), *requests)
	}
	if contains(stdout.String(), "Showing first page") {
		t.Errorf("should not show pagination hint when --all fetches everything")
	}
}

func TestOrderList_SinceFilterPassedToAPI(t *testing.T) {
	srv, requests := orderServer(t, [][]int32{{101}})
	var stdout, stderr bytes.Buffer
	cmd := cmdForOrderList(t, srv, &stdout, &stderr)
	listAll, listDomain, listUntil, listStatus = false, "", "", ""
	if err := cmd.ParseFlags([]string{"--since", "2026-01-01"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}

	if err := runList(cmd, nil); err != nil {
		t.Fatalf("runList: %v", err)
	}
	if len(*requests) == 0 || !contains((*requests)[0], "createDateStart=2026-01-01") {
		t.Errorf("expected createDateStart=2026-01-01 in request URL, got: %v", *requests)
	}
}

func TestOrderList_StatusFilterPassedToAPI(t *testing.T) {
	srv, requests := orderServer(t, [][]int32{{101}})
	var stdout, stderr bytes.Buffer
	cmd := cmdForOrderList(t, srv, &stdout, &stderr)
	listAll, listDomain, listSince, listUntil = false, "", "", ""
	if err := cmd.ParseFlags([]string{"--status", "success"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}

	if err := runList(cmd, nil); err != nil {
		t.Fatalf("runList: %v", err)
	}
	if len(*requests) == 0 || !contains((*requests)[0], "orderStatus=success") {
		t.Errorf("expected orderStatus=success in request URL, got: %v", *requests)
	}
}

func TestOrderList_FilterAutoPages(t *testing.T) {
	srv, requests := orderServer(t, [][]int32{
		{101}, // page 1 — NextPage=2
		{102}, // page 2 — no NextPage
	})
	var stdout, stderr bytes.Buffer
	cmd := cmdForOrderList(t, srv, &stdout, &stderr)
	listAll, listUntil, listStatus = false, "", ""
	if err := cmd.ParseFlags([]string{"--domain", "acme.io", "--since", "2026-01-01"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}

	if err := runList(cmd, nil); err != nil {
		t.Fatalf("runList: %v", err)
	}
	if len(*requests) != 2 {
		t.Errorf("expected 2 requests (filter auto-paginates), got %d: %v", len(*requests), *requests)
	}
}

func contains(s, sub string) bool {
	if len(sub) == 0 {
		return true
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// ---- order get --------------------------------------------------------------

func cmdForOrderGet(t *testing.T, srv *httptest.Server) *cobra.Command {
	t.Helper()
	client, err := api.New(api.Options{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("api.New: %v", err)
	}
	var stdout bytes.Buffer
	out := &output.Config{Format: output.FormatTable, Color: output.ColorNever, Writer: &stdout, EWriter: &bytes.Buffer{}}
	cmd := &cobra.Command{}
	ctx := context.WithValue(context.Background(), cmdutil.KeyOutput, out)
	ctx = context.WithValue(ctx, cmdutil.KeyClient, client)
	cmd.SetContext(ctx)
	return cmd
}

func TestOrderGet_BadID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("API should not be called for pre-flight validation failure: %s %s", r.Method, r.URL)
		http.Error(w, "unexpected call", http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	cmd := cmdForOrderGet(t, srv)
	if err := runGet(cmd, []string{"notanumber"}); err == nil {
		t.Fatal("expected error for non-integer ID, got nil")
	}
}

func TestOrderGet_Success(t *testing.T) {
	var id int32 = 42
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(gen.Order{Id: &id})
	}))
	t.Cleanup(srv.Close)

	cmd := cmdForOrderGet(t, srv)
	if err := runGet(cmd, []string{"42"}); err != nil {
		t.Fatalf("runGet: %v", err)
	}
	// Assert what was rendered. err == nil passes while the renderer emits an
	// empty table, so the data the command exists to show can vanish silently.
	if buf, ok := cmdutil.Out(cmd).Writer.(*bytes.Buffer); ok {
		got := buf.String()
		if !strings.Contains(got, "42") {
			t.Errorf("output is missing %q (the order id):\n%s", "42", got)
		}
	} else {
		t.Fatal("output writer is not a *bytes.Buffer")
	}
}

// TestOrderRows_Currency guards a regression where the total was rendered with
// a hardcoded "$" while gen.Order carries a Currency field documented as
// ('USD', 'CNY') — so a CNY order was displayed as dollars, understating or
// overstating it by the exchange rate.
func TestOrderRows_Currency(t *testing.T) {
	usd, cny := "USD", "CNY"
	amount := float64(100)

	tests := []struct {
		name        string
		currency    *string
		wantContain string
		wantAbsent  string
	}{
		{"USD renders with a dollar sign", &usd, "$100.00", "USD"},
		{"unset currency defaults to USD", nil, "$100.00", "USD"},
		{"CNY is not rendered as dollars", &cny, "CNY", "$"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			out := &output.Config{
				Format: output.FormatTable, Color: output.ColorNever,
				Writer: &bytes.Buffer{}, EWriter: &bytes.Buffer{},
			}
			rows := orderRows(out, []gen.Order{{FinalAmount: &amount, Currency: tc.currency}})
			if len(rows) != 1 {
				t.Fatalf("expected 1 row, got %d", len(rows))
			}
			total := rows[0][3]
			if !strings.Contains(total, tc.wantContain) {
				t.Errorf("expected total to contain %q, got %q", tc.wantContain, total)
			}
			if strings.Contains(total, tc.wantAbsent) {
				t.Errorf("total should not contain %q, got %q", tc.wantAbsent, total)
			}
		})
	}
}

// ---- dry-run / real request drift -------------------------------------------

var httpMethods = map[string]bool{
	"GET": true, "POST": true, "PUT": true, "PATCH": true, "DELETE": true,
}

func dryRunLine(t *testing.T, s string) string {
	t.Helper()
	for _, line := range strings.Split(s, "\n") {
		f := strings.Fields(line)
		if len(f) >= 2 && httpMethods[f[0]] && strings.HasPrefix(f[1], "/") {
			return f[0] + " " + f[1]
		}
	}
	t.Fatalf("no dry-run METHOD/path line found in output: %q", s)
	return ""
}

// cmdForRefund builds a refund command with --order-id/--item-ids set and a
// root carrying --yes (skips the confirm) and optionally --dry-run.
func cmdForRefund(t *testing.T, srv *httptest.Server, dryRun bool) *cobra.Command {
	t.Helper()
	client, err := api.New(api.Options{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("api.New: %v", err)
	}
	out := &output.Config{
		Format: output.FormatTable, Color: output.ColorNever,
		Writer: &bytes.Buffer{}, EWriter: &bytes.Buffer{},
	}
	cmd := &cobra.Command{}
	ctx := context.WithValue(context.Background(), cmdutil.KeyOutput, out)
	ctx = context.WithValue(ctx, cmdutil.KeyClient, client)
	cmd.SetContext(ctx)
	cmd.Flags().Int32Var(&refundOrderID, "order-id", 0, "")
	cmd.Flags().Int32SliceVar(&refundItemIDs, "item-ids", nil, "")
	if err := cmd.ParseFlags([]string{"--order-id", "42", "--item-ids", "7"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	t.Cleanup(func() { refundOrderID = 0; refundItemIDs = nil })

	root := &cobra.Command{Use: "namecom"}
	var dr, yes bool
	root.PersistentFlags().BoolVar(&dr, "dry-run", false, "")
	root.PersistentFlags().BoolVarP(&yes, "yes", "y", false, "")
	if err := root.PersistentFlags().Set("yes", "true"); err != nil {
		t.Fatalf("setting yes flag: %v", err)
	}
	if dryRun {
		if err := root.PersistentFlags().Set("dry-run", "true"); err != nil {
			t.Fatalf("setting dry-run flag: %v", err)
		}
	}
	root.AddCommand(cmd)
	return cmd
}

// TestDryRunMatchesRealRequest_Refund asserts --dry-run reports the request the
// refund command actually sends. It printed "/core/v1/orders:refund"; the real
// endpoint is "/core/v1/refund". Refunds are irreversible, so a dry-run that
// misreports the endpoint is exactly the wrong place to have drift.
func TestDryRunMatchesRealRequest_Refund(t *testing.T) {
	const resp = `{}`

	var dryRunHits int
	dsrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		dryRunHits++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(resp))
	}))
	t.Cleanup(dsrv.Close)
	dcmd := cmdForRefund(t, dsrv, true)
	if err := runRefund(dcmd, nil); err != nil {
		t.Fatalf("dry-run invocation: %v", err)
	}
	// The whole promise of --dry-run on an irreversible money operation is that
	// nothing happens. Comparing only the printed method and path would not
	// notice a missing `return` that printed the preview AND then refunded.
	if dryRunHits != 0 {
		t.Errorf("--dry-run issued %d real request(s) — a refund cannot be undone", dryRunHits)
	}
	buf, ok := cmdutil.Out(dcmd).Writer.(*bytes.Buffer)
	if !ok {
		t.Fatal("output writer is not a *bytes.Buffer")
	}
	printed := dryRunLine(t, buf.String())

	var last string
	var sentBody map[string]any
	lsrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		last = r.Method + " " + r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&sentBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(resp))
	}))
	t.Cleanup(lsrv.Close)
	lcmd := cmdForRefund(t, lsrv, false)
	if err := runRefund(lcmd, nil); err != nil {
		t.Fatalf("live invocation: %v", err)
	}
	if last == "" {
		t.Fatal("no request was made")
	}

	if printed != last {
		t.Errorf("--dry-run reports %q but the command actually sends %q", printed, last)
	}

	// Assert the refund targets what the flags asked for. Matching only the
	// endpoint would let the order ID or item IDs be wrong — refunding the wrong
	// items on the wrong order, invisibly.
	if got := sentBody["orderId"]; got != float64(42) {
		t.Errorf("refund sent orderId %#v, want 42", got)
	}
	items, ok := sentBody["orderItemIds"].([]any)
	if !ok || len(items) != 1 || items[0] != float64(7) {
		t.Errorf("refund sent orderItemIds %#v, want [7]", sentBody["orderItemIds"])
	}
}

// TestRefund_DeclinedConfirmationDoesNotRefund pins the other irreversible
// path: answering "no" must not spend money. Nothing covered the declined
// branch, so discarding confirmRefund's answer went unnoticed.
func TestRefund_DeclinedConfirmationDoesNotRefund(t *testing.T) {
	defer output.StubInteractive(false)()

	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)

	// No --yes and no TTY: Confirm refuses rather than assuming consent.
	cmd := cmdForRefund(t, srv, false)
	cmd.Root().PersistentFlags().Set("yes", "false") //nolint:errcheck // test setup
	if err := runRefund(cmd, nil); err == nil {
		t.Fatal("expected refusal without confirmation")
	}
	if hits != 0 {
		t.Errorf("a refund was issued without confirmation (%d request(s))", hits)
	}
}

// TestOrderGet_ShowsOrderItems guards a workflow dead end: `order get` shared
// orderRows with `order list`, rendering the same four columns (ID/STATUS/
// DATE/TOTAL) and dropping Order.OrderItems entirely.
//
// But orderItems[].id is the required input to `order refund --item-ids`, so in
// table mode there was no way to discover it — you had to already know to run
// `-o json | jq`. Fetching a single order exists precisely to see its items.
func TestOrderGet_ShowsOrderItems(t *testing.T) {
	const resp = `{
		"id": 12345, "status": "success", "createDate": "2026-01-15", "finalAmount": 29.98,
		"orderItems": [
			{"id": 555, "name": "acme.io", "price": 19.99, "isRefundable": true, "type": "registration", "status": "success", "duration": 1, "quantity": 1},
			{"id": 556, "name": "acme.dev", "price": 9.99, "isRefundable": false, "type": "renewal", "status": "success", "duration": 1, "quantity": 1}
		]
	}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(resp))
	}))
	t.Cleanup(srv.Close)

	client, err := api.New(api.Options{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("api.New: %v", err)
	}
	var buf bytes.Buffer
	out := &output.Config{Format: output.FormatTable, Color: output.ColorNever, Writer: &buf, EWriter: &bytes.Buffer{}}
	cmd := &cobra.Command{}
	ctx := context.WithValue(context.Background(), cmdutil.KeyOutput, out)
	ctx = context.WithValue(ctx, cmdutil.KeyClient, client)
	cmd.SetContext(ctx)

	if err := runGet(cmd, []string{"12345"}); err != nil {
		t.Fatalf("runGet: %v", err)
	}
	got := buf.String()

	for _, want := range []string{"555", "556", "acme.io", "acme.dev"} {
		if !strings.Contains(got, want) {
			t.Errorf("order get should show item %q so it can be passed to --item-ids; output:\n%s", want, got)
		}
	}
	// Refundability decides whether a refund can even be attempted.
	if !strings.Contains(strings.ToLower(got), "refundable") {
		t.Errorf("order get should indicate which items are refundable; output:\n%s", got)
	}
}

// TestOrderRows_LargeAmountPrecision guards money precision. The spec declares
// the monetary fields as `type: number, format: float`, which oapi-codegen
// faithfully maps to float32 — about 7 significant decimal digits. That is
// enough for ordinary prices but not for the seven-figure sums premium domain
// sales reach: 1234567.89 stored as float32 formats as 1234567.88, a cent off,
// and re-marshals to 1234567.9 in `-o json`.
//
// The CLI performs no arithmetic on money, so nothing compounds — but it must
// at least echo the API's own figures faithfully.
func TestOrderRows_LargeAmountPrecision(t *testing.T) {
	const raw = `{"id":1,"status":"success","createDate":"2026-01-15","finalAmount":1234567.89}`

	var o gen.Order
	if err := json.Unmarshal([]byte(raw), &o); err != nil {
		t.Fatalf("decoding order: %v", err)
	}

	out := &output.Config{
		Format: output.FormatTable, Color: output.ColorNever,
		Writer: &bytes.Buffer{}, EWriter: &bytes.Buffer{},
	}
	rows := orderRows(out, []gen.Order{o})
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if got := rows[0][3]; got != "$1234567.89" {
		t.Errorf("large order total lost precision: got %q, want %q", got, "$1234567.89")
	}

	// And it must survive a JSON round-trip unchanged.
	b, err := json.Marshal(o)
	if err != nil {
		t.Fatalf("marshalling order: %v", err)
	}
	if !strings.Contains(string(b), "1234567.89") {
		t.Errorf("amount did not round-trip through JSON: %s", b)
	}
}
