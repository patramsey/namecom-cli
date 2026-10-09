package cmd

import (
	"strings"
	"testing"
)

// TestListFooter_PageRange: `--limit 1 --page 2` says "Showing 2–2", though
// the API answers that page with from 1, to 2. The footer printed the API's
// range, "Showing 1–2 of 4 records", for one record (#325).
func TestListFooter_PageRange(t *testing.T) {
	for name, tc := range map[string]struct {
		args  []string
		route string
		body  string
		want  string
	}{
		"dns list": {
			args:  []string{"dns", "list", "example.com"},
			route: "GET /core/v1/domains/example.com/records",
			body:  `{"records":[{"id":13552177,"host":"_sip._tcp","type":"SRV","answer":"sip.example.com","ttl":300}],"totalCount":4,"from":1,"to":2,"nextPage":3,"lastPage":4}`,
			want:  "Showing 2–2 of 4 records",
		},
		"domain list": {
			args:  []string{"domain", "list"},
			route: "GET /core/v1/domains",
			body:  `{"domains":[{"domainName":"b.com"}],"totalCount":6522,"from":1,"to":2,"nextPage":3,"lastPage":6522}`,
			want:  "Showing 2–2 of 6,522 domains",
		},
		"order list": {
			args:  []string{"order", "list"},
			route: "GET /core/v1/orders",
			body:  `{"orders":[{"id":2}],"totalCount":9122,"from":1,"to":2,"nextPage":3,"lastPage":9122}`,
			want:  "Showing 2–2 of 9,122 orders",
		},
	} {
		t.Run(name, func(t *testing.T) {
			args := append(tc.args, "--limit", "1", "--page", "2", "-o", "table")
			_, stderr, code, n := runCounted(t, map[string]reply{tc.route: {200, tc.body}}, args...)
			if code != 0 || !strings.Contains(stderr, tc.want) || n != 1 {
				t.Errorf("exit %d after %d requests, want 0 after 1 and %q in stderr:\n%s", code, n, tc.want, stderr)
			}
		})
	}
}
