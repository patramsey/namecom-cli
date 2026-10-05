package cmd

import "testing"

// TestChecksForUpdates: shell completion never looks for a release. The
// Homebrew formula generates its completion scripts by running `namecom
// completion <shell>` in brew's sandbox, and __complete runs on every TAB.
func TestChecksForUpdates(t *testing.T) {
	for _, tt := range []struct {
		args []string
		want bool
	}{
		{nil, true},
		{[]string{"domain", "list"}, true},
		{[]string{"completion", "zsh"}, false},
		{[]string{"__complete", "domain", ""}, false},
		{[]string{"__completeNoDesc", "domain", ""}, false},
	} {
		if got := checksForUpdates(tt.args); got != tt.want {
			t.Errorf("checksForUpdates(%q) = %v, want %v", tt.args, got, tt.want)
		}
	}
}
