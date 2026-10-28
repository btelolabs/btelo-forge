package ui

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/image/draw"
	"golang.org/x/image/math/f64"

	"github.com/btelolabs/btelo-forge/internal/gfx"
	"github.com/btelolabs/btelo-forge/internal/paint"
)

// artTool is what a press on the canvas does.
type artTool int

const (
	toolMove artTool = iota
	toolMarquee
	toolLasso
	toolWand
	toolPicker
	toolBrush
	toolEraser
	toolHand
	toolZoom
	toolClone
	toolGradient
	toolBucket
	toolShape
	toolText
	toolPen
	toolNode
	toolBoard
	toolPencil
	toolDodge
	toolBurn
	toolSponge
	toolBlur
	toolSharpen
	toolSmudge
	toolMaskGradient
)

// artDoc is a canvas tab: a layered picture, saved with its layers next to the recordings and exported as PNG.
type artDoc struct {
	d         *paint.Doc
	path      string // the .btcanvas file it is saved to
	dirty     bool
	changed   time.Time
	saving    bool
	saveErr   error
	zoom      float64
	fit       bool     // the zoom follows the window
	fitFor    gfx.Rect // the viewport the zoom was last fitted to
	panX      float64  // the canvas point shown at the middle of the viewport
	panY      float64
	view      *image.RGBA     // the viewport's pixels: the picture over a checkerboard
	viewFor   viewKey         // the viewport, zoom and pan the view was made for
	stale     image.Rectangle // canvas area to bring into the view
	stroke    *paint.Stroke
	move      *paint.MoveSession
	xf        *transformEdit       // Free Transform under way
	backdrop  *image.RGBA          // the video's picture, behind a drawing on it; never saved or exported
	noBack    bool                 // the backdrop is turned off in the View menu
	text      *textEdit            // a text layer being typed
	vstroke   *vectorStroke        // the vector brush's stroke under way
	pen       *penEdit             // a curve being drawn with the pen
	nodes     *nodeEdit            // the node tool's chosen nodes and drag
	shape     *paint.VectorSession // the shape being dragged, shown as it will be
	snapTo    snapTargets          // what the drag under way snaps to, gathered as it began
	snapMark  *paint.Pt            // where it snapped this frame, for the overlay
	picked    []int                // layers chosen besides the active one, for booleans
	layerHeld int                  // one of several chosen layers pressed in the panel: chosen alone if let go without a drag
	layerDrag *layerDrag           // layers being dragged in the layers panel
	board     int                  // the artboard the artboard tool has chosen
	boardDrag *boardEdit           // the artboard being made, moved or resized
	boardName string               // the chosen board's name as typed
	boardSize map[string]string    // its width and height as typed
	props     *paint.VectorSession // a vector property changed live, such as the stroke's width or a colour being dragged
	gradStop  int                  // the stop of the chosen vector layer's gradient that the colours edit
	gradDrag  *gradDrag            // the gradient tool's drag on a vector layer
	stopsDrag *stopsDrag           // a stop dragged along the options bar's gradient
	drawing   bool                 // a gradient or shape is being dragged, from drawFrom to drawTo
	drawFrom  [2]float64
	drawTo    [2]float64
	// The clone stamp: where it was last set to clone from, and the distance from the brush it keeps once aligned.
	cloneFrom    [2]float64
	cloneSet     bool
	cloneOffset  image.Point
	cloneAligned bool
	moveFrom     [2]float64       // where in the picture the move began
	descend      int              // the node a click without a drag goes into, by auto-select
	boardLayer   map[int]int      // the layer last chosen in each artboard, by board
	mode         paint.SelectMode // how the shape being drawn will combine with the selection
	selFrom      image.Point
	selNow       image.Point
	selDrag      bool
	lasso        [][2]float64 // the lasso's outline so far, in picture pixels
	ants         selectionAnts
	panFrom      [4]float64 // pointer and pan where a hand drag began
	nav          int        // the navigation drag under way: navPan or navZoom, with Space or the zoom key held
	navZoom      float64    // the zoom where it began
	renaming     int
	rename       string
	sliceInfo    int                 // the node whose slice the ⓘ panel explains
	sliceSizes   sliceSizeCache      // the size of what it shows
	sliceRows    map[int]paint.Slice // the slices and folder groups, by node, as the layers panel last listed them
	cursor       [2]float64          // the pointer in canvas pixels, for the status line
	over         bool                // the pointer is over the canvas
}

type viewKey struct {
	vp         image.Rectangle
	zoom, x, y float64
}

func (a *App) artTitle(ad *artDoc) string {
	if ad.d.SmartSource() != "" {
		return a.smartTitle(ad)
	}
	return strings.TrimSuffix(filepath.Base(ad.path), paint.Ext)
}

// openArt shows a picture in a canvas tab, the current one when it is empty.
func (a *App) openArt(d *paint.Doc, path string) {
	if a.occupied() {
		a.newTab()
	}
	a.art = &artDoc{d: d, path: path, fit: true, renaming: -1}
	a.art.refresh()
	a.linkSmart(a.art)
}

// newCanvas makes an empty canvas, with no size: it has no edge and grows with what is drawn on it, as Affinity
// Designer's does. Its file is named now but written only with the first change, a moment after it as every change
// is, so a canvas opened and closed again with nothing on it leaves no file behind.
func (a *App) newCanvas() error {
	d := paint.New(a.u.tf("Layer %d", 1))
	path, err := a.capturePath(a.u.t("Canvas %s"), paint.Ext, time.Now())
	if err != nil {
		return err
	}
	a.openArt(d, path)
	return nil
}

// openImageAsArt opens a picture file, such as a screenshot, as a new canvas to be saved next to it; as a new
// canvas's, its file is written only with the first change, so a picture only looked at leaves nothing behind.
func (a *App) openImageAsArt(path string) {
	a.background(func(ctx context.Context) {
		img, err := loadImage(path)
		var d *paint.Doc
		if err == nil {
			d, err = paint.FromImage(img, a.u.tc("layer", "Background"))
		}
		a.post(func() {
			if err != nil {
				a.toast(a.u.tf("Cannot open %s: %s", filepath.Base(path), a.describe(err)), true)
				return
			}
			a.openArt(d, a.freePath(strings.TrimSuffix(path, filepath.Ext(path))+paint.Ext))
		})
	})
}

// openCanvasFile opens a saved canvas.
func (a *App) openCanvasFile(path string) { a.openCanvasFileWith(path, nil) }

func loadImage(path string) (image.Image, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	return img, err
}

// saveArt writes the canvas with its layers: a copy is taken here and written on another goroutine, or here when
// wait is true (closing).
func (a *App) saveArt(ad *artDoc, wait bool) error {
	if ad.xf != nil || ad.text != nil || a.adjust != nil && a.adjust.ad == ad || a.style != nil && a.style.ad == ad {
		if !wait {
			return nil // saved once the transform is applied or the text committed
		}
		a.applyTransform(ad)
		a.commitText(ad)
		if a.adjust != nil && a.adjust.ad == ad {
			a.closeAdjust(false)
		}
		if a.style != nil && a.style.ad == ad {
			a.closeStyle(false)
		}
	}
	if !ad.dirty || ad.saving && !wait {
		return nil
	}
	snap := ad.d.Snapshot()
	// A drawing on the video gets its picture again, for the overlay.
	images := a.drawingImages(ad.path)
	var flat *image.RGBA
	if len(images) > 0 {
		// The drawing's picture is its frame, the video frame's size, transparent where nothing is drawn.
		flat = ad.d.Composite(ad.d.Frame)
	}
	ad.dirty, ad.saving = false, true
	write := func() error {
		var buf bytes.Buffer
		if err := snap.Save(&buf); err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(ad.path), 0o755); err != nil {
			return err
		}
		tmp := ad.path + ".saving"
		if err := os.WriteFile(tmp, buf.Bytes(), 0o644); err != nil {
			return err
		}
		if err := os.Rename(tmp, ad.path); err != nil {
			return err
		}
		for _, path := range images {
			if err := writePNG(path, flat); err != nil {
				return err
			}
		}
		return nil
	}
	if wait {
		err := write()
		ad.saving, ad.saveErr = false, err
		if err != nil {
			ad.dirty = true
		} else {
			a.mindPictureChanged(ad.path) // the maps showing this canvas show what was saved
		}
		a.drawingsChanged(images)
		return err
	}
	a.background(func(context.Context) {
		err := write()
		a.post(func() {
			ad.saving, ad.saveErr = false, err
			a.drawingsChanged(images)
			if err != nil {
				ad.dirty, ad.changed = true, time.Now() // try again after the usual delay, not at once
				a.toast(a.u.tf("The canvas could not be saved: %s", a.describe(err)), true)
			} else {
				a.mindPictureChanged(ad.path) // the maps showing this canvas show what was saved
			}
		})
	})
	return nil
}

// artSaveDelay is how long after the last change a canvas is saved: long enough that the pauses between strokes do
// not each write the whole canvas. Closing, quitting and a crash save what is left at once.
const artSaveDelay = 5 * time.Second

// autosaveArt saves canvases a moment after the last change.
func (a *App) autosaveArt() {
	for _, d := range a.docs {
		if ad := d.art; ad != nil {
			if due, ok := a.artSaveDue(ad); ok && !time.Now().Before(due) {
				a.saveArt(ad, false)
			}
		}
	}
}

// artSaveDue is when a canvas with changes will be saved; ok is false while there is nothing to save or it cannot
// be saved yet (a stroke, transform, text or adjustment under way, or a save already running).
func (a *App) artSaveDue(ad *artDoc) (due time.Time, ok bool) {
	if !ad.dirty || ad.saving || ad.stroke != nil || ad.xf != nil || ad.text != nil || a.adjust != nil && a.adjust.ad == ad ||
		a.style != nil && a.style.ad == ad {
		return time.Time{}, false
	}
	return ad.changed.Add(artSaveDelay), true
}

// touched notes a change to the picture within r (in canvas pixels).
func (ad *artDoc) touched(r image.Rectangle) {
	ad.dirty, ad.changed = true, time.Now()
	if e := ad.d.EffectReach(); e > 0 && !r.Empty() {
		r = r.Inset(-e) // a layer's effects show around what changed
	}
	ad.update(r)
}

// refresh draws the whole view again, after a change that may touch any of it (undo, layer operations).
func (ad *artDoc) refresh() { ad.view = nil }

// update draws the view again within the canvas area r.
func (ad *artDoc) update(r image.Rectangle) { ad.stale = ad.stale.Union(r) }

// changedAll records a change that may touch the whole picture or the layers.
func (ad *artDoc) changedAll() {
	ad.dirty, ad.changed = true, time.Now()
	ad.refresh()
}

// origin is where canvas point (0, 0) is in the window.
func (ad *artDoc) origin(vp gfx.Rect) (x, y float64) {
	return float64(vp.X+vp.W/2) - ad.panX*ad.zoom, float64(vp.Y+vp.H/2) - ad.panY*ad.zoom
}

// toCanvas converts a window position to canvas pixels.
func (ad *artDoc) toCanvas(vp gfx.Rect, sx, sy float32) (float64, float64) {
	x, y := ad.origin(vp)
	return (float64(sx) - x) / ad.zoom, (float64(sy) - y) / ad.zoom
}

// visible is the canvas area the viewport shows.
func (ad *artDoc) visible(vp gfx.Rect) image.Rectangle {
	x0, y0 := ad.toCanvas(vp, vp.X, vp.Y)
	x1, y1 := ad.toCanvas(vp, vp.Right(), vp.Bottom())
	return image.Rect(int(math.Floor(x0)), int(math.Floor(y0)), int(math.Ceil(x1)), int(math.Ceil(y1)))
}

// fitView fits the canvas to the window now and as the window changes, as View › Fit on Screen does.
func (ad *artDoc) fitView() { ad.fit, ad.fitFor = true, gfx.Rect{} }

// fitZoom shows the pages (the frame and the artboards), or else all that is painted, at most at its own size; an
// empty canvas without pages is shown at its own size about its origin.
func (ad *artDoc) fitZoom(vp gfx.Rect) {
	r := ad.d.Frame
	for _, b := range ad.d.Boards() {
		r = r.Union(b.Rect)
	}
	if r.Empty() {
		r = ad.d.Reach()
	}
	ad.fitTo(vp, r)
}

// fitTo shows the canvas area r whole in the view, at most at its own size.
func (ad *artDoc) fitTo(vp gfx.Rect, r image.Rectangle) {
	if r.Empty() {
		ad.zoom, ad.panX, ad.panY = 1, 0, 0
		return
	}
	ad.zoom = min(float64(vp.W)*0.94/float64(r.Dx()), float64(vp.H)*0.94/float64(r.Dy()), 1)
	ad.zoom = max(ad.zoom, minZoom)
	ad.panX, ad.panY = float64(r.Min.X+r.Max.X)/2, float64(r.Min.Y+r.Max.Y)/2
}

// The zoom's range.
const (
	minZoom = 0.01
	maxZoom = 64
)

// zoomAt changes the zoom keeping the canvas point under (sx, sy) where it is.
func (ad *artDoc) zoomAt(vp gfx.Rect, sx, sy float32, zoom float64) {
	zoom = min(max(zoom, minZoom), maxZoom)
	cx, cy := ad.toCanvas(vp, sx, sy)
	ad.zoom, ad.fit = zoom, false
	ad.panX = cx - (float64(sx)-float64(vp.X+vp.W/2))/zoom
	ad.panY = cy - (float64(sy)-float64(vp.Y+vp.H/2))/zoom
}

// look is the colour the picture shows at canvas pixel (x, y), paper and all.
func (ad *artDoc) look(x, y int) color.RGBA {
	img := image.NewRGBA(image.Rect(x, y, x+1, y+1))
	ad.d.Render(img, img.Rect, 0)
	return img.RGBAAt(x, y)
}

var (
	checkLight = color.RGBA{0x2c, 0x2e, 0x34, 0xff}
	checkDark  = color.RGBA{0x22, 0x24, 0x29, 0xff}
	// pasteboard darkens the canvas beyond a frame, as Photoshop's and Affinity's do.
	pasteboard = color.RGBA{0x0c, 0x0d, 0x10, 0x9c}
)

// drawView brings the viewport's pixels up to date and shows them: all of them when the viewport, zoom or pan
// changed, otherwise only what painting changed. Only what the viewport shows is rendered, from the level of
// detail its zoom needs, so a frame costs about the same whatever the canvas holds.
func (a *App) drawView(ad *artDoc, vp gfx.Rect) {
	u := &a.u
	rp := vp.Pixels()
	if ad.fit && ad.fitFor != vp {
		// Fitted again only when the window changes or a fit is asked for, so drawing never moves the view.
		ad.fitZoom(vp)
		ad.fitFor = vp
	}
	ad.d.View = ad.visible(vp)
	key := viewKey{vp: rp, zoom: ad.zoom, x: ad.panX, y: ad.panY}
	ox, oy := ad.origin(vp)
	ox, oy = ox-float64(rp.Min.X), oy-float64(rp.Min.Y) // canvas origin in the view's pixels
	var area image.Rectangle                            // in view pixels
	switch {
	case ad.view == nil || ad.view.Rect.Size() != rp.Size():
		ad.view = image.NewRGBA(image.Rect(0, 0, rp.Dx(), rp.Dy()))
		ad.viewFor, area = key, ad.view.Rect
	case key != ad.viewFor:
		area = ad.view.Rect
		if key.zoom == ad.viewFor.zoom && key.vp == ad.viewFor.vp {
			// A pan by whole pixels moves what is already drawn and draws only what came into view.
			dx := (ad.viewFor.x - key.x) * key.zoom
			dy := (ad.viewFor.y - key.y) * key.zoom
			if ix, iy := math.Round(dx), math.Round(dy); math.Abs(dx-ix) < 1e-6 && math.Abs(dy-iy) < 1e-6 {
				area = scrollView(ad.view, int(ix), int(iy))
				ad.stale = ad.stale.Union(image.Rectangle{}) // what changed elsewhere is still to draw
			}
		}
		ad.viewFor = key
	}
	if !ad.stale.Empty() {
		s := ad.stale
		area = area.Union(image.Rect(int(math.Floor(ox+float64(s.Min.X)*ad.zoom))-1, int(math.Floor(oy+float64(s.Min.Y)*ad.zoom))-1,
			int(math.Ceil(ox+float64(s.Max.X)*ad.zoom))+1, int(math.Ceil(oy+float64(s.Max.Y)*ad.zoom))+1))
		ad.stale = image.Rectangle{}
	}
	if area = area.Intersect(ad.view.Rect); !area.Empty() {
		a.renderView(ad, area, ox, oy)
	}
	u.c.Blit(ad.view, rp.Min.X, rp.Min.Y, true)
}

// scrollView moves a view's pixels by (dx, dy) and returns the band that came into view, to be drawn.
func scrollView(view *image.RGBA, dx, dy int) image.Rectangle {
	r := view.Rect
	if abs(dx) >= r.Dx() || abs(dy) >= r.Dy() {
		return r
	}
	moved := image.NewRGBA(r)
	draw.Draw(moved, r.Add(image.Pt(dx, dy)), view, r.Min, draw.Src)
	copy(view.Pix, moved.Pix)
	var band image.Rectangle
	switch {
	case dx > 0:
		band = image.Rect(r.Min.X, r.Min.Y, r.Min.X+dx, r.Max.Y)
	case dx < 0:
		band = image.Rect(r.Max.X+dx, r.Min.Y, r.Max.X, r.Max.Y)
	}
	switch {
	case dy > 0:
		band = band.Union(image.Rect(r.Min.X, r.Min.Y, r.Max.X, r.Min.Y+dy))
	case dy < 0:
		band = band.Union(image.Rect(r.Min.X, r.Max.Y+dy, r.Max.X, r.Max.Y))
	}
	return band
}

// renderView draws the view's area (in view pixels; the canvas origin is at ox, oy) from the picture: the
// checkerboard where it is transparent, the video's picture behind a drawing on it, and the pasteboard beyond a
// frame.
func (a *App) renderView(ad *artDoc, area image.Rectangle, ox, oy float64) {
	u := &a.u
	z := ad.zoom
	dst := ad.view.SubImage(area).(*image.RGBA)
	// The checkerboard is fixed to the window, in squares of 8 logical pixels: two rows of it are made, and each row
	// of the area is a copy of one of them.
	cell := max(int(u.px(8)), 4)
	var rows [2][]uint8
	for i := range rows {
		rows[i] = make([]uint8, area.Dx()*4)
		for x := area.Min.X; x < area.Max.X; x++ {
			c := checkLight
			if (x/cell+i)%2 == 1 {
				c = checkDark
			}
			copy(rows[i][(x-area.Min.X)*4:], []uint8{c.R, c.G, c.B, c.A})
		}
	}
	for y := area.Min.Y; y < area.Max.Y; y++ {
		copy(ad.view.Pix[ad.view.PixOffset(area.Min.X, y):][:area.Dx()*4], rows[(y/cell)%2])
	}
	var scaler draw.Interpolator = draw.ApproxBiLinear
	if z >= 1 {
		scaler = draw.NearestNeighbor // pixels stay sharp when zoomed in
	}
	if back := ad.backdrop; back != nil && !ad.noBack {
		// A drawing on the video shows the video's picture behind it.
		paint.Resample(scaler, dst, f64.Aff3{z, 0, ox, 0, z, oy}, back, back.Rect, draw.Src)
	}
	// The canvas at the level of detail the zoom needs: a level pixel is scale view pixels.
	l := paint.LevelFor(z)
	scale := z * float64(int(1)<<l)
	lr := image.Rect(int(math.Floor((float64(area.Min.X)-ox)/scale))-1, int(math.Floor((float64(area.Min.Y)-oy)/scale))-1,
		int(math.Ceil((float64(area.Max.X)-ox)/scale))+1, int(math.Ceil((float64(area.Max.Y)-oy)/scale))+1)
	img := image.NewRGBA(lr)
	ad.d.Render(img, lr, l)
	scaleOver(ad.view, area, img, scale, ox, oy, z >= 1)
	// The frame and the artboards are the pages; beyond them is the pasteboard.
	var pages []image.Rectangle
	toView := func(f image.Rectangle) image.Rectangle {
		return image.Rect(int(math.Round(ox+float64(f.Min.X)*z)), int(math.Round(oy+float64(f.Min.Y)*z)),
			int(math.Round(ox+float64(f.Max.X)*z)), int(math.Round(oy+float64(f.Max.Y)*z)))
	}
	if f := ad.d.Frame; !f.Empty() {
		pages = append(pages, toView(f))
	}
	for _, b := range ad.d.Boards() {
		pages = append(pages, toView(b.Rect))
	}
	if len(pages) > 0 {
		shadeOutside(ad.view, area, pages)
	}
}

// unpremultiplied is the colour of a picture pixel, for the colour picker tool.
func unpremultiplied(c color.RGBA) color.NRGBA {
	if c.A == 0 {
		return color.NRGBA{}
	}
	return color.NRGBAModel.Convert(c).(color.NRGBA)
}
