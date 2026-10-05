package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/patramsey/namecom-cli/cmd/cmdutil"
	"github.com/patramsey/namecom-cli/internal/api"
	"github.com/patramsey/namecom-cli/internal/output"
	"github.com/spf13/cobra"
)

// TestReportError_AuthHintOnStderrOnce pins issue #163. On exit 3 Execute
// printed "→ Run 'namecom auth status'…" with cfg.Hint, which writes to
// stdout, so `namecom domain get x.com > out.txt` with a bad token put the
// hint in the file. It also followed the hint the error had already printed.
func TestReportError_AuthHintOnStderrOnce(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{"no credentials", cmdutil.NewAuthError(errors.New("no credentials configured"))},
		{"api 401", &api.APIError{StatusCode: 401, Message: "Unauthorized"}},
	}
	for _, tc := range tests {
		for _, f := range []output.Format{output.FormatTable, output.FormatJSON, output.FormatYAML} {
			t.Run(tc.name+" "+string(f), func(t *testing.T) {
				var w, ew bytes.Buffer
				cfg := &output.Config{Format: f, Color: output.ColorNever, Writer: &w, EWriter: &ew}
				if code := reportError(cfg, tc.err); code != 3 {
					t.Errorf("exit code = %d, want 3", code)
				}
				if w.Len() != 0 {
					t.Errorf("an error must write nothing to stdout, got:\n%s", w.String())
				}
				if n := strings.Count(ew.String(), "hint"); n != 1 {
					t.Errorf("want exactly one hint on stderr, got %d:\n%s", n, ew.String())
				}
				if !strings.Contains(ew.String(), "namecom auth") {
					t.Errorf("hint should point at the auth commands, got:\n%s", ew.String())
				}
			})
		}
	}
}

// TestReportError_RestrictedHasNoAuthAdvice keeps #161 fixed now that hints
// come from the error itself: a RestrictedError is a 403 meaning the account
// is not enrolled in a gated program, so it keeps exit 3 but must not tell the
// user to check or reconfigure credentials that are fine.
func TestReportError_RestrictedHasNoAuthAdvice(t *testing.T) {
	forbidden := &api.APIError{StatusCode: 403, Message: "Permission Denied"}
	for _, err := range []error{
		cmdutil.AsRestricted(forbidden, "internal transfer-in", "approved enterprise reseller"),
		fmt.Errorf("ctx: %w", cmdutil.AsRestricted(forbidden, "contact verify", "approved reseller")),
	} {
		for _, f := range []output.Format{output.FormatTable, output.FormatJSON, output.FormatYAML} {
			t.Run(err.Error()+" "+string(f), func(t *testing.T) {
				var w, ew bytes.Buffer
				cfg := &output.Config{Format: f, Color: output.ColorNever, Writer: &w, EWriter: &ew}
				if code := reportError(cfg, err); code != 3 {
					t.Errorf("exit code = %d, want 3", code)
				}
				if w.Len() != 0 {
					t.Errorf("an error must write nothing to stdout, got:\n%s", w.String())
				}
				for _, bad := range []string{"auth login", "auth status", "check your credentials"} {
					if strings.Contains(ew.String(), bad) {
						t.Errorf("restricted 403 should not suggest %q, got:\n%s", bad, ew.String())
					}
				}
			})
		}
	}
}

// TestReportError_NotFoundSaysWhatToDoOnce pins #234: `domain get nope.com`
// printed its own "run 'namecom domain list'" and then the 404's generic
// "check the domain name or ID" hint.
func TestReportError_NotFoundSaysWhatToDoOnce(t *testing.T) {
	err := cmdutil.NotFound(&api.APIError{StatusCode: 404, Message: "Not Found"},
		`domain "nope.com" not found — run 'namecom domain list' to see your domains`)
	for _, f := range []output.Format{output.FormatTable, output.FormatJSON} {
		var ew bytes.Buffer
		cfg := &output.Config{Format: f, Color: output.ColorNever, Writer: &bytes.Buffer{}, EWriter: &ew}
		if code := reportError(cfg, err); code != 4 {
			t.Errorf("%s: exit code = %d, want 4", f, code)
		}
		if strings.Contains(ew.String(), "hint") {
			t.Errorf("%s: the message already says what to do; want no hint, got:\n%s", f, ew.String())
		}
	}
}

// TestReportError_HintsMatchTheStatus pins #234: a 403 ("IP not whitelisted")
// was told to log in again, which new credentials cannot fix; a 401 mentioned
// the sandbox's separate token in production; and a read that got a non-JSON
// 200 was warned that "a change may have been made".
func TestReportError_HintsMatchTheStatus(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		sandbox   bool
		want, not string
	}{
		{"403", &api.APIError{StatusCode: 403, Message: "Permission Denied", Details: "IP not whitelisted"}, false, "IP address", "auth login"},
		{"401 production", &api.APIError{StatusCode: 401, Message: "Unauthorized"}, false, "auth login", "sandbox"},
		{"401 sandbox", &api.APIError{StatusCode: 401, Message: "Unauthorized"}, true, "sandbox uses a separate API token", ""},
		{"undecodable read", api.NormalizeError(&json.SyntaxError{}), false, "--debug", "change"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var ew bytes.Buffer
			cfg := &output.Config{Format: output.FormatTable, Color: output.ColorNever, Writer: &bytes.Buffer{}, EWriter: &ew, Sandbox: tt.sandbox}
			reportError(cfg, tt.err)
			if !strings.Contains(ew.String(), tt.want) {
				t.Errorf("want %q in:\n%s", tt.want, ew.String())
			}
			if tt.not != "" && strings.Contains(ew.String(), tt.not) {
				t.Errorf("must not mention %q:\n%s", tt.not, ew.String())
			}
		})
	}
}

// TestCredentialErrors_OneLineOneHint pins #234's credential half. With no
// credentials the message and the hint both said "run 'namecom auth login'",
// and the hint offered `auth status` even to `auth status` itself. A missing
// profile spread over four lines with the advice in the middle.
func TestCredentialErrors_OneLineOneHint(t *testing.T) {
	tests := []struct {
		name, config, profile string
		authStatus            bool
		wantHint              string // "" means no hint line at all
		notWant               string
	}{
		{name: "nothing configured", wantHint: "auth login"},
		{name: "missing profile", config: loneProfile, profile: "nope", wantHint: "auth login --profile nope"},
		{name: "several profiles, no default", config: "profiles:\n  a:\n    username: u\n    token: t\n  b:\n    username: u\n    token: t\n"},
		{name: "failing token_cmd in auth status", config: "profiles:\n  work:\n    username: u\n    token_cmd: exit 1\n",
			authStatus: true, wantHint: "token_cmd", notWant: "auth status"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			withConfig(t, tt.config)
			prevGF := gf
			t.Cleanup(func() { gf = prevGF })
			gf = globalFlags{profile: tt.profile}

			cmd := &cobra.Command{Use: "list"}
			if tt.authStatus {
				cmd = &cobra.Command{Use: "status"}
				(&cobra.Command{Use: "auth"}).AddCommand(cmd)
			}
			out := &output.Config{Format: output.FormatTable, Color: output.ColorNever, Writer: &bytes.Buffer{}, EWriter: &bytes.Buffer{}}
			cmd.SetContext(context.WithValue(context.Background(), cmdutil.KeyOutput, out))

			err := initContext(cmd)
			if err == nil {
				t.Fatal("initContext succeeded, want a credential error")
			}
			var ew bytes.Buffer
			out.EWriter = &ew
			if code := reportError(out, err); code != 3 {
				t.Errorf("exit code = %d, want 3", code)
			}
			lines := strings.Split(strings.TrimRight(ew.String(), "\n"), "\n")
			wantLines := 1
			if tt.wantHint != "" {
				wantLines = 2
			}
			if len(lines) != wantLines {
				t.Fatalf("want %d lines (message, then any hint), got %d:\n%s", wantLines, len(lines), ew.String())
			}
			if tt.wantHint != "" && !strings.Contains(lines[1], tt.wantHint) {
				t.Errorf("hint %q should mention %q", lines[1], tt.wantHint)
			}
			if strings.Count(ew.String(), "auth login") > 1 {
				t.Errorf("'auth login' is suggested more than once:\n%s", ew.String())
			}
			if tt.notWant != "" && strings.Contains(ew.String(), tt.notWant) {
				t.Errorf("should not mention %q:\n%s", tt.notWant, ew.String())
			}
		})
	}
}

// TestErrorOutput_ArgCountHonoursOutputFlag pins issue #164. Cobra checks the
// argument count before PersistentPreRunE, so resolvedOut was still nil and
// `namecom domain get a b -o table` printed the JSON envelope in a pipe
// regardless of -o.
func TestErrorOutput_ArgCountHonoursOutputFlag(t *testing.T) {
	for _, f := range []output.Format{output.FormatTable, output.FormatJSON, output.FormatYAML} {
		t.Run(string(f), func(t *testing.T) {
			prevGF, prevOut := gf, resolvedOut
			t.Cleanup(func() { gf, resolvedOut = prevGF, prevOut; rootCmd.SetArgs(nil) })
			resolvedOut = nil

			rootCmd.SetArgs([]string{"domain", "get", "a", "b", "-o", string(f)})
			err := cmdutil.ClassifyCobraUsage(rootCmd.ExecuteContext(context.Background()))
			if err == nil || !strings.Contains(err.Error(), "too many arguments") {
				t.Fatalf("want an argument-count error, got %v", err)
			}
			if resolvedOut != nil {
				t.Fatal("PersistentPreRunE ran; the test no longer exercises the early-failure path")
			}
			if got := errorOutput().Format; got != f {
				t.Errorf("error rendered as %q, want %q from -o", got, f)
			}
		})
	}
}
