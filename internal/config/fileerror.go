package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// FileError is a config file the CLI cannot read or write: a directory or a
// named pipe at the path, a directory it may not write, a rename another
// program blocked. Like UnsafeFileError it is a credential problem (exit 3)
// with the fix as its hint; these surfaced as the raw OS error, typed "api",
// exit 1, naming a temporary file the user never made (#363).
type FileError struct {
	// Op is "read" or "write".
	Op   string
	Path string
	// Problem completes "cannot <op> config <path>: …".
	Problem string
	// Fix is the step that resolves it, shown as the hint.
	Fix string
	Err error
}

func (e *FileError) Error() string {
	return fmt.Sprintf("cannot %s config %s: %s", e.Op, e.Path, e.Problem)
}

func (e *FileError) Unwrap() error { return e.Err }

// UserHint is the fix, printed with the error.
func (e *FileError) UserHint() string { return e.Fix }

// pathFix is the fix for a config path that names the wrong kind of thing:
// with NAMECOM_CONFIG set, point it elsewhere; otherwise move the thing aside.
func pathFix(path string) string {
	if os.Getenv("NAMECOM_CONFIG") != "" {
		return "point NAMECOM_CONFIG at a regular file, such as " + filepath.Join(path, "config.yaml")
	}
	return "move " + path + " aside so namecom can create its config file there"
}

// kindOf names a non-regular file's type for an error message.
func kindOf(mode fs.FileMode) string {
	switch {
	case mode.IsDir():
		return "a directory"
	case mode&fs.ModeNamedPipe != 0:
		return "a named pipe"
	case mode&fs.ModeSocket != 0:
		return "a socket"
	case mode&fs.ModeDevice != 0:
		return "a device"
	}
	return "not a regular file"
}

// checkReadType refuses, before the file is opened, a config path that is
// not a regular file. Opening a named pipe blocks until something writes to
// it, so `NAMECOM_CONFIG=fifo namecom …` hung; a directory failed later with
// "read …: is a directory", the path repeated. os.Stat follows a symbolic
// link, which reading — unlike writing — does, and stats without opening.
//
// The null device is allowed: NAMECOM_CONFIG=/dev/null isolates a run from
// any config.
func checkReadType(path string, info fs.FileInfo) error {
	if info.Mode().IsRegular() || isDevNull(path, info) {
		return nil
	}
	return &FileError{Op: "read", Path: path, Problem: "it is " + kindOf(info.Mode()), Fix: pathFix(path)}
}

// isDevNull reports whether path, stat'd as info, is the null device: named
// as os.DevNull ("NUL" on Windows), or a character device that is the same
// file.
func isDevNull(path string, info fs.FileInfo) bool {
	if strings.EqualFold(path, os.DevNull) {
		return true
	}
	if info.Mode()&fs.ModeCharDevice == 0 {
		return false
	}
	null, err := os.Stat(os.DevNull)
	return err == nil && os.SameFile(info, null)
}

// readError wraps a failure to open or read the config.
func readError(path string, err error) error {
	fe := &FileError{Op: "read", Path: path, Problem: rootCause(err), Err: err,
		Fix: "check that the file exists and you can read it, or point NAMECOM_CONFIG at another config"}
	if errors.Is(err, fs.ErrPermission) {
		fe.Fix = "you cannot read it: if the file is yours, run 'chmod 600 " + shellQuote(path) + "'"
		if runtime.GOOS == "windows" {
			fe.Fix = "you cannot read it: check the file's permissions, or point NAMECOM_CONFIG at a config you can read"
		}
	}
	return fe
}

// dirNotWritable is the error for a save into a directory the user cannot
// write, when writing the file in place is not possible either.
func dirNotWritable(path string, err error) error {
	dir := filepath.Dir(path)
	fix := fmt.Sprintf("make %s writable ('chmod u+w %s'), or point NAMECOM_CONFIG at a file in a directory you can write", dir, shellQuote(dir))
	if runtime.GOOS == "windows" {
		fix = fmt.Sprintf("make %s writable, or point NAMECOM_CONFIG at a file in a directory you can write", dir)
	}
	return &FileError{
		Op: "write", Path: path, Err: err,
		Problem: fmt.Sprintf("its directory %s is not writable, and namecom saves the config by writing a new file there", dir),
		Fix:     fix,
	}
}

// renameError is the error for a failed replace of the config by the new
// file. On Windows that is "Access is denied" when another program holds the
// file open — an editor, a sync client, a virus scanner.
func renameError(goos, path string, err error) error {
	fe := &FileError{Op: "write", Path: path, Problem: rootCause(err), Err: err,
		Fix: "check that you can write " + filepath.Dir(path) + ", then try again"}
	if goos == "windows" {
		fe.Fix = "another program (an editor, a sync client or a virus scanner) may have the file open; close it and try again"
	}
	return fe
}

// writeError wraps any other failure to write the config.
func writeError(path string, err error) error {
	return &FileError{Op: "write", Path: path, Problem: rootCause(err), Err: err,
		Fix: "check the disk has space and you can write " + filepath.Dir(path)}
}

// rootCause is err without the operation and path *fs.PathError and
// *os.LinkError prefix, which the FileError message already gives: "read
// /x/adir: is a directory" named the path twice.
func rootCause(err error) string {
	var pe *fs.PathError
	if errors.As(err, &pe) {
		return pe.Err.Error()
	}
	var le *os.LinkError
	if errors.As(err, &le) {
		return le.Err.Error()
	}
	return err.Error()
}
