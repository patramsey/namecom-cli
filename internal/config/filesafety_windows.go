//go:build windows

package config

import "io/fs"

// currentUID has no meaning on Windows, where ownership is an ACL question
// this package does not attempt; fileOwner never reports an owner to compare.
var currentUID = func() int { return -1 }

// fileOwner reports no owner on Windows, so the ownership checks are skipped.
func fileOwner(_ fs.FileInfo) (int, bool) { return 0, false }

// syncDir is a no-op on Windows: a directory cannot be opened for Sync there,
// and MoveFileEx, which os.Rename uses, needs no separate flush of the entry.
func syncDir(_ string) error { return nil }
