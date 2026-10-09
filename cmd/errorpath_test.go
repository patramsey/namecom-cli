package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

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
				// Table mode prints the hint as a "→" line (#238), the
				// structured modes as error.hint, and — deprecated, for one
				// release — again as a top-level "hint" key (#240).
				marker, want := "hint", 2
				if f == output.FormatTable {
					marker, want = "→ ", 1
				}
				if n := strings.Count(ew.String(), marker); n != want {
					t.Errorf("want the hint %d time(s) on stderr, got %d:\n%s", want, n, ew.String())
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
// "check the domain name or ID" hint. Since #291 that advice is the hint
// rather than part of the message, so JSON carries it in error.hint.
func TestReportError_NotFoundSaysWhatToDoOnce(t *testing.T) {
	err := cmdutil.DomainNotFound(&api.APIError{StatusCode: 404, Message: "Not Found"}, "nope.com")

	var ew bytes.Buffer
	cfg := &output.Config{Format: output.FormatTable, Color: output.ColorNever, Writer: &bytes.Buffer{}, EWriter: &ew}
	if code := reportError(cfg, err); code != 4 {
		t.Errorf("table: exit code = %d, want 4", code)
	}
	if want := "✗ domain \"nope.com\" not found\n→ run 'namecom domain list' to see your domains\n"; ew.String() != want {
		t.Errorf("table: got:\n%s\nwant:\n%s", ew.String(), want)
	}

	ew.Reset()
	cfg.Format = output.FormatJSON
	if code := reportError(cfg, err); code != 4 {
		t.Errorf("json: exit code = %d, want 4", code)
	}
	var doc struct {
		Error struct{ Message, Hint string }
	}
	if jerr := json.Unmarshal(ew.Bytes(), &doc); jerr != nil {
		t.Fatalf("json: %v\n%s", jerr, ew.String())
	}
	if doc.Error.Message != `domain "nope.com" not found` || doc.Error.Hint != "run 'namecom domain list' to see your domains" {
		t.Errorf("json: want the message and hint apart, got:\n%s", ew.String())
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
		// #324: the API's message says what is wrong; the account settings
		// are not it.
		{"403 expired domain", &api.APIError{StatusCode: 403, Message: "Permission denied. The domain is expired."}, false, "The domain is expired.", "API settings"},
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

// TestReportError_TimeoutNamesTheBudget pins #234: a timeout printed Go's
// "context deadline exceeded (Client.Timeout exceeded …)" with no hint.
func TestReportError_TimeoutNamesTheBudget(t *testing.T) {
	prevGF := gf
	t.Cleanup(func() { gf = prevGF })
	gf.timeout = 3 * time.Second

	var ew bytes.Buffer
	cfg := &output.Config{Format: output.FormatTable, Color: output.ColorNever, Writer: &bytes.Buffer{}, EWriter: &ew}
	err := fmt.Errorf("getting domain: %w", &url.Error{Op: "Get", URL: "https://api.name.com/core/v1/domains/x.com", Err: context.DeadlineExceeded})
	if code := reportError(cfg, err); code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	want := "✗ getting domain: request to api.name.com timed out after 3s\n→ raise --timeout"
	if !strings.HasPrefix(ew.String(), want) {
		t.Errorf("got:\n%s\nwant it to start with:\n%s", ew.String(), want)
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
// regardless of -o. (`domain get` now takes several domains, #244, so the
// test uses `domain contacts get`, which still takes one.)
func TestErrorOutput_ArgCountHonoursOutputFlag(t *testing.T) {
	for _, f := range []output.Format{output.FormatTable, output.FormatJSON, output.FormatYAML} {
		t.Run(string(f), func(t *testing.T) {
			prevGF, prevOut := gf, resolvedOut
			t.Cleanup(func() { gf, resolvedOut = prevGF, prevOut; rootCmd.SetArgs(nil) })
			resolvedOut = nil

			rootCmd.SetArgs([]string{"domain", "contacts", "get", "a", "b", "-o", string(f)})
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

// TestREADME_ErrorExample pins #314: the README's error envelope showed
// "Not Found" and "check the name or ID for typos", the wording 0.5.2 (#291)
// replaced. The example is what `domain get example.com -o json` prints for
// a domain not in the account.
func TestREADME_ErrorExample(t *testing.T) {
	withConfig(t, loneProfile)
	readme, err := os.ReadFile("../README.md")
	if err != nil {
		t.Fatal(err)
	}
	// A Windows checkout has CRLF line endings; compare as LF.
	readme = bytes.ReplaceAll(readme, []byte("\r\n"), []byte("\n"))
	_, after, ok := strings.Cut(string(readme), "- **Errors** are one document on stderr:")
	if !ok {
		t.Fatal("the README's Errors section moved")
	}
	_, after, _ = strings.Cut(after, "```json\n")
	block, _, _ := strings.Cut(after, "```")
	var want map[string]any
	if err := json.Unmarshal([]byte(block), &want); err != nil {
		t.Fatalf("README example is not JSON: %v\n%s", err, block)
	}

	resetFlags(t, []string{"domain", "get"})
	srv, _ := apiStub(t, map[string]reply{"GET /core/v1/domains/example.com": {404, `{"message":"Not Found"}`}})
	_, stderr, code := runContract(t, "--base-url", srv.URL, "domain", "get", "example.com", "-o", "json")
	if code != 4 {
		t.Errorf("exit %d, want 4", code)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(stderr), &got); err != nil {
		t.Fatalf("stderr is not one document: %v\n%s", err, stderr)
	}
	// Only the error object: the stub's --base-url adds a warning.
	if g, w := fmt.Sprint(got["error"]), fmt.Sprint(want["error"]); g != w {
		t.Errorf("README example is not what the CLI prints:\n got: %s\nwant: %s", g, w)
	}
}

// TestMissingArgument_UsageInHint pins #313: a missing argument put the
// usage line in error.message ("domain is required — try: …") and left
// error.hint empty, where an extra argument or an unknown flag puts its.
func TestMissingArgument_UsageInHint(t *testing.T) {
	withConfig(t, loneProfile)
	for _, tc := range []struct {
		args          []string
		message, hint string
	}{
		{[]string{"dns", "list"}, "domain is required", "usage: namecom dns list <domain> [flags]"},
		{[]string{"dns", "delete", "example.com"}, "id is required", "usage: namecom dns delete <domain> <id> [<id>...] [flags]"},
		{[]string{"order", "get"}, "id is required", "usage: namecom order get <id> [flags]"},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			_, stderr, code := runContract(t, append(tc.args, "-o", "json")...)
			var env struct {
				Error struct{ Type, Message, Hint string } `json:"error"`
			}
			if err := json.Unmarshal([]byte(stderr), &env); err != nil {
				t.Fatalf("stderr is not the error envelope: %v\n%s", err, stderr)
			}
			if code != 2 || env.Error.Type != "usage" || env.Error.Message != tc.message || env.Error.Hint != tc.hint {
				t.Errorf("exit %d, error %+v; want exit 2, usage %q, hint %q", code, env.Error, tc.message, tc.hint)
			}
		})
	}
}

// TestErrorOutput_EarlyFailureHonoursOutputFlag pins the follow-up noted on
// #247: when cobra fails before it parses flags — an unknown top-level
// command, or an unknown flag placed before -o — -o was ignored, so
// `-o table` in a pipe still got the JSON envelope. The arguments are
// scanned for it instead.
func TestErrorOutput_EarlyFailureHonoursOutputFlag(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want output.Format
	}{
		{"unknown command", []string{"bogus", "-o", "table"}, output.FormatTable},
		{"unknown flag first", []string{"domain", "get", "--bogus", "-o", "yaml", "x"}, output.FormatYAML},
		{"--output=", []string{"bogus", "--output=table"}, output.FormatTable},
		{"-oyaml", []string{"bogus", "-oyaml"}, output.FormatYAML},
		{"last one wins", []string{"bogus", "-o", "yaml", "--output", "table"}, output.FormatTable},
		{"after -- is an argument", []string{"bogus", "--", "-o", "table"}, output.FormatJSON},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prevGF, prevOut, prevArgs := gf, resolvedOut, os.Args
			t.Cleanup(func() { gf, resolvedOut, os.Args = prevGF, prevOut, prevArgs; rootCmd.SetArgs(nil) })
			resolvedOut = nil
			gf.output = ""
			os.Args = append([]string{"namecom"}, tc.args...)

			rootCmd.SetArgs(tc.args)
			if err := rootCmd.ExecuteContext(context.Background()); err == nil {
				t.Fatal("want an early failure")
			}
			if resolvedOut != nil {
				t.Fatal("PersistentPreRunE ran; the test no longer exercises the early-failure path")
			}
			// A test binary's stdout is not a terminal, so the default is JSON.
			if got := errorOutput().Format; got != tc.want {
				t.Errorf("error rendered as %q, want %q", got, tc.want)
			}
		})
	}
}

// TestErrorOutput_FilterConflictHonoursOutputFlag pins #291: a --jq or
// --fields that cannot go with the other output flags failed in
// buildOutputConfig, and the error then fell back to the TTY default, so
// `--jq … -o table` in a pipe printed the JSON envelope. A valid -o is still
// the format the error is shown in.
func TestErrorOutput_FilterConflictHonoursOutputFlag(t *testing.T) {
	for _, args := range [][]string{
		{"domain", "list", "--jq", ".data", "-o", "table"},
		{"domain", "list", "--jq", ".data", "-o", "tsv"},
		{"domain", "list", "-q", "--fields", "domainName", "-o", "table"},
	} {
		t.Run(strings.Join(args[2:], " "), func(t *testing.T) {
			resetFlags(t, args)
			_, stderr, code := runContract(t, args...)
			if code != 2 {
				t.Errorf("exit %d, want 2", code)
			}
			if !strings.HasPrefix(stderr, "✗ ") {
				t.Errorf("want a ✗ line, as -o asked for text, got:\n%s", stderr)
			}
		})
	}
}
