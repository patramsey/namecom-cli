package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

// UnsafeFileError is returned when the config file is not safe to read or to
// write: writable by other users, owned by someone else, or — for a write — a
// symbolic link. The CLI classifies it as a credential problem (exit 3): the
// file is where the credentials live, and refusing it means none can be used.
type UnsafeFileError struct {
	// Op is "use" for a read, "write" for a save.
	Op   string
	Path string
	// Reason completes "it …": "is writable by other users (mode 0666)".
	Reason string
	// Fix is the command or step that resolves it, shown as the hint.
	Fix string
}

func (e *UnsafeFileError) Error() string {
	return fmt.Sprintf("refusing to %s config %s: it %s", e.Op, e.Path, e.Reason)
}

// UserHint is the fix, printed with the error.
func (e *UnsafeFileError) UserHint() string { return e.Fix }

// checkReadable refuses a config another user could have written. A profile's
// token_cmd is run as a shell command, so anyone who can write the file can
// run commands as the user — ssh refuses such a config for the same reason.
//
// info must come from the opened file (f.Stat), not from the path, so the file
// checked is the file read even if the path is swapped in between. That is
// also why the directory is not checked: whoever can write it can only
// replace the file with one they own, which the owner check refuses.
func checkReadable(path string, info fs.FileInfo) error {
	if uid, ok := fileOwner(info); ok && uid != currentUID() && uid != 0 {
		return &UnsafeFileError{
			Op: "use", Path: path,
			Reason: fmt.Sprintf("is owned by another user (uid %d)", uid),
			Fix:    fmt.Sprintf("if the file is yours, run 'sudo chown \"$(id -un)\" %s'; otherwise point NAMECOM_CONFIG at a config you own", shellQuote(path)),
		}
	}
	// Mode bits only for a regular file: NAMECOM_CONFIG=/dev/null, which
	// isolates a run from any config, is a root-owned 0666 device.
	if info.Mode().IsRegular() && writableByOthers(runtime.GOOS, info.Mode()) {
		return &UnsafeFileError{
			Op: "use", Path: path,
			Reason: fmt.Sprintf("is writable by other users (mode %#o), who could make namecom run commands as you through token_cmd", info.Mode().Perm()),
			Fix:    fmt.Sprintf("run 'chmod 600 %s'", shellQuote(path)),
		}
	}
	return nil
}

// writableByOthers reports whether mode lets the group or everyone write the
// file. Always false on Windows, for the reason exposedMode gives.
func writableByOthers(goos string, mode fs.FileMode) bool {
	if goos == "windows" {
		return false
	}
	return mode.Perm()&0o022 != 0
}

// checkWritable refuses to save over anything but a regular file the user
// owns, or a missing one.
//
// A symbolic link is refused rather than followed. Save renames a new file
// into place, which never writes through a link — it would replace the link
// itself — but replacing a link someone made on purpose (a dotfiles checkout)
// is a surprise, and following one is how a dangling link at NAMECOM_CONFIG
// made `auth login` create a token file wherever it pointed.
func checkWritable(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("checking config %s: %w", path, err)
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		fix := "remove the link, or point NAMECOM_CONFIG at a regular file"
		if target, err := filepath.EvalSymlinks(path); err == nil {
			fix = fmt.Sprintf("set NAMECOM_CONFIG=%s to write the file it points to", shellQuote(target))
		}
		return &UnsafeFileError{Op: "write", Path: path, Reason: "is a symbolic link", Fix: fix}
	}
	if !info.Mode().IsRegular() {
		return &UnsafeFileError{
			Op: "write", Path: path, Reason: "is not a regular file",
			Fix: "point NAMECOM_CONFIG at a regular file",
		}
	}
	if uid, ok := fileOwner(info); ok && uid != currentUID() {
		return &UnsafeFileError{
			Op: "write", Path: path,
			Reason: fmt.Sprintf("is owned by another user (uid %d)", uid),
			Fix:    "point NAMECOM_CONFIG at a config you own",
		}
	}
	return nil
}

// shellQuote quotes s for a POSIX shell when it needs it, so a suggested
// command can be pasted as is — the macOS config directory, "Application
// Support", has a space in it.
func shellQuote(s string) string {
	safe := func(r rune) bool {
		return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("/._-+=:,@~", r)
	}
	if s != "" && strings.IndexFunc(s, func(r rune) bool { return !safe(r) }) < 0 {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// Warnings Load raised, kept until TakeWarnings so the CLI can print them
// through its output config: as "! …" lines, or in the JSON/YAML warnings
// array. Load printed them itself, and only to a terminal, so a script never
// learned that its token file was readable by others.
var (
	warnMu      sync.Mutex
	loadWarning []string
)

func addWarning(msg string) {
	warnMu.Lock()
	defer warnMu.Unlock()
	for _, w := range loadWarning {
		if w == msg {
			return // several Loads in one run warn once
		}
	}
	loadWarning = append(loadWarning, msg)
}

// TakeWarnings returns the warnings raised by Load since the last call, and
// forgets them.
func TakeWarnings() []string {
	warnMu.Lock()
	defer warnMu.Unlock()
	w := loadWarning
	loadWarning = nil
	return w
}
