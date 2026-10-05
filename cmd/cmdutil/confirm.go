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

// ErrAborted is the error for a declined confirmation or a form cancelled
// with Ctrl-C or Esc. It exits 1, so a script can tell a declined delete from
// a completed one.
//
// A decline used to print "! aborted" and exit 0, while Ctrl-C in some forms
// printed "✗ aborted" and exited 1 (#236). Both now return this, and the error
// renderer prints it like any other failure: "✗ aborted".
var ErrAborted = errors.New("aborted")

// FormError returns ErrAborted for a form the user cancelled, and err
// otherwise. Every huh form's Run error goes through it.
func FormError(err error) error {
	if errors.Is(err, huh.ErrUserAborted) {
		return ErrAborted
	}
	return err
}

// Confirm prompts the user for a yes/no confirmation using a styled huh form.
// Returns true if confirmed, and false for "No" or a cancelled prompt; callers
// turn false into ErrAborted. If yes is true, skips the prompt entirely.
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
	return NewUsageError(&ConfirmationRequiredError{
		msg: fmt.Sprintf("confirmation required for %s — pass --yes to confirm when not running in a terminal", what),
	})
}

// ConfirmationRequiredError is the usage error needsYes returns, typed so the
// JSON error envelope can say "confirmation_required" rather than "usage"
// (#240): a script that sees it knows --yes is the fix.
type ConfirmationRequiredError struct{ msg string }

func (e *ConfirmationRequiredError) Error() string { return e.msg }

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
//
// The environment is left out when the prompt is tagged "[sandbox]", which
// said the word a second time (#247). Under --base-url it names the URL
// instead: production or sandbox is then only where the credentials came
// from, not where the request goes.
func PromptContext(cmd *cobra.Command) string {
	return accountContext(cmd, true)
}

// AccountContext is PromptContext for text with no "[sandbox]" tag of its own
// — the dry-run "Would charge" line — so it always names the environment.
func AccountContext(cmd *cobra.Command) string {
	return accountContext(cmd, false)
}

func accountContext(cmd *cobra.Command, tagged bool) string {
	env := "production"
	switch {
	case baseURLOverride(cmd) != "":
		env = "base URL overridden: " + baseURLOverride(cmd)
	case tagged && Out(cmd).Sandbox:
		env = "" // Confirm's [sandbox] tag says it
	case IsSandbox(cmd) || Out(cmd).Sandbox:
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

	who := ""
	switch {
	case profile != "" && id.Username != "":
		who = fmt.Sprintf("profile %s (%s)", profile, id.Username)
	case profile != "":
		who = "profile " + profile
	case id.Username != "":
		who = id.Username
	}
	switch {
	case env == "":
		return who
	case who == "":
		return env
	}
	return env + " · " + who
}

// baseURLOverride returns the --base-url value, or "" when it was not set.
func baseURLOverride(cmd *cobra.Command) string {
	f := cmd.Root().PersistentFlags().Lookup("base-url")
	if f == nil {
		return ""
	}
	return f.Value.String()
}
