// Package noquery stops termenv from querying the terminal while packages are
// initialized.
//
// Bubble Tea, which huh pulls in for the prompts, calls
// lipgloss.HasDarkBackground from its init function. On a terminal that makes
// termenv write `ESC]11;?` (the background colour query) and `ESC[6n` (a cursor
// position query) to stdout and wait for the answers — for every command,
// before any code of ours runs, whatever --color or NO_COLOR say. A terminal
// that does not answer stalls the command, and one that answers late leaves
// the reply in the shell's input.
//
// termenv skips the queries when TERM is "dumb", so this package's init sets
// it, and package output puts the real value back with Restore. That only
// works if this init runs before Bubble Tea's, which the Go spec guarantees as
// long as this package imports nothing but os: packages are initialized in
// import-path order among those whose imports are done, so this one is ready
// as soon as os is, and it sorts before os/signal and os/exec, which Bubble
// Tea imports and which cannot be ready any earlier. Keep it that way —
// importing anything else here can let Bubble Tea's init run first.
// TestNoQueryInitOrder in package output checks the order.
package noquery

import "os"

var (
	term    string
	hadTerm bool
)

func init() {
	term, hadTerm = os.LookupEnv("TERM")
	_ = os.Setenv("TERM", "dumb")
}

// Restore puts TERM back to what it was before this package's init. Package
// output calls it from its own init, which runs after Bubble Tea's.
func Restore() {
	if hadTerm {
		_ = os.Setenv("TERM", term)
		return
	}
	_ = os.Unsetenv("TERM")
}
