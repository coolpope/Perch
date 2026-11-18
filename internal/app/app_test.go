package app

import (
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/i18n"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"

	"perch/internal/settings"
	"perch/internal/workspace"
)

const (
	testWidth  int = 1100
	testHeight int = 700
)

// TestMain runs the tests in English whatever language the machine reads.
func TestMain(m *testing.M) {
	i18n.SetLocaleFunc(func() string { return "en" })
	os.Exit(m.Run())
}

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

func writePNG(t *testing.T, root string, name string, width int, height int) {
	t.Helper()
	file := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	handle, err := os.Create(file)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(handle, image.NewRGBA(image.Rect(0, 0, width, height))); err != nil {
		t.Fatal(errors.Join(err, handle.Close()))
	}
	if err := handle.Close(); err != nil {
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
	if a.notice != (toast{}) {
		t.Fatalf("save reported %+v", a.notice)
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

func TestImageOpensWithItsDimensions(t *testing.T) {
	root := t.TempDir()
	writePNG(t, root, "photo.png", 3, 2)
	a, tester, posted := openWithPosts(t, root)
	clickFile(t, tester, "photo.png")
	followPosts(t, posted, tester, func() bool {
		d := a.activeDocument()
		return d != nil && d.image != nil && d.image.decoded
	})
	tester.Frame()

	d := a.activeDocument()
	if d == nil || d.path != "photo.png" || !d.isRasterImage() {
		t.Fatalf("opened %+v; texts %q", d, tester.Texts())
	}
	if d.editor != nil {
		t.Error("a picture tab has an editor")
	}
	if d.image.width != 3 || d.image.height != 2 {
		t.Errorf("picture is %d × %d pixels", d.image.width, d.image.height)
	}
	if !tester.HasText("3 × 2") {
		t.Errorf("status line does not show the dimensions; texts %q", tester.Texts())
	}
	if a.brief != (toast{}) {
		t.Errorf("opening the picture reported %+v", a.brief)
	}
}

func TestCorruptImageShowsAToastAndOpensNothing(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "broken.png", "not a png")
	a, tester := openWorkspace(t, root)
	clickFile(t, tester, "broken.png")

	if a.activeDocument() != nil || len(a.documents) != 0 {
		t.Fatalf("opened %+v for a corrupt picture", a.documents)
	}
	if want := "broken.png could not be shown"; a.brief.title != want {
		t.Errorf("message %+v, want %q", a.brief, want)
	}
	if !tester.HasText("broken.png could not be shown") {
		t.Errorf("texts %q", tester.Texts())
	}
}

func TestSVGSwitchesBetweenPreviewAndSource(t *testing.T) {
	root := t.TempDir()
	source := `<svg xmlns="http://www.w3.org/2000/svg" width="120" height="80"><rect width="120" height="80" fill="#1464F0"/></svg>`
	writeFile(t, root, "icon.svg", source)
	a, tester := openWorkspace(t, root)
	clickFile(t, tester, "icon.svg")

	d := a.activeDocument()
	if d == nil || !d.isSVG() || !d.showsSVGPreview() {
		t.Fatalf("opened %+v; texts %q", d, tester.Texts())
	}
	if d.editor == nil || d.editor.Buffer.String() != source {
		t.Errorf("editor buffer %q", d.editor.Buffer.String())
	}
	if !tester.HasText("120 × 80") {
		t.Errorf("status line does not show the dimensions; texts %q", tester.Texts())
	}
	if err := tester.Click("Source"); err != nil {
		t.Fatalf("%v; texts %q", err, tester.Texts())
	}
	tester.Frame()
	if d.showsSVGPreview() {
		t.Error("the SVG stayed on its preview")
	}
	if !tester.HasText("Ln 1, Col 1") {
		t.Errorf("the source lost its status bar; texts %q", tester.Texts())
	}
	if err := tester.Click("Preview"); err != nil {
		t.Fatalf("%v; texts %q", err, tester.Texts())
	}
	tester.Frame()
	if !d.showsSVGPreview() {
		t.Error("the SVG did not return to its preview")
	}
	edited := strings.Replace(source, `width="120" height="80"`, `width="200" height="100"`, 1)
	d.editor.Buffer.Edit(editor.Edit{Start: 0, End: d.editor.Buffer.Len(), Text: edited})
	tester.Frame()
	if d.image.width != 200 || !tester.HasText("200 × 100") {
		t.Errorf("preview followed the edit to %d × %d; texts %q", d.image.width, d.image.height, tester.Texts())
	}
}

func TestBrokenSVGOpensOnItsSource(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "broken.svg", "<svg")
	a, tester := openWorkspace(t, root)
	clickFile(t, tester, "broken.svg")

	d := a.activeDocument()
	if d == nil || d.showsSVGPreview() || d.editor.Buffer.String() != "<svg" {
		t.Fatalf("opened %+v; message %+v", d, a.brief)
	}
}

func TestHalfWrittenImageKeepsTheLastPicture(t *testing.T) {
	root := t.TempDir()
	writePNG(t, root, "photo.png", 40, 30)
	a, tester, posted := openWithPosts(t, root)
	clickFile(t, tester, "photo.png")
	followPosts(t, posted, tester, func() bool {
		d := a.activeDocument()
		return d != nil && d.image != nil && d.image.decoded
	})
	d := a.activeDocument()
	before := d.image.raster

	file := filepath.Join(root, "photo.png")
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, data[:len(data)-12], 0o644); err != nil {
		t.Fatal(err)
	}
	a.applyFileEvents(writeEvent)
	followPosts(t, posted, tester, func() bool { return a.brief != (toast{}) })
	if !slices.Contains(a.documents, d) || d.image.raster != before || d.image.width != 40 {
		t.Errorf("the tab lost its picture: open %v, %d wide", slices.Contains(a.documents, d), d.image.width)
	}
}

func TestImageFollowsTheDisk(t *testing.T) {
	root := t.TempDir()
	writePNG(t, root, "photo.png", 2, 2)
	a, tester, posted := openWithPosts(t, root)
	clickFile(t, tester, "photo.png")
	followPosts(t, posted, tester, func() bool {
		d := a.activeDocument()
		return d != nil && d.image != nil && d.image.decoded
	})
	d := a.activeDocument()
	before := d.image.raster

	writePNG(t, root, "photo.png", 4, 3)
	a.applyFileEvents(writeEvent)
	followPosts(t, posted, tester, func() bool {
		return d.image.raster != before && d.image.width == 4 && d.image.height == 3
	})
	tester.Frame()
	if !tester.HasText("4 × 3") {
		t.Errorf("status line did not follow the disk; texts %q", tester.Texts())
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

func gitIn(t *testing.T, root string, args ...string) {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", root, "-c", "user.name=t", "-c", "user.email=t@example.invalid"}, args...)...)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, output)
	}
}

func reviewedRepository(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	gitIn(t, root, "init", "--quiet")
	writeFile(t, root, "a.go", "package a\n")
	writeFile(t, root, "b.go", "package b\n")
	writeFile(t, root, "plan.md", "# Plan\n\nFirst step\n\nSecond step\n")
	gitIn(t, root, "add", ".")
	gitIn(t, root, "commit", "--quiet", "-m", "start")
	return root
}

func settleChanges(t *testing.T, a *App, tester *ui.Tester, posted <-chan func()) {
	t.Helper()
	followPosts(t, posted, tester, func() bool { return a.review.loaded && !a.review.running })
	tester.Frame()
}

func TestReviewFollowsAgentWorkToTheEnd(t *testing.T) {
	root := reviewedRepository(t)
	a, tester, posted := openWithPosts(t, root)
	t.Cleanup(a.stopChanges)
	tester.Frame()
	a.showChanges()
	settleChanges(t, a, tester, posted)

	writeFile(t, root, "a.go", "package a\n\nfunc Agent() {}\n")
	writeFile(t, root, "b.go", "package b\n\nvar Two = 2\n")
	gitIn(t, root, "add", "a.go")
	gitIn(t, root, "commit", "--quiet", "-m", "agent commit")
	a.refreshChanges()
	settleChanges(t, a, tester, posted)
	if len(a.review.listing.Changes) != 2 || !tester.HasText("0 of 2 files reviewed") {
		t.Fatalf("listing %+v; texts %q", a.review.listing, tester.Texts())
	}

	a.selectChange("a.go")
	followPosts(t, posted, tester, func() bool { return a.review.diff != nil })
	tester.Frame()
	if !tester.HasText("Mark Reviewed") {
		t.Fatalf("texts %q", tester.Texts())
	}
	a.markSelectedReviewed()
	if a.review.selected != "b.go" {
		t.Errorf("moved on to %q", a.review.selected)
	}
	followPosts(t, posted, tester, func() bool {
		change, _ := a.review.change("a.go")
		return change.Reviewed && a.review.diff != nil && !a.review.running
	})

	a.setReviewed("b.go", true)
	settleChanges(t, a, tester, posted)
	writeFile(t, root, "a.go", "package a\n\nfunc Agent() {}\n\nfunc More() {}\n")
	a.refreshChanges()
	followPosts(t, posted, tester, func() bool {
		change, _ := a.review.change("a.go")
		return !change.Reviewed && !a.review.running
	})
	if change, _ := a.review.change("a.go"); change.Added != 2 {
		t.Errorf("a new edit shows %+v", change)
	}

	a.markAllReviewed()
	followPosts(t, posted, tester, func() bool { return unreadCount(a.review.listing.Changes) == 0 && !a.review.running })
	tester.Frame()
	if err := tester.Click("Finish Review"); err != nil {
		t.Fatalf("%v; texts %q", err, tester.Texts())
	}
	followPosts(t, posted, tester, func() bool {
		return tester.HasText("No changes since your last review.") && tester.HasText("Undo Finish Review")
	})

	if err := tester.Click("Undo Finish Review"); err != nil {
		t.Fatalf("%v; texts %q", err, tester.Texts())
	}
	followPosts(t, posted, tester, func() bool {
		return tester.HasText("2 of 2 files reviewed") && !tester.HasText("Undo Finish Review")
	})
}

func TestEditorMarksTheLinesLeftToRead(t *testing.T) {
	root := reviewedRepository(t)
	writeFile(t, root, ".gitignore", "secret.txt\n")
	writeFile(t, root, "secret.txt", "private\n")
	writeFile(t, root, "a.go", "package a\n\nfunc Agent() {}\n")
	a, tester, posted := openWithPosts(t, root)
	t.Cleanup(a.stopChanges)
	tester.Frame()
	a.showChanges()
	settleChanges(t, a, tester, posted)

	a.openFile("a.go")
	tester.Frame()
	d := a.activeDocument()
	followPosts(t, posted, tester, func() bool { return d.editor.DiffBase != nil })
	if got := d.editor.DiffBase.String(); got != "package a\n" {
		t.Errorf("compared with %q", got)
	}

	a.setReviewed("a.go", true)
	followPosts(t, posted, tester, func() bool {
		return d.editor.DiffBase != nil && d.editor.DiffBase.String() == d.editor.Buffer.String()
	})

	a.openFile("b.go")
	tester.Frame()
	if d.editor.DiffBase != nil {
		t.Error("a tab out of sight kept its comparison text")
	}
	ignored, err := a.review.store.Baseline(context.Background(), "secret.txt")
	if err != nil || ignored.Known {
		t.Errorf("an ignored file has baseline %+v, %v", ignored, err)
	}
}

func TestMarkdownChangeRendersAndOpensTheFile(t *testing.T) {
	root := reviewedRepository(t)
	writeFile(t, root, "plan.md", "# Plan\n\nFirst step\n\nSecond step changed\n")
	a, tester, posted := openWithPosts(t, root)
	t.Cleanup(a.stopChanges)
	tester.Frame()
	a.showChanges()
	settleChanges(t, a, tester, posted)
	a.selectChange("plan.md")
	followPosts(t, posted, tester, func() bool { return a.review.preview != nil })
	tester.Frame()
	tester.Frame()

	if !tester.HasText("Second step changed") || tester.HasText("# Plan") {
		t.Fatalf("texts %q", tester.Texts())
	}
	if want := []lineRange{{first: 4, end: 5}}; !slices.Equal(a.review.marks, want) {
		t.Errorf("marks %+v", a.review.marks)
	}
	if err := tester.Click("Open File"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if d := a.activeDocument(); d == nil || d.path != "plan.md" || a.showsChange() {
		t.Errorf("document %+v, still in the diff: %v", d, a.showsChange())
	}
}

func TestChangesExplainAFolderOutsideGit(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "a.go", "package a\n")
	a, tester, posted := openWithPosts(t, root)
	t.Cleanup(a.stopChanges)
	tester.Frame()
	a.showChanges()
	settleChanges(t, a, tester, posted)
	if !tester.HasText("Changes need a Git repository.") {
		t.Errorf("texts %q", tester.Texts())
	}
}

func TestLinesMapAcrossADiff(t *testing.T) {
	hunks := []editor.DiffHunk{{OldStart: 2, OldEnd: 4, NewStart: 2, NewEnd: 7}}
	cases := []struct {
		side editor.DiffSide
		line int
		want int
	}{
		{editor.DiffNew, 5, 5},
		{editor.DiffOld, 1, 1},
		{editor.DiffOld, 3, 2},
		{editor.DiffOld, 6, 9},
	}
	for _, test := range cases {
		if got := newLineFor(hunks, test.side, test.line); got != test.want {
			t.Errorf("line %d of side %d became %d, want %d", test.line, test.side, got, test.want)
		}
	}
}

func TestSettingsChangeTheOpenWindow(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "main.go", "package main\n")
	writeFile(t, root, "README.md", "# Title\n")
	a, tester := openWorkspace(t, root)
	clickFile(t, tester, "main.go")

	a.showSettings()
	tester.Frame()
	for _, want := range []string{"Settings", "Theme", "Font size", "Wrap long lines", "Open Markdown as"} {
		if !tester.HasText(want) {
			t.Fatalf("settings do not show %q; texts %q", want, tester.Texts())
		}
	}
	if err := tester.Click("Source"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if a.settings.MarkdownView != settings.MarkdownSource {
		t.Errorf("settings are %+v", a.settings)
	}
	if err := tester.Click("Close Settings"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if a.settingsOpen {
		t.Error("the settings stayed open")
	}

	next := a.settings
	next.FontSize = 18
	next.WordWrap = true
	next.TabSize = 2
	a.updateSettings(next)
	tester.Frame()
	d := a.activeDocument()
	if d.editor.Font.Size != 18 || !d.editor.SoftWrap || d.editor.TabSize != 2 {
		t.Errorf("editor font %v, wrap %v, tab %d", d.editor.Font.Size, d.editor.SoftWrap, d.editor.TabSize)
	}

	a.openFile("README.md")
	tester.Frame()
	if a.activeDocument().showsPreview() {
		t.Error("Markdown opened as a preview against the setting")
	}
}

func TestSidebarTooltipStaysClearOfTheWindowControls(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "main.go", "package main\n")
	_, tester := openWorkspace(t, root)
	tester.Frame()

	const buttonBottom float32 = (headerHeight + sidebarTabHeight) / 2
	tester.Move(trafficLightRoom+sidebarTabWidth/2, headerHeight/2)
	tester.Advance(2 * time.Second)
	tester.Frame()
	tip, shown := tester.AccessByRole(ui.RoleTooltip, "")
	if !shown {
		t.Fatalf("no tooltip; texts %q", tester.Texts())
	}
	if tip.Bounds.Y < buttonBottom {
		t.Errorf("tooltip at y %v covers the window controls above y %v", tip.Bounds.Y, buttonBottom)
	}
}

func TestEveryMessageIsTranslated(t *testing.T) {
	read := func(name string) map[string]any {
		t.Helper()
		data, err := os.ReadFile(filepath.Join("..", "..", "locales", name))
		if err != nil {
			t.Fatal(err)
		}
		var catalog map[string]any
		if err := json.Unmarshal(data, &catalog); err != nil {
			t.Fatal(err)
		}
		return catalog
	}
	english, chinese := read("en.json"), read("zh-Hans.json")
	for key, text := range english {
		if strings.HasPrefix(key, "@") {
			continue
		}
		translated, found := chinese[key]
		if !found || translated == "" || translated == text {
			t.Errorf("%s has no Chinese text: %v", key, translated)
		}
	}
}

// definitionProject writes a Go module and opens its main file.
func definitionProject(t *testing.T) (*App, *ui.Tester, <-chan func()) {
	t.Helper()
	if _, err := exec.LookPath("gopls"); err != nil {
		t.Skip("gopls is not installed")
	}
	root := t.TempDir()
	writeFile(t, root, "go.mod", "module example.test\n\ngo 1.22\n")
	writeFile(t, root, "util.go", "package main\n\n// Helper is here.\nfunc Helper() int { return 1 }\n")
	writeFile(t, root, "main.go", "package main\n\nimport \"fmt\"\n\nfunc main() {\n\tfmt.Println(Helper())\n}\n")
	a, tester, posted := openWithPosts(t, root)
	t.Cleanup(a.stopDefinitions)
	tester.Frame()
	a.openFile("main.go")
	tester.Frame()
	return a, tester, posted
}

func definitionOffset(d *document, word string) int {
	return strings.Index(d.editor.Buffer.String(), word) + 1
}

func TestDefinitionOpensTheFileAndComesBack(t *testing.T) {
	a, tester, posted := definitionProject(t)
	start := a.activeDocument()
	a.goToDefinition(start, definitionOffset(start, "Helper"))
	followPosts(t, posted, tester, func() bool { return !a.definition.pending })
	d := a.activeDocument()
	if d == nil || d.path != "util.go" {
		t.Fatalf("opened %+v; message %+v", d, a.brief)
	}
	if got := d.editor.SelectedText(); got != "Helper" || d.editor.CursorPos().Line != 3 {
		t.Errorf("selected %q on line %d", got, d.editor.CursorPos().Line)
	}

	a.goBack()
	if d := a.activeDocument(); d.path != "main.go" || d.editor.CursorPos().Line != 5 {
		t.Errorf("back at %s line %d", d.path, d.editor.CursorPos().Line)
	}
	a.goForward()
	if d := a.activeDocument(); d.path != "util.go" {
		t.Errorf("forward at %s", d.path)
	}
}

func TestDefinitionInALibraryOpensReadOnly(t *testing.T) {
	a, tester, posted := definitionProject(t)
	start := a.activeDocument()
	a.goToDefinition(start, definitionOffset(start, "Println"))
	followPosts(t, posted, tester, func() bool { return !a.definition.pending })
	tester.Frame()
	d := a.activeDocument()
	if d == nil || !d.external || d.name() != "print.go" || !d.editor.ReadOnly {
		t.Fatalf("opened %+v; message %+v", d, a.brief)
	}
	if !tester.HasText("Read only") {
		t.Errorf("texts %q", tester.Texts())
	}
	if err := a.save(d); err != nil {
		t.Errorf("saving a library file: %v", err)
	}
}

func TestDefinitionExplainsAFileWithoutAServer(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "notes.txt", "plain words\n")
	a, tester := openWorkspace(t, root)
	clickFile(t, tester, "notes.txt")
	a.goToDefinition(a.activeDocument(), 1)
	if want := "Go to definition is not available for notes.txt"; a.brief.title != want {
		t.Errorf("message %+v", a.brief)
	}
}

func TestQuickOpenStillWorksAfterAJump(t *testing.T) {
	a, tester, posted := definitionProject(t)
	start := a.activeDocument()
	a.goToDefinition(start, definitionOffset(start, "Helper"))
	followPosts(t, posted, tester, func() bool { return !a.definition.pending })
	tester.Frame()
	a.goBack()
	tester.Frame()
	a.goForward()
	tester.Frame()

	a.showQuickOpen()
	tester.Frame()
	followPosts(t, posted, tester, func() bool { return a.quick.loaded })
	tester.Frame()
	tester.Type("main:6")
	tester.Frame()
	tester.Key(0, ui.KeyEnter)
	tester.Frame()
	if d := a.activeDocument(); d.path != "main.go" || d.editor.CursorPos().Line != 5 {
		t.Errorf("opened %s line %d", d.path, d.editor.CursorPos().Line)
	}
	tester.Frame()
	if !a.activeDocument().editor.Focused() {
		t.Error("the keyboard did not reach the editor")
	}
	tester.Key(0, ui.KeyRight)
	tester.Frame()
	if got := a.activeDocument().editor.CursorPos(); got.Line != 5 || got.Col != 1 {
		t.Errorf("Right moved the caret to %+v", got)
	}
}
