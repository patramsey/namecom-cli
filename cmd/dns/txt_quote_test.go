package dns

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/patramsey/namecom-cli/cmd/cmdutil"
	"github.com/patramsey/namecom-cli/internal/drifttest"
	"github.com/patramsey/namecom-cli/internal/output"
	"github.com/spf13/cobra"
)

// txtSendCases are TXT values as typed, and the answer sent for each (#360).
// The API refuses a value that starts or ends with `"`, or ends with `\`,
// and accepts the same value quoted and escaped; every other value is sent
// as it is.
var txtSendCases = []struct{ name, in, sent string }{
	{"ends with a quote", `say "x"`, `"say \"x\""`},
	{"starts with a quote", `"x" said`, `"\"x\" said"`},
	{"ends with a backslash", `trailing\`, `"trailing\\"`},
	{"unbalanced quotes", `"a"b"`, `"\"a\"b\""`},
	{"inner quotes are sent as typed", `v=spf1 "quoted" ~all`, `v=spf1 "quoted" ~all`},
	{"inner backslash is sent as typed", `a\b`, `a\b`},
	{"quoted strings are zone syntax", `"a" "b"`, `"a" "b"`},
	{"an escaped quoted value is sent as typed", `"say \"x\""`, `"say \"x\""`},
	{"plain", `v=spf1 -all`, `v=spf1 -all`},
}

func TestAPITXT(t *testing.T) {
	for _, tc := range txtSendCases {
		if got := apiTXT(tc.in); got != tc.sent {
			t.Errorf("%s: apiTXT(%q) = %q, want %q", tc.name, tc.in, got, tc.sent)
		}
		if apiTXTRefuses(tc.sent) {
			t.Errorf("%s: %q is sent, which the API refuses", tc.name, tc.sent)
		}
		// The value sent is the same record as the value typed.
		if normAnswer("TXT", tc.sent) != normAnswer("TXT", tc.in) {
			t.Errorf("%s: %q and %q do not match", tc.name, tc.sent, tc.in)
		}
	}
	long := strings.Repeat("k", 300) + `"`
	if got, want := apiTXT(long), `"`+strings.Repeat("k", 255)+`" "`+strings.Repeat("k", 45)+`\""`; got != want {
		t.Errorf("a long value is split at 255 bytes: got %q", got)
	}
}

// TestRequestShape_DNSTXTQuoting pins the wire body of create and update for
// each value, and that --dry-run previews that body.
func TestRequestShape_DNSTXTQuoting(t *testing.T) {
	const stub = `{"id":42,"domainName":"example.com","host":"t","type":"TXT","answer":"old","ttl":300}`
	for _, tc := range txtSendCases {
		answer, _ := json.Marshal(tc.sent)
		t.Run("create "+tc.name, func(t *testing.T) {
			build := func(t *testing.T, srv *httptest.Server) *cobra.Command {
				cmd := cmdForCreate(t, srv)
				if err := cmd.ParseFlags([]string{"--type", "TXT", "--host", "t", "--answer", tc.in}); err != nil {
					t.Fatalf("ParseFlags: %v", err)
				}
				return cmd
			}
			drifttest.AssertRequest(t, drifttest.Request{
				Method: "POST", Path: "/core/v1/domains/example.com/records",
				Body: fmt.Sprintf(`{"answer":%s,"host":"t","ttl":300,"type":"TXT"}`, answer),
			}, build, runCreate, []string{"example.com"}, stub)
			drifttest.AssertDryRunBodyMatches(t, build, runCreate, []string{"example.com"}, stub)
		})
		t.Run("update "+tc.name, func(t *testing.T) {
			build := func(t *testing.T, srv *httptest.Server) *cobra.Command {
				cmd := cmdForUpdate(t, srv)
				if err := cmd.ParseFlags([]string{"--answer", tc.in}); err != nil {
					t.Fatalf("ParseFlags: %v", err)
				}
				return cmd
			}
			drifttest.AssertRequest(t, drifttest.Request{
				Method: "PUT", Path: "/core/v1/domains/example.com/records/42",
				Body: fmt.Sprintf(`{"answer":%s,"host":"t","ttl":300,"type":"TXT"}`, answer),
			}, build, runUpdate, []string{"example.com", "42"}, stub)
			drifttest.AssertDryRunBodyMatches(t, build, runUpdate, []string{"example.com", "42"}, stub)
		})
	}
}

// txtFileCases are a zone line and a JSON record for one value each, and the
// answer sent for it. A zone file's quotes are read by the parser, so its
// value `"a" "b"` (written "\"a\" \"b\"") is content, and quoted again.
var txtFileCases = []struct{ name, zone, json, sent string }{
	{"ends with a quote", `t 300 IN TXT "say \"x\""`, `"say \"x\""`, `"say \"x\""`},
	{"starts with a quote", `t 300 IN TXT "\"x\" said"`, `"\"x\" said"`, `"\"x\" said"`},
	{"ends with a backslash", `t 300 IN TXT "trailing\\"`, `"trailing\\"`, `"trailing\\"`},
	{"inner quotes", `t 300 IN TXT "v=spf1 \"quoted\" ~all"`, `"v=spf1 \"quoted\" ~all"`, `v=spf1 "quoted" ~all`},
	{"a value that looks quoted", `t 300 IN TXT "\"a\" \"b\""`, `"\"\\\"a\\\" \\\"b\\\"\""`, `"\"a\" \"b\""`},
}

// TestDNSImportSync_TXTQuoting: import and sync send the quoted form for the
// values the API refuses unquoted, from a zone file and from JSON, and the
// dry run previews the bodies sent.
func TestDNSImportSync_TXTQuoting(t *testing.T) {
	for _, tc := range txtFileCases {
		for _, f := range []struct{ name, content string }{
			{"q.zone", "$ORIGIN example.com.\n" + tc.zone + "\n"},
			{"q.json", `[{"type":"TXT","host":"t","answer":` + tc.json + `,"ttl":300}]`},
		} {
			want := []string{`POST /core/v1/domains/example.com/records {"answer":` + jsonString(tc.sent) + `,"host":"t","ttl":300,"type":"TXT"}`}
			t.Run("import "+tc.name+" "+f.name, func(t *testing.T) {
				z, srv := newFakeZone(t)
				z.txtAsAPI = true
				file := writeFile(t, f.name, f.content)
				dry, _, err := runImportFile(t, srv, runOpts{dryRun: true, format: output.FormatJSON}, file, false)
				if err != nil {
					t.Fatalf("import --dry-run: %v", err)
				}
				if _, _, err := runImportFile(t, srv, runOpts{}, file, false); err != nil {
					t.Fatalf("import: %v", err)
				}
				if got := z.sentLog(); !reflect.DeepEqual(got, want) {
					t.Errorf("sent %q, want %q", got, want)
				}
				if previewed := previewBodies(t, dry); !reflect.DeepEqual(previewed, []string{`{"answer":` + jsonString(tc.sent) + `,"host":"t","ttl":300,"type":"TXT"}`}) {
					t.Errorf("previewed %q", previewed)
				}
			})
			t.Run("sync "+tc.name+" "+f.name, func(t *testing.T) {
				z, srv := newFakeZone(t)
				z.txtAsAPI = true
				file := writeFile(t, f.name, f.content)
				dry, _, err := runSyncFile(t, srv, runOpts{dryRun: true, format: output.FormatJSON}, file, false, false)
				if err != nil {
					t.Fatalf("sync --dry-run: %v", err)
				}
				if _, _, err := runSyncFile(t, srv, runOpts{yes: true}, file, false, false); err != nil {
					t.Fatalf("sync: %v", err)
				}
				if got := z.sentLog(); !reflect.DeepEqual(got, want) {
					t.Errorf("sent %q, want %q", got, want)
				}
				if previewed := previewBodies(t, dry); !reflect.DeepEqual(previewed, []string{`{"answer":` + jsonString(tc.sent) + `,"host":"t","ttl":300,"type":"TXT"}`}) {
					t.Errorf("previewed %q", previewed)
				}
				// Synced again, the file matches the record the API stored.
				before := len(z.sentLog())
				if _, _, err := runSyncFile(t, srv, runOpts{yes: true}, file, false, false); err != nil {
					t.Fatalf("second sync: %v", err)
				}
				if got := z.sentLog()[before:]; len(got) != 0 {
					t.Errorf("a second sync sent %q", got)
				}
			})
		}
	}
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// previewBodies is the bodies of a JSON dry run's requests, canonicalized.
func previewBodies(t *testing.T, dry string) []string {
	t.Helper()
	var doc struct {
		Data []struct {
			Body json.RawMessage `json:"body"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(dry), &doc); err != nil {
		t.Fatalf("dry run is not JSON: %v\n%s", err, dry)
	}
	var out []string
	for _, d := range doc.Data {
		out = append(out, canonical(string(d.Body)))
	}
	return out
}

// TestDNSCreate_TXTQuotedThenFound: a value created quoted is found again by
// --if-not-exists in the spelling it was typed in, and by sync.
func TestDNSCreate_TXTQuotedThenFound(t *testing.T) {
	for _, tc := range txtSendCases[:4] {
		t.Run(tc.name, func(t *testing.T) {
			z, srv := newFakeZone(t)
			z.txtAsAPI = true
			cmd, _ := createFor(t, srv, runOpts{yes: true}, "--type", "TXT", "--host", "t", "--answer", tc.in)
			if err := runCreate(cmd, []string{"example.com"}); err != nil {
				t.Fatalf("create: %v", err)
			}
			cmd, captured := createFor(t, srv, runOpts{yes: true}, "--type", "TXT", "--host", "t", "--answer", tc.in, "--if-not-exists")
			if err := runCreate(cmd, []string{"example.com"}); err != nil {
				t.Fatalf("create --if-not-exists: %v", err)
			}
			if stdout, _ := captured(); !strings.Contains(stdout, "already exists") {
				t.Errorf("--if-not-exists did not find the record: %q", stdout)
			}
			if got := z.writeLog(); !reflect.DeepEqual(got, []string{"POST"}) {
				t.Errorf("writes = %q, want one POST", got)
			}
		})
	}
}

// exportZoneOf runs `dns export --zone` against z and returns the zone.
func exportZoneOf(t *testing.T, srv *httptest.Server) (stdout, stderr string) {
	t.Helper()
	cmd, out, errOut := commandFor(t, srv, runOpts{})
	exportZone = true
	t.Cleanup(func() { exportZone = false })
	if err := runExport(cmd, []string{"example.com"}); err != nil {
		t.Fatalf("export: %v", err)
	}
	return out.String(), errOut.String()
}

// txtExportCases are TXT answers as the API stores them, and the zone-file
// rdata of the value each means (#360): the API's `\"` storage escape is
// undone, and a value stored quoted loses that one layer of quotes, before
// the value is escaped for the zone.
var txtExportCases = []struct{ name, stored, rdata, value string }{
	{"stored escape", `v=spf1 \"quoted\" ~all`, `"v=spf1 \"quoted\" ~all"`, `v=spf1 "quoted" ~all`},
	{"stored quoted", `"say \"x\""`, `"say \"x\""`, `say "x"`},
	{"stored quoted, leading quote", `"\"x\" said"`, `"\"x\" said"`, `"x" said`},
	{"stored quoted backslash", `"trailing\\"`, `"trailing\\"`, `trailing\`},
	{"run-together strings", `"a""b"`, `"a" "b"`, `ab`},
	{"plain", `v=spf1 -all`, `"v=spf1 -all"`, `v=spf1 -all`},
	{"plain backslash", `a\b`, `"a\\b"`, `a\b`},
}

// TestDNSExport_ZoneTXTIsTheValue: the zone export writes the value each
// record means, the zone parser reads it back as that value, and the export
// synced straight back plans nothing. The JSON export keeps what the API
// holds.
func TestDNSExport_ZoneTXTIsTheValue(t *testing.T) {
	var recs []fakeRecord
	for i, tc := range txtExportCases {
		recs = append(recs, fakeRecord{ID: i + 1, Host: fmt.Sprintf("t%d", i), Type: "TXT", Answer: tc.stored, TTL: 300})
	}
	z, srv := newFakeZone(t, recs...)
	z.txtAsAPI = true
	zone, _ := exportZoneOf(t, srv)

	lines := strings.Split(strings.TrimSuffix(zone, "\n"), "\n")
	if len(lines) != len(txtExportCases) {
		t.Fatalf("export has %d lines, want %d:\n%s", len(lines), len(txtExportCases), zone)
	}
	parsed, err := parseZone(zone, "example.com")
	if err != nil {
		t.Fatalf("the export does not parse: %v\n%s", err, zone)
	}
	for i, tc := range txtExportCases {
		if want := "\tTXT\t" + tc.rdata; !strings.HasSuffix(lines[i], want) {
			t.Errorf("%s: line %q, want it to end %q", tc.name, lines[i], want)
		}
		// The parser's value, before it is quoted again for the API.
		var got string
		for _, s := range mustCharStrings(t, strings.SplitN(lines[i], "\t", 5)[4]) {
			got += s
		}
		if got != tc.value {
			t.Errorf("%s: the zone reads back as %q, want %q", tc.name, got, tc.value)
		}
		if parsed[i].Host != fmt.Sprintf("t%d", i) {
			t.Errorf("%s: host %q", tc.name, parsed[i].Host)
		}
	}

	file := writeFile(t, "example.com.zone", zone)
	before := len(z.requestLog())
	stdout, _, err := runSyncFile(t, srv, runOpts{yes: true, format: output.FormatJSON}, file, true, false)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if !strings.Contains(stdout, `"changed": false`) {
		t.Errorf("syncing the export back changed something:\n%s", stdout)
	}
	if got := z.requestLog()[before:]; !reflect.DeepEqual(got, []string{listRecords}) {
		t.Errorf("requests = %q, want the list alone", got)
	}

	// JSON keeps the API's value.
	exportZone = false
	cmd, out, _ := commandFor(t, srv, runOpts{format: output.FormatJSON})
	if err := runExport(cmd, []string{"example.com"}); err != nil {
		t.Fatalf("JSON export: %v", err)
	}
	if !strings.Contains(out.String(), `"answer": "v=spf1 \\\"quoted\\\" ~all"`) {
		t.Errorf("the JSON export does not carry the stored value:\n%s", out.String())
	}
}

func mustCharStrings(t *testing.T, rdata string) []string {
	t.Helper()
	parts, err := parseCharStrings(rdata, false)
	if err != nil {
		t.Fatalf("rdata %q: %v", rdata, err)
	}
	return parts
}

// TestDNSExport_ZoneTXTLoads checks the exported TXT values with BIND's
// named-checkzone, where it is installed: the zone loads, and holds each
// value the record means.
func TestDNSExport_ZoneTXTLoads(t *testing.T) {
	checkzone, err := exec.LookPath("named-checkzone")
	if err != nil {
		t.Skip("named-checkzone not installed")
	}
	var recs []fakeRecord
	for i, tc := range txtExportCases {
		recs = append(recs, fakeRecord{ID: i + 1, Host: fmt.Sprintf("t%d", i), Type: "TXT", Answer: tc.stored, TTL: 300})
	}
	_, srv := newFakeZone(t, recs...)
	exported, _ := exportZoneOf(t, srv)
	zone := "$ORIGIN example.com.\n" +
		"@\t300\tIN\tSOA\tns1.example.com. hostmaster.example.com. 1 3600 600 86400 300\n" +
		"@\t300\tIN\tNS\tns1.example.com.\n" +
		"ns1\t300\tIN\tA\t192.0.2.1\n" + exported
	path := filepath.Join(t.TempDir(), "example.com.zone")
	if err := os.WriteFile(path, []byte(zone), 0o600); err != nil {
		t.Fatalf("writing zone: %v", err)
	}
	dump, err := exec.Command(checkzone, "-q", "-D", "-o", "-", "example.com", path).CombinedOutput() //nolint:gosec
	if err != nil {
		t.Fatalf("named-checkzone rejected the zone: %v\n%s\nzone:\n%s", err, dump, zone)
	}
	for _, tc := range txtExportCases {
		if !strings.Contains(string(dump), tc.rdata) {
			t.Errorf("%s: loaded zone lacks %s:\n%s", tc.name, tc.rdata, dump)
		}
	}
}

// TestDNSExport_ZoneEmpty: an empty zone says so on stderr in a terminal,
// and writes nothing to the zone file.
func TestDNSExport_ZoneEmpty(t *testing.T) {
	_, srv := newFakeZone(t)
	stdout, stderr := exportZoneOf(t, srv)
	if stdout != "" {
		t.Errorf("stdout = %q, want an empty zone", stdout)
	}
	if !strings.Contains(stderr, "No DNS records found.") {
		t.Errorf("stderr = %q", stderr)
	}
}

// TestValidDNSAnswer_TXTNonASCII: the API takes only ASCII in a TXT value,
// so a Unicode one is refused before anything is sent — by create, and by a
// sync dry run, which planned a create certain to fail.
func TestValidDNSAnswer_TXTNonASCII(t *testing.T) {
	for _, v := range []string{"café", "ok 👍"} {
		cmd, _ := createFor(t, neverCalledServer(t), runOpts{yes: true}, "--type", "TXT", "--answer", v)
		err := runCreate(cmd, []string{"example.com"})
		var ue *cmdutil.UsageError
		if !errors.As(err, &ue) || !strings.Contains(err.Error(), "--answer must be ASCII") {
			t.Errorf("create %q: err = %v", v, err)
		}
	}
	file := writeFile(t, "q.zone", "$ORIGIN example.com.\nt 300 IN TXT \"café\"\n")
	_, _, err := runSyncFile(t, neverCalledServer(t), runOpts{dryRun: true}, file, false, false)
	var ue *cmdutil.UsageError
	if !errors.As(err, &ue) || !strings.Contains(err.Error(), `line 2 (TXT t): TXT record answer must be ASCII: "é" (byte 4)`) {
		t.Errorf("sync: err = %v", err)
	}
}

// TestPrepareRecords_NamesFields: a record read from a file is named by its
// line or index, and its fields as the file has them — not as the flags of
// `dns create`, which import and sync do not have.
func TestPrepareRecords_NamesFields(t *testing.T) {
	cases := []struct {
		name string
		rec  inputRecord
		want string
	}{
		{"bad address", inputRecord{Source: "record 1", Type: "A", Host: "www", Answer: "x", TTL: 300},
			`record 1 (A www): answer must be a valid IPv4 address for A records, got "x"`},
		{"CAA", inputRecord{Source: "line 3", Type: "CAA", Host: "@", Answer: `0 issue "ca.example"`, TTL: 300},
			"line 3 (CAA @): name.com's API does not accept CAA records — type must be one of"},
		{"bad host", inputRecord{Source: "record 2", Type: "A", Host: "a b", Answer: "192.0.2.1", TTL: 300},
			`record 2 (A a b): host "a b" must not contain spaces`},
		{"control character", inputRecord{Source: "line 4", Type: "TXT", Host: "a\ab", Answer: "x", TTL: 300},
			`line 4 (TXT "a\ab"): host "a\ab" must not contain "\a"`},
		{"ttl", inputRecord{Source: "record 5", Type: "A", Host: "www", Answer: "192.0.2.1", TTL: 60},
			"record 5 (A www): ttl must be at least 300 seconds (got 60)"},
		{"priority", inputRecord{Source: "record 6", Type: "MX", Host: "@", Answer: "mx.example.com", TTL: 300, Priority: prio(70000)},
			"record 6 (MX @): priority must be between 0 and 65535 (got 70000)"},
		{"mx with a priority in it", inputRecord{Source: "record 7", Type: "MX", Host: "@", Answer: "10 mx.example.com", TTL: 300},
			"MX record answer must be a hostname (got \"10 mx.example.com\") — set priority with priority"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := prepareRecords([]inputRecord{tc.rec}, false)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v\nwant it to contain %s", err, tc.want)
			}
			if err != nil && strings.Contains(err.Error(), "--") {
				t.Errorf("err names a flag: %v", err)
			}
		})
	}
}

// TestDNSImport_ResultListsCreated: the import result keeps its keys and adds
// the records created, with their IDs, which -q prints one per line.
func TestDNSImport_ResultListsCreated(t *testing.T) {
	const records = `[{"type":"A","host":"www","answer":"192.0.2.1","ttl":300},{"type":"A","host":"api","answer":"192.0.2.2","ttl":300}]`

	t.Run("json", func(t *testing.T) {
		_, srv := newFakeZone(t, fakeRecord{ID: 1, Host: "www", Type: "A", Answer: "192.0.2.1", TTL: 300})
		stdout, _, err := runImportFile(t, srv, runOpts{format: output.FormatJSON}, writeFile(t, "r.json", records), true)
		if err != nil {
			t.Fatalf("import: %v", err)
		}
		var res struct {
			Success, Changed bool
			Message          string
			Skipped          int
			Created          []planRecord
		}
		if err := json.Unmarshal([]byte(stdout), &res); err != nil {
			t.Fatalf("not JSON: %v\n%s", err, stdout)
		}
		if !res.Success || !res.Changed || res.Skipped != 1 || res.Message != "Imported 1 record to example.com (1 already present, skipped)" {
			t.Errorf("result = %+v", res)
		}
		if len(res.Created) != 1 || res.Created[0].ID != 100 || res.Created[0].Host != "api" || res.Created[0].Answer != "192.0.2.2" {
			t.Errorf("created = %+v", res.Created)
		}
	})
	t.Run("quiet", func(t *testing.T) {
		_, srv := newFakeZone(t)
		cmd, out, _ := commandFor(t, srv, runOpts{})
		cmdutil.Out(cmd).QuietMode = true
		importFile = writeFile(t, "r.json", records)
		t.Cleanup(func() { importFile = "" })
		if err := runImport(cmd, []string{"example.com"}); err != nil {
			t.Fatalf("import: %v", err)
		}
		if got := out.String(); got != "100\n101\n" {
			t.Errorf("-q printed %q, want the new IDs", got)
		}
	})
	t.Run("nothing created", func(t *testing.T) {
		_, srv := newFakeZone(t, fakeRecord{ID: 1, Host: "www", Type: "A", Answer: "192.0.2.1", TTL: 300})
		stdout, _, err := runImportFile(t, srv, runOpts{format: output.FormatJSON}, writeFile(t, "r.json", records[:strings.Index(records, ",{")]+"]"), true)
		if err != nil {
			t.Fatalf("import: %v", err)
		}
		if !strings.Contains(stdout, `"changed": false`) || !strings.Contains(stdout, `"created": []`) {
			t.Errorf("stdout = %s", stdout)
		}
	})
}

// TestDNSCreate_IfNotExistsHintNamesTheFlags: the suggested update carries
// the flags that make it, where it failed "nothing to update".
func TestDNSCreate_IfNotExistsHintNamesTheFlags(t *testing.T) {
	_, srv := newFakeZone(t, fakeRecord{ID: 9, Host: "", Type: "MX", Answer: "mx.example.com", TTL: 300, Priority: prio(10)})
	cmd, captured := createFor(t, srv, runOpts{yes: true},
		"--type", "MX", "--answer", "mx.example.com", "--ttl", "600", "--priority", "20", "--if-not-exists")
	if err := runCreate(cmd, []string{"example.com"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, stderr := captured(); !strings.Contains(stderr, "run 'namecom dns update example.com 9 --ttl 600 --priority 20' to change it") {
		t.Errorf("stderr = %q", stderr)
	}
}

// TestDNSDelete_IfExistsKeepsArgumentOrder: the result lists each ID where
// it was given; the skipped one came last.
func TestDNSDelete_IfExistsKeepsArgumentOrder(t *testing.T) {
	_, srv := newFakeZone(t,
		fakeRecord{ID: 1, Host: "a", Type: "A", Answer: "192.0.2.1", TTL: 300},
		fakeRecord{ID: 3, Host: "c", Type: "A", Answer: "192.0.2.3", TTL: 300})
	cmd, out, _ := commandFor(t, srv, runOpts{yes: true, format: output.FormatJSON})
	deleteIfExists = true
	t.Cleanup(func() { deleteIfExists = false })
	if err := runDelete(cmd, []string{"example.com", "1", "2", "3"}); err != nil {
		t.Fatalf("delete: %v", err)
	}
	var res struct {
		Data []struct {
			ID      int  `json:"id"`
			Changed bool `json:"changed"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out.Bytes(), &res); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	var got []string
	for _, d := range res.Data {
		got = append(got, fmt.Sprintf("%d:%v", d.ID, d.Changed))
	}
	if want := []string{"1:true", "2:false", "3:true"}; !reflect.DeepEqual(got, want) {
		t.Errorf("items = %q, want %q", got, want)
	}
}
