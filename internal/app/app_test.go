package app

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

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
	if a.activeDocument() != nil || a.notice == "" {
		t.Errorf("document %v, notice %q for a binary file", a.activeDocument(), a.notice)
	}
}
