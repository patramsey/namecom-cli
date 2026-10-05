package cmd

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/patramsey/namecom-cli/internal/output"
)

// runCapturingStderr runs `namecom <args> -o table` through the real root,
// with spinners recorded, and returns the recorder, what reached stderr, and
// the command's error.
func runCapturingStderr(t *testing.T, srv *httptest.Server, args []string) (*output.SpinRecorder, string, error) {
	t.Helper()
	rec, restore := output.RecordSpinners()
	t.Cleanup(restore)
	t.Cleanup(output.StubInteractive(false))

	devnull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("opening %s: %v", os.DevNull, err)
	}
	errFile, err := os.Create(filepath.Join(t.TempDir(), "stderr"))
	if err != nil {
		t.Fatalf("creating stderr file: %v", err)
	}
	stdout, stderr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = devnull, errFile
	t.Cleanup(func() { os.Stdout, os.Stderr = stdout, stderr; _ = devnull.Close(); _ = errFile.Close() })

	prev := gf
	t.Cleanup(func() { gf = prev; rootCmd.SetArgs(nil) })
	rootCmd.SetArgs(append([]string{"--base-url", srv.URL, "--yes", "-o", "table"}, args...))
	runErr := rootCmd.ExecuteContext(context.Background())

	got, err := os.ReadFile(errFile.Name())
	if err != nil {
		t.Fatalf("reading stderr: %v", err)
	}
	return rec, string(got), runErr
}

// TestRetry_ShownInSpinner pins the spinner half of #232: a retry was printed
// as its own line over the spinner's frame. It now goes into the spinner's
// text, and nothing is printed.
func TestRetry_ShownInSpinner(t *testing.T) {
	withConfig(t, loneProfile)
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if calls.Add(1) == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"message":"Too Many Requests"}`))
			return
		}
		_, _ = w.Write([]byte(spinDomain))
	}))
	t.Cleanup(srv.Close)

	rec, stderr, err := runCapturingStderr(t, srv, []string{"domain", "get", "example.com"})
	if err != nil {
		t.Fatalf("domain get: %v", err)
	}
	started := rec.Started()
	if len(started) == 0 {
		t.Fatal("domain get started no spinner; the case tests nothing")
	}
	want := started[0] + " rate limited, retrying in 0s (1/3)"
	shown := rec.Shown()
	found := false
	for _, s := range shown {
		found = found || s == want
	}
	if !found {
		t.Errorf("spinner never showed %q; shown %q", want, shown)
	}
	if strings.Contains(stderr, "retrying") {
		t.Errorf("retry printed on stderr as well as in the spinner: %q", stderr)
	}
}

// TestRetry_CancelledStatusRequestsAreSilent reproduces #232 end to end:
// `status` runs its requests in parallel, and when one failed the cancelled
// rest were each announced as "retrying (attempt 1, waiting 1s)…". --debug
// is on so a retry line would be printed even without a terminal.
func TestRetry_CancelledStatusRequestsAreSilent(t *testing.T) {
	withConfig(t, loneProfile)
	var inFlight sync.WaitGroup
	inFlight.Add(2) // the other two domain lists, which the failure cancels
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "balance"):
			_, _ = w.Write([]byte(`{"balance":1}`))
		case strings.Contains(r.URL.Path, "transfers"):
			_, _ = w.Write([]byte(`{"transfers":[]}`))
		case r.URL.Query().Get("locked") == "false":
			inFlight.Wait()
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"message":"Unauthorized"}`))
		default:
			inFlight.Done()
			<-r.Context().Done()
		}
	}))
	t.Cleanup(srv.Close)

	rec, stderr, err := runCapturingStderr(t, srv, []string{"--debug", "status"})
	if err == nil {
		t.Fatal("status succeeded; want the 401")
	}
	// The two blocked domain lists are always cancelled, and each logs a
	// failed attempt whose error is the 401 (errgroup's cancellation cause),
	// not "context canceled". The balance and transfers requests answer at
	// once but can still be in flight when the 401 cancels the group — seen
	// on the Windows runner — so they may add to the count. Only "at least
	// two" is deterministic; what matters is below: none is resent or
	// announced.
	if n := strings.Count(stderr, "← error:"); n < 2 {
		t.Fatalf("want at least 2 cancelled requests in the debug log, got %d; stderr:\n%s", n, stderr)
	}
	if strings.Contains(stderr, "(retry") {
		t.Errorf("a cancelled request was resent:\n%s", stderr)
	}
	if strings.Contains(stderr, "retrying") {
		t.Errorf("cancelled requests were announced as retries:\n%s", stderr)
	}
	for _, s := range rec.Shown() {
		if strings.Contains(s, "retrying") {
			t.Errorf("spinner announced a retry for a cancelled request: %q", s)
		}
	}
}
