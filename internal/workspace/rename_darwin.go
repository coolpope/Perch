//go:build darwin

package workspace

import (
	"io/fs"

	"golang.org/x/sys/unix"
)

// renameNoReplace renames a file or folder without replacing a destination
// that appeared after the caller checked it.
func renameNoReplace(oldPath string, newPath string) error {
	err := unix.RenameatxNp(unix.AT_FDCWD, oldPath, unix.AT_FDCWD, newPath, unix.RENAME_EXCL)
	if err == unix.EEXIST {
		return fs.ErrExist
	}
	return err
}
