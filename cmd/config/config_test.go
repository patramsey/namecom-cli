package config

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/patramsey/namecom-cli/cmd/cmdutil"
	"github.com/patramsey/namecom-cli/internal/config"
	"github.com/patramsey/namecom-cli/internal/output"
	"github.com/spf13/cobra"
)

// secretToken is a distinctive value so any leak is unmistakable in output.
const secretToken = "SUPERSECRET-TOKEN-VALUE"

// configCmd writes a config file containing a real-looking token, points the
// CLI at it via NAMECOM_CONFIG, and returns a command whose output lands in the
// returned buffer.
func configCmd(t *testing.T, format output.Format) (*cobra.Command, *bytes.Buffer) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	contents := "default: prod\nprofiles:\n  prod:\n    username: alice\n    token: " + secretToken + "\n" +
		"  helper:\n    username: bob\n    token_cmd: op read op://vault/namecom/token\n"
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("writing config: %v", err)
	}
	t.Setenv("NAMECOM_CONFIG", path)
	// Neutralize ambient profile selection: runShow honours NAMECOM_PROFILE, so
	// a developer with it exported saw these tests fail against a profile the
	// fixture never defines.
	t.Setenv("NAMECOM_PROFILE", "")

	var buf bytes.Buffer
	out := &output.Config{Format: format, Color: output.ColorNever, Writer: &buf, EWriter: &bytes.Buffer{}}
	cmd := &cobra.Command{}
	cmd.SetContext(context.WithValue(context.Background(), cmdutil.KeyOutput, out))
	return cmd, &buf
}

// TestListProfiles_NeverLeaksToken is a credential-disclosure guard.
// `config list-profiles` serialized config.Profile directly, and Profile.Token
// carries no `json:"-"`. Because DefaultConfig() selects JSON whenever stdout
// is not a TTY, `namecom config list-profiles | anything` wrote every profile's
// live token to stdout — into pipes, CI logs, and shell redirects. The table
// path never showed tokens, so the leak was clearly unintended.
func TestListProfiles_NeverLeaksToken(t *testing.T) {
	for _, format := range []output.Format{output.FormatJSON, output.FormatYAML, output.FormatTable} {
		t.Run(string(format), func(t *testing.T) {
			cmd, buf := configCmd(t, format)
			if err := runListProfiles(cmd, nil); err != nil {
				t.Fatalf("runListProfiles: %v", err)
			}
			got := buf.String()
			if strings.Contains(got, secretToken) {
				t.Errorf("CREDENTIAL LEAK: token appeared in %s output:\n%s", format, got)
			}
			// token_cmd can itself embed a secret (vault paths, arguments).
			if strings.Contains(got, "op://vault/namecom/token") {
				t.Errorf("token_cmd leaked in %s output:\n%s", format, got)
			}
			// The command must still be useful: profile names have to appear.
			if !strings.Contains(got, "prod") {
				t.Errorf("profile names missing from %s output:\n%s", format, got)
			}
		})
	}
}

// TestShow_DoesNotExposeTokenCmdArguments covers a subtler leak than a raw
// token: a token_cmd's *arguments* can themselves carry a secret (an inline
// bearer token, a vault path). `config show` printed the whole command, so it
// could appear on a shared screen or in a screenshot. Naming the helper program
// answers "where does my token come from?" without echoing its arguments.
func TestShow_DoesNotExposeTokenCmdArguments(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	contents := "default: helper\nprofiles:\n  helper:\n    username: bob\n" +
		`    token_cmd: "curl -H 'Authorization: Bearer INLINE-SECRET' https://vault.example/token"` + "\n"
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("writing config: %v", err)
	}
	t.Setenv("NAMECOM_CONFIG", path)
	// Neutralize ambient profile selection: runShow honours NAMECOM_PROFILE, so
	// a developer with it exported saw these tests fail against a profile the
	// fixture never defines.
	t.Setenv("NAMECOM_PROFILE", "")

	var buf bytes.Buffer
	out := &output.Config{Format: output.FormatTable, Color: output.ColorNever, Writer: &buf, EWriter: &bytes.Buffer{}}
	cmd := &cobra.Command{}
	cmd.SetContext(context.WithValue(context.Background(), cmdutil.KeyOutput, out))

	if err := runShow(cmd, nil); err != nil {
		t.Fatalf("runShow: %v", err)
	}
	got := buf.String()
	if strings.Contains(got, "INLINE-SECRET") {
		t.Errorf("token_cmd arguments leaked a secret:\n%s", got)
	}
	if !strings.Contains(got, "token_cmd") {
		t.Errorf("output should still say the token comes from a token_cmd:\n%s", got)
	}
	if !strings.Contains(got, "curl") {
		t.Errorf("output should still name the helper program:\n%s", got)
	}
}

// showCmdFor writes a config whose DEFAULT profile carries both a literal token
// and a token_cmd, then returns a command rendering into the returned buffer.
//
// Both credentials must live on the *default* profile, because that is the only
// one runShow reads. The previous fixture put token_cmd on a second profile, so
// the assertion guarding it inspected data the command never touched.
func showCmdFor(t *testing.T, format output.Format, overrideProfile string) (*cobra.Command, *bytes.Buffer) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	contents := "default: prod\nprofiles:\n" +
		"  prod:\n    username: alice\n    token: " + secretToken + "\n" +
		`    token_cmd: "op read op://vault/namecom/token"` + "\n" +
		"  sandy:\n    username: bob\n    token: " + secretToken + "-SANDY\n    sandbox: true\n"
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("writing config: %v", err)
	}
	t.Setenv("NAMECOM_CONFIG", path)
	t.Setenv("NAMECOM_PROFILE", "")

	var buf bytes.Buffer
	out := &output.Config{Format: format, Color: output.ColorNever, Writer: &buf, EWriter: &bytes.Buffer{}}
	cmd := &cobra.Command{}
	ctx := context.WithValue(context.Background(), cmdutil.KeyOutput, out)
	if overrideProfile != "" {
		// The --profile flag reaches commands as resolved Overrides on the
		// context, which is the path root.go populates.
		ctx = context.WithValue(ctx, cmdutil.KeyOverrides, config.Overrides{Profile: overrideProfile})
	}
	cmd.SetContext(ctx)
	return cmd, &buf
}

// TestShow_NeverLeaksToken guards `config show` against disclosing the
// credential of the profile it is describing.
//
// The earlier version could not meaningfully fail: its fixture's default
// profile had no token_cmd, so the assertion guarding token_cmd inspected a
// profile runShow never reads. Here the default profile carries BOTH a literal
// token and a token_cmd, so both leak paths are live in every output format.
func TestShow_NeverLeaksToken(t *testing.T) {
	for _, format := range []output.Format{output.FormatJSON, output.FormatYAML, output.FormatTable} {
		t.Run(string(format), func(t *testing.T) {
			cmd, buf := showCmdFor(t, format, "")
			if err := runShow(cmd, nil); err != nil {
				t.Fatalf("runShow: %v", err)
			}
			got := buf.String()

			if strings.Contains(got, secretToken) {
				t.Errorf("CREDENTIAL LEAK: the token appeared in %s output:\n%s", format, got)
			}
			// A token_cmd's arguments can themselves be a secret (a vault path,
			// an inline bearer token), so the full command must not be echoed.
			if strings.Contains(got, "op://vault/namecom/token") {
				t.Errorf("token_cmd arguments leaked in %s output:\n%s", format, got)
			}
			// The command must still be useful — it has to say WHICH profile it
			// is describing, or the redaction has cost all the value.
			if !strings.Contains(got, "prod") {
				t.Errorf("%s output should still identify the profile:\n%s", format, got)
			}
		})
	}
}

// TestShow_HonorsProfileSelection guards a wrong-answer bug: runShow read
// cfgFile.Default only, so `config show --profile sandbox` — the command's own
// documented example — reported the production profile's endpoint.
//
// The earlier version only set NAMECOM_PROFILE and never seeded the Overrides
// the --profile flag actually travels on, so the flag branch it was named for
// was untested; blanking that branch left it green.
func TestShow_HonorsProfileSelection(t *testing.T) {
	t.Run("--profile flag", func(t *testing.T) {
		cmd, buf := showCmdFor(t, output.FormatJSON, "sandy")
		if err := runShow(cmd, nil); err != nil {
			t.Fatalf("runShow: %v", err)
		}
		assertDescribesSandy(t, buf.String())
	})

	t.Run("NAMECOM_PROFILE env var", func(t *testing.T) {
		cmd, buf := showCmdFor(t, output.FormatJSON, "")
		t.Setenv("NAMECOM_PROFILE", "sandy")
		if err := runShow(cmd, nil); err != nil {
			t.Fatalf("runShow: %v", err)
		}
		assertDescribesSandy(t, buf.String())
	})

	t.Run("flag beats env", func(t *testing.T) {
		// Documented precedence: flag > env > profile > default.
		cmd, buf := showCmdFor(t, output.FormatJSON, "sandy")
		t.Setenv("NAMECOM_PROFILE", "prod")
		if err := runShow(cmd, nil); err != nil {
			t.Fatalf("runShow: %v", err)
		}
		assertDescribesSandy(t, buf.String())
	})

	t.Run("falls back to the file default", func(t *testing.T) {
		cmd, buf := showCmdFor(t, output.FormatJSON, "")
		if err := runShow(cmd, nil); err != nil {
			t.Fatalf("runShow: %v", err)
		}
		got := buf.String()
		if !strings.Contains(got, "prod") || !strings.Contains(got, "api.name.com") {
			t.Errorf("with no selection the file default should be described, got: %s", got)
		}
	})
}

// assertDescribesSandy checks the output describes the sandbox profile — both
// its name and its endpoint, since reporting the wrong endpoint is the concrete
// harm (a user believing they are pointed at sandbox when they are not).
func assertDescribesSandy(t *testing.T, got string) {
	t.Helper()
	if !strings.Contains(got, "sandy") {
		t.Errorf("expected the selected profile 'sandy', got: %s", got)
	}
	if !strings.Contains(got, "api.dev.name.com") {
		t.Errorf("expected the sandbox endpoint for a sandbox profile, got: %s", got)
	}
	if strings.Contains(got, "api.name.com\"") {
		t.Errorf("reported the production endpoint for a sandbox profile: %s", got)
	}
}
