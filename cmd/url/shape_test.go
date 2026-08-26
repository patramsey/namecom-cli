package url

import (
	"net/http/httptest"
	"testing"

	"github.com/spf13/cobra"

	"github.com/patramsey/namecom-cli/internal/drifttest"
)

func TestRequestShape_URL(t *testing.T) {
	const stub = `{"id":7,"domainName":"example.com","host":"@","forwardsTo":"https://example.org","type":"redirect"}`
	t.Run("create", func(t *testing.T) {
		build := func(t *testing.T, srv *httptest.Server) *cobra.Command {
			cmd := cmdForURLCreate(t, srv)
			if err := cmd.ParseFlags([]string{"--to", "https://example.org"}); err != nil {
				t.Fatalf("ParseFlags: %v", err)
			}
			return cmd
		}
		drifttest.AssertRequest(t, drifttest.Request{
			Method: "POST", Path: "/core/v1/domains/example.com/url/forwarding", Body: `{"forwardsTo":"https://example.org","host":"@","type":"redirect"}`,
		}, build, runCreate, []string{"example.com"}, stub)
	})
	t.Run("update", func(t *testing.T) {
		build := func(t *testing.T, srv *httptest.Server) *cobra.Command {
			cmd := cmdForURLUpdate(t, srv)
			if err := cmd.ParseFlags([]string{"--to", "https://elsewhere.example"}); err != nil {
				t.Fatalf("ParseFlags: %v", err)
			}
			return cmd
		}
		drifttest.AssertRequest(t, drifttest.Request{
			Method: "PATCH", Path: "/core/v1/urlforwarding/example.com/7", Body: `{"forwardsTo":"https://elsewhere.example","type":"redirect"}`,
		}, build, runUpdate, []string{"example.com", "7"}, stub)
	})
	t.Run("delete", func(t *testing.T) {
		drifttest.AssertRequest(t, drifttest.Request{
			Method: "DELETE", Path: "/core/v1/urlforwarding/example.com/7",
		}, cmdForURLDelete, runDelete, []string{"example.com", "7"}, stub)
	})
}
