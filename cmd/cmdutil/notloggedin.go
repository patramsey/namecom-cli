package cmdutil

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/patramsey/namecom-cli/internal/config"
	"github.com/patramsey/namecom-cli/internal/output"
)

// NotLoggedInError is what every command reports when no credentials are
// configured anywhere. It names the config file it looked in — the README
// sends people to `auth status` for that path, and macOS's Application
// Support location is the known trap — and unwraps to
// config.ErrNoCredentials.
type NotLoggedInError struct{ Path string }

func (e *NotLoggedInError) Error() string {
	if e.Path == "" {
		return "Not logged in."
	}
	return "Not logged in. Looked in " + e.Path + "."
}

func (e *NotLoggedInError) Unwrap() error { return config.ErrNoCredentials }

// NotLoggedIn returns the exit-3 error for having no usable credentials. Its
// hint names only the fix: the generic auth hint also suggested 'namecom auth
// status', which on `auth status` pointed at the command just run (#239).
//
// When half of the pair came from a flag or the environment, the hint names
// the other half. Sending a CI job that set NAMECOM_TOKEN but not
// NAMECOM_USERNAME to `auth login` was no help (#246).
func NotLoggedIn(ov config.Overrides) error {
	path, _ := config.ActivePath()
	hint := "run 'namecom auth login' to set up credentials"
	if !output.IsInteractive() {
		hint = "set NAMECOM_USERNAME and NAMECOM_TOKEN, or run 'namecom auth login' in a terminal"
	}
	switch {
	case ov.Token != "":
		hint = "--token is set but no username is: pass --username as well"
	case os.Getenv("NAMECOM_TOKEN") != "":
		hint = "NAMECOM_TOKEN is set but no username is: set NAMECOM_USERNAME as well"
	case ov.Username != "":
		hint = "--username is set but no token is: set NAMECOM_TOKEN as well"
	case os.Getenv("NAMECOM_USERNAME") != "":
		hint = "NAMECOM_USERNAME is set but no token is: set NAMECOM_TOKEN as well"
	}
	return &AuthError{Err: &NotLoggedInError{Path: path}, Hint: hint}
}

// RequireProfile fails when --profile or NAMECOM_PROFILE names a profile the
// config file does not have. A missing one leaves no usable credentials, so it
// is an auth error (exit 3), like every other way of having none (#210).
// API commands and config show both check it, so they fail alike.
func RequireProfile(f *config.File, ov config.Overrides) error {
	name := ov.Profile
	if name == "" {
		name = os.Getenv("NAMECOM_PROFILE")
	}
	if name == "" || f == nil {
		return nil
	}
	if _, ok := f.Profiles[name]; ok {
		return nil
	}
	cfgPath, _ := config.ActivePath()
	names := make([]string, 0, len(f.Profiles))
	for k := range f.Profiles {
		names = append(names, k)
	}
	sort.Strings(names)
	available := "no profiles configured"
	if len(names) > 0 {
		available = "available: " + strings.Join(names, ", ")
	}
	return NewAuthErrorHint(fmt.Errorf("profile %q not found in %s (%s)", name, cfgPath, available),
		fmt.Sprintf("run 'namecom auth login --profile %s' to create it", name))
}

// CredentialsError classifies an error from config.Resolve,
// config.Identity or config.CheckComplete: a malformed NAMECOM_SANDBOX is how
// the command was invoked (exit 2, #225), and missing credentials are an auth
// problem (exit 3). It returns nil for anything else, which the caller
// handles.
func CredentialsError(err error, ov config.Overrides) error {
	if _, isEnv := errors.AsType[*config.EnvError](err); isEnv {
		return NewUsageError(err)
	}
	if !errors.Is(err, config.ErrNoCredentials) {
		return nil
	}
	// Resolve adds context to this error when it can say something more
	// specific than "nothing is configured" — several profiles exist but
	// none is the default, say. Substituting the generic text threw that
	// away and pointed the user at `auth login`, which overwrites.
	//nolint:errorlint // identity, not chain: the bare sentinel means
	// Resolve had nothing to add, so the friendlier text below applies.
	if err != config.ErrNoCredentials {
		return NewAuthErrorHint(err, "") // its message says what to do
	}
	return NotLoggedIn(ov)
}
