package app

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"slices"

	"github.com/egoist/mygo/ui"

	"perch/internal/lsp"
	"perch/locales"
)

const (
	languagesListHeight float32 = 372
	languageRowHeight   float32 = 54
	copiedExample       string  = "\x00example"
)

// languagesPage is the settings page of the language servers.
type languagesPage struct {
	open      bool
	languages []lsp.Language
	// installed holds what has been found out, by language ID, once checked.
	installed map[string]bool
	checked   bool
	// copied is the language whose install command was just copied.
	copied string
}

// languageRegistry reads the languages in use. It reads the configuration file
// each time, so that an edit counts without restarting Perch. A file with a
// mistake is reported, and the built-in languages keep working.
func (a *App) languageRegistry() lsp.Registry {
	if a.languageFile == "" {
		return lsp.NewRegistry(nil, a.settings.DisabledLanguages)
	}
	custom, err := lsp.LoadCustom(a.languageFile)
	if err != nil {
		slog.Warn("read language servers", "file", a.languageFile, "error", err)
		a.notice = toast{title: locales.LanguageConfigInvalid(), detail: err.Error()}
	}
	return lsp.NewRegistry(custom, a.settings.DisabledLanguages)
}

// showLanguages opens the page and finds out which servers are installed. The
// goroutine ends with that search, whose result is dropped if the page closed.
func (a *App) showLanguages() {
	languages := a.languageRegistry().Languages()
	a.languages = languagesPage{open: true, languages: languages}
	go func() {
		installed := make(map[string]bool, len(languages))
		for _, language := range languages {
			installed[language.ID] = lsp.Installed(language.Server)
		}
		a.post(func() {
			if a.languages.open {
				a.languages.installed, a.languages.checked = installed, true
			}
		})
	}()
}

// setLanguageEnabled turns a language server on or off in every window.
func (a *App) setLanguageEnabled(id string, enabled bool) {
	disabled := slices.DeleteFunc(slices.Clone(a.settings.DisabledLanguages), func(known string) bool { return known == id })
	if !enabled {
		disabled = append(disabled, id)
		slices.Sort(disabled)
	}
	next := a.settings
	next.DisabledLanguages = disabled
	a.updateSettings(next)
}

// openLanguageConfig opens the configuration file, creating it the first time.
func (a *App) openLanguageConfig(c *ui.Context) {
	if err := writeLanguageTemplate(a.languageFile); err != nil {
		slog.Warn("create language servers file", "file", a.languageFile, "error", err)
		a.notice = toast{title: locales.LanguageConfigWriteFailed(), detail: err.Error()}
		return
	}
	c.OpenURL((&url.URL{Scheme: "file", Path: filepath.ToSlash(a.languageFile)}).String())
}

// writeLanguageTemplate creates the configuration file unless it exists.
func writeLanguageTemplate(file string) error {
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return fmt.Errorf("create %q: %w", file, err)
	}
	handle, err := os.OpenFile(file, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if errors.Is(err, fs.ErrExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("create %q: %w", file, err)
	}
	_, writeErr := handle.WriteString(lsp.ConfigTemplate)
	if err := errors.Join(writeErr, handle.Close()); err != nil {
		return fmt.Errorf("write %q: %w", file, err)
	}
	return nil
}

func (a *App) languagesContent(c *ui.Context) {
	colors := a.look.palette
	ui.Column(c).FillWidth().Padding(6, 20, 16, 20).Children(func() {
		ui.Row(c).FillWidth().Height(48).Gap(6).AlignItems(ui.Center).Children(func() {
			back := ui.Box(c).Size(24, 24).Center().Radius(6).Label(locales.LanguagesBack())
			if back.Hovered() {
				back.Background(colors.hover)
			}
			if back.Clicked() {
				a.languages = languagesPage{}
			}
			back.Children(func() { ui.Icon(c, iconBack).FontSize(15).TextColor(colors.muted) })
			ui.Text(c, locales.LanguageServersTitle()).FontSize(16).FontWeight(600)
		})
		ui.Scroll(c).FillWidth().Height(languagesListHeight).Children(func() {
			for _, language := range a.languages.languages {
				c.Key(language.ID)
				a.languageRowView(c, language, !slices.Contains(a.settings.DisabledLanguages, language.ID))
			}
		})
		a.languagesFooterView(c)
	})
}

func (a *App) languageRowView(c *ui.Context, language lsp.Language, enabled bool) {
	colors := a.look.palette
	ui.Row(c).FillWidth().Height(languageRowHeight).Gap(10).Padding(0, 10, 0, 0).AlignItems(ui.Center).
		BorderWidth(0, 0, 1, 0).BorderColor(colors.border).Children(func() {
		ui.Column(c).Grow(1).Shrink(1).Gap(3).Children(func() {
			ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
				ui.Text(c, language.Name).MaxLines(1)
				if language.Custom {
					ui.Text(c, locales.LanguageCustom()).FontSize(smallFontSize - 1).TextColor(colors.accent)
				}
			})
			ui.Text(c, language.Server.Name+" · "+a.languageStatus(language)).
				FontSize(smallFontSize - 1).TextColor(colors.muted).MaxLines(1)
		})
		a.languageInstallView(c, language)
		on := enabled
		ui.Switch(c, &on).Label(language.Name)
		if on != enabled {
			a.setLanguageEnabled(language.ID, on)
		}
	})
}

func (a *App) languageStatus(language lsp.Language) string {
	switch {
	case !a.languages.checked:
		return locales.LanguageChecking()
	case a.languages.installed[language.ID]:
		return locales.LanguageInstalled()
	default:
		return locales.LanguageNotInstalled()
	}
}

// languageInstallView offers the install command of a server that is missing.
func (a *App) languageInstallView(c *ui.Context, language lsp.Language) {
	page := &a.languages
	if !page.checked || page.installed[language.ID] || language.Server.Install == "" {
		return
	}
	label := locales.CopyInstallCommand()
	if page.copied == language.ID {
		label = locales.CopiedToClipboard()
	}
	if ui.Button(c, label).Shrink(0).Clicked() {
		c.WriteClipboard(language.Server.Install)
		page.copied = language.ID
	}
}

func (a *App) languagesFooterView(c *ui.Context) {
	ui.Column(c).FillWidth().Gap(10).Padding(12, 0, 0, 0).Children(func() {
		ui.Text(c, locales.LanguageConfigHint()).FontSize(smallFontSize).TextColor(a.look.palette.muted)
		ui.Row(c).FillWidth().Gap(8).Children(func() {
			ui.Spacer(c)
			label := locales.CopyExample()
			if a.languages.copied == copiedExample {
				label = locales.CopiedToClipboard()
			}
			if ui.Button(c, label).Clicked() {
				c.WriteClipboard(lsp.ConfigExample)
				a.languages.copied = copiedExample
			}
			if a.languageFile != "" && ui.Button(c, locales.OpenLanguageConfig()).Clicked() {
				a.openLanguageConfig(c)
			}
		})
	})
}
