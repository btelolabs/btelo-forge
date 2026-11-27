package ui

import (
	"fmt"
	"image"
	"image/color"
	"math"
	"runtime"
	"sync"

	"golang.org/x/image/draw"

	"github.com/btelolabs/btelo-forge/internal/capture"
	"github.com/btelolabs/btelo-forge/internal/gfx"
)

// ---- Picking a region

// pickPurpose is what a region is picked for, which the bar at the top of every display says with its own symbol,
// colour and words, so a screenshot is not mistaken for a recording.
type pickPurpose struct {
	glyph string
	hue   color.RGBA // the symbol and the outline of the region
	title string
	hint  string
}

// pickOptions customize region picking: frozen desktop snapshots (for flicker-free, popup-preserving screenshots)
// and desktop window/popup rectangles for single-click hover auto-snap.
type pickOptions struct {
	snaps   map[string]*image.RGBA // full-display snapshots keyed by Display.Key(); nil for live overlay
	dimmed  map[string]*image.RGBA // pre-baked darkened copy of snaps[key]; computed lazily if missing
	windows []capture.WindowRect   // front-to-back desktop physical rects for hover auto-snap
}

// recordPick is the purpose of picking the region to record.
func (a *App) recordPick() pickPurpose {
	u := &a.u
	return pickPurpose{glyph: icRecord, hue: colDanger, title: u.t("Record the screen"),
		hint: u.t("Drag to choose the region to record · click to record the whole screen · Esc cancels")}
}

// shotPick is the purpose of picking the region of a screenshot going to a target.
func (a *App) shotPick(to shotTarget) pickPurpose {
	u := &a.u
	title := u.t("Screenshot onto the clipboard")
	switch to {
	case shotFile:
		title = u.t("Screenshot into the pictures folder")
	case shotPath:
		title = u.t("Screenshot, copying its path")
	}
	return pickPurpose{glyph: icCamera, hue: colAccent, title: title,
		hint: u.t("Drag to select · click a window or screen · Tab toggles · C copies colour · Esc cancels")}
}

// pickHandle identifies an active resize handle or move drag in Alt-release adjust mode.
type pickHandle int

const (
	handleNone pickHandle = iota
	handleMove
	handleNW
	handleN
	handleNE
	handleE
	handleSE
	handleS
	handleSW
	handleW
)

// pickBarKind tracks which top bar (if any) is currently drawn on a frozen pickScreen, so mouse moves away from
// the bar avoid including the bar in the dirty-rect upload.
type pickBarKind int

const (
	barNone pickBarKind = iota
	barIdle
	barAdjust
)

// regionPick dims every display and lets the user drag out a region of one of them, or single-click a hovered
// window/popup (when snap targets are provided) or a whole display.
type regionPick struct {
	screens   []pickScreen
	what      pickPurpose
	windows   []capture.WindowRect
	on        int // the display the region is being dragged or adjusted on
	from, to  image.Point
	dragging  bool
	spacePan  bool        // Space held during drag: move box without resizing
	panLast   image.Point // previous cursor position while panning
	adjusting bool        // Alt held on mouse-up: 8 resize handles + drag-to-move + arrow nudge
	adjRect   image.Rectangle
	adjDrag   pickHandle
	adjStart  image.Point
	adjOrigin image.Rectangle
	snapOff   bool // Tab toggles hover window/popup auto-snap off/on
	done      func(d capture.Display, region image.Rectangle, ok bool)
}

// pickScreen is the picker's window over one display.
type pickScreen struct {
	s          *surface
	d          capture.Display
	snap       *image.RGBA
	dimmed     *image.RGBA
	hovered    image.Rectangle // display-local auto-snapped window/popup rect, or empty
	hoverTitle string
	baseDrawn  bool            // dimmed backdrop has been copied to s.c.Img
	barKind    pickBarKind     // top bar currently on s.c.Img
	barRect    image.Rectangle // bounds of the top bar currently on s.c.Img
	prevDirty  image.Rectangle // union of dynamic elements drawn on the last frame
}

// pickRegion lets the user choose part of any display over a live transparent overlay: a drag picks a rectangle of
// the display it starts on, a click that whole display, Esc or the right button cancels. Without overlays it picks
// the whole chosen display at once.
func (a *App) pickRegion(what pickPurpose, done func(d capture.Display, region image.Rectangle, ok bool)) {
	a.pickRegionWith(what, pickOptions{}, done)
}

// pickRegionWith opens the region picker with optional frozen snapshots and window auto-snap targets.
func (a *App) pickRegionWith(what pickPurpose, opts pickOptions, done func(d capture.Display, region image.Rectangle, ok bool)) {
	sc := &a.over
	if sc.pick != nil {
		return
	}
	focus, ok := a.pointerDisplay()
	if !ok {
		done(capture.Display{}, image.Rectangle{}, false)
		return
	}
	// The display under the pointer (or the chosen display) opens last, so its window is in front with the keyboard.
	order := make([]capture.Display, 0, len(a.rec.displays))
	for _, d := range a.rec.displays {
		if d.Key() != focus.Key() {
			order = append(order, d)
		}
	}
	order = append(order, focus)
	p := &regionPick{what: what, windows: opts.windows, done: done}
	for _, d := range order {
		s, err := a.openSurface(OverlayOptions{Bounds: displayBounds(d), Activate: true, Excluded: true})
		if err != nil {
			for _, ps := range p.screens {
				ps.s.close()
			}
			chosen, cok := a.chosenDisplay()
			if !cok {
				chosen = focus
			}
			done(chosen, image.Rectangle{}, true)
			return
		}
		key := d.Key()
		ps := pickScreen{s: s, d: d}
		if opts.snaps != nil {
			ps.snap = opts.snaps[key]
		}
		if opts.dimmed != nil {
			ps.dimmed = opts.dimmed[key]
		}
		p.screens = append(p.screens, ps)
	}
	sc.pick = p
}

func (a *App) pickTick(p *regionPick) {
	sc := &a.over
	finish := func(d capture.Display, r image.Rectangle, ok bool) {
		for _, ps := range p.screens {
			ps.s.close()
		}
		sc.pick = nil
		p.done(d, r, ok)
	}
	// Every window's input is read each frame, so none piles up; any of them may cancel or handle keys.
	changed := make([]bool, len(p.screens))
	cancel := false
	confirm := false
	toggleSnap := false
	copyColor := false
	nudgeDX, nudgeDY := 0, 0
	for i := range p.screens {
		ps := &p.screens[i]
		in, moved := a.beginSurface(ps.s)
		changed[i] = moved
		if ps.s.o.Closed() || ps.s.u.rightAt != nil {
			cancel = true
		}
		for _, k := range in.Keys {
			step := 1
			if k.Shift {
				step = 10
			}
			switch {
			case k.Code == keyEscape:
				cancel = true
			case k.Code == keyEnter:
				confirm = true
			case k.Code == keyTab && !k.Ctrl && !k.Alt:
				toggleSnap = true
			case k.Code == 'C' && !k.Ctrl && !k.Alt:
				copyColor = true
			case k.Code == keyLeft:
				nudgeDX -= step
			case k.Code == keyRight:
				nudgeDX += step
			case k.Code == keyUp:
				nudgeDY -= step
			case k.Code == keyDown:
				nudgeDY += step
			}
		}
	}
	if cancel {
		finish(capture.Display{}, image.Rectangle{}, false)
		return
	}
	if toggleSnap && !p.dragging && !p.adjusting {
		p.snapOff = !p.snapOff
		for i := range changed {
			changed[i] = true
		}
	}
	if copyColor {
		if ps, pt, ok := p.pointerScreen(); ok && ps.snap != nil {
			b := ps.snap.Bounds()
			px := min(max(pt.X, b.Min.X), b.Max.X-1)
			py := min(max(pt.Y, b.Min.Y), b.Max.Y-1)
			c := ps.snap.RGBAAt(px, py)
			hex := fmt.Sprintf("#%02X%02X%02X", c.R, c.G, c.B)
			if err := a.host.SetClipboard(hex); err != nil {
				a.toast(a.u.tf("Could not copy: %s", a.describe(err)), true)
			} else {
				a.toast(a.u.tf("Copied colour %s", hex), false)
			}
			finish(capture.Display{}, image.Rectangle{}, false)
			return
		}
	}

	// Arrow-key nudging: shifts adjRect in adjust mode, or the active corner during drag.
	if nudgeDX != 0 || nudgeDY != 0 {
		switch {
		case p.adjusting:
			d := p.screens[p.on].d
			p.adjRect = shiftRectClamped(p.adjRect, image.Pt(nudgeDX, nudgeDY), image.Rect(0, 0, d.Width, d.Height))
			changed[p.on] = true
		case p.dragging:
			d := p.screens[p.on].d
			p.to.X = min(max(p.to.X+nudgeDX, 0), d.Width)
			p.to.Y = min(max(p.to.Y+nudgeDY, 0), d.Height)
			changed[p.on] = true
		}
	}

	// Keyboard Enter confirms adjust mode, or captures the hovered window/screen.
	if confirm {
		switch {
		case p.adjusting:
			finish(p.screens[p.on].d, p.adjRect, true)
			return
		case !p.dragging:
			if ps, _, ok := p.pointerScreen(); ok {
				r := image.Rectangle{}
				if !p.snapOff && !ps.hovered.Empty() {
					r = ps.hovered
				}
				finish(ps.d, r, true)
				return
			}
		}
	}

	// Update hovered window/popup auto-snap when idle.
	if !p.dragging && !p.adjusting {
		for i := range p.screens {
			ps := &p.screens[i]
			u, d := &ps.s.u, ps.d
			var wantRect image.Rectangle
			var wantTitle string
			if !p.snapOff && len(p.windows) > 0 && u.mx >= 0 && u.my >= 0 && u.mx < float32(d.Width) && u.my < float32(d.Height) {
				wantRect, wantTitle = hitWindow(d, int(u.mx), int(u.my), p.windows)
			}
			if wantRect != ps.hovered || wantTitle != ps.hoverTitle {
				ps.hovered, ps.hoverTitle = wantRect, wantTitle
				changed[i] = true
			}
		}
	}

	// Adjust mode (entered by holding Alt on mouse-up): 8 resize handles + drag-to-move + double-click confirm.
	if p.adjusting {
		ps := &p.screens[p.on]
		u, d := &ps.s.u, ps.d
		at := image.Pt(int(u.mx), int(u.my))
		bounds := image.Rect(0, 0, d.Width, d.Height)
		if u.double && at.In(p.adjRect) {
			finish(d, p.adjRect, true)
			return
		}
		hit := p.hitHandle(u, p.adjRect, at)
		if u.pressed {
			if hit != handleNone {
				u.pressed = false
				u.active = "pick/adj"
				p.adjDrag = hit
				p.adjStart = at
				p.adjOrigin = p.adjRect
			} else {
				// Pressing outside the selection starts a fresh drag on this display.
				p.adjusting, p.adjDrag = false, handleNone
				u.pressed = false
				u.active = "pick"
				p.from, p.to, p.dragging, p.spacePan = at, at, true, false
				changed[p.on] = true
			}
		}
		if p.adjusting && u.dragging("pick/adj") {
			p.adjRect = applyHandleDrag(p.adjOrigin, p.adjDrag, at.Sub(p.adjStart), bounds)
			changed[p.on] = true
		}
		if p.adjusting && u.letGo("pick/adj") {
			p.adjDrag = handleNone
			changed[p.on] = true
		}
		// Clicking on another display starts a fresh drag there.
		if p.adjusting {
			for i := range p.screens {
				if i == p.on {
					continue
				}
				ou, od := &p.screens[i].s.u, p.screens[i].d
				if ou.press("pick", gfx.Rect{W: float32(od.Width), H: float32(od.Height)}) {
					prev := p.on
					oat := image.Pt(int(ou.mx), int(ou.my))
					p.adjusting, p.adjDrag = false, handleNone
					p.on, p.from, p.to, p.dragging, p.spacePan = i, oat, oat, true, false
					changed[prev] = true
					changed[i] = true
					break
				}
			}
		}
	} else {
		for i := range p.screens {
			ps := &p.screens[i]
			u, d := &ps.s.u, ps.d
			at := image.Pt(int(u.mx), int(u.my))
			if u.press("pick", gfx.Rect{W: float32(d.Width), H: float32(d.Height)}) && !p.dragging {
				p.on, p.from, p.to, p.dragging, p.spacePan = i, at, at, true, false
				changed[i] = true
			}
			if !p.dragging || p.on != i {
				continue
			}
			if u.dragging("pick") {
				if u.isHeld(keySpace) {
					if !p.spacePan {
						p.spacePan, p.panLast = true, at
					} else if delta := at.Sub(p.panLast); delta != (image.Point{}) {
						p.panLast = at
						p.panSelection(delta, image.Rect(0, 0, d.Width, d.Height))
					}
				} else {
					p.spacePan = false
					p.to = at
				}
			}
			if u.letGo("pick") {
				r := p.region()
				if r.Dx() < 8 || r.Dy() < 8 {
					if !p.snapOff && !ps.hovered.Empty() {
						r = ps.hovered // a click on a hovered window/popup captures it
					} else {
						r = image.Rectangle{} // a click on the desktop captures the whole display
					}
					finish(d, r, true)
					return
				}
				if u.alt {
					p.dragging, p.spacePan = false, false
					p.adjusting, p.adjRect, p.adjDrag = true, r, handleNone
					changed[i] = true
				} else {
					finish(d, r, true)
					return
				}
			}
		}
	}
	for i := range p.screens {
		if changed[i] {
			a.drawPick(p, i)
		}
	}
}

// pointerScreen returns the pickScreen currently under the pointer (or the active screen), along with the pointer
// coordinates in that display's local pixels.
func (p *regionPick) pointerScreen() (*pickScreen, image.Point, bool) {
	if p.dragging || p.adjusting {
		ps := &p.screens[p.on]
		return ps, image.Pt(int(ps.s.u.mx), int(ps.s.u.my)), true
	}
	for i := range p.screens {
		ps := &p.screens[i]
		u, d := &ps.s.u, ps.d
		if u.mx >= 0 && u.my >= 0 && u.mx < float32(d.Width) && u.my < float32(d.Height) {
			return ps, image.Pt(int(u.mx), int(u.my)), true
		}
	}
	if len(p.screens) > 0 {
		ps := &p.screens[len(p.screens)-1]
		return ps, image.Pt(int(ps.s.u.mx), int(ps.s.u.my)), true
	}
	return nil, image.Point{}, false
}

// region is the rectangle being dragged out, within its display.
func (p *regionPick) region() image.Rectangle {
	d := p.screens[p.on].d
	return image.Rectangle{Min: p.from, Max: p.to}.Canon().Intersect(image.Rect(0, 0, d.Width, d.Height))
}

// panSelection shifts both corners of the active drag selection by delta while keeping the box inside bounds.
func (p *regionPick) panSelection(delta image.Point, bounds image.Rectangle) {
	r := image.Rectangle{Min: p.from, Max: p.to}.Canon()
	dx, dy := delta.X, delta.Y
	if r.Min.X+dx < bounds.Min.X {
		dx = bounds.Min.X - r.Min.X
	}
	if r.Max.X+dx > bounds.Max.X {
		dx = bounds.Max.X - r.Max.X
	}
	if r.Min.Y+dy < bounds.Min.Y {
		dy = bounds.Min.Y - r.Min.Y
	}
	if r.Max.Y+dy > bounds.Max.Y {
		dy = bounds.Max.Y - r.Max.Y
	}
	d := image.Pt(dx, dy)
	p.from = p.from.Add(d)
	p.to = p.to.Add(d)
}

// shiftRectClamped moves r by delta while preserving its size inside bounds.
func shiftRectClamped(r image.Rectangle, delta image.Point, bounds image.Rectangle) image.Rectangle {
	w, h := r.Dx(), r.Dy()
	x := min(max(r.Min.X+delta.X, bounds.Min.X), bounds.Max.X-w)
	y := min(max(r.Min.Y+delta.Y, bounds.Min.Y), bounds.Max.Y-h)
	return image.Rect(x, y, x+w, y+h)
}

// hitWindow finds the frontmost window/popup containing display-local point (x, y) and returns its clipped
// display-local rectangle and title.
func hitWindow(d capture.Display, x, y int, windows []capture.WindowRect) (image.Rectangle, string) {
	pt := image.Pt(d.X+x, d.Y+y)
	db := displayBounds(d)
	for _, w := range windows {
		if !pt.In(w.Rect) {
			continue
		}
		clipped := w.Rect.Intersect(db)
		if clipped.Dx() < 8 || clipped.Dy() < 8 {
			continue
		}
		return clipped.Sub(db.Min), w.Title
	}
	return image.Rectangle{}, ""
}

// handlePoints returns the 8 handle positions on r in display pixels.
func handlePoints(r image.Rectangle) [8]struct {
	h pickHandle
	p image.Point
} {
	mx, my := (r.Min.X+r.Max.X)/2, (r.Min.Y+r.Max.Y)/2
	return [8]struct {
		h pickHandle
		p image.Point
	}{
		{handleNW, image.Pt(r.Min.X, r.Min.Y)},
		{handleN, image.Pt(mx, r.Min.Y)},
		{handleNE, image.Pt(r.Max.X, r.Min.Y)},
		{handleE, image.Pt(r.Max.X, my)},
		{handleSE, image.Pt(r.Max.X, r.Max.Y)},
		{handleS, image.Pt(mx, r.Max.Y)},
		{handleSW, image.Pt(r.Min.X, r.Max.Y)},
		{handleW, image.Pt(r.Min.X, my)},
	}
}

func (p *regionPick) hitHandle(u *ctx, r image.Rectangle, at image.Point) pickHandle {
	rad := max(int(math.Round(float64(u.px(10)))), 8)
	for _, hp := range handlePoints(r) {
		if abs(at.X-hp.p.X) <= rad && abs(at.Y-hp.p.Y) <= rad {
			return hp.h
		}
	}
	// Also allow grabbing anywhere along the edges.
	edge := max(int(math.Round(float64(u.px(5)))), 4)
	if at.Y >= r.Min.Y-edge && at.Y <= r.Max.Y+edge {
		if abs(at.X-r.Min.X) <= edge {
			return handleW
		}
		if abs(at.X-r.Max.X) <= edge {
			return handleE
		}
	}
	if at.X >= r.Min.X-edge && at.X <= r.Max.X+edge {
		if abs(at.Y-r.Min.Y) <= edge {
			return handleN
		}
		if abs(at.Y-r.Max.Y) <= edge {
			return handleS
		}
	}
	if at.In(r) {
		return handleMove
	}
	return handleNone
}

func handleCursor(h pickHandle) Cursor {
	switch h {
	case handleMove:
		return CursorMove
	case handleNW, handleNE, handleSE, handleSW:
		return CursorResizeCorner
	case handleN, handleE, handleS, handleW:
		return CursorResize
	default:
		return CursorCross
	}
}

func applyHandleDrag(orig image.Rectangle, h pickHandle, delta image.Point, bounds image.Rectangle) image.Rectangle {
	const minSize = 8
	if h == handleMove {
		return shiftRectClamped(orig, delta, bounds)
	}
	r := orig
	switch h {
	case handleNW:
		r.Min.X = min(max(orig.Min.X+delta.X, bounds.Min.X), orig.Max.X-minSize)
		r.Min.Y = min(max(orig.Min.Y+delta.Y, bounds.Min.Y), orig.Max.Y-minSize)
	case handleN:
		r.Min.Y = min(max(orig.Min.Y+delta.Y, bounds.Min.Y), orig.Max.Y-minSize)
	case handleNE:
		r.Max.X = max(min(orig.Max.X+delta.X, bounds.Max.X), orig.Min.X+minSize)
		r.Min.Y = min(max(orig.Min.Y+delta.Y, bounds.Min.Y), orig.Max.Y-minSize)
	case handleE:
		r.Max.X = max(min(orig.Max.X+delta.X, bounds.Max.X), orig.Min.X+minSize)
	case handleSE:
		r.Max.X = max(min(orig.Max.X+delta.X, bounds.Max.X), orig.Min.X+minSize)
		r.Max.Y = max(min(orig.Max.Y+delta.Y, bounds.Max.Y), orig.Min.Y+minSize)
	case handleS:
		r.Max.Y = max(min(orig.Max.Y+delta.Y, bounds.Max.Y), orig.Min.Y+minSize)
	case handleSW:
		r.Min.X = min(max(orig.Min.X+delta.X, bounds.Min.X), orig.Max.X-minSize)
		r.Max.Y = max(min(orig.Max.Y+delta.Y, bounds.Max.Y), orig.Min.Y+minSize)
	case handleW:
		r.Min.X = min(max(orig.Min.X+delta.X, bounds.Min.X), orig.Max.X-minSize)
	}
	return r
}

// drawPick draws the picker over one display. With a frozen snapshot (screenshots), it updates only the dirty
// bounding region against the pre-baked dimmed backdrop so hover/drag/loupe redraws stay fast on 4K/5K displays.
// Without a snapshot (live recording picker), it uses a translucent dim overlay with a clear cutout.
func (a *App) drawPick(p *regionPick, i int) {
	ps := &p.screens[i]
	if ps.snap != nil {
		a.drawPickFrozen(p, i)
		return
	}
	a.drawPickLive(p, i)
}

func (a *App) drawPickLive(p *regionPick, i int) {
	ps := &p.screens[i]
	u, c, d := &ps.s.u, ps.s.c, ps.d
	u.cursor = CursorCross
	c.ClearTo(color.RGBA{0, 0, 0, 110})
	switch {
	case p.dragging && p.on == i:
		r := p.region()
		// Nearly transparent rather than clear, so the window keeps the mouse there.
		draw.Draw(c.Img, r, image.NewUniform(color.RGBA{0, 0, 0, 1}), image.Point{}, draw.Src)
		drawSelectionChrome(u, r, "", p.what.hue, false)
	case p.adjusting && p.on == i:
		r := p.adjRect
		draw.Draw(c.Img, r, image.NewUniform(color.RGBA{0, 0, 0, 1}), image.Point{}, draw.Src)
		drawSelectionChrome(u, r, "", p.what.hue, true)
		drawPickBar(u, pickPurpose{glyph: p.what.glyph, hue: p.what.hue, title: p.what.title,
			hint: u.t("Drag handles or arrows to adjust · Enter or double-click captures · Esc cancels")}, float32(d.Width))
		if p.adjDrag != handleNone {
			u.cursor = handleCursor(p.adjDrag)
		} else {
			u.cursor = handleCursor(p.hitHandle(u, r, image.Pt(int(u.mx), int(u.my))))
		}
	default:
		drawPickBar(u, p.what, float32(d.Width))
		// A crosshair through the pointer helps lining the corner up; it is off the window once the pointer leaves.
		c.Fill(gfx.Rect{X: 0, Y: u.my, W: float32(d.Width), H: 1}, gfx.Alpha(colWhite, 0.35))
		c.Fill(gfx.Rect{X: u.mx, Y: 0, W: 1, H: float32(d.Height)}, gfx.Alpha(colWhite, 0.35))
	}
	ps.s.end(image.Rectangle{})
}

func (a *App) drawPickFrozen(p *regionPick, i int) {
	ps := &p.screens[i]
	u, c, d := &ps.s.u, ps.s.c, ps.d
	full := c.Img.Rect
	u.cursor = CursorCross

	if ps.dimmed == nil {
		ps.dimmed = dimSnapshot(ps.snap)
	}

	var wantBar pickBarKind
	switch {
	case p.adjusting && p.on == i:
		wantBar = barAdjust
	case p.dragging && p.on == i:
		wantBar = barNone
	default:
		wantBar = barIdle
	}

	var dirty image.Rectangle
	if !ps.baseDrawn {
		copyRect(c.Img, ps.dimmed, full)
		ps.baseDrawn = true
		ps.barKind = barNone
		ps.barRect = image.Rectangle{}
		ps.prevDirty = image.Rectangle{}
		dirty = full
	} else {
		dirty = ps.prevDirty
		copyRect(c.Img, ps.dimmed, ps.prevDirty)
		if wantBar != ps.barKind && !ps.barRect.Empty() {
			copyRect(c.Img, ps.dimmed, ps.barRect)
			dirty = dirty.Union(ps.barRect)
			ps.barRect = image.Rectangle{}
		}
	}

	var newDirty image.Rectangle
	addDirty := func(r image.Rectangle) {
		if !r.Empty() {
			newDirty = newDirty.Union(r)
		}
	}

	var activeRect image.Rectangle
	var activeTitle string
	var showHandles bool
	switch {
	case p.dragging && p.on == i:
		activeRect = p.region()
	case p.adjusting && p.on == i:
		activeRect = p.adjRect
		showHandles = true
		if p.adjDrag != handleNone {
			u.cursor = handleCursor(p.adjDrag)
		} else {
			u.cursor = handleCursor(p.hitHandle(u, activeRect, image.Pt(int(u.mx), int(u.my))))
		}
	case !p.snapOff && !ps.hovered.Empty():
		activeRect = ps.hovered
		activeTitle = ps.hoverTitle
	}

	if !activeRect.Empty() {
		copyRect(c.Img, ps.snap, activeRect)
		addDirty(activeRect)
		addDirty(drawSelectionChrome(u, activeRect, activeTitle, p.what.hue, showHandles))
	}

	// Draw or restore the top purpose bar when it changed or when dynamic elements overlapped it.
	barPurpose := p.what
	if wantBar == barAdjust {
		barPurpose.hint = u.t("Drag handles or arrows to adjust · Enter or double-click captures · Esc cancels")
	}
	if wantBar != barNone {
		needBar := wantBar != ps.barKind || ps.barRect.Empty() || dirty.Overlaps(ps.barRect) || newDirty.Overlaps(ps.barRect)
		if needBar {
			if !ps.barRect.Empty() {
				copyRect(c.Img, ps.dimmed, ps.barRect)
				dirty = dirty.Union(ps.barRect)
			}
			br := drawPickBar(u, barPurpose, float32(d.Width))
			ps.barRect = br
			dirty = dirty.Union(br)
		}
	}
	ps.barKind = wantBar

	// Local crosshair and pixel magnifier loupe when the pointer is inside this display.
	inDisplay := u.mx >= 0 && u.my >= 0 && u.mx < float32(d.Width) && u.my < float32(d.Height)
	if inDisplay && (!p.adjusting || p.adjDrag != handleNone && p.adjDrag != handleMove) {
		if !p.dragging && !p.adjusting {
			arm := u.px(32)
			hBar := gfx.Rect{X: u.mx - arm, Y: u.my, W: 2 * arm, H: 1}
			vBar := gfx.Rect{X: u.mx, Y: u.my - arm, W: 1, H: 2 * arm}
			c.Fill(hBar, gfx.Alpha(colWhite, 0.7))
			c.Fill(vBar, gfx.Alpha(colWhite, 0.7))
			addDirty(image.Rect(int(u.mx-arm)-1, int(u.my-arm)-1, int(u.mx+arm)+2, int(u.my+arm)+2))
		}
		var measure image.Rectangle
		if (p.dragging || p.adjusting) && p.on == i {
			measure = activeRect
		} else if !ps.hovered.Empty() {
			measure = ps.hovered
		}
		addDirty(drawPickLoupe(u, ps.snap, image.Pt(int(u.mx), int(u.my)), measure, ps.barRect, d))
	}

	dirty = dirty.Union(newDirty).Intersect(full)
	ps.prevDirty = newDirty.Intersect(full)
	ps.s.end(dirty)
}

// drawSelectionChrome draws the outline, optional 8 resize handles, and dimension/title pill for r, returning
// the bounding rectangle of everything drawn.
func drawSelectionChrome(u *ctx, r image.Rectangle, title string, hue color.RGBA, handles bool) image.Rectangle {
	c := u.c
	box := gfx.Rect{X: float32(r.Min.X), Y: float32(r.Min.Y), W: float32(r.Dx()), H: float32(r.Dy())}
	w := max(u.px(2), 1)
	for _, e := range []gfx.Rect{
		{X: box.X - w, Y: box.Y - w, W: box.W + 2*w, H: w},
		{X: box.X - w, Y: box.Bottom(), W: box.W + 2*w, H: w},
		{X: box.X - w, Y: box.Y, W: w, H: box.H},
		{X: box.Right(), Y: box.Y, W: w, H: box.H},
	} {
		c.Fill(e, hue)
	}
	wi := int(math.Ceil(float64(w)))
	bounds := image.Rect(r.Min.X-wi, r.Min.Y-wi, r.Max.X+wi, r.Max.Y+wi)
	if handles {
		hs := max(u.px(8), 6)
		half := hs / 2
		hsi := int(math.Ceil(float64(half))) + 2
		for _, hp := range handlePoints(r) {
			hr := gfx.Rect{X: float32(hp.p.X) - half, Y: float32(hp.p.Y) - half, W: hs, H: hs}
			c.Fill(hr, colWhite)
			c.Outline(hr, max(u.px(1.5), 1), hue)
		}
		bounds = bounds.Union(image.Rect(r.Min.X-hsi, r.Min.Y-hsi, r.Max.X+hsi, r.Max.Y+hsi))
	}
	label := fmt.Sprintf("%d × %d", r.Dx(), r.Dy())
	if title != "" {
		label = title + " · " + label
	}
	f := u.bold(12)
	maxW := max(float32(c.Img.Rect.Dx())-u.px(24), u.px(120))
	text := f.Ellipsize(label, min(maxW, u.px(340)))
	lw := f.Measure(text) + u.px(16)
	lh := u.px(24)
	lx := min(max(box.X, u.px(4)), float32(c.Img.Rect.Dx())-lw-u.px(4))
	ly := box.Y - u.px(30)
	if ly < u.px(4) {
		ly = box.Y + u.px(6)
	}
	tag := gfx.Rect{X: lx, Y: ly, W: lw, H: lh}
	c.Fill(tag, gfx.Alpha(colBlack, 0.84))
	u.label(f, tag, text, colWhite, 0.5)
	return bounds.Union(tag.Pixels().Inset(-1))
}

// drawPickLoupe renders a 15×15 pixel magnifier @ 8× zoom with grid, crosshair, dimensions/coords, and #RRGGBB
// readout near pt, flipping away from display edges and the top bar. Returns its pixel bounds.
func drawPickLoupe(u *ctx, snap *image.RGBA, pt image.Point, measure image.Rectangle, avoid image.Rectangle, d capture.Display) image.Rectangle {
	const cells = 15
	const half = cells / 2
	cell := max(int(math.Round(float64(u.px(8)))), 4)
	gridW := cells * cell
	gridH := cells * cell
	infoH := int(math.Round(float64(u.px(40))))
	border := max(int(math.Round(float64(u.px(2)))), 1)
	totalW := gridW + 2*border
	totalH := gridH + infoH + 2*border

	off := max(int(math.Round(float64(u.px(20)))), 12)
	margin := max(int(math.Round(float64(u.px(8)))), 4)
	x := pt.X + off
	y := pt.Y + off
	if x+totalW > d.Width-margin {
		x = pt.X - off - totalW
	}
	if y+totalH > d.Height-margin {
		y = pt.Y - off - totalH
	}
	x = min(max(x, margin), max(margin, d.Width-totalW-margin))
	y = min(max(y, margin), max(margin, d.Height-totalH-margin))
	outer := image.Rect(x, y, x+totalW, y+totalH)
	if !avoid.Empty() && outer.Overlaps(avoid) {
		altY := avoid.Max.Y + margin
		if altY+totalH <= d.Height-margin {
			y = altY
			outer = image.Rect(x, y, x+totalW, y+totalH)
		}
	}

	c := u.c
	// Outer dark card frame.
	c.Fill(gfx.Rect{X: float32(x), Y: float32(y), W: float32(totalW), H: float32(totalH)}, gfx.Alpha(colPanel, 0.96))

	// Render zoomed 15×15 pixel grid directly into c.Img for speed.
	gx0, gy0 := x+border, y+border
	sb := snap.Bounds()
	img := c.Img
	ib := img.Bounds()
	showGrid := cell >= 6
	for cy := range cells {
		sy := pt.Y + cy - half
		dy0 := gy0 + cy*cell
		for cx := range cells {
			sx := pt.X + cx - half
			var col color.RGBA
			if sx >= sb.Min.X && sx < sb.Max.X && sy >= sb.Min.Y && sy < sb.Max.Y {
				si := snap.PixOffset(sx, sy)
				col = color.RGBA{snap.Pix[si], snap.Pix[si+1], snap.Pix[si+2], 255}
			} else {
				col = color.RGBA{18, 18, 22, 255}
			}
			dx0 := gx0 + cx*cell
			x1, y1 := max(dx0, ib.Min.X), max(dy0, ib.Min.Y)
			x2, y2 := min(dx0+cell, ib.Max.X), min(dy0+cell, ib.Max.Y)
			for py := y1; py < y2; py++ {
				row := img.PixOffset(x1, py)
				isGridRow := showGrid && py == dy0
				for px := x1; px < x2; px, row = px+1, row+4 {
					if isGridRow || (showGrid && px == dx0) {
						// Subtle grid line: blend 25% toward black.
						img.Pix[row] = uint8((uint32(col.R) * 192) >> 8)
						img.Pix[row+1] = uint8((uint32(col.G) * 192) >> 8)
						img.Pix[row+2] = uint8((uint32(col.B) * 192) >> 8)
						img.Pix[row+3] = 255
					} else {
						img.Pix[row] = col.R
						img.Pix[row+1] = col.G
						img.Pix[row+2] = col.B
						img.Pix[row+3] = 255
					}
				}
			}
		}
	}

	// Highlight the center pixel cell with contrasting double border + subtle crosshair bands.
	midX := float32(gx0 + half*cell)
	midY := float32(gy0 + half*cell)
	cellF := float32(cell)
	c.Fill(gfx.Rect{X: float32(gx0), Y: midY, W: midX - float32(gx0), H: cellF}, gfx.Alpha(colAccent, 0.16))
	c.Fill(gfx.Rect{X: midX + cellF, Y: midY, W: float32(gx0+gridW) - (midX + cellF), H: cellF}, gfx.Alpha(colAccent, 0.16))
	c.Fill(gfx.Rect{X: midX, Y: float32(gy0), W: cellF, H: midY - float32(gy0)}, gfx.Alpha(colAccent, 0.16))
	c.Fill(gfx.Rect{X: midX, Y: midY + cellF, W: cellF, H: float32(gy0+gridH) - (midY + cellF)}, gfx.Alpha(colAccent, 0.16))
	c.Outline(gfx.Rect{X: midX - 1, Y: midY - 1, W: cellF + 2, H: cellF + 2}, 1, colBlack)
	c.Outline(gfx.Rect{X: midX, Y: midY, W: cellF, H: cellF}, 1, colWhite)

	// Center pixel color + readout footer.
	spx := min(max(pt.X, sb.Min.X), sb.Max.X-1)
	spy := min(max(pt.Y, sb.Min.Y), sb.Max.Y-1)
	center := snap.RGBAAt(spx, spy)
	infoY := float32(gy0 + gridH)
	padX := u.px(8)
	line1 := fmt.Sprintf("%d, %d", pt.X, pt.Y)
	if !measure.Empty() {
		line1 = fmt.Sprintf("%d × %d", measure.Dx(), measure.Dy())
	}
	u.label(u.bold(11), gfx.Rect{X: float32(gx0) + padX, Y: infoY + u.px(2), W: float32(gridW) - 2*padX, H: u.px(18)}, line1, colText, 0)
	swatchSize := u.px(10)
	swatchY := infoY + u.px(23)
	swatch := gfx.Rect{X: float32(gx0) + padX, Y: swatchY, W: swatchSize, H: swatchSize}
	c.Fill(swatch, color.RGBA{center.R, center.G, center.B, 255})
	c.Outline(swatch, 1, colBorderHi)
	hex := fmt.Sprintf("#%02X%02X%02X", center.R, center.G, center.B)
	u.label(u.face(11), gfx.Rect{X: swatch.Right() + u.px(6), Y: infoY + u.px(19), W: float32(gridW) - 2*padX - swatchSize - u.px(20), H: u.px(18)}, hex, colDim, 0)
	u.label(u.bold(10.5), gfx.Rect{X: float32(gx0+gridW) - padX - u.px(14), Y: infoY + u.px(19), W: u.px(14), H: u.px(18)}, "C", colAccent, 1)

	// Crisp outer border.
	c.Outline(gfx.Rect{X: float32(x), Y: float32(y), W: float32(totalW), H: float32(totalH)}, float32(border), colBorderHi)
	return outer.Inset(-1)
}

// drawPickBar draws the bar at the top of a display: the purpose's symbol and title in its colour, then the hint.
// It returns the bar's pixel bounding rectangle.
func drawPickBar(u *ctx, what pickPurpose, width float32) image.Rectangle {
	c := u.c
	title, hint := u.bold(14), u.face(14)
	gap, pad, mark := u.px(14), u.px(16), u.px(28)
	tw, hw := title.Measure(what.title), hint.Measure(what.hint)
	maxBarW := max(width-u.px(24), u.px(200))
	fixed := pad + mark + u.px(8) + tw + gap + u.px(1) + gap + pad
	hintText := what.hint
	if fixed+hw > maxBarW {
		avail := max(maxBarW-fixed, u.px(40))
		hintText = hint.Ellipsize(hintText, avail)
		hw = hint.Measure(hintText)
	}
	w := min(fixed+hw, maxBarW)
	bar := gfx.Rect{X: (width - w) / 2, Y: u.px(40), W: w, H: u.px(44)}
	c.Fill(bar, gfx.Alpha(colPanel, 0.94))
	c.Fill(gfx.Rect{X: bar.X, Y: bar.Y, W: u.px(3), H: bar.H}, what.hue)
	x := bar.X + pad
	u.icon(what.glyph, gfx.Rect{X: x, Y: bar.Y, W: mark, H: bar.H}, 20, what.hue)
	x += mark + u.px(8)
	u.label(title, gfx.Rect{X: x, Y: bar.Y, W: tw, H: bar.H}, what.title, colText, 0)
	x += tw + gap
	c.Fill(gfx.Rect{X: x, Y: bar.Y + u.px(12), W: u.px(1), H: bar.H - u.px(24)}, colBorder)
	x += u.px(1) + gap
	u.label(hint, gfx.Rect{X: x, Y: bar.Y, W: hw, H: bar.H}, hintText, colDim, 0)
	return bar.Pixels().Inset(-1)
}

// copyRect copies pixels inside r from src into dst row-by-row without allocating.
func copyRect(dst, src *image.RGBA, r image.Rectangle) {
	if dst == nil || src == nil {
		return
	}
	r = r.Intersect(dst.Rect).Intersect(src.Rect)
	if r.Empty() {
		return
	}
	rowBytes := r.Dx() * 4
	if r == dst.Rect && r == src.Rect && dst.Stride == src.Stride && rowBytes == dst.Stride {
		copy(dst.Pix, src.Pix)
		return
	}
	for y := r.Min.Y; y < r.Max.Y; y++ {
		di := dst.PixOffset(r.Min.X, y)
		si := src.PixOffset(r.Min.X, y)
		copy(dst.Pix[di:di+rowBytes], src.Pix[si:si+rowBytes])
	}
}

// dimSnapshot returns an opaque darkened copy (~55% brightness) of src, parallelized across rows for 4K/5K displays.
func dimSnapshot(src *image.RGBA) *image.RGBA {
	if src == nil {
		return nil
	}
	b := src.Rect
	w, h := b.Dx(), b.Dy()
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	if w <= 0 || h <= 0 {
		return dst
	}
	run := func(y0, y1 int) {
		rowBytes := w * 4
		for y := y0; y < y1; y++ {
			sp := src.Pix[y*src.Stride : y*src.Stride+rowBytes]
			dp := dst.Pix[y*dst.Stride : y*dst.Stride+rowBytes]
			for i := 0; i < rowBytes; i += 4 {
				dp[i] = uint8((uint32(sp[i]) * 140) >> 8)
				dp[i+1] = uint8((uint32(sp[i+1]) * 140) >> 8)
				dp[i+2] = uint8((uint32(sp[i+2]) * 140) >> 8)
				dp[i+3] = 255
			}
		}
	}
	workers := min(runtime.GOMAXPROCS(0), 8)
	if w*h < 256<<10 || workers <= 1 {
		run(0, h)
		return dst
	}
	var wg sync.WaitGroup
	step := (h + workers - 1) / workers
	for y := 0; y < h; y += step {
		y0, y1 := y, min(y+step, h)
		wg.Add(1)
		go func() {
			defer wg.Done()
			run(y0, y1)
		}()
	}
	wg.Wait()
	return dst
}

// dimSnapshots pre-bakes darkened copies for every display snapshot in parallel.
func dimSnapshots(snaps map[string]*image.RGBA) map[string]*image.RGBA {
	if len(snaps) == 0 {
		return nil
	}
	out := make(map[string]*image.RGBA, len(snaps))
	if len(snaps) == 1 {
		for k, img := range snaps {
			out[k] = dimSnapshot(img)
		}
		return out
	}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for k, img := range snaps {
		wg.Add(1)
		go func(key string, src *image.RGBA) {
			defer wg.Done()
			d := dimSnapshot(src)
			mu.Lock()
			out[key] = d
			mu.Unlock()
		}(k, img)
	}
	wg.Wait()
	return out
}
