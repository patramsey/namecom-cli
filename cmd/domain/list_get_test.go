package domain

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"net/http/httptest"
	"testing"

	"github.com/patramsey/namecom-cli/cmd/cmdutil"
	"github.com/patramsey/namecom-cli/internal/api"
	"github.com/patramsey/namecom-cli/internal/api/gen"
	"github.com/patramsey/namecom-cli/internal/output"
	"github.com/spf13/cobra"
)

// ---- filterToWildcard -------------------------------------------------------

func TestFilterToWildcard(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"acme", "*acme*"},
		{"acme.io", "*acme.io*"},
		{"*acme", "*acme"},    // already has wildcard — leave alone
		{"acme*", "acme*"},    // already has wildcard — leave alone
		{"*acme*", "*acme*"},  // already has wildcard — leave alone
	}
	for _, tt := range tests {
		if got := filterToWildcard(tt.input); got != tt.want {
			t.Errorf("filterToWildcard(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// ---- pagination + filter behavior -------------------------------------------

// domainServer builds an httptest.Server that serves paginated domain list
// responses. pages is a slice of domain-name slices; page i returns pages[i]
// and sets NextPage to i+2 if there's a next page, else 0.
// It also records every request URL so tests can inspect query params.
func domainServer(t *testing.T, pages [][]string) (*httptest.Server, *[]string) {
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
		var domains []gen.DomainResponsePayload
		for _, name := range pages[idx] {
			n := name
			domains = append(domains, gen.DomainResponsePayload{DomainName: n})
		}
		var nextPage int32
		if idx+1 < len(pages) {
			nextPage = int32(idx + 2)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(gen.ListDomainsResponseSchema{
			Domains:  domains,
			NextPage: &nextPage,
		})
	}))
	t.Cleanup(srv.Close)
	return srv, &received
}

// cmdForDomainList builds a cobra.Command wired with a test API client,
// stdout buffer, and stderr buffer. It registers the same flags as init()
// so cmd.Flags().Changed() works correctly.
func cmdForDomainList(t *testing.T, srv *httptest.Server, stdout, stderr *bytes.Buffer) *cobra.Command {
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

	// Register the same flags runList checks via cmd.Flags().Changed().
	cmd.Flags().StringVar(&listFilter, "filter", "", "")
	cmd.Flags().StringVar(&listTLD, "tld", "", "")
	cmd.Flags().StringVar(&listExpiringAfter, "expiring-after", "", "")
	cmd.Flags().StringVar(&listExpiringBefore, "expiring-before", "", "")
	cmd.Flags().BoolVar(&listAll, "all", false, "")
	cmd.Flags().StringVar(&listSort, "sort", "", "")
	return cmd
}

func TestDomainList_PaginationStopsAtFirstPage(t *testing.T) {
	srv, requests := domainServer(t, [][]string{
		{"acme.io", "beta.io"}, // page 1 — NextPage=2 set
		{"gamma.io"},           // page 2 — should NOT be fetched
	})
	var stdout, stderr bytes.Buffer
	cmd := cmdForDomainList(t, srv, &stdout, &stderr)
	listAll, listFilter, listTLD, listExpiringAfter, listExpiringBefore = false, "", "", "", ""

	if err := runList(cmd, nil); err != nil {
		t.Fatalf("runList: %v", err)
	}
	if len(*requests) != 1 {
		t.Errorf("expected 1 request (first page only), got %d: %v", len(*requests), *requests)
	}
	out := stdout.String()
	if !contains(out, "acme.io") || !contains(out, "beta.io") {
		t.Errorf("output missing expected domains: %q", out)
	}
	if contains(out, "gamma.io") {
		t.Errorf("output contains domain from page 2 which should not have been fetched")
	}
	if !contains(stdout.String(), "Showing first page") {
		t.Errorf("expected pagination hint in stdout: %q", stdout.String())
	}
}

func TestDomainList_AllFetchesAllPages(t *testing.T) {
	srv, requests := domainServer(t, [][]string{
		{"acme.io"},  // page 1
		{"beta.io"},  // page 2
		{"gamma.io"}, // page 3 — no NextPage
	})
	var stdout, stderr bytes.Buffer
	cmd := cmdForDomainList(t, srv, &stdout, &stderr)
	listAll, listFilter, listTLD, listExpiringAfter, listExpiringBefore = false, "", "", "", ""
	if err := cmd.ParseFlags([]string{"--all"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}

	if err := runList(cmd, nil); err != nil {
		t.Fatalf("runList: %v", err)
	}
	if len(*requests) != 3 {
		t.Errorf("expected 3 requests (all pages), got %d: %v", len(*requests), *requests)
	}
	out := stdout.String()
	for _, d := range []string{"acme.io", "beta.io", "gamma.io"} {
		if !contains(out, d) {
			t.Errorf("output missing %q", d)
		}
	}
	if contains(stdout.String(), "Showing first page") {
		t.Errorf("should not show pagination hint when --all fetches everything")
	}
}

func TestDomainList_FilterWrapsWildcardAndAutoPages(t *testing.T) {
	srv, requests := domainServer(t, [][]string{
		{"acme.io"},  // page 1 — NextPage=2
		{"acme.com"}, // page 2 — no NextPage
	})
	var stdout, stderr bytes.Buffer
	cmd := cmdForDomainList(t, srv, &stdout, &stderr)
	listAll, listExpiringAfter, listExpiringBefore, listTLD = false, "", "", ""
	if err := cmd.ParseFlags([]string{"--filter", "acme"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}

	if err := runList(cmd, nil); err != nil {
		t.Fatalf("runList: %v", err)
	}
	if len(*requests) != 2 {
		t.Errorf("expected 2 requests (filter auto-paginates), got %d: %v", len(*requests), *requests)
	}
	for _, u := range *requests {
		if !contains(u, "domainName=%2Aacme%2A") && !contains(u, "domainName=*acme*") {
			t.Errorf("request URL missing wildcard-wrapped filter: %q", u)
		}
	}
}

func TestDomainList_TLDFilterPassedToAPI(t *testing.T) {
	srv, requests := domainServer(t, [][]string{{"acme.io"}})
	var stdout, stderr bytes.Buffer
	cmd := cmdForDomainList(t, srv, &stdout, &stderr)
	listAll, listFilter, listExpiringAfter, listExpiringBefore = false, "", "", ""
	if err := cmd.ParseFlags([]string{"--tld", "io"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}

	if err := runList(cmd, nil); err != nil {
		t.Fatalf("runList: %v", err)
	}
	if len(*requests) == 0 || !contains((*requests)[0], "tld=io") {
		t.Errorf("expected tld=io in request URL, got: %v", *requests)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 ||
		func() bool {
			for i := 0; i+len(sub) <= len(s); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
			return false
		}())
}
