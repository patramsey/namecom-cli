//go:build windows

package config

import (
	"strings"
	"testing"
)

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

// TestRunTokenCmd_CmdExeMultiLine is TestRunTokenCmd_RejectsMultiLineOutput
// for cmd.exe, whose echo ends every line with \r\n: a helper that prints two
// lines is still rejected, and the CRLF after a lone token is still trimmed.
func TestRunTokenCmd_CmdExeMultiLine(t *testing.T) {
	if tok, err := runTokenCmd(`echo s3cret& echo user: me`); err == nil {
		t.Errorf("runTokenCmd(two lines) = %q, want an error", tok)
	} else if strings.Contains(err.Error(), "s3cret") {
		t.Errorf("error echoes the helper's output: %v", err)
	}
	if tok, err := runTokenCmd(`echo s3cret`); err != nil || tok != "s3cret" {
		t.Errorf("runTokenCmd(echo s3cret) = %q, %v; want s3cret", tok, err)
	}
}
