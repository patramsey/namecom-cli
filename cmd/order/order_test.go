package order

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
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
		if pageQ != "" {
			fmt.Sscanf(pageQ, "%d", &pageNum)
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
		{101},  // page 1 — NextPage=2
		{102},  // page 2 — no NextPage
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
