package api

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"time"
)

// NetworkError is a request that got no HTTP response: it timed out, the host
// could not be resolved or reached, or the connection was refused or failed.
//
// net/http reports these as *url.Error, whose text begins with the method and
// the whole quoted URL — `Get "http://127.0.0.1:1/core/v1/domains/x.com":
// dial tcp 127.0.0.1:1: connect: connection refused` — and a timeout read
// `context deadline exceeded (Client.Timeout exceeded while awaiting
// headers)`, which says neither how long it waited nor what to change (#234).
// NormalizeError converts them to this; the exit code is still 1.
type NetworkError struct {
	// Host is the host:port the request was sent to.
	Host string
	// Timeout is the time budget that ran out, when the caller knows it. Root
	// sets it from --timeout; zero leaves it out of the message.
	Timeout time.Duration
	err     *url.Error
}

func (e *NetworkError) Unwrap() error { return e.err }

// TimedOut reports whether the request ran out of time rather than failing.
func (e *NetworkError) TimedOut() bool {
	return e.err.Timeout() && !errors.Is(e.err, context.Canceled)
}

func (e *NetworkError) Error() string {
	cause := e.err.Err
	switch {
	case e.TimedOut():
		if e.Timeout > 0 {
			return fmt.Sprintf("request to %s timed out after %s", e.Host, e.Timeout)
		}
		return fmt.Sprintf("request to %s timed out", e.Host)
	case errors.Is(cause, context.Canceled):
		return fmt.Sprintf("request to %s was cancelled", e.Host)
	}
	if dnsErr, ok := errors.AsType[*net.DNSError](cause); ok {
		return fmt.Sprintf("could not look up %s: %s", dnsErr.Name, dnsErr.Err)
	}
	if certificateErr(cause) {
		return fmt.Sprintf("TLS certificate from %s was not accepted: %v", e.Host, cause)
	}
	if opErr, ok := errors.AsType[*net.OpError](cause); ok && opErr.Op == "dial" {
		// The bare reason — "connection refused" — without "dial tcp
		// 127.0.0.1:1: connect:", which only repeats the host.
		reason := opErr.Err
		if sysErr, ok := errors.AsType[*os.SyscallError](reason); ok {
			reason = sysErr.Err
		}
		return fmt.Sprintf("could not connect to %s: %v", e.Host, reason)
	}
	return fmt.Sprintf("request to %s failed: %v", e.Host, cause)
}

// UserHint says what to change: the time budget for a timeout, the network or
// the base URL for a connection that could not be made.
func (e *NetworkError) UserHint() string {
	switch {
	case e.TimedOut():
		return "raise --timeout to wait longer (it covers retries too), or check your network connection"
	case errors.Is(e.err.Err, context.Canceled), certificateErr(e.err.Err):
		return ""
	}
	if !isNameComHost(e.Host) {
		return "could not reach the API — check --base-url and your network connection"
	}
	return "could not reach the API — check your network connection"
}

// isNameComHost reports whether host, with or without a port, is one of the
// API's own.
func isNameComHost(host string) bool {
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	return host == "api.name.com" || host == "api.dev.name.com"
}

// asNetworkError converts the *url.Error in err's chain to a *NetworkError,
// keeping any context a caller wrapped around it. It returns nil when err has
// no *url.Error.
func asNetworkError(err error) error {
	urlErr, ok := errors.AsType[*url.Error](err)
	if !ok {
		return nil
	}
	host := urlErr.URL
	if u, perr := url.Parse(urlErr.URL); perr == nil && u.Host != "" {
		host = u.Host
	}
	converted := &NetworkError{Host: host, err: urlErr}
	if outer, inner := err.Error(), urlErr.Error(); outer != inner {
		return &contextError{msg: outer, inner: inner, err: converted}
	}
	return converted
}
