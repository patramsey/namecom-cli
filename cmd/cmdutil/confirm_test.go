package cmdutil

import (
	"strings"
	"testing"

	"github.com/patramsey/namecom-cli/internal/output"
)

// yes=true must short-circuit before SandboxTag/IsInteractive are consulted,
// so this is safe to run regardless of the test environment's stdin.
func TestConfirm_YesSkipsPrompt(t *testing.T) {
	out := &output.Config{Color: output.ColorNever, Sandbox: true}
	ok, err := Confirm(out, true, "Delete acme.io?")
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
	_, err := Confirm(out, false, "Delete acme.io?")
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
	_, err := Confirm(out, false, "Delete acme.io?")
	if err == nil {
		t.Fatal("Confirm(yes=false) in non-interactive mode = nil error, want error")
	}
	if strings.Contains(err.Error(), "[sandbox]") {
		t.Errorf("error = %q, want no '[sandbox]' tag in production", err.Error())
	}
}
