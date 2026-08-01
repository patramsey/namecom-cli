package api

import (
	"io"
	"net/http"
	"strings"
	"testing"
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
