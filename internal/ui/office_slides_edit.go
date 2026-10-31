package ui

import (
	"bytes"
	"context"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/btelolabs/btelo-forge/internal/gfx"
	"github.com/btelolabs/btelo-forge/internal/office/pptx"
)

// Selection, dragging and the keys that work on shapes.

// What a press on the slide turned into.
const (
	slideActPress = iota
	slideActMove
	slideActResize
	slideActRotate
	slideActMarquee
	slideActPlace
)

// slideAct is a drag under way.
type slideAct struct {
	kind     int
	handle   int // 0 to 7 clockwise from the top left
	x0, y0   float64
	x1, y1   float64
	ids      []int
	boxes    []pptx.Box
	bx       [4]float64 // the chosen shapes' bounds as the drag began
	key      string
	hit      int  // the shape pressed
	wasOnly  bool // it was the only one chosen already, so a click types in it
	additive bool
}

// slideGuide is an alignment line in points; a vertical one when vert is true.
type slideGuide struct {
	vert bool
	at   float64
}

var slideDragCount int

func (e *slidesEditor) newDragKey() string {
	slideDragCount++
	return "drag:" + strconv.Itoa(slideDragCount)
}

// top are the slide's top-level shapes.
func (e *slidesEditor) top() []*pptx.Shape {
	if s := e.slide(); s != nil {
		return e.d.Shapes(s)
	}
	return nil
}

func (e *slidesEditor) chosen() []*pptx.Shape {
	s := e.slide()
	if s == nil {
		return nil
	}
	var out []*pptx.Shape
	for _, id := range e.sel {
		if k := e.d.Find(s, id); k != nil {
			out = append(out, k)
		}
	}
	return out
}

// hitOne reports that a point on the slide lands on the shape, tol points around it.
func hitOne(sh *pptx.Shape, x, y, tol float64, filled bool) bool {
	b := sh.Box
	if !b.Contains(x, y, tol) {
		return false
	}
	lx, ly := b.Local().Inverse().Apply(x, y)
	if sh.Kind == pptx.KindConnector {
		// Distance to the diagonal, which is what most connectors are; bent ones have a box worth pressing.
		if b.W < 1 || b.H < 1 {
			return true
		}
		d := math.Abs(lx*b.H-ly*b.W) / math.Hypot(b.W, b.H)
		return d <= tol*2
	}
	if filled {
		return true
	}
	return lx < tol*2 || ly < tol*2 || lx > b.W-tol*2 || ly > b.H-tol*2
}

// hit is the topmost shape at a point: a group as a whole.
func (e *slidesEditor) hit(x, y float64) *pptx.Shape {
	s := e.slide()
	tol := 4 / e.view.Scale
	var inTree func(list []*pptx.Shape) *pptx.Shape
	inTree = func(list []*pptx.Shape) *pptx.Shape {
		for i := len(list) - 1; i >= 0; i-- {
			k := list[i]
			if k.Hidden {
				continue
			}
			if k.Kind == pptx.KindGroup {
				if inTree(k.Kids) != nil {
					return k
				}
				continue
			}
			fill, _, _ := e.d.Look(s, k)
			filled := fill.A != 0 || k.HasText() || k.EmptyPlaceholder() || k.Kind != pptx.KindShape
			if hitOne(k, x, y, tol, filled) {
				return k
			}
		}
		return nil
	}
	return inTree(e.top())
}

// handlePoints are the handles of the selection in window pixels, clockwise from the top left, and the turning
// handle's place; ok is false with nothing chosen.
func (e *slidesEditor) handlePoints() (pts [8][2]float32, turn [2]float32, single bool, ok bool) {
	list := e.chosen()
	if len(list) == 0 {
		return pts, turn, false, false
	}
	var m pptx.Matrix
	var w, h float64
	if len(list) == 1 {
		b := list[0].Box
		w, h = b.W, b.H
		k := b
		k.FlipH, k.FlipV = false, false
		m = e.view.Matrix().Mul(k.Local())
		single = true
	} else {
		x, y, bw, bh := boundsOf(list)
		w, h = bw, bh
		m = e.view.Matrix().Mul(pptx.Translate(x, y))
	}
	spots := [8][2]float64{{0, 0}, {w / 2, 0}, {w, 0}, {w, h / 2}, {w, h}, {w / 2, h}, {0, h}, {0, h / 2}}
	for i, p := range spots {
		x, y := m.Apply(p[0], p[1])
		pts[i] = [2]float32{float32(x), float32(y)}
	}
	gap := float64(e.a.u.px(26)) / e.view.Scale
	tx, ty := m.Apply(w/2, -gap)
	turn = [2]float32{float32(tx), float32(ty)}
	return pts, turn, single, true
}

func boundsOf(list []*pptx.Shape) (x, y, w, h float64) {
	x0, y0, x1, y1 := math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)
	for _, k := range list {
		bx, by, bw, bh := k.Box.Bounds()
		x0, y0, x1, y1 = min(x0, bx), min(y0, by), max(x1, bx+bw), max(y1, by+bh)
	}
	return x0, y0, x1 - x0, y1 - y0
}

func near(ax, ay, bx, by, r float32) bool {
	dx, dy := ax-bx, ay-by
	return dx*dx+dy*dy <= r*r
}

// handleAt is the handle at a window point: 0 to 7, 8 for turning, -1 for none.
func (e *slidesEditor) handleAt(x, y float32) int {
	pts, turn, single, ok := e.handlePoints()
	if !ok {
		return -1
	}
	r := e.a.u.px(7)
	if single && near(x, y, turn[0], turn[1], r+e.a.u.px(1)) {
		if e.chosen()[0].Kind != pptx.KindTable {
			return 8
		}
	}
	for i, p := range pts {
		if near(x, y, p[0], p[1], r) {
			return i
		}
	}
	return -1
}

func (e *slidesEditor) begin(kind int, x, y float64) *slideAct {
	a := &slideAct{kind: kind, x0: x, y0: y, x1: x, y1: y, key: e.newDragKey(), hit: -1}
	for _, k := range e.chosen() {
		a.ids = append(a.ids, k.ID)
		a.boxes = append(a.boxes, k.Box)
	}
	if len(a.ids) > 0 {
		a.bx[0], a.bx[1], a.bx[2], a.bx[3] = boundsOf(e.chosen())
	}
	e.act = a
	return a
}

// pointer handles the mouse on the slide.
func (e *slidesEditor) pointer(r gfx.Rect) {
	u := &e.a.u
	const id = "office/slides/canvas"
	if u.hover(id, r) && u.wheel != 0 {
		e.wheel(r)
	}
	if e.text != nil && e.textPointer(id, r) {
		return
	}
	if u.hover(id, r) && e.act == nil {
		e.cursorAt(u.mx, u.my)
	}
	if u.rightAt != nil && !u.blocked && r.Contains(u.rightAt.X, u.rightAt.Y) {
		e.contextMenu()
	}
	if u.press(id, r) {
		e.press(u.pressX, u.pressY)
	}
	if e.act != nil && u.dragging(id) {
		sx, sy := e.toSlide(u.mx, u.my)
		e.drag(sx, sy)
	}
	if u.letGo(id) {
		e.release()
	}
}

func (e *slidesEditor) wheel(r gfx.Rect) {
	u := &e.a.u
	if u.ctrl {
		k := e.view.Scale
		next := min(max(k*math.Pow(1.15, float64(u.wheel)), 0.05), 16*float64(u.s))
		ax, ay := e.toSlide(u.mx, u.my)
		e.zoom = next / float64(u.s)
		// Keeping the point under the pointer where it is.
		e.panX = float64(u.mx) - float64(r.X) - 20 - ax*next
		e.panY = float64(u.my) - float64(r.Y) - 20 - ay*next
		e.a.requestFrame()
		return
	}
	if e.d.W*e.view.Scale > float64(r.W) || e.d.H*e.view.Scale > float64(r.H) {
		e.panY += float64(u.wheel) * float64(u.px(60))
		e.a.requestFrame()
		return
	}
	if u.wheel < 0 {
		e.goTo(e.cur + 1)
	} else {
		e.goTo(e.cur - 1)
	}
}

func (e *slidesEditor) cursorAt(x, y float32) {
	u := &e.a.u
	if e.placing != "" {
		u.cursor = CursorCross
		return
	}
	switch h := e.handleAt(x, y); {
	case h == 8:
		u.cursor = CursorHand
	case h == 0 || h == 2 || h == 4 || h == 6:
		u.cursor = CursorResizeCorner
	case h >= 0:
		u.cursor = CursorResize
	default:
		sx, sy := e.toSlide(x, y)
		if k := e.hit(sx, sy); k != nil {
			if k.EmptyPlaceholder() {
				u.cursor = CursorText
			} else {
				u.cursor = CursorMove
			}
		}
	}
}

func (e *slidesEditor) press(px, py float32) {
	u := &e.a.u
	x, y := e.toSlide(px, py)
	if e.placing != "" {
		e.begin(slideActPlace, x, y)
		return
	}
	if !u.double {
		if h := e.handleAt(px, py); h >= 0 {
			if h == 8 {
				e.begin(slideActRotate, x, y)
			} else {
				e.begin(slideActResize, x, y).handle = h
			}
			return
		}
	}
	k := e.hit(x, y)
	if k == nil {
		if !u.shift {
			e.sel = nil
		}
		a := e.begin(slideActMarquee, x, y)
		a.additive = u.shift
		return
	}
	if u.double {
		e.sel = []int{k.ID}
		e.startTextAt(k, x, y)
		return
	}
	in := slices.Contains(e.sel, k.ID)
	only := in && len(e.sel) == 1
	switch {
	case u.shift && in:
		e.sel = slices.DeleteFunc(e.sel, func(v int) bool { return v == k.ID })
	case u.shift:
		e.sel = append(e.sel, k.ID)
	case !in:
		e.sel = []int{k.ID}
	}
	if k.EmptyPlaceholder() && len(e.sel) == 1 {
		e.startTextAt(k, x, y)
		return
	}
	a := e.begin(slideActPress, x, y)
	a.hit, a.wasOnly = k.ID, only && !u.shift
}

func (e *slidesEditor) drag(x, y float64) {
	a := e.act
	u := &e.a.u
	a.x1, a.y1 = x, y
	if a.kind == slideActPress {
		if !u.drag.moved {
			return
		}
		a.kind = slideActMove
		if len(a.ids) == 0 {
			return
		}
	}
	s := e.slide()
	switch a.kind {
	case slideActMove:
		dx, dy := x-a.x0, y-a.y0
		if u.shift {
			if math.Abs(dx) > math.Abs(dy) {
				dy = 0
			} else {
				dx = 0
			}
		}
		dx, dy = e.snap(a, dx, dy)
		boxes := make([]pptx.Box, len(a.boxes))
		for i, b := range a.boxes {
			b.X += dx
			b.Y += dy
			boxes[i] = b
		}
		e.d.SetBox(s, a.key, a.ids, boxes)
	case slideActResize:
		e.resize(a, x, y)
	case slideActRotate:
		b := a.boxes[0]
		cx, cy := b.X+b.W/2, b.Y+b.H/2
		ang := math.Atan2(y-cy, x-cx)*180/math.Pi + 90
		if u.shift {
			ang = math.Round(ang/15) * 15
		}
		b.Rot = math.Mod(ang+360, 360)
		e.d.SetBox(s, a.key, a.ids, []pptx.Box{b})
	}
	e.a.requestFrame()
}

// snap brings a move to the nearest edge or centre of the slide or another shape, and shows the line.
func (e *slidesEditor) snap(a *slideAct, dx, dy float64) (float64, float64) {
	e.guides = e.guides[:0]
	if e.a.u.alt {
		return dx, dy
	}
	tol := 6 / e.view.Scale
	var xs, ys []float64
	xs = append(xs, 0, e.d.W/2, e.d.W)
	ys = append(ys, 0, e.d.H/2, e.d.H)
	for _, k := range e.top() {
		if slices.Contains(a.ids, k.ID) || k.Hidden {
			continue
		}
		x, y, w, h := k.Box.Bounds()
		xs = append(xs, x, x+w/2, x+w)
		ys = append(ys, y, y+h/2, y+h)
	}
	best := func(from []float64, targets []float64, d float64) (float64, float64, bool) {
		out, at, found := d, 0.0, false
		gap := tol
		for _, f := range from {
			for _, t := range targets {
				if g := math.Abs(f + d - t); g < gap {
					gap, out, at, found = g, t-f, t, true
				}
			}
		}
		return out, at, found
	}
	b := a.bx
	nx, gx, okx := best([]float64{b[0], b[0] + b[2]/2, b[0] + b[2]}, xs, dx)
	ny, gy, oky := best([]float64{b[1], b[1] + b[3]/2, b[1] + b[3]}, ys, dy)
	if okx {
		e.guides = append(e.guides, slideGuide{true, gx})
	}
	if oky {
		e.guides = append(e.guides, slideGuide{false, gy})
	}
	return nx, ny
}

// resize stretches the selection by a handle, the opposite side staying where it is.
func (e *slidesEditor) resize(a *slideAct, x, y float64) {
	u := &e.a.u
	s := e.slide()
	h := a.handle
	left, right := h == 0 || h == 6 || h == 7, h == 2 || h == 3 || h == 4
	up, down := h == 0 || h == 1 || h == 2, h == 4 || h == 5 || h == 6
	corner := (left || right) && (up || down)
	const least = 1.0
	if len(a.ids) == 1 {
		b := a.boxes[0]
		// In the box's own turned frame, from its centre.
		lx, ly := pptx.Rotate(-b.Rot).Apply(x-(b.X+b.W/2), y-(b.Y+b.H/2))
		sx, sy := pptx.Rotate(-b.Rot).Apply(a.x0-(b.X+b.W/2), a.y0-(b.Y+b.H/2))
		dx, dy := lx-sx, ly-sy
		w, h2 := b.W, b.H
		if left {
			w -= dx
		}
		if right {
			w += dx
		}
		if up {
			h2 -= dy
		}
		if down {
			h2 += dy
		}
		k := e.d.Find(s, a.ids[0])
		if corner && b.W > 0 && b.H > 0 && (u.shift || k != nil && k.Kind == pptx.KindPicture) {
			f := max(w/b.W, h2/b.H)
			w, h2 = b.W*f, b.H*f
		}
		w, h2 = max(w, least), max(h2, least)
		// The side held still stays still on the slide.
		mx, my := 0.0, 0.0
		if left {
			mx = (b.W - w) / 2
		} else if right {
			mx = (w - b.W) / 2
		}
		if up {
			my = (b.H - h2) / 2
		} else if down {
			my = (h2 - b.H) / 2
		}
		ox, oy := pptx.Rotate(b.Rot).Apply(mx, my)
		cx, cy := b.X+b.W/2+ox, b.Y+b.H/2+oy
		b.X, b.Y, b.W, b.H = cx-w/2, cy-h2/2, w, h2
		e.d.SetBox(s, a.key, a.ids, []pptx.Box{b})
		return
	}
	bx := a.bx
	nx, ny, nw, nh := bx[0], bx[1], bx[2], bx[3]
	dx, dy := x-a.x0, y-a.y0
	if left {
		nx += dx
		nw -= dx
	}
	if right {
		nw += dx
	}
	if up {
		ny += dy
		nh -= dy
	}
	if down {
		nh += dy
	}
	nw, nh = max(nw, least), max(nh, least)
	kx, ky := nw/max(bx[2], 1e-6), nh/max(bx[3], 1e-6)
	boxes := make([]pptx.Box, len(a.boxes))
	for i, b := range a.boxes {
		b.X, b.Y = nx+(b.X-bx[0])*kx, ny+(b.Y-bx[1])*ky
		b.W, b.H = b.W*kx, b.H*ky
		boxes[i] = b
	}
	e.d.SetBox(s, a.key, a.ids, boxes)
}

func (e *slidesEditor) release() {
	a := e.act
	e.act = nil
	e.guides = nil
	if a == nil {
		return
	}
	e.d.EndBurst()
	s := e.slide()
	switch a.kind {
	case slideActPress:
		if a.wasOnly {
			if k := e.d.Find(s, a.hit); k != nil {
				e.startTextAt(k, a.x0, a.y0)
			}
		}
	case slideActMarquee:
		x0, y0 := min(a.x0, a.x1), min(a.y0, a.y1)
		x1, y1 := max(a.x0, a.x1), max(a.y0, a.y1)
		if x1-x0 < 1 && y1-y0 < 1 {
			return
		}
		for _, k := range e.top() {
			x, y, w, h := k.Box.Bounds()
			if !k.Hidden && x >= x0 && y >= y0 && x+w <= x1 && y+h <= y1 && !slices.Contains(e.sel, k.ID) {
				e.sel = append(e.sel, k.ID)
			}
		}
	case slideActPlace:
		x0, y0 := min(a.x0, a.x1), min(a.y0, a.y1)
		w, h := math.Abs(a.x1-a.x0), math.Abs(a.y1-a.y0)
		preset := e.placing
		e.placing = ""
		if w < 4 && h < 4 {
			w, h = 144, 96
			if preset == pptx.ShapeTextBox {
				w, h = 216, 30
			}
		}
		box := pptx.Box{X: x0, Y: y0, W: w, H: h}
		if preset == "line" || strings.Contains(preset, "Connector") {
			box.FlipH, box.FlipV = a.x1 < a.x0, a.y1 < a.y0 // a line runs from where the drag began
		}
		id, err := e.d.AddShape(s, preset, box)
		if err != nil {
			e.a.toast(e.a.describe(err), true)
			return
		}
		e.sel = []int{id}
		if preset == pptx.ShapeTextBox {
			if k := e.d.Find(s, id); k != nil {
				e.beginText(pptx.Target{ID: id}, pptx.Pos{})
			}
		}
	}
}

// drawOverlay draws the selection's frames and handles, and what is being dragged out.
func (e *slidesEditor) drawOverlay() {
	u := &e.a.u
	line := max(1, u.px(1))
	view := e.view.Matrix()
	outline := func(b pptx.Box, col [4]uint8) {
		k := b
		k.FlipH, k.FlipV = false, false
		m := view.Mul(k.Local())
		pts := [4][2]float64{{0, 0}, {b.W, 0}, {b.W, b.H}, {0, b.H}}
		for i := range pts {
			x0, y0 := m.Apply(pts[i][0], pts[i][1])
			x1, y1 := m.Apply(pts[(i+1)%4][0], pts[(i+1)%4][1])
			u.c.Line(float32(x0), float32(y0), float32(x1), float32(y1), line, gfx.Alpha(colAccent, float32(col[3])/255))
		}
	}
	for _, k := range e.chosen() {
		outline(k.Box, [4]uint8{0, 0, 0, 255})
	}
	if e.text == nil {
		if pts, turn, single, ok := e.handlePoints(); ok {
			size := u.px(9)
			if single {
				if k := e.chosen()[0]; k.Kind != pptx.KindTable {
					u.c.Line(pts[1][0], pts[1][1], turn[0], turn[1], line, colAccent)
					u.c.Circle(turn[0], turn[1], u.px(6), colWhite)
					u.c.Circle(turn[0], turn[1], u.px(4.5), colAccent)
				}
			}
			for _, p := range pts {
				r := gfx.Rect{X: p[0] - size/2, Y: p[1] - size/2, W: size, H: size}
				u.c.Fill(r, colWhite)
				u.c.Outline(r, line, colAccent)
			}
		}
	}
	for _, g := range e.guides {
		if g.vert {
			x := float32(g.at*e.view.Scale + e.view.X)
			u.c.Fill(gfx.Rect{X: x, Y: e.area.Y, W: line, H: e.area.H}, colDanger)
		} else {
			y := float32(g.at*e.view.Scale + e.view.Y)
			u.c.Fill(gfx.Rect{X: e.area.X, Y: y, W: e.area.W, H: line}, colDanger)
		}
	}
	if a := e.act; a != nil && (a.kind == slideActMarquee || a.kind == slideActPlace) {
		r := e.box2px(min(a.x0, a.x1), min(a.y0, a.y1), math.Abs(a.x1-a.x0), math.Abs(a.y1-a.y0))
		u.c.Fill(r, gfx.Alpha(colAccent, 0.15))
		u.c.Outline(r, line, colAccent)
	}
	e.drawText()
}

// Keys that work on the slide. The caret's keys are handled in textKeys.
func (e *slidesEditor) editorKeys() {
	u := &e.a.u
	if u.blocked {
		return
	}
	if e.text != nil {
		e.textKeys()
		return
	}
	if u.focus != "" {
		return
	}
	s := e.slide()
	var rest []Key
	for _, k := range u.keys {
		if !e.key(k, s) {
			rest = append(rest, k)
		}
	}
	u.keys = rest
	if len(u.typed) > 0 && s != nil && len(e.sel) == 1 {
		if k := e.chosen(); len(k) == 1 && k[0].Kind == pptx.KindShape {
			text := string(u.typed)
			u.typed = nil
			e.startTextEnd(k[0])
			if e.text != nil {
				e.insertTyped(text)
			}
		}
	}
}

func (e *slidesEditor) key(k Key, s *pptx.Slide) bool {
	const f5 = 0x74
	switch {
	case k.Code == f5 && !k.Ctrl:
		e.startShow(k.Shift)
	case k.Ctrl && k.Code == 'M':
		e.addSlideLike()
	case k.Code == keyPageUp:
		e.goTo(e.cur - 1)
	case k.Code == keyPageDn:
		e.goTo(e.cur + 1)
	case s == nil:
		return false
	case k.Code == keyEscape:
		if e.placing != "" {
			e.placing = ""
		} else if len(e.sel) > 0 {
			e.sel = nil
		} else {
			return false
		}
	case k.Code == keyDelete || k.Code == keyBack:
		if len(e.sel) == 0 {
			return false
		}
		e.deleteChosen()
	case k.Code == keyLeft || k.Code == keyRight || k.Code == keyUp || k.Code == keyDown:
		if len(e.sel) == 0 {
			if k.Code == keyUp || k.Code == keyLeft {
				e.goTo(e.cur - 1)
			} else {
				e.goTo(e.cur + 1)
			}
			return true
		}
		e.nudge(k)
	case k.Ctrl && k.Code == 'A':
		e.sel = e.sel[:0]
		for _, sh := range e.top() {
			e.sel = append(e.sel, sh.ID)
		}
	case k.Ctrl && k.Code == 'C':
		e.copyChosen()
	case k.Ctrl && k.Code == 'X':
		if e.copyChosen() {
			e.deleteChosen()
		}
	case k.Ctrl && k.Code == 'V':
		e.paste()
	case k.Ctrl && k.Code == 'D':
		e.duplicateChosen()
	case k.Ctrl && k.Shift && k.Code == 'G':
		e.ungroup()
	case k.Ctrl && k.Code == 'G':
		e.group()
	case k.Code == keyTab:
		e.cycle(k.Shift)
	case k.Code == keyEnter || k.Code == 0x71: // F2
		if c := e.chosen(); len(c) == 1 {
			e.startTextEnd(c[0])
		} else {
			return false
		}
	default:
		return false
	}
	e.a.requestFrame()
	return true
}

func (e *slidesEditor) cycle(back bool) {
	list := e.top()
	if len(list) == 0 {
		return
	}
	at := -1
	if len(e.sel) > 0 {
		at = slices.IndexFunc(list, func(k *pptx.Shape) bool { return k.ID == e.sel[0] })
	}
	if back {
		at = (at - 1 + len(list)) % len(list)
	} else {
		at = (at + 1) % len(list)
	}
	e.sel = []int{list[at].ID}
}

// nudge moves the chosen shapes a little: a key press is a step, and a burst of presses one undo.
func (e *slidesEditor) nudge(k Key) {
	step := 72.0 / 16
	if k.Ctrl {
		step = 1
	}
	dx, dy := 0.0, 0.0
	switch k.Code {
	case keyLeft:
		dx = -step
	case keyRight:
		dx = step
	case keyUp:
		dy = -step
	case keyDown:
		dy = step
	}
	var ids []int
	var boxes []pptx.Box
	for _, sh := range e.chosen() {
		b := sh.Box
		b.X += dx
		b.Y += dy
		ids, boxes = append(ids, sh.ID), append(boxes, b)
	}
	e.d.SetBox(e.slide(), "nudge", ids, boxes)
}

func (e *slidesEditor) deleteChosen() {
	s := e.slide()
	if s == nil || len(e.sel) == 0 {
		return
	}
	if err := e.d.DeleteShapes(s, e.sel); err != nil {
		e.a.toast(e.a.describe(err), true)
	}
	e.sel = nil
}

// slidesClip holds shapes copied from any presentation in the window; slidesClipMark is what went on the system's
// clipboard with them, so that a paste can tell whether something newer has been copied since.
var (
	slidesClip     *pptx.Clip
	slidesClipMark string
)

func (e *slidesEditor) copyChosen() bool {
	s := e.slide()
	if s == nil || len(e.sel) == 0 {
		return false
	}
	var ids []int
	var words []string
	for _, k := range e.chosen() {
		ids = append(ids, k.ID)
		if t := e.shapeWords(k); t != "" {
			words = append(words, t)
		}
	}
	slidesClip = e.d.Copy(s, ids)
	slidesClipMark = strings.Join(words, "\n")
	if slidesClipMark == "" {
		slidesClipMark = "Btelo Forge shapes"
	}
	if e.a.host != nil {
		e.a.host.SetClipboard(slidesClipMark)
	}
	return true
}

func (e *slidesEditor) shapeWords(k *pptx.Shape) string {
	body, _ := e.d.Body(e.slide(), pptx.Target{ID: k.ID})
	if body == nil {
		return ""
	}
	return strings.ReplaceAll(pptx.PlainText(body), "\v", "\n")
}

// paste takes what the clipboard holds: shapes copied here, or a picture, picture files or words from elsewhere.
func (e *slidesEditor) paste() {
	s := e.slide()
	if s == nil {
		return
	}
	img, files, text := e.a.host.Paste()
	if !slidesClip.Empty() && (text == slidesClipMark || text == "" && img == nil && len(files) == 0) {
		ids, err := e.d.Paste(s, slidesClip, 12, 12)
		if err != nil {
			e.a.toast(e.a.describe(err), true)
			return
		}
		e.sel = ids
		return
	}
	switch {
	case img != nil:
		e.a.background(func(context.Context) {
			var b bytes.Buffer
			err := png.Encode(&b, img)
			e.a.post(func() {
				if err == nil {
					e.addPicture(b.Bytes(), "")
				}
			})
		})
	case len(files) > 0:
		e.dropped(files)
	case text != "":
		c := min(e.d.W/2, e.d.W-20)
		id, err := e.d.AddShape(s, pptx.ShapeTextBox, pptx.Box{X: c - 200, Y: e.d.H/2 - 40, W: 400, H: 40})
		if err != nil {
			return
		}
		e.sel = []int{id}
		e.setWords(pptx.Target{ID: id}, text)
	}
}

// setWords replaces a body's words as a step, and fits it.
func (e *slidesEditor) setWords(t pptx.Target, text string) error {
	s := e.slide()
	err := e.d.Edit("", []*pptx.Slide{s}, false, func() error {
		body, _ := e.d.Body(s, t)
		if body == nil {
			return errSlidesNoWords
		}
		pptx.SetBodyText(body, text)
		e.fit(t)
		return nil
	})
	return err
}

// fit makes a body's box suit its words after they changed, within the step that changed them.
func (e *slidesEditor) fit(t pptx.Target) {
	s := e.slide()
	k := e.d.Find(s, t.ID)
	switch {
	case k == nil:
	case t.Cell:
		e.r.FitTable(s, k)
	default:
		e.r.FitText(s, k)
	}
}

func (e *slidesEditor) addPicture(data []byte, name string) {
	s := e.slide()
	if s == nil {
		return
	}
	id, err := e.d.AddPicture(s, data, pptx.Box{}, name)
	if err != nil {
		e.a.toast(e.a.describe(err), true)
		return
	}
	e.sel = []int{id}
	e.a.requestFrame()
}

// addPictureFile reads a picture away from the window and adds it.
func (e *slidesEditor) addPictureFile(path string) {
	e.a.background(func(context.Context) {
		data, err := os.ReadFile(path)
		e.a.post(func() {
			if err != nil {
				e.a.toast(e.a.describe(err), true)
				return
			}
			if !e.closed {
				e.addPicture(data, filepath.Base(path))
			}
		})
	})
}

// pictureInto asks for a picture and puts it where an empty picture placeholder is.
func (e *slidesEditor) pictureInto(k *pptx.Shape) {
	u := &e.a.u
	e.a.pickFiles(pickRequest{key: "picture", title: u.t("Insert a picture"), kinds: u.t("Pictures"), exts: pickPictures}, func(paths []string) {
		e.fillPicture(k, paths[0])
	})
}

// fillPicture puts the picture in the file at path where the empty placeholder k is.
func (e *slidesEditor) fillPicture(k *pptx.Shape, path string) {
	s := e.slide()
	data, err := os.ReadFile(path)
	if err != nil {
		e.a.toast(e.a.describe(err), true)
		return
	}
	// Fitted inside the placeholder, keeping the picture's shape.
	b := k.Box
	if w, h, ok := pptx.PictureSize(data); ok {
		f := min(b.W/float64(w), b.H/float64(h))
		pw, ph := float64(w)*f, float64(h)*f
		b.X, b.Y, b.W, b.H = b.X+(b.W-pw)/2, b.Y+(b.H-ph)/2, pw, ph
	}
	id, err := e.d.AddPicture(s, data, b, filepath.Base(path))
	if err != nil {
		e.a.toast(e.a.describe(err), true)
		return
	}
	e.d.DeleteShapes(s, []int{k.ID})
	e.sel = []int{id}
}

func (e *slidesEditor) duplicateChosen() {
	s := e.slide()
	if s == nil || len(e.sel) == 0 {
		return
	}
	clip := e.d.Copy(s, e.sel)
	ids, err := e.d.Paste(s, clip, 12, 12)
	if err != nil {
		e.a.toast(e.a.describe(err), true)
		return
	}
	e.sel = ids
}

func (e *slidesEditor) group() {
	if len(e.sel) < 2 {
		return
	}
	id, err := e.d.Group(e.slide(), e.sel)
	if err != nil {
		e.a.toast(e.a.u.t("A group needs at least two shapes that are not placeholders."), true)
		return
	}
	e.sel = []int{id}
}

func (e *slidesEditor) ungroup() {
	var ids []int
	for _, k := range e.chosen() {
		if k.Kind == pptx.KindGroup {
			out, err := e.d.Ungroup(e.slide(), k.ID)
			if err == nil {
				ids = append(ids, out...)
			}
		} else {
			ids = append(ids, k.ID)
		}
	}
	e.sel = ids
}

func (e *slidesEditor) reorder(how pptx.Order) {
	if len(e.sel) > 0 {
		e.d.Reorder(e.slide(), e.sel, how)
	}
}

// align lines the chosen shapes up: by each other, or by the slide when one is chosen. side is one of l, c, r, t, m, b.
func (e *slidesEditor) align(side string) {
	list := e.chosen()
	if len(list) == 0 {
		return
	}
	x, y, w, h := 0.0, 0.0, e.d.W, e.d.H
	if len(list) > 1 {
		x, y, w, h = boundsOf(list)
	}
	var ids []int
	var boxes []pptx.Box
	for _, k := range list {
		b := k.Box
		bx, by, bw, bh := b.Bounds()
		switch side {
		case "l":
			b.X += x - bx
		case "c":
			b.X += x + w/2 - (bx + bw/2)
		case "r":
			b.X += x + w - (bx + bw)
		case "t":
			b.Y += y - by
		case "m":
			b.Y += y + h/2 - (by + bh/2)
		case "b":
			b.Y += y + h - (by + bh)
		}
		ids, boxes = append(ids, k.ID), append(boxes, b)
	}
	e.d.SetBox(e.slide(), "", ids, boxes)
}

// distribute spaces three or more shapes evenly between the outermost two.
func (e *slidesEditor) distribute(across bool) {
	list := e.chosen()
	if len(list) < 3 {
		return
	}
	pos := func(k *pptx.Shape) (float64, float64) {
		x, y, w, h := k.Box.Bounds()
		if across {
			return x, w
		}
		return y, h
	}
	sorted := slices.Clone(list)
	slices.SortStableFunc(sorted, func(a, b *pptx.Shape) int {
		pa, _ := pos(a)
		pb, _ := pos(b)
		return int(math.Copysign(1, pa-pb))
	})
	first, _ := pos(sorted[0])
	lp, ls := pos(sorted[len(sorted)-1])
	used := 0.0
	for _, k := range sorted {
		_, s := pos(k)
		used += s
	}
	gap := (lp + ls - first - used) / float64(len(sorted)-1)
	at := first
	var ids []int
	var boxes []pptx.Box
	for _, k := range sorted {
		p, s := pos(k)
		b := k.Box
		if across {
			b.X += at - p
		} else {
			b.Y += at - p
		}
		at += s + gap
		ids, boxes = append(ids, k.ID), append(boxes, b)
	}
	e.d.SetBox(e.slide(), "", ids, boxes)
}

func (e *slidesEditor) contextMenu() {
	u := &e.a.u
	x, y := e.toSlide(u.rightAt.X, u.rightAt.Y)
	if k := e.hit(x, y); k != nil && !slices.Contains(e.sel, k.ID) {
		e.sel = []int{k.ID}
	}
	has := len(e.sel) > 0
	groups := false
	for _, k := range e.chosen() {
		groups = groups || k.Kind == pptx.KindGroup
	}
	items := []menuItem{
		{label: u.t("Cut"), key: "Ctrl+X", disabled: !has, run: func() {
			if e.copyChosen() {
				e.deleteChosen()
			}
		}},
		{label: u.t("Copy"), key: "Ctrl+C", disabled: !has, run: func() { e.copyChosen() }},
		{label: u.t("Paste"), key: "Ctrl+V", run: e.paste},
		{label: u.t("Duplicate"), key: "Ctrl+D", disabled: !has, run: e.duplicateChosen},
		{label: u.t("Delete"), key: "Del", disabled: !has, run: e.deleteChosen},
		menuSep,
		{label: u.t("Group"), key: "Ctrl+G", disabled: len(e.sel) < 2, run: e.group},
		{label: u.t("Ungroup"), key: "Ctrl+Shift+G", disabled: !groups, run: e.ungroup},
		menuSep,
		{label: u.t("Bring to front"), disabled: !has, run: func() { e.reorder(pptx.ToFront) }},
		{label: u.t("Send to back"), disabled: !has, run: func() { e.reorder(pptx.ToBack) }},
	}
	e.a.openMenu("office/slides/context", gfx.Rect{X: u.rightAt.X, Y: u.rightAt.Y}, items)
}
