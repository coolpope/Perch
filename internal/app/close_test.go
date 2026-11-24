package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo"

	"github.com/marukolibro/Perch/internal/review"
	"github.com/marukolibro/Perch/locales"
)

// closeFromMenu invokes the same menu item as the configured accelerator.
func closeFromMenu(t *testing.T, d *Desktop, a *App) {
	t.Helper()
	for _, item := range d.menu().Items()[1].Submenu {
		if item.Label == locales.CloseTab() && item.Accelerator == "CmdOrCtrl+W" {
			item.Click(item, a.window)
			return
		}
	}
	t.Fatal("missing Close Tab menu item")
}

func TestCloseCommandTargetsFrontmostViewInItsWindow(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "notes.md", "# Notes\n")
	a, tester := openWorkspace(t, root)
	a.openFile("notes.md")
	doc := a.activeDocument()
	appendText(doc, "draft")
	welcome := New()
	desktop := NewDesktop()
	for _, w := range []*App{a, welcome} {
		w.window = &mygo.Window{}
		w.post = func(action func()) { action() }
		desktop.windows = append(desktop.windows, w)
	}
	welcome.showSettings()
	a.showOutline()
	a.showSettings()
	tester.Frame()
	closeFromMenu(t, desktop, a)
	tester.Frame()
	if a.settingsOpen || !a.outline.open || !welcome.settingsOpen || a.askCloseTab {
		t.Fatal("closing settings also affected the finder, document or other window")
	}
	closeFromMenu(t, desktop, a)
	tester.Frame()
	if a.outline.open || a.askCloseTab || a.activeDocument() != doc {
		t.Fatal("closing the outline affected the document")
	}
	closeFromMenu(t, desktop, a)
	tester.Frame()
	if !a.askCloseTab || a.closingDocument != doc {
		t.Fatal("closing the edited document did not ask about its changes")
	}
	closeFromMenu(t, desktop, a)
	tester.Frame()
	if a.askCloseTab || a.closingDocument != nil || a.activeDocument() != doc || !doc.modified() {
		t.Fatal("closing the save prompt should cancel and keep the edits")
	}
	a.showSettings()
	a.askCloseWindow = true
	tester.Frame()
	closeFromMenu(t, desktop, a)
	if a.askCloseWindow || a.closeConfirmed || !a.settingsOpen || !doc.modified() {
		t.Fatal("closing the window save prompt should cancel and return to settings")
	}
	closeFromMenu(t, desktop, welcome)
	if welcome.settingsOpen || !a.settingsOpen {
		t.Fatal("closing settings on the welcome screen affected the project window")
	}
}

func TestCloseCommandDismissesProjectViewsBeforeCleanTab(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "notes.txt", "notes")
	a, tester := openWorkspace(t, root)
	a.openFile("notes.txt")
	doc := a.activeDocument()
	dismiss := func(open *bool) {
		t.Helper()
		tester.Frame()
		if !*open {
			t.Fatal("view did not open")
		}
		a.closeActiveTab()
		tester.Frame()
		if *open || a.activeDocument() != doc || a.askCloseTab {
			t.Fatal("close did not dismiss just the foreground view")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a.quick = quickOpen{open: true, cancel: cancel}
	dismiss(&a.quick.open)
	if ctx.Err() == nil {
		t.Fatal("closing quick open left the listing running")
	}
	a.showProjectSymbols()
	dismiss(&a.projectSymbols.open)
	a.referencesAtCaret()
	dismiss(&a.references.open)
	for _, action := range []fileAction{fileActionNewFile, fileActionNewFolder, fileActionRename} {
		a.openNameDialog(action, doc.path, false, "new-name")
		dismiss(&a.files.open)
	}
	if _, err := os.Stat(filepath.Join(root, "new-name")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cancelled file operation left a path: %v", err)
	}
	a.askTrash(doc.path)
	dismiss(&a.files.open)
	if got := readFile(t, root, doc.path); got != "notes" {
		t.Fatalf("file changed to %q", got)
	}
	a.openFolderList(doc.path)
	dismiss(&a.folderList.open)
	a.status.kinds = map[string]review.Kind{}
	a.openFileHistory(doc.path)
	dismiss(&a.fileHistory.open)

	a.sidebarTab = sidebarChanges
	a.review.listing.Changes = []review.Change{{Path: doc.path}}
	a.review.selected = doc.path
	tester.Frame()
	a.closeActiveTab()
	if a.showsChange() || a.activeDocument() != doc {
		t.Fatal("closing the diff affected the document")
	}
	a.history.selected = "commit"
	tester.Frame()
	a.closeActiveTab()
	if a.showsCommit() || a.activeDocument() != doc {
		t.Fatal("closing the commit affected the document")
	}
	a.closeActiveTab()
	if len(a.documents) != 0 || a.askCloseTab {
		t.Fatal("closing the clean tab did not remove it")
	}
}
