package url

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/patramsey/namecom-cli/cmd/cmdutil"
)

// TestURLCreate_FQDNHost pins --host read as `dns create` reads it: a fully
// qualified host in the zone is made relative, and the domain itself is the
// apex. Sent as typed, fq.example.com became a forwarding for
// fq.example.com.example.com, and a trailing dot was an "empty label" error.
func TestURLCreate_FQDNHost(t *testing.T) {
	for in, want := range map[string]string{
		"fq":               "fq",
		"fq.example.com":   "fq",
		"fq.example.com.":  "fq",
		"example.com":      "@",
		"example.com.":     "@",
		"*.example.com":    "*",
		"a.b.example.com.": "a.b",
	} {
		t.Run(in, func(t *testing.T) {
			var sent []byte
			n := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				n++
				sent, _ = io.ReadAll(r.Body)
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"id":7,"host":"` + want + `","forwardsTo":"https://example.org","type":"redirect"}`))
			}))
			t.Cleanup(srv.Close)
			cmd := cmdForURLCreate(t, srv)
			if err := cmd.ParseFlags([]string{"--to", "https://example.org", "--host", in}); err != nil {
				t.Fatalf("ParseFlags: %v", err)
			}
			if err := runCreate(cmd, []string{"example.com"}); err != nil {
				t.Fatalf("runCreate: %v", err)
			}
			var body struct {
				Host string `json:"host"`
			}
			if err := json.Unmarshal(sent, &body); err != nil {
				t.Fatalf("body %s: %v", sent, err)
			}
			if body.Host != want {
				t.Errorf("sent host %q, want %q", body.Host, want)
			}
			if n != 1 {
				t.Errorf("sent %d requests, want 1", n)
			}
			stdout := cmdutil.Out(cmd).Writer.(*bytes.Buffer).String()
			if !strings.Contains(stdout, ": "+want+" → ") {
				t.Errorf("success line %q should name the host sent, %q", stdout, want)
			}
		})
	}
}

// A trailing-dot name outside the zone is absolute: a usage error before any
// request, not a forwarding under the zone.
func TestURLCreate_OutOfZoneAbsoluteHost(t *testing.T) {
	cmd := cmdForURLCreate(t, neverCalledServer(t))
	if err := cmd.ParseFlags([]string{"--to", "https://example.org", "--host", "sweep.other.com."}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	err := runCreate(cmd, []string{"example.com"})
	// The value quoted, as `dns create` says it (#323).
	if _, ok := errors.AsType[*cmdutil.UsageError](err); !ok || err.Error() != `--host "sweep.other.com." is not in example.com` {
		t.Fatalf("runCreate = %v, want a usage error saying the host is not in example.com", err)
	}
}
