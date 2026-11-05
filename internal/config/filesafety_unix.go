//go:build !windows

package config

import (
	"io/fs"
	"os"
	"syscall"
)

// currentUID is the user a config must belong to. Overridable in tests, which
// cannot create a file owned by someone else without root.
var currentUID = os.Geteuid

// fileOwner returns the uid that owns info, and false when it is unknown.
func fileOwner(info fs.FileInfo) (int, bool) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return int(st.Uid), true
}

// syncDir flushes dir's entries, so a rename into it survives a crash.
func syncDir(dir string) error {
	d, err := os.Open(dir) //nolint:gosec // G304: the config file's own directory
	if err != nil {
		return err
	}
	defer func() { _ = d.Close() }()
	return d.Sync()
}

// openNoFollow opens path for writing without following a symbolic link, so
// writeInPlace cannot be redirected by a link swapped in after the check.
func openNoFollow(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_WRONLY|syscall.O_NOFOLLOW, 0) //nolint:gosec // G304: the config file Save is writing
}
