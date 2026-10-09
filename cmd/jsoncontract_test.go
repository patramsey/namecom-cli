package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
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
	return runContractTTY(t, false, args...)
}

// runContractTTY is runContract with output.IsInteractive answering
// interactive.
func runContractTTY(t *testing.T, interactive bool, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	t.Cleanup(output.StubInteractive(interactive))

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
	prevGF, prevResolved, prevClient := gf, resolvedOut, resolvedClient
	os.Stdout, os.Stderr = outF, errF
	resolvedOut, resolvedClient = nil, nil
	t.Cleanup(func() {
		os.Stdout, os.Stderr = prevOut, prevErr
		gf, resolvedOut, resolvedClient = prevGF, prevResolved, prevClient
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
	// wantResults checks a write over several targets: the write result,
	// changed when any target changed, and each target under "data" as
	// {target, changed} pairs — target is the domain, or the record ID.
	wantResults := func(changed bool, targets ...[]any) check {
		return func(t *testing.T, doc map[string]any) {
			wantResult(changed)(t, doc)
			wantList(len(targets))(t, doc)
			data, _ := doc["data"].([]any)
			for i, want := range targets {
				if i >= len(data) {
					break
				}
				item, _ := data[i].(map[string]any)
				target := item["domain"]
				if _, ok := want[0].(float64); ok {
					target = item["id"]
				}
				if target != want[0] || item["changed"] != want[1] || item["message"] == "" {
					t.Errorf("data[%d] = %v, want target %v with changed %v", i, item, want[0], want[1])
				}
			}
		}
	}
	// wantRecord checks `dns create --if-not-exists`: the record, with
	// "changed" saying whether it was created.
	wantRecord := func(changed bool) check {
		return func(t *testing.T, doc map[string]any) {
			if doc["id"] != float64(42) || doc["changed"] != changed {
				t.Errorf(`want the record with id 42 and "changed": %v, got %v`, changed, doc)
			}
		}
	}

	// One A record, as the API returns it, and a file listing the same.
	const aRecord = `{"id":42,"domainName":"example.com","host":"www","fqdn":"www.example.com.","type":"A","answer":"192.0.2.1","ttl":300}`
	recordsFile := filepath.Join(t.TempDir(), "records.json")
	if err := os.WriteFile(recordsFile, []byte(`{"data":[{"type":"A","host":"www","answer":"192.0.2.1","ttl":300}]}`), 0o600); err != nil {
		t.Fatal(err)
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

		// The commands #259–#261 added, held to the same rules.
		{
			name:   "domain get with one domain is the object",
			args:   []string{"domain", "get", "a.com"},
			routes: map[string]reply{"GET /core/v1/domains/a.com": {200, `{"domainName":"a.com"}`}},
			check: func(t *testing.T, doc map[string]any) {
				if doc["domainName"] != "a.com" {
					t.Errorf("want the domain object, got %v", doc)
				}
			},
		},
		{
			name: "domain get with several domains is a list",
			args: []string{"domain", "get", "a.com", "b.com"},
			routes: map[string]reply{
				"GET /core/v1/domains/a.com": {200, `{"domainName":"a.com"}`},
				"GET /core/v1/domains/b.com": {200, `{"domainName":"b.com"}`},
			},
			check: wantList(2),
		},
		{
			name: "toggle over several domains",
			args: []string{"domain", "lock", "on", "a.com", "b.com", "--yes"},
			routes: map[string]reply{
				"GET /core/v1/domains/a.com": {200, `{"domainName":"a.com","locked":true}`},
				"GET /core/v1/domains/b.com": {200, `{"domainName":"b.com","locked":false}`},
			},
			check: wantResults(true, []any{"a.com", false}, []any{"b.com", true}),
		},
		{
			name: "toggle over several domains, none changed",
			args: []string{"domain", "lock", "on", "a.com", "b.com", "--yes"},
			routes: map[string]reply{
				"GET /core/v1/domains/a.com": {200, `{"domainName":"a.com","locked":true}`},
				"GET /core/v1/domains/b.com": {200, `{"domainName":"b.com","locked":true}`},
			},
			check: wantResults(false, []any{"a.com", false}, []any{"b.com", false}),
		},
		{
			name: "dns delete with several IDs",
			args: []string{"dns", "delete", "example.com", "1", "2", "--yes"},
			routes: map[string]reply{
				"GET /core/v1/domains/example.com/records/1": {200, `{"id":1,"type":"A","answer":"192.0.2.1"}`},
				"GET /core/v1/domains/example.com/records/2": {200, `{"id":2,"type":"A","answer":"192.0.2.2"}`},
			},
			check: wantResults(true, []any{float64(1), true}, []any{float64(2), true}),
		},
		{
			name: "dns delete --if-exists, one present and one gone",
			args: []string{"dns", "delete", "example.com", "1", "2", "--if-exists", "--yes"},
			routes: map[string]reply{
				"GET /core/v1/domains/example.com/records/1": {200, `{"id":1,"type":"A","answer":"192.0.2.1"}`},
				"GET /core/v1/domains/example.com/records/2": {404, `{"message":"Not Found"}`},
			},
			check: wantResults(true, []any{float64(1), true}, []any{float64(2), false}),
		},
		{
			name: "dns delete --if-exists, all gone",
			args: []string{"dns", "delete", "example.com", "1", "2", "--if-exists", "--yes"},
			routes: map[string]reply{
				"GET /core/v1/domains/example.com/records/1": {404, `{"message":"Not Found"}`},
				"GET /core/v1/domains/example.com/records/2": {404, `{"message":"Not Found"}`},
				"GET /core/v1/domains/example.com/records":   {200, `{"records":[]}`},
			},
			check: wantResults(false, []any{float64(1), false}, []any{float64(2), false}),
		},
		{
			name: "dns delete --if-exists, one ID gone",
			args: []string{"dns", "delete", "example.com", "1", "--if-exists", "--yes"},
			routes: map[string]reply{
				"GET /core/v1/domains/example.com/records/1": {404, `{"message":"Not Found"}`},
				"GET /core/v1/domains/example.com/records":   {200, `{"records":[]}`},
			},
			check: wantResult(false),
		},
		{
			name:   "dns create --if-not-exists, already there",
			args:   []string{"dns", "create", "example.com", "--type", "A", "--host", "www", "--answer", "192.0.2.1", "--if-not-exists", "--yes"},
			routes: map[string]reply{"GET /core/v1/domains/example.com/records": {200, `{"records":[` + aRecord + `]}`}},
			check:  wantRecord(false),
		},
		{
			name: "dns create --if-not-exists, created",
			args: []string{"dns", "create", "example.com", "--type", "A", "--host", "www", "--answer", "192.0.2.1", "--if-not-exists", "--yes"},
			routes: map[string]reply{
				"GET /core/v1/domains/example.com/records":  {200, `{"records":[]}`},
				"POST /core/v1/domains/example.com/records": {200, aRecord},
			},
			check: wantRecord(true),
		},
		{
			name:   "dns import --skip-existing with nothing new",
			args:   []string{"dns", "import", "example.com", "--file", recordsFile, "--skip-existing", "--yes"},
			routes: map[string]reply{"GET /core/v1/domains/example.com/records": {200, `{"records":[` + aRecord + `]}`}},
			check:  wantResult(false),
		},
		{
			name:   "dns sync --dry-run",
			args:   []string{"dns", "sync", "example.com", "--file", recordsFile, "--dry-run"},
			routes: map[string]reply{"GET /core/v1/domains/example.com/records": {200, `{"records":[]}`}},
			check: func(t *testing.T, doc map[string]any) {
				wantList(1)(t, doc)
				creates, _ := doc["creates"].([]any)
				if doc["dryRun"] != true || len(creates) != 1 {
					t.Errorf(`want {"dryRun": true, "creates": [1 record], "data": [1 request]}, got %v`, doc)
				}
			},
		},
		{
			name: "dns sync",
			args: []string{"dns", "sync", "example.com", "--file", recordsFile, "--yes"},
			routes: map[string]reply{
				"GET /core/v1/domains/example.com/records":  {200, `{"records":[]}`},
				"POST /core/v1/domains/example.com/records": {200, aRecord},
			},
			check: func(t *testing.T, doc map[string]any) {
				applied, _ := doc["applied"].([]any)
				if doc["changed"] != true || len(applied) != 1 {
					t.Errorf(`want {"changed": true, "applied": [1 change]}, got %v`, doc)
				}
			},
		},
		{
			name:   "dns sync with nothing to change",
			args:   []string{"dns", "sync", "example.com", "--file", recordsFile, "--yes"},
			routes: map[string]reply{"GET /core/v1/domains/example.com/records": {200, `{"records":[` + aRecord + `]}`}},
			check: func(t *testing.T, doc map[string]any) {
				if doc["changed"] != false || doc["unchanged"] != float64(1) {
					t.Errorf(`want {"changed": false, "unchanged": 1}, got %v`, doc)
				}
			},
		},
		{
			name: "config show credential sources",
			args: []string{"config", "show"},
			check: func(t *testing.T, doc map[string]any) {
				for _, k := range []string{"profileSource", "usernameSource", "tokenSource", "endpointSource"} {
					if _, ok := doc[k]; !ok {
						t.Errorf("config show has no %q key: %v", k, doc)
					}
				}
			},
		},
		{
			name:   "auth status credential sources",
			args:   []string{"auth", "status"},
			routes: map[string]reply{"GET /core/v1/hello": {200, `{"username":"workuser"}`}},
			check: func(t *testing.T, doc map[string]any) {
				for _, k := range []string{"usernameSource", "tokenSource", "verified"} {
					if _, ok := doc[k]; !ok {
						t.Errorf("auth status has no %q key: %v", k, doc)
					}
				}
			},
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
			// A 403 about the domain, not the credentials, is not an auth
			// failure: a loop stopping on exit 3 stopped at the first
			// expired domain (#324).
			name:       "403 for an expired domain",
			args:       []string{"dns", "list", "example.com"},
			routes:     map[string]reply{"GET /core/v1/domains/example.com/records": {403, `{"message":"Permission denied. The domain is expired."}`}},
			wantType:   output.ErrorTypeAPI,
			wantStatus: 403,
			wantCode:   1,
		},
		{
			name:       "403 for the account",
			args:       []string{"domain", "get", "example.com"},
			routes:     map[string]reply{"GET /core/v1/domains/example.com": {403, `{"message":"Permission Denied","details":"IP not whitelisted"}`}},
			wantType:   output.ErrorTypeAuth,
			wantStatus: 403,
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
			// The API answers a create for an existing mailbox with 200 and
			// the entry unchanged (#283): a conflict, with no status.
			name: "email create on an existing mailbox",
			args: []string{"email", "create", "example.com", "info", "--to", "new@example.org", "--yes"},
			routes: map[string]reply{"POST /core/v1/domains/example.com/email/forwarding": {200,
				`{"domainName":"example.com","emailBox":"info","emailTo":"old@example.org"}`}},
			wantType: output.ErrorTypeConflict,
			wantCode: 1,
		},
		{
			name:     "usage",
			args:     []string{"domain", "get"},
			wantType: output.ErrorTypeUsage,
			wantCode: 2,
		},
		{
			// A check made before any request is how the command was
			// invoked, not an API failure (#291).
			name:     "vanity-ns hostname outside the domain",
			args:     []string{"vanity-ns", "get", "example.com", "ns1.other.com"},
			wantType: output.ErrorTypeUsage,
			wantCode: 2,
		},
		{
			name:     "config use for a profile that does not exist",
			args:     []string{"config", "use", "nosuch", "--dry-run"},
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
			// The advice is in error.hint, not appended to the message (#324).
			if tc.wantType == output.ErrorTypeConfirmationRequired {
				if hint, _ := e["hint"].(string); hint != "pass --yes to confirm when not running in a terminal" {
					t.Errorf("error.hint = %q, want the --yes advice", hint)
				}
				if msg, _ := e["message"].(string); strings.Contains(msg, "--yes") {
					t.Errorf("error.message %q should leave the advice to the hint", msg)
				}
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

// TestJSONContract_NotFoundHint pins #291: a not-found error names what is
// missing in error.message and says what to run in error.hint. The advice was
// folded into the message, with no hint, for the commands that named the
// object, and the domain-scoped lists said only "Not Found" or "Domain not
// found." with the generic hint. Each sends one request, as before.
func TestJSONContract_NotFoundHint(t *testing.T) {
	withConfig(t, loneProfile)
	domainMsg, domainHint := `domain "example.com" not found`, "run 'namecom domain list' to see your domains"
	tests := []struct {
		args      []string
		msg, hint string
	}{
		{[]string{"domain", "get", "example.com"}, domainMsg, domainHint},
		{[]string{"domain", "auth-code", "example.com"}, domainMsg, domainHint},
		{[]string{"dns", "list", "example.com"}, domainMsg, domainHint},
		{[]string{"dns", "export", "example.com"}, domainMsg, domainHint},
		{[]string{"email", "list", "example.com"}, domainMsg, domainHint},
		{[]string{"url", "list", "example.com"}, domainMsg, domainHint},
		{[]string{"dnssec", "list", "example.com"}, domainMsg, domainHint},
		{[]string{"vanity-ns", "list", "example.com"}, domainMsg, domainHint},
		{[]string{"url", "get", "example.com", "7"}, "URL forwarding 7 not found on example.com",
			"run 'namecom url list example.com' to see its forwarding IDs"},
		{[]string{"email", "get", "example.com", "info"}, "mailbox info@example.com not found",
			"run 'namecom email list example.com' to see its mailboxes"},
		{[]string{"dnssec", "get", "example.com", "ABCD"}, "DS record ABCD not found on example.com",
			"run 'namecom dnssec list example.com' to see its digests"},
		{[]string{"vanity-ns", "get", "example.com", "ns1.example.com"}, "vanity nameserver ns1.example.com not found on example.com",
			"run 'namecom vanity-ns list example.com' to see its vanity nameservers"},
		{[]string{"order", "get", "12"}, "order 12 not found", "run 'namecom order list' to see your orders"},
		{[]string{"transfer", "get", "example.com"}, `transfer of "example.com" not found`,
			"run 'namecom transfer list' to see active transfers"},
	}
	for _, tc := range tests {
		t.Run(strings.Join(tc.args[:2], " "), func(t *testing.T) {
			resetFlags(t, tc.args)
			var n atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				n.Add(1)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"message":"Not Found"}`))
			}))
			t.Cleanup(srv.Close)
			_, stderr, code := runContract(t, append([]string{"--base-url", srv.URL, "-o", "json"}, tc.args...)...)
			if code != 4 {
				t.Errorf("exit %d, want 4", code)
			}
			if got := n.Load(); got != 1 {
				t.Errorf("sent %d requests, want 1", got)
			}
			e, _ := decodeDoc(t, "stderr", stderr)["error"].(map[string]any)
			if e["type"] != output.ErrorTypeNotFound || e["message"] != tc.msg || e["hint"] != tc.hint {
				t.Errorf("want type not_found, message %q, hint %q; got:\n%s", tc.msg, tc.hint, stderr)
			}
		})
	}
}

// TestJSONContract_CheckExitStatusType pins #288: `domain check
// --exit-status` finding a name taken exits 1 with an error typed
// "unavailable". It was "api", though nothing failed at the API.
func TestJSONContract_CheckExitStatusType(t *testing.T) {
	withConfig(t, loneProfile)
	args := []string{"domain", "check", "--exit-status", "--authoritative", "example.com"}
	resetFlags(t, args)
	srv := contractServer(t, map[string]reply{"POST /core/v1/domains:checkAvailability": {200,
		`{"results":[{"domainName":"example.com","purchasable":false}]}`}})
	_, stderr, code := runContract(t, append([]string{"--base-url", srv.URL, "-o", "json"}, args...)...)
	if code != 1 {
		t.Errorf("exit %d, want 1", code)
	}
	e, _ := decodeDoc(t, "stderr", stderr)["error"].(map[string]any)
	if e["type"] != output.ErrorTypeUnavailable {
		t.Errorf("error.type = %v, want %q\n%s", e["type"], output.ErrorTypeUnavailable, stderr)
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
	// Where each came from (#246), as sibling keys, as in the successful
	// output.
	for _, k := range []string{"usernameSource", "tokenSource", "endpointSource"} {
		if s, _ := d[k].(string); s == "" {
			t.Errorf("details has no %q: %v", k, d)
		}
	}
}

// TestJSONContract_SyncOutcomeUnknown pins how `dns sync` reports a write
// that failed in a way that may have gone through (#243): stdout is the
// result document, with what was applied and the failed change marked
// "outcomeUnknown"; stderr is the envelope, exit 6, with the idempotency key
// and a hint to run sync again rather than to pin the key.
func TestJSONContract_SyncOutcomeUnknown(t *testing.T) {
	withConfig(t, loneProfile)
	file := filepath.Join(t.TempDir(), "records.json")
	if err := os.WriteFile(file, []byte(`[{"type":"A","host":"www","answer":"192.0.2.1","ttl":300}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	args := []string{"dns", "sync", "example.com", "--file", file, "--yes"}
	resetFlags(t, args)
	srv := contractServer(t, map[string]reply{
		"GET /core/v1/domains/example.com/records":  {200, `{"records":[]}`},
		"POST /core/v1/domains/example.com/records": {500, `{"message":"Internal Error"}`},
	})
	stdout, stderr, code := runContract(t, append([]string{"--base-url", srv.URL, "-o", "json"}, args...)...)
	if code != 6 {
		t.Errorf("exit %d, want 6", code)
	}
	res := decodeDoc(t, "stdout", stdout)
	failed, _ := res["failed"].(map[string]any)
	if res["changed"] != false || failed["outcomeUnknown"] != true || failed["action"] != "create" {
		t.Errorf(`want {"changed": false, "failed": {"action": "create", "outcomeUnknown": true, …}}, got %v`, res)
	}
	e, _ := decodeDoc(t, "stderr", stderr)["error"].(map[string]any)
	hint, _ := e["hint"].(string)
	if e["status"] != float64(500) || e["idempotencyKey"] == nil || !strings.Contains(hint, "run sync again") {
		t.Errorf("want status 500, the idempotency key, and a hint to run sync again, got:\n%s", stderr)
	}
}

// TestJSONContract_BaseURLEnvWarning: the notice that NAMECOM_BASE_URL (#246)
// is in use is a collected warning in JSON mode, like --base-url's, so stderr
// stays one document.
func TestJSONContract_BaseURLEnvWarning(t *testing.T) {
	withConfig(t, loneProfile)
	args := []string{"domain", "get", "example.com"}
	resetFlags(t, args)
	srv := contractServer(t, map[string]reply{"GET /core/v1/domains/example.com": {200, `{"domainName":"example.com"}`}})
	t.Setenv("NAMECOM_BASE_URL", srv.URL)
	stdout, stderr, code := runContract(t, append([]string{"-o", "json"}, args...)...)
	if code != 0 {
		t.Fatalf("exit %d, stderr:\n%s", code, stderr)
	}
	decodeDoc(t, "stdout", stdout)
	list, _ := decodeDoc(t, "stderr", stderr)["warnings"].([]any)
	if len(list) != 1 || !strings.Contains(list[0].(string), "NAMECOM_BASE_URL") {
		t.Errorf(`stderr should be {"warnings": ["NAMECOM_BASE_URL is set: …"]}, got:%s`, stderr)
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

// TestJSONContract_APIMethodNotAllowed pins #291's 405 item, from #282:
// `namecom api /core/v1/orders -f …` sends a POST, which the API answers
// with 404 "Method Not Allowed", and the hint said to check the name or ID
// for typos. It now says -f made the POST and to pass -X GET; with the
// method given, the hint is about the method. One request either way.
func TestJSONContract_APIMethodNotAllowed(t *testing.T) {
	withConfig(t, loneProfile)
	for _, tc := range []struct {
		args       []string
		status     int
		wantHint   string
		wantCode   int
		wantStatus float64
	}{
		{[]string{"api", "/core/v1/orders", "-f", "perPage=1", "--yes"}, 404, "pass -X GET", 4, 404},
		{[]string{"api", "/core/v1/orders", "-f", "perPage=1", "--yes"}, 405, "pass -X GET", 1, 405},
		{[]string{"api", "-X", "DELETE", "/core/v1/orders", "--yes"}, 405, "does not accept this method", 1, 405},
	} {
		t.Run(fmt.Sprint(tc.args[2], " ", tc.status), func(t *testing.T) {
			resetFlags(t, tc.args)
			var n atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				n.Add(1)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(`{"message":"Method Not Allowed"}`))
			}))
			t.Cleanup(srv.Close)
			_, stderr, code := runContract(t, append([]string{"--base-url", srv.URL, "-o", "json"}, tc.args...)...)
			if code != tc.wantCode {
				t.Errorf("exit %d, want %d", code, tc.wantCode)
			}
			if got := n.Load(); got != 1 {
				t.Errorf("sent %d requests, want 1", got)
			}
			e, _ := decodeDoc(t, "stderr", stderr)["error"].(map[string]any)
			hint, _ := e["hint"].(string)
			if !strings.Contains(hint, tc.wantHint) || strings.Contains(hint, "typos") || e["status"] != tc.wantStatus {
				t.Errorf("want status %v and a hint containing %q, got:\n%s", tc.wantStatus, tc.wantHint, stderr)
			}
		})
	}
}

// TestJSONContract_WrongTokenNamed pins #291: an unknown flag ahead of the
// subcommand was reported as an unknown command, naming whatever word cobra
// had skipped to, and an argument to a command that takes none was an
// "unknown command" too.
func TestJSONContract_WrongTokenNamed(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--bogus", "domain", "list"}, "unknown flag: --bogus"},
		{[]string{"--bogus", "-o", "json", "domain", "list"}, "unknown flag: --bogus"},
		{[]string{"-Z", "domain", "list"}, `unknown shorthand flag: 'Z' in -Z`},
		{[]string{"--sandbox", "nosuch"}, `unknown command "nosuch" for "namecom"`},
		{[]string{"status", "extra"}, `namecom status takes no arguments, got "extra"`},
		{[]string{"version", "extra"}, `namecom version takes no arguments, got "extra"`},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			prevArgs := os.Args
			t.Cleanup(func() { os.Args = prevArgs })
			args := append([]string{"-o", "json"}, tc.args...)
			os.Args = append([]string{"namecom"}, args...)
			_, stderr, code := runContract(t, args...)
			if code != 2 {
				t.Errorf("exit %d, want 2", code)
			}
			e, _ := decodeDoc(t, "stderr", stderr)["error"].(map[string]any)
			if e["type"] != output.ErrorTypeUsage || e["message"] != tc.want {
				t.Errorf("want type usage, message %q; got:\n%s", tc.want, stderr)
			}
		})
	}
}
