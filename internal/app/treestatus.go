package app

import (
	"context"
	"errors"
	"log/slog"
	"os/exec"
	"path"
	"time"

	"github.com/egoist/mygo/ui"

	"perch/internal/review"
)

const (
	treeStatusDelay time.Duration = 600 * time.Millisecond
	treeDotSize     float32       = 6
)

// treeStatus colors the rows of the file tree by what Git sees changed. It
// holds only the changed files, read again after a burst of writes.
type treeStatus struct {
	ctx    context.Context
	cancel context.CancelFunc
	kinds  map[string]review.Kind
	// dirs are the folders that hold a changed file.
	dirs    map[string]struct{}
	running bool
	again   bool
	queued  bool
}

// startTreeStatus begins coloring the rows of the open folder.
func (a *App) startTreeStatus() {
	a.stopTreeStatus()
	a.status.ctx, a.status.cancel = context.WithCancel(context.Background())
	a.refreshTreeStatus()
}

// stopTreeStatus forgets the colors and ends the reading under way.
func (a *App) stopTreeStatus() {
	if a.status.cancel != nil {
		a.status.cancel()
	}
	a.status = treeStatus{}
}

// scheduleTreeStatus reads the status again once a burst of writes has passed.
func (a *App) scheduleTreeStatus() {
	if a.status.ctx == nil || a.status.queued {
		return
	}
	a.status.queued = true
	a.after(treeStatusDelay, func() {
		a.status.queued = false
		a.refreshTreeStatus()
	})
}

// refreshTreeStatus reads the status off the main thread, one reading at a
// time. The goroutine is owned by the open folder: stopTreeStatus cancels its
// context.
func (a *App) refreshTreeStatus() {
	s := &a.status
	if s.ctx == nil {
		return
	}
	if s.running {
		s.again = true
		return
	}
	s.running = true
	ctx, root := s.ctx, a.root
	go func() {
		kinds, err := review.FileStatuses(ctx, root)
		a.post(func() { a.treeStatusRead(ctx, kinds, err) })
	}()
}

// treeStatusRead records the status. A folder outside Git has no colors, and
// a failure keeps the colors the rows have.
func (a *App) treeStatusRead(ctx context.Context, kinds map[string]review.Kind, err error) {
	s := &a.status
	if ctx != s.ctx {
		return
	}
	s.running = false
	switch {
	case errors.Is(err, review.ErrNotRepository) || errors.Is(err, exec.ErrNotFound):
		s.kinds, s.dirs = nil, nil
	case err != nil:
		slog.Warn("read the status of the file tree", "root", a.root, "error", err)
	default:
		s.kinds, s.dirs = kinds, changedDirs(kinds)
	}
	if s.again {
		s.again = false
		a.refreshTreeStatus()
	}
}

// changedDirs returns every folder that holds one of the changed files.
func changedDirs(kinds map[string]review.Kind) map[string]struct{} {
	dirs := map[string]struct{}{}
	for file := range kinds {
		for dir := path.Dir(file); dir != "."; dir = path.Dir(dir) {
			if _, seen := dirs[dir]; seen {
				break
			}
			dirs[dir] = struct{}{}
		}
	}
	return dirs
}

// treeStatusView marks the end of a row of the tree: a changed file with its
// letter, and a folder holding changes with a dot.
func (a *App) treeStatusView(c *ui.Context, item string, isDir bool) {
	colors := a.look.palette
	if isDir {
		if _, changed := a.status.dirs[item]; changed {
			ui.Spacer(c)
			ui.Box(c).Size(treeDotSize, treeDotSize).Radius(treeDotSize / 2).Background(colors.warning.Alpha(0.8)).Shrink(0)
		}
		return
	}
	kind, changed := a.status.kinds[item]
	if !changed {
		return
	}
	letter, color := kindLetter(kind, colors)
	ui.Spacer(c)
	ui.Text(c, letter).Font("monospace").FontSize(smallFontSize - 1).FontWeight(600).TextColor(color).Shrink(0)
}

// treeNameColor is the color of a changed file's name, and false for a file
// Git sees unchanged.
func (a *App) treeNameColor(item string) (ui.Color, bool) {
	kind, changed := a.status.kinds[item]
	if !changed {
		return ui.Color{}, false
	}
	_, color := kindLetter(kind, a.look.palette)
	return color, true
}
