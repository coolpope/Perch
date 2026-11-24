package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui/editor"

	"github.com/marukolibro/Perch/internal/session"
	"github.com/marukolibro/Perch/internal/settings"
	"github.com/marukolibro/Perch/locales"
)

func TestToggleSidebarHidesAndRestoresThroughSession(t *testing.T) {
	root := t.TempDir()
	sessionDir := t.TempDir()
	writeFile(t, root, "main.go", "package main\n")

	a, tester, posted := openSessionProject(t, root, sessionDir)
	tester.Frame()
	if a.sidebarHidden {
		t.Fatal("sidebar started hidden")
	}

	a.toggleSidebar()
	tester.Frame()
	if !a.sidebarHidden {
		t.Fatal("sidebar did not hide")
	}
	if tester.HasText(filepath.Base(root)) {
		t.Errorf("hidden sidebar still rendered project header; texts %q", tester.Texts())
	}

	a.saveSessionNow()
	saved, err := session.Load(session.Path(sessionDir, root))
	if err != nil {
		t.Fatalf("load saved session: %v", err)
	}
	if !saved.SidebarHidden {
		t.Fatal("saved session did not record SidebarHidden")
	}

	// Switching to Search or Changes reopens the hidden sidebar.
	a.showFindInFiles()
	if a.sidebarHidden || a.sidebarTab != sidebarSearch {
		t.Fatalf("showFindInFiles left hidden=%v tab=%d", a.sidebarHidden, a.sidebarTab)
	}
	a.toggleSidebar()
	a.showChanges()
	t.Cleanup(a.stopChanges)
	if a.sidebarHidden || a.sidebarTab != sidebarChanges {
		t.Fatalf("showChanges left hidden=%v tab=%d", a.sidebarHidden, a.sidebarTab)
	}

	// Reopening the project restores the hidden sidebar state.
	reopened, reopenedTester, reopenedPosted := openSessionProject(t, root, sessionDir)
	_ = posted
	_ = reopenedPosted
	reopenedTester.Frame()
	if !reopened.sidebarHidden {
		t.Fatal("reopened window did not restore SidebarHidden")
	}
}

func TestFontSizeZoomStepsAndResets(t *testing.T) {
	a := New()
	if a.settings.FontSize != settings.DefaultFontSize {
		t.Fatalf("initial font size %d, want %d", a.settings.FontSize, settings.DefaultFontSize)
	}
	a.changeFontSize(1)
	if a.settings.FontSize != settings.DefaultFontSize+1 {
		t.Fatalf("after zoom in: %d", a.settings.FontSize)
	}
	a.changeFontSize(100)
	if a.settings.FontSize != settings.MaxFontSize {
		t.Fatalf("clamped max: %d, want %d", a.settings.FontSize, settings.MaxFontSize)
	}
	a.changeFontSize(-100)
	if a.settings.FontSize != settings.MinFontSize {
		t.Fatalf("clamped min: %d, want %d", a.settings.FontSize, settings.MinFontSize)
	}
	a.resetFontSize()
	if a.settings.FontSize != settings.DefaultFontSize {
		t.Fatalf("after reset: %d, want %d", a.settings.FontSize, settings.DefaultFontSize)
	}
}

func TestCopyLineReferenceInEditorAndDiff(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "pkg/demo.go", "line1\nline2\nline3\nline4\nline5\n")
	writeFile(t, root, "README.md", "# Title\n\nBody\n")
	a, tester := openWorkspace(t, root)
	var copied string
	a.copyText = func(text string) { copied = text }

	a.openFile("pkg/demo.go")
	tester.Frame()
	d := a.activeDocument()

	// Single caret line.
	d.editor.GoTo(editor.Pos{Line: 2, Col: 2})
	a.copyLineReference()
	if copied != "pkg/demo.go:3" {
		t.Fatalf("single line ref = %q, want pkg/demo.go:3", copied)
	}
	if a.brief.title != locales.CopiedToClipboard() || a.brief.detail != "pkg/demo.go:3" {
		t.Errorf("toast = %+v", a.brief)
	}

	// Multi-line selection ending inside line 4 (0-based 3).
	buf := d.editor.Buffer
	d.editor.Select(buf.Offset(editor.Pos{Line: 1, Col: 1}), buf.Offset(editor.Pos{Line: 3, Col: 3}))
	a.copyLineReference()
	if copied != "pkg/demo.go:2-4" {
		t.Fatalf("multi line ref = %q, want pkg/demo.go:2-4", copied)
	}

	// Selection ending at column 0 of line 4 excludes line 4.
	d.editor.Select(buf.Offset(editor.Pos{Line: 1, Col: 0}), buf.Offset(editor.Pos{Line: 3, Col: 0}))
	a.copyLineReference()
	if copied != "pkg/demo.go:2-3" {
		t.Fatalf("trailing line-start ref = %q, want pkg/demo.go:2-3", copied)
	}

	// Markdown preview does not copy a line reference.
	copied = ""
	a.openFile("README.md")
	tester.Frame()
	a.copyLineReference()
	if copied != "" {
		t.Fatalf("markdown preview copied %q, want empty", copied)
	}

	// Diff view copies selected lines or caret line.
	a2, tester2, posted2 := changedRepository(t)
	var diffCopied string
	a2.copyText = func(text string) { diffCopied = text }
	a2.selectChange("a.go")
	followPosts(t, posted2, tester2, func() bool { return a2.review.diff != nil && !a2.review.diff.Comparing() })
	tester2.Frame()
	a2.review.diff.SetSelectedLines(editor.DiffLineRange{Side: editor.DiffNew, Start: 3, End: 5}, true)
	a2.copyLineReference()
	if diffCopied != "a.go:4-6" {
		t.Fatalf("diff ref = %q, want a.go:4-6", diffCopied)
	}
}

func TestReopenClosedTabRestoresPositionAndSkipsMissing(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "a.go", "package a\n\nvar x = 1\n")
	writeFile(t, root, "b.md", "# B\n\nline 3\n")
	writeFile(t, root, "gone.go", "package gone\n")

	a, tester := openWorkspace(t, root)
	a.openFile("a.go")
	a.activeDocument().editor.GoTo(editor.Pos{Line: 2, Col: 4})
	a.openFile("b.md")
	a.activeDocument().markdownView = markdownSource
	a.openFile("gone.go")
	tester.Frame()

	a.closeTab(a.documents[0]) // a.go
	a.closeTab(a.documents[0]) // b.md
	a.closeTab(a.documents[0]) // gone.go

	if err := os.Remove(filepath.Join(root, "gone.go")); err != nil {
		t.Fatal(err)
	}

	// Reopening skips deleted gone.go and restores b.md in source mode.
	a.reopenClosedTab()
	d := a.activeDocument()
	if d == nil || d.path != "b.md" || d.markdownView != markdownSource {
		t.Fatalf("first reopen got %+v", d)
	}

	// Next reopen restores a.go at its previous cursor position.
	a.reopenClosedTab()
	d = a.activeDocument()
	if d == nil || d.path != "a.go" {
		t.Fatalf("second reopen got %+v", d)
	}
	if pos := d.editor.CursorPos(); pos != (editor.Pos{Line: 2, Col: 4}) {
		t.Errorf("restored cursor = %+v, want {2 4}", pos)
	}

	// Trashing a file does not push it onto the reopen stack.
	a.trashItem = os.RemoveAll
	a.openFile("a.go")
	a.files = fileTreeState{action: fileActionTrash, target: "a.go", open: true}
	a.trashTarget()
	if len(a.closedTabs) != 0 {
		t.Errorf("trashed file stayed in closedTabs: %+v", a.closedTabs)
	}
}

func TestBatchCloseTabsQueuesModifiedDocuments(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "one.go", "package one\n")
	writeFile(t, root, "two.go", "package two\n")
	writeFile(t, root, "three.go", "package three\n")
	writeFile(t, root, "four.go", "package four\n")

	a, tester := openWorkspace(t, root)
	a.openFile("one.go")
	a.openFile("two.go")
	appendText(a.activeDocument(), "// dirty two\n")
	a.openFile("three.go")
	a.openFile("four.go")
	appendText(a.activeDocument(), "// dirty four\n")
	tester.Frame()

	// Close others keeping one.go: three.go closes immediately; two.go and four.go queue.
	a.closeOtherTabs(a.documents[0])
	if len(a.documents) != 3 {
		t.Fatalf("documents after clean close = %d, want 3", len(a.documents))
	}
	if !a.askCloseTab || a.closingDocument == nil || a.closingDocument.path != "two.go" {
		t.Fatalf("expected prompt for two.go, got ask=%v doc=%+v", a.askCloseTab, a.closingDocument)
	}
	if len(a.pendingClose) != 1 || a.pendingClose[0].path != "four.go" {
		t.Fatalf("pendingClose = %+v, want [four.go]", a.pendingClose)
	}

	// Discard two.go -> prompt advances to four.go.
	a.closeTab(a.closingDocument)
	a.askCloseTab = false
	a.closingDocument = nil
	a.continuePendingClose()
	if !a.askCloseTab || a.closingDocument == nil || a.closingDocument.path != "four.go" {
		t.Fatalf("expected prompt for four.go, got ask=%v doc=%+v", a.askCloseTab, a.closingDocument)
	}

	// Close tabs to the right on clean tabs.
	a.askCloseTab = false
	a.closingDocument = nil
	a.pendingClose = nil
	a.closeTab(a.documents[1]) // four.go
	a.openFile("three.go")
	a.closeTabsToRight(0)
	if len(a.documents) != 1 || a.documents[0].path != "one.go" {
		t.Fatalf("after closeTabsToRight: %d tabs", len(a.documents))
	}
}

func TestRevealInTreeExpandsNestedFoldersAndSelectsRow(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "a/b/c/leaf.go", "package c\n")
	writeFile(t, root, "top.go", "package top\n")

	a, tester := openWorkspace(t, root)
	a.openFile("a/b/c/leaf.go")
	a.sidebarTab = sidebarSearch
	a.sidebarHidden = true
	tester.Frame()

	a.revealActiveFile()
	tester.Frame()
	if a.sidebarHidden || a.sidebarTab != sidebarFiles {
		t.Fatalf("reveal left hidden=%v tab=%d", a.sidebarHidden, a.sidebarTab)
	}
	for _, dir := range []string{"a", "a/b", "a/b/c"} {
		if !a.tree.Open.Has(dir) {
			t.Errorf("folder %q was not expanded", dir)
		}
	}
	if got := a.selectedTreePath(); got != "a/b/c/leaf.go" {
		t.Errorf("selected tree row = %q, want a/b/c/leaf.go", got)
	}
}

func TestQuickOpenPrioritizesRecentFilesAndGoToLine(t *testing.T) {
	paths := []string{"alpha.go", "beta.go", "gamma.go", "zebra.go"}
	matches := recentFirstMatches(paths, []string{"zebra.go", "gamma.go", "missing.go"}, "zebra.go", 10)
	got := make([]string, len(matches))
	for i, m := range matches {
		got[i] = m.Path
	}
	want := []string{"gamma.go", "zebra.go", "alpha.go", "beta.go"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("recentFirstMatches = %v, want %v", got, want)
	}

	root := t.TempDir()
	writeFile(t, root, "lines.go", "one\ntwo\nthree\nfour\nfive\n")
	a, tester := openWorkspace(t, root)

	// Without an open file, Go to Line shows a toast rather than opening.
	a.showGoToLine()
	if a.quick.open || a.brief.title != locales.OpenFileForLine() {
		t.Fatalf("without file: open=%v brief=%+v", a.quick.open, a.brief)
	}

	a.openFile("lines.go")
	tester.Frame()
	a.showGoToLine()
	tester.Frame()
	if !a.quick.open || !a.quick.lineOnly {
		t.Fatalf("go to line did not open: %+v", a.quick)
	}

	// Out-of-range input keeps the dialog open with an invalid hint.
	a.quick.query = "99"
	tester.Frame()
	if msg := a.quickOpenMessage(); msg != locales.GoToLineInvalid(6) {
		t.Errorf("out-of-range message = %q, want %q", msg, locales.GoToLineInvalid(6))
	}
	a.acceptQuickOpen()
	if !a.quick.open {
		t.Fatal("out-of-range submit should keep Go to Line open")
	}

	// Valid line number jumps and closes.
	a.quick.query = "4"
	tester.Frame()
	if msg := a.quickOpenMessage(); msg != locales.PressEnterForLine(4) {
		t.Errorf("valid message = %q, want %q", msg, locales.PressEnterForLine(4))
	}
	a.acceptQuickOpen()
	if a.quick.open {
		t.Fatal("valid submit did not close Go to Line")
	}
	if line := a.activeDocument().editor.CursorPos().Line; line != 3 {
		t.Errorf("jumped to line %d, want 3", line)
	}
}
