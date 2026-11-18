package app

import "github.com/egoist/mygo/ui"

// The floating finders, quick open and go to symbol, share this shape.
const (
	finderWidth      float32 = 600
	finderListHeight float32 = 336
	finderRowHeight  float32 = 32
	finderListInset  float32 = 6
	finderNoteHeight float32 = 88
	finderTopSpace   float32 = 84
)

// finderPanel builds the floating panel of a finder over a dimmed window.
func (a *App) finderPanel(c *ui.Context, open *bool, content func()) {
	colors := a.look.palette
	ui.DialogBase(c, open, func(backdrop ui.Element, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.16)).Justify(ui.Start).Padding(finderTopSpace, 0, 0, 0)
		panel.Width(finderWidth).Background(colors.background).Radius(radiusDialog).Border(1, colors.border).
			Shadow(0, 12, 36, 0, ui.RGBA(0, 0, 0, 0.22))
		content()
	})
}

// finderMoveShortcuts moves a finder's choice with Up and Down.
func finderMoveShortcuts(c *ui.Context, move func(int)) {
	if c.Shortcut(0, ui.KeyDown) {
		move(1)
	}
	if c.Shortcut(0, ui.KeyUp) {
		move(-1)
	}
}

// finderRow builds one row of a finder, highlighted when it is chosen.
func (a *App) finderRow(c *ui.Context, chosen int, index int) ui.Element {
	row := ui.Row(c).FillWidth().Height(finderRowHeight).Gap(9).Padding(0, 10).Radius(radiusControl).AlignItems(ui.Center)
	if index == chosen {
		row.Background(a.look.palette.hover)
	}
	return row
}

// finderHeightFor fits a finder's list to its rows, up to its full height.
func finderHeightFor(rows int) float32 {
	return min(finderListHeight, float32(rows)*finderRowHeight+2*finderListInset)
}

// finderNote says why a finder shows no rows, with a hint when it has one.
func (a *App) finderNote(c *ui.Context, message string, detail string) {
	colors := a.look.palette
	ui.Column(c).FillWidth().Height(finderNoteHeight).Center().Gap(4).Padding(0, 24).Children(func() {
		ui.Text(c, message).TextColor(colors.muted).MaxLines(2)
		if detail != "" {
			ui.Text(c, detail).FontSize(smallFontSize - 1).TextColor(colors.muted).MaxLines(2)
		}
	})
}
