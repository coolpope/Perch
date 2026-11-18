package app

import (
	"log/slog"

	"github.com/egoist/mygo/ui/editor"

	"perch/internal/review"
)

// lineBaselineKey names what the bars beside the line numbers compare: a
// document with what had been read of its file at one point of the review.
type lineBaselineKey struct {
	document   *document
	store      *review.Store
	generation uint64
}

// followLineBaseline marks the lines of the shown document left to read. Only
// that document holds the text it is compared with, so that open tabs cost no
// second copy of their files. The goroutine it starts ends with the read, or
// when stopChanges cancels its context.
func (a *App) followLineBaseline(d *document) {
	r := &a.review
	wanted := lineBaselineKey{document: d, store: r.store, generation: r.readGeneration}
	if wanted == r.baselineKey {
		return
	}
	if previous := r.baselineKey.document; previous != nil && previous != d && previous.editor != nil {
		previous.editor.DiffBase = nil
	}
	r.baselineKey = wanted
	if r.store == nil || d.editor == nil || d.external {
		return
	}
	ctx, store, relPath := r.ctx, r.store, d.path
	go func() {
		baseline, err := store.Baseline(ctx, relPath)
		a.post(func() { a.lineBaselineRead(wanted, baseline, err) })
	}()
}

// lineBaselineRead shows the bars of a document that is still the one wanted.
func (a *App) lineBaselineRead(key lineBaselineKey, baseline review.Baseline, err error) {
	if a.review.baselineKey != key {
		return
	}
	if err != nil {
		slog.Warn("read line baseline", "path", key.document.path, "error", err)
		return
	}
	if !baseline.Known {
		key.document.editor.DiffBase = nil
		return
	}
	key.document.editor.DiffBase = editor.NewBuffer(baseline.Text)
}
