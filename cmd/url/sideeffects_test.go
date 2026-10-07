package url

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/patramsey/namecom-cli/cmd/cmdutil"
	"github.com/patramsey/namecom-cli/internal/output"
)

// fixedServer answers a GET with get and anything else with other, counting
// the requests.
func fixedServer(t *testing.T, get, other string, n *atomic.Int32) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(get))
			return
		}
		_, _ = w.Write([]byte(other))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestURLDelete_ApexSaysTheARecordStays pins #286: deleting an apex
// forwarding leaves the A record name.com added for it, and nothing said so.
// A subdomain's A record goes with it, so nothing is said there. No request
// is added: delete already fetches the forwarding for its prompt.
func TestURLDelete_ApexSaysTheARecordStays(t *testing.T) {
	for _, tc := range []struct {
		name, host string
		wantNote   bool
	}{
		{"apex", `""`, true},
		{"apex @", `"@"`, true},
		{"subdomain", `"www"`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var n atomic.Int32
			srv := fixedServer(t, `{"id":7,"host":`+tc.host+`,"forwardsTo":"https://acme.io","type":"redirect"}`, `{}`, &n)
			cmd := cmdForURLDelete(t, srv)
			if err := runDelete(cmd, []string{"example.com", "7"}); err != nil {
				t.Fatalf("runDelete: %v", err)
			}
			stderr := cmdutil.Out(cmd).EWriter.(*bytes.Buffer).String()
			if got := strings.Contains(stderr, "A record"); got != tc.wantNote {
				t.Errorf("stderr = %q, want a note about the A record: %v", stderr, tc.wantNote)
			}
			if tc.wantNote && !strings.Contains(stderr, "namecom dns delete example.com") {
				t.Errorf("stderr = %q, want the dns delete command", stderr)
			}
			if got := n.Load(); got != 2 {
				t.Errorf("sent %d requests, want 2 (GET, DELETE)", got)
			}
		})
	}
	if !strings.Contains(createCmd.Long, "apex") || !strings.Contains(createCmd.Long, "A record") {
		t.Error("create's help should say forwarding the apex adds an A record that delete leaves")
	}
}

// TestURLCreate_TitleMetaNeedMasked pins #286: --title and --meta are
// "(masked only)" but were sent and stored on a redirect. They are now a
// usage error unless --type is masked, before any request.
func TestURLCreate_TitleMetaNeedMasked(t *testing.T) {
	for _, flags := range [][]string{
		{"--title", "T"},
		{"--meta", "M", "--type", "302"},
	} {
		t.Run(strings.Join(flags, " "), func(t *testing.T) {
			cmd := cmdForURLCreate(t, neverCalledServer(t))
			t.Cleanup(func() { createForwardsTo = ""; createType = "redirect"; createTitle = ""; createMeta = "" })
			if err := cmd.ParseFlags(append([]string{"--to", "https://example.org"}, flags...)); err != nil {
				t.Fatalf("ParseFlags: %v", err)
			}
			err := runCreate(cmd, []string{"example.com"})
			if _, ok := errors.AsType[*cmdutil.UsageError](err); !ok || !strings.Contains(err.Error(), "masked") {
				t.Errorf("runCreate = %v, want a usage error naming masked", err)
			}
		})
	}
}

// TestURLUpdate_TitleMetaNeedMasked is TestURLCreate_TitleMetaNeedMasked for
// update, where the type that counts is the one the forwarding will have:
// --type when passed, which is checked before the GET, else the current one.
// An empty --title still clears a title stored on a redirect.
func TestURLUpdate_TitleMetaNeedMasked(t *testing.T) {
	defer output.StubInteractive(false)()
	const redirect = `{"id":1,"host":"@","forwardsTo":"https://keep.example","type":"redirect","title":"Old"}`
	const masked = `{"id":1,"host":"@","forwardsTo":"https://keep.example","type":"masked","title":"Old"}`
	for _, tc := range []struct {
		name, get    string
		flags        []string
		wantErr      bool
		wantRequests int32
	}{
		{"title on a redirect", redirect, []string{"--title", "X"}, true, 1},
		{"meta with --type 302", masked, []string{"--type", "302", "--meta", "M"}, true, 0},
		{"title with --type masked", redirect, []string{"--type", "masked", "--title", "X"}, false, 2},
		{"clearing a redirect's title", redirect, []string{"--title", ""}, false, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var n atomic.Int32
			srv := fixedServer(t, tc.get, tc.get, &n)
			cmd := cmdForURLUpdate(t, srv)
			if err := cmd.ParseFlags(tc.flags); err != nil {
				t.Fatalf("ParseFlags: %v", err)
			}
			err := runUpdate(cmd, []string{"example.com", "1"})
			_, usage := errors.AsType[*cmdutil.UsageError](err)
			if tc.wantErr && !usage {
				t.Errorf("runUpdate = %v, want a usage error", err)
			}
			if !tc.wantErr && err != nil {
				t.Errorf("runUpdate = %v, want success", err)
			}
			if got := n.Load(); got != tc.wantRequests {
				t.Errorf("sent %d requests, want %d", got, tc.wantRequests)
			}
		})
	}
}

// TestURLUpdate_LeavingMaskedWarnsOfKeptTitle pins #286: switching a masked
// forwarding to a redirect kept its title and meta without a word. They are
// still kept, so switching back restores them, and a warning says so.
func TestURLUpdate_LeavingMaskedWarnsOfKeptTitle(t *testing.T) {
	defer output.StubInteractive(false)()
	var n atomic.Int32
	const masked = `{"id":1,"host":"@","forwardsTo":"https://keep.example","type":"masked","title":"Old","meta":"M"}`
	srv := fixedServer(t, masked, masked, &n)
	cmd := cmdForURLUpdate(t, srv)
	if err := cmd.ParseFlags([]string{"--type", "redirect"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	if err := runUpdate(cmd, []string{"example.com", "1"}); err != nil {
		t.Fatalf("runUpdate: %v", err)
	}
	stderr := cmdutil.Out(cmd).EWriter.(*bytes.Buffer).String()
	if !strings.Contains(stderr, "title and meta") || !strings.Contains(stderr, `--title ""`) {
		t.Errorf("stderr = %q, want a warning that the title and meta are kept", stderr)
	}
	if got := n.Load(); got != 2 {
		t.Errorf("sent %d requests, want 2 (GET, PATCH)", got)
	}
}
