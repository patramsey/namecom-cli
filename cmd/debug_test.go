package cmd

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/patramsey/namecom-cli/internal/output"
)

// debugAuthCode is the transfer auth code the tests below send and receive.
// It must never appear in a --debug log.
const debugAuthCode = "Zq7-SECRET-auth-9x"

// runWithDebugFile runs `namecom <args>` through the real root with
// --debug-file, and returns what was logged there.
func runWithDebugFile(t *testing.T, srv *httptest.Server, args []string) string {
	t.Helper()
	t.Cleanup(output.StubInteractive(false))

	devnull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("opening %s: %v", os.DevNull, err)
	}
	stdout, stderr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = devnull, devnull
	t.Cleanup(func() { os.Stdout, os.Stderr = stdout, stderr; _ = devnull.Close() })

	logPath := filepath.Join(t.TempDir(), "debug.log")
	prev := gf
	t.Cleanup(func() { gf = prev; rootCmd.SetArgs(nil) })
	rootCmd.SetArgs(append([]string{"--base-url", srv.URL, "--yes", "-o", "json", "--debug-file", logPath}, args...))
	if err := rootCmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("namecom %s: %v", strings.Join(args, " "), err)
	}
	data, err := os.ReadFile(logPath) //nolint:gosec // a path under t.TempDir()
	if err != nil {
		t.Fatalf("reading debug log: %v", err)
	}
	return string(data)
}

// TestDebugLogRedactsAuthCode pins that --debug never writes a transfer auth
// code, in either direction. It is the secret that moves a domain to another
// registrar, and a debug log is what gets pasted into a bug report.
func TestDebugLogRedactsAuthCode(t *testing.T) {
	withConfig(t, loneProfile)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, ":getAuthCode"):
			_, _ = w.Write([]byte(`{"authCode":"` + debugAuthCode + `"}`))
		case strings.HasSuffix(r.URL.Path, "/transfers"):
			// The code echoed back inside a nested object, as an API that
			// returned the transfer request would.
			_, _ = w.Write([]byte(`{"transfer":{"domainName":"example.com","status":"pending","authCode":"` + debugAuthCode + `"},"order":1,"totalPaid":12.99}`))
		default:
			_, _ = w.Write([]byte(`{"domainName":"example.com","locked":false,"authCode":"` + debugAuthCode + `"}`))
		}
	}))
	t.Cleanup(srv.Close)

	for _, args := range [][]string{
		{"transfer", "create", "example.com", "--auth-code", debugAuthCode},
		{"transfer", "internal-in", "example.com", "--auth-code", debugAuthCode},
		{"domain", "auth-code", "example.com"},
	} {
		t.Run(strings.Join(args[:2], " "), func(t *testing.T) {
			log := runWithDebugFile(t, srv, args)
			if !strings.Contains(log, "body:") {
				t.Fatalf("debug log shows no bodies; the test proves nothing:\n%s", log)
			}
			if strings.Contains(log, debugAuthCode) {
				t.Errorf("debug log contains the auth code:\n%s", log)
			}
			if !strings.Contains(log, `"authCode":"[redacted]"`) {
				t.Errorf("debug log should show the auth code as [redacted]:\n%s", log)
			}
		})
	}
}

// executeRoot runs `namecom <args>` through the real root with stdout and
// stderr discarded, and returns its error.
func executeRoot(t *testing.T, args ...string) error {
	t.Helper()
	t.Cleanup(output.StubInteractive(false))
	devnull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("opening %s: %v", os.DevNull, err)
	}
	stdout, stderr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = devnull, devnull
	prev := gf
	t.Cleanup(func() {
		os.Stdout, os.Stderr = stdout, stderr
		_ = devnull.Close()
		gf = prev
		rootCmd.SetArgs(nil)
	})
	rootCmd.SetArgs(args)
	return rootCmd.ExecuteContext(context.Background())
}

// TestDebugFile_ExistingFileMadePrivate pins #187: only a new --debug-file was
// created 0600. One that already existed kept its mode, so a world-readable
// file went on collecting request and response bodies.
func TestDebugFile_ExistingFileMadePrivate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix permission bits do not apply on windows")
	}
	withConfig(t, loneProfile)
	srv := helloServer(t)
	logPath := filepath.Join(t.TempDir(), "debug.log")
	if err := os.WriteFile(logPath, nil, 0o644); err != nil { //nolint:gosec // the mode under test
		t.Fatal(err)
	}
	if err := executeRoot(t, "--base-url", srv.URL, "-o", "json", "--debug-file", logPath, "auth", "status"); err != nil {
		t.Fatalf("auth status: %v", err)
	}
	fi, err := os.Stat(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if mode := fi.Mode().Perm(); mode != 0o600 {
		t.Errorf("--debug-file mode = %o, want 600", mode)
	}
}
