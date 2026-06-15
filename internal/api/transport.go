package api

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"math"
	"math/rand"
	"net/http"
	"os"
	"strconv"
	"time"

	"golang.org/x/time/rate"
)

// retryTransport wraps an http.RoundTripper with client-side rate limiting and
// bounded retries. The name.com API permits 20 req/s; we deliberately run well
// under that (see defaultRPS) to leave headroom for other consumers on the
// same account and for burst from interactive use.
type retryTransport struct {
	base       http.RoundTripper
	limiter    *rate.Limiter
	maxRetries int
	debug      bool
	// baseDelay is the first-retry backoff; subsequent retries double it up to
	// a 30s cap. Zero means the production default (1s). Tests set it small.
	baseDelay time.Duration
}

// retryableStatus reports whether a status code warrants a retry.
func retryableStatus(code int) bool {
	switch code {
	case http.StatusTooManyRequests, // 429
		http.StatusInternalServerError, // 500
		http.StatusBadGateway,          // 502
		http.StatusServiceUnavailable,  // 503
		http.StatusGatewayTimeout:      // 504
		return true
	}
	return false
}

// idempotent reports whether retrying req is safe. GET/HEAD/PUT/DELETE are
// idempotent by definition; a POST is retryable only when it carries an
// idempotency key, which the API uses to de-duplicate retried mutations.
func idempotent(req *http.Request) bool {
	switch req.Method {
	case http.MethodGet, http.MethodHead, http.MethodPut, http.MethodDelete:
		return true
	case http.MethodPost:
		return req.Header.Get("X-Idempotency-Key") != ""
	}
	return false
}

func (t *retryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	ctx := req.Context()

	// Capture the body so it can be replayed on each attempt. The generated
	// client builds requests from byte readers, so GetBody is normally set;
	// fall back to buffering if not.
	if req.Body != nil && req.GetBody == nil {
		buf, err := io.ReadAll(req.Body)
		_ = req.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("buffering request body: %w", err)
		}
		req.GetBody = func() (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader(buf)), nil
		}
	}

	var lastResp *http.Response
	var lastErr error
	for attempt := 0; attempt <= t.maxRetries; attempt++ {
		// Respect the rate limit before every attempt.
		if err := t.limiter.Wait(ctx); err != nil {
			return nil, err
		}

		// Rewind the body for this attempt.
		if req.GetBody != nil {
			body, err := req.GetBody()
			if err != nil {
				return nil, fmt.Errorf("rewinding request body: %w", err)
			}
			req.Body = body
		}

		t.logRequest(req, attempt)
		resp, err := t.base.RoundTrip(req)
		lastResp, lastErr = resp, err

		// Network/transport error: retry only if the request is idempotent.
		if err != nil {
			if attempt < t.maxRetries && idempotent(req) {
				if !t.sleepBackoff(ctx, attempt, nil) {
					return nil, ctx.Err()
				}
				continue
			}
			return nil, err
		}

		t.logResponse(resp, attempt)

		// Decide whether to retry based on status.
		if attempt < t.maxRetries && retryableStatus(resp.StatusCode) {
			// 429 is always retryable; 5xx only for idempotent requests.
			if resp.StatusCode == http.StatusTooManyRequests || idempotent(req) {
				retryAfter := parseRetryAfter(resp.Header.Get("Retry-After"))
				// Drain and close so the connection can be reused.
				_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
				_ = resp.Body.Close()
				if !t.sleepBackoff(ctx, attempt, retryAfter) {
					return nil, ctx.Err()
				}
				continue
			}
		}
		return resp, nil
	}
	return lastResp, lastErr
}

// sleepBackoff waits before the next attempt. If retryAfter is non-nil it is
// honored; otherwise an exponential backoff with jitter is used. Returns false
// if the context is cancelled while waiting.
func (t *retryTransport) sleepBackoff(ctx context.Context, attempt int, retryAfter *time.Duration) bool {
	var d time.Duration
	if retryAfter != nil {
		d = *retryAfter
	} else {
		unit := t.baseDelay
		if unit == 0 {
			unit = time.Second
		}
		const maxBackoff = 30 * time.Second
		// unit, 2*unit, 4*unit, ... capped, with ±20% jitter.
		base := min(time.Duration(math.Pow(2, float64(attempt)))*unit, maxBackoff)
		jitter := time.Duration(rand.Int63n(int64(base)/5 + 1)) //nolint:gosec // jitter, not crypto
		d = base - base/10 + jitter
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// parseRetryAfter parses a Retry-After header value (delta-seconds form only,
// which is what the API emits). Returns nil if absent or unparseable.
func parseRetryAfter(v string) *time.Duration {
	if v == "" {
		return nil
	}
	if secs, err := strconv.Atoi(v); err == nil && secs >= 0 {
		d := time.Duration(secs) * time.Second
		return &d
	}
	return nil
}

func (t *retryTransport) logRequest(req *http.Request, attempt int) {
	if !t.debug {
		return
	}
	prefix := "→"
	if attempt > 0 {
		prefix = fmt.Sprintf("→ (retry %d)", attempt)
	}
	// Authorization is intentionally never logged.
	fmt.Fprintf(os.Stderr, "%s %s %s\n", prefix, req.Method, req.URL.String())
}

func (t *retryTransport) logResponse(resp *http.Response, attempt int) {
	if !t.debug {
		return
	}
	fmt.Fprintf(os.Stderr, "← %s\n", resp.Status)
	_ = attempt
}
