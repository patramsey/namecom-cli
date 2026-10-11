package cmd

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// TestTokenFlagWarns pins #341's fourth part: --token works on every command
// but auth login, and put the token in the process list and shell history
// without a word. It now warns, through the warnings document in JSON mode,
// and the command still runs with one request.
func TestTokenFlagWarns(t *testing.T) {
	stdout, stderr, code, n := runRedacted(t, nil,
		"--username", "ci", "--token", "flag-token-0123456789", "api", "GET", "/core/v1/hello", "-o", "json")
	if code != 0 {
		t.Fatalf("exit %d:\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	doc := decodeDoc(t, "stderr", stderr)
	// The stub's --base-url has a warning of its own.
	warnings, _ := doc["warnings"].([]any)
	about := 0
	for _, w := range warnings {
		if s, _ := w.(string); strings.HasPrefix(s, "--token ") {
			about++
		}
	}
	if about != 1 {
		t.Errorf("warnings = %v, want one about --token", doc["warnings"])
	}
	if n != 1 {
		t.Errorf("sent %d requests, want 1", n)
	}

	_, stderr, _, _ = runRedacted(t, nil, "api", "GET", "/core/v1/hello", "-o", "json")
	if strings.Contains(stderr, "--token") {
		t.Errorf("warned without --token:\n%s", stderr)
	}
}

// TestAuthCodeFromStdin: transfer create and internal-in had no way to take
// the auth code but argv when there is no terminal (#341). `--auth-code -`
// reads it from standard input, and the request carries it.
func TestAuthCodeFromStdin(t *testing.T) {
	stubStart(t, func(string) error { return nil })
	tests := []struct {
		name     string
		args     []string
		path     string
		requests int32
	}{
		// The pricing GET, then the POST, as in TestRequestCounts.
		{"transfer create", []string{"transfer", "create", "example.com", "--auth-code", "-", "--yes", "-o", "json"},
			"/core/v1/transfers", 2},
		{"transfer internal-in", []string{"transfer", "internal-in", "example.com", "--auth-code", "-", "--yes", "-o", "json"},
			"/core/v1/transfers/internal/in", 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			withConfig(t, redactProfile)
			resetFlags(t, tc.args)
			var n atomic.Int32
			var sent atomic.Value
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				n.Add(1)
				b, _ := io.ReadAll(r.Body)
				if r.Method == http.MethodPost && r.URL.Path == tc.path {
					sent.Store(string(b))
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{}`))
			}))
			t.Cleanup(srv.Close)
			rootCmd.SetIn(strings.NewReader("STDINCODE123\n"))
			t.Cleanup(func() { rootCmd.SetIn(nil) })

			stdout, stderr, code := runContract(t, append([]string{"--base-url", srv.URL}, tc.args...)...)
			if code != 0 {
				t.Fatalf("exit %d:\nstdout: %s\nstderr: %s", code, stdout, stderr)
			}
			if body, _ := sent.Load().(string); !strings.Contains(body, `"authCode":"STDINCODE123"`) {
				t.Errorf("POST %s body = %s, want the code read from stdin", tc.path, body)
			}
			if n.Load() != tc.requests {
				t.Errorf("sent %d requests, want %d", n.Load(), tc.requests)
			}
		})
	}

	t.Run("empty stdin is a usage error before any request", func(t *testing.T) {
		args := []string{"transfer", "create", "example.com", "--auth-code", "-", "--yes", "-o", "json"}
		rootCmd.SetIn(strings.NewReader("\n"))
		t.Cleanup(func() { rootCmd.SetIn(nil) })
		_, stderr, code, n := runRedacted(t, nil, args...)
		if code != 2 || !strings.Contains(stderr, "read no code") {
			t.Errorf("exit %d, stderr %s; want exit 2 saying no code was read", code, stderr)
		}
		if n != 0 {
			t.Errorf("sent %d requests, want 0", n)
		}
	})
}
