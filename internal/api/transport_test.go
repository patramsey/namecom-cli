package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
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
	// Two ways a wait can be cut short, and they now end differently.
	//
	// When the wait plainly cannot fit the deadline, the transport no longer
	// sleeps into cancellation — it hands back the response it already has, so
	// a 429 stays a 429 instead of becoming "context deadline exceeded". When
	// the wait does fit and the context is cancelled underneath it, the sleep
	// must still abandon the backoff rather than run it to completion.

	t.Run("a wait that cannot fit returns the response, not a context error", func(t *testing.T) {
		var calls int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			atomic.AddInt32(&calls, 1)
			w.Header().Set("Retry-After", "30")
			w.WriteHeader(http.StatusTooManyRequests)
		}))
		defer srv.Close()

		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
		start := time.Now()
		resp, err := newTestClient(http.DefaultTransport).Do(req)
		elapsed := time.Since(start)

		if err != nil {
			t.Fatalf("got a transport error instead of the 429: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusTooManyRequests {
			t.Errorf("status = %d, want the 429 preserved", resp.StatusCode)
		}
		if elapsed > 2*time.Second {
			t.Errorf("did not short-circuit the backoff: took %v", elapsed)
		}
		if got := atomic.LoadInt32(&calls); got != 1 {
			t.Errorf("calls = %d, want 1 — no retry fits in the budget", got)
		}
	})

	t.Run("cancelling during an eligible backoff abandons it", func(t *testing.T) {
		// No deadline, so the wait is eligible and the transport commits to
		// sleeping. Checking only err != nil would pass even if sleep() ignored
		// ctx entirely and blocked for the full schedule — verified: replacing
		// the ctx.Done() select with a bare <-timer.C left this green while the
		// call took 30s instead of 0.1s. The elapsed-time bound is the assertion
		// that actually holds sleep() to account.
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Retry-After", "30")
			w.WriteHeader(http.StatusTooManyRequests)
		}))
		defer srv.Close()

		ctx, cancel := context.WithCancel(context.Background())
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
		time.AfterFunc(100*time.Millisecond, cancel)
		defer cancel()

		start := time.Now()
		_, err := newTestClient(http.DefaultTransport).Do(req)
		elapsed := time.Since(start)

		if err == nil {
			t.Fatal("expected an error from the cancelled context")
		}
		if elapsed > 2*time.Second {
			t.Errorf("cancellation did not interrupt the backoff: took %v", elapsed)
		}
		if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("expected a context error, got %v", err)
		}
	})
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

// failingTransport returns network errors for the first `failures` calls,
// then delegates to base for subsequent calls.
type failingTransport struct {
	base     http.RoundTripper
	failures int32
	calls    atomic.Int32
}

func (ft *failingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	n := ft.calls.Add(1)
	if n <= ft.failures {
		// Return the shape a real connection failure has — *net.OpError, which
		// satisfies net.Error. A plain fmt.Errorf is indistinguishable from a
		// permanent client-side construction failure (an invalid header, a bad
		// scheme), which the transport deliberately does NOT retry.
		return nil, &net.OpError{
			Op:  "dial",
			Net: "tcp",
			Err: fmt.Errorf("simulated connection error (call %d)", n),
		}
	}
	return ft.base.RoundTrip(req)
}

func TestNetworkErrorRetriedForIdempotentGET(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	ft := &failingTransport{base: http.DefaultTransport, failures: 2}
	client := newTestClient(ft)
	resp, err := client.Get(srv.URL)
	if err != nil {
		t.Fatalf("expected success after retries, got: %v", err)
	}
	resp.Body.Close()
	if got := ft.calls.Load(); got != 3 {
		t.Errorf("calls = %d, want 3 (2 network errors then success)", got)
	}
}

func TestNetworkErrorNotRetriedForPOST(t *testing.T) {
	ft := &failingTransport{failures: 99} // always fail
	client := newTestClient(ft)
	req, _ := http.NewRequest(http.MethodPost, "http://127.0.0.1:1", strings.NewReader("{}"))
	_, err := client.Do(req)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if got := ft.calls.Load(); got != 1 {
		t.Errorf("calls = %d, want 1 (POST must not retry on network errors)", got)
	}
}

func TestParseRetryAfter(t *testing.T) {
	tests := []struct {
		input   string
		wantNil bool
		wantSec int
	}{
		{"5", false, 5},
		{"0", false, 0},
		{"", true, 0},
		{"Wed, 21 Oct 2015 07:28:00 GMT", true, 0}, // HTTP-date not supported
		{"not-a-number", true, 0},
		{"-1", true, 0}, // negative: rejected by secs >= 0 guard
	}
	for _, tt := range tests {
		d := parseRetryAfter(tt.input)
		if tt.wantNil {
			if d != nil {
				t.Errorf("parseRetryAfter(%q) = %v, want nil", tt.input, *d)
			}
		} else {
			if d == nil {
				t.Errorf("parseRetryAfter(%q) = nil, want %v", tt.input, time.Duration(tt.wantSec)*time.Second)
			} else if *d != time.Duration(tt.wantSec)*time.Second {
				t.Errorf("parseRetryAfter(%q) = %v, want %v", tt.input, *d, time.Duration(tt.wantSec)*time.Second)
			}
		}
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

// TestNoRetryOnPermanentRequestError guards wasted backoff. The transport
// retries ANY error from the underlying RoundTrip when the method is
// idempotent — but client-side request-construction failures (an invalid
// header value, an unsupported scheme, a bad method) can never succeed on a
// second attempt. Retrying them burns the full backoff schedule before
// surfacing a failure the caller could have had immediately: a typo'd
// `namecom api --header` took over 7 seconds to report.
func TestNoRetryOnPermanentRequestError(t *testing.T) {
	var attempts int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts++
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c, err := New(Options{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("api.New: %v", err)
	}

	req, err := http.NewRequest(http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	// An invalid header value: net/http refuses to write this, every time.
	req.Header["X-Bad"] = []string{"value\r\nX-Injected: yes"}

	start := time.Now()
	_, err = c.HTTPClient().Do(req)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected an error for an invalid header value")
	}
	if attempts != 0 {
		t.Errorf("request should never have reached the server, got %d attempts", attempts)
	}
	if elapsed > 500*time.Millisecond {
		t.Errorf("permanent request error was retried with backoff: took %v", elapsed)
	}
}

// TestStillRetriesTransientNetworkError is the counterpart to
// TestNoRetryOnPermanentRequestError: narrowing the retry condition must not
// disable retries for the failures it exists to absorb. A connection refused
// surfaces as *net.OpError (a net.Error) and must still be retried.
func TestStillRetriesTransientNetworkError(t *testing.T) {
	// Bind then immediately close, so the port is almost certainly refusing.
	dead := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	deadURL := dead.URL
	dead.Close()

	var retries int
	c, err := New(Options{
		BaseURL:    deadURL,
		MaxRetries: 2,
		OnRetry:    func(int, time.Duration) { retries++ },
	})
	if err != nil {
		t.Fatalf("api.New: %v", err)
	}

	req, err := http.NewRequest(http.MethodGet, deadURL, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	if _, err := c.HTTPClient().Do(req); err == nil {
		t.Fatal("expected a connection error against a closed listener")
	}
	if retries == 0 {
		t.Error("a transient network error must still be retried")
	}
}

// TestMaxRetriesCanBeDisabled covers the gap in Options.MaxRetries: 0 means
// "use the default", so there was no value that meant "don't retry". Callers
// wanting fail-fast behaviour — a health check, a test, an interactive command
// that would rather surface an error than stall — had no way to ask for it.
// A negative value now disables retries entirely.
func TestMaxRetriesCanBeDisabled(t *testing.T) {
	tests := []struct {
		name        string
		maxRetries  int
		wantCalls   int32
		description string
	}{
		{"zero means default", 0, 4, "1 attempt + 3 retries"},
		{"explicit count", 1, 2, "1 attempt + 1 retry"},
		{"negative disables", -1, 1, "single attempt, no retries"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				w.WriteHeader(http.StatusInternalServerError) // retryable for GET
			}))
			defer srv.Close()

			c, err := New(Options{BaseURL: srv.URL, MaxRetries: tc.maxRetries})
			if err != nil {
				t.Fatalf("api.New: %v", err)
			}
			resp, err := c.HTTPClient().Get(srv.URL)
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			_ = resp.Body.Close()

			if got := calls.Load(); got != tc.wantCalls {
				t.Errorf("MaxRetries=%d: got %d call(s), want %d (%s)",
					tc.maxRetries, got, tc.wantCalls, tc.description)
			}
		})
	}
}

// TestRetriesEOF covers a regression introduced when retries were narrowed to
// net.Error: a peer closing a FRESH connection surfaces as bare io.EOF, which
// does not satisfy net.Error. That is the single most common transient failure
// in practice — a load balancer draining, a server restarting, a proxy
// recycling — and net/http's own internal retry does not absorb it either
// (shouldRetryRequest short-circuits unless the connection was reused).
//
// Failing to retry a transient error costs reliability; retrying a permanent
// one only costs time. The classification must not be so tight that the common
// case falls through.
func TestRetriesEOF(t *testing.T) {
	// Accept connections and close them immediately for the first N attempts,
	// then hand off to a real handler.
	var attempts atomic.Int32
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = ln.Close() }()

	go func() {
		for {
			c, aerr := ln.Accept()
			if aerr != nil {
				return
			}
			if attempts.Add(1) <= 2 {
				_ = c.Close() // peer sees EOF
				continue
			}
			go func(conn net.Conn) {
				defer func() { _ = conn.Close() }()
				buf := make([]byte, 1024)
				_, _ = conn.Read(buf)
				_, _ = conn.Write([]byte("HTTP/1.1 200 OK\r\nContent-Length: 2\r\n\r\n{}"))
			}(c)
		}
	}()

	base := "http://" + ln.Addr().String()
	c, err := New(Options{BaseURL: base, MaxRetries: 4})
	if err != nil {
		t.Fatalf("api.New: %v", err)
	}
	resp, err := c.HTTPClient().Get(base + "/core/v1/domains")
	if err != nil {
		t.Fatalf("an EOF on a fresh connection must be retried, got: %v", err)
	}
	_ = resp.Body.Close()

	if got := attempts.Load(); got < 3 {
		t.Errorf("expected retries past the closed connections, got %d attempt(s)", got)
	}
}

// TestRateLimiterPacesRequests covers the client-side rate limiter, which had
// no test at all: newTestClient installs rate.Inf, so every existing test
// bypasses it. The limiter exists to stay under the API's published ceiling and
// to leave headroom for other consumers on the same account — deleting it would
// have been invisible.
//
// Uses a deliberately high RPS so the test stays fast while the pacing is still
// measurable.
func TestRateLimiterPacesRequests(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// 50 rps with a burst of 1: the first request goes immediately, each
	// subsequent one waits ~20ms.
	const rps, burst, n = 50, 1, 5
	c, err := New(Options{BaseURL: srv.URL, RPS: rps, Burst: burst})
	if err != nil {
		t.Fatalf("api.New: %v", err)
	}

	start := time.Now()
	for i := 0; i < n; i++ {
		resp, gerr := c.HTTPClient().Get(srv.URL)
		if gerr != nil {
			t.Fatalf("request %d: %v", i, gerr)
		}
		_ = resp.Body.Close()
	}
	elapsed := time.Since(start)

	if got := hits.Load(); got != n {
		t.Fatalf("expected %d requests to reach the server, got %d", n, got)
	}
	// (n-1) intervals at 1/rps each, with slack for scheduling.
	minExpected := time.Duration(n-1) * time.Second / rps / 2
	if elapsed < minExpected {
		t.Errorf("requests were not rate limited: %d requests in %v, expected at least %v",
			n, elapsed, minExpected)
	}
}

// TestRetryAfterDoesNotOutliveDeadline guards the 429 path against burning the
// caller's whole time budget on a wait the server asked for.
//
// A 429 carrying "Retry-After: 600" used to be slept on until the client
// timeout fired. The user waited the full 30s and got "context deadline
// exceeded (Client.Timeout exceeded while awaiting headers)" — exit 1, a
// transport error, with the rate-limit answer the server had already given
// thrown away. Handing the response back keeps the status, the body, and the
// exit code 5 that scripts branch on.
func TestRetryAfterDoesNotOutliveDeadline(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.Header().Set("Retry-After", "600")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"message":"slow down"}`))
	}))
	defer srv.Close()

	tr := &retryTransport{
		base:       http.DefaultTransport,
		limiter:    rate.NewLimiter(rate.Inf, 1),
		maxRetries: 3,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)

	start := time.Now()
	resp, err := tr.RoundTrip(req)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("RoundTrip returned an error instead of the 429: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Errorf("status = %d, want 429 preserved", resp.StatusCode)
	}
	if elapsed > time.Second {
		t.Errorf("took %s; it should return immediately rather than sleep into the deadline", elapsed)
	}
	if calls != 1 {
		t.Errorf("server saw %d calls, want 1 — no retry fits in the budget", calls)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "slow down") {
		t.Errorf("response body was consumed: %q", body)
	}
}

// TestRetryAfterHonouredWhenItFits pins the other side: a short Retry-After
// inside the budget is still respected, so this fix did not disable the header.
func TestRetryAfterHonouredWhenItFits(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		if calls == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	tr := &retryTransport{
		base:       http.DefaultTransport,
		limiter:    rate.NewLimiter(rate.Inf, 1),
		maxRetries: 3,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)

	resp, err := tr.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200 after the retry", resp.StatusCode)
	}
	if calls != 2 {
		t.Errorf("server saw %d calls, want 2", calls)
	}
}

// TestRetryAfterClamped pins the cap on a server-supplied wait for the case
// with no deadline at all. Computed backoff was always bounded by maxBackoff;
// the header went through unbounded, so one response could park the process
// for as long as it liked.
func TestRetryAfterClamped(t *testing.T) {
	tr := &retryTransport{}
	huge := 12 * time.Hour
	if got := tr.backoffDelay(0, &huge); got != maxBackoff {
		t.Errorf("backoffDelay with Retry-After 12h = %s, want it clamped to %s", got, maxBackoff)
	}
	short := 2 * time.Second
	if got := tr.backoffDelay(0, &short); got != short {
		t.Errorf("backoffDelay with Retry-After 2s = %s, want it honoured", got)
	}
}

// TestNetworkRetryRespectsDeadline covers the deadline check on the
// network-error path, the sibling of the one on the status path.
//
// Retrying a connection failure is only worth doing if there is time left to
// make the attempt. Sleeping through the remaining budget first turns a
// concrete "connection refused" into a context error that says nothing about
// why the call failed.
func TestNetworkRetryRespectsDeadline(t *testing.T) {
	// A port with nothing listening: RoundTrip fails immediately and the error
	// is transient by the transport's classification, so a retry is eligible.
	tr := &retryTransport{
		base:       http.DefaultTransport,
		limiter:    rate.NewLimiter(rate.Inf, 1),
		maxRetries: 3,
		baseDelay:  10 * time.Second, // far longer than the budget below
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1:1", nil)

	start := time.Now()
	_, err := tr.RoundTrip(req)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected the connection error to be returned")
	}
	if elapsed > time.Second {
		t.Errorf("took %s; the wait does not fit the deadline and should not be taken", elapsed)
	}
	// The underlying failure must survive rather than being replaced by a
	// context error produced by our own sleep.
	if errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("connection error was masked by our own backoff: %v", err)
	}
}
