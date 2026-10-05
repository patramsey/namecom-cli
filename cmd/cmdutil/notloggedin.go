package cmdutil

import (
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

// NotLoggedIn returns the exit-3 error for having no credentials at all. Its
// hint names only the fix: the generic auth hint also suggested 'namecom auth
// status', which on `auth status` pointed at the command just run (#239).
func NotLoggedIn() error {
	path, _ := config.ActivePath()
	hint := "run 'namecom auth login' to set up credentials"
	if !output.IsInteractive() {
		hint = "set NAMECOM_USERNAME and NAMECOM_TOKEN, or run 'namecom auth login' in a terminal"
	}
	return &AuthError{Err: &NotLoggedInError{Path: path}, Hint: hint}
}
