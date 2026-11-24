package app

import (
	"maps"
	"slices"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/marukolibro/Perch/locales"
)

// shortcutEdit keeps the draft in its window until Save.
type shortcutEdit struct {
	action shortcutAction
	keys   keyCombo
}

func (a *App) shortcutSettingsView(c *ui.Context) {
	if a.shortcutEdit.action.id != "" {
		a.editShortcutView(c)
		return
	}
	for _, group := range shortcutGroups {
		var rows []settingLine
		for _, action := range group.actions {
			name := strings.TrimSuffix(action.label(), "…")
			rows = append(rows, settingLine{label: name, control: func() {
				keys := action.binding(a.settings)
				label := keys.label()
				if label == "" {
					label = locales.ShortcutUnassigned()
				}
				if ui.Button(c, label).Label(locales.ChangeShortcut(name)).Clicked() {
					a.shortcutEdit = shortcutEdit{action, keys}
				}
			}})
		}
		a.settingsGroup(c, group.title(), rows...)
	}
}

func (a *App) editShortcutView(c *ui.Context) {
	edit := &a.shortcutEdit
	title := strings.TrimSuffix(edit.action.label(), "…")
	a.settingsGroup(c, title,
		settingLine{label: locales.ShortcutModifiers(), control: func() {
			ui.Row(c).Gap(4).Wrap().Children(func() {
				for _, m := range shortcutModifiers {
					on := edit.keys.mods&m.mod != 0
					ui.Toggle(c, &on, m.name)
					if on {
						edit.keys.mods |= m.mod
					} else {
						edit.keys.mods &^= m.mod
					}
				}
			})
		}},
		settingLine{label: locales.ShortcutKey(), control: func() {
			ui.Combobox(c, &edit.keys.key, shortcutKeyNames).Width(140).Label(locales.ChooseShortcutKey())
		}},
	)
	if slices.ContainsFunc(numberedTabKeys[:recentNumbered], func(action shortcutAction) bool { return action.id == edit.action.id }) {
		ui.Text(c, locales.ShortcutRecentHint()).FontSize(smallFontSize).TextColor(a.look.palette.muted)
	}
	issue := ""
	if !edit.keys.valid() {
		issue = locales.ShortcutNeedsModifier()
	} else if owner := shortcutConflict(a.settings, edit.action, edit.keys, mygo.App.Menu()); owner != "" {
		issue = locales.ShortcutConflict(strings.TrimSuffix(owner, "…"))
	}
	if issue != "" {
		ui.Text(c, issue).TextColor(a.look.palette.warning)
	}
	ui.Row(c).FillWidth().Wrap().Gap(6).Children(func() {
		if ui.Button(c, locales.ShortcutDefault()).Clicked() {
			edit.keys = edit.action.defaults
		}
		if ui.Button(c, locales.ShortcutClear()).Clicked() {
			edit.keys = keyCombo{}
		}
		ui.Spacer(c)
		if ui.Button(c, locales.Cancel()).Clicked() {
			a.shortcutEdit = shortcutEdit{}
			return
		}
		if ui.PrimaryButton(c, locales.Save()).Disabled(issue != "").Clicked() && issue == "" {
			a.saveShortcut(edit.action, edit.keys)
			a.shortcutEdit = shortcutEdit{}
		}
	})
}

func (a *App) saveShortcut(action shortcutAction, keys keyCombo) {
	next := a.settings
	next.Shortcuts = maps.Clone(next.Shortcuts)
	if keys == action.defaults {
		delete(next.Shortcuts, action.id)
	} else {
		if next.Shortcuts == nil {
			next.Shortcuts = make(map[string]string)
		}
		next.Shortcuts[action.id] = keys.accelerator()
	}
	a.updateSettings(next)
}
