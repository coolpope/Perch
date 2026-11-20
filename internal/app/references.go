package app

import (
	"cmp"
	"context"
	"errors"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/marukolibro/Perch/internal/lsp"
	"github.com/marukolibro/Perch/locales"
)

const (
	referencesTimeout time.Duration = time.Minute
	// maxReferences keeps the list to what a person reads.
	maxReferences int = 500
	// maxReferenceLine is how much of a line a row shows around the use.
	maxReferenceLine int = 160
)

// reference is one use of a symbol, with the line it is on.
type reference struct {
	location lsp.Location
	// shown is the file, relative to the project when it is inside.
	shown string
	// line is the trimmed text of the line, and mark the use within it.
	line string
	mark textMark
}

// referencesState is the list of the uses of a symbol, asked of the language
// server of the open file. It holds them only while it shows.
type referencesState struct {
	open    bool
	loading bool
	word    string
	results []reference
	// files is how many files the uses are in, and more reports uses
	// beyond maxReferences.
	files   int
	more    bool
	chosen  int
	notice  outlineNotice
	list    ui.ListState
	cancel  context.CancelFunc
	request uint64
	origin  place
}

// referencesAtCaret lists the uses of the symbol at the caret.
func (a *App) referencesAtCaret() {
	d := a.activeTextDocument()
	if d == nil || d.showsPreview() {
		a.openReferences(outlineNotice{message: locales.ReferencesNeedFile()})
		return
	}
	a.findReferences(d, d.editor.Cursor())
}

// openReferences shows the list with a notice in place of rows.
func (a *App) openReferences(notice outlineNotice) {
	a.hideQuickOpen()
	a.closeOutline()
	a.closeProjectSymbols()
	a.closeReferences()
	a.references.open = true
	a.references.notice = notice
}

// findReferences asks the file's language server where the symbol at offset
// is used. The goroutine ends with the uses and their lines, or when closing
// the list or asking again cancels it.
func (a *App) findReferences(d *document, offset int) {
	file := d.absolutePath(a.root)
	registry := a.languageRegistry()
	known, language, supported := registry.Match(file)
	switch {
	case !supported:
		a.openReferences(outlineNotice{message: locales.SymbolsUnsupported(d.name())})
		return
	case !registry.Enabled(known.ID):
		a.openReferences(outlineNotice{message: locales.SymbolsTurnedOff(known.Name), detail: locales.DefinitionTurnOnHint()})
		return
	}
	buffer := d.editor.Buffer
	at := buffer.Pos(offset)
	line := buffer.Line(at.Line)
	word := wordAt(line, at.Col)
	if word == "" {
		a.openReferences(outlineNotice{message: locales.ReferencesNeedWord()})
		return
	}
	a.openReferences(outlineNotice{})
	s := &a.references
	s.loading, s.word = true, word
	s.origin = place{path: d.path, external: d.external, position: at}
	document := lsp.Document{Path: file, Language: language, Version: buffer.Version(), Text: buffer.String()}
	position := lsp.Position{Line: at.Line, Character: lsp.UTF16Column(line, at.Col)}
	unsaved := a.unsavedTexts()
	pool := a.languagePool()
	server := known.Server
	ctx, cancel := context.WithTimeout(context.Background(), referencesTimeout)
	s.cancel = cancel
	request := s.request
	go func() {
		defer cancel()
		found, err := pool.References(ctx, server, document, position)
		var results []reference
		if err == nil {
			results = referencesOf(a.root, file, found, unsaved)
		}
		a.post(func() {
			if a.references.open && a.references.request == request {
				a.referencesAnswered(server, results, len(found), err)
			}
		})
	}()
}

// unsavedTexts returns the text of each open file with unsaved edits, by its
// absolute path, which the server reads in place of the file's.
func (a *App) unsavedTexts() map[string]string {
	texts := map[string]string{}
	for _, d := range a.documents {
		if d.editor != nil && d.modified() {
			texts[d.absolutePath(a.root)] = d.editor.Buffer.String()
		}
	}
	return texts
}

func (a *App) referencesAnswered(server lsp.Server, results []reference, found int, err error) {
	s := &a.references
	s.loading, s.cancel = false, nil
	a.closeIdleServersLater()
	switch {
	case errors.Is(err, context.Canceled):
	case errors.Is(err, lsp.ErrServerMissing):
		detail := ""
		if strings.TrimSpace(server.Install) != "" {
			detail = locales.ServerInstall(server.Install)
		}
		s.notice = outlineNotice{message: locales.ServerMissing(server.Name), detail: detail}
	case errors.Is(err, lsp.ErrUnsupported):
		s.notice = outlineNotice{message: locales.ReferencesServerUnsupported(server.Name)}
	case err != nil:
		slog.Warn("find references", "server", server.Name, "root", a.root, "error", err)
		s.notice = outlineNotice{message: locales.ReferencesFailed(), detail: err.Error()}
	default:
		s.results, s.more, s.chosen = results, found > len(results), 0
		s.files = countReferenceFiles(results)
		s.list.ScrollTo(0, ui.Start)
	}
}

// referencesOf orders the uses, those in the asking file first, and reads the
// line of each, from unsaved text where a file has it.
func referencesOf(root string, asking string, found []lsp.Location, unsaved map[string]string) []reference {
	sorted := slices.Clone(found)
	slices.SortStableFunc(sorted, func(x lsp.Location, y lsp.Location) int {
		return cmp.Or(
			cmp.Compare(referenceFileOrder(x.Path, asking), referenceFileOrder(y.Path, asking)),
			cmp.Compare(x.Path, y.Path),
			cmp.Compare(x.Range.Start.Line, y.Range.Start.Line),
			cmp.Compare(x.Range.Start.Character, y.Range.Start.Character),
		)
	})
	sorted = sorted[:min(len(sorted), maxReferences)]
	lines := map[string][]string{}
	results := make([]reference, 0, len(sorted))
	for _, location := range sorted {
		fileLines, read := lines[location.Path]
		if !read {
			fileLines = referenceFileLines(location.Path, unsaved)
			lines[location.Path] = fileLines
		}
		shown := location.Path
		if relPath, inside := insideProject(root, location.Path); inside {
			shown = relPath
		}
		text, mark := referenceLine(fileLines, location.Range)
		results = append(results, reference{location: location, shown: filepath.ToSlash(shown), line: text, mark: mark})
	}
	return results
}

// referenceFileOrder puts the asking file before the others.
func referenceFileOrder(file string, asking string) int {
	if file == asking {
		return 0
	}
	return 1
}

// referenceFileLines returns the lines of a file, nil when it cannot be read,
// as its row then shows no line.
func referenceFileLines(file string, unsaved map[string]string) []string {
	if text, open := unsaved[file]; open {
		return strings.Split(text, "\n")
	}
	data, err := os.ReadFile(file)
	if err != nil {
		slog.Warn("read a reference's file", "path", file, "error", err)
		return nil
	}
	return strings.Split(string(data), "\n")
}

// referenceLine returns the line of a use without its indentation, cut to
// what a row shows, and where the use is in it.
func referenceLine(lines []string, span lsp.Range) (string, textMark) {
	if span.Start.Line < 0 || span.Start.Line >= len(lines) {
		return "", textMark{}
	}
	line := strings.TrimRight(lines[span.Start.Line], "\r")
	start := lsp.ByteColumn(line, span.Start.Character)
	end := len(line)
	if span.End.Line == span.Start.Line {
		end = lsp.ByteColumn(line, span.End.Character)
	}
	end = max(start, min(end, len(line)))
	indent := len(line) - len(strings.TrimLeft(line, " \t"))
	cut := min(indent, start)
	if start-cut > maxReferenceLine/2 {
		cut = start - maxReferenceLine/2
	}
	cut = runeStart(line, cut)
	stop := runeStart(line, min(len(line), cut+maxReferenceLine))
	stop = max(stop, min(end, len(line)))
	return line[cut:stop], textMark{start: start - cut, end: min(end, stop) - cut}
}

// runeStart moves a byte offset back to the start of the character it falls
// in.
func runeStart(text string, offset int) int {
	for offset > 0 && offset < len(text) && text[offset]&0xC0 == 0x80 {
		offset--
	}
	return offset
}

// countReferenceFiles returns how many files the uses are in.
func countReferenceFiles(results []reference) int {
	files := 0
	for index, result := range results {
		if index == 0 || result.location.Path != results[index-1].location.Path {
			files++
		}
	}
	return files
}

// closeReferences closes the list and ends its request.
func (a *App) closeReferences() {
	request := a.references.request + 1
	if a.references.cancel != nil {
		a.references.cancel()
	}
	a.references = referencesState{request: request}
}

func (a *App) moveReferenceChoice(step int) {
	s := &a.references
	count := len(s.results)
	if count == 0 {
		return
	}
	s.chosen = (s.chosen + step + count) % count
	s.list.ScrollIntoView(s.chosen)
}

// acceptReference goes to the chosen use, which Go Back returns from.
func (a *App) acceptReference() {
	s := &a.references
	if s.loading || s.chosen < 0 || s.chosen >= len(s.results) {
		return
	}
	target := s.results[s.chosen].location
	origin := s.origin
	a.closeReferences()
	a.showDefinition(origin, target)
}

func (a *App) referencesView(c *ui.Context) {
	if !a.references.open {
		return
	}
	a.finderPanel(c, &a.references.open, func() { a.referencesContent(c) })
	if !a.references.open {
		a.closeReferences()
	}
}

func (a *App) referencesContent(c *ui.Context) {
	colors := a.look.palette
	s := &a.references
	finderMoveShortcuts(c, a.moveReferenceChoice)
	if c.Shortcut(0, ui.KeyEnter) {
		a.acceptReference()
		return
	}
	ui.Column(c).FillWidth().Children(func() {
		ui.Row(c).FillWidth().Padding(13, 16).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, locales.ReferencesTitle()).FontSize(15).Shrink(0)
			if s.word != "" {
				ui.Text(c, s.word).Font("monospace").FontSize(14).TextColor(colors.accent).MaxLines(1).Shrink(1)
			}
			ui.Spacer(c)
			if summary := a.referencesSummary(); summary != "" {
				ui.Text(c, summary).FontSize(smallFontSize - 1).TextColor(colors.muted).Shrink(0)
			}
		})
		ui.Box(c).FillWidth().Height(1).Background(colors.border)
		a.referenceResults(c)
	})
}

// referencesSummary counts the uses and their files, once they are known.
func (a *App) referencesSummary() string {
	s := &a.references
	if s.loading || len(s.results) == 0 {
		return ""
	}
	if s.more {
		return locales.ReferencesCountMore(len(s.results), s.files)
	}
	return locales.ReferencesCount(len(s.results), s.files)
}

// referencesMessage says why the list shows no rows, and is empty when it has
// rows to show.
func (a *App) referencesMessage() outlineNotice {
	s := &a.references
	switch {
	case s.notice.message != "":
		return s.notice
	case s.loading:
		return outlineNotice{message: locales.ReadingReferences()}
	case len(s.results) == 0:
		return outlineNotice{message: locales.NoReferences(s.word)}
	default:
		return outlineNotice{}
	}
}

func (a *App) referenceResults(c *ui.Context) {
	s := &a.references
	if message := a.referencesMessage(); message.message != "" {
		a.finderNote(c, message.message, message.detail)
		return
	}
	clicked := noRow
	ui.List(c, &s.list, len(s.results), func(row int) {
		if a.referenceRow(c, row) {
			clicked = row
		}
	}).FillWidth().Height(finderHeightFor(len(s.results))).Padding(finderListInset)
	if clicked != noRow {
		s.chosen = clicked
		a.acceptReference()
	}
}

// referenceRow builds one use, its file and line number then its line, and
// reports a click on it.
func (a *App) referenceRow(c *ui.Context, index int) bool {
	colors := a.look.palette
	result := a.references.results[index]
	row := a.finderRow(c, a.references.chosen, index)
	clicked := row.Clicked()
	row.Children(func() {
		a.fileIconView(c, result.shown, iconFontSize)
		ui.Text(c, path.Base(result.shown)+":"+strconv.Itoa(result.location.Range.Start.Line+1)).
			FontSize(smallFontSize).MaxLines(1).Shrink(0)
		marks := []textMark{}
		if result.mark.end > result.mark.start {
			marks = append(marks, result.mark)
		}
		ui.RichText(c, markedSpans(result.line, marks, colors.muted, colors.text)...).
			Font("monospace").FontSize(smallFontSize - 1).MaxLines(1).Shrink(1).MinWidth(0)
		ui.Spacer(c)
		if folder := path.Dir(result.shown); folder != "." {
			ui.Text(c, folder).FontSize(smallFontSize - 2).TextColor(colors.muted).MaxLines(1).Shrink(2).MinWidth(0)
		}
	})
	return clicked
}
