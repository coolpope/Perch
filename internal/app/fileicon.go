package app

import (
	"log/slog"
	"path"

	"github.com/egoist/mygo/ui"

	"github.com/marukolibro/Perch/internal/fileicons"
)

// fileIconCache keeps what file icons cost to find and draw: the icon of each
// path shown, and each icon parsed. A project shows a few dozen kinds of
// files, so both stay small; they are let go with the folder.
type fileIconCache struct {
	byPath  map[string]fileIconChoice
	svgs    map[string]*ui.SVG
	folders map[folderIconKey]*ui.SVG
}

// folderIconKey names a folder icon, open or closed.
type folderIconKey struct {
	icon string
	open bool
}

// fileIconChoice is the icon a path has, if any.
type fileIconChoice struct {
	icon  fileicons.Icon
	found bool
}

// fileIconView draws the icon of a file of the project: the icon its kind of
// file has, in the color for the theme's appearance, or a plain page.
func (a *App) fileIconView(c *ui.Context, relPath string, size float32) ui.Element {
	choice := a.fileIconFor(relPath)
	if !choice.found {
		return ui.Icon(c, iconFile).FontSize(size).TextColor(a.look.palette.muted)
	}
	svg := a.fileIconSVG(choice.icon)
	if svg == nil {
		return ui.Icon(c, iconFile).FontSize(size).TextColor(a.look.palette.muted)
	}
	color := choice.icon.Light
	if a.look.theme.Dark {
		color = choice.icon.Dark
	}
	return coloredIcon(c, svg, size).TextColor(ui.Hex(color))
}

// fileIconFor finds the icon of a path once.
func (a *App) fileIconFor(relPath string) fileIconChoice {
	cache := &a.fileIcons
	if choice, known := cache.byPath[relPath]; known {
		return choice
	}
	icon, found, err := fileicons.For(relPath)
	if err != nil {
		slog.Warn("find a file icon", "path", relPath, "error", err)
	}
	if cache.byPath == nil {
		cache.byPath = map[string]fileIconChoice{}
	}
	choice := fileIconChoice{icon: icon, found: found}
	cache.byPath[relPath] = choice
	return choice
}

// fileIconSVG parses an icon once, and returns nil for one that cannot be read.
func (a *App) fileIconSVG(icon fileicons.Icon) *ui.SVG {
	cache := &a.fileIcons
	if svg, parsed := cache.svgs[icon.Name]; parsed {
		return svg
	}
	if cache.svgs == nil {
		cache.svgs = map[string]*ui.SVG{}
	}
	data, err := fileicons.SVG(icon)
	var svg *ui.SVG
	if err == nil {
		svg, err = ui.ParseSVG(data)
	}
	if err != nil {
		slog.Warn("draw a file icon", "icon", icon.Name, "error", err)
	}
	cache.svgs[icon.Name] = svg
	return svg
}

// treeIconView draws the icon of a row of the tree: the folder's icon, open or
// closed, or the file's icon.
func (a *App) treeIconView(c *ui.Context, item string, isDir bool, quiet bool) {
	var icon ui.Element
	if isDir {
		icon = a.folderIconView(c, path.Base(item), a.tree.Open.Has(item))
	} else {
		icon = a.fileIconView(c, item, iconFontSize)
	}
	if quiet {
		icon.Opacity(ignoredRowAlpha)
	}
}

// folderIconView draws a folder's icon: its own, in its colors, or the plain
// folder in the theme's quiet color.
func (a *App) folderIconView(c *ui.Context, name string, open bool) ui.Element {
	colors := a.look.palette
	icon, err := fileicons.ForFolder(name)
	if err != nil {
		slog.Warn("find a folder icon", "folder", name, "error", err)
		return ui.Icon(c, iconFolder).FontSize(iconFontSize).TextColor(colors.muted)
	}
	svg := a.folderIconSVG(icon, open)
	if svg == nil {
		return ui.Icon(c, iconFolder).FontSize(iconFontSize).TextColor(colors.muted)
	}
	return coloredIcon(c, svg, iconFontSize).TextColor(colors.muted)
}

// coloredIcon shows an icon in its own colors, as ui.Icon draws every shape in
// the text's: only its currentColor parts take the text color.
func coloredIcon(c *ui.Context, svg *ui.SVG, size float32) ui.Element {
	return ui.Image(c, svg).Size(size, size).Shrink(0)
}

// folderIconSVG parses a folder icon once, and returns nil for one that cannot
// be read.
func (a *App) folderIconSVG(icon string, open bool) *ui.SVG {
	cache := &a.fileIcons
	key := folderIconKey{icon: icon, open: open}
	if svg, parsed := cache.folders[key]; parsed {
		return svg
	}
	if cache.folders == nil {
		cache.folders = map[folderIconKey]*ui.SVG{}
	}
	data, err := fileicons.FolderSVG(icon, open)
	var svg *ui.SVG
	if err == nil {
		svg, err = ui.ParseSVG(data)
	}
	if err != nil {
		slog.Warn("draw a folder icon", "icon", icon, "open", open, "error", err)
	}
	cache.folders[key] = svg
	return svg
}
