package email

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/patramsey/namecom-cli/cmd/cmdutil"
)

// countingServer answers every request with body and counts them.
func countingServer(t *testing.T, body string, n *atomic.Int32) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestEmailCreate_ExistingMailboxIsConflict pins #283: the API answers a
// create for a mailbox that already exists with 200 and the existing entry,
// unchanged. The CLI printed "Created … → <--to>" and exited 0. It now fails
// with a conflict naming where the mailbox really forwards, from the one
// request it already made.
func TestEmailCreate_ExistingMailboxIsConflict(t *testing.T) {
	var n atomic.Int32
	srv := countingServer(t, `{"domainName":"example.com","emailBox":"info","emailTo":"old@example.org"}`, &n)

	cmd := cmdForEmailCreate(t, srv)
	if err := cmd.ParseFlags([]string{"--to", "new@example.org"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	err := runCreate(cmd, []string{"example.com", "info"})

	var conflict *cmdutil.ConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("runCreate = %v, want a *cmdutil.ConflictError", err)
	}
	if want := "info@example.com already forwards to old@example.org"; !strings.Contains(err.Error(), want) {
		t.Errorf("error = %q, want it to contain %q", err, want)
	}
	if !strings.Contains(conflict.UserHint(), "namecom email update example.com info --to new@example.org") {
		t.Errorf("hint = %q, want the email update command", conflict.UserHint())
	}
	if got := cmdutil.Out(cmd).Writer.(*bytes.Buffer).String(); got != "" {
		t.Errorf("stdout = %q, want nothing for a create that changed nothing", got)
	}
	if got := n.Load(); got != 1 {
		t.Errorf("sent %d requests, want 1", got)
	}
}

// TestEmailCreate_SuccessPrintsTheResponse pins the other half of #283: the
// success line printed the --to flag. It prints the API's answer, so the
// address shown is the one stored.
func TestEmailCreate_SuccessPrintsTheResponse(t *testing.T) {
	var n atomic.Int32
	srv := countingServer(t, `{"domainName":"example.com","emailBox":"info","emailTo":"me@example.org"}`, &n)

	cmd := cmdForEmailCreate(t, srv)
	// The same address in another case is the same forward, not a conflict.
	if err := cmd.ParseFlags([]string{"--to", "Me@Example.org"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	if err := runCreate(cmd, []string{"example.com", "info"}); err != nil {
		t.Fatalf("runCreate: %v", err)
	}
	if got, want := cmdutil.Out(cmd).Writer.(*bytes.Buffer).String(), "Created forwarding info@example.com → me@example.org"; !strings.Contains(got, want) {
		t.Errorf("stdout = %q, want %q", got, want)
	}
	if got := n.Load(); got != 1 {
		t.Errorf("sent %d requests, want 1", got)
	}
}
