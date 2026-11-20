package fileicons

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestEveryIconParses(t *testing.T) {
	data, err := assets.ReadFile("assets/rules.json")
	if err != nil {
		t.Fatal(err)
	}
	var rules []rule
	if err := json.Unmarshal(data, &rules); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, r := range rules {
		if seen[r.Icon] {
			continue
		}
		seen[r.Icon] = true
		svg, err := SVG(Icon{Name: r.Icon})
		if err != nil {
			t.Error(err)
			continue
		}
		if _, err := ui.ParseSVG(svg); err != nil {
			t.Errorf("icon %s: %v", r.Icon, err)
		}
	}
}

func TestEveryFolderIconParses(t *testing.T) {
	data, err := assets.ReadFile("assets/folders.json")
	if err != nil {
		t.Fatal(err)
	}
	var rules []folderRule
	if err := json.Unmarshal(data, &rules); err != nil {
		t.Fatal(err)
	}
	icons := []string{GenericFolder}
	for _, r := range rules {
		icons = append(icons, r.Icon)
	}
	for _, icon := range icons {
		for _, open := range []bool{false, true} {
			svg, err := FolderSVG(icon, open)
			if err == nil {
				_, err = ui.ParseSVG(svg)
			}
			if err != nil {
				t.Errorf("folder icon %s open %v: %v", icon, open, err)
			}
		}
	}
	for name, want := range map[string]string{"docs": "docs", "node_modules": "node", "src": "src", "tests": "tests", "whatever": GenericFolder} {
		if got, err := ForFolder(name); err != nil || got != want {
			t.Errorf("folder %s has icon %q, %v; want %s", name, got, err, want)
		}
	}
}

func TestCommonFilesHaveTheirIcons(t *testing.T) {
	cases := map[string]string{
		"main.go":                  "go",
		"internal/app/app_test.go": "go",
		"go.mod":                   "goconfig",
		"src/App.tsx":              "jsx",
		"server.py":                "python",
		"lib.rs":                   "rust",
		"README.md":                "readme",
		"docs/guide.md":            "markdown",
		"package.json":             "npm",
		"Dockerfile":               "docker",
		".gitignore":               "gitignore",
	}
	cases[filepath.Join(t.TempDir(), "Dockerfile")] = "docker"
	for relPath, want := range cases {
		icon, found, err := For(relPath)
		if err != nil || !found || icon.Name != want {
			t.Errorf("%s has icon %+v, %v, %v; want %s", relPath, icon, found, err, want)
		}
	}
	if _, found, err := For("notes.unknown-kind"); found || err != nil {
		t.Errorf("an unknown file has an icon: %v, %v", found, err)
	}
}
