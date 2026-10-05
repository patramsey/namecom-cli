package domain

import (
	"strings"
	"testing"
	"time"
)

// TestLockDate: the transfer-lock refusal printed the API's raw timestamp,
// "until 2026-11-28T06:37:39Z", where every other date reads
// "2026-11-28 (in 2 months)" (#238). A value that does not parse is passed
// through rather than lost.
func TestLockDate(t *testing.T) {
	soon := time.Now().UTC().Add(10 * 24 * time.Hour)
	for _, s := range []string{soon.Format(time.RFC3339), soon.Format("2006-01-02 15:04:05")} {
		got := lockDate(s)
		if want := soon.Format("2006-01-02") + " (in "; !strings.HasPrefix(got, want) || !strings.HasSuffix(got, " days)") {
			t.Errorf("lockDate(%q) = %q, want %q… days)", s, got, want)
		}
	}
	if got := lockDate("next Tuesday"); got != "next Tuesday" {
		t.Errorf("lockDate of an unparseable value = %q, want it unchanged", got)
	}
}
