package cmd

import (
	"strings"
	"testing"
)

// TestDomainGet_ContactsLast pins #364: in -o yaml the four contacts, some
// fifty lines, came before createDate and expireDate. Contacts now go last
// in JSON and YAML alike, for one domain and for several, and --fields still
// knows the keys.
func TestDomainGet_ContactsLast(t *testing.T) {
	routes := map[string]reply{
		"GET /core/v1/domains/a.com": {200, `{"domainName":"a.com","contacts":{"registrant":{"firstName":"Jane"}},` +
			`"createDate":"2020-01-01T00:00:00Z","expireDate":"2027-01-01T00:00:00Z"}`},
		"GET /core/v1/domains/b.com": {200, `{"domainName":"b.com","contacts":{"registrant":{"firstName":"Jo"}},` +
			`"expireDate":"2028-01-01T00:00:00Z"}`},
	}
	for _, tc := range []struct {
		args       []string
		date, cont string
	}{
		{[]string{"domain", "get", "a.com", "-o", "json"}, `"expireDate"`, `"contacts"`},
		{[]string{"domain", "get", "a.com", "-o", "yaml"}, "\nexpireDate:", "\ncontacts:"},
		{[]string{"domain", "get", "a.com", "b.com", "-o", "yaml"}, "expireDate:", "contacts:"},
	} {
		stdout, stderr, code, _ := runCounted(t, routes, tc.args...)
		if code != 0 {
			t.Fatalf("%v: exit %d: %s", tc.args, code, stderr)
		}
		d, c := strings.Index(stdout, tc.date), strings.Index(stdout, tc.cont)
		if d < 0 || c < 0 || c < d {
			t.Errorf("%v: want %s before %s:\n%s", tc.args, tc.date, tc.cont, stdout)
		}
		if strings.Count(stdout, "Jane") != 1 {
			t.Errorf("%v: contacts lost or doubled:\n%s", tc.args, stdout)
		}
	}
	stdout, stderr, code, _ := runCounted(t, routes, "domain", "get", "a.com", "-o", "json", "--fields", "expireDate,contacts")
	if code != 0 || !strings.Contains(stdout, "2027-01-01") {
		t.Errorf("--fields on the reordered domain: exit %d\n%s%s", code, stdout, stderr)
	}
}
