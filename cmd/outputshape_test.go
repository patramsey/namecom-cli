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
