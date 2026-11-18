package app

import (
	"net/url"
	"path"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"perch/internal/htmlblock"
	"perch/locales"
)

const (
	htmlBlockGap  float32 = 10
	htmlItemGap   float32 = 6
	htmlPillRound float32 = 5
)

// htmlHeadingSizes are the font sizes of the heading levels, from level 1.
var htmlHeadingSizes = [...]float32{30, 24, 20, 17, 15, 14}

// attachMarkdownActions makes a document's links and HTML blocks work.
func (a *App) attachMarkdownActions(d *document) {
	d.markdownOptions.OnLink = func(target string) { a.openLink(d, target) }
	d.markdownOptions.HTML = func(c *ui.Context, source string) { a.htmlBlocksView(c, d, source) }
}

// openLink follows a link of a document: a web address in the browser, and a
// path to a file of the workspace in a tab.
func (a *App) openLink(d *document, target string) {
	address, err := url.Parse(target)
	if err != nil {
		a.tell(toast{title: locales.LinkFailed(), detail: target})
		return
	}
	switch address.Scheme {
	case "http", "https", "mailto":
		go a.openExternal(target)
	case "":
		a.openRelativeLink(d, address.Path)
	default:
		a.tell(toast{title: locales.LinkUnsupported(address.Scheme), detail: target})
	}
}

// openExternal runs off the main thread, which the system call needs free.
func (a *App) openExternal(target string) {
	if err := mygo.Shell.OpenExternal(target); err != nil {
		a.post(func() { a.tell(toast{title: locales.LinkFailed(), detail: err.Error()}) })
	}
}

func (a *App) openRelativeLink(d *document, target string) {
	if target == "" {
		return
	}
	relPath := path.Join(path.Dir(d.path), target)
	if strings.HasPrefix(target, "/") {
		relPath = strings.TrimPrefix(target, "/")
	}
	a.openFile(relPath)
	if current := a.activeDocument(); current != nil && current.path == relPath {
		a.chooseTab(a.active)
	}
}

// htmlBlocksView shows an HTML block of a Markdown file.
func (a *App) htmlBlocksView(c *ui.Context, d *document, source string) {
	blocks, err := d.htmlBlocks(source)
	if err != nil {
		ui.Text(c, source).TextColor(a.look.palette.muted)
		return
	}
	ui.Column(c).FillWidth().Gap(htmlBlockGap).Children(func() {
		for index, block := range blocks {
			c.Key(index)
			a.htmlBlockView(c, d, block)
		}
	})
}

func (a *App) htmlBlockView(c *ui.Context, d *document, block htmlblock.Block) {
	if block.Rule {
		ui.Box(c).FillWidth().Height(1).Background(a.look.palette.border)
		return
	}
	ui.Column(c).FillWidth().Gap(htmlItemGap).Children(func() {
		for index, line := range block.Lines {
			c.Key(index)
			a.htmlLineView(c, d, block, line)
		}
	})
}

func (a *App) htmlLineView(c *ui.Context, d *document, block htmlblock.Block, line []htmlblock.Item) {
	if !hasImage(line) {
		a.htmlTextView(c, d, block, line)
		return
	}
	ui.Row(c).FillWidth().Wrap().Gap(htmlItemGap).AlignItems(ui.Center).Justify(flexAlign(block.Align)).Children(func() {
		for index, item := range line {
			c.Key(index)
			if item.Image != nil {
				a.htmlImageView(c, d, item)
				continue
			}
			a.htmlTextView(c, d, htmlblock.Block{Heading: block.Heading}, []htmlblock.Item{item})
		}
	})
}

func hasImage(line []htmlblock.Item) bool {
	for _, item := range line {
		if item.Image != nil {
			return true
		}
	}
	return false
}

func flexAlign(align htmlblock.Align) ui.Align {
	switch align {
	case htmlblock.AlignCenter:
		return ui.Center
	case htmlblock.AlignEnd:
		return ui.End
	default:
		return ui.Start
	}
}

func (a *App) htmlTextView(c *ui.Context, d *document, block htmlblock.Block, line []htmlblock.Item) {
	text := ui.RichText(c).FillWidth().TextAlign(flexAlign(block.Align))
	if block.Heading > 0 {
		text.FontSize(htmlHeadingSizes[block.Heading-1]).FontWeight(650)
	}
	text.Children(func() {
		if block.Bullet {
			ui.Text(c, "•  ")
		}
		for index, item := range line {
			c.Key(index)
			a.htmlSpanView(c, d, item)
		}
	})
}

func (a *App) htmlSpanView(c *ui.Context, d *document, item htmlblock.Item) {
	var span ui.Element
	if item.Link != "" {
		span = ui.Link(c, item.Text, item.Link)
		if span.Clicked() && isRelativeLink(item.Link) {
			a.openLink(d, item.Link)
		}
	} else {
		span = ui.Text(c, item.Text)
	}
	if item.Bold {
		span.FontWeight(600)
	}
	if item.Italic {
		span.Italic()
	}
	if item.Code {
		span.Background(a.look.palette.hover)
	}
}

// isRelativeLink reports a link to a file of the workspace.
func isRelativeLink(target string) bool {
	address, err := url.Parse(target)
	return err == nil && address.Scheme == ""
}

func (a *App) htmlImageView(c *ui.Context, d *document, item htmlblock.Item) {
	address := item.Image.Light
	if a.look.theme.Dark {
		address = item.Image.Dark
	}
	frame := ui.Box(c).MaxWidthPercent(100)
	if item.Link != "" && frame.Clicked() {
		a.openLink(d, item.Link)
	}
	frame.Children(func() {
		if source := a.imageFor(d, address); source != nil {
			showImage(c, source, *item.Image)
			return
		}
		a.imagePillView(c, *item.Image, item.Link != "")
	})
}

// showImage draws an image at the size the HTML gives it, keeping its shape.
func showImage(c *ui.Context, source ui.ImageSource, image htmlblock.Image) {
	width, height := ui.ImageSize(source)
	element := ui.Image(c, source).Radius(6).MaxWidthPercent(100)
	if width > 0 && height > 0 {
		element.AspectRatio(width / height)
	}
	switch {
	case image.Width > 0:
		element.Width(float32(image.Width))
	case image.Height > 0 && height > 0:
		element.Width(float32(image.Height) * width / height)
	}
	element.Label(image.Alt)
}

// imagePillView stands in for a picture Perch does not show, such as a badge
// on the web, which Perch does not fetch.
func (a *App) imagePillView(c *ui.Context, image htmlblock.Image, linked bool) {
	colors := a.look.palette
	label := image.Alt
	if label == "" {
		label = locales.ImageFallback()
	}
	color := colors.muted
	if linked {
		color = colors.accent
	}
	ui.Row(c).Padding(3, 8).Radius(htmlPillRound).Background(colors.hover.Alpha(0.6)).Children(func() {
		ui.Text(c, label).FontSize(smallFontSize).TextColor(color).MaxLines(1)
	})
}

// imageFor returns a local image of a document once it has loaded, and starts
// loading it the first time it is asked for. An image that cannot be loaded
// stays nil and shows its text. The goroutine ends with the load.
func (a *App) imageFor(d *document, address string) ui.ImageSource {
	if !isWorkspaceImage(d.path, address) {
		return nil
	}
	if source, known := d.images[address]; known {
		return source
	}
	d.images[address] = nil
	load := d.markdownOptions.LoadImage
	go func() {
		source, err := load(filepath.FromSlash(address))
		a.post(func() {
			if err == nil {
				d.images[address] = source
			}
		})
	}()
	return nil
}

// isWorkspaceImage reports an image address that stays inside the workspace,
// so that a file cannot show pictures from elsewhere on the machine.
func isWorkspaceImage(documentPath string, address string) bool {
	parsed, err := url.Parse(address)
	if err != nil || parsed.Scheme != "" || parsed.Host != "" || address == "" || strings.HasPrefix(address, "/") {
		return false
	}
	return filepath.IsLocal(filepath.FromSlash(path.Join(path.Dir(documentPath), address)))
}
