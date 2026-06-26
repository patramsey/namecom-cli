package api

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/time/rate"
)

func newTestClient(rt http.RoundTripper) *http.Client {
	return &http.Client{Transport: &retryTransport{
		base:       rt,
		limiter:    rate.NewLimiter(rate.Inf, 1), // no rate limiting in tests
		maxRetries: 3,
		baseDelay:  time.Millisecond, // keep retry tests fast
	}}
}

func TestRetryOn429(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if atomic.AddInt32(&calls, 1) < 3 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	resp, err := newTestClient(http.DefaultTransport).Get(srv.URL)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	if got := atomic.LoadInt32(&calls); got != 3 {
		t.Errorf("calls = %d, want 3 (two 429s then success)", got)
	}
}

func TestRetry5xxIdempotentOnly(t *testing.T) {
	tests := []struct {
		name      string
		method    string
		idemKey   bool
		wantCalls int32
	}{
		{name: "GET retries", method: http.MethodGet, wantCalls: 4}, // 1 + 3 retries
		// POST is never retried on 5xx regardless of idempotency key — only a
		// handful of name.com endpoints document key support, so we can't
		// safely assume the server will de-duplicate an arbitrary POST.
		{name: "POST no key, no retry", method: http.MethodPost, wantCalls: 1},
		{name: "POST with idempotency key, no retry", method: http.MethodPost, idemKey: true, wantCalls: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var calls int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				atomic.AddInt32(&calls, 1)
				w.WriteHeader(http.StatusInternalServerError)
			}))
			defer srv.Close()

			req, _ := http.NewRequest(tt.method, srv.URL, strings.NewReader("{}"))
			if tt.idemKey {
				req.Header.Set("X-Idempotency-Key", "abc")
			}
			resp, err := newTestClient(http.DefaultTransport).Do(req)
			if err != nil {
				t.Fatalf("Do: %v", err)
			}
			resp.Body.Close()
			if got := atomic.LoadInt32(&calls); got != tt.wantCalls {
				t.Errorf("calls = %d, want %d", got, tt.wantCalls)
			}
		})
	}
}

func TestNoRetryOn4xx(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()

	resp, err := newTestClient(http.DefaultTransport).Get(srv.URL)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	resp.Body.Close()
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("calls = %d, want 1 (4xx not retried)", got)
	}
}

func TestContextCancelStopsRetry(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "30") // long, so we cancel during backoff
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
	_, err := newTestClient(http.DefaultTransport).Do(req)
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
}

func TestIdempotent(t *testing.T) {
	cases := map[string]struct {
		method  string
		idemKey bool
		want    bool
	}{
		"GET":      {http.MethodGet, false, true},
		"DELETE":   {http.MethodDelete, false, true},
		"PUT":      {http.MethodPut, false, true},
		"POST":     {http.MethodPost, false, false},
		"POST+key": {http.MethodPost, true, false}, // key does not make POST idempotent for 5xx
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			req, _ := http.NewRequest(c.method, "http://x", nil)
			if c.idemKey {
				req.Header.Set("X-Idempotency-Key", "k")
			}
			if got := idempotent(req); got != c.want {
				t.Errorf("idempotent(%s) = %v, want %v", name, got, c.want)
			}
		})
	}
}

func TestParseError(t *testing.T) {
	body := `{"message":"Domain not found","details":"example.com"}`
	resp := &http.Response{
		StatusCode: http.StatusNotFound,
		Body:       io.NopCloser(strings.NewReader(body)),
	}
	e := parseError(resp)
	if e.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d", e.StatusCode)
	}
	if e.Message != "Domain not found" || e.Details != "example.com" {
		t.Errorf("unexpected: %+v", e)
	}
	if !strings.Contains(e.Error(), "Domain not found") {
		t.Errorf("Error() = %q", e.Error())
	}
}

func TestParseErrorUnauthorizedHint(t *testing.T) {
	resp := &http.Response{
		StatusCode: http.StatusUnauthorized,
		Body:       io.NopCloser(strings.NewReader(`{"message":"unauthorized"}`)),
	}
	e := parseError(resp)
	if !strings.Contains(e.Details, "sandbox") {
		t.Errorf("expected sandbox hint, got %q", e.Details)
	}
}
