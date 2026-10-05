package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// outcomeServer answers every request with status, and records the
// idempotency key of the last one.
func outcomeServer(t *testing.T, status int, key *string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*key = r.Header.Get("X-Idempotency-Key")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"message":"Internal Error"}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// send makes one request through c the way `namecom api` does, and returns
// its error as the CLI would see it.
func send(t *testing.T, c *Client, method, path string) error {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), method, c.BaseURL()+path, strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Prepare(req); err != nil {
		t.Fatal(err)
	}
	resp, err := c.HTTPClient().Do(req) //nolint:gosec // the test's own stub
	if err != nil {
		return NormalizeError(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		return parseError(resp)
	}
	return nil
}

// TestOutcomeUnknown pins #243: a write the server may have carried out —
// a 5xx, or a failure after sending — is wrapped with the idempotency key it
// was sent with, and nothing else is. The method decides what is a write, not
// whether the command marked it (#247).
func TestOutcomeUnknown(t *testing.T) {
	tests := []struct {
		name    string
		method  string
		status  int
		unknown bool
		keyed   bool
	}{
		{"POST 500", http.MethodPost, 500, true, true},
		{"DELETE 503", http.MethodDelete, 503, true, true},
		{"PATCH 502 carries no key", http.MethodPatch, 502, true, false},
		{"GET 500 is a read", http.MethodGet, 500, false, false},
		{"POST 400 was refused", http.MethodPost, 400, false, false},
		{"POST 429 was not processed", http.MethodPost, 429, false, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var sentKey string
			srv := outcomeServer(t, tc.status, &sentKey)
			c, err := New(Options{BaseURL: srv.URL, MaxRetries: -1})
			if err != nil {
				t.Fatal(err)
			}
			got := c.OutcomeUnknown(send(t, c, tc.method, "/core/v1/x"))

			u, ok := errors.AsType[*OutcomeUnknownError](got)
			if ok != tc.unknown {
				t.Fatalf("outcome unknown = %v, want %v (err %v)", ok, tc.unknown, got)
			}
			if !ok {
				return
			}
			if tc.keyed && (u.Key == "" || u.Key != sentKey) {
				t.Errorf("Key = %q, want the key that was sent, %q", u.Key, sentKey)
			}
			if !tc.keyed && u.Key != "" {
				t.Errorf("Key = %q, want none for %s", u.Key, tc.method)
			}
			if tc.keyed && !strings.Contains(u.UserHint(), "re-run with --idempotency-key "+sentKey) {
				t.Errorf("hint %q does not name the key", u.UserHint())
			}
			if apiErr, ok := errors.AsType[*APIError](got); !ok || !apiErr.Write {
				t.Error("the unmarked write's APIError should be marked a write, for its own hint")
			}
		})
	}
}

// TestOutcomeUnknown_Network covers the no-response half: a write that timed
// out after it was sent is unknown; one whose connection was refused never
// left, and is not.
func TestOutcomeUnknown_Network(t *testing.T) {
	t.Run("timeout", func(t *testing.T) {
		done := make(chan struct{})
		srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			select {
			case <-done:
			case <-r.Context().Done():
			}
		}))
		t.Cleanup(srv.Close)
		t.Cleanup(func() { close(done) }) // runs first, releasing the handler
		c, err := New(Options{BaseURL: srv.URL, MaxRetries: -1, Timeout: 50 * time.Millisecond})
		if err != nil {
			t.Fatal(err)
		}
		got := c.OutcomeUnknown(send(t, c, http.MethodPost, "/core/v1/x"))
		u, ok := errors.AsType[*OutcomeUnknownError](got)
		if !ok || u.Key == "" {
			t.Fatalf("a timed-out POST should be outcome-unknown with its key, got %v", got)
		}
		if _, ok := errors.AsType[*NetworkError](got); !ok {
			t.Errorf("the wrapped error should still be the NetworkError, got %T", errors.Unwrap(got))
		}
	})
	t.Run("connection refused", func(t *testing.T) {
		c, err := New(Options{BaseURL: "http://127.0.0.1:1", MaxRetries: -1})
		if err != nil {
			t.Fatal(err)
		}
		got := c.OutcomeUnknown(send(t, c, http.MethodPost, "/core/v1/x"))
		if _, ok := errors.AsType[*OutcomeUnknownError](got); ok {
			t.Errorf("a refused connection sent nothing, so its outcome is known: %v", got)
		}
	})
}

// TestOutcomeUnknown_LaterRequestClears: the error a command returns is its
// last request's, so a later request that succeeded clears an earlier
// failed write rather than lending its key to an unrelated error.
func TestOutcomeUnknown_LaterRequestClears(t *testing.T) {
	status := 500
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
	}))
	t.Cleanup(srv.Close)
	c, err := New(Options{BaseURL: srv.URL, MaxRetries: -1})
	if err != nil {
		t.Fatal(err)
	}
	failed := send(t, c, http.MethodPost, "/core/v1/x")
	status = 200
	if err := send(t, c, http.MethodGet, "/core/v1/y"); err != nil {
		t.Fatal(err)
	}
	if _, ok := errors.AsType[*OutcomeUnknownError](c.OutcomeUnknown(failed)); ok {
		t.Error("an older write's 5xx was matched after a later request succeeded")
	}
}
