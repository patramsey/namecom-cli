package config

import (
	"io/fs"
	"testing"
)

// TestExposedMode guards #181. On Windows Go reports every writable file as
// 0666, and Save's chmod 0600 only clears the read-only attribute, so the
// "accessible by other users" warning appeared on every interactive command
// and nothing the user could do would clear it. Unix mode bits say nothing
// about who can read a file there; the check is skipped.
func TestExposedMode(t *testing.T) {
	tests := []struct {
		goos string
		mode fs.FileMode
		want bool
	}{
		{"linux", 0o600, false},
		{"linux", 0o644, true},
		{"darwin", 0o640, true},
		{"darwin", 0o600, false},
		{"windows", 0o666, false},
		{"windows", 0o444, false},
	}
	for _, tt := range tests {
		if got := exposedMode(tt.goos, tt.mode); got != tt.want {
			t.Errorf("exposedMode(%s, %#o) = %v, want %v", tt.goos, tt.mode, got, tt.want)
		}
	}
}
