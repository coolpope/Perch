// Package search finds files by name and text inside them. It keeps nothing
// between searches: a listing lives as long as the caller holds it, and the
// text of a file only while that file is scanned.
package search

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/marukolibro/Perch/internal/proc"
	"github.com/marukolibro/Perch/internal/workspace"
)

// MaxFiles is the most files a listing holds.
const MaxFiles int = 100_000

// Listing is the searchable files of a workspace.
type Listing struct {
	// Paths are relative to the workspace root, separated by slashes and
	// sorted.
	Paths []string
	// Truncated reports a workspace with more than MaxFiles files.
	Truncated bool
}

// ListFiles lists the files of root. In a Git work tree it leaves out what Git
// ignores; elsewhere it walks the folder.
func ListFiles(ctx context.Context, root string) (Listing, error) {
	inside, err := isGitWorkTree(ctx, root)
	if err != nil {
		return Listing{}, err
	}
	if inside {
		return listGitFiles(ctx, root)
	}
	return walkFiles(ctx, root)
}

// isGitWorkTree reports whether root is inside a Git work tree. A machine
// without Git has no work trees.
func isGitWorkTree(ctx context.Context, root string) (bool, error) {
	if _, err := exec.LookPath("git"); err != nil {
		return false, nil
	}
	output, err := proc.CommandContext(ctx, "git", "-C", root, "rev-parse", "--is-inside-work-tree").Output()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("check Git work tree %q: %w", root, err)
	}
	return strings.TrimSpace(string(output)) == "true", nil
}

func listGitFiles(ctx context.Context, root string) (Listing, error) {
	command := proc.CommandContext(ctx, "git", "-C", root, "ls-files", "--cached", "--others", "--exclude-standard", "-z")
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil {
		return Listing{}, fmt.Errorf("list Git files of %q: %w: %s", root, err, strings.TrimSpace(stderr.String()))
	}
	paths := make([]string, 0, bytes.Count(output, []byte{0}))
	for name := range strings.SplitSeq(string(output), "\x00") {
		if name != "" && !workspace.IsIgnored(name) && isRegularFile(root, name) {
			paths = append(paths, name)
		}
	}
	slices.Sort(paths)
	paths = slices.Compact(paths)
	if len(paths) > MaxFiles {
		return Listing{Paths: slices.Clip(paths[:MaxFiles]), Truncated: true}, nil
	}
	return Listing{Paths: paths}, nil
}

// isRegularFile drops what Git lists but cannot be opened: tracked files
// deleted from the work tree, and submodules.
func isRegularFile(root string, relPath string) bool {
	info, err := os.Stat(filepath.Join(root, filepath.FromSlash(relPath)))
	return err == nil && info.Mode().IsRegular()
}

func walkFiles(ctx context.Context, root string) (Listing, error) {
	var listing Listing
	err := filepath.WalkDir(root, func(file string, entry fs.DirEntry, err error) error {
		if err != nil {
			return skipUnreadable(entry, err)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if file == root {
			return nil
		}
		relative, err := filepath.Rel(root, file)
		if err != nil {
			return fmt.Errorf("list %q: %w", file, err)
		}
		name := filepath.ToSlash(relative)
		if workspace.IsIgnored(name) {
			return skipEntry(entry)
		}
		if entry.IsDir() || !isRegularFile(root, name) {
			return nil
		}
		if len(listing.Paths) == MaxFiles {
			listing.Truncated = true
			return filepath.SkipAll
		}
		listing.Paths = append(listing.Paths, name)
		return nil
	})
	if err != nil {
		return Listing{}, fmt.Errorf("list files of %q: %w", root, err)
	}
	slices.Sort(listing.Paths)
	return listing, nil
}

// skipUnreadable passes over a directory that cannot be read or a file that
// went away during the walk, as an agent's temporary files do.
func skipUnreadable(entry fs.DirEntry, err error) error {
	if errors.Is(err, fs.ErrNotExist) || errors.Is(err, fs.ErrPermission) {
		return skipEntry(entry)
	}
	return err
}

func skipEntry(entry fs.DirEntry) error {
	if entry != nil && entry.IsDir() {
		return filepath.SkipDir
	}
	return nil
}
