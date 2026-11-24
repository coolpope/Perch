package app

import "github.com/egoist/mygo/ui"

// lastTabNumber is the number that always means the last tab, as in browsers.
const lastTabNumber int = 9

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

// tabShortcuts switches tabs with the configured cycle and position keys.
func (a *App) tabShortcuts(c *ui.Context) {
	if cycleNextKeys.pressed(c, a.settings) {
		a.nextTab()
	}
	if cyclePreviousKeys.pressed(c, a.settings) {
		a.previousTab()
	}
	for place, key := range numberedTabKeys {
		if !key.pressed(c, a.settings) {
			continue
		}
		if index, found := tabAtNumber(place+1, len(a.documents)); found {
			a.showTab(index)
		}
	}
}
