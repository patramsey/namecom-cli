//go:build windows

package output

import (
	"os"

	"golang.org/x/sys/windows"
)

// enableVirtualTerminal turns on virtual terminal processing for stdout and
// stderr, so the console interprets escape codes instead of printing them.
// Windows 10 consoles support it but do not always have it on; nothing else in
// the CLI turned it on, and the spinner writes `\r\033[K` itself.
//
// It reports false when either stream is a console that refuses the mode — an
// older conhost. A stream that is not a console (a pipe, a file) needs nothing.
// The mode is left on at exit: the shell sets the console mode it wants when it
// takes the console back.
func enableVirtualTerminal() bool {
	ok := true
	for _, f := range []*os.File{os.Stdout, os.Stderr} {
		h := windows.Handle(f.Fd())
		var mode uint32
		if err := windows.GetConsoleMode(h, &mode); err != nil {
			continue
		}
		if mode&windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING != 0 {
			continue
		}
		if err := windows.SetConsoleMode(h, mode|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING); err != nil {
			ok = false
		}
	}
	return ok
}
