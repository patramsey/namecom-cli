package cmd

import (
	"strings"
	"testing"
)

// TestTSVShape_SameWithFields: a command's -o tsv shape does not depend on
// --fields. One object is field<TAB>value rows with or without it, and a dry
// run is a list — a header and a row — with or without it. url get and email
// get printed a list's header and one row, and order get two tables in one
// stream, until --fields turned them into rows; a dry run went the other
// way. --fields picks rows (or columns) and leaves their values alone.
func TestTSVShape_SameWithFields(t *testing.T) {
	stubStart(t, func(string) error { return nil })
	t.Setenv("BROWSER", "")
	tests := []struct {
		name   string
		args   []string
		routes map[string]reply
		fields string
		want   string // stdout without --fields
		picked string // stdout with --fields
	}{
		{name: "url get", args: []string{"url", "get", "example.com", "7"},
			routes: map[string]reply{"GET /core/v1/urlforwarding/example.com/7": {200,
				`{"id":7,"domainName":"example.com","host":"www","forwardsTo":"https://example.org","type":"redirect"}`}},
			fields: "id,host",
			want:   "id\t7\nhost\twww\ndomainName\texample.com\nforwardsTo\thttps://example.org\nmeta\t\ntitle\t\ntype\tredirect\n",
			picked: "id\t7\nhost\twww\n"},
		{name: "email get", args: []string{"email", "get", "example.com", "info"},
			routes: map[string]reply{"GET /core/v1/domains/example.com/email/forwarding/info": {200,
				`{"domainName":"example.com","emailBox":"info","emailTo":"me@example.org"}`}},
			fields: "emailTo",
			want:   "domainName\texample.com\nemailBox\tinfo\nemailTo\tme@example.org\n",
			picked: "emailTo\tme@example.org\n"},
		{name: "open --dry-run", args: []string{"open", "--dry-run"},
			fields: "url,dryRun",
			want:   "url\thttps://www.name.com/account/domain/\nopened\tfalse\ndryRun\ttrue\n",
			picked: "url\thttps://www.name.com/account/domain/\ndryRun\ttrue\n"},
		{name: "dns create --dry-run", args: []string{"dns", "create", "example.com", "--type", "A", "--answer", "192.0.2.1", "--dry-run"},
			fields: "method,path",
			want:   "method\tpath\tbody\nPOST\t/core/v1/domains/example.com/records\t{\"answer\":\"192.0.2.1\",\"host\":\"@\",\"ttl\":300,\"type\":\"A\"}\n",
			picked: "method\tpath\nPOST\t/core/v1/domains/example.com/records\n"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			stdout, stderr, code, _ := runCounted(t, tc.routes, append(tc.args, "-o", "tsv")...)
			if code != 0 {
				t.Fatalf("exit %d, stderr:\n%s", code, stderr)
			}
			if stdout != tc.want {
				t.Errorf("-o tsv:\n%q\nwant:\n%q", stdout, tc.want)
			}
			stdout, stderr, code, _ = runCounted(t, tc.routes, append(tc.args, "-o", "tsv", "--fields", tc.fields)...)
			if code != 0 {
				t.Fatalf("--fields: exit %d, stderr:\n%s", code, stderr)
			}
			if stdout != tc.picked {
				t.Errorf("-o tsv --fields %s:\n%q\nwant:\n%q", tc.fields, stdout, tc.picked)
			}
		})
	}
}

// TestTSVShape_OrderGet: order get is one object, so one set of
// field<TAB>value rows, its items a JSON array in one cell. It printed the
// order as a list row and then a second table of items, whose rows had
// another column count, in the same stream.
func TestTSVShape_OrderGet(t *testing.T) {
	routes := map[string]reply{"GET /core/v1/orders/42": {200,
		`{"id":42,"createDate":"2026-01-02T03:04:05Z","currency":"USD","finalAmount":12.99,"status":"success",` +
			`"orderItems":[{"id":9,"name":"example.com","productType":"domain_registration","price":12.99}]}`}}
	stdout, stderr, code, n := runCounted(t, routes, "order", "get", "42", "-o", "tsv")
	if code != 0 {
		t.Fatalf("exit %d, stderr:\n%s", code, stderr)
	}
	if n != 1 {
		t.Errorf("sent %d requests, want 1", n)
	}
	for line := range strings.SplitSeq(strings.TrimSuffix(stdout, "\n"), "\n") {
		if strings.Count(line, "\t") != 1 {
			t.Errorf("line %q is not field<TAB>value in:\n%s", line, stdout)
		}
	}
	for _, s := range []string{"id\t42\n", "\nfinalAmount\t12.99\n", "\norderItems\t[{\"duration\":0,\"id\":9,"} {
		if !strings.Contains(stdout, s) {
			t.Errorf("want %q in:\n%s", s, stdout)
		}
	}
	picked, stderr, code, _ := runCounted(t, routes, "order", "get", "42", "-o", "tsv", "--fields", "id,orderItems")
	if code != 0 {
		t.Fatalf("--fields: exit %d, stderr:\n%s", code, stderr)
	}
	for line := range strings.SplitSeq(strings.TrimSuffix(picked, "\n"), "\n") {
		if !strings.Contains(stdout, line+"\n") {
			t.Errorf("--fields row %q is not one of the rows without it:\n%s", line, stdout)
		}
	}
}

// TestListFooters: every paged list prints one footer, after its table, and
// says where the page sits whenever the API gives a total — on the last page
// too, which said "1 domain" where page 1 said "Showing 1–2 of 5 domains".
// order list said "2 orders" whatever its total, and offered --status and
// --domain to narrow it when they were given; dns list said "1 record".
func TestListFooters(t *testing.T) {
	tests := []struct {
		name   string
		args   []string
		routes map[string]reply
		want   string // stderr exactly
	}{
		{name: "domain list, last page", args: []string{"domain", "list", "--page", "3", "--limit", "2"},
			routes: map[string]reply{"GET /core/v1/domains": {200, `{"domains":[{"domainName":"e.io"}],"from":5,"to":5,"totalCount":5}`}},
			want:   "Showing 5–5 of 5 domains\n"},
		{name: "domain list, whole list", args: []string{"domain", "list"},
			routes: map[string]reply{"GET /core/v1/domains": {200, `{"domains":[{"domainName":"a.io"}],"from":1,"to":1,"totalCount":1}`}},
			want:   "1 domain\n"},
		{name: "contact unverified, last page", args: []string{"contact", "unverified", "--page", "2", "--limit", "1"},
			routes: map[string]reply{"GET /core/v1/contacts/unverified": {200,
				`{"unverifiedContacts":[{"verificationId":2,"email":"b@example.com","verifyBy":"2099-01-02T00:00:00Z","domains":["b.com"]}],"from":2,"to":2,"lastPage":2,"totalCount":2}`}},
			want: "Showing 2–2 of 2 unverified contacts\n"},
		{name: "order list", args: []string{"order", "list", "--limit", "2"},
			routes: map[string]reply{"GET /core/v1/orders": {200,
				`{"orders":[{"id":1,"status":"success"},{"id":2,"status":"success"}],"from":1,"to":2,"nextPage":2,"lastPage":4561,"totalCount":9122}`}},
			want: "Showing 1–2 of 9,122 orders · newest first · --page 2 for more, --all for everything, or narrow with --since, --domain or --status\n"},
		{name: "order list --status --domain", args: []string{"order", "list", "--limit", "2", "--status", "success", "--domain", "a.com"},
			routes: map[string]reply{"GET /core/v1/orders": {200,
				`{"orders":[{"id":1,"status":"success"},{"id":2,"status":"success"}],"from":1,"to":2,"nextPage":2,"lastPage":3,"totalCount":5}`}},
			want: "Showing 1–2 of 5 orders · newest first · --page 2 for more, --all for everything, or narrow with --since\n"},
		{name: "dns list", args: []string{"dns", "list", "example.com", "--limit", "1"},
			routes: map[string]reply{"GET /core/v1/domains/example.com/records": {200,
				`{"records":[{"id":7,"host":"www","type":"A","answer":"192.0.2.1","ttl":300}],"from":1,"to":1,"nextPage":2,"lastPage":6,"totalCount":6}`}},
			want: "Showing 1–1 of 6 records · --page 2 for more, --all for everything\n"},
		{name: "dns list --type, filtered after fetching", args: []string{"dns", "list", "example.com", "--limit", "1", "--type", "A"},
			routes: map[string]reply{"GET /core/v1/domains/example.com/records": {200,
				`{"records":[{"id":7,"host":"www","type":"A","answer":"192.0.2.1","ttl":300}],"from":1,"to":1,"nextPage":2,"lastPage":6,"totalCount":6}`}},
			want: "1 record · --page 2 for more, --all for everything\n"},
		{name: "transfer list", args: []string{"transfer", "list", "--limit", "1"},
			routes: map[string]reply{"GET /core/v1/transfers": {200,
				`{"transfers":[{"domainName":"a.com","status":"pending"}],"from":1,"to":1,"nextPage":2,"lastPage":3,"totalCount":3}`}},
			want: "Showing 1–1 of 3 transfers · --page 2 for more, --all for everything\n"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, stderr, code, n := runCounted(t, tc.routes, append(tc.args, "-o", "table")...)
			if code != 0 {
				t.Fatalf("exit %d, stderr:\n%s", code, stderr)
			}
			if got := withoutWarnings(stderr); got != tc.want {
				t.Errorf("stderr:\n%q\nwant:\n%q", got, tc.want)
			}
			if n != 1 {
				t.Errorf("sent %d requests, want 1", n)
			}
		})
	}
}

// TestListFooters_PastTheEnd: a page past the end of a list says so, with or
// without --fields, rather than a footer counting the page the API answered
// with instead (it answers page 1 again when the list fits on one).
func TestListFooters_PastTheEnd(t *testing.T) {
	routes := map[string]reply{"GET /core/v1/domains": {200,
		`{"domains":[{"domainName":"a.io"},{"domainName":"b.io"}],"from":1,"to":2,"totalCount":2}`}}
	stdout, stderr, code, n := runCounted(t, routes, "domain", "list", "--page", "3", "--limit", "2", "-o", "table")
	if code != 0 || n != 1 || stdout != "" {
		t.Fatalf("exit %d, %d requests, stdout %q, stderr:\n%s", code, n, stdout, stderr)
	}
	if got := withoutWarnings(stderr); !strings.HasPrefix(got, "No domains on page 3.\n") || strings.Contains(got, "Showing") {
		t.Errorf("stderr:\n%s", got)
	}
	stdout, stderr, code, _ = runCounted(t, routes, "domain", "list", "--page", "3", "--limit", "2", "--fields", "domainName", "-o", "table")
	if code != 0 || stdout != "" {
		t.Fatalf("--fields: exit %d, stdout %q, stderr:\n%s", code, stdout, stderr)
	}
	if got, want := withoutWarnings(stderr), "No domains on page 3 · that is past the last page; leave out --page to start at the first\n"; got != want {
		t.Errorf("--fields stderr:\n%q\nwant:\n%q", got, want)
	}
}

// withoutWarnings is stderr without its "! " warnings and their continuation
// lines: the --base-url these tests use, contact unverified's deadline.
func withoutWarnings(stderr string) string {
	var b strings.Builder
	for _, line := range strings.SplitAfter(stderr, "\n") {
		if !strings.HasPrefix(line, "! ") && !strings.HasPrefix(line, "  ") {
			b.WriteString(line)
		}
	}
	return b.String()
}

// TestDNSList_JSONTotal: dns list's JSON has the zone's size as total, as
// the other lists the API counts do; the response always had it. A list
// filtered by --type or --host has none: the zone's size is not its own.
func TestDNSList_JSONTotal(t *testing.T) {
	routes := map[string]reply{"GET /core/v1/domains/example.com/records": {200,
		`{"records":[{"id":7,"host":"www","type":"A","answer":"192.0.2.1","ttl":300}],"from":1,"to":1,"nextPage":2,"lastPage":6,"totalCount":6}`}}
	stdout, stderr, code, n := runCounted(t, routes, "dns", "list", "example.com", "--limit", "1", "-o", "json")
	if code != 0 || n != 1 {
		t.Fatalf("exit %d, %d requests, stderr:\n%s", code, n, stderr)
	}
	if !strings.Contains(stdout, `"nextPage": 2,`) || !strings.Contains(stdout, `"total": 6`) {
		t.Errorf("want nextPage and total in:\n%s", stdout)
	}
	stdout, _, _, _ = runCounted(t, routes, "dns", "list", "example.com", "--limit", "1", "--type", "A", "-o", "json")
	if strings.Contains(stdout, `"total"`) {
		t.Errorf("a filtered list claimed the zone's total:\n%s", stdout)
	}
}
