// Package htmlblock reads the HTML that READMEs put between Markdown blocks
// into a small model of aligned lines of text, links and images. It models
// what such files use rather than the whole of HTML.
package htmlblock

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// Align is the horizontal placement of a block's lines.
type Align uint8

// The placements a block can ask for.
const (
	AlignStart Align = iota
	AlignCenter
	AlignEnd
)

// Image is a picture, with the version for each appearance.
type Image struct {
	Light string
	Dark  string
	Alt   string
	// Width and Height are in DIPs, and 0 when the HTML does not say.
	Width  int
	Height int
}

// Item is a run of text or an image, either of which may be a link.
type Item struct {
	Text   string
	Bold   bool
	Italic bool
	Code   bool
	Link   string
	Image  *Image
}

// Block is a paragraph, heading, list item or rule.
type Block struct {
	Align Align
	// Heading is the level of a heading, and 0 for other blocks.
	Heading int
	Bullet  bool
	Rule    bool
	Lines   [][]Item
}

var (
	whitespace = regexp.MustCompile(`[ \t\r\n\f]+`)
	textAlign  = regexp.MustCompile(`(?i)text-align\s*:\s*(left|right|center|start|end)`)
)

// Parse reads a fragment of HTML.
func Parse(source string) ([]Block, error) {
	body := &html.Node{Type: html.ElementNode, DataAtom: atom.Body, Data: "body"}
	nodes, err := html.ParseFragment(strings.NewReader(source), body)
	if err != nil {
		return nil, fmt.Errorf("parse HTML block: %w", err)
	}
	r := &reader{}
	for _, node := range nodes {
		r.walk(node, inherited{})
	}
	r.endBlock()
	return r.blocks, nil
}

// inherited is what an element passes down to its content.
type inherited struct {
	align   Align
	heading int
	bullet  bool
	bold    bool
	italic  bool
	code    bool
	link    string
}

// reader collects blocks as it walks the tree.
type reader struct {
	blocks  []Block
	lines   [][]Item
	line    []Item
	current inherited
}

func (r *reader) walk(node *html.Node, in inherited) {
	switch node.Type {
	case html.TextNode:
		r.addText(node.Data, in)
	case html.ElementNode:
		r.walkElement(node, in)
	}
}

func (r *reader) walkChildren(node *html.Node, in inherited) {
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		r.walk(child, in)
	}
}

func (r *reader) walkElement(node *html.Node, in inherited) {
	switch node.DataAtom {
	case atom.Script, atom.Style, atom.Head, atom.Template, atom.Noscript:
	case atom.Br:
		r.endLine()
	case atom.Hr:
		r.endBlock()
		r.blocks = append(r.blocks, Block{Rule: true})
	case atom.Img:
		r.addImage(imageOf(node), in)
	case atom.Picture:
		r.addImage(pictureOf(node), in)
	case atom.A:
		in.link = attribute(node, "href")
		r.walkChildren(node, in)
	case atom.B, atom.Strong, atom.Summary, atom.Th:
		in.bold = true
		r.walkInlineOrBlock(node, in)
	case atom.I, atom.Em, atom.Cite:
		in.italic = true
		r.walkChildren(node, in)
	case atom.Code, atom.Kbd, atom.Samp, atom.Tt:
		in.code = true
		r.walkChildren(node, in)
	default:
		r.walkInlineOrBlock(node, in)
	}
}

func (r *reader) walkInlineOrBlock(node *html.Node, in inherited) {
	if !isBlockElement(node.DataAtom) {
		r.walkChildren(node, in)
		return
	}
	r.endBlock()
	in = blockContext(node, in)
	r.current = in
	r.walkChildren(node, in)
	r.endBlock()
}

func isBlockElement(name atom.Atom) bool {
	switch name {
	case atom.P, atom.Div, atom.Center, atom.Section, atom.Article, atom.Header, atom.Footer, atom.Main, atom.Nav,
		atom.Aside, atom.Blockquote, atom.Figure, atom.Figcaption, atom.Details, atom.Summary, atom.Ul, atom.Ol,
		atom.Li, atom.Table, atom.Thead, atom.Tbody, atom.Tfoot, atom.Tr, atom.Pre,
		atom.H1, atom.H2, atom.H3, atom.H4, atom.H5, atom.H6:
		return true
	default:
		return false
	}
}

func blockContext(node *html.Node, in inherited) inherited {
	in.align = alignOf(node, in.align)
	in.bullet = node.DataAtom == atom.Li
	if level := headingLevel(node.DataAtom); level > 0 {
		in.heading = level
		in.bold = true
	}
	return in
}

func headingLevel(name atom.Atom) int {
	switch name {
	case atom.H1:
		return 1
	case atom.H2:
		return 2
	case atom.H3:
		return 3
	case atom.H4:
		return 4
	case atom.H5:
		return 5
	case atom.H6:
		return 6
	default:
		return 0
	}
}

func alignOf(node *html.Node, parent Align) Align {
	if node.DataAtom == atom.Center {
		return AlignCenter
	}
	value := strings.ToLower(attribute(node, "align"))
	if match := textAlign.FindStringSubmatch(attribute(node, "style")); match != nil {
		value = strings.ToLower(match[1])
	}
	switch value {
	case "center":
		return AlignCenter
	case "right", "end":
		return AlignEnd
	case "left", "start":
		return AlignStart
	default:
		return parent
	}
}

func attribute(node *html.Node, name string) string {
	for _, attr := range node.Attr {
		if attr.Key == name {
			return strings.TrimSpace(attr.Val)
		}
	}
	return ""
}

func imageOf(node *html.Node) Image {
	source := attribute(node, "src")
	return Image{
		Light:  source,
		Dark:   source,
		Alt:    attribute(node, "alt"),
		Width:  pixels(attribute(node, "width")),
		Height: pixels(attribute(node, "height")),
	}
}

// pictureOf reads a picture's img and the sources that replace it in each
// appearance.
func pictureOf(node *html.Node) Image {
	var image Image
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if child.DataAtom == atom.Img {
			image = imageOf(child)
		}
	}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if child.DataAtom != atom.Source {
			continue
		}
		source := firstSource(attribute(child, "srcset"))
		media := strings.ToLower(attribute(child, "media"))
		switch {
		case strings.Contains(media, "dark"):
			image.Dark = source
		case strings.Contains(media, "light"):
			image.Light = source
		}
	}
	return image
}

// firstSource returns the first address of a srcset.
func firstSource(srcset string) string {
	first, _, _ := strings.Cut(srcset, ",")
	fields := strings.Fields(first)
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

// pixels reads a size in pixels. A percentage says nothing about pixels.
func pixels(value string) int {
	size, err := strconv.Atoi(strings.TrimSuffix(value, "px"))
	if err != nil || size < 0 {
		return 0
	}
	return size
}

func (r *reader) addText(text string, in inherited) {
	collapsed := whitespace.ReplaceAllString(text, " ")
	if collapsed == "" {
		return
	}
	r.begin(in)
	r.line = append(r.line, Item{Text: collapsed, Bold: in.bold, Italic: in.italic, Code: in.code, Link: in.link})
}

func (r *reader) addImage(image Image, in inherited) {
	r.begin(in)
	r.line = append(r.line, Item{Image: &image, Link: in.link})
}

// begin takes the block's placement from content outside any block element.
func (r *reader) begin(in inherited) {
	if len(r.line) == 0 && len(r.lines) == 0 {
		r.current = in
	}
}

func (r *reader) endLine() {
	if line := tidyLine(r.line); len(line) > 0 {
		r.lines = append(r.lines, line)
	}
	r.line = nil
}

func (r *reader) endBlock() {
	r.endLine()
	if len(r.lines) > 0 {
		r.blocks = append(r.blocks, Block{
			Align:   r.current.align,
			Heading: r.current.heading,
			Bullet:  r.current.bullet,
			Lines:   r.lines,
		})
	}
	r.lines = nil
}

// tidyLine drops the spaces that only separate images or end the line.
func tidyLine(line []Item) []Item {
	tidy := make([]Item, 0, len(line))
	for index, item := range line {
		blank := item.Image == nil && strings.TrimSpace(item.Text) == ""
		if blank && (len(tidy) == 0 || tidy[len(tidy)-1].Image != nil || nextIsImage(line, index)) {
			continue
		}
		tidy = append(tidy, item)
	}
	if len(tidy) == 0 {
		return nil
	}
	if tidy[0].Image == nil {
		tidy[0].Text = strings.TrimLeft(tidy[0].Text, " ")
	}
	last := len(tidy) - 1
	if tidy[last].Image == nil {
		tidy[last].Text = strings.TrimRight(tidy[last].Text, " ")
	}
	return tidy
}

func nextIsImage(line []Item, index int) bool {
	return index+1 >= len(line) || line[index+1].Image != nil
}
