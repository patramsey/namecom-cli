package dns

import (
	"bytes"
	"strings"
	"testing"

	"github.com/patramsey/namecom-cli/internal/output"
)

// TestSyncPlan_UpdateLineShowsTheNewRecord pins #382: the plan line of a
// CNAME update named the old target in its heading — "update CNAME alias →
// www.old … : answer www.old → other.example.net" — so it read as though
// the record stayed. The heading is the record as the update leaves it, as
// a create line is; the changes say what it was.
func TestSyncPlan_UpdateLineShowsTheNewRecord(t *testing.T) {
	before := planRecord{ID: 9, Type: "CNAME", Host: "alias", Answer: "www.old.example", TTL: 300}
	after := before
	after.Answer = "other.example.net"
	p := &syncPlan{Domain: "example.com", Updates: []planUpdate{{
		planRecord: after, Before: before, Changes: recordDiffStrings(before, after),
	}}}
	var w bytes.Buffer
	if err := printPlan(&output.Config{Format: output.FormatTable, Color: output.ColorNever, Writer: &w, EWriter: &w}, p); err != nil {
		t.Fatal(err)
	}
	want := "  update CNAME alias → other.example.net (TTL 300) [id 9]: answer www.old.example → other.example.net\n"
	if !strings.Contains(w.String(), want) {
		t.Errorf("plan:\n%s\nwant the line:\n%s", w.String(), want)
	}
}
