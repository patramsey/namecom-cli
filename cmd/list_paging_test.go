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

// pagedListBodies is every list in pagedLists with its path and page p's one
// item.
var pagedListBodies = []struct {
	args []string
	path string
	body func(p int) string
}{
	{[]string{"domain", "list"}, "/core/v1/domains", func(p int) string { return fmt.Sprintf(`"domains":[{"domainName":"d%d.example"}]`, p) }},
	{[]string{"dns", "list", "example.com"}, "/core/v1/domains/example.com/records", func(p int) string { return fmt.Sprintf(`"records":[{"id":%d,"type":"A"}]`, p) }},
	{[]string{"email", "list", "example.com"}, "/core/v1/domains/example.com/email/forwarding", func(p int) string { return fmt.Sprintf(`"emailForwarding":[{"emailBox":"box%d"}]`, p) }},
	{[]string{"url", "list", "example.com"}, "/core/v1/urlforwarding/example.com", func(p int) string { return fmt.Sprintf(`"urlForwarding":[{"id":%d}]`, p) }},
	{[]string{"vanity-ns", "list", "example.com"}, "/core/v1/domains/example.com/vanity_nameservers", func(p int) string {
		return fmt.Sprintf(`"vanityNameservers":[{"hostname":"ns%d.example.com"}]`, p)
	}},
	{[]string{"transfer", "list"}, "/core/v1/transfers", func(p int) string { return fmt.Sprintf(`"transfers":[{"domainName":"t%d.example"}]`, p) }},
	{[]string{"order", "list"}, "/core/v1/orders", func(p int) string { return fmt.Sprintf(`"orders":[{"id":%d}]`, p) }},
	{[]string{"contact", "unverified"}, "/core/v1/contacts/unverified", func(p int) string { return fmt.Sprintf(`"unverifiedContacts":[{"verificationId":%d}]`, p) }},
}

// TestPagedLists_RequestSize pins #290's request size: when a list fetches
// every page, each request asks for 1000 items whatever --limit says, so
// `domain list --limit 2 --all` no longer sends one request per two domains.
// --limit still sets the size of the one page a list fetches without --all.
// Each list is served three pages; the counts are exact.
func TestPagedLists_RequestSize(t *testing.T) {
	withConfig(t, loneProfile)
	modes := []struct {
		name    string
		flags   []string
		pages   []int
		perPage int // 0: none sent, the API's default
		warn    bool
	}{
		{name: "one page at the API's default", pages: []int{1}},
		{name: "--limit 2 is one page of 2", flags: []string{"--limit", "2"}, pages: []int{1}, perPage: 2},
		{name: "--all at 1000", flags: []string{"--all"}, pages: []int{1, 2, 3}, perPage: 1000},
		{name: "--all --limit 2 at 1000, with a warning", flags: []string{"--all", "--limit", "2"}, pages: []int{1, 2, 3}, perPage: 1000, warn: true},
		{name: "-q at 1000", flags: []string{"-q"}, pages: []int{1, 2, 3}, perPage: 1000},
	}
	for _, l := range pagedListBodies {
		for _, m := range modes {
			t.Run(strings.Join(l.args[:2], " ")+"/"+m.name, func(t *testing.T) {
				resetFlags(t, l.args)
				srv, reqs := pagedStub(t, l.path, 3, l.body)
				args := append(append([]string{"--base-url", srv.URL, "-o", "table"}, l.args...), m.flags...)
				_, stderr := runCapturingBoth(t, args)

				got := reqs()
				if fmt.Sprint(pagesOf(got)) != fmt.Sprint(m.pages) {
					t.Errorf("fetched pages %v, want %v", pagesOf(got), m.pages)
				}
				for _, r := range got {
					if r.perPage != m.perPage {
						t.Errorf("page %d sent perPage=%d, want %d", r.page, r.perPage, m.perPage)
					}
				}
				if warned := strings.Contains(stderr, "--limit does not apply with --all"); warned != m.warn {
					t.Errorf("--limit warning printed = %v, want %v; stderr:\n%s", warned, m.warn, stderr)
				}
			})
		}
	}
}

// TestDNSList_PastLastPageIsEmpty pins #290: the records API answers a page
// past the last with page 1, so `dns list --page 5` printed the first record
// again and a script paging until an empty page never stopped. It is an
// empty page now, in one request, whether or not --limit set the page size.
func TestDNSList_PastLastPageIsEmpty(t *testing.T) {
	withConfig(t, loneProfile)
	for _, tc := range []struct {
		name  string
		flags []string
	}{
		{"--limit 1", []string{"--page", "5", "--limit", "1"}},
		{"the API's page size", []string{"--page", "5"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := []string{"dns", "list", "example.com"}
			var mu sync.Mutex
			var n int
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				mu.Lock()
				n++
				mu.Unlock()
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"records":[{"id":1,"type":"A","host":"@","answer":"192.0.2.1","ttl":300}],"totalCount":1,"from":1,"to":1}`))
			}))
			t.Cleanup(srv.Close)

			resetFlags(t, args)
			stdout, _ := runCapturingBoth(t, append(append([]string{"--base-url", srv.URL, "-o", "json"}, args...), tc.flags...))
			if got := strings.Join(strings.Fields(stdout), ""); got != `{"data":[]}` {
				t.Errorf("stdout = %s, want an empty page", got)
			}
			resetFlags(t, args)
			_, stderr := runCapturingBoth(t, append(append([]string{"--base-url", srv.URL, "-o", "table"}, args...), tc.flags...))
			if !strings.Contains(stderr, "No DNS records on page 5") || strings.Contains(stderr, "first record") {
				t.Errorf("stderr = %q, want it to say page 5 is past the end", stderr)
			}
			mu.Lock()
			defer mu.Unlock()
			if n != 2 {
				t.Errorf("sent %d requests for two runs, want 2", n)
			}
		})
	}
}

// TestDNSExport_ReadsZoneAtMaxPerPage: export, like sync, import
// --skip-existing and create --if-not-exists, reads the whole zone, and paged
// it at the API's default of 500 (#294). It asks for 1000 a page.
func TestDNSExport_ReadsZoneAtMaxPerPage(t *testing.T) {
	withConfig(t, loneProfile)
	args := []string{"dns", "export", "example.com"}
	resetFlags(t, args)
	srv, reqs := pagedStub(t, "/core/v1/domains/example.com/records", 2, func(p int) string {
		return fmt.Sprintf(`"records":[{"id":%d,"type":"A","host":"@","answer":"192.0.2.%d","ttl":300}]`, p, p)
	})
	runCapturingBoth(t, append([]string{"--base-url", srv.URL}, args...))
	if got := reqs(); fmt.Sprint(got) != fmt.Sprint([]listRequest{{1, 1000}, {2, 1000}}) {
		t.Errorf("sent %v, want pages 1 and 2 at perPage 1000", got)
	}
}

// TestPagedLists_EmptyPastLastPage pins #290's empty-state hints: past the
// last page, `url list --page 2` suggested creating the first forwarding and
// `contact unverified --page 3` that new verifications take ~10 minutes.
func TestPagedLists_EmptyPastLastPage(t *testing.T) {
	withConfig(t, loneProfile)
	for _, l := range pagedListBodies {
		t.Run(strings.Join(l.args[:2], " "), func(t *testing.T) {
			resetFlags(t, l.args)
			key := strings.Split(l.body(1), `"`)[1]
			srv, reqs := pagedStub(t, l.path, 1, func(int) string { return `"` + key + `":[]` })
			_, stderr := runCapturingBoth(t, append(append([]string{"--base-url", srv.URL, "-o", "table"}, l.args...), "--page", "3"))
			if !strings.Contains(stderr, "on page 3") || !strings.Contains(stderr, "past the last page") {
				t.Errorf("stderr = %q, want it to say page 3 is past the last page", stderr)
			}
			for _, wrong := range []string{"to add", "first domain", "10 minutes", "initiate"} {
				if strings.Contains(stderr, wrong) {
					t.Errorf("stderr = %q, has the empty-list hint %q", stderr, wrong)
				}
			}
			if n := len(reqs()); n != 1 {
				t.Errorf("sent %d requests, want 1", n)
			}
		})
	}
}

// TestContactUnverified_ShowingFooter pins #290: under --limit, the footer
// "1 unverified contact · --page 2 for more" read as the total. It now gives
// the range and the total, as domain list does.
func TestContactUnverified_ShowingFooter(t *testing.T) {
	withConfig(t, loneProfile)
	args := []string{"contact", "unverified"}
	resetFlags(t, args)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"unverifiedContacts":[{"verificationId":1,"email":"a@example.com"}],"from":1,"to":1,"lastPage":2,"nextPage":2,"totalCount":2}`))
	}))
	t.Cleanup(srv.Close)
	_, stderr := runCapturingBoth(t, append([]string{"--base-url", srv.URL, "-o", "table"}, append(args, "--limit", "1")...))
	if !strings.Contains(stderr, "Showing 1–1 of 2 unverified contacts") {
		t.Errorf("stderr = %q, want the Showing 1–1 of 2 footer", stderr)
	}
}
