package api

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	sdkcore "github.com/namedotcom/core-api-go/core"
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

// TestAPIError_401SandboxNoteOnlyInSandbox pins #234. Every 401 carried "note:
// sandbox uses a separate API token from production" in its message, in
// production too. The note is now in the hint, and only for the sandbox.
func TestAPIError_401SandboxNoteOnlyInSandbox(t *testing.T) {
	e := parseError(makeResp(401, `{"message":"unauthorized","details":"bad token"}`))
	if got := e.Error(); got != "unauthorized (bad token)" {
		t.Errorf("Error() = %q, want the API's message and details untouched", got)
	}
	if hint := e.UserHint(); strings.Contains(hint, "sandbox") || !strings.Contains(hint, "auth login") {
		t.Errorf("production hint = %q, want auth login and no sandbox note", hint)
	}
	e.Sandbox = true
	if hint := e.UserHint(); !strings.Contains(hint, "sandbox uses a separate API token") {
		t.Errorf("sandbox hint = %q, want the separate-token note", hint)
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
		// A 403 is not fixed by new credentials (#234).
		{403, "IP address"},
		{404, "name or ID"},
		{429, "rate limit"},
		// A bare 5xx on a read: nothing changed, so retrying is safe.
		// A 5xx that explains itself, or answers a write, is covered by
		// TestServerErrorHint.
		{500, "retry later"},
		{503, "retry later"},
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

// TestAPIError_MethodNotAllowedHint pins #291: the API's "Method Not Allowed",
// as a 405 or as the 404 it gives POST /core/v1/orders, was told to check the
// name or ID for typos.
func TestAPIError_MethodNotAllowedHint(t *testing.T) {
	for _, e := range []*APIError{
		ErrorFromResponse(405, []byte(`{"message":"Method Not Allowed"}`)),
		ErrorFromResponse(405, nil),
		ErrorFromResponse(404, []byte(`{"message":"Method Not Allowed"}`)),
	} {
		if hint := e.UserHint(); !strings.Contains(hint, "does not accept this method") {
			t.Errorf("%d %q: hint = %q, want one about the method", e.StatusCode, e.Message, hint)
		}
	}
	if hint := ErrorFromResponse(404, []byte(`{"message":"Not Found"}`)).UserHint(); !strings.Contains(hint, "typos") {
		t.Errorf("a plain 404 should keep its hint, got %q", hint)
	}
}

// TestAPIError_UnauthorizedNoteFormatting guards doubled parentheses. Error()
// renders details as "message (details)", and the 401 note was itself wrapped
// in parens, producing:
//
//	Unauthorized ((note: sandbox uses a separate API token from production))
//
// When the API supplies its own details the note must join them readably rather
// than nest another parenthetical inside. The note has since moved to the hint
// (#234), and the message must not grow one back.
func TestAPIError_UnauthorizedNoteFormatting(t *testing.T) {
	t.Run("no api details", func(t *testing.T) {
		e := ErrorFromResponse(401, []byte(`{"message":"Unauthorized"}`))
		got := e.Error()
		if strings.Contains(got, "((") || strings.Contains(got, "))") {
			t.Errorf("doubled parentheses in error: %q", got)
		}
		if got != "Unauthorized" {
			t.Errorf("Error() = %q, want the API's message alone", got)
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
		text := "BEGIN " + strings.Repeat("upstream connect error or disconnect ", 800)
		e := ErrorFromResponse(502, []byte(text))
		if len(e.Message) > maxFallbackMessage+80 {
			t.Errorf("message is %d chars, want it bounded near %d", len(e.Message), maxFallbackMessage)
		}
		if !strings.Contains(e.Message, "truncated") {
			t.Errorf("truncation is not disclosed: %q", e.Message)
		}
		if !strings.HasPrefix(e.Message, "BEGIN ") {
			t.Errorf("the front of the body was dropped: %q", e.Message)
		}
	})

	// #234: an HTML page was still quoted, markup and all, up to the cap:
	// `<html><head><title>502 Bad Gateway</title></head><body>…`. Only its
	// title says anything, and only when it adds to the status.
	for name, tc := range map[string]struct {
		status int
		body   string
		want   string
	}{
		"title repeats the status": {502,
			"<html><head><title>502 Bad Gateway</title></head><body><center><h1>502 Bad Gateway</h1></center><hr><center>nginx</center></body></html>",
			"HTTP 502 Bad Gateway (HTML error page)"},
		"title adds something": {503,
			"<!DOCTYPE html>\n<html><head><title>\n  Down for maintenance &amp; upgrades\n</title></head><body>" + strings.Repeat("<p>x</p>", 500) + "</body></html>",
			"HTTP 503 Service Unavailable: Down for maintenance & upgrades (HTML error page)"},
		"no title": {502, "<html><body><h1>502 Bad Gateway</h1></body></html>",
			"HTTP 502 Bad Gateway (HTML error page)"},
		"upper-case tags": {500, "<HTML><HEAD><TITLE>Oops</TITLE></HEAD></HTML>",
			"HTTP 500 Internal Server Error: Oops (HTML error page)"},
	} {
		t.Run("an HTML page is reduced to its title: "+name, func(t *testing.T) {
			if got := ErrorFromResponse(tc.status, []byte(tc.body)).Message; got != tc.want {
				t.Errorf("message = %q, want %q", got, tc.want)
			}
		})
	}

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

	t.Run("truncation does not split a multi-byte character", func(t *testing.T) {
		// 399 ASCII bytes then "é" (two bytes) puts the cut between them (#189).
		body := strings.Repeat("a", maxFallbackMessage-1) + "é and more"
		e := ErrorFromResponse(503, []byte(body))
		if !utf8.ValidString(e.Message) {
			t.Fatalf("message is not valid UTF-8: %q", e.Message)
		}
		if !strings.Contains(e.Message, "truncated") {
			t.Errorf("truncation is not disclosed: %q", e.Message)
		}
	})

	t.Run("invalid UTF-8 in the body is replaced", func(t *testing.T) {
		e := ErrorFromResponse(502, []byte("bad \xff\xfe gateway"))
		if !utf8.ValidString(e.Message) {
			t.Fatalf("message is not valid UTF-8: %q", e.Message)
		}
		if !strings.Contains(e.Message, "gateway") {
			t.Errorf("message lost the readable text: %q", e.Message)
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

// TestServerErrorHint covers issue #131. The sandbox answers 500 for what is
// really a validation error, and every 5xx used to get "try again shortly" —
// advice to retry a request that can never succeed. When the body is the API's
// envelope with something specific to say, the hint must not call the failure
// transient; with no message (empty body, a gateway's HTML page, or only a
// generic "Server error") the retry advice stands.
//
// #234 made the wording depend on the request too: a read is told retrying is
// safe and never that "the request itself may be invalid", a write that the
// change may have been made, and 500, 502 and 503 read the same.
func TestServerErrorHint(t *testing.T) {
	// Captured from the sandbox: vanity-ns create with a reserved IP.
	const reservedIP = `{"message":"Server error","details":"Command Failed - IP Address 192.0.2.53 Is Reserved"}`

	t.Run("a 5xx with an API message is not called transient", func(t *testing.T) {
		e := parseError(makeResp(500, reservedIP))
		if !strings.Contains(e.Error(), "IP Address 192.0.2.53 Is Reserved") {
			t.Errorf("Error() = %q, want the server's explanation in it", e.Error())
		}
		hint := e.UserHint()
		if strings.Contains(hint, "retry") || strings.Contains(hint, "try again") {
			t.Errorf("hint = %q, must not suggest the failure is transient", hint)
		}
		if !strings.Contains(hint, "gave the reason") {
			t.Errorf("hint = %q, want it to point at the API's reason", hint)
		}
	})

	t.Run("a specific message without details counts", func(t *testing.T) {
		e := ErrorFromResponse(500, []byte(`{"message":"IP Address Is Reserved"}`))
		if hint := e.UserHint(); strings.Contains(hint, "retry") {
			t.Errorf("hint = %q, must not suggest the failure is transient", hint)
		}
	})

	for name, tc := range map[string]struct {
		status int
		body   string
	}{
		"empty body":                      {500, ""},
		"gateway HTML page":               {502, "<html><body><h1>502 Bad Gateway</h1></body></html>"},
		"plain-text body":                 {503, "upstream connect error"},
		"envelope with a generic message": {500, `{"message":"Server error"}`},
		"envelope with the status text":   {503, `{"message":"Service Unavailable","details":null}`},
	} {
		t.Run(name+" is one wording", func(t *testing.T) {
			read := ErrorFromResponse(tc.status, []byte(tc.body))
			if hint := read.UserHint(); hint != "the failure is on name.com's side; nothing was changed, so it is safe to retry later" {
				t.Errorf("read hint = %q, want the one transient wording", hint)
			}
			write := ErrorFromResponse(tc.status, []byte(tc.body))
			write.Write = true
			if hint := write.UserHint(); !strings.Contains(hint, "may or may not have been made") {
				t.Errorf("write hint = %q, want it to warn the change may have been made", hint)
			}
		})
	}

	t.Run("an explained 5xx on a write still says to check", func(t *testing.T) {
		e := ErrorFromResponse(500, []byte(reservedIP))
		e.Write = true
		if hint := e.UserHint(); !strings.Contains(hint, "check whether the change was made") {
			t.Errorf("hint = %q, want it to say to check the change", hint)
		}
	})
}

// TestMarkWrite pins the seam RunWrite uses: it finds the API error, or the
// unreadable-reply error, under a caller's wrapping and under the SDK's own
// error type, and leaves anything else alone.
func TestMarkWrite(t *testing.T) {
	if MarkWrite(nil) != nil {
		t.Error("MarkWrite(nil) != nil")
	}
	apiErr := &APIError{StatusCode: 500}
	if err := MarkWrite(fmt.Errorf("creating: %w", apiErr)); err == nil || !apiErr.Write {
		t.Error("a wrapped *APIError was not marked")
	}
	sdkErr := sdkcore.NewAPIError(500, nil, errors.New(`{"message":"Server error"}`))
	got, ok := errors.AsType[*APIError](MarkWrite(fmt.Errorf("creating: %w", sdkErr)))
	if !ok || !got.Write {
		t.Errorf("an SDK error was not normalized and marked: %#v", got)
	}
	u := &UnexpectedResponseError{Reason: "x"}
	_ = MarkWrite(u)
	if !u.Write {
		t.Error("an *UnexpectedResponseError was not marked")
	}
	plain := errors.New("boom")
	if MarkWrite(plain) != plain {
		t.Error("an unrelated error should come back unchanged")
	}
}

// TestAPIError_DomainStateDenied pins #324: the API answers 403 for an
// expired domain, which is not a credential or account problem, and was
// reported as one, with exit 3 and the API-settings hint.
func TestAPIError_DomainStateDenied(t *testing.T) {
	tests := []struct {
		e        *APIError
		denied   bool
		wantHint bool
	}{
		{&APIError{StatusCode: 403, Message: "Permission denied. The domain is expired."}, true, false},
		{&APIError{StatusCode: 403, Message: "Permission Denied", Details: "The domain is locked"}, true, false},
		{&APIError{StatusCode: 403, Message: "Permission Denied"}, false, true},
		{&APIError{StatusCode: 403, Message: "Permission Denied", Details: "IP not whitelisted"}, false, true},
		{&APIError{StatusCode: 403, Message: "HTTP 403 Forbidden (HTML error page)"}, false, true},
		{&APIError{StatusCode: 401, Message: "domain"}, false, true},
	}
	for _, tt := range tests {
		if got := tt.e.DomainStateDenied(); got != tt.denied {
			t.Errorf("%v: DomainStateDenied() = %v, want %v", tt.e, got, tt.denied)
		}
		if got := tt.e.AuthFailure(); got == tt.denied {
			t.Errorf("%v: AuthFailure() = %v, want %v", tt.e, got, !tt.denied)
		}
		if got := tt.e.UserHint() != ""; got != tt.wantHint {
			t.Errorf("%v: hint %q, want one: %v", tt.e, tt.e.UserHint(), tt.wantHint)
		}
	}
}
