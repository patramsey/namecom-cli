package cmd

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/patramsey/namecom-cli/internal/output"
)

// TestQuietPaging pins #277: -q paged fully even when --page or --limit was
// given, so `order list -q --limit 1` walked the whole history one order per
// request, and `--page 2 -q` printed every page. -q alone still pages fully
// (#99); an explicit --page or --limit fetches that one page, as it does
// without -q, and says on stderr that there is more.
//
// Every paged list is served three pages of one item, the item named after
// its page, so stdout shows which pages were printed and the request count
// shows which were fetched.
func TestQuietPaging(t *testing.T) {
	withConfig(t, loneProfile)

	lists := []struct {
		name string
		args []string
		path string
		// body is the response for page p: one item, identified by p.
		body func(p int) string
		// item is what -q prints for page p's item.
		item func(p int) string
	}{
		{
			name: "domain list", args: []string{"domain", "list"}, path: "/core/v1/domains",
			body: func(p int) string { return fmt.Sprintf(`"domains":[{"domainName":"d%d.example"}]`, p) },
			item: func(p int) string { return fmt.Sprintf("d%d.example", p) },
		},
		{
			name: "order list", args: []string{"order", "list"}, path: "/core/v1/orders",
			body: func(p int) string { return fmt.Sprintf(`"orders":[{"id":%d}]`, p) },
			item: strconv.Itoa,
		},
		{
			name: "contact unverified", args: []string{"contact", "unverified"}, path: "/core/v1/contacts/unverified",
			body: func(p int) string { return fmt.Sprintf(`"unverifiedContacts":[{"verificationId":%d}]`, p) },
			item: strconv.Itoa,
		},
		{
			name: "dns list", args: []string{"dns", "list", "example.com"}, path: "/core/v1/domains/example.com/records",
			body: func(p int) string { return fmt.Sprintf(`"records":[{"id":%d,"type":"A"}]`, p) },
			item: strconv.Itoa,
		},
		{
			name: "email list", args: []string{"email", "list", "example.com"}, path: "/core/v1/domains/example.com/email/forwarding",
			body: func(p int) string { return fmt.Sprintf(`"emailForwarding":[{"emailBox":"box%d"}]`, p) },
			item: func(p int) string { return fmt.Sprintf("box%d", p) },
		},
		{
			name: "url list", args: []string{"url", "list", "example.com"}, path: "/core/v1/urlforwarding/example.com",
			body: func(p int) string { return fmt.Sprintf(`"urlForwarding":[{"id":%d}]`, p) },
			item: strconv.Itoa,
		},
		{
			name: "vanity-ns list", args: []string{"vanity-ns", "list", "example.com"}, path: "/core/v1/domains/example.com/vanity_nameservers",
			body: func(p int) string { return fmt.Sprintf(`"vanityNameservers":[{"hostname":"ns%d.example.com"}]`, p) },
			item: func(p int) string { return fmt.Sprintf("ns%d.example.com", p) },
		},
		{
			name: "transfer list", args: []string{"transfer", "list"}, path: "/core/v1/transfers",
			body: func(p int) string { return fmt.Sprintf(`"transfers":[{"domainName":"t%d.example"}]`, p) },
			item: func(p int) string { return fmt.Sprintf("t%d.example", p) },
		},
	}

	const lastPage = 3
	modes := []struct {
		name  string
		flags []string
		pages []int  // the pages fetched and printed, in order
		more  string // the stderr footer, "" for none
	}{
		{name: "-q alone pages fully", pages: []int{1, 2, 3}},
		{name: "-q --all pages fully", flags: []string{"--all"}, pages: []int{1, 2, 3}},
		{name: "-q --limit 1 fetches one page", flags: []string{"--limit", "1"}, pages: []int{1}, more: "--page 2 for more"},
		{name: "-q --page 2 fetches page 2", flags: []string{"--page", "2"}, pages: []int{2}, more: "--page 3 for more"},
		{name: "-q --page 3 is the last page", flags: []string{"--page", "3"}, pages: []int{3}},
	}

	for _, l := range lists {
		for _, m := range modes {
			t.Run(l.name+"/"+m.name, func(t *testing.T) {
				resetFlags(t, l.args)

				var mu sync.Mutex
				var fetched []int
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					_, _ = io.Copy(io.Discard, r.Body)
					w.Header().Set("Content-Type", "application/json")
					if r.Method != http.MethodGet || r.URL.Path != l.path {
						_, _ = w.Write([]byte(`{}`))
						return
					}
					p := 1
					if s := r.URL.Query().Get("page"); s != "" {
						p, _ = strconv.Atoi(s)
					}
					mu.Lock()
					fetched = append(fetched, p)
					mu.Unlock()
					next := ""
					if p < lastPage {
						next = fmt.Sprintf(`,"nextPage":%d`, p+1)
					}
					_, _ = fmt.Fprintf(w, `{%s,"totalCount":%d,"lastPage":%d%s}`, l.body(p), lastPage, lastPage, next)
				}))
				t.Cleanup(srv.Close)

				args := append([]string{"--base-url", srv.URL, "-q"}, l.args...)
				args = append(args, m.flags...)
				stdout, stderr := runCapturingBoth(t, args)

				mu.Lock()
				got := append([]int(nil), fetched...)
				mu.Unlock()
				if len(got) != len(m.pages) {
					t.Errorf("namecom %s made %d list requests (pages %v), want %d (pages %v)",
						strings.Join(args[2:], " "), len(got), got, len(m.pages), m.pages)
				}

				var want strings.Builder
				for _, p := range m.pages {
					want.WriteString(l.item(p) + "\n")
				}
				if stdout != want.String() {
					t.Errorf("stdout = %q, want %q", stdout, want.String())
				}
				if m.more == "" {
					if strings.Contains(stderr, "for more") {
						t.Errorf("stderr has a more-pages footer on the last page:\n%s", stderr)
					}
				} else if !strings.Contains(stderr, m.more) {
					t.Errorf("stderr = %q, want it to contain %q", stderr, m.more)
				}
			})
		}
	}
}

// runCapturingBoth runs `namecom <args>` through the real root and returns
// what it wrote to stdout and to stderr.
func runCapturingBoth(t *testing.T, args []string) (string, string) {
	t.Helper()
	t.Cleanup(output.StubInteractive(false))

	dir := t.TempDir()
	outF, err := os.Create(filepath.Join(dir, "stdout")) //nolint:gosec // a path under t.TempDir()
	if err != nil {
		t.Fatalf("creating stdout file: %v", err)
	}
	errF, err := os.Create(filepath.Join(dir, "stderr")) //nolint:gosec // a path under t.TempDir()
	if err != nil {
		t.Fatalf("creating stderr file: %v", err)
	}
	stdout, stderr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = outF, errF
	restore := func() { os.Stdout, os.Stderr = stdout, stderr }
	t.Cleanup(func() { restore(); _ = outF.Close(); _ = errF.Close() })

	prev := gf
	t.Cleanup(func() { gf = prev; rootCmd.SetArgs(nil) })
	rootCmd.SetArgs(args)
	runErr := rootCmd.ExecuteContext(context.Background())
	restore()
	if runErr != nil {
		t.Fatalf("namecom %s: %v", strings.Join(args, " "), runErr)
	}
	o, err := os.ReadFile(outF.Name())
	if err != nil {
		t.Fatalf("reading stdout: %v", err)
	}
	e, err := os.ReadFile(errF.Name())
	if err != nil {
		t.Fatalf("reading stderr: %v", err)
	}
	return string(o), string(e)
}
