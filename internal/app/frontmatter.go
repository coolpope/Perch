package app

import (
	"strings"

	"github.com/egoist/mygo/ui"
)

const frontMatterKeyWidth float32 = 120

// frontMatter is the YAML block that opens a Markdown file, as skills, posts
// and docs sites put their metadata there.
type frontMatter struct {
	// raw is the YAML between its fences.
	raw string
	// size is how many bytes of the file it takes, with its fences: 0 for a
	// file without one.
	size int
}

// frontField is one top-level "key: value" line of front matter.
type frontField struct {
	key   string
	value string
}

// splitFrontMatter finds the front matter that opens text: a line of "---",
// the YAML, and a line of "---" or "...".
func splitFrontMatter(text string) frontMatter {
	first, _, found := strings.Cut(text, "\n")
	if !found || strings.TrimRight(first, "\r") != "---" {
		return frontMatter{}
	}
	offset := len(first) + 1
	for offset <= len(text) {
		line, _, more := strings.Cut(text[offset:], "\n")
		fence := strings.TrimRight(line, "\r")
		if fence == "---" || fence == "..." {
			size := offset + len(line)
			if more {
				size++
			}
			raw := strings.TrimRight(text[len(first)+1:offset], "\r\n")
			return frontMatter{raw: raw, size: size}
		}
		if !more {
			break
		}
		offset += len(line) + 1
	}
	return frontMatter{}
}

// frontFields reads front matter made only of top-level "key: value" lines,
// and reports false for anything richer, which then shows as it is written.
func frontFields(raw string) ([]frontField, bool) {
	var fields []frontField
	for line := range strings.SplitSeq(raw, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		key, value, found := strings.Cut(line, ":")
		if !found || key == "" || strings.ContainsAny(key[:1], " \t-#") || strings.ContainsAny(key, " \t") {
			return nil, false
		}
		value = strings.TrimSpace(value)
		if value == "|" || value == ">" || strings.HasPrefix(value, "|-") || strings.HasPrefix(value, ">-") {
			return nil, false
		}
		fields = append(fields, frontField{key: key, value: unquoted(value)})
	}
	return fields, len(fields) > 0
}

// unquoted drops the quotes around a YAML scalar.
func unquoted(value string) string {
	if len(value) >= 2 && (value[0] == '"' || value[0] == '\'') && value[len(value)-1] == value[0] {
		return value[1 : len(value)-1]
	}
	return value
}

// markdownPreviewView shows a document rendered, its front matter as a card
// above the rest.
func (a *App) markdownPreviewView(c *ui.Context, d *document, render func(body string) ui.Element) {
	body := d.markdownText()
	ui.Column(c).MaxWidth(markdownMaxWidth).FillWidth().Gap(18).Children(func() {
		if front := d.previewFront; front.size > 0 {
			a.frontMatterView(c, front)
		}
		render(body).FillWidth()
	})
}

// frontMatterView shows metadata as a quiet card of names and values, or as
// it is written when it is more than that.
func (a *App) frontMatterView(c *ui.Context, front frontMatter) {
	colors := a.look.palette
	fields, simple := frontFields(front.raw)
	ui.Column(c.Key("front-matter")).FillWidth().Radius(radiusControl).Border(1, colors.border).
		Background(colors.sidebar.Alpha(0.6)).Clip().Children(func() {
		if !simple {
			ui.Text(c, front.raw).Font("monospace").FontSize(smallFontSize).TextColor(colors.muted).Padding(10, 14)
			return
		}
		for index, field := range fields {
			c.Key(field.key)
			row := ui.Row(c).FillWidth().Gap(16).Padding(8, 14).AlignItems(ui.Start)
			if index > 0 {
				row.BorderWidth(1, 0, 0, 0).BorderColor(colors.border)
			}
			row.Children(func() {
				ui.Text(c, field.key).Width(frontMatterKeyWidth).Shrink(0).FontSize(smallFontSize + 1).TextColor(colors.muted)
				ui.Text(c, field.value).Grow(1).Shrink(1).MinWidth(0).FontSize(smallFontSize + 1)
			})
		}
	})
}
