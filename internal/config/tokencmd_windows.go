//go:build windows

package config

import (
	"os/exec"
	"strings"
	"syscall"
)

// setRawCmdLine hands cmd.exe its command line exactly as shellArgv built it.
//
// Without it, Go joins the arguments with the C runtime's quoting rules,
// escaping the quotes shellArgv put around the token_cmd line — and cmd.exe
// does not undo that escaping, so `/s /c "…"` would no longer be the outer
// quotes /s strips.
func setRawCmdLine(cmd *exec.Cmd, prog string, args []string) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CmdLine: prog + " " + strings.Join(args, " "),
	}
}

// setProcessGroup is a no-op on Windows.
//
// There is no process group to kill here: the default CommandContext
// cancellation kills cmd.exe, and WaitDelay still bounds how long the CLI
// waits for anything the helper started.
func setProcessGroup(_ *exec.Cmd) {}
