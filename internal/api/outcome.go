package api

import (
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
)

// OutcomeUnknownError wraps the error from a request that changes something —
// any method but GET and HEAD — when the server may have carried it out: it
// answered 5xx, or the connection failed or timed out after the request was
// sent (#243). Such a write is not retried on a 5xx, which is right (a retried
// register can charge twice), but it left the user with "Internal Error" and no
// way to make a re-run recognisable as a retry. This names the idempotency key
// the request carried, so it can be pinned with --idempotency-key.
//
// It exits 6, apart from other failures, because what a script does next —
// check, then retry with the same key — differs from what it does for an
// error that certainly changed nothing.
type OutcomeUnknownError struct {
	// Method and Path are the write's request line.
	Method, Path string
	// Key is the X-Idempotency-Key the request was sent with, or "" for a
	// method that carries none (PATCH; see isWrite).
	Key string
	Err error
}

func (e *OutcomeUnknownError) Error() string { return e.Err.Error() }
func (e *OutcomeUnknownError) Unwrap() error { return e.Err }

// IdempotencyKey is the key the write was sent with, for the error envelope's
// "idempotencyKey" field.
func (e *OutcomeUnknownError) IdempotencyKey() string { return e.Key }

// UserHint says the outcome is unknown and how to retry it. It replaces the
// hint of the error it wraps, which said only half of this.
//
// Re-running with the key is safe only where the endpoint honours it, and
// most ignore it (see the README's "Idempotency keys"), so the hint does not
// promise that the key alone prevents a duplicate.
func (e *OutcomeUnknownError) UserHint() string {
	if e.Key == "" {
		return "outcome unknown: the change may or may not have been made — check before retrying"
	}
	return fmt.Sprintf("outcome unknown; re-run with --idempotency-key %s — but check first whether the change was made, since most endpoints ignore the key", e.Key)
}

// changesState reports whether a request with this method may change
// something on the server. It decides what counts as a write for
// OutcomeUnknownError, from the request itself: a write that bypassed
// cmdutil.RunWrite and api.MarkWrite was otherwise treated as a read, and told
// a 5xx was "safe to retry" (#247).
func changesState(method string) bool {
	return method != http.MethodGet && method != http.MethodHead
}

// writeLog remembers whether the most recent request was a write whose outcome
// is unknown. retryTransport records every request's final result; the error a
// command returns comes from its last request, so that is the one compared.
type writeLog struct {
	mu   sync.Mutex
	last *OutcomeUnknownError // nil when the last request was not one
	// status is last's final HTTP status, or 0 when it got no response.
	status int
}

// record notes the final result of req: a write's 5xx or post-send failure,
// or anything else, which clears the previous entry.
func (l *writeLog) record(req *http.Request, resp *http.Response, err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.last, l.status = nil, 0
	if !changesState(req.Method) {
		return
	}
	switch {
	case err != nil && maybeSent(err):
	case err == nil && resp != nil && resp.StatusCode >= 500:
		l.status = resp.StatusCode
	default:
		return
	}
	key := ""
	if isWrite(req.Method) {
		key = req.Header.Get("X-Idempotency-Key")
	}
	l.last = &OutcomeUnknownError{Method: req.Method, Path: req.URL.Path, Key: key}
}

// maybeSent reports whether a request that failed with err may have reached
// the server. A connection that was never made — refused, unresolvable, or
// rejected for its certificate — sent nothing. Anything else, a timeout
// waiting for the response or a reset mid-request, may have.
func maybeSent(err error) bool {
	if opErr, ok := errors.AsType[*net.OpError](err); ok && opErr.Op == "dial" {
		return false
	}
	if _, ok := errors.AsType[*net.DNSError](err); ok {
		return false
	}
	if _, ok := errors.AsType[*tls.RecordHeaderError](err); ok {
		return false
	}
	return !certificateErr(err)
}

// OutcomeUnknown returns err wrapped in an *OutcomeUnknownError when it is the
// failure of a write whose outcome is unknown — a 5xx, or a network failure
// after sending, from the last request this client made — and err unchanged
// otherwise. A matching *APIError is also marked as a write, so its own hint
// agrees, whether or not the command marked it.
func (c *Client) OutcomeUnknown(err error) error {
	if c == nil || c.writes == nil || err == nil {
		return err
	}
	if _, ok := errors.AsType[*OutcomeUnknownError](err); ok {
		return err
	}
	c.writes.mu.Lock()
	last, status := c.writes.last, c.writes.status
	c.writes.mu.Unlock()
	if last == nil {
		return err
	}
	err = NormalizeError(err)
	if apiErr, ok := errors.AsType[*APIError](err); ok {
		if status == 0 || apiErr.StatusCode != status {
			return err
		}
		apiErr.Write = true
	} else if _, ok := errors.AsType[*NetworkError](err); !ok || status != 0 {
		return err
	}
	wrapped := *last
	wrapped.Err = err
	return &wrapped
}
