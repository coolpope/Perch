package app

import (
	"fmt"
	"math"
	"os/exec"
	"slices"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"

	"perch/locales"
)

// outlineGoProject writes a Go module with a type, its method, and enough
// functions to scroll, and opens it.
func outlineGoProject(t *testing.T) (*App, *ui.Tester, <-chan func()) {
	t.Helper()
	if _, err := exec.LookPath("gopls"); err != nil {
		t.Skip("gopls is not installed")
	}
	root := t.TempDir()
	writeFile(t, root, "go.mod", "module example.test\n\ngo 1.22\n")
	var source strings.Builder
	source.WriteString("package main\n\ntype Store struct{}\n\nfunc (s Store) Save() {}\n\nfunc Helper() int { return 1 }\n")
	for index := 1; index <= 80; index++ {
		fmt.Fprintf(&source, "\nfunc Step%02d() {}\n", index)
	}
	writeFile(t, root, "store.go", source.String())
	a, tester, posted := openWithPosts(t, root)
	a.openFile("store.go")
	tester.Frame()
	t.Cleanup(a.closeOutline)
	t.Cleanup(a.stopDefinitions)
	return a, tester, posted
}

func waitForOutline(t *testing.T, a *App, tester *ui.Tester, posted <-chan func()) {
	t.Helper()
	followPosts(t, posted, tester, func() bool {
		return !a.outline.loading && (len(a.outline.symbols) > 0 || a.outline.notice.message != "")
	})
	tester.Frame()
}

func outlineSymbolIndex(a *App, name string) int {
	for index, symbol := range a.outline.symbols {
		if symbol.name == name {
			return index
		}
	}
	return noRow
}

func TestOutlineListsGoSymbolsAndChoosesOne(t *testing.T) {
	a, tester, posted := outlineGoProject(t)
	a.showOutline()
	tester.Frame()
	waitForOutline(t, a, tester, posted)

	helper := outlineSymbolIndex(a, "Helper")
	if helper == noRow {
		t.Fatalf("no Helper function; texts %q", tester.Texts())
	}
	if kind := a.outline.symbols[helper].kind; kind != outlineFunction {
		t.Errorf("Helper is kind %v, want a function", kind)
	}
	save := outlineSymbolIndex(a, "Save")
	if save == noRow {
		t.Fatalf("no Save symbol; texts %q", tester.Texts())
	}
	if container := a.outline.symbols[save].container; container != "Store" {
		t.Errorf("Save is inside %q, want Store", container)
	}
	if kind := a.outline.symbols[save].kind; kind != outlineMethod {
		t.Errorf("Save is kind %v, want a method", kind)
	}

	tester.Type("Save")
	tester.Frame()
	if len(a.outline.matches) != 1 {
		t.Fatalf("query Save matched %d symbols", len(a.outline.matches))
	}
	tester.Key(0, ui.KeyEnter)
	tester.Frame()

	d := a.activeDocument()
	if d == nil || d.path != "store.go" {
		t.Fatalf("opened %+v", d)
	}
	if line := d.editor.CursorPos().Line; line != 4 {
		t.Errorf("caret on line %d, want 4", line)
	}
	if !d.editor.Focused() {
		t.Error("the editor did not take the keyboard")
	}
	if a.outline.open || a.outline.symbols != nil {
		t.Errorf("the finder kept its symbols: %+v", a.outline)
	}
}

func TestOutlineEscapeRestoresCaretAndScroll(t *testing.T) {
	a, tester, posted := outlineGoProject(t)
	d := a.activeDocument()
	selected := editor.Selection{Anchor: d.editor.Buffer.Offset(editor.Pos{Line: 2}), Head: d.editor.Buffer.Offset(editor.Pos{Line: 2, Col: 4})}
	d.editor.SetSelections(selected)
	tester.Frame()
	d.editor.SetScrollY(120)
	tester.Frame()
	beforeScroll := d.editor.ScrollY()

	a.showOutline()
	tester.Frame()
	waitForOutline(t, a, tester, posted)
	tester.Type("Step80")
	tester.Frame()
	if len(a.outline.matches) != 1 {
		t.Fatalf("query Step80 matched %d symbols", len(a.outline.matches))
	}
	tester.Key(0, ui.KeyDown)
	tester.Frame()
	tester.Frame()
	if d.editor.ScrollY() <= beforeScroll {
		t.Fatalf("the preview did not scroll: %v, was %v", d.editor.ScrollY(), beforeScroll)
	}

	tester.Key(0, ui.KeyEscape)
	tester.Frame()
	tester.Frame()
	if got := d.editor.Selection(); got != selected {
		t.Errorf("selection %+v, want %+v", got, selected)
	}
	if got := d.editor.ScrollY(); math.Abs(got-beforeScroll) > 1 {
		t.Errorf("scroll at %v, want %v", got, beforeScroll)
	}
	if a.outline.open || a.outline.symbols != nil {
		t.Errorf("the finder kept its symbols: %+v", a.outline)
	}
}

func TestOutlineListsMarkdownHeadingsWithoutAServer(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "guide.md", "# Guide\n\nIntro\n\n## Install\n\nRun it.\n")
	a, tester := openWorkspace(t, root)
	a.openFile("guide.md")
	tester.Frame()
	if !a.activeDocument().showsPreview() {
		t.Fatal("Markdown did not open as a preview")
	}

	a.showOutline()
	tester.Frame()
	if a.definition.pool != nil {
		t.Error("a Markdown outline started a language server")
	}
	if outlineSymbolIndex(a, "Guide") == noRow || outlineSymbolIndex(a, "Install") == noRow {
		t.Fatalf("headings %+v", a.outline.symbols)
	}
	if container := a.outline.symbols[outlineSymbolIndex(a, "Install")].container; container != "Guide" {
		t.Errorf("Install is inside %q, want Guide", container)
	}

	tester.Type("Install")
	tester.Frame()
	tester.Key(0, ui.KeyEnter)
	tester.Frame()
	d := a.activeDocument()
	if d.markdownView != markdownSource {
		t.Error("choosing a heading did not show the source")
	}
	if line := d.editor.CursorPos().Line; line != 4 {
		t.Errorf("caret on line %d, want 4", line)
	}
	if a.outline.symbols != nil {
		t.Error("the finder kept its headings")
	}

	a.showOutline()
	tester.Frame()
	if a.definition.pool != nil {
		t.Error("the source outline started a language server")
	}
	if len(a.outline.symbols) != 2 {
		t.Errorf("source headings %+v", a.outline.symbols)
	}
	tester.Key(0, ui.KeyEscape)
	tester.Frame()
	if d.markdownView != markdownSource {
		t.Error("Escape left the source it opened in")
	}
}

func TestOutlineTurnedOffLanguageStartsNoServer(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "go.mod", "module example.test\n\ngo 1.22\n")
	writeFile(t, root, "main.go", "package main\n\nfunc main() {}\n")
	a, tester := openWorkspace(t, root)
	a.settings.DisabledLanguages = []string{"go"}
	a.openFile("main.go")
	tester.Frame()

	a.showOutline()
	tester.Frame()
	if a.definition.pool != nil {
		t.Error("a turned-off language started a server")
	}
	if a.outline.loading {
		t.Error("a turned-off language is still loading")
	}
	if want := locales.DefinitionTurnedOff("Go"); a.outline.notice.message != want {
		t.Errorf("message %q, want %q", a.outline.notice.message, want)
	}
	if want := locales.DefinitionTurnOnHint(); a.outline.notice.detail != want {
		t.Errorf("hint %q, want %q", a.outline.notice.detail, want)
	}
	if !slices.Contains(tester.Texts(), locales.DefinitionTurnedOff("Go")) {
		t.Errorf("texts %q", tester.Texts())
	}
}
