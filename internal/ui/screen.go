package ui

import (
	"fmt"
	"image"
	"image/color"
	"time"

	"golang.org/x/image/draw"

	"github.com/btelolabs/btelo-forge/internal/capture"
	"github.com/btelolabs/btelo-forge/internal/gfx"
)

// OverlayOptions say how a window over the screen behaves.
type OverlayOptions struct {
	Bounds       image.Rectangle // on the desktop, in physical pixels
	Activate     bool            // takes the keyboard
	ClickThrough bool            // the mouse goes through to the windows below
	Excluded     bool            // left out of screen recordings and screenshots
}

// Overlay is a borderless window over the screen whose pixels may be partly transparent.
type Overlay interface {
	Bounds() image.Rectangle
	Scale() float32
	Input() Input // what happened since the last call
	Present(img *image.RGBA, dirty image.Rectangle) error
	SetClickThrough(on bool)
	TakeKeyboard(on bool)                    // in front with the keyboard, or giving it back to the window that had it
	Raise()                                  // in front of the other overlays again, and with the keyboard if it has taken it
	TextInput(on bool, x, y, height float32) // the IME on or off, at (x, y) in its pixels
	SetCursor(Cursor)
	Move(r image.Rectangle)
	Closed() bool
	Close()
}

// overlayHost is a window that can open overlays; without it, regions are not picked and the screen is not drawn
// on.
type overlayHost interface {
	OpenOverlay(o OverlayOptions) (Overlay, error)
	Pointer() image.Point // where the mouse pointer is on the desktop, in physical pixels
}

// surface is an overlay with its own canvas and immediate-mode state, apart from the main window's.
type surface struct {
	o     Overlay
	c     *gfx.Canvas
	u     ctx
	shown bool
}

func (a *App) openSurface(o OverlayOptions) (*surface, error) {
	host, ok := a.host.(overlayHost)
	if !ok {
		return nil, capture.ErrUnsupported
	}
	ov, err := host.OpenOverlay(o)
	if err != nil {
		return nil, err
	}
	s := &surface{o: ov, c: gfx.NewCanvas(o.Bounds.Dx(), o.Bounds.Dy())}
	s.u.fonts, s.u.host = a.u.fonts, a.host
	return s, nil
}

// begin reads the overlay's input into its own state; it reports whether anything happened.
func (a *App) beginSurface(s *surface) (Input, bool) {
	return a.beginSurfaceWith(s, s.o.Input())
}

// beginSurfaceWith starts a frame of the overlay with input taken from it already.
func (a *App) beginSurfaceWith(s *surface, in Input) (Input, bool) {
	moved := in.X != s.u.mx || in.Y != s.u.my
	s.u.c, s.u.s, s.u.lang = s.c, s.o.Scale(), a.u.lang
	s.u.begin(in)
	return in, moved || in.acted() || !s.shown
}

// endSurface shows what was drawn within dirty (everything when it is empty).
func (s *surface) end(dirty image.Rectangle) {
	s.u.end()
	s.o.SetCursor(s.u.cursor)
	s.o.Present(s.c.Img, dirty)
	s.shown = true
}

func (s *surface) close() {
	if s != nil {
		s.o.Close()
	}
}

// displayBounds is where a display is on the desktop.
func displayBounds(d capture.Display) image.Rectangle {
	return image.Rect(d.X, d.Y, d.X+d.Width, d.Y+d.Height)
}

// screenState is what is over the screen: a region being picked, the drawing and the floating toolbar.
type screenState struct {
	pick   *regionPick
	draw   *screenDraw
	bar    *recordBar
	frame  *surface    // the outline of the region being recorded
	barPos image.Point // where the user dragged the recording bar, relative to its display; zero is the default place
	flow   barFlow     // the options bar being laid out in rows
}

// screenTick looks after the windows over the screen, every frame and while the main window is minimized.
func (a *App) screenTick() {
	sc := &a.over
	if sc.pick != nil {
		a.pickTick(sc.pick)
	}
	if sc.frame != nil && a.rec.rec == nil && !a.rec.starting {
		sc.frame.close()
		sc.frame = nil
	}
	if sc.draw != nil {
		a.drawTick(sc.draw)
	}
	// The recording's bar is there while recording.
	switch want := a.rec.rec != nil; {
	case want && sc.bar == nil:
		a.openRecordBar()
	case !want && sc.bar != nil:
		sc.bar.s.close()
		sc.bar = nil
	}
	if sc.bar != nil {
		a.recordBarTick(sc.bar)
	}
}

// closeScreen closes every window over the screen, as the app closes.
func (a *App) closeScreen() {
	sc := &a.over
	if sc.pick != nil {
		for _, ps := range sc.pick.screens {
			ps.s.close()
		}
	}
	if sc.bar != nil {
		sc.bar.s.close()
	}
	a.closeScreenDraw()
	sc.frame.close()
	*sc = screenState{}
}

// ---- Picking a region

// pickPurpose is what a region is picked for, which the bar at the top of every display says with its own symbol,
// colour and words, so a screenshot is not mistaken for a recording.
type pickPurpose struct {
	glyph string
	hue   color.RGBA // the symbol and the outline of the region
	title string
	hint  string
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
		hint: u.t("Drag to choose the region to capture · click to capture the whole screen · Esc cancels")}
}

// regionPick dims every display and lets the user drag out a region of one of them.
type regionPick struct {
	screens  []pickScreen
	what     pickPurpose
	on       int // the display the region is being dragged on
	from, to image.Point
	dragging bool
	done     func(d capture.Display, region image.Rectangle, ok bool)
}

// pickScreen is the picker's window over one display.
type pickScreen struct {
	s *surface
	d capture.Display
}

// pickRegion lets the user choose part of any display: a drag picks a rectangle of the display it starts on, a
// click that whole display, Esc or the right button cancels. Without overlays it picks the whole chosen display at
// once.
func (a *App) pickRegion(what pickPurpose, done func(d capture.Display, region image.Rectangle, ok bool)) {
	sc := &a.over
	if sc.pick != nil {
		return
	}
	chosen, ok := a.chosenDisplay()
	if !ok {
		done(capture.Display{}, image.Rectangle{}, false)
		return
	}
	// The chosen display opens last, so its window is the one in front with the keyboard.
	order := []capture.Display{}
	for _, d := range a.rec.displays {
		if d.Key() != chosen.Key() {
			order = append(order, d)
		}
	}
	order = append(order, chosen)
	p := &regionPick{what: what, done: done}
	for _, d := range order {
		s, err := a.openSurface(OverlayOptions{Bounds: displayBounds(d), Activate: true, Excluded: true})
		if err != nil {
			for _, ps := range p.screens {
				ps.s.close()
			}
			done(chosen, image.Rectangle{}, true)
			return
		}
		p.screens = append(p.screens, pickScreen{s: s, d: d})
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
	// Every window's input is read each frame, so none piles up; any of them may cancel.
	changed := make([]bool, len(p.screens))
	cancel := false
	for i, ps := range p.screens {
		in, moved := a.beginSurface(ps.s)
		changed[i] = moved
		if ps.s.o.Closed() || ps.s.u.rightAt != nil {
			cancel = true
		}
		for _, k := range in.Keys {
			if k.Code == keyEscape {
				cancel = true
			}
		}
	}
	if cancel {
		finish(capture.Display{}, image.Rectangle{}, false)
		return
	}
	for i, ps := range p.screens {
		u, d := &ps.s.u, ps.d
		at := image.Pt(int(u.mx), int(u.my))
		if u.press("pick", gfx.Rect{W: float32(d.Width), H: float32(d.Height)}) && !p.dragging {
			p.on, p.from, p.to, p.dragging = i, at, at, true
		}
		if !p.dragging || p.on != i {
			continue
		}
		if u.dragging("pick") {
			p.to = at
		}
		if u.letGo("pick") {
			r := p.region()
			if r.Dx() < 8 || r.Dy() < 8 {
				r = image.Rectangle{} // a click: the whole display
			}
			finish(d, r, true)
			return
		}
	}
	for i := range p.screens {
		if changed[i] {
			a.drawPick(p, i)
		}
	}
}

// region is the rectangle being dragged out, within its display.
func (p *regionPick) region() image.Rectangle {
	d := p.screens[p.on].d
	return image.Rectangle{Min: p.from, Max: p.to}.Canon().Intersect(image.Rect(0, 0, d.Width, d.Height))
}

// drawPick draws the picker over one display: dimmed, with the region being dragged clear and outlined, and the
// bar saying what the region is for while nothing is dragged there.
func (a *App) drawPick(p *regionPick, i int) {
	ps := p.screens[i]
	u, c, d := &ps.s.u, ps.s.c, ps.d
	u.cursor = CursorCross
	c.ClearTo(color.RGBA{0, 0, 0, 110})
	if p.dragging && p.on == i {
		r := p.region()
		// Nearly transparent rather than clear, so the window keeps the mouse there.
		draw.Draw(c.Img, r, image.NewUniform(color.RGBA{0, 0, 0, 1}), image.Point{}, draw.Src)
		box := gfx.Rect{X: float32(r.Min.X), Y: float32(r.Min.Y), W: float32(r.Dx()), H: float32(r.Dy())}
		w := max(u.px(2), 1)
		for _, e := range []gfx.Rect{{X: box.X - w, Y: box.Y - w, W: box.W + 2*w, H: w}, {X: box.X - w, Y: box.Bottom(), W: box.W + 2*w, H: w},
			{X: box.X - w, Y: box.Y, W: w, H: box.H}, {X: box.Right(), Y: box.Y, W: w, H: box.H}} {
			c.Fill(e, p.what.hue)
		}
		label := fmt.Sprintf("%d × %d", r.Dx(), r.Dy())
		f := u.bold(12)
		lw := f.Measure(label) + u.px(16)
		ly := box.Y - u.px(30)
		if ly < 0 {
			ly = box.Y + u.px(6)
		}
		tag := gfx.Rect{X: box.X, Y: ly, W: lw, H: u.px(24)}
		c.Fill(tag, gfx.Alpha(colBlack, 0.8))
		u.label(f, tag, label, colWhite, 0.5)
	} else {
		drawPickBar(u, p.what, float32(d.Width))
		// A crosshair through the pointer helps lining the corner up; it is off the window once the pointer leaves.
		c.Fill(gfx.Rect{X: 0, Y: u.my, W: float32(d.Width), H: 1}, gfx.Alpha(colWhite, 0.35))
		c.Fill(gfx.Rect{X: u.mx, Y: 0, W: 1, H: float32(d.Height)}, gfx.Alpha(colWhite, 0.35))
	}
	ps.s.end(image.Rectangle{})
}

// drawPickBar draws the bar at the top of a display: the purpose's symbol and title in its colour, then the hint.
func drawPickBar(u *ctx, what pickPurpose, width float32) {
	c := u.c
	title, hint := u.bold(14), u.face(14)
	gap, pad, mark := u.px(14), u.px(16), u.px(28)
	tw, hw := title.Measure(what.title), hint.Measure(what.hint)
	w := pad + mark + u.px(8) + tw + gap + u.px(1) + gap + hw + pad
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
	u.label(hint, gfx.Rect{X: x, Y: bar.Y, W: hw, H: bar.H}, what.hint, colDim, 0)
}

// ---- The recording's bar

// recordBar sits at the top right of the recorded display, left out of the recording: the time, pause and stop.
// Drawing on the screen has its own panel; this bar is only the recording's.
type recordBar struct {
	s        *surface
	d        capture.Display
	dragFrom image.Point // the pointer on the desktop where a drag of the bar began
	barFrom  image.Point
	drawnAt  time.Time
}

// The recording bar's measures, in logical pixels.
const (
	barHeight = 52
	barButton = 42 // an icon button and the gap after it
	barWidth  = 8 + 14 + 6 + 72 + 2*barButton + 2
)

func (a *App) openRecordBar() {
	sc := &a.over
	if a.rec.rec == nil {
		return
	}
	d := a.rec.rec.Options().Display
	scale := float32(1)
	if host, ok := a.host.(interface{ Scale() float32 }); ok {
		scale = host.Scale()
	}
	w, h := int(barWidth*scale), int(barHeight*scale)
	pos := image.Pt(d.Width-w-int(24*scale), int(24*scale))
	if sc.barPos != (image.Point{}) {
		pos = sc.barPos
	}
	b := image.Rectangle{Min: image.Pt(d.X+pos.X, d.Y+pos.Y), Max: image.Pt(d.X+pos.X+w, d.Y+pos.Y+h)}
	s, err := a.openSurface(OverlayOptions{Bounds: b, Excluded: true})
	if err != nil {
		return
	}
	sc.bar = &recordBar{s: s, d: d}
}

func (a *App) recordBarTick(tb *recordBar) {
	_, changed := a.beginSurface(tb.s)
	u := &tb.s.u
	r := &a.rec
	// The time changes every second.
	if !changed && time.Since(tb.drawnAt) < 500*time.Millisecond || r.rec == nil {
		return
	}
	tb.drawnAt = time.Now()
	c := tb.s.c
	c.ClearTo(color.RGBA{})
	full := gfx.Rect{W: float32(c.Img.Rect.Dx()), H: float32(c.Img.Rect.Dy())}
	c.Fill(full, gfx.Alpha(colPanel, 0.96))
	c.Outline(full, max(1, u.px(1)), colBorder)
	h := u.px(barHeight - 12)
	y := u.px(6)
	x := u.px(8)
	// The grip drags the bar.
	grip := gfx.Rect{X: x, Y: y, W: u.px(14), H: h}
	drawGrip(u, grip)
	if u.hover("bar/grip", grip) {
		u.cursor = CursorMove
	}
	bounds := tb.s.o.Bounds()
	if u.press("bar/grip", grip) {
		tb.dragFrom, tb.barFrom = bounds.Min.Add(image.Pt(int(u.mx), int(u.my))), bounds.Min
	}
	if u.dragging("bar/grip") {
		at := bounds.Min.Add(image.Pt(int(u.mx), int(u.my)))
		min := tb.barFrom.Add(at.Sub(tb.dragFrom))
		tb.s.o.Move(image.Rectangle{Min: min, Max: min.Add(bounds.Size())})
	}
	if u.letGo("bar/grip") {
		a.over.barPos = tb.s.o.Bounds().Min.Sub(image.Pt(tb.d.X, tb.d.Y))
	}
	x = grip.Right() + u.px(6)
	st := r.rec.Status()
	dot := colDanger
	if st.Paused {
		dot = colDim
	}
	c.Circle(x+u.px(8), y+h/2, u.px(5), dot)
	u.label(u.bold(13), gfx.Rect{X: x + u.px(18), Y: y, W: u.px(52), H: h}, shortClock(st.Elapsed.Seconds()), colText, 0)
	x += u.px(72)
	button := func(id, glyph, hint string) bool {
		b := gfx.Rect{X: x, Y: y, W: h, H: h}
		x += u.px(barButton)
		return u.iconButton("bar/"+id, b, glyph, hint, false, true)
	}
	glyph, hint := icPause, u.t("Pause the recording")
	if st.Paused {
		glyph, hint = icRecord, u.t("Resume the recording")
	}
	if button("pause", glyph, withKey(hint, a.hotkeyLabel(hotkeyPause))) && !r.busy && !r.stopping {
		a.pauseRecording(!st.Paused, nil)
	}
	if button("stop", icStop, withKey(u.t("Stop and edit the recording"), a.hotkeyLabel(hotkeyRecord))) && !r.stopping {
		a.stopRecording(nil)
	}
	tb.s.end(image.Rectangle{})
}

// drawGrip draws the dots of a handle that drags a bar or a panel.
func drawGrip(u *ctx, grip gfx.Rect) {
	for i := 0; i < 3; i++ {
		for j := 0; j < 2; j++ {
			u.c.Circle(grip.X+u.px(4)+float32(j)*u.px(6), grip.Y+grip.H/2-u.px(6)+float32(i)*u.px(6), u.px(1.4), colDim)
		}
	}
}

// ---- The outline of a recorded region

// showRegionFrame outlines the region being recorded, left out of the recording and letting the mouse through.
func (a *App) showRegionFrame(d capture.Display, region image.Rectangle) {
	sc := &a.over
	sc.frame.close()
	sc.frame = nil
	if region.Empty() {
		return
	}
	s, err := a.openSurface(OverlayOptions{Bounds: displayBounds(d), ClickThrough: true, Excluded: true})
	if err != nil {
		return
	}
	box := gfx.Rect{X: float32(region.Min.X), Y: float32(region.Min.Y), W: float32(region.Dx()), H: float32(region.Dy())}
	w := float32(3)
	for _, e := range []gfx.Rect{{X: box.X - w, Y: box.Y - w, W: box.W + 2*w, H: w}, {X: box.X - w, Y: box.Bottom(), W: box.W + 2*w, H: w},
		{X: box.X - w, Y: box.Y, W: w, H: box.H}, {X: box.Right(), Y: box.Y, W: w, H: box.H}} {
		s.c.Fill(e, colDanger)
	}
	s.o.Present(s.c.Img, image.Rectangle{})
	sc.frame = s
}
