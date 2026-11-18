package app

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"github.com/egoist/mygo/ui/markdown"
)

const (
	headerHeight     float32 = 40
	pathBarHeight    float32 = 30
	statusBarHeight  float32 = 24
	trafficLightRoom float32 = 84
	markdownMaxWidth float32 = 760
	smallFontSize    float32 = 12
	iconFontSize     float32 = 14

	sidebarTabWidth    float32 = 34
	sidebarTabHeight   float32 = 28
	sidebarTabIconSize float32 = 16
)

// view builds the window.
func (a *App) view(c *ui.Context) {
	a.look = a.lookFor(c.Theme())
	c.SetTheme(a.look.theme)
	a.toastsView(c)
	if a.root == "" {
		a.welcomeView(c)
		return
	}
	ui.Split(c, &a.sidebarWidth, func() { a.sidebarView(c) }, func() { a.mainView(c) }).Fill()
	a.closeTabAlert(c)
	a.closeWindowAlert(c)
	a.quickOpenView(c)
}

func (a *App) welcomeView(c *ui.Context) {
	colors := a.look.palette
	ui.Column(c).Fill().Children(func() {
		ui.Box(c).FillWidth().Height(headerHeight).Shrink(0).DragWindow()
		ui.Column(c).Grow(1).FillWidth().Center().Gap(10).Padding(0, 0, headerHeight, 0).Children(func() {
			ui.Icon(c, iconPerch).FontSize(44).TextColor(colors.accent)
			ui.Text(c, "Perch").FontSize(26).FontWeight(600).LetterSpacing(-0.3)
			ui.Text(c, "A quiet place to read code while your agents work.").TextColor(colors.muted)
			ui.Box(c).Height(8)
			if ui.PrimaryButton(c, "Open Folder").Clicked() {
				a.chooseFolder()
			}
			ui.Text(c, "⌘O").FontSize(smallFontSize).TextColor(colors.muted)
		})
	})
}

func (a *App) sidebarView(c *ui.Context) {
	colors := a.look.palette
	ui.Column(c).Fill().Background(colors.sidebar).Children(func() {
		ui.Row(c).FillWidth().Height(headerHeight).Shrink(0).PaddingStart(trafficLightRoom).PaddingEnd(12).AlignItems(ui.Center).DragWindow().Children(func() {
			ui.Text(c, filepath.Base(a.root)).FontWeight(600).TextColor(colors.muted).MaxLines(1)
		})
		ui.Row(c).FillWidth().Shrink(0).Gap(2).Padding(0, 10, 6, 10).Children(func() {
			if a.sidebarTabView(c, iconFiles, "Files", a.sidebarTab == sidebarFiles) {
				a.sidebarTab = sidebarFiles
			}
			if a.sidebarTabView(c, iconSearch, "Search", a.sidebarTab == sidebarSearch) {
				a.showFindInFiles()
			}
		})
		if a.sidebarTab == sidebarSearch {
			a.findInFilesView(c)
			return
		}
		a.fileTreeView(c)
	})
}

// sidebarTabView builds a button of the sidebar's views and reports a click.
func (a *App) sidebarTabView(c *ui.Context, icon *ui.SVG, label string, active bool) bool {
	colors := a.look.palette
	button := ui.Box(c).Size(sidebarTabWidth, sidebarTabHeight).Center().Radius(6).Label(label).Tooltip(label)
	color := colors.muted
	switch {
	case active:
		button.Background(colors.accent.Alpha(0.14))
		color = colors.accent
	case button.Hovered():
		button.Background(colors.hover)
		color = colors.text
	}
	clicked := button.Clicked()
	button.Children(func() {
		ui.Icon(c, icon).FontSize(sidebarTabIconSize).TextColor(color)
	})
	return clicked
}

func (a *App) fileTreeView(c *ui.Context) {
	ui.Themed(c, a.look.tree).Grow(1).FillWidth().Children(func() {
		a.tree.List.Selected = &a.treeRow
		tree := ui.Outline(c, &a.tree, a.childPaths("."), a.childPaths, func(item string) {
			a.treeRowView(c, item)
		}).Grow(1).FillWidth().Label("Files")
		if tree.Changed() {
			a.chooseTreeRow()
		}
	})
}

func (a *App) treeRowView(c *ui.Context, item string) {
	entry := a.entries[item]
	colors := a.look.palette
	ui.Row(c).Gap(7).AlignItems(ui.Center).Children(func() {
		if entry.IsDir {
			ui.Icon(c, iconFolder).FontSize(iconFontSize).TextColor(colors.muted)
		} else {
			ui.Icon(c, iconFile).FontSize(iconFontSize).TextColor(fileTint(entry.Name, colors))
		}
		ui.Text(c, entry.Name).MaxLines(1)
	})
}

// chooseTreeRow opens the file of the chosen row, or opens or closes the
// directory.
func (a *App) chooseTreeRow() {
	if a.treeRow < 0 || a.treeRow >= a.tree.Rows() {
		return
	}
	item := a.tree.Item(a.treeRow)
	if a.entries[item].IsDir {
		toggleOpen(&a.tree.Open, item)
		return
	}
	a.openFile(item)
}

func toggleOpen(open *ui.Selection[string], item string) {
	if open.Has(item) {
		open.Remove(item)
		return
	}
	open.Add(item)
}

// chooseTab shows a tab and marks its file in the tree when the row shows.
func (a *App) chooseTab(index int) {
	a.active = index
	a.treeRow = noRow
	for row := range a.tree.Rows() {
		if a.tree.Item(row) == a.documents[index].path {
			a.treeRow = row
			return
		}
	}
}

func (a *App) mainView(c *ui.Context) {
	ui.Column(c).Fill().Background(a.look.palette.background).Children(func() {
		a.tabsView(c)
		d := a.activeDocument()
		if d == nil {
			ui.Column(c).Grow(1).FillWidth().Center().Gap(8).Children(func() {
				ui.Icon(c, iconPerch).FontSize(32).TextColor(a.look.palette.border)
				ui.Text(c, "Choose a file to read").TextColor(a.look.palette.muted)
			})
			return
		}
		a.pathBarView(c, d)
		a.conflictView(c, d)
		a.documentView(c, d)
		a.statusView(c, d)
	})
}

func (a *App) tabsView(c *ui.Context) {
	colors := a.look.palette
	var closing *document
	ui.Row(c).FillWidth().Height(headerHeight).Shrink(0).Background(colors.sidebar).DragWindow().Children(func() {
		ui.ScrollHorizontal(c).Shrink(1).FillHeight().Children(func() {
			ui.Row(c).FillHeight().Children(func() {
				for i, d := range a.documents {
					c.Key(d.path)
					if a.tabView(c, d, i == a.active, i) {
						closing = d
					}
				}
			})
		})
	})
	if closing != nil {
		a.requestCloseTab(closing)
	}
}

// tabView builds a tab and reports a click on its close button.
func (a *App) tabView(c *ui.Context, d *document, active bool, index int) bool {
	colors := a.look.palette
	closed := false
	tab := ui.Column(c).FillHeight()
	if active {
		tab.Background(colors.background)
	} else if tab.Hovered() {
		tab.Background(colors.hover.Alpha(0.5))
	}
	if tab.Clicked() {
		a.chooseTab(index)
	}
	hovered := tab.Hovered()
	tab.Children(func() {
		strip := ui.Box(c).FillWidth().Height(2).Shrink(0)
		if active {
			strip.Background(colors.accent)
		}
		ui.Row(c).Grow(1).Gap(6).PaddingStart(14).PaddingEnd(8).AlignItems(ui.Center).Children(func() {
			name := ui.Text(c, d.name()).MaxLines(1)
			if !active {
				name.TextColor(colors.muted)
			}
			closed = a.tabCloseView(c, d, hovered || active)
		})
	})
	return closed
}

// tabCloseView shows the unsaved mark of a tab until the pointer comes to
// the tab, and then its close button.
func (a *App) tabCloseView(c *ui.Context, d *document, showClose bool) bool {
	colors := a.look.palette
	button := ui.Box(c).Size(18, 18).Center().Radius(4).Label("Close " + d.path)
	if button.Hovered() {
		button.Background(colors.hover)
	}
	clicked := button.Clicked()
	button.Children(func() {
		switch {
		case d.modified() && !button.Hovered():
			ui.Box(c).Size(7, 7).Radius(4).Background(colors.accent)
		case showClose:
			ui.Icon(c, iconClose).FontSize(12).TextColor(colors.muted)
		}
	})
	return clicked
}

func (a *App) pathBarView(c *ui.Context, d *document) {
	colors := a.look.palette
	parts := strings.Split(d.path, "/")
	ui.Row(c).FillWidth().Height(pathBarHeight).Shrink(0).Gap(5).Padding(0, 14).AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(colors.border).Children(func() {
		for i, part := range parts {
			if i > 0 {
				ui.Text(c, "›").FontSize(smallFontSize).TextColor(colors.muted)
			}
			text := ui.Text(c, part).FontSize(smallFontSize).MaxLines(1)
			if i < len(parts)-1 {
				text.TextColor(colors.muted)
			}
		}
		ui.Spacer(c)
		if d.isMarkdown() {
			ui.Segmented(c, &d.markdownView, "Preview", "Edit")
		}
	})
}

// bannerView shows a message tinted with color, with actions at its end.
func (a *App) bannerView(c *ui.Context, message string, color ui.Color, actions func()) {
	ui.Row(c).FillWidth().Shrink(0).Gap(8).Padding(7, 14).AlignItems(ui.Center).Background(color.Alpha(0.1)).Children(func() {
		ui.Text(c, message).TextColor(color).Grow(1).Shrink(1)
		actions()
	})
}

// conflictView asks which version wins when the file on disk disagrees with
// the document.
func (a *App) conflictView(c *ui.Context, d *document) {
	if d.conflict == conflictNone {
		return
	}
	message, keep, drop := conflictWording(d.conflict)
	a.bannerView(c, message, a.look.palette.warning, func() {
		if ui.Button(c, drop).Clicked() {
			a.useDisk(d)
		}
		if ui.PrimaryButton(c, keep).Clicked() {
			a.keepMine(d)
		}
	})
}

func conflictWording(kind conflict) (message string, keep string, drop string) {
	if kind == conflictRemoved {
		return "This file was deleted on disk.", "Restore File", "Close Tab"
	}
	return "This file changed on disk while you were editing it.", "Keep Mine", "Use Disk Version"
}

func (a *App) documentView(c *ui.Context, d *document) {
	c.Key(d.path)
	if d.showsPreview() {
		ui.Scroll(c).Grow(1).FillWidth().Padding(28, 32).AlignItems(ui.Center).Children(func() {
			markdown.View(c, d.markdownText(), d.markdownOptions).MaxWidth(markdownMaxWidth).FillWidth()
		})
		return
	}
	d.editor.Theme = a.look.code
	editor.View(c, d.editor).Bind(&d.focus).Grow(1).FillWidth()
}

func (a *App) statusView(c *ui.Context, d *document) {
	colors := a.look.palette
	position := d.editor.CursorPos()
	language := "Plain Text"
	if l := d.editor.Buffer.Language(); l != nil {
		language = l.Name
	}
	state := "Saved"
	if d.modified() {
		state = "Edited"
	}
	item := func(text string) {
		ui.Text(c, text).FontSize(smallFontSize - 1).TextColor(colors.muted)
	}
	ui.Row(c).FillWidth().Height(statusBarHeight).Shrink(0).Gap(16).Padding(0, 14).AlignItems(ui.Center).Background(colors.sidebar).Children(func() {
		item(state)
		ui.Spacer(c)
		item(fmt.Sprintf("Ln %d, Col %d", position.Line+1, position.Col+1))
		item(fmt.Sprintf("%d lines", d.editor.Buffer.LineCount()))
		item(language)
	})
}

const (
	alertDiscard  int = 1
	alertSave     int = 2
	unsavedDetail     = "Your changes will be lost if you don't save them."
)

func (a *App) closeTabAlert(c *ui.Context) {
	if a.closingDocument == nil {
		return
	}
	d := a.closingDocument
	title := fmt.Sprintf("Save changes to %s?", d.name())
	switch ui.AlertDialog(c, &a.askCloseTab, title, unsavedDetail, "Cancel", "Don't Save", "Save") {
	case alertDiscard:
		a.closeTab(d)
	case alertSave:
		if err := a.save(d); err != nil {
			a.notice = err.Error()
		} else {
			a.closeTab(d)
		}
	}
	if !a.askCloseTab {
		a.closingDocument = nil
	}
}

func (a *App) closeWindowAlert(c *ui.Context) {
	if !a.askCloseWindow {
		return
	}
	switch ui.AlertDialog(c, &a.askCloseWindow, "Save changes before closing?", unsavedDetail, "Cancel", "Don't Save", "Save All") {
	case alertDiscard:
		a.confirmCloseWindow()
	case alertSave:
		if a.saveAll() {
			a.confirmCloseWindow()
		}
	}
}
