package app

import (
	"fmt"
	"slices"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"

	"perch/internal/settings"
	"perch/locales"
)

const (
	settingsWidth    float32 = 520
	settingsRowSpace float32 = 44

	languageSystem  = ""
	languageEnglish = "en"
	languageChinese = "zh-Hans"
)

// showSettings opens the settings over the window.
func (a *App) showSettings() {
	a.languages = languagesPage{}
	a.agents = agentsPage{}
	a.settingsOpen = true
}

// updateSettings applies new settings to every window.
func (a *App) updateSettings(next settings.Settings) {
	a.settings = next
	if a.desktop != nil {
		a.desktop.settingsChanged(next)
	}
}

// codeFont returns the font of code in the current settings.
func (a *App) codeFont() ui.Font {
	return ui.Font{Size: float32(a.settings.FontSize)}
}

// applyEditorSettings gives an editor the current settings.
func (a *App) applyEditorSettings(state *editor.State) {
	state.Font = a.codeFont()
	state.TabSize = a.settings.TabSize
	state.SoftWrap = a.settings.WordWrap
}

// applyDiffSettings gives a change's diff the current settings.
func (a *App) applyDiffSettings(diff *editor.DiffState) {
	diff.Font = a.codeFont()
	diff.TabSize = a.settings.TabSize
	diff.Wrap = a.settings.WordWrap
	diff.Layout = editor.DiffUnified
	if a.settings.DiffLayout == settings.DiffSplit {
		diff.Layout = editor.DiffSplit
	}
}

func (a *App) settingsView(c *ui.Context) {
	if !a.settingsOpen {
		return
	}
	colors := a.look.palette
	ui.DialogBase(c, &a.settingsOpen, func(backdrop ui.Element, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.22))
		panel.Width(settingsWidth).Background(colors.background).Radius(radiusDialog).Border(1, colors.border).
			Shadow(0, 14, 40, 0, ui.RGBA(0, 0, 0, 0.25)).Label(locales.SettingsTitle())
		a.settingsContent(c)
	})
}

func (a *App) settingsContent(c *ui.Context) {
	if a.languages.open {
		a.languagesContent(c)
		return
	}
	if a.agents.open {
		a.agentsContent(c)
		return
	}
	colors := a.look.palette
	ui.Column(c).FillWidth().Padding(6, 20, 16, 20).Children(func() {
		ui.Row(c).FillWidth().Height(48).AlignItems(ui.Center).Children(func() {
			ui.Text(c, locales.SettingsTitle()).FontSize(16).FontWeight(600)
			ui.Spacer(c)
			button := ui.Box(c).Size(24, 24).Center().Radius(radiusSmall).Label(locales.CloseSettings())
			if button.Hovered() {
				button.Background(colors.hover)
			}
			if button.Clicked() {
				a.settingsOpen = false
			}
			button.Children(func() { ui.Icon(c, iconClose).FontSize(14).TextColor(colors.muted) })
		})
		a.settingsSection(c, locales.SectionAppearance(), func() {
			a.settingRow(c, locales.SettingLanguage(), func() { a.languageControl(c) })
			a.settingRow(c, locales.SettingTheme(), func() { a.appearanceControl(c) })
		})
		a.settingsSection(c, locales.SectionEditor(), func() {
			a.settingRow(c, locales.SettingFontSize(), func() { a.fontSizeControl(c) })
			a.settingRow(c, locales.SettingTabSize(), func() { a.tabSizeControl(c) })
			a.settingRow(c, locales.SettingWrap(), func() { a.wordWrapControl(c) })
		})
		a.settingsSection(c, locales.SectionReading(), func() {
			a.settingRow(c, locales.SettingMarkdown(), func() { a.markdownViewControl(c) })
			a.settingRow(c, locales.SettingDiff(), func() { a.diffLayoutControl(c) })
		})
		a.settingsSection(c, locales.SectionNavigation(), func() {
			a.settingRow(c, locales.SettingLanguageServers(), func() {
				if ui.Button(c, locales.ManageLanguages()).Clicked() {
					a.showLanguages()
				}
			})
		})
		a.settingsSection(c, locales.SectionAgents(), func() {
			a.settingRow(c, locales.SettingCodingAgents(), func() {
				if ui.Button(c, locales.ManageAgents()).Clicked() {
					a.showAgents()
				}
			})
		})
	})
}

func (a *App) settingsSection(c *ui.Context, title string, rows func()) {
	ui.Column(c).FillWidth().Padding(10, 0, 4, 0).Children(func() {
		ui.Text(c, title).FontSize(smallFontSize).FontWeight(600).TextColor(a.look.palette.muted).Padding(0, 0, 2, 0)
		rows()
	})
}

func (a *App) settingRow(c *ui.Context, label string, control func()) {
	c.Key(label)
	ui.Row(c).FillWidth().Height(settingsRowSpace).AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).
		BorderColor(a.look.palette.border).Children(func() {
		ui.Text(c, label)
		ui.Spacer(c)
		control()
	})
}

// choiceControl shows options and returns the one chosen.
func choiceControl[T comparable](c *ui.Context, current T, values []T, labels ...string) T {
	index := max(slices.Index(values, current), 0)
	ui.Segmented(c, &index, labels...)
	return values[index]
}

// languageControl names each language in itself, so that a person who cannot
// read the current one still finds their own.
func (a *App) languageControl(c *ui.Context) {
	current := a.chosenLanguage()
	values := []string{languageSystem, languageEnglish, languageChinese}
	chosen := choiceControl(c, current, values, locales.FollowSystem(), "English", "简体中文")
	if chosen != current {
		a.setLanguage(chosen)
	}
}

func (a *App) appearanceControl(c *ui.Context) {
	values := []settings.Appearance{settings.AppearanceSystem, settings.AppearanceLight, settings.AppearanceDark}
	chosen := choiceControl(c, a.settings.Appearance, values, locales.FollowSystem(), locales.ThemeLight(), locales.ThemeDark())
	if chosen != a.settings.Appearance {
		next := a.settings
		next.Appearance = chosen
		a.updateSettings(next)
	}
}

func (a *App) fontSizeControl(c *ui.Context) {
	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Text(c, fmt.Sprintf("%d px", a.settings.FontSize)).TextColor(a.look.palette.muted)
		size := float64(a.settings.FontSize)
		ui.Stepper(c, &size, float64(settings.MinFontSize), float64(settings.MaxFontSize), 1).Label(locales.SettingFontSize())
		if int(size) != a.settings.FontSize {
			next := a.settings
			next.FontSize = int(size)
			a.updateSettings(next)
		}
	})
}

func (a *App) tabSizeControl(c *ui.Context) {
	chosen := choiceControl(c, a.settings.TabSize, settings.TabSizes, locales.TabSizeAuto(), "2", "4", "8")
	if chosen != a.settings.TabSize {
		next := a.settings
		next.TabSize = chosen
		a.updateSettings(next)
	}
}

func (a *App) wordWrapControl(c *ui.Context) {
	wrap := a.settings.WordWrap
	ui.Switch(c, &wrap).Label(locales.SettingWrap())
	if wrap != a.settings.WordWrap {
		next := a.settings
		next.WordWrap = wrap
		a.updateSettings(next)
	}
}

func (a *App) markdownViewControl(c *ui.Context) {
	values := []settings.MarkdownView{settings.MarkdownPreview, settings.MarkdownSource}
	chosen := choiceControl(c, a.settings.MarkdownView, values, locales.MarkdownPreview(), locales.MarkdownSource())
	if chosen != a.settings.MarkdownView {
		next := a.settings
		next.MarkdownView = chosen
		a.updateSettings(next)
	}
}

func (a *App) diffLayoutControl(c *ui.Context) {
	values := []settings.DiffLayout{settings.DiffUnified, settings.DiffSplit}
	chosen := choiceControl(c, a.settings.DiffLayout, values, locales.DiffUnified(), locales.DiffSideBySide())
	if chosen != a.settings.DiffLayout {
		next := a.settings
		next.DiffLayout = chosen
		a.updateSettings(next)
	}
}
