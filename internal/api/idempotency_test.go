package api

import (
	"context"
	coreapigo "github.com/namedotcom/core-api-go"
	"github.com/namedotcom/core-api-go/option"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestIdempotencyKeyExplicitWins guards a money bug: the client's request
// editor ran AFTER the generated request builder and did an unconditional
// Header.Set, so a key the caller supplied via params.XIdempotencyKey was
// overwritten by the per-invocation UUID on the context.
//
// Combined with the per-command --idempotency-key flags shadowing the root
// persistent one (so the context key was always a fresh uuid.New()), retrying a
// timed-out `domain register` sent a DIFFERENT key each attempt — defeating the
// exact mechanism that prevents a double charge. transport.go deliberately
// refuses to retry POSTs on 5xx because it cannot assume idempotency; that care
// was wasted while the key never arrived.
func TestIdempotencyKeyExplicitWins(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("X-Idempotency-Key")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	c, err := New(Options{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("api.New: %v", err)
	}

	const explicit = "USER-SUPPLIED-KEY"
	ctx := ContextWithIdempotencyKey(context.Background(), "PER-INVOCATION-UUID")
	key := explicit
	// The header is set directly rather than through
	// option.WithXIdempotencyKey, which corrupts the value: the SDK builds it
	// as fmt.Sprintf("*%v", *key), so the caller's key goes out with a literal
	// asterisk in front of it. That is a generator bug, not a usage error —
	// see docs/upstream/core-api-go-idempotency-key-asterisk.md. This test is
	// about our transport preserving a key the caller already set, so it sets
	// one the honest way.
	name := "example.com"
	h := http.Header{}
	h.Set("X-Idempotency-Key", key)
	if _, err := c.SDK().Domains.CreateDomain(ctx,
		&coreapigo.CreateDomainRequest{Domain: &coreapigo.DomainCreatePayload{DomainName: &name}},
		option.WithHTTPHeader(h)); err != nil {
		t.Fatalf("CreateDomain: %v", err)
	}

	if got != explicit {
		t.Errorf("caller-supplied idempotency key must reach the wire: sent %q, want %q", got, explicit)
	}
}

// TestIdempotencyKeyFallsBackToContext pins that the context key is still used
// when the caller supplies none — that is what makes a plain retry safe.
func TestIdempotencyKeyFallsBackToContext(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("X-Idempotency-Key")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	c, err := New(Options{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("api.New: %v", err)
	}

	ctx := ContextWithIdempotencyKey(context.Background(), "PER-INVOCATION-UUID")
	name := "example.com"
	if _, err := c.SDK().Domains.CreateDomain(ctx,
		&coreapigo.CreateDomainRequest{Domain: &coreapigo.DomainCreatePayload{DomainName: &name}}); err != nil {
		t.Fatalf("CreateDomain: %v", err)
	}

	if got != "PER-INVOCATION-UUID" {
		t.Errorf("context idempotency key should apply when caller supplies none, got %q", got)
	}
}

// TestIdempotencyKeyIsPerOperation guards the scoping of the generated key.
//
// The key was minted once per process and pinned on the context, so every
// write in one invocation carried the same one. `dns import` posts once per
// record, so a 50-record file went out under a single key — and an API
// honouring keys as documented ("reusing the same key returns the original
// result instead of repeating the operation") would create record 1, echo it
// back for the other 49, and let the CLI report the file as fully imported.
//
// A key identifies an OPERATION. Retries of one operation still share theirs:
// the editor runs once where the request is built, and retryTransport replays
// that same *http.Request with its headers already set.
func TestIdempotencyKeyIsPerOperation(t *testing.T) {
	var keys []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		keys = append(keys, r.Header.Get("X-Idempotency-Key"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	c, err := New(Options{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("api.New: %v", err)
	}

	// Three record creations, the shape `dns import` produces.
	ctx := context.Background()
	for _, host := range []string{"a", "b", "c"} {
		if _, err := c.SDK().DNS.CreateRecord(ctx, &coreapigo.DNSCreateRecordBody{
			DomainName: "example.com",
			Type:       "A", Host: host, Answer: "1.2.3.4",
		}); err != nil {
			t.Fatalf("CreateRecord(%s): %v", host, err)
		}
	}

	if len(keys) != 3 {
		t.Fatalf("server saw %d writes, want 3", len(keys))
	}
	seen := map[string]bool{}
	for i, k := range keys {
		if k == "" {
			t.Fatalf("write %d carried no idempotency key", i+1)
		}
		if seen[k] {
			t.Errorf("key %q reused across separate operations: %v", k, keys)
		}
		seen[k] = true
	}
}

// TestIdempotencyKeyPinnedAcrossOperations pins --idempotency-key's behaviour:
// naming a key deliberately still applies it to every write, which is what
// makes a re-run of a failed invocation collapse onto the original.
func TestIdempotencyKeyPinnedAcrossOperations(t *testing.T) {
	var keys []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		keys = append(keys, r.Header.Get("X-Idempotency-Key"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	c, err := New(Options{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("api.New: %v", err)
	}

	ctx := ContextWithIdempotencyKey(context.Background(), "PINNED-123")
	for _, host := range []string{"a", "b"} {
		if _, err := c.SDK().DNS.CreateRecord(ctx, &coreapigo.DNSCreateRecordBody{
			DomainName: "example.com",
			Type:       "A", Host: host, Answer: "1.2.3.4",
		}); err != nil {
			t.Fatalf("CreateRecord(%s): %v", host, err)
		}
	}
	for _, k := range keys {
		if k != "PINNED-123" {
			t.Errorf("key = %q, want the pinned value", k)
		}
	}
}

// TestIdempotencyKeyAbsentOnReads pins that GETs stay clean — the header is a
// write-path concern and stamping it on reads would be noise.
func TestIdempotencyKeyAbsentOnReads(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("X-Idempotency-Key")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"records":[]}`))
	}))
	defer srv.Close()

	c, err := New(Options{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("api.New: %v", err)
	}
	if _, err := c.SDK().DNS.ListRecords(context.Background(),
		&coreapigo.ListRecordsRequest{DomainName: "example.com"}); err != nil {
		t.Fatalf("ListRecords: %v", err)
	}
	if got != "" {
		t.Errorf("a read carried an idempotency key: %q", got)
	}
}

// TestIdempotencyKeyStableAcrossRetries pins the property the whole scheme
// depends on: every attempt at one write carries one key. A retry that changed
// it would defeat the point of sending one at all.
//
// The mechanism is that retryTransport replays the same *http.Request and
// headerTransport only fills headers that are absent, so the key set on the
// first attempt persists. That holds regardless of how the two transports are
// nested — checked by inverting them, which does not fail this test. What would
// fail it is any change that rebuilds the request per attempt, or that stamps
// the key unconditionally.
//
// PUT is used because retryTransport deliberately will not retry a POST on 5xx.
func TestIdempotencyKeyStableAcrossRetries(t *testing.T) {
	var keys []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		keys = append(keys, r.Header.Get("X-Idempotency-Key"))
		if len(keys) == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	c, err := New(Options{BaseURL: srv.URL, MaxRetries: 1})
	if err != nil {
		t.Fatalf("api.New: %v", err)
	}

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPut, srv.URL+"/core/v1/thing", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	resp, err := c.HTTPClient().Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if len(keys) != 2 {
		t.Fatalf("expected the 500 to be retried once, got %d attempt(s)", len(keys))
	}
	if keys[0] == "" {
		t.Fatal("no idempotency key was sent on a PUT")
	}
	if keys[0] != keys[1] {
		t.Errorf("retry sent a different idempotency key:\n  attempt 1: %s\n  attempt 2: %s\n"+
			"one write must go out under one key; something is rebuilding the "+
			"request or re-stamping the header per attempt", keys[0], keys[1])
	}
}
