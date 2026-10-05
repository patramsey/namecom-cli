package api

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestNetworkError_Wording pins #234: transport failures were shown with Go's
// `Get "http://…":` prefix, and a timeout as "context deadline exceeded
// (Client.Timeout exceeded while awaiting headers)".
func TestNetworkError_Wording(t *testing.T) {
	const u = "https://api.name.com/core/v1/domains/x.com"
	refused := &net.OpError{Op: "dial", Net: "tcp",
		Err: &os.SyscallError{Syscall: "connect", Err: syscall.ECONNREFUSED}}
	tests := []struct {
		name      string
		err       error
		timeout   time.Duration
		want      string
		wantHint  string
		notInHint string
	}{
		{"timeout with budget", &url.Error{Op: "Get", URL: u, Err: context.DeadlineExceeded}, 3 * time.Second,
			"request to api.name.com timed out after 3s", "raise --timeout", ""},
		{"timeout without budget", &url.Error{Op: "Get", URL: u, Err: context.DeadlineExceeded}, 0,
			"request to api.name.com timed out", "raise --timeout", ""},
		{"refused", &url.Error{Op: "Get", URL: "http://127.0.0.1:1/core/v1/x", Err: refused}, 0,
			"could not connect to 127.0.0.1:1: " + syscall.ECONNREFUSED.Error(), "--base-url", ""},
		{"no such host", &url.Error{Op: "Get", URL: u, Err: &net.OpError{Op: "dial", Net: "tcp",
			Err: &net.DNSError{Err: "no such host", Name: "api.name.com", IsNotFound: true}}}, 0,
			"could not look up api.name.com: no such host", "network connection", "--base-url"},
		{"cancelled", &url.Error{Op: "Get", URL: u, Err: context.Canceled}, 0,
			"request to api.name.com was cancelled", "", ""},
		{"other", &url.Error{Op: "Post", URL: u, Err: errors.New("refusing to follow a redirect")}, 0,
			"request to api.name.com failed: refusing to follow a redirect", "network connection", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NormalizeError(tt.err)
			netErr, ok := errors.AsType[*NetworkError](got)
			if !ok {
				t.Fatalf("NormalizeError = %T, want a *NetworkError", got)
			}
			netErr.Timeout = tt.timeout
			if got.Error() != tt.want {
				t.Errorf("Error() = %q, want %q", got.Error(), tt.want)
			}
			if strings.Contains(got.Error(), `"`) {
				t.Errorf("message still quotes the URL: %q", got.Error())
			}
			hint := netErr.UserHint()
			if tt.wantHint == "" && hint != "" || !strings.Contains(hint, tt.wantHint) {
				t.Errorf("hint = %q, want %q", hint, tt.wantHint)
			}
			if tt.notInHint != "" && strings.Contains(hint, tt.notInHint) {
				t.Errorf("hint = %q, must not mention %q", hint, tt.notInHint)
			}
		})
	}
}

// TestNetworkError_KeepsCallerContext: the caller's wrapping stays, and the
// timeout set after conversion still reaches the message.
func TestNetworkError_KeepsCallerContext(t *testing.T) {
	err := NormalizeError(fmt.Errorf("fetching domain: %w",
		&url.Error{Op: "Get", URL: "https://api.name.com/x", Err: context.DeadlineExceeded}))
	netErr, ok := errors.AsType[*NetworkError](err)
	if !ok {
		t.Fatalf("NormalizeError = %T, want a *NetworkError in the chain", err)
	}
	netErr.Timeout = 5 * time.Second
	if want := "fetching domain: request to api.name.com timed out after 5s"; err.Error() != want {
		t.Errorf("Error() = %q, want %q", err.Error(), want)
	}
	if NormalizeError(err) != err {
		t.Error("normalizing twice should be a no-op")
	}
}

// TestNetworkError_RealTimeout runs the client against a stub that answers
// too late, so the test sees the error net/http really produces.
func TestNetworkError_RealTimeout(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(func() { close(release); srv.Close() })

	c, err := New(Options{BaseURL: srv.URL, Timeout: 50 * time.Millisecond, MaxRetries: -1})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := c.HTTPClient().Get(srv.URL + "/core/v1/hello")
	if err == nil {
		_ = resp.Body.Close()
		t.Fatal("want a timeout")
	}
	got := NormalizeError(err)
	netErr, ok := errors.AsType[*NetworkError](got)
	if !ok || !netErr.TimedOut() {
		t.Fatalf("NormalizeError = %v (%T), want a timed-out *NetworkError", got, got)
	}
	if strings.Contains(got.Error(), "Client.Timeout") || strings.Contains(got.Error(), "Get \"") {
		t.Errorf("message keeps Go's wording: %q", got.Error())
	}
}
