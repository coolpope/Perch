package app

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"time"

	"github.com/egoist/mygo/ui"

	"perch/internal/lsp"
)

// breadcrumbDelay waits for typing to pause before asking for symbols again.
const breadcrumbDelay time.Duration = 300 * time.Millisecond

// breadcrumbState keeps the active document's last symbols until they refresh.
type breadcrumbState struct {
	document *document
	path     string
	version  uint64
	disabled []string
	due      time.Time
	waiting  bool
	starts   uint64
	request  uint64
	cancel   context.CancelFunc
	symbols  []outlineSymbol
	caret    int
	chain    []int
}

// clearBreadcrumb drops the cache and invalidates replies.
func (a *App) clearBreadcrumb() {
	if a.breadcrumb.cancel != nil {
		a.breadcrumb.cancel()
	}
	a.breadcrumb = breadcrumbState{request: a.breadcrumb.request + 1, caret: -1}
}

// breadcrumbMatches checks the document, path, and enabled languages.
func (a *App) breadcrumbMatches(d *document) bool {
	b := &a.breadcrumb
	return b.document == d && b.path == d.path &&
		slices.Equal(b.disabled, a.settings.DisabledLanguages)
}

// syncBreadcrumb waits for edits to settle, and asks again once a server has
// started.
func (a *App) syncBreadcrumb(c *ui.Context, d *document) {
	b := &a.breadcrumb
	if d == nil || d.editor == nil {
		if b.document != nil {
			a.clearBreadcrumb()
		}
		return
	}
	now := c.Now()
	version := d.editor.Buffer.Version()
	same := a.breadcrumbMatches(d)
	if !same {
		a.clearBreadcrumb()
		b.document, b.path, b.disabled = d, d.path, a.settings.DisabledLanguages
	}
	if !same || b.version != version {
		if b.cancel != nil {
			b.cancel()
			b.cancel = nil
		}
		// Edits can move the caret past the old ranges. Keep the visible path
		// until the next caret move without an edit or until the symbols refresh.
		if b.caret >= 0 {
			b.caret = d.editor.Cursor()
		}
		b.version, b.waiting = version, false
		b.due = now.Add(breadcrumbDelay)
	}
	if d.showsSVGPreview() || (b.due.IsZero() && !b.waiting) {
		return
	}
	if delay := b.due.Sub(now); !b.due.IsZero() && delay > 0 {
		c.After(delay)
		return
	}
	pool := a.definition.pool
	var starts uint64
	if pool != nil {
		starts = pool.StartCount()
	}
	// A document waiting for its server asks again only once a server has
	// started, as go to definition does, checked in frames that draw anyway:
	// a timer would keep the window drawing, which holds the renderer's memory.
	if !b.due.IsZero() || starts != b.starts {
		b.due = time.Time{}
		b.starts, b.waiting = starts, false
		a.refreshBreadcrumb(d, pool)
	}
}

// refreshBreadcrumb parses local headings or asks an already running server.
func (a *App) refreshBreadcrumb(d *document, pool *lsp.Pool) {
	b := &a.breadcrumb
	if d.isMarkdown() {
		b.symbols = markdownOutlineSymbols(d)
		b.caret = -1
		return
	}
	registry, err := a.readLanguageRegistry()
	if err != nil {
		slog.Debug("read breadcrumb language configuration", "file", a.languageFile, "error", err)
	}
	file := d.absolutePath(a.root)
	known, language, supported := registry.Match(file)
	if !supported || !registry.Enabled(known.ID) {
		return
	}
	if pool == nil || b.starts == 0 {
		b.waiting = true
		return
	}
	request := b.request
	buffer := d.editor.Buffer.Snapshot()
	ctx, cancel := context.WithTimeout(context.Background(), documentSymbolsTimeout)
	b.cancel = cancel
	go func() {
		defer cancel()
		symbols, running, err := pool.DocumentSymbolsIfRunning(ctx, known.Server, symbolDocument(file, language, buffer))
		a.post(func() {
			if running && a.definition.pool == pool {
				a.closeIdleServersLater()
			}
			if request != b.request || a.activeDocument() != d || !a.breadcrumbMatches(d) ||
				buffer.Version() != d.editor.Buffer.Version() {
				return
			}
			b.cancel = nil
			b.waiting = !running
			if err != nil {
				if !errors.Is(err, context.Canceled) {
					slog.Debug("read breadcrumb symbols", "server", known.Server.Name, "path", d.path, "error", err)
				}
				return
			}
			if !running {
				return
			}
			b.symbols = outlineSymbolsOf(symbols)
			slices.SortStableFunc(b.symbols, func(a, b outlineSymbol) int {
				if start := compareSymbolPositions(a.extent.Start, b.extent.Start); start != 0 {
					return start
				}
				return compareSymbolPositions(b.extent.End, a.extent.End)
			})
			b.caret = -1
		})
	}()
}

// symbolChain finds the ranges containing the caret, from outermost to innermost.
// It compares a valid caret to the ranges without indexing the buffer with them.
func (b *breadcrumbState) symbolChain() []int {
	if len(b.symbols) == 0 {
		return nil
	}
	ed := b.document.editor
	caret := ed.Cursor()
	if caret == b.caret {
		return b.chain
	}
	b.caret = caret
	pos := ed.CursorPos()
	at := lsp.Position{Line: pos.Line, Character: lsp.UTF16Column(ed.Buffer.Line(pos.Line), pos.Col)}
	b.chain = b.chain[:0]
	for i := range b.symbols {
		extent := &b.symbols[i].extent
		if compareSymbolPositions(extent.Start, at) > 0 {
			break
		}
		if compareSymbolPositions(at, extent.End) < 0 {
			b.chain = append(b.chain, i)
		}
	}
	return b.chain
}

// symbolCrumbsView uses only the space left by the file path. Navigation clamps
// each selection start to the current buffer, even while the symbols are stale.
func (a *App) symbolCrumbsView(c *ui.Context, d *document) {
	if d.showsSVGPreview() {
		return
	}
	chain := a.breadcrumb.symbolChain()
	if len(chain) == 0 {
		return
	}
	colors := a.look.palette
	ui.Row(c).Grow(1).Basis(0).MinWidth(0).ClipX().AlignItems(ui.Center).Children(func() {
		for _, index := range chain {
			symbol := &a.breadcrumb.symbols[index]
			button := ui.ButtonBase(c.Key(index)).Shrink(1).MinWidth(0).ClipX().Justify(ui.Start).Gap(4).
				Radius(radiusSmall).Role(ui.RoleButton).Label(symbol.name).Tooltip(symbol.name)
			if button.Hovered() || button.Focused() {
				button.Background(colors.hover)
			}
			button.Children(func() {
				ui.Text(c, "›").MarginStart(5).Shrink(0).FontSize(smallFontSize).TextColor(colors.muted)
				ui.Box(c).Size(5, 5).Shrink(0).Radius(3).Background(outlineKindColor(symbol.kind, colors))
				ui.Text(c, symbol.name).FontSize(smallFontSize).TextColor(colors.muted).MinWidth(0).MaxLines(1)
			})
			if button.Clicked() {
				position := editorPos(d, symbol.span.Start)
				d.reveal(position, position)
			}
		}
	})
}
