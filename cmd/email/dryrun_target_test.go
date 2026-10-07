package email

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/patramsey/namecom-cli/cmd/cmdutil"
	"github.com/patramsey/namecom-cli/internal/output"
)

// mailboxServer answers a GET of the mailbox with mailbox, or a 404 when it
// is "", and any other request with `{}`, recording each as "METHOD /path".
func mailboxServer(t *testing.T, mailbox string, seen *[]string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*seen = append(*seen, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet && mailbox == "" {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"Not Found"}`))
			return
		}
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(mailbox))
			return
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

const getInfo = "GET /core/v1/domains/example.com/email/forwarding/info"

// TestEmailDryRun_ChecksTheMailbox pins ISSUE-07: email update and delete
// previewed a request for a mailbox that did not exist, and exited 0, where
// the real run exits 4. A dry run now checks the mailbox with one GET, as
// `url delete --dry-run` and `dns delete --dry-run` check theirs.
func TestEmailDryRun_ChecksTheMailbox(t *testing.T) {
	for name, run := range map[string]func(*httptest.Server) error{
		"update": func(srv *httptest.Server) error {
			cmd := withDryRun(t, cmdForEmailUpdate(t, srv), true)
			if err := cmd.ParseFlags([]string{"--to", "new@example.org"}); err != nil {
				t.Fatalf("ParseFlags: %v", err)
			}
			return runUpdate(cmd, []string{"example.com", "info"})
		},
		"delete": func(srv *httptest.Server) error {
			return runDelete(withDryRun(t, cmdForEmailDelete(t, srv), true), []string{"example.com", "info"})
		},
	} {
		t.Run(name+" of a missing mailbox", func(t *testing.T) {
			var seen []string
			err := run(mailboxServer(t, "", &seen))
			if !cmdutil.IsNotFound(err) || !strings.Contains(err.Error(), "mailbox info@example.com not found") {
				t.Errorf("dry run = %v, want the mailbox not found", err)
			}
			if strings.Join(seen, ",") != getInfo {
				t.Errorf("sent %v, want only %s", seen, getInfo)
			}
		})
		t.Run(name+" of an existing mailbox", func(t *testing.T) {
			var seen []string
			if err := run(mailboxServer(t, stubInfo, &seen)); err != nil {
				t.Errorf("dry run = %v, want the preview", err)
			}
			if strings.Join(seen, ",") != getInfo {
				t.Errorf("sent %v, want only %s", seen, getInfo)
			}
		})
	}
}

const stubInfo = `{"domainName":"example.com","emailBox":"info","emailTo":"old@example.org"}`

// A dry-run create of a mailbox that already forwards elsewhere is the
// conflict the real create reports; one that is missing, or already forwards
// to --to, previews the create.
func TestEmailCreateDryRun_ChecksForAnExistingMailbox(t *testing.T) {
	for name, tc := range map[string]struct {
		mailbox, to string
		conflict    bool
	}{
		"forwards elsewhere": {stubInfo, "new@example.org", true},
		"forwards there":     {stubInfo, "Old@example.org", false},
		"missing":            {"", "new@example.org", false},
	} {
		t.Run(name, func(t *testing.T) {
			var seen []string
			cmd := withDryRun(t, cmdForEmailCreate(t, mailboxServer(t, tc.mailbox, &seen)), true)
			if err := cmd.ParseFlags([]string{"--to", tc.to}); err != nil {
				t.Fatalf("ParseFlags: %v", err)
			}
			err := runCreate(cmd, []string{"example.com", "info"})
			_, isConflict := errors.AsType[*cmdutil.ConflictError](err)
			if isConflict != tc.conflict || (!tc.conflict && err != nil) {
				t.Errorf("dry run = %v, want a conflict: %v", err, tc.conflict)
			}
			if strings.Join(seen, ",") != getInfo {
				t.Errorf("sent %v, want only %s", seen, getInfo)
			}
			stdout := cmdutil.Out(cmd).Writer.(*bytes.Buffer).String()
			if got := strings.Contains(stdout, "POST"); got == tc.conflict {
				t.Errorf("stdout = %q; want the POST previewed: %v", stdout, !tc.conflict)
			}
		})
	}
}

// TestEmailNotes_ReachJSON pins ISSUE-07: the notes about the DNS records
// forwarding adds and leaves printed only in a table. In JSON they are
// warnings, which come out as {"warnings": […]} on stderr.
func TestEmailNotes_ReachJSON(t *testing.T) {
	for name, tc := range map[string]struct {
		run  func(*httptest.Server) (*output.Config, error)
		want string
	}{
		"create": {func(srv *httptest.Server) (*output.Config, error) {
			cmd := cmdForEmailCreate(t, srv)
			cmdutil.Out(cmd).Format = output.FormatJSON
			if err := cmd.ParseFlags([]string{"--to", "old@example.org"}); err != nil {
				t.Fatalf("ParseFlags: %v", err)
			}
			return cmdutil.Out(cmd), runCreate(cmd, []string{"example.com", "info"})
		}, "mx3.name.com"},
		"delete": {func(srv *httptest.Server) (*output.Config, error) {
			cmd := withDryRun(t, cmdForEmailDelete(t, srv), false)
			cmdutil.Out(cmd).Format = output.FormatJSON
			return cmdutil.Out(cmd), runDelete(cmd, []string{"example.com", "info"})
		}, "MX and SPF records"},
	} {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(stubInfo))
			}))
			t.Cleanup(srv.Close)
			out, err := tc.run(srv)
			if err != nil {
				t.Fatalf("run: %v", err)
			}
			if !json.Valid(out.Writer.(*bytes.Buffer).Bytes()) {
				t.Errorf("stdout is not JSON: %s", out.Writer.(*bytes.Buffer).String())
			}
			if got := out.EWriter.(*bytes.Buffer).String(); got != "" {
				t.Errorf("stderr = %q before the warnings are flushed, want nothing", got)
			}
			if w := strings.Join(out.TakeWarnings(), "\n"); !strings.Contains(w, tc.want) {
				t.Errorf("warnings = %q, want the note about %q", w, tc.want)
			}
		})
	}
}
