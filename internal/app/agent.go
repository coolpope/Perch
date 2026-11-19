package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"

	"perch/internal/agentlink"
	"perch/internal/workspace"
	"perch/locales"
)

const (
	// maxAgentSelection is how much selected code an agent reads at once.
	maxAgentSelection int = 64 << 10
	// maxAgentNote keeps a note to the few lines that fit above the code.
	maxAgentNote int = 1000

	gitDirName = ".git"
)

// tourStop is one place an agent shows, with lines counted from 1. Line is 0
// for a file as a whole.
type tourStop struct {
	path    string
	line    int
	endLine int
	note    string
}

// agentTour is the places an agent is showing. A single place is a tour of one
// stop.
type agentTour struct {
	stops []tourStop
	index int
}

// startAgentLink lets coding agents reach this app. Perch works without it, so
// a failure is only logged. The serving goroutine lasts as long as the app.
func (d *Desktop) startAgentLink() {
	socket, err := agentlink.SocketPath()
	if err != nil {
		slog.Warn("agent link unavailable", "error", err)
		return
	}
	listener, err := agentlink.Listen(socket)
	if err != nil {
		slog.Warn("agent link unavailable", "socket", socket, "error", err)
		return
	}
	go agentlink.Serve(context.Background(), listener, func(request agentlink.Request) agentlink.Reply {
		var reply agentlink.Reply
		mygo.RunOnMain(func() { reply = d.answerAgent(request) })
		return reply
	})
}

// answerAgent runs one agent call on the main thread.
func (d *Desktop) answerAgent(request agentlink.Request) agentlink.Reply {
	switch request.Method {
	case agentlink.MethodContext:
		a := windowHolding(d.windows, request.Folder)
		if a == nil {
			return agentlink.Reply{Error: fmt.Sprintf("Perch has no window for %q. Call perch_show to open the project.", request.Folder)}
		}
		found := a.contextForAgent()
		return agentlink.Reply{Context: &found}
	case agentlink.MethodShow:
		return d.showForAgent(request)
	default:
		return agentlink.Reply{Error: fmt.Sprintf("unknown method %q", request.Method)}
	}
}

// showForAgent shows places in the agent's project, opening the project when no
// window has it. The window comes forward without taking the keyboard from the
// terminal the person is typing in.
func (d *Desktop) showForAgent(request agentlink.Request) agentlink.Reply {
	a := windowHolding(d.windows, request.Folder)
	if a == nil {
		d.openFolder(emptyWindow(d.windows), projectRootOf(request.Folder))
		a = windowHolding(d.windows, request.Folder)
	}
	if a == nil {
		return agentlink.Reply{Error: fmt.Sprintf("Perch could not open the project of %q.", request.Folder)}
	}
	shown, err := a.startTour(request)
	a.window.Update(func() {})
	if err != nil {
		return agentlink.Reply{Error: err.Error()}
	}
	if a.window.IsMinimized() {
		a.window.Restore()
	}
	a.window.ShowInactive()
	return agentlink.Reply{Shown: &shown}
}

// windowHolding returns the window of the innermost project that contains
// folder.
func windowHolding(windows []*App, folder string) *App {
	var holder *App
	for _, a := range windows {
		if a.root == "" {
			continue
		}
		if _, inside := insideProject(a.root, folder); inside && (holder == nil || len(a.root) > len(holder.root)) {
			holder = a
		}
	}
	return holder
}

func emptyWindow(windows []*App) *App {
	index := slices.IndexFunc(windows, func(a *App) bool { return a.root == "" })
	if index < 0 {
		return nil
	}
	return windows[index]
}

// projectRootOf returns the Git work tree around folder, or folder outside Git.
func projectRootOf(folder string) string {
	for dir := folder; ; dir = filepath.Dir(dir) {
		if _, err := os.Stat(filepath.Join(dir, gitDirName)); err == nil {
			return dir
		}
		if dir == filepath.Dir(dir) {
			return folder
		}
	}
}

// startTour checks every stop an agent named and shows the first, so that a
// mistake in a later stop does not surface halfway through.
func (a *App) startTour(request agentlink.Request) (agentlink.Shown, error) {
	if count := len(request.Stops); count == 0 || count > agentlink.MaxTourStops {
		return agentlink.Shown{}, fmt.Errorf("a tour has 1 to %d stops, not %d", agentlink.MaxTourStops, count)
	}
	stops := make([]tourStop, 0, len(request.Stops))
	for index, requested := range request.Stops {
		stop, err := a.checkedStop(request.Folder, requested)
		if err != nil {
			return agentlink.Shown{}, fmt.Errorf("stop %d: %w", index+1, err)
		}
		stops = append(stops, stop)
	}
	if err := a.goToStop(stops[0]); err != nil {
		return agentlink.Shown{}, fmt.Errorf("stop 1: %w", err)
	}
	a.agentTour = agentTour{stops: stops}
	first := stops[0]
	return agentlink.Shown{Project: a.root, File: first.path, Line: first.line, EndLine: first.endLine, Stops: len(stops)}, nil
}

// checkedStop resolves a stop inside the project and checks its lines.
func (a *App) checkedStop(folder string, requested agentlink.Stop) (tourStop, error) {
	if len(requested.Note) > maxAgentNote {
		return tourStop{}, fmt.Errorf("the note has %d bytes; keep it under %d", len(requested.Note), maxAgentNote)
	}
	if requested.EndLine != 0 && requested.Line == 0 {
		return tourStop{}, errors.New("endLine needs line")
	}
	if requested.EndLine != 0 && requested.EndLine < requested.Line {
		return tourStop{}, fmt.Errorf("endLine %d comes before line %d", requested.EndLine, requested.Line)
	}
	target := requested.Path
	if !filepath.IsAbs(target) {
		target = filepath.Join(folder, target)
	}
	relPath, inside := insideProject(a.root, target)
	if !inside {
		return tourStop{}, fmt.Errorf("%q is outside the project %q", requested.Path, a.root)
	}
	stop := tourStop{path: relPath, line: requested.Line, endLine: max(requested.EndLine, requested.Line), note: strings.TrimSpace(requested.Note)}
	if imageKindOf(relPath) == imageKindRaster {
		if stop.line != 0 {
			return tourStop{}, fmt.Errorf("%q is an image and has no lines", relPath)
		}
		return stop, nil
	}
	count, err := a.lineCountOf(relPath)
	if err != nil {
		return tourStop{}, err
	}
	if stop.line < 0 || stop.endLine > count {
		return tourStop{}, fmt.Errorf("lines %d to %d are outside %q, which has %d lines", stop.line, stop.endLine, relPath, count)
	}
	return stop, nil
}

// lineCountOf counts the lines of a file as the person sees them.
func (a *App) lineCountOf(relPath string) (int, error) {
	index := slices.IndexFunc(a.documents, func(d *document) bool { return d.path == relPath && d.editor != nil })
	if index != noDocument {
		return a.documents[index].editor.Buffer.LineCount(), nil
	}
	file, err := workspace.ReadText(a.root, relPath)
	if err != nil {
		return 0, fmt.Errorf("could not open %q: %w", relPath, err)
	}
	return strings.Count(file.Text, "\n") + 1, nil
}

// goToStop opens a stop's file and selects its lines.
func (a *App) goToStop(stop tourStop) error {
	a.openFile(stop.path)
	d := a.activeDocument()
	if d == nil || d.path != stop.path {
		return fmt.Errorf("could not open %q", stop.path)
	}
	a.chooseTab(a.active)
	if stop.line == 0 {
		return nil
	}
	if d.editor == nil {
		return fmt.Errorf("%q is an image and has no lines", stop.path)
	}
	if count := d.editor.Buffer.LineCount(); stop.endLine > count {
		return fmt.Errorf("%q now has %d lines, and this stop ends at line %d", stop.path, count, stop.endLine)
	}
	end := editor.Pos{Line: stop.endLine - 1, Col: len(d.editor.Buffer.Line(stop.endLine - 1))}
	d.reveal(editor.Pos{Line: stop.line - 1}, end)
	return nil
}

// moveTour goes to the stop that is step places away, when there is one.
func (a *App) moveTour(step int) {
	next := a.agentTour.index + step
	if next < 0 || next >= len(a.agentTour.stops) {
		return
	}
	if err := a.goToStop(a.agentTour.stops[next]); err != nil {
		a.tell(toast{title: locales.TourStopFailed(), detail: err.Error()})
		return
	}
	a.agentTour.index = next
}

// contextForAgent says what the person is looking at.
func (a *App) contextForAgent() agentlink.Context {
	found := agentlink.Context{Project: a.root, View: agentlink.ViewEmpty}
	if stops := len(a.agentTour.stops); stops > 1 {
		found.Tour = &agentlink.TourPosition{Stop: a.agentTour.index + 1, Stops: stops}
	}
	if a.showsChange() {
		found.File, found.View = a.review.selected, agentlink.ViewDiff
		return found
	}
	d := a.activeDocument()
	if d == nil {
		return found
	}
	found.File, found.Unsaved, found.View = d.path, d.modified(), agentViewOf(d)
	if found.View != agentlink.ViewSource {
		return found
	}
	caret := d.editor.CursorPos()
	found.Caret = &agentlink.Caret{Line: caret.Line + 1, Column: caret.Col + 1}
	found.Selection = selectionOf(d.editor)
	return found
}

func agentViewOf(d *document) agentlink.View {
	switch {
	case d.editor == nil || (d.isSVG() && d.markdownView == markdownPreview):
		return agentlink.ViewImage
	case d.showsPreview():
		return agentlink.ViewPreview
	default:
		return agentlink.ViewSource
	}
}

// selectionOf returns the selected code, or nil when only a caret shows.
func selectionOf(state *editor.State) *agentlink.Selection {
	selected := state.Selection()
	if selected.Anchor == selected.Head {
		return nil
	}
	start := state.Buffer.Pos(min(selected.Anchor, selected.Head))
	end := state.Buffer.Pos(max(selected.Anchor, selected.Head))
	lastLine := end.Line
	if end.Col == 0 && end.Line > start.Line {
		lastLine-- // A selection of whole lines stops at the start of the next.
	}
	text := state.SelectedText()
	truncated := len(text) > maxAgentSelection
	if truncated {
		text = strings.ToValidUTF8(text[:maxAgentSelection], "")
	}
	return &agentlink.Selection{StartLine: start.Line + 1, EndLine: lastLine + 1, Text: text, Truncated: truncated}
}

// agentTourView shows the agent's note above the code. A tour of several stops
// stays in view on every file, so that the person can always get back to it.
func (a *App) agentTourView(c *ui.Context, d *document) {
	tour := a.agentTour
	if len(tour.stops) == 0 {
		return
	}
	stop := tour.stops[tour.index]
	several := len(tour.stops) > 1
	if !several && (stop.note == "" || stop.path != d.path) {
		return
	}
	colors := a.look.palette
	a.bannerView(c, stop.note, colors.accent, func() {
		if several {
			a.tourControlsView(c, tour)
		}
		button := ui.Box(c).Size(20, 20).Center().Radius(radiusSmall).Shrink(0).Label(locales.Dismiss())
		if button.Hovered() {
			button.Background(colors.hover)
		}
		if button.Clicked() {
			a.agentTour = agentTour{}
		}
		button.Children(func() {
			ui.Icon(c, iconClose).FontSize(12).TextColor(colors.muted)
		})
	})
}

func (a *App) tourControlsView(c *ui.Context, tour agentTour) {
	ui.Text(c, fmt.Sprintf("%d / %d", tour.index+1, len(tour.stops))).FontSize(smallFontSize).TextColor(a.look.palette.muted).Shrink(0)
	if ui.Button(c, locales.TourPrevious()).Disabled(tour.index == 0).Shrink(0).Clicked() {
		a.moveTour(-1)
	}
	if tour.index == len(tour.stops)-1 {
		if ui.PrimaryButton(c, locales.TourDone()).Shrink(0).Clicked() {
			a.agentTour = agentTour{}
		}
		return
	}
	if ui.PrimaryButton(c, locales.TourNext()).Shrink(0).Clicked() {
		a.moveTour(1)
	}
}
