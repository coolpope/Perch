package app

import (
	"maps"
	"path/filepath"
	"slices"
	"testing"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/marukolibro/Perch/internal/settings"
)

func TestEditShortcutInSettings(t *testing.T) {
	a, tester := openWorkspace(t, t.TempDir())
	click := func(label string) {
		t.Helper()
		if err := tester.Click(label); err != nil {
			t.Fatal(err)
		}
		tester.Frame()
	}
	a.showSettings()
	tester.Frame()
	click("Shortcuts")
	click("Change shortcut for Open Folder")
	click("Shift")
	if !tester.HasText("Already used by “Go to Symbol in File”.") {
		t.Fatalf("missing conflict: %q", tester.Texts())
	}
	click("Cancel")
	if len(a.settings.Shortcuts) != 0 {
		t.Fatal("Cancel saved a draft")
	}

	click("Change shortcut for Go to File")
	click("Shift")
	click("Choose a key")
	tester.Type("F7")
	tester.Key(0, ui.KeyEnter)
	tester.Frame()
	click("Save")
	want := keyCombo{ui.Cmd | ui.Shift, "F7"}
	if got := goToFileKeys.binding(a.settings); got != want {
		t.Fatalf("saved shortcut = %v, want %v", got, want)
	}
	click("Close Settings")
	if !tester.HasText(want.label()) || slices.Contains(tester.Texts(), goToFileKeys.defaults.label()) {
		t.Fatalf("stale empty editor hints: %q", tester.Texts())
	}

	a.showSettings()
	tester.Frame()
	click("Shortcuts")
	click("Change shortcut for Go to File")
	click("Clear")
	click("Save")
	if value, ok := a.settings.Shortcuts[goToFileKeys.id]; !ok || value != "" {
		t.Fatalf("clear failed: %v", a.settings.Shortcuts)
	}
	click("Change shortcut for Go to File")
	click("Restore Default")
	click("Save")
	if len(a.settings.Shortcuts) != 0 {
		t.Fatalf("reset left overrides: %v", a.settings.Shortcuts)
	}
}

func TestShortcutOverridesSurviveRestart(t *testing.T) {
	d := NewDesktop()
	menu := d.menu()
	saved := map[string]string{
		goToFileKeys.id:       openFolderKeys.defaults.accelerator(),
		openFolderKeys.id:     goToFileKeys.defaults.accelerator(),
		closeTabKeys.id:       "",
		findInFileKeys.id:     "CmdOrCtrl+C",
		saveKeys.id:           "CmdOrCtrl+W",
		previousChangeKeys.id: "Unknown+F5",
		"unknownAction":       "F7",
	}
	file := filepath.Join(t.TempDir(), "settings.json")
	d.settings.Shortcuts = saved
	if err := settings.Save(file, d.settings); err != nil {
		t.Fatal(err)
	}
	loaded, err := settings.Load(file)
	if err != nil {
		t.Fatal(err)
	}
	got := cleanShortcutOverrides(loaded.Shortcuts, menu)
	want := map[string]string{goToFileKeys.id: saved[goToFileKeys.id], openFolderKeys.id: saved[openFolderKeys.id], closeTabKeys.id: "", saveKeys.id: saved[saveKeys.id]}
	if !maps.Equal(got, want) {
		t.Fatalf("loaded overrides = %v, want %v", got, want)
	}
	d.settings.Shortcuts = got
	fileItems := d.menu().Items()[1].Submenu
	for _, test := range []struct {
		action shortcutAction
		keys   string
	}{{openFolderKeys, want[openFolderKeys.id]}, {goToFileKeys, want[goToFileKeys.id]}, {closeTabKeys, ""}, {saveKeys, want[saveKeys.id]}} {
		var item *mygo.MenuItem
		for _, candidate := range fileItems {
			if candidate.Label == test.action.label() {
				item = candidate
				break
			}
		}
		if item == nil || item.Accelerator != test.keys {
			t.Errorf("%s menu binding = %v, want %q", test.action.id, item, test.keys)
		}
	}
	// Restoring Save's default also invalidates the downstream override of that key.
	if bad := cleanShortcutOverrides(map[string]string{goToFileKeys.id: "CmdOrCtrl+S", saveKeys.id: "CmdOrCtrl+C"}, menu); len(bad) != 0 {
		t.Fatalf("collision after fallback: %v", bad)
	}
}

func TestNumberedTabUsesChangedShortcut(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "a.go", "package a\n")
	writeFile(t, root, "b.go", "package b\n")
	a, tester := openWorkspace(t, root)
	clickFile(t, tester, "a.go")
	clickFile(t, tester, "b.go")
	a.saveShortcut(numberedTabKeys[0], keyCombo{0, "F7"})
	tester.Frame()
	tester.Key(ui.Cmd, ui.Key1)
	if a.active != 1 {
		t.Fatal("old shortcut still selected the first tab")
	}
	tester.Key(0, ui.KeyF7)
	if a.active != 0 {
		t.Fatal("new shortcut did not select the first tab")
	}
}

func TestChangedShortcutsReachEveryWindow(t *testing.T) {
	previousMenu := mygo.App.Menu()
	t.Cleanup(func() { mygo.App.SetMenu(previousMenu) })
	d := NewDesktop()
	d.settingsFile = filepath.Join(t.TempDir(), "settings.json")
	first, second := New(), New()
	for _, a := range []*App{first, second} {
		a.desktop = d
		a.window = &mygo.Window{}
		d.windows = append(d.windows, a)
	}
	first.saveShortcut(findInFileKeys, keyCombo{0, "F8"})
	if b := second.editorBindings; len(b) != 1 || b[0].Key != ui.KeyF || b[0].Mods != ui.Cmd {
		t.Fatalf("editor still handles its original Find shortcut: %v", b)
	}
	if got := findInFileKeys.binding(second.settings); got != (keyCombo{0, "F8"}) {
		t.Fatalf("second window has %v", got)
	}
	loaded, err := settings.Load(d.settingsFile)
	if err != nil || !maps.Equal(loaded.Shortcuts, second.settings.Shortcuts) {
		t.Fatalf("saved %v, error %v", loaded.Shortcuts, err)
	}
	// An unrelated edit from the other window must keep this override.
	second.saveShortcut(goToFileKeys, keyCombo{0, "F7"})
	first.saveShortcut(findInFileKeys, findInFileKeys.defaults)
	if len(second.editorBindings) != 0 || second.settings.Shortcuts[goToFileKeys.id] != "F7" {
		t.Fatalf("reset changed other bindings: %v, %v", second.editorBindings, second.settings.Shortcuts)
	}
}
