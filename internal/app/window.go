package app

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

// Start shows the window, opening initialFolder when it is not empty.
func (a *App) Start(initialFolder string) {
	mygo.App.SetMenu(a.menu())
	a.after = func(delay time.Duration, action func()) {
		time.AfterFunc(delay, func() { a.post(action) })
	}
	a.window = mygo.NewWindow(mygo.WindowOptions{
		Title:     "Perch",
		Width:     1180,
		Height:    780,
		MinWidth:  640,
		MinHeight: 420,
		StateKey:  "main",
		// The sidebar and the tabs run under the window's controls.
		TitleBarStyle:        mygo.TitleBarHidden,
		TrafficLightPosition: &mygo.Point{X: 16, Y: 13},
		Content:              ui.View(a.view),
	})
	a.window.OnClose(a.handleWindowClose)
	if initialFolder != "" {
		a.openFolder(initialFolder)
	}
}

func (a *App) menu() *mygo.Menu {
	return mygo.NewMenu([]*mygo.MenuItem{
		{Role: mygo.RoleAppMenu},
		{Label: "File", Submenu: []*mygo.MenuItem{
			a.menuItem("Open Folder…", "CmdOrCtrl+O", a.chooseFolder),
			a.menuItem("Go to File…", "CmdOrCtrl+P", a.showQuickOpen),
			mygo.Separator(),
			a.menuItem("Save", "CmdOrCtrl+S", a.saveActive),
			a.menuItem("Close Tab", "CmdOrCtrl+W", a.closeActiveTab),
		}},
		{Role: mygo.RoleEditMenu},
		{Label: "Find", Submenu: []*mygo.MenuItem{
			a.menuItem("Find…", "CmdOrCtrl+F", func() { a.runFind(editor.Find) }),
			a.menuItem("Find and Replace…", "CmdOrCtrl+Alt+F", func() { a.runFind(editor.FindReplace) }),
			mygo.Separator(),
			a.menuItem("Find in Files…", "CmdOrCtrl+Shift+F", a.showFindInFiles),
		}},
		{Label: "View", Submenu: []*mygo.MenuItem{
			a.menuItem("Toggle Markdown Preview", "CmdOrCtrl+Shift+V", a.toggleMarkdownView),
		}},
		{Role: mygo.RoleWindowMenu},
	})
}

// menuItem runs action as an update of the window, so that it redraws.
func (a *App) menuItem(label string, accelerator string, action func()) *mygo.MenuItem {
	return &mygo.MenuItem{
		Label:       label,
		Accelerator: accelerator,
		Click: func(*mygo.MenuItem, *mygo.Window) {
			a.window.Update(action)
		},
	}
}

// runFind opens the find bar of the current file, which shows over its
// source.
func (a *App) runFind(command editor.Command) {
	d := a.activeDocument()
	if d == nil {
		return
	}
	d.markdownView = markdownSource
	d.editor.Run(command)
}

func (a *App) toggleMarkdownView() {
	d := a.activeDocument()
	if d == nil || !d.isMarkdown() {
		return
	}
	if d.markdownView == markdownPreview {
		d.markdownView = markdownSource
		return
	}
	d.markdownView = markdownPreview
}

// chooseFolder asks for a folder off the main thread, which the dialog needs
// to stay free.
func (a *App) chooseFolder() {
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
			Parent:    a.window,
			Title:     "Open Folder",
			Directory: true,
		})
		a.window.Update(func() {
			if err != nil {
				a.notice = fmt.Sprintf("Open folder dialog failed: %v", err)
				return
			}
			if len(paths) > 0 {
				a.openFolder(paths[0])
			}
		})
	}()
}

// openFolder switches the window to root and follows its changes. Unsaved
// edits keep the current folder open.
func (a *App) openFolder(root string) {
	if slices.ContainsFunc(a.documents, (*document).modified) {
		a.tell(toast{title: "Unsaved changes", detail: "Save or close modified files before opening another folder."})
		return
	}
	if err := a.loadFolder(root); err != nil {
		a.notice = err.Error()
		return
	}
	a.window.SetTitle(filepath.Base(a.root))
	if a.stopWatch != nil {
		a.stopWatch()
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.stopWatch = cancel
	if err := a.watch(ctx, a.root); err != nil {
		a.notice = err.Error()
	}
}

// handleWindowClose keeps the window open while unsaved edits need an answer.
func (a *App) handleWindowClose(event *mygo.CloseEvent) {
	if a.closeConfirmed || !slices.ContainsFunc(a.documents, (*document).modified) {
		return
	}
	event.PreventDefault()
	a.window.Update(func() { a.askCloseWindow = true })
}

func (a *App) confirmCloseWindow() {
	a.closeConfirmed = true
	a.window.Close()
}
