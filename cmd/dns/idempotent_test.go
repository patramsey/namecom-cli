package dns

import (
	"encoding/json"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/patramsey/namecom-cli/cmd/cmdutil"
	"github.com/patramsey/namecom-cli/internal/output"
	"github.com/spf13/cobra"
)

func runImportFile(t *testing.T, srv *httptest.Server, o runOpts, file string, skip bool) (stdout, stderr string, err error) {
	t.Helper()
	cmd, out, errOut := commandFor(t, srv, o)
	importFile, importSkipExisting = file, skip
	t.Cleanup(func() { importFile, importSkipExisting = "", false })
	err = runImport(cmd, []string{"example.com"})
	return out.String(), errOut.String(), err
}

// TestDNSImport_SkipExisting pins --skip-existing: records already in the
// zone are skipped rather than failing the import, so an import that stopped
// partway can be run again — and running it a third time sends nothing.
func TestDNSImport_SkipExisting(t *testing.T) {
	z, srv := newFakeZone(t, fakeRecord{ID: 1, Host: "www", Type: "A", Answer: "192.0.2.1", TTL: 300})
	file := writeFile(t, "records.json", `[
	  {"type":"A","host":"www","answer":"192.0.2.1","ttl":300},
	  {"type":"CNAME","host":"docs","answer":"WWW.example.com.","ttl":300},
	  {"type":"A","host":"api","answer":"192.0.2.2","ttl":300}
	]`)

	t.Run("without it the import stops at the existing record", func(t *testing.T) {
		_, _, err := runImportFile(t, srv, runOpts{}, file, false)
		if err == nil || !strings.Contains(err.Error(), "already exists") {
			t.Errorf("want the API's refusal, got %v", err)
		}
	})

	t.Run("dry run previews only what is missing", func(t *testing.T) {
		z.writes = nil
		stdout, stderr, err := runImportFile(t, srv, runOpts{dryRun: true}, file, true)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Count(stdout, "POST ") != 2 || strings.Contains(stdout, `"host": "www"`) {
			t.Errorf("preview should list the two missing records only:\n%s", stdout)
		}
		if !strings.Contains(stderr, "Skipped 1 record already in the zone") {
			t.Errorf("stderr = %q", stderr)
		}
		if len(z.writeLog()) != 0 {
			t.Errorf("dry run wrote %q", z.writeLog())
		}
	})

	t.Run("skips what exists, creates the rest", func(t *testing.T) {
		stdout, _, err := runImportFile(t, srv, runOpts{}, file, true)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(stdout, "Imported 2 records to example.com (1 already present, skipped)") {
			t.Errorf("stdout = %q", stdout)
		}
	})

	t.Run("a re-run sends nothing", func(t *testing.T) {
		z.writes = nil
		stdout, _, err := runImportFile(t, srv, runOpts{}, file, true)
		if err != nil {
			t.Fatal(err)
		}
		if len(z.writeLog()) != 0 || !strings.Contains(stdout, "Imported 0 records to example.com (3 already present, skipped)") {
			t.Errorf("writes %q, stdout %q", z.writeLog(), stdout)
		}
	})
}

// TestDNSImport_PartialFailureSuggestsSkipExisting pins the retry advice: a
// re-run with --skip-existing continues without duplicating.
func TestDNSImport_PartialFailureSuggestsSkipExisting(t *testing.T) {
	z, srv := newFakeZone(t)
	z.fail = func(_ string, r fakeRecord) bool { return r.Host == "boom" }
	file := writeFile(t, "records.json", `[
	  {"type":"A","host":"one","answer":"192.0.2.1","ttl":300},
	  {"type":"A","host":"boom","answer":"192.0.2.2","ttl":300}
	]`)
	_, stderr, err := runImportFile(t, srv, runOpts{}, file, false)
	if err == nil || !strings.Contains(stderr, "re-run with --skip-existing") {
		t.Errorf("err %v, stderr %q", err, stderr)
	}
}

// TestDNSImport_ZoneFile pins zone-file input: what `dns export --zone`
// writes can be imported.
func TestDNSImport_ZoneFile(t *testing.T) {
	z, srv := newFakeZone(t)
	file := writeFile(t, "example.com.zone", `$ORIGIN example.com.
$TTL 600
@     IN MX  10 mail
mail  IN A   192.0.2.25
@     IN TXT "v=spf1 mx -all"
_sip._tcp SRV 5 10 5060 sip.example.net.
*     IN A   192.0.2.9
`)
	if _, _, err := runImportFile(t, srv, runOpts{}, file, false); err != nil {
		t.Fatal(err)
	}
	want := []string{
		`POST /core/v1/domains/example.com/records {"answer":"mail.example.com","host":"@","priority":10,"ttl":600,"type":"MX"}`,
		`POST /core/v1/domains/example.com/records {"answer":"192.0.2.25","host":"mail","ttl":600,"type":"A"}`,
		`POST /core/v1/domains/example.com/records {"answer":"v=spf1 mx -all","host":"@","ttl":600,"type":"TXT"}`,
		`POST /core/v1/domains/example.com/records {"answer":"10 5060 sip.example.net","host":"_sip._tcp","priority":5,"ttl":600,"type":"SRV"}`,
		`POST /core/v1/domains/example.com/records {"answer":"192.0.2.9","host":"*","ttl":600,"type":"A"}`,
	}
	if got := z.sentLog(); !reflect.DeepEqual(got, want) {
		t.Errorf("sent:\n  %q\nwant:\n  %q", got, want)
	}
}

// createFor is a `dns create` command wired to srv, with its flags parsed.
func createFor(t *testing.T, srv *httptest.Server, o runOpts, flags ...string) (*cobra.Command, func() (string, string)) {
	t.Helper()
	cmd, stdout, stderr := commandFor(t, srv, o)
	cmd.Flags().StringVar(&createType, "type", "", "")
	cmd.Flags().StringVar(&createHost, "host", "@", "")
	cmd.Flags().StringVar(&createAnswer, "answer", "", "")
	cmd.Flags().Int64Var(&createTTL, "ttl", 300, "")
	cmd.Flags().Int64Var(&createPriority, "priority", 0, "")
	cmd.Flags().BoolVar(&createIfNotExists, "if-not-exists", false, "")
	if err := cmd.ParseFlags(flags); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	t.Cleanup(func() { createIfNotExists = false })
	return cmd, func() (string, string) { return stdout.String(), stderr.String() }
}

// TestDNSCreate_IfNotExists pins --if-not-exists: an identical record (host,
// type and answer, compared as sync compares them) is reported by its ID with
// exit 0 instead of the API's "Record already exists".
func TestDNSCreate_IfNotExists(t *testing.T) {
	existing := fakeRecord{ID: 42, Host: "www", Type: "CNAME", Answer: "example.net", TTL: 300}

	t.Run("an existing record prints its ID", func(t *testing.T) {
		z, srv := newFakeZone(t, existing)
		cmd, _ := createFor(t, srv, runOpts{yes: true},
			"--type", "CNAME", "--host", "WWW", "--answer", "example.net.", "--if-not-exists")
		cmdutil.Out(cmd).QuietMode = true
		if err := runCreate(cmd, []string{"example.com"}); err != nil {
			t.Fatalf("runCreate: %v", err)
		}
		stdout := cmdutil.Out(cmd).Writer.(interface{ String() string }).String()
		if strings.TrimSpace(stdout) != "42" {
			t.Errorf("quiet output = %q, want the existing ID", stdout)
		}
		if len(z.writeLog()) != 0 {
			t.Errorf("wrote %q", z.writeLog())
		}
	})

	t.Run("JSON is the existing record, and a TTL difference is warned about", func(t *testing.T) {
		_, srv := newFakeZone(t, existing)
		cmd, captured := createFor(t, srv, runOpts{yes: true, format: output.FormatJSON},
			"--type", "CNAME", "--host", "www", "--answer", "example.net", "--ttl", "3600", "--if-not-exists")
		if err := runCreate(cmd, []string{"example.com"}); err != nil {
			t.Fatalf("runCreate: %v", err)
		}
		stdout, stderr := captured()
		var got map[string]any
		if err := json.Unmarshal([]byte(stdout), &got); err != nil || got["id"] != float64(42) {
			t.Errorf("stdout = %q (%v)", stdout, err)
		}
		if !strings.Contains(stderr, "TTL 300, not 3600") || !strings.Contains(stderr, "dns update example.com 42") {
			t.Errorf("stderr = %q", stderr)
		}
	})

	t.Run("a dry run reports the existing record and previews nothing", func(t *testing.T) {
		z, srv := newFakeZone(t, existing)
		cmd, captured := createFor(t, srv, runOpts{dryRun: true},
			"--type", "CNAME", "--host", "www", "--answer", "example.net", "--if-not-exists")
		if err := runCreate(cmd, []string{"example.com"}); err != nil {
			t.Fatalf("runCreate: %v", err)
		}
		stdout, _ := captured()
		if strings.Contains(stdout, "POST") || !strings.Contains(stdout, "already exists on example.com (id 42)") {
			t.Errorf("stdout = %q", stdout)
		}
		if len(z.writeLog()) != 0 {
			t.Errorf("wrote %q", z.writeLog())
		}
	})

	t.Run("no match creates", func(t *testing.T) {
		z, srv := newFakeZone(t, existing)
		cmd, _ := createFor(t, srv, runOpts{yes: true},
			"--type", "CNAME", "--host", "docs", "--answer", "example.net", "--if-not-exists")
		if err := runCreate(cmd, []string{"example.com"}); err != nil {
			t.Fatalf("runCreate: %v", err)
		}
		if got := z.writeLog(); !reflect.DeepEqual(got, []string{"POST"}) {
			t.Errorf("writes = %q", got)
		}
	})
}

func deleteFor(t *testing.T, srv *httptest.Server, ifExists bool) *cobra.Command {
	t.Helper()
	cmd, _, _ := commandFor(t, srv, runOpts{yes: true})
	deleteIfExists = ifExists
	t.Cleanup(func() { deleteIfExists = false })
	return cmd
}

// TestDNSDelete_IfExists pins --if-exists: a record already gone exits 0, so
// a retried delete does not fail — but a missing domain still exits 4, so a
// typo is not reported as success.
func TestDNSDelete_IfExists(t *testing.T) {
	t.Run("absent record", func(t *testing.T) {
		z, srv := newFakeZone(t)
		cmd := deleteFor(t, srv, true)
		if err := runDelete(cmd, []string{"example.com", "77"}); err != nil {
			t.Fatalf("want success for an absent record, got %v", err)
		}
		if out := cmdutil.Out(cmd).Writer.(interface{ String() string }).String(); !strings.Contains(out, "Record 77 is not on example.com: nothing to delete") {
			t.Errorf("stdout = %q", out)
		}
		if len(z.writeLog()) != 0 {
			t.Errorf("wrote %q", z.writeLog())
		}
	})
	t.Run("absent record without the flag is still exit 4", func(t *testing.T) {
		_, srv := newFakeZone(t)
		if err := runDelete(deleteFor(t, srv, false), []string{"example.com", "77"}); !cmdutil.IsNotFound(err) {
			t.Errorf("want not found, got %v", err)
		}
	})
	t.Run("absent domain", func(t *testing.T) {
		_, srv := newFakeZone(t)
		err := runDelete(deleteFor(t, srv, true), []string{"other.com", "77"})
		if !cmdutil.IsNotFound(err) || !strings.Contains(err.Error(), `domain "other.com" not found`) {
			t.Errorf("want the domain's not-found, got %v", err)
		}
	})
	t.Run("present record is deleted", func(t *testing.T) {
		z, srv := newFakeZone(t, fakeRecord{ID: 77, Host: "www", Type: "A", Answer: "192.0.2.1", TTL: 300})
		if err := runDelete(deleteFor(t, srv, true), []string{"example.com", "77"}); err != nil {
			t.Fatal(err)
		}
		if got := z.writeLog(); !reflect.DeepEqual(got, []string{"DELETE 77"}) {
			t.Errorf("writes = %q", got)
		}
	})
}

// TestDNSDelete_IfExistsSeveral pins --if-exists with several IDs: absent
// ones are skipped with a note and the rest deleted under one prompt that
// lists only them; all absent is exit 0; a missing domain is still exit 4;
// and without the flag one missing ID fails before anything is deleted.
func TestDNSDelete_IfExistsSeveral(t *testing.T) {
	present := []fakeRecord{
		{ID: 1, Host: "www", Type: "A", Answer: "192.0.2.1", TTL: 300},
		{ID: 3, Host: "api", Type: "A", Answer: "192.0.2.3", TTL: 300},
	}
	t.Run("absent IDs are skipped, the rest deleted", func(t *testing.T) {
		z, srv := newFakeZone(t, present...)
		cmd, _, stderr := commandFor(t, srv, runOpts{yes: true})
		deleteIfExists = true
		t.Cleanup(func() { deleteIfExists = false })
		var prompts []string
		defer cmdutil.StubConfirm(func(p string) bool { prompts = append(prompts, p); return true })()
		if err := runDelete(cmd, []string{"example.com", "1", "2", "3", "4"}); err != nil {
			t.Fatal(err)
		}
		if got := z.writeLog(); !reflect.DeepEqual(got, []string{"DELETE 1", "DELETE 3"}) {
			t.Errorf("writes = %q", got)
		}
		if len(prompts) != 1 || !strings.Contains(prompts[0], "these 2 records") || strings.Contains(prompts[0], "192.0.2.2") {
			t.Errorf("prompts = %q, want one listing records 1 and 3", prompts)
		}
		for _, want := range []string{"record 2 is not on example.com", "record 4 is not on example.com"} {
			if !strings.Contains(strings.ToLower(stderr.String()), want) {
				t.Errorf("stderr = %q, want %q", stderr.String(), want)
			}
		}
		stdout := cmdutil.Out(cmd).Writer.(interface{ String() string }).String()
		if !strings.Contains(stdout, "Deleted record 1") || !strings.Contains(stdout, "Deleted record 3") {
			t.Errorf("stdout = %q", stdout)
		}
	})
	t.Run("all absent is exit 0", func(t *testing.T) {
		z, srv := newFakeZone(t, present...)
		cmd := deleteFor(t, srv, true)
		if err := runDelete(cmd, []string{"example.com", "2", "4"}); err != nil {
			t.Fatalf("want success, got %v", err)
		}
		out := cmdutil.Out(cmd).Writer.(interface{ String() string }).String()
		if !strings.Contains(out, "Record 2 is not on example.com") || !strings.Contains(out, "Record 4 is not on example.com") {
			t.Errorf("stdout = %q", out)
		}
		if len(z.writeLog()) != 0 {
			t.Errorf("wrote %q", z.writeLog())
		}
	})
	t.Run("absent domain is exit 4", func(t *testing.T) {
		_, srv := newFakeZone(t, present...)
		err := runDelete(deleteFor(t, srv, true), []string{"other.com", "2", "4"})
		if !cmdutil.IsNotFound(err) || !strings.Contains(err.Error(), `domain "other.com" not found`) {
			t.Errorf("want the domain's not-found, got %v", err)
		}
	})
	t.Run("a dry run previews only the present records", func(t *testing.T) {
		z, srv := newFakeZone(t, present...)
		cmd, stdout, _ := commandFor(t, srv, runOpts{dryRun: true, format: output.FormatJSON})
		deleteIfExists = true
		t.Cleanup(func() { deleteIfExists = false })
		if err := runDelete(cmd, []string{"example.com", "1", "2", "3"}); err != nil {
			t.Fatal(err)
		}
		var reqs []map[string]any
		if err := json.Unmarshal(stdout.Bytes(), &reqs); err != nil || len(reqs) != 2 {
			t.Fatalf("stdout = %q (%v), want a two-request array", stdout.String(), err)
		}
		if len(z.writeLog()) != 0 {
			t.Errorf("wrote %q", z.writeLog())
		}
	})
	t.Run("without the flag one missing ID deletes nothing", func(t *testing.T) {
		z, srv := newFakeZone(t, present...)
		err := runDelete(deleteFor(t, srv, false), []string{"example.com", "1", "2", "3"})
		if !cmdutil.IsNotFound(err) || !strings.Contains(err.Error(), "record 2 not found") {
			t.Errorf("want record 2's not-found, got %v", err)
		}
		if len(z.writeLog()) != 0 {
			t.Errorf("wrote %q", z.writeLog())
		}
	})
}

// TestDNSList_HostFilter pins --host: "@" and the bare domain mean the apex,
// and a host may be given relative or fully qualified.
func TestDNSList_HostFilter(t *testing.T) {
	_, srv := newFakeZone(t,
		fakeRecord{ID: 1, Host: "", Type: "A", Answer: "192.0.2.1", TTL: 300},
		fakeRecord{ID: 2, Host: "www", Type: "A", Answer: "192.0.2.2", TTL: 300},
		fakeRecord{ID: 3, Host: "www", Type: "AAAA", Answer: "2001:db8::2", TTL: 300},
		fakeRecord{ID: 4, Host: "api", Type: "A", Answer: "192.0.2.4", TTL: 300},
	)
	cases := []struct{ host, rtype, want string }{
		{"www", "", "2\n3\n"},
		{"WWW.example.com.", "", "2\n3\n"},
		{"www", "AAAA", "3\n"},
		{"@", "", "1\n"},
		{"example.com", "", "1\n"},
		{"nothing", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.host+"/"+tc.rtype, func(t *testing.T) {
			cmd, stdout, _ := commandFor(t, srv, runOpts{})
			cmd.Flags().BoolVar(&listAll, "all", false, "")
			cmdutil.Out(cmd).QuietMode = true
			listHost, listType = tc.host, tc.rtype
			t.Cleanup(func() { listHost, listType = "", "" })
			if err := runList(cmd, []string{"example.com"}); err != nil {
				t.Fatal(err)
			}
			if stdout.String() != tc.want {
				t.Errorf("IDs = %q, want %q", stdout.String(), tc.want)
			}
		})
	}

	t.Run("an empty result names the filter", func(t *testing.T) {
		cmd, _, stderr := commandFor(t, srv, runOpts{})
		cmd.Flags().BoolVar(&listAll, "all", false, "")
		listHost = "nothing"
		t.Cleanup(func() { listHost = "" })
		if err := runList(cmd, []string{"example.com"}); err != nil {
			t.Fatal(err)
		}
		if out := cmdutil.Out(cmd).Writer.(interface{ String() string }).String() + stderr.String(); !strings.Contains(out, "DNS record at nothing") {
			t.Errorf("output = %q", out)
		}
	})
}
