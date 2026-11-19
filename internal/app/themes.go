package app

import (
	"image"
	"image/color"

	"github.com/egoist/mygo/ui"

	"perch/internal/settings"
	"perch/locales"
)

const (
	// grainTile is the side of the paper grain's tile, in pixels and points.
	grainTile int = 192
	// paperGrainLight and paperGrainDark are how strongly the grain marks
	// the sheet, as a fraction of full black or white.
	paperGrainLight float32 = 0.035
	paperGrainDark  float32 = 0.03
)

// themeChoice is a theme the settings offer.
type themeChoice struct {
	id    settings.Theme
	name  func() string
	about func() string
}

// themeChoices lists the themes in the order the settings show them.
func themeChoices() []themeChoice {
	return []themeChoice{
		{settings.ThemeGrokBot, func() string { return "Grok Bot" }, locales.ThemeGrokBotAbout},
		{settings.ThemePaper, func() string { return "Paper" }, locales.ThemePaperAbout},
		{settings.ThemePerch, func() string { return "Perch" }, locales.ThemePerchAbout},
	}
}

// paletteFor returns the colors of a theme in one appearance.
func paletteFor(theme settings.Theme, dark bool) palette {
	switch {
	case theme == settings.ThemePaper && dark:
		return paperDarkPalette().completed()
	case theme == settings.ThemePaper:
		return paperLightPalette().completed()
	case theme == settings.ThemePerch && dark:
		return perchDarkPalette().completed()
	case theme == settings.ThemePerch:
		return perchLightPalette().completed()
	case dark:
		return grokBotDarkPalette().completed()
	default:
		return grokBotLightPalette().completed()
	}
}

// completed fills the colors a theme leaves to follow from its others.
func (p palette) completed() palette {
	if p.fill.A == 0 {
		p.fill = p.accent
	}
	if p.code.A == 0 {
		p.code = p.text
	}
	if p.gutter.A == 0 {
		p.gutter = p.muted.Alpha(0.55)
	}
	if p.gutterNow.A == 0 {
		p.gutterNow = p.text
	}
	if p.selection.A == 0 {
		p.selection = p.accent.Alpha(0.22)
	}
	return p
}

// grokBotLightPalette is the Sand Light of the Grok Bot app: white greys, ink
// in shades of near-black, near-black buttons and its blue for the accent.
func grokBotLightPalette() palette {
	return palette{
		background:  ui.Hex("#ffffff"),
		sidebar:     ui.Hex("#f5f5f5"),
		hover:       ui.Hex("#e8e8e8"),
		border:      ui.Hex("#e5e5e5"),
		text:        ui.Hex("#1c1c1c"),
		muted:       ui.Hex("#656565"),
		accent:      ui.Hex("#1680f3"),
		warning:     ui.Hex("#aa6300"),
		danger:      ui.Hex("#cd2834"),
		keyword:     ui.Hex("#7547d0"),
		typeName:    ui.Hex("#008273"),
		function:    ui.Hex("#0b6dd3"),
		str:         ui.Hex("#00854a"),
		number:      ui.Hex("#c14c00"),
		comment:     ui.Hex("#878787"),
		markup:      ui.Hex("#aa6300"),
		fill:        ui.Hex("#1c1c1c"),
		fillHover:   ui.Hex("#363636"),
		fillPressed: ui.Hex("#525252"),
		fillText:    ui.Hex("#ffffff"),
		gutter:      ui.Hex("#999999"),
		gutterNow:   ui.Hex("#3d3d3d"),
		selection:   ui.RGBA(30, 134, 252, 0.20),
		added:       ui.Hex("#10ab61"),
		changed:     ui.Hex("#1680f3"),
		removed:     ui.Hex("#e72e3b"),
	}
}

// grokBotDarkPalette is the Sand Dark of the Grok Bot app: near-black greys,
// near-white buttons with dark labels, and code in warm terminal colors.
func grokBotDarkPalette() palette {
	return palette{
		background:  ui.Hex("#161616"),
		sidebar:     ui.Hex("#0e0e0e"),
		hover:       ui.Hex("#262626"),
		border:      ui.Hex("#262626"),
		text:        ui.Hex("#ededed"),
		muted:       ui.Hex("#a9a9a9"),
		accent:      ui.Hex("#4d9bff"),
		warning:     ui.Hex("#e28600"),
		danger:      ui.Hex("#ff6463"),
		keyword:     ui.Hex("#bc97ff"),
		typeName:    ui.Hex("#29c6be"),
		function:    ui.Hex("#93adff"),
		str:         ui.Hex("#91c17a"),
		number:      ui.Hex("#d59556"),
		comment:     ui.Hex("#8d867e"),
		markup:      ui.Hex("#cfb47c"),
		code:        ui.Hex("#d7d1c9"),
		fill:        ui.Hex("#f5f5f5"),
		fillHover:   ui.Hex("#dbdbdb"),
		fillPressed: ui.Hex("#c1c1c1"),
		fillText:    ui.Hex("#161616"),
		gutter:      ui.Hex("#5c5c5c"),
		gutterNow:   ui.Hex("#c1c1c1"),
		selection:   ui.RGBA(24, 132, 252, 0.30),
		added:       ui.Hex("#00ba69"),
		changed:     ui.Hex("#4d9bff"),
		removed:     ui.Hex("#ff6463"),
	}
}

// paperLightPalette is one neutral grey sheet with near-black ink and a single
// blue accent, under a fine grain.
func paperLightPalette() palette {
	return palette{
		background: ui.Hex("#F4F4F4"),
		sidebar:    ui.Hex("#EDEDED"),
		hover:      ui.Hex("#DDDDDD"),
		border:     ui.Hex("#DDDDDD"),
		text:       ui.Hex("#111111"),
		muted:      ui.Hex("#666666"),
		accent:     ui.Hex("#447DFC"),
		warning:    ui.Hex("#8A5A00"),
		danger:     ui.Hex("#B3261E"),
		keyword:    ui.Hex("#2F5FD0"),
		typeName:   ui.Hex("#2F6F6A"),
		function:   ui.Hex("#3A4A6B"),
		str:        ui.Hex("#4F6B2E"),
		number:     ui.Hex("#8A5A00"),
		comment:    ui.Hex("#8C8C8C"),
		markup:     ui.Hex("#6A4FB0"),
		fillText:   ui.Hex("#FFFFFF"),
		grain:      paperGrainLight,
	}
}

// paperDarkPalette is the paper theme on a dark sheet.
func paperDarkPalette() palette {
	return palette{
		background: ui.Hex("#161616"),
		sidebar:    ui.Hex("#1D1D1D"),
		hover:      ui.Hex("#2C2C2C"),
		border:     ui.Hex("#2C2C2C"),
		text:       ui.Hex("#EDEDED"),
		muted:      ui.Hex("#A0A0A0"),
		accent:     ui.Hex("#7AA2FF"),
		warning:    ui.Hex("#D9A441"),
		danger:     ui.Hex("#E5705F"),
		keyword:    ui.Hex("#7AA2FF"),
		typeName:   ui.Hex("#7CC4BC"),
		function:   ui.Hex("#C6CFE0"),
		str:        ui.Hex("#A9C77A"),
		number:     ui.Hex("#DDB061"),
		comment:    ui.Hex("#707070"),
		markup:     ui.Hex("#C3A0E0"),
		fillText:   ui.Hex("#111111"),
		grain:      paperGrainDark,
	}
}

// perchLightPalette is warm paper with the blue of the app icon.
func perchLightPalette() palette {
	return palette{
		background: ui.Hex("#FBFAF7"),
		sidebar:    ui.Hex("#F3F1EB"),
		hover:      ui.Hex("#E9E6DE"),
		border:     ui.Hex("#E5E1D8"),
		text:       ui.Hex("#2B2925"),
		muted:      ui.Hex("#7D776C"),
		accent:     ui.Hex("#1464F0"),
		warning:    ui.Hex("#9A6A10"),
		danger:     ui.Hex("#B3382C"),
		keyword:    ui.Hex("#A8461F"),
		typeName:   ui.Hex("#1F7A6D"),
		function:   ui.Hex("#2F5FA8"),
		str:        ui.Hex("#55792A"),
		number:     ui.Hex("#9A6A10"),
		comment:    ui.Hex("#9A9487"),
		markup:     ui.Hex("#7A4A9E"),
	}
}

// perchDarkPalette is warm graphite with the blue of the app icon.
func perchDarkPalette() palette {
	return palette{
		background: ui.Hex("#1C1B19"),
		sidebar:    ui.Hex("#171614"),
		hover:      ui.Hex("#2A2926"),
		border:     ui.Hex("#2E2C29"),
		text:       ui.Hex("#E7E2D9"),
		muted:      ui.Hex("#928C80"),
		accent:     ui.Hex("#4A9DFF"),
		warning:    ui.Hex("#D9A441"),
		danger:     ui.Hex("#E5705F"),
		keyword:    ui.Hex("#E59068"),
		typeName:   ui.Hex("#6CC4B4"),
		function:   ui.Hex("#8DB4EA"),
		str:        ui.Hex("#A9C77A"),
		number:     ui.Hex("#DDB061"),
		comment:    ui.Hex("#6F6A60"),
		markup:     ui.Hex("#C3A0E0"),
	}
}

// grainHash is a small integer hash that makes the grain the same every time.
func grainHash(x uint32, y uint32) uint32 {
	h := x*374761393 + y*668265263
	h = (h ^ (h >> 13)) * 1274126177
	return h ^ (h >> 16)
}

// grainImage bakes a tile of faint specks: dark ones for a light sheet and
// light ones for a dark sheet. Its pixels are premultiplied by their alpha.
func grainImage(strength float32, dark bool) *image.RGBA {
	tile := image.NewRGBA(image.Rect(0, 0, grainTile, grainTile))
	for y := range grainTile {
		for x := range grainTile {
			noise := float32(grainHash(uint32(x), uint32(y))&0xFF) / 255
			alpha := uint8(noise*strength*255 + 0.5)
			ink := uint8(0)
			if dark {
				ink = alpha
			}
			tile.SetRGBA(x, y, color.RGBA{R: ink, G: ink, B: ink, A: alpha})
		}
	}
	return tile
}

// grainView lays the theme's grain over the window, where the pointer passes
// through it.
func (a *App) grainView(c *ui.Context) {
	grain := a.look.grain
	if grain == nil {
		return
	}
	size := float32(grainTile)
	ui.Box(c.Key("grain")).Absolute().Left(0).Top(0).Right(0).Bottom(0).PassThrough().IgnoreInput(true).AccessHidden(true).
		Draw(func(p *ui.Painter, r ui.Rect) {
			for y := float32(0); y < r.H; y += size {
				for x := float32(0); x < r.W; x += size {
					tile := ui.Rect{X: r.X + x, Y: r.Y + y, W: min(size, r.W-x), H: min(size, r.H-y)}
					p.ImageRegion(grain, ui.Rect{W: tile.W, H: tile.H}, tile, ui.ImageOptions{})
				}
			}
		})
}

// themePreview draws a small window in a theme's colors.
func themePreview(p *ui.Painter, r ui.Rect, colors palette) {
	p.Fill(r, colors.sidebar, 0)
	rail := r.W * 0.3
	panel := ui.Rect{X: r.X + rail, Y: r.Y + 10, W: r.W - rail - 6, H: r.H - 16}
	p.Fill(panel, colors.background, 5)
	for row, width := range []float32{0.62, 0.46, 0.7} {
		bar := ui.Rect{X: r.X + 8, Y: r.Y + 16 + float32(row)*11, W: (rail - 14) * width * 1.3, H: 4}
		p.Fill(bar, colors.muted.Alpha(0.55), 2)
	}
	lines := []struct {
		indent float32
		parts  []ui.Color
		widths []float32
	}{
		{0, []ui.Color{colors.keyword, colors.function}, []float32{14, 26}},
		{8, []ui.Color{colors.typeName, colors.code}, []float32{20, 18}},
		{8, []ui.Color{colors.keyword, colors.str}, []float32{12, 34}},
		{8, []ui.Color{colors.comment}, []float32{42}},
		{0, []ui.Color{colors.number, colors.code}, []float32{10, 24}},
	}
	for row, line := range lines {
		x := panel.X + 8 + line.indent
		for index, part := range line.parts {
			width := min(line.widths[index], panel.X+panel.W-x-6)
			if width <= 0 {
				break
			}
			p.Fill(ui.Rect{X: x, Y: panel.Y + 8 + float32(row)*10, W: width, H: 4}, part, 2)
			x += width + 4
		}
	}
	dot := ui.Rect{X: panel.X + panel.W - 22, Y: panel.Y + panel.H - 12, W: 16, H: 7}
	p.Fill(dot, colors.fill, 3.5)
}
