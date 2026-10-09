package dns

import (
	"encoding/json"
	"errors"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/patramsey/namecom-cli/cmd/cmdutil"
	"github.com/patramsey/namecom-cli/internal/output"
	"github.com/spf13/cobra"
)

// TestZoneHost pins how a --host is read: relative to the zone, or fully
// qualified with or without the trailing dot, and the domain itself as the
// apex (#285).
func TestZoneHost(t *testing.T) {
	cases := map[string]string{
		"www":                   "www",
		"@":                     "@",
		"sweep.example.com":     "sweep",
		"sweep.example.com.":    "sweep",
		"Sweep.Example.COM":     "Sweep",
		"a.b.example.com":       "a.b",
		"example.com":           "@",
		"example.com.":          "@",
		"*.example.com":         "*",
		"example.com.other.org": "example.com.other.org",
		"notexample.com":        "notexample.com",
	}
	for in, want := range cases {
		got, err := zoneHost(in, "example.com")
		if err != nil || got != want {
			t.Errorf("zoneHost(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if got, err := zoneHost("www.bücher.de", "xn--bcher-kva.de"); err != nil || got != "www" {
		t.Errorf("IDN: zoneHost = %q, %v; want www", got, err)
	}
	for _, bad := range []string{".", "a..example.com", ""} {
		if _, err := zoneHost(bad, "example.com"); err == nil {
			t.Errorf("zoneHost(%q) accepted", bad)
		}
	}
}

// TestZoneHost_AbsoluteOutsideZone: a trailing dot makes a name absolute, so
// one outside the zone is refused, naming the zone. It was made relative,
// and sweep.example.org. created sweep.example.org.example.com. A leading
// dot is an empty label, not an empty --host.
func TestZoneHost_AbsoluteOutsideZone(t *testing.T) {
	cases := map[string]string{
		"sweep.example.org.":  `--host "sweep.example.org." is not in example.com`,
		"www.":                `--host "www." is not in example.com`,
		"example.com.other.":  `--host "example.com.other." is not in example.com`,
		"notexample.com.":     `--host "notexample.com." is not in example.com`,
		".example.com":        "empty label",
		".example.com.":       "empty label",
		"sweep.example.org..": "empty label",
	}
	for in, want := range cases {
		_, err := zoneHost(in, "example.com")
		var ue *cmdutil.UsageError
		if !errors.As(err, &ue) || !strings.Contains(err.Error(), want) {
			t.Errorf("zoneHost(%q) err = %v, want a usage error containing %q", in, err, want)
		}
	}
	// One shape with `url create` (#323): the quoted value in the message,
	// the advice in the hint.
	_, err := zoneHost("sweep.example.org.", "example.com")
	if want := `--host "sweep.example.org." is not in example.com`; err == nil || err.Error() != want {
		t.Errorf("err = %v, want %q", err, want)
	}
	if want := `a trailing dot makes a name absolute; without it, "sweep.example.org" is a host under example.com`; errorHintOf(t, err) != want {
		t.Errorf("hint = %q, want %q", errorHintOf(t, err), want)
	}
}

// TestDNSImport_OutOfZoneHostNamesNoFlag pins #323: a record in the file
// with an out-of-zone host was reported as `--host "…"`, a flag the command
// was not given. It says host.
func TestDNSImport_OutOfZoneHostNamesNoFlag(t *testing.T) {
	z, srv := newFakeZone(t)
	file := writeFile(t, "r.json", `[{"type":"A","host":"x.other.org.","answer":"192.0.2.1","ttl":300}]`)
	_, _, err := runImportFile(t, srv, runOpts{yes: true}, file, false)
	if want := `record 1: host "x.other.org." is not in example.com`; err == nil || err.Error() != want {
		t.Errorf("err = %v, want %q", err, want)
	}
	if _, ok := errors.AsType[*cmdutil.UsageError](err); !ok {
		t.Errorf("err = %T, want a usage error", err)
	}
	if got := z.requestLog(); len(got) != 0 {
		t.Errorf("requests = %q, want none", got)
	}
}

// TestDNSHost_AbsoluteOutsideZoneSendsNothing: create, update, list and a
// JSON import refuse the name before any request.
func TestDNSHost_AbsoluteOutsideZoneSendsNothing(t *testing.T) {
	const host = "sweep.example.org."
	var ue *cmdutil.UsageError
	check := func(t *testing.T, z *fakeZone, err error) {
		t.Helper()
		if !errors.As(err, &ue) || !strings.Contains(err.Error(), "is not in example.com") {
			t.Errorf("err = %v, want a usage error", err)
		}
		if got := z.requestLog(); len(got) != 0 {
			t.Errorf("requests = %q, want none", got)
		}
	}
	t.Run("create", func(t *testing.T) {
		z, srv := newFakeZone(t)
		cmd, _ := createFor(t, srv, runOpts{yes: true}, "--type", "A", "--host", host, "--answer", "192.0.2.10", "--if-not-exists")
		check(t, z, runCreate(cmd, []string{"example.com"}))
	})
	t.Run("update", func(t *testing.T) {
		z, srv := cnameZone(t)
		cmd, _ := updateFor(t, srv, runOpts{}, "--host", host)
		check(t, z, runUpdate(cmd, []string{"example.com", "5"}))
	})
	t.Run("list", func(t *testing.T) {
		z, srv := newFakeZone(t)
		cmd, _, _ := commandFor(t, srv, runOpts{})
		cmd.Flags().BoolVar(&listAll, "all", false, "")
		listHost = host
		t.Cleanup(func() { listHost = "" })
		check(t, z, runList(cmd, []string{"example.com"}))
	})
	t.Run("import JSON", func(t *testing.T) {
		z, srv := newFakeZone(t)
		file := writeFile(t, "r.json", `[{"type":"A","host":"`+host+`","answer":"192.0.2.10","ttl":300}]`)
		_, _, err := runImportFile(t, srv, runOpts{}, file, false)
		check(t, z, err)
		if err != nil && !strings.Contains(err.Error(), "record 1") {
			t.Errorf("err = %v, want it to name the record", err)
		}
	})
}

// TestDNSCreate_FQDNHost: a fully qualified --host names the record in the
// zone rather than doubling the domain, so --if-not-exists finds it (#285).
func TestDNSCreate_FQDNHost(t *testing.T) {
	for _, host := range []string{"sweep.example.com", "sweep.example.com."} {
		t.Run(host, func(t *testing.T) {
			z, srv := newFakeZone(t)
			cmd, captured := createFor(t, srv, runOpts{yes: true},
				"--type", "A", "--host", host, "--answer", "192.0.2.10", "--if-not-exists")
			if err := runCreate(cmd, []string{"example.com"}); err != nil {
				t.Fatalf("runCreate: %v", err)
			}
			if got, want := z.sentLog(), []string{`POST /core/v1/domains/example.com/records {"answer":"192.0.2.10","host":"sweep","ttl":300,"type":"A"}`}; !reflect.DeepEqual(got, want) {
				t.Errorf("sent %q, want %q", got, want)
			}
			if got, want := z.requestLog(), []string{listRecords, "POST /core/v1/domains/example.com/records"}; !reflect.DeepEqual(got, want) {
				t.Errorf("requests = %q, want %q", got, want)
			}
			if stdout, _ := captured(); !strings.Contains(stdout, "Created A sweep.example.com → 192.0.2.10") {
				t.Errorf("stdout = %q", stdout)
			}

			// Run again: the record is found, not created twice.
			cmd, captured = createFor(t, srv, runOpts{yes: true},
				"--type", "A", "--host", host, "--answer", "192.0.2.10", "--if-not-exists")
			if err := runCreate(cmd, []string{"example.com"}); err != nil {
				t.Fatalf("second runCreate: %v", err)
			}
			if stdout, _ := captured(); !strings.Contains(stdout, "already exists") {
				t.Errorf("second run stdout = %q", stdout)
			}
			if n := len(z.writeLog()); n != 1 {
				t.Errorf("writes = %q, want one POST", z.writeLog())
			}
		})
	}

	t.Run("the domain itself is the apex", func(t *testing.T) {
		z, srv := newFakeZone(t)
		cmd, _ := createFor(t, srv, runOpts{yes: true}, "--type", "A", "--host", "example.com", "--answer", "192.0.2.10")
		if err := runCreate(cmd, []string{"example.com"}); err != nil {
			t.Fatalf("runCreate: %v", err)
		}
		if got := z.sentLog(); len(got) != 1 || !strings.Contains(got[0], `"host":"@"`) {
			t.Errorf("sent %q", got)
		}
	})
}

// TestDNSImport_FQDNHostInJSON: a JSON record whose host is fully qualified
// is read as the record in the zone, as `dns create --host` reads it.
func TestDNSImport_FQDNHostInJSON(t *testing.T) {
	z, srv := newFakeZone(t, fakeRecord{ID: 1, Host: "www", Type: "A", Answer: "192.0.2.1", TTL: 300})
	file := writeFile(t, "records.json", `[
	  {"type":"A","host":"www.example.com.","answer":"192.0.2.1","ttl":300},
	  {"type":"A","host":"example.com","answer":"192.0.2.2","ttl":300}
	]`)
	if _, _, err := runImportFile(t, srv, runOpts{}, file, true); err != nil {
		t.Fatal(err)
	}
	want := []string{`POST /core/v1/domains/example.com/records {"answer":"192.0.2.2","host":"@","ttl":300,"type":"A"}`}
	if got := z.sentLog(); !reflect.DeepEqual(got, want) {
		t.Errorf("sent %q, want %q", got, want)
	}
}

// updateFor is a `dns update` command wired to srv, with its flags parsed.
func updateFor(t *testing.T, srv *httptest.Server, o runOpts, flags ...string) (*cobra.Command, func() (string, string)) {
	t.Helper()
	cmd, stdout, stderr := commandFor(t, srv, o)
	cmd.Flags().StringVar(&updateType, "type", "", "")
	cmd.Flags().StringVar(&updateHost, "host", "", "")
	cmd.Flags().StringVar(&updateAnswer, "answer", "", "")
	cmd.Flags().Int64Var(&updateTTL, "ttl", 0, "")
	cmd.Flags().Int64Var(&updatePriority, "priority", 0, "")
	if err := cmd.ParseFlags(flags); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	t.Cleanup(func() { updateType, updateHost, updateAnswer, updateTTL, updatePriority = "", "", "", 0, 0 })
	return cmd, func() (string, string) { return stdout.String(), stderr.String() }
}

const getRecord5 = "GET /core/v1/domains/example.com/records/5"

func cnameZone(t *testing.T) (*fakeZone, *httptest.Server) {
	return newFakeZone(t, fakeRecord{ID: 5, Host: "www", Type: "CNAME", Answer: "target.example.net", TTL: 300})
}

// TestDNSUpdate_NoChange: with nothing to change, dns update reads the
// record and sends no PUT, and says so — "changed": false in JSON (#285).
func TestDNSUpdate_NoChange(t *testing.T) {
	cases := map[string][]string{
		"same answer":                {"--answer", "target.example.net"},
		"same answer, trailing dot":  {"--answer", "target.example.net."},
		"same host, fully qualified": {"--host", "www.example.com"},
		"same ttl":                   {"--ttl", "300"},
	}
	for name, flags := range cases {
		t.Run(name, func(t *testing.T) {
			z, srv := cnameZone(t)
			cmd, captured := updateFor(t, srv, runOpts{}, flags...)
			if err := runUpdate(cmd, []string{"example.com", "5"}); err != nil {
				t.Fatalf("runUpdate: %v", err)
			}
			if got, want := z.requestLog(), []string{getRecord5}; !reflect.DeepEqual(got, want) {
				t.Errorf("requests = %q, want %q", got, want)
			}
			if stdout, _ := captured(); !strings.Contains(stdout, "nothing to change") {
				t.Errorf("stdout = %q", stdout)
			}
		})
	}

	t.Run("JSON says changed false", func(t *testing.T) {
		z, srv := cnameZone(t)
		cmd, captured := updateFor(t, srv, runOpts{format: output.FormatJSON}, "--ttl", "300")
		if err := runUpdate(cmd, []string{"example.com", "5"}); err != nil {
			t.Fatalf("runUpdate: %v", err)
		}
		stdout, _ := captured()
		var got map[string]any
		if err := json.Unmarshal([]byte(stdout), &got); err != nil || got["changed"] != false || got["id"] != float64(5) {
			t.Errorf("stdout = %q (%v)", stdout, err)
		}
		if n := len(z.requestLog()); n != 1 {
			t.Errorf("requests = %q", z.requestLog())
		}
	})

	t.Run("a dry run previews nothing", func(t *testing.T) {
		z, srv := cnameZone(t)
		cmd, captured := updateFor(t, srv, runOpts{dryRun: true}, "--ttl", "300")
		if err := runUpdate(cmd, []string{"example.com", "5"}); err != nil {
			t.Fatalf("runUpdate: %v", err)
		}
		if stdout, _ := captured(); strings.Contains(stdout, "PUT") || !strings.Contains(stdout, "nothing to change") {
			t.Errorf("stdout = %q", stdout)
		}
		if n := len(z.requestLog()); n != 1 {
			t.Errorf("requests = %q", z.requestLog())
		}
	})
}

// TestDNSUpdate_ChangeSaysChanged: a real update is one GET and one PUT, and
// JSON carries "changed": true.
func TestDNSUpdate_ChangeSaysChanged(t *testing.T) {
	z, srv := cnameZone(t)
	cmd, captured := updateFor(t, srv, runOpts{format: output.FormatJSON}, "--ttl", "600")
	if err := runUpdate(cmd, []string{"example.com", "5"}); err != nil {
		t.Fatalf("runUpdate: %v", err)
	}
	stdout, _ := captured()
	var got map[string]any
	if err := json.Unmarshal([]byte(stdout), &got); err != nil || got["changed"] != true || got["ttl"] != float64(600) {
		t.Errorf("stdout = %q (%v)", stdout, err)
	}
	if got, want := z.requestLog(), []string{getRecord5, "PUT /core/v1/domains/example.com/records/5"}; !reflect.DeepEqual(got, want) {
		t.Errorf("requests = %q, want %q", got, want)
	}
}

// TestDNSUpdate_NoFlagsIsUsageError: dns update with no value to change is
// a usage error, sent nowhere, as domain update's is.
func TestDNSUpdate_NoFlagsIsUsageError(t *testing.T) {
	z, srv := cnameZone(t)
	cmd, _ := updateFor(t, srv, runOpts{})
	err := runUpdate(cmd, []string{"example.com", "5"})
	var ue *cmdutil.UsageError
	if !errors.As(err, &ue) || !strings.Contains(err.Error(), "--answer") {
		t.Errorf("err = %v, want a usage error naming the flags", err)
	}
	if n := len(z.requestLog()); n != 0 {
		t.Errorf("requests = %q, want none", z.requestLog())
	}
}

// TestDNSUpdate_FQDNHost: --host on update reads a fully qualified name as
// create does.
func TestDNSUpdate_FQDNHost(t *testing.T) {
	z, srv := cnameZone(t)
	cmd, _ := updateFor(t, srv, runOpts{}, "--host", "docs.example.com.")
	if err := runUpdate(cmd, []string{"example.com", "5"}); err != nil {
		t.Fatalf("runUpdate: %v", err)
	}
	if got := z.sentLog(); len(got) != 1 || !strings.Contains(got[0], `"host":"docs"`) {
		t.Errorf("sent %q", got)
	}
}

// TestDNSList_EmptyHostFilterMessage: the empty state pluralises "record",
// not the host (#285).
func TestDNSList_EmptyHostFilterMessage(t *testing.T) {
	_, srv := newFakeZone(t, fakeRecord{ID: 1, Host: "api", Type: "A", Answer: "192.0.2.1", TTL: 300})
	cases := []struct{ host, rtype, want string }{
		{"www", "", "No DNS records at www found."},
		{"nomatch", "", "No DNS records at nomatch found."},
		{"www", "mx", "No MX records at www found."},
		{"", "mx", "No MX records found."},
	}
	for _, tc := range cases {
		t.Run(tc.host+"/"+tc.rtype, func(t *testing.T) {
			cmd, _, stderr := commandFor(t, srv, runOpts{})
			cmd.Flags().BoolVar(&listAll, "all", false, "")
			listHost, listType = tc.host, tc.rtype
			t.Cleanup(func() { listHost, listType = "", "" })
			if err := runList(cmd, []string{"example.com"}); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(stderr.String(), tc.want) {
				t.Errorf("stderr = %q, want %q", stderr.String(), tc.want)
			}
		})
	}
}
