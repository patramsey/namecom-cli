package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"html"
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
	// Write marks the reply to a request that changes something, set by
	// MarkWrite. A 5xx then warns that the change may have been made; for a
	// read it does not, since nothing could have changed.
	Write bool
	// Sandbox is set when the request went to the sandbox API, so a 401 can
	// mention its separate token without saying so in production too.
	Sandbox bool
	// CredentialSource names the flags or environment variables the
	// credentials came from, e.g. "NAMECOM_USERNAME and NAMECOM_TOKEN"; ""
	// when they came from a profile. A 401 then says what to fix rather than
	// suggesting `auth login`, which a CI job cannot run.
	CredentialSource string
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

// MethodNotAllowed reports whether the API refused the request's method
// rather than its path: a 405, or a 404 whose message says "Method Not
// Allowed", as the API answers POST /core/v1/orders (#282). Its hint was the
// 404's "check the name or ID for typos", which is no help (#291).
func (e *APIError) MethodNotAllowed() bool {
	return e.StatusCode == http.StatusMethodNotAllowed ||
		(e.StatusCode == http.StatusNotFound && strings.EqualFold(strings.TrimSpace(e.Message), "Method Not Allowed"))
}

// UserHint returns an actionable next-step hint for display alongside the
// error, chosen by status and by whether the request was a write (#234).
func (e *APIError) UserHint() string {
	if e.MethodNotAllowed() {
		return "the path exists but does not accept this method — check which method the API documents for it"
	}
	switch e.StatusCode {
	case 401:
		hint := "the API rejected the username or token — run 'namecom auth login' to replace them"
		if e.CredentialSource != "" {
			hint = "the API rejected the username or token — check " + e.CredentialSource
		}
		if e.Sandbox {
			hint += " (the sandbox uses a separate API token from production)"
		}
		return hint
	case 403:
		// Not "log in again": the API answers 403 to credentials it accepted,
		// for an account without permission or a request from an IP address
		// the account's API settings do not allow. New credentials fix neither.
		return "the account is not permitted to do this, or the API is not accepting requests from your IP address — check the account's API settings at https://www.name.com/account/settings/api"
	case 404:
		return "check the name or ID for typos"
	case 429:
		// Say how long when the server said so: "wait a moment" is misleading
		// advice next to a Retry-After of ten minutes.
		if e.RetryAfter > 0 {
			return fmt.Sprintf("rate limited — the API asked to wait %s before retrying", e.RetryAfter.Round(time.Second))
		}
		return "rate limited — wait a moment and try again"
	}
	if e.StatusCode >= 500 {
		// One wording for every 5xx, varied only by what is true of this
		// request. 500, 502 and 503 used to get three different hints, and
		// a read was told "the request itself may be invalid".
		//
		// The API answers 500 for some validation failures — a reserved IP
		// on vanity-ns create, for one. When it says why (explained), the
		// same request will fail the same way. A write the server failed
		// partway may still have happened, which is why a POST is not
		// retried on a 5xx.
		switch {
		case e.Write && e.explained:
			return "name.com gave the reason above; check whether the change was made before sending it again"
		case e.Write:
			return "name.com failed while handling this change, so it may or may not have been made — check before retrying"
		case e.explained:
			return "name.com gave the reason above; repeating the same request will likely fail the same way"
		}
		return "the failure is on name.com's side; nothing was changed, so it is safe to retry later"
	}
	return ""
}

// MarkWrite records that err came from a request that changes something, so
// its hint can say a change may have been made. It normalizes err first and
// returns it, otherwise unchanged; nil stays nil.
//
// The SDK's errors do not carry the request's method, and a write command
// also makes reads — `dns update` fetches the record first — so the command
// cannot be the judge either. cmdutil.RunWrite marks what its send returns.
func MarkWrite(err error) error {
	err = NormalizeError(err)
	if apiErr, ok := errors.AsType[*APIError](err); ok {
		apiErr.Write = true
	}
	if u, ok := errors.AsType[*UnexpectedResponseError](err); ok {
		u.Write = true
	}
	return err
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
	// A 401 used to carry "note: sandbox uses a separate API token from
	// production" in its details, in production too. It is now part of the
	// hint, and only when Sandbox is set (#234).
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
	if looksLikeHTML(msg) {
		return summarizeHTML(msg, statusCode)
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

// maxHTMLTitle bounds the part of an HTML page's title kept in the message.
const maxHTMLTitle = 120

// looksLikeHTML reports whether a body, whitespace already collapsed, is an
// HTML page: what a proxy or load balancer sends in place of the API's JSON.
func looksLikeHTML(s string) bool {
	l := strings.ToLower(s)
	if !strings.HasPrefix(l, "<") {
		return false
	}
	return strings.HasPrefix(l, "<!doctype html") || strings.Contains(l, "<html") ||
		strings.Contains(l, "<title") || strings.Contains(l, "<body")
}

// summarizeHTML reduces an HTML error page to the status line and its title
// (#234). Even shortened to maxFallbackMessage, the markup was most of the
// message — `<html><head><title>502 Bad Gateway</title></head><body>…` — and
// the title is the only part written to be read. It is kept only when it says
// more than the status does.
func summarizeHTML(page string, statusCode int) string {
	status := fmt.Sprintf("HTTP %d %s", statusCode, http.StatusText(statusCode))
	title := htmlTitle(page)
	l := strings.ToLower(title)
	if title == "" || strings.Contains(l, strings.ToLower(http.StatusText(statusCode))) ||
		strings.Trim(l, "0123456789 ") == "" {
		return status + " (HTML error page)"
	}
	return status + ": " + title + " (HTML error page)"
}

// htmlTitle returns the text of page's <title>, entities decoded and cut to
// maxHTMLTitle, or "" when it has none.
func htmlTitle(page string) string {
	l := asciiLower(page) // offsets into l are offsets into page
	start := strings.Index(l, "<title")
	if start < 0 {
		return ""
	}
	open := strings.IndexByte(l[start:], '>')
	if open < 0 {
		return ""
	}
	start += open + 1
	end := strings.Index(l[start:], "</title")
	if end < 0 {
		return ""
	}
	title := strings.Join(strings.Fields(html.UnescapeString(page[start:start+end])), " ")
	if len(title) > maxHTMLTitle {
		cut := maxHTMLTitle
		for cut > 0 && !utf8.RuneStart(title[cut]) {
			cut--
		}
		title = strings.TrimSpace(title[:cut]) + "…"
	}
	return title
}

// asciiLower lower-cases A-Z only, so every byte stays where it was.
// strings.ToLower can change a character's encoded length.
func asciiLower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if 'A' <= c && c <= 'Z' {
			b[i] = c + ('a' - 'A')
		}
	}
	return string(b)
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
