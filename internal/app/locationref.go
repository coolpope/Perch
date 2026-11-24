package app

import (
	"fmt"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"

	"github.com/marukolibro/Perch/internal/review"
	"github.com/marukolibro/Perch/locales"
)

// formatLineReference formats a file path with its 1-based line or line range.
func formatLineReference(filePath string, startLine int, endLine int) string {
	if endLine <= startLine {
		return fmt.Sprintf("%s:%d", filePath, startLine)
	}
	return fmt.Sprintf("%s:%d-%d", filePath, startLine, endLine)
}

// editorLineRange returns the 1-based line range covered by the editor's
// selection, or its caret line when nothing is selected. A selection that ends
// at column 0 of a later line stops at the line before it.
func editorLineRange(state *editor.State) (int, int) {
	sel := state.Selection()
	if sel.Empty() {
		line := state.CursorPos().Line + 1
		return line, line
	}
	start := state.Buffer.Pos(sel.Start())
	end := state.Buffer.Pos(sel.End())
	endLine := end.Line
	if end.Col == 0 && endLine > start.Line {
		endLine--
	}
	return start.Line + 1, endLine + 1
}

// diffLineReference builds a line reference for a diff view, mapping old-side
// lines onto the new file when the file still exists.
func diffLineReference(filePath string, deleted bool, diff *editor.DiffState) string {
	if diff == nil {
		return ""
	}
	if lines, selected := diff.SelectedLines(); selected {
		start, end := diffLineNumber(diff, deleted, lines.Side, lines.Start), diffLineNumber(diff, deleted, lines.Side, lines.End)
		return formatLineReference(filePath, min(start, end)+1, max(start, end)+1)
	}
	line := diffLineNumber(diff, deleted, diff.ActiveSide(), diff.CursorPos().Line)
	return formatLineReference(filePath, line+1, line+1)
}

func diffLineNumber(diff *editor.DiffState, deleted bool, side editor.DiffSide, line int) int {
	if deleted || side == editor.DiffNew {
		return max(0, line)
	}
	return max(0, newLineFor(diff.Hunks(), side, line))
}

// activeLineReference returns the path:line or path:start-end reference for
// what is currently shown in the main panel, or empty when none applies.
func (a *App) activeLineReference() string {
	if a.showsChange() {
		change, found := a.review.change(a.review.selected)
		if !found || a.review.diff == nil || a.showsRenderedChange() {
			return ""
		}
		return diffLineReference(change.Path, change.Kind == review.Deleted, a.review.diff)
	}
	if a.showsCommit() {
		h := &a.history
		if h.detail == nil || h.diff == nil || h.file == "" {
			return ""
		}
		deleted := false
		for _, change := range h.detail.Changes {
			if change.Path == h.file {
				deleted = change.Kind == review.Deleted
				break
			}
		}
		return diffLineReference(h.file, deleted, h.diff)
	}
	d := a.activeTextDocument()
	if d == nil || d.showsPreview() || d.showsSVGPreview() {
		return ""
	}
	start, end := editorLineRange(d.editor)
	return formatLineReference(d.path, start, end)
}

// copyLineReference copies the active file's path and line range to the
// clipboard and shows a brief confirmation.
func (a *App) copyLineReference() {
	ref := a.activeLineReference()
	if ref == "" {
		return
	}
	a.copyText(ref)
	a.tell(toast{tone: toneInfo, title: locales.CopiedToClipboard(), detail: ref})
}

// attachEditorMenu adds Perch's actions after the editor's built-in menu.
func (a *App) attachEditorMenu(d *document) {
	if d.editor == nil {
		return
	}
	d.editor.ContextMenu = func(m *ui.Menu) {
		icons := menuIcons()
		m.Separator()
		if m.Item(locales.MenuCopyLineRef()).Icon(icons.copyLink).Chosen() {
			a.copyLineReference()
		}
		if !d.external && m.Item(locales.MenuRevealInTree()).Icon(icons.locate).Chosen() {
			a.revealInTree(d.path)
		}
	}
}

// diffMenu builds the context menu of a diff view.
func (a *App) diffMenu(m *ui.Menu) {
	if a.activeLineReference() == "" {
		return
	}
	icons := menuIcons()
	if m.Item(locales.MenuCopyLineRef()).Icon(icons.copyLink).Chosen() {
		a.copyLineReference()
	}
}
