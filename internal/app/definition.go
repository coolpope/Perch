package app

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/egoist/mygo/ui/editor"

	"perch/internal/lsp"
	"perch/internal/workspace"
	"perch/locales"
)

const (
	definitionTimeout time.Duration = time.Minute
	// serverIdleLimit is how long a language server stays without being asked
	// anything.
	serverIdleLimit  time.Duration = 5 * time.Minute
	maxHistoryPlaces int           = 100
)

// place is a position in a file, to come back to.
type place struct {
	path     string
	external bool
	position editor.Pos
}

// definitionState is the state of going to definitions.
type definitionState struct {
	pool    *lsp.Pool
	request uint64
	pending bool
	cancel  context.CancelFunc
	back    []place
	forward []place
}

// attachDefinitions makes Cmd+click and F12 in a document go to definitions.
func (a *App) attachDefinitions(d *document) {
	d.editor.OnDefinition = func(offset int) { a.goToDefinition(d, offset) }
}

// stopDefinitions ends the running request and the project's language servers.
func (a *App) stopDefinitions() {
	if a.definition.cancel != nil {
		a.definition.cancel()
	}
	if pool := a.definition.pool; pool != nil {
		go pool.Close()
	}
	a.definition = definitionState{}
}

// definitionAtCaret goes to the definition of the symbol at the caret.
func (a *App) definitionAtCaret() {
	if d := a.activeDocument(); d != nil && !d.showsPreview() {
		a.goToDefinition(d, d.editor.Cursor())
	}
}

// goToDefinition asks the file's language server where the symbol at offset is
// defined. The goroutine ends with the answer, or when a newer request or
// stopDefinitions cancels it.
func (a *App) goToDefinition(d *document, offset int) {
	file := d.absolutePath(a.root)
	server, language, supported := lsp.ServerFor(file)
	if !supported {
		a.tell(toast{title: locales.DefinitionUnsupported(d.name())})
		return
	}
	s := &a.definition
	if s.cancel != nil {
		s.cancel()
	}
	if s.pool == nil {
		s.pool = lsp.NewPool(a.root)
	}
	buffer := d.editor.Buffer
	at := buffer.Pos(offset)
	line := buffer.Line(at.Line)
	document := lsp.Document{Path: file, Language: language, Version: buffer.Version(), Text: buffer.String()}
	position := lsp.Position{Line: at.Line, Character: lsp.UTF16Column(line, at.Col)}
	origin := place{path: d.path, external: d.external, position: at}
	word := wordAt(line, at.Col)

	ctx, cancel := context.WithTimeout(context.Background(), definitionTimeout)
	s.request++
	s.pending, s.cancel = true, cancel
	request, pool := s.request, s.pool
	go func() {
		defer cancel()
		found, err := pool.Definition(ctx, server, document, position)
		a.post(func() {
			if request == a.definition.request {
				a.definitionAnswered(origin, server, word, found, err)
			}
		})
	}()
}

// wordAt returns the identifier at a byte column of a line.
func wordAt(line string, column int) string {
	isWord := func(char rune) bool { return char == '_' || unicode.IsLetter(char) || unicode.IsDigit(char) }
	start := strings.LastIndexFunc(line[:column], func(char rune) bool { return !isWord(char) }) + 1
	end := strings.IndexFunc(line[column:], func(char rune) bool { return !isWord(char) })
	if end < 0 {
		return line[start:]
	}
	return line[start : column+end]
}

func (a *App) definitionAnswered(origin place, server lsp.Server, word string, found []lsp.Location, err error) {
	s := &a.definition
	s.pending = false
	pool := s.pool
	a.after(serverIdleLimit, func() { go pool.CloseIdle(serverIdleLimit) })
	switch {
	case errors.Is(err, context.Canceled):
	case errors.Is(err, lsp.ErrServerMissing):
		a.tell(toast{title: locales.ServerMissing(server.Name), detail: locales.ServerInstall(server.Install)})
	case err != nil:
		slog.Warn("go to definition", "server", server.Name, "error", err)
		a.tell(toast{title: locales.DefinitionFailed(), detail: err.Error()})
	case len(found) == 0:
		a.tell(toast{title: locales.NoDefinition(word)})
	default:
		a.showDefinition(origin, found[0])
	}
}

func (a *App) showDefinition(origin place, target lsp.Location) {
	relPath, inside := insideProject(a.root, target.Path)
	path, external := target.Path, true
	if inside {
		path, external = relPath, false
	}
	d := a.openPlaceDocument(path, external)
	if d == nil {
		return
	}
	a.definition.back = appendPlace(a.definition.back, origin)
	a.definition.forward = nil
	d.reveal(editorPos(d, target.Range.Start), editorPos(d, target.Range.End))
}

// editorPos converts a position of the protocol to one of the document.
func editorPos(d *document, position lsp.Position) editor.Pos {
	line := d.editor.Buffer.Line(position.Line)
	return editor.Pos{Line: position.Line, Col: lsp.ByteColumn(line, position.Character)}
}

// insideProject returns a file's path in the project. Servers may report the
// real path of a folder that the project opened through a link.
func insideProject(root string, file string) (string, bool) {
	if relPath, ok := relativeInside(root, file); ok {
		return relPath, true
	}
	realRoot, rootErr := filepath.EvalSymlinks(root)
	realFile, fileErr := filepath.EvalSymlinks(file)
	if rootErr != nil || fileErr != nil {
		return "", false
	}
	return relativeInside(realRoot, realFile)
}

func relativeInside(root string, file string) (string, bool) {
	relPath, err := filepath.Rel(root, file)
	if err != nil || !filepath.IsLocal(relPath) {
		return "", false
	}
	return filepath.ToSlash(relPath), true
}

// openPlaceDocument shows the document of a place, and returns nil when it could
// not be opened, which the person has then been told.
func (a *App) openPlaceDocument(path string, external bool) *document {
	if external {
		a.openExternalFile(path)
	} else {
		a.openFile(path)
	}
	d := a.activeDocument()
	if d == nil || d.path != path {
		return nil
	}
	a.chooseTab(a.active)
	return d
}

// openExternalFile shows a file outside the workspace in a read-only tab.
func (a *App) openExternalFile(file string) {
	a.closeChange()
	index := slices.IndexFunc(a.documents, func(d *document) bool { return d.path == file })
	if index != noDocument {
		a.active = index
		return
	}
	text, err := workspace.ReadExternalText(file)
	if err != nil {
		a.tell(openFailure(file, err))
		return
	}
	d := newExternalDocument(file, text)
	a.attachDefinitions(d)
	a.documents = append(a.documents, d)
	a.active = len(a.documents) - 1
}

// appendPlace remembers a place, forgetting the oldest beyond the limit.
func appendPlace(places []place, added place) []place {
	places = append(slices.Clone(places), added)
	return places[max(len(places)-maxHistoryPlaces, 0):]
}

// currentPlace returns where the caret is now.
func (a *App) currentPlace() (place, bool) {
	d := a.activeDocument()
	if d == nil {
		return place{}, false
	}
	return place{path: d.path, external: d.external, position: d.editor.CursorPos()}, true
}

// goBack returns to where the last definition was asked for.
func (a *App) goBack() {
	s := &a.definition
	s.back, s.forward = a.travel(s.back, s.forward)
}

// goForward undoes goBack.
func (a *App) goForward() {
	s := &a.definition
	s.forward, s.back = a.travel(s.forward, s.back)
}

// travel goes to the last place of from and remembers the current one in to.
func (a *App) travel(from []place, to []place) ([]place, []place) {
	if len(from) == 0 {
		return from, to
	}
	target := from[len(from)-1]
	current, known := a.currentPlace()
	d := a.openPlaceDocument(target.path, target.external)
	if d == nil {
		return from[:len(from)-1], to
	}
	d.reveal(target.position, target.position)
	if known {
		to = appendPlace(to, current)
	}
	return from[:len(from)-1], to
}
