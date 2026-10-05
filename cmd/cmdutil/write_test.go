package cmdutil

import (
	"bytes"
	"context"
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/patramsey/namecom-cli/internal/config"
	"github.com/patramsey/namecom-cli/internal/output"
	"github.com/spf13/cobra"
)

type testBody struct {
	Name   string `json:"name"`
	Secret string `json:"secret,omitempty"`
}

// writeCmd builds a child command under a root carrying --dry-run and --yes,
// with an output config whose streams the test can read back.
func writeCmd(t *testing.T, dryRun, yes bool) (cmd *cobra.Command, stdout, stderr *bytes.Buffer) {
	t.Helper()
	root := &cobra.Command{Use: "namecom"}
	var dr, y bool
	root.PersistentFlags().BoolVar(&dr, "dry-run", dryRun, "")
	root.PersistentFlags().BoolVar(&y, "yes", yes, "")
	cmd = &cobra.Command{Use: "child"}
	root.AddCommand(cmd)

	stdout, stderr = &bytes.Buffer{}, &bytes.Buffer{}
	out := &output.Config{Format: output.FormatTable, Color: output.ColorNever, Writer: stdout, EWriter: stderr}
	cmd.SetContext(context.WithValue(context.Background(), KeyOutput, out))
	return cmd, stdout, stderr
}

// stubConfirm replaces confirmFunc for the test's duration.
func stubConfirm(t *testing.T, f func(*output.Config, bool, string) (bool, error)) {
	t.Helper()
	prev := confirmFunc
	confirmFunc = func(out *output.Config, yes bool, msg, _ string) (bool, error) { return f(out, yes, msg) }
	t.Cleanup(func() { confirmFunc = prev })
}

func failIfConfirmed(t *testing.T) {
	t.Helper()
	stubConfirm(t, func(_ *output.Config, _ bool, msg string) (bool, error) {
		t.Errorf("confirmation was requested (%q) but must not be", msg)
		return true, nil
	})
}

func failIfSent(t *testing.T) func(context.Context, testBody) error {
	return func(context.Context, testBody) error {
		t.Error("send was called but must not be")
		return nil
	}
}

func TestRunWrite_DryRunPreviewsBodyWithoutPromptingOrSending(t *testing.T) {
	failIfConfirmed(t)
	// Interactive, so a prompt would be possible if RunWrite asked for one.
	defer output.StubInteractive(true)()
	cmd, stdout, _ := writeCmd(t, true, false)

	sent, err := RunWrite(cmd, Write[testBody]{
		Method: "POST", Path: "/core/v1/things",
		Body:   testBody{Name: "a"},
		Prompt: "Create a thing?",
		Spin:   "Creating…",
	}, failIfSent(t))
	if err != nil || sent {
		t.Fatalf("RunWrite = (%v, %v), want (false, nil)", sent, err)
	}
	got := stdout.String()
	if !strings.HasPrefix(got, "POST /core/v1/things\n") {
		t.Errorf("preview line missing, got %q", got)
	}
	if !strings.Contains(got, `"name": "a"`) {
		t.Errorf("preview does not show the body, got %q", got)
	}
}

func TestRunWrite_DryRunNoBodyPrintsOnlyTheRequestLine(t *testing.T) {
	cmd, stdout, _ := writeCmd(t, true, false)
	sent, err := RunWrite(cmd, Write[NoBody]{Method: "DELETE", Path: "/core/v1/things/1", Prompt: "Delete?"},
		func(context.Context, NoBody) error {
			t.Error("send was called under --dry-run")
			return nil
		})
	if err != nil || sent {
		t.Fatalf("RunWrite = (%v, %v), want (false, nil)", sent, err)
	}
	if got := stdout.String(); got != "DELETE /core/v1/things/1\n" {
		t.Errorf("preview = %q, want only the request line", got)
	}
}

// TestRunWrite_DryRunPreviewFailureIsReturned covers the output half of #168:
// a body the preview cannot encode (a +Inf price) must fail the command, not
// print nothing and exit 0.
func TestRunWrite_DryRunPreviewFailureIsReturned(t *testing.T) {
	failIfConfirmed(t)
	cmd, stdout, _ := writeCmd(t, true, false)
	sent, err := RunWrite(cmd, Write[map[string]float64]{
		Method: "POST", Path: "/core/v1/domains",
		Body: map[string]float64{"purchasePrice": math.Inf(1)}, Prompt: "Register?",
	}, func(context.Context, map[string]float64) error {
		t.Error("send was called under --dry-run")
		return nil
	})
	if err == nil || sent {
		t.Fatalf("RunWrite = (%v, %v), want (false, an encoding error)", sent, err)
	}
	if stdout.Len() != 0 {
		t.Errorf("a failed preview should print nothing, got %q", stdout.String())
	}
}

func TestRunWrite_PreviewRedactsButSendGetsTheBody(t *testing.T) {
	w := Write[testBody]{
		Method: "POST", Path: "/core/v1/things",
		Body: testBody{Name: "a", Secret: "hunter2"},
		Preview: func(b testBody) any {
			b.Secret = "[redacted]"
			return b
		},
	}

	cmd, stdout, _ := writeCmd(t, true, false)
	if _, err := RunWrite(cmd, w, failIfSent(t)); err != nil {
		t.Fatal(err)
	}
	if got := stdout.String(); strings.Contains(got, "hunter2") || !strings.Contains(got, "[redacted]") {
		t.Errorf("preview must redact the secret, got %q", got)
	}

	cmd, _, _ = writeCmd(t, false, false)
	var got testBody
	if _, err := RunWrite(cmd, w, func(_ context.Context, b testBody) error { got = b; return nil }); err != nil {
		t.Fatal(err)
	}
	if got.Secret != "hunter2" {
		t.Errorf("send received secret %q; redaction must apply to the preview only", got.Secret)
	}
}

func TestRunWrite_YesSendsTheBodyUnchanged(t *testing.T) {
	cmd, stdout, stderr := writeCmd(t, false, true)
	want := testBody{Name: "a", Secret: "s"}
	var got testBody
	sent, err := RunWrite(cmd, Write[testBody]{
		Method: "POST", Path: "/core/v1/things", Body: want, Prompt: "Create a thing?",
	}, func(_ context.Context, b testBody) error { got = b; return nil })
	if err != nil || !sent {
		t.Fatalf("RunWrite = (%v, %v), want (true, nil)", sent, err)
	}
	if got != want {
		t.Errorf("send received %+v, want %+v", got, want)
	}
	if stdout.Len() != 0 || stderr.Len() != 0 {
		t.Errorf("a confirmed send printed nothing itself; got stdout %q stderr %q", stdout, stderr)
	}
}

func TestRunWrite_NonInteractiveWithoutYesErrorsWithThePrompt(t *testing.T) {
	defer output.StubInteractive(false)()
	cmd, _, _ := writeCmd(t, false, false)
	sent, err := RunWrite(cmd, Write[testBody]{
		Method: "POST", Path: "/core/v1/things", Body: testBody{Name: "a"}, Prompt: "Create a thing for $5.00?",
	}, failIfSent(t))
	if sent {
		t.Error("sent = true without confirmation")
	}
	if err == nil {
		t.Fatal("expected the non-interactive confirmation error")
	}
	for _, want := range []string{"Create a thing for $5.00?", "pass --yes"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not contain %q", err, want)
		}
	}
}

func TestRunWrite_DeclineAborts(t *testing.T) {
	var asked string
	stubConfirm(t, func(_ *output.Config, yes bool, msg string) (bool, error) {
		if yes {
			t.Error("confirm was told --yes, which was not passed")
		}
		asked = msg
		return false, nil
	})
	cmd, _, stderr := writeCmd(t, false, false)
	sent, err := RunWrite(cmd, Write[testBody]{
		Method: "POST", Path: "/core/v1/things", Body: testBody{Name: "a"}, Prompt: "Create a thing?",
	}, failIfSent(t))
	if err != nil || sent {
		t.Fatalf("RunWrite = (%v, %v), want (false, nil) so the command exits 0", sent, err)
	}
	if asked != "Create a thing?" {
		t.Errorf("confirm asked %q", asked)
	}
	if !strings.Contains(stderr.String(), "aborted") {
		t.Errorf("stderr = %q, want an 'aborted' warning", stderr)
	}
}

func TestRunWrite_EmptyPromptSendsWithoutConfirming(t *testing.T) {
	failIfConfirmed(t)
	defer output.StubInteractive(false)()
	cmd, _, _ := writeCmd(t, false, false)
	called := false
	sent, err := RunWrite(cmd, Write[testBody]{Method: "PUT", Path: "/core/v1/things/1", Body: testBody{Name: "a"}},
		func(context.Context, testBody) error { called = true; return nil })
	if err != nil || !sent || !called {
		t.Fatalf("RunWrite = (%v, %v), called=%v; want a send with no confirmation", sent, err, called)
	}
}

func TestRunWrite_SendErrorIsReturnedWithSentTrue(t *testing.T) {
	cmd, _, _ := writeCmd(t, false, true)
	boom := errors.New("boom")
	sent, err := RunWrite(cmd, Write[NoBody]{Method: "DELETE", Path: "/x"},
		func(context.Context, NoBody) error { return boom })
	if !sent || !errors.Is(err, boom) {
		t.Fatalf("RunWrite = (%v, %v), want (true, boom)", sent, err)
	}
}

// contextCmd is writeCmd with a config file, overrides and sandbox setting on
// the context, as root.go stores them.
func contextCmd(t *testing.T, f *config.File, ov config.Overrides, sandbox bool) *cobra.Command {
	t.Helper()
	cmd, _, _ := writeCmd(t, false, false)
	Out(cmd).Sandbox = sandbox
	ctx := context.WithValue(cmd.Context(), KeyConfig, f)
	ctx = context.WithValue(ctx, KeyOverrides, ov)
	cmd.SetContext(ctx)
	return cmd
}

// clearCredentialEnv keeps the developer's own NAMECOM_* settings out of a
// test that resolves an identity.
func clearCredentialEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{"NAMECOM_USERNAME", "NAMECOM_TOKEN", "NAMECOM_PROFILE", "NAMECOM_SANDBOX"} {
		t.Setenv(k, "")
	}
}

// TestPromptContext covers #228: a write prompt names the environment, the
// profile and the account, so a production purchase cannot be mistaken for a
// sandbox one, or one profile's account for another's.
func TestPromptContext(t *testing.T) {
	clearCredentialEnv(t)
	two := &config.File{Default: "work", Profiles: map[string]config.Profile{
		"work":     {Username: "acme-corp", Token: "t"},
		"personal": {Username: "me", Token: "t", Sandbox: true},
	}}
	tests := []struct {
		name    string
		f       *config.File
		ov      config.Overrides
		sandbox bool
		want    string
	}{
		{"default profile", two, config.Overrides{}, false, "production · profile work (acme-corp)"},
		{"--profile", two, config.Overrides{Profile: "personal"}, true, "sandbox · profile personal (me)"},
		{"flags supply everything", two, config.Overrides{Username: "bob", Token: "x"}, false, "production · bob"},
		{"no config file", nil, config.Overrides{Username: "bob", Token: "x"}, false, "production · bob"},
		{"nothing known", nil, config.Overrides{}, true, "sandbox"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := PromptContext(contextCmd(t, tc.f, tc.ov, tc.sandbox)); got != tc.want {
				t.Errorf("PromptContext = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestRunWrite_PromptCarriesContext covers #228 end to end: the confirmation
// receives the context line, and a script without --yes sees it in the error.
func TestRunWrite_PromptCarriesContext(t *testing.T) {
	clearCredentialEnv(t)
	f := &config.File{Profiles: map[string]config.Profile{"work": {Username: "acme-corp", Token: "t"}}}
	const want = "production · profile work (acme-corp)"
	w := Write[testBody]{Method: "POST", Path: "/core/v1/things", Body: testBody{Name: "a"}, Prompt: "Create a thing?"}

	t.Run("prompt", func(t *testing.T) {
		var detail string
		prev := confirmFunc
		confirmFunc = func(_ *output.Config, _ bool, _, d string) (bool, error) { detail = d; return false, nil }
		t.Cleanup(func() { confirmFunc = prev })
		if _, err := RunWrite(contextCmd(t, f, config.Overrides{}, false), w, failIfSent(t)); err != nil {
			t.Fatal(err)
		}
		if detail != want {
			t.Errorf("confirm detail = %q, want %q", detail, want)
		}
	})

	t.Run("non-interactive refusal", func(t *testing.T) {
		defer output.StubInteractive(false)()
		_, err := RunWrite(contextCmd(t, f, config.Overrides{}, false), w, failIfSent(t))
		if err == nil || !strings.Contains(err.Error(), want) || !strings.Contains(err.Error(), "pass --yes") {
			t.Errorf("error = %v, want it to carry %q and name --yes", err, want)
		}
	})
}
