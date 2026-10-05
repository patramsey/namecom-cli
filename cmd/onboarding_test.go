package cmd

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/patramsey/namecom-cli/internal/config"
	"github.com/patramsey/namecom-cli/internal/output"
)

// renderedError renders err as the CLI would and returns stderr.
func renderedError(err error) string {
	var w, ew bytes.Buffer
	reportError(&output.Config{Format: output.FormatTable, Color: output.ColorNever, Writer: &w, EWriter: &ew}, err)
	return ew.String()
}

// TestNotLoggedIn_SameAnswerEverywhere guards #239: with no config, `auth
// status`, `config show` and API commands each said something different,
// `config show` exited 1 while the others exited 3, none said which file they
// had looked in, and `auth status` hinted at running `auth status`.
func TestNotLoggedIn_SameAnswerEverywhere(t *testing.T) {
	for name, run := range map[string]func() error{
		"auth status": func() error { cmd, _ := jsonCmd(t); return runAuthStatus(cmd, nil) },
		"API command": func() error { cmd, _ := jsonCmd(t); return initContext(cmd) },
		"config show": func() error { return executeRoot(t, "config", "show") },
	} {
		t.Run(name, func(t *testing.T) {
			path := withConfig(t, "")
			err := run()
			if err == nil {
				t.Fatal("succeeded with no credentials")
			}
			if got := exitCode(err); got != 3 {
				t.Errorf("exit code = %d, want 3: %v", got, err)
			}
			if want := "Not logged in. Looked in " + path + "."; err.Error() != want {
				t.Errorf("error = %q, want %q", err.Error(), want)
			}
			if !errors.Is(err, config.ErrNoCredentials) {
				t.Error("error does not unwrap to config.ErrNoCredentials")
			}
			stderr := renderedError(err)
			if strings.Contains(stderr, "auth status") {
				t.Errorf("hint points at auth status:\n%s", stderr)
			}
			if !strings.Contains(stderr, "namecom auth login") {
				t.Errorf("hint does not say how to log in:\n%s", stderr)
			}
		})
	}
}

// TestAuthStatus_ProfileErrorsDoNotPointAtItself: other credential problems
// seen by `auth status` used the generic hint, which suggests `auth status`.
func TestAuthStatus_ProfileErrorsDoNotPointAtItself(t *testing.T) {
	withConfig(t, twoProfiles)
	prev := gf
	gf = globalFlags{profile: "nope"}
	t.Cleanup(func() { gf = prev })

	cmd, _ := jsonCmd(t)
	err := runAuthStatus(cmd, nil)
	if err == nil || exitCode(err) != 3 {
		t.Fatalf("auth status --profile nope = %v, want an exit-3 error", err)
	}
	if stderr := renderedError(err); strings.Contains(stderr, "auth status") {
		t.Errorf("hint points at auth status:\n%s", stderr)
	}
}

// TestConfigShow_UnknownProfileExits3: config show with credentials it cannot
// use exits 3, as API commands do with the same config.
func TestConfigShow_UnknownProfileExits3(t *testing.T) {
	withConfig(t, "profiles:\n  a:\n    username: u\n    token: t\n  b:\n    username: v\n    token: t\n")
	if err := executeRoot(t, "config", "show"); exitCode(err) != 3 {
		t.Errorf("config show with no default profile = %v (exit %d), want exit 3", err, exitCode(err))
	}
}

// runBare executes the real root with args and returns what it printed.
func runBare(t *testing.T, args ...string) string {
	t.Helper()
	var buf bytes.Buffer
	prev := gf
	t.Cleanup(func() {
		gf = prev
		rootCmd.SetArgs(nil)
		rootCmd.SetOut(nil)
		_ = rootCmd.Flags().Set("help", "false")
	})
	rootCmd.SetOut(&buf)
	rootCmd.SetArgs(args)
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("namecom %v: %v", args, err)
	}
	return buf.String()
}

// TestBareRoot_GettingStarted guards #239: a bare `namecom` with no
// credentials printed the whole command list, none of which works yet. It now
// says how to log in, as gh does; --help, and anyone logged in, still get the
// full help.
func TestBareRoot_GettingStarted(t *testing.T) {
	t.Run("not logged in", func(t *testing.T) {
		withConfig(t, "")
		got := runBare(t)
		for _, want := range []string{"not logged in", "namecom auth login", apiSettingsURL} {
			if !strings.Contains(got, want) {
				t.Errorf("banner does not mention %q:\n%s", want, got)
			}
		}
		if strings.Contains(got, "Exit codes") {
			t.Errorf("bare namecom printed the full help, not the banner:\n%s", got)
		}
	})
	t.Run("--help still prints the help", func(t *testing.T) {
		withConfig(t, "")
		if got := runBare(t, "--help"); !strings.Contains(got, "Exit codes") || strings.Contains(got, "not logged in") {
			t.Errorf("namecom --help:\n%s", got)
		}
	})
	t.Run("logged in", func(t *testing.T) {
		withConfig(t, loneProfile)
		if got := runBare(t); !strings.Contains(got, "Exit codes") {
			t.Errorf("bare namecom while logged in did not print the help:\n%s", got)
		}
	})
	t.Run("token_cmd counts as logged in", func(t *testing.T) {
		withConfig(t, "profiles:\n  work:\n    username: u\n    token_cmd: false\n")
		if got := runBare(t); !strings.Contains(got, "Exit codes") {
			t.Errorf("bare namecom with a token_cmd profile did not print the help:\n%s", got)
		}
	})
	t.Run("env credentials count as logged in", func(t *testing.T) {
		withConfig(t, "")
		t.Setenv("NAMECOM_USERNAME", "u")
		t.Setenv("NAMECOM_TOKEN", "t")
		if got := runBare(t); !strings.Contains(got, "Exit codes") {
			t.Errorf("bare namecom with env credentials did not print the help:\n%s", got)
		}
	})
}
