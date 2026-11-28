package vanity

import (
	"net/http/httptest"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/patramsey/namecom-cli/internal/drifttest"
)

func TestRequestShape_Vanity(t *testing.T) {
	const stub = `{"domainName":"example.com","hostname":"ns1.example.com","ips":["1.2.3.4"]}`
	t.Run("create", func(t *testing.T) {
		build := func(t *testing.T, srv *httptest.Server) *cobra.Command {
			cmd := cmdForCreate(t, srv)
			if err := cmd.ParseFlags([]string{"--hostname", "ns1.example.com", "--ips", "1.2.3.4"}); err != nil {
				t.Fatalf("ParseFlags: %v", err)
			}
			return cmd
		}
		drifttest.AssertRequest(t, drifttest.Request{
			Method: "POST", Path: "/core/v1/domains/example.com/vanity_nameservers", Body: `{"hostname":"ns1","ips":["1.2.3.4"]}`,
		}, build, runCreate, []string{"example.com"}, stub)
	})
	t.Run("update", func(t *testing.T) {
		build := func(t *testing.T, srv *httptest.Server) *cobra.Command {
			cmd := cmdForUpdate(t, srv)
			if err := cmd.ParseFlags([]string{"--ips", "5.6.7.8"}); err != nil {
				t.Fatalf("ParseFlags: %v", err)
			}
			return cmd
		}
		drifttest.AssertRequest(t, drifttest.Request{
			Method: "PUT", Path: "/core/v1/domains/example.com/vanity_nameservers/ns1.example.com", Body: `{"ips":["5.6.7.8"]}`,
		}, build, runUpdate, []string{"example.com", "ns1.example.com"}, stub)
	})

	// A repeated --ips adds to the list; it kept only the last value (#381).
	t.Run("update --ips repeated", func(t *testing.T) {
		for _, c := range []*cobra.Command{createCmd, updateCmd} {
			if _, ok := c.Flags().Lookup("ips").Value.(pflag.SliceValue); !ok {
				t.Fatalf("%s --ips is not a list flag", c.Name())
			}
		}
		build := func(t *testing.T, srv *httptest.Server) *cobra.Command {
			cmd := cmdForUpdate(t, srv)
			if err := cmd.ParseFlags([]string{"--ips", "5.6.7.8", "--ips", "192.0.2.1"}); err != nil {
				t.Fatalf("ParseFlags: %v", err)
			}
			return cmd
		}
		drifttest.AssertRequest(t, drifttest.Request{
			Method: "PUT", Path: "/core/v1/domains/example.com/vanity_nameservers/ns1.example.com", Body: `{"ips":["5.6.7.8","192.0.2.1"]}`,
		}, build, runUpdate, []string{"example.com", "ns1.example.com"}, stub)
	})
}
