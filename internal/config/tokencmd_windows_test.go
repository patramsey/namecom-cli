//go:build windows

package config

import "testing"

// withControllingTerminal is a no-op on Windows, where setProcessGroup does not
// consult a terminal.
func withControllingTerminal(t *testing.T, _ bool) { t.Helper() }

// TestRunTokenCmd_CmdExe runs a helper through cmd.exe (#183), with the quoting
// a real one uses: the quotes must reach the command as written.
func TestRunTokenCmd_CmdExe(t *testing.T) {
	tok, err := runTokenCmd(`echo "s3cret"& rem trailing`)
	if err != nil {
		t.Fatalf("runTokenCmd via cmd.exe: %v", err)
	}
	if tok != `"s3cret"` {
		t.Errorf("token = %q, want %q", tok, `"s3cret"`)
	}
}
