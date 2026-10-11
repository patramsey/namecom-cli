package config

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The tests in this file run on every platform, Windows included; the Unix
// mode and ownership cases are in filesafety_unix_test.go.

// TestSave_ReplacesAtomically guards #342: Save truncated the config and
// wrote it in place, so a crash or a full disk mid-write lost every profile.
// It now renames a finished file over the old one, which on Windows needs
// MoveFileEx's replace-existing behavior — exercised here by saving twice.
func TestSave_ReplacesAtomically(t *testing.T) {
	isolateHome(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	t.Setenv("NAMECOM_CONFIG", path)

	for _, tok := range []string{"FIRST", "SECOND"} {
		if err := Save(&File{Default: "a", Profiles: map[string]Profile{"a": {Username: "u", Token: tok}}}); err != nil {
			t.Fatalf("Save(%s): %v", tok, err)
		}
	}
	f, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := f.Profiles["a"].Token; got != "SECOND" {
		t.Errorf("token = %q, want SECOND", got)
	}
	wantPrivateMode(t, path)

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("directory holds %v; want only config.yaml, no leftover temp files", names)
	}
}

// symlinkOrSkip makes link point at target, skipping the test where the OS
// will not let this user create one (Windows without developer mode).
func symlinkOrSkip(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("cannot create a symlink here: %v", err)
	}
}

// TestSave_RefusesSymlink guards #342: a dangling symlink at NAMECOM_CONFIG
// made `auth login` create the link's target, token and all.
func TestSave_RefusesSymlink(t *testing.T) {
	isolateHome(t)
	dir := t.TempDir()

	t.Run("dangling", func(t *testing.T) {
		target := filepath.Join(dir, "elsewhere.yaml")
		link := filepath.Join(dir, "dangling.yaml")
		symlinkOrSkip(t, target, link)
		t.Setenv("NAMECOM_CONFIG", link)

		err := Save(&File{Profiles: map[string]Profile{"a": {Username: "u", Token: "SECRET"}}})
		var ue *UnsafeFileError
		if !errors.As(err, &ue) {
			t.Fatalf("Save = %v, want *UnsafeFileError", err)
		}
		if !strings.Contains(err.Error(), "symbolic link") {
			t.Errorf("error = %q, want it to say the path is a symbolic link", err)
		}
		if _, err := os.Lstat(target); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("Save created the link's target (%v); it must not write through a link", err)
		}
	})

	t.Run("to an existing file", func(t *testing.T) {
		target := filepath.Join(dir, "real.yaml")
		if err := os.WriteFile(target, []byte("profiles: {}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(dir, "link.yaml")
		symlinkOrSkip(t, target, link)
		t.Setenv("NAMECOM_CONFIG", link)

		err := Save(&File{Profiles: map[string]Profile{"a": {Username: "u", Token: "SECRET"}}})
		var ue *UnsafeFileError
		if !errors.As(err, &ue) {
			t.Fatalf("Save = %v, want *UnsafeFileError", err)
		}
		// The hint names the real file, so following it is one step.
		resolved, _ := filepath.EvalSymlinks(target)
		if !strings.Contains(ue.UserHint(), "NAMECOM_CONFIG=") || !strings.Contains(ue.UserHint(), filepath.Base(resolved)) {
			t.Errorf("hint = %q, want it to suggest NAMECOM_CONFIG=<target>", ue.UserHint())
		}
		if data, _ := os.ReadFile(target); strings.Contains(string(data), "SECRET") {
			t.Error("Save wrote through the symlink")
		}
		if info, err := os.Lstat(link); err != nil || info.Mode()&fs.ModeSymlink == 0 {
			t.Errorf("the link was replaced (%v); a refused save must leave it alone", err)
		}
	})
}

// TestSave_RefusesNonRegularFile: a directory at the config path is an error
// naming the problem, not a rename failure.
func TestSave_RefusesNonRegularFile(t *testing.T) {
	isolateHome(t)
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NAMECOM_CONFIG", path)
	var ue *UnsafeFileError
	if err := Save(&File{}); !errors.As(err, &ue) {
		t.Fatalf("Save = %v, want *UnsafeFileError", err)
	}
}

func TestWritableByOthers(t *testing.T) {
	tests := []struct {
		goos string
		mode fs.FileMode
		want bool
	}{
		{"linux", 0o600, false},
		{"linux", 0o644, false},
		{"linux", 0o640, false},
		{"linux", 0o620, true},
		{"darwin", 0o602, true},
		{"darwin", 0o666, true},
		{"windows", 0o666, false},
	}
	for _, tt := range tests {
		if got := writableByOthers(tt.goos, tt.mode); got != tt.want {
			t.Errorf("writableByOthers(%s, %#o) = %v, want %v", tt.goos, tt.mode, got, tt.want)
		}
	}
}

func TestShellQuote(t *testing.T) {
	tests := map[string]string{
		"/home/u/.config/namecom/config.yaml":                      "/home/u/.config/namecom/config.yaml",
		"/Users/u/Library/Application Support/namecom/config.yaml": "'/Users/u/Library/Application Support/namecom/config.yaml'",
		"/tmp/it's":  `'/tmp/it'\''s'`,
		"/tmp/$HOME": "'/tmp/$HOME'",
		"":           "''",
	}
	for in, want := range tests {
		if got := shellQuote(in); got != want {
			t.Errorf("shellQuote(%q) = %s, want %s", in, got, want)
		}
	}
}
