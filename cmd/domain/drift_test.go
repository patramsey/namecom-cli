package domain

import (
	"net/http/httptest"
	"testing"

	"github.com/spf13/cobra"

	"github.com/patramsey/namecom-cli/internal/drifttest"
)

// TestDryRunMatchesRealRequest_DomainBody asserts that the body --dry-run
// prints is the body actually sent, for the two domain writes whose preview
// had drifted from it.
//
// TestDryRunMatchesRealRequest_Domain checks only the method and path, which
// both of these got right:
//
//   - `contacts set` previewed the bare contacts object, but the SDK wraps it
//     as {"contacts": {...}}, so anyone copying the preview into `namecom api`
//     sent a body the endpoint does not accept.
//   - `set-ns` previewed no body at all, then echoed the raw --ns string with
//     its whitespace intact, while the request carries a trimmed array.
func TestDryRunMatchesRealRequest_DomainBody(t *testing.T) {
	t.Run("set-ns", func(t *testing.T) {
		build := func(t *testing.T, srv *httptest.Server) *cobra.Command {
			cmd := cmdForSetNS(t, srv)
			// Spaces around the entries: the request trims them, so the
			// preview must too.
			if err := cmd.ParseFlags([]string{"--ns", "ns1.example.net, ns2.example.net"}); err != nil {
				t.Fatalf("ParseFlags: %v", err)
			}
			return cmd
		}
		drifttest.AssertDryRunBodyMatches(t, build, runSetNS, []string{"example.com"}, domainStub)
	})

	t.Run("contacts set", func(t *testing.T) {
		const contacts = `{"registrant":{"firstName":"Ada","lastName":"Lovelace","email":"ada@example.com"}}`
		build := func(t *testing.T, srv *httptest.Server) *cobra.Command {
			return cmdForContactsSet(t, srv, contacts)
		}
		drifttest.AssertDryRunBodyMatches(t, build, runContactsSet, []string{"example.com"}, domainStub)
	})
}
