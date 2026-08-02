package dns

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/patramsey/namecom-cli/cmd/cmdutil"
	"github.com/spf13/cobra"
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

// TestDryRunMatchesRealRequest_DNS runs each mutating dns command twice — once
// with --dry-run to capture what we PRINT, once against a live test server to
// capture what we SEND — and asserts they agree.
//
// DNS is the highest-stakes preview in the CLI: `dns update` is a full PUT
// replacement and `dns delete` removes a live record, so this is exactly the
// command someone reaches for --dry-run on before touching production.
func TestDryRunMatchesRealRequest_DNS(t *testing.T) {
	const getResponse = `{"id":42,"type":"A","host":"www","answer":"1.2.3.4","ttl":3600}`

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
				if err := cmd.ParseFlags([]string{
					"--type", "A", "--host", "www", "--answer", "1.2.3.4",
				}); err != nil {
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
				if err := cmd.ParseFlags([]string{"--answer", "5.6.7.8"}); err != nil {
					t.Fatalf("ParseFlags: %v", err)
				}
				return cmd
			},
			args: []string{"example.com", "42"},
			run:  runUpdate,
		},
		{
			name:  "delete",
			setup: func(t *testing.T, srv *httptest.Server) *cobra.Command { return cmdForDelete(t, srv) },
			args:  []string{"example.com", "42"},
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
		})
	}
}
