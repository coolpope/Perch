package app

import (
	"slices"

	"github.com/egoist/mygo/ui/editor"

	"perch/internal/review"
	"perch/locales"
)

// changeLanding is where a file shown by moving between changes lands once
// its diff is ready.
type changeLanding int8

const (
	landNowhere   changeLanding = 0
	landFirstHunk changeLanding = 1
	landLastHunk  changeLanding = -1
)

// moveToChange goes to the next change when step is 1, and the previous one
// when it is -1: in the diff of the review, going on to the next changed file
// past its last change, or among the lines of the open file left to read.
func (a *App) moveToChange(step int) {
	if a.showsChange() {
		a.moveThroughDiff(step)
		return
	}
	if a.showsCommit() {
		a.moveThroughCommit(step)
		return
	}
	d := a.activeTextDocument()
	if d == nil {
		return
	}
	if d.showsPreview() {
		d.markdownView = markdownSource
	}
	before := d.editor.Selection()
	from := d.editor.CursorPos().Line
	if !d.editor.Run(hunkCommand(step)) {
		a.tell(toast{title: locales.NoChangesInFile(), tone: toneInfo})
		return
	}
	// The editor goes round to the other end; the review stops at it, as the
	// diff does.
	to := d.editor.CursorPos().Line
	switch {
	case step > 0 && to <= from:
		d.editor.SetSelections(before)
		a.tell(toast{title: locales.LastChange(), tone: toneInfo})
	case step < 0 && to >= from:
		d.editor.SetSelections(before)
		a.tell(toast{title: locales.FirstChange(), tone: toneInfo})
	}
}

func hunkCommand(step int) editor.Command {
	if step > 0 {
		return editor.NextHunk
	}
	return editor.PreviousHunk
}

// moveThroughDiff goes to a neighbouring change of the diff on screen, or to
// the first or last change of the neighbouring file.
func (a *App) moveThroughDiff(step int) {
	r := &a.review
	if r.diff == nil && !r.unreadable {
		return // The change is still being read.
	}
	if r.diff != nil {
		r.view = changeSource
	}
	if stepDiff(r.diff, step) {
		return
	}
	next := neighbourChange(r.listing.Changes, r.selected, step)
	if next == "" {
		a.tellEndOfChanges(step)
		return
	}
	a.selectChange(next)
	a.review.landing = landingFor(step)
}

// stepDiff goes to the neighbouring change of a diff, and reports false when
// there is none that way.
func stepDiff(diff *editor.DiffState, step int) bool {
	if diff == nil {
		return false
	}
	current, inside := diff.CurrentHunk()
	target := neighbourHunk(current, inside, step)
	if target < 0 || target >= len(diff.Hunks()) {
		return false
	}
	diff.GoToHunk(target)
	return true
}

// landingFor is where a file reached by a step lands: its first change going
// forward, and its last going back.
func landingFor(step int) changeLanding {
	if step < 0 {
		return landLastHunk
	}
	return landFirstHunk
}

func (a *App) tellEndOfChanges(step int) {
	if step > 0 {
		a.tell(toast{title: locales.LastChange(), tone: toneInfo})
		return
	}
	a.tell(toast{title: locales.FirstChange(), tone: toneInfo})
}

// neighbourHunk returns the hunk a step leads to from the one CurrentHunk
// reports: the caret is in current when inside, and past it otherwise.
func neighbourHunk(current int, inside bool, step int) int {
	if step > 0 || inside {
		return current + step
	}
	return current
}

// neighbourChange returns the changed file a step away from relPath in the
// order the review lists them, or nothing at either end.
func neighbourChange(changes []review.Change, relPath string, step int) string {
	index := slices.IndexFunc(changes, func(c review.Change) bool { return c.Path == relPath })
	next := index + step
	if index < 0 || next < 0 || next >= len(changes) {
		return ""
	}
	return changes[next].Path
}

// landOnChange puts a file reached by moving between changes at its first or
// last change, once its diff has been compared.
func (a *App) landOnChange() {
	r := &a.review
	if r.landing != landNowhere && r.diff != nil && !r.diff.Comparing() && len(r.diff.Hunks()) > 0 {
		r.view = changeSource
	}
	r.landing = landDiff(r.diff, r.unreadable, r.landing)
}

// landDiff puts a diff at its first or last change once it has been compared,
// and returns where it is still to land.
func landDiff(diff *editor.DiffState, unreadable bool, landing changeLanding) changeLanding {
	switch {
	case landing == landNowhere:
		return landNowhere
	case unreadable:
		return landNowhere
	case diff == nil || diff.Comparing():
		return landing
	}
	if hunks := diff.Hunks(); len(hunks) > 0 {
		target := 0
		if landing == landLastHunk {
			target = len(hunks) - 1
		}
		diff.GoToHunk(target)
	}
	return landNowhere
}
