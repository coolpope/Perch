package app

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown/parser"

	"perch/internal/lsp"
	"perch/internal/search"
	"perch/locales"
)

const outlineTimeout time.Duration = time.Minute

// outlineKind is the kind of a symbol as the outline groups it.
type outlineKind uint8

const (
	outlineOther outlineKind = iota
	outlineFunction
	outlineMethod
	outlineType
	outlineField
	outlineConstant
	outlineVariable
	outlineHeading
)

// outlineSymbol is one symbol of a document. Its positions are fresh from
// the language server or the Markdown parser, and are clamped to the current
// buffer when they are used.
type outlineSymbol struct {
	name      string
	container string
	kind      outlineKind
	span      lsp.Range
}

// outlineMatch is a symbol that matches the query, with the byte offsets of
// the matched characters in its name.
type outlineMatch struct {
	symbol    int
	positions []int
}

// outlineOrigin is what the editor showed before the finder opened. Previewing
// a symbol only scrolls, so the caret and the selections stay where they were.
type outlineOrigin struct {
	document *document
	scroll   float64
	view     int
}

// outlineNotice says why the finder cannot list symbols, with a hint when it
// has one.
type outlineNotice struct {
	message string
	detail  string
}

// outlineState is the state of the symbol finder. It holds the symbols only
// while it shows, so that a closed finder costs no memory.
type outlineState struct {
	open    bool
	query   string
	loading bool
	symbols []outlineSymbol
	matches []outlineMatch
	chosen  int
	notice  outlineNotice
	list    ui.ListState
	input   ui.Handle
	cancel  context.CancelFunc
	origin  outlineOrigin
	// previewed reports that the editor moved to show a symbol.
	previewed bool

	// previewPending is the line to scroll to once the editor lays out, as
	// after showing a Markdown preview's source.
	previewPending bool
	previewLine    int
}

// showOutline opens the symbol finder for the active document. It starts a
// language server only now, when the file needs one, and shares it with go to
// definition. The request goroutine belongs to its context: closing the finder,
// opening another folder, or closing the window cancels it.
func (a *App) showOutline() {
	if a.outline.open {
		a.outline.input.Focus()
		return
	}
	a.hideQuickOpen()
	a.closeOutline()
	a.outlineRequest++
	request := a.outlineRequest

	d := a.activeDocument()
	origin := outlineOrigin{}
	if d != nil && d.editor != nil {
		origin = outlineOrigin{document: d, scroll: d.editor.ScrollY(), view: d.markdownView}
	}
	a.outline = outlineState{open: true, origin: origin}
	a.outline.input.Focus()

	switch {
	case d == nil:
		a.outline.notice = outlineNotice{message: locales.OpenFileForSymbols()}
	case d.editor == nil, d.showsSVGPreview():
		a.outline.notice = outlineNotice{message: locales.SymbolsForImage()}
	case d.isMarkdown():
		a.outline.symbols = markdownOutlineSymbols(d)
		sortOutlineSymbols(a.outline.symbols)
		a.rankOutline()
	default:
		file := d.absolutePath(a.root)
		registry := a.languageRegistry()
		known, language, supported := registry.Match(file)
		if !supported {
			a.outline.notice = outlineNotice{message: locales.SymbolsUnsupported(d.name())}
			return
		}
		if !registry.Enabled(known.ID) {
			a.outline.notice = outlineNotice{message: locales.SymbolsTurnedOff(known.Name), detail: locales.DefinitionTurnOnHint()}
			return
		}
		a.requestOutlineSymbols(d, known.Server, language, request)
	}
}

// requestOutlineSymbols asks the file's language server for its symbols off
// the main thread. The goroutine ends with the answer, or when its context is
// canceled.
func (a *App) requestOutlineSymbols(d *document, server lsp.Server, language string, request uint64) {
	buffer := d.editor.Buffer
	document := lsp.Document{
		Path: d.absolutePath(a.root), Language: language,
		Version: buffer.Version(), Text: buffer.String(),
	}
	pool := a.languagePool()
	ctx, cancel := context.WithTimeout(context.Background(), outlineTimeout)
	a.outline.cancel = cancel
	a.outline.loading = true
	go func() {
		defer cancel()
		symbols, err := pool.DocumentSymbols(ctx, server, document)
		a.post(func() {
			if request == a.outlineRequest && a.outline.open {
				a.outlineAnswered(server, symbols, err)
			}
		})
	}()
}

func (a *App) outlineAnswered(server lsp.Server, symbols []lsp.Symbol, err error) {
	a.outline.loading = false
	a.closeIdleServersLater()
	switch {
	case errors.Is(err, context.Canceled):
		return
	case errors.Is(err, lsp.ErrServerMissing):
		detail := ""
		if strings.TrimSpace(server.Install) != "" {
			detail = locales.ServerInstall(server.Install)
		}
		a.outline.notice = outlineNotice{message: locales.ServerMissing(server.Name), detail: detail}
	case err != nil:
		slog.Warn("document symbols", "server", server.Name, "root", a.root, "error", err)
		a.outline.notice = outlineNotice{message: locales.SymbolsFailed()}
	default:
		a.outline.symbols = outlineSymbolsOf(symbols)
		sortOutlineSymbols(a.outline.symbols)
		a.rankOutline()
	}
}

func outlineSymbolsOf(symbols []lsp.Symbol) []outlineSymbol {
	out := make([]outlineSymbol, 0, len(symbols))
	for _, symbol := range symbols {
		name, container := outlineSymbolText(symbol.Name, symbol.Container)
		if name == "" {
			continue
		}
		out = append(out, outlineSymbol{
			name: name, container: container,
			kind: outlineKindOf(symbol.Kind), span: symbol.SelectionRange,
		})
	}
	return out
}

// outlineSymbolText splits the method names some servers send, such as Go's
// "(Store).Save", into the name and its container.
func outlineSymbolText(name string, container string) (string, string) {
	name = strings.TrimSpace(name)
	container = strings.TrimSpace(container)
	if !strings.HasPrefix(name, "(") {
		return name, container
	}
	end := strings.Index(name, ").")
	if end <= 1 {
		return name, container
	}
	receiver := strings.TrimPrefix(name[1:end], "*")
	method := strings.TrimSpace(name[end+2:])
	if receiver == "" || method == "" {
		return name, container
	}
	if container == "" {
		container = receiver
	}
	return method, container
}

func outlineKindOf(kind lsp.SymbolKind) outlineKind {
	switch kind {
	case lsp.SymbolFunction:
		return outlineFunction
	case lsp.SymbolMethod, lsp.SymbolConstructor:
		return outlineMethod
	case lsp.SymbolClass, lsp.SymbolEnum, lsp.SymbolInterface, lsp.SymbolStruct, lsp.SymbolTypeParameter:
		return outlineType
	case lsp.SymbolProperty, lsp.SymbolField, lsp.SymbolEnumMember, lsp.SymbolEvent:
		return outlineField
	case lsp.SymbolConstant:
		return outlineConstant
	case lsp.SymbolVariable:
		return outlineVariable
	default:
		return outlineOther
	}
}

func sortOutlineSymbols(symbols []outlineSymbol) {
	slices.SortStableFunc(symbols, func(a, b outlineSymbol) int {
		switch {
		case a.span.Start.Line < b.span.Start.Line:
			return -1
		case a.span.Start.Line > b.span.Start.Line:
			return 1
		case a.span.Start.Character < b.span.Start.Character:
			return -1
		case a.span.Start.Character > b.span.Start.Character:
			return 1
		default:
			return 0
		}
	})
}

// markdownOutlineSymbols reads the headings of the buffer without a language
// server, nesting each level under the heading before it.
func markdownOutlineSymbols(d *document) []outlineSymbol {
	source := d.editor.Buffer.String()
	doc := parser.Parse(source)
	type level struct {
		value int
		name  string
	}
	var stack []level
	symbols := make([]outlineSymbol, 0)
	for index := range doc.Len() {
		block := doc.Block(index)
		if block.Kind != parser.Heading {
			continue
		}
		name := markdownHeadingText(doc, index)
		if name == "" {
			name = markdownHeadingFallback(source, block.Start, block.End)
		}
		if name == "" {
			continue
		}
		for len(stack) > 0 && stack[len(stack)-1].value >= block.Level {
			stack = stack[:len(stack)-1]
		}
		container := ""
		if len(stack) > 0 {
			container = stack[len(stack)-1].name
		}
		line := d.editor.Buffer.Pos(block.Start).Line
		span := lsp.Range{
			Start: lsp.Position{Line: line, Character: 0},
			End:   lsp.Position{Line: line, Character: 0},
		}
		symbols = append(symbols, outlineSymbol{name: name, container: container, kind: outlineHeading, span: span})
		stack = append(stack, level{value: block.Level, name: name})
	}
	return symbols
}

// markdownHeadingText joins the visible text of a heading, without its
// Markdown marks.
func markdownHeadingText(doc *parser.Document, block int) string {
	var text strings.Builder
	for _, inline := range doc.Inlines(block) {
		switch inline.Kind {
		case parser.Text, parser.Code, parser.Math:
			text.WriteString(inline.Text)
		case parser.SoftBreak, parser.LineBreak:
			text.WriteByte(' ')
		}
	}
	return strings.Join(strings.Fields(text.String()), " ")
}

// markdownHeadingFallback reads a heading whose inline text is empty, as a
// heading of nothing but marks.
func markdownHeadingFallback(source string, start int, end int) string {
	if start < 0 || end > len(source) || start >= end {
		return ""
	}
	line, _, _ := strings.Cut(source[start:end], "\n")
	line = strings.TrimSpace(line)
	line = strings.TrimSpace(strings.TrimLeft(line, "#"))
	line = strings.TrimSpace(strings.TrimRight(line, "#"))
	return strings.Join(strings.Fields(line), " ")
}

// closeOutline cancels the request and drops the symbols.
func (a *App) closeOutline() {
	a.outlineRequest++
	if a.outline.cancel != nil {
		a.outline.cancel()
	}
	a.outline = outlineState{}
}

// dismissOutline closes the finder and returns the editor to what it showed.
func (a *App) dismissOutline() {
	origin := a.outline.origin
	previewed := a.outline.previewed
	a.closeOutline()
	d := origin.document
	if !previewed || d == nil || d.editor == nil || slices.Index(a.documents, d) == noDocument {
		return
	}
	d.markdownView = origin.view
	d.editor.SetScrollY(origin.scroll)
}

// outlineDocument returns the document the finder was opened for, while it is
// still open in a tab.
func (a *App) outlineDocument() (*document, bool) {
	d := a.outline.origin.document
	if d == nil || d.editor == nil || slices.Index(a.documents, d) == noDocument {
		return nil, false
	}
	return d, true
}

// rankOutline orders the symbols by the query: file order while it is empty,
// and the ranking of search.RankPaths once it names something.
func (a *App) rankOutline() {
	q := &a.outline
	q.previewPending = false
	q.matches = nil
	query := strings.TrimSpace(q.query)
	if query == "" {
		q.matches = make([]outlineMatch, 0, len(q.symbols))
		for index := range q.symbols {
			q.matches = append(q.matches, outlineMatch{symbol: index})
		}
	} else {
		byName := make(map[string][]int, len(q.symbols))
		names := make([]string, 0, len(q.symbols))
		for index, symbol := range q.symbols {
			if _, seen := byName[symbol.name]; !seen {
				names = append(names, symbol.name)
			}
			byName[symbol.name] = append(byName[symbol.name], index)
		}
		for _, match := range search.RankPaths(names, query, len(names)) {
			for _, index := range byName[match.Path] {
				q.matches = append(q.matches, outlineMatch{symbol: index, positions: match.Positions})
			}
		}
	}
	q.chosen = 0
	q.list.ScrollTo(0, ui.Start)
}

func (a *App) moveOutlineChoice(step int) {
	q := &a.outline
	count := len(q.matches)
	if count == 0 {
		return
	}
	q.chosen = (q.chosen + step + count) % count
	q.list.ScrollIntoView(q.chosen)
	a.previewOutlineChoice()
}

// previewOutlineChoice shows the chosen symbol in the editor by scrolling to
// it, without taking the keyboard from the finder.
func (a *App) previewOutlineChoice() {
	q := &a.outline
	if q.chosen < 0 || q.chosen >= len(q.matches) {
		return
	}
	d, ok := a.outlineDocument()
	if !ok {
		return
	}
	symbol := q.symbols[q.matches[q.chosen].symbol]
	position := editorPos(d, symbol.span.Start)
	if d.isMarkdown() && d.showsPreview() {
		d.markdownView = markdownSource
	}
	q.previewLine = position.Line
	q.previewPending = true
	q.previewed = true
}

// applyOutlinePreview scrolls to the chosen symbol once its editor has laid
// out, which a Markdown preview does only in the next frame.
func (a *App) applyOutlinePreview(c *ui.Context) {
	q := &a.outline
	if !q.previewPending {
		return
	}
	d, ok := a.outlineDocument()
	if !ok {
		q.previewPending = false
		return
	}
	if d.editor.ViewportHeight() <= 0 {
		c.After(0)
		return
	}
	line := max(0, min(q.previewLine, d.editor.Buffer.LineCount()-1))
	d.editor.ScrollTo(line)
	q.previewPending = false
}

// acceptOutline goes to the chosen symbol and moves the keyboard to the
// editor.
func (a *App) acceptOutline() {
	q := &a.outline
	if q.chosen < 0 || q.chosen >= len(q.matches) {
		return
	}
	d, ok := a.outlineDocument()
	if !ok {
		return
	}
	symbol := q.symbols[q.matches[q.chosen].symbol]
	position := editorPos(d, symbol.span.Start)
	a.closeOutline()
	d.reveal(position, position)
}

func (a *App) outlineView(c *ui.Context) {
	if !a.outline.open {
		return
	}
	a.finderPanel(c, &a.outline.open, func() { a.outlineContent(c) })
	if !a.outline.open {
		a.dismissOutline()
		return
	}
	a.applyOutlinePreview(c)
}

func (a *App) outlineContent(c *ui.Context) {
	colors := a.look.palette
	finderMoveShortcuts(c, a.moveOutlineChoice)
	submitted := false
	ui.Column(c).FillWidth().Children(func() {
		ui.Row(c).FillWidth().Padding(13, 16).Children(func() {
			input := ui.TextInputBase(c, &a.outline.query).Bind(&a.outline.input).Grow(1).FontSize(15).
				Placeholder(locales.GoToSymbolPlaceholder()).Label(locales.GoToSymbol())
			if input.Changed() {
				a.rankOutline()
			}
			submitted = input.Submitted()
		})
		ui.Box(c).FillWidth().Height(1).Background(colors.border)
		a.outlineResults(c)
	})
	if submitted {
		a.acceptOutline()
	}
}

// outlineMessage says why the finder shows no rows, and is empty when it has
// rows to show.
func (a *App) outlineMessage() outlineNotice {
	q := &a.outline
	switch {
	case q.notice.message != "":
		return q.notice
	case q.loading:
		return outlineNotice{message: locales.ReadingSymbols()}
	case len(q.symbols) == 0:
		return outlineNotice{message: locales.NoSymbols()}
	case len(q.matches) == 0:
		return outlineNotice{message: locales.NoMatchingSymbols()}
	default:
		return outlineNotice{}
	}
}

func (a *App) outlineResults(c *ui.Context) {
	q := &a.outline
	if message := a.outlineMessage(); message.message != "" {
		a.finderNote(c, message.message, message.detail)
		return
	}
	clicked := noRow
	ui.List(c, &q.list, len(q.matches), func(row int) {
		if a.outlineRow(c, row) {
			clicked = row
		}
	}).FillWidth().Height(finderHeightFor(len(q.matches))).Padding(finderListInset)
	if clicked != noRow {
		q.chosen = clicked
		a.acceptOutline()
	}
}

// outlineRow builds one symbol row and reports a click on it.
func (a *App) outlineRow(c *ui.Context, index int) bool {
	colors := a.look.palette
	match := a.outline.matches[index]
	symbol := a.outline.symbols[match.symbol]
	row := a.finderRow(c, a.outline.chosen, index)
	clicked := row.Clicked()
	row.Children(func() {
		markedText(c, symbol.name, marksFrom(match.positions, 0), colors.text, colors.accent)
		if symbol.container != "" {
			ui.Text(c, symbol.container).FontSize(smallFontSize - 1).TextColor(colors.muted).MaxLines(1).Shrink(1)
		}
		ui.Spacer(c)
		ui.Row(c).Gap(4).Shrink(0).AlignItems(ui.Center).Children(func() {
			ui.Box(c).Size(5, 5).Radius(3).Background(outlineKindColor(symbol.kind, colors))
			ui.Text(c, outlineKindLabel(symbol.kind)).FontSize(smallFontSize - 2).TextColor(colors.muted)
		})
	})
	return clicked
}

func outlineKindLabel(kind outlineKind) string {
	switch kind {
	case outlineFunction:
		return locales.SymbolFunction()
	case outlineMethod:
		return locales.SymbolMethod()
	case outlineType:
		return locales.SymbolType()
	case outlineField:
		return locales.SymbolField()
	case outlineConstant:
		return locales.SymbolConstant()
	case outlineVariable:
		return locales.SymbolVariable()
	case outlineHeading:
		return locales.SymbolHeading()
	default:
		return locales.SymbolOther()
	}
}

func outlineKindColor(kind outlineKind, colors palette) ui.Color {
	switch kind {
	case outlineFunction, outlineMethod:
		return colors.function
	case outlineType, outlineHeading:
		return colors.typeName
	case outlineField:
		return colors.markup
	case outlineConstant:
		return colors.number
	case outlineVariable:
		return colors.str
	default:
		return colors.muted
	}
}
