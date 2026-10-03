package output

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestHasDarkBackground pins the decision that replaced asking the terminal
// (issue #187): lipgloss asked for the background colour with `ESC]11;?` and
// `ESC[6n` on stdout, which stalls a terminal that does not answer.
func TestHasDarkBackground(t *testing.T) {
	for _, tc := range []struct {
		colorfgbg string
		want      bool
	}{
		{"", true},      // unset: assume dark
		{"15;0", true},  // white on black
		{"0;15", false}, // black on bright white
		{"0;7", false},  // black on white
		{"0;default;15", false},
		{"15;default;0", true},
		{"7;8", true},       // bright black is dark
		{"0;9", false},      // bright red is light
		{"0;default", true}, // unreadable: assume dark
		{"garbage", true},
	} {
		if got := hasDarkBackground(tc.colorfgbg); got != tc.want {
			t.Errorf("hasDarkBackground(%q) = %v, want %v", tc.colorfgbg, got, tc.want)
		}
	}
}

// TestNoQueryInitOrder checks the init order noquery relies on: its init must
// run before Bubble Tea's, which queries the terminal, and this package's
// init must put TERM back afterwards. It re-runs this test binary with
// GODEBUG=inittrace=1, which prints each package's init as it runs.
func TestNoQueryInitOrder(t *testing.T) {
	const probe = "xterm-noquery-probe"
	if os.Getenv("NAMECOM_NOQUERY_CHILD") == "1" {
		if got := os.Getenv("TERM"); got != probe {
			t.Fatalf("TERM after init = %q, want %q restored", got, probe)
		}
		return
	}

	cmd := exec.Command(os.Args[0], "-test.run=^TestNoQueryInitOrder$", "-test.count=1") //nolint:gosec // re-runs this test binary
	cmd.Env = append(os.Environ(), "GODEBUG=inittrace=1", "NAMECOM_NOQUERY_CHILD=1", "TERM="+probe)
	b, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("child: %v\n%s", err, b)
	}
	order := map[string]int{}
	for i, line := range strings.Split(string(b), "\n") {
		if f := strings.Fields(line); len(f) > 1 && f[0] == "init" {
			order[f[1]] = i
		}
	}
	nq, ok1 := order["github.com/patramsey/namecom-cli/internal/output/noquery"]
	bt, ok2 := order["github.com/charmbracelet/bubbletea"]
	out, ok3 := order["github.com/patramsey/namecom-cli/internal/output"]
	if !ok1 || !ok2 || !ok3 {
		t.Fatalf("inittrace is missing a package (noquery %v, bubbletea %v, output %v):\n%s", ok1, ok2, ok3, b)
	}
	if nq >= bt || bt >= out {
		t.Errorf("init order: noquery line %d, bubbletea line %d, output line %d; want noquery < bubbletea < output", nq, bt, out)
	}
}

// TestNoVirtualTerminal: on a Windows console that refuses virtual terminal
// processing (issue #187), escape codes print as text, so auto colour and the
// spinner are off. --color always is an explicit request and still colours.
func TestNoVirtualTerminal(t *testing.T) {
	if _, set := os.LookupEnv("NO_COLOR"); set {
		t.Skip("NO_COLOR is set, so auto colour is off regardless")
	}
	t.Setenv("CLICOLOR_FORCE", "1")
	if !(&Config{Color: ColorAuto}).ColorEnabled() {
		t.Fatal("precondition: CLICOLOR_FORCE=1 should enable auto colour")
	}

	prev := vtSupported
	vtSupported = false
	t.Cleanup(func() { vtSupported = prev })

	if (&Config{Color: ColorAuto}).ColorEnabled() {
		t.Error("ColorEnabled() in auto mode = true without VT processing, want false")
	}
	if !(&Config{Color: ColorAlways}).ColorEnabled() {
		t.Error("ColorEnabled() with --color always = false, want true")
	}
	if spinnerTTY() {
		t.Error("spinnerTTY() = true without VT processing, want false")
	}
}
