package sdkspike

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	coreapigo "github.com/namedotcom/core-api-go"
	"github.com/namedotcom/core-api-go/option"
)

// ---- Q1: does WithoutRetries hold at client scope? -------------------------

// TestWithoutRetriesHoldsAtClientScope is the question the spike exists to
// answer first, because the fallback is expensive: if the option only takes
// effect per call, every one of the ~53 call sites has to carry it, and any
// site added later that forgets it silently regains POST-on-5xx retries.
func TestWithoutRetriesHoldsAtClientScope(t *testing.T) {
	// CONFIRMED SDK DEFECT, not a usage error. internal/retrier.go's NewRetrier
	// reads only `attempts` out of the options it is given and discards
	// `disabled` entirely, so a client built with WithoutRetries still gets a
	// Retrier with the default 2 attempts. Run() honours `disabled` only from
	// per-call options. The option is therefore silently ignored at client
	// scope — which is exactly where a safety setting most needs to hold.
	t.Run("client-scoped WithoutRetries is silently ignored", func(t *testing.T) {
		var calls int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			atomic.AddInt32(&calls, 1)
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"message":"boom"}`))
		}))
		t.Cleanup(srv.Close)

		c := New(srv.URL, "u", "t", &http.Client{Timeout: 5 * time.Second})
		_, err := c.DNS.ListRecords(context.Background(), &coreapigo.ListRecordsRequest{
			DomainName: "example.com",
		})
		if err == nil {
			t.Fatal("expected the 500 to surface as an error")
		}
		// Asserted as the defect it is, so the day the SDK fixes it this test
		// fails and says so. Nothing here mitigates it — see
		// docs/upstream/core-api-go-mitigations.md for what that would cost.
		if got := atomic.LoadInt32(&calls); got == 1 {
			t.Errorf("client-scoped WithoutRetries now holds (server saw 1 request) — " +
				"the SDK has been fixed; update the draft in docs/upstream/ and issue #40")
		} else {
			t.Logf("server saw %d requests despite client-scoped WithoutRetries — option silently ignored", got)
		}
	})

	t.Run("per-call WithoutRetries does suppress it", func(t *testing.T) {
		// The fallback, and the reason the client-scoped failure is expensive:
		// this form works, so every call site has to carry the option.
		var calls int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			atomic.AddInt32(&calls, 1)
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"message":"boom"}`))
		}))
		t.Cleanup(srv.Close)

		c := New(srv.URL, "u", "t", &http.Client{Timeout: 5 * time.Second})
		_, _ = c.DNS.ListRecords(context.Background(), &coreapigo.ListRecordsRequest{
			DomainName: "example.com",
		}, option.WithoutRetries())

		if got := atomic.LoadInt32(&calls); got != 1 {
			t.Errorf("server saw %d requests, want exactly 1 with a per-call WithoutRetries", got)
		}
	})

	t.Run("without it, the SDK retries a 500", func(t *testing.T) {
		// The control. If this ever stops retrying, the option above is proving
		// nothing and the test has quietly become a tautology.
		var calls int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			atomic.AddInt32(&calls, 1)
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"message":"boom"}`))
		}))
		t.Cleanup(srv.Close)

		c := NewWithSDKRetries(srv.URL, "u", "t", &http.Client{Timeout: 30 * time.Second})
		_, _ = c.DNS.ListRecords(context.Background(), &coreapigo.ListRecordsRequest{
			DomainName: "example.com",
		})
		if got := atomic.LoadInt32(&calls); got < 2 {
			t.Errorf("server saw %d requests, want >1 — the control is not exercising the retrier", got)
		}
	})
}

// TestSDKRetriesPostOn5xx pins the behaviour that makes WithoutRetries
// mandatory rather than merely tidy.
//
// internal/retrier.go dispatches on status code alone, with no method check, so
// a POST is retried on a 5xx. Our transport refuses that deliberately —
// CreateDomain and ProcessRefund are the calls at stake — and the API honours
// idempotency keys on only five operations, of which record creation is not
// one. A retried POST is a second write.
func TestSDKRetriesPostOn5xx(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"message":"boom"}`))
	}))
	t.Cleanup(srv.Close)

	c := NewWithSDKRetries(srv.URL, "u", "t", &http.Client{Timeout: 30 * time.Second})
	// Host is a plain string on the create body but a *string on the update
	// body — one of several asymmetries the SDK inherits from the spec.
	_, _ = c.DNS.CreateRecord(context.Background(), &coreapigo.DNSCreateRecordBody{
		DomainName: "example.com",
		Type:       coreapigo.DNSCreateRecordBodyType("A"),
		Host:       "spike",
		Answer:     "1.2.3.4",
	})

	if got := atomic.LoadInt32(&calls); got < 2 {
		t.Errorf("POST was sent %d time(s); expected the SDK to retry it on 5xx", got)
	} else {
		t.Logf("SDK sent the POST %d times on 5xx — this is why WithoutRetries is required, not optional", got)
	}
}

// TestSDKBackoffIgnoresContext pins the second transport problem, which the
// issue did not anticipate: internal/retrier.go sleeps with a bare time.Sleep,
// so a cancelled context does not interrupt the wait. Our own sleep selects on
// ctx.Done(), and #41 additionally declines a wait that cannot fit the
// deadline. Left live, the SDK's retrier makes --timeout unenforceable during
// backoff.
func TestSDKBackoffIgnoresContext(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "2")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"message":"slow down"}`))
	}))
	t.Cleanup(srv.Close)

	c := NewWithSDKRetries(srv.URL, "u", "t", &http.Client{Timeout: 30 * time.Second})
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, _ = c.DNS.ListRecords(ctx, &coreapigo.ListRecordsRequest{DomainName: "example.com"})
	elapsed := time.Since(start)

	// A context-aware backoff would abandon the wait at ~100ms. The bare
	// time.Sleep runs the full Retry-After first.
	if elapsed >= 2*time.Second {
		t.Logf("call took %s against a 100ms context deadline — the SDK's backoff is not context-aware", elapsed)
	} else {
		t.Errorf("call took %s; expected the bare time.Sleep to overrun the deadline. "+
			"If the SDK has become context-aware, this finding is stale and the recommendation should be revisited", elapsed)
	}
}

// ---- Q2: does the pagination guard carry over? ------------------------------

// TestListAllRecordsPagination checks that the walk terminates on both the
// normal and the pathological case, using the guard re-typed for the SDK's
// *int page fields.
func TestListAllRecordsPagination(t *testing.T) {
	t.Run("walks every page", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			if r.URL.Query().Get("page") == "2" {
				_, _ = w.Write([]byte(`{"records":[{"id":2,"host":"b","type":"A","answer":"2.2.2.2","ttl":300}],"lastPage":2,"totalCount":2}`))
				return
			}
			_, _ = w.Write([]byte(`{"records":[{"id":1,"host":"a","type":"A","answer":"1.1.1.1","ttl":300}],"nextPage":2,"lastPage":2,"totalCount":2}`))
		}))
		t.Cleanup(srv.Close)

		c := New(srv.URL, "u", "t", &http.Client{Timeout: 5 * time.Second})
		records, requests, err := ListAllRecords(context.Background(), c, "example.com")
		if err != nil {
			t.Fatalf("ListAllRecords: %v", err)
		}
		if len(records) != 2 {
			t.Errorf("got %d records across 2 pages, want 2", len(records))
		}
		if requests != 2 {
			t.Errorf("made %d requests, want 2", requests)
		}
	})

	t.Run("a non-advancing nextPage terminates the walk", func(t *testing.T) {
		var calls int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			if atomic.AddInt32(&calls, 1) > 10 {
				t.Error("walk did not terminate against a non-advancing nextPage")
				http.Error(w, "loop", http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"records":[{"id":1,"host":"a","type":"A","answer":"1.1.1.1","ttl":300}],"nextPage":2,"lastPage":99,"totalCount":99}`))
		}))
		t.Cleanup(srv.Close)

		c := New(srv.URL, "u", "t", &http.Client{Timeout: 5 * time.Second})
		_, requests, err := ListAllRecords(context.Background(), c, "example.com")
		if err != nil {
			t.Fatalf("ListAllRecords: %v", err)
		}
		if requests != 2 {
			t.Errorf("made %d requests, want 2 (page 1 -> 2, then the page stops advancing)", requests)
		}
	})
}

// ---- Q3: read-modify-write ergonomics ---------------------------------------

// TestUpdateRecordMergesOnlyChangedFields checks the RMW path behaves as
// cmd/dns/dns.go does, and records what the request actually looked like so the
// two can be compared side by side.
func TestUpdateRecordMergesOnlyChangedFields(t *testing.T) {
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`{"id":7,"host":"www","type":"A","answer":"1.1.1.1","ttl":600,"domainName":"example.com"}`))
			return
		}
		buf := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(buf)
		body = string(buf)
		_, _ = w.Write([]byte(`{"id":7,"host":"www","type":"A","answer":"9.9.9.9","ttl":600,"domainName":"example.com"}`))
	}))
	t.Cleanup(srv.Close)

	c := New(srv.URL, "u", "t", &http.Client{Timeout: 5 * time.Second})
	answer := "9.9.9.9"
	got, err := UpdateRecord(context.Background(), c, "example.com", 7, Changed{Answer: &answer})
	if err != nil {
		t.Fatalf("UpdateRecord: %v", err)
	}
	if derefStr(got.Answer) != "9.9.9.9" {
		t.Errorf("answer = %q, want the updated value", derefStr(got.Answer))
	}
	// The unchanged fields must survive: the API replaces the whole record, so
	// anything the merge drops is silently erased.
	for _, want := range []string{`"ttl":600`, `"host":"www"`, `"type":"A"`, `"answer":"9.9.9.9"`} {
		if !contains(body, want) {
			t.Errorf("request body is missing %s — a full-replacement PUT would erase it:\n%s", want, body)
		}
	}
	t.Logf("update request body: %s", body)
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (func() bool {
		for i := 0; i+len(needle) <= len(haystack); i++ {
			if haystack[i:i+len(needle)] == needle {
				return true
			}
		}
		return false
	})()
}

// countingTransport records how many requests actually pass through it.
type countingTransport struct {
	base  http.RoundTripper
	calls int32
}

func (c *countingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	atomic.AddInt32(&c.calls, 1)
	return c.base.RoundTrip(r)
}

// TestOurTransportSurvivesTheWiring is the load-bearing assumption of the whole
// recommendation: that handing the SDK our *http.Client keeps our RoundTripper
// — rate limiter, POST-on-5xx refusal, deadline guard, Retry-After clamp — in
// the path for every call.
//
// It is worth an explicit test because the per-call options are rebuilt from
// scratch on every method (that is what breaks client-scoped WithoutRetries),
// and CallParams carries a Client field alongside DisableRetries. Only a
// fallback in caller.Call — `client := c.client; if params.Client != nil` —
// keeps the client-scoped one alive. If that fallback ever goes the way the
// retry flag went, every request would silently route through
// http.DefaultClient and the rate limiter would be gone with no visible signal.
func TestOurTransportSurvivesTheWiring(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"records":[],"totalCount":0}`))
	}))
	t.Cleanup(srv.Close)

	tr := &countingTransport{base: http.DefaultTransport}
	c := New(srv.URL, "u", "t", &http.Client{Transport: tr, Timeout: 5 * time.Second})

	if _, err := c.DNS.ListRecords(context.Background(), &coreapigo.ListRecordsRequest{
		DomainName: "example.com",
	}); err != nil {
		t.Fatalf("ListRecords: %v", err)
	}
	if got := atomic.LoadInt32(&tr.calls); got != 1 {
		t.Errorf("our RoundTripper saw %d requests, want 1 — the SDK bypassed the client we supplied", got)
	}
}

// TestBasicAuthMatchesOurHeader pins that the SDK's auth is byte-identical to
// what internal/api/client.go builds today, so the migration is not quietly a
// credential change.
func TestBasicAuthMatchesOurHeader(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"records":[],"totalCount":0}`))
	}))
	t.Cleanup(srv.Close)

	c := New(srv.URL, "alice", "s3cret", &http.Client{Timeout: 5 * time.Second})
	if _, err := c.DNS.ListRecords(context.Background(), &coreapigo.ListRecordsRequest{
		DomainName: "example.com",
	}); err != nil {
		t.Fatalf("ListRecords: %v", err)
	}

	want := "Basic " + base64.StdEncoding.EncodeToString([]byte("alice:s3cret"))
	if got != want {
		t.Errorf("Authorization = %q, want %q", got, want)
	}
}
