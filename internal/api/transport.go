package api

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"math"
	"math/rand"
	"net/http"
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
	logw       io.Writer // nil = no debug logging; non-nil = write request/response log here
	// baseDelay is the first-retry backoff; subsequent retries double it up to
	// a 30s cap. Zero means the production default (1s). Tests set it small.
	baseDelay time.Duration
	// onRetry, when set, is called just before sleeping between attempts so the
	// caller can surface "retrying…" feedback to the user.
	onRetry func(attempt int, delay time.Duration)
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

// idempotent reports whether retrying req on a 5xx is safe. GET/HEAD/PUT/DELETE
// are idempotent by HTTP definition. POST is NOT included here: only a handful
// of name.com endpoints document X-Idempotency-Key support, so we cannot safely
// retry arbitrary POST requests on 5xx — the server may have already committed
// the operation. 429 retries are handled separately (always safe: rejected means
// not processed).
func idempotent(req *http.Request) bool {
	switch req.Method {
	case http.MethodGet, http.MethodHead, http.MethodPut, http.MethodDelete:
		return true
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
				delay := t.backoffDelay(attempt, nil)
				if t.onRetry != nil {
					t.onRetry(attempt+1, delay)
				}
				if !t.sleep(ctx, delay) {
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
				delay := t.backoffDelay(attempt, retryAfter)
				if t.onRetry != nil {
					t.onRetry(attempt+1, delay)
				}
				if !t.sleep(ctx, delay) {
					return nil, ctx.Err()
				}
				continue
			}
		}
		return resp, nil
	}
	return lastResp, lastErr
}

// backoffDelay computes the wait duration for the next attempt. If retryAfter
// is non-nil it is honored; otherwise exponential backoff with jitter is used.
func (t *retryTransport) backoffDelay(attempt int, retryAfter *time.Duration) time.Duration {
	if retryAfter != nil {
		return *retryAfter
	}
	unit := t.baseDelay
	if unit == 0 {
		unit = time.Second
	}
	const maxBackoff = 30 * time.Second
	base := min(time.Duration(math.Pow(2, float64(attempt)))*unit, maxBackoff)
	jitter := time.Duration(rand.Int63n(int64(base)/5 + 1)) //nolint:gosec // jitter, not crypto
	return base - base/10 + jitter
}

// sleep waits for d, returning false if ctx is cancelled first.
func (t *retryTransport) sleep(ctx context.Context, d time.Duration) bool {
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
	if t.logw == nil {
		return
	}
	prefix := "→"
	if attempt > 0 {
		prefix = fmt.Sprintf("→ (retry %d)", attempt)
	}
	// Authorization is intentionally never logged.
	fmt.Fprintf(t.logw, "%s %s %s\n", prefix, req.Method, req.URL.String())
	if req.Body != nil && req.GetBody != nil {
		body, err := req.GetBody()
		if err == nil {
			data, _ := io.ReadAll(io.LimitReader(body, 4096))
			_ = body.Close()
			if len(data) > 0 {
				fmt.Fprintf(t.logw, "  body: %s\n", data)
			}
		}
	}
}

func (t *retryTransport) logResponse(resp *http.Response, _ int) {
	if t.logw == nil {
		return
	}
	fmt.Fprintf(t.logw, "← %s\n", resp.Status)
	// Buffer the body so we can log it and still hand it to the caller.
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	_ = resp.Body.Close()
	resp.Body = io.NopCloser(bytes.NewReader(data))
	if len(data) > 0 {
		fmt.Fprintf(t.logw, "  body: %s\n", data)
	}
}
