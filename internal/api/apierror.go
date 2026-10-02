package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"
)

// APIError is a normalized name.com API error. The API returns a consistent
// JSON envelope of {"message": string, "details": string|null} on 4xx/5xx
// responses; this captures that along with the HTTP status for exit-code
// mapping and user-facing messages.
type APIError struct {
	StatusCode int
	Message    string
	Details    string
	// RetryAfter carries the Retry-After header from a 429, when the server
	// sent one. Zero means it did not.
	RetryAfter time.Duration
	// explained is set when a 5xx body was the API's envelope with something
	// specific to say, rather than an empty body, a proxy's page, or a bare
	// "Server error". See UserHint.
	explained bool
	// Body is the raw response body, set only by callers that show it — the
	// `namecom api` passthrough. It becomes the error envelope's details.
	Body []byte
}

// ErrorDetails returns the response body for the structured error envelope:
// the parsed JSON when the body is JSON, the text otherwise, and nil when no
// body was kept.
func (e *APIError) ErrorDetails() any {
	if len(e.Body) == 0 {
		return nil
	}
	if json.Valid(e.Body) {
		return json.RawMessage(e.Body)
	}
	return string(e.Body)
}

func (e *APIError) Error() string {
	if e.Details != "" {
		return fmt.Sprintf("%s (%s)", e.Message, e.Details)
	}
	if e.Message != "" {
		return e.Message
	}
	return fmt.Sprintf("HTTP %d", e.StatusCode)
}

// UserHint returns an actionable next-step hint for display alongside the error.
func (e *APIError) UserHint() string {
	switch e.StatusCode {
	case 401, 403:
		return "run 'namecom auth login' to reconfigure credentials"
	case 404:
		return "the requested resource was not found — check the domain name or ID"
	case 429:
		// Say how long when the server said so: "wait a moment" is misleading
		// advice next to a Retry-After of ten minutes.
		if e.RetryAfter > 0 {
			return fmt.Sprintf("rate limited — the API asked to wait %s before retrying", e.RetryAfter.Round(time.Second))
		}
		return "rate limited — wait a moment and try again"
	}
	if e.StatusCode >= 500 {
		// The API answers 500 for some validation failures — a reserved IP on
		// vanity-ns create, for one. When it says why, retrying will not help,
		// so don't call it transient; the message is already on the error line.
		if e.explained {
			return "name.com returned a server error; if it persists, the request itself may be invalid"
		}
		return "name.com API error — try again shortly"
	}
	return ""
}

// errorEnvelope matches the API's error body shape.
type errorEnvelope struct {
	Message string `json:"message"`
	Details string `json:"details"`
}

// ErrorFromResponse builds an *APIError from a status code and an
// already-read response body. Callers that read the body themselves — the raw
// `namecom api` passthrough — use this so their failures carry the same type,
// exit-code mapping, and user hints as every generated endpoint call.
func ErrorFromResponse(statusCode int, body []byte) *APIError {
	e := &APIError{StatusCode: statusCode}
	var env errorEnvelope
	if err := json.Unmarshal(body, &env); err == nil && env.Message != "" {
		e.Message = env.Message
		e.Details = env.Details
		e.explained = statusCode >= 500 &&
			(strings.TrimSpace(env.Details) != "" || !isGenericServerMessage(env.Message, statusCode))
	} else {
		e.Message = summarizeBody(body, statusCode)
	}
	if statusCode == http.StatusUnauthorized {
		// Error() already renders details as "message (details)", so the note
		// must not carry its own parentheses — that produced
		// "Unauthorized ((note: …))". Join to any API-supplied details rather
		// than nesting a second parenthetical inside the first.
		const note = "note: sandbox uses a separate API token from production"
		if d := strings.TrimSpace(e.Details); d != "" {
			e.Details = d + "; " + note
		} else {
			e.Details = note
		}
	}
	return e
}

// isGenericServerMessage reports whether a 5xx envelope's message says nothing
// beyond the status itself, e.g. "Server error" or "Internal Server Error".
func isGenericServerMessage(msg string, statusCode int) bool {
	m := strings.TrimSpace(msg)
	return strings.EqualFold(m, "server error") || strings.EqualFold(m, http.StatusText(statusCode))
}

// maxFallbackMessage bounds how much of a non-JSON error body becomes the
// error message.
const maxFallbackMessage = 400

// summarizeBody turns a body that is not the API's JSON envelope into a usable
// one-line message.
//
// It used to be passed through verbatim, capped only by the 1 MiB read limit.
// A proxy answering with an HTML error page therefore became the error text: a
// 502 from nginx rendered as a single 20 KB line of markup, in the terminal and
// inside the JSON error envelope alike. Collapse the whitespace, keep the
// front of it, and say how much was dropped.
//
// The message ends up in the terminal and in a JSON envelope, so it must be
// valid UTF-8: invalid bytes in the body are replaced, and the cut is moved
// back to a rune boundary rather than splitting a multi-byte character (#189).
func summarizeBody(body []byte, statusCode int) string {
	msg := strings.Join(strings.Fields(strings.ToValidUTF8(string(body), "\uFFFD")), " ")
	if msg == "" {
		return http.StatusText(statusCode)
	}
	if len(msg) > maxFallbackMessage {
		cut := maxFallbackMessage
		for cut > 0 && !utf8.RuneStart(msg[cut]) {
			cut--
		}
		return fmt.Sprintf("%s… (%d bytes of non-JSON body truncated)",
			strings.TrimSpace(msg[:cut]), len(body))
	}
	return msg
}

// parseError builds an APIError from a non-2xx response, reading and closing
// the body. The caller should only invoke this for non-2xx responses.
func parseError(resp *http.Response) *APIError {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	_ = resp.Body.Close()
	e := ErrorFromResponse(resp.StatusCode, body)
	if ra := parseRetryAfter(resp.Header.Get("Retry-After")); ra != nil {
		e.RetryAfter = *ra
	}
	return e
}
