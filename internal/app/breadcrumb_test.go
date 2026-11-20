package app

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"

	"github.com/marukolibro/Perch/locales"
)

// TestBreadcrumbGoFollowsRanges exercises passive startup, nested ranges, and
// UTF-16 navigation through a real Go language server.
func TestBreadcrumbGoFollowsRanges(t *testing.T) {
	if _, err := exec.LookPath("gopls"); err != nil {
		t.Skip("gopls is not installed")
	}
	root := t.TempDir()
	line := "type Store struct { /* 😀 */ Field string }"
	writeFile(t, root, "go.mod", "module example.test\n\ngo 1.22\n")
	writeFile(t, root, "store.go", "package example\n\n"+line+"\n")
	writeFile(t, root, "other.go", "package example\n\nfunc Other() {}\n")
	a, tester, posted := openWithPosts(t, root)
	t.Cleanup(a.release)

	a.openFile("store.go")
	d := a.activeDocument()
	d.editor.GoTo(editor.Pos{Line: 2, Col: strings.Index(line, "string")})
	tester.Frame()
	tester.Advance(breadcrumbDelay)
	if pool := a.definition.pool; pool != nil && pool.Running() != 0 {
		t.Fatal("opening the file started a language server")
	}
	if _, found := tester.AccessByRole(ui.RoleButton, "Store"); found || a.notice.title != "" || a.brief.title != "" {
		t.Fatalf("a file without a running server should show only its path: %q", tester.Texts())
	}

	a.showOutline()
	tester.Frame()
	waitForOutline(t, a, tester, posted)
	a.closeOutline()
	tester.Frame()
	tester.Frame()
	followPosts(t, posted, tester, func() bool {
		_, found := tester.AccessByRole(ui.RoleButton, "Field")
		return found
	})

	outer, outerOK := tester.AccessByRole(ui.RoleButton, "Store")
	inner, innerOK := tester.AccessByRole(ui.RoleButton, "Field")
	if !outerOK || !innerOK || outer.Bounds.X >= inner.Bounds.X {
		t.Fatalf("want Store before Field: %q", tester.Texts())
	}
	if err := tester.Click("Field"); err != nil {
		t.Fatal(err)
	}
	want := editor.Pos{Line: 2, Col: strings.Index(line, "Field")}
	if got := d.editor.CursorPos(); got != want {
		t.Errorf("caret at %+v, want %+v", got, want)
	}
	d.editor.GoTo(editor.Pos{Line: 2, Col: strings.Index(line, "string") + len("string")})
	tester.Frame()
	if _, found := tester.AccessByRole(ui.RoleButton, "Field"); found {
		t.Error("the field's range end still shows its crumb")
	}
	if _, found := tester.AccessByRole(ui.RoleButton, "Store"); !found {
		t.Error("moving the caret dropped the containing type")
	}

	d.editor.GoTo(want)
	tester.Frame()
	tester.Type("ExtendedIdentifier")
	tester.Frame()
	if _, found := tester.AccessByRole(ui.RoleButton, "Field"); !found {
		t.Error("typing past the cached range made the crumb disappear")
	}
	tester.Advance(breadcrumbDelay)
	followPosts(t, posted, tester, func() bool {
		_, found := tester.AccessByRole(ui.RoleButton, "ExtendedIdentifierField")
		return found
	})

	a.openFile("other.go")
	tester.Frame()
	if _, found := tester.AccessByRole(ui.RoleButton, "Store"); found {
		t.Error("the previous tab still shows its symbol")
	}
	if _, found := tester.AccessByRole(ui.RoleButton, "ExtendedIdentifierField"); found {
		t.Error("the previous tab still shows its edited symbol")
	}
}

// TestBreadcrumbMarkdownFollowsEdits exercises section boundaries and a
// refresh after typing in the editor.
func TestBreadcrumbMarkdownFollowsEdits(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "guide.md", "---\ntitle: Example\n---\n\n# Guide\n\n## Install\n\nStart here.\n\n# Appendix\nLast line")
	a, tester := openWorkspace(t, root)
	a.openFile("guide.md")
	d := a.activeDocument()
	d.editor.GoTo(editor.Pos{Line: 8})
	tester.Frame()
	tester.Advance(breadcrumbDelay)
	outer, outerOK := tester.AccessByRole(ui.RoleButton, "Guide")
	inner, innerOK := tester.AccessByRole(ui.RoleButton, "Install")
	if !outerOK || !innerOK || outer.Bounds.X >= inner.Bounds.X {
		t.Fatalf("want Guide before Install: %q", tester.Texts())
	}
	if !d.showsPreview() {
		t.Fatal("showing the Markdown breadcrumb switched out of preview")
	}
	if err := tester.Click("Guide"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if got := d.editor.CursorPos(); got != (editor.Pos{Line: 4}) || d.showsPreview() || !d.editor.Focused() {
		t.Fatalf("heading click did not focus its source at line 5: %+v", got)
	}

	d.editor.GoTo(editor.Pos{Line: 11, Col: len("Last line")})
	tester.Frame()
	if _, found := tester.AccessByRole(ui.RoleButton, "Appendix"); !found {
		t.Error("the final section did not include the end of the file")
	}
	if _, found := tester.AccessByRole(ui.RoleButton, "Guide"); found {
		t.Error("the previous top-level section still shows its crumb")
	}
	d.editor.GoTo(editor.Pos{Line: 10, Col: len("# Appendix")})
	tester.Type(" revised")
	tester.Frame()
	tester.Advance(breadcrumbDelay / 2)
	tester.Type(" today")
	tester.Frame()
	tester.Advance(breadcrumbDelay / 2)
	if _, found := tester.AccessByRole(ui.RoleButton, "Appendix revised today"); found {
		t.Error("the first edit's deadline refreshed before the second edit settled")
	}
	tester.Advance(breadcrumbDelay / 2)
	if _, found := tester.AccessByRole(ui.RoleButton, "Appendix revised today"); !found {
		t.Errorf("the edited heading did not refresh: %q", tester.Texts())
	}
}

// TestBreadcrumbKeepsFileAndControlsVisible checks that long symbol names
// shrink before the filename or controls at a narrow width.
func TestBreadcrumbKeepsFileAndControlsVisible(t *testing.T) {
	const (
		width  = 350
		height = 60
		outer  = "Installing the tooling for the local preview and the development workspace"
		inner  = "Verifying the configuration and troubleshooting connection errors"
	)
	root := t.TempDir()
	writeFile(t, root, "docs/guide.md", "# "+outer+"\n\n## "+inner+"\n\nBody.\n")
	a := New()
	if err := a.loadFolder(root); err != nil {
		t.Fatal(err)
	}
	a.openFile("docs/guide.md")
	d := a.activeDocument()
	d.editor.GoTo(editor.Pos{Line: 4})
	tester := ui.NewTester(func(c *ui.Context) {
		a.look = a.lookFor(c.Theme())
		c.SetTheme(a.look.theme)
		a.syncBreadcrumb(c, d)
		a.pathBarView(c, d)
	}, width, height)
	fileBefore, fileOK := tester.Find("guide.md")
	editBefore, editOK := tester.Find(locales.MarkdownEdit())
	if !fileOK || !editOK || editBefore.X+editBefore.W > width {
		t.Fatalf("file or edit control is missing at width %d: %q", width, tester.Texts())
	}
	tester.Advance(breadcrumbDelay)
	if fileAfter, ok := tester.Find("guide.md"); !ok || fileBefore != fileAfter {
		t.Errorf("adding symbols moved or shrank the file: %v to %v", fileBefore, fileAfter)
	}
	if editAfter, ok := tester.Find(locales.MarkdownEdit()); !ok || editBefore != editAfter {
		t.Errorf("adding symbols moved or shrank the edit control: %v to %v", editBefore, editAfter)
	}
	narrow, narrowOK := tester.AccessByRole(ui.RoleButton, inner)
	tester.SetSize(900, height)
	wide, wideOK := tester.AccessByRole(ui.RoleButton, inner)
	if !narrowOK || !wideOK || narrow.Bounds.W >= wide.Bounds.W {
		t.Errorf("symbol did not shrink: narrow %v, wide %v", narrow.Bounds, wide.Bounds)
	}
}
