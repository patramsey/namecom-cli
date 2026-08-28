package domain

import (
	"bytes"
	"context"
	"encoding/json"
	coreapigo "github.com/namedotcom/core-api-go"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/patramsey/namecom-cli/cmd/cmdutil"
	"github.com/patramsey/namecom-cli/internal/api"
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
		{"*acme", "*acme"},   // already has wildcard — leave alone
		{"acme*", "acme*"},   // already has wildcard — leave alone
		{"*acme*", "*acme*"}, // already has wildcard — leave alone
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
//
// It also records every request URL so tests can inspect query params. That
// recording is mutex-guarded because runList fetches pages 2..N concurrently
// (errgroup, SetLimit(5)), so the handler runs on several goroutines at once —
// an unsynchronized append here is a real race, and -race caught it
// intermittently: it only fires when two page requests actually overlap.
//
// The accessor returns a copy rather than the slice itself. Handing back a
// *[]string, as this used to, makes an unlocked read the path of least
// resistance and invites the same bug back at the call site.
func domainServer(t *testing.T, pages [][]string) (*httptest.Server, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var received []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		received = append(received, r.URL.String())
		mu.Unlock()
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
		var domains []*coreapigo.DomainResponsePayload
		for _, name := range pages[idx] {
			n := name
			domains = append(domains, &coreapigo.DomainResponsePayload{DomainName: n})
		}
		var nextPage int
		lastPage := len(pages)
		if idx+1 < len(pages) {
			nextPage = idx + 2
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(coreapigo.ListDomainsResponse{
			Domains:  domains,
			NextPage: &nextPage,
			LastPage: &lastPage,
		})
	}))
	t.Cleanup(srv.Close)
	return srv, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(received)
	}
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
	cmd.Flags().IntVar(&listPage, "page", 1, "")
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
	listAll, listFilter, listTLD, listExpiringAfter, listExpiringBefore, listPage = false, "", "", "", "", 1

	if err := runList(cmd, nil); err != nil {
		t.Fatalf("runList: %v", err)
	}
	if len(requests()) != 1 {
		t.Errorf("expected 1 request (first page only), got %d: %v", len(requests()), requests())
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
	listAll, listFilter, listTLD, listExpiringAfter, listExpiringBefore, listPage = false, "", "", "", "", 1
	if err := cmd.ParseFlags([]string{"--all"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}

	if err := runList(cmd, nil); err != nil {
		t.Fatalf("runList: %v", err)
	}
	if len(requests()) != 3 {
		t.Errorf("expected 3 requests (all pages), got %d: %v", len(requests()), requests())
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
	listAll, listExpiringAfter, listExpiringBefore, listTLD, listPage = false, "", "", "", 1
	if err := cmd.ParseFlags([]string{"--filter", "acme"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}

	if err := runList(cmd, nil); err != nil {
		t.Fatalf("runList: %v", err)
	}
	if len(requests()) != 2 {
		t.Errorf("expected 2 requests (filter auto-paginates), got %d: %v", len(requests()), requests())
	}
	for _, u := range requests() {
		if !contains(u, "domainName=%2Aacme%2A") && !contains(u, "domainName=*acme*") {
			t.Errorf("request URL missing wildcard-wrapped filter: %q", u)
		}
	}
}

func TestDomainList_TLDFilterPassedToAPI(t *testing.T) {
	srv, requests := domainServer(t, [][]string{{"acme.io"}})
	var stdout, stderr bytes.Buffer
	cmd := cmdForDomainList(t, srv, &stdout, &stderr)
	listAll, listFilter, listExpiringAfter, listExpiringBefore, listPage = false, "", "", "", 1
	if err := cmd.ParseFlags([]string{"--tld", "io"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}

	if err := runList(cmd, nil); err != nil {
		t.Fatalf("runList: %v", err)
	}
	if len(requests()) == 0 || !contains(requests()[0], "tld=io") {
		t.Errorf("expected tld=io in request URL, got: %v", requests())
	}
}

// domainServerNoLastPage serves paginated responses where LastPage is omitted
// (nil), so only NextPage is available — exercises the sequential fallback path.
func domainServerNoLastPage(t *testing.T, pages [][]string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
		var domains []*coreapigo.DomainResponsePayload
		for _, name := range pages[idx] {
			n := name
			domains = append(domains, &coreapigo.DomainResponsePayload{DomainName: n})
		}
		var nextPage int
		if idx+1 < len(pages) {
			nextPage = idx + 2
		}
		w.Header().Set("Content-Type", "application/json")
		// Deliberately omit LastPage to trigger the sequential fallback.
		_ = json.NewEncoder(w).Encode(coreapigo.ListDomainsResponse{
			Domains:  domains,
			NextPage: &nextPage,
		})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestDomainList_AllSequentialFallbackWhenNoLastPage(t *testing.T) {
	srv := domainServerNoLastPage(t, [][]string{
		{"alpha.com"}, // page 1 — NextPage=2, no LastPage
		{"beta.com"},  // page 2 — NextPage=3, no LastPage
		{"gamma.com"}, // page 3 — NextPage=0 (no more)
	})
	var stdout, stderr bytes.Buffer
	cmd := cmdForDomainList(t, srv, &stdout, &stderr)
	listAll, listFilter, listTLD, listExpiringAfter, listExpiringBefore, listPage = false, "", "", "", "", 1
	if err := cmd.ParseFlags([]string{"--all"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}

	if err := runList(cmd, nil); err != nil {
		t.Fatalf("runList: %v", err)
	}
	out := stdout.String()
	for _, d := range []string{"alpha.com", "beta.com", "gamma.com"} {
		if !contains(out, d) {
			t.Errorf("output missing %q — sequential NextPage walk may be broken", d)
		}
	}
}

func TestDomainList_EmptyResult(t *testing.T) {
	srv, _ := domainServer(t, [][]string{{}})
	var stdout, stderr bytes.Buffer
	cmd := cmdForDomainList(t, srv, &stdout, &stderr)
	listAll, listFilter, listTLD, listExpiringAfter, listExpiringBefore, listPage = false, "", "", "", "", 1

	if err := runList(cmd, nil); err != nil {
		t.Fatalf("runList: %v", err)
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

// ---- domain get -------------------------------------------------------------

func cmdForDomainGet(t *testing.T, srv *httptest.Server) *cobra.Command {
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

func TestDomainGet_BadDomain(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("API should not be called for pre-flight validation failure: %s %s", r.Method, r.URL)
		http.Error(w, "unexpected call", http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	cmd := cmdForDomainGet(t, srv)
	if err := runGet(cmd, []string{"nodot"}); err == nil {
		t.Fatal("expected error for domain without dot, got nil")
	}
}

func TestDomainGet_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(coreapigo.DomainResponsePayload{DomainName: "example.com"})
	}))
	t.Cleanup(srv.Close)

	cmd := cmdForDomainGet(t, srv)
	if err := runGet(cmd, []string{"example.com"}); err != nil {
		t.Fatalf("runGet: %v", err)
	}
}

func TestDomainGet_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"not found","details":"domain not found"}`))
	}))
	t.Cleanup(srv.Close)

	cmd := cmdForDomainGet(t, srv)
	err := runGet(cmd, []string{"example.com"})
	if err == nil {
		t.Fatal("expected error for 404 domain, got nil")
	}
	if !contains(err.Error(), "not found") {
		t.Errorf("expected 'not found' in error, got: %v", err)
	}
}

func TestDomainGet_DomainNormalized(t *testing.T) {
	var receivedPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(coreapigo.DomainResponsePayload{DomainName: "example.com"})
	}))
	t.Cleanup(srv.Close)

	cmd := cmdForDomainGet(t, srv)
	if err := runGet(cmd, []string{"EXAMPLE.COM"}); err != nil {
		t.Fatalf("runGet: %v", err)
	}
	if contains(receivedPath, "EXAMPLE") {
		t.Errorf("domain not normalized in request path: %q", receivedPath)
	}
	if !contains(receivedPath, "example.com") {
		t.Errorf("expected 'example.com' in path, got: %q", receivedPath)
	}
}

// TestDomainList_JSONEnvelope covers the JSON and YAML branches of the list
// output. Function-level coverage read as covered because runList is entered
// through the table path; the format switch inside it never ran.
//
// `domain list` is the most likely of all these commands to be consumed by a
// script — it is the one that enumerates what an account owns — so its
// envelope, and the nextPage that tells a caller to ask for more, are worth
// pinning.
func TestDomainList_JSONEnvelope(t *testing.T) {
	for _, tc := range []struct {
		name   string
		format output.Format
		want   string
	}{
		{"json", output.FormatJSON, `"data"`},
		{"yaml", output.FormatYAML, "data:"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				// nextPage present and --all unset, so the walk stops early
				// with more available — the branch that fills the envelope's
				// nextPage.
				_, _ = w.Write([]byte(`{"domains":[{"domainName":"example.com","expireDate":"2027-01-01"}],` +
					`"totalCount":2,"nextPage":2,"lastPage":2}`))
			}))
			t.Cleanup(srv.Close)

			var stdout, stderr bytes.Buffer
			cmd := cmdForDomainList(t, srv, &stdout, &stderr)
			out := &output.Config{
				Format: tc.format, Color: output.ColorNever,
				Writer: &stdout, EWriter: &stderr,
			}
			cmd.SetContext(context.WithValue(cmd.Context(), cmdutil.KeyOutput, out))

			if err := runList(cmd, nil); err != nil {
				t.Fatalf("runList: %v", err)
			}
			got := stdout.String()
			if !strings.Contains(got, tc.want) {
				t.Errorf("expected a %s envelope containing %q, got: %q", tc.name, tc.want, got)
			}
			// The payload, not just the envelope: `"data": null` contains
			// `"data"` too, so the key alone passes on an empty result.
			if !strings.Contains(got, "example.com") {
				t.Errorf("%s envelope carried no domains: %q", tc.name, got)
			}
			if !strings.Contains(strings.ToLower(got), "nextpage") {
				t.Errorf("%s envelope omitted nextPage despite more results: %q", tc.name, got)
			}
		})
	}
}
