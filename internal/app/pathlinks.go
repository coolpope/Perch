package app

import (
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egoist/mygo/ui"

	"perch/locales"
)

const (
	folderListWidth     float32 = 280
	folderListRowHeight float32 = 28
	folderListMaxHeight float32 = 340
)

// folderListState is the list a part of the path bar opens: the folder it
// shows, which a click on a folder in it changes, and the part it opened from.
type folderListState struct {
	open   bool
	anchor string
	folder string
	list   ui.ListState
}

// pathLinksView shows a project file's path as parts that open the folder
// they are in, as the file tree lists it, with the part chosen marked.
func (a *App) pathLinksView(c *ui.Context, relPath string) {
	parts := strings.Split(relPath, "/")
	ui.Row(c).MinWidth(0).ClipX().Gap(1).AlignItems(ui.Center).Children(func() {
		for index := range parts {
			if index > 0 {
				ui.Text(c, "›").FontSize(smallFontSize).TextColor(a.look.palette.muted).Margin(0, 3)
			}
			a.pathLinkView(c, strings.Join(parts[:index+1], "/"), index == len(parts)-1)
		}
	})
}

// pathLinkView builds one part of the path and the folder list it opens.
func (a *App) pathLinkView(c *ui.Context, partPath string, isFile bool) {
	colors := a.look.palette
	s := &a.folderList
	shown := s.open && s.anchor == partPath
	button := ui.ButtonBase(c.Key(partPath)).Shrink(1).MinWidth(0).Padding(2, 4).Radius(radiusSmall).Label(path.Base(partPath))
	if button.Hovered() || shown {
		button.Background(colors.hover)
	}
	color := colors.muted
	if isFile {
		color = colors.text
	}
	button.Children(func() {
		ui.Text(c, path.Base(partPath)).FontSize(smallFontSize).TextColor(color).MinWidth(0).MaxLines(1)
	})
	if button.Clicked() {
		a.openFolderList(partPath)
	}
	if !s.open || s.anchor != partPath {
		return
	}
	ui.Popover(c, button, &s.open, func() { a.folderListView(c) })
}

// openFolderList lists the folder a part of the path is in.
func (a *App) openFolderList(partPath string) {
	a.folderList = folderListState{open: true, anchor: partPath, folder: path.Dir(partPath)}
	a.scrollFolderListTo(partPath)
}

// scrollFolderListTo shows an entry of the listed folder at the top of the
// list, as the part it opened from.
func (a *App) scrollFolderListTo(entry string) {
	s := &a.folderList
	if index := slices.Index(a.childPaths(s.folder), entry); index > 0 {
		s.list.ScrollTo(index, ui.Center)
		return
	}
	s.list.ScrollTo(0, ui.Start)
}

// folderListView shows the listed folder's name, a way up, and its entries.
func (a *App) folderListView(c *ui.Context) {
	colors := a.look.palette
	s := &a.folderList
	entries := a.childPaths(s.folder)
	ui.Column(c).Width(folderListWidth).Children(func() {
		ui.Row(c).FillWidth().Gap(6).Padding(2, 4, 6, 4).AlignItems(ui.Center).Children(func() {
			if s.folder != "." {
				up := ui.ButtonBase(c).Size(20, 20).Center().Radius(radiusSmall).Label(locales.ParentFolder()).Tooltip(locales.ParentFolder())
				if up.Hovered() {
					up.Background(colors.hover)
				}
				up.Children(func() { ui.Text(c, "‹").FontSize(smallFontSize + 2).TextColor(colors.muted) })
				if up.Clicked() {
					child := s.folder
					s.folder = path.Dir(s.folder)
					a.scrollFolderListTo(child)
				}
			}
			ui.Text(c, a.folderListTitle()).FontSize(smallFontSize - 1).FontWeight(600).TextColor(colors.muted).MaxLines(1).Shrink(1)
		})
		if len(entries) == 0 {
			ui.Text(c, locales.EmptyFolder()).FontSize(smallFontSize - 1).TextColor(colors.muted).Padding(8)
			return
		}
		height := min(folderListMaxHeight, float32(len(entries))*folderListRowHeight)
		chosen := ""
		ui.List(c, &s.list, len(entries), func(row int) {
			if a.folderListRow(c, entries[row]) {
				chosen = entries[row]
			}
		}).FillWidth().Height(height)
		if chosen != "" {
			a.chooseFolderListEntry(chosen)
		}
	})
}

// folderListTitle names the listed folder, the project by its folder's name.
func (a *App) folderListTitle() string {
	if a.folderList.folder == "." {
		return filepath.Base(a.root)
	}
	return a.folderList.folder
}

// folderListRow builds one entry, marked when it is the part the list opened
// from, and reports a click on it.
func (a *App) folderListRow(c *ui.Context, entry string) bool {
	colors := a.look.palette
	isDir := a.entries[entry].IsDir
	row := ui.ButtonBase(c.Key(entry)).FillWidth().Height(folderListRowHeight).Gap(7).Padding(0, 8).
		Radius(radiusControl).Justify(ui.Start).AlignItems(ui.Center).Label(path.Base(entry))
	if row.Hovered() || entry == a.folderList.anchor {
		row.Background(colors.hover)
	}
	row.Children(func() {
		a.treeIconView(c, entry, isDir, false)
		ui.Text(c, path.Base(entry)).FontSize(smallFontSize).MaxLines(1).Shrink(1)
		if isDir {
			ui.Spacer(c)
			ui.Text(c, "›").FontSize(smallFontSize).TextColor(colors.muted)
		}
	})
	return row.Clicked()
}

// chooseFolderListEntry goes into a folder, or opens a file and closes the
// list.
func (a *App) chooseFolderListEntry(entry string) {
	if a.entries[entry].IsDir {
		a.folderList.folder = entry
		a.folderList.list.ScrollTo(0, ui.Start)
		return
	}
	a.folderList = folderListState{}
	a.openFile(entry)
}
