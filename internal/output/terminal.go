package output

import (
	"os"
	"strconv"
	"strings"

	// Imported for its init order only: Bubble Tea's init queries the
	// terminal, and this package's init must run after it to undo noquery.
	_ "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/patramsey/namecom-cli/internal/output/noquery"
)

func init() {
	noquery.Restore()

	// The colour profile is read from the environment, not the terminal, but
	// lipgloss caches it the first time it is asked: if anything asked while
	// noquery had TERM set to "dumb", every style would render plain. Set it
	// from the restored environment so that cannot happen.
	r := lipgloss.DefaultRenderer()
	r.SetColorProfile(r.Output().EnvColorProfile())

	// AdaptiveColor asks the terminal for its background colour the first time
	// one is rendered, unless the answer has been set. Set it, so nothing this
	// CLI renders ever queries the terminal.
	r.SetHasDarkBackground(hasDarkBackground(os.Getenv("COLORFGBG")))
}

// hasDarkBackground decides the background from COLORFGBG, which rxvt,
// Konsole, iTerm2 and others set to "fg;bg" (sometimes "fg;default;bg") in
// ANSI colour numbers. Without it, or with a value it cannot read, the answer
// is dark: that is the common case, and the colours this package picks for a
// dark background stay legible on a light one.
//
// The terminal is not asked. Asking means writing a query to stdout and
// waiting for a reply, which stalls a terminal that does not answer and leaks
// a late reply into the shell.
func hasDarkBackground(colorfgbg string) bool {
	i := strings.LastIndex(colorfgbg, ";")
	if i < 0 {
		return true
	}
	bg, err := strconv.Atoi(colorfgbg[i+1:])
	if err != nil {
		return true
	}
	// 7 is white and 9–15 the bright colours; the rest are black and the
	// dark colours.
	return bg != 7 && (bg < 9 || bg > 15)
}
