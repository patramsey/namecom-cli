package email

import (
	"net/http/httptest"
	"testing"

	"github.com/spf13/cobra"

	"github.com/patramsey/namecom-cli/internal/drifttest"
)

func TestRequestShape_Email(t *testing.T) {
	const stub = `{"domainName":"example.com","emailBox":"sales","emailTo":"me@elsewhere.test"}`
	t.Run("create", func(t *testing.T) {
		build := func(t *testing.T, srv *httptest.Server) *cobra.Command {
			cmd := cmdForEmailCreate(t, srv)
			if err := cmd.ParseFlags([]string{"--to", "me@elsewhere.test"}); err != nil {
				t.Fatalf("ParseFlags: %v", err)
			}
			return cmd
		}
		drifttest.AssertRequest(t, drifttest.Request{
			Method: "POST", Path: "/core/v1/domains/example.com/email/forwarding", Body: `{"emailBox":"sales","emailTo":"me@elsewhere.test"}`,
		}, build, runCreate, []string{"example.com", "sales"}, stub)
	})
	t.Run("update", func(t *testing.T) {
		build := func(t *testing.T, srv *httptest.Server) *cobra.Command {
			cmd := cmdForEmailUpdate(t, srv)
			if err := cmd.ParseFlags([]string{"--to", "new@elsewhere.test"}); err != nil {
				t.Fatalf("ParseFlags: %v", err)
			}
			return cmd
		}
		drifttest.AssertRequest(t, drifttest.Request{
			Method: "PUT", Path: "/core/v1/domains/example.com/email/forwarding/sales", Body: `{"emailTo":"new@elsewhere.test"}`,
		}, build, runUpdate, []string{"example.com", "sales"}, stub)
	})
}
