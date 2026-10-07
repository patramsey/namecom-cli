package dns

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/patramsey/namecom-cli/cmd/cmdutil"
	"github.com/patramsey/namecom-cli/internal/output"
)

// The API stores a TXT value's double quotes escaped: `v=spf1 "quoted" ~all`
// is listed back as `v=spf1 \"quoted\" ~all` (#284).
const (
	storedTXT    = `v=spf1 \"quoted\" ~all`
	unescapedTXT = `v=spf1 "quoted" ~all`
	listRecords  = "GET /core/v1/domains/example.com/records"
)

func escapedTXTZone(t *testing.T) (*fakeZone, *httptest.Server) {
	t.Helper()
	return newFakeZone(t, fakeRecord{ID: 7, Host: "swtxt", Type: "TXT", Answer: storedTXT, TTL: 300})
}

// TestNormAnswer_TXTEscapedQuotes pins the comparison: the stored, escaped
// form matches the value as typed, as zone syntax spells it, and as a JSON
// export carries it.
func TestNormAnswer_TXTEscapedQuotes(t *testing.T) {
	want := normAnswer("TXT", storedTXT)
	for _, a := range []string{
		unescapedTXT,               // --answer, a zone file's parsed value, or a JSON answer
		storedTXT,                  // `dns export` JSON
		`"v=spf1 \"quoted\" ~all"`, // zone syntax in --answer
	} {
		if got := normAnswer("TXT", a); got != want {
			t.Errorf("normAnswer(TXT, %q) = %q, want %q", a, got, want)
		}
	}
	if got := normAnswer("TXT", `a\\b`); got != `a\b` {
		t.Errorf(`normAnswer(TXT, a\\b) = %q, want a\b`, got)
	}
	if got := normAnswer("TXT", `\"a\" \"b\"`); got != normAnswer("TXT", `"a" "b"`) {
		t.Errorf("stored quoted strings = %q, want them to match as sent", got)
	}
}

// TestDNSCreate_IfNotExistsEscapedTXT: --if-not-exists finds the stored
// record rather than POSTing a duplicate the API answers with a 500.
func TestDNSCreate_IfNotExistsEscapedTXT(t *testing.T) {
	z, srv := escapedTXTZone(t)
	cmd, captured := createFor(t, srv, runOpts{yes: true},
		"--type", "TXT", "--host", "swtxt", "--answer", unescapedTXT, "--if-not-exists")
	if err := runCreate(cmd, []string{"example.com"}); err != nil {
		t.Fatalf("runCreate: %v", err)
	}
	if stdout, _ := captured(); !strings.Contains(stdout, "already exists on example.com (id 7)") {
		t.Errorf("stdout = %q", stdout)
	}
	if got, want := z.requestLog(), []string{listRecords}; !reflect.DeepEqual(got, want) {
		t.Errorf("requests = %q, want %q", got, want)
	}
}

// TestDNSSync_EscapedTXTIsUnchanged: a zone line in BIND spelling and a JSON
// answer both match the stored record, so sync sends nothing.
func TestDNSSync_EscapedTXTIsUnchanged(t *testing.T) {
	files := map[string]string{
		"q.zone":   `swtxt.example.com. 300 IN TXT "v=spf1 \"quoted\" ~all"` + "\n",
		"q.json":   `[{"type":"TXT","host":"swtxt","answer":"v=spf1 \"quoted\" ~all","ttl":300}]`,
		"esc.json": `[{"type":"TXT","host":"swtxt","answer":"v=spf1 \\\"quoted\\\" ~all","ttl":300}]`,
		"esc.zone": `swtxt.example.com. 300 IN TXT "v=spf1 \\\"quoted\\\" ~all"` + "\n",
	}
	for name, content := range files {
		t.Run(name, func(t *testing.T) {
			z, srv := escapedTXTZone(t)
			stdout, _, err := runSyncFile(t, srv, runOpts{yes: true, format: output.FormatJSON}, writeFile(t, name, content), true, false)
			if err != nil {
				t.Fatalf("sync: %v", err)
			}
			if !strings.Contains(stdout, `"changed": false`) {
				t.Errorf("stdout = %s", stdout)
			}
			if got, want := z.requestLog(), []string{listRecords}; !reflect.DeepEqual(got, want) {
				t.Errorf("requests = %q, want %q", got, want)
			}
		})
	}
}

// TestDNSImport_SkipExistingEscapedTXT: --skip-existing skips the record.
func TestDNSImport_SkipExistingEscapedTXT(t *testing.T) {
	z, srv := escapedTXTZone(t)
	file := writeFile(t, "q.zone", `swtxt.example.com. 300 IN TXT "v=spf1 \"quoted\" ~all"`+"\n")
	stdout, _, err := runImportFile(t, srv, runOpts{}, file, true)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if !strings.Contains(stdout, "Imported 0 records to example.com (1 already present, skipped)") {
		t.Errorf("stdout = %q", stdout)
	}
	if got, want := z.requestLog(), []string{listRecords}; !reflect.DeepEqual(got, want) {
		t.Errorf("requests = %q, want %q", got, want)
	}
}

// The API stores a multi-string TXT value with the strings run together:
// `--answer '"a" "b"'` is listed back as `"a""b"`.
const storedSplitTXT = `"a""b"`

func splitTXTZone(t *testing.T) (*fakeZone, *httptest.Server) {
	t.Helper()
	return newFakeZone(t, fakeRecord{ID: 8, Host: "imptxt2", Type: "TXT", Answer: storedSplitTXT, TTL: 300})
}

// TestNormAnswer_TXTSplitStrings: the stored form, the strings as typed or
// in a zone file, and their concatenation all compare equal.
func TestNormAnswer_TXTSplitStrings(t *testing.T) {
	want := normAnswer("TXT", storedSplitTXT)
	for _, a := range []string{`"a" "b"`, "\"a\"\t\"b\"", `ab`, `"ab"`, `\"a\"\"b\"`} {
		if got := normAnswer("TXT", a); got != want {
			t.Errorf("normAnswer(TXT, %q) = %q, want %q", a, got, want)
		}
	}
	if got := normAnswer("TXT", `"a""b`); got == want {
		t.Errorf("an unterminated string matched: %q", got)
	}
}

// TestDNSCreate_IfNotExistsSplitTXT: --if-not-exists finds the stored record
// rather than POSTing again, which the API answered with a 500 (exit 6).
func TestDNSCreate_IfNotExistsSplitTXT(t *testing.T) {
	z, srv := splitTXTZone(t)
	cmd, captured := createFor(t, srv, runOpts{yes: true},
		"--type", "TXT", "--host", "imptxt2", "--answer", `"a" "b"`, "--if-not-exists")
	if err := runCreate(cmd, []string{"example.com"}); err != nil {
		t.Fatalf("runCreate: %v", err)
	}
	if stdout, _ := captured(); !strings.Contains(stdout, "already exists on example.com (id 8)") {
		t.Errorf("stdout = %q", stdout)
	}
	if got, want := z.requestLog(), []string{listRecords}; !reflect.DeepEqual(got, want) {
		t.Errorf("requests = %q, want %q", got, want)
	}
}

// TestDNSSync_SplitTXTIsUnchanged: a zone line `"a" "b"` and a JSON answer in
// either spelling match the stored record, so sync plans nothing. It planned
// a second record and kept the stored one beside it.
func TestDNSSync_SplitTXTIsUnchanged(t *testing.T) {
	files := map[string]string{
		"split.zone":  `imptxt2.example.com. 300 IN TXT "a" "b"` + "\n",
		"split.json":  `[{"type":"TXT","host":"imptxt2","answer":"\"a\" \"b\"","ttl":300}]`,
		"stored.json": `[{"type":"TXT","host":"imptxt2","answer":"\"a\"\"b\"","ttl":300}]`,
	}
	for name, content := range files {
		t.Run(name, func(t *testing.T) {
			z, srv := splitTXTZone(t)
			stdout, _, err := runSyncFile(t, srv, runOpts{yes: true, format: output.FormatJSON}, writeFile(t, name, content), true, false)
			if err != nil {
				t.Fatalf("sync: %v", err)
			}
			if !strings.Contains(stdout, `"changed": false`) {
				t.Errorf("stdout = %s", stdout)
			}
			if got, want := z.requestLog(), []string{listRecords}; !reflect.DeepEqual(got, want) {
				t.Errorf("requests = %q, want %q", got, want)
			}
		})
	}
}

// TestDNSImport_SkipExistingSplitTXT: --skip-existing skips the record.
func TestDNSImport_SkipExistingSplitTXT(t *testing.T) {
	z, srv := splitTXTZone(t)
	file := writeFile(t, "split.zone", `imptxt2.example.com. 300 IN TXT "a" "b"`+"\n")
	stdout, _, err := runImportFile(t, srv, runOpts{}, file, true)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if !strings.Contains(stdout, "Imported 0 records to example.com (1 already present, skipped)") {
		t.Errorf("stdout = %q", stdout)
	}
	if got, want := z.requestLog(), []string{listRecords}; !reflect.DeepEqual(got, want) {
		t.Errorf("requests = %q, want %q", got, want)
	}
}

// TestDNSCreate_SuccessShowsStoredAnswer: the success line printed the answer
// as typed, `"a" "b"`, where the API stored `"a""b"`.
func TestDNSCreate_SuccessShowsStoredAnswer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":13546100,"type":"TXT","host":"imptxt2","answer":"\"a\"\"b\"","ttl":300}`))
	}))
	t.Cleanup(srv.Close)
	cmd, captured := createFor(t, srv, runOpts{yes: true}, "--type", "TXT", "--host", "imptxt2", "--answer", `"a" "b"`)
	if err := runCreate(cmd, []string{"example.com"}); err != nil {
		t.Fatalf("runCreate: %v", err)
	}
	if stdout, _ := captured(); stdout != "✓ Created TXT imptxt2.example.com → \"a\"\"b\" (id 13546100)\n" {
		t.Errorf("stdout = %q", stdout)
	}
}

// TestReadRecordsFile_YAML: `dns export -o yaml` is not read back, and the
// error says so rather than calling the file a zone file.
func TestReadRecordsFile_YAML(t *testing.T) {
	const yaml = "data:\n- answer: 192.0.2.1\n  host: www\n  ttl: 300\n  type: A\n"
	for _, name := range []string{"d1.yaml", "d1.yml", "d1.txt"} {
		_, _, err := readRecordsFile(writeFile(t, name, yaml), "example.com")
		var ue *cmdutil.UsageError
		if !errors.As(err, &ue) || !strings.Contains(err.Error(), "is YAML, which this command does not read") || strings.Contains(err.Error(), "zone file:") {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	_, _, err := readRecordsFile(writeFile(t, "bad.zone", "www 300 IN\n"), "example.com")
	if err == nil || !strings.Contains(err.Error(), "is not JSON, so it was read as a zone file: line 1") {
		t.Errorf("zone: err = %v", err)
	}
}
