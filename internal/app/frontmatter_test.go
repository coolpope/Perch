package app

import (
	"slices"
	"testing"
)

func TestFrontMatterSplitsFromTheText(t *testing.T) {
	cases := []struct {
		name   string
		text   string
		raw    string
		body   string
		simple bool
	}{
		{"skill", "---\nname: perch\ndescription: \"Reads code\"\n---\n\n# Title\n", "name: perch\ndescription: \"Reads code\"", "\n# Title\n", true},
		{"crlf and dots", "---\r\ntitle: Post\r\n...\r\nBody", "title: Post", "Body", true},
		{"nested", "---\ntags:\n  - go\n---\nBody", "tags:\n  - go", "Body", false},
		{"no closing fence", "---\ntitle: Post\n\nBody", "", "---\ntitle: Post\n\nBody", false},
		{"a rule, not front matter", "Intro\n\n---\n\nMore", "", "Intro\n\n---\n\nMore", false},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			front := splitFrontMatter(test.text)
			if front.raw != test.raw || test.text[front.size:] != test.body {
				t.Errorf("raw %q body %q", front.raw, test.text[front.size:])
			}
			if _, simple := frontFields(front.raw); simple != test.simple {
				t.Errorf("simple %v", simple)
			}
		})
	}
}

func TestPreviewShowsFrontMatterAsACard(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "SKILL.md", "---\nname: perch\ndescription: Work through Perch.\n---\n\n# Working with Perch\n\n## Show one place\n")
	a, tester := openWorkspace(t, root)
	a.openFile("SKILL.md")
	tester.Frame()
	for _, want := range []string{"name", "perch", "description", "Work through Perch.", "Working with Perch"} {
		if !slices.Contains(tester.Texts(), want) {
			t.Fatalf("no %q; texts %q", want, tester.Texts())
		}
	}

	a.showOutline()
	tester.Frame()
	if len(a.outline.symbols) != 2 || a.outline.symbols[0].name != "Working with Perch" || a.outline.symbols[0].span.Start.Line != 5 {
		t.Errorf("headings %+v", a.outline.symbols)
	}
}
