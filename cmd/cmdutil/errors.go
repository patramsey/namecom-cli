package cmdutil

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/patramsey/namecom-cli/internal/api"
)

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

// RestrictedError wraps a 403 from an operation the API gates behind account
// approval — reseller or enterprise programs the average account is not in.
//
// These operations are exposed because resellers use this CLI too, and hiding
// them would leave that audience without tooling. But a bare 403 sends everyone
// else to the generic "check your credentials" hint, which is the wrong
// diagnosis: the credentials are fine, the account simply is not enrolled.
// Wrapping preserves the auth exit code while replacing the message.
type RestrictedError struct {
	Err error
	// Program names the approval required, e.g. "approved reseller".
	Program string
	// Operation is the human name of what was attempted.
	Operation string
}

func (e *RestrictedError) Error() string {
	return fmt.Sprintf("%s requires an %s account — contact name.com support to request access (%v)",
		e.Operation, e.Program, e.Err)
}

func (e *RestrictedError) Unwrap() error { return e.Err }

// AsRestricted converts a 403 into a RestrictedError explaining the gate.
// Any other error is returned unchanged, so genuine auth failures still read as
// auth failures.
func AsRestricted(err error, operation, program string) error {
	if err == nil {
		return nil
	}
	var apiErr *api.APIError
	if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusForbidden {
		return &RestrictedError{Err: err, Program: program, Operation: operation}
	}
	return err
}

// cobraUsagePrefixes are the messages cobra produces for invocation mistakes it
// validates itself, after our own hooks have run.
//
// SetFlagErrorFunc in root.go covers flag *parsing*, but cobra checks required
// flags and flag groups later, inside execute(), and offers no hook for that
// path — a missing required flag surfaced as a bare error and exited 1 while
// `--badflag` right next to it exited 2. Matching the message is unpleasant but
// it is the only seam cobra exposes; the strings are stable and asserted by
// TestClassifyCobraUsage, which fails loudly if an upgrade rewords one.
var cobraUsagePrefixes = []string{
	"required flag(s) ",
	"if any flags in the group ",
	"unknown command ",
	"unknown flag: ",
	"unknown shorthand flag: ",
	"invalid argument ",
	"flag needs an argument",
}

// ClassifyCobraUsage wraps cobra's own invocation errors as UsageError so they
// reach the documented exit code 2. Errors that are already classified, and
// errors from anywhere else, pass through untouched.
func ClassifyCobraUsage(err error) error {
	if err == nil {
		return nil
	}
	var usage *UsageError
	var auth *AuthError
	if errors.As(err, &usage) || errors.As(err, &auth) {
		return err
	}
	msg := err.Error()
	for _, p := range cobraUsagePrefixes {
		if strings.Contains(msg, p) {
			return NewUsageError(err)
		}
	}
	return err
}
