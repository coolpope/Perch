package app

import (
	"os"
	"slices"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"

	"github.com/marukolibro/Perch/internal/workspace"
	"github.com/marukolibro/Perch/locales"
)

const (
	// lastTabNumber is the number that always means the last tab, as in browsers.
	lastTabNumber int = 9
	// maxClosedTabs bounds the stack that Reopen Closed Tab walks back through.
	maxClosedTabs int = 20
	// maxRecentFiles bounds the recently viewed files prioritized in Quick Open.
	maxRecentFiles int = 30
)

// closedTab is the lightweight state kept to reopen a recently closed tab.
type closedTab struct {
	path     string
	external bool
	line     int
	col      int
	source   bool
}

// tabNumberKeys are the keys that choose a tab by its place, from the first.
var tabNumberKeys = []ui.Key{
	ui.Key1, ui.Key2, ui.Key3, ui.Key4, ui.Key5, ui.Key6, ui.Key7, ui.Key8, ui.Key9,
}

// tabAfter returns the tab step places from active, going round the ends.
func tabAfter(active int, count int, step int) int {
	return (active + step + count) % count
}

// tabAtNumber returns the tab a number key chooses, and whether there is one.
func tabAtNumber(number int, count int) (int, bool) {
	if number == lastTabNumber {
		return count - 1, count > 0
	}
	return number - 1, number <= count
}

// nextTab shows the tab after the current one.
func (a *App) nextTab() {
	if len(a.documents) > 0 {
		a.showTab(tabAfter(a.active, len(a.documents), 1))
	}
}

// previousTab shows the tab before the current one.
func (a *App) previousTab() {
	if len(a.documents) > 0 {
		a.showTab(tabAfter(a.active, len(a.documents), -1))
	}
}

// showTab shows a tab and moves the keyboard to its text.
func (a *App) showTab(index int) {
	a.chooseTab(index)
	d := a.documents[index]
	if d.editor != nil && !d.showsPreview() && !d.showsSVGPreview() {
		d.focus.Focus()
	}
}

// tabShortcuts switches tabs with Control+Tab and Command+number.
func (a *App) tabShortcuts(c *ui.Context) {
	if c.Shortcut(ui.Ctrl, ui.KeyTab) {
		a.nextTab()
	}
	if c.Shortcut(ui.Ctrl|ui.Shift, ui.KeyTab) {
		a.previousTab()
	}
	for place, key := range tabNumberKeys {
		if !c.Shortcut(ui.Cmd, key) {
			continue
		}
		if index, found := tabAtNumber(place+1, len(a.documents)); found {
			a.showTab(index)
		}
	}
}

// tabMenu builds the context menu of a tab.
func (a *App) tabMenu(m *ui.Menu, d *document, index int) {
	icons := menuIcons()
	if m.Item(locales.CloseTab()).Chosen() {
		a.requestCloseTab(d)
	}
	if len(a.documents) > 1 && m.Item(locales.TabCloseOthers()).Chosen() {
		a.closeOtherTabs(d)
	}
	if index < len(a.documents)-1 && m.Item(locales.TabCloseRight()).Chosen() {
		a.closeTabsToRight(index)
	}
	if m.Item(locales.TabCloseAll()).Chosen() {
		a.closeAllTabs()
	}
	m.Separator()
	if d.external {
		if m.Item(locales.FileMenuCopyPath()).Icon(icons.copyPath).Chosen() {
			a.copyText(d.path)
		}
		return
	}
	if m.Item(locales.FileMenuCopyPath()).Icon(icons.copyPath).Chosen() {
		a.copyText(a.fullPath(d.path))
	}
	if m.Item(locales.FileMenuCopyRelativePath()).Icon(icons.copyLink).Chosen() {
		a.copyText(d.path)
	}
	if m.Item(locales.MenuRevealInTree()).Icon(icons.locate).Chosen() {
		a.revealInTree(d.path)
	}
}

// closeOtherTabs closes every open tab except keep, asking for unsaved ones.
func (a *App) closeOtherTabs(keep *document) {
	targets := make([]*document, 0, len(a.documents))
	for _, d := range a.documents {
		if d != keep {
			targets = append(targets, d)
		}
	}
	a.closeTabs(targets)
}

// closeTabsToRight closes the tabs after index, asking for unsaved ones.
func (a *App) closeTabsToRight(index int) {
	if index < 0 || index+1 >= len(a.documents) {
		return
	}
	a.closeTabs(slices.Clone(a.documents[index+1:]))
}

// closeAllTabs closes every open tab, asking for unsaved ones.
func (a *App) closeAllTabs() {
	a.closeTabs(slices.Clone(a.documents))
}

// closeTabs closes clean targets immediately and queues modified ones for
// confirmation in order.
func (a *App) closeTabs(targets []*document) {
	a.pendingClose = nil
	var dirty []*document
	for _, d := range targets {
		if d.modified() {
			dirty = append(dirty, d)
			continue
		}
		a.closeTab(d)
	}
	if len(dirty) == 0 {
		return
	}
	a.pendingClose = dirty[1:]
	a.askCloseModifiedTab(dirty[0])
}

func (a *App) askCloseModifiedTab(d *document) {
	if index := slices.Index(a.documents, d); index != noDocument {
		a.active = index
	}
	a.closingDocument = d
	a.askCloseTab = true
}

// continuePendingClose advances a batch close after one modified tab was
// confirmed.
func (a *App) continuePendingClose() {
	for len(a.pendingClose) > 0 {
		next := a.pendingClose[0]
		a.pendingClose = a.pendingClose[1:]
		if slices.Index(a.documents, next) == noDocument {
			continue
		}
		if !next.modified() {
			a.closeTab(next)
			continue
		}
		a.askCloseModifiedTab(next)
		return
	}
}

// recordClosedTab pushes a closed tab onto the reopen stack, deduplicating by
// path.
func (a *App) recordClosedTab(d *document) {
	if d == nil || d.path == "" {
		return
	}
	entry := closedTab{path: d.path, external: d.external}
	if d.editor != nil {
		pos := d.editor.CursorPos()
		entry.line, entry.col = pos.Line, pos.Col
	}
	if d.isMarkdown() || d.isSVG() {
		entry.source = d.markdownView == markdownSource
	}
	tabs := slices.DeleteFunc(slices.Clone(a.closedTabs), func(item closedTab) bool {
		return item.path == entry.path && item.external == entry.external
	})
	tabs = append(tabs, entry)
	if len(tabs) > maxClosedTabs {
		tabs = tabs[len(tabs)-maxClosedTabs:]
	}
	a.closedTabs = tabs
}

// reopenClosedTab opens the most recently closed tab that is not currently open
// and still exists on disk.
func (a *App) reopenClosedTab() {
	for len(a.closedTabs) > 0 {
		last := a.closedTabs[len(a.closedTabs)-1]
		a.closedTabs = a.closedTabs[:len(a.closedTabs)-1]
		if a.isTabOpen(last.path, last.external) {
			continue
		}
		if !a.closedTabExists(last) {
			continue
		}
		d := a.openPlaceDocument(last.path, last.external)
		if d == nil {
			continue
		}
		if d.isMarkdown() || d.isSVG() {
			if last.source {
				d.markdownView = markdownSource
			} else {
				d.markdownView = markdownPreview
			}
		}
		if d.editor != nil {
			pos := d.editor.Buffer.Pos(d.editor.Buffer.Offset(editor.Pos{Line: last.line, Col: last.col}))
			d.editor.GoTo(pos)
			if !d.showsPreview() && !d.showsSVGPreview() {
				d.focus.Focus()
			}
		}
		return
	}
}

func (a *App) isTabOpen(filePath string, external bool) bool {
	return slices.ContainsFunc(a.documents, func(d *document) bool {
		return d.external == external && d.path == filePath
	})
}

func (a *App) closedTabExists(entry closedTab) bool {
	if entry.external {
		info, err := os.Stat(entry.path)
		return err == nil && !info.IsDir()
	}
	stat, err := workspace.Stat(a.root, entry.path)
	return err == nil && !stat.IsDir
}

// recordRecentFile places a workspace file at the front of the recent list.
func (a *App) recordRecentFile(relPath string) {
	if relPath == "" {
		return
	}
	recent := slices.DeleteFunc(slices.Clone(a.recentFiles), func(p string) bool { return p == relPath })
	recent = slices.Insert(recent, 0, relPath)
	if len(recent) > maxRecentFiles {
		recent = recent[:maxRecentFiles]
	}
	a.recentFiles = recent
}

// renameTrackedPaths updates recent files and closed tabs after a rename.
func (a *App) renameTrackedPaths(oldPath string, newPath string) {
	for index, entry := range a.closedTabs {
		if !entry.external && removesDocument(oldPath, entry.path) {
			a.closedTabs[index].path = newPath + strings.TrimPrefix(entry.path, oldPath)
		}
	}
	for index, relPath := range a.recentFiles {
		if removesDocument(oldPath, relPath) {
			a.recentFiles[index] = newPath + strings.TrimPrefix(relPath, oldPath)
		}
	}
}

// removeTrackedPaths drops trashed paths from recent files and closed tabs.
func (a *App) removeTrackedPaths(target string) {
	a.closedTabs = slices.DeleteFunc(slices.Clone(a.closedTabs), func(entry closedTab) bool {
		return !entry.external && removesDocument(target, entry.path)
	})
	a.recentFiles = slices.DeleteFunc(slices.Clone(a.recentFiles), func(relPath string) bool {
		return removesDocument(target, relPath)
	})
}
