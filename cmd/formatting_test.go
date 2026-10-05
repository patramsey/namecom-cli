package cmd

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/patramsey/namecom-cli/internal/output"
)

// Two domains as `domain list` gets them. The second has no locked key; the
// SDK's type prints it as false all the same.
const formattingDomains = `{"domains":[` +
	`{"domainName":"a.com","locked":true,"autorenewEnabled":false,"expireDate":"2030-01-02T00:00:00Z"},` +
	`{"domainName":"b.com","autorenewEnabled":true,"expireDate":"2031-03-04T00:00:00Z"}` +
	`],"totalCount":2}`

// runFormatting runs `namecom <args>` against a stub answering routes, with
// --base-url pointing at it, and returns stdout, stderr and the exit code.
func runFormatting(t *testing.T, routes map[string]reply, args ...string) (string, string, int) {
	t.Helper()
	withConfig(t, loneProfile)
	resetFlags(t, args)
	srv := contractServer(t, routes)
	return runContract(t, append([]string{"--base-url", srv.URL}, args...)...)
}

// TestFormattingTopic: `namecom help formatting` prints the topic, root help
// lists it, and the flags point to it.
func TestFormattingTopic(t *testing.T) {
	var buf strings.Builder
	rootCmd.SetOut(&buf)
	t.Cleanup(func() { rootCmd.SetOut(nil) })
	if err := executeRoot(t, "help", "formatting"); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"--fields", "--jq", "tsv", `\t`} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("help formatting does not mention %s", want)
		}
	}
	if !strings.Contains(section(renderHelp(t), "Help Topics:"), "formatting") {
		t.Error("root help does not list the formatting topic")
	}
	for _, name := range []string{"fields", "jq"} {
		if f := rootCmd.PersistentFlags().Lookup(name); !strings.Contains(f.Usage, "help formatting") {
			t.Errorf("--%s does not point to the topic: %q", name, f.Usage)
		}
	}
}

// TestFields pins --fields (#241): it keeps the named keys, in the order
// named, of each list item — leaving the list's envelope alone — or of a
// single object, and prints them in whichever format was asked for.
func TestFields(t *testing.T) {
	list := map[string]reply{"GET /core/v1/domains": {200, formattingDomains}}
	get := map[string]reply{"GET /core/v1/domains/a.com": {200, `{"domainName":"a.com","locked":true,"renewalPrice":12.99}`}}

	tests := []struct {
		name   string
		routes map[string]reply
		args   []string
		want   string
	}{
		{
			name:   "json list keeps the envelope and the named order",
			routes: list,
			args:   []string{"domain", "list", "-o", "json", "--fields", "locked,domainName"},
			want: `{
  "data": [
    {
      "locked": true,
      "domainName": "a.com"
    },
    {
      "locked": false,
      "domainName": "b.com"
    }
  ],
  "total": 2
}
`,
		},
		{
			name:   "yaml",
			routes: list,
			args:   []string{"domain", "list", "-o", "yaml", "--fields", "domainName"},
			want:   "data:\n    - domainName: a.com\n    - domainName: b.com\ntotal: 2\n",
		},
		{
			name:   "tsv has a header of the fields",
			routes: list,
			args:   []string{"domain", "list", "-o", "tsv", "--fields", "domainName,locked,autorenewEnabled"},
			want:   "domainName\tlocked\tautorenewEnabled\na.com\ttrue\tfalse\nb.com\tfalse\ttrue\n",
		},
		{
			name:   "tsv --no-header",
			routes: list,
			args:   []string{"domain", "list", "-o", "tsv", "--no-header", "--fields", "domainName"},
			want:   "a.com\nb.com\n",
		},
		{
			// Not a terminal, so the table is plain; the columns are the
			// fields, in order, and a missing value is the table's dash.
			name:   "table shows exactly those columns",
			routes: list,
			args:   []string{"domain", "list", "-o", "table", "--fields", "locked,domainName"},
			want:   "locked  domainName\ntrue    a.com\nfalse   b.com\n",
		},
		{
			name:   "repeated flag and spaces",
			routes: list,
			args:   []string{"domain", "list", "-o", "tsv", "--fields", "domainName", "--fields", " locked "},
			want:   "domainName\tlocked\na.com\ttrue\nb.com\tfalse\n",
		},
		{
			name:   "an object is projected itself",
			routes: get,
			args:   []string{"domain", "get", "a.com", "-o", "json", "--fields", "renewalPrice,domainName"},
			want:   "{\n  \"renewalPrice\": 12.99,\n  \"domainName\": \"a.com\"\n}\n",
		},
		{
			name:   "an object in tsv is one row",
			routes: get,
			args:   []string{"domain", "get", "a.com", "-o", "tsv", "--fields", "domainName,renewalPrice"},
			want:   "domainName\trenewalPrice\na.com\t12.99\n",
		},
		{
			name: "a write result",
			routes: map[string]reply{
				"GET /core/v1/domains/a.com":   {200, `{"domainName":"a.com","locked":false}`},
				"PATCH /core/v1/domains/a.com": {200, `{"domainName":"a.com","locked":true}`},
			},
			args: []string{"domain", "lock", "on", "a.com", "--yes", "-o", "tsv", "--fields", "changed"},
			want: "changed\ntrue\n",
		},
		{
			name: "a dry run",
			args: []string{"dns", "create", "example.com", "--type", "A", "--host", "www", "--answer", "192.0.2.1", "--dry-run", "-o", "json", "--fields", "method,path"},
			want: "{\n  \"method\": \"POST\",\n  \"path\": \"/core/v1/domains/example.com/records\"\n}\n",
		},
		{
			name:   "an empty list",
			routes: map[string]reply{"GET /core/v1/domains": {200, `{"domains":[]}`}},
			args:   []string{"domain", "list", "-o", "tsv", "--fields", "anything"},
			want:   "anything\n",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			stdout, stderr, code := runFormatting(t, tc.routes, tc.args...)
			if code != 0 {
				t.Fatalf("exit %d, stderr:\n%s", code, stderr)
			}
			if stdout != tc.want {
				t.Errorf("stdout:\n%s\nwant:\n%s", stdout, tc.want)
			}
		})
	}
}

// TestFields_WarningsKeepTheirFormat: --fields runs the command in JSON mode,
// which collects warnings for a JSON document. Asked for a table, they still
// print as "! " lines, not as {"warnings": …}.
func TestFields_WarningsKeepTheirFormat(t *testing.T) {
	_, stderr, code := runFormatting(t, map[string]reply{"GET /core/v1/domains": {200, formattingDomains}},
		"domain", "list", "-o", "table", "--fields", "domainName")
	if code != 0 {
		t.Fatalf("exit %d, stderr:\n%s", code, stderr)
	}
	if !strings.HasPrefix(stderr, "! --base-url is set") {
		t.Errorf("want the --base-url warning as a \"! \" line, got:\n%s", stderr)
	}
}

// TestFields_Unknown: a field no item has is a usage error that names the
// fields there are, and prints nothing — unless a write was sent, which a
// usage error would misreport as not made: then the document is printed as
// it was, with a warning.
func TestFields_Unknown(t *testing.T) {
	stdout, stderr, code := runFormatting(t, map[string]reply{"GET /core/v1/domains": {200, formattingDomains}},
		"domain", "list", "-o", "json", "--fields", "domainName,domainNme")
	if code != 2 {
		t.Fatalf("exit %d, want 2; stderr:\n%s", code, stderr)
	}
	if stdout != "" {
		t.Errorf("stdout should be empty, got:\n%s", stdout)
	}
	env := decodeDoc(t, "stderr", stderr)
	e, _ := env["error"].(map[string]any)
	msg, _ := e["message"].(string)
	if e["type"] != output.ErrorTypeUsage || !strings.Contains(msg, `"domainNme"`) ||
		!strings.Contains(msg, "domainName, autorenewEnabled, locked") {
		t.Errorf("want a usage error naming domainNme and the fields available, got %v", e)
	}

	stdout, stderr, code = runFormatting(t, map[string]reply{
		"GET /core/v1/domains/a.com":   {200, `{"domainName":"a.com","locked":false}`},
		"PATCH /core/v1/domains/a.com": {200, `{"domainName":"a.com","locked":true}`},
	}, "domain", "lock", "on", "a.com", "--yes", "-o", "json", "--fields", "nope")
	if code != 0 {
		t.Fatalf("after a write: exit %d, want 0; stderr:\n%s", code, stderr)
	}
	if doc := decodeDoc(t, "stdout", stdout); doc["changed"] != true {
		t.Errorf("want the unfiltered write result, got %v", doc)
	}
	if !strings.Contains(stderr, `unknown field \"nope\"`) || !strings.Contains(stderr, "printed unfiltered") {
		t.Errorf("want a warning about the field, got:\n%s", stderr)
	}
}

// TestJQ pins --jq (#241): the exact document -o json prints, filtered by
// gojq, a string result printed without quotes and anything else as compact
// JSON, one result to a line.
func TestJQ(t *testing.T) {
	list := map[string]reply{"GET /core/v1/domains": {200, formattingDomains}}
	tests := []struct {
		name   string
		routes map[string]reply
		args   []string
		want   string
	}{
		{"strings print raw", list, []string{"domain", "list", "--jq", ".data[].domainName"}, "a.com\nb.com\n"},
		{"anything else is compact JSON", list, []string{"domain", "list", "--jq", ".data[0] | {domainName, locked}"}, `{"domainName":"a.com","locked":true}` + "\n"},
		{"the envelope is there to read", list, []string{"domain", "list", "--jq", ".total"}, "2\n"},
		{"with -o json", list, []string{"domain", "list", "-o", "json", "--jq", ".data | length"}, "2\n"},
		{"after --fields", list, []string{"domain", "list", "--fields", "locked", "--jq", ".data[0] | keys"}, `["locked"]` + "\n"},
		{"no output", list, []string{"domain", "list", "--jq", "empty"}, ""},
		{
			name: "a dry run",
			args: []string{"dns", "create", "example.com", "--type", "A", "--host", "www", "--answer", "192.0.2.1", "--dry-run", "--jq", `.method + " " + .body.answer`},
			want: "POST 192.0.2.1\n",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			stdout, stderr, code := runFormatting(t, tc.routes, tc.args...)
			if code != 0 {
				t.Fatalf("exit %d, stderr:\n%s", code, stderr)
			}
			if stdout != tc.want {
				t.Errorf("stdout:\n%q\nwant:\n%q", stdout, tc.want)
			}
		})
	}
}

// TestJQ_UsageErrors: what can be wrong on the command line is a usage error
// before anything is sent; an expression that fails on the output is one
// after a read, with nothing printed.
func TestJQ_UsageErrors(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string // in the error message
	}{
		{"bad expression", []string{"domain", "list", "--jq", ".data["}, "--jq: "},
		{"unknown function", []string{"domain", "list", "--jq", "nosuch(1)"}, "nosuch"},
		{"with -o yaml", []string{"domain", "list", "-o", "yaml", "--jq", "."}, "cannot be combined with -o yaml"},
		{"with -o table", []string{"domain", "list", "-o", "table", "--jq", "."}, "cannot be combined with -o table"},
		{"--quiet and --jq", []string{"domain", "list", "-q", "--jq", "."}, "--quiet cannot be combined with --jq"},
		{"--quiet and --fields", []string{"domain", "list", "-q", "--fields", "domainName"}, "--quiet cannot be combined with --fields"},
		{"empty --fields", []string{"domain", "list", "--fields", ","}, "at least one field"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			withConfig(t, loneProfile)
			resetFlags(t, tc.args)
			var hits atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				hits.Add(1)
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"records":[]}`))
			}))
			t.Cleanup(srv.Close)
			stdout, stderr, code := runContract(t, append([]string{"--base-url", srv.URL}, tc.args...)...)
			if code != 2 {
				t.Fatalf("exit %d, want 2; stderr:\n%s", code, stderr)
			}
			if stdout != "" {
				t.Errorf("stdout should be empty, got:\n%s", stdout)
			}
			if !strings.Contains(stderr, tc.want) {
				t.Errorf("stderr should mention %q:\n%s", tc.want, stderr)
			}
			if hits.Load() != 0 {
				t.Errorf("%d requests sent; a usage error must stop the command first", hits.Load())
			}
		})
	}

	// Fails on this output: .data is an array, not an object.
	stdout, stderr, code := runFormatting(t, map[string]reply{"GET /core/v1/domains": {200, formattingDomains}},
		"domain", "list", "--jq", ".data.domainName")
	if code != 2 || stdout != "" || !strings.Contains(stderr, "--jq: ") {
		t.Errorf("a runtime --jq error: exit %d, stdout %q, stderr:\n%s", code, stdout, stderr)
	}

	// A command whose output is not JSON: a zone file.
	stdout, stderr, code = runFormatting(t, map[string]reply{"GET /core/v1/domains/example.com/records": {200,
		`{"records":[{"id":1,"domainName":"example.com","fqdn":"example.com.","type":"A","answer":"192.0.2.1","ttl":300}]}`}},
		"dns", "export", "example.com", "--zone", "--fields", "x")
	if code != 2 || stdout != "" || !strings.Contains(stderr, "needs JSON output") {
		t.Errorf("a zone file with --fields: exit %d, stdout %q, stderr:\n%s", code, stdout, stderr)
	}
}

// TestJQ_CommandErrorStaysOnStderr: --jq filters stdout only. A failing
// command's error envelope is on stderr as always, with its exit code.
func TestJQ_CommandErrorStaysOnStderr(t *testing.T) {
	stdout, stderr, code := runFormatting(t, map[string]reply{"GET /core/v1/domains/a.com": {404, `{"message":"Not Found"}`}},
		"domain", "get", "a.com", "--jq", ".domainName")
	if code != 4 {
		t.Fatalf("exit %d, want 4; stderr:\n%s", code, stderr)
	}
	if stdout != "" {
		t.Errorf("stdout should be empty, got %q", stdout)
	}
	env := decodeDoc(t, "stderr", stderr)
	if e, _ := env["error"].(map[string]any); e["type"] != output.ErrorTypeNotFound {
		t.Errorf("want the not_found envelope, got %v", env)
	}
}

// TestTSV pins -o tsv without --fields (#241): the table's own columns,
// unstyled, a missing value empty rather than a dash, and a value's tab or
// line break escaped so each row stays one line. An object a table shows as
// field and value prints that way too.
func TestTSV(t *testing.T) {
	t.Run("a list is the table's columns", func(t *testing.T) {
		stdout, stderr, code := runFormatting(t, map[string]reply{"GET /core/v1/domains/example.com/records": {200,
			`{"records":[{"id":7,"domainName":"example.com","host":"","fqdn":"example.com.","type":"TXT","answer":"a\tb\nc\\d","ttl":300}],"totalCount":1}`}},
			"dns", "list", "example.com", "-o", "tsv")
		if code != 0 {
			t.Fatalf("exit %d, stderr:\n%s", code, stderr)
		}
		lines := strings.Split(strings.TrimSuffix(stdout, "\n"), "\n")
		if len(lines) != 2 {
			t.Fatalf("want a header and one row, got:\n%s", stdout)
		}
		if !strings.HasPrefix(lines[0], "ID\t") {
			t.Errorf("want the table's header, got %q", lines[0])
		}
		if !strings.Contains(lines[1], `a\tb\nc\\d`) {
			t.Errorf("want the answer escaped, got %q", lines[1])
		}
		if strings.Contains(stdout, output.None) || strings.Contains(stdout, "\x1b[") {
			t.Errorf("no dashes or escape codes in TSV:\n%q", stdout)
		}
	})
	t.Run("--no-header", func(t *testing.T) {
		stdout, _, code := runFormatting(t, map[string]reply{"GET /core/v1/domains": {200, formattingDomains}},
			"domain", "list", "-o", "tsv", "--no-header")
		if code != 0 || !strings.HasPrefix(stdout, "a.com\t") {
			t.Errorf("exit %d, want rows only:\n%s", code, stdout)
		}
		if !strings.Contains(stdout, "2030-01-02\t") {
			t.Errorf("want the expiry as a plain date:\n%s", stdout)
		}
	})
	t.Run("an object is field and value rows", func(t *testing.T) {
		stdout, stderr, code := runFormatting(t, map[string]reply{"GET /core/v1/domains/a.com": {200, `{"domainName":"a.com","locked":true}`}},
			"domain", "get", "a.com", "-o", "tsv")
		if code != 0 {
			t.Fatalf("exit %d, stderr:\n%s", code, stderr)
		}
		for line := range strings.SplitSeq(strings.TrimSuffix(stdout, "\n"), "\n") {
			if strings.Count(line, "\t") != 1 {
				t.Errorf("want field<TAB>value, got %q in:\n%s", line, stdout)
			}
		}
	})
	t.Run("a write result", func(t *testing.T) {
		stdout, _, code := runFormatting(t, map[string]reply{"GET /core/v1/domains/a.com": {200, `{"domainName":"a.com","locked":true}`}},
			"domain", "lock", "on", "a.com", "--yes", "-o", "tsv")
		if code != 0 || !strings.Contains(stdout, "changed\tfalse\n") {
			t.Errorf("exit %d, want changed<TAB>false:\n%s", code, stdout)
		}
	})
	t.Run("a dry run", func(t *testing.T) {
		stdout, _, code := runFormatting(t, nil,
			"dns", "create", "example.com", "--type", "A", "--host", "www", "--answer", "192.0.2.1", "--dry-run", "-o", "tsv")
		want := "method\tpath\tbody\nPOST\t/core/v1/domains/example.com/records\t"
		if code != 0 || !strings.HasPrefix(stdout, want) {
			t.Errorf("exit %d, want %q…, got:\n%s", code, want, stdout)
		}
	})
	t.Run("-q still wins", func(t *testing.T) {
		stdout, _, code := runFormatting(t, map[string]reply{"GET /core/v1/domains": {200, formattingDomains}},
			"domain", "list", "-o", "tsv", "-q")
		if code != 0 || stdout != "a.com\nb.com\n" {
			t.Errorf("exit %d, want one domain per line, got:\n%s", code, stdout)
		}
	})
}
