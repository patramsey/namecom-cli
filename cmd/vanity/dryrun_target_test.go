package vanity

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/patramsey/namecom-cli/cmd/cmdutil"
	"github.com/spf13/cobra"
)

// TestDryRun_MissingNameserverIsNotFound pins #292: a dry run of update or
// delete previewed a change to a vanity nameserver that does not exist, and
// exited 0. It reads the nameserver first — one GET, under --dry-run only —
// and fails not_found as the real request would. A real run sends the write
// alone.
func TestDryRun_MissingNameserverIsNotFound(t *testing.T) {
	for _, tc := range []struct {
		name  string
		build func(*testing.T, *httptest.Server) *cobra.Command
		run   func(*cobra.Command, []string) error
	}{
		{"update", cmdForUpdate, runUpdate},
		{"delete", baseCmd, runDelete},
	} {
		t.Run(tc.name+" dry run", func(t *testing.T) {
			var requests []string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests = append(requests, r.Method)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"message":"Not Found","details":"Hostname not found."}`))
			}))
			t.Cleanup(srv.Close)

			cmd := withDryRun(t, tc.build(t, srv), true)
			err := tc.run(cmd, []string{"example.com", "ns9"})
			if !cmdutil.IsNotFound(err) {
				t.Fatalf("want not found, got %v", err)
			}
			if !strings.Contains(err.Error(), "ns9.example.com") {
				t.Errorf("the error should name the nameserver: %v", err)
			}
			if got := strings.Join(requests, " "); got != "GET" {
				t.Errorf("requests = %q, want one GET", got)
			}
		})

		t.Run(tc.name+" real run sends only the write", func(t *testing.T) {
			var requests []string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests = append(requests, r.Method)
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"hostname":"ns9.example.com","ips":[]}`))
			}))
			t.Cleanup(srv.Close)

			cmd := withDryRun(t, tc.build(t, srv), false)
			if err := tc.run(cmd, []string{"example.com", "ns9"}); err != nil {
				t.Fatalf("run: %v", err)
			}
			if len(requests) != 1 || requests[0] == http.MethodGet {
				t.Errorf("requests = %v, want the write alone", requests)
			}
		})
	}
}

// TestCreate_DryRunOfDomainNotInAccount pins #362: `vanity-ns create
// --dry-run` for a domain not in the account previewed the nameserver and
// exited 0. It reads the domain — one GET, under --dry-run only — and fails
// not_found, as the create would. A real run sends the create alone.
func TestCreate_DryRunOfDomainNotInAccount(t *testing.T) {
	for _, dry := range []bool{true, false} {
		var requests []string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requests = append(requests, r.Method+" "+r.URL.Path)
			w.Header().Set("Content-Type", "application/json")
			if r.Method == http.MethodGet {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"message":"Not Found"}`))
				return
			}
			_, _ = w.Write([]byte(`{"hostname":"ns1.example.com","ips":["192.0.2.1"]}`))
		}))
		t.Cleanup(srv.Close)

		cmd := cmdForCreate(t, srv)
		if err := cmd.ParseFlags([]string{"--hostname", "ns1", "--ips", "192.0.2.1"}); err != nil {
			t.Fatal(err)
		}
		err := runCreate(withDryRun(t, cmd, dry), []string{"example.com"})
		got := strings.Join(requests, " ")
		if dry {
			if !cmdutil.IsNotFound(err) || !strings.Contains(err.Error(), "example.com") {
				t.Errorf("dry run: want not found naming the domain, got %v", err)
			}
			if got != "GET /core/v1/domains/example.com" {
				t.Errorf("dry run: requests = %q, want the domain read alone", got)
			}
			continue
		}
		if err != nil || got != "POST /core/v1/domains/example.com/vanity_nameservers" {
			t.Errorf("real run: err %v, requests %q, want the create alone", err, got)
		}
	}
}

// TestIPs_Validated pins #292: --ips took "999.1.1.1" and previewed it. Each
// entry must be an IPv4 or IPv6 address, as in `dns create`, refused before
// any request.
func TestIPs_Validated(t *testing.T) {
	for _, bad := range []string{"999.1.1.1", "1.2.3", "ns1.example.com", "1.2.3.4,::g"} {
		t.Run("create "+bad, func(t *testing.T) {
			cmd := cmdForCreate(t, neverCalledServer(t))
			if err := cmd.ParseFlags([]string{"--hostname", "ns1", "--ips", bad}); err != nil {
				t.Fatal(err)
			}
			if err := runCreate(withDryRun(t, cmd, true), []string{"example.com"}); !isUsage(err) {
				t.Errorf("want a usage error, got %v", err)
			}
		})
		t.Run("update "+bad, func(t *testing.T) {
			cmd := cmdForUpdate(t, neverCalledServer(t))
			if err := cmd.ParseFlags([]string{"--ips", bad}); err != nil {
				t.Fatal(err)
			}
			if err := runUpdate(withDryRun(t, cmd, true), []string{"example.com", "ns1"}); !isUsage(err) {
				t.Errorf("want a usage error, got %v", err)
			}
		})
	}
	if _, err := parseIPs("192.0.2.1, 2001:db8::1"); err != nil {
		t.Errorf("IPv4 and IPv6 must pass: %v", err)
	}
}
