// Package domain implements the `namecom domain` command group.
package domain

import (
	"github.com/patramsey/namecom-cli/cmd/cmdutil"
	"github.com/patramsey/namecom-cli/internal/output"
	"github.com/spf13/cobra"
)

// Cmd is the `namecom domain` parent command.
var Cmd = &cobra.Command{
	Use:   "domain",
	Short: "Search, register, and manage your domains",
}

func init() {
	cmdutil.GroupCmd(Cmd)
	Cmd.AddCommand(
		listCmd,
		getCmd,
		checkCmd,
		searchCmd,
		registerCmd,
		updateCmd,
		lockCmd,
		autorenewCmd,
		privacyCmd,
		setNSCmd,
		contactsCmd,
		authCodeCmd,
		pricingCmd,
		renewCmd,
		claimsCmd,
		requirementsCmd,
	)
}

func confirm(out *output.Config, yes bool, msg string) (bool, error) {
	return cmdutil.Confirm(out, yes, msg)
}

// derefBool dereferences a *bool, returning false for nil.
func derefBool(b *bool) bool {
	if b == nil {
		return false
	}
	return *b
}
