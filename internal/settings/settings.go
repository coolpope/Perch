// Package settings holds the preferences Perch keeps between launches.
package settings

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
)

// Limits of the code font's size.
const (
	MinFontSize     int = 11
	MaxFontSize     int = 22
	DefaultFontSize int = 13
)

// Appearance is the look of the windows.
type Appearance string

// The appearances to choose from.
const (
	AppearanceSystem Appearance = "system"
	AppearanceLight  Appearance = "light"
	AppearanceDark   Appearance = "dark"
)

// Theme is the color theme of the windows.
type Theme string

// The themes to choose from.
const (
	ThemeGrokBot Theme = "grokbot"
	ThemePaper   Theme = "paper"
	ThemePerch   Theme = "perch"
)

// MarkdownView is how Markdown files open.
type MarkdownView string

// The views a Markdown file can open in.
const (
	MarkdownPreview MarkdownView = "preview"
	MarkdownSource  MarkdownView = "source"
)

// DiffLayout is how a change shows its two versions.
type DiffLayout string

// The layouts of a change.
const (
	DiffUnified DiffLayout = "unified"
	DiffSplit   DiffLayout = "split"
)

// TabSizes are the indentation widths to choose from. 0 follows each language.
var TabSizes = []int{0, 2, 4, 8}

// Settings are the preferences of every window.
type Settings struct {
	Appearance   Appearance   `json:"appearance"`
	Theme        Theme        `json:"theme"`
	FontSize     int          `json:"fontSize"`
	TabSize      int          `json:"tabSize"`
	WordWrap     bool         `json:"wordWrap"`
	MarkdownView MarkdownView `json:"markdownView"`
	DiffLayout   DiffLayout   `json:"diffLayout"`
	// DisabledLanguages are the IDs of the language servers turned off.
	DisabledLanguages []string `json:"disabledLanguages,omitempty"`
}

// Default returns the settings of a first launch.
func Default() Settings {
	return Settings{
		Appearance:   AppearanceSystem,
		Theme:        ThemeGrokBot,
		FontSize:     DefaultFontSize,
		MarkdownView: MarkdownPreview,
		DiffLayout:   DiffUnified,
	}
}

// valid replaces each value Perch does not offer with its default.
func valid(s Settings) Settings {
	defaults := Default()
	if !slices.Contains([]Appearance{AppearanceSystem, AppearanceLight, AppearanceDark}, s.Appearance) {
		s.Appearance = defaults.Appearance
	}
	if !slices.Contains([]Theme{ThemeGrokBot, ThemePaper, ThemePerch}, s.Theme) {
		s.Theme = defaults.Theme
	}
	if s.FontSize < MinFontSize || s.FontSize > MaxFontSize {
		s.FontSize = defaults.FontSize
	}
	if !slices.Contains(TabSizes, s.TabSize) {
		s.TabSize = defaults.TabSize
	}
	if s.MarkdownView != MarkdownPreview && s.MarkdownView != MarkdownSource {
		s.MarkdownView = defaults.MarkdownView
	}
	if s.DiffLayout != DiffUnified && s.DiffLayout != DiffSplit {
		s.DiffLayout = defaults.DiffLayout
	}
	return s
}

// Load reads the settings in file. A file that does not exist yet gives the
// defaults. A file that cannot be read gives the defaults and the error.
func Load(file string) (Settings, error) {
	data, err := os.ReadFile(file)
	if errors.Is(err, fs.ErrNotExist) {
		return Default(), nil
	}
	if err != nil {
		return Default(), fmt.Errorf("read settings %q: %w", file, err)
	}
	loaded := Default()
	if err := json.Unmarshal(data, &loaded); err != nil {
		return Default(), fmt.Errorf("read settings %q: %w", file, err)
	}
	return valid(loaded), nil
}

// Save writes the settings to file through a temporary file, so that a failed
// write keeps the last settings.
func Save(file string, s Settings) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("encode settings: %w", err)
	}
	dir := filepath.Dir(file)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("save settings %q: %w", file, err)
	}
	temp, err := os.CreateTemp(dir, ".settings-*.tmp")
	if err != nil {
		return fmt.Errorf("save settings %q: %w", file, err)
	}
	_, writeErr := temp.Write(append(data, '\n'))
	if err := errors.Join(writeErr, temp.Close()); err != nil {
		return errors.Join(fmt.Errorf("save settings %q: %w", file, err), os.Remove(temp.Name()))
	}
	if err := os.Rename(temp.Name(), file); err != nil {
		return errors.Join(fmt.Errorf("save settings %q: %w", file, err), os.Remove(temp.Name()))
	}
	return nil
}
