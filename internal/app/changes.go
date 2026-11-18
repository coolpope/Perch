package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"github.com/egoist/mygo/ui/markdown"
	"github.com/egoist/mygo/ui/markdown/parser"

	"perch/internal/review"
	"perch/internal/workspace"
	"perch/locales"
)

const (
	changesRefreshDelay time.Duration = 700 * time.Millisecond
	changeRowHeight     float32       = 28
	changeMarkSize      float32       = 18
	changedBlockBar     float32       = 3
)

// Indexes of the switch of a Markdown file's change.
const (
	changeRendered int = 0
	changeSource   int = 1
)

// lineRange is the lines from first up to end, exclusive, counted from 0.
type lineRange struct {
	first int
	end   int
}

// changesPanel is the state of the review of the window's project.
type changesPanel struct {
	store         *review.Store
	listing       review.Listing
	loaded        bool
	notRepository bool
	failure       string

	running bool
	again   bool
	queued  bool
	ctx     context.Context
	cancel  context.CancelFunc

	selected string
	// shownVersion is the version whose texts the diff shows, which is the
	// one a mark on the selected file records.
	shownVersion string
	diff         *editor.DiffState
	unreadable   bool
	view         int
	preview      *document
	lineStarts   []int
	marks        []lineRange
	list         ui.ListState
	// reopen is where the last finished review stood, to undo finishing it.
	reopen *review.Checkpoint
	// readGeneration counts the changes to what has been read, and baselineKey
	// names the text the shown document is compared with.
	readGeneration uint64
	baselineKey    lineBaselineKey
}

// showChanges turns the sidebar to the review and brings it up to date.
func (a *App) showChanges() {
	if a.root == "" {
		return
	}
	a.sidebarTab = sidebarChanges
	a.refreshChanges()
}

// beginReview fixes where the project's review starts as soon as the project
// opens, before an agent commits anything. The goroutine ends with that work, or
// when stopChanges cancels its context.
func (a *App) beginReview() {
	r := &a.review
	r.ctx, r.cancel = context.WithCancel(context.Background())
	ctx, root := r.ctx, a.root
	go func() {
		store, err := review.Open(ctx, root)
		if errors.Is(err, review.ErrNotRepository) {
			return
		}
		if err == nil {
			err = store.Begin(ctx)
		}
		if err != nil {
			slog.Warn("begin review", "root", root, "error", err)
		}
		a.post(func() { a.reviewBegun(ctx, store) })
	}()
}

func (a *App) reviewBegun(ctx context.Context, store *review.Store) {
	if ctx.Err() != nil || a.review.store != nil {
		closeStore(store)
		return
	}
	a.review.store = store
	if a.sidebarTab == sidebarChanges {
		a.refreshChanges()
	}
}

// stopChanges ends the review's background work and lets go of its store.
func (a *App) stopChanges() {
	if a.review.cancel != nil {
		a.review.cancel()
	}
	closeStore(a.review.store)
	a.review = changesPanel{}
}

// closeStore removes the store's temporary files off the main thread.
func closeStore(store *review.Store) {
	if store == nil {
		return
	}
	go func() {
		if err := store.Close(); err != nil {
			slog.Warn("close review store", "error", err)
		}
	}()
}

// refreshChanges lists the changes again. One listing runs at a time, and a
// request meanwhile runs once it ends. The goroutine ends with the listing, or
// when stopChanges cancels its context.
func (a *App) refreshChanges() {
	r := &a.review
	if r.running {
		r.again = true
		return
	}
	if r.ctx == nil {
		r.ctx, r.cancel = context.WithCancel(context.Background())
	}
	r.running = true
	ctx, store, root := r.ctx, r.store, a.root
	go func() {
		opened, listing, err := listChanges(ctx, store, root)
		a.post(func() { a.changesListed(ctx, opened, listing, err) })
	}()
}

func listChanges(ctx context.Context, store *review.Store, root string) (*review.Store, review.Listing, error) {
	if store == nil {
		opened, err := review.Open(ctx, root)
		if err != nil {
			return nil, review.Listing{}, err
		}
		store = opened
	}
	listing, err := store.Changes(ctx)
	return store, listing, err
}

func (a *App) changesListed(ctx context.Context, store *review.Store, listing review.Listing, err error) {
	if ctx.Err() != nil {
		if store != a.review.store {
			closeStore(store)
		}
		return
	}
	r := &a.review
	switch {
	case r.store == nil:
		r.store = store
	case store != r.store:
		closeStore(store)
	}
	r.running = false
	r.loaded = true
	r.notRepository = errors.Is(err, review.ErrNotRepository)
	r.failure = ""
	if err != nil {
		slog.Warn("list changes", "root", a.root, "error", err)
		r.failure = locales.ChangesFailed()
	} else {
		r.listing = listing
		a.followSelectedChange()
	}
	if r.again {
		r.again = false
		a.refreshChanges()
	}
}

// followSelectedChange shows the selected file as it now is, or leaves the diff
// when the file no longer differs.
func (a *App) followSelectedChange() {
	r := &a.review
	if r.selected == "" {
		return
	}
	change, found := r.change(r.selected)
	if !found {
		a.closeChange()
		return
	}
	a.loadSides(change)
}

func (r *changesPanel) change(relPath string) (review.Change, bool) {
	index := slices.IndexFunc(r.listing.Changes, func(c review.Change) bool { return c.Path == relPath })
	if index < 0 {
		return review.Change{}, false
	}
	return r.listing.Changes[index], true
}

// scheduleChangesRefresh follows the disk while the review shows, waiting for a
// burst of writes to pass.
func (a *App) scheduleChangesRefresh() {
	if a.sidebarTab != sidebarChanges || a.review.queued {
		return
	}
	a.review.queued = true
	a.after(changesRefreshDelay, func() {
		a.review.queued = false
		if a.sidebarTab == sidebarChanges {
			a.refreshChanges()
		}
	})
}

// selectChange shows a file's change in the main area.
func (a *App) selectChange(relPath string) {
	change, found := a.review.change(relPath)
	if !found {
		return
	}
	a.closeChange()
	a.review.selected = relPath
	a.loadSides(change)
}

// closeChange leaves the diff and lets go of its texts.
func (a *App) closeChange() {
	r := &a.review
	r.selected, r.shownVersion = "", ""
	r.diff, r.preview = nil, nil
	r.unreadable = false
	r.lineStarts, r.marks = nil, nil
	r.view = changeRendered
}

func (a *App) showsChange() bool {
	return a.sidebarTab == sidebarChanges && a.review.selected != ""
}

// loadSides reads the texts of a change off the main thread.
func (a *App) loadSides(change review.Change) {
	r := &a.review
	ctx, store, version := r.ctx, r.store, r.listing.Version
	go func() {
		sides, err := store.Sides(ctx, version, change)
		a.post(func() {
			if ctx.Err() == nil && a.review.selected == change.Path {
				a.sidesLoaded(change, version, sides, err)
			}
		})
	}()
}

func (a *App) sidesLoaded(change review.Change, version string, sides review.Sides, err error) {
	r := &a.review
	if err != nil {
		slog.Warn("read change", "path", change.Path, "error", err)
		r.failure = locales.ChangesFailed()
		return
	}
	r.shownVersion = version
	r.unreadable = sides.Unreadable
	if sides.Unreadable {
		r.diff, r.preview = nil, nil
		return
	}
	if r.diff != nil && r.diff.Old.String() == sides.Old && r.diff.New.String() == sides.New {
		return
	}
	r.diff = a.newChangeDiff(change.Path, sides)
	r.preview = a.newChangePreview(change, sides.New)
	r.lineStarts = lineStartsOf(sides.New)
}

func (a *App) newChangeDiff(relPath string, sides review.Sides) *editor.DiffState {
	language := editor.LanguageForText(path.Base(relPath), sides.New+sides.Old)
	before, after := editor.NewBuffer(sides.Old), editor.NewBuffer(sides.New)
	before.SetLanguage(language)
	after.SetLanguage(language)
	return &editor.DiffState{Old: before, New: after, Layout: editor.DiffUnified, LineHeight: codeLineHeight, ScrollPastEnd: true}
}

// newChangePreview prepares the rendered view of a Markdown file that still
// exists, and returns nil for any other file.
func (a *App) newChangePreview(change review.Change, text string) *document {
	d := newDocument(a.root, change.Path, workspace.TextFile{Text: text})
	if !d.isMarkdown() || change.Kind == review.Deleted {
		return nil
	}
	a.attachMarkdownActions(d)
	plain := &markdown.Options{LoadImage: d.markdownOptions.LoadImage, OnLink: d.markdownOptions.OnLink, HTML: d.markdownOptions.HTML}
	d.markdownOptions.Block = func(c *ui.Context, doc *parser.Document, index int) bool {
		return a.changedBlockView(c, doc, index, plain)
	}
	return d
}

// changedBlockView marks a top-level block whose lines changed.
func (a *App) changedBlockView(c *ui.Context, doc *parser.Document, index int, plain *markdown.Options) bool {
	block := doc.Block(index)
	if block.Parent != 0 {
		return false
	}
	if !touchesRanges(a.review.marks, sourceLines(a.review.lineStarts, block.Start, block.End)) {
		return false
	}
	accent := a.look.palette.accent
	ui.Column(c).FillWidth().Padding(4, 10, 4, 12).Radius(3).Background(accent.Alpha(0.07)).
		BorderWidth(0, 0, 0, changedBlockBar).BorderColor(accent).Children(func() {
		markdown.View(c, doc.Source()[block.Start:block.End], plain).FillWidth()
	})
	return true
}

// lineStartsOf returns the offset each line of text starts at.
func lineStartsOf(text string) []int {
	starts := []int{0}
	for offset := 0; ; {
		next := strings.IndexByte(text[offset:], '\n')
		if next < 0 {
			return starts
		}
		offset += next + 1
		starts = append(starts, offset)
	}
}

// sourceLines returns the lines a span of bytes covers.
func sourceLines(lineStarts []int, start int, end int) lineRange {
	first := sort.SearchInts(lineStarts, start+1) - 1
	last := sort.SearchInts(lineStarts, max(start, end-1)+1) - 1
	return lineRange{first: first, end: last + 1}
}

// changedRanges returns the new lines of each change. A deletion marks the line
// that follows it.
func changedRanges(hunks []editor.DiffHunk) []lineRange {
	ranges := make([]lineRange, 0, len(hunks))
	for _, hunk := range hunks {
		ranges = append(ranges, lineRange{first: hunk.NewStart, end: max(hunk.NewEnd, hunk.NewStart+1)})
	}
	return ranges
}

func touchesRanges(ranges []lineRange, lines lineRange) bool {
	return slices.ContainsFunc(ranges, func(r lineRange) bool { return r.first < lines.end && lines.first < r.end })
}

// newLineFor returns the line of the new text at a line of a diff's side.
func newLineFor(hunks []editor.DiffHunk, side editor.DiffSide, line int) int {
	if side == editor.DiffNew {
		return line
	}
	shift := 0
	for _, hunk := range hunks {
		if line < hunk.OldStart {
			break
		}
		if line < hunk.OldEnd {
			return hunk.NewStart
		}
		shift += (hunk.NewEnd - hunk.NewStart) - (hunk.OldEnd - hunk.OldStart)
	}
	return line + shift
}

// openSelectedChange goes from the diff to the same line of the full file.
func (a *App) openSelectedChange() {
	r := &a.review
	change, found := r.change(r.selected)
	if !found || change.Kind == review.Deleted {
		return
	}
	line := 0
	if r.diff != nil && !a.showsRenderedChange() {
		line = newLineFor(r.diff.Hunks(), r.diff.ActiveSide(), r.diff.CursorPos().Line)
	}
	position := editor.Pos{Line: line}
	a.openAt(change.Path, position, position)
}

func (a *App) showsRenderedChange() bool {
	return a.review.preview != nil && a.review.view == changeRendered
}

// runReviewAction changes the review off the main thread and lists it again.
func (a *App) runReviewAction(action func(context.Context, *review.Store) error) {
	ctx, store := a.review.ctx, a.review.store
	if store == nil {
		return
	}
	go func() {
		err := action(ctx, store)
		a.post(func() {
			if ctx.Err() != nil {
				return
			}
			if err != nil {
				a.notice = toast{title: locales.ReviewUpdateFailed(), detail: err.Error()}
			}
			a.review.readGeneration++
			a.refreshChanges()
		})
	}()
}

// setReviewed marks a file, showing the mark at once.
func (a *App) setReviewed(relPath string, reviewed bool) {
	r := &a.review
	version := r.listing.Version
	if relPath == r.selected && r.shownVersion != "" {
		version = r.shownVersion
	}
	index := slices.IndexFunc(r.listing.Changes, func(c review.Change) bool { return c.Path == relPath })
	if index < 0 {
		return
	}
	changes := slices.Clone(r.listing.Changes)
	changes[index].Reviewed = reviewed
	r.listing.Changes = changes
	paths := []string{relPath}
	a.runReviewAction(func(ctx context.Context, store *review.Store) error {
		if reviewed {
			return store.MarkReviewed(ctx, version, paths)
		}
		return store.MarkUnreviewed(ctx, paths)
	})
}

// markSelectedReviewed marks the file on screen and moves on to the next unread
// one.
func (a *App) markSelectedReviewed() {
	r := &a.review
	if !a.showsChange() {
		return
	}
	next := nextUnread(r.listing.Changes, r.selected)
	a.setReviewed(r.selected, true)
	if next != "" {
		a.selectChange(next)
	}
}

// nextUnread returns the unread file after relPath, going round to the start,
// or nothing when no other file is unread.
func nextUnread(changes []review.Change, relPath string) string {
	start := slices.IndexFunc(changes, func(c review.Change) bool { return c.Path == relPath })
	for step := 1; step < len(changes); step++ {
		candidate := changes[(start+step+len(changes))%len(changes)]
		if !candidate.Reviewed && candidate.Path != relPath {
			return candidate.Path
		}
	}
	return ""
}

func (a *App) markAllReviewed() {
	r := &a.review
	version := r.listing.Version
	changes := slices.Clone(r.listing.Changes)
	paths := make([]string, 0, len(changes))
	for index := range changes {
		if !changes[index].Reviewed {
			paths = append(paths, changes[index].Path)
			changes[index].Reviewed = true
		}
	}
	r.listing.Changes = changes
	a.runReviewAction(func(ctx context.Context, store *review.Store) error {
		return store.MarkReviewed(ctx, version, paths)
	})
}

// finishReview starts a new review from what was just read.
func (a *App) finishReview() {
	version := a.review.listing.Version
	a.closeChange()
	a.review.listing.Changes = nil
	a.runReviewAction(func(ctx context.Context, store *review.Store) error {
		checkpoint, err := store.Finish(ctx, version)
		if err != nil {
			return err
		}
		a.post(func() {
			if ctx.Err() == nil {
				a.review.reopen = &checkpoint
			}
		})
		return nil
	})
}

// reopenReview undoes finishing the last review, keeping the offer until the
// review is back.
func (a *App) reopenReview() {
	checkpoint := a.review.reopen
	a.runReviewAction(func(ctx context.Context, store *review.Store) error {
		if err := store.Reopen(ctx, *checkpoint); err != nil {
			return err
		}
		a.post(func() {
			if ctx.Err() == nil && a.review.reopen == checkpoint {
				a.review.reopen = nil
			}
		})
		return nil
	})
}

func unreadCount(changes []review.Change) int {
	count := 0
	for index := range changes {
		if !changes[index].Reviewed {
			count++
		}
	}
	return count
}

func (a *App) changesPanelView(c *ui.Context) {
	r := &a.review
	colors := a.look.palette
	ui.Column(c).Grow(1).FillWidth().Children(func() {
		if message := changesMessage(r); message != "" {
			ui.Column(c).Grow(1).FillWidth().Center().Gap(12).Padding(0, 20).Children(func() {
				ui.Text(c, message).TextColor(colors.muted).TextAlign(ui.Center)
				a.reopenReviewView(c)
			})
			return
		}
		a.changesHeaderView(c)
		ui.List(c, &r.list, len(r.listing.Changes), func(row int) {
			a.changeRowView(c, r.listing.Changes[row])
		}).Grow(1).FillWidth().Padding(2, 6)
		a.changesFooterView(c)
	})
}

// changesMessage says why the panel lists no files, and is empty when it does.
func changesMessage(r *changesPanel) string {
	switch {
	case r.notRepository:
		return locales.ChangesNeedGit()
	case r.failure != "":
		return r.failure
	case !r.loaded:
		return locales.ReadingChanges()
	case len(r.listing.Changes) == 0:
		return locales.NoChanges()
	default:
		return ""
	}
}

func (a *App) changesHeaderView(c *ui.Context) {
	changes := a.review.listing.Changes
	read := len(changes) - unreadCount(changes)
	ui.Row(c).FillWidth().Shrink(0).Padding(2, 14, 6, 14).Children(func() {
		ui.Text(c, locales.ReviewProgress(read, len(changes))).
			FontSize(smallFontSize - 1).TextColor(a.look.palette.muted)
	})
}

func (a *App) changesFooterView(c *ui.Context) {
	ui.Column(c).FillWidth().Shrink(0).Gap(6).Padding(8, 10).BorderWidth(1, 0, 0, 0).BorderColor(a.look.palette.border).Children(func() {
		a.reopenReviewView(c)
		if unreadCount(a.review.listing.Changes) > 0 {
			if ui.Button(c, locales.MarkAllReviewed()).FillWidth().Clicked() {
				a.markAllReviewed()
			}
			return
		}
		if ui.PrimaryButton(c, locales.FinishReview()).FillWidth().Clicked() {
			a.finishReview()
		}
	})
}

// reopenReviewView offers to undo finishing the last review.
func (a *App) reopenReviewView(c *ui.Context) {
	if a.review.reopen != nil && ui.Button(c, locales.ReopenReview()).FillWidth().Clicked() {
		a.reopenReview()
	}
}

func (a *App) changeRowView(c *ui.Context, change review.Change) {
	colors := a.look.palette
	name := path.Base(change.Path)
	row := ui.Row(c).FillWidth().Height(changeRowHeight).Gap(7).Padding(0, 8).Radius(5).AlignItems(ui.Center)
	switch {
	case change.Path == a.review.selected:
		row.Background(colors.hover)
	case row.Hovered():
		row.Background(colors.hover.Alpha(0.5))
	}
	rowClicked := row.Clicked()
	markClicked := false
	row.Children(func() {
		markClicked = a.changeMarkView(c, change)
		text := ui.Text(c, name).MaxLines(1).Shrink(0)
		if change.Reviewed {
			text.TextColor(colors.muted)
		}
		if dir := path.Dir(change.Path); dir != "." {
			ui.Text(c, dir).FontSize(smallFontSize - 1).TextColor(colors.muted).MaxLines(1).Shrink(1)
		}
		ui.Spacer(c)
		a.changeCountsView(c, change)
	})
	switch {
	case markClicked:
		a.setReviewed(change.Path, !change.Reviewed)
	case rowClicked:
		a.selectChange(change.Path)
	}
}

// changeMarkView builds the button that marks a file and reports a click on it.
func (a *App) changeMarkView(c *ui.Context, change review.Change) bool {
	colors := a.look.palette
	label := locales.MarkPathReviewed(change.Path)
	icon, color := iconCircle, colors.muted
	if change.Reviewed {
		label = locales.MarkPathUnread(change.Path)
		icon, color = iconCheck, colors.accent
	}
	button := ui.Box(c).Size(changeMarkSize, changeMarkSize).Center().Shrink(0).Label(label)
	clicked := button.Clicked()
	button.Children(func() {
		ui.Icon(c, icon).FontSize(iconFontSize).TextColor(color)
	})
	return clicked
}

func (a *App) changeCountsView(c *ui.Context, change review.Change) {
	colors := a.look.palette
	small := smallFontSize - 1
	switch {
	case change.Binary:
		ui.Text(c, locales.BinaryFile()).FontSize(small).TextColor(colors.muted).Shrink(0)
	default:
		if change.Added > 0 {
			ui.Text(c, fmt.Sprintf("+%d", change.Added)).FontSize(small).TextColor(colors.str).Shrink(0)
		}
		if change.Removed > 0 {
			ui.Text(c, fmt.Sprintf("−%d", change.Removed)).FontSize(small).TextColor(colors.danger).Shrink(0)
		}
	}
}

func (a *App) changeView(c *ui.Context) {
	r := &a.review
	change, found := r.change(r.selected)
	if !found {
		return
	}
	a.changeBarView(c, change)
	c.Key(r.selected)
	switch {
	case r.unreadable:
		a.changeNoteView(c, locales.CannotShowAsText())
	case r.diff == nil:
		a.changeNoteView(c, locales.ReadingChange())
	case a.showsRenderedChange():
		a.renderedChangeView(c)
	default:
		r.diff.Theme = a.look.code
		a.applyDiffSettings(r.diff)
		editor.DiffView(c, r.diff).Grow(1).FillWidth().OnShortcut(0, ui.KeyEnter, a.openSelectedChange)
	}
}

func (a *App) changeNoteView(c *ui.Context, message string) {
	ui.Column(c).Grow(1).FillWidth().Center().Children(func() {
		ui.Text(c, message).TextColor(a.look.palette.muted)
	})
}

func (a *App) renderedChangeView(c *ui.Context) {
	r := &a.review
	r.marks = changedRanges(r.diff.Hunks())
	ui.Scroll(c).Grow(1).FillWidth().Padding(28, 32).AlignItems(ui.Center).Children(func() {
		markdown.View(c, r.preview.markdownText(), r.preview.markdownOptions).MaxWidth(markdownMaxWidth).FillWidth()
	})
}

func (a *App) changeBarView(c *ui.Context, change review.Change) {
	colors := a.look.palette
	r := &a.review
	ui.Row(c).FillWidth().Height(pathBarHeight+8).Shrink(0).Gap(5).Padding(0, 8, 0, 14).AlignItems(ui.Center).
		BorderWidth(0, 0, 1, 0).BorderColor(colors.border).Children(func() {
		a.pathCrumbsView(c, change.Path)
		ui.Text(c, changeKindLabel(change.Kind)).FontSize(smallFontSize-1).TextColor(colors.muted).Padding(0, 0, 0, 6)
		ui.Spacer(c)
		if r.preview != nil {
			ui.Segmented(c, &r.view, locales.ChangeRendered(), locales.ChangeDiff())
		}
		if change.Kind != review.Deleted && ui.Button(c, locales.OpenFile()).Clicked() {
			a.openSelectedChange()
		}
		if change.Reviewed {
			if ui.Button(c, locales.MarkUnread()).Clicked() {
				a.setReviewed(change.Path, false)
			}
			return
		}
		if ui.PrimaryButton(c, locales.MarkReviewed()).Clicked() {
			a.markSelectedReviewed()
		}
	})
}

func changeKindLabel(kind review.Kind) string {
	switch kind {
	case review.Added:
		return locales.KindAdded()
	case review.Deleted:
		return locales.KindDeleted()
	default:
		return locales.KindModified()
	}
}
