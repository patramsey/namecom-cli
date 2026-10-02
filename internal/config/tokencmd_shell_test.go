package config

import (
	"reflect"
	"testing"
)

// TestShellArgv guards #183: token_cmd ran through `sh -c` everywhere, and a
// stock Windows install has no sh, so it failed with `exec: "sh": executable
// file not found`. On Windows it now runs through cmd.exe.
//
// The command line is handed to cmd.exe verbatim (tokencmd_windows.go), and
// /s with the outer quotes is what makes cmd keep everything between them as
// written; /d keeps an AutoRun registry entry from running first and printing
// into the token.
func TestShellArgv(t *testing.T) {
	const line = `op read "op://vault/namecom/token"`
	tests := []struct {
		goos     string
		wantProg string
		wantArgs []string
	}{
		{"linux", "sh", []string{"-c", line}},
		{"darwin", "sh", []string{"-c", line}},
		{"windows", "cmd.exe", []string{"/d", "/s", "/c", `"` + line + `"`}},
	}
	for _, tt := range tests {
		prog, args := shellArgv(tt.goos, line)
		if prog != tt.wantProg || !reflect.DeepEqual(args, tt.wantArgs) {
			t.Errorf("shellArgv(%s) = %s %q, want %s %q", tt.goos, prog, args, tt.wantProg, tt.wantArgs)
		}
	}
}
