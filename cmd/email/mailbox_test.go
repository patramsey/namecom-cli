package email

import (
	"errors"
	"testing"

	"github.com/patramsey/namecom-cli/cmd/cmdutil"
	"github.com/spf13/cobra"
)

// TestEmail_MailboxCheckedLocally pins #361: a mailbox of "*" reached the
// path as %2A, name.com redirected it to an HTML 403 page, and the CLI
// reported an auth failure (exit 3), so a script concluded its credentials
// were broken. "?", "/", "." and ".." reached the path too, and `email get`
// checked nothing at all. Every command that takes a mailbox now refuses
// these as a usage error, before any request.
func TestEmail_MailboxCheckedLocally(t *testing.T) {
	commands := []struct {
		name  string
		setup func(*testing.T) *cobra.Command
		run   func(*cobra.Command, []string) error
	}{
		{"get", func(t *testing.T) *cobra.Command { return cmdForEmailGet(t, neverCalledServer(t)) }, runGet},
		{"create", func(t *testing.T) *cobra.Command {
			createEmailTo = "dest@example.org"
			t.Cleanup(func() { createEmailTo = "" })
			return cmdForEmailCreate(t, neverCalledServer(t))
		}, runCreate},
		{"update", func(t *testing.T) *cobra.Command {
			cmd := cmdForEmailUpdate(t, neverCalledServer(t))
			if err := cmd.ParseFlags([]string{"--to", "dest@example.org"}); err != nil {
				t.Fatalf("ParseFlags: %v", err)
			}
			return cmd
		}, runUpdate},
		{"delete", func(t *testing.T) *cobra.Command { return cmdForEmailDelete(t, neverCalledServer(t)) }, runDelete},
	}
	for _, c := range commands {
		for _, box := range []string{"*", "?", "/", ".", "..", "a/b", "bad box", "info@example.com"} {
			t.Run(c.name+" "+box, func(t *testing.T) {
				err := c.run(c.setup(t), []string{"example.com", box})
				if _, ok := errors.AsType[*cmdutil.UsageError](err); !ok {
					t.Fatalf("err = %v (%T), want a usage error", err, err)
				}
			})
		}
	}
}
