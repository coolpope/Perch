package app

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui/editor"

	"perch/internal/agentlink"
	"perch/locales"
)

func TestAgentShowsLinesAndReadsTheSelection(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "internal/store.go", "package internal\n\nfunc Save() {\n\treturn\n}\n")
	a, tester := openWorkspace(t, root)
	tester.Frame()

	empty := a.contextForAgent()
	if empty.View != agentlink.ViewEmpty || empty.File != "" {
		t.Errorf("empty window %+v", empty)
	}

	shown, err := a.startTour(agentlink.Request{
		Folder: filepath.Join(root, "internal"),
		Stops:  []agentlink.Stop{{Path: "store.go", Line: 3, EndLine: 4, Note: "Save returns early here."}},
	})
	if err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if shown.File != "internal/store.go" || shown.Line != 3 || shown.EndLine != 4 {
		t.Errorf("shown %+v", shown)
	}
	if !slices.Contains(tester.Texts(), "Save returns early here.") {
		t.Errorf("the note did not show: %q", tester.Texts())
	}

	found := a.contextForAgent()
	if found.File != "internal/store.go" || found.View != agentlink.ViewSource || found.Tour != nil {
		t.Fatalf("context %+v", found)
	}
	if found.Selection == nil || found.Selection.StartLine != 3 || found.Selection.EndLine != 4 {
		t.Fatalf("selection %+v", found.Selection)
	}
	if want := "func Save() {\n\treturn"; found.Selection.Text != want {
		t.Errorf("selected %q, want %q", found.Selection.Text, want)
	}

	d := a.activeDocument()
	buffer := d.editor.Buffer
	d.editor.Select(buffer.Offset(editor.Pos{Line: 2}), buffer.Offset(editor.Pos{Line: 3}))
	if whole := a.contextForAgent().Selection; whole.EndLine != 3 {
		t.Errorf("a whole line selected through line %d", whole.EndLine)
	}
	d.editor.GoTo(editor.Pos{Line: 1})
	if caret := a.contextForAgent(); caret.Selection != nil || caret.Caret.Line != 2 {
		t.Errorf("caret context %+v", caret)
	}
}

func TestPersonMovesThroughAnAgentTour(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "a.go", "package a\n\nfunc A() {}\n")
	writeFile(t, root, "b.go", "package b\n\nfunc B() {}\n")
	a, tester := openWorkspace(t, root)
	tester.Frame()

	shown, err := a.startTour(agentlink.Request{Folder: root, Stops: []agentlink.Stop{
		{Path: "a.go", Line: 3, Note: "Start with A."},
		{Path: "b.go", Line: 3, Note: "B follows from it."},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if shown.Stops != 2 {
		t.Errorf("shown %+v", shown)
	}
	tester.Frame()
	if !slices.Contains(tester.Texts(), "1 / 2") || !slices.Contains(tester.Texts(), "Start with A.") {
		t.Fatalf("texts %q", tester.Texts())
	}

	if err := tester.Click(locales.TourNext()); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if d := a.activeDocument(); d.path != "b.go" || d.editor.CursorPos().Line != 2 {
		t.Errorf("second stop at %s line %d", d.path, d.editor.CursorPos().Line)
	}
	if !slices.Contains(tester.Texts(), "B follows from it.") {
		t.Errorf("texts %q", tester.Texts())
	}
	if tour := a.contextForAgent().Tour; tour == nil || tour.Stop != 2 || tour.Stops != 2 {
		t.Errorf("tour %+v", tour)
	}

	if err := tester.Click(locales.TourDone()); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if len(a.agentTour.stops) != 0 || slices.Contains(tester.Texts(), "2 / 2") {
		t.Errorf("the tour stayed: %+v", a.agentTour)
	}
}

func TestAgentCannotShowWhatIsNotInTheProject(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	writeFile(t, root, "main.go", "package main\n")
	writeFile(t, outside, "secret.go", "package secret\n")
	a, tester := openWorkspace(t, root)
	tester.Frame()

	cases := []struct {
		name string
		stop agentlink.Stop
		want string
	}{
		{"another folder", agentlink.Stop{Path: filepath.Join(outside, "secret.go")}, "outside the project"},
		{"climbing out", agentlink.Stop{Path: "../secret.go"}, "outside the project"},
		{"a line past the end", agentlink.Stop{Path: "main.go", Line: 9}, "outside"},
		{"a reversed range", agentlink.Stop{Path: "main.go", Line: 2, EndLine: 1}, "comes before"},
		{"an end without a start", agentlink.Stop{Path: "main.go", EndLine: 1}, "needs line"},
		{"a missing file", agentlink.Stop{Path: "gone.go"}, "could not open"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			request := agentlink.Request{Folder: root, Stops: []agentlink.Stop{{Path: "main.go"}, test.stop}}
			_, err := a.startTour(request)
			if err == nil || !strings.Contains(err.Error(), test.want) || !strings.Contains(err.Error(), "stop 2") {
				t.Errorf("error %v, want %q in stop 2", err, test.want)
			}
			if a.activeDocument() != nil {
				t.Error("a tour with a bad stop opened a file")
			}
		})
	}
}

func TestAgentCallsFindTheInnermostProject(t *testing.T) {
	outer := &App{root: "/work"}
	inner := &App{root: "/work/aurora"}
	windows := []*App{{root: ""}, outer, inner}
	if got := windowHolding(windows, "/work/aurora/internal"); got != inner {
		t.Errorf("found %+v", got)
	}
	if got := windowHolding(windows, "/elsewhere"); got != nil {
		t.Errorf("found %+v", got)
	}
}
