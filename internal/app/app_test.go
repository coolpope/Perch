package app

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"

	"perch/internal/workspace"
)

const (
	testWidth  int = 1100
	testHeight int = 700
)

func writeFile(t *testing.T, root string, name string, text string) {
	t.Helper()
	file := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, root string, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func openWorkspace(t *testing.T, root string) (*App, *ui.Tester) {
	t.Helper()
	a := New()
	if err := a.loadFolder(root); err != nil {
		t.Fatal(err)
	}
	return a, ui.NewTester(a.view, testWidth, testHeight)
}

func clickFile(t *testing.T, tester *ui.Tester, name string) {
	t.Helper()
	if err := tester.Click(name); err != nil {
		t.Fatalf("click %s: %v; texts %q", name, err, tester.Texts())
	}
	tester.Frame()
}

func appendText(d *document, text string) {
	end := d.editor.Buffer.Len()
	d.editor.Buffer.Edit(editor.Edit{Start: end, End: end, Text: text})
}

var writeEvent = []mygo.FileEvent{{Op: mygo.FileWrite, Path: "main.go"}}

func TestOpenEditAndSaveKeepsLineEndings(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "main.go", "package main\r\n")
	a, tester := openWorkspace(t, root)
	clickFile(t, tester, "main.go")

	d := a.activeDocument()
	if d == nil {
		t.Fatalf("no document opened; texts %q", tester.Texts())
	}
	appendText(d, "// edited\n")
	tester.Frame()
	if !tester.HasText("Edited") {
		t.Errorf("status bar does not show unsaved edits; texts %q", tester.Texts())
	}
	a.saveActive()
	if a.notice != "" {
		t.Fatalf("save reported %q", a.notice)
	}
	if got, want := readFile(t, root, "main.go"), "package main\r\n// edited\r\n"; got != want {
		t.Errorf("saved %q, want %q", got, want)
	}
	if d.modified() {
		t.Error("document is still modified after saving")
	}
}

func TestMarkdownRendersAndSwitchesToSource(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "README.md", "# Perch Title\n\nBody text\n")
	a, tester := openWorkspace(t, root)
	clickFile(t, tester, "README.md")

	if !tester.HasText("Perch Title") || tester.HasText("# Perch Title") {
		t.Errorf("heading is not rendered; texts %q", tester.Texts())
	}
	a.toggleMarkdownView()
	tester.Frame()
	if a.activeDocument().showsPreview() {
		t.Error("document still shows its preview after switching to source")
	}
}

func TestMarkdownShowsItsHTMLBlocks(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "README.md", `<h1 align="center">Perch Readme</h1>

<p align="center">
  <a href="docs/guide.md">The guide</a>
  <img src="https://img.shields.io/badge/ci.svg" alt="Build status">
</p>

Body text
`)
	writeFile(t, root, "docs/guide.md", "# Guide\n")
	a, tester := openWorkspace(t, root)
	clickFile(t, tester, "README.md")
	tester.Frame()

	for _, want := range []string{"Perch Readme", "The guide", "Build status", "Body text"} {
		if !tester.HasText(want) {
			t.Errorf("preview does not show %q; texts %q", want, tester.Texts())
		}
	}
	if tester.HasText(`<h1 align="center">Perch Readme</h1>`) {
		t.Error("preview shows the HTML source")
	}
	a.openLink(a.activeDocument(), "docs/guide.md")
	if d := a.activeDocument(); d == nil || d.path != "docs/guide.md" {
		t.Errorf("relative link opened %+v", d)
	}
}

func TestCleanDocumentFollowsTheDisk(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "main.go", "package main\n")
	a, tester := openWorkspace(t, root)
	clickFile(t, tester, "main.go")

	writeFile(t, root, "main.go", "package agent\n")
	a.applyFileEvents(writeEvent)
	tester.Frame()
	d := a.activeDocument()
	if got := d.editor.Buffer.String(); got != "package agent\n" {
		t.Errorf("buffer %q after the agent's write", got)
	}
	if d.modified() || d.conflict != conflictNone {
		t.Errorf("modified %v, conflict %d after reloading", d.modified(), d.conflict)
	}
}

func TestUnsavedEditsAreNeverOverwritten(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "main.go", "package main\n")
	a, tester := openWorkspace(t, root)
	clickFile(t, tester, "main.go")
	d := a.activeDocument()
	appendText(d, "// mine\n")

	writeFile(t, root, "main.go", "package agent\n")
	if err := a.save(d); !errors.Is(err, workspace.ErrChangedOnDisk) {
		t.Fatalf("save over the agent's write returned %v", err)
	}
	tester.Frame()
	if d.conflict != conflictChanged || !tester.HasText("Keep Mine") {
		t.Fatalf("conflict %d; texts %q", d.conflict, tester.Texts())
	}
	if got := readFile(t, root, "main.go"); got != "package agent\n" {
		t.Errorf("disk %q after the refused save", got)
	}

	if err := tester.Click("Keep Mine"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if got, want := readFile(t, root, "main.go"), "package main\n// mine\n"; got != want {
		t.Errorf("disk %q after keeping mine, want %q", got, want)
	}
	if d.conflict != conflictNone || d.modified() {
		t.Errorf("conflict %d, modified %v after keeping mine", d.conflict, d.modified())
	}
}

func TestUseDiskVersionDropsEdits(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "main.go", "package main\n")
	a, tester := openWorkspace(t, root)
	clickFile(t, tester, "main.go")
	d := a.activeDocument()
	appendText(d, "// mine\n")

	writeFile(t, root, "main.go", "package agent\n")
	a.applyFileEvents(writeEvent)
	tester.Frame()
	if err := tester.Click("Use Disk Version"); err != nil {
		t.Fatalf("%v; texts %q", err, tester.Texts())
	}
	tester.Frame()
	if got := d.editor.Buffer.String(); got != "package agent\n" || d.modified() {
		t.Errorf("buffer %q, modified %v after using the disk version", got, d.modified())
	}
}

func TestDeletedFileCanBeRestored(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "pkg/main.go", "package main\n")
	a := New()
	if err := a.loadFolder(root); err != nil {
		t.Fatal(err)
	}
	a.openFile("pkg/main.go")
	d := a.activeDocument()
	if err := os.RemoveAll(filepath.Join(root, "pkg")); err != nil {
		t.Fatal(err)
	}
	a.applyFileEvents([]mygo.FileEvent{{Op: mygo.FileRemove, Path: "pkg", IsDir: true}})
	if d.conflict != conflictRemoved {
		t.Fatalf("conflict %d after the file was deleted", d.conflict)
	}
	a.keepMine(d)
	if got := readFile(t, root, "pkg/main.go"); got != "package main\n" {
		t.Errorf("restored %q", got)
	}
}

func TestBinaryFileIsRefused(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "image.bin", "a\x00b")
	a := New()
	if err := a.loadFolder(root); err != nil {
		t.Fatal(err)
	}
	a.openFile("image.bin")
	if a.activeDocument() != nil || a.brief.title != "image.bin is not a text file" {
		t.Errorf("document %v, message %+v for a binary file", a.activeDocument(), a.brief)
	}
}

func TestOpenFailureGoesAwayByItself(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "server", "a\x00b")
	writeFile(t, root, "main.go", "package main\n")
	a, tester := openWorkspace(t, root)
	var expire func()
	a.after = func(_ time.Duration, action func()) { expire = action }

	clickFile(t, tester, "server")
	if !tester.HasText("server is not a text file") {
		t.Fatalf("texts %q", tester.Texts())
	}
	expire()
	tester.Frame()
	if tester.HasText("server is not a text file") {
		t.Error("the message outlived its time")
	}

	clickFile(t, tester, "server")
	clickFile(t, tester, "main.go")
	if tester.HasText("server is not a text file") {
		t.Error("the message stayed after another file opened")
	}
}

func TestTooLargeFileSaysItsSize(t *testing.T) {
	err := &workspace.FileTooLargeError{Path: "bin/server", Size: 59712482, Limit: workspace.MaxTextFileBytes}
	if !errors.Is(err, workspace.ErrFileTooLarge) {
		t.Error("the error does not match ErrFileTooLarge")
	}
	got := openFailure("bin/server", err)
	want := toast{title: "server is too large to open", detail: "It is 57 MB. Perch opens files up to 20 MB."}
	if got != want {
		t.Errorf("message %+v", got)
	}
}

// followPosts runs what background work hands to the window on the test's
// thread until done reports true.
func followPosts(t *testing.T, posted <-chan func(), tester *ui.Tester, done func() bool) {
	t.Helper()
	deadline := time.After(10 * time.Second)
	for !done() {
		select {
		case action := <-posted:
			action()
			tester.Frame()
		case <-deadline:
			t.Fatalf("background work did not finish; texts %q", tester.Texts())
		}
	}
}

func openWithPosts(t *testing.T, root string) (*App, *ui.Tester, <-chan func()) {
	t.Helper()
	a, tester := openWorkspace(t, root)
	posted := make(chan func(), 64)
	a.post = func(action func()) { posted <- action }
	return a, tester, posted
}

func TestQuickOpenGoesToAFileAndLine(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "internal/app/view.go", "package app\n\nfunc view() {}\n")
	writeFile(t, root, "README.md", "# Perch\n")
	a, tester, posted := openWithPosts(t, root)
	tester.Frame()

	a.showQuickOpen()
	tester.Frame()
	followPosts(t, posted, tester, func() bool { return a.quick.loaded })
	tester.Type("view:3")
	tester.Frame()
	if len(a.quick.matches) != 1 || a.quick.matches[0].Path != "internal/app/view.go" {
		t.Fatalf("matches %+v for query %q", a.quick.matches, a.quick.query)
	}
	tester.Key(0, ui.KeyEnter)
	tester.Frame()

	d := a.activeDocument()
	if d == nil || d.path != "internal/app/view.go" {
		t.Fatalf("opened %+v; texts %q", d, tester.Texts())
	}
	if got := d.editor.CursorPos().Line; got != 2 {
		t.Errorf("caret on line %d, want 2", got)
	}
	if a.quick.open || a.quick.listing.Paths != nil {
		t.Errorf("finder still open or still holding its list: %+v", a.quick.listing)
	}
}

func TestQuickOpenRowOpensOnClick(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "alpha.go", "package alpha\n")
	writeFile(t, root, "beta.go", "package beta\n")
	a, tester, posted := openWithPosts(t, root)
	tester.Frame()
	a.showQuickOpen()
	tester.Frame()
	followPosts(t, posted, tester, func() bool { return a.quick.loaded })
	tester.Frame()
	tester.Key(0, ui.KeyDown)
	tester.Frame()
	if a.quick.chosen != 1 {
		t.Errorf("Down chose row %d", a.quick.chosen)
	}
	if err := tester.Click("beta.go"); err != nil {
		t.Fatalf("%v; texts %q", err, tester.Texts())
	}
	tester.Frame()
	if d := a.activeDocument(); d == nil || d.path != "beta.go" {
		t.Errorf("opened %+v", d)
	}
}

func TestFindInFilesOpensTheMatch(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "docs/plan.md", "# Plan\n\nThe needle is here\n")
	writeFile(t, root, "main.go", "package main\n")
	a, tester, posted := openWithPosts(t, root)
	tester.Frame()

	a.showFindInFiles()
	tester.Frame()
	a.find.query = "NEEDLE"
	a.startSearch()
	followPosts(t, posted, tester, func() bool { return a.find.finished })
	if a.find.summary.Hits != 1 || !tester.HasText("1 result in 1 file") {
		t.Fatalf("summary %+v; texts %q", a.find.summary, tester.Texts())
	}
	a.openSearchRow(a.find.chosen)
	tester.Frame()

	d := a.activeDocument()
	if d == nil || d.path != "docs/plan.md" {
		t.Fatalf("opened %+v", d)
	}
	if d.showsPreview() {
		t.Error("a match in Markdown stays hidden behind the preview")
	}
	if got := d.editor.SelectedText(); got != "needle" {
		t.Errorf("selected %q", got)
	}
	if got := d.editor.CursorPos().Line; got != 2 {
		t.Errorf("caret on line %d", got)
	}
}

func TestANewSearchReplacesTheLastOne(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "a.txt", "first second\n")
	a, tester, posted := openWithPosts(t, root)
	tester.Frame()
	a.find.query = "first"
	a.startSearch()
	a.find.query = "second"
	a.startSearch()
	followPosts(t, posted, tester, func() bool { return a.find.finished })
	if len(a.find.rows) != 2 || a.find.rows[1].hit.Preview != "first second" || a.find.rows[1].hit.Column != 6 {
		t.Errorf("rows %+v", a.find.rows)
	}

	a.find.regexp = true
	a.find.query = "("
	a.startSearch()
	tester.Frame()
	if !tester.HasText("Invalid regular expression") && a.sidebarTab == sidebarSearch {
		t.Errorf("texts %q", tester.Texts())
	}
	if a.find.failure == "" {
		t.Error("an invalid regular expression reported nothing")
	}
}

func TestFindBarOpensOverMarkdownSource(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "README.md", "# Title\n\nBody\n")
	a, tester := openWorkspace(t, root)
	clickFile(t, tester, "README.md")
	a.runFind(editor.Find)
	tester.Frame()
	if a.activeDocument().showsPreview() {
		t.Error("find left the document on its preview")
	}
}

func TestLaterLaunchesNameFoldersFromTheirOwnDirectory(t *testing.T) {
	if got := folderArgument([]string{"beta"}, "/work"); got != "/work/beta" {
		t.Errorf("relative folder became %q", got)
	}
	if got := folderArgument([]string{"/other/beta"}, "/work"); got != "/other/beta" {
		t.Errorf("absolute folder became %q", got)
	}
	if got := folderArgument(nil, "/work"); got != "" {
		t.Errorf("no argument became %q", got)
	}
}

func TestAFolderGoesToTheRightWindow(t *testing.T) {
	empty := &App{}
	project := &App{root: "/work/alpha"}
	windows := []*App{project, empty}

	if got := chooseDestination(windows, empty, "/work/alpha"); got.existing != project {
		t.Errorf("an open project went to %+v", got)
	}
	if got := chooseDestination(windows, empty, "/work/beta"); got.empty != empty {
		t.Errorf("the empty window was passed over: %+v", got)
	}
	if got := chooseDestination(windows, project, "/work/beta"); got != (folderDestination{}) {
		t.Errorf("a project window was replaced: %+v", got)
	}
}
