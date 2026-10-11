package dns

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	coreapigo "github.com/namedotcom/core-api-go"
	"github.com/patramsey/namecom-cli/internal/output"
)

// zoneInjection is a payload that, written raw into a zone line, ends it and
// adds a record of its own (#340): an A record for the apex pointing at
// 6.6.6.6, then a terminal escape. It carries each control character the
// issue names: \n, \t, \r and ESC. It is lowercase (zone class and type are
// case-insensitive) so that the names it lands in, which parseZone
// lowercases, compare byte for byte after the round trip.
const zoneInjection = "\n@\t1\tin\ta\t6.6.6.6\r\n\x1b[2j"

// exportZoneText runs `dns export --zone` against srv and returns stdout.
func exportZoneText(t *testing.T, body string) string {
	t.Helper()
	cmd, buf := cmdForExport(t, recordsServer(t, body), output.FormatTable)
	exportZone = true
	t.Cleanup(func() { exportZone = false })
	if err := runExport(cmd, []string{"example.com"}); err != nil {
		t.Fatalf("runExport: %v", err)
	}
	return buf.String()
}

// hostileRecords is one record of every type `dns export --zone` writes, with
// zoneInjection in its owner name and in its answer.
func hostileRecords() []*coreapigo.Record {
	s := func(v string) *string { return &v }
	p := zoneInjection
	mk := func(id int, typ, answer string, prio *int64) *coreapigo.Record {
		host := fmt.Sprintf("h%d%s", id, p)
		return &coreapigo.Record{
			ID: &id, Host: s(host), Fqdn: s(host + ".example.com."),
			Type: s(typ), Answer: s(answer), TTL: 300, Priority: prio,
		}
	}
	return []*coreapigo.Record{
		mk(1, "A", "192.0.2.1"+p, nil),
		mk(2, "AAAA", "2001:db8::1"+p, nil),
		mk(3, "CNAME", "t"+p+".example.net", nil),
		mk(4, "NS", "ns"+p+".example.net", nil),
		mk(5, "MX", "mx"+p+".example.net", p64(10)),
		mk(6, "SRV", "10 5060 sip"+p+".example.net", p64(5)),
		mk(7, "TXT", "v=spf1"+p+" ~all", nil),
		mk(8, "CAA", `0 issue "ca`+p+`.example"`, nil),
		mk(9, "ANAME", "lb"+p+".example.net", nil),
	}
}

func recordsBody(t *testing.T, recs []*coreapigo.Record) string {
	t.Helper()
	b, err := json.Marshal(map[string]any{"records": recs})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// checkNoInjectedLines fails unless text is exactly n lines, none of which a
// payload started: no raw control character but the tab separators, and no
// line that does not begin with an owner name (or the ANAME comment).
func checkNoInjectedLines(t *testing.T, text string, n int) {
	t.Helper()
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	if len(lines) != n {
		t.Fatalf("export is %d lines for %d records:\n%s", len(lines), n, text)
	}
	for _, l := range lines {
		for i := 0; i < len(l); i++ {
			if c := l[i]; isControl(c) && c != '\t' {
				t.Errorf("raw control byte %#x in line %q", c, l)
			}
		}
		if !strings.HasPrefix(l, "h") && !strings.HasPrefix(l, anameCommentPrefix+"h") {
			t.Errorf("line does not start with its owner: %q", l)
		}
		body := strings.TrimPrefix(l, anameCommentPrefix)
		if f := strings.Split(body, "\t"); len(f) != 5 {
			t.Errorf("line has %d tab-separated fields, want 5: %q", len(f), l)
		}
	}
}

// TestDNSExport_ZoneEscapesControlCharacters pins #340: a control character
// in an owner name or in any answer — A, AAAA, CNAME, NS, MX, SRV, TXT, CAA,
// and the ANAME comment — is escaped, so it cannot end the line and inject a
// record. The escapes are ones parseZone decodes, so every record reads back
// as the record it was written from, by the comparison `dns sync` makes.
func TestDNSExport_ZoneEscapesControlCharacters(t *testing.T) {
	recs := hostileRecords()
	text := exportZoneText(t, recordsBody(t, recs))
	checkNoInjectedLines(t, text, len(recs))

	got, err := parseZone(text, "example.com")
	if err != nil {
		t.Fatalf("parsing exported zone: %v\n%s", err, text)
	}
	if len(got) != len(recs) {
		t.Fatalf("parsed %d records from %d:\n%s", len(got), len(recs), text)
	}
	for i, r := range recs {
		g := got[i]
		if gk, wk := keyOf(g.Type, g.Host, g.Answer), liveKey(r); gk != wk {
			t.Errorf("record %d read back as %+v, want %+v", i+1, gk, wk)
		}
		if r.Priority != nil && (g.Priority == nil || *g.Priority != *r.Priority) {
			t.Errorf("record %d priority = %v, want %d", i+1, g.Priority, *r.Priority)
		}
	}
}

// TestDNSExport_ZoneEscapesOtherFields covers the fields beyond control
// characters that end or redirect a field — space, ";", "(", ")", a quote, a
// backslash — in a type the command does not know (written field by field),
// in the type itself, and an owner the API left empty, which would start the
// line with a tab and join the previous owner.
func TestDNSExport_ZoneEscapesOtherFields(t *testing.T) {
	text := exportZoneText(t, `{"records":[
	  {"id":1,"host":"a","fqdn":"a.example.com.","type":"HINFO","answer":"x;y (z) \"q\" \\ `+"\\n"+`evil","ttl":300},
	  {"id":2,"host":"b","fqdn":"b.example.com.","type":"X`+"\\n"+`@ 1 IN A 6.6.6.6","answer":"1","ttl":300},
	  {"id":3,"host":"c","type":"A","answer":"192.0.2.3","ttl":300},
	  {"id":4,"host":"","type":"A","answer":"192.0.2.4","ttl":300}
	]}`)
	want := "a.example.com.\t300\tIN\tHINFO\tx\\;y \\(z\\) \\\"q\\\" \\\\ \\010evil\n" +
		"b.example.com.\t300\tIN\tX\\010@\\0321\\032IN\\032A\\0326.6.6.6\t1\n" +
		"c.example.com.\t300\tIN\tA\t192.0.2.3\n" +
		"example.com.\t300\tIN\tA\t192.0.2.4\n"
	if text != want {
		t.Errorf("export =\n%q\nwant\n%q", text, want)
	}
}

// TestDNSSync_ZoneExportRoundTrips: a zone exported with --zone and synced
// straight back — --prune-all, so CAA and apex NS are compared too — plans no
// change, for ordinary records and for ones holding control characters.
func TestDNSSync_ZoneExportRoundTrips(t *testing.T) {
	legit := []fakeRecord{
		{ID: 1, Host: "", Type: "NS", Answer: "ns1.name.com", TTL: 300},
		{ID: 2, Host: "", Type: "A", Answer: "192.0.2.1", TTL: 300},
		{ID: 3, Host: "v6", Type: "AAAA", Answer: "2001:db8::1", TTL: 600},
		{ID: 4, Host: "www", Type: "CNAME", Answer: "example.com", TTL: 300},
		{ID: 5, Host: "", Type: "MX", Answer: "mail.example.com", TTL: 300, Priority: prio(10)},
		{ID: 6, Host: "_sip._tcp", Type: "SRV", Answer: "10 5060 sip.example.com", TTL: 300, Priority: prio(5)},
		{ID: 7, Host: "", Type: "TXT", Answer: "v=spf1 include:_spf.example.net ~all", TTL: 300},
		{ID: 8, Host: "dkim._domainkey", Type: "TXT", Answer: strings.Repeat("k", 300), TTL: 300},
		{ID: 9, Host: "", Type: "CAA", Answer: `0 issue "letsencrypt.org"`, TTL: 300},
		{ID: 10, Host: "lb", Type: "ANAME", Answer: "lb.example.net", TTL: 300},
		{ID: 11, Host: "sub", Type: "NS", Answer: "ns.example.net", TTL: 300},
	}
	// Only answers that can hold such bytes: sync refuses a host with
	// whitespace in it before planning, which is its own check, not this one.
	p := zoneInjection
	hostile := []fakeRecord{
		{ID: 22, Host: "h2", Type: "TXT", Answer: "a" + p + "b", TTL: 300},
		{ID: 23, Host: "h3", Type: "CAA", Answer: "0 issue \"ca\tx\"", TTL: 300},
	}
	for name, recs := range map[string][]fakeRecord{"legitimate": legit, "control characters": hostile} {
		t.Run(name, func(t *testing.T) {
			z, srv := newFakeZone(t, recs...)
			cmd, buf := cmdForExport(t, srv, output.FormatTable)
			exportZone = true
			t.Cleanup(func() { exportZone = false })
			if err := runExport(cmd, []string{"example.com"}); err != nil {
				t.Fatalf("runExport: %v", err)
			}
			file := writeFile(t, "example.com.zone", buf.String())
			stdout, stderr, err := runSyncFile(t, srv, runOpts{yes: true, format: output.FormatJSON}, file, true, true)
			if err != nil {
				t.Fatalf("sync: %v\nzone:\n%s\nstderr: %s", err, buf.String(), stderr)
			}
			if w := z.writeLog(); len(w) != 0 {
				t.Errorf("writes = %q, want none\nzone:\n%s\nstdout: %s", w, buf.String(), stdout)
			}
			if !strings.Contains(stdout, `"changed": false`) {
				t.Errorf("stdout = %s", stdout)
			}
		})
	}
}
