package app

import "github.com/egoist/mygo/ui"

// lastTabNumber is the number that always means the last tab, as in browsers.
const lastTabNumber int = 9

// tabNumberKeys are the keys that choose a tab by its place, from the first.
var tabNumberKeys = []ui.Key{
	ui.Key1, ui.Key2, ui.Key3, ui.Key4, ui.Key5, ui.Key6, ui.Key7, ui.Key8, ui.Key9,
}

// tabAfter returns the tab step places from active, going round the ends.
func tabAfter(active int, count int, step int) int {
	return (active + step + count) % count
}

// tabAtNumber returns the tab a number key chooses, and whether there is one.
func tabAtNumber(number int, count int) (int, bool) {
	if number == lastTabNumber {
		return count - 1, count > 0
	}
	return number - 1, number <= count
}

// nextTab shows the tab after the current one.
func (a *App) nextTab() {
	if len(a.documents) > 0 {
		a.showTab(tabAfter(a.active, len(a.documents), 1))
	}
}

// previousTab shows the tab before the current one.
func (a *App) previousTab() {
	if len(a.documents) > 0 {
		a.showTab(tabAfter(a.active, len(a.documents), -1))
	}
}

// showTab shows a tab and moves the keyboard to its text.
func (a *App) showTab(index int) {
	a.chooseTab(index)
	d := a.documents[index]
	if d.editor != nil && !d.showsPreview() && !d.showsSVGPreview() {
		d.focus.Focus()
	}
}

// tabShortcuts switches tabs with Control+Tab and Command+number.
func (a *App) tabShortcuts(c *ui.Context) {
	if c.Shortcut(ui.Ctrl, ui.KeyTab) {
		a.nextTab()
	}
	if c.Shortcut(ui.Ctrl|ui.Shift, ui.KeyTab) {
		a.previousTab()
	}
	for place, key := range tabNumberKeys {
		if !c.Shortcut(ui.Cmd, key) {
			continue
		}
		if index, found := tabAtNumber(place+1, len(a.documents)); found {
			a.showTab(index)
		}
	}
}
