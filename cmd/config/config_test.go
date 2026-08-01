package config

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/patramsey/namecom-cli/cmd/cmdutil"
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

// TestShow_NeverLeaksToken covers the sibling command on the same config.
func TestShow_NeverLeaksToken(t *testing.T) {
	for _, format := range []output.Format{output.FormatJSON, output.FormatYAML, output.FormatTable} {
		t.Run(string(format), func(t *testing.T) {
			cmd, buf := configCmd(t, format)
			if err := runShow(cmd, nil); err != nil {
				t.Fatalf("runShow: %v", err)
			}
			got := buf.String()
			if strings.Contains(got, secretToken) {
				t.Errorf("CREDENTIAL LEAK: token appeared in %s output:\n%s", format, got)
			}
			if strings.Contains(got, "op://vault/namecom/token") {
				t.Errorf("token_cmd leaked in %s output:\n%s", format, got)
			}
		})
	}
}
