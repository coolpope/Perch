package app

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"github.com/egoist/mygo/ui/markdown"

	"github.com/marukolibro/Perch/internal/lsp"
)

const (
	hoverTimeout   time.Duration = 10 * time.Second
	hoverMaxWidth  float32       = 520
	hoverMaxHeight float32       = 320
)

// hoverState is the language server's hover being asked for. Only one is out
// at a time: a newer one cancels it.
type hoverState struct {
	request uint64
	cancel  context.CancelFunc
	// options renders hovers in the theme they were made for.
	options *markdown.Options
	theme   *markdown.Theme
}

// answerHover shows what the pointer rests on: the line's commit while blame
// is on, and otherwise what the file's language server says.
func (a *App) answerHover(d *document, request editor.HoverRequest) {
	if d.blame != nil {
		a.blameHover(d, request)
		return
	}
	a.languageHover(d, request)
}

// stopHover cancels the hover being asked for.
func (a *App) stopHover() {
	if a.hover.cancel != nil {
		a.hover.cancel()
	}
	a.hover = hoverState{request: a.hover.request + 1}
}

// languageHover asks the file's language server about the word under the
// pointer, only when the server already runs: going to a definition or
// opening the symbols starts it, and resting the pointer does not. The
// goroutine ends with the answer, or when a newer hover or stopHover cancels
// it.
func (a *App) languageHover(d *document, request editor.HoverRequest) {
	pool := a.definition.pool
	if pool == nil || d.showsPreview() {
		return
	}
	file := d.absolutePath(a.root)
	registry := a.languageRegistry()
	known, language, supported := registry.Match(file)
	if !supported || !registry.Enabled(known.ID) {
		return
	}
	buffer := d.editor.Buffer
	if request.Version != buffer.Version() {
		return
	}
	at := buffer.Pos(request.Offset)
	line := buffer.Line(at.Line)
	if wordAt(line, at.Col) == "" {
		return
	}
	document := lsp.Document{Path: file, Language: language, Version: buffer.Version(), Text: buffer.String()}
	position := lsp.Position{Line: at.Line, Character: lsp.UTF16Column(line, at.Col)}

	a.stopHover()
	ctx, cancel := context.WithTimeout(context.Background(), hoverTimeout)
	a.hover.cancel = cancel
	asked := a.hover.request
	go func() {
		defer cancel()
		hover, running, err := pool.HoverIfRunning(ctx, known.Server, document, position)
		if !running {
			return
		}
		a.post(func() {
			if asked == a.hover.request {
				a.hoverAnswered(d, request, known.Server, hover, err)
			}
		})
	}()
}

// hoverAnswered shows the server's answer over the text it is about, while
// the document still has the text it was asked about.
func (a *App) hoverAnswered(d *document, request editor.HoverRequest, server lsp.Server, hover lsp.Hover, err error) {
	a.hover.cancel = nil
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return
	case err != nil:
		slog.Warn("hover", "server", server.Name, "path", d.path, "error", err)
		return
	}
	text := strings.TrimSpace(hover.Markdown)
	if text == "" || d.editor == nil || slices.Index(a.documents, d) == noDocument || d.editor.Buffer.Version() != request.Version {
		return
	}
	start, end := hoverSpan(d, request.Offset, hover.Range)
	options := a.hoverOptions()
	d.editor.ShowHover(start, end, "", func(c *ui.Context) { hoverCardView(c, text, options) })
}

// hoverSpan returns the text a hover is about: the range the server names, or
// else the word at offset.
func hoverSpan(d *document, offset int, span *lsp.Range) (int, int) {
	buffer := d.editor.Buffer
	if span != nil {
		return buffer.Offset(editorPos(d, span.Start)), buffer.Offset(editorPos(d, span.End))
	}
	at := buffer.Pos(offset)
	start, end := wordBounds(buffer.Line(at.Line), at.Col)
	lineStart := buffer.Offset(editor.Pos{Line: at.Line})
	return lineStart + start, lineStart + end
}

// hoverOptions returns how hovers render in the current look, made again
// when the look changes.
func (a *App) hoverOptions() *markdown.Options {
	if a.hover.options == nil || a.hover.theme != a.look.hover {
		a.hover.options = &markdown.Options{Theme: a.look.hover, WrapCode: true, CodeBlock: hoverCodeView}
		a.hover.theme = a.look.hover
	}
	return a.hover.options
}

// hoverCodeView shows a hover's code as signatures read: highlighted, without
// the language and copy button of a document's code blocks, as the hover's
// text is selectable already.
func hoverCodeView(c *ui.Context, block *markdown.CodeBlockView) {
	block.Body(c)
}

// hoverCardView shows a hover's Markdown, scrolling when it is long.
func hoverCardView(c *ui.Context, text string, options *markdown.Options) {
	ui.Scroll(c).MaxWidth(hoverMaxWidth).MaxHeight(hoverMaxHeight).Children(func() {
		markdown.View(c, text, options).FillWidth()
	})
}
