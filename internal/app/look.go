package app

import (
	"path"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

// palette is the color scheme of one appearance: warm paper or graphite with
// a single rust accent.
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
		accent:     ui.Hex("#B4532A"),
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
		accent:     ui.Hex("#E08B5C"),
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
	code *editor.Theme
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
	theme.Radius = 6
	theme.FontSize = 13
	tree := theme
	tree.Accent = colors.hover
	tree.AccentHover = colors.hover
	tree.AccentPressed = colors.hover
	tree.AccentText = colors.text
	tree.InactiveAccent = colors.hover
	return &look{palette: colors, theme: &theme, tree: &tree, code: newCodeTheme(&theme, colors)}
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
	iconFolder = ui.MustParseSVG([]byte(`<svg ` + iconAttributes + `><path d="M20 20a2 2 0 0 0 2-2V8a2 2 0 0 0-2-2h-7.9a2 2 0 0 1-1.69-.9L9.6 3.9A2 2 0 0 0 7.93 3H4a2 2 0 0 0-2 2v13a2 2 0 0 0 2 2Z"/></svg>`))
	iconFile   = ui.MustParseSVG([]byte(`<svg ` + iconAttributes + `><path d="M15 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V7Z"/><path d="M14 2v4a2 2 0 0 0 2 2h4"/></svg>`))
	iconClose  = ui.MustParseSVG([]byte(`<svg ` + iconAttributes + `><path d="M18 6 6 18"/><path d="m6 6 12 12"/></svg>`))
	iconPerch  = ui.MustParseSVG([]byte(`<svg ` + iconAttributes + `><path d="M3 20h18"/><path d="M11 20v-3"/><path d="M5 12a7 7 0 0 1 12.5-4.3L21 7l-2.6 3.1A7 7 0 0 1 12 17H9a4 4 0 0 1-4-4Z"/><path d="M15 9h.01"/></svg>`))
)
