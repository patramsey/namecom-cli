// Package domain implements the `namecom domain` command group.
package domain

import (
	"github.com/namedotcom/namecom-cli/cmd/cmdutil"
	"github.com/spf13/cobra"
)

// Cmd is the `namecom domain` parent command.
var Cmd = &cobra.Command{
	Use:   "domain",
	Short: "Search, register, and manage your domains",
}

func init() {
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
	)
}

func confirm(yes bool, msg string) (bool, error) {
	return cmdutil.Confirm(yes, msg)
}

// ptr returns a pointer to v, used for optional struct fields.
func ptr[T any](v T) *T { return &v }

// derefBool dereferences a *bool, returning false for nil.
func derefBool(b *bool) bool {
	if b == nil {
		return false
	}
	return *b
}
