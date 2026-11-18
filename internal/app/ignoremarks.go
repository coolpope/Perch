package app

import (
	"context"
	"errors"
	"log/slog"
	"os/exec"
	"path"
	"path/filepath"
	"slices"

	"perch/internal/review"
)

// ignoredRowAlpha is how strongly a row that Git ignores shows against the
// sidebar: quieter than other rows while its name stays readable.
const ignoredRowAlpha float32 = 0.72

// ignoreMarks remembers which rows of the file tree Git ignores. Directories
// are asked about one at a time, so that a burst of file changes runs a single
// Git process.
type ignoreMarks struct {
	ctx    context.Context
	cancel context.CancelFunc
	// names holds the ignored names of each directory that has answered.
	names   map[string]map[string]struct{}
	waiting []string
	asking  bool
}

// startIgnoreMarks begins marking the rows of the open folder.
func (a *App) startIgnoreMarks() {
	a.stopIgnoreMarks()
	ctx, cancel := context.WithCancel(context.Background())
	a.ignored.ctx = ctx
	a.ignored.cancel = cancel
	for dir := range a.children {
		a.askIgnored(dir)
	}
}

// stopIgnoreMarks forgets the marks and ends the question being asked.
func (a *App) stopIgnoreMarks() {
	if a.ignored.cancel != nil {
		a.ignored.cancel()
	}
	a.ignored = ignoreMarks{}
}

// askIgnored queues a listed directory, whose rows may have changed.
func (a *App) askIgnored(dir string) {
	if a.ignored.ctx == nil || slices.Contains(a.ignored.waiting, dir) {
		return
	}
	a.ignored.waiting = append(a.ignored.waiting, dir)
	a.askNextIgnored()
}

// askNextIgnored asks Git about the next directory. The goroutine it starts is
// owned by the open folder: stopIgnoreMarks cancels its context.
func (a *App) askNextIgnored() {
	if a.ignored.asking || len(a.ignored.waiting) == 0 {
		return
	}
	dir := a.ignored.waiting[0]
	a.ignored.waiting = a.ignored.waiting[1:]
	a.ignored.asking = true
	ctx := a.ignored.ctx
	folder := filepath.Join(a.root, filepath.FromSlash(dir))
	names := childNames(a.children[dir])
	go func() {
		found, err := review.IgnoredNames(ctx, folder, names)
		a.post(func() { a.ignoredAnswered(ctx, dir, found, err) })
	}()
}

// childNames returns the last element of each path.
func childNames(paths []string) []string {
	names := make([]string, 0, len(paths))
	for _, item := range paths {
		names = append(names, path.Base(item))
	}
	return names
}

// ignoredAnswered records what Git said about a directory. A folder outside Git
// has no marks, and a failure leaves the marks the rows already have.
func (a *App) ignoredAnswered(ctx context.Context, dir string, found []string, err error) {
	if ctx != a.ignored.ctx {
		return
	}
	a.ignored.asking = false
	defer a.askNextIgnored()
	if errors.Is(err, review.ErrNotRepository) || errors.Is(err, exec.ErrNotFound) {
		return
	}
	if err != nil {
		slog.Warn("list the names Git ignores", "dir", dir, "error", err)
		return
	}
	if a.ignored.names == nil {
		a.ignored.names = map[string]map[string]struct{}{}
	}
	names := make(map[string]struct{}, len(found))
	for _, name := range found {
		names[name] = struct{}{}
	}
	a.ignored.names[dir] = names
}

// treeRowIgnored reports whether Git ignores a row or a folder that holds it.
func (a *App) treeRowIgnored(item string) bool {
	for current := item; current != "."; current = path.Dir(current) {
		if _, ignored := a.ignored.names[path.Dir(current)][path.Base(current)]; ignored {
			return true
		}
	}
	return false
}
