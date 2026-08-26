package dnssec

import (
	"net/http/httptest"
	"testing"

	"github.com/spf13/cobra"

	"github.com/patramsey/namecom-cli/internal/drifttest"
)

// TestRequestShape_DNSSEC pins the wire request for the mutating dnssec
// commands. Captured from the generated client so it can be asserted unchanged
// after the port to the Core SDK — see #40.
func TestRequestShape_DNSSEC(t *testing.T) {
	const stub = `{"domainName":"example.com","keyTag":2371,"algorithm":13,"digestType":2,"digest":"ABC123"}`

	t.Run("create", func(t *testing.T) {
		build := func(t *testing.T, srv *httptest.Server) *cobra.Command {
			cmd := cmdForCreate(t, srv)
			if err := cmd.ParseFlags([]string{
				"--key-tag", "2371", "--algorithm", "13",
				"--digest-type", "2", "--digest", "ABC123",
			}); err != nil {
				t.Fatalf("ParseFlags: %v", err)
			}
			return cmd
		}
		drifttest.AssertRequest(t, drifttest.Request{
			Method: "POST",
			Path:   "/core/v1/domains/example.com/dnssec",
			Body:   `{"algorithm":13,"digest":"ABC123","digestType":2,"keyTag":2371}`,
		}, build, runCreate, []string{"example.com"}, stub)
	})
}
