// Package domain implements the `namecom domain` command group.
package domain

import (
	coreapigo "github.com/namedotcom/core-api-go"
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
	cmdutil.MarkWrite(registerCmd, renewCmd, updateCmd, lockCmd, autorenewCmd, privacyCmd, setNSCmd, contactsSetCmd)
	cmdutil.SetResult(registerCmd, output.KeysOf(coreapigo.CreateDomainResponse{})...)
	cmdutil.SetResult(renewCmd, output.KeysOf(coreapigo.RenewDomainResponse{})...)
	// The domain as updated, or Unchanged's result when there was nothing to
	// send.
	cmdutil.SetResult(updateCmd, append(output.KeysOf(coreapigo.DomainResponsePayload{}), output.ResultKeys()...)...)
	// check offers a register in a terminal, but never under --yes.
	cmdutil.MarkList(searchCmd, checkCmd)
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

// confirm is cmdutil.Confirm, replaceable in tests. The prompts it guards (the
// register offer after `check`, the trademark-claim acknowledgement) are not
// RunWrite confirmations, so cmdutil.StubConfirm does not reach them, and a
// test that simulates a terminal would otherwise open a real form: huh falls
// back to /dev/tty or CONIN$ when stdin is not one, and on a Windows CI runner
// CONIN$ exists, so the form waited for input until the test timed out.
var confirm = cmdutil.Confirm

// derefBool dereferences a *bool, returning false for nil.
func derefBool(b *bool) bool {
	if b == nil {
		return false
	}
	return *b
}
