package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/patramsey/namecom-cli/cmd/cmdutil"
	"github.com/patramsey/namecom-cli/internal/output"
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

// TestHelpText pins the wording conventions #237 brought every page to.
func TestHelpText(t *testing.T) {
	thirdPerson := map[string]bool{
		"Displays": true, "Opens": true, "Asks": true, "Shows": true, "Lists": true,
		"Gets": true, "Returns": true, "Creates": true, "Deletes": true, "Updates": true,
		"Prints": true, "Manages": true, "Sets": true, "Removes": true, "Adds": true,
	}
	var walk func(*cobra.Command)
	walk = func(c *cobra.Command) {
		for _, sub := range c.Commands() {
			walk(sub)
		}
		path := c.CommandPath()
		// A Long that only repeats Short says nothing the command list did not.
		if c.Long != "" && strings.TrimSuffix(c.Long, ".") == strings.TrimSuffix(c.Short, ".") {
			t.Errorf("%s: Long only repeats Short", path)
		}
		if f := strings.Fields(c.Long); len(f) > 0 && thirdPerson[f[0]] {
			t.Errorf("%s: Long starts %q; use the imperative, as every other page does", path, f[0])
		}
		// Examples lead with the plain form, and one that skips the
		// confirmation is labelled, so --yes is not what a reader copies first.
		lines := strings.Split(strings.TrimSpace(c.Example), "\n")
		for i, line := range lines {
			if !strings.Contains(line, "--yes") {
				continue
			}
			if i == 0 {
				t.Errorf("%s: the first example passes --yes", path)
			} else if !strings.HasPrefix(strings.TrimSpace(lines[i-1]), "#") {
				t.Errorf("%s: an example passes --yes without a comment line above it saying why:\n%s", path, line)
			}
		}
	}
	walk(rootCmd)

	// The url pages say "redirect", and the dnssec pages "DS record", rather
	// than the mix of terms they used.
	for group, stale := range map[string]string{"url": "forwarding", "dnssec": "DNSSEC key"} {
		c, _, _ := rootCmd.Find([]string{group})
		for _, sub := range append(c.Commands(), c) {
			text := sub.Short + " " + sub.Long
			sub.LocalFlags().VisitAll(func(f *pflag.Flag) { text += " " + f.Usage })
			if strings.Contains(text, stale) {
				t.Errorf("%s help still says %q", sub.CommandPath(), stale)
			}
		}
	}
}

// TestUnknownCommandSuggestionsField pins #237: in JSON mode the commands an
// unknown one was probably meant to be are listed in error.suggestions, beside
// the unchanged message and hint, so a script need not parse the hint.
func TestUnknownCommandSuggestionsField(t *testing.T) {
	for args, want := range map[string][]string{
		"dns delet": {"namecom dns delete"},
		"whoami":    {"namecom auth status"},
		"frob":      nil,
	} {
		t.Run(args, func(t *testing.T) {
			err := suggestFor(cmdutil.ClassifyCobraUsage(executeRoot(t, strings.Fields(args)...)))
			var ew bytes.Buffer
			cfg := &output.Config{Format: output.FormatJSON, Writer: &bytes.Buffer{}, EWriter: &ew}
			if code := reportError(cfg, err); code != 2 {
				t.Fatalf("exit %d, want 2", code)
			}
			var env struct {
				Error struct {
					Message     string   `json:"message"`
					Suggestions []string `json:"suggestions"`
				} `json:"error"`
				Hint string `json:"hint"`
			}
			if err := json.Unmarshal(ew.Bytes(), &env); err != nil {
				t.Fatalf("envelope does not parse: %v\n%s", err, ew.String())
			}
			if !slices.Equal(env.Error.Suggestions, want) {
				t.Errorf("suggestions = %q, want %q\n%s", env.Error.Suggestions, want, ew.String())
			}
			if !strings.HasPrefix(env.Error.Message, "unknown command ") || strings.Contains(env.Error.Message, "\n") || env.Hint == "" {
				t.Errorf("message and hint should be unchanged:\n%s", ew.String())
			}
		})
	}
}
