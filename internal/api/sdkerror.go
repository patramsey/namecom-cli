package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
		if reason := undecodableBody(err); reason != "" {
			return &UnexpectedResponseError{Reason: reason, Err: err}
		}
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
	if _, ok := errors.AsType[*UnexpectedResponseError](err); ok {
		return err
	}
	sdkErr, ok := errors.AsType[*sdkcore.APIError](err)
	if !ok {
		return normalizeDecodeError(err)
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

// UnexpectedResponseError is a successful (2xx) response whose body could not
// be read as the type the endpoint promises: empty, not JSON, or JSON of the
// wrong shape.
//
// The SDK reports these with the decoder's own text — `json: cannot unmarshal
// array into Go value of type struct { api.embed; … }`, or `expected a
// **api.DomainResponsePayload response, but the server responded with nothing`
// — which names Go types and says nothing a user can act on (#158). It is not
// an *APIError: the status was a success, so it exits 1 rather than a code
// derived from the status.
type UnexpectedResponseError struct {
	Reason string
	Err    error
	// Write marks the response to a request that changes something, so the
	// hint warns that the change may have been made. It is set by MarkWrite.
	// It used to be the other way round — a Read flag that only one caller
	// set — so `domain get` on a non-JSON 200 warned that "a change may have
	// been made" (#234).
	Write bool
}

func (e *UnexpectedResponseError) Error() string {
	return "unexpected response from the API: " + e.Reason
}

func (e *UnexpectedResponseError) Unwrap() error { return e.Err }

// UserHint warns that a write may have gone through: the API answered with a
// success status, only its reply was unreadable.
func (e *UnexpectedResponseError) UserHint() string {
	if !e.Write {
		return "--debug shows the response"
	}
	return "the API reported success, so a change may have been made — check before retrying; --debug shows the response"
}

// undecodableBody reports why err, exactly as an SDK method returned it, is a
// failure to decode a 2xx response body, or "" when it is not one.
//
// It inspects err itself, not its chain. A command that cannot parse a file
// the user supplied wraps the same decoder errors (`parsing import file: …`),
// and that is not an API response.
func undecodableBody(err error) string {
	switch e := err.(type) {
	case *json.UnmarshalTypeError:
		if field := strings.Trim(e.Field, "."); field != "" {
			return fmt.Sprintf("field %q is a JSON %s, not the expected type", field, e.Value)
		}
		return fmt.Sprintf("the body is a JSON %s, not the expected shape", e.Value)
	case *json.SyntaxError:
		return "the body is not valid JSON"
	}
	if err == io.ErrUnexpectedEOF { // the bare value, as the decoder returns it
		return "the body ended before its JSON was complete"
	}
	switch {
	case isEmptyBodyError(err):
		return "the body was empty"
	case strings.HasPrefix(err.Error(), "unable to parse datetime string"):
		return "a date in the body could not be parsed"
	}
	return ""
}

// isEmptyBodyError matches the SDK's error for a 2xx with no body where one was
// expected. It is a plain fmt.Errorf with no type to match on, but its text is
// the SDK's own, so it is recognized wherever it sits in a chain.
func isEmptyBodyError(err error) bool {
	msg := err.Error()
	return strings.HasPrefix(msg, "expected a ") &&
		strings.HasSuffix(msg, "response, but the server responded with nothing")
}

// normalizeDecodeError is NormalizeError's half of #158. A decode error the
// SDK returned bare is converted; the SDK's empty-body error is also found
// under a caller's wrapping, keeping that context.
func normalizeDecodeError(err error) error {
	if reason := undecodableBody(err); reason != "" {
		return &UnexpectedResponseError{Reason: reason, Err: err}
	}
	for inner := errors.Unwrap(err); inner != nil; inner = errors.Unwrap(inner) {
		if !isEmptyBodyError(inner) {
			continue
		}
		converted := &UnexpectedResponseError{Reason: "the body was empty", Err: inner}
		return &contextError{
			msg: strings.Replace(err.Error(), inner.Error(), converted.Error(), 1),
			err: converted,
		}
	}
	return err
}
