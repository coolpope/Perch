package app

import (
	"bytes"
	"fmt"
	"path"
	"path/filepath"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"github.com/egoist/mygo/ui/markdown"

	"perch/internal/htmlblock"
	"perch/internal/workspace"
)

// conflict says how the file on disk disagrees with an open document.
type conflict uint8

const (
	conflictNone conflict = iota
	// conflictChanged is a file rewritten while the document has unsaved
	// edits.
	conflictChanged
	// conflictRemoved is a file that no longer exists.
	conflictRemoved
)

// codeLineHeight is the height of code lines as a multiple of the font size.
const codeLineHeight float32 = 1.6

// Indexes of the Markdown view switch.
const (
	markdownPreview int = 0
	markdownSource  int = 1
)

// document is an open file.
type document struct {
	// path is relative to the workspace root.
	path   string
	editor *editor.State
	// focus names the editor's view, to move the keyboard to it.
	focus ui.Handle
	// digest identifies the disk content the buffer was last in step with.
	digest   workspace.Digest
	conflict conflict
	// disk is the version on disk during a conflictChanged.
	disk workspace.TextFile

	markdownView    int
	markdownOptions *markdown.Options
	// previewText holds the rendered source only once a preview has shown,
	// so that code files keep one copy of their text in memory.
	previewText    string
	previewVersion uint64
	previewLoaded  bool
	// htmlParsed keeps the HTML blocks of the preview, which build every
	// frame, until the text changes.
	htmlParsed map[string][]htmlblock.Block
	images     map[string]ui.ImageSource
}

func newDocument(root string, relPath string, file workspace.TextFile) *document {
	buffer := editor.NewBuffer(file.Text)
	buffer.SetLanguage(editor.LanguageForText(path.Base(relPath), file.Text))
	dir := filepath.Join(root, filepath.FromSlash(path.Dir(relPath)))
	return &document{
		path:            relPath,
		editor:          &editor.State{Buffer: buffer, Label: relPath, LineHeight: codeLineHeight},
		digest:          file.Digest,
		markdownView:    markdownPreview,
		markdownOptions: &markdown.Options{LoadImage: markdown.FileImages(dir)},
		htmlParsed:      map[string][]htmlblock.Block{},
		images:          map[string]ui.ImageSource{},
	}
}

func (d *document) name() string {
	return path.Base(d.path)
}

func (d *document) modified() bool {
	return d.editor.Buffer.Modified()
}

func (d *document) isMarkdown() bool {
	language := d.editor.Buffer.Language()
	return language != nil && language.Name == "Markdown"
}

// showsPreview reports whether the document shows rendered Markdown.
func (d *document) showsPreview() bool {
	return d.isMarkdown() && d.markdownView == markdownPreview
}

// markdownText returns the text to render, reading the buffer only after
// it changes rather than on every frame.
func (d *document) markdownText() string {
	version := d.editor.Buffer.Version()
	if !d.previewLoaded || version != d.previewVersion {
		d.previewText = d.editor.Buffer.String()
		d.previewVersion = version
		d.previewLoaded = true
		d.htmlParsed = map[string][]htmlblock.Block{}
	}
	return d.previewText
}

// htmlBlocks returns the model of an HTML block of the preview.
func (d *document) htmlBlocks(source string) ([]htmlblock.Block, error) {
	if blocks, parsed := d.htmlParsed[source]; parsed {
		return blocks, nil
	}
	blocks, err := htmlblock.Parse(source)
	if err != nil {
		return nil, err
	}
	d.htmlParsed[source] = blocks
	return blocks, nil
}

// reveal shows the source with the range from start to end selected, and
// moves the keyboard to it.
func (d *document) reveal(start editor.Pos, end editor.Pos) {
	d.markdownView = markdownSource
	d.editor.GoTo(start)
	if end != start {
		buffer := d.editor.Buffer
		d.editor.Select(buffer.Offset(start), buffer.Offset(end))
	}
	d.focus.Focus()
}

// content returns the bytes a save writes, with each line's original ending.
func (d *document) content() ([]byte, error) {
	var out bytes.Buffer
	if _, err := d.editor.Buffer.WriteTo(&out); err != nil {
		return nil, fmt.Errorf("read buffer of %q: %w", d.path, err)
	}
	return out.Bytes(), nil
}

// markSaved records that the buffer now matches the disk content of digest.
func (d *document) markSaved(digest workspace.Digest) {
	d.digest = digest
	d.conflict = conflictNone
	d.disk = workspace.TextFile{}
	d.editor.Buffer.MarkSaved()
}

// reload replaces the buffer with the version on disk, keeping the scroll
// position.
func (d *document) reload(file workspace.TextFile) {
	scrollY := d.editor.ScrollY()
	d.editor.Buffer.Reset(file.Text)
	d.editor.SetScrollY(scrollY)
	d.digest = file.Digest
	d.conflict = conflictNone
	d.disk = workspace.TextFile{}
}
