package api

import (
	"encoding/json"
	"errors"
	"net/http"
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

	out := &APIError{StatusCode: apiErr.StatusCode}
	if apiErr.Header != nil {
		if ra := parseRetryAfter(apiErr.Header.Get("Retry-After")); ra != nil {
			out.RetryAfter = *ra
		}
	}

	// The SDK folds the response body into the error's message rather than
	// exposing it. The API's envelope is {"message":…,"details":…}, so try to
	// recover those two fields; fall back to the raw text when it is not that
	// shape, which is what a proxy or gateway error looks like.
	msg := apiErr.Error()
	if envelope := extractEnvelope(msg); envelope != nil {
		out.Message, out.Details = envelope.Message, envelope.Details
	} else {
		out.Message = strings.TrimSpace(msg)
	}
	if out.Message == "" {
		out.Message = http.StatusText(apiErr.StatusCode)
	}
	return out
}

// errEnvelope is the API's error shape.
type errEnvelope struct {
	Message string `json:"message"`
	Details string `json:"details"`
}

// extractEnvelope pulls the {"message":…} object out of an SDK error string.
// The SDK prefixes the body with its own text, so the JSON is located rather
// than parsed from the start.
func extractEnvelope(s string) *errEnvelope {
	i := strings.Index(s, "{")
	if i < 0 {
		return nil
	}
	var e errEnvelope
	if err := json.Unmarshal([]byte(s[i:]), &e); err != nil {
		return nil
	}
	if e.Message == "" {
		return nil
	}
	return &e
}
