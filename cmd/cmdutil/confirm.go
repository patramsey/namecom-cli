package cmdutil

import (
	"errors"
	"fmt"
	"os"

	"github.com/charmbracelet/huh"
	"github.com/patramsey/namecom-cli/internal/config"
	"github.com/patramsey/namecom-cli/internal/output"
	"github.com/spf13/cobra"
)

// Confirm prompts the user for a yes/no confirmation using a styled huh form.
// Returns true if confirmed. If yes is true, skips the prompt entirely.
// In non-interactive mode without --yes, returns a clear error.
// The prompt is tagged "[sandbox]" when out targets the sandbox API, so the
// decision point makes the environment unmistakable.
//
// detail, when not empty, is shown as one dim line under the question and is
// repeated in the non-interactive error. RunWrite passes PromptContext: the
// environment, profile and account the write acts on.
func Confirm(out *output.Config, yes bool, msg, detail string) (bool, error) {
	msg = out.SandboxTag() + msg
	if yes {
		return true, nil
	}
	if !output.IsInteractive() {
		if detail != "" {
			return false, fmt.Errorf("%s [%s] — pass --yes to confirm in non-interactive mode", msg, detail)
		}
		return false, fmt.Errorf("%s — pass --yes to confirm in non-interactive mode", msg)
	}
	var result bool
	field := huh.NewConfirm().
		Title(msg).
		Affirmative("Yes").
		Negative("No").
		Value(&result)
	if detail != "" {
		field = field.Description(detail)
	}
	form := huh.NewForm(huh.NewGroup(field))
	if err := form.Run(); err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			return false, nil
		}
		return false, err
	}
	return result, nil
}

// PromptContext describes who a write acts as, for the line under its
// confirmation: "production · profile work (acme-corp)". A production
// purchase used to read only "Register x?", and with several profiles nothing
// said which account would pay (#228).
//
// It is built from what root.go stored on the context, through the
// config.Identity that Resolve itself uses, so it names the credentials the
// request is really sent with. The profile is left out when it is not in the
// config file, or when flags or the environment supply both the username and
// the token, since the profile then supplied neither.
func PromptContext(cmd *cobra.Command) string {
	env := "production"
	if IsSandbox(cmd) {
		env = "sandbox"
	}
	f, _ := cmd.Context().Value(KeyConfig).(*config.File)
	ov := Overrides(cmd)
	id := config.Identity(f, ov)

	profile := id.Profile
	if f == nil {
		profile = ""
	} else if _, ok := f.Profiles[profile]; !ok {
		profile = ""
	}
	userSet := ov.Username != "" || os.Getenv("NAMECOM_USERNAME") != ""
	tokenSet := ov.Token != "" || os.Getenv("NAMECOM_TOKEN") != ""
	if userSet && tokenSet {
		profile = ""
	}

	switch {
	case profile != "" && id.Username != "":
		return fmt.Sprintf("%s · profile %s (%s)", env, profile, id.Username)
	case profile != "":
		return fmt.Sprintf("%s · profile %s", env, profile)
	case id.Username != "":
		return fmt.Sprintf("%s · %s", env, id.Username)
	default:
		return env
	}
}
