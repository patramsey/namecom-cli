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

// parseError builds an APIError from a non-2xx response, reading and closing
// the body. The caller should only invoke this for non-2xx responses.
func parseError(resp *http.Response) *APIError {
	e := &APIError{StatusCode: resp.StatusCode}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	_ = resp.Body.Close()

	var env errorEnvelope
	if err := json.Unmarshal(body, &env); err == nil && env.Message != "" {
		e.Message = env.Message
		e.Details = env.Details
	} else {
		// Non-JSON or unexpected body: surface a trimmed snippet.
		e.Message = strings.TrimSpace(string(body))
		if e.Message == "" {
			e.Message = http.StatusText(resp.StatusCode)
		}
	}

	// Add an actionable hint for the most common credential mistake.
	if resp.StatusCode == http.StatusUnauthorized {
		e.Details = strings.TrimSpace(e.Details + " (note: sandbox uses a separate API token from production)")
	}
	return e
}
