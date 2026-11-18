package app

import (
	"path/filepath"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"github.com/egoist/mygo/ui/markdown"

	"perch/locales"
)

const (
	headerHeight     float32 = 40
	pathBarHeight    float32 = 36
	statusBarHeight  float32 = 24
	trafficLightRoom float32 = 84
	markdownMaxWidth float32 = 760
	smallFontSize    float32 = 12
	iconFontSize     float32 = 14

	sidebarTabWidth    float32 = 34
	sidebarTabHeight   float32 = 28
	sidebarTabIconSize float32 = 16
	projectNameHeight  float32 = 30
	minSidebarWidth    float32 = 232

	tabHeight   float32 = 30
	panelMargin float32 = 8
	// panelGap is the space each panel keeps from the divider between them.
	panelGap     float32 = 4
	treeRowInset float32 = 8
	// tabTextLift raises a tab's name, whose lowercase letters sit below the
	// middle of its line, to the middle of the tab.
	tabTextLift float32 = 2
)

// view builds the window.
func (a *App) view(c *ui.Context) {
	a.look = a.lookFor(c.Theme())
	c.SetTheme(a.look.theme)
	c.SetMenuPresentation(ui.PopoverMenus)
	a.toastsView(c)
	if a.root == "" {
		a.welcomeView(c)
		a.outlineView(c)
		a.settingsView(c)
		return
	}
	a.sidebarWidth = max(a.sidebarWidth, minSidebarWidth)
	ui.Themed(c, a.look.shell).Fill().Children(func() {
		ui.Split(c, &a.sidebarWidth, func() {
			ui.Themed(c, a.look.theme).Fill().Children(func() { a.sidebarView(c) })
		}, func() {
			ui.Themed(c, a.look.theme).Fill().Children(func() { a.mainView(c) })
		}).Fill()
	})
	a.closeTabAlert(c)
	a.closeWindowAlert(c)
	a.quickOpenView(c)
	a.outlineView(c)
	a.settingsView(c)
	a.fileDialogView(c)
	a.trackSession()
}

func (a *App) welcomeView(c *ui.Context) {
	colors := a.look.palette
	ui.Column(c).Fill().Children(func() {
		ui.Box(c).FillWidth().Height(headerHeight).Shrink(0).DragWindow()
		ui.Column(c).Grow(1).FillWidth().Center().Gap(10).Padding(0, 0, headerHeight, 0).Children(func() {
			ui.Icon(c, iconPerch).FontSize(44).TextColor(colors.accent)
			ui.Text(c, "Perch").FontSize(26).FontWeight(600).LetterSpacing(-0.3)
			ui.Text(c, locales.AppTagline()).TextColor(colors.muted)
			ui.Box(c).Height(8)
			if ui.PrimaryButton(c, locales.OpenFolder()).Clicked() {
				a.chooseFolder()
			}
			ui.Text(c, "⌘O").FontSize(smallFontSize).TextColor(colors.muted)
			a.recentProjectsView(c)
		})
	})
}

func (a *App) sidebarView(c *ui.Context) {
	colors := a.look.palette
	ui.Column(c).Fill().Background(colors.sidebar).Children(func() {
		a.sidebarHeaderView(c)
		ui.Column(c).Grow(1).FillWidth().Padding(0, panelGap, panelMargin, panelMargin).Children(func() {
			a.panelView(c, func() {
				a.projectNameView(c)
				a.sidebarContentView(c)
			})
		})
	})
}

// sidebarContentView shows the sidebar's chosen view.
func (a *App) sidebarContentView(c *ui.Context) {
	switch a.sidebarTab {
	case sidebarSearch:
		a.findInFilesView(c)
	case sidebarChanges:
		a.changesPanelView(c)
	default:
		a.fileTreeView(c)
	}
}

// panelView builds one of the window's rounded panels.
func (a *App) panelView(c *ui.Context, content func()) {
	colors := a.look.palette
	ui.Column(c).Fill().Clip().Radius(radiusCard).Background(colors.background).Border(1, colors.border).Children(content)
}

// sidebarHeaderView puts the sidebar's views beside the window's controls.
func (a *App) sidebarHeaderView(c *ui.Context) {
	ui.Row(c).FillWidth().Height(headerHeight).Shrink(0).Gap(2).PaddingStart(trafficLightRoom).PaddingEnd(8).
		AlignItems(ui.Center).DragWindow().Children(func() {
		if a.sidebarTabView(c, iconFiles, locales.TabFiles(), a.sidebarTab == sidebarFiles) {
			a.sidebarTab = sidebarFiles
		}
		if a.sidebarTabView(c, iconSearch, locales.TabSearch(), a.sidebarTab == sidebarSearch) {
			a.showFindInFiles()
		}
		if a.sidebarTabView(c, iconChanges, locales.TabChanges(), a.sidebarTab == sidebarChanges) {
			a.showChanges()
		}
		ui.Spacer(c)
		if a.sidebarTabView(c, iconSettings, locales.SettingsTitle(), false) {
			a.showSettings()
		}
	})
}

// projectNameView names the project on a line of its own, with room for a long
// name, and shows where it is when the pointer rests on it.
func (a *App) projectNameView(c *ui.Context) {
	colors := a.look.palette
	ui.Row(c).FillWidth().Height(projectNameHeight).Shrink(0).Padding(0, 14).AlignItems(ui.Center).Children(func() {
		name := ui.Text(c, filepath.Base(a.root)).FontWeight(600).MaxLines(1).Shrink(1)
		sidebarTooltip(c, name, a.root, colors)
	})
}

// sidebarTabView builds a button of the sidebar's views and reports a click.
func (a *App) sidebarTabView(c *ui.Context, icon *ui.SVG, label string, active bool) bool {
	colors := a.look.palette
	button := ui.Box(c).Size(sidebarTabWidth, sidebarTabHeight).Center().Radius(radiusControl).Label(label)
	sidebarTooltip(c, button, label, colors)
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

// sidebarTooltip names a sidebar button below it, clear of the window's controls
// above the sidebar.
func sidebarTooltip(c *ui.Context, button ui.Element, label string, colors palette) {
	ui.TooltipBase(c, button, func(tip ui.Element) {
		tip.AttachTo(button, ui.AnchorBottom, ui.AnchorTop).Margin(6, 0, 0, 0).Padding(4, 8).Radius(radiusSmall).
			Background(colors.text).TextColor(colors.background).FontSize(smallFontSize)
		tip.Children(func() { ui.Text(c, label) })
	})
}

func (a *App) fileTreeView(c *ui.Context) {
	ui.Themed(c, a.look.tree).Grow(1).FillWidth().Children(func() {
		a.tree.List.Selected = &a.treeRow
		tree := ui.Outline(c, &a.tree, a.childPaths("."), a.childPaths, func(item string) {
			a.treeRowView(c, item)
		}).Grow(1).FillWidth().Padding(0, treeRowInset).Label(locales.TabFiles()).ContextMenu(func(m *ui.Menu) {
			a.fileMenu(m, ".")
		})
		if tree.Changed() {
			a.chooseTreeRow()
		}
	})
}

func (a *App) treeRowView(c *ui.Context, item string) {
	entry := a.entries[item]
	colors := a.look.palette
	ui.Row(c).Grow(1).FillWidth().Gap(7).AlignItems(ui.Center).Children(func() {
		if entry.IsDir {
			ui.Icon(c, iconFolder).FontSize(iconFontSize).TextColor(colors.muted)
		} else {
			ui.Icon(c, iconFile).FontSize(iconFontSize).TextColor(fileTint(entry.Name, colors))
		}
		ui.Text(c, entry.Name).MaxLines(1)
	}).ContextMenu(func(m *ui.Menu) {
		a.fileMenu(m, item)
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
	a.closeOutline()
	a.closeChange()
	a.active = index
	a.treeRow = noRow
	for row := range a.tree.Rows() {
		if a.tree.Item(row) == a.documents[index].path {
			a.treeRow = row
			return
		}
	}
}

// mainView floats the open file on a rounded panel below its tabs.
func (a *App) mainView(c *ui.Context) {
	a.tabShortcuts(c)
	colors := a.look.palette
	ui.Column(c).Fill().Background(colors.sidebar).Children(func() {
		a.tabsView(c)
		ui.Column(c).Grow(1).FillWidth().Padding(0, panelMargin, panelMargin, panelGap).Children(func() {
			a.panelView(c, func() { a.panelContentView(c) })
		})
	})
}

// panelContentView shows the review, the open file, or an invitation to open one.
func (a *App) panelContentView(c *ui.Context) {
	if a.showsChange() {
		a.changeView(c)
		return
	}
	d := a.activeDocument()
	if d == nil {
		ui.Column(c).Grow(1).FillWidth().Center().Gap(8).Children(func() {
			ui.Icon(c, iconPerch).FontSize(32).TextColor(a.look.palette.border)
			ui.Text(c, locales.ChooseFileToRead()).TextColor(a.look.palette.muted)
		})
		return
	}
	a.pathBarView(c, d)
	a.conflictView(c, d)
	a.documentView(c, d)
	a.statusView(c, d)
}

func (a *App) tabsView(c *ui.Context) {
	colors := a.look.palette
	var closing *document
	ui.Row(c).FillWidth().Height(headerHeight).Shrink(0).PaddingStart(panelGap).PaddingEnd(panelMargin).Background(colors.sidebar).DragWindow().Children(func() {
		ui.ScrollHorizontal(c).Shrink(1).FillHeight().Children(func() {
			ui.Row(c).FillHeight().Gap(4).AlignItems(ui.Center).Children(func() {
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
	tab := ui.Row(c).Height(tabHeight).Gap(6).PaddingStart(12).PaddingEnd(6).AlignItems(ui.Center).Radius(radiusControl)
	if active {
		tab.Background(colors.background).Border(1, colors.border)
	} else if tab.Hovered() {
		tab.Background(colors.hover.Alpha(0.5))
	}
	if tab.Clicked() {
		a.chooseTab(index)
	}
	hovered := tab.Hovered()
	tab.Children(func() {
		name := ui.Text(c, d.name()).MaxLines(1).Padding(0, 0, tabTextLift, 0)
		if !active {
			name.TextColor(colors.muted)
		}
		closed = a.tabCloseView(c, d, hovered || active)
	})
	return closed
}

// tabCloseView shows the unsaved mark of a tab until the pointer comes to
// the tab, and then its close button.
func (a *App) tabCloseView(c *ui.Context, d *document, showClose bool) bool {
	colors := a.look.palette
	button := ui.Box(c).Size(18, 18).Center().Radius(radiusSmall).Label(locales.ClosePath(d.path))
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

// pathCrumbsView shows a file's path, its folders quieter than its name.
func (a *App) pathCrumbsView(c *ui.Context, relPath string) {
	colors := a.look.palette
	parts := strings.Split(relPath, "/")
	for i, part := range parts {
		if i > 0 {
			ui.Text(c, "›").FontSize(smallFontSize).TextColor(colors.muted)
		}
		text := ui.Text(c, part).FontSize(smallFontSize).MaxLines(1)
		if i < len(parts)-1 {
			text.TextColor(colors.muted)
		}
	}
}

func (a *App) pathBarView(c *ui.Context, d *document) {
	colors := a.look.palette
	ui.Row(c).FillWidth().Height(pathBarHeight).Shrink(0).Gap(5).Padding(0, 14).AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(colors.border).Children(func() {
		a.pathCrumbsView(c, d.displayPath())
		if d.external {
			ui.Text(c, locales.ReadOnly()).FontSize(smallFontSize-1).TextColor(colors.muted).Padding(0, 0, 0, 6)
		}
		ui.Spacer(c)
		switch {
		case d.isMarkdown():
			ui.Segmented(c, &d.markdownView, locales.MarkdownPreview(), locales.MarkdownEdit())
		case d.isSVG():
			ui.Segmented(c, &d.markdownView, locales.MarkdownPreview(), locales.MarkdownSource())
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
		if d.editor == nil {
			return
		}
		if ui.PrimaryButton(c, keep).Clicked() {
			a.keepMine(d)
		}
	})
}

func conflictWording(kind conflict) (message string, keep string, drop string) {
	if kind == conflictRemoved {
		return locales.ConflictRemoved(), locales.RestoreFile(), locales.CloseTab()
	}
	return locales.ConflictChanged(), locales.KeepMine(), locales.UseDiskVersion()
}

func (a *App) documentView(c *ui.Context, d *document) {
	c.Key(d.path)
	a.followLineBaseline(d)
	switch {
	case d.isRasterImage():
		a.rasterImageView(c, d)
		return
	case d.showsSVGPreview():
		a.svgImageView(c, d)
		return
	}
	if d.showsPreview() {
		ui.Scroll(c).TrackScroll(&d.previewScroll).Grow(1).FillWidth().Padding(28, 32).AlignItems(ui.Center).Children(func() {
			markdown.View(c, d.markdownText(), d.markdownOptions).MaxWidth(markdownMaxWidth).FillWidth()
		})
		return
	}
	d.editor.Theme = a.look.code
	a.applyEditorSettings(d.editor)
	if d.scrollPending && d.editor.ViewportHeight() > 0 {
		d.editor.SetScrollY(d.restoredScroll)
		d.scrollPending = false
	}
	editor.View(c, d.editor).Bind(&d.focus).Grow(1).FillWidth()
	if d.scrollPending {
		c.After(0)
	}
}

func (a *App) statusView(c *ui.Context, d *document) {
	colors := a.look.palette
	item := func(text string) {
		ui.Text(c, text).FontSize(smallFontSize - 1).TextColor(colors.muted)
	}
	if d.isRasterImage() || d.showsSVGPreview() {
		ui.Row(c).FillWidth().Height(statusBarHeight).Shrink(0).Padding(0, 14).AlignItems(ui.Center).BorderWidth(1, 0, 0, 0).BorderColor(colors.border).Children(func() {
			ui.Spacer(c)
			item(d.image.status())
		})
		return
	}
	position := d.editor.CursorPos()
	language := locales.PlainText()
	if l := d.editor.Buffer.Language(); l != nil {
		language = l.Name
	}
	state := locales.StatusSaved()
	if d.modified() {
		state = locales.StatusEdited()
	}
	ui.Row(c).FillWidth().Height(statusBarHeight).Shrink(0).Gap(16).Padding(0, 14).AlignItems(ui.Center).BorderWidth(1, 0, 0, 0).BorderColor(colors.border).Children(func() {
		item(state)
		if a.definition.pending {
			item(locales.FindingDefinition())
		}
		ui.Spacer(c)
		item(locales.CursorPosition(position.Line+1, position.Col+1))
		item(locales.LineCount(d.editor.Buffer.LineCount()))
		item(language)
	})
}

const (
	alertDiscard int = 1
	alertSave    int = 2
)

func (a *App) closeTabAlert(c *ui.Context) {
	if a.closingDocument == nil {
		return
	}
	d := a.closingDocument
	title := locales.SaveChangesTo(d.name())
	switch ui.AlertDialog(c, &a.askCloseTab, title, locales.UnsavedDetail(), locales.Cancel(), locales.DontSave(), locales.Save()) {
	case alertDiscard:
		a.closeTab(d)
	case alertSave:
		if err := a.save(d); err != nil {
			a.notice = saveFailure(d.name(), err)
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
	switch ui.AlertDialog(c, &a.askCloseWindow, locales.SaveBeforeClosing(), locales.UnsavedDetail(), locales.Cancel(), locales.DontSave(), locales.SaveAll()) {
	case alertDiscard:
		a.confirmCloseWindow()
	case alertSave:
		if a.saveAll() {
			a.confirmCloseWindow()
		}
	}
}
