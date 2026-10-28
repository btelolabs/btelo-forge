package ui

import (
	"fmt"
	"image"
	"math"
	"strconv"
	"strings"

	"github.com/btelolabs/btelo-forge/internal/gfx"
	"github.com/btelolabs/btelo-forge/internal/paint"
)

const (
	icCrop      = "\ue3be"
	icRotateCW  = "\ue41a"
	icRotateCCW = "\ue419"
	icFlipH     = "\ue3e8"
	icCancel    = "\ue5c9"
	icReset     = "\ue042"
)

// xbox is a box that Free Transform or the crop tool shows handles on: its middle, its size (negative when
// mirrored) and its turn in degrees clockwise, in picture pixels.
type xbox struct{ cx, cy, w, h, angle float64 }

func boxOf(r image.Rectangle) xbox {
	return xbox{cx: float64(r.Min.X+r.Max.X) / 2, cy: float64(r.Min.Y+r.Max.Y) / 2, w: float64(r.Dx()), h: float64(r.Dy())}
}

// axes are the directions of the box's width and height.
func (b xbox) axes() (ex, ey [2]float64) {
	s, c := math.Sincos(b.angle * math.Pi / 180)
	return [2]float64{c, s}, [2]float64{-s, c}
}

// at is a point of the box, with i and j from -1 (left, top) to 1 (right, bottom).
func (b xbox) at(i, j float64) [2]float64 {
	ex, ey := b.axes()
	return [2]float64{b.cx + i*b.w/2*ex[0] + j*b.h/2*ey[0], b.cy + i*b.w/2*ex[1] + j*b.h/2*ey[1]}
}

// inside reports whether p is within the box.
func (b xbox) inside(p [2]float64) bool {
	ex, ey := b.axes()
	dx, dy := p[0]-b.cx, p[1]-b.cy
	return math.Abs(dx*ex[0]+dy*ex[1]) <= math.Abs(b.w)/2 && math.Abs(dx*ey[0]+dy*ey[1]) <= math.Abs(b.h)/2
}

// boxHandles are the handles, clockwise from the top left: the corners and the middles of the sides.
var boxHandles = [8][2]float64{{-1, -1}, {0, -1}, {1, -1}, {1, 0}, {1, 1}, {0, 1}, {-1, 1}, {-1, 0}}

// What a press on a box takes hold of, besides a handle (0 to 7).
const (
	grabNone   = -1
	grabMove   = 8
	grabRotate = 9
	grabNew    = 10 // the crop tool: a new box drawn from the press
)

// atLeast keeps a size at least m across, on the side it is.
func atLeast(v, m float64) float64 {
	if math.Abs(v) >= m {
		return v
	}
	if v < 0 {
		return -m
	}
	return m
}

// resize drags handle h of b to p: the opposite side stays where it is, or the middle with fromCentre, and the
// proportions stay with proportional. Dragging past the opposite side mirrors the box, as in Photoshop.
func (b xbox) resize(h int, p [2]float64, fromCentre, proportional bool) xbox {
	i, j := boxHandles[h][0], boxHandles[h][1]
	ex, ey := b.axes()
	k, anchor := 1.0, b.at(-i, -j)
	if fromCentre {
		k, anchor = 2, [2]float64{b.cx, b.cy}
	}
	vx := (p[0]-anchor[0])*ex[0] + (p[1]-anchor[1])*ex[1]
	vy := (p[0]-anchor[0])*ey[0] + (p[1]-anchor[1])*ey[1]
	out := b
	if proportional && i != 0 && j != 0 {
		// How far along the diagonal from the anchor to the handle the pointer is.
		dx, dy := i*b.w/k, j*b.h/k
		f := (vx*dx + vy*dy) / (dx*dx + dy*dy)
		out.w, out.h = b.w*f, b.h*f
	} else {
		if i != 0 {
			out.w = k * vx * i
		}
		if j != 0 {
			out.h = k * vy * j
		}
		switch {
		case proportional && j == 0:
			out.h = b.h * out.w / b.w
		case proportional && i == 0:
			out.w = b.w * out.h / b.h
		}
	}
	out.w, out.h = atLeast(out.w, 1), atLeast(out.h, 1)
	if !fromCentre {
		out.cx = anchor[0] + i*out.w/2*ex[0] + j*out.h/2*ey[0]
		out.cy = anchor[1] + i*out.w/2*ex[1] + j*out.h/2*ey[1]
	}
	return out
}

// affine maps the rectangle src onto the box.
func (b xbox) affine(src image.Rectangle) paint.Affine {
	c := boxOf(src)
	return paint.Translate(-c.cx, -c.cy).
		Then(paint.Affine{A: b.w / c.w, E: b.h / c.h}).
		Then(paint.Rotate(b.angle, 0, 0)).
		Then(paint.Translate(b.cx, b.cy))
}

// rect is an upright box in whole pixels.
func (b xbox) rect() image.Rectangle {
	w, h := math.Abs(b.w)/2, math.Abs(b.h)/2
	return image.Rect(int(math.Round(b.cx-w)), int(math.Round(b.cy-h)), int(math.Round(b.cx+w)), int(math.Round(b.cy+h)))
}

// toScreen is where a picture point is in the window.
func (ad *artDoc) toScreen(vp gfx.Rect, p [2]float64) (float32, float32) {
	sx, sy := ad.origin(vp)
	return float32(sx + p[0]*ad.zoom), float32(sy + p[1]*ad.zoom)
}

// boxGrab is what a press at the pointer takes hold of: a handle, the inside to move it, or (with rotate) the
// outside to turn it.
func (a *App) boxGrab(ad *artDoc, vp gfx.Rect, b xbox, rotate bool) int {
	u := &a.u
	reach := u.px(8)
	for h, hp := range boxHandles {
		x, y := ad.toScreen(vp, b.at(hp[0], hp[1]))
		if dx, dy := u.mx-x, u.my-y; dx*dx+dy*dy <= reach*reach {
			return h
		}
	}
	switch {
	case b.inside(ad.cursor):
		return grabMove
	case rotate:
		return grabRotate
	}
	return grabNone
}

// grabCursor is the pointer's look over what a press would take hold of.
func grabCursor(g int) Cursor {
	switch g {
	case grabMove:
		return CursorMove
	case grabRotate, grabNone, grabNew:
		return CursorCross
	}
	return CursorResize
}

// drawBox outlines a box with its handles, visible on any picture.
func (a *App) drawBox(ad *artDoc, vp gfx.Rect, b xbox, centre bool) {
	u := &a.u
	var pts [4][2]float32
	for i, c := range [4][2]float64{{-1, -1}, {1, -1}, {1, 1}, {-1, 1}} {
		pts[i][0], pts[i][1] = ad.toScreen(vp, b.at(c[0], c[1]))
	}
	w := max(1, u.px(1))
	for i := range 4 {
		p, q := pts[i], pts[(i+1)%4]
		u.c.Line(p[0], p[1], q[0], q[1], w+2, gfx.Alpha(colBlack, 0.5))
		u.c.Line(p[0], p[1], q[0], q[1], w, colWhite)
	}
	s := u.px(4)
	for _, hp := range boxHandles {
		x, y := ad.toScreen(vp, b.at(hp[0], hp[1]))
		r := gfx.Rect{X: x - s, Y: y - s, W: 2 * s, H: 2 * s}
		u.c.Fill(r.Inset(-w), colBlack)
		u.c.Fill(r, colWhite)
	}
	if centre {
		x, y := ad.toScreen(vp, [2]float64{b.cx, b.cy})
		u.c.RoundOutline(gfx.Rect{X: x - s, Y: y - s, W: 2 * s, H: 2 * s}, s, w, colWhite)
		u.c.Line(x-s*1.6, y, x+s*1.6, y, w, colWhite)
		u.c.Line(x, y-s*1.6, x, y+s*1.6, w, colWhite)
	}
}

// ---- Free Transform

// transformEdit is Free Transform under way: the chosen layer's content, or the selected part, shown in a box
// whose handles scale, turn and move it until it is applied or cancelled.
type transformEdit struct {
	s      *paint.TransformSession
	src    image.Rectangle // the content before transforming
	box    xbox
	shown  xbox // what the picture shows
	start  xbox // the box when the drag began
	grab   int
	from   [2]float64 // where the drag began, in picture pixels
	fields map[string]string

	mode      int           // xfFree, or Skew, Distort, Perspective or Warp
	quad      [4][2]float64 // their quadrilateral: top left, top right, bottom right, bottom left
	warp      [16][2]float64
	shownQuad [4][2]float64 // what the picture shows
	shownWarp [16][2]float64
	pt        int // the handle being dragged, or -1 for the whole shape
	startQuad [4][2]float64
	startWarp [16][2]float64
}

// beginTransform starts Free Transform on the chosen layer, as Ctrl+T does.
func (a *App) beginTransform(ad *artDoc) {
	if ad.xf != nil || ad.stroke != nil || ad.move != nil {
		return
	}
	s, err := ad.d.BeginTransform()
	if err != nil {
		a.toast(a.describe(err), true)
		return
	}
	b := boxOf(s.Bounds())
	ad.xf = &transformEdit{s: s, src: s.Bounds(), box: b, shown: b, grab: grabNone, fields: map[string]string{}, pt: -1}
	ad.selDrag, ad.lasso = false, nil
}

// applyTransform records the transform as it stands.
func (a *App) applyTransform(ad *artDoc) {
	if ad.xf == nil {
		return
	}
	a.showTransform(ad)
	ad.xf.s.Apply() // at full quality
	ad.xf = nil
	ad.changedAll()
}

// cancelTransform puts the layer back as it was.
func (a *App) cancelTransform(ad *artDoc) {
	if ad.xf == nil {
		return
	}
	ad.xf.s.Cancel()
	ad.xf = nil
	ad.refresh()
}

// transformPointer drags the transform's handles: corners scale in proportion (Shift frees them), sides stretch
// (Shift keeps the proportions), Alt scales about the middle, the inside moves and the outside turns, by 15° steps
// with Shift; a double click inside applies it.
func (a *App) transformPointer(ad *artDoc, vp gfx.Rect) {
	u := &a.u
	const id = "art/view"
	xf := ad.xf
	if xf.mode != xfFree {
		a.meshPointer(ad, vp)
		return
	}
	if u.hover(id, vp) {
		u.cursor = grabCursor(a.boxGrab(ad, vp, xf.box, true))
	}
	if u.press(id, vp) {
		g := a.boxGrab(ad, vp, xf.box, true)
		if corner := map[int]int{0: 0, 2: 1, 4: 2, 6: 3}; u.ctrl && g >= 0 && g < len(boxHandles) {
			if i, ok := corner[g]; ok {
				// Ctrl on a corner distorts, as in Photoshop.
				a.setTransformMode(ad, xfDistort)
				xf.pt, xf.from, xf.startQuad = i, ad.cursor, xf.quad
				return
			}
		}
		if g == grabMove && u.double {
			u.active = ""
			a.applyTransform(ad)
			return
		}
		xf.grab, xf.start, xf.from = g, xf.box, ad.cursor
	}
	if u.dragging(id) {
		p := ad.cursor
		switch g := xf.grab; g {
		case grabMove:
			xf.box.cx, xf.box.cy = xf.start.cx+p[0]-xf.from[0], xf.start.cy+p[1]-xf.from[1]
		case grabRotate:
			turn := math.Atan2(p[1]-xf.start.cy, p[0]-xf.start.cx) - math.Atan2(xf.from[1]-xf.start.cy, xf.from[0]-xf.start.cx)
			angle := xf.start.angle + turn*180/math.Pi
			if u.shift {
				angle = math.Round(angle/15) * 15
			}
			xf.box.angle = math.Remainder(angle, 360)
		case grabNone:
		default:
			corner := boxHandles[g][0] != 0 && boxHandles[g][1] != 0
			xf.box = xf.start.resize(g, p, u.alt, corner != u.shift)
		}
	}
	if u.letGo(id) {
		xf.grab = grabNone
	}
	a.showTransform(ad)
}

// showTransform brings the picture up to date with the transform's box.
func (a *App) showTransform(ad *artDoc) {
	xf := ad.xf
	if xf != nil && xf.mode != xfFree {
		if xf.mode == xfWarp && xf.warp == xf.shownWarp || xf.mode != xfWarp && xf.quad == xf.shownQuad {
			return
		}
		xf.shownQuad, xf.shownWarp = xf.quad, xf.warp
		changed, err := xf.s.SetMesh(xf.meshOf(), xfSteps[xf.mode])
		if err != nil {
			a.toast(a.describe(err), true)
			return
		}
		ad.touched(changed)
		return
	}
	if xf == nil || xf.box == xf.shown {
		return
	}
	xf.shown = xf.box
	ad.touched(xf.s.Set(xf.box.affine(xf.src), true))
}

// transformKeys are the keys of Free Transform: Enter applies it, Esc cancels it and the arrows nudge it.
func (a *App) transformKeys(ad *artDoc, k Key) bool {
	xf := ad.xf
	step := 1.0
	if k.Shift {
		step = 10
	}
	switch k.Code {
	case keyEnter:
		a.applyTransform(ad)
	case keyEscape:
		a.cancelTransform(ad)
	case keyLeft, keyRight, keyUp, keyDown:
		dx, dy := 0.0, 0.0
		switch k.Code {
		case keyLeft:
			dx = -step
		case keyRight:
			dx = step
		case keyUp:
			dy = -step
		default:
			dy = step
		}
		if xf.mode == xfFree {
			xf.box.cx, xf.box.cy = xf.box.cx+dx, xf.box.cy+dy
		} else {
			xf.pt, xf.startQuad, xf.startWarp = -1, xf.quad, xf.warp
			a.dragMeshPoint(ad, dx, dy)
		}
	default:
		return false
	}
	a.showTransform(ad)
	return true
}

// numberField is a labelled number box in an options bar; typing a number sets it at once. It returns where the
// next field goes.
func (a *App) numberField(id string, x, y, h float32, label, unit string, typed map[string]string, value float64, set func(float64)) float32 {
	u := &a.u
	lw := u.face(12).Measure(label) + u.px(6)
	u.label(u.face(12), gfx.Rect{X: x, Y: y, W: lw, H: h}, label, colDim, 0)
	x += lw
	text := typed[id]
	if u.focus != id {
		text = strconv.FormatFloat(math.Round(value*10)/10, 'f', -1, 64)
	}
	changed, _ := u.textBox(id, gfx.Rect{X: x, Y: y, W: u.px(70), H: h}, &text, "", false)
	typed[id] = text
	if v, err := strconv.ParseFloat(strings.TrimSpace(text), 64); changed && err == nil && !math.IsInf(v, 0) {
		set(v)
	}
	x += u.px(72)
	if unit != "" {
		u.label(u.face(11.5), gfx.Rect{X: x, Y: y, W: u.px(22), H: h}, unit, colDim, 0)
		x += u.px(22)
	}
	return x + u.px(8)
}

// drawTransformOptions is the options bar during Free Transform: where the box is, its scale and turn, flips, and
// cancel and apply as in Photoshop.
func (a *App) drawTransformOptions(ad *artDoc, r gfx.Rect) {
	u := &a.u
	xf := ad.xf
	b := &xf.box
	x := a.drawTransformModes(ad, r.X, r)
	if xf.mode != xfFree {
		u.label(u.face(12), gfx.Rect{X: x, Y: r.Y, W: r.Right() - x - u.px(76), H: r.H},
			u.t("Drag the handles; drag elsewhere to move it all; Enter applies, Esc cancels"), colDim, 0)
	}
	scaleW, scaleH := 100*b.w/float64(xf.src.Dx()), 100*b.h/float64(xf.src.Dy())
	if xf.mode == xfFree {
		x = a.transformFields(ad, x, r, scaleW, scaleH)
	}
	if u.iconButton("art/xf/cancel", gfx.Rect{X: r.Right() - u.px(66), Y: r.Y, W: u.px(30), H: r.H}, icCancel, withKey(u.t("Cancel the transform"), "Esc"), false, true) {
		a.cancelTransform(ad)
		return
	}
	if u.iconButton("art/xf/apply", gfx.Rect{X: r.Right() - u.px(32), Y: r.Y, W: u.px(30), H: r.H}, icCheck, withKey(u.t("Apply the transform"), "Enter"), true, true) {
		a.applyTransform(ad)
		return
	}
	a.showTransform(ad)
}

// transformFields are Free Transform's numbers and turns; it returns where the next control goes.
func (a *App) transformFields(ad *artDoc, x float32, r gfx.Rect, scaleW, scaleH float64) float32 {
	u := &a.u
	xf := ad.xf
	b := &xf.box
	x = a.numberField("art/xf/x", x, r.Y, r.H, "X", "px", xf.fields, b.cx, func(v float64) { b.cx = v })
	x = a.numberField("art/xf/y", x, r.Y, r.H, "Y", "px", xf.fields, b.cy, func(v float64) { b.cy = v })
	x = a.numberField("art/xf/w", x, r.Y, r.H, u.t("W"), "%", xf.fields, scaleW, func(v float64) {
		if v != 0 {
			b.w = atLeast(v/100*float64(xf.src.Dx()), 1)
		}
	})
	x = a.numberField("art/xf/h", x, r.Y, r.H, u.t("H"), "%", xf.fields, scaleH, func(v float64) {
		if v != 0 {
			b.h = atLeast(v/100*float64(xf.src.Dy()), 1)
		}
	})
	x = a.numberField("art/xf/angle", x, r.Y, r.H, u.t("Angle"), "°", xf.fields, b.angle, func(v float64) { b.angle = math.Remainder(v, 360) })
	for _, c := range []struct {
		id, glyph, hint string
		run             func()
	}{
		{"fliph", icFlipH, u.t("Flip horizontal"), func() { b.w = -b.w }},
		{"rotcw", icRotateCW, u.t("Rotate 90° clockwise"), func() { b.angle = math.Remainder(b.angle+90, 360) }},
		{"rotccw", icRotateCCW, u.t("Rotate 90° counter clockwise"), func() { b.angle = math.Remainder(b.angle-90, 360) }},
	} {
		if x+u.px(32) > r.Right()-u.px(72) {
			break
		}
		if u.iconButton("art/xf/"+c.id, gfx.Rect{X: x, Y: r.Y, W: u.px(30), H: r.H}, c.glyph, c.hint, false, true) {
			c.run()
		}
		x += u.px(32)
	}
	return x
}

// ---- The crop tool

// cropRatios are the crop tool's proportions; 0 is free and -1 the picture's own.
var (
	cropRatios     = []float64{0, -1, 1, 4.0 / 3, 3.0 / 2, 16.0 / 9, 9.0 / 16}
	cropRatioNames = msgs("Free", "Original ratio", "1:1 (square)", "4:3", "3:2", "16:9", "9:16")
)

// cropEdit is the crop tool's box, which starts on the whole picture.
type cropEdit struct {
	box, start xbox
	grab       int
	from       [2]float64
	frame      image.Rectangle // the frame the box was made for
}

// cropRatio is the proportion the crop box keeps, or 0.
func (a *App) cropRatio(ad *artDoc) float64 {
	i := a.settings.Paint.CropRatio
	if i <= 0 || i >= len(cropRatios) {
		return 0
	}
	if cropRatios[i] < 0 {
		e := cropExtent(ad)
		return float64(e.Dx()) / float64(e.Dy())
	}
	return cropRatios[i]
}

// cropExtent is what the crop box starts on: the frame, or else what is painted, or else what is on screen.
func cropExtent(ad *artDoc) image.Rectangle {
	if r := ad.d.Frame; !r.Empty() {
		return r
	}
	if r := ad.d.Bounds(); !r.Empty() {
		return r
	}
	return ad.d.View
}

// syncCrop gives the crop tool its box: the frame, again whenever the frame changed.
func (a *App) syncCrop(ad *artDoc) {
	if ad.crop != nil && ad.crop.frame == ad.d.Frame {
		return
	}
	ad.crop = &cropEdit{box: boxOf(cropExtent(ad)), grab: grabNone, frame: ad.d.Frame}
	a.fitCropRatio(ad)
}

// fitCropRatio shrinks the crop box about its middle to the chosen proportion.
func (a *App) fitCropRatio(ad *artDoc) {
	ratio := a.cropRatio(ad)
	if ratio == 0 || ad.crop == nil {
		return
	}
	b := &ad.crop.box
	if math.Abs(b.w)/math.Abs(b.h) > ratio {
		b.w = math.Copysign(math.Abs(b.h)*ratio, b.w)
	} else {
		b.h = math.Copysign(math.Abs(b.w)/ratio, b.h)
	}
}

// cropPointer drags the crop box: its handles, the inside to move it, or a new box from outside it; a double
// click inside crops.
func (a *App) cropPointer(ad *artDoc, vp gfx.Rect) {
	u := &a.u
	const id = "art/view"
	c := ad.crop
	ratio := a.cropRatio(ad)
	if u.hover(id, vp) {
		u.cursor = grabCursor(a.boxGrab(ad, vp, c.box, false))
	}
	if u.press(id, vp) {
		g := a.boxGrab(ad, vp, c.box, false)
		if g == grabMove && u.double {
			u.active = ""
			a.applyCrop(ad)
			return
		}
		if g == grabNone {
			g = grabNew
		}
		c.grab, c.start, c.from = g, c.box, ad.cursor
	}
	if u.dragging(id) {
		p := ad.cursor
		switch g := c.grab; g {
		case grabMove:
			c.box.cx, c.box.cy = c.start.cx+p[0]-c.from[0], c.start.cy+p[1]-c.from[1]
		case grabNew:
			w, h := p[0]-c.from[0], p[1]-c.from[1]
			if ratio > 0 {
				h = math.Copysign(math.Abs(w)/ratio, h)
			} else if u.shift {
				h = math.Copysign(math.Abs(w), h)
			}
			c.box = xbox{cx: c.from[0] + w/2, cy: c.from[1] + h/2, w: atLeast(math.Abs(w), 1), h: atLeast(math.Abs(h), 1)}
		case grabNone:
		default:
			corner := boxHandles[g][0] != 0 && boxHandles[g][1] != 0
			b := c.start.resize(g, p, u.alt, ratio > 0 || corner && u.shift)
			b.w, b.h = math.Abs(b.w), math.Abs(b.h) // a crop box is never mirrored
			c.box = b
		}
	}
	if u.letGo(id) {
		if c.grab == grabNew && !u.drag.moved {
			c.box = c.start // a click outside the box leaves it
		}
		c.grab = grabNone
	}
}

// applyCrop crops the picture to the box.
func (a *App) applyCrop(ad *artDoc) {
	if ad.crop == nil {
		return
	}
	r := ad.crop.box.rect()
	if r == ad.d.Frame {
		return
	}
	a.artRun(ad, func() error { return ad.d.Crop(r, !a.settings.Paint.CropKeep) })
	ad.crop = nil
	ad.fitView()
}

// cropKeys are the crop tool's keys: Enter crops, Esc puts the box back on the whole picture.
func (a *App) cropKeys(ad *artDoc, k Key) bool {
	switch k.Code {
	case keyEnter:
		a.applyCrop(ad)
	case keyEscape:
		ad.crop = nil
	default:
		return false
	}
	return true
}

// drawCropOverlay darkens what the crop will remove and shows the box with a rule of thirds.
func (a *App) drawCropOverlay(ad *artDoc, vp gfx.Rect) {
	u := &a.u
	b := ad.crop.box
	x0, y0 := ad.toScreen(vp, b.at(-1, -1))
	x1, y1 := ad.toScreen(vp, b.at(1, 1))
	x0, x1 = min(x0, x1), max(x0, x1)
	y0, y1 = min(y0, y1), max(y0, y1)
	shade := gfx.Alpha(colBlack, 0.55)
	u.c.Fill(gfx.Rect{X: vp.X, Y: vp.Y, W: vp.W, H: max(0, y0-vp.Y)}, shade)
	u.c.Fill(gfx.Rect{X: vp.X, Y: y1, W: vp.W, H: max(0, vp.Bottom()-y1)}, shade)
	u.c.Fill(gfx.Rect{X: vp.X, Y: y0, W: max(0, x0-vp.X), H: y1 - y0}, shade)
	u.c.Fill(gfx.Rect{X: x1, Y: y0, W: max(0, vp.Right()-x1), H: y1 - y0}, shade)
	third := gfx.Alpha(colWhite, 0.35)
	for i := float32(1); i < 3; i++ {
		u.c.Fill(gfx.Rect{X: x0 + (x1-x0)*i/3, Y: y0, W: max(1, u.px(1)), H: y1 - y0}, third)
		u.c.Fill(gfx.Rect{X: x0, Y: y0 + (y1-y0)*i/3, W: x1 - x0, H: max(1, u.px(1))}, third)
	}
	a.drawBox(ad, vp, b, false)
}

// drawCropOptions is the crop tool's options bar: the proportion, the size, whether cropped pixels are deleted,
// and reset, cancel and crop.
func (a *App) drawCropOptions(ad *artDoc, r gfx.Rect) {
	u := &a.u
	p := &a.settings.Paint
	x := r.X
	a.dropdown("art/crop/ratio", gfx.Rect{X: x, Y: r.Y, W: u.px(140), H: r.H}, u.ts(cropRatioNames), p.CropRatio, nil, func(i int) {
		p.CropRatio = i
		a.saveSettings()
		a.fitCropRatio(ad)
	})
	x += u.px(150)
	cr := ad.crop.box.rect()
	u.label(u.face(12), gfx.Rect{X: x, Y: r.Y, W: u.px(120), H: r.H}, fmt.Sprintf("%d × %d px", cr.Dx(), cr.Dy()), colText, 0)
	x += u.px(124)
	if on := u.checkbox("art/crop/delete", gfx.Rect{X: x, Y: r.Y, W: u.px(190), H: r.H}, u.t("Delete cropped pixels"), !p.CropKeep); on == p.CropKeep {
		p.CropKeep = !on
		a.saveSettings()
	}
	if u.iconButton("art/crop/reset", gfx.Rect{X: r.Right() - u.px(100), Y: r.Y, W: u.px(30), H: r.H}, icReset, u.t("Reset the crop box"), false, true) {
		ad.crop = nil
	}
	if u.iconButton("art/crop/cancel", gfx.Rect{X: r.Right() - u.px(66), Y: r.Y, W: u.px(30), H: r.H}, icCancel, withKey(u.t("Cancel the crop"), "Esc"), false, true) {
		ad.crop = nil
	}
	if u.iconButton("art/crop/apply", gfx.Rect{X: r.Right() - u.px(32), Y: r.Y, W: u.px(30), H: r.H}, icCheck, withKey(u.t("Crop"), "Enter"), true, true) {
		a.applyCrop(ad)
	}
}

// ---- The Image menu's dialogs and Select › Feather

const (
	dialogNone = iota
	dialogFeather
	dialogImageSize
	dialogCanvasSize
	dialogRotate
)

// artDialog is the canvas editor's open dialog, which asks for one or two numbers.
type artDialog struct {
	kind   int
	a, b   string  // the numbers as typed
	keep   bool    // Image Size: keep the proportions
	ratio  float64 // the proportions kept
	anchor int     // Canvas Size: where the picture stays, 0 to 8 from the top left
}

func (a *App) openDialog(kind int) {
	ad := a.art
	if ad == nil {
		return
	}
	e := ad.d.Extent()
	if e.Empty() {
		e = ad.d.View
	}
	f := artDialog{kind: kind, keep: true, anchor: 4, ratio: float64(e.Dx()) / float64(max(e.Dy(), 1))}
	switch kind {
	case dialogFeather:
		f.a = strconv.Itoa(int(max(a.settings.Paint.Feather, 5)))
	case dialogImageSize, dialogCanvasSize:
		f.a, f.b = strconv.Itoa(e.Dx()), strconv.Itoa(e.Dy())
	case dialogRotate:
		f.a = "0"
	}
	a.dialog = f
}

// dialogRow is a labelled number box of a dialog; it reports a change, and Enter pressed in it.
func (a *App) dialogRow(id string, in gfx.Rect, y float32, label, unit string, value *string) (changed, entered bool) {
	u := &a.u
	enter := false
	if u.focus == id {
		for _, k := range u.keys {
			enter = enter || k.Code == keyEnter
		}
	}
	u.label(u.face(12.5), gfx.Rect{X: in.X, Y: y, W: u.px(110), H: u.px(32)}, label, colDim, 0)
	changed, done := u.textBox(id, gfx.Rect{X: in.X + u.px(116), Y: y, W: u.px(90), H: u.px(32)}, value, "", false)
	u.label(u.face(12), gfx.Rect{X: in.X + u.px(214), Y: y, W: u.px(100), H: u.px(32)}, unit, colDim, 0)
	return changed, done && enter
}

func number(s string) (float64, bool) {
	v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	return v, err == nil && !math.IsNaN(v) && !math.IsInf(v, 0)
}

func (a *App) drawArtDialog() {
	f := &a.dialog
	ad := a.art
	if f.kind == dialogNone || ad == nil {
		f.kind = dialogNone
		return
	}
	u := &a.u
	heights := map[int]float32{dialogFeather: 170, dialogImageSize: 250, dialogCanvasSize: 300, dialogRotate: 170}
	titles := map[int]string{dialogFeather: u.t("Feather selection"), dialogImageSize: u.t("Image size"),
		dialogCanvasSize: u.t("Canvas size"), dialogRotate: u.t("Rotate canvas")}
	u.c.Fill(a.screen, gfxShade)
	box := rectCentered(a.screen, u.px(360), u.px(heights[f.kind]))
	panel(u, box)
	in := box.Inset(u.px(22))
	u.label(u.bold(16), gfx.Rect{X: in.X, Y: in.Y, W: in.W, H: u.px(24)}, titles[f.kind], colText, 0)
	y := in.Y + u.px(40)
	var valid, entered bool
	var run func() error
	switch f.kind {
	case dialogFeather:
		_, entered = a.dialogRow("dialog/a", in, y, u.t("Feather radius"), u.t("pixels"), &f.a)
		v, ok := number(f.a)
		valid = ok && v > 0 && v <= 1000
		run = func() error { ad.d.FeatherSelection(v); return nil }
	case dialogImageSize, dialogCanvasSize:
		changedW, enterW := a.dialogRow("dialog/a", in, y, u.t("Width"), u.t("pixels"), &f.a)
		changedH, enterH := a.dialogRow("dialog/b", in, y+u.px(40), u.t("Height"), u.t("pixels"), &f.b)
		entered = enterW || enterH
		y += u.px(84)
		if f.kind == dialogImageSize {
			f.keep = u.checkbox("dialog/keep", gfx.Rect{X: in.X, Y: y, W: in.W, H: u.px(28)}, u.t("Keep the proportions"), f.keep)
			if v, ok := number(f.a); f.keep && changedW && ok {
				f.b = strconv.Itoa(max(1, int(math.Round(v/f.ratio))))
			}
			if v, ok := number(f.b); f.keep && changedH && ok {
				f.a = strconv.Itoa(max(1, int(math.Round(v*f.ratio))))
			}
		} else {
			a.drawAnchor(gfx.Rect{X: in.X + u.px(116), Y: y, W: u.px(84), H: u.px(84)})
			u.label(u.face(12.5), gfx.Rect{X: in.X, Y: y, W: u.px(110), H: u.px(28)}, u.t("Anchor"), colDim, 0)
		}
		w, okW := number(f.a)
		h, okH := number(f.b)
		valid = okW && okH && w >= 1 && h >= 1 && w <= 16384 && h <= 16384 && w == math.Trunc(w) && h == math.Trunc(h)
		if f.kind == dialogImageSize {
			run = func() error { return ad.d.ResizeImage(int(w), int(h)) }
		} else {
			ax, ay := float64(f.anchor%3)/2, float64(f.anchor/3)/2
			run = func() error { return ad.d.ResizeCanvas(int(w), int(h), ax, ay) }
		}
	case dialogRotate:
		_, entered = a.dialogRow("dialog/a", in, y, u.t("Angle"), u.t("° clockwise"), &f.a)
		v, ok := number(f.a)
		valid = ok && v >= -360 && v <= 360
		run = func() error { return ad.d.RotateCanvasBy(v) }
	}
	foot := in.Bottom() - u.px(34)
	if !valid {
		u.label(u.face(12), gfx.Rect{X: in.X, Y: foot, W: in.W - u.px(214), H: u.px(34)}, u.t("Check the numbers"), colWarning, 0)
	}
	if u.button("dialog/cancel", gfx.Rect{X: in.Right() - u.px(210), Y: foot, W: u.px(100), H: u.px(34)}, "", u.t("Cancel"), ghost, true) {
		f.kind = dialogNone
	}
	if u.button("dialog/ok", gfx.Rect{X: in.Right() - u.px(100), Y: foot, W: u.px(100), H: u.px(34)}, "", u.t("OK"), primary, valid) || entered && valid {
		resized := f.kind != dialogFeather
		f.kind = dialogNone
		a.artRun(ad, run)
		if resized {
			ad.fitView()
		}
	}
	for _, k := range u.keys {
		if k.Code == keyEscape {
			f.kind = dialogNone
		}
	}
	u.keys = nil
}

// drawAnchor is Canvas Size's anchor: nine squares, the chosen one where the picture stays.
func (a *App) drawAnchor(r gfx.Rect) {
	u := &a.u
	f := &a.dialog
	cell := r.W / 3
	for i := range 9 {
		c := gfx.Rect{X: r.X + float32(i%3)*cell, Y: r.Y + float32(i/3)*cell, W: cell - u.px(3), H: cell - u.px(3)}
		id := fmt.Sprintf("dialog/anchor/%d", i)
		bg := colSurface
		switch {
		case i == f.anchor:
			bg = colAccent
		case u.hover(id, c):
			bg = colSurfaceHi
		}
		u.c.RoundRect(c, u.px(4), bg)
		if u.clicked(id, c) {
			f.anchor = i
		}
	}
}
