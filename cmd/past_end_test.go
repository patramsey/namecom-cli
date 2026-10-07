package cmd

import (
	"fmt"
	"strings"
	"testing"
)

// TestPagedLists_PastTheEnd pins ISSUE-01: every paged list answers a --page
// past its end the same way, an empty page in one request, however the API
// answers it. It answers with page 1 again when the list fits on one page
// (domain list, order list), and with a 400, "Page exceeds available pages",
// past the end of a list of several. domain list and order list printed
// page 1 again for every --page, so a script paging until an empty page never
// stopped, and failed with exit 1 past a longer list's end.
func TestPagedLists_PastTheEnd(t *testing.T) {
	withConfig(t, loneProfile)

	// counts is whether the list's reply carries totalCount, from which a
	// repeated page 1 can be told apart. The others' replies do not, and the
	// API answers them past the end with an empty page, so that is what
	// their stub serves.
	counts := map[string]bool{"domain": true, "dns": true, "transfer": true, "order": true, "contact": true}

	for _, l := range pagedListBodies {
		key := strings.Split(l.body(1), `"`)[1]
		pageOne := fmt.Sprintf(`{%s,"totalCount":1,"from":1,"to":1}`, l.body(1))
		pastOnePage := pageOne
		if !counts[l.args[0]] {
			pastOnePage = `{"` + key + `":[]}`
		}
		route := "GET " + l.path
		noun := map[string]string{
			"domain": "domains", "dns": "DNS records", "email": "email forwardings", "url": "URL forwardings",
			"vanity-ns": "vanity nameservers", "transfer": "transfers", "order": "orders", "contact": "unverified contacts",
		}[l.args[0]]

		for _, sc := range []struct {
			name  string
			flags []string
			reply reply
			// want is the one request sent, after the path; empty means
			// page 1, which is not past the end and lists its one item.
			query string
			past  int // the page reported past the end; 0 when none is
		}{
			{"page 1 fits", []string{"--page", "1", "--limit", "5"}, reply{200, pageOne}, "page=1&perPage=5", 0},
			{"page 2 past a one-page list", []string{"--page", "2", "--limit", "5"}, reply{200, pastOnePage}, "page=2&perPage=5", 2},
			{"--all from page 2 past a one-page list", []string{"--all", "--page", "2"}, reply{200, pastOnePage}, "page=2&perPage=1000", 2},
			{"past a multi-page list's end", []string{"--page", "9", "--limit", "1"}, reply{400, `{"message":"Page exceeds available pages"}`}, "page=9&perPage=1", 9},
		} {
			t.Run(strings.Join(l.args[:2], " ")+"/"+sc.name, func(t *testing.T) {
				want := []string{route + "?" + sc.query}
				for _, format := range []string{"json", "table"} {
					resetFlags(t, l.args)
					srv, requests := apiStub(t, map[string]reply{route: sc.reply})
					args := append(append([]string{"--base-url", srv.URL, "-o", format}, l.args...), sc.flags...)
					stdout, stderr, code := runContract(t, args...)
					if code != 0 {
						t.Fatalf("-o %s exited %d, want 0; stderr:\n%s", format, code, stderr)
					}
					// Suffix: order list also sends dir=desc, sorted first.
					if got := requests(); len(got) != 1 || !strings.HasPrefix(got[0], route+"?") || !strings.HasSuffix(got[0], sc.query) {
						t.Errorf("-o %s sent %q, want only %q", format, got, want)
					}
					switch {
					case format == "json" && sc.past > 0:
						if got := strings.Join(strings.Fields(stdout), ""); got != `{"data":[]}` {
							t.Errorf("stdout = %s, want an empty page", got)
						}
					case format == "json":
						if !strings.Contains(stdout, `"data": [`) || strings.Contains(stdout, `"data": []`) {
							t.Errorf("stdout = %s, want page 1's item", stdout)
						}
					case sc.past > 0:
						if msg := fmt.Sprintf("No %s on page %d.", noun, sc.past); !strings.Contains(stderr, msg) || !strings.Contains(stderr, "past the last page") {
							t.Errorf("stderr = %q, want %q and the past-the-last-page note", stderr, msg)
						}
					default:
						if strings.Contains(stderr, "past the last page") {
							t.Errorf("stderr = %q, page 1 is not past the end", stderr)
						}
					}
				}
			})
		}
	}
}

// TestDNSList_PageTwoAtPerPageOne pins ISSUE-01: the records API reports
// from:1 for page 2 at perPage 1, and dns list took that to mean page 2 was
// past the end. It threw the record away and said "No DNS records on page 2"
// with four more pages to go, so a script paging with --limit 1 stopped
// there.
func TestDNSList_PageTwoAtPerPageOne(t *testing.T) {
	withConfig(t, loneProfile)
	args := []string{"dns", "list", "example.com"}
	resetFlags(t, args)
	srv, requests := apiStub(t, map[string]reply{
		"GET /core/v1/domains/example.com/records": {200, `{"records":[` + stubRecord(43) + `],"totalCount":6,"from":1,"to":2,"lastPage":6,"nextPage":3}`},
	})
	stdout, stderr, code := runContract(t, append([]string{"--base-url", srv.URL, "-o", "json"}, append(args, "--page", "2", "--limit", "1")...)...)
	if code != 0 {
		t.Fatalf("exited %d; stderr:\n%s", code, stderr)
	}
	if !strings.Contains(stdout, `"id": 43`) || !strings.Contains(stdout, `"nextPage": 3`) {
		t.Errorf("stdout = %s, want record 43 and nextPage 3", stdout)
	}
	if got := requests(); len(got) != 1 {
		t.Errorf("sent %q, want one request", got)
	}
}
