package cmd

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/patramsey/namecom-cli/internal/output"
)

// TestWriteOutcomeUnknown pins #243 through the root: a write that answered
// 5xx, or timed out after it was sent, exits 6, and its error envelope names
// the idempotency key the request carried — the generated one, or the one
// --idempotency-key pinned — with a hint saying how to re-run it.
func TestWriteOutcomeUnknown(t *testing.T) {
	withConfig(t, loneProfile)
	create := []string{"dns", "create", "example.com", "--type", "A", "--host", "www", "--answer", "192.0.2.1", "--yes"}

	tests := []struct {
		name     string
		extra    []string
		hang     bool
		wantType string
		wantKey  string // "" means whatever the stub received
	}{
		{name: "5xx", wantType: output.ErrorTypeAPI},
		{name: "5xx with a pinned key", extra: []string{"--idempotency-key", "my-key-1"}, wantType: output.ErrorTypeAPI, wantKey: "my-key-1"},
		{name: "timeout", extra: []string{"--timeout", "100ms"}, hang: true, wantType: output.ErrorTypeNetwork},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var mu sync.Mutex
			var sent string
			done := make(chan struct{})
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				mu.Lock()
				sent = r.Header.Get("X-Idempotency-Key")
				mu.Unlock()
				if tc.hang {
					select {
					case <-done:
					case <-r.Context().Done():
					}
					return
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`{"message":"Internal Error"}`))
			}))
			t.Cleanup(srv.Close)
			t.Cleanup(func() { close(done) })

			resetFlags(t, create)
			args := append(append([]string{"--base-url", srv.URL, "-o", "json"}, tc.extra...), create...)
			_, stderr, code := runContract(t, args...)
			if code != 6 {
				t.Errorf("exit %d, want 6:\n%s", code, stderr)
			}
			mu.Lock()
			want := sent
			mu.Unlock()
			if tc.wantKey != "" {
				want = tc.wantKey
			}
			e, _ := decodeDoc(t, "stderr", stderr)["error"].(map[string]any)
			if want == "" || e["idempotencyKey"] != want {
				t.Errorf("error.idempotencyKey = %v, want %q, the key that was sent", e["idempotencyKey"], want)
			}
			if hint, _ := e["hint"].(string); !strings.HasPrefix(hint, "outcome unknown; re-run with --idempotency-key "+want) {
				t.Errorf("hint = %q, want it to start with the re-run instruction", hint)
			}
			if e["type"] != tc.wantType {
				t.Errorf("error.type = %v, want %q", e["type"], tc.wantType)
			}
		})
	}
}

// TestWriteOutcomeKnown is the other side: a 4xx write and a 5xx read keep
// their exit codes and get no idempotency key.
func TestWriteOutcomeKnown(t *testing.T) {
	withConfig(t, loneProfile)
	for _, tc := range []struct {
		name   string
		args   []string
		status int
	}{
		{"4xx write", []string{"dns", "create", "example.com", "--type", "A", "--host", "www", "--answer", "192.0.2.1", "--yes"}, 400},
		{"5xx read", []string{"domain", "get", "example.com", "--timeout", "200ms"}, 500},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(`{"message":"no"}`))
			}))
			t.Cleanup(srv.Close)
			resetFlags(t, tc.args)
			_, stderr, code := runContract(t, append([]string{"--base-url", srv.URL, "-o", "json"}, tc.args...)...)
			if code != 1 {
				t.Errorf("exit %d, want 1", code)
			}
			if strings.Contains(stderr, "idempotencyKey") || strings.Contains(stderr, "outcome unknown") {
				t.Errorf("a failure with a known outcome claimed otherwise:\n%s", stderr)
			}
		})
	}
}
