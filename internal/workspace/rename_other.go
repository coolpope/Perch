//go:build !darwin && !windows

package workspace

import (
	"errors"
	"io/fs"
	"os"
)

// renameNoReplace checks the destination before renaming it. This is not
// atomic; macOS and Windows have an exclusive rename of their own.
func renameNoReplace(oldPath string, newPath string) error {
	_, err := os.Lstat(newPath)
	switch {
	case err == nil:
		return fs.ErrExist
	case !errors.Is(err, fs.ErrNotExist):
		return err
	}
	return os.Rename(oldPath, newPath)
}
