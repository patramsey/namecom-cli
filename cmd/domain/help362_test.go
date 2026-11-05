package domain

import (
	"strings"
	"testing"
)

// TestGetHelp_SaysTheReadsAreConcurrent pins #362: `domain get` help said
// "the domains not yet read are not requested", but they are read several
// at a time and those in flight are cancelled after a failure.
func TestGetHelp_SaysTheReadsAreConcurrent(t *testing.T) {
	if strings.Contains(getCmd.Long, "not yet read") {
		t.Errorf("domain get help still says the rest are not requested:\n%s", getCmd.Long)
	}
	if !strings.Contains(getCmd.Long, "several at a time") || !strings.Contains(getCmd.Long, "cancelled") {
		t.Errorf("domain get help should say how the reads are made:\n%s", getCmd.Long)
	}
}
