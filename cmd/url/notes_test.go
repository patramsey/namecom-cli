package url

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/patramsey/namecom-cli/cmd/cmdutil"
	"github.com/patramsey/namecom-cli/internal/output"
)

// TestURLUpdate_MissingIDNamesIt pins #313: update of a missing ID
// showed the API's "URL forwarding entry not found.", where get and delete
// name the ID and domain and say how to list them.
func TestURLUpdate_MissingIDNamesIt(t *testing.T) {
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"URL forwarding entry not found."}`))
	}))
	t.Cleanup(srv.Close)

	cmd := cmdForURLUpdate(t, srv)
	if err := cmd.ParseFlags([]string{"--to", "https://example.org/b"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	err := runUpdate(cmd, []string{"example.com", "99999"})
	if !cmdutil.IsNotFound(err) || err.Error() != "URL forwarding 99999 not found on example.com" {
		t.Fatalf("runUpdate = %v, want the not-found url get gives", err)
	}
	var h interface{ UserHint() string }
	if !errors.As(err, &h) || !strings.Contains(h.UserHint(), "namecom url list example.com") {
		t.Errorf("hint should say how to list the forwarding IDs: %v", err)
	}
	if got := n.Load(); got != 1 {
		t.Errorf("sent %d requests, want 1", got)
	}
}

// TestURLDelete_ApexNoteReachesJSON pins #313: the note that an apex
// delete leaves its A record printed only in a table. It is the only pointer
// to the record that makes a later apex create answer Duplicate Record, so
// in JSON it is a warning, on stderr as {"warnings": […]}.
func TestURLDelete_ApexNoteReachesJSON(t *testing.T) {
	var n atomic.Int32
	srv := fixedServer(t, `{"id":7,"host":"","forwardsTo":"https://acme.io","type":"redirect"}`, `{}`, &n)
	cmd := cmdForURLDelete(t, srv)
	out := cmdutil.Out(cmd)
	out.Format = output.FormatJSON
	if err := runDelete(cmd, []string{"example.com", "7"}); err != nil {
		t.Fatalf("runDelete: %v", err)
	}
	if stdout := out.Writer.(*bytes.Buffer).Bytes(); !json.Valid(stdout) {
		t.Errorf("stdout is not one JSON document: %s", stdout)
	}
	if w := strings.Join(out.TakeWarnings(), "\n"); !strings.Contains(w, "A record") || !strings.Contains(w, "namecom dns delete example.com") {
		t.Errorf("warnings = %q, want the apex A record note", w)
	}
	if got := n.Load(); got != 2 {
		t.Errorf("sent %d requests, want 2 (GET, DELETE)", got)
	}
}
