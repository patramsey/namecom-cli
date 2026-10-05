package dns

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/patramsey/namecom-cli/cmd/cmdutil"
	"github.com/patramsey/namecom-cli/internal/api"
)

// deleteServer serves records 1, 2 and 3 of example.com and records the ID
// of every DELETE. A DELETE of failID is refused with a 400.
func deleteServer(t *testing.T, failID string) (*httptest.Server, *[]string) {
	t.Helper()
	var mu sync.Mutex
	var deleted []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		id, ok := strings.CutPrefix(r.URL.Path, "/core/v1/domains/example.com/records/")
		if !ok || !slices.Contains([]string{"1", "2", "3"}, id) {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"Not Found"}`))
			return
		}
		switch r.Method {
		case http.MethodGet:
			_, _ = fmt.Fprintf(w, `{"id":%s,"host":"h%s","type":"A","answer":"10.0.0.%s","ttl":300}`, id, id, id) //nolint:gosec // test stub; id is one of 1, 2, 3
		case http.MethodDelete:
			mu.Lock()
			deleted = append(deleted, id)
			mu.Unlock()
			if id == failID {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"message":"Invalid Argument"}`))
				return
			}
			_, _ = w.Write([]byte(`{}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &deleted
}

// TestDNSDelete_SeveralIDs covers #244: `dns delete D 1 2 3` failed with "too
// many arguments". It now fetches every record, asks once — listing each —
// and deletes them in order. A repeated ID is deleted once.
func TestDNSDelete_SeveralIDs(t *testing.T) {
	srv, deleted := deleteServer(t, "")
	var prompts []string
	defer cmdutil.StubConfirm(func(p string) bool { prompts = append(prompts, p); return true })()

	cmd := cmdForDelete(t, srv)
	if err := runDelete(cmd, []string{"example.com", "1", "3", "2", "3"}); err != nil {
		t.Fatalf("runDelete: %v", err)
	}
	if !slices.Equal(*deleted, []string{"1", "3", "2"}) {
		t.Errorf("deleted %q, want 1, 3, 2", *deleted)
	}
	want := "Delete these 3 records from example.com?\n" +
		"  A h1 → 10.0.0.1 (TTL 300)\n" +
		"  A h3 → 10.0.0.3 (TTL 300)\n" +
		"  A h2 → 10.0.0.2 (TTL 300)"
	if len(prompts) != 1 || prompts[0] != want {
		t.Errorf("prompts = %q\nwant one: %q", prompts, want)
	}
	got := cmdutil.Out(cmd).Writer.(*bytes.Buffer).String()
	for _, id := range []string{"1", "2", "3"} {
		if !strings.Contains(got, "Deleted record "+id+" from example.com") {
			t.Errorf("output does not report record %s deleted:\n%s", id, got)
		}
	}
}

// TestDNSDelete_SeveralIDsStopAtTheFirstFailure: a refused DELETE stops the
// rest. The records deleted before it are reported, the error says how many
// that was, and the API error stays reachable for the exit code.
func TestDNSDelete_SeveralIDsStopAtTheFirstFailure(t *testing.T) {
	srv, deleted := deleteServer(t, "2")
	defer cmdutil.StubConfirm(func(string) bool { return true })()

	cmd := cmdForDelete(t, srv)
	err := runDelete(cmd, []string{"example.com", "1", "2", "3"})
	if err == nil || !strings.Contains(err.Error(), "deleting record 2:") ||
		!strings.Contains(err.Error(), "stopped after deleting 1 of 3 records") {
		t.Fatalf("runDelete = %v, want it to name record 2 and stop after one of three", err)
	}
	if apiErr, ok := errors.AsType[*api.APIError](err); !ok || apiErr.StatusCode != http.StatusBadRequest {
		t.Errorf("the API error must stay reachable, got %T", err)
	}
	if !slices.Equal(*deleted, []string{"1", "2"}) {
		t.Errorf("deleted %q, want 1 and 2 and nothing after the failure", *deleted)
	}
	got := cmdutil.Out(cmd).Writer.(*bytes.Buffer).String()
	if !strings.Contains(got, "Deleted record 1 from example.com") || strings.Contains(got, "Deleted record 3") {
		t.Errorf("want record 1 reported deleted and record 3 not:\n%s", got)
	}
}

// TestDNSDelete_SeveralIDsMissingOneDeletesNothing: every record is fetched
// before the prompt, so one missing ID fails the command before any DELETE.
func TestDNSDelete_SeveralIDsMissingOneDeletesNothing(t *testing.T) {
	srv, deleted := deleteServer(t, "")
	defer cmdutil.StubConfirm(func(p string) bool { t.Errorf("unexpected prompt %q", p); return true })()

	err := runDelete(cmdForDelete(t, srv), []string{"example.com", "1", "99"})
	if !cmdutil.IsNotFound(err) || !strings.Contains(err.Error(), "record 99 not found") {
		t.Errorf("runDelete = %v, want not-found naming record 99", err)
	}
	if len(*deleted) != 0 {
		t.Errorf("deleted %q although record 99 does not exist", *deleted)
	}
}
