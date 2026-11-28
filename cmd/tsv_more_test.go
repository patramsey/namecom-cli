package cmd

import (
	"fmt"
	"strings"
	"testing"
)

// TestTSVLists_SayThereIsMore pins #382: `domain list -o tsv` printed 251 of
// 6,522 domains and nothing on stderr, where the table has a footer and
// JSON has nextPage. Every paged list now says, in one line on stderr, that
// there is more and how to get it — and stdout stays rows only. The last
// page, or --all, says nothing.
func TestTSVLists_SayThereIsMore(t *testing.T) {
	withConfig(t, loneProfile)

	lists := []struct {
		name string
		args []string
		path string
		body func(p int) string
	}{
		{"domain list", []string{"domain", "list"}, "/core/v1/domains",
			func(p int) string { return fmt.Sprintf(`"domains":[{"domainName":"d%d.io"}]`, p) }},
		{"dns list", []string{"dns", "list", "example.com"}, "/core/v1/domains/example.com/records",
			func(p int) string {
				return fmt.Sprintf(`"records":[{"id":%d,"type":"A","host":"www","answer":"192.0.2.1"}]`, p)
			}},
		{"dns list --type, nothing on the page", []string{"dns", "list", "example.com", "--type", "MX"}, "/core/v1/domains/example.com/records",
			func(p int) string {
				return fmt.Sprintf(`"records":[{"id":%d,"type":"A","host":"www","answer":"192.0.2.1"}]`, p)
			}},
		{"order list", []string{"order", "list"}, "/core/v1/orders",
			func(p int) string { return fmt.Sprintf(`"orders":[{"id":%d}]`, p) }},
		{"email list", []string{"email", "list", "example.com"}, "/core/v1/domains/example.com/email/forwarding",
			func(p int) string {
				return fmt.Sprintf(`"emailForwarding":[{"emailBox":"b%d","emailTo":"a@example.org"}]`, p)
			}},
		{"url list", []string{"url", "list", "example.com"}, "/core/v1/urlforwarding/example.com",
			func(p int) string {
				return fmt.Sprintf(`"urlForwarding":[{"id":%d,"host":"example.com","forwardsTo":"https://x.example"}]`, p)
			}},
		{"vanity-ns list", []string{"vanity-ns", "list", "example.com"}, "/core/v1/domains/example.com/vanity_nameservers",
			func(p int) string { return fmt.Sprintf(`"vanityNameservers":[{"hostname":"ns%d.example.com"}]`, p) }},
		{"transfer list", []string{"transfer", "list"}, "/core/v1/transfers",
			func(p int) string { return fmt.Sprintf(`"transfers":[{"domainName":"t%d.org"}]`, p) }},
		{"contact unverified", []string{"contact", "unverified"}, "/core/v1/contacts/unverified",
			func(p int) string {
				return fmt.Sprintf(`"unverifiedContacts":[{"verificationId":%d,"email":"a@example.com"}]`, p)
			}},
	}
	// A field each list's items have, for --fields.
	fieldOf := map[string]string{
		"domain": "domainName", "dns": "id", "order": "id", "email": "emailBox", "url": "id",
		"vanity-ns": "hostname", "transfer": "domainName", "contact": "verificationId",
	}
	for _, l := range lists {
		for _, mode := range []struct {
			name  string
			flags []string
			more  bool
		}{
			{"first page", nil, true},
			{"--fields", nil, true},
			{"last page", []string{"--page", "2"}, false},
			{"--all", []string{"--all"}, false},
		} {
			t.Run(l.name+"/"+mode.name, func(t *testing.T) {
				flags := mode.flags
				if mode.name == "--fields" {
					flags = []string{"--fields", fieldOf[l.args[0]]}
				}
				resetFlags(t, l.args[:2])
				srv, _ := pagedStub(t, l.path, 2, l.body)
				args := append(append([]string{"--base-url", srv.URL, "-o", "tsv"}, l.args...), flags...)
				stdout, stderr, code := runContract(t, args...)
				if code != 0 {
					t.Fatalf("exit %d; stderr:\n%s", code, stderr)
				}

				if strings.Contains(stdout, "for more") {
					t.Errorf("stdout has the note, want rows only:\n%s", stdout)
				}
				hasNote := strings.Contains(stderr, "--page 2 for more, --all for everything")
				if hasNote != mode.more {
					t.Errorf("stderr = %q, want the more-pages note: %v\nstdout: %q", stderr, mode.more, stdout)
				}
				if n := strings.Count(stderr, "for more"); mode.more && n != 1 {
					t.Errorf("stderr has the note %d times, want once:\n%s", n, stderr)
				}
			})
		}
	}
}
