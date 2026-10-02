package api

import (
	"errors"
	"strings"

	sdkcore "github.com/namedotcom/core-api-go/core"
)

// FromSDKError converts an error returned by the Core SDK into the *APIError
// the rest of the CLI already understands, and returns other errors unchanged.
//
// This exists because exit codes are a documented contract — 4 for not-found, 5
// for rate-limited, 3 for auth — and root.go derives them by inspecting
// *APIError. An SDK call that returned *core.APIError instead would fall
// through to the generic "1", silently turning a 404 into an indistinguishable
// failure for any script branching on it. v0.3.0 shipped a release note about
// exit codes; quietly regressing them during the #40 migration is not an option.
//
// Retry-After is recovered from the response header the SDK preserves, which is
// what makes the 429 message able to say how long to wait rather than "wait a
// moment".
func FromSDKError(err error) error {
	if err == nil {
		return nil
	}
	var apiErr *sdkcore.APIError
	if !errors.As(err, &apiErr) {
		return err
	}

	// The SDK keeps the response body as the error it wraps, verbatim; the
	// typed errors (*InternalServerError and the rest) embed the same value.
	// Hand it to ErrorFromResponse so SDK calls are normalized exactly as the
	// `namecom api` passthrough is. This used to parse the envelope out of the
	// error string on its own, and so missed what was added there later: the
	// explained-5xx hint (#131), the summary of an HTML proxy page, and the
	// sandbox note on a 401 (#159).
	var body []byte
	if inner := apiErr.Unwrap(); inner != nil {
		body = []byte(inner.Error())
	}
	out := ErrorFromResponse(apiErr.StatusCode, body)
	if apiErr.Header != nil {
		if ra := parseRetryAfter(apiErr.Header.Get("Retry-After")); ra != nil {
			out.RetryAfter = *ra
		}
	}
	return out
}

// NormalizeError converts a Core SDK error anywhere in err's chain into an
// *APIError, keeping whatever context was wrapped around it.
//
// FromSDKError is applied at individual call sites, and that is not enough on
// its own: it is opt-in, and eight commands shipped without it. Each of them
// exited 1 on a 404 rather than the documented 4 and printed the raw response
// body — `404: {"message":"Not Found"}` — as its error. Execute calls this once
// on every command's error, so conversion no longer depends on each command
// remembering to ask for it.
//
// Unlike FromSDKError, which returns a bare *APIError, this keeps an outer
// message: "fetching domain: <sdk error>" becomes "fetching domain: Not Found"
// rather than losing the "fetching domain" a caller chose to add.
func NormalizeError(err error) error {
	if err == nil {
		return nil
	}
	if _, ok := errors.AsType[*APIError](err); ok {
		return err // already converted somewhere in the chain
	}
	sdkErr, ok := errors.AsType[*sdkcore.APIError](err)
	if !ok {
		return err
	}
	converted := FromSDKError(sdkErr)
	outer, inner := err.Error(), sdkErr.Error()
	if outer == inner {
		return converted
	}
	return &contextError{msg: strings.Replace(outer, inner, converted.Error(), 1), err: converted}
}

// contextError carries a caller's wrapped message over a converted *APIError,
// so the text keeps its context and exit-code classification still finds the
// status code through Unwrap.
type contextError struct {
	msg string
	err error
}

func (e *contextError) Error() string { return e.msg }
func (e *contextError) Unwrap() error { return e.err }
