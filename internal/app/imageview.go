package app

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	_ "image/gif"  // image.DecodeConfig
	_ "image/jpeg" // image.DecodeConfig
	_ "image/png"  // image.DecodeConfig
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime/debug"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/marukolibro/Perch/internal/workspace"
	"github.com/marukolibro/Perch/locales"
)

const (
	// maxImageFileBytes is the largest image Perch previews from disk.
	maxImageFileBytes int64 = 30 << 20
	// maxImagePixels is the largest picture Perch decodes, in pixels. Decoding
	// holds every pixel at once, whatever size the picture keeps afterwards,
	// so this limit is what keeps the app under its memory budget.
	maxImagePixels int64 = 16_000_000
	// largeImagePixels is the size from which a picture's memory goes back to
	// the system at once instead of waiting for the next collection.
	largeImagePixels int64 = 4_000_000
	// maxImageSide is the longest side a picture keeps in memory, which holds
	// an open tab to about 64 MB however large the file is.
	maxImageSide int = 4096
)

// imageKind is the kind of preview an image file needs.
type imageKind uint8

const (
	imageKindNone imageKind = iota
	// imageKindRaster is a picture decoded into pixels.
	imageKindRaster
	// imageKindSVG is an SVG, editable as source and drawn as a preview.
	imageKindSVG
)

// imageStamp tells a rewritten file from one left alone without reading it.
type imageStamp struct {
	size    int64
	modTime time.Time
}

// sameAs reports whether two stamps describe the same version of a file.
func (s imageStamp) sameAs(other imageStamp) bool {
	return s.size == other.size && s.modTime.Equal(other.modTime)
}

// imageView is the state of an image document's preview.
type imageView struct {
	kind imageKind
	// raster is the decoded picture, nil while it decodes or after release.
	raster *ui.Bitmap
	// svgPicture is the parsed SVG of the buffer at svgVersion.
	svgPicture *ui.SVG
	svgVersion uint64
	svgLoaded  bool
	svgFailure error
	// width and height are the picture's pixels, and size its file's bytes.
	width  int
	height int
	size   int64
	// stamp is the version of the file last read.
	stamp imageStamp
	// request counts reads, so a decode that an older read started is
	// discarded.
	request uint64
	// decoded marks a raster picture ready to show.
	decoded bool
	// silent leaves out the toast of an initial decode that a restored tab
	// fails.
	silent bool
}

// release drops the decoded picture so that a closed tab can be collected.
func (v *imageView) release() {
	large := v.raster != nil && isLargeImage(v.width, v.height)
	v.request++
	v.raster = nil
	v.decoded = false
	v.svgPicture = nil
	if large {
		go debug.FreeOSMemory()
	}
}

// isLargeImage reports whether a picture is worth returning memory for.
func isLargeImage(width int, height int) bool {
	return int64(width)*int64(height) >= largeImagePixels
}

// status returns the picture's dimensions and file size, as
// "1280 × 720 · 214 KB".
func (v *imageView) status() string {
	size := imageFileSize(v.size)
	if v.width <= 0 || v.height <= 0 {
		return size
	}
	return fmt.Sprintf("%d × %d · %s", v.width, v.height, size)
}

// imageFileSize writes a byte count as B, KB or MB.
func imageFileSize(size int64) string {
	switch {
	case size < 1<<10:
		return fmt.Sprintf("%d B", size)
	case size < 1<<20:
		return fmt.Sprintf("%d KB", (size+512)/1024)
	default:
		text := strconv.FormatFloat(float64(size)/(1<<20), 'f', 1, 64)
		return strings.TrimSuffix(text, ".0") + " MB"
	}
}

// imageKindOf names the preview an extension needs, by extension alone.
func imageKindOf(name string) imageKind {
	switch strings.ToLower(path.Ext(name)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp":
		return imageKindRaster
	case ".svg":
		return imageKindSVG
	default:
		return imageKindNone
	}
}

// isRasterImage reports whether the document shows a picture decoded into
// pixels, which has no editor.
func (d *document) isRasterImage() bool {
	return d.image != nil && d.image.kind == imageKindRaster
}

// isSVG reports whether the document is an SVG, whose source is editable.
func (d *document) isSVG() bool {
	return d.image != nil && d.image.kind == imageKindSVG
}

// showsSVGPreview reports whether the document shows its SVG drawn rather
// than its source.
func (d *document) showsSVGPreview() bool {
	return d.isSVG() && d.markdownView == markdownPreview
}

// imageTooLargeError reports an image above maxImageFileBytes.
type imageTooLargeError struct {
	Path  string
	Size  int64
	Limit int64
}

func (e *imageTooLargeError) Error() string {
	return fmt.Sprintf("read image %q (%d bytes, limit %d): file is too large", e.Path, e.Size, e.Limit)
}

// imageTooManyPixelsError reports an image above maxImagePixels.
type imageTooManyPixelsError struct {
	Path   string
	Width  int
	Height int
	Limit  int64
}

func (e *imageTooManyPixelsError) Error() string {
	return fmt.Sprintf("read image %q (%d × %d pixels, limit %d): picture has too many pixels",
		e.Path, e.Width, e.Height, e.Limit)
}

// imageFailure says in plain words why an image did not open.
func imageFailure(relPath string, err error) toast {
	name := path.Base(relPath)
	var tooLarge *imageTooLargeError
	var tooManyPixels *imageTooManyPixelsError
	switch {
	case errors.As(err, &tooLarge):
		return toast{
			title:  locales.ImageTooLarge(name),
			detail: locales.ImageTooLargeDetail(imageFileSize(tooLarge.Size), imageFileSize(tooLarge.Limit)),
		}
	case errors.As(err, &tooManyPixels):
		return toast{
			title: locales.ImageTooManyPixels(name),
			detail: locales.ImageTooManyPixelsDetail(
				tooManyPixels.Width,
				tooManyPixels.Height,
				int(tooManyPixels.Limit/1_000_000),
			),
		}
	case errors.Is(err, fs.ErrNotExist), errors.Is(err, fs.ErrPermission):
		return openFailure(relPath, err)
	default:
		return toast{title: locales.ImageOpenFailed(name), detail: locales.ImageOpenFailedDetail()}
	}
}

// rasterFile is an image read from disk before its pixels decode.
type rasterFile struct {
	data   []byte
	digest workspace.Digest
	width  int
	height int
	size   int64
	stamp  imageStamp
}

// decodedRaster is a picture ready to show, with the facts of its file.
type decodedRaster struct {
	bitmap *ui.Bitmap
	width  int
	height int
	size   int64
}

// statImage returns the stamp of an image of the workspace.
func statImage(root string, relPath string) (imageStamp, error) {
	info, err := os.Stat(filepath.Join(root, filepath.FromSlash(relPath)))
	if err != nil {
		return imageStamp{}, fmt.Errorf("stat image %q: %w", relPath, err)
	}
	return imageStamp{size: info.Size(), modTime: info.ModTime()}, nil
}

// readRaster reads an image of the workspace, refusing one that is too large
// or whose dimensions exceed maxImagePixels. It reads the dimensions before
// any pixels decode.
func readRaster(root string, relPath string) (rasterFile, error) {
	local := filepath.FromSlash(relPath)
	if !filepath.IsLocal(local) {
		return rasterFile{}, fmt.Errorf("read image %q: %w", relPath, workspace.ErrOutsideWorkspace)
	}
	file := filepath.Join(root, local)
	info, err := os.Stat(file)
	if err != nil {
		return rasterFile{}, fmt.Errorf("read image %q: %w", relPath, err)
	}
	if info.Size() > maxImageFileBytes {
		return rasterFile{}, &imageTooLargeError{Path: relPath, Size: info.Size(), Limit: maxImageFileBytes}
	}
	data, err := readImageBytes(file, relPath)
	if err != nil {
		return rasterFile{}, err
	}
	// The ui package registers WebP with image.Decode; its own imports
	// register it for DecodeConfig too.
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return rasterFile{}, fmt.Errorf("read image %q: %w", relPath, err)
	}
	if config.Width <= 0 || config.Height <= 0 {
		return rasterFile{}, fmt.Errorf("read image %q: it has no pixels", relPath)
	}
	if pixels := int64(config.Width) * int64(config.Height); pixels > maxImagePixels {
		return rasterFile{}, &imageTooManyPixelsError{
			Path:   relPath,
			Width:  config.Width,
			Height: config.Height,
			Limit:  maxImagePixels,
		}
	}
	return rasterFile{
		data:   data,
		digest: workspace.DigestOf(data),
		width:  config.Width,
		height: config.Height,
		size:   int64(len(data)),
		stamp:  imageStamp{size: info.Size(), modTime: info.ModTime()},
	}, nil
}

// readImageBytes reads at most maxImageFileBytes of an image file.
func readImageBytes(file string, relPath string) ([]byte, error) {
	handle, err := os.Open(file)
	if err != nil {
		return nil, fmt.Errorf("read image %q: %w", relPath, err)
	}
	data, readErr := io.ReadAll(io.LimitReader(handle, maxImageFileBytes+1))
	closeErr := handle.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return nil, fmt.Errorf("read image %q: %w", relPath, err)
	}
	if int64(len(data)) > maxImageFileBytes {
		return nil, &imageTooLargeError{Path: relPath, Size: int64(len(data)), Limit: maxImageFileBytes}
	}
	return data, nil
}

// openRasterImage opens a picture in a tab and decodes its pixels off the
// main thread.
func (a *App) openRasterImage(relPath string) {
	file, err := readRaster(a.root, relPath)
	if err != nil {
		a.tell(imageFailure(relPath, err))
		return
	}
	d := a.newRasterDocument(relPath, file)
	a.brief = toast{}
	a.documents = append(a.documents, d)
	a.active = len(a.documents) - 1
	a.recordRecentFile(relPath)
	a.startImageDecode(d, file)
}

// newRasterDocument makes a picture's document without appending it or
// starting its decode.
func (a *App) newRasterDocument(relPath string, file rasterFile) *document {
	return &document{
		path:   relPath,
		digest: file.digest,
		image: &imageView{
			kind:   imageKindRaster,
			width:  file.width,
			height: file.height,
			size:   file.size,
			stamp:  file.stamp,
		},
	}
}

// openSVGFile opens an SVG on its preview, or on its source when it cannot be
// drawn, so that a broken SVG can still be repaired.
func (a *App) openSVGFile(relPath string) {
	file, err := workspace.ReadText(a.root, relPath)
	if err != nil {
		a.tell(openFailure(relPath, err))
		return
	}
	d := a.newSVGDocument(relPath, file)
	a.attachDefinitions(d)
	a.attachEditorMenu(d)
	a.brief = toast{}
	a.documents = append(a.documents, d)
	a.active = len(a.documents) - 1
	a.recordRecentFile(relPath)
}

// newSVGDocument makes an SVG's document, on its preview or its source when it
// cannot be drawn, without appending it.
func (a *App) newSVGDocument(relPath string, file workspace.TextFile) *document {
	d := newDocument(a.root, relPath, file)
	d.image = &imageView{kind: imageKindSVG, size: int64(len(file.Text))}
	if _, err := d.svgPicture(); err != nil {
		d.markdownView = markdownSource
	}
	return d
}

// startImageDecode decodes the pixels of an image off the main thread. The
// goroutine it starts belongs to the document's image view: it ends when the
// decoder returns, and its result is discarded when the tab closed or a newer
// read replaced it.
func (a *App) startImageDecode(d *document, file rasterFile) {
	view := d.image
	view.request++
	request := view.request
	go func() {
		bitmap, err := ui.DecodeBitmapScaled(file.data, maxImageSide)
		decoded := decodedRaster{bitmap: bitmap, width: file.width, height: file.height, size: file.size}
		a.post(func() { a.imageDecoded(d, request, decoded, err) })
		file.data = nil
		if isLargeImage(file.width, file.height) {
			debug.FreeOSMemory()
		}
	}()
}

// imageDecoded shows a decoded picture. A picture that cannot be decoded closes
// a tab with nothing to show, and leaves the last good picture of one rewritten
// on disk, as a file an agent is still writing is.
func (a *App) imageDecoded(d *document, request uint64, decoded decodedRaster, err error) {
	view := d.image
	if view == nil || view.request != request || !slices.Contains(a.documents, d) {
		return
	}
	if err != nil {
		if !view.decoded {
			if !view.silent {
				a.tell(imageFailure(d.path, err))
			}
			a.closeTab(d)
			return
		}
		a.tell(imageFailure(d.path, err))
		return
	}
	view.raster = decoded.bitmap
	view.decoded = true
	view.width, view.height, view.size = decoded.width, decoded.height, decoded.size
}

// reconcileImage follows an image rewritten or removed on disk, keeping the
// last good picture when the new one cannot be read. Every change in the
// workspace comes here, so a file left alone is not read again.
func (a *App) reconcileImage(d *document) {
	stamp, err := statImage(a.root, d.path)
	if errors.Is(err, fs.ErrNotExist) {
		d.conflict = conflictRemoved
		return
	}
	if err == nil && stamp.sameAs(d.image.stamp) {
		d.conflict = conflictNone
		return
	}
	d.image.stamp = stamp
	file, err := readRaster(a.root, d.path)
	if errors.Is(err, fs.ErrNotExist) {
		d.conflict = conflictRemoved
		return
	}
	if err != nil {
		a.tell(imageFailure(d.path, err))
		return
	}
	if file.digest == d.digest {
		d.conflict = conflictNone
		return
	}
	d.digest = file.digest
	d.conflict = conflictNone
	a.startImageDecode(d, file)
}

// svgPicture parses the buffer's SVG, again only after an edit, so that the
// preview follows unsaved changes without parsing on every frame.
func (d *document) svgPicture() (*ui.SVG, error) {
	view := d.image
	if view == nil || view.kind != imageKindSVG || d.editor == nil {
		return nil, errors.New("document has no SVG preview")
	}
	version := d.editor.Buffer.Version()
	if view.svgLoaded && view.svgVersion == version {
		if view.svgFailure != nil {
			return nil, fmt.Errorf("preview SVG %q: %w", d.path, view.svgFailure)
		}
		return view.svgPicture, nil
	}
	source, err := ui.ParseSVG([]byte(d.editor.Buffer.String()))
	if err == nil {
		if width, height := source.Size(); width <= 0 || height <= 0 {
			source, err = nil, errors.New("it has no size")
		}
	}
	view.svgLoaded = true
	view.svgVersion = version
	view.svgPicture = source
	view.svgFailure = err
	if err != nil {
		view.width, view.height = 0, 0
		return nil, fmt.Errorf("preview SVG %q: %w", d.path, err)
	}
	width, height := source.Size()
	view.width, view.height = int(width+0.5), int(height+0.5)
	return source, nil
}

// rasterImageView fills the pane with the picture, scaled down to fit and
// centered.
func (a *App) rasterImageView(c *ui.Context, d *document) {
	if !d.image.decoded {
		a.imageMessageView(c, locales.ImageOpening())
		return
	}
	a.imagePaneView(c, d.image.raster, d.name())
}

// svgImageView draws the buffer's SVG, or says why it cannot.
func (a *App) svgImageView(c *ui.Context, d *document) {
	source, err := d.svgPicture()
	if err != nil {
		a.imageMessageView(c, locales.SvgPreviewFailed())
		return
	}
	a.imagePaneView(c, source, d.name())
}

// imagePaneView centers a picture in the document's pane, never enlarging it.
func (a *App) imagePaneView(c *ui.Context, source ui.ImageSource, label string) {
	ui.Column(c).Grow(1).FillWidth().Padding(24).Children(func() {
		ui.Image(c, source).Fit(ui.ScaleDown).Fill().Label(label)
	})
}

// imageMessageView shows a quiet message in place of a picture.
func (a *App) imageMessageView(c *ui.Context, message string) {
	ui.Column(c).Grow(1).FillWidth().Center().Padding(0, 24).Children(func() {
		ui.Text(c, message).TextColor(a.look.palette.muted)
	})
}
