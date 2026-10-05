package cmdutil

import (
	"errors"
	"strings"
	"testing"

	"github.com/patramsey/namecom-cli/internal/output"
)

// yes=true must short-circuit before SandboxTag/IsInteractive are consulted,
// so this is safe to run regardless of the test environment's stdin.
func TestConfirm_YesSkipsPrompt(t *testing.T) {
	out := &output.Config{Color: output.ColorNever, Sandbox: true}
	ok, err := Confirm(out, true, "Delete acme.io?", "")
	if err != nil {
		t.Fatalf("Confirm(yes=true) returned error: %v", err)
	}
	if !ok {
		t.Error("Confirm(yes=true) = false, want true")
	}
}

// The non-interactive error path only triggers when stdin isn't a TTY (e.g.
// in CI). Skip rather than block on a real prompt when run interactively.
func TestConfirm_NonInteractiveError_SandboxTag(t *testing.T) {
	if output.IsInteractive() {
		t.Skip("stdin is a TTY in this environment — would block on a prompt")
	}
	out := &output.Config{Color: output.ColorNever, Sandbox: true}
	_, err := Confirm(out, false, "Delete acme.io?", "")
	if err == nil {
		t.Fatal("Confirm(yes=false) in non-interactive mode = nil error, want error")
	}
	if !strings.Contains(err.Error(), "[sandbox]") {
		t.Errorf("error = %q, want it to contain '[sandbox]'", err.Error())
	}
	if !strings.Contains(err.Error(), "pass --yes") {
		t.Errorf("error = %q, want it to mention --yes", err.Error())
	}
}

func TestConfirm_NonInteractiveError_NoSandboxTagInProduction(t *testing.T) {
	if output.IsInteractive() {
		t.Skip("stdin is a TTY in this environment — would block on a prompt")
	}
	out := &output.Config{Color: output.ColorNever, Sandbox: false}
	_, err := Confirm(out, false, "Delete acme.io?", "")
	if err == nil {
		t.Fatal("Confirm(yes=false) in non-interactive mode = nil error, want error")
	}
	if strings.Contains(err.Error(), "[sandbox]") {
		t.Errorf("error = %q, want no '[sandbox]' tag in production", err.Error())
	}
}

// TestConfirm_NonInteractiveIsUsageError pins #236: the refusal names a
// missing flag, so it exits 2, and it is a statement about the question
// rather than the question itself.
func TestConfirm_NonInteractiveIsUsageError(t *testing.T) {
	defer output.StubInteractive(false)()
	out := &output.Config{Color: output.ColorNever}
	_, err := Confirm(out, false, "Delete acme.io?", "production · alice")
	var usage *UsageError
	if !errors.As(err, &usage) {
		t.Fatalf("error = %v (%T), want a *UsageError so it exits 2", err, err)
	}
	want := `confirmation required for "Delete acme.io?" (production · alice) — pass --yes`
	if !strings.HasPrefix(err.Error(), want) {
		t.Errorf("error = %q, want it to start %q", err, want)
	}
}

func TestRequiredFlags(t *testing.T) {
	err := RequiredFlags(true, "type", "answer")
	var usage *UsageError
	if !errors.As(err, &usage) {
		t.Fatalf("RequiredFlags = %T, want *UsageError", err)
	}
	if got, want := err.Error(), `required flag(s) "type", "answer" not set`; got != want {
		t.Errorf("message = %q, want %q", got, want)
	}
	if got, want := usage.UserHint(), "pass --type and --answer, or run in a terminal to be prompted"; got != want {
		t.Errorf("hint = %q, want %q", got, want)
	}
	if h := RequiredFlags(false, "ns").(*UsageError).UserHint(); h != "" {
		t.Errorf("a flag nothing prompts for got the terminal hint %q", h)
	}
}
