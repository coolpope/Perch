package search

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func write(t *testing.T, root string, name string, text string) {
	t.Helper()
	file := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func run(t *testing.T, root string, args ...string) {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", root}, args...)...)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, output)
	}
}

func TestListFilesFollowsGit(t *testing.T) {
	root := t.TempDir()
	run(t, root, "init", "--quiet")
	write(t, root, ".gitignore", "build/\n")
	write(t, root, "main.go", "package main\n")
	write(t, root, "gone.go", "package main\n")
	write(t, root, "new.go", "package main\n")
	write(t, root, "build/out.go", "package main\n")
	write(t, root, "node_modules/lib/index.js", "1\n")
	run(t, root, "add", "main.go", "gone.go", ".gitignore")
	if err := os.Remove(filepath.Join(root, "gone.go")); err != nil {
		t.Fatal(err)
	}

	listing, err := ListFiles(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{".gitignore", "main.go", "new.go"}
	if !slices.Equal(listing.Paths, want) {
		t.Errorf("listed %q, want %q", listing.Paths, want)
	}
}

func TestListFilesWalksOtherFolders(t *testing.T) {
	root := t.TempDir()
	write(t, root, "a/b.go", "1\n")
	write(t, root, "node_modules/c.js", "1\n")
	listing, err := ListFiles(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"a/b.go"}; !slices.Equal(listing.Paths, want) {
		t.Errorf("listed %q, want %q", listing.Paths, want)
	}
}

func TestRankPathsPrefersTheFileName(t *testing.T) {
	paths := []string{
		"internal/view/helpers/other.go",
		"docs/overview-notes.md",
		"internal/app/view.go",
		"view.go.bak/readme.md",
	}
	matches := RankPaths(paths, "view", 10)
	if len(matches) == 0 || matches[0].Path != "internal/app/view.go" {
		t.Fatalf("ranked %+v", matches)
	}
	if got := RankPaths(paths, "iav", 10); len(got) == 0 || got[0].Path != "internal/app/view.go" {
		t.Errorf("letters across folders ranked %+v", got)
	}
	if got := RankPaths(paths, "zzz", 10); len(got) != 0 {
		t.Errorf("unrelated query matched %+v", got)
	}
}

func TestParseTarget(t *testing.T) {
	cases := map[string]Target{
		"view.go:42": {Path: "view.go", Line: 42},
		":7":         {Path: "", Line: 7},
		"view.go":    {Path: "view.go"},
		"a:b":        {Path: "a:b"},
		"view.go:0":  {Path: "view.go:0"},
	}
	for query, want := range cases {
		if got := ParseTarget(query); got != want {
			t.Errorf("ParseTarget(%q) = %+v, want %+v", query, got, want)
		}
	}
}

func searchText(t *testing.T, root string, paths []string, query Query) ([]FileHits, Summary) {
	t.Helper()
	pattern, err := query.Compile()
	if err != nil {
		t.Fatal(err)
	}
	var found []FileHits
	summary, err := Text(context.Background(), root, paths, pattern, func(hits FileHits) {
		found = append(found, hits)
	})
	if err != nil {
		t.Fatal(err)
	}
	return found, summary
}

func TestTextReportsPlacesAndSkipsBinaryFiles(t *testing.T) {
	root := t.TempDir()
	write(t, root, "a.go", "package a\r\n\tfunc Hello() {}\r\nhello hello\r\n")
	write(t, root, "b.bin", "hello\x00world")
	found, summary := searchText(t, root, []string{"a.go", "b.bin"}, Query{Text: "hello"})

	if summary.Files != 1 || summary.Hits != 3 || len(found) != 1 {
		t.Fatalf("summary %+v, found %+v", summary, found)
	}
	first := found[0].Hits[0]
	if first.Line != 1 || first.Column != 6 || first.EndColumn != 11 {
		t.Errorf("first hit %+v", first)
	}
	if got := first.Preview[first.PreviewStart:first.PreviewEnd]; got != "Hello" || first.Preview != "func Hello() {}" {
		t.Errorf("preview %q marks %q", first.Preview, got)
	}
	last := found[0].Hits[2]
	if last.Line != 2 || last.Column != 6 || strings.Contains(last.Preview, "\r") {
		t.Errorf("last hit %+v", last)
	}
}

func TestTextHonorsOptionsAndTheLimit(t *testing.T) {
	root := t.TempDir()
	write(t, root, "a.txt", "Alpha alpha a.c abc\n")
	if _, summary := searchText(t, root, []string{"a.txt"}, Query{Text: "alpha", CaseSensitive: true}); summary.Hits != 1 {
		t.Errorf("case sensitive found %d", summary.Hits)
	}
	if _, summary := searchText(t, root, []string{"a.txt"}, Query{Text: "a.c"}); summary.Hits != 1 {
		t.Errorf("literal found %d", summary.Hits)
	}
	if _, summary := searchText(t, root, []string{"a.txt"}, Query{Text: "a.c", Regexp: true}); summary.Hits != 2 {
		t.Errorf("regexp found %d", summary.Hits)
	}
	if _, err := (Query{Text: "(", Regexp: true}).Compile(); err == nil {
		t.Error("an invalid regular expression compiled")
	}

	write(t, root, "many.txt", strings.Repeat("x\n", MaxHits+5))
	_, summary := searchText(t, root, []string{"many.txt"}, Query{Text: "x"})
	if summary.Hits != MaxHits || !summary.Truncated {
		t.Errorf("summary %+v at the limit", summary)
	}
}
