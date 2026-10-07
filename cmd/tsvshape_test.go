package cmd

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// countingServer is contractServer that also counts the requests it answers.
func countingServer(t *testing.T, routes map[string]reply) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		rep, ok := routes[r.Method+" "+r.URL.Path]
		if !ok {
			rep = reply{200, `{}`}
		}
		if rep.status != 0 {
			w.WriteHeader(rep.status)
		}
		_, _ = w.Write([]byte(rep.body))
	}))
	t.Cleanup(srv.Close)
	return srv, &n
}

// runCounted is runFormatting that also returns how many requests were sent.
func runCounted(t *testing.T, routes map[string]reply, args ...string) (stdout, stderr string, code int, requests int32) {
	t.Helper()
	withConfig(t, loneProfile)
	resetFlags(t, args)
	srv, n := countingServer(t, routes)
	stdout, stderr, code = runContract(t, append([]string{"--base-url", srv.URL}, args...)...)
	return stdout, stderr, code, n.Load()
}

// TestTSVShape pins #289: what -o tsv prints has one shape per command,
// whatever the data — a list is a header row (printed when the list is empty
// too) and a row per item with fixed columns; one object is field<TAB>value
// rows, the same rows whatever it holds — and a read never prints a write's
// success/changed/message rows. Each case also pins how many requests the
// command sends, so a fixed shape never costs a request.
func TestTSVShape(t *testing.T) {
	stubStart(t, func(string) error { return nil })
	t.Setenv("BROWSER", "")
	tests := []struct {
		name     string
		routes   map[string]reply
		args     []string
		want     string // stdout exactly, when set
		has      []string
		hasNot   []string
		requests int32
	}{
		// Empty lists print their header.
		{name: "domain list", args: []string{"domain", "list"},
			routes: map[string]reply{"GET /core/v1/domains": {200, `{"domains":[]}`}},
			want:   "DOMAIN\tEXPIRES\tAUTO-RENEW\tLOCKED\tPRIVACY\n", requests: 1},
		{name: "domain list --filter", args: []string{"domain", "list", "--filter", "nomatch"},
			routes: map[string]reply{"GET /core/v1/domains": {200, `{"domains":[]}`}},
			want:   "DOMAIN\tEXPIRES\tAUTO-RENEW\tLOCKED\tPRIVACY\n", requests: 1},
		{name: "dns list", args: []string{"dns", "list", "example.com"},
			want: "ID\tTYPE\tHOST\tANSWER\tTTL\tPRIORITY\n", requests: 1},
		{name: "dns list --type", args: []string{"dns", "list", "example.com", "--type", "CAA"},
			want: "ID\tTYPE\tHOST\tANSWER\tTTL\tPRIORITY\n", requests: 1},
		{name: "transfer list", args: []string{"transfer", "list"}, want: "DOMAIN\tSTATUS\n", requests: 1},
		{name: "url list", args: []string{"url", "list", "example.com"}, want: "ID\tHOST\tFORWARDS TO\tTYPE\n", requests: 1},
		{name: "email list", args: []string{"email", "list", "example.com"}, want: "MAILBOX\tFORWARDS TO\n", requests: 1},
		{name: "vanity-ns list", args: []string{"vanity-ns", "list", "example.com"}, want: "HOSTNAME\tIPS\n", requests: 1},
		{name: "dnssec list", args: []string{"dnssec", "list", "example.com"}, want: "KEY TAG\tALGORITHM\tDIGEST TYPE\tDIGEST\n", requests: 1},
		{name: "contact unverified", args: []string{"contact", "unverified"}, want: "ID\tEMAIL\tDEADLINE\tDOMAINS\n", requests: 1},
		{name: "order list", args: []string{"order", "list"}, has: []string{"DATE\t"}, requests: 1},
		{name: "empty list --no-header", args: []string{"transfer", "list", "--no-header"}, want: "", requests: 1},

		// Fixed columns.
		{name: "dns list without MX or SRV has PRIORITY", args: []string{"dns", "list", "example.com"},
			routes: map[string]reply{"GET /core/v1/domains/example.com/records": {200,
				`{"records":[{"id":7,"host":"www","type":"A","answer":"192.0.2.1","ttl":300}]}`}},
			want: "ID\tTYPE\tHOST\tANSWER\tTTL\tPRIORITY\n7\tA\twww\t192.0.2.1\t300\t\n", requests: 1},

		// One object: the JSON keys, no write-result rows.
		{name: "domain claims", args: []string{"domain", "claims", "example.com"},
			routes:   map[string]reply{"POST /core/v1/domaininfo/claims/example.com": {200, `{"domain":"example.com","claims":[]}`}},
			want:     "domain\texample.com\nclaims\t[]\nclaimsProcessActive\t\nclaimId\t\nnotBefore\t\nnotAfter\t\nclaimsNotice\t\n",
			requests: 1},
		{name: "auth status", args: []string{"auth", "status"},
			has:    []string{"username\tworkuser\n", "usernameSource\t", "profile\twork\n", "profileSource\t", "tokenSource\t", "verified\ttrue\n"},
			hasNot: []string{"success", "changed", "message", "Profile", "••"}, requests: 1},
		{name: "config show", args: []string{"config", "show"},
			has:    []string{"profile\twork\n", "profileSource\t", "username\tworkuser\n", "endpointSource\t"},
			hasNot: []string{"Profile", "Config file", "  ("}, requests: 0},
		{name: "open", args: []string{"open", "example.com"},
			want: "url\thttps://www.name.com/account/domain/details#?domain=example.com\nopened\ttrue\ndryRun\tfalse\n", requests: 0},
		{name: "domain contacts get", args: []string{"domain", "contacts", "get", "example.com"},
			routes: map[string]reply{"GET /core/v1/domains/example.com": {200,
				`{"domainName":"example.com","contacts":{"registrant":{"firstName":"Jane","email":"j@example.com","isVerified":true},"tech":{"lastName":"Doe"}}}`}},
			want: "Role\tFirst name\tLast name\tCompany\tAddress 1\tAddress 2\tCity\tState\tZip\tCountry\tEmail\tPhone\tFax\tVerified\tVerification ID\n" +
				"registrant\tJane\t\t\t\t\t\t\t\t\tj@example.com\t\t\tyes\t\n" +
				"admin\t\t\t\t\t\t\t\t\t\t\t\t\t\t\n" +
				"tech\t\tDoe\t\t\t\t\t\t\t\t\t\t\t\t\n" +
				"billing\t\t\t\t\t\t\t\t\t\t\t\t\t\t\n",
			requests: 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			stdout, stderr, code, n := runCounted(t, tc.routes, append(tc.args, "-o", "tsv")...)
			if code != 0 {
				t.Fatalf("exit %d, stderr:\n%s", code, stderr)
			}
			if tc.want != "" || len(tc.has) == 0 {
				if stdout != tc.want {
					t.Errorf("stdout:\n%q\nwant:\n%q", stdout, tc.want)
				}
			}
			for _, s := range tc.has {
				if !strings.Contains(stdout, s) {
					t.Errorf("want %q in:\n%s", s, stdout)
				}
			}
			for _, s := range tc.hasNot {
				if strings.Contains(stdout, s) {
					t.Errorf("want no %q in:\n%s", s, stdout)
				}
			}
			for line := range strings.SplitSeq(strings.TrimSuffix(stdout, "\n"), "\n") {
				if strings.HasPrefix(line, "success\t") || strings.HasPrefix(line, "changed\t") {
					t.Errorf("a read printed a write's result row %q", line)
				}
			}
			if n != tc.requests {
				t.Errorf("sent %d requests, want %d", n, tc.requests)
			}
		})
	}
}

// TestTSVShape_DomainGetBatch: several domains have the same columns whether
// or not any of them is in a transfer lock or has a renewal price, and one
// domain the same rows (#289). A column only some batches had moved every
// later column, so a script reading by position read the wrong field.
func TestTSVShape_DomainGetBatch(t *testing.T) {
	routes := map[string]reply{
		"GET /core/v1/domains/a.com": {200, `{"domainName":"a.com","transferLockExpiresAt":"2099-01-02T00:00:00Z","renewalPrice":12.99}`},
		"GET /core/v1/domains/b.com": {200, `{"domainName":"b.com"}`},
		"GET /core/v1/domains/c.com": {200, `{"domainName":"c.com","privacyEnabled":true}`},
	}
	header := func(args ...string) string {
		t.Helper()
		stdout, stderr, code, n := runCounted(t, routes, append(append([]string{"domain", "get"}, args...), "-o", "tsv")...)
		if code != 0 {
			t.Fatalf("exit %d, stderr:\n%s", code, stderr)
		}
		if want := int32(len(args)); n != want {
			t.Errorf("%v: sent %d requests, want %d", args, n, want)
		}
		lines := strings.Split(strings.TrimSuffix(stdout, "\n"), "\n")
		if len(args) == 1 {
			fields := make([]string, len(lines))
			for i, l := range lines {
				fields[i], _, _ = strings.Cut(l, "\t")
			}
			return strings.Join(fields, "\t")
		}
		for _, l := range lines[1:] {
			if strings.Count(l, "\t") != strings.Count(lines[0], "\t") {
				t.Errorf("row %q does not have the header's columns %q", l, lines[0])
			}
		}
		return lines[0]
	}
	mixed := header("a.com", "b.com")
	plain := header("b.com", "c.com")
	one := header("b.com")
	if mixed != plain || plain != one {
		t.Errorf("columns depend on the domains:\n a.com b.com: %q\n b.com c.com: %q\n b.com:       %q", mixed, plain, one)
	}
	if !strings.Contains(mixed, "Transfer lock\tPrivacy") {
		t.Errorf("want Transfer lock before Privacy: %q", mixed)
	}
}

// TestFields_Footer: --fields in a table keeps the list's footer on stderr,
// so a list cut short by --limit says there is more (#289), in the words it
// has without --fields: it said "2 of 6,522 results".
func TestFields_Footer(t *testing.T) {
	routes := map[string]reply{"GET /core/v1/domains": {200,
		`{"domains":[{"domainName":"a.com"},{"domainName":"b.com"}],"from":1,"to":2,"nextPage":2,"lastPage":3262,"totalCount":6522}`}}
	stdout, stderr, code, n := runCounted(t, routes, "domain", "list", "--limit", "2", "--fields", "domainName", "-o", "table")
	if code != 0 {
		t.Fatalf("exit %d, stderr:\n%s", code, stderr)
	}
	if stdout != "domainName\na.com\nb.com\n" {
		t.Errorf("stdout:\n%s", stdout)
	}
	const want = "Showing 1–2 of 6,522 domains · --page 2 for more, --all for everything\n"
	if withoutWarnings(stderr) != want {
		t.Errorf("stderr:\n%q\nwant:\n%q", stderr, want)
	}
	if n != 1 {
		t.Errorf("sent %d requests, want 1", n)
	}
	_, plain, _, _ := runCounted(t, routes, "domain", "list", "--limit", "2", "-o", "table")
	if withoutWarnings(plain) != want {
		t.Errorf("without --fields:\n%q\nwant:\n%q", plain, want)
	}
}

// TestDNSExport_FileFormatsOnly: dns export writes a file, so -o table,
// -o tsv and -q are usage errors, before any request (#289). They were
// ignored, and the JSON printed anyway.
func TestDNSExport_FileFormatsOnly(t *testing.T) {
	for _, args := range [][]string{{"-o", "tsv"}, {"-o", "table"}, {"-q"}, {"-o", "tsv", "--zone"}} {
		stdout, stderr, code, n := runCounted(t, nil, append([]string{"dns", "export", "example.com"}, args...)...)
		if code != 2 || stdout != "" || n != 0 {
			t.Errorf("%v: exit %d, %d requests, stdout %q, stderr:\n%s", args, code, n, stdout, stderr)
		}
	}
	stdout, stderr, code, n := runCounted(t, nil, "dns", "export", "example.com", "-o", "json")
	if code != 0 || !strings.HasPrefix(stdout, "{\n  \"data\": [") || n != 1 {
		t.Errorf("-o json: exit %d, %d requests, stdout %q, stderr:\n%s", code, n, stdout, stderr)
	}
}

// TestContactUnverified_WarningInEveryFormat: the deadline warning is one of
// the JSON "warnings" on stderr, where it was left out, and a piped table
// prints it with the "! " every warning has, not "WARNING: " (#289).
func TestContactUnverified_WarningInEveryFormat(t *testing.T) {
	routes := map[string]reply{"GET /core/v1/contacts/unverified": {200,
		`{"unverifiedContacts":[{"verificationId":5,"email":"j@example.com","verifyBy":"2000-01-02T00:00:00Z","domains":["a.com"]}]}`}}
	_, stderr, code, n := runCounted(t, routes, "contact", "unverified", "-o", "json")
	if code != 0 || n != 1 {
		t.Fatalf("exit %d, %d requests, stderr:\n%s", code, n, stderr)
	}
	if !strings.Contains(stderr, `"warnings"`) || !strings.Contains(stderr, "Verification deadline passed") {
		t.Errorf("want the deadline warning in the JSON warnings, got:\n%s", stderr)
	}
	_, stderr, code, _ = runCounted(t, routes, "contact", "unverified", "-o", "table")
	if code != 0 || !strings.Contains(stderr, "! Verification deadline passed") || strings.Contains(stderr, "WARNING:") {
		t.Errorf("exit %d, want a \"! \" warning, got:\n%s", code, stderr)
	}
}
