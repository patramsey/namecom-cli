package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	coreapigo "github.com/namedotcom/core-api-go"
)

// TestUnusableSuccessBody pins #158: a 2xx whose body cannot be decoded into
// the response type used to surface the decoder's own text, Go type names
// included — `json: cannot unmarshal array into Go value of type struct {
// api.embed; …}`, or `expected a **api.DomainResponsePayload response, but the
// server responded with nothing`.
func TestUnusableSuccessBody(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
	}{
		{"wrong JSON shape", http.StatusOK, `[]`},
		{"wrong field type", http.StatusOK, `{"domainName":5}`},
		{"unparseable date", http.StatusOK, `{"createDate":"not a date"}`},
		{"not JSON", http.StatusOK, `<html>ok</html>`},
		{"truncated JSON", http.StatusOK, `{"domainName":"example.com"`},
		{"204 with no body", http.StatusNoContent, ``},
		{"200 with no body", http.StatusOK, ``},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			c, err := New(Options{BaseURL: srv.URL})
			if err != nil {
				t.Fatalf("api.New: %v", err)
			}
			_, sdkErr := c.SDK().Domains.GetDomain(context.Background(),
				&coreapigo.GetDomainRequest{DomainName: "example.com"})
			if sdkErr == nil {
				t.Fatal("the SDK accepted an unusable body; the fixture exercises nothing")
			}

			for _, conv := range []struct {
				name string
				fn   func(error) error
			}{{"FromSDKError", FromSDKError}, {"NormalizeError", NormalizeError}} {
				got := conv.fn(sdkErr)
				if _, ok := errors.AsType[*UnexpectedResponseError](got); !ok {
					t.Errorf("%s returned %T (%v), want *UnexpectedResponseError", conv.name, got, got)
					continue
				}
				msg := got.Error()
				if !strings.HasPrefix(msg, "unexpected response from the API") {
					t.Errorf("%s message = %q, want it to say the response was unexpected", conv.name, msg)
				}
				for _, leak := range []string{"Go value", "api.", "internal.", "**", "struct {"} {
					if strings.Contains(msg, leak) {
						t.Errorf("%s message leaks Go internals (%q): %q", conv.name, leak, msg)
					}
				}
				// A 2xx is not an API error: it exits 1, not a status-derived code.
				if _, ok := errors.AsType[*APIError](got); ok {
					t.Errorf("%s returned an *APIError for a %d", conv.name, tc.status)
				}
			}
		})
	}
}

// TestNormalizeErrorLeavesLocalDecodeErrorsAlone keeps the #158 conversion to
// errors the SDK produced. A command that fails to parse a file the user gave
// it — `dns import`, `--contacts-file` — wraps the decoder's error, and calling
// that an unexpected API response would send the user looking in the wrong
// place.
func TestNormalizeErrorLeavesLocalDecodeErrorsAlone(t *testing.T) {
	var target []string
	local := json.Unmarshal([]byte(`{`), &target)
	err := fmt.Errorf("parsing import file: %w", local)
	if got := NormalizeError(err); got != err {
		t.Errorf("NormalizeError rewrote a local parse error: %v", got)
	}
}

// TestNormalizeErrorKeepsContextOnEmptyBody: an SDK "responded with nothing"
// error wrapped in a caller's context keeps that context.
func TestNormalizeErrorKeepsContextOnEmptyBody(t *testing.T) {
	inner := errors.New("expected a **api.DomainResponsePayload response, but the server responded with nothing")
	got := NormalizeError(fmt.Errorf("fetching domain: %w", inner))
	if _, ok := errors.AsType[*UnexpectedResponseError](got); !ok {
		t.Fatalf("NormalizeError returned %T, want an *UnexpectedResponseError in the chain", got)
	}
	msg := got.Error()
	if !strings.HasPrefix(msg, "fetching domain: unexpected response from the API") || strings.Contains(msg, "**api.") {
		t.Errorf("message = %q, want the context kept and the type name gone", msg)
	}
}
