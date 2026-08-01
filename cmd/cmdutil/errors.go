package cmdutil

// Error classification for exit codes.
//
// The documented table is:
//
//	0 success, 1 API/runtime, 2 usage, 3 auth, 4 not-found, 5 rate-limited
//
// Codes 4 and 5 (and 3 for an API 401/403) fall out of *api.APIError. But
// anything that fails BEFORE a request — a malformed flag, a missing argument,
// no credentials at all — carried no classification, so every one of those
// collapsed to exit 1. These wrappers let those paths say what kind of failure
// they are without the top level having to pattern-match error strings.

// UsageError marks a problem with how the command was invoked: an unknown flag,
// a bad argument count, an unparseable flag value. Maps to exit code 2.
type UsageError struct{ Err error }

func (e *UsageError) Error() string { return e.Err.Error() }
func (e *UsageError) Unwrap() error { return e.Err }

// NewUsageError wraps err as a usage problem. Returns nil for a nil err so it
// is safe to apply to a function result directly.
func NewUsageError(err error) error {
	if err == nil {
		return nil
	}
	return &UsageError{Err: err}
}

// AuthError marks a credential problem: none configured, or a credential helper
// that failed. Maps to exit code 3, which also triggers the top-level hint
// pointing at 'namecom auth login'.
type AuthError struct{ Err error }

func (e *AuthError) Error() string { return e.Err.Error() }
func (e *AuthError) Unwrap() error { return e.Err }

// NewAuthError wraps err as a credential problem. Returns nil for a nil err.
func NewAuthError(err error) error {
	if err == nil {
		return nil
	}
	return &AuthError{Err: err}
}
