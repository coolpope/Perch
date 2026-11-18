package workspace

import (
	"fmt"
	"os"
	"path"
)

// Stat returns the entry at relPath, following a symbolic link as the tree
// does.
func Stat(root string, relPath string) (Entry, error) {
	file, err := resolve(root, relPath)
	if err != nil {
		return Entry{}, err
	}
	info, err := os.Stat(file)
	if err != nil {
		return Entry{}, fmt.Errorf("stat %q: %w", relPath, err)
	}
	return Entry{Path: relPath, Name: path.Base(relPath), IsDir: info.IsDir()}, nil
}

// CreateFile creates an empty file, refusing a path that already exists.
func CreateFile(root string, relPath string) error {
	file, err := resolve(root, relPath)
	if err != nil {
		return err
	}
	handle, err := os.OpenFile(file, os.O_WRONLY|os.O_CREATE|os.O_EXCL, defaultFileMode)
	if err != nil {
		return fmt.Errorf("create file %q: %w", relPath, err)
	}
	if err := handle.Close(); err != nil {
		return fmt.Errorf("create file %q: %w", relPath, err)
	}
	return nil
}

// CreateDir creates a folder, refusing a path that already exists.
func CreateDir(root string, relPath string) error {
	dir, err := resolve(root, relPath)
	if err != nil {
		return err
	}
	if err := os.Mkdir(dir, 0o755); err != nil {
		return fmt.Errorf("create folder %q: %w", relPath, err)
	}
	return nil
}

// Rename moves a file or folder, refusing to replace anything at newRelPath.
func Rename(root string, oldRelPath string, newRelPath string) error {
	oldPath, err := resolve(root, oldRelPath)
	if err != nil {
		return err
	}
	newPath, err := resolve(root, newRelPath)
	if err != nil {
		return err
	}
	if err := renameNoReplace(oldPath, newPath); err != nil {
		return fmt.Errorf("rename %q to %q: %w", oldRelPath, newRelPath, err)
	}
	return nil
}
