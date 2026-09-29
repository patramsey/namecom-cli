package cmdutil

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

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
	confirmFunc = f
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
