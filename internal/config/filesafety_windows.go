//go:build windows

package config

import (
	"io/fs"
	"os"
)

// currentUID has no meaning on Windows, where ownership is an ACL question
// this package does not attempt; fileOwner never reports an owner to compare.
var currentUID = func() int { return -1 }

// fileOwner reports no owner on Windows, so the ownership checks are skipped.
func fileOwner(_ fs.FileInfo) (int, bool) { return 0, false }

// syncDir is a no-op on Windows: a directory cannot be opened for Sync there,
// and MoveFileEx, which os.Rename uses, needs no separate flush of the entry.
func syncDir(_ string) error { return nil }

// openNoFollow opens path for writing. Windows has no O_NOFOLLOW; writeInPlace
// compares the opened file with the one checkWritable accepted instead.
func openNoFollow(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_WRONLY, 0) //nolint:gosec // G304: the config file Save is writing
}
