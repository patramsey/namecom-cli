package vanity

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/patramsey/namecom-cli/cmd/cmdutil"
	"github.com/spf13/cobra"

	"github.com/patramsey/namecom-cli/internal/drifttest"
)

// httpMethods is the set recognized when scanning dry-run output for the
// METHOD /path line.
var httpMethods = map[string]bool{
	"GET": true, "POST": true, "PUT": true, "PATCH": true, "DELETE": true,
}

// withDryRun attaches a root command carrying --dry-run and --yes=true so
// cmdutil.IsDryRun / IsYes see them, mirroring how the real CLI wires
// persistent flags. --yes is always set: these commands confirm before
// mutating, and the live half of the comparison must not block on a prompt.
func withDryRun(t *testing.T, child *cobra.Command, dryRun bool) *cobra.Command {
	t.Helper()
	root := &cobra.Command{Use: "namecom"}
	var dr, yes bool
	root.PersistentFlags().BoolVar(&dr, "dry-run", false, "")
	root.PersistentFlags().BoolVar(&yes, "yes", false, "")
	if err := root.PersistentFlags().Set("yes", "true"); err != nil {
		t.Fatalf("setting yes flag: %v", err)
	}
	if dryRun {
		if err := root.PersistentFlags().Set("dry-run", "true"); err != nil {
			t.Fatalf("setting dry-run flag: %v", err)
		}
	}
	root.AddCommand(child)
	return child
}

func dryRunLine(t *testing.T, s string) string {
	t.Helper()
	for _, line := range strings.Split(s, "\n") {
		f := strings.Fields(line)
		if len(f) >= 2 && httpMethods[f[0]] && strings.HasPrefix(f[1], "/") {
			return f[0] + " " + f[1]
		}
	}
	t.Fatalf("no dry-run METHOD/path line found in output: %q", s)
	return ""
}

func captureDryRunLine(t *testing.T, run func(*httptest.Server) (*cobra.Command, error), getResponse string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(getResponse))
	}))
	t.Cleanup(srv.Close)

	cmd, err := run(srv)
	if err != nil {
		t.Fatalf("dry-run invocation failed: %v", err)
	}
	buf, ok := cmdutil.Out(cmd).Writer.(*bytes.Buffer)
	if !ok {
		t.Fatal("output writer is not a *bytes.Buffer")
	}
	return dryRunLine(t, buf.String())
}

// captureRealRequest runs the command for real and returns the method and path
// of the last request it made — the mutating one, after any read-modify-write
// GET.
func captureRealRequest(t *testing.T, run func(*httptest.Server) (*cobra.Command, error), getResponse string) string {
	t.Helper()
	var last string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		last = r.Method + " " + r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(getResponse))
	}))
	t.Cleanup(srv.Close)

	if _, err := run(srv); err != nil {
		t.Fatalf("live invocation failed: %v", err)
	}
	if last == "" {
		t.Fatal("no request was made")
	}
	return last
}

// TestDryRunMatchesRealRequest_Vanity runs each mutating vanity-ns command
// twice — once with --dry-run to capture what we PRINT, once against a live
// test server to capture what we SEND — and asserts they agree.
//
// Vanity nameservers are the records other domains delegate to, so a preview
// that names the wrong route is worse than no preview: the user checks it,
// believes they are editing one host, and mutates another.
func TestDryRunMatchesRealRequest_Vanity(t *testing.T) {
	const getResponse = `{"domainName":"example.com","hostname":"ns1.example.com","ips":["1.2.3.4"]}`

	tests := []struct {
		name  string
		setup func(*testing.T, *httptest.Server) *cobra.Command
		args  []string
		run   func(*cobra.Command, []string) error
	}{
		{
			name: "create",
			setup: func(t *testing.T, srv *httptest.Server) *cobra.Command {
				cmd := cmdForCreate(t, srv)
				if err := cmd.ParseFlags([]string{"--hostname", "ns1.example.com", "--ips", "1.2.3.4"}); err != nil {
					t.Fatalf("ParseFlags: %v", err)
				}
				return cmd
			},
			args: []string{"example.com"},
			run:  runCreate,
		},
		{
			name: "update",
			setup: func(t *testing.T, srv *httptest.Server) *cobra.Command {
				cmd := cmdForUpdate(t, srv)
				if err := cmd.ParseFlags([]string{"--ips", "5.6.7.8"}); err != nil {
					t.Fatalf("ParseFlags: %v", err)
				}
				return cmd
			},
			args: []string{"example.com", "ns1.example.com"},
			run:  runUpdate,
		},
		{
			name:  "delete",
			setup: func(t *testing.T, srv *httptest.Server) *cobra.Command { return cmdForDelete(t, srv) },
			args:  []string{"example.com", "ns1.example.com"},
			run:   runDelete,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			printed := captureDryRunLine(t, func(srv *httptest.Server) (*cobra.Command, error) {
				cmd := withDryRun(t, tc.setup(t, srv), true)
				return cmd, tc.run(cmd, tc.args)
			}, getResponse)

			sent := captureRealRequest(t, func(srv *httptest.Server) (*cobra.Command, error) {
				cmd := withDryRun(t, tc.setup(t, srv), false)
				return cmd, tc.run(cmd, tc.args)
			}, getResponse)

			if printed != sent {
				t.Errorf("--dry-run reports %q but the command actually sends %q", printed, sent)
			}

			// The body too: what --dry-run prints must be what is sent.
			drifttest.AssertDryRunBodyMatches(t, tc.setup, tc.run, tc.args, getResponse)
		})
	}
}

// TestVanity_BareLabelQualified covers issue #114: create accepts a bare label
// (`--hostname ns1`) and the API stores ns1.<domain>, but get, update and
// delete sent `ns1` through as the path parameter and got "Hostname not
// found." All four now normalize the hostname the same way, and --dry-run
// must preview the qualified path, not the one typed.
func TestVanity_BareLabelQualified(t *testing.T) {
	// Shape of the sandbox's reply to a bare-label create.
	const stub = `{"domainName":"example.com","hostname":"ns1.example.com","ips":["203.0.114.53"]}`
	const want = "/core/v1/domains/example.com/vanity_nameservers/ns1.example.com"

	commands := []struct {
		name, method string
		setup        func(*testing.T, *httptest.Server) *cobra.Command
		run          func(*cobra.Command, []string) error
		writes       bool
	}{
		{"get", "GET", func(t *testing.T, srv *httptest.Server) *cobra.Command { return baseCmd(t, srv) }, runGet, false},
		{"update", "PUT", func(t *testing.T, srv *httptest.Server) *cobra.Command {
			cmd := cmdForUpdate(t, srv)
			if err := cmd.ParseFlags([]string{"--ips", "203.0.114.53"}); err != nil {
				t.Fatalf("ParseFlags: %v", err)
			}
			return cmd
		}, runUpdate, true},
		{"delete", "DELETE", func(t *testing.T, srv *httptest.Server) *cobra.Command { return cmdForDelete(t, srv) }, runDelete, true},
	}
	spellings := []struct{ name, hostname string }{
		{"bare label", "ns1"},
		{"bare label, upper case", "NS1"},
		{"fqdn", "ns1.example.com"},
		{"fqdn, trailing dot and mixed case", "NS1.Example.COM."},
	}

	for _, c := range commands {
		for _, s := range spellings {
			t.Run(c.name+"/"+s.name, func(t *testing.T) {
				args := []string{"example.com", s.hostname}
				sent := captureRealRequest(t, func(srv *httptest.Server) (*cobra.Command, error) {
					cmd := withDryRun(t, c.setup(t, srv), false)
					return cmd, c.run(cmd, args)
				}, stub)
				if sent != c.method+" "+want {
					t.Errorf("sent %q, want %q", sent, c.method+" "+want)
				}
				if !c.writes {
					return
				}
				printed := captureDryRunLine(t, func(srv *httptest.Server) (*cobra.Command, error) {
					cmd := withDryRun(t, c.setup(t, srv), true)
					return cmd, c.run(cmd, args)
				}, stub)
				if printed != c.method+" "+want {
					t.Errorf("--dry-run previewed %q, want %q", printed, c.method+" "+want)
				}
			})
		}
	}
}

// TestVanity_HostnameWrongDomain holds the positional hostname to the same
// rule as create's --hostname: a name under another domain is rejected before
// any request, rather than sent and answered with a 404.
func TestVanity_HostnameWrongDomain(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*testing.T, *httptest.Server) *cobra.Command
		run   func(*cobra.Command, []string) error
	}{
		{"get", baseCmd, runGet},
		{"update", func(t *testing.T, srv *httptest.Server) *cobra.Command {
			cmd := cmdForUpdate(t, srv)
			updateIPs = "203.0.114.53"
			return cmd
		}, runUpdate},
		{"delete", cmdForDelete, runDelete},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cmd := tc.setup(t, neverCalledServer(t))
			err := tc.run(cmd, []string{"example.com", "ns1.other.com"})
			if err == nil {
				t.Fatal("expected error for hostname outside the target domain, got nil")
			}
			if !strings.Contains(err.Error(), "example.com") {
				t.Errorf("error should name the expected domain, got: %v", err)
			}
		})
	}
}
