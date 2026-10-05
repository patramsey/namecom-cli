package contact

import (
	"strings"
	"testing"
	"time"

	coreapigo "github.com/namedotcom/core-api-go"
)

// TestDeadlineWarning_FollowsTense: the callout said domains "may be LOCKED
// … after the deadline" when the deadlines had passed 73 and 77 days earlier
// (#238).
func TestDeadlineWarning_FollowsTense(t *testing.T) {
	now := time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC)
	at := func(days int) *coreapigo.UnverifiedContact {
		d := now.AddDate(0, 0, days)
		return &coreapigo.UnverifiedContact{VerifyBy: &d}
	}
	tests := []struct {
		name     string
		contacts []*coreapigo.UnverifiedContact
		want     string
		notWant  string
	}{
		{"all passed", []*coreapigo.UnverifiedContact{at(-73), at(-77)}, "deadline passed", "after"},
		{"some passed", []*coreapigo.UnverifiedContact{at(-3), at(10)}, "Some deadlines have passed", ""},
		{"none passed", []*coreapigo.UnverifiedContact{at(10)}, "by the deadline", "passed"},
	}
	for _, tt := range tests {
		got := deadlineWarning(tt.contacts, now)
		if !strings.Contains(got, tt.want) || (tt.notWant != "" && strings.Contains(got, tt.notWant)) {
			t.Errorf("%s: %q, want %q and not %q", tt.name, got, tt.want, tt.notWant)
		}
	}
}
