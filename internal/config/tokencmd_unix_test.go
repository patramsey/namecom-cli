//go:build !windows

package config

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"
)

// ttyChildEnv marks the re-executed test binary running inside a pseudo-terminal.
const ttyChildEnv = "NAMECOM_TEST_TOKENCMD_TTY_CHILD"

// TestRunTokenCmd_ReadsTerminal guards issue #86: a token_cmd that prompts on
// the terminal must be able to read the answer.
//
// The helper used to be started in its own process group unconditionally. That
// group is a background group on the controlling terminal, so the first read of
// /dev/tty stopped the helper with SIGTTIN and the CLI sat there until the
// timeout killed it — the "interactive unlock" the timeout was sized for could
// never happen.
//
// A real controlling terminal is needed to see this, so the test re-runs
// itself under script(1), which allocates a pty, and types the answer into it.
// No pty library is a dependency; script ships with macOS and util-linux.
func TestRunTokenCmd_ReadsTerminal(t *testing.T) {
	if os.Getenv(ttyChildEnv) == "1" {
		// Short enough that a regression fails fast, long enough for a slow CI
		// runner to get the line through the pty.
		tokenCmdTimeout = 5 * time.Second
		fmt.Println("READY")
		tok, err := runTokenCmd(`read x </dev/tty; echo "got-$x"`)
		fmt.Printf("\nRESULT tok=%q err=%v\n", tok, err)
		return
	}

	scriptPath, err := exec.LookPath("script")
	if err != nil {
		t.Skip("script(1) not available to allocate a pty")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	child := []string{exe, "-test.run=^TestRunTokenCmd_ReadsTerminal$", "-test.count=1"}

	var args []string
	switch runtime.GOOS {
	case "linux":
		// util-linux: the command is a single shell string passed to -c.
		args = []string{"-qec", strings.Join(child, " "), "/dev/null"}
	case "darwin", "freebsd", "netbsd", "openbsd", "dragonfly":
		args = append([]string{"-q", "/dev/null"}, child...)
	default:
		t.Skipf("no known script(1) invocation for %s", runtime.GOOS)
	}

	cmd := exec.Command(scriptPath, args...) //nolint:gosec // script(1) from PATH re-running this test binary
	cmd.Env = append(os.Environ(), ttyChildEnv+"=1")
	// Type the answer only once the child is running, and hold stdin open until
	// it exits: script(1) turns EOF on its stdin into ^D on the terminal, and
	// input written before the pty is set up can be discarded.
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	outR, outW := io.Pipe()
	cmd.Stdout = outW
	cmd.Stderr = outW
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
		_ = outW.Close()
	}()

	var out strings.Builder
	sc := bufio.NewScanner(outR)
	for sc.Scan() {
		line := sc.Text()
		out.WriteString(line + "\n")
		if strings.TrimSpace(line) == "READY" {
			_, _ = io.WriteString(stdin, "secret\n")
		}
	}
	if err := <-done; err != nil {
		t.Fatalf("script: %v\n%s", err, out.String())
	}

	if !strings.Contains(out.String(), `RESULT tok="got-secret" err=<nil>`) {
		t.Errorf("token_cmd could not read the terminal; child output:\n%s", out.String())
	}
}

// withControllingTerminal pins hasControllingTerminal for the test.
func withControllingTerminal(t *testing.T, has bool) {
	t.Helper()
	prev := hasControllingTerminal
	hasControllingTerminal = func() bool { return has }
	t.Cleanup(func() { hasControllingTerminal = prev })
}

// TestSetProcessGroup_OnlyWithoutTerminal pins the decision behind #86: a new
// process group only when there is no terminal for the helper to prompt on.
func TestSetProcessGroup_OnlyWithoutTerminal(t *testing.T) {
	t.Run("no terminal", func(t *testing.T) {
		withControllingTerminal(t, false)
		cmd := exec.Command("true")
		setProcessGroup(cmd)
		if cmd.SysProcAttr == nil || !cmd.SysProcAttr.Setpgid {
			t.Error("without a terminal the helper should get its own process group")
		}
		if cmd.Cancel == nil {
			t.Error("without a terminal a timeout should kill the whole group")
		}
	})
	t.Run("terminal", func(t *testing.T) {
		withControllingTerminal(t, true)
		cmd := exec.Command("true")
		setProcessGroup(cmd)
		if cmd.SysProcAttr != nil && cmd.SysProcAttr.Setpgid {
			t.Error("with a terminal the helper must stay in the foreground group, or reading it stops the helper with SIGTTIN")
		}
	})
}
