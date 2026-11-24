package app

import (
	"os"
	"slices"

	"github.com/egoist/mygo/ui"

	"github.com/marukolibro/Perch/locales"
)

// keycapView shows a shortcut as a key cap.
func (a *App) keycapView(c *ui.Context, label string) {
	if label == "" {
		return
	}
	colors := a.look.palette
	ui.Box(c).Shrink(0).Padding(1, 5, 2, 5).Radius(5).Background(colors.background).
		Border(1, colors.border).BorderWidth(1, 1, 2, 1).Children(func() {
		ui.Text(c, label).Font("monospace").FontSize(smallFontSize - 2).TextColor(colors.muted)
	})
}

// welcomeView invites to open a folder: with the button, a recent project or
// by dropping one on the window.
func (a *App) welcomeView(c *ui.Context) {
	colors := a.look.palette
	page := ui.Column(c).Fill()
	dragging := page.FileDragOver()
	if dropped := page.DroppedFiles(); len(dropped) > 0 {
		a.openDroppedFolder(dropped[0])
	}
	page.Children(func() {
		ui.Box(c).FillWidth().Height(headerHeight).Shrink(0).DragWindow()
		ui.Column(c).Grow(1).FillWidth().Center().Gap(10).Padding(0, 0, headerHeight, 0).Children(func() {
			ui.Icon(c, iconPerch).FontSize(44).TextColor(colors.accent)
			ui.Text(c, "Perch").FontSize(26).FontWeight(600).LetterSpacing(-0.3)
			ui.Text(c, locales.AppTagline()).TextColor(colors.muted)
			ui.Box(c).Height(8)
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				if ui.PrimaryButton(c, locales.OpenFolder()).Clicked() {
					a.chooseFolder()
				}
				a.keycapView(c, openFolderKeys.binding(a.settings).label())
			})
			a.recentProjectsView(c)
			a.dropHintView(c, dragging)
		})
	})
}

// dropHintView says a folder can be dropped on the window, and lights up while
// files are dragged over it.
func (a *App) dropHintView(c *ui.Context, dragging bool) {
	colors := a.look.palette
	hint := ui.Box(c).Width(recentRowWidth).Padding(9).Margin(6, 0, 0, 0).Center().Radius(radiusControl).Border(1, colors.border)
	color := colors.muted
	if dragging {
		hint.Border(1, colors.accent).Background(colors.accent.Alpha(0.08))
		color = colors.accent
	}
	hint.Children(func() {
		ui.Text(c, locales.DropFolderHint()).FontSize(smallFontSize - 1).TextColor(color)
	})
}

// openDroppedFolder opens a folder dropped on the welcome screen, and says so
// when what was dropped is not a folder.
func (a *App) openDroppedFolder(path string) {
	info, err := os.Stat(path)
	switch {
	case err != nil:
		a.notice = toast{title: locales.FolderOpenFailed(), detail: err.Error()}
	case !info.IsDir():
		a.notice = toast{title: locales.DropFolderNotFile()}
	case a.desktop != nil:
		a.desktop.openFolder(a, path)
	}
}

// noFileHint is a shortcut the empty editor teaches.
type noFileHint struct {
	label string
	keys  shortcutAction
}

// noFileView fills the editor while no file is open with the shortcuts that
// find one, which are otherwise only in the menus.
func (a *App) noFileView(c *ui.Context) {
	colors := a.look.palette
	hints := []noFileHint{
		{locales.HintOpenFile(), goToFileKeys},
		{locales.HintSearchProject(), findInFilesKeys},
		{locales.HintProjectSymbols(), goToProjectSymbolKeys},
		{locales.HintShowChanges(), showChangesKeys},
		{locales.HintFindReferences(), findReferencesKeys},
	}
	hints = slices.DeleteFunc(hints, func(hint noFileHint) bool { return hint.keys.binding(a.settings).key == "" })
	ui.Column(c).Grow(1).FillWidth().Center().Gap(8).Children(func() {
		ui.Icon(c, iconPerch).FontSize(32).TextColor(colors.border)
		ui.Text(c, locales.ChooseFileToRead()).TextColor(colors.muted)
		ui.Box(c).Height(4)
		ui.Row(c).Gap(14).Children(func() {
			ui.Column(c).Gap(7).AlignItems(ui.End).Children(func() {
				for _, hint := range hints {
					ui.Box(c).Height(noFileHintHeight).Center().Children(func() {
						ui.Text(c, hint.label).FontSize(smallFontSize - 1).TextColor(colors.muted)
					})
				}
			})
			ui.Column(c).Gap(7).AlignItems(ui.Start).Children(func() {
				for _, hint := range hints {
					ui.Box(c).Height(noFileHintHeight).Center().Children(func() {
						a.keycapView(c, hint.keys.binding(a.settings).label())
					})
				}
			})
		})
	})
}

// noFileHintHeight lines up a hint's words with its key cap.
const noFileHintHeight float32 = 18
