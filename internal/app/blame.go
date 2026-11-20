package app

import (
	"log/slog"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"

	"perch/internal/review"
	"perch/locales"
)

const (
	blameCardWidth    float32 = 340
	fileHistoryWidth  float32 = 380
	fileHistoryHeight float32 = 360
	// fileHistoryTitleHeight is the room of the popover's title.
	fileHistoryTitleHeight float32 = 30
	fileHistoryEmptyHeight float32 = 120
	pathBarButtonSize      float32 = 26
	pathBarIconFontSize    float32 = 15
)

// blameState is who last changed each line of a document, read while the
// person asks for it and let go when they stop.
type blameState struct {
	blame review.Blame
	// version is the version of the text the blame was read for.
	version uint64
	loading bool
	// pending is the hover waiting for the blame being read.
	pending *editor.HoverRequest
}

// fileHistory is the list of commits that changed one file, read a page at a
// time while its popover is open.
type fileHistory struct {
	open       bool
	path       string
	commits    []review.Commit
	loading    bool
	complete   bool
	failure    string
	generation uint64
	list       ui.ListState
}

// inGit reports whether the open folder is in a Git work tree.
func (a *App) inGit() bool {
	return a.status.kinds != nil
}

// toggleBlame turns on or off the commit that shows when the pointer rests on a
// line of a document.
func (a *App) toggleBlame(d *document) {
	if d.blame != nil {
		d.blame = nil
		return
	}
	d.blame = &blameState{}
	a.readBlame(d)
}

// readBlame reads the blame of the document's text as it is now, off the main
// thread. The goroutine is owned by the open folder: closing it cancels the
// context.
func (a *App) readBlame(d *document) {
	state := d.blame
	if state == nil || state.loading || a.status.ctx == nil {
		return
	}
	state.loading = true
	ctx, root, relPath := a.status.ctx, a.root, d.path
	text, version := d.editor.Buffer.String(), d.editor.Buffer.Version()
	go func() {
		blame, err := review.BlameText(ctx, root, relPath, text)
		a.post(func() {
			if ctx.Err() == nil && d.blame == state {
				a.blameRead(d, blame, version, err)
			}
		})
	}()
}

func (a *App) blameRead(d *document, blame review.Blame, version uint64, err error) {
	state := d.blame
	state.loading = false
	if err != nil {
		slog.Warn("read blame", "path", d.path, "error", err)
		a.tell(toast{title: locales.BlameFailed(), detail: err.Error()})
		a.toggleBlame(d)
		return
	}
	state.blame, state.version = blame, version
	if pending := state.pending; pending != nil {
		state.pending = nil
		a.blameHover(d, *pending)
	}
}

// blameHover answers the editor's hover with the commit that last changed the
// line under the pointer, reading the blame again once the text has changed.
func (a *App) blameHover(d *document, request editor.HoverRequest) {
	state := d.blame
	if state == nil {
		return
	}
	if state.version != request.Version {
		state.pending = &request
		a.readBlame(d)
		return
	}
	buffer := d.editor.Buffer
	line := buffer.Pos(request.Offset).Line
	if line >= len(state.blame.Lines) {
		return
	}
	commit := state.blame.Commits[state.blame.Lines[line]]
	start := buffer.Offset(editor.Pos{Line: line})
	end := start + len(buffer.Line(line))
	d.editor.ShowHover(start, end, "", func(c *ui.Context) { a.blameCardView(c, commit, d.path) })
}

// blameCardView shows the commit that last changed a line.
func (a *App) blameCardView(c *ui.Context, commit review.BlameCommit, relPath string) {
	colors := a.look.palette
	ui.Column(c).Width(blameCardWidth).Gap(5).Padding(2, 0).Children(func() {
		if commit.Uncommitted {
			ui.Text(c, locales.NotCommittedYet()).FontWeight(600)
			return
		}
		ui.Text(c, commit.Summary).FontWeight(600).MaxLines(2)
		ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
			ui.Text(c, commit.Short).Font("monospace").FontSize(smallFontSize - 1).TextColor(colors.muted).Shrink(0)
			ui.Text(c, commit.Author+" · "+relativeTime(commit.When, time.Now())).FontSize(smallFontSize - 1).
				TextColor(colors.muted).MaxLines(1).Shrink(1)
			ui.Spacer(c)
			if ui.Button(c, locales.ViewCommit()).Clicked() {
				a.showCommitOf(review.Commit{
					Hash: commit.Hash, Short: commit.Short, Author: commit.Author, When: commit.When, Subject: commit.Summary,
				}, relPath)
			}
		})
	})
}

// showCommitOf turns to the Git page and shows a commit at one of its files.
func (a *App) showCommitOf(commit review.Commit, relPath string) {
	a.fileHistory.open = false
	a.showChanges()
	a.selectCommit(commit, relPath)
}

// openFileHistory lists the commits that changed a file.
func (a *App) openFileHistory(relPath string) {
	h := &a.fileHistory
	h.generation++
	*h = fileHistory{open: true, path: relPath, generation: h.generation}
	a.loadMoreFileHistory()
}

// loadMoreFileHistory reads the next page of a file's history off the main
// thread. The goroutine is owned by the open folder.
func (a *App) loadMoreFileHistory() {
	h := &a.fileHistory
	if h.loading || h.complete || a.status.ctx == nil {
		return
	}
	h.loading = true
	ctx, root, relPath, skip, generation := a.status.ctx, a.root, h.path, len(h.commits), h.generation
	go func() {
		commits, err := review.FileLog(ctx, root, relPath, skip, historyPage)
		a.post(func() {
			if ctx.Err() == nil && a.fileHistory.generation == generation {
				a.fileHistoryLoaded(commits, err)
			}
		})
	}()
}

func (a *App) fileHistoryLoaded(commits []review.Commit, err error) {
	h := &a.fileHistory
	h.loading = false
	if err != nil {
		slog.Warn("read file history", "path", h.path, "error", err)
		h.failure = locales.HistoryFailed()
		return
	}
	h.commits = append(h.commits, commits...)
	h.complete = len(commits) < historyPage
}

// gitButtonsView puts the document's blame switch and history on its path bar.
func (a *App) gitButtonsView(c *ui.Context, d *document) {
	if !a.inGit() || d.external || d.editor == nil || d.isRasterImage() {
		return
	}
	if a.pathBarButton(c, iconBlame, locales.BlameLines(), d.blame != nil).Clicked() {
		a.toggleBlame(d)
	}
	h := &a.fileHistory
	button := a.pathBarButton(c, iconHistory, locales.FileHistory(), h.open && h.path == d.path)
	if button.Clicked() {
		a.openFileHistory(d.path)
	}
	if h.path != d.path {
		return
	}
	ui.Popover(c, button, &h.open, func() {
		a.fileHistoryView(c)
	})
}

// pathBarButton builds a compact icon button shared by the path bar and file tree.
func (a *App) pathBarButton(c *ui.Context, icon *ui.SVG, label string, active bool) ui.Element {
	colors := a.look.palette
	button := ui.ButtonBase(c).Size(pathBarButtonSize, pathBarButtonSize).Radius(radiusSmall).
		Role(ui.RoleButton).Label(label).Tooltip(label)
	color := colors.muted
	switch {
	case active:
		button.Background(colors.accent.Alpha(0.14))
		color = colors.accent
	case button.Hovered():
		button.Background(colors.hover)
		color = colors.text
	}
	if button.FocusVisible() {
		button.Border(1, colors.accent)
	}
	button.Children(func() {
		ui.Icon(c, icon).FontSize(pathBarIconFontSize).TextColor(color)
	})
	return button
}

func (a *App) fileHistoryView(c *ui.Context) {
	h := &a.fileHistory
	colors := a.look.palette
	ui.Column(c).Width(fileHistoryWidth).Height(fileHistoryHeightFor(len(h.commits))).Children(func() {
		ui.Text(c, locales.FileHistory()).FontSize(smallFontSize-1).FontWeight(600).TextColor(colors.muted).Padding(4, 8, 6, 8)
		switch {
		case h.failure != "":
			a.panelMessageView(c, h.failure)
			return
		case len(h.commits) == 0 && h.loading:
			a.panelMessageView(c, locales.ReadingHistory())
			return
		case len(h.commits) == 0:
			a.panelMessageView(c, locales.NoFileHistory())
			return
		}
		now := time.Now()
		ui.List(c, &h.list, len(h.commits), func(row int) {
			commit := h.commits[row]
			item := ui.Column(c).FillWidth().Height(commitRowHeight).Gap(3).Padding(0, 8).Radius(radiusControl).
				Center().AlignItems(ui.Start).Label(commit.Subject)
			if item.Hovered() {
				item.Background(colors.hover)
			}
			if item.Clicked() {
				a.showCommitOf(commit, h.path)
			}
			item.Children(func() {
				ui.Text(c, commit.Subject).MaxLines(1)
				ui.Text(c, commit.Short+" · "+commit.Author+" · "+relativeTime(commit.When, now)).
					FontSize(smallFontSize - 1).TextColor(colors.muted).MaxLines(1)
			})
		}).Grow(1).FillWidth()
		if h.list.AtEnd() {
			a.loadMoreFileHistory()
		}
	})
}

// fileHistoryHeightFor fits the popover to its commits, up to
// fileHistoryHeight, keeping room for a message when there are none.
func fileHistoryHeightFor(commits int) float32 {
	if commits == 0 {
		return fileHistoryEmptyHeight
	}
	return min(fileHistoryHeight, fileHistoryTitleHeight+float32(commits)*commitRowHeight)
}

// stopFileHistory forgets the file history, whose reading ends with the
// folder's context.
func (a *App) stopFileHistory() {
	a.fileHistory = fileHistory{}
}
