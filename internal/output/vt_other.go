//go:build !windows

package output

// enableVirtualTerminal is a no-op outside Windows: terminals there interpret
// escape codes without being asked.
func enableVirtualTerminal() bool { return true }
