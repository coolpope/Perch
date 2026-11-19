package app

import (
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"perch/locales"
)

// changedRepository has a.go changed in two places and b.go in one, since the
// review began.
func changedRepository(t *testing.T) (*App, *ui.Tester, <-chan func()) {
	t.Helper()
	root := reviewedRepository(t)
	lines := []string{"package a", ""}
	for index := range 12 {
		lines = append(lines, "var keep"+string(rune('A'+index))+" = 0")
	}
	writeFile(t, root, "a.go", strings.Join(lines, "\n")+"\n")
	gitIn(t, root, "commit", "--quiet", "-am", "longer a")
	a, tester, posted := openWithPosts(t, root)
	t.Cleanup(a.stopChanges)
	tester.Frame()
	a.showChanges()
	settleChanges(t, a, tester, posted)

	lines[3] = "var first = 1"
	lines[11] = "var second = 2"
	writeFile(t, root, "a.go", strings.Join(lines, "\n")+"\n")
	writeFile(t, root, "b.go", "package b\n\nvar third = 3\n")
	a.refreshChanges()
	settleChanges(t, a, tester, posted)
	if len(a.review.listing.Changes) != 2 {
		t.Fatalf("listing %+v", a.review.listing.Changes)
	}
	return a, tester, posted
}

func TestNextChangeWalksTheReviewAcrossFiles(t *testing.T) {
	a, tester, posted := changedRepository(t)
	a.selectChange("a.go")
	followPosts(t, posted, tester, func() bool { return a.review.diff != nil && !a.review.diff.Comparing() })
	tester.Frame()

	caretLine := func() int { return a.review.diff.CursorPos().Line }
	a.moveToChange(1)
	tester.Frame()
	if line := caretLine(); line != 3 {
		t.Errorf("first change at line %d, want 3", line)
	}
	a.moveToChange(1)
	tester.Frame()
	if line := caretLine(); line != 11 {
		t.Errorf("second change at line %d, want 11", line)
	}

	a.moveToChange(1)
	followPosts(t, posted, tester, func() bool { return a.review.selected == "b.go" && a.review.diff != nil })
	tester.Frame()
	tester.Frame()
	if line := caretLine(); line != 1 || a.review.landing != landNowhere {
		t.Errorf("b.go landed on line %d, landing %v", line, a.review.landing)
	}

	a.moveToChange(-1)
	followPosts(t, posted, tester, func() bool { return a.review.selected == "a.go" && a.review.diff != nil })
	tester.Frame()
	tester.Frame()
	if line := caretLine(); line != 11 {
		t.Errorf("going back landed on line %d, want 11", line)
	}
}

func TestNextChangeStepsThroughTheLinesOfAFile(t *testing.T) {
	a, tester, posted := changedRepository(t)
	a.sidebarTab = sidebarFiles
	a.openFile("a.go")
	tester.Frame()
	d := a.activeDocument()
	followPosts(t, posted, tester, func() bool { return d.editor.DiffBase != nil })
	tester.Frame()

	a.moveToChange(1)
	if line := d.editor.CursorPos().Line; line != 3 {
		t.Errorf("first change at line %d, want 3", line)
	}
	a.moveToChange(1)
	if line := d.editor.CursorPos().Line; line != 11 {
		t.Errorf("second change at line %d, want 11", line)
	}
	a.moveToChange(1)
	if a.brief.title != locales.LastChange() || d.editor.CursorPos().Line != 11 {
		t.Errorf("past the last change: %+v at line %d", a.brief, d.editor.CursorPos().Line)
	}
	a.moveToChange(-1)
	if line := d.editor.CursorPos().Line; line != 3 {
		t.Errorf("back to line %d, want 3", line)
	}
}

func TestProjectSymbolsFindAFunctionInAnotherFile(t *testing.T) {
	if _, err := exec.LookPath("gopls"); err != nil {
		t.Skip("gopls is not installed")
	}
	root := t.TempDir()
	writeFile(t, root, "go.mod", "module example.test\n\ngo 1.22\n")
	writeFile(t, root, "main.go", "package main\n\nfunc main() { helperInOther() }\n")
	writeFile(t, root, "other.go", "package main\n\n// helperInOther is found from main.go.\nfunc helperInOther() {}\n")
	a, tester, posted := openWithPosts(t, root)
	a.after = func(_ time.Duration, action func()) { action() }
	t.Cleanup(a.stopDefinitions)
	a.openFile("main.go")
	tester.Frame()

	a.showProjectSymbols()
	tester.Frame()
	if !tester.HasText(locales.ProjectSymbolsHint()) {
		t.Fatalf("texts %q", tester.Texts())
	}
	tester.Type("helperInOther")
	tester.Frame()
	followPosts(t, posted, tester, func() bool {
		return !a.projectSymbols.loading && (len(a.projectSymbols.results) > 0 || a.projectSymbols.notice.message != "")
	})
	tester.Frame()
	if len(a.projectSymbols.results) == 0 || a.projectSymbols.results[0].name != "helperInOther" {
		t.Fatalf("results %+v; notice %+v", a.projectSymbols.results, a.projectSymbols.notice)
	}
	if !tester.HasText("other.go:4") {
		t.Errorf("texts %q", tester.Texts())
	}

	tester.Key(0, ui.KeyEnter)
	tester.Frame()
	d := a.activeDocument()
	if d == nil || d.path != "other.go" || d.editor.CursorPos().Line != 3 {
		t.Fatalf("opened %+v", d)
	}
	if a.projectSymbols.open {
		t.Error("the finder stayed open")
	}
	a.goBack()
	if d := a.activeDocument(); d.path != "main.go" {
		t.Errorf("Go Back returned to %q", d.path)
	}
}
