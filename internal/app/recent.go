package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"perch/locales"
)

const (
	maxRecentProjects int = 10
	recentVersion     int = 1
	recentFileName        = "recent-projects.json"

	recentRowWidth      float32 = 360
	recentRowHeight     float32 = 42
	recentListGap       float32 = 2
	recentListMaxHeight float32 = 320
	// recentChromeRoom is the room the welcome title, tagline and buttons take.
	recentChromeRoom float32 = 330
)

// recentData is the JSON of the recent projects.
type recentData struct {
	Version int      `json:"version"`
	Paths   []string `json:"paths"`
}

// addRecent puts root first, removes its older copy and keeps the limit.
func addRecent(paths []string, root string) []string {
	entries := make([]string, 0, len(paths)+1)
	entries = append(entries, root)
	entries = append(entries, paths...)
	return cleanRecent(entries)
}

// cleanRecent removes duplicates and paths that cannot be saved, and keeps the
// first maxRecentProjects.
func cleanRecent(paths []string) []string {
	cleaned := make([]string, 0, min(len(paths), maxRecentProjects))
	seen := make(map[string]bool, len(paths))
	for _, path := range paths {
		path = filepath.Clean(path)
		if !filepath.IsAbs(path) || seen[path] {
			continue
		}
		seen[path] = true
		cleaned = append(cleaned, path)
		if len(cleaned) == maxRecentProjects {
			break
		}
	}
	return cleaned
}

// loadRecent reads the recent projects. A corrupt or older file is logged and
// gives an empty list.
func loadRecent(file string) []string {
	data, err := os.ReadFile(file)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		slog.Warn("read recent projects", "file", file, "error", err)
		return nil
	}
	var stored recentData
	if err := json.Unmarshal(data, &stored); err != nil {
		slog.Warn("read recent projects", "file", file, "error", err)
		return nil
	}
	if stored.Version != recentVersion {
		slog.Warn("read recent projects", "file", file, "version", stored.Version)
		return nil
	}
	return cleanRecent(stored.Paths)
}

// saveRecent writes the recent projects through a temporary file.
func saveRecent(file string, paths []string) error {
	data, err := json.MarshalIndent(recentData{Version: recentVersion, Paths: paths}, "", "  ")
	if err != nil {
		return fmt.Errorf("encode recent projects: %w", err)
	}
	dir := filepath.Dir(file)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("save recent projects %q: %w", file, err)
	}
	temp, err := os.CreateTemp(dir, ".recent-*.tmp")
	if err != nil {
		return fmt.Errorf("save recent projects %q: %w", file, err)
	}
	_, writeErr := temp.Write(append(data, '\n'))
	if err := errors.Join(writeErr, temp.Close()); err != nil {
		return errors.Join(fmt.Errorf("save recent projects %q: %w", file, err), os.Remove(temp.Name()))
	}
	if err := os.Rename(temp.Name(), file); err != nil {
		return errors.Join(fmt.Errorf("save recent projects %q: %w", file, err), os.Remove(temp.Name()))
	}
	return nil
}

// recentExisting returns the recent projects that still exist, in their order.
func (d *Desktop) recentExisting() []string {
	return existingFolders(d.recent)
}

// existingFolders returns the paths that are folders now, in their order.
func existingFolders(paths []string) []string {
	existing := make([]string, 0, len(paths))
	for _, root := range paths {
		info, err := os.Stat(root)
		if err == nil && info.IsDir() {
			existing = append(existing, root)
		}
	}
	return existing
}

// checkRecentRoots finds which recent projects the welcome screen can list,
// once for each version of the list, since a folder on a slow disk must not
// hold up a frame. The goroutine ends with that search.
func (a *App) checkRecentRoots() {
	if a.recentChecked {
		return
	}
	a.recentChecked = true
	paths := slices.Clone(a.desktop.recent)
	go func() {
		existing := existingFolders(paths)
		a.post(func() { a.recentRoots = existing })
	}()
}

// recordRecent puts a folder at the top of the recent projects.
func (d *Desktop) recordRecent(root string) {
	next := addRecent(d.recent, root)
	if slices.Equal(next, d.recent) {
		return
	}
	d.recent = next
	d.recentChanged()
}

// removeRecent drops a folder that no longer exists.
func (d *Desktop) removeRecent(root string) {
	root = filepath.Clean(root)
	next := slices.DeleteFunc(slices.Clone(d.recent), func(path string) bool { return path == root })
	if len(next) == len(d.recent) {
		return
	}
	d.recent = next
	d.recentChanged()
}

// recentChanged saves the list, rebuilds the menu and redraws the windows.
func (d *Desktop) recentChanged() {
	if d.recentFile != "" {
		if err := saveRecent(d.recentFile, d.recent); err != nil {
			slog.Warn("save recent projects", "file", d.recentFile, "error", err)
		}
	}
	mygo.App.SetMenu(d.menu())
	for _, a := range d.windows {
		if a.window != nil {
			a.window.Update(func() { a.recentChecked = false })
		}
	}
}

// openRecent opens a recent project, or says it is gone and forgets it.
func (d *Desktop) openRecent(from *App, root string) {
	info, err := os.Stat(root)
	switch {
	case errors.Is(err, fs.ErrNotExist), err == nil && !info.IsDir():
		d.removeRecent(root)
		if a := d.currentApp(from); a != nil {
			a.tell(toast{title: locales.FileGone(filepath.Base(root)), detail: locales.FileGoneDetail()})
		}
	case err != nil:
		if a := d.currentApp(from); a != nil {
			a.tell(toast{title: locales.FolderOpenFailed(), detail: err.Error()})
		}
	default:
		d.openFolder(from, root)
	}
}

// recentMenuItems fills File > Open Recent in the list's order.
func (d *Desktop) recentMenuItems() []*mygo.MenuItem {
	paths := d.recentExisting()
	if len(paths) == 0 {
		return []*mygo.MenuItem{{Label: locales.NoRecentProjects(), Disabled: true}}
	}
	items := make([]*mygo.MenuItem, 0, len(paths))
	for _, root := range paths {
		root := root
		items = append(items, &mygo.MenuItem{
			Label:    filepath.Base(root),
			Subtitle: shortenProjectPath(root),
			ToolTip:  root,
			Click: func(_ *mygo.MenuItem, window *mygo.Window) {
				a := d.appOf(window)
				if a != nil {
					a.window.Update(func() { d.openRecent(a, root) })
				}
			},
		})
	}
	return items
}

// shortenProjectPath writes a path shorter, from the home folder or the last
// folders.
func shortenProjectPath(full string) string {
	display := full
	if home, err := os.UserHomeDir(); err == nil {
		if rel, err := filepath.Rel(home, full); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			display = filepath.Join("~", rel)
		}
	}
	parts := strings.Split(filepath.ToSlash(display), "/")
	if len(parts) > 3 {
		return "…/" + strings.Join(parts[len(parts)-3:], "/")
	}
	return display
}

// recentProjectsView lists the recent projects under the welcome screen's
// Open Folder button.
func (a *App) recentProjectsView(c *ui.Context) {
	if a.desktop == nil {
		return
	}
	a.checkRecentRoots()
	roots := a.recentRoots
	if len(roots) == 0 {
		return
	}
	colors := a.look.palette
	_, height := c.Size()
	ui.Box(c).Height(8)
	ui.Text(c, locales.RecentProjects()).FontSize(smallFontSize).TextColor(colors.muted)
	ui.Scroll(c).Width(recentRowWidth).Height(recentListHeight(len(roots), height)).Label(locales.RecentProjects()).Children(func() {
		ui.Column(c).FillWidth().Gap(recentListGap).Children(func() {
			for _, root := range roots {
				a.recentProjectRow(c, root)
			}
		})
	})
}

// recentListHeight fits the recent rows in the window, up to the list's room.
func recentListHeight(count int, windowHeight float32) float32 {
	room := min(recentListMaxHeight, max(0, windowHeight-recentChromeRoom))
	return min(float32(count)*(recentRowHeight+recentListGap), room)
}

// recentProjectRow shows one recent project and opens it on a click.
func (a *App) recentProjectRow(c *ui.Context, root string) {
	colors := a.look.palette
	name := filepath.Base(root)
	row := ui.ButtonBase(c).Width(recentRowWidth).Height(recentRowHeight).Gap(9).Padding(6, 8).Radius(radiusControl).Label(name)
	if row.Hovered() {
		row.Background(colors.hover)
	}
	clicked := row.Clicked()
	row.Children(func() {
		ui.Icon(c, iconFolder).FontSize(iconFontSize).TextColor(colors.muted)
		ui.Column(c).Grow(1).Shrink(1).Gap(1).Children(func() {
			ui.Text(c, name).MaxLines(1)
			ui.Text(c, shortenProjectPath(root)).FontSize(smallFontSize - 1).TextColor(colors.muted).MaxLines(1)
		})
	})
	if clicked {
		a.openRecentProject(root)
	}
}

// openRecentProject opens a recent project in this window.
func (a *App) openRecentProject(root string) {
	if a.desktop == nil || a.window == nil {
		return
	}
	a.window.Update(func() { a.desktop.openRecent(a, root) })
}
