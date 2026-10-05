package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/patramsey/namecom-cli/cmd/cmdutil"
	"github.com/patramsey/namecom-cli/internal/config"
	"github.com/patramsey/namecom-cli/internal/output"
	"github.com/spf13/cobra"
)

// Commands that name "the active profile" — auth logout, auth status, status —
// each picked it with their own chain: --profile, then `default:`, then the
// literal "default". API commands go through config.Resolve, which also honors
// NAMECOM_PROFILE and the implied default. The mismatch made
// `NAMECOM_PROFILE=staging namecom auth logout` delete the production profile,
// and made auth status — which CI runs to confirm which credentials it has —
// authenticate as staging while reporting prod.

const twoProfiles = "default: prod\nprofiles:\n" +
	"  prod:\n    username: produser\n    token: ptok\n" +
	"  staging:\n    username: stageuser\n    token: stok\n    sandbox: true\n"

// loneProfile has no `default:` key and no profile named "default"; Resolve
// uses it because it is the only one.
const loneProfile = "profiles:\n  work:\n    username: workuser\n    token: wtok\n"

// withConfig points the CLI at a config file holding contents and clears the
// ambient environment that would otherwise select a profile or credentials.
func withConfig(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("writing config: %v", err)
	}
	t.Setenv("NAMECOM_CONFIG", path)
	for _, k := range []string{"NAMECOM_PROFILE", "NAMECOM_USERNAME", "NAMECOM_TOKEN", "NAMECOM_SANDBOX", "NAMECOM_BASE_URL"} {
		t.Setenv(k, "")
	}
	return path
}

func jsonCmd(t *testing.T) (*cobra.Command, *bytes.Buffer) {
	t.Helper()
	var buf bytes.Buffer
	out := &output.Config{Format: output.FormatJSON, Color: output.ColorNever, Writer: &buf, EWriter: &bytes.Buffer{}}
	cmd := &cobra.Command{}
	cmd.SetContext(context.WithValue(context.Background(), cmdutil.KeyOutput, out))
	return cmd, &buf
}

func TestAuthLogout_RemovesTheActiveProfile(t *testing.T) {
	tests := []struct {
		name     string
		contents string
		env      string // NAMECOM_PROFILE
		flag     string // logout's own --profile
		wantGone string
		wantKept string
	}{
		{"NAMECOM_PROFILE", twoProfiles, "staging", "", "staging", "prod"},
		{"file default", twoProfiles, "", "", "prod", "staging"},
		{"--profile beats NAMECOM_PROFILE", twoProfiles, "staging", "prod", "prod", "staging"},
		{"lone profile not named default", loneProfile, "", "", "work", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			withConfig(t, tt.contents)
			t.Setenv("NAMECOM_PROFILE", tt.env)
			prev := logoutProfile
			logoutProfile = tt.flag
			t.Cleanup(func() { logoutProfile = prev })

			cmd, _ := jsonCmd(t)
			if err := runAuthLogout(cmd, nil); err != nil {
				t.Fatalf("runAuthLogout: %v", err)
			}
			f, err := config.Load()
			if err != nil {
				t.Fatalf("config.Load: %v", err)
			}
			if _, ok := f.Profiles[tt.wantGone]; ok {
				t.Errorf("profile %q should have been removed; left %v", tt.wantGone, f.Profiles)
			}
			if tt.wantKept != "" {
				if _, ok := f.Profiles[tt.wantKept]; !ok {
					t.Errorf("profile %q was removed, but it was not the active profile", tt.wantKept)
				}
			}
		})
	}
}

// helloServer answers the auth status credential check.
func helloServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestAuthStatus_ReportsTheActiveProfile(t *testing.T) {
	tests := []struct {
		name, contents, env, wantProfile, wantUser string
	}{
		{"NAMECOM_PROFILE", twoProfiles, "staging", "staging", "stageuser"},
		{"lone profile not named default", loneProfile, "", "work", "workuser"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			withConfig(t, tt.contents)
			t.Setenv("NAMECOM_PROFILE", tt.env)
			prev := gf
			gf = globalFlags{baseURL: helloServer(t).URL}
			t.Cleanup(func() { gf = prev })

			cmd, buf := jsonCmd(t)
			if err := runAuthStatus(cmd, nil); err != nil {
				t.Fatalf("runAuthStatus: %v", err)
			}
			var got map[string]any
			if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
				t.Fatalf("parsing output: %v\n%s", err, buf.String())
			}
			if got["profile"] != tt.wantProfile || got["username"] != tt.wantUser {
				t.Errorf("reported profile %q / username %q, want %q / %q",
					got["profile"], got["username"], tt.wantProfile, tt.wantUser)
			}
		})
	}
}

func TestStatus_ReportsTheActiveProfile(t *testing.T) {
	tests := []struct {
		name, contents, env, wantProfile string
	}{
		{"NAMECOM_PROFILE", twoProfiles, "staging", "staging"},
		{"lone profile not named default", loneProfile, "", "work"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			withConfig(t, tt.contents)
			t.Setenv("NAMECOM_PROFILE", tt.env)
			f, err := config.Load()
			if err != nil {
				t.Fatalf("config.Load: %v", err)
			}

			cmd, buf := statusCmdFor(t, statusServer(t, http.StatusOK, `{"balance":1}`))
			cmdutil.Out(cmd).Format = output.FormatJSON
			cmd.SetContext(context.WithValue(cmd.Context(), cmdutil.KeyConfig, f))
			if err := runStatus(cmd, nil); err != nil {
				t.Fatalf("runStatus: %v", err)
			}
			var got statusSummary
			if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
				t.Fatalf("parsing output: %v\n%s", err, buf.String())
			}
			if got.Profile != tt.wantProfile {
				t.Errorf("status reported profile %q, want %q", got.Profile, tt.wantProfile)
			}
		})
	}
}

// TestPersistentPreRun_StoresOverridesForCredentialFreeCommands: `config show
// --profile sandbox` is that command's documented example, but the config
// group skips client init, and only client init stored the global flags on
// the context — so the flag never reached the command, which described the
// default profile instead.
func TestPersistentPreRun_StoresOverridesForCredentialFreeCommands(t *testing.T) {
	withConfig(t, twoProfiles)
	prev := gf
	gf = globalFlags{profile: "staging", color: "auto"}
	t.Cleanup(func() { gf = prev })

	show, _, err := rootCmd.Find([]string{"config", "show"})
	if err != nil {
		t.Fatalf("finding config show: %v", err)
	}
	show.SetContext(context.Background())
	if err := persistentPreRunE(show, nil); err != nil {
		t.Fatalf("persistentPreRunE: %v", err)
	}
	if got := cmdutil.Overrides(show).Profile; got != "staging" {
		t.Errorf("config show sees --profile %q, want staging", got)
	}
}

// TestAuthStatus_RejectionNamesTheCredentials pins #187: with a bad token,
// auth status printed only "Unauthorized", although the error hints point
// users to it to see which credentials are in use.
func TestAuthStatus_RejectionNamesTheCredentials(t *testing.T) {
	path := withConfig(t, loneProfile)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"message":"Unauthorized"}`))
	}))
	t.Cleanup(srv.Close)
	prev := gf
	gf = globalFlags{baseURL: srv.URL}
	t.Cleanup(func() { gf = prev })

	cmd, _ := jsonCmd(t)
	err := runAuthStatus(cmd, nil)
	if got := exitCode(err); got != 3 {
		t.Errorf("exit code = %d, want 3 (%v)", got, err)
	}
	for _, want := range []string{"Unauthorized", `profile "work"`, `username "workuser"`, srv.URL, path} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("error %v does not mention %s", err, want)
		}
	}
}

// TestUnknownProfile_IsAnAuthError pins #210: an unknown profile, named by
// --profile or NAMECOM_PROFILE, exited 1 like an API failure. It means there
// are no usable credentials, so it exits 3 and names the profiles that exist.
func TestUnknownProfile_IsAnAuthError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		t.Errorf("no request expected, got %s %s", r.Method, r.URL)
	}))
	t.Cleanup(srv.Close)
	for _, tc := range []struct {
		name string
		env  string
		args []string
	}{
		{"flag", "", []string{"--profile", "nope"}},
		{"env", "nope", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			withConfig(t, twoProfiles)
			t.Setenv("NAMECOM_PROFILE", tc.env)
			args := append([]string{"--base-url", srv.URL}, tc.args...)
			err := executeRoot(t, append(args, "domain", "list")...)
			if got := exitCode(cmdutil.ClassifyCobraUsage(err)); got != 3 {
				t.Errorf("exit %d (%v), want 3", got, err)
			}
			// Sorted, so the list reads the same on every run.
			if err == nil || !strings.Contains(err.Error(), `profile "nope" not found`) ||
				!strings.Contains(err.Error(), "prod, staging") {
				t.Errorf("error %v should name the missing profile and list prod, staging", err)
			}
		})
	}
}
