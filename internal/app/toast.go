package app

import (
	"errors"
	"fmt"
	"io/fs"
	"path"
	"time"

	"github.com/egoist/mygo/ui"

	"perch/internal/workspace"
	"perch/locales"
)

const (
	toastDuration  time.Duration = 4 * time.Second
	toastWidth     float32       = 340
	toastEdgeSpace float32       = 14
	bytesPerMB     int64         = 1 << 20
)

// toast is a short message that floats over the window.
type toast struct {
	title  string
	detail string
}

// tell shows a message that goes away by itself, replacing the last one.
func (a *App) tell(message toast) {
	a.brief = message
	a.briefID++
	a.scheduleBriefEnd(a.briefID)
}

func (a *App) scheduleBriefEnd(id uint64) {
	a.after(toastDuration, func() { a.endBrief(id) })
}

// endBrief removes a message whose time is up, waiting while the pointer rests
// on it so that it can be read.
func (a *App) endBrief(id uint64) {
	if id != a.briefID {
		return
	}
	if a.briefHovered {
		a.scheduleBriefEnd(id)
		return
	}
	a.brief = toast{}
}

// openFailure says in plain words why a file did not open.
func openFailure(relPath string, err error) toast {
	name := path.Base(relPath)
	var tooLarge *workspace.FileTooLargeError
	switch {
	case errors.As(err, &tooLarge):
		return toast{
			title:  locales.FileTooLarge(name),
			detail: locales.FileTooLargeDetail(sizeInMB(tooLarge.Size), sizeInMB(tooLarge.Limit)),
		}
	case errors.Is(err, workspace.ErrBinaryFile):
		return toast{title: locales.NotTextFile(name), detail: locales.NotTextFileDetail()}
	case errors.Is(err, fs.ErrNotExist):
		return toast{title: locales.FileGone(name), detail: locales.FileGoneDetail()}
	case errors.Is(err, fs.ErrPermission):
		return toast{title: locales.CannotRead(name), detail: locales.CheckPermissions()}
	default:
		return toast{title: locales.CouldNotOpen(name), detail: err.Error()}
	}
}

// saveFailure says in plain words why a file was not saved.
func saveFailure(name string, err error) toast {
	if errors.Is(err, workspace.ErrChangedOnDisk) || errors.Is(err, errUnresolvedConflict) {
		return toast{title: locales.ChangedOnDisk(name), detail: locales.ChooseVersion()}
	}
	return toast{title: locales.SaveFailed(name), detail: err.Error()}
}

func sizeInMB(size int64) string {
	return fmt.Sprintf("%d MB", (size+bytesPerMB/2)/bytesPerMB)
}

// toastsView floats the current messages over the bottom corner of the window,
// where they cover no controls and move nothing.
func (a *App) toastsView(c *ui.Context) {
	if a.brief == (toast{}) && a.notice == (toast{}) {
		a.briefHovered = false
		return
	}
	ui.Overlay(c, func() {
		ui.Column(c).Absolute().Right(toastEdgeSpace).Bottom(statusBarHeight + toastEdgeSpace).Width(toastWidth).Gap(8).Children(func() {
			if a.notice != (toast{}) {
				c.Key("notice")
				if a.toastView(c, a.notice) {
					a.notice = toast{}
				}
			}
			if a.brief != (toast{}) {
				c.Key("brief")
				if a.toastView(c, a.brief) {
					a.brief = toast{}
				}
			}
		})
	})
}

// toastView builds a message and reports a click on its close button.
func (a *App) toastView(c *ui.Context, message toast) bool {
	colors := a.look.palette
	closed := false
	card := ui.Row(c).FillWidth().Gap(10).Padding(11, 10, 11, 12).Radius(10).Background(colors.background).
		Border(1, colors.border).Shadow(0, 8, 24, 0, ui.RGBA(0, 0, 0, 0.2))
	if message == a.brief {
		a.briefHovered = card.Hovered()
	}
	card.Children(func() {
		ui.Icon(c, iconAlert).FontSize(16).TextColor(colors.danger).Shrink(0)
		ui.Column(c).Grow(1).Shrink(1).Gap(2).Children(func() {
			ui.Text(c, message.title).FontWeight(600)
			if message.detail != "" {
				ui.Text(c, message.detail).FontSize(smallFontSize).TextColor(colors.muted)
			}
		})
		button := ui.Box(c).Size(20, 20).Center().Radius(5).Shrink(0).Label(locales.Dismiss())
		if button.Hovered() {
			button.Background(colors.hover)
		}
		closed = button.Clicked()
		button.Children(func() {
			ui.Icon(c, iconClose).FontSize(12).TextColor(colors.muted)
		})
	})
	return closed
}
