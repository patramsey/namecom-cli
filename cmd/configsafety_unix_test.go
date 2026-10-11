//go:build !windows

package cmd

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/patramsey/namecom-cli/internal/config"
	"github.com/patramsey/namecom-cli/internal/output"
)

// configSafetyServer answers every request with body and counts them.
func configSafetyServer(t *testing.T, body string) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, &n
}

// TestConfigOthersCanWrite_IsRefused guards #342: a 0666 config's token_cmd
// ran on `domain list`, so anyone who could write the file could run
// commands as the user. The run now fails as an auth problem (exit 3) before
// token_cmd runs or any request is sent, with the chmod as the hint.
func TestConfigOthersCanWrite_IsRefused(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "pwned")
	path := withConfig(t, "profiles:\n  work:\n    username: u\n    token_cmd: touch "+marker+"; echo tok\n")
	if err := os.Chmod(path, 0o666); err != nil { //nolint:gosec // the mode under test
		t.Fatal(err)
	}
	config.TakeWarnings()
	args := []string{"domain", "list"}
	resetFlags(t, args)
	srv, requests := configSafetyServer(t, `{"domains":[]}`)

	_, stderr, code := runContract(t, append([]string{"--base-url", srv.URL, "-o", "json"}, args...)...)
	if code != 3 {
		t.Errorf("exit %d, want 3; stderr:\n%s", code, stderr)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("token_cmd ran from a config other users can write")
	}
	if n := requests.Load(); n != 0 {
		t.Errorf("sent %d requests, want 0", n)
	}
	e, _ := decodeDoc(t, "stderr", stderr)["error"].(map[string]any)
	if e["type"] != output.ErrorTypeAuth {
		t.Errorf("error.type = %v, want %q", e["type"], output.ErrorTypeAuth)
	}
	if hint, _ := e["hint"].(string); !strings.Contains(hint, "chmod 600 "+path) {
		t.Errorf("error.hint = %q, want the chmod command", hint)
	}
}

// TestConfigOthersCanRead_WarnsInJSON: the read-bits warning used to print
// only to a terminal, so a script never saw it. It is now a collected
// warning, in JSON mode part of the {"warnings": [...]} document, and the
// command still runs with its one request.
func TestConfigOthersCanRead_WarnsInJSON(t *testing.T) {
	path := withConfig(t, loneProfile)
	if err := os.Chmod(path, 0o644); err != nil { //nolint:gosec // the mode under test
		t.Fatal(err)
	}
	config.TakeWarnings()
	args := []string{"domain", "list"}
	resetFlags(t, args)
	srv, requests := configSafetyServer(t, `{"domains":[]}`)

	stdout, stderr, code := runContract(t, append([]string{"--base-url", srv.URL, "-o", "json"}, args...)...)
	if code != 0 {
		t.Fatalf("exit %d, stderr:\n%s", code, stderr)
	}
	decodeDoc(t, "stdout", stdout)
	if n := requests.Load(); n != 1 {
		t.Errorf("sent %d requests, want 1", n)
	}
	list, _ := decodeDoc(t, "stderr", stderr)["warnings"].([]any)
	found := false
	for _, w := range list {
		if s, _ := w.(string); strings.Contains(s, "readable by other users") && strings.Contains(s, "chmod 600 "+path) {
			found = true
		}
	}
	if !found {
		t.Errorf("warnings should include the readable-config warning, got:\n%s", stderr)
	}
}
