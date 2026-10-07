package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/patramsey/namecom-cli/cmd/cmdutil"
	"github.com/patramsey/namecom-cli/internal/output"
	"github.com/spf13/cobra"
)

// The config-writing commands ignored the global --dry-run (#166): `auth
// logout --dry-run` deleted the profile and `auth login --dry-run` saved one.
// Under --dry-run they describe the change and leave the file alone.

// unchanged fails the test if the file at path no longer holds before.
// before is "" for a file that must not exist.
func unchanged(t *testing.T, path, before string) {
	t.Helper()
	data, err := os.ReadFile(path) //nolint:gosec // test temp file
	if before == "" {
		if err == nil {
			t.Errorf("--dry-run created %s:\n%s", path, data)
		}
		return
	}
	if err != nil {
		t.Fatalf("reading config: %v", err)
	}
	if string(data) != before {
		t.Errorf("--dry-run rewrote the config:\nbefore:\n%s\nafter:\n%s", before, data)
	}
}

func TestAuthLogout_DryRunLeavesConfig(t *testing.T) {
	path := withConfig(t, twoProfiles)
	prev := logoutProfile
	logoutProfile = "staging"
	t.Cleanup(func() { logoutProfile = prev })

	cmd, buf := jsonCmd(t)
	cmd.PersistentFlags().Bool("dry-run", true, "")
	if err := runAuthLogout(cmd, nil); err != nil {
		t.Fatalf("runAuthLogout --dry-run: %v", err)
	}
	unchanged(t, path, twoProfiles)

	var got map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("dry-run output is not one JSON document: %v\n%s", err, buf.String())
	}
	if got["dryRun"] != true || got["action"] != "remove_profile" || got["profile"] != "staging" || got["config"] != path {
		t.Errorf("dry-run should describe removing staging from %s, got %v", path, got)
	}
	// prod stays the default, so the preview says so.
	if got["default"] != "prod" {
		t.Errorf("default after the change = %v, want prod", got["default"])
	}
}

// TestAuthLogout_DryRunNamesTheResultingDefault pins #292: removing the
// default profile with one other left previewed "default": "", though the
// one left becomes the implied default. The preview names it, and where it
// comes from, in JSON and in the table line.
func TestAuthLogout_DryRunNamesTheResultingDefault(t *testing.T) {
	const contents = "default: default\nprofiles:\n  default:\n    username: a\n    token: t1\n  prod:\n    username: b\n    token: t2\n"
	for _, tc := range []struct {
		name, profile, wantDefault, wantSource string
	}{
		{"implied by the one left", "default", "prod", "implied"},
		{"the default: key", "prod", "default", "config"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := withConfig(t, contents)
			prev := logoutProfile
			logoutProfile = tc.profile
			t.Cleanup(func() { logoutProfile = prev })

			cmd, buf := jsonCmd(t)
			cmd.PersistentFlags().Bool("dry-run", true, "")
			if err := runAuthLogout(cmd, nil); err != nil {
				t.Fatalf("runAuthLogout --dry-run: %v", err)
			}
			unchanged(t, path, contents)
			var got map[string]any
			if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
				t.Fatalf("not JSON: %v\n%s", err, buf.String())
			}
			if got["default"] != tc.wantDefault || got["defaultSource"] != tc.wantSource {
				t.Errorf("default = %v (%v), want %s (%s)", got["default"], got["defaultSource"], tc.wantDefault, tc.wantSource)
			}

			cmd, buf = jsonCmd(t)
			cmdutil.Out(cmd).Format = output.FormatTable
			cmd.PersistentFlags().Bool("dry-run", true, "")
			if err := runAuthLogout(cmd, nil); err != nil {
				t.Fatalf("runAuthLogout --dry-run: %v", err)
			}
			if want := fmt.Sprintf("leaving %q as the default", tc.wantDefault); !strings.Contains(buf.String(), want) {
				t.Errorf("table = %q, want it to say %s", buf.String(), want)
			}
		})
	}
}

func TestAuthLogin_DryRunLeavesConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	withConfig(t, "")
	t.Setenv("NAMECOM_CONFIG", path) // a file that does not exist yet
	stubLoginForm(t, false)
	prev := loginProfile
	loginProfile = "work"
	t.Cleanup(func() { loginProfile = prev })

	cmd, buf := jsonCmd(t)
	cmd.PersistentFlags().Bool("dry-run", true, "")
	if err := runAuthLogin(cmd, nil); err != nil {
		t.Fatalf("runAuthLogin --dry-run: %v", err)
	}
	unchanged(t, path, "")

	var got map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("dry-run output is not one JSON document: %v\n%s", err, buf.String())
	}
	if got["dryRun"] != true || got["action"] != "save_profile" || got["profile"] != "work" ||
		got["username"] != "alice" || got["default"] != "work" {
		t.Errorf("dry-run should describe saving profile work for alice, got %v", got)
	}
	if strings.Contains(buf.String(), "tok") {
		t.Errorf("dry-run printed the token:\n%s", buf.String())
	}
}

// TestDNSImport_GlobalDryRunEitherPosition pins #187: dns import had its own
// --dry-run, which shadowed the global flag and hid it from the command's
// help. With only the global flag, both positions still preview.
func TestDNSImport_GlobalDryRunEitherPosition(t *testing.T) {
	withConfig(t, loneProfile)
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		t.Errorf("--dry-run sent %s %s", r.Method, r.URL)
	}))
	t.Cleanup(srv.Close)
	path := filepath.Join(t.TempDir(), "records.json")
	if err := os.WriteFile(path, []byte(`[{"type":"A","host":"www","answer":"1.2.3.4","ttl":300}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"--dry-run", "dns", "import", "example.com", "--file", path},
		{"dns", "import", "example.com", "--file", path, "--dry-run"},
	} {
		if err := executeRoot(t, append([]string{"--base-url", srv.URL, "-o", "json"}, args...)...); err != nil {
			t.Errorf("namecom %s: %v", strings.Join(args, " "), err)
		}
	}
	if f := dnsImportCmd().LocalNonPersistentFlags().Lookup("dry-run"); f != nil {
		t.Error("dns import still defines its own --dry-run")
	}
}

func dnsImportCmd() *cobra.Command {
	c, _, err := rootCmd.Find([]string{"dns", "import"})
	if err != nil {
		panic(err)
	}
	return c
}
