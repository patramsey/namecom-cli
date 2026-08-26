package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	coreapigo "github.com/namedotcom/core-api-go"
	sdkcore "github.com/namedotcom/core-api-go/core"
)

// TestSDKSharesOurTransport is the assumption the whole migration rests on: the
// SDK is handed our *http.Client, so every request it makes goes through
// headerTransport and retryTransport. If that ever stops being true, the SDK
// starts talking to the API unauthenticated and unthrottled, with its own retry
// policy — none of which is visible from the call site.
func TestSDKSharesOurTransport(t *testing.T) {
	var gotAuth, gotUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotUA = r.Header.Get("Authorization"), r.Header.Get("User-Agent")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"records":[]}`))
	}))
	defer srv.Close()

	c, err := New(Options{BaseURL: srv.URL, UserAgent: "namecom-cli/test"})
	if err != nil {
		t.Fatalf("api.New: %v", err)
	}
	if _, err := c.SDK().DNS.ListRecords(context.Background(),
		&coreapigo.ListRecordsRequest{DomainName: "example.com"}); err != nil {
		t.Fatalf("SDK call failed: %v", err)
	}

	// No credentials are passed to the SDK constructor; these headers can only
	// have come from our transport.
	if gotAuth == "" {
		t.Error("SDK request carried no Authorization header — it is not going through headerTransport")
	}
	if gotUA != "namecom-cli/test" {
		t.Errorf("SDK request User-Agent = %q, want the one our client sets", gotUA)
	}
}

// TestSDKRetriesAreDisabledAtClientScope pins the option that makes the SDK
// safe to adopt. Its retrier dispatches on status code with no method check, so
// with retries live it replays a POST on a 5xx against endpoints that do not
// deduplicate. Our retryTransport owns retry policy; the SDK's must stay off.
//
// This was silently ignored at client scope through v1.33.2
// (namedotcom/core-api-go#3). A regression upstream would be invisible here
// without this test.
func TestSDKRetriesAreDisabledAtClientScope(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"message":"boom"}`))
	}))
	defer srv.Close()

	// MaxRetries negative disables our own retries too, so the count reflects
	// the SDK's behaviour alone.
	c, err := New(Options{BaseURL: srv.URL, MaxRetries: -1})
	if err != nil {
		t.Fatalf("api.New: %v", err)
	}
	_, _ = c.SDK().DNS.ListRecords(context.Background(),
		&coreapigo.ListRecordsRequest{DomainName: "example.com"})

	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("server saw %d requests; the SDK's own retrier is active. "+
			"option.WithoutRetries() at client scope is what keeps a POST from "+
			"being replayed on a 5xx", got)
	}
}

// TestFromSDKError converts the SDK's error into the one root.go maps to exit
// codes. Without it a 404 from an SDK call exits 1 instead of 4, which is a
// silent break of a documented contract rather than a visible failure.
func TestFromSDKError(t *testing.T) {
	t.Run("maps status and message", func(t *testing.T) {
		src := sdkcore.NewAPIError(http.StatusNotFound, nil,
			errAPI(`{"message":"Domain Not Found","details":"example.com"}`))
		got, ok := FromSDKError(src).(*APIError)
		if !ok {
			t.Fatalf("FromSDKError returned %T, want *APIError", FromSDKError(src))
		}
		if got.StatusCode != http.StatusNotFound {
			t.Errorf("StatusCode = %d, want 404 — exit code 4 depends on it", got.StatusCode)
		}
		if got.Message != "Domain Not Found" {
			t.Errorf("Message = %q, want the envelope's message", got.Message)
		}
		if got.Details != "example.com" {
			t.Errorf("Details = %q, want the envelope's details", got.Details)
		}
	})

	t.Run("recovers Retry-After from the header", func(t *testing.T) {
		h := http.Header{}
		h.Set("Retry-After", "30")
		src := sdkcore.NewAPIError(http.StatusTooManyRequests, h, errAPI(`{"message":"slow down"}`))
		got := FromSDKError(src).(*APIError)
		if got.RetryAfter != 30*time.Second {
			t.Errorf("RetryAfter = %s, want 30s — the 429 hint quotes this", got.RetryAfter)
		}
	})

	t.Run("falls back when the body is not the API envelope", func(t *testing.T) {
		src := sdkcore.NewAPIError(http.StatusBadGateway, nil, errAPI("<html>gateway timeout</html>"))
		got := FromSDKError(src).(*APIError)
		if got.StatusCode != http.StatusBadGateway {
			t.Errorf("StatusCode = %d, want 502", got.StatusCode)
		}
		if got.Message == "" {
			t.Error("Message is empty; a proxy error must still say something")
		}
	})

	t.Run("passes non-SDK errors through untouched", func(t *testing.T) {
		src := errAPI("dial tcp: connection refused")
		if got := FromSDKError(src); got != src {
			t.Errorf("FromSDKError rewrote a non-API error: %v", got)
		}
	})

	t.Run("nil stays nil", func(t *testing.T) {
		if got := FromSDKError(nil); got != nil {
			t.Errorf("FromSDKError(nil) = %v, want nil", got)
		}
	})
}

type stringErr string

func (e stringErr) Error() string { return string(e) }

func errAPI(s string) error { return stringErr(s) }
