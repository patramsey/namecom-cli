//go:build !windows

package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeConfigMode writes contents to a config file with mode perm — chmod'd
// after writing, so the umask cannot narrow it — and points NAMECOM_CONFIG at
// it.
func writeConfigMode(t *testing.T, contents string, perm os.FileMode) string {
	t.Helper()
	isolateHome(t)
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, perm); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NAMECOM_CONFIG", path)
	return path
}

// pretendOwnedByOther makes the files this test creates look owned by
// another user, which a test cannot arrange for real without root.
func pretendOwnedByOther(t *testing.T) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("root-owned files are accepted, and as root every test file is root's")
	}
	prev := currentUID
	other := os.Geteuid() + 1
	currentUID = func() int { return other }
	t.Cleanup(func() { currentUID = prev })
}

// TestLoad_RefusesConfigOthersCanWrite guards #342: a 0666 config with a
// token_cmd ran the command on `domain list` — anyone who could write the
// file could run commands as the user, silently when stderr was not a
// terminal. Load now refuses the file, so its token_cmd is never reached.
func TestLoad_RefusesConfigOthersCanWrite(t *testing.T) {
	for _, perm := range []os.FileMode{0o666, 0o620, 0o602} {
		path := writeConfigMode(t, "default: a\nprofiles:\n  a:\n    username: u\n    token_cmd: echo tok\n", perm)
		TakeWarnings()

		f, err := Load()
		var ue *UnsafeFileError
		if !errors.As(err, &ue) {
			t.Fatalf("mode %#o: Load = %v, %v; want *UnsafeFileError", perm, f, err)
		}
		if !strings.Contains(err.Error(), "writable by other users") {
			t.Errorf("mode %#o: error = %q, want it to say who can write it", perm, err)
		}
		if want := "chmod 600 " + path; !strings.Contains(ue.UserHint(), want) {
			t.Errorf("mode %#o: hint = %q, want it to contain %q", perm, ue.UserHint(), want)
		}
	}
}

// TestLoad_RefusesConfigOwnedByOther: a file someone else owns is theirs to
// rewrite, token_cmd included.
func TestLoad_RefusesConfigOwnedByOther(t *testing.T) {
	writeConfigMode(t, "profiles: {}\n", 0o600)
	pretendOwnedByOther(t)

	_, err := Load()
	var ue *UnsafeFileError
	if !errors.As(err, &ue) || !strings.Contains(err.Error(), "owned by another user") {
		t.Fatalf("Load = %v, want *UnsafeFileError about the owner", err)
	}
	if !strings.Contains(ue.UserHint(), "chown") {
		t.Errorf("hint = %q, want a chown command", ue.UserHint())
	}
}

// TestLoad_WarnsWhenOthersCanRead keeps the read-bits warning, now raised
// whatever stderr is, through TakeWarnings: Load printed it only to a
// terminal, so a script never learned its token file was readable.
func TestLoad_WarnsWhenOthersCanRead(t *testing.T) {
	path := writeConfigMode(t, "profiles: {}\n", 0o644)
	TakeWarnings()

	if _, err := Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, err := Load(); err != nil {
		t.Fatalf("second Load: %v", err)
	}
	w := TakeWarnings()
	if len(w) != 1 {
		t.Fatalf("warnings = %q, want exactly one however often Load ran", w)
	}
	if !strings.Contains(w[0], "readable by other users") || !strings.Contains(w[0], "chmod 600 "+path) {
		t.Errorf("warning = %q, want it to say so and give the chmod", w[0])
	}
	if w := TakeWarnings(); len(w) != 0 {
		t.Errorf("TakeWarnings did not forget: %q", w)
	}
}

func TestLoad_PrivateConfigIsQuiet(t *testing.T) {
	writeConfigMode(t, "profiles: {}\n", 0o600)
	TakeWarnings()
	if _, err := Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if w := TakeWarnings(); len(w) != 0 {
		t.Errorf("warnings = %q, want none for a 0600 config", w)
	}
}

// TestLoad_DevNull: NAMECOM_CONFIG=/dev/null isolates a run from any config.
// It is a root-owned 0666 device, which the mode checks must not refuse.
func TestLoad_DevNull(t *testing.T) {
	if _, err := os.Stat(os.DevNull); err != nil {
		t.Skip(err)
	}
	isolateHome(t)
	t.Setenv("NAMECOM_CONFIG", os.DevNull)
	TakeWarnings()
	f, err := Load()
	if err != nil {
		t.Fatalf("Load(/dev/null): %v", err)
	}
	if len(f.Profiles) != 0 {
		t.Errorf("profiles = %v, want none", f.Profiles)
	}
	if w := TakeWarnings(); len(w) != 0 {
		t.Errorf("warnings = %q, want none for /dev/null", w)
	}
}

// TestSave_RefusesFileOwnedByOther guards #342: writing into a file someone
// else owns left the token readable when the chmod that followed failed.
func TestSave_RefusesFileOwnedByOther(t *testing.T) {
	path := writeConfigMode(t, "profiles: {}\n", 0o600)
	pretendOwnedByOther(t)

	err := Save(&File{Profiles: map[string]Profile{"a": {Username: "u", Token: "SECRET"}}})
	var ue *UnsafeFileError
	if !errors.As(err, &ue) || !strings.Contains(err.Error(), "owned by another user") {
		t.Fatalf("Save = %v, want *UnsafeFileError about the owner", err)
	}
	if data, _ := os.ReadFile(path); strings.Contains(string(data), "SECRET") {
		t.Error("Save wrote the token anyway")
	}
}

// TestSave_FailureKeepsOldConfig: when the new file cannot be written, the
// old one is untouched — it used to be truncated before the write began.
func TestSave_FailureKeepsOldConfig(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores the directory permission this test relies on")
	}
	path := writeConfigMode(t, "default: a\nprofiles:\n  a:\n    username: u\n    token: OLD\n", 0o600)
	dir := filepath.Dir(path)
	if err := os.Chmod(dir, 0o500); err != nil { //nolint:gosec // a read-only directory, so the save fails
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) }) //nolint:gosec // restores t.TempDir's mode for its cleanup

	if err := Save(&File{Default: "a", Profiles: map[string]Profile{"a": {Username: "u", Token: "NEW"}}}); err == nil {
		t.Fatal("Save into a read-only directory succeeded")
	}
	data, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(data), "OLD") {
		t.Errorf("old config lost after a failed save: %q, %v", data, err)
	}
}

// TestSave_TightensExposedConfig: an existing 0644 config comes back 0600,
// because the new file is created 0600 and renamed over it — the token is
// never written into the readable file at all.
func TestSave_TightensExposedConfig(t *testing.T) {
	path := writeConfigMode(t, "profiles: {}\n", 0o644)
	if err := Save(&File{Profiles: map[string]Profile{"a": {Username: "u", Token: "T"}}}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	wantPrivateMode(t, path)
}
