package cmd

import (
	"errors"
	"strings"
	"testing"

	"github.com/patramsey/namecom-cli/cmd/cmdutil"
)

// TestAliases pins #237: `dns rm`, `email add` and `domain ls` were unknown
// commands. Each alias must resolve to the command it stands for.
func TestAliases(t *testing.T) {
	for alias, want := range map[string]string{
		"domain ls":       "namecom domain list",
		"dns ls":          "namecom dns list",
		"dns rm":          "namecom dns delete",
		"dns add":         "namecom dns create",
		"dnssec ls":       "namecom dnssec list",
		"dnssec rm":       "namecom dnssec delete",
		"dnssec add":      "namecom dnssec create",
		"email ls":        "namecom email list",
		"email rm":        "namecom email delete",
		"email add":       "namecom email create",
		"url ls":          "namecom url list",
		"url rm":          "namecom url delete",
		"url add":         "namecom url create",
		"vanity-ns ls":    "namecom vanity-ns list",
		"vanity-ns rm":    "namecom vanity-ns delete",
		"vanity-ns add":   "namecom vanity-ns create",
		"transfer ls":     "namecom transfer list",
		"order ls":        "namecom order list",
		"config ls":       "namecom config list-profiles",
		"config profiles": "namecom config list-profiles",
	} {
		c, _, err := rootCmd.Find(strings.Fields(alias))
		if err != nil {
			t.Errorf("namecom %s: %v", alias, err)
			continue
		}
		if got := c.CommandPath(); got != want {
			t.Errorf("namecom %s resolves to %q, want %q", alias, got, want)
		}
	}
}

// TestRootSuggestFor pins #237: `namecom records`, `redirect`, `login` and
// `whoami` failed with no suggestion, because nothing about the spelling is
// close to the command meant.
func TestRootSuggestFor(t *testing.T) {
	for word, want := range map[string]string{
		"records":  "'namecom dns'",
		"redirect": "'namecom url'",
		"forward":  "'namecom url'",
		"login":    "'namecom auth login'",
		"whoami":   "'namecom auth status'",
	} {
		t.Run(word, func(t *testing.T) {
			err := suggestFor(cmdutil.ClassifyCobraUsage(executeRoot(t, word)))
			if exitCode(err) != 2 {
				t.Fatalf("namecom %s: exit %d (%v), want 2", word, exitCode(err), err)
			}
			u, _ := errors.AsType[*cmdutil.UsageError](err)
			if hint := u.UserHint(); hint != "did you mean "+want+"?" {
				t.Errorf("namecom %s: hint %q, want it to suggest %s", word, hint, want)
			}
		})
	}

	// A word with no mapping keeps cobra's own answer.
	err := suggestFor(cmdutil.ClassifyCobraUsage(executeRoot(t, "domian")))
	u, _ := errors.AsType[*cmdutil.UsageError](err)
	if u == nil || u.UserHint() != "did you mean 'namecom domain'?" {
		t.Errorf("namecom domian: %v, want cobra's own suggestion of domain", err)
	}
}
