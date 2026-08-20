package api

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func makeResp(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func TestParseError_JSONEnvelope(t *testing.T) {
	e := parseError(makeResp(422, `{"message":"invalid domain","details":"domain already exists"}`))
	if e.StatusCode != 422 {
		t.Errorf("StatusCode = %d, want 422", e.StatusCode)
	}
	if e.Message != "invalid domain" {
		t.Errorf("Message = %q, want %q", e.Message, "invalid domain")
	}
	if e.Details != "domain already exists" {
		t.Errorf("Details = %q, want %q", e.Details, "domain already exists")
	}
}

func TestParseError_JSONNoDetails(t *testing.T) {
	e := parseError(makeResp(404, `{"message":"not found"}`))
	if e.Message != "not found" {
		t.Errorf("Message = %q, want %q", e.Message, "not found")
	}
	if e.Details != "" {
		t.Errorf("Details = %q, want empty", e.Details)
	}
}

func TestParseError_NonJSON(t *testing.T) {
	e := parseError(makeResp(503, "Service Unavailable"))
	if e.Message != "Service Unavailable" {
		t.Errorf("Message = %q, want plain-text body", e.Message)
	}
}

func TestParseError_EmptyBody(t *testing.T) {
	e := parseError(makeResp(500, ""))
	if e.Message != http.StatusText(500) {
		t.Errorf("Message = %q, want %q", e.Message, http.StatusText(500))
	}
}

func TestParseError_401AppendsSandboxHint(t *testing.T) {
	e := parseError(makeResp(401, `{"message":"unauthorized","details":"bad token"}`))
	if !strings.Contains(e.Details, "sandbox") {
		t.Errorf("401 Details should mention sandbox, got %q", e.Details)
	}
}

func TestAPIError_ErrorString(t *testing.T) {
	tests := []struct {
		e    APIError
		want string
	}{
		{APIError{StatusCode: 404, Message: "not found"}, "not found"},
		{APIError{StatusCode: 422, Message: "bad input", Details: "field required"}, "bad input (field required)"},
		{APIError{StatusCode: 500}, "HTTP 500"},
	}
	for _, tt := range tests {
		if got := tt.e.Error(); got != tt.want {
			t.Errorf("Error() = %q, want %q", got, tt.want)
		}
	}
}

func TestAPIError_UserHint(t *testing.T) {
	tests := []struct {
		status      int
		wantContain string
	}{
		{401, "auth login"},
		{403, "auth login"},
		{404, "not found"},
		{429, "rate limit"},
		{500, "try again"},
		{503, "try again"},
		{200, ""},
	}
	for _, tt := range tests {
		e := &APIError{StatusCode: tt.status}
		hint := e.UserHint()
		if tt.wantContain == "" {
			if hint != "" {
				t.Errorf("status %d: expected no hint, got %q", tt.status, hint)
			}
		} else if !strings.Contains(hint, tt.wantContain) {
			t.Errorf("status %d: hint %q missing %q", tt.status, hint, tt.wantContain)
		}
	}
}

// TestAPIError_UnauthorizedNoteFormatting guards doubled parentheses. Error()
// renders details as "message (details)", and the 401 note was itself wrapped
// in parens, producing:
//
//	Unauthorized ((note: sandbox uses a separate API token from production))
//
// When the API supplies its own details the note must join them readably rather
// than nest another parenthetical inside.
func TestAPIError_UnauthorizedNoteFormatting(t *testing.T) {
	t.Run("no api details", func(t *testing.T) {
		e := ErrorFromResponse(401, []byte(`{"message":"Unauthorized"}`))
		got := e.Error()
		if strings.Contains(got, "((") || strings.Contains(got, "))") {
			t.Errorf("doubled parentheses in error: %q", got)
		}
		if !strings.Contains(got, "sandbox uses a separate API token") {
			t.Errorf("expected the sandbox hint, got: %q", got)
		}
	})

	t.Run("with api details", func(t *testing.T) {
		e := ErrorFromResponse(401, []byte(`{"message":"Unauthorized","details":"token expired"}`))
		got := e.Error()
		if strings.Contains(got, "((") || strings.Contains(got, "))") {
			t.Errorf("doubled parentheses in error: %q", got)
		}
		if !strings.Contains(got, "token expired") {
			t.Errorf("API's own details must be preserved, got: %q", got)
		}
	})
}

// TestSummarizeBody covers error bodies that are not the API's JSON envelope.
//
// They used to become the error message verbatim, bounded only by parseError's
// 1 MiB read limit. A 502 HTML page from a proxy rendered as a single 20 KB
// line — in the terminal and inside the JSON error envelope alike.
func TestSummarizeBody(t *testing.T) {
	t.Run("a long non-JSON body is truncated and counted", func(t *testing.T) {
		html := "<html><body>" + strings.Repeat("<p>nginx error page</p>", 800) + "</body></html>"
		e := ErrorFromResponse(502, []byte(html))
		if len(e.Message) > maxFallbackMessage+80 {
			t.Errorf("message is %d chars, want it bounded near %d", len(e.Message), maxFallbackMessage)
		}
		if !strings.Contains(e.Message, "truncated") {
			t.Errorf("truncation is not disclosed: %q", e.Message)
		}
		if !strings.Contains(e.Message, "<html>") {
			t.Errorf("the front of the body was dropped: %q", e.Message)
		}
	})

	t.Run("newlines are collapsed so the message stays one line", func(t *testing.T) {
		e := ErrorFromResponse(500, []byte("upstream\n  connect\n\terror"))
		if strings.ContainsAny(e.Message, "\n\t") {
			t.Errorf("message spans lines: %q", e.Message)
		}
		if e.Message != "upstream connect error" {
			t.Errorf("message = %q, want %q", e.Message, "upstream connect error")
		}
	})

	t.Run("a short body is passed through", func(t *testing.T) {
		if got := ErrorFromResponse(503, []byte("upstream down")).Message; got != "upstream down" {
			t.Errorf("message = %q, want %q", got, "upstream down")
		}
	})

	t.Run("an empty body falls back to the status text", func(t *testing.T) {
		want := http.StatusText(http.StatusServiceUnavailable)
		if got := ErrorFromResponse(http.StatusServiceUnavailable, nil).Message; got != want {
			t.Errorf("message = %q, want %q", got, want)
		}
	})

	t.Run("a proper JSON envelope is untouched", func(t *testing.T) {
		e := ErrorFromResponse(422, []byte(`{"message":"bad ttl","details":"minimum is 300"}`))
		if e.Message != "bad ttl" || e.Details != "minimum is 300" {
			t.Errorf("got %+v, want the envelope decoded", e)
		}
	})
}

// TestRetryAfterHint checks that a 429's hint reflects what the server asked
// for. "wait a moment and try again" is misleading next to a ten-minute
// Retry-After, and that combination is exactly what used to be swallowed
// entirely — slept on until the client timeout fired and reported as a
// transport error.
func TestRetryAfterHint(t *testing.T) {
	long := &APIError{StatusCode: 429, Message: "slow down", RetryAfter: 10 * time.Minute}
	if hint := long.UserHint(); !strings.Contains(hint, "10m") {
		t.Errorf("hint = %q, want it to name the wait", hint)
	}
	bare := &APIError{StatusCode: 429, Message: "slow down"}
	if hint := bare.UserHint(); !strings.Contains(hint, "wait a moment") {
		t.Errorf("hint = %q, want the generic wording when no header was sent", hint)
	}
}

// TestParseErrorCapturesRetryAfter pins that the header survives the trip from
// response to APIError. UserHint reads RetryAfter to say how long the API asked
// for, and nothing else populates it — a hint that silently fell back to "wait
// a moment" would look correct while having lost the number.
func TestParseErrorCapturesRetryAfter(t *testing.T) {
	withHeader := func(status int, body, retryAfter string) *http.Response {
		resp := makeResp(status, body)
		if resp.Header == nil {
			resp.Header = http.Header{}
		}
		if retryAfter != "" {
			resp.Header.Set("Retry-After", retryAfter)
		}
		return resp
	}

	t.Run("a delta-seconds header is captured", func(t *testing.T) {
		e := parseError(withHeader(http.StatusTooManyRequests, `{"message":"slow down"}`, "600"))
		if e.RetryAfter != 10*time.Minute {
			t.Errorf("RetryAfter = %s, want 10m", e.RetryAfter)
		}
		if !strings.Contains(e.UserHint(), "10m") {
			t.Errorf("hint = %q, want it to name the wait", e.UserHint())
		}
	})

	t.Run("no header leaves it zero", func(t *testing.T) {
		e := parseError(withHeader(http.StatusTooManyRequests, `{"message":"slow down"}`, ""))
		if e.RetryAfter != 0 {
			t.Errorf("RetryAfter = %s, want zero", e.RetryAfter)
		}
	})

	t.Run("an unparseable header leaves it zero", func(t *testing.T) {
		e := parseError(withHeader(http.StatusTooManyRequests, `{"message":"slow down"}`, "Wed, 21 Oct 2026 07:28:00 GMT"))
		if e.RetryAfter != 0 {
			t.Errorf("RetryAfter = %s, want zero for the HTTP-date form", e.RetryAfter)
		}
	})
}
