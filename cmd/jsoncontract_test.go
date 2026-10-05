package cmd

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/patramsey/namecom-cli/internal/output"
	"github.com/spf13/cobra"
)

// reply is one canned stub response.
type reply struct {
	status int
	body   string
}

// contractServer answers "METHOD /path" from routes, and anything else with
// 200 {}.
func contractServer(t *testing.T, routes map[string]reply) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		rep, ok := routes[r.Method+" "+r.URL.Path]
		if !ok {
			rep = reply{200, `{}`}
		}
		if rep.status != 0 {
			w.WriteHeader(rep.status)
		}
		_, _ = w.Write([]byte(rep.body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// runContract runs `namecom <args>` in-process through run(), the same path
// Execute takes, and returns stdout, stderr and the exit code. Nothing is a
// terminal, so confirmations cannot be answered.
func runContract(t *testing.T, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	t.Cleanup(output.StubInteractive(false))

	dir := t.TempDir()
	outF, err := os.Create(filepath.Join(dir, "stdout")) //nolint:gosec // a path under t.TempDir()
	if err != nil {
		t.Fatal(err)
	}
	errF, err := os.Create(filepath.Join(dir, "stderr")) //nolint:gosec // a path under t.TempDir()
	if err != nil {
		t.Fatal(err)
	}
	prevOut, prevErr := os.Stdout, os.Stderr
	prevGF, prevResolved := gf, resolvedOut
	os.Stdout, os.Stderr = outF, errF
	resolvedOut = nil
	t.Cleanup(func() {
		os.Stdout, os.Stderr = prevOut, prevErr
		gf, resolvedOut = prevGF, prevResolved
		rootCmd.SetArgs(nil)
		_ = outF.Close()
		_ = errF.Close()
	})

	rootCmd.SetArgs(args)
	code = run()
	os.Stdout, os.Stderr = prevOut, prevErr

	o, _ := os.ReadFile(outF.Name())
	e, _ := os.ReadFile(errF.Name())
	return string(o), string(e), code
}

// mustFind returns the command args name.
func mustFind(t *testing.T, args []string) *cobra.Command {
	t.Helper()
	c, _, err := rootCmd.Find(args)
	if err != nil {
		t.Fatalf("finding %v: %v", args, err)
	}
	return c
}

// camelKey is the JSON contract's key style (#240).
var camelKey = regexp.MustCompile(`^[a-z][a-zA-Z0-9]*$`)

// decodeDoc parses s as exactly one JSON document, failing the test
// otherwise, and checks that every key in it is camelCase.
func decodeDoc(t *testing.T, what, s string) map[string]any {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(s))
	var doc map[string]any
	if err := dec.Decode(&doc); err != nil {
		t.Fatalf("%s is not one JSON object: %v\n%s", what, err, s)
	}
	if dec.More() {
		t.Fatalf("%s holds more than one JSON document:\n%s", what, s)
	}
	var walk func(path string, v any)
	walk = func(path string, v any) {
		switch v := v.(type) {
		case map[string]any:
			for k, child := range v {
				// The record body of a dry-run is the API's, and the API's
				// keys are camelCase too, so nothing is exempt.
				if !camelKey.MatchString(k) {
					t.Errorf("%s: key %q at %s is not camelCase", what, k, path)
				}
				walk(path+"."+k, child)
			}
		case []any:
			for _, child := range v {
				walk(path+"[]", child)
			}
		}
	}
	walk("$", doc)
	return doc
}

// TestJSONContract walks one representative command of each shape the README's
// "JSON contract" promises, in JSON mode against a stub, and checks the shape
// (#240). It runs in-process — one root, one stub per case — rather than as a
// child process per command, to stay fast.
func TestJSONContract(t *testing.T) {
	withConfig(t, loneProfile)

	const (
		domain   = `{"domainName":"example.com","locked":true,"autorenewEnabled":false}`
		unlocked = `{"domainName":"example.com","locked":false}`
		record   = `{"id":42,"domainName":"example.com","host":"www","fqdn":"www.example.com.","type":"TXT","answer":"a<b & c>d","ttl":300}`
	)

	type check func(t *testing.T, stdout map[string]any)
	wantList := func(n int) check {
		return func(t *testing.T, doc map[string]any) {
			data, ok := doc["data"].([]any)
			if !ok {
				t.Fatalf(`want {"data": [...]}, got %v`, doc)
			}
			if len(data) != n {
				t.Errorf("data has %d items, want %d", len(data), n)
			}
		}
	}
	wantResult := func(changed bool) check {
		return func(t *testing.T, doc map[string]any) {
			if doc["success"] != true || doc["changed"] != changed || doc["message"] == "" {
				t.Errorf(`want {"success": true, "changed": %v, "message": …}, got %v`, changed, doc)
			}
		}
	}

	tests := []struct {
		name   string
		args   []string
		routes map[string]reply
		check  check
	}{
		{
			name:   "paged list",
			args:   []string{"domain", "list"},
			routes: map[string]reply{"GET /core/v1/domains": {200, `{"domains":[` + domain + `],"totalCount":1}`}},
			check:  wantList(1),
		},
		{
			name: "domain check",
			args: []string{"domain", "check", "--authoritative", "example.com"},
			routes: map[string]reply{"POST /core/v1/domains:checkAvailability": {200,
				`{"results":[{"domainName":"example.com","sld":"example","tld":"com","purchasable":true,"purchasePrice":12.99}]}`}},
			check: wantList(1),
		},
		{
			name:   "dns export",
			args:   []string{"dns", "export", "example.com"},
			routes: map[string]reply{"GET /core/v1/domains/example.com/records": {200, `{"records":[` + record + `],"totalCount":1}`}},
			check:  wantList(1),
		},
		{
			name:  "config list-profiles",
			args:  []string{"config", "list-profiles"},
			check: wantList(1),
		},
		{
			name: "status keys",
			args: []string{"status"},
			routes: map[string]reply{
				"GET /core/v1/domains": {200, `{"domains":[{"domainName":"old.example","expireDate":"2020-01-01T00:00:00Z"}],"totalCount":1}`},
			},
			check: func(t *testing.T, doc map[string]any) {
				for _, k := range []string{"domainsTotal", "expiringCritical", "expiringSoon"} {
					if _, ok := doc[k]; !ok {
						t.Errorf("status has no %q key: %v", k, doc)
					}
				}
			},
		},
		{
			name: "dry-run",
			args: []string{"dns", "create", "example.com", "--type", "A", "--host", "www", "--answer", "192.0.2.1", "--dry-run"},
			check: func(t *testing.T, doc map[string]any) {
				if doc["dryRun"] != true || doc["method"] != "POST" {
					t.Errorf(`want {"dryRun": true, "method": "POST", …}, got %v`, doc)
				}
			},
		},
		{
			name:   "write with a resource",
			args:   []string{"dns", "delete", "example.com", "42", "--yes"},
			routes: map[string]reply{"GET /core/v1/domains/example.com/records/42": {200, record}},
			check:  wantResult(true),
		},
		{
			name:   "toggle that changes something",
			args:   []string{"domain", "lock", "on", "example.com", "--yes"},
			routes: map[string]reply{"GET /core/v1/domains/example.com": {200, unlocked}, "PATCH /core/v1/domains/example.com": {200, domain}},
			check:  wantResult(true),
		},
		{
			name:   "toggle already in that state",
			args:   []string{"domain", "lock", "on", "example.com", "--yes"},
			routes: map[string]reply{"GET /core/v1/domains/example.com": {200, domain}},
			check:  wantResult(false),
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resetFlags(t, tc.args)
			srv := contractServer(t, tc.routes)
			stdout, stderr, code := runContract(t, append([]string{"--base-url", srv.URL, "-o", "json"}, tc.args...)...)
			if code != 0 {
				t.Fatalf("exit %d, stderr:\n%s", code, stderr)
			}
			tc.check(t, decodeDoc(t, "stdout", stdout))
			if skipClientInit(mustFind(t, tc.args)) {
				return // builds no client, so has no --base-url warning
			}

			// stderr is one JSON document too: the --base-url warning,
			// which used to be a plain "! …" line, is in "warnings".
			warn := decodeDoc(t, "stderr", stderr)
			list, _ := warn["warnings"].([]any)
			if len(warn) != 1 || len(list) == 0 || !strings.Contains(list[0].(string), "--base-url") {
				t.Errorf(`stderr should be {"warnings": ["--base-url is set: …"]}, got:%s`, stderr)
			}
		})
	}
}

// TestJSONContract_NoHTMLEscaping pins SetEscapeHTML(false): a TXT record's
// "<", ">" and "&" came out as backslash-u escapes (#240).
func TestJSONContract_NoHTMLEscaping(t *testing.T) {
	withConfig(t, loneProfile)
	args := []string{"dns", "list", "example.com"}
	resetFlags(t, args)
	srv := contractServer(t, map[string]reply{"GET /core/v1/domains/example.com/records": {200,
		`{"records":[{"id":1,"domainName":"example.com","type":"TXT","answer":"a<b & c>d","ttl":300}],"totalCount":1}`}})
	stdout, stderr, code := runContract(t, append([]string{"--base-url", srv.URL, "-o", "json"}, args...)...)
	if code != 0 {
		t.Fatalf("exit %d, stderr:\n%s", code, stderr)
	}
	if !strings.Contains(stdout, `"a<b & c>d"`) {
		t.Errorf("want the answer unescaped, got:\n%s", stdout)
	}
}

// TestJSONContract_Errors checks the error envelope for each type a script
// can branch on: one JSON document on stderr with error.type, error.status
// for an API answer, error.message and error.hint, the deprecated top-level
// hint beside it, and the warnings the run collected (#240).
func TestJSONContract_Errors(t *testing.T) {
	withConfig(t, loneProfile)

	tests := []struct {
		name       string
		args       []string
		routes     map[string]reply
		wantType   string
		wantStatus float64 // 0: no "status" key
		wantCode   int
	}{
		{
			name:       "not found",
			args:       []string{"domain", "get", "example.com"},
			routes:     map[string]reply{"GET /core/v1/domains/example.com": {404, `{"message":"Not Found"}`}},
			wantType:   output.ErrorTypeNotFound,
			wantStatus: 404,
			wantCode:   4,
		},
		{
			name:       "auth",
			args:       []string{"domain", "get", "example.com"},
			routes:     map[string]reply{"GET /core/v1/domains/example.com": {401, `{"message":"Unauthorized"}`}},
			wantType:   output.ErrorTypeAuth,
			wantStatus: 401,
			wantCode:   3,
		},
		{
			name:       "rate limited",
			args:       []string{"domain", "get", "example.com", "--timeout", "500ms"},
			routes:     map[string]reply{"GET /core/v1/domains/example.com": {429, `{"message":"Too Many Requests"}`}},
			wantType:   output.ErrorTypeRateLimited,
			wantStatus: 429,
			wantCode:   5,
		},
		{
			// As the sandbox answers a duplicate record.
			name: "conflict",
			args: []string{"dns", "create", "example.com", "--type", "A", "--host", "www", "--answer", "192.0.2.1", "--yes"},
			routes: map[string]reply{"POST /core/v1/domains/example.com/records": {400,
				`{"message":"Parameter Value Error","details":"Record already exists"}`}},
			wantType:   output.ErrorTypeConflict,
			wantStatus: 400,
			wantCode:   1,
		},
		{
			name:     "usage",
			args:     []string{"domain", "get"},
			wantType: output.ErrorTypeUsage,
			wantCode: 2,
		},
		{
			name:     "confirmation required",
			args:     []string{"dns", "delete", "example.com", "42"},
			routes:   map[string]reply{"GET /core/v1/domains/example.com/records/42": {200, `{"id":42,"type":"A","answer":"192.0.2.1"}`}},
			wantType: output.ErrorTypeConfirmationRequired,
			wantCode: 2,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resetFlags(t, tc.args)
			srv := contractServer(t, tc.routes)
			stdout, stderr, code := runContract(t, append([]string{"--base-url", srv.URL, "-o", "json"}, tc.args...)...)
			if code != tc.wantCode {
				t.Errorf("exit %d, want %d", code, tc.wantCode)
			}
			if stdout != "" {
				t.Errorf("an error must leave stdout empty, got:\n%s", stdout)
			}
			doc := decodeDoc(t, "stderr", stderr)
			e, ok := doc["error"].(map[string]any)
			if !ok {
				t.Fatalf(`want {"error": {...}}, got:\n%s`, stderr)
			}
			if e["type"] != tc.wantType {
				t.Errorf("error.type = %v, want %q", e["type"], tc.wantType)
			}
			if got, _ := e["status"].(float64); got != tc.wantStatus {
				t.Errorf("error.status = %v, want %v", e["status"], tc.wantStatus)
			}
			if msg, _ := e["message"].(string); msg == "" {
				t.Error("error.message is empty")
			}
			if hint, _ := e["hint"].(string); hint != "" && doc["hint"] != hint {
				t.Errorf("the deprecated top-level hint should repeat error.hint %q, got %v", hint, doc["hint"])
			}
			// Usage errors fail before the client is built, so no warning.
			if tc.wantType != output.ErrorTypeUsage {
				if list, _ := doc["warnings"].([]any); len(list) == 0 {
					t.Errorf("the --base-url warning should be in the envelope's warnings, got:\n%s", stderr)
				}
			}
		})
	}
}

// TestJSONContract_AuthStatusDetails pins #240's last finding: a rejected
// `auth status` packed the profile, username, endpoint and config path into
// the message only. They are fields of error.details now.
func TestJSONContract_AuthStatusDetails(t *testing.T) {
	withConfig(t, loneProfile)
	srv := contractServer(t, map[string]reply{"GET /core/v1/hello": {401, `{"message":"Unauthorized"}`}})
	_, stderr, code := runContract(t, "--base-url", srv.URL, "-o", "json", "auth", "status")
	if code != 3 {
		t.Errorf("exit %d, want 3", code)
	}
	e, _ := decodeDoc(t, "stderr", stderr)["error"].(map[string]any)
	d, _ := e["details"].(map[string]any)
	if e["type"] != output.ErrorTypeAuth || d["profile"] != "work" || d["username"] != "workuser" || d["endpoint"] != srv.URL || d["config"] == "" {
		t.Errorf("want type auth and details {profile, username, endpoint, config}, got:\n%s", stderr)
	}
}

// TestJSONContract_UnknownCommandSuggestions keeps #237's suggestions in the
// new envelope, under error.suggestions with type usage.
func TestJSONContract_UnknownCommandSuggestions(t *testing.T) {
	_, stderr, code := runContract(t, "-o", "json", "records")
	if code != 2 {
		t.Errorf("exit %d, want 2", code)
	}
	e, _ := decodeDoc(t, "stderr", stderr)["error"].(map[string]any)
	list, _ := e["suggestions"].([]any)
	if e["type"] != output.ErrorTypeUsage || len(list) == 0 || list[0] != "namecom dns" {
		t.Errorf(`want type usage and suggestions ["namecom dns", …], got:\n%s`, stderr)
	}
}
