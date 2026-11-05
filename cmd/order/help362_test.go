package order

import (
	"strings"
	"testing"
)

// TestGetHelp_NamesNoListFilters pins #362: `order get --help` mentioned
// --since and --until, which only `order list` has. list still explains
// what the order clock means for them.
func TestGetHelp_NamesNoListFilters(t *testing.T) {
	if strings.Contains(getCmd.Long, "--since") || strings.Contains(getCmd.Long, "--until") {
		t.Errorf("order get help names list's filters:\n%s", getCmd.Long)
	}
	if !strings.Contains(listCmd.Long, "--since/--until") {
		t.Errorf("order list help should still explain its date filters:\n%s", listCmd.Long)
	}
}
