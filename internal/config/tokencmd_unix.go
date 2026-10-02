//go:build !windows

package config

import (
	"os"
	"os/exec"
	"syscall"
)

// hasControllingTerminal reports whether this process has a terminal a helper
// could prompt on. Opening /dev/tty succeeds exactly when there is one, however
// stdin and stdout are redirected — and a prompting helper reads /dev/tty
// directly, because its stdin is not the terminal. Overridable in tests.
var hasControllingTerminal = func() bool {
	f, err := os.Open("/dev/tty")
	if err != nil {
		return false
	}
	_ = f.Close()
	return true
}

// setRawCmdLine is a no-op outside Windows: sh receives its arguments as an
// argv array, with nothing to re-parse.
func setRawCmdLine(_ *exec.Cmd, _ string, _ []string) {}

// setProcessGroup puts the helper in its own process group and makes
// cancellation kill the whole group — unless there is a controlling terminal
// the helper might prompt on.
//
// exec.CommandContext only signals the direct child. A token_cmd written as a
// pipeline — `op read … | tr -d '\n'`, `vault read … | jq -r .token`, which is
// how most credential helpers are written — forks grandchildren that survive
// the shell's death, keep the inherited stdout/stderr descriptors open, and go
// on running for the full duration of whatever they were doing.
//
// WaitDelay alone stops the CLI blocking on them, but leaves them running.
// Killing the group actually ends the helper.
//
// But a new process group is a background group on the terminal, and a
// background process that reads the terminal is stopped with SIGTTIN: a helper
// prompting for a passphrase hung until the timeout (#86). So when there is a
// controlling terminal the helper stays in the CLI's own foreground group.
//
// The cost is that a timeout on that path kills only the shell. WaitDelay still
// bounds how long the CLI waits, but the rest of a pipeline is left to finish
// on its own. Handing the helper the terminal instead (SysProcAttr.Foreground)
// would keep the group kill, but the CLI would then have to take the terminal
// back from a background group on every exit path — Ctrl-C at the prompt
// included — or leave the user's shell without it. Staying in the CLI's group
// also means Ctrl-C at the prompt reaches the helper, which it did not before.
func setProcessGroup(cmd *exec.Cmd) {
	if hasControllingTerminal() {
		return
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		// Negative pid signals the entire process group.
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
