package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// APIError is a normalized name.com API error. The API returns a consistent
// JSON envelope of {"message": string, "details": string|null} on 4xx/5xx
// responses; this captures that along with the HTTP status for exit-code
// mapping and user-facing messages.
type APIError struct {
	StatusCode int
	Message    string
	Details    string
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
		return "rate limited — wait a moment and try again"
	}
	if e.StatusCode >= 500 {
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
	} else {
		e.Message = strings.TrimSpace(string(body))
		if e.Message == "" {
			e.Message = http.StatusText(statusCode)
		}
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

// parseError builds an APIError from a non-2xx response, reading and closing
// the body. The caller should only invoke this for non-2xx responses.
func parseError(resp *http.Response) *APIError {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	_ = resp.Body.Close()
	return ErrorFromResponse(resp.StatusCode, body)
}
