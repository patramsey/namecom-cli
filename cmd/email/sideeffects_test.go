package email

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/patramsey/namecom-cli/cmd/cmdutil"
	"github.com/patramsey/namecom-cli/internal/output"
)

func stderrOf(t *testing.T, cmdOut *output.Config) string {
	t.Helper()
	return cmdOut.EWriter.(*bytes.Buffer).String()
}

// TestEmailCreate_SaysWhichDNSRecordsAreAdded pins #286: name.com adds MX
// and SPF records for forwarding, and nothing said so. Create says it, on
// stderr, without a request to look: one request in all.
func TestEmailCreate_SaysWhichDNSRecordsAreAdded(t *testing.T) {
	var n atomic.Int32
	srv := countingServer(t, `{"domainName":"example.com","emailBox":"info","emailTo":"me@example.org"}`, &n)

	cmd := cmdForEmailCreate(t, srv)
	if err := cmd.ParseFlags([]string{"--to", "me@example.org"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	if err := runCreate(cmd, []string{"example.com", "info"}); err != nil {
		t.Fatalf("runCreate: %v", err)
	}
	stderr := stderrOf(t, cmdutil.Out(cmd))
	for _, want := range []string{"mx3.name.com", "v=spf1 a mx ~all", "namecom dns list example.com --host @"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr = %q, want it to mention %q", stderr, want)
		}
	}
	if got := n.Load(); got != 1 {
		t.Errorf("sent %d requests, want 1", got)
	}
	if !strings.Contains(createCmd.Long, "mx3.name.com") {
		t.Error("create's help should say which records name.com adds")
	}
}

// TestEmailDelete_SaysTheDNSRecordsStay pins #286: deleting a mailbox leaves
// the MX and SPF records forwarding added, and nothing said so.
func TestEmailDelete_SaysTheDNSRecordsStay(t *testing.T) {
	var n atomic.Int32
	srv := countingServer(t, `{}`, &n)

	cmd := cmdForEmailDelete(t, srv)
	if err := runDelete(cmd, []string{"example.com", "info"}); err != nil {
		t.Fatalf("runDelete: %v", err)
	}
	stderr := stderrOf(t, cmdutil.Out(cmd))
	for _, want := range []string{"MX", "SPF", "namecom dns delete example.com"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr = %q, want it to mention %q", stderr, want)
		}
	}
	// --yes: nothing to show a prompt for, so no GET.
	if got := n.Load(); got != 1 {
		t.Errorf("sent %d requests, want 1 (the DELETE)", got)
	}
}

// TestEmailDelete_PromptShowsTheTarget pins #286: the prompt named only the
// mailbox. It now says where the mailbox forwards, which needs a GET, so the
// GET is made only when a prompt is shown; a missing mailbox fails there,
// before the prompt.
func TestEmailDelete_PromptShowsTheTarget(t *testing.T) {
	defer output.StubInteractive(true)()
	for _, tc := range []struct {
		name, body string
		status     int
		wantPrompt string
	}{
		{"present", `{"domainName":"example.com","emailBox":"info","emailTo":"me@example.org"}`, http.StatusOK,
			"Delete forwarding info@example.com → me@example.org?"},
		{"missing", `{"message":"Not Found"}`, http.StatusNotFound, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var prompts []string
			defer cmdutil.StubConfirm(func(p string) bool { prompts = append(prompts, p); return false })()
			var n atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				n.Add(1)
				if r.Method != http.MethodGet {
					t.Errorf("sent %s %s without a yes", r.Method, r.URL)
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			t.Cleanup(srv.Close)

			cmd := cmdForEmailDelete(t, srv)
			if err := cmd.PersistentFlags().Set("yes", "false"); err != nil {
				t.Fatal(err)
			}
			err := runDelete(cmd, []string{"example.com", "info"})
			if got := n.Load(); got != 1 {
				t.Errorf("sent %d requests, want 1 (the GET)", got)
			}
			if tc.wantPrompt == "" {
				if !cmdutil.IsNotFound(err) || !strings.Contains(err.Error(), "mailbox info@example.com not found") {
					t.Errorf("runDelete = %v, want not-found naming the mailbox", err)
				}
				if len(prompts) != 0 {
					t.Errorf("prompted %q for a mailbox that does not exist", prompts)
				}
				return
			}
			if len(prompts) != 1 || prompts[0] != tc.wantPrompt {
				t.Errorf("prompts = %q\nwant      [%q]", prompts, tc.wantPrompt)
			}
		})
	}
}

// TestEmail_NotFoundNamesTheMailbox pins #286: update and delete of a
// missing mailbox said only "Not Found". They now name it and the list
// command, as dns and url do, and still exit 4.
func TestEmail_NotFoundNamesTheMailbox(t *testing.T) {
	const want = "mailbox nosuchbox@example.com not found"
	for _, tc := range []struct {
		name string
		run  func(*httptest.Server) error
	}{
		{"update", func(srv *httptest.Server) error {
			cmd := cmdForEmailUpdate(t, srv)
			if err := cmd.ParseFlags([]string{"--to", "new@example.org"}); err != nil {
				t.Fatal(err)
			}
			return runUpdate(cmd, []string{"example.com", "nosuchbox"})
		}},
		{"delete", func(srv *httptest.Server) error {
			return runDelete(cmdForEmailDelete(t, srv), []string{"example.com", "nosuchbox"})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var n atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				n.Add(1)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"message":"Not Found"}`))
			}))
			t.Cleanup(srv.Close)

			err := tc.run(srv)
			if !cmdutil.IsNotFound(err) || !strings.Contains(err.Error(), want) {
				t.Errorf("err = %v, want not-found containing %q", err, want)
			}
			if got := n.Load(); got != 1 {
				t.Errorf("sent %d requests, want 1", got)
			}
		})
	}
}
