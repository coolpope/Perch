package app

import (
	"path"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

// The corner radii of the interface, from its smallest parts to its panels.
const (
	radiusSmall   float32 = 6
	radiusControl float32 = 9
	radiusCard    float32 = 14
	radiusDialog  float32 = 18
)

// palette is the color scheme of one appearance: warm paper or graphite with
// a single blue accent taken from the app icon.
type palette struct {
	background ui.Color
	sidebar    ui.Color
	hover      ui.Color
	border     ui.Color
	text       ui.Color
	muted      ui.Color
	accent     ui.Color
	warning    ui.Color
	danger     ui.Color

	keyword  ui.Color
	typeName ui.Color
	function ui.Color
	str      ui.Color
	number   ui.Color
	comment  ui.Color
	markup   ui.Color
}

func lightPalette() palette {
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

func darkPalette() palette {
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

// look is everything the window draws with in one appearance.
type look struct {
	palette palette
	theme   *ui.Theme
	// tree marks the chosen file with a quiet fill, which keeps the colors
	// of its icon readable.
	tree *ui.Theme
	// shell draws the divider beside the sidebar in the sidebar's color, so
	// that the main panel stands on its own.
	shell *ui.Theme
	code  *editor.Theme
}

func newLook(base *ui.Theme, colors palette) *look {
	theme := *base.WithAccent(colors.accent)
	theme.Background = colors.background
	theme.Surface = colors.sidebar
	theme.SurfaceHover = colors.hover
	theme.SurfacePressed = colors.hover
	theme.Border = colors.border
	theme.Text = colors.text
	theme.TextMuted = colors.muted
	theme.Warning = colors.warning
	theme.Danger = colors.danger
	theme.Radius = radiusControl
	theme.FontSize = 13
	tree := theme
	tree.Accent = colors.hover
	tree.AccentHover = colors.hover
	tree.AccentPressed = colors.hover
	tree.AccentText = colors.text
	tree.InactiveAccent = colors.hover
	shell := theme
	shell.Border = colors.sidebar
	return &look{palette: colors, theme: &theme, tree: &tree, shell: &shell, code: newCodeTheme(&theme, colors)}
}

func newCodeTheme(theme *ui.Theme, colors palette) *editor.Theme {
	code := editor.DefaultTheme(theme)
	code.ActiveLine = colors.hover.Alpha(0.45)
	code.LineNumber = colors.muted.Alpha(0.55)
	code.ActiveLineNumber = colors.text
	code.IndentGuide = colors.border
	styles := map[editor.TokenKind]ui.Color{
		editor.TokenKeyword:  colors.keyword,
		editor.TokenType:     colors.typeName,
		editor.TokenFunction: colors.function,
		editor.TokenProperty: colors.text,
		editor.TokenVariable: colors.text,
		editor.TokenConstant: colors.number,
		editor.TokenNumber:   colors.number,
		editor.TokenString:   colors.str,
		editor.TokenEscape:   colors.keyword,
		editor.TokenRegexp:   colors.str,
		editor.TokenComment:  colors.comment,
		editor.TokenTag:      colors.keyword,
		editor.TokenMeta:     colors.markup,
		editor.TokenHeading:  colors.keyword,
		editor.TokenLink:     colors.function,
		editor.TokenCode:     colors.str,
	}
	for kind, color := range styles {
		code.Tokens[kind].Color = color
	}
	code.Tokens[editor.TokenComment].Italic = true
	return code
}

// lookFor returns the look of the window's appearance, built once for each.
func (a *App) lookFor(base *ui.Theme) *look {
	if base.Dark {
		if a.darkLook == nil {
			a.darkLook = newLook(base, darkPalette())
		}
		return a.darkLook
	}
	if a.lightLook == nil {
		a.lightLook = newLook(base, lightPalette())
	}
	return a.lightLook
}

// fileTint colors a file's icon by the kind of file it is.
func fileTint(name string, colors palette) ui.Color {
	switch strings.ToLower(path.Ext(name)) {
	case ".go", ".rs", ".py", ".rb", ".java", ".c", ".h", ".cpp", ".swift":
		return colors.typeName
	case ".ts", ".tsx", ".js", ".jsx", ".css", ".html", ".vue", ".svelte":
		return colors.function
	case ".md", ".markdown", ".mdx", ".txt":
		return colors.accent
	case ".json", ".yaml", ".yml", ".toml", ".mod", ".sum", ".lock":
		return colors.number
	default:
		return colors.muted
	}
}

const iconAttributes = `xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"`

var (
	iconFolder     = ui.MustParseSVG([]byte(`<svg ` + iconAttributes + `><path d="M20 20a2 2 0 0 0 2-2V8a2 2 0 0 0-2-2h-7.9a2 2 0 0 1-1.69-.9L9.6 3.9A2 2 0 0 0 7.93 3H4a2 2 0 0 0-2 2v13a2 2 0 0 0 2 2Z"/></svg>`))
	iconFile       = ui.MustParseSVG([]byte(`<svg ` + iconAttributes + `><path d="M15 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V7Z"/><path d="M14 2v4a2 2 0 0 0 2 2h4"/></svg>`))
	iconFiles      = ui.MustParseSVG([]byte(`<svg ` + iconAttributes + `><path d="M20 7h-3a2 2 0 0 1-2-2V2"/><path d="M9 18a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h7l4 4v10a2 2 0 0 1-2 2Z"/><path d="M3 7.6v12.8A1.6 1.6 0 0 0 4.6 22h9.8"/></svg>`))
	iconSearch     = ui.MustParseSVG([]byte(`<svg ` + iconAttributes + `><circle cx="11" cy="11" r="8"/><path d="m21 21-4.3-4.3"/></svg>`))
	iconChanges    = ui.MustParseSVG([]byte(`<svg ` + iconAttributes + `><circle cx="18" cy="18" r="3"/><circle cx="6" cy="6" r="3"/><path d="M13 6h3a2 2 0 0 1 2 2v7"/><path d="M11 18H8a2 2 0 0 1-2-2V9"/></svg>`))
	iconCircle     = ui.MustParseSVG([]byte(`<svg ` + iconAttributes + `><circle cx="12" cy="12" r="9"/></svg>`))
	iconCheck      = ui.MustParseSVG([]byte(`<svg ` + iconAttributes + `><circle cx="12" cy="12" r="9"/><path d="m8.5 12 2.5 2.5 4.5-5"/></svg>`))
	iconSettings   = ui.MustParseSVG([]byte(`<svg ` + iconAttributes + `><path d="M21 7h-9"/><path d="M14 17H3"/><circle cx="17" cy="17" r="3"/><circle cx="7" cy="7" r="3"/></svg>`))
	iconAlert      = ui.MustParseSVG([]byte(`<svg ` + iconAttributes + `><circle cx="12" cy="12" r="10"/><path d="M12 8v4"/><path d="M12 16h.01"/></svg>`))
	iconClose      = ui.MustParseSVG([]byte(`<svg ` + iconAttributes + `><path d="M18 6 6 18"/><path d="m6 6 12 12"/></svg>`))
	iconFilePlus   = ui.MustParseSVG([]byte(`<svg ` + iconAttributes + `><path d="M15 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V7Z"/><path d="M14 2v4a2 2 0 0 0 2 2h4"/><path d="M9 15h6"/><path d="M12 18v-6"/></svg>`))
	iconFolderPlus = ui.MustParseSVG([]byte(`<svg ` + iconAttributes + `><path d="M12 10v6"/><path d="M9 13h6"/><path d="M20 20a2 2 0 0 0 2-2V8a2 2 0 0 0-2-2h-7.9a2 2 0 0 1-1.69-.9L9.6 3.9A2 2 0 0 0 7.93 3H4a2 2 0 0 0-2 2v13a2 2 0 0 0 2 2Z"/></svg>`))
	iconPencil     = ui.MustParseSVG([]byte(`<svg ` + iconAttributes + `><path d="M21.17 6.81a2.82 2.82 0 0 0-3.99-3.99L3.84 16.17a2 2 0 0 0-.5.83l-1.32 4.35a.5.5 0 0 0 .62.62l4.35-1.32a2 2 0 0 0 .83-.5Z"/><path d="m15 5 4 4"/></svg>`))
	iconTrash      = ui.MustParseSVG([]byte(`<svg ` + iconAttributes + `><path d="M3 6h18"/><path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6"/><path d="M8 6V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"/><path d="M10 11v6"/><path d="M14 11v6"/></svg>`))
	iconCopy       = ui.MustParseSVG([]byte(`<svg ` + iconAttributes + `><rect width="14" height="14" x="8" y="8" rx="2"/><path d="M4 16a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h10a2 2 0 0 1 2 2"/></svg>`))
	iconLink       = ui.MustParseSVG([]byte(`<svg ` + iconAttributes + `><path d="M10 13a5 5 0 0 0 7.54.54l3-3a5 5 0 0 0-7.07-7.07l-1.72 1.71"/><path d="M14 11a5 5 0 0 0-7.54-.54l-3 3a5 5 0 0 0 7.07 7.07l1.71-1.71"/></svg>`))
	iconReveal     = ui.MustParseSVG([]byte(`<svg ` + iconAttributes + `><path d="m6 14 1.5-2.9A2 2 0 0 1 9.24 10H20a2 2 0 0 1 1.94 2.5l-1.54 6a2 2 0 0 1-1.95 1.5H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h3.9a2 2 0 0 1 1.69.9l.81 1.2a2 2 0 0 0 1.67.9H18a2 2 0 0 1 2 2v2"/></svg>`))
	iconBack       = ui.MustParseSVG([]byte(`<svg ` + iconAttributes + `><path d="m15 18-6-6 6-6"/></svg>`))
	iconPerch      = ui.MustParseSVG([]byte(`<svg ` + iconAttributes + `><path d="M3 20h18"/><path d="M11 20v-3"/><path d="M5 12a7 7 0 0 1 12.5-4.3L21 7l-2.6 3.1A7 7 0 0 1 12 17H9a4 4 0 0 1-4-4Z"/><path d="M15 9h.01"/></svg>`))
)
