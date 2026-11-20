package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/egoist/mygo/ui"

	"github.com/marukolibro/Perch/internal/lsp"
	"github.com/marukolibro/Perch/locales"
)

const (
	// projectSymbolDelay waits for typing to pause before asking the server.
	projectSymbolDelay   time.Duration = 150 * time.Millisecond
	projectSymbolTimeout time.Duration = time.Minute
	// maxProjectSymbols keeps the list to what a person reads.
	maxProjectSymbols int = 200
)

// projectSymbol is a symbol found anywhere in the project.
type projectSymbol struct {
	name      string
	container string
	kind      outlineKind
	location  lsp.Location
	// shown is where it is, relative to the project when it is inside.
	shown     string
	positions []int
}

// projectSymbolsState is the finder of symbols across the project, asked of
// the language server of the open file.
type projectSymbolsState struct {
	open    bool
	query   string
	loading bool
	// asked is the query the results answer.
	asked   string
	results []projectSymbol
	chosen  int
	notice  outlineNotice
	list    ui.ListState
	input   ui.Handle
	cancel  context.CancelFunc
	request uint64
	server  lsp.Server
	// anchor is the absolute path of the file whose server answers.
	anchor string
}

// showProjectSymbols opens the finder for the language of the open file.
func (a *App) showProjectSymbols() {
	if a.projectSymbols.open {
		a.projectSymbols.input.Focus()
		return
	}
	a.hideQuickOpen()
	a.closeOutline()
	a.closeReferences()
	a.closeProjectSymbols()
	s := &a.projectSymbols
	s.open = true
	s.input.Focus()
	d := a.activeTextDocument()
	if d == nil || d.showsSVGPreview() {
		s.notice = outlineNotice{message: locales.ProjectSymbolsNeedFile()}
		return
	}
	file := d.absolutePath(a.root)
	registry := a.languageRegistry()
	known, _, supported := registry.Match(file)
	switch {
	case !supported:
		s.notice = outlineNotice{message: locales.SymbolsUnsupported(d.name())}
	case !registry.Enabled(known.ID):
		s.notice = outlineNotice{message: locales.SymbolsTurnedOff(known.Name), detail: locales.DefinitionTurnOnHint()}
	default:
		s.server, s.anchor = known.Server, file
	}
}

// closeProjectSymbols closes the finder and ends its request.
func (a *App) closeProjectSymbols() {
	request := a.projectSymbols.request + 1
	if a.projectSymbols.cancel != nil {
		a.projectSymbols.cancel()
	}
	a.projectSymbols = projectSymbolsState{request: request}
}

// projectQueryChanged asks for the symbols of the new query once typing pauses.
func (a *App) projectQueryChanged() {
	s := &a.projectSymbols
	s.request++
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
	query := strings.TrimSpace(s.query)
	if query == "" || s.anchor == "" {
		s.loading, s.results, s.asked, s.chosen = false, nil, "", 0
		return
	}
	s.loading = true
	request := s.request
	a.after(projectSymbolDelay, func() {
		if a.projectSymbols.open && a.projectSymbols.request == request {
			a.requestProjectSymbols(query, request)
		}
	})
}

// requestProjectSymbols asks the server off the main thread. The goroutine
// ends with the answer, or when a newer query or closing cancels it.
func (a *App) requestProjectSymbols(query string, request uint64) {
	s := &a.projectSymbols
	pool := a.languagePool()
	ctx, cancel := context.WithTimeout(context.Background(), projectSymbolTimeout)
	s.cancel = cancel
	server, anchor := s.server, s.anchor
	go func() {
		defer cancel()
		symbols, err := pool.WorkspaceSymbols(ctx, server, anchor, query)
		a.post(func() {
			if a.projectSymbols.open && a.projectSymbols.request == request {
				a.projectSymbolsAnswered(query, symbols, err)
			}
		})
	}()
}

func (a *App) projectSymbolsAnswered(query string, symbols []lsp.ProjectSymbol, err error) {
	s := &a.projectSymbols
	s.loading = false
	s.cancel = nil
	a.closeIdleServersLater()
	switch {
	case errors.Is(err, context.Canceled):
		return
	case errors.Is(err, lsp.ErrServerMissing):
		detail := ""
		if strings.TrimSpace(s.server.Install) != "" {
			detail = locales.ServerInstall(s.server.Install)
		}
		s.notice = outlineNotice{message: locales.ServerMissing(s.server.Name), detail: detail}
	case errors.Is(err, lsp.ErrUnsupported):
		s.notice = outlineNotice{message: locales.ProjectSymbolsServerUnsupported(s.server.Name)}
	case err != nil:
		slog.Warn("project symbols", "server", s.server.Name, "root", a.root, "error", err)
		s.notice = outlineNotice{message: locales.SymbolsFailed()}
	default:
		s.results, s.asked, s.chosen = a.projectSymbolsOf(query, symbols), query, 0
		s.list.ScrollTo(0, ui.Start)
	}
}

func (a *App) projectSymbolsOf(query string, symbols []lsp.ProjectSymbol) []projectSymbol {
	out := make([]projectSymbol, 0, min(len(symbols), maxProjectSymbols))
	for _, symbol := range symbols[:min(len(symbols), maxProjectSymbols)] {
		name, container := outlineSymbolText(symbol.Name, symbol.Container)
		shown := symbol.Path
		if relPath, inside := insideProject(a.root, symbol.Path); inside {
			shown = relPath
		}
		out = append(out, projectSymbol{
			name: name, container: container, kind: outlineKindOf(symbol.Kind),
			location: lsp.Location{Path: symbol.Path, Range: symbol.Range},
			shown:    fmt.Sprintf("%s:%d", shown, symbol.Range.Start.Line+1), positions: subsequenceOf(query, name),
		})
	}
	return out
}

// subsequenceOf returns where the letters of query fall in name, ignoring case,
// or nothing when they do not all appear in order.
func subsequenceOf(query string, name string) []int {
	wanted := []rune(strings.ToLower(query))
	positions := make([]int, 0, len(wanted))
	next := 0
	for index, char := range name {
		if next < len(wanted) && unicode.ToLower(char) == wanted[next] {
			positions = append(positions, index)
			next++
		}
	}
	if next < len(wanted) {
		return nil
	}
	return positions
}

func (a *App) moveProjectSymbolChoice(step int) {
	s := &a.projectSymbols
	if len(s.results) == 0 {
		return
	}
	s.chosen = max(0, min(s.chosen+step, len(s.results)-1))
	s.list.ScrollIntoView(s.chosen)
}

// acceptProjectSymbol goes to the chosen symbol, which Go Back returns from.
func (a *App) acceptProjectSymbol() {
	s := &a.projectSymbols
	if s.loading || s.chosen < 0 || s.chosen >= len(s.results) {
		return
	}
	target := s.results[s.chosen].location
	a.closeProjectSymbols()
	if origin, known := a.currentPlace(); known {
		a.showDefinition(origin, target)
		return
	}
	a.openLocation(target)
}

func (a *App) projectSymbolsView(c *ui.Context) {
	if !a.projectSymbols.open {
		return
	}
	a.finderPanel(c, &a.projectSymbols.open, func() { a.projectSymbolsContent(c) })
	if !a.projectSymbols.open {
		a.closeProjectSymbols()
	}
}

func (a *App) projectSymbolsContent(c *ui.Context) {
	colors := a.look.palette
	s := &a.projectSymbols
	finderMoveShortcuts(c, a.moveProjectSymbolChoice)
	submitted := false
	ui.Column(c).FillWidth().Children(func() {
		ui.Row(c).FillWidth().Padding(13, 16).Children(func() {
			input := ui.TextInputBase(c, &s.query).Bind(&s.input).Grow(1).FontSize(15).
				Placeholder(locales.ProjectSymbolsPlaceholder()).Label(locales.MenuGoToProjectSymbol())
			if input.Changed() {
				a.projectQueryChanged()
			}
			submitted = input.Submitted()
		})
		ui.Box(c).FillWidth().Height(1).Background(colors.border)
		a.projectSymbolResults(c)
	})
	if submitted {
		a.acceptProjectSymbol()
	}
}

// projectSymbolsMessage says why the finder shows no rows, and is empty when
// it has rows to show.
func (a *App) projectSymbolsMessage() outlineNotice {
	s := &a.projectSymbols
	switch {
	case s.notice.message != "":
		return s.notice
	case strings.TrimSpace(s.query) == "":
		return outlineNotice{message: locales.ProjectSymbolsHint()}
	case s.loading && len(s.results) == 0:
		return outlineNotice{message: locales.ReadingSymbols()}
	case len(s.results) == 0 && s.asked != "":
		return outlineNotice{message: locales.NoMatchingSymbols()}
	case len(s.results) == 0:
		return outlineNotice{message: locales.ReadingSymbols()}
	default:
		return outlineNotice{}
	}
}

func (a *App) projectSymbolResults(c *ui.Context) {
	s := &a.projectSymbols
	if message := a.projectSymbolsMessage(); message.message != "" {
		a.finderNote(c, message.message, message.detail)
		return
	}
	clicked := noRow
	ui.List(c, &s.list, len(s.results), func(row int) {
		if a.projectSymbolRow(c, row) {
			clicked = row
		}
	}).FillWidth().Height(finderHeightFor(len(s.results))).Padding(finderListInset)
	if clicked != noRow {
		s.chosen = clicked
		a.acceptProjectSymbol()
	}
}

// projectSymbolRow builds one row and reports a click on it.
func (a *App) projectSymbolRow(c *ui.Context, index int) bool {
	colors := a.look.palette
	symbol := a.projectSymbols.results[index]
	row := a.finderRow(c, a.projectSymbols.chosen, index)
	clicked := row.Clicked()
	row.Children(func() {
		ui.Box(c).Size(5, 5).Radius(3).Shrink(0).Background(outlineKindColor(symbol.kind, colors))
		markedText(c, symbol.name, marksFrom(symbol.positions, 0), colors.text, colors.accent)
		if symbol.container != "" {
			ui.Text(c, symbol.container).FontSize(smallFontSize - 1).TextColor(colors.muted).MaxLines(1).Shrink(1)
		}
		ui.Spacer(c)
		ui.Text(c, filepath.ToSlash(symbol.shown)).FontSize(smallFontSize - 1).TextColor(colors.muted).MaxLines(1).Shrink(2)
	})
	return clicked
}
