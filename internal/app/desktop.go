package app

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"slices"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui/editor"

	"perch/internal/settings"
	"perch/locales"
)

const (
	windowWidth   int = 1180
	windowHeight  int = 780
	cascadeOffset int = 28
	// stateKeyBytes is how much of a folder's hash names its window state.
	stateKeyBytes int = 12

	settingsFileName = "settings.json"
	languageFileName = "language-servers.json"
	sessionsDirName  = "sessions"
)

// Desktop owns Perch's windows, one project each, and the menu they share. Its
// methods run on the main thread.
type Desktop struct {
	windows      []*App
	settings     settings.Settings
	settingsFile string
	languageFile string
	recentFile   string
	recent       []string
	sessionDir   string
}

// NewDesktop returns a desktop with no windows.
func NewDesktop() *Desktop {
	return &Desktop{settings: settings.Default()}
}

// windowPlacement is where a new window opens. The state remembered under
// stateKey wins over x and y.
type windowPlacement struct {
	x        int
	y        int
	stateKey string
}

// Start opens the first window, with the folder named by args, the command
// line after the program's name, and takes the folders of later launches.
func (d *Desktop) Start(args []string) {
	loadErr := d.loadSettings()
	mygo.App.SetMenu(d.menu())
	mygo.App.OnSecondInstance(d.openLaunch)
	mygo.App.OnLocaleChanged(func(string) { mygo.App.SetMenu(d.menu()) })
	d.startAgentLink()
	d.openWindow(folderArgument(args, ""))
	if loadErr != nil {
		d.reportSettingsFailure(locales.SettingsReadFailed(), loadErr)
	}
}

// loadSettings reads the saved settings and applies their appearance. Perch
// runs on the defaults when they cannot be read.
func (d *Desktop) loadSettings() error {
	dir, err := mygo.App.Path(mygo.PathUserData)
	if err != nil {
		return fmt.Errorf("find the settings folder: %w", err)
	}
	d.settingsFile = filepath.Join(dir, settingsFileName)
	d.languageFile = filepath.Join(dir, languageFileName)
	d.recentFile = filepath.Join(dir, recentFileName)
	d.recent = loadRecent(d.recentFile)
	d.sessionDir = filepath.Join(dir, sessionsDirName)
	loaded, err := settings.Load(d.settingsFile)
	d.settings = loaded
	applyAppearance(loaded.Appearance)
	return err
}

func applyAppearance(appearance settings.Appearance) {
	switch appearance {
	case settings.AppearanceLight:
		mygo.Theme.SetSource(mygo.ThemeLight)
	case settings.AppearanceDark:
		mygo.Theme.SetSource(mygo.ThemeDark)
	default:
		mygo.Theme.SetSource(mygo.ThemeSystem)
	}
}

// settingsChanged applies new settings to every window and saves them.
func (d *Desktop) settingsChanged(next settings.Settings) {
	appearanceChanged := next.Appearance != d.settings.Appearance
	d.settings = next
	for _, a := range d.windows {
		a.settings = next
		a.window.Update(func() {})
	}
	if appearanceChanged {
		applyAppearance(next.Appearance)
	}
	if d.settingsFile == "" {
		d.reportSettingsFailure(locales.SettingsSaveFailed(), errors.New("the settings folder is unknown"))
		return
	}
	if err := settings.Save(d.settingsFile, next); err != nil {
		d.reportSettingsFailure(locales.SettingsSaveFailed(), err)
	}
}

func (d *Desktop) reportSettingsFailure(title string, err error) {
	if a := d.currentApp(nil); a != nil {
		a.tell(toast{title: title, detail: err.Error()})
	}
}

// folderArgument returns the folder that args name, relative to workingDir, or
// nothing.
func folderArgument(args []string, workingDir string) string {
	if len(args) == 0 {
		return ""
	}
	if filepath.IsAbs(args[0]) || workingDir == "" {
		return args[0]
	}
	return filepath.Join(workingDir, args[0])
}

// openLaunch answers another launch of Perch: its folder opens, and a launch
// without one brings the last window forward.
func (d *Desktop) openLaunch(args []string, workingDir string) {
	folder := folderArgument(args, workingDir)
	if folder != "" {
		d.openFolder(nil, folder)
		return
	}
	if a := d.currentApp(nil); a != nil {
		a.window.Focus()
	}
}

// openWindow opens a window, with the project at root when root is not empty.
func (d *Desktop) openWindow(root string) {
	a := New()
	a.desktop = d
	a.settings = d.settings
	a.languageFile = d.languageFile
	a.session.dir = d.sessionDir
	a.chosenLanguage = mygo.App.ChosenLocale
	a.setLanguage = mygo.App.SetLocale
	a.show(root, d.placementFor(root))
	a.window.OnClosed(func() { d.windowClosed(a) })
	d.windows = append(d.windows, a)
}

// placementFor sets a new window off from the current one, so that neither
// hides the other, and has each project remember its own window.
func (d *Desktop) placementFor(root string) windowPlacement {
	placement := windowPlacement{stateKey: stateKeyFor(root)}
	if a := d.currentApp(nil); a != nil {
		x, y := a.window.Position()
		placement.x, placement.y = x+cascadeOffset, y+cascadeOffset
	}
	return placement
}

// stateKeyFor names the remembered window of a project without keeping its
// path in the state file.
func stateKeyFor(root string) string {
	if root == "" {
		return "welcome"
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		absolute = root
	}
	sum := sha256.Sum256([]byte(absolute))
	return "project-" + hex.EncodeToString(sum[:stateKeyBytes])
}

func (d *Desktop) windowClosed(a *App) {
	a.release()
	d.windows = slices.DeleteFunc(slices.Clone(d.windows), func(other *App) bool { return other == a })
}

// folderDestination says which window takes a folder.
type folderDestination struct {
	// existing already shows the folder, and comes forward.
	existing *App
	// empty has no project yet, and takes the folder.
	empty *App
}

// chooseDestination finds the window for a folder. When neither field is set,
// the folder gets a new window.
func chooseDestination(windows []*App, from *App, root string) folderDestination {
	index := slices.IndexFunc(windows, func(a *App) bool { return a.root == root })
	if index >= 0 {
		return folderDestination{existing: windows[index]}
	}
	if from != nil && from.root == "" {
		return folderDestination{empty: from}
	}
	return folderDestination{}
}

// openFolder shows a folder: in the window that has it, in the empty window it
// was asked from, or in a new window.
func (d *Desktop) openFolder(from *App, folder string) {
	root, err := filepath.Abs(folder)
	if err != nil {
		d.reportOpenFailure(from, err)
		return
	}
	destination := chooseDestination(d.windows, from, root)
	switch {
	case destination.existing != nil:
		d.recordRecent(root)
		destination.existing.window.Focus()
	case destination.empty != nil:
		destination.empty.openFolder(root)
	default:
		d.openWindow(root)
	}
}

func (d *Desktop) reportOpenFailure(from *App, err error) {
	if a := d.currentApp(from); a != nil {
		a.tell(toast{title: locales.FolderOpenFailed(), detail: err.Error()})
	}
}

// newWindow brings forward an empty window, or opens one.
func (d *Desktop) newWindow() {
	index := slices.IndexFunc(d.windows, func(a *App) bool { return a.root == "" })
	if index >= 0 {
		d.windows[index].window.Focus()
		return
	}
	d.openWindow("")
}

// currentApp returns the app of a window, of the focused window, or of the
// newest one.
func (d *Desktop) currentApp(preferred *App) *App {
	if preferred != nil {
		return preferred
	}
	focused := mygo.FocusedWindow()
	index := slices.IndexFunc(d.windows, func(a *App) bool { return a.window == focused })
	if index >= 0 {
		return d.windows[index]
	}
	if len(d.windows) > 0 {
		return d.windows[len(d.windows)-1]
	}
	return nil
}

func (d *Desktop) appOf(window *mygo.Window) *App {
	index := slices.IndexFunc(d.windows, func(a *App) bool { return a.window == window })
	if index >= 0 {
		return d.windows[index]
	}
	return d.currentApp(nil)
}

func (d *Desktop) menu() *mygo.Menu {
	return mygo.NewMenu([]*mygo.MenuItem{
		{Label: "Perch", Submenu: []*mygo.MenuItem{
			{Role: mygo.RoleAbout},
			mygo.Separator(),
			d.menuItem(locales.MenuSettings(), "CmdOrCtrl+,", (*App).showSettings),
			mygo.Separator(),
			{Role: mygo.RoleServices},
			mygo.Separator(),
			{Role: mygo.RoleHide},
			{Role: mygo.RoleHideOthers},
			{Role: mygo.RoleUnhide},
			mygo.Separator(),
			{Role: mygo.RoleQuit},
		}},
		{Label: locales.MenuFile(), Submenu: []*mygo.MenuItem{
			{Label: locales.MenuNewWindow(), Accelerator: "CmdOrCtrl+Shift+N", Click: func(*mygo.MenuItem, *mygo.Window) { d.newWindow() }},
			d.menuItem(locales.MenuOpenFolder(), "CmdOrCtrl+O", (*App).chooseFolder),
			{Label: locales.MenuOpenRecent(), Submenu: d.recentMenuItems()},
			d.menuItem(locales.MenuGoToFile(), "CmdOrCtrl+P", (*App).showQuickOpen),
			mygo.Separator(),
			d.menuItem(locales.Save(), "CmdOrCtrl+S", (*App).saveActive),
			d.menuItem(locales.CloseTab(), "CmdOrCtrl+W", (*App).closeActiveTab),
			d.menuItem(locales.MenuCloseWindow(), "CmdOrCtrl+Shift+W", func(a *App) { a.window.Close() }),
		}},
		{Role: mygo.RoleEditMenu},
		{Label: locales.MenuFind(), Submenu: []*mygo.MenuItem{
			d.menuItem(locales.MenuFindInFile(), "CmdOrCtrl+F", func(a *App) { a.runFind(editor.Find) }),
			d.menuItem(locales.MenuFindReplace(), "CmdOrCtrl+Alt+F", func(a *App) { a.runFind(editor.FindReplace) }),
			mygo.Separator(),
			d.menuItem(locales.MenuFindInFiles(), "CmdOrCtrl+Shift+F", (*App).showFindInFiles),
		}},
		{Label: locales.MenuGo(), Submenu: []*mygo.MenuItem{
			d.menuItem(locales.MenuGoToSymbol(), "CmdOrCtrl+Shift+O", (*App).showOutline),
			d.menuItem(locales.MenuGoToProjectSymbol(), "CmdOrCtrl+T", (*App).showProjectSymbols),
			d.menuItem(locales.MenuGoToDefinition(), "F12", (*App).definitionAtCaret),
			mygo.Separator(),
			d.menuItem(locales.MenuGoBack(), "Ctrl+-", (*App).goBack),
			d.menuItem(locales.MenuGoForward(), "Ctrl+Shift+-", (*App).goForward),
			mygo.Separator(),
			d.menuItem(locales.MenuNextStop(), "F8", func(a *App) { a.moveTour(1) }),
			d.menuItem(locales.MenuPreviousStop(), "Shift+F8", func(a *App) { a.moveTour(-1) }),
			mygo.Separator(),
			d.menuItem(locales.MenuNextTab(), "CmdOrCtrl+Shift+]", (*App).nextTab),
			d.menuItem(locales.MenuPreviousTab(), "CmdOrCtrl+Shift+[", (*App).previousTab),
		}},
		{Label: locales.MenuView(), Submenu: []*mygo.MenuItem{
			d.menuItem(locales.MenuToggleMarkdown(), "CmdOrCtrl+Shift+V", (*App).toggleMarkdownView),
			mygo.Separator(),
			d.menuItem(locales.MenuShowChanges(), "CmdOrCtrl+Shift+R", (*App).showChanges),
			d.menuItem(locales.MenuMarkReviewed(), "CmdOrCtrl+Enter", (*App).markSelectedReviewed),
			mygo.Separator(),
			d.menuItem(locales.MenuNextChange(), "Alt+F5", func(a *App) { a.moveToChange(1) }),
			d.menuItem(locales.MenuPreviousChange(), "Alt+Shift+F5", func(a *App) { a.moveToChange(-1) }),
		}},
		{Role: mygo.RoleWindowMenu},
	})
}

// menuItem runs action on the chosen window's app, as an update of that window
// so that it redraws.
func (d *Desktop) menuItem(label string, accelerator string, action func(*App)) *mygo.MenuItem {
	return &mygo.MenuItem{
		Label:       label,
		Accelerator: accelerator,
		Click: func(_ *mygo.MenuItem, window *mygo.Window) {
			a := d.appOf(window)
			if a != nil {
				a.window.Update(func() { action(a) })
			}
		},
	}
}
