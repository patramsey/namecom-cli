package cmd

import (
	"encoding/json"
	"testing"
)

// TestEmptyCountedListTotal: a list the API counts says "total": 0 when it is
// empty, as it says N otherwise, so `--jq .total` is a number either way. It
// was left out, `{"data": []}` (#325). A page past the end is a plain empty
// page, and a list the API does not count has no total at all.
func TestEmptyCountedListTotal(t *testing.T) {
	withConfig(t, loneProfile)
	emptyZone := reply{200, `{"records":[],"totalCount":0,"from":1,"to":0}`}
	for name, tc := range map[string]struct {
		args   []string
		routes map[string]reply
		want   any // the total, or nil for none
	}{
		"dns list, empty zone": {
			args:   []string{"dns", "list", "example.com"},
			routes: map[string]reply{"GET /core/v1/domains/example.com/records": emptyZone},
			want:   0.0,
		},
		"domain list, none match": {
			args:   []string{"domain", "list", "--tld", "de"},
			routes: map[string]reply{"GET /core/v1/domains": {200, `{"domains":[],"totalCount":0}`}},
			want:   0.0,
		},
		"order list, none match": {
			args:   []string{"order", "list"},
			routes: map[string]reply{"GET /core/v1/orders": {200, `{"orders":[],"totalCount":0}`}},
			want:   0.0,
		},
		"transfer list, none": {
			args:   []string{"transfer", "list"},
			routes: map[string]reply{"GET /core/v1/transfers": {200, `{"transfers":[],"totalCount":0}`}},
			want:   0.0,
		},
		"dns list, past the end": {
			args: []string{"dns", "list", "example.com", "--page", "3", "--limit", "1"},
			routes: map[string]reply{"GET /core/v1/domains/example.com/records": {200,
				`{"records":[],"totalCount":2,"lastPage":2}`}},
			want: nil,
		},
		"url list, not counted": {
			args:   []string{"url", "list", "example.com"},
			routes: map[string]reply{"GET /core/v1/domains/example.com/url/forwarding": {200, `{"urlForwarding":[]}`}},
			want:   nil,
		},
	} {
		t.Run(name, func(t *testing.T) {
			resetFlags(t, tc.args)
			srv, _ := apiStub(t, tc.routes)
			stdout, stderr, code := runContract(t, append([]string{"--base-url", srv.URL, "-o", "json"}, tc.args...)...)
			if code != 0 {
				t.Fatalf("exit %d; stderr:\n%s", code, stderr)
			}
			var doc map[string]any
			if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
				t.Fatalf("%v:\n%s", err, stdout)
			}
			total, has := doc["total"]
			if tc.want == nil && has || tc.want != nil && total != tc.want {
				t.Errorf("total %v (present %v), want %v:\n%s", total, has, tc.want, stdout)
			}
		})
	}
}
