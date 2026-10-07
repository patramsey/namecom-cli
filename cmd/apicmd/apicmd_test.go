package apicmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/patramsey/namecom-cli/cmd/cmdutil"
	"github.com/patramsey/namecom-cli/internal/api"
	"github.com/patramsey/namecom-cli/internal/config"
	"github.com/patramsey/namecom-cli/internal/output"
	"github.com/spf13/cobra"
)

// apiCmd wires a command against srv with real credentials configured.
func apiCmd(t *testing.T, srv *httptest.Server) (*cobra.Command, *bytes.Buffer) {
	t.Helper()
	client, err := api.New(api.Options{
		BaseURL: srv.URL,
		Creds:   config.Credentials{Username: "alice", Token: "s3cret"},
	})
	if err != nil {
		t.Fatalf("api.New: %v", err)
	}
	var buf bytes.Buffer
	out := &output.Config{Format: output.FormatJSON, Color: output.ColorNever, Writer: &buf, EWriter: &bytes.Buffer{}}
	cmd := &cobra.Command{}
	ctx := context.WithValue(context.Background(), cmdutil.KeyOutput, out)
	ctx = context.WithValue(ctx, cmdutil.KeyClient, client)
	cmd.SetContext(ctx)
	// A write confirms, and off a terminal that needs --yes (#282). The
	// tests send as a script does; withRoot(cmd, false, false) takes it away.
	withRoot(cmd, false, true)
	t.Cleanup(func() {
		apiBody, apiHeaders, apiInput, apiMethod = "", nil, "", ""
		apiFields, apiTyped = nil, nil
		apiInclude, apiPaginate = false, false
		apiMaxPages = defaultMaxPages
	})
	return cmd, &buf
}

// TestAPI_SendsAuthorization guards a total-breakage bug: runAPI built a request
// by hand and sent it via client.HTTPClient(). Auth is injected by a request
// editor registered on the *generated* client, not by the http.Client's
// transport, so every `namecom api` call went out unauthenticated and 401'd.
// The command's own Long text promises "Auth, rate limiting, and retries are
// applied automatically".
//
// The 401 was doubly misleading: it maps to exit code 3, which tells the user to
// run `namecom auth login` — sending them to re-enter credentials that were
// already correct.
func TestAPI_SendsAuthorization(t *testing.T) {
	var gotAuth, gotUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotUA = r.Header.Get("User-Agent")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(srv.Close)

	cmd, _ := apiCmd(t, srv)
	if err := runAPI(cmd, []string{"GET", "/core/v1/domains"}); err != nil {
		t.Fatalf("runAPI: %v", err)
	}

	if gotAuth == "" {
		t.Error("namecom api sent no Authorization header — every call would 401")
	}
	// "alice:s3cret" base64-encoded.
	if want := "Basic YWxpY2U6czNjcmV0"; gotAuth != want {
		t.Errorf("expected %q, got %q", want, gotAuth)
	}
	if gotUA == "" {
		t.Error("namecom api sent no User-Agent header")
	}
}

// TestAPI_UserHeaderOverridesDefault pins that --header still wins, so the
// escape hatch stays usable for testing alternate auth.
func TestAPI_UserHeaderOverridesDefault(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)

	cmd, _ := apiCmd(t, srv)
	apiHeaders = []string{"Authorization: Bearer OVERRIDE"}
	if err := runAPI(cmd, []string{"GET", "/core/v1/domains"}); err != nil {
		t.Fatalf("runAPI: %v", err)
	}
	if gotAuth != "Bearer OVERRIDE" {
		t.Errorf("--header should override the default auth, got %q", gotAuth)
	}
}

// TestAPI_ReturnsAPIErrorForExitCode pins that a non-2xx from `namecom api`
// produces an *api.APIError. runAPI returned a plain fmt.Errorf, so root.go's
// exitCode mapping (which type-asserts *api.APIError) fell through to the
// generic 1 — losing the documented 3/5 codes that scripts branch on, and
// suppressing APIError.UserHint().
func TestAPI_ReturnsAPIErrorForExitCode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"message":"Permission Denied","details":"bad token"}`))
	}))
	t.Cleanup(srv.Close)

	cmd, _ := apiCmd(t, srv)
	err := runAPI(cmd, []string{"GET", "/core/v1/domains"})
	if err == nil {
		t.Fatal("expected an error for HTTP 401, got nil")
	}
	var apiErr *api.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *api.APIError so exit codes map correctly, got %T: %v", err, err)
	}
	if apiErr.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected StatusCode 401, got %d", apiErr.StatusCode)
	}
	if apiErr.Message != "Permission Denied" {
		t.Errorf("API's own message should be preserved, got %q", apiErr.Message)
	}
}

// TestBuildAPIURL_CannotRetargetAnotherHost is a credential-exfiltration guard.
//
// `namecom api` takes an arbitrary path from argv and the resulting request
// carries the account's Authorization header. If a crafted path could move the
// request to another host, the credential goes with it.
//
// This asserts on the URL that gets BUILT, deliberately. An earlier version
// stood up an httptest server and checked what its handler observed — which is
// no check at all: if a hostile path really does retarget the request, the local
// handler never runs and every assertion passes vacuously. It was verified to
// pass against a knowingly vulnerable implementation (url.ResolveReference)
// while real requests carrying the credential went to an external host. Even
// asserting "the call returned an error" is too weak, because a DNS failure
// produces an error only AFTER the connection was attempted.
func TestBuildAPIURL_CannotRetargetAnotherHost(t *testing.T) {
	const base = "https://api.name.com"

	hostile := []string{
		"//evil.example/steal",
		"https://evil.example/steal",
		"http://evil.example/steal",
		"../../../../evil.example/steal",
		"/core/v1/../../../evil.example",
		`\\evil.example\steal`,
		"https://user:pass@evil.example/steal",
		"//evil.example",
		// Query strings are passed through, so they must not open a way round
		// the guard: a host hidden before or inside the query stays a path.
		"//evil.example?x=1",
		"https://evil.example/steal?perPage=1",
		"?@evil.example",
		"/core/v1/domains?next=//evil.example",
	}
	for _, p := range hostile {
		t.Run(p, func(t *testing.T) {
			got, err := buildAPIURL(base, p)
			if err != nil {
				return // refusing outright is a fine outcome
			}
			u, perr := url.Parse(got)
			if perr != nil {
				t.Fatalf("built an unparseable URL %q: %v", got, perr)
			}
			if u.Host != "api.name.com" {
				t.Errorf("path %q built %q — host %q, credential would be sent off-site",
					p, got, u.Host)
			}
			if u.Scheme != "https" {
				t.Errorf("path %q downgraded the scheme to %q", p, u.Scheme)
			}
		})
	}
}

// TestBuildAPIURL_KeepsLegitimatePaths is the counterweight: the guard must not
// break ordinary use.
func TestBuildAPIURL_KeepsLegitimatePaths(t *testing.T) {
	const base = "https://api.name.com"
	cases := map[string]string{
		"/core/v1/domains":             "https://api.name.com/core/v1/domains",
		"core/v1/domains":              "https://api.name.com/core/v1/domains",
		"/core/v1/domains/example.com": "https://api.name.com/core/v1/domains/example.com",
		// Query strings reach the server intact. url.JoinPath escapes "?" as
		// part of the path, so these became /core/v1/orders%3FperPage=2 and
		// the API answered 403 — every filter, sort, and page parameter was
		// unreachable through the command meant as the escape hatch.
		"/core/v1/orders?perPage=2":          "https://api.name.com/core/v1/orders?perPage=2",
		"/core/v1/orders?perPage=2&dir=desc": "https://api.name.com/core/v1/orders?perPage=2&dir=desc",
		"/core/v1/domains?domainName=*.io":   "https://api.name.com/core/v1/domains?domainName=*.io",
	}
	for in, want := range cases {
		got, err := buildAPIURL(base, in)
		if err != nil {
			t.Errorf("buildAPIURL(%q) errored: %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("buildAPIURL(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestAPI_HeaderInjectionRejected pins that --header cannot smuggle extra
// headers or a request line via embedded CRLF.
func TestAPI_HeaderInjectionRejected(t *testing.T) {
	var got http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)

	cmd, _ := apiCmd(t, srv)
	apiHeaders = []string{"X-Probe: ok\r\nX-Injected: yes"}
	// Either a rejection or a sanitized send is fine; a smuggled header is not.
	_ = runAPI(cmd, []string{"GET", "/core/v1/domains"})

	if got.Get("X-Injected") != "" {
		t.Errorf("CRLF in --header smuggled an additional header: %v", got)
	}
}

// TestAPI_CredentialNotForwardedOnCrossHostRedirect guards the other direction:
// the path is safe, but a response can still try to move the request. Go's
// http.Client strips sensitive headers when a redirect crosses hosts — this
// pins that behavior so a future custom CheckRedirect cannot silently undo it.
func TestAPI_CredentialNotForwardedOnCrossHostRedirect(t *testing.T) {
	var attackerSawAuth string
	attacker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attackerSawAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(attacker.Close)

	// httptest binds 127.0.0.1; Go compares redirect hosts by NAME (ports are
	// ignored), so two httptest servers look like the same domain and net/http
	// copies the header; headerTransport removes it then, which
	// TestCredentialNotForwardedToOtherPort in internal/api covers. This test
	// is about net/http's own stripping, so point the redirect at "localhost" — same
	// machine, genuinely different hostname — to exercise the cross-domain path.
	attackerHost := strings.Replace(attacker.URL, "127.0.0.1", "localhost", 1)
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, attackerHost+"/steal", http.StatusFound)
	}))
	t.Cleanup(origin.Close)

	cmd, _ := apiCmd(t, origin)
	_ = runAPI(cmd, []string{"GET", "/core/v1/domains"})

	if attackerSawAuth != "" {
		t.Errorf("Authorization header followed a cross-host redirect: %q", attackerSawAuth)
	}
}

// TestAPI_PropagatesMethodPathAndBody covers what `namecom api` is FOR: passing
// the caller's method, path, and body through untouched. Nothing asserted any
// of it — hardcoding runAPI to a bodyless GET left every apicmd test green,
// because they only checked headers and error types.
//
// This is the raw escape hatch. If it quietly rewrites the request, a user
// debugging an API problem is debugging the wrong request.
func TestAPI_PropagatesMethodPathAndBody(t *testing.T) {
	tests := []struct {
		name, method, path, data string
	}{
		{"GET with no body", "GET", "/core/v1/domains", ""},
		{"POST with a JSON body", "POST", "/core/v1/domains/example.com/records", `{"host":"@","type":"A"}`},
		{"PATCH with a body", "PATCH", "/core/v1/domains/example.com", `{"locked":true}`},
		{"DELETE with no body", "DELETE", "/core/v1/domains/example.com/records/7", ""},
		{"lowercase method is upcased", "post", "/core/v1/domains", `{}`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var gotMethod, gotPath, gotBody string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotMethod, gotPath = r.Method, r.URL.Path
				b, _ := io.ReadAll(r.Body)
				gotBody = string(b)
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{}`))
			}))
			t.Cleanup(srv.Close)

			cmd, _ := apiCmd(t, srv)
			apiBody = tc.data
			if err := runAPI(cmd, []string{tc.method, tc.path}); err != nil {
				t.Fatalf("runAPI: %v", err)
			}

			if want := strings.ToUpper(tc.method); gotMethod != want {
				t.Errorf("method: got %q, want %q", gotMethod, want)
			}
			if gotPath != tc.path {
				t.Errorf("path: got %q, want %q", gotPath, tc.path)
			}
			if gotBody != tc.data {
				t.Errorf("body: got %q, want %q", gotBody, tc.data)
			}
		})
	}
}

// TestAPI_ReadsBodyFromStdin covers `--data -`, which the command's own help
// advertises. Nothing exercised it.
func TestAPI_ReadsBodyFromStdin(t *testing.T) {
	const payload = `{"host":"www","type":"CNAME"}`

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	go func() {
		defer func() { _ = w.Close() }()
		_, _ = w.WriteString(payload)
	}()
	orig := os.Stdin
	os.Stdin = r
	t.Cleanup(func() { os.Stdin = orig; _ = r.Close() })

	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		b, _ := io.ReadAll(req.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)

	cmd, _ := apiCmd(t, srv)
	apiBody = "-"
	if err := runAPI(cmd, []string{"POST", "/core/v1/domains/example.com/records"}); err != nil {
		t.Fatalf("runAPI: %v", err)
	}
	if gotBody != payload {
		t.Errorf("--data - should stream stdin as the body: got %q, want %q", gotBody, payload)
	}
}

// dryRun hangs cmd under a root carrying --dry-run set to on, which is where
// cmdutil.IsDryRun looks for it, and --yes.
func dryRun(cmd *cobra.Command, on bool) { withRoot(cmd, on, true) }

// withRoot hangs cmd under a root carrying --dry-run and --yes as given.
func withRoot(cmd *cobra.Command, dryRun, yes bool) {
	root := &cobra.Command{Use: "namecom"}
	var dr, y bool
	root.PersistentFlags().BoolVar(&dr, "dry-run", dryRun, "")
	root.PersistentFlags().BoolVar(&y, "yes", yes, "")
	cmd.Use = "api"
	root.AddCommand(cmd)
}

// stdinWith replaces os.Stdin with a pipe carrying payload for the test.
func stdinWith(t *testing.T, payload string) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	go func() {
		defer func() { _ = w.Close() }()
		_, _ = w.WriteString(payload)
	}()
	orig := os.Stdin
	os.Stdin = r
	t.Cleanup(func() { os.Stdin = orig; _ = r.Close() })
}

// refuseAll is a server that fails the test if any request reaches it.
func refuseAll(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("--dry-run sent %s %s to the server", r.Method, r.URL.RequestURI())
		w.WriteHeader(http.StatusTeapot)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestAPI_DryRunSendsNothing pins issue #109: `namecom api` never checked
// --dry-run, so `api POST /core/v1/domains --data … --dry-run` would have
// registered the domain. A raw passthrough cannot tell which requests change
// state, so every method but GET and HEAD is previewed instead of sent.
func TestAPI_DryRunSendsNothing(t *testing.T) {
	tests := []struct {
		method, path, data, wantLine, wantBody string
	}{
		{"POST", "/core/v1/domains", `{"domain":{"domainName":"example.com"}}`,
			"POST /core/v1/domains", `{"domain":{"domainName":"example.com"}}`},
		{"PUT", "/core/v1/domains/example.com/records/7?x=1", `{"host":"www"}`,
			"PUT /core/v1/domains/example.com/records/7?x=1", `{"host":"www"}`},
		{"patch", "/core/v1/domains/example.com", `{"locked":true}`,
			"PATCH /core/v1/domains/example.com", `{"locked":true}`},
		{"DELETE", "/core/v1/domains/example.com/records/7", "",
			"DELETE /core/v1/domains/example.com/records/7", ""},
	}
	for _, tc := range tests {
		t.Run(tc.method, func(t *testing.T) {
			cmd, buf := apiCmd(t, refuseAll(t))
			dryRun(cmd, true)
			apiBody = tc.data
			if err := runAPI(cmd, []string{tc.method, tc.path}); err != nil {
				t.Fatalf("runAPI: %v", err)
			}
			doc := parseDryRun(t, buf)
			if got := doc.Method + " " + doc.Path; got != tc.wantLine {
				t.Errorf("preview names %q, want %q", got, tc.wantLine)
			}
			if got := string(doc.Body); got != tc.wantBody {
				t.Errorf("preview body = %s, want %s", got, tc.wantBody)
			}
		})
	}
}

// dryRunDoc is the document --dry-run prints in JSON mode, with the body kept
// as raw JSON so tests compare it exactly.
type dryRunDoc struct {
	DryRun bool            `json:"dryRun"`
	Method string          `json:"method"`
	Path   string          `json:"path"`
	Body   json.RawMessage `json:"body"`
}

// parseDryRun decodes the --dry-run document, compacting its body.
func parseDryRun(t *testing.T, buf *bytes.Buffer) dryRunDoc {
	t.Helper()
	var doc dryRunDoc
	if err := json.Unmarshal(buf.Bytes(), &doc); err != nil {
		t.Fatalf("--dry-run output is not a JSON document: %v\n%s", err, buf.String())
	}
	if !doc.DryRun {
		t.Errorf(`--dry-run document lacks "dryRun": true: %s`, buf.String())
	}
	if len(doc.Body) > 0 {
		var c bytes.Buffer
		if err := json.Compact(&c, doc.Body); err != nil {
			t.Fatalf("compacting body: %v", err)
		}
		doc.Body = c.Bytes()
	}
	return doc
}

// TestAPI_DryRunTableKeepsRequestLine: the human preview is unchanged in
// table mode — the request line, then the indented body.
func TestAPI_DryRunTableKeepsRequestLine(t *testing.T) {
	cmd, buf := apiCmd(t, refuseAll(t))
	cmdutil.Out(cmd).Format = output.FormatTable
	dryRun(cmd, true)
	apiBody = `{"locked":true}`
	if err := runAPI(cmd, []string{"PATCH", "/core/v1/domains/example.com"}); err != nil {
		t.Fatalf("runAPI: %v", err)
	}
	if got, want := buf.String(), "PATCH /core/v1/domains/example.com\n  {\n    \"locked\": true\n  }\n"; got != want {
		t.Errorf("table preview = %q, want %q", got, want)
	}
}

// TestAPI_DryRunStillRunsReads pins the GET/HEAD exemption: the flag's help
// says reads are unaffected, and a read under --dry-run is how a user looks at
// what a previewed write would act on.
func TestAPI_DryRunStillRunsReads(t *testing.T) {
	for _, method := range []string{"GET", "HEAD"} {
		t.Run(method, func(t *testing.T) {
			var hits int
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				hits++
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{}`))
			}))
			t.Cleanup(srv.Close)

			cmd, _ := apiCmd(t, srv)
			dryRun(cmd, true)
			if err := runAPI(cmd, []string{method, "/core/v1/domains"}); err != nil {
				t.Fatalf("runAPI: %v", err)
			}
			if hits != 1 {
				t.Errorf("%s under --dry-run reached the server %d time(s), want 1", method, hits)
			}
		})
	}
}

// TestAPI_DryRunPreviewsStdinBody covers `--data -` under --dry-run. Stdin can
// be read only once, so the preview must come from the same read the request
// would have used.
func TestAPI_DryRunPreviewsStdinBody(t *testing.T) {
	stdinWith(t, `{"host":"www","type":"CNAME"}`)
	cmd, buf := apiCmd(t, refuseAll(t))
	dryRun(cmd, true)
	apiBody = "-"
	if err := runAPI(cmd, []string{"POST", "/core/v1/domains/example.com/records"}); err != nil {
		t.Fatalf("runAPI: %v", err)
	}
	if got := string(parseDryRun(t, buf).Body); got != `{"host":"www","type":"CNAME"}` {
		t.Errorf("preview does not show the stdin body, got %s", got)
	}
}

// TestAPI_DryRunPreviewsNonJSONBodyRaw: --data is sent verbatim whether or not
// it is JSON, so a body that is not JSON is previewed as the string it is
// rather than dropped.
func TestAPI_DryRunPreviewsNonJSONBodyRaw(t *testing.T) {
	cmd, buf := apiCmd(t, refuseAll(t))
	dryRun(cmd, true)
	apiBody = "host=www"
	if err := runAPI(cmd, []string{"POST", "/core/v1/domains/example.com/records"}); err != nil {
		t.Fatalf("runAPI: %v", err)
	}
	if got := string(parseDryRun(t, buf).Body); got != `"host=www"` {
		t.Errorf("preview body = %s, want the raw body as a JSON string", got)
	}
}

// TestDryRunMatchesRealRequest_API runs one request with --dry-run and then
// without, and asserts the preview names the method, path, query and body that
// are really sent.
func TestDryRunMatchesRealRequest_API(t *testing.T) {
	const path = "/core/v1/domains/example.com/records?perPage=2"
	const data = `{"host":"@","type":"A","answer":"10.0.0.1","ttl":300}`

	cmd, buf := apiCmd(t, refuseAll(t))
	dryRun(cmd, true)
	apiBody = data
	if err := runAPI(cmd, []string{"POST", path}); err != nil {
		t.Fatalf("runAPI (dry run): %v", err)
	}
	doc := parseDryRun(t, buf)
	printedLine, printedBody := doc.Method+" "+doc.Path, string(doc.Body)

	var sentLine, sentBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sentLine = r.Method + " " + r.URL.RequestURI()
		b, _ := io.ReadAll(r.Body)
		sentBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)
	cmd, _ = apiCmd(t, srv)
	dryRun(cmd, false)
	apiBody = data
	if err := runAPI(cmd, []string{"POST", path}); err != nil {
		t.Fatalf("runAPI: %v", err)
	}

	if printedLine != sentLine {
		t.Errorf("--dry-run reports %q but the command sends %q", printedLine, sentLine)
	}
	var printed, sent any
	if err := json.Unmarshal([]byte(printedBody), &printed); err != nil {
		t.Fatalf("previewed body is not JSON: %v\n%s", err, printedBody)
	}
	if err := json.Unmarshal([]byte(sentBody), &sent); err != nil {
		t.Fatalf("sent body is not JSON: %v\n%s", err, sentBody)
	}
	if !reflect.DeepEqual(printed, sent) {
		t.Errorf("--dry-run previews %v but the command sends %v", printed, sent)
	}
}

// notFound serves every request a 404 with body.
func notFound(t *testing.T, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestAPI_StructuredErrorIsOneDocument guards issue #130. On a non-2xx,
// runAPI wrote "HTTP 404" and the raw body to stderr, and Execute then wrote
// the JSON error envelope after them — three things on stderr, so
// `namecom api … -o json 2> err.json; jq . err.json` failed. In JSON and YAML
// modes the envelope is the only thing on stderr, with the response body
// inside it.
//
// Execute exits the process, so this reproduces its error path directly:
// runAPI, then out.Error on the same stderr writer.
func TestAPI_StructuredErrorIsOneDocument(t *testing.T) {
	tests := []struct {
		name, body string
		want       any
	}{
		{"json body", `{"message":"Not Found","details":"no such domain"}`,
			map[string]any{"message": "Not Found", "details": "no such domain"}},
		{"non-json body", "<html>404</html>", "<html>404</html>"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cmd, _ := apiCmd(t, notFound(t, tc.body))
			out := cmdutil.Out(cmd)
			var stderr bytes.Buffer
			out.EWriter = &stderr

			err := runAPI(cmd, []string{"GET", "/core/v1/domains/does-not-exist.com"})
			if err == nil {
				t.Fatal("expected an error for HTTP 404")
			}
			// Exit-code classification must not change: still an
			// *api.APIError carrying the 404, so the command exits 4.
			var apiErr *api.APIError
			if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusNotFound {
				t.Fatalf("want *api.APIError with status 404, got %T: %v", err, err)
			}
			out.Error(err)

			dec := json.NewDecoder(bytes.NewReader(stderr.Bytes()))
			var env struct {
				Error struct {
					Message string `json:"message"`
					Details any    `json:"details"`
				} `json:"error"`
			}
			if err := dec.Decode(&env); err != nil {
				t.Fatalf("stderr is not a JSON document: %v\n%s", err, stderr.String())
			}
			if dec.More() {
				t.Fatalf("stderr holds more than one JSON document:\n%s", stderr.String())
			}
			if env.Error.Message == "" {
				t.Errorf("envelope lost its message:\n%s", stderr.String())
			}
			if !reflect.DeepEqual(env.Error.Details, tc.want) {
				t.Errorf("error.details = %#v, want the response body %#v", env.Error.Details, tc.want)
			}
		})
	}
}

// TestAPI_TableErrorKeepsRawBody pins the other side of #130: in table mode
// the human-readable "HTTP <status>" line and raw body stay on stderr.
func TestAPI_TableErrorKeepsRawBody(t *testing.T) {
	const body = `{"message":"Not Found"}`
	cmd, _ := apiCmd(t, notFound(t, body))
	out := cmdutil.Out(cmd)
	out.Format = output.FormatTable
	var stderr bytes.Buffer
	out.EWriter = &stderr

	if err := runAPI(cmd, []string{"GET", "/core/v1/domains/does-not-exist.com"}); err == nil {
		t.Fatal("expected an error for HTTP 404")
	}
	if want := "HTTP 404\n" + body + "\n"; stderr.String() != want {
		t.Errorf("table-mode stderr = %q, want %q", stderr.String(), want)
	}
}

// TestMain points os.Stdin at the null device for the whole package. runAPI
// reads a piped stdin as the body (#231), so a test that inherited the
// runner's stdin — a pipe in many CI systems — would send whatever was on it,
// or block. Tests that want stdin swap it in with stdinWith.
func TestMain(m *testing.M) {
	null, err := os.Open(os.DevNull)
	if err != nil {
		panic(err)
	}
	os.Stdin = null
	os.Exit(m.Run())
}

// bodyServer records the body and Content-Type of the request it serves.
func bodyServer(t *testing.T) (srv *httptest.Server, body, contentType *string) {
	t.Helper()
	var b, ct string
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		b, ct = string(raw), r.Header.Get("Content-Type")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)
	return srv, &b, &ct
}

// TestAPI_PipedStdinIsTheBody pins #231: the help's own example pipes a
// record into `namecom api POST` without --data, and stdin was ignored — the
// preview showed no body and the request went out empty.
func TestAPI_PipedStdinIsTheBody(t *testing.T) {
	const payload = `{"host":"www","type":"CNAME"}`

	t.Run("sent", func(t *testing.T) {
		stdinWith(t, payload)
		srv, body, ct := bodyServer(t)
		cmd, _ := apiCmd(t, srv)
		if err := runAPI(cmd, []string{"POST", "/core/v1/domains/example.com/records"}); err != nil {
			t.Fatalf("runAPI: %v", err)
		}
		if *body != payload {
			t.Errorf("piped stdin should be the body: got %q, want %q", *body, payload)
		}
		if *ct != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", *ct)
		}
	})

	t.Run("previewed", func(t *testing.T) {
		stdinWith(t, payload)
		cmd, buf := apiCmd(t, refuseAll(t))
		dryRun(cmd, true)
		if err := runAPI(cmd, []string{"POST", "/core/v1/domains/example.com/records"}); err != nil {
			t.Fatalf("runAPI: %v", err)
		}
		if got := string(parseDryRun(t, buf).Body); got != payload {
			t.Errorf("--dry-run preview body = %s, want %s", got, payload)
		}
	})
}

// TestAPI_EmptyStdinSendsNoBody: a CI job's stdin is often empty or the null
// device. Neither is a body, so the request carries none — not an empty one
// labelled application/json — and `--data -` on an empty pipe agrees.
func TestAPI_EmptyStdinSendsNoBody(t *testing.T) {
	for _, tc := range []struct {
		name, data string
		pipe       bool
	}{
		{"null device", "", false},
		{"null device with --data -", "-", false},
		{"empty pipe", "", true},
		{"empty pipe with --data -", "-", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.pipe {
				stdinWith(t, "")
			}
			srv, body, ct := bodyServer(t)
			cmd, _ := apiCmd(t, srv)
			apiBody = tc.data
			if err := runAPI(cmd, []string{"DELETE", "/core/v1/domains/example.com/records/7"}); err != nil {
				t.Fatalf("runAPI: %v", err)
			}
			if *body != "" || *ct != "" {
				t.Errorf("sent body %q with Content-Type %q, want neither", *body, *ct)
			}
		})
	}
}

// TestAPI_StdinIgnoredWhenNotWanted: reads take no body, and an explicit
// --data, even an empty one, is the body instead of stdin. An explicitly empty
// --data is the way out for a caller whose stdin is a pipe that never closes.
func TestAPI_StdinIgnoredWhenNotWanted(t *testing.T) {
	t.Run("GET", func(t *testing.T) {
		stdinWith(t, `{"x":1}`)
		srv, body, _ := bodyServer(t)
		cmd, _ := apiCmd(t, srv)
		if err := runAPI(cmd, []string{"GET", "/core/v1/domains"}); err != nil {
			t.Fatalf("runAPI: %v", err)
		}
		if *body != "" {
			t.Errorf("GET sent stdin as a body: %q", *body)
		}
	})
	t.Run("explicit empty --data", func(t *testing.T) {
		stdinWith(t, `{"x":1}`)
		srv, body, _ := bodyServer(t)
		cmd, _ := apiCmd(t, srv)
		cmd.Flags().StringVar(&apiBody, "data", "", "")
		if err := cmd.Flags().Set("data", ""); err != nil {
			t.Fatal(err)
		}
		if err := runAPI(cmd, []string{"POST", "/core/v1/domains/example.com/records"}); err != nil {
			t.Fatalf("runAPI: %v", err)
		}
		if *body != "" {
			t.Errorf("--data '' still read stdin: %q", *body)
		}
	})
}
