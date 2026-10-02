package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	coreapigo "github.com/namedotcom/core-api-go"
)

// TestWriteRedirectNotFollowed pins #185. net/http follows a 302 or 303 on a
// POST by sending a GET to the new location, without the body. Against a stub
// that redirected `dns create`, that GET answered `{}`, the SDK decoded it as a
// record, and the CLI printed "Created A record (id 0)" with exit 0 — nothing
// had been created.
func TestWriteRedirectNotFollowed(t *testing.T) {
	for _, status := range []int{http.StatusFound, http.StatusSeeOther, http.StatusTemporaryRedirect} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var followed int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/elsewhere" {
					atomic.AddInt32(&followed, 1)
					_, _ = w.Write([]byte(`{}`))
					return
				}
				http.Redirect(w, r, "/elsewhere", status)
			}))
			defer srv.Close()

			c, err := New(Options{BaseURL: srv.URL})
			if err != nil {
				t.Fatalf("api.New: %v", err)
			}
			ttl := int64(300)
			rec, err := c.SDK().DNS.CreateRecord(context.Background(), &coreapigo.DNSCreateRecordBody{
				DomainName: "example.com", Type: "A", Host: "www", Answer: "192.0.2.1", TTL: &ttl,
			})
			if err == nil {
				t.Fatalf("a redirected POST reported success (record %+v)", rec)
			}
			if got := atomic.LoadInt32(&followed); got != 0 {
				t.Errorf("the redirect was followed %d time(s); a write must not be resent elsewhere", got)
			}
			if !strings.Contains(err.Error(), "redirect") {
				t.Errorf("error = %q, want it to say a redirect was refused", err)
			}
		})
	}
}

// TestReadRedirectStillFollowed: a GET is safe to replay at a new location, so
// refusing write redirects must not change reads.
func TestReadRedirectStillFollowed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/elsewhere" {
			_, _ = w.Write([]byte(`{"records":[]}`))
			return
		}
		http.Redirect(w, r, "/elsewhere", http.StatusFound)
	}))
	defer srv.Close()

	c, err := New(Options{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("api.New: %v", err)
	}
	if _, err := c.SDK().DNS.ListRecords(context.Background(),
		&coreapigo.ListRecordsRequest{DomainName: "example.com"}); err != nil {
		t.Errorf("a redirected GET failed: %v", err)
	}
}
