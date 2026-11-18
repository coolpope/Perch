package lsp

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestColumnsCountUTF16Units(t *testing.T) {
	line := "a中😀b"
	cases := []struct{ bytes, units int }{{0, 0}, {1, 1}, {4, 2}, {8, 4}, {9, 5}}
	for _, test := range cases {
		if got := UTF16Column(line, test.bytes); got != test.units {
			t.Errorf("UTF16Column(%d) = %d, want %d", test.bytes, got, test.units)
		}
		if got := ByteColumn(line, test.units); got != test.bytes {
			t.Errorf("ByteColumn(%d) = %d, want %d", test.units, got, test.bytes)
		}
	}
}

func TestLocationsOfEveryShape(t *testing.T) {
	cases := map[string]int{
		`null`: 0,
		`[]`:   0,
		`{"uri":"file:///a%20b.go","range":{"start":{"line":1,"character":2},"end":{"line":1,"character":3}}}`:                    1,
		`[{"targetUri":"file:///c.go","targetSelectionRange":{"start":{"line":4,"character":0},"end":{"line":4,"character":1}}}]`: 1,
	}
	for text, want := range cases {
		got, err := parseLocations(json.RawMessage(text))
		if err != nil || len(got) != want {
			t.Errorf("%s gave %+v, %v", text, got, err)
		}
	}
	spaced, err := parseLocations(json.RawMessage(`{"uri":"file:///a%20b.go"}`))
	if err != nil || spaced[0].Path != "/a b.go" {
		t.Errorf("escaped path gave %+v, %v", spaced, err)
	}
}

func TestMissingServerIsNamed(t *testing.T) {
	_, err := Start(context.Background(), Server{Name: "absent", Program: "perch-no-such-server"}, t.TempDir())
	if !errors.Is(err, ErrServerMissing) {
		t.Errorf("started with %v", err)
	}
}

// goProject writes a module whose main.go calls a function of another file.
func goProject(t *testing.T) (string, Document, Position) {
	t.Helper()
	if _, err := findProgram(goServer); err != nil {
		t.Skip("gopls is not installed")
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"go.mod":  "module example.test\n\ngo 1.22\n",
		"util.go": "package main\n\n// Helper is defined here.\nfunc Helper() int { return 1 }\n",
		"main.go": "package main\n\nfunc main() {\n\t_ = Helper()\n}\n",
	}
	for name, text := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	document := Document{Path: filepath.Join(root, "main.go"), Language: "go", Version: 1, Text: files["main.go"]}
	return root, document, Position{Line: 3, Character: strings.Index("\t_ = Helper()", "Helper") + 1}
}

func TestDefinitionThroughARealServer(t *testing.T) {
	root, document, position := goProject(t)
	pool := NewPool(root)
	defer pool.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	found, err := pool.Definition(ctx, goServer, document, position)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 || filepath.Base(found[0].Path) != "util.go" || found[0].Range.Start.Line != 3 {
		t.Fatalf("found %+v", found)
	}

	document.Version = 2
	document.Text = "package main\n\nfunc local() {}\n\nfunc main() {\n\tlocal()\n}\n"
	found, err = pool.Definition(ctx, goServer, document, Position{Line: 5, Character: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 || filepath.Base(found[0].Path) != "main.go" || found[0].Range.Start.Line != 2 {
		t.Errorf("after an unsaved edit found %+v", found)
	}

	pool.CloseIdle(time.Hour)
	if pool.Running() != 1 {
		t.Error("a server in use left")
	}
	pool.CloseIdle(0)
	if pool.Running() != 0 {
		t.Error("an idle server stayed")
	}
}
