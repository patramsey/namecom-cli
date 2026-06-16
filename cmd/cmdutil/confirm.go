package cmdutil

import (
	"errors"
	"fmt"

	"github.com/charmbracelet/huh"
	"github.com/patramsey/namecom-cli/internal/output"
)

// Confirm prompts the user for a yes/no confirmation using a styled huh form.
// Returns true if confirmed. If yes is true, skips the prompt entirely.
// In non-interactive mode without --yes, returns a clear error.
// The prompt is tagged "[sandbox]" when out targets the sandbox API, so the
// decision point makes the environment unmistakable.
func Confirm(out *output.Config, yes bool, msg string) (bool, error) {
	msg = out.SandboxTag() + msg
	if yes {
		return true, nil
	}
	if !output.IsInteractive() {
		return false, fmt.Errorf("%s — pass --yes to confirm in non-interactive mode", msg)
	}
	var result bool
	form := huh.NewForm(
		huh.NewGroup(
			huh.NewConfirm().
				Title(msg).
				Affirmative("Yes").
				Negative("No").
				Value(&result),
		),
	)
	if err := form.Run(); err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			return false, nil
		}
		return false, err
	}
	return result, nil
}
