package app

import (
	"context"
	"log/slog"
	"path"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"

	"github.com/marukolibro/Perch/internal/search"
	"github.com/marukolibro/Perch/locales"
)

const (
	quickOpenLimit   int = 50
	markedTextWeight int = 600
)

// quickOpen is the state of the file finder. It holds the file list only
// while it shows, so that a closed finder costs no memory.
type quickOpen struct {
	open    bool
	query   string
	loaded  bool
	listing search.Listing
	matches []search.PathMatch
	chosen  int
	failure string
	list    ui.ListState
	input   ui.Handle
	cancel  context.CancelFunc
}

// showQuickOpen opens the finder and lists the workspace's files. The goroutine
// it starts ends with the listing, or when hideQuickOpen cancels it.
func (a *App) showQuickOpen() {
	if a.root == "" {
		return
	}
	a.closeOutline()
	a.hideQuickOpen()
	ctx, cancel := context.WithCancel(context.Background())
	a.quick = quickOpen{open: true, cancel: cancel}
	a.quick.input.Focus()
	root := a.root
	go func() {
		listing, err := search.ListFiles(ctx, root)
		a.post(func() {
			if ctx.Err() == nil {
				a.quickOpenListed(listing, err)
			}
		})
	}()
}

func (a *App) quickOpenListed(listing search.Listing, err error) {
	a.quick.loaded = true
	if err != nil {
		slog.Warn("list files", "root", a.root, "error", err)
		a.quick.failure = locales.ListFilesFailed()
		return
	}
	a.quick.listing = listing
	a.rankQuickOpen()
}

// hideQuickOpen closes the finder and lets go of its file list.
func (a *App) hideQuickOpen() {
	if a.quick.cancel != nil {
		a.quick.cancel()
	}
	a.quick = quickOpen{}
}

func (a *App) rankQuickOpen() {
	target := search.ParseTarget(a.quick.query)
	a.quick.matches = search.RankPaths(a.quick.listing.Paths, target.Path, quickOpenLimit)
	a.quick.chosen = 0
	a.quick.list.ScrollTo(0, ui.Start)
}

func (a *App) moveQuickOpenChoice(step int) {
	count := len(a.quick.matches)
	if count == 0 {
		return
	}
	a.quick.chosen = (a.quick.chosen + step + count) % count
	a.quick.list.ScrollIntoView(a.quick.chosen)
}

// acceptQuickOpen goes to what the query names: a line of the current file
// for a query such as ":42", and otherwise the chosen file.
func (a *App) acceptQuickOpen() {
	target := search.ParseTarget(a.quick.query)
	if target.Path == "" && target.Line > 0 {
		a.hideQuickOpen()
		if d := a.activeDocument(); d != nil {
			d.reveal(lineStart(target.Line), lineStart(target.Line))
		}
		return
	}
	if a.quick.chosen < len(a.quick.matches) {
		a.openQuickOpenMatch(a.quick.chosen)
	}
}

func (a *App) openQuickOpenMatch(index int) {
	target := search.ParseTarget(a.quick.query)
	relPath := a.quick.matches[index].Path
	a.hideQuickOpen()
	if target.Line > 0 {
		a.openAt(relPath, lineStart(target.Line), lineStart(target.Line))
		return
	}
	a.openFile(relPath)
	d := a.activeDocument()
	if d == nil || d.path != relPath {
		return
	}
	a.chooseTab(a.active)
	if !d.showsPreview() && d.editor != nil {
		d.focus.Focus()
	}
}

// lineStart returns the start of a line counted from 1.
func lineStart(line int) editor.Pos {
	return editor.Pos{Line: line - 1}
}

func (a *App) quickOpenView(c *ui.Context) {
	if !a.quick.open {
		return
	}
	a.finderPanel(c, &a.quick.open, func() { a.quickOpenContent(c) })
	if !a.quick.open {
		a.hideQuickOpen()
	}
}

func (a *App) quickOpenContent(c *ui.Context) {
	colors := a.look.palette
	finderMoveShortcuts(c, a.moveQuickOpenChoice)
	submitted := false
	ui.Column(c).FillWidth().Children(func() {
		ui.Row(c).FillWidth().Padding(13, 16).Children(func() {
			input := ui.TextInputBase(c, &a.quick.query).Bind(&a.quick.input).Grow(1).FontSize(15).
				Placeholder(locales.GoToFilePlaceholder()).Label(locales.GoToFile())
			if input.Changed() {
				a.rankQuickOpen()
			}
			submitted = input.Submitted()
		})
		ui.Box(c).FillWidth().Height(1).Background(colors.border)
		a.quickOpenResults(c)
	})
	if submitted {
		a.acceptQuickOpen()
	}
}

func (a *App) quickOpenResults(c *ui.Context) {
	q := &a.quick
	if message := a.quickOpenMessage(); message != "" {
		a.finderNote(c, message, "")
		return
	}
	q.list.Key = func(row int) any { return q.matches[row].Path }
	clicked := noRow
	ui.List(c, &q.list, len(q.matches), func(row int) {
		if a.quickOpenRow(c, row) {
			clicked = row
		}
	}).FillWidth().Height(finderHeightFor(len(q.matches))).Padding(finderListInset)
	if clicked != noRow {
		a.openQuickOpenMatch(clicked)
	}
}

// quickOpenMessage says why the finder shows no rows, and is empty when it
// has rows to show.
func (a *App) quickOpenMessage() string {
	q := &a.quick
	target := search.ParseTarget(q.query)
	switch {
	case q.failure != "":
		return q.failure
	case target.Path == "" && target.Line > 0 && a.activeTextDocument() != nil:
		return locales.PressEnterForLine(target.Line)
	case target.Path == "" && target.Line > 0:
		return locales.OpenFileForLine()
	case !q.loaded:
		return locales.ListingFiles()
	case len(q.matches) == 0:
		return locales.NoMatchingFiles()
	default:
		return ""
	}
}

// quickOpenRow builds a row of the finder and reports a click on it.
func (a *App) quickOpenRow(c *ui.Context, index int) bool {
	colors := a.look.palette
	match := a.quick.matches[index]
	name := path.Base(match.Path)
	dir := path.Dir(match.Path)
	row := a.finderRow(c, a.quick.chosen, index)
	clicked := row.Clicked()
	row.Children(func() {
		a.fileIconView(c, match.Path, iconFontSize)
		markedText(c, name, marksFrom(match.Positions, len(match.Path)-len(name)), colors.text, colors.accent)
		if dir != "." {
			ui.Text(c, dir).FontSize(smallFontSize).TextColor(colors.muted).MaxLines(1).Shrink(1)
		}
	})
	return clicked
}

// marksFrom returns the positions at or after offset, relative to it.
func marksFrom(positions []int, offset int) []textMark {
	marks := make([]textMark, 0, len(positions))
	for _, position := range positions {
		if position >= offset {
			marks = append(marks, textMark{start: position - offset, end: position - offset + 1})
		}
	}
	return marks
}

// textMark is a run of bytes of a text to show in the mark color.
type textMark struct {
	start int
	end   int
}

// markedText shows text on one line with its marked runs colored and bold.
func markedText(c *ui.Context, text string, marks []textMark, color ui.Color, markColor ui.Color) {
	ui.RichText(c, markedSpans(text, joinMarks(marks), color, markColor)...).MaxLines(1).Shrink(1)
}

func markedSpans(text string, marks []textMark, color ui.Color, markColor ui.Color) []ui.Span {
	spans := make([]ui.Span, 0, 2*len(marks)+1)
	at := 0
	for _, mark := range marks {
		if mark.start > at {
			spans = append(spans, ui.Span{Text: text[at:mark.start], Color: color})
		}
		spans = append(spans, ui.Span{Text: text[mark.start:mark.end], Color: markColor, Weight: markedTextWeight})
		at = mark.end
	}
	if at < len(text) {
		spans = append(spans, ui.Span{Text: text[at:], Color: color})
	}
	return spans
}

// joinMarks merges marks that touch, which come in increasing order.
func joinMarks(marks []textMark) []textMark {
	joined := make([]textMark, 0, len(marks))
	for _, mark := range marks {
		if last := len(joined) - 1; last >= 0 && joined[last].end >= mark.start {
			joined[last].end = max(joined[last].end, mark.end)
			continue
		}
		joined = append(joined, mark)
	}
	return joined
}
