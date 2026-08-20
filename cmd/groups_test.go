package cmd

import (
	"errors"
	"strings"
	"testing"

	"github.com/patramsey/namecom-cli/cmd/cmdutil"
	"github.com/spf13/cobra"
)

// TestEveryGroupRejectsUnknownSubcommands walks the real command tree and
// asserts that every parent group is wired through cmdutil.GroupCmd.
//
// The unit test in cmd/cmdutil covers the helper; this one covers the wiring,
// which is the part that actually regresses. Cobra checks for unknown commands
// only on the root, so a group added later without GroupCmd silently goes back
// to printing help and exiting 0 for a typo — and nothing else would notice.
func TestEveryGroupRejectsUnknownSubcommands(t *testing.T) {
	var walk func(*cobra.Command)
	walk = func(c *cobra.Command) {
		for _, sub := range c.Commands() {
			walk(sub)
		}
		// Only groups: commands that hold subcommands and are not the root.
		if !c.HasAvailableSubCommands() || !c.HasParent() {
			return
		}
		// cobra's generated `completion` tree is not ours to police.
		if c.Name() == "completion" {
			return
		}

		t.Run(c.CommandPath(), func(t *testing.T) {
			if c.Args == nil {
				t.Fatalf("%q has subcommands but no Args validator — a typo'd subcommand exits 0; wrap it in cmdutil.GroupCmd", c.CommandPath())
			}
			err := c.Args(c, []string{"nosuchsubcommand"})
			if err == nil {
				t.Fatalf("%q accepted an unknown subcommand", c.CommandPath())
			}
			var u *cmdutil.UsageError
			if !errors.As(err, &u) {
				t.Errorf("%q rejects unknown subcommands but not as a UsageError, so it exits 1 instead of 2: %v", c.CommandPath(), err)
			}
			if !strings.Contains(err.Error(), "nosuchsubcommand") {
				t.Errorf("%q error does not name the offending word: %v", c.CommandPath(), err)
			}
			if err := c.Args(c, nil); err != nil {
				t.Errorf("%q rejects being invoked bare, which should print help: %v", c.CommandPath(), err)
			}
		})
	}
	walk(rootCmd)
}
