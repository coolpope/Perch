package app

import (
	"os"
	"runtime"
	"strings"

	"github.com/egoist/mygo/ui"

	"perch/locales"
)

// shortcut is a key with the modifiers it is pressed with, which
// shortcutLabel writes as the platform shows it.
type shortcut struct {
	command bool
	shift   bool
	key     string
}

// shortcutLabel writes a shortcut as its platform shows it: ⇧⌘F on macOS,
// Ctrl+Shift+F elsewhere.
func shortcutLabel(goos string, keys shortcut) string {
	if goos == "darwin" {
		label := ""
		if keys.shift {
			label += "⇧"
		}
		if keys.command {
			label += "⌘"
		}
		return label + keys.key
	}
	parts := make([]string, 0, 3)
	if keys.command {
		parts = append(parts, "Ctrl")
	}
	if keys.shift {
		parts = append(parts, "Shift")
	}
	return strings.Join(append(parts, keys.key), "+")
}

// keycapView shows a shortcut as a key cap.
func (a *App) keycapView(c *ui.Context, label string) {
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
				a.keycapView(c, shortcutLabel(runtime.GOOS, shortcut{command: true, key: "O"}))
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
	keys  shortcut
}

// noFileView fills the editor while no file is open with the shortcuts that
// find one, which are otherwise only in the menus.
func (a *App) noFileView(c *ui.Context) {
	colors := a.look.palette
	hints := []noFileHint{
		{locales.HintOpenFile(), shortcut{command: true, key: "P"}},
		{locales.HintSearchProject(), shortcut{command: true, shift: true, key: "F"}},
		{locales.HintProjectSymbols(), shortcut{command: true, key: "T"}},
		{locales.HintShowChanges(), shortcut{command: true, shift: true, key: "R"}},
		{locales.HintFindReferences(), shortcut{shift: true, key: "F12"}},
	}
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
						a.keycapView(c, shortcutLabel(runtime.GOOS, hint.keys))
					})
				}
			})
		})
	})
}

// noFileHintHeight lines up a hint's words with its key cap.
const noFileHintHeight float32 = 18
