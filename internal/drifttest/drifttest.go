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
//   - **Preview accuracy.** That --dry-run reports the same method, path and
//     body it would really send, and that it performs no *write*. Reads are
//     allowed and expected: `transfer create --dry-run` fetches pricing so it
//     can show what the transfer would cost, and `domain register --dry-run`
//     checks availability and trademark claims. The rule --dry-run promises is
//     "print the request instead of sending it" for writes, so that is what is
//     asserted.
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
// persistent flags.
//
// Exactly one of the two is set. The live half gets --yes, because these
// commands confirm before mutating and it must not block on a prompt. The
// dry-run half does not: --dry-run must never prompt, and go test is not a
// TTY, so a command that asks anyway fails with "pass --yes to confirm in
// non-interactive mode" — exactly what a CI user sees. Setting --yes here too
// is what let `transfer create --dry-run` ask for confirmation unnoticed.
func WithDryRun(t *testing.T, child *cobra.Command, dryRun bool) *cobra.Command {
	t.Helper()
	root := &cobra.Command{Use: "namecom"}
	var dr, yes bool
	root.PersistentFlags().BoolVar(&dr, "dry-run", false, "")
	root.PersistentFlags().BoolVar(&yes, "yes", false, "")
	flag := "yes"
	if dryRun {
		flag = "dry-run"
	}
	if err := root.PersistentFlags().Set(flag, "true"); err != nil {
		t.Fatalf("setting %s flag: %v", flag, err)
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

	printed, _ := dryRunLine(t, build, run, args, stubResponse)

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

// isQueryPOST reports whether path is one of the API's lookups that are POSTs
// only so they can take a body. `domain register --dry-run` makes both — the
// availability check and the trademark-claims check — before it has a body to
// preview, and neither changes anything.
func isQueryPOST(path string) bool {
	return strings.HasSuffix(path, ":checkAvailability") ||
		strings.HasPrefix(path, "/core/v1/domaininfo/claims/")
}

// dryRunLine runs the command with --dry-run and extracts the METHOD /path line
// it printed, plus everything printed after it. A dry run must not write: the
// test fails if the stub server sees anything but a read.
func dryRunLine(t *testing.T, build Build, run Run, args []string, stubResponse string) (line, rest string) {
	t.Helper()

	var wrote string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Reads during a dry run are legitimate — see the package comment. Only
		// a write means the flag was ignored.
		if r.Method != http.MethodGet && r.Method != http.MethodHead && !isQueryPOST(r.URL.Path) {
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

	// JSON mode prints the preview as a document, not a request line.
	if doc, ok := structuredDryRun(out); ok {
		if wrote != "" {
			t.Errorf("--dry-run performed a write: %s; it must only print", wrote)
		}
		return doc.Method + " " + doc.Path, string(doc.Body)
	}

	lines := strings.Split(out, "\n")
	for i, line := range lines {
		f := strings.Fields(line)
		if len(f) >= 2 && httpMethods[f[0]] && strings.HasPrefix(f[1], "/") {
			// Checked after a line is found so the more useful failure wins:
			// a command with no dry-run branch at all reports that, rather
			// than reporting that it made a request.
			if wrote != "" {
				t.Errorf("--dry-run performed a write: %s; it must only print", wrote)
			}
			return f[0] + " " + f[1], strings.Join(lines[i+1:], "\n")
		}
	}
	if wrote != "" {
		t.Fatalf("--dry-run printed no METHOD/path line and performed a write (%s): %q", wrote, out)
	}
	t.Fatalf("no dry-run METHOD/path line found in output: %q", out)
	return "", ""
}

// structuredDryRun decodes the {"dry_run": true, …} document out.DryRun prints
// in JSON mode, reporting false when out does not start with one.
func structuredDryRun(out string) (doc struct {
	DryRun bool            `json:"dry_run"`
	Method string          `json:"method"`
	Path   string          `json:"path"`
	Body   json.RawMessage `json:"body"`
}, ok bool) {
	if err := json.NewDecoder(strings.NewReader(out)).Decode(&doc); err != nil {
		return doc, false
	}
	return doc, doc.DryRun
}

// AssertDryRunBodyMatches asserts that the body --dry-run prints is the body
// the command really sends, compared as canonical JSON.
//
// This is the stricter sibling of AssertDryRunMatches, for commands whose
// preview carries no secret and so has no reason to differ from the wire. A
// preview built by hand from the command's inputs, rather than from the value
// handed to the SDK, drifts silently whenever the SDK wraps or reshapes it.
//
// A command that previews no body may send `{}`: that is the SDK's
// EmptyObject placeholder for a bodyless endpoint (namedotcom/core-api-go#8),
// not a body the user chose, and the shape tests pin it separately.
func AssertDryRunBodyMatches(t *testing.T, build Build, run Run, args []string, stubResponse string) {
	t.Helper()
	assertDryRunBody(t, build, run, args, stubResponse, nil)
}

// AssertDryRunBodyMatchesRedacted is AssertDryRunBodyMatches for a command
// that redacts secrets from its preview. redacted maps each redacted top-level
// field to the placeholder the preview shows in its place. The sent body must
// carry every such field; with the placeholders substituted in, it must then
// equal the preview exactly, so redaction cannot hide any other difference.
func AssertDryRunBodyMatchesRedacted(t *testing.T, build Build, run Run, args []string, stubResponse string, redacted map[string]string) {
	t.Helper()
	assertDryRunBody(t, build, run, args, stubResponse, redacted)
}

func assertDryRunBody(t *testing.T, build Build, run Run, args []string, stubResponse string, redacted map[string]string) {
	t.Helper()

	printed := dryRunBody(t, build, run, args, stubResponse)

	var sent string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		sent = string(b)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(stubResponse))
	}))
	t.Cleanup(srv.Close)

	cmd := WithDryRun(t, build(t, srv), false)
	if err := run(cmd, args); err != nil {
		t.Fatalf("live invocation failed: %v", err)
	}
	if len(redacted) > 0 {
		sent = redact(t, sent, redacted)
	}
	got, want := canonJSON(printed), canonJSON(sent)
	if got == "" && want == "{}" {
		return
	}
	if got != want {
		t.Errorf("--dry-run previews a body the command does not send:\n  printed: %s\n  sent:    %s", got, want)
	}
}

// redact substitutes placeholders for the named top-level fields of body.
func redact(t *testing.T, body string, fields map[string]string) string {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		t.Fatalf("sent body is not a JSON object, so it cannot be redacted: %q", body)
	}
	for k, placeholder := range fields {
		if _, ok := m[k]; !ok {
			t.Errorf("sent body has no %q field to redact: %s", k, body)
		}
		m[k] = placeholder
	}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("re-marshalling redacted body: %v", err)
	}
	return string(b)
}

// dryRunBody runs the command with --dry-run and returns the JSON document
// printed after the METHOD /path line, or "" if none was printed.
func dryRunBody(t *testing.T, build Build, run Run, args []string, stubResponse string) string {
	t.Helper()

	_, rest := dryRunLine(t, build, run, args, stubResponse)
	rest = strings.TrimSpace(rest)
	if rest == "" {
		return ""
	}
	// The body is the first JSON value; anything printed after it is not.
	var v json.RawMessage
	if err := json.NewDecoder(strings.NewReader(rest)).Decode(&v); err != nil {
		t.Fatalf("--dry-run printed something after the request line that is not a JSON body: %q", rest)
	}
	return string(v)
}
