//go:build !windows

package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
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
	// The file is read-only as well, so the in-place fallback cannot write
	// it either.
	path := writeConfigMode(t, "default: a\nprofiles:\n  a:\n    username: u\n    token: OLD\n", 0o400)
	readOnlyDir(t, filepath.Dir(path))

	err := Save(&File{Default: "a", Profiles: map[string]Profile{"a": {Username: "u", Token: "NEW"}}})
	var fe *FileError
	if !errors.As(err, &fe) {
		t.Fatalf("Save = %v, want *FileError", err)
	}
	// It names the directory, not the temporary file the user never made.
	if !strings.Contains(err.Error(), "is not writable") || strings.Contains(err.Error(), ".tmp-") {
		t.Errorf("error = %q, want it to say the directory is not writable", err)
	}
	if !strings.Contains(fe.UserHint(), "chmod u+w") {
		t.Errorf("hint = %q, want the chmod that fixes it", fe.UserHint())
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

// readOnlyDir makes dir read-only for the rest of the test, skipping as root,
// which the permission does not stop.
func readOnlyDir(t *testing.T, dir string) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("root ignores the directory permission this test relies on")
	}
	if err := os.Chmod(dir, 0o500); err != nil { //nolint:gosec // a read-only directory, so no new file can be made in it
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) }) //nolint:gosec // restores t.TempDir's mode for its cleanup
}

// TestSave_ReadOnlyDirWritesInPlace guards #363: with the directory
// read-only and the file writable, saving worked before the atomic save
// (#342) and then failed naming a temporary file. It writes the file in
// place, still ending 0600.
func TestSave_ReadOnlyDirWritesInPlace(t *testing.T) {
	path := writeConfigMode(t, "default: a\nprofiles:\n  a:\n    username: u\n    token: OLD\n", 0o644)
	dir := filepath.Dir(path)
	readOnlyDir(t, dir)
	TakeWarnings()
	if _, err := Load(); err != nil { // raises the readable-by-others warning
		t.Fatal(err)
	}

	if err := Save(&File{Default: "a", Profiles: map[string]Profile{"a": {Username: "u", Token: "NEW"}}}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if data, _ := os.ReadFile(path); !strings.Contains(string(data), "NEW") {
		t.Errorf("config = %q, want the new token", data)
	}
	wantPrivateMode(t, path)
	if w := TakeWarnings(); len(w) != 0 {
		t.Errorf("warnings = %q; the save made the file 0600, so none is due", w)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("directory holds %d entries, want only the config", len(entries))
	}
}

// TestSave_ReadOnlyDirNoFile: with nothing to write in place, the error says
// the directory must be writable.
func TestSave_ReadOnlyDirNoFile(t *testing.T) {
	isolateHome(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	t.Setenv("NAMECOM_CONFIG", path)
	readOnlyDir(t, dir)

	err := Save(&File{Profiles: map[string]Profile{"a": {Username: "u", Token: "T"}}})
	var fe *FileError
	if !errors.As(err, &fe) || !strings.Contains(err.Error(), dir+" is not writable") {
		t.Fatalf("Save = %v, want *FileError saying %s is not writable", err, dir)
	}
}

// TestSave_ClearsExposedWarning guards #363: "readable by other users (0644)"
// printed after the save had already made the file 0600.
func TestSave_ClearsExposedWarning(t *testing.T) {
	writeConfigMode(t, "profiles: {}\n", 0o644)
	TakeWarnings()
	if _, err := Load(); err != nil {
		t.Fatal(err)
	}
	if err := Save(&File{Profiles: map[string]Profile{"a": {Username: "u", Token: "T"}}}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if w := TakeWarnings(); len(w) != 0 {
		t.Errorf("warnings = %q, want none after the save tightened the mode", w)
	}
}

// TestLoad_FIFODoesNotBlock guards #363: a named pipe at NAMECOM_CONFIG hung
// in os.Open, before the regular-file check could refuse it.
func TestLoad_FIFODoesNotBlock(t *testing.T) {
	isolateHome(t)
	path := filepath.Join(t.TempDir(), "fifo")
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Skipf("cannot make a FIFO here: %v", err)
	}
	t.Setenv("NAMECOM_CONFIG", path)

	done := make(chan error, 1)
	go func() { _, err := Load(); done <- err }()
	select {
	case err := <-done:
		var fe *FileError
		if !errors.As(err, &fe) || !strings.Contains(err.Error(), "named pipe") {
			t.Errorf("Load = %v, want *FileError saying it is a named pipe", err)
		}
	case <-time.After(5 * time.Second):
		// Unblock the open so the goroutine can finish.
		if w, err := os.OpenFile(path, os.O_WRONLY, 0); err == nil {
			_ = w.Close()
		}
		t.Fatal("Load blocked on a FIFO")
	}
}
