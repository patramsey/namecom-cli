package dns

import (
	"bytes"
	"strings"
	"testing"

	coreapigo "github.com/namedotcom/core-api-go"
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
