package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"testing"
)

func TestScrubSecrets(t *testing.T) {
	t.Cleanup(resetSecrets)
	resetSecrets()
	AddSecret("ab<c&d e/f")
	AddSecret("short") // under minSecretLen: ignored

	tests := []struct{ in, want string }{
		{"code ab<c&d e/f refused", "code [redacted] refused"},
		{`{"q":"ab<c&d e/f"}`, `{"q":"[redacted]"}`},
		{"?code=ab%3Cc%26d+e%2Ff", "?code=[redacted]"},
		{"a short word", "a short word"},
	}
	for _, tc := range tests {
		if got := ScrubSecrets(tc.in); got != tc.want {
			t.Errorf("ScrubSecrets(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestScrubWriter(t *testing.T) {
	t.Cleanup(resetSecrets)
	resetSecrets()
	AddSecret("s3cret-value")
	var buf bytes.Buffer
	line := []byte("body: {\"echo\":\"s3cret-value\"}\n")
	n, err := scrubWriter{&buf}.Write(line)
	if err != nil || n != len(line) {
		t.Errorf("Write = %d, %v; want %d, nil", n, err, len(line))
	}
	if got := buf.String(); got != "body: {\"echo\":\"[redacted]\"}\n" {
		t.Errorf("logged %q", got)
	}
}

// TestRedactError: the message and the envelope's details lose the secret;
// the error underneath, which decides the exit code and hint, is kept.
func TestRedactError(t *testing.T) {
	t.Cleanup(resetSecrets)
	resetSecrets()
	AddSecret("SECRETCODE123")
	apiErr := &APIError{StatusCode: 400, Message: "Invalid auth code SECRETCODE123",
		Body: []byte(`{"message":"Invalid auth code SECRETCODE123"}`)}
	err := RedactError(fmt.Errorf("creating transfer: %w", apiErr))

	if got := err.Error(); got != "creating transfer: Invalid auth code [redacted]" {
		t.Errorf("message = %q", got)
	}
	if got, ok := errors.AsType[*APIError](err); !ok || got != apiErr {
		t.Error("the APIError underneath is gone")
	}
	d, ok := err.(detailer)
	if !ok {
		t.Fatal("no details")
	}
	raw, _ := json.Marshal(d.ErrorDetails())
	if bytes.Contains(raw, []byte("SECRETCODE123")) {
		t.Errorf("details = %s", raw)
	}

	plain := errors.New("nothing secret here")
	if RedactError(plain) != plain {
		t.Error("an error without a secret was wrapped")
	}
}

func TestRedactURL(t *testing.T) {
	tests := []struct{ in, want string }{
		{"https://api.name.com/core/v1/domains?page=2", "https://api.name.com/core/v1/domains?page=2"},
		{"https://api.name.com/x?token=abc&page=1", "https://api.name.com/x?token=[redacted]&page=1"},
		{"https://api.name.com/x?access_token=abc&api_key=def&apiTokenName=ci",
			"https://api.name.com/x?access_token=[redacted]&api_key=[redacted]&apiTokenName=ci"},
		{"https://user:pw@api.name.com/x", "https://[redacted]@api.name.com/x"},
		{"https://user@api.name.com/x", "https://[redacted]@api.name.com/x"},
	}
	for _, tc := range tests {
		u, err := url.Parse(tc.in)
		if err != nil {
			t.Fatal(err)
		}
		if got := redactURL(u); got != tc.want {
			t.Errorf("redactURL(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
