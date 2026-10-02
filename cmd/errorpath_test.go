package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/patramsey/namecom-cli/cmd/cmdutil"
	"github.com/patramsey/namecom-cli/internal/api"
	"github.com/patramsey/namecom-cli/internal/output"
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
