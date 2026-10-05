package domain

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	coreapigo "github.com/namedotcom/core-api-go"
	"github.com/patramsey/namecom-cli/cmd/cmdutil"
	"github.com/patramsey/namecom-cli/internal/api"
)

// toggleServer answers GET /core/v1/domains/{d} with each domain's current
// state from state (true: every setting is on), and records every PATCH by
// domain. A PATCH for failOn is refused with a 400.
func toggleServer(t *testing.T, state map[string]bool, failOn string) (*httptest.Server, *[]string) {
	t.Helper()
	var mu sync.Mutex
	var patched []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		d := strings.TrimPrefix(r.URL.Path, "/core/v1/domains/")
		on, ok := state[d]
		if !ok {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
			http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
			return
		}
		switch r.Method {
		case http.MethodGet:
			v := fmt.Sprint(on)
			_, _ = fmt.Fprintf(w, `{"domainName":%q,"locked":%s,"autorenewEnabled":%s,"privacyEnabled":%s}`, d, v, v, v) //nolint:gosec // test stub; d is one of state's keys
		case http.MethodPatch:
			mu.Lock()
			patched = append(patched, d)
			mu.Unlock()
			if d == failOn {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"message":"Invalid Argument"}`))
				return
			}
			_, _ = fmt.Fprintf(w, `{"domainName":%q}`, d) //nolint:gosec // test stub; d is one of state's keys
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &patched
}

// TestToggle_SeveralDomainsConfirmOnce covers #244's multi-domain writes: a
// toggle given several domains reads each, skips the one already in the
// requested state, asks once — listing every domain the yes approves — and
// then changes them in order.
func TestToggle_SeveralDomainsConfirmOnce(t *testing.T) {
	srv, patched := toggleServer(t, map[string]bool{"a.com": true, "b.com": false, "c.com": true}, "")
	var prompts []string
	defer cmdutil.StubConfirm(func(p string) bool { prompts = append(prompts, p); return true })()

	cmd := withRootFlags(t, baseCmd(t, srv))
	if err := runLock(cmd, []string{"off", "a.com", "b.com", "c.com"}); err != nil {
		t.Fatalf("runLock: %v", err)
	}
	if !slices.Equal(*patched, []string{"a.com", "c.com"}) {
		t.Errorf("patched %q, want a.com then c.com (b.com is already unlocked)", *patched)
	}
	want := "Remove the transfer lock on these 2 domains? Anyone with a domain's auth code can then transfer it away.\n  a.com\n  c.com"
	if len(prompts) != 1 || prompts[0] != want {
		t.Errorf("prompts = %q\nwant one: %q", prompts, want)
	}
	got := cmdutil.Out(cmd).Writer.(*bytes.Buffer).String()
	for _, line := range []string{
		"Transfer lock is already off for b.com; nothing to change",
		"Transfer lock disabled for a.com",
		"Transfer lock disabled for c.com",
	} {
		if !strings.Contains(got, line) {
			t.Errorf("output is missing %q:\n%s", line, got)
		}
	}
}

// TestToggle_SeveralDomainsDecline: one no sends nothing at all.
func TestToggle_SeveralDomainsDecline(t *testing.T) {
	srv, patched := toggleServer(t, map[string]bool{"a.com": false, "b.com": false}, "")
	defer cmdutil.StubConfirm(func(string) bool { return false })()

	err := runAutorenew(withRootFlags(t, baseCmd(t, srv)), []string{"on", "a.com", "b.com"})
	if !errors.Is(err, cmdutil.ErrAborted) {
		t.Errorf("runAutorenew = %v, want ErrAborted", err)
	}
	if len(*patched) != 0 {
		t.Errorf("patched %q after a decline", *patched)
	}
}

// TestToggle_StopsAtTheFirstFailure: a refused change stops the rest, and the
// error says how many were made before it, while the API error stays
// reachable for the exit code.
func TestToggle_StopsAtTheFirstFailure(t *testing.T) {
	srv, patched := toggleServer(t, map[string]bool{"a.com": false, "b.com": false, "c.com": false}, "b.com")
	defer cmdutil.StubConfirm(func(string) bool { return true })()

	cmd := withRootFlags(t, baseCmd(t, srv))
	err := runAutorenew(cmd, []string{"a.com", "b.com", "c.com", "on"})
	if err == nil || !strings.Contains(err.Error(), "stopped after changing 1 of 3 domains") {
		t.Fatalf("runAutorenew = %v, want it to stop after one of three", err)
	}
	if apiErr, ok := errors.AsType[*api.APIError](err); !ok || apiErr.StatusCode != http.StatusBadRequest {
		t.Errorf("the API error must stay reachable, got %T", err)
	}
	if !slices.Equal(*patched, []string{"a.com", "b.com"}) {
		t.Errorf("patched %q, want a.com and b.com and nothing after the failure", *patched)
	}
	if got := cmdutil.Out(cmd).Writer.(*bytes.Buffer).String(); !strings.Contains(got, "Auto-renewal enabled for a.com") {
		t.Errorf("the change made before the failure must be reported:\n%s", got)
	}
}

// TestToggle_DomainsFromStdin: "-" reads the domains from stdin. privacy on
// does not confirm, so none is asked even for several.
func TestToggle_DomainsFromStdin(t *testing.T) {
	srv, patched := toggleServer(t, map[string]bool{"a.com": false, "b.com": false}, "")
	defer cmdutil.StubConfirm(func(p string) bool { t.Errorf("unexpected prompt %q", p); return false })()

	cmd := withRootFlags(t, baseCmd(t, srv))
	cmd.SetIn(strings.NewReader("# privacy for all\na.com\n\nB.com\n"))
	if err := runPrivacy(cmd, []string{"on", "-"}); err != nil {
		t.Fatalf("runPrivacy: %v", err)
	}
	if !slices.Equal(*patched, []string{"a.com", "b.com"}) {
		t.Errorf("patched %q, want a.com and b.com", *patched)
	}
}

// TestGet_SeveralDomains covers `domain get` with several domains (#244): one
// named domain keeps the single JSON object; several, or "-" even when stdin
// holds one, are an array in the order given.
func TestGet_SeveralDomains(t *testing.T) {
	srv, _ := toggleServer(t, map[string]bool{"a.com": true, "b.com": false}, "")
	for _, tc := range []struct {
		name, stdin string
		args, want  []string
		array       bool
	}{
		{"one", "", []string{"a.com"}, []string{"a.com"}, false},
		{"two", "", []string{"b.com", "a.com"}, []string{"b.com", "a.com"}, true},
		{"stdin with one line", "a.com\n", []string{"-"}, []string{"a.com"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd, buf := cmdForCheckJSON(t, srv)
			cmd.SetIn(strings.NewReader(tc.stdin))
			if err := runGet(cmd, tc.args); err != nil {
				t.Fatalf("runGet: %v", err)
			}
			var got []string
			if tc.array {
				var ds []coreapigo.DomainResponsePayload
				if err := json.Unmarshal(buf.Bytes(), &ds); err != nil {
					t.Fatalf("want a JSON array: %v\n%s", err, buf.String())
				}
				for _, d := range ds {
					got = append(got, d.DomainName)
				}
			} else {
				var d coreapigo.DomainResponsePayload
				if err := json.Unmarshal(buf.Bytes(), &d); err != nil {
					t.Fatalf("want a JSON object: %v\n%s", err, buf.String())
				}
				got = []string{d.DomainName}
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// TestCheck_NamesFromStdin: `domain check -` reads the names from stdin
// rather than failing "-" as a domain name (#244).
func TestCheck_NamesFromStdin(t *testing.T) {
	s := &chunkServer{}
	srv := httptest.NewServer(s.handler(t))
	t.Cleanup(srv.Close)

	cmd, buf := cmdForCheckJSON(t, srv)
	cmd.SetIn(strings.NewReader("name001.com\n# skip me\n\nNAME002.com\r\n"))
	if err := runCheck(cmd, []string{"name000.com", "-"}); err != nil {
		t.Fatalf("runCheck: %v", err)
	}
	var got []*coreapigo.SearchResult
	if err := unmarshalData(buf.Bytes(), &got); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, buf.String())
	}
	var names []string
	for _, r := range got {
		names = append(names, r.DomainName)
	}
	if want := []string{"name000.com", "name001.com", "name002.com"}; !slices.Equal(names, want) {
		t.Errorf("checked %q, want %q", names, want)
	}
}
