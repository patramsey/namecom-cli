//go:build !windows

package config

import (
	"os/exec"
	"syscall"
)

// setProcessGroup puts the helper in its own process group and makes
// cancellation kill the whole group.
//
// exec.CommandContext only signals the direct child. A token_cmd written as a
// pipeline — `op read … | tr -d '\n'`, `vault read … | jq -r .token`, which is
// how most credential helpers are written — forks grandchildren that survive
// the shell's death, keep the inherited stdout/stderr descriptors open, and go
// on running for the full duration of whatever they were doing.
//
// WaitDelay alone stops the CLI blocking on them, but leaves them running.
// Killing the group actually ends the helper.
func setProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		// Negative pid signals the entire process group.
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
