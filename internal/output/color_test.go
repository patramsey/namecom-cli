package output

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// TestApplyColorProfile pins issue #175. --color always made ColorEnabled
// return true, but lipgloss detects the terminal on its own and, with stdout
// piped, renders every style as plain text — so `--color always | cat` had no
// escape codes. --color never is the converse: lipgloss must not colour
// anything a caller styles without checking ColorEnabled.
func TestApplyColorProfile(t *testing.T) {
	prev := lipgloss.ColorProfile()
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })

	t.Run("always colours piped output", func(t *testing.T) {
		lipgloss.SetColorProfile(termenv.Ascii) // what a pipe is detected as
		(&Config{Color: ColorAlways}).ApplyColorProfile()
		if got := styleError.Render("x"); !strings.Contains(got, "\x1b[") {
			t.Errorf("--color always rendered %q, want ANSI escapes", got)
		}
	})

	// A terminal that already supports colour keeps its own profile: forcing
	// one must not downgrade a truecolor terminal.
	t.Run("always keeps a detected colour profile", func(t *testing.T) {
		lipgloss.SetColorProfile(termenv.TrueColor)
		(&Config{Color: ColorAlways}).ApplyColorProfile()
		if got := lipgloss.ColorProfile(); got != termenv.TrueColor {
			t.Errorf("profile = %v, want TrueColor kept", got)
		}
	})

	t.Run("never strips colour on a terminal", func(t *testing.T) {
		lipgloss.SetColorProfile(termenv.ANSI256)
		(&Config{Color: ColorNever}).ApplyColorProfile()
		if got := styleError.Render("x"); strings.Contains(got, "\x1b[") {
			t.Errorf("--color never rendered %q, want no escapes", got)
		}
	})

	t.Run("auto leaves detection alone", func(t *testing.T) {
		lipgloss.SetColorProfile(termenv.ANSI)
		(&Config{Color: ColorAuto}).ApplyColorProfile()
		if got := lipgloss.ColorProfile(); got != termenv.ANSI {
			t.Errorf("profile = %v, want ANSI unchanged", got)
		}
	})
}
