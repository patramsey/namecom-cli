package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
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

// TestSDKDoesNotSleepAfterFinalResponse pins the workaround for #162
// (namedotcom/core-api-go#12). With retries disabled, the SDK's retrier still
// sleeps on a 429, 408 or 5xx before returning it — honouring Retry-After up to
// 60s — although it will not retry. Our transport has already applied the
// retry policy and the deadline guard by then, so any sleep after it is pure
// delay, and under --timeout it turned a 429 into a body-read failure.
//
// Retry-After values here are short so a regression costs seconds, not a
// minute; each is still well beyond the time the call is allowed.
func TestSDKDoesNotSleepAfterFinalResponse(t *testing.T) {
	cases := []struct {
		name       string
		status     int
		retryAfter string
		opts       Options
		wantType   func(error) bool
	}{
		{
			name: "429 with retries off", status: http.StatusTooManyRequests, retryAfter: "3",
			opts: Options{MaxRetries: -1},
			wantType: func(err error) bool {
				_, ok := errors.AsType[*coreapigo.TooManyRequestsError](err)
				return ok
			},
		},
		{
			// The transport hands the 429 back because 3s does not fit in 1s.
			// The SDK then slept into the deadline and failed reading the body:
			// exit 1 with "request canceled" instead of exit 5.
			name: "429 whose Retry-After outlasts --timeout", status: http.StatusTooManyRequests, retryAfter: "3",
			opts: Options{Timeout: time.Second},
			wantType: func(err error) bool {
				_, ok := errors.AsType[*coreapigo.TooManyRequestsError](err)
				return ok
			},
		},
		{
			name: "final 500", status: http.StatusInternalServerError,
			opts: Options{MaxRetries: -1},
			wantType: func(err error) bool {
				_, ok := errors.AsType[*coreapigo.InternalServerError](err)
				return ok
			},
		},
		{
			name: "final 408", status: http.StatusRequestTimeout,
			opts: Options{MaxRetries: -1},
			wantType: func(err error) bool {
				_, ok := errors.AsType[*sdkcore.APIError](err)
				return ok
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var calls int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				atomic.AddInt32(&calls, 1)
				if tc.retryAfter != "" {
					w.Header().Set("Retry-After", tc.retryAfter)
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(`{"message":"try later","details":"stub"}`))
			}))
			defer srv.Close()

			opts := tc.opts
			opts.BaseURL = srv.URL
			c, err := New(opts)
			if err != nil {
				t.Fatalf("api.New: %v", err)
			}
			start := time.Now()
			_, sdkErr := c.SDK().Domains.GetDomain(context.Background(),
				&coreapigo.GetDomainRequest{DomainName: "example.com"})
			elapsed := time.Since(start)

			if elapsed > 500*time.Millisecond {
				t.Errorf("call took %s; the final response must be returned without a sleep", elapsed.Round(time.Millisecond))
			}
			if got := atomic.LoadInt32(&calls); got != 1 {
				t.Errorf("server saw %d requests, want 1", got)
			}
			if !tc.wantType(sdkErr) {
				t.Errorf("error is %T (%v); the SDK's typed error decoding was lost", sdkErr, sdkErr)
			}
			apiErr, ok := FromSDKError(sdkErr).(*APIError)
			if !ok {
				t.Fatalf("FromSDKError returned %T (%v), want *APIError", FromSDKError(sdkErr), FromSDKError(sdkErr))
			}
			if apiErr.StatusCode != tc.status {
				t.Errorf("StatusCode = %d, want %d", apiErr.StatusCode, tc.status)
			}
			if apiErr.Message != "try later" || apiErr.Details != "stub" {
				t.Errorf("got %q (%q), want the body's envelope", apiErr.Message, apiErr.Details)
			}
			if tc.retryAfter != "" && apiErr.RetryAfter != 3*time.Second {
				t.Errorf("RetryAfter = %s, want 3s", apiErr.RetryAfter)
			}
		})
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

	// #159: FromSDKError must normalize exactly as ErrorFromResponse does, or
	// every command except `namecom api` loses the fixes made there.
	t.Run("an explained 500 is not called transient", func(t *testing.T) {
		src := sdkcore.NewAPIError(http.StatusInternalServerError, nil,
			errAPI(`{"message":"Invalid IP","details":"reserved"}`))
		got := FromSDKError(src).(*APIError)
		if strings.Contains(got.UserHint(), "try again shortly") {
			t.Errorf("hint = %q; the API said why it failed, so retrying will not help", got.UserHint())
		}
	})

	t.Run("a non-JSON body is summarized", func(t *testing.T) {
		page := "<html>\n<body>" + strings.Repeat("<p>502 Bad Gateway</p>\n", 200) + "</body></html>"
		src := sdkcore.NewAPIError(http.StatusBadGateway, nil, errAPI(page))
		got := FromSDKError(src).(*APIError)
		if len(got.Message) > maxFallbackMessage+80 {
			t.Errorf("message is %d bytes; a proxy page must be summarized, not quoted whole", len(got.Message))
		}
		if strings.Contains(got.Message, "\n") {
			t.Errorf("message spans lines: %q", got.Message)
		}
	})

	// The sandbox note is the hint's, and only in the sandbox (#234).
	t.Run("a 401 gets the sandbox note in the sandbox only", func(t *testing.T) {
		src := sdkcore.NewAPIError(http.StatusUnauthorized, nil, errAPI(`{"message":"Unauthorized"}`))
		got := FromSDKError(src).(*APIError)
		if strings.Contains(got.Error()+got.UserHint(), "sandbox") {
			t.Errorf("production 401 mentions the sandbox: %q / %q", got.Error(), got.UserHint())
		}
		got.Sandbox = true
		if !strings.Contains(got.UserHint(), "sandbox uses a separate API token") {
			t.Errorf("hint = %q, want the sandbox token note", got.UserHint())
		}
	})

	t.Run("a typed SDK error is converted the same way", func(t *testing.T) {
		src := &coreapigo.InternalServerError{APIError: sdkcore.NewAPIError(http.StatusInternalServerError, nil,
			errAPI(`{"message":"Invalid IP","details":"reserved"}`))}
		got, ok := FromSDKError(src).(*APIError)
		if !ok {
			t.Fatalf("FromSDKError returned %T, want *APIError", FromSDKError(src))
		}
		if got.Message != "Invalid IP" || got.Details != "reserved" || !got.explained {
			t.Errorf("got %+v, want the envelope decoded and marked explained", got)
		}
	})

	t.Run("a body-less error falls back to the status text", func(t *testing.T) {
		src := sdkcore.NewAPIError(http.StatusServiceUnavailable, nil, nil)
		if got := FromSDKError(src).(*APIError).Message; got != http.StatusText(http.StatusServiceUnavailable) {
			t.Errorf("Message = %q, want the status text", got)
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
