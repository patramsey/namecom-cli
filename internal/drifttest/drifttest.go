// Package drifttest provides the request-shape assertions used by the command
// packages' drift tests.
//
// Two different things are being guarded, and they are not the same check:
//
//   - **Wire shape.** What the command actually sends: method, path, and body.
//     This is what a client swap must not change. It is asserted against an
//     explicit expectation written by hand, so the test fails if the request
//     moves for any reason — including a reason that looks harmless.
//
//   - **Preview accuracy.** That --dry-run reports the same method and path it
//     would really send, and that it performs no *write*. Reads are allowed and
//     expected: `transfer create --dry-run` fetches pricing so it can show what
//     the transfer would cost, and `domain register --dry-run` checks
//     availability. The rule --dry-run promises is "print the request instead of
//     sending it" for writes, so that is what is asserted.
//
// Only the mutating commands are worth this. A GET that returns the wrong
// thing is visible; a POST whose body quietly changed is not.
package drifttest

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/patramsey/namecom-cli/cmd/cmdutil"
	"github.com/spf13/cobra"
)

// Request is a captured HTTP request, reduced to the parts that constitute the
// contract with the API.
type Request struct {
	Method string
	Path   string
	// Body is compared as canonicalised JSON, so key order and whitespace do
	// not matter. Empty means the request is expected to carry no body.
	Body string
}

var httpMethods = map[string]bool{
	"GET": true, "POST": true, "PUT": true, "PATCH": true, "DELETE": true,
}

// Build constructs the command under test, wired to srv.
type Build func(t *testing.T, srv *httptest.Server) *cobra.Command

// Run invokes the command's RunE equivalent.
type Run func(cmd *cobra.Command, args []string) error

// WithDryRun attaches a root command carrying --dry-run and --yes so
// cmdutil.IsDryRun / IsYes see them, mirroring how the real CLI wires
// persistent flags. --yes is always set: these commands confirm before
// mutating, and the live half must not block on a prompt.
func WithDryRun(t *testing.T, child *cobra.Command, dryRun bool) *cobra.Command {
	t.Helper()
	root := &cobra.Command{Use: "namecom"}
	var dr, yes bool
	root.PersistentFlags().BoolVar(&dr, "dry-run", false, "")
	root.PersistentFlags().BoolVar(&yes, "yes", false, "")
	if err := root.PersistentFlags().Set("yes", "true"); err != nil {
		t.Fatalf("setting yes flag: %v", err)
	}
	if dryRun {
		if err := root.PersistentFlags().Set("dry-run", "true"); err != nil {
			t.Fatalf("setting dry-run flag: %v", err)
		}
	}
	root.AddCommand(child)
	return child
}

// canonJSON normalises a JSON document so comparisons ignore key order and
// formatting. Non-JSON input is returned trimmed, so a mismatch still reports
// something readable rather than an error about the assertion itself.
func canonJSON(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		return s
	}
	b, err := json.Marshal(v)
	if err != nil {
		return s
	}
	return string(b)
}

// AssertRequest runs cmd for real against a stub server and asserts the request
// it sends matches want. When the command performs a read-modify-write, the
// mutating request is the last one, and that is the one compared.
func AssertRequest(t *testing.T, want Request, build Build, run Run, args []string, stubResponse string) {
	t.Helper()

	var gotMethod, gotPath, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotMethod, gotPath, gotBody = r.Method, r.URL.Path, string(b)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(stubResponse))
	}))
	t.Cleanup(srv.Close)

	cmd := WithDryRun(t, build(t, srv), false)
	if err := run(cmd, args); err != nil {
		t.Fatalf("live invocation failed: %v", err)
	}
	if gotMethod == "" {
		t.Fatal("no request was made")
	}

	if got, w := gotMethod+" "+gotPath, want.Method+" "+want.Path; got != w {
		t.Errorf("request line drifted:\n  sent: %s\n  want: %s", got, w)
	}
	if got, w := canonJSON(gotBody), canonJSON(want.Body); got != w {
		t.Errorf("request body drifted:\n  sent: %s\n  want: %s", got, w)
	}
}

// AssertDryRunMatches asserts that --dry-run reports the same METHOD and path
// the command really sends. Body is not compared — see the package comment.
func AssertDryRunMatches(t *testing.T, build Build, run Run, args []string, stubResponse string) {
	t.Helper()

	printed := dryRunLine(t, build, run, args, stubResponse)

	var last string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		last = r.Method + " " + r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(stubResponse))
	}))
	t.Cleanup(srv.Close)

	cmd := WithDryRun(t, build(t, srv), false)
	if err := run(cmd, args); err != nil {
		t.Fatalf("live invocation failed: %v", err)
	}
	if last == "" {
		t.Fatal("no request was made")
	}
	if printed != last {
		t.Errorf("--dry-run reports %q but the command actually sends %q", printed, last)
	}
}

// dryRunLine runs the command with --dry-run and extracts the METHOD /path line
// it printed. A dry run must not reach the network: the stub server fails the
// test if it is contacted.
func dryRunLine(t *testing.T, build Build, run Run, args []string, stubResponse string) string {
	t.Helper()

	var wrote string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Reads during a dry run are legitimate — see the package comment. Only
		// a write means the flag was ignored.
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			wrote = r.Method + " " + r.URL.Path
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(stubResponse))
	}))
	t.Cleanup(srv.Close)

	cmd := WithDryRun(t, build(t, srv), true)
	if err := run(cmd, args); err != nil {
		t.Fatalf("dry-run invocation failed: %v", err)
	}
	buf, ok := cmdutil.Out(cmd).Writer.(*bytes.Buffer)
	if !ok {
		t.Fatal("output writer is not a *bytes.Buffer")
	}
	out := buf.String()

	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) >= 2 && httpMethods[f[0]] && strings.HasPrefix(f[1], "/") {
			// Checked after a line is found so the more useful failure wins:
			// a command with no dry-run branch at all reports that, rather
			// than reporting that it made a request.
			if wrote != "" {
				t.Errorf("--dry-run performed a write: %s; it must only print", wrote)
			}
			return f[0] + " " + f[1]
		}
	}
	if wrote != "" {
		t.Fatalf("--dry-run printed no METHOD/path line and performed a write (%s): %q", wrote, out)
	}
	t.Fatalf("no dry-run METHOD/path line found in output: %q", out)
	return ""
}
