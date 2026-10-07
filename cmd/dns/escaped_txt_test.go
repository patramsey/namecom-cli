package dns

import (
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

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
