package dns

import (
	"bytes"
	"errors"
	"reflect"
	"strings"
	"testing"

	coreapigo "github.com/namedotcom/core-api-go"
	"github.com/patramsey/namecom-cli/cmd/cmdutil"
)

// TestDNSList_PriorityColumnOnlyForMXAndSRV: the PRIORITY column appeared on
// A, CNAME and TXT tables, where it is always empty (#233). It now appears
// only when a listed record is MX or SRV, and a missing priority there shows
// as "—" rather than a blank cell (#238).
func TestDNSList_PriorityColumnOnlyForMXAndSRV(t *testing.T) {
	str := func(s string) *string { return &s }
	prio := int64(10)
	records := []*coreapigo.Record{
		{Type: str("A"), Host: str("www"), Answer: str("192.0.2.1"), TTL: 300},
		{Type: str("TXT"), Host: str(""), Answer: str("v=spf1 -all"), TTL: 300},
		{Type: str("MX"), Host: str(""), Answer: str("mx1.example.com"), TTL: 300, Priority: &prio},
		{Type: str("MX"), Host: str(""), Answer: str("mx2.example.com"), TTL: 300},
	}

	render := func(t *testing.T, typ string) string {
		t.Helper()
		var stdout bytes.Buffer
		cmd := cmdForList(t, recordServer(t, records, 0), &stdout)
		listAll, listType = false, typ
		t.Cleanup(func() { listType = "" })
		if err := runList(cmd, []string{"example.com"}); err != nil {
			t.Fatalf("runList: %v", err)
		}
		return stdout.String()
	}

	t.Run("grouped", func(t *testing.T) {
		got := render(t, "")
		// Each type is its own table; only the MX one has a PRIORITY header.
		sections := strings.Split(got, "\nMX\n")
		if len(sections) != 2 {
			t.Fatalf("want an MX section:\n%s", got)
		}
		if strings.Contains(sections[0], "PRIORITY") {
			t.Errorf("A/TXT tables show PRIORITY:\n%s", sections[0])
		}
		if !strings.Contains(sections[1], "PRIORITY") || !strings.Contains(sections[1], "10") {
			t.Errorf("MX table lost PRIORITY:\n%s", sections[1])
		}
		if !strings.Contains(sections[1], "—") {
			t.Errorf("a missing MX priority should show as —:\n%s", sections[1])
		}
	})

	t.Run("filtered to A", func(t *testing.T) {
		if got := render(t, "A"); strings.Contains(got, "PRIORITY") {
			t.Errorf("--type A shows PRIORITY:\n%s", got)
		}
	})

	t.Run("filtered to MX", func(t *testing.T) {
		if got := render(t, "MX"); !strings.Contains(got, "PRIORITY") {
			t.Errorf("--type MX lost PRIORITY:\n%s", got)
		}
	})
}

// TestDNSCreate_PriorityMatchesType: an MX or SRV record without --priority
// was sent without one, and the API refused it, while a warning said the CLI
// used 0 — and a dry run previewed it with exit 0. --priority on any other
// type was sent and silently dropped. Both are usage errors before any
// request, dry run included.
func TestDNSCreate_PriorityMatchesType(t *testing.T) {
	cases := map[string]struct {
		flags []string
		want  string
	}{
		"MX without --priority":  {[]string{"--type", "MX", "--answer", "mail.example.com"}, "MX records need a priority: pass --priority"},
		"SRV without --priority": {[]string{"--type", "SRV", "--host", "_sip._tcp", "--answer", "0 5060 sip.example.com"}, "SRV records need a priority"},
		"A with --priority":      {[]string{"--type", "A", "--answer", "192.0.2.5", "--priority", "10"}, "--priority applies only to MX and SRV records, not A"},
		"TXT with --priority 0":  {[]string{"--type", "TXT", "--answer", "hello", "--priority", "0"}, "not TXT"},
	}
	for name, tc := range cases {
		for _, dry := range []bool{false, true} {
			t.Run(name+map[bool]string{true: ", dry run"}[dry], func(t *testing.T) {
				z, srv := newFakeZone(t)
				cmd, _ := createFor(t, srv, runOpts{yes: true, dryRun: dry}, append(tc.flags, "--if-not-exists")...)
				err := runCreate(cmd, []string{"example.com"})
				var ue *cmdutil.UsageError
				if !errors.As(err, &ue) || !strings.Contains(err.Error(), tc.want) {
					t.Errorf("err = %v, want a usage error containing %q", err, tc.want)
				}
				if got := z.requestLog(); len(got) != 0 {
					t.Errorf("requests = %q, want none", got)
				}
			})
		}
	}
}

// TestDNSCreate_ExplicitZeroPriorityNoWarning: --priority 0 was sent but
// still warned "because --priority was not set".
func TestDNSCreate_ExplicitZeroPriorityNoWarning(t *testing.T) {
	z, srv := newFakeZone(t)
	cmd, captured := createFor(t, srv, runOpts{yes: true}, "--type", "MX", "--answer", "mail.example.com", "--priority", "0")
	if err := runCreate(cmd, []string{"example.com"}); err != nil {
		t.Fatalf("runCreate: %v", err)
	}
	if _, stderr := captured(); strings.Contains(stderr, "priority") {
		t.Errorf("stderr = %q, want no priority warning", stderr)
	}
	want := []string{`POST /core/v1/domains/example.com/records {"answer":"mail.example.com","host":"@","priority":0,"ttl":300,"type":"MX"}`}
	if got := z.sentLog(); !reflect.DeepEqual(got, want) {
		t.Errorf("sent %q, want %q", got, want)
	}
}

// TestDNSUpdate_PriorityMatchesType: as create, after the one GET that
// read-modify-write needs and with no PUT.
func TestDNSUpdate_PriorityMatchesType(t *testing.T) {
	cases := map[string]struct {
		rec   fakeRecord
		flags []string
		want  string
	}{
		"--priority on an A record": {fakeRecord{ID: 5, Host: "www", Type: "A", Answer: "192.0.2.1", TTL: 300}, []string{"--priority", "10"}, "not A"},
		"A made MX, no priority":    {fakeRecord{ID: 5, Host: "www", Type: "A", Answer: "192.0.2.1", TTL: 300}, []string{"--type", "MX", "--answer", "mail.example.com"}, "MX records need a priority"},
		"MX with none stored":       {fakeRecord{ID: 5, Host: "", Type: "MX", Answer: "mail.example.com", TTL: 300}, []string{"--ttl", "600"}, "MX records need a priority"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			z, srv := newFakeZone(t, tc.rec)
			cmd, _ := updateFor(t, srv, runOpts{}, tc.flags...)
			err := runUpdate(cmd, []string{"example.com", "5"})
			var ue *cmdutil.UsageError
			if !errors.As(err, &ue) || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want a usage error containing %q", err, tc.want)
			}
			if got, want := z.requestLog(), []string{getRecord5}; !reflect.DeepEqual(got, want) {
				t.Errorf("requests = %q, want %q", got, want)
			}
		})
	}

	t.Run("A made MX with --priority", func(t *testing.T) {
		z, srv := newFakeZone(t, fakeRecord{ID: 5, Host: "mx", Type: "A", Answer: "192.0.2.1", TTL: 300})
		cmd, _ := updateFor(t, srv, runOpts{}, "--type", "MX", "--answer", "mail.example.com", "--priority", "10")
		if err := runUpdate(cmd, []string{"example.com", "5"}); err != nil {
			t.Fatalf("runUpdate: %v", err)
		}
		if got := z.writeLog(); !reflect.DeepEqual(got, []string{"PUT 5"}) {
			t.Errorf("writes = %q", got)
		}
	})
}

// TestDNSImportSync_PriorityFromFile: a file's MX record without a priority
// is refused before anything is sent, naming the record; a priority on an A
// record is not sent, as the API would drop it.
func TestDNSImportSync_PriorityFromFile(t *testing.T) {
	const noPrio = `[{"type":"A","host":"www","answer":"192.0.2.1","ttl":300},{"type":"MX","host":"@","answer":"mail.example.com","ttl":300}]`
	t.Run("import", func(t *testing.T) {
		z, srv := newFakeZone(t)
		_, _, err := runImportFile(t, srv, runOpts{}, writeFile(t, "r.json", noPrio), false)
		var ue *cmdutil.UsageError
		if !errors.As(err, &ue) || !strings.Contains(err.Error(), "record 2 (MX @): MX records need a priority") {
			t.Errorf("err = %v", err)
		}
		if got := z.requestLog(); len(got) != 0 {
			t.Errorf("requests = %q, want none", got)
		}
	})
	t.Run("sync", func(t *testing.T) {
		z, srv := newFakeZone(t)
		_, _, err := runSyncFile(t, srv, runOpts{dryRun: true}, writeFile(t, "r.json", noPrio), false, false)
		var ue *cmdutil.UsageError
		if !errors.As(err, &ue) || !strings.Contains(err.Error(), "record 2 (MX @): MX records need a priority") {
			t.Errorf("err = %v", err)
		}
		if got, want := z.requestLog(), []string{listRecords}; !reflect.DeepEqual(got, want) {
			t.Errorf("requests = %q, want %q", got, want)
		}
	})
	t.Run("a priority on A is not sent", func(t *testing.T) {
		z, srv := newFakeZone(t)
		file := writeFile(t, "r.json", `[{"type":"A","host":"www","answer":"192.0.2.1","ttl":300,"priority":10}]`)
		if _, _, err := runImportFile(t, srv, runOpts{}, file, false); err != nil {
			t.Fatal(err)
		}
		want := []string{`POST /core/v1/domains/example.com/records {"answer":"192.0.2.1","host":"www","ttl":300,"type":"A"}`}
		if got := z.sentLog(); !reflect.DeepEqual(got, want) {
			t.Errorf("sent %q, want %q", got, want)
		}
	})
}
