package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/patramsey/namecom-cli/cmd/cmdutil"
	"github.com/patramsey/namecom-cli/internal/config"
	"github.com/patramsey/namecom-cli/internal/output"
	"github.com/spf13/cobra"
)

// ciLogin sets up a non-interactive `auth login` (#246): no terminal, the
// given --username, and login's own flags as given, all restored afterwards.
// Unlike stubLoginForm it leaves the replace-profile confirmation real, so
// what it does off a terminal is what is tested.
func ciLogin(t *testing.T, username string, withToken bool, tokenCmd string) {
	t.Helper()
	t.Cleanup(output.StubInteractive(false))
	prevGF, prevW, prevC, prevN, prevP := gf, loginWithToken, loginTokenCmd, loginNoVerify, loginProfile
	t.Cleanup(func() {
		gf, loginWithToken, loginTokenCmd, loginNoVerify, loginProfile = prevGF, prevW, prevC, prevN, prevP
	})
	gf = globalFlags{username: username}
	loginWithToken, loginTokenCmd, loginNoVerify, loginProfile = withToken, tokenCmd, false, "default"
}

// stdinCmd is jsonCmd with stdin reading from in.
func stdinCmd(t *testing.T, in string) (*cobra.Command, *bytes.Buffer) {
	t.Helper()
	cmd, buf := jsonCmd(t)
	cmd.SetIn(strings.NewReader(in))
	return cmd, buf
}

func TestAuthLogin_WithToken(t *testing.T) {
	t.Run("reads, trims, verifies and saves", func(t *testing.T) {
		withConfig(t, "")
		ciLogin(t, "alice", true, "")
		setLoginProfile(t, "ci")
		hello := stubHello(t, http.StatusOK, `{"username":"alice"}`)

		cmd, _ := stdinCmd(t, "tok123\n")
		cmd.PersistentFlags().Bool("sandbox", true, "")
		if err := runAuthLogin(cmd, nil); err != nil {
			t.Fatalf("runAuthLogin --with-token: %v", err)
		}
		if u, p := hello.user.Load(), hello.pass.Load(); u != "alice" || p != "tok123" {
			t.Errorf("credential check sent %v/%v, want alice/tok123", u, p)
		}
		f, _ := config.Load()
		if p := f.Profiles["ci"]; p.Username != "alice" || p.Token != "tok123" || !p.Sandbox || p.TokenCmd != "" {
			t.Errorf("saved %+v, want alice/tok123 in the sandbox", p)
		}
	})

	t.Run("rejected: exit 3, nothing saved, nothing asked", func(t *testing.T) {
		path := withConfig(t, loneProfile)
		ciLogin(t, "alice", true, "")
		setLoginProfile(t, "fresh")
		stubHello(t, http.StatusUnauthorized, `{"message":"Unauthorized"}`)
		asked := stubRetryLogin(t, true)

		cmd, _ := stdinCmd(t, "bad")
		err := runAuthLogin(cmd, nil)
		if exitCode(err) != 3 || !strings.Contains(fmtErr(err), "rejected") {
			t.Errorf("got %v (exit %d), want the exit-3 rejection", err, exitCode(err))
		}
		if *asked != 0 {
			t.Error("offered to try again without a terminal")
		}
		unchanged(t, path, loneProfile)
	})

	t.Run("unreachable: not saved, and says how to save anyway", func(t *testing.T) {
		path := withConfig(t, loneProfile)
		ciLogin(t, "alice", true, "")
		setLoginProfile(t, "fresh")
		stubUnreachable(t)

		cmd, _ := stdinCmd(t, "tok")
		err := runAuthLogin(cmd, nil)
		if err == nil || !strings.Contains(err.Error(), "--no-verify") {
			t.Errorf("got %v, want an error naming --no-verify", err)
		}
		unchanged(t, path, loneProfile)
	})

	t.Run("--no-verify saves without a request", func(t *testing.T) {
		withConfig(t, "")
		ciLogin(t, "alice", true, "")
		loginNoVerify = true
		hello := stubHello(t, http.StatusOK, `{}`)

		cmd, _ := stdinCmd(t, "tok")
		if err := runAuthLogin(cmd, nil); err != nil {
			t.Fatalf("runAuthLogin --no-verify: %v", err)
		}
		if n := hello.hits.Load(); n != 0 {
			t.Errorf("--no-verify sent %d requests", n)
		}
		f, _ := config.Load()
		if p := f.Profiles["default"]; p.Token != "tok" {
			t.Errorf("saved %+v, want token tok", p)
		}
	})

	t.Run("NAMECOM_BASE_URL is where the check goes", func(t *testing.T) {
		withConfig(t, "")
		ciLogin(t, "alice", true, "")
		hello := stubHello(t, http.StatusOK, `{"username":"alice"}`)
		t.Setenv("NAMECOM_BASE_URL", gf.baseURL)
		gf.baseURL = ""

		cmd, _ := stdinCmd(t, "tok")
		if err := runAuthLogin(cmd, nil); err != nil {
			t.Fatalf("runAuthLogin: %v", err)
		}
		if hello.hits.Load() != 1 {
			t.Errorf("the check sent %d requests to NAMECOM_BASE_URL, want 1", hello.hits.Load())
		}
	})
}

func TestAuthLogin_TokenCmd(t *testing.T) {
	t.Run("runs the command to verify, saves the command", func(t *testing.T) {
		withConfig(t, "")
		// echo works the same through sh and cmd.exe; cmd.exe's trailing
		// CRLF is trimmed.
		ciLogin(t, "alice", false, "echo helpertok")
		hello := stubHello(t, http.StatusOK, `{"username":"alice"}`)

		cmd, _ := stdinCmd(t, "")
		if err := runAuthLogin(cmd, nil); err != nil {
			t.Fatalf("runAuthLogin --token-cmd: %v", err)
		}
		if p := hello.pass.Load(); p != "helpertok" {
			t.Errorf("credential check sent token %v, want the command's output", p)
		}
		f, _ := config.Load()
		if p := f.Profiles["default"]; p.TokenCmd != "echo helpertok" || p.Token != "" || p.Username != "alice" {
			t.Errorf("saved %+v, want the token_cmd and no token", p)
		}
	})

	t.Run("a failing command saves nothing", func(t *testing.T) {
		path := withConfig(t, loneProfile)
		ciLogin(t, "alice", false, "exit 3")
		setLoginProfile(t, "fresh")
		hello := stubHello(t, http.StatusOK, `{}`)

		cmd, _ := stdinCmd(t, "")
		err := runAuthLogin(cmd, nil)
		if exitCode(err) != 3 || !strings.Contains(err.Error(), "--token-cmd") {
			t.Errorf("got %v (exit %d), want an exit-3 error naming --token-cmd", err, exitCode(err))
		}
		if hello.hits.Load() != 0 {
			t.Error("sent a credential check without a token")
		}
		unchanged(t, path, loneProfile)
	})

	t.Run("--no-verify does not run it", func(t *testing.T) {
		withConfig(t, "")
		ciLogin(t, "alice", false, "exit 3")
		loginNoVerify = true

		cmd, _ := stdinCmd(t, "")
		if err := runAuthLogin(cmd, nil); err != nil {
			t.Fatalf("runAuthLogin --token-cmd --no-verify: %v", err)
		}
		f, _ := config.Load()
		if p := f.Profiles["default"]; p.TokenCmd != "exit 3" {
			t.Errorf("saved %+v, want the token_cmd", p)
		}
	})

	t.Run("--dry-run previews it and writes nothing", func(t *testing.T) {
		path := withConfig(t, loneProfile)
		ciLogin(t, "alice", false, "echo helpertok")
		setLoginProfile(t, "fresh")
		stubHello(t, http.StatusOK, `{"username":"alice"}`)

		cmd, buf := jsonCmd(t)
		cmd.PersistentFlags().Bool("dry-run", true, "")
		if err := runAuthLogin(cmd, nil); err != nil {
			t.Fatalf("runAuthLogin --dry-run: %v", err)
		}
		var got map[string]any
		if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
			t.Fatalf("preview is not one JSON document: %v\n%s", err, buf.String())
		}
		if got["action"] != "save_profile" || got["usesTokenCmd"] != true {
			t.Errorf("preview = %v, want save_profile with usesTokenCmd", got)
		}
		if strings.Contains(buf.String(), "helpertok") {
			t.Errorf("preview shows the token:\n%s", buf.String())
		}
		unchanged(t, path, loneProfile)
	})
}

// TestAuthLogin_NonInteractiveReplace pins #253 for the non-interactive path:
// replacing a profile cannot be asked about without a terminal, so it needs
// --yes, and --yes replaces it.
func TestAuthLogin_NonInteractiveReplace(t *testing.T) {
	const existing = "default: default\nprofiles:\n  default:\n    username: old\n    token: oldtok\n"

	t.Run("without --yes", func(t *testing.T) {
		path := withConfig(t, existing)
		ciLogin(t, "alice", true, "")
		hello := stubHello(t, http.StatusOK, `{}`)

		cmd, _ := stdinCmd(t, "tok")
		err := runAuthLogin(cmd, nil)
		if exitCode(err) != 2 || !strings.Contains(fmtErr(err), "--yes") {
			t.Errorf("got %v (exit %d), want a usage error asking for --yes", err, exitCode(err))
		}
		if hello.hits.Load() != 0 {
			t.Error("checked credentials it was not allowed to save")
		}
		unchanged(t, path, existing)
	})

	t.Run("with --yes", func(t *testing.T) {
		withConfig(t, existing)
		ciLogin(t, "alice", true, "")
		stubHello(t, http.StatusOK, `{"username":"alice"}`)

		cmd, _ := stdinCmd(t, "tok")
		cmd.PersistentFlags().Bool("yes", true, "")
		if err := runAuthLogin(cmd, nil); err != nil {
			t.Fatalf("runAuthLogin --yes: %v", err)
		}
		f, _ := config.Load()
		if p := f.Profiles["default"]; p.Username != "alice" || p.Token != "tok" {
			t.Errorf("saved %+v, want alice/tok", p)
		}
	})
}

// TestAuthLogin_NonInteractiveUsageErrors: each mistake is a usage error
// (exit 2) found before anything is read, sent or written.
func TestAuthLogin_NonInteractiveUsageErrors(t *testing.T) {
	tests := []struct {
		name      string
		username  string
		withToken bool
		tokenCmd  string
		token     string // global --token
		stdin     string
		want      string
	}{
		{"no flags and no terminal", "", false, "", "", "", "--with-token"},
		{"--with-token without --username", "", true, "", "", "tok", "--username"},
		{"--token-cmd without --username", "", false, "echo x", "", "", "--username"},
		{"both token sources", "alice", true, "echo x", "", "tok", "together"},
		{"--token on the command line", "alice", false, "", "tok", "", "--with-token"},
		{"empty standard input", "alice", true, "", "", " \n", "no token"},
		{"more than one line", "alice", true, "", "", "tok\nsecond\n", "one line"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := withConfig(t, loneProfile)
			ciLogin(t, tt.username, tt.withToken, tt.tokenCmd)
			gf.token = tt.token
			hello := stubHello(t, http.StatusOK, `{}`)

			cmd, _ := stdinCmd(t, tt.stdin)
			err := runAuthLogin(cmd, nil)
			if _, ok := errors.AsType[*cmdutil.UsageError](err); !ok {
				t.Fatalf("got %v, want a usage error", err)
			}
			if !strings.Contains(fmtErr(err), tt.want) {
				t.Errorf("error and hint %q do not mention %q", fmtErr(err), tt.want)
			}
			if strings.Contains(fmtErr(err), "second") {
				t.Errorf("echoed standard input: %q", fmtErr(err))
			}
			if hello.hits.Load() != 0 {
				t.Error("sent a credential check")
			}
			unchanged(t, path, loneProfile)
		})
	}
}

// fmtErr is err's message and hint together.
func fmtErr(err error) string {
	if err == nil {
		return ""
	}
	s := err.Error()
	if h, ok := errors.AsType[interface {
		error
		UserHint() string
	}](err); ok {
		s += " | " + h.UserHint()
	}
	return s
}

// TestEnvOnlyCredentials covers #246's CI case: no config file at all, the
// credentials in NAMECOM_USERNAME and NAMECOM_TOKEN. API commands worked;
// config show reported a missing profile, auth status could not say where
// the values came from, and a 401 said to run `auth login`.
func TestEnvOnlyCredentials(t *testing.T) {
	envOnly := func(t *testing.T) {
		t.Helper()
		withConfig(t, "")
		t.Setenv("NAMECOM_CONFIG", t.TempDir()+"/missing.yaml")
		t.Setenv("NAMECOM_USERNAME", "ciuser")
		t.Setenv("NAMECOM_TOKEN", "citok")
	}

	t.Run("config show", func(t *testing.T) {
		envOnly(t)
		cmd, buf := jsonCmd(t)
		if err := configShow(t, cmd); err != nil {
			t.Fatalf("config show with env-only credentials: %v", err)
		}
		var got map[string]string
		if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
			t.Fatalf("parsing: %v\n%s", err, buf.String())
		}
		want := map[string]string{ //nolint:gosec // G101: source names, not credentials
			"profile": "", "username": "ciuser",
			"usernameSource": "env NAMECOM_USERNAME", "tokenSource": "env NAMECOM_TOKEN",
			"endpoint": "https://api.name.com", "endpointSource": "default",
		}
		for k, v := range want {
			if got[k] != v {
				t.Errorf("%s = %q, want %q (all: %v)", k, got[k], v, got)
			}
		}
	})

	t.Run("auth status reports the sources", func(t *testing.T) {
		envOnly(t)
		prev := gf
		gf = globalFlags{baseURL: helloServer(t).URL}
		t.Cleanup(func() { gf = prev })

		cmd, buf := jsonCmd(t)
		if err := runAuthStatus(cmd, nil); err != nil {
			t.Fatalf("auth status with env-only credentials: %v", err)
		}
		var got map[string]any
		if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
			t.Fatalf("parsing: %v\n%s", err, buf.String())
		}
		if got["username"] != "ciuser" || got["usernameSource"] != "env NAMECOM_USERNAME" ||
			got["tokenSource"] != "env NAMECOM_TOKEN" || got["endpointSource"] != "flag --base-url" {
			t.Errorf("auth status = %v, want ciuser with env and flag sources", got)
		}
		if _, ok := got["token"]; ok {
			t.Errorf("auth status emitted a token key: %v", got)
		}
	})

	t.Run("a 401 names NAMECOM_TOKEN, not auth login", func(t *testing.T) {
		envOnly(t)
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
		if exitCode(err) != 3 || !strings.Contains(err.Error(), "token from env NAMECOM_TOKEN") {
			t.Errorf("got %v, want an exit-3 error naming the token's source", err)
		}
		stderr := renderedError(err)
		if !strings.Contains(stderr, "NAMECOM_TOKEN") || strings.Contains(stderr, "auth login") {
			t.Errorf("hint should name NAMECOM_TOKEN and not auth login:\n%s", stderr)
		}
	})

	t.Run("token without a username", func(t *testing.T) {
		envOnly(t)
		t.Setenv("NAMECOM_USERNAME", "")
		prev := gf
		gf = globalFlags{}
		t.Cleanup(func() { gf = prev })
		for name, run := range map[string]func() error{
			"API command": func() error { cmd, _ := jsonCmd(t); return initContext(cmd) },
			"config show": func() error { cmd, _ := jsonCmd(t); return configShow(t, cmd) },
		} {
			err := run()
			if exitCode(err) != 3 {
				t.Errorf("%s: got %v, want exit 3", name, err)
			}
			if stderr := renderedError(err); !strings.Contains(stderr, "NAMECOM_USERNAME") || strings.Contains(stderr, "auth login") {
				t.Errorf("%s: hint should ask for NAMECOM_USERNAME, not auth login:\n%s", name, stderr)
			}
		}
	})
}

// configShow runs `config show` through the real command, so the overrides
// reach it as they do from root.
func configShow(t *testing.T, cmd *cobra.Command) error {
	t.Helper()
	show, _, err := rootCmd.Find([]string{"config", "show"})
	if err != nil {
		t.Fatal(err)
	}
	show.SetContext(cmd.Context())
	return show.RunE(show, nil)
}

// TestBaseURLEnv: NAMECOM_BASE_URL points API commands at a stub as --base-url
// does, with the same validation and warning, and the flag wins (#246).
func TestBaseURLEnv(t *testing.T) {
	setup := func(t *testing.T) {
		t.Helper()
		withConfig(t, loneProfile)
		prev := gf
		gf = globalFlags{}
		t.Cleanup(func() { gf = prev })
	}

	t.Run("used, and warned about", func(t *testing.T) {
		setup(t)
		srv := helloServer(t)
		t.Setenv("NAMECOM_BASE_URL", srv.URL)
		cmd, _ := jsonCmd(t)
		if err := initContext(cmd); err != nil {
			t.Fatalf("initContext: %v", err)
		}
		if got := cmdutil.APIClient(cmd).BaseURL(); got != srv.URL {
			t.Errorf("client base URL = %q, want NAMECOM_BASE_URL's %q", got, srv.URL)
		}
		// In JSON mode the warning is kept for the end of the command (#240).
		if w := strings.Join(cmdutil.Out(cmd).TakeWarnings(), "\n"); !strings.Contains(w, "NAMECOM_BASE_URL is set") {
			t.Errorf("no warning naming NAMECOM_BASE_URL:\n%s", w)
		}
	})

	t.Run("the flag wins", func(t *testing.T) {
		setup(t)
		t.Setenv("NAMECOM_BASE_URL", "http://env.invalid")
		gf.baseURL = "http://127.0.0.1:1"
		cmd, _ := jsonCmd(t)
		if err := initContext(cmd); err != nil {
			t.Fatalf("initContext: %v", err)
		}
		if got := cmdutil.APIClient(cmd).BaseURL(); got != "http://127.0.0.1:1" {
			t.Errorf("client base URL = %q, want the flag's", got)
		}
	})

	t.Run("an invalid value is a usage error naming the variable", func(t *testing.T) {
		setup(t)
		t.Setenv("NAMECOM_BASE_URL", "api.name.com")
		cmd, _ := jsonCmd(t)
		err := initContext(cmd)
		if exitCode(err) != 2 || !strings.Contains(err.Error(), "NAMECOM_BASE_URL") {
			t.Errorf("got %v (exit %d), want a usage error naming NAMECOM_BASE_URL", err, exitCode(err))
		}
	})
}
