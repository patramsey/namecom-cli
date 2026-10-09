package domain

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/patramsey/namecom-cli/cmd/cmdutil"
)

// TestDomainList_UnknownTLDIsNotTheWholeAccount pins #322 item 1: the API
// ignores a tld it does not know and answers with every domain, which the
// CLI printed as if filtered — so a typo in `domain list --tld cmo -q |
// xargs …` acted on the whole account. A page holding a domain outside the
// TLD is that case: a usage error, from the one request already made, and no
// further pages fetched even with --all.
func TestDomainList_UnknownTLDIsNotTheWholeAccount(t *testing.T) {
	for _, tc := range []struct {
		name  string
		args  []string
		quiet bool
	}{
		{"table", nil, false},
		{"all", []string{"--all"}, false},
		{"quiet", nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, requests := domainServer(t, [][]string{
				{"acme.com", "beta.io"}, // page 1 — the unfiltered account
				{"gamma.net"},
			})
			var stdout, stderr bytes.Buffer
			cmd := cmdForDomainList(t, srv, &stdout, &stderr)
			listAll, listFilter, listTLD, listExpiringAfter, listExpiringBefore, listPage = false, "", "", "", "", 1
			cmdutil.Out(cmd).QuietMode = tc.quiet
			if err := cmd.ParseFlags(append([]string{"--tld", "cmo"}, tc.args...)); err != nil {
				t.Fatal(err)
			}
			err := runList(cmd, nil)
			var ue *cmdutil.UsageError
			if !errors.As(err, &ue) || !strings.Contains(err.Error(), "cmo") {
				t.Fatalf("want a usage error naming the TLD, got %v", err)
			}
			if stdout.Len() != 0 {
				t.Errorf("nothing may be printed as if filtered, got %q", stdout.String())
			}
			if n := len(requests()); n != 1 {
				t.Errorf("want 1 request, got %d: %v", n, requests())
			}
		})
	}
}

// A TLD the API knows answers only its own domains — in any case, and for a
// two-label TLD — and lists as before, from one request.
func TestDomainList_KnownTLDLists(t *testing.T) {
	for _, tc := range []struct{ tld, domain string }{
		{"io", "acme.io"},
		{"IO", "ACME.IO"},
		{"co.uk", "acme.co.uk"},
	} {
		srv, requests := domainServer(t, [][]string{{tc.domain}})
		var stdout, stderr bytes.Buffer
		cmd := cmdForDomainList(t, srv, &stdout, &stderr)
		listAll, listFilter, listTLD, listExpiringAfter, listExpiringBefore, listPage = false, "", "", "", "", 1
		if err := cmd.ParseFlags([]string{"--tld", tc.tld}); err != nil {
			t.Fatal(err)
		}
		if err := runList(cmd, nil); err != nil {
			t.Fatalf("--tld %s: %v", tc.tld, err)
		}
		if !strings.Contains(stdout.String(), tc.domain) || len(requests()) != 1 {
			t.Errorf("--tld %s: want %s from 1 request, got %d requests:\n%s", tc.tld, tc.domain, len(requests()), stdout.String())
		}
	}
}

// The API ignores renewalPrice and privacyEnabled as sort keys and answers in
// its default order, so help and completion offer only keys that sort
// (#322 item 3). Any key is still sent as typed.
func TestDomainList_SortKeysOfferedAreOnesTheAPISortsBy(t *testing.T) {
	for _, k := range []string{"renewalPrice", "privacyEnabled"} {
		if slices.Contains(sortFields, k) {
			t.Errorf("--sort offers %s, which the API ignores", k)
		}
	}
	usage := listCmd.Flags().Lookup("sort").Usage
	if !strings.Contains(usage, "ignores") {
		t.Errorf("--sort help should say the API ignores other keys: %q", usage)
	}
}

// --sort-dir takes any case, as --status and --type do, and sends it
// lowercased: DESC was a usage error.
func TestDomainList_SortDirAnyCase(t *testing.T) {
	var mu sync.Mutex
	var gotDir string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		gotDir = r.URL.Query().Get("dir")
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"domains":[],"totalCount":0}`))
	}))
	t.Cleanup(srv.Close)
	cmd := baseCmd(t, srv)
	cmd.Flags().StringVar(&listSortDir, "sort-dir", "", "")
	cmd.Flags().IntVar(&listPage, "page", 1, "")
	t.Cleanup(func() { listSortDir = ""; listPage = 1 })
	if err := cmd.ParseFlags([]string{"--sort-dir", "DESC"}); err != nil {
		t.Fatal(err)
	}
	if err := runList(cmd, nil); err != nil {
		t.Fatalf("--sort-dir DESC: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if gotDir != "desc" {
		t.Errorf("want dir=desc, got %q", gotDir)
	}
}

// A filter that matches nothing says "No domains found.", as every list
// does, rather than a warning.
func TestDomainList_FilteredEmptyIsNoDomainsFound(t *testing.T) {
	srv, _ := domainServer(t, [][]string{{}})
	var stdout, stderr bytes.Buffer
	cmd := cmdForDomainList(t, srv, &stdout, &stderr)
	listAll, listFilter, listTLD, listExpiringAfter, listExpiringBefore, listPage = false, "", "", "", "", 1
	if err := cmd.ParseFlags([]string{"--expiring-after", "2099-01-01"}); err != nil {
		t.Fatal(err)
	}
	if err := runList(cmd, nil); err != nil {
		t.Fatal(err)
	}
	got := stderr.String()
	if !strings.Contains(got, "No domains found.") || strings.Contains(got, "matched") || strings.Contains(got, "register") {
		t.Errorf("want only \"No domains found.\", got %q", got)
	}
}

// `check foo.com.` was a usage error, while the dns commands take an FQDN
// with its trailing dot. One dot is dropped; two are still an error.
func TestCheck_TrailingDot(t *testing.T) {
	var mu sync.Mutex
	var sent []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			DomainNames []string `json:"domainNames"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		sent = body.DomainNames
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[{"domainName":"foo.com","purchasable":false}]}`))
	}))
	t.Cleanup(srv.Close)

	cmd, _ := cmdForCheckJSON(t, srv)
	checkAuthoritative = true
	if err := runCheck(cmd, []string{"foo.com."}); err != nil {
		t.Fatalf("check foo.com.: %v", err)
	}
	mu.Lock()
	if strings.Join(sent, ",") != "foo.com" {
		t.Errorf("want foo.com sent, got %v", sent)
	}
	mu.Unlock()

	cmd, _ = cmdForCheckJSON(t, neverCalledServer(t))
	if _, ok := errors.AsType[*cmdutil.UsageError](runCheck(cmd, []string{"foo.com.."})); !ok {
		t.Error("foo.com.. must still be a usage error")
	}
}

// The search and check footers name the noun, as every list has since 0.5.3:
// "2 domains", not "2 results".
func TestCheck_FooterNamesDomains(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[{"domainName":"a.com","purchasable":false},{"domainName":"b.com","purchasable":false}]}`))
	}))
	t.Cleanup(srv.Close)
	cmd := cmdForCheck(t, srv)
	checkAuthoritative = true
	if err := runCheck(cmd, []string{"a.com", "b.com"}); err != nil {
		t.Fatal(err)
	}
	got := cmdutil.Out(cmd).EWriter.(*bytes.Buffer).String()
	if !strings.Contains(got, "2 domains") || strings.Contains(got, "results") {
		t.Errorf("want a \"2 domains\" footer, got %q", got)
	}
}

// --filter's help says what the API does with % and _, which it reads as
// LIKE wildcards (#322); the CLI cannot escape them.
func TestDomainList_FilterHelpNamesTheAPIWildcards(t *testing.T) {
	usage := listCmd.Flags().Lookup("filter").Usage
	for _, want := range []string{"*", "%", "_"} {
		if !strings.Contains(usage, want) {
			t.Errorf("--filter help should mention %q: %q", want, usage)
		}
	}
}
