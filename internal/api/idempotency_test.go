package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/patramsey/namecom-cli/internal/api/gen"
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
	if _, err := c.Gen().CreateDomain(ctx, &gen.CreateDomainParams{XIdempotencyKey: &key},
		gen.CreateDomainJSONRequestBody{Domain: gen.DomainCreatePayload{DomainName: "example.com"}}); err != nil {
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
	if _, err := c.Gen().CreateDomain(ctx, &gen.CreateDomainParams{},
		gen.CreateDomainJSONRequestBody{Domain: gen.DomainCreatePayload{DomainName: "example.com"}}); err != nil {
		t.Fatalf("CreateDomain: %v", err)
	}

	if got != "PER-INVOCATION-UUID" {
		t.Errorf("context idempotency key should apply when caller supplies none, got %q", got)
	}
}
