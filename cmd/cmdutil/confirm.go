package cmdutil

import (
	"errors"
	"fmt"
	"os"
	"strings"

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
		return false, needsYes(msg, detail)
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

// needsYes is the refusal when a confirmation cannot be asked: no terminal
// and no --yes. It is a usage error (exit 2), since what is missing is a flag.
// It used to exit 1 like an API failure, and it read as a question put to
// nobody — "Delete x? — pass --yes …" (#236). The question is still quoted
// whole, since it is what --yes would approve: a price, a year count.
func needsYes(msg, detail string) error {
	what := `"` + msg + `"`
	if detail != "" {
		what += " (" + detail + ")"
	}
	return NewUsageError(fmt.Errorf("confirmation required for %s — pass --yes to confirm when not running in a terminal", what))
}

// RequiredFlags is the usage error (exit 2) for flags a command needs and was
// not given. prompted says a terminal would have asked for them, which the
// hint then says, so the error explains why the same command works by hand.
//
// url and email create returned a bare error here and exited 1, while
// transfer create exited 2 for the same situation (#236).
func RequiredFlags(prompted bool, names ...string) error {
	quoted := make([]string, len(names))
	flags := make([]string, len(names))
	for i, n := range names {
		quoted[i] = fmt.Sprintf("%q", n)
		flags[i] = "--" + n
	}
	err := fmt.Errorf("required flag(s) %s not set", strings.Join(quoted, ", "))
	if !prompted {
		return NewUsageError(err)
	}
	return NewUsageErrorHint(err, "pass "+joinNames(flags)+", or run in a terminal to be prompted")
}

// PromptedRequired ends the help of a flag that is required but asked for
// when the command runs in a terminal.
const PromptedRequired = "(required; prompted in a terminal)"

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
	id, err := config.Identity(f, ov)
	if err != nil {
		// An unparseable NAMECOM_SANDBOX: the command fails with a usage
		// error before any prompt, so there is no context worth showing.
		return ""
	}

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
