package dns

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/spf13/cobra"

	"github.com/patramsey/namecom-cli/cmd/cmdutil"
	"github.com/patramsey/namecom-cli/internal/drifttest"
)

// sentRecordBody runs a dns write for real and returns the body of its last
// write request (the PUT or POST, after any read-modify-write GET).
func sentRecordBody(t *testing.T, build drifttest.Build, run drifttest.Run, args []string, response string) map[string]any {
	t.Helper()
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			b, _ := io.ReadAll(r.Body)
			if err := json.Unmarshal(b, &body); err != nil {
				t.Errorf("request body is not JSON: %v (%s)", err, b)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(response))
	}))
	t.Cleanup(srv.Close)
	if err := run(drifttest.WithDryRun(t, build(t, srv), false), args); err != nil {
		t.Fatalf("live invocation failed: %v", err)
	}
	if body == nil {
		t.Fatal("no write request was made")
	}
	return body
}

// TestDNSWrites_IDNNamesArePunycode guards #187: a Unicode --host, or a
// Unicode hostname in a CNAME, ANAME, MX, NS or SRV answer, was sent to the
// server as typed. Like a domain argument (#160), it must be sent — and
// previewed by --dry-run — in its ASCII form. TXT answers are free text and
// are left alone.
func TestDNSWrites_IDNNamesArePunycode(t *testing.T) {
	const createResp = `{"id":1,"type":"A","host":"www","answer":"1.2.3.4","ttl":300}`
	const getResp = `{"id":42,"type":"CNAME","host":"www","answer":"example.net","ttl":3600}`
	create := func(flags ...string) drifttest.Build {
		return func(t *testing.T, srv *httptest.Server) *cobra.Command {
			cmd := cmdForCreate(t, srv)
			if err := cmd.ParseFlags(flags); err != nil {
				t.Fatalf("ParseFlags: %v", err)
			}
			return cmd
		}
	}
	update := func(flags ...string) drifttest.Build {
		return func(t *testing.T, srv *httptest.Server) *cobra.Command {
			cmd := cmdForUpdate(t, srv)
			if err := cmd.ParseFlags(flags); err != nil {
				t.Fatalf("ParseFlags: %v", err)
			}
			return cmd
		}
	}
	tests := []struct {
		name       string
		build      drifttest.Build
		run        drifttest.Run
		args       []string
		resp       string
		host, ansr string
	}{
		{"create host", create("--type", "A", "--host", "bücher", "--answer", "1.2.3.4"),
			runCreate, []string{"example.com"}, createResp, "xn--bcher-kva", "1.2.3.4"},
		{"create wildcard IDN host", create("--type", "A", "--host", "*.Bücher", "--answer", "1.2.3.4"),
			runCreate, []string{"example.com"}, createResp, "*.xn--bcher-kva", "1.2.3.4"},
		{"create CNAME target", create("--type", "CNAME", "--host", "www", "--answer", "www.bücher.de."),
			runCreate, []string{"example.com"}, createResp, "www", "www.xn--bcher-kva.de."},
		{"create MX target", create("--type", "MX", "--host", "@", "--answer", "mail.bücher.de", "--priority", "10"),
			runCreate, []string{"example.com"}, createResp, "@", "mail.xn--bcher-kva.de"},
		{"create SRV target", create("--type", "SRV", "--host", "_sip._tcp", "--answer", "0 5060 sip.bücher.de", "--priority", "1"),
			runCreate, []string{"example.com"}, createResp, "_sip._tcp", "0 5060 sip.xn--bcher-kva.de"},
		{"create TXT left alone", create("--type", "TXT", "--host", "@", "--answer", "grüße"),
			runCreate, []string{"example.com"}, createResp, "@", "grüße"},
		{"update host", update("--host", "bücher"),
			runUpdate, []string{"example.com", "42"}, getResp, "xn--bcher-kva", "example.net"},
		{"update CNAME target", update("--answer", "bücher.de"),
			runUpdate, []string{"example.com", "42"}, getResp, "www", "xn--bcher-kva.de"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			body := sentRecordBody(t, tc.build, tc.run, tc.args, tc.resp)
			if body["host"] != tc.host || body["answer"] != tc.ansr {
				t.Errorf("sent host %v answer %v, want %q %q", body["host"], body["answer"], tc.host, tc.ansr)
			}
			drifttest.AssertDryRunBodyMatches(t, tc.build, tc.run, tc.args, tc.resp)
		})
	}
}

// TestDNSImport_IDNNamesArePunycode is the import half of the test above.
func TestDNSImport_IDNNamesArePunycode(t *testing.T) {
	bodies, err := runImportCapturing(t, `[
	  {"host":"bücher","type":"CNAME","answer":"www.bücher.de","ttl":300},
	  {"host":"txt","type":"TXT","answer":"grüße","ttl":300}
	]`)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if len(bodies) != 2 {
		t.Fatalf("want 2 requests, got %d", len(bodies))
	}
	if bodies[0]["host"] != "xn--bcher-kva" || bodies[0]["answer"] != "www.xn--bcher-kva.de" {
		t.Errorf("CNAME sent as host %v answer %v, want punycode", bodies[0]["host"], bodies[0]["answer"])
	}
	if bodies[1]["answer"] != "grüße" {
		t.Errorf("TXT answer sent as %v, want it unchanged", bodies[1]["answer"])
	}
}

// TestDNSWrites_InvalidIDNIsUsageError: a host or target with no valid IDNA
// form is a usage error (exit 2), and nothing is sent.
func TestDNSWrites_InvalidIDNIsUsageError(t *testing.T) {
	tests := []struct{ name, rtype, host, answer string }{
		{"host", "A", "\u0300bücher", "1.2.3.4"},
		{"CNAME target", "CNAME", "www", "bücher-.de"},
		{"SRV target", "SRV", "_sip._tcp", "0 5060 bücher-.de"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cmd := cmdForCreate(t, neverCalledServer(t))
			if err := cmd.ParseFlags([]string{"--type", tc.rtype, "--host", tc.host, "--answer", tc.answer}); err != nil {
				t.Fatalf("ParseFlags: %v", err)
			}
			err := runCreate(cmd, []string{"example.com"})
			var ue *cmdutil.UsageError
			if !errors.As(err, &ue) {
				t.Fatalf("runCreate = %v (%T), want *cmdutil.UsageError", err, err)
			}
		})
	}

	_, err := runImportCapturing(t, `[{"host":"\u0300bücher","type":"A","answer":"1.2.3.4","ttl":300}]`)
	var ue *cmdutil.UsageError
	if !errors.As(err, &ue) {
		t.Fatalf("import of an invalid IDN host = %v (%T), want *cmdutil.UsageError", err, err)
	}
}

// TestDNSCreate_IDNPathIsPunycode pins issue #160 end to end for one command.
// A Unicode domain argument used to reach the URL path percent-encoded
// (/core/v1/domains/b%C3%BCcher.com/...), which name.com's edge answers with an
// HTML 403, and --dry-run previewed that same unconverted path. Both must use
// the ASCII form.
func TestDNSCreate_IDNPathIsPunycode(t *testing.T) {
	const want = "POST /core/v1/domains/xn--bcher-kva.com/records"
	const response = `{"id":1,"type":"A","host":"www","answer":"1.2.3.4","ttl":300}`
	setup := func(t *testing.T, srv *httptest.Server) *cobra.Command {
		cmd := cmdForCreate(t, srv)
		if err := cmd.ParseFlags([]string{"--type", "A", "--host", "www", "--answer", "1.2.3.4"}); err != nil {
			t.Fatalf("ParseFlags: %v", err)
		}
		return cmd
	}
	args := []string{"bücher.com"}

	printed := captureDryRunLine(t, func(srv *httptest.Server) (*cobra.Command, error) {
		cmd := withDryRun(t, setup(t, srv), true)
		return cmd, runCreate(cmd, append([]string(nil), args...))
	}, response)
	if printed != want {
		t.Errorf("--dry-run previews %q, want %q", printed, want)
	}

	sent := captureRealRequest(t, func(srv *httptest.Server) (*cobra.Command, error) {
		cmd := withDryRun(t, setup(t, srv), false)
		return cmd, runCreate(cmd, append([]string(nil), args...))
	}, response)
	if sent != want {
		t.Errorf("sent %q, want %q", sent, want)
	}
}

// TestDNSCreate_InvalidIDNIsUsageError: a name with no valid ASCII form is a
// usage error (exit 2) and nothing is sent.
func TestDNSCreate_InvalidIDNIsUsageError(t *testing.T) {
	cmd := cmdForCreate(t, neverCalledServer(t))
	createType, createHost, createAnswer, createTTL, createPriority = "A", "@", "1.2.3.4", 300, 0

	err := runCreate(cmd, []string{"bücher-.com"})
	var ue *cmdutil.UsageError
	if !errors.As(err, &ue) {
		t.Fatalf("runCreate(bücher-.com) = %v (%T), want *cmdutil.UsageError", err, err)
	}
}
