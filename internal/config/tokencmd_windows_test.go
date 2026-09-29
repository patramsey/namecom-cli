//go:build windows

package config

import "testing"

// withControllingTerminal is a no-op on Windows, where setProcessGroup does not
// consult a terminal.
func withControllingTerminal(t *testing.T, _ bool) { t.Helper() }
