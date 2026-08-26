package dns

import (
	"net/http/httptest"
	"testing"

	"github.com/spf13/cobra"

	"github.com/patramsey/namecom-cli/internal/drifttest"
)

// TestRequestShape_DNS pins the exact wire request for each mutating dns
// command.
//
// This is the migration guard for #40. The bodies below were captured from the
// generated client before the port to the Core SDK and are asserted unchanged
// after it: the client underneath may change, the bytes on the wire may not.
// It is deliberately written against the command entry points rather than
// either client, so the same file compiles and passes on both sides of the
// swap.
func TestRequestShape_DNS(t *testing.T) {
	const stub = `{"id":42,"domainName":"example.com","host":"www","type":"A","answer":"1.2.3.4","ttl":300}`

	t.Run("create", func(t *testing.T) {
		build := func(t *testing.T, srv *httptest.Server) *cobra.Command {
			cmd := cmdForCreate(t, srv)
			if err := cmd.ParseFlags([]string{
				"--type", "A", "--host", "www", "--answer", "1.2.3.4",
			}); err != nil {
				t.Fatalf("ParseFlags: %v", err)
			}
			return cmd
		}
		drifttest.AssertRequest(t, drifttest.Request{
			Method: "POST",
			Path:   "/core/v1/domains/example.com/records",
			Body:   `{"answer":"1.2.3.4","host":"www","ttl":300,"type":"A"}`,
		}, build, runCreate, []string{"example.com"}, stub)
	})

	t.Run("update merges the fetched record", func(t *testing.T) {
		build := func(t *testing.T, srv *httptest.Server) *cobra.Command {
			cmd := cmdForUpdate(t, srv)
			if err := cmd.ParseFlags([]string{"--answer", "5.6.7.8"}); err != nil {
				t.Fatalf("ParseFlags: %v", err)
			}
			return cmd
		}
		// A full PUT replacement built from the GET plus the changed flag. The
		// unset fields coming back from the stub are what make this the
		// read-modify-write assertion, not just a body check.
		drifttest.AssertRequest(t, drifttest.Request{
			Method: "PUT",
			Path:   "/core/v1/domains/example.com/records/42",
			Body:   `{"answer":"5.6.7.8","host":"www","ttl":300,"type":"A"}`,
		}, build, runUpdate, []string{"example.com", "42"}, stub)
	})

	t.Run("delete", func(t *testing.T) {
		drifttest.AssertRequest(t, drifttest.Request{
			Method: "DELETE",
			Path:   "/core/v1/domains/example.com/records/42",
		}, cmdForDelete, runDelete, []string{"example.com", "42"}, stub)
	})
}
