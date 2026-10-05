package cmdutil

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
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
type UsageError struct {
	Err error
	// Hint is the fix to suggest, such as the corrected command line. Empty
	// defers to any hint carried by Err.
	Hint string
}

func (e *UsageError) Error() string { return e.Err.Error() }
func (e *UsageError) Unwrap() error { return e.Err }

// UserHint returns Hint, or else the hint of the error it wraps. The error
// renderer stops at the outermost error with a UserHint, so an empty answer
// here must not hide one underneath.
func (e *UsageError) UserHint() string {
	if e.Hint != "" {
		return e.Hint
	}
	if h, ok := errors.AsType[interface {
		error
		UserHint() string
	}](e.Err); ok {
		return h.UserHint()
	}
	return ""
}

// NewUsageError wraps err as a usage problem. Returns nil for a nil err so it
// is safe to apply to a function result directly.
func NewUsageError(err error) error {
	if err == nil {
		return nil
	}
	return &UsageError{Err: err}
}

// NewUsageErrorHint is NewUsageError with the fix to suggest (#234): a usage
// error that only says what was wrong leaves the user to work out the
// command they meant.
func NewUsageErrorHint(err error, hint string) error {
	if err == nil {
		return nil
	}
	return &UsageError{Err: err, Hint: hint}
}

// AuthError marks a credential problem: none configured, or a credential helper
// that failed. Maps to exit code 3.
type AuthError struct {
	Err error
	// Hint replaces the generic hint when set. custom marks a hint chosen by
	// the failing path even when it is "", meaning the message already says
	// what to do (NewAuthErrorHint).
	Hint   string
	custom bool
}

func (e *AuthError) Error() string { return e.Err.Error() }
func (e *AuthError) Unwrap() error { return e.Err }

// UserHint points at the auth commands, unless the error was built with its
// own hint. The error renderer prints it with the error, on stderr, in every
// output format.
func (e *AuthError) UserHint() string {
	if e.custom || e.Hint != "" {
		return e.Hint
	}
	return "run 'namecom auth status' to check your credentials, or 'namecom auth login' to reconfigure"
}

// NewAuthError wraps err as a credential problem. Returns nil for a nil err.
func NewAuthError(err error) error {
	if err == nil {
		return nil
	}
	return &AuthError{Err: err}
}

// NewAuthErrorHint is NewAuthError with its own hint in place of the generic
// one. Pass "" when err's message already says what to do: the generic hint
// repeated `auth login` after a message that had just suggested it, and
// offered `auth status` to someone who had just run it (#234).
func NewAuthErrorHint(err error, hint string) error {
	if err == nil {
		return nil
	}
	return &AuthError{Err: err, Hint: hint, custom: true}
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

// UserHint replaces the 403's own "run 'namecom auth login'" hint, which the
// error display would otherwise find by unwrapping to the *api.APIError.
func (e *RestrictedError) UserHint() string {
	return "your credentials are fine — this account is not enrolled as an " + e.Program
}

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
	// Positional-arg validators: MaximumNArgs, ExactArgs and RangeArgs say
	// "arg(s), received", MinimumNArgs "arg(s), only received". NoArgs reports
	// "unknown command", above. cmdutil.ExactArgs/MinimumNArgs classify
	// themselves; these catch cobra's own, as on `namecom open`.
	" arg(s), received ",
	" arg(s), only received ",
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
	if strings.HasPrefix(msg, "unknown command ") {
		return restateUnknownCommand(msg)
	}
	for _, p := range cobraUsagePrefixes {
		if strings.Contains(msg, p) {
			return NewUsageError(err)
		}
	}
	return err
}

// restateUnknownCommand turns cobra's unknown-command error — one line, then
// "\n\nDid you mean this?\n\tdomain\n" when it has a suggestion — into the
// same error GroupCmd returns: the line alone, with the suggestion as the hint.
func restateUnknownCommand(msg string) error {
	first, rest, _ := strings.Cut(msg, "\n")
	var suggestions []string
	if _, list, ok := strings.Cut(rest, "Did you mean this?\n"); ok {
		suggestions = strings.Fields(list)
	}
	// cobra formats the line as `unknown command %q for %q`.
	word, path := "", ""
	after := strings.TrimPrefix(first, "unknown command ")
	if q, err := strconv.QuotedPrefix(after); err == nil {
		word, _ = strconv.Unquote(q)
		if p, err := strconv.QuotedPrefix(strings.TrimPrefix(after, q+" for ")); err == nil {
			path, _ = strconv.Unquote(p)
		}
	}
	if word == "" || path == "" {
		return NewUsageError(errors.New(first))
	}
	return UnknownCommand(word, path, suggestions)
}

// UnknownCommandError is an unknown subcommand, Word, typed after Path.
// Suggestions are the commands it was probably meant to be, as full command
// lines ("namecom dns delete"), for the error envelope's `suggestions` field.
type UnknownCommandError struct {
	Word        string
	Path        string
	Suggestions []string
}

func (e *UnknownCommandError) Error() string {
	return fmt.Sprintf("unknown command %q for %q", e.Word, e.Path)
}

// UnknownCommand is a usage error for an unknown subcommand word of path.
// suggestions are subcommands relative to path, one word or several ("auth
// login"). The hint names them, or points at the help when there are none:
// `namecom dns rm` used to get nothing to go on (#234). The near misses were
// in the message itself, over three more lines.
func UnknownCommand(word, path string, suggestions []string) error {
	e := &UnknownCommandError{Word: word, Path: path}
	hint := fmt.Sprintf("run '%s --help' for usage", path)
	if len(suggestions) > 0 {
		quoted := make([]string, len(suggestions))
		for i, s := range suggestions {
			e.Suggestions = append(e.Suggestions, path+" "+s)
			quoted[i] = "'" + path + " " + s + "'"
		}
		hint = "did you mean " + strings.Join(quoted, " or ") + "?"
	}
	return NewUsageErrorHint(e, hint)
}

// RequireField returns an *api.UnexpectedResponseError when value, the field
// that identifies the resource a get command fetched, is its zero value — an
// empty string or a nil pointer. what names it for the message: "the domain
// name" reads as "the response did not include the domain name".
//
// A `200 {}` decodes without error into a response with every field zero, and
// get commands printed that as an empty resource and exited 0 (#187). Only
// single-resource reads use this: an empty list is a valid answer, and a write
// has its own checks on what it needs back.
func RequireField[T comparable](what string, value T) error {
	var zero T
	if value != zero {
		return nil
	}
	return &api.UnexpectedResponseError{Reason: "the response did not include " + what}
}
