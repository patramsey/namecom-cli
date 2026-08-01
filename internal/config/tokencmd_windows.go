//go:build windows

package config

import "os/exec"

// setProcessGroup is a no-op on Windows.
//
// token_cmd is executed through `sh -c`, which is not present on a stock
// Windows install, so this path is effectively unreachable there. The default
// CommandContext cancellation plus WaitDelay still bound how long the CLI will
// wait.
func setProcessGroup(_ *exec.Cmd) {}
