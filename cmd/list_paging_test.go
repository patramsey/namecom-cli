package cmd

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/patramsey/namecom-cli/cmd/cmdutil"
)

// listRequest is one GET a paged-list stub served: the page and perPage it
// asked for, perPage 0 when none was sent.
type listRequest struct{ page, perPage int }

// pagedStub serves lastPage pages at path, page p's items from body(p), and
// records each list GET. Anything else is answered with {} and not recorded.
func pagedStub(t *testing.T, path string, lastPage int, body func(p int) string) (*httptest.Server, func() []listRequest) {
	t.Helper()
	var mu sync.Mutex
	var reqs []listRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodGet || r.URL.Path != path {
			_, _ = w.Write([]byte(`{}`))
			return
		}
		p := 1
		if s := r.URL.Query().Get("page"); s != "" {
			p, _ = strconv.Atoi(s)
		}
		pp, _ := strconv.Atoi(r.URL.Query().Get("perPage"))
		mu.Lock()
		reqs = append(reqs, listRequest{p, pp})
		mu.Unlock()
		next := ""
		if p < lastPage {
			next = fmt.Sprintf(`,"nextPage":%d`, p+1)
		}
		_, _ = fmt.Fprintf(w, `{%s,"totalCount":%d,"lastPage":%d,"from":%d,"to":%d%s}`, body(p), lastPage, lastPage, p, p, next)
	}))
	t.Cleanup(srv.Close)
	return srv, func() []listRequest {
		mu.Lock()
		defer mu.Unlock()
		return append([]listRequest(nil), reqs...)
	}
}

// pagesOf is the pages reqs asked for, in order. domain list fetches pages
// after the first in parallel, so their order is not fixed.
func pagesOf(reqs []listRequest) []int {
	pages := make([]int, len(reqs))
	for i, r := range reqs {
		pages[i] = r.page
	}
	slices.Sort(pages)
	return pages
}

// TestFilteredLists_OnePage pins #281: a filter on domain, order or dns list
// made it fetch every page, at --limit per request, whatever --page and
// --limit said: `order list --status failed --limit 2` sent 52 GETs, and
// `domain list --expiring-after … --limit 1` one per matching domain. A filter
// now changes what a page holds, never how many are fetched: one page, and
// the "--page N for more" footer when there are more, as without a filter.
//
// Each list is served three pages of one matching item.
func TestFilteredLists_OnePage(t *testing.T) {
	withConfig(t, loneProfile)

	lists := []struct {
		name string
		args []string
		path string
		body func(p int) string
	}{
		{
			name: "domain list --tld", args: []string{"domain", "list", "--tld", "io"}, path: "/core/v1/domains",
			body: func(p int) string { return fmt.Sprintf(`"domains":[{"domainName":"d%d.io"}]`, p) },
		},
		{
			name: "domain list --expiring-after", args: []string{"domain", "list", "--expiring-after", "2030-01-01"}, path: "/core/v1/domains",
			body: func(p int) string { return fmt.Sprintf(`"domains":[{"domainName":"d%d.io"}]`, p) },
		},
		{
			name: "order list --status", args: []string{"order", "list", "--status", "failed"}, path: "/core/v1/orders",
			body: func(p int) string { return fmt.Sprintf(`"orders":[{"id":%d}]`, p) },
		},
		{
			name: "order list --domain", args: []string{"order", "list", "--domain", "*smoke*"}, path: "/core/v1/orders",
			body: func(p int) string { return fmt.Sprintf(`"orders":[{"id":%d}]`, p) },
		},
		{
			name: "dns list --type", args: []string{"dns", "list", "example.com", "--type", "A"}, path: "/core/v1/domains/example.com/records",
			body: func(p int) string { return fmt.Sprintf(`"records":[{"id":%d,"type":"A","host":"www"}]`, p) },
		},
		{
			name: "dns list --host", args: []string{"dns", "list", "example.com", "--host", "www"}, path: "/core/v1/domains/example.com/records",
			body: func(p int) string { return fmt.Sprintf(`"records":[{"id":%d,"type":"A","host":"www"}]`, p) },
		},
	}
	modes := []struct {
		name  string
		flags []string
		pages []int
		more  string
	}{
		{name: "no paging flags", pages: []int{1}, more: "--page 2 for more"},
		{name: "--limit 1", flags: []string{"--limit", "1"}, pages: []int{1}, more: "--page 2 for more"},
		{name: "--page 2", flags: []string{"--page", "2"}, pages: []int{2}, more: "--page 3 for more"},
		{name: "--all", flags: []string{"--all"}, pages: []int{1, 2, 3}},
	}

	for _, l := range lists {
		for _, m := range modes {
			t.Run(l.name+"/"+m.name, func(t *testing.T) {
				resetFlags(t, l.args[:2])
				srv, reqs := pagedStub(t, l.path, 3, l.body)
				args := append(append([]string{"--base-url", srv.URL, "-o", "table"}, l.args...), m.flags...)
				_, stderr := runCapturingBoth(t, args)

				if got := pagesOf(reqs()); fmt.Sprint(got) != fmt.Sprint(m.pages) {
					t.Errorf("namecom %s fetched pages %v, want %v", strings.Join(args[4:], " "), got, m.pages)
				}
				if m.more == "" {
					if strings.Contains(stderr, "for more") {
						t.Errorf("stderr has a more-pages footer after the last page:\n%s", stderr)
					}
				} else if !strings.Contains(stderr, m.more) {
					t.Errorf("stderr = %q, want it to contain %q", stderr, m.more)
				}
			})
		}
	}
}

// TestDNSList_FilterMissOnPageSaysMore pins the dns half of #281. --type and
// --host filter the page fetched, client-side, so a page with no match is
// not a zone with no match: the footer must still say there are more pages,
// where the empty-state message used to end the output.
func TestDNSList_FilterMissOnPageSaysMore(t *testing.T) {
	withConfig(t, loneProfile)
	args := []string{"dns", "list", "example.com"}
	resetFlags(t, args)
	srv, reqs := pagedStub(t, "/core/v1/domains/example.com/records", 2, func(p int) string {
		return fmt.Sprintf(`"records":[{"id":%d,"type":"TXT","host":"www"}]`, p)
	})
	_, stderr := runCapturingBoth(t, append([]string{"--base-url", srv.URL, "-o", "table"}, append(args, "--type", "A")...))
	if n := len(reqs()); n != 1 {
		t.Errorf("sent %d requests, want 1", n)
	}
	if !strings.Contains(stderr, "--page 2 for more") {
		t.Errorf("stderr = %q, want it to say --page 2 for more", stderr)
	}
}

// TestEnumFilters_UnknownValueIsUsage pins #281: the API ignores a filter
// value it does not know, so `order list --status bogus` walked every order
// in the account, and `dns list --type BOGUS` and `domain claims
// --purchase-type bogus` exited 0. Each is a usage error before any request.
func TestEnumFilters_UnknownValueIsUsage(t *testing.T) {
	withConfig(t, loneProfile)
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		t.Errorf("no request expected, got %s %s", r.Method, r.URL)
	}))
	t.Cleanup(srv.Close)
	for _, args := range [][]string{
		{"order", "list", "--status", "bogus"},
		{"dns", "list", "example.com", "--type", "BOGUS"},
		{"domain", "claims", "example.com", "--purchase-type", "bogus"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			resetFlags(t, args[:2])
			err := cmdutil.ClassifyCobraUsage(executeRoot(t, append([]string{"--base-url", srv.URL, "-o", "json"}, args...)...))
			if got := exitCode(err); got != 2 {
				t.Errorf("namecom %s exited %d (%v), want 2", strings.Join(args, " "), got, err)
			}
		})
	}
}
