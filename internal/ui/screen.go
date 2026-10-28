package ui

import (
	"fmt"
	"image"
	"image/color"
	"math"
	"time"

	"golang.org/x/image/draw"

	"github.com/btelolabs/btelo-forge/internal/capture"
	"github.com/btelolabs/btelo-forge/internal/gfx"
	"github.com/btelolabs/btelo-forge/internal/paint"
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
	TakeKeyboard(on bool) // in front with the keyboard, or giving it back to the window that had it
	SetCursor(Cursor)
	Move(r image.Rectangle)
	Closed() bool
	Close()
}

// overlayHost is a window that can open overlays; without it, regions are not picked and the screen is not drawn
// on.
type overlayHost interface {
	OpenOverlay(o OverlayOptions) (Overlay, error)
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
	in := s.o.Input()
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
	pick    *regionPick
	draw    *screenDraw
	bar     *toolbar
	frame   *surface    // the outline of the region being recorded
	barPos  image.Point // where the user dragged the toolbar, relative to its display; zero is the default place
	barMove bool
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
	// The toolbar is there while recording or drawing.
	want := a.rec.rec != nil || sc.draw != nil
	switch {
	case want && sc.bar == nil:
		a.openToolbar()
	case !want && sc.bar != nil:
		sc.bar.s.close()
		sc.bar = nil
	}
	if sc.bar != nil {
		a.toolbarTick(sc.bar)
	}
}

// closeScreen closes every window over the screen, as the app closes.
func (a *App) closeScreen() {
	sc := &a.over
	if sc.pick != nil {
		sc.pick.s.close()
	}
	if sc.draw != nil {
		sc.draw.s.close()
	}
	if sc.bar != nil {
		sc.bar.s.close()
	}
	sc.frame.close()
	*sc = screenState{}
}

// ---- Picking a region

// regionPick dims a display and lets the user drag out a region of it.
type regionPick struct {
	s        *surface
	d        capture.Display
	from, to image.Point
	dragging bool
	done     func(region image.Rectangle, ok bool)
}

// pickRegion lets the user choose part of the display: a drag picks a rectangle, a click the whole display, Esc or
// the right button cancels. Without overlays it picks the whole display at once.
func (a *App) pickRegion(d capture.Display, done func(region image.Rectangle, ok bool)) {
	sc := &a.over
	if sc.pick != nil {
		return
	}
	s, err := a.openSurface(OverlayOptions{Bounds: displayBounds(d), Activate: true, Excluded: true})
	if err != nil {
		done(image.Rectangle{}, true)
		return
	}
	sc.pick = &regionPick{s: s, d: d, done: done}
}

func (a *App) pickTick(p *regionPick) {
	sc := &a.over
	in, changed := a.beginSurface(p.s)
	u := &p.s.u
	finish := func(r image.Rectangle, ok bool) {
		p.s.close()
		sc.pick = nil
		p.done(r, ok)
	}
	if p.s.o.Closed() {
		finish(image.Rectangle{}, false)
		return
	}
	for _, k := range in.Keys {
		if k.Code == keyEscape {
			finish(image.Rectangle{}, false)
			return
		}
	}
	if u.rightAt != nil {
		finish(image.Rectangle{}, false)
		return
	}
	at := image.Pt(int(u.mx), int(u.my))
	full := image.Rect(0, 0, p.d.Width, p.d.Height)
	if u.press("pick", gfx.Rect{W: float32(p.d.Width), H: float32(p.d.Height)}) {
		p.from, p.to, p.dragging = at, at, true
	}
	if u.dragging("pick") {
		p.to = at
	}
	if u.letGo("pick") {
		p.dragging = false
		r := image.Rectangle{Min: p.from, Max: p.to}.Canon().Intersect(full)
		if r.Dx() < 8 || r.Dy() < 8 {
			r = image.Rectangle{} // a click: the whole display
		}
		finish(r, true)
		return
	}
	if !changed {
		return
	}
	u.cursor = CursorCross
	c := p.s.c
	shade := color.RGBA{0, 0, 0, 110}
	c.ClearTo(shade)
	if p.dragging {
		r := image.Rectangle{Min: p.from, Max: p.to}.Canon().Intersect(full)
		// Nearly transparent rather than clear, so the window keeps the mouse there.
		draw.Draw(c.Img, r, image.NewUniform(color.RGBA{0, 0, 0, 1}), image.Point{}, draw.Src)
		box := gfx.Rect{X: float32(r.Min.X), Y: float32(r.Min.Y), W: float32(r.Dx()), H: float32(r.Dy())}
		w := max(u.px(2), 1)
		for _, e := range []gfx.Rect{{X: box.X - w, Y: box.Y - w, W: box.W + 2*w, H: w}, {X: box.X - w, Y: box.Bottom(), W: box.W + 2*w, H: w},
			{X: box.X - w, Y: box.Y, W: w, H: box.H}, {X: box.Right(), Y: box.Y, W: w, H: box.H}} {
			c.Fill(e, colAccent)
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
		hint := u.t("Drag to choose a region · click for the whole screen · Esc cancels")
		f := u.face(14)
		w := f.Measure(hint) + u.px(32)
		pill := gfx.Rect{X: (float32(p.d.Width) - w) / 2, Y: u.px(40), W: w, H: u.px(40)}
		c.Fill(pill, gfx.Alpha(colPanel, 0.92))
		u.label(f, pill, hint, colText, 0.5)
		// A crosshair through the pointer helps lining the corner up.
		c.Fill(gfx.Rect{X: 0, Y: u.my, W: float32(p.d.Width), H: 1}, gfx.Alpha(colWhite, 0.35))
		c.Fill(gfx.Rect{X: u.mx, Y: 0, W: 1, H: float32(p.d.Height)}, gfx.Alpha(colWhite, 0.35))
	}
	p.s.end(image.Rectangle{})
}

// ---- Drawing on the screen

// screenDraw is a transparent window over a display that the brush paints on; what it shows is in recordings and
// screenshots. While not drawing, the mouse goes through it to the programs below.
type screenDraw struct {
	s       *surface
	d       capture.Display
	doc     *paint.Doc
	drawing bool // the window takes the mouse to paint
	eraser  bool
	stroke  *paint.Stroke
	dirty   image.Rectangle
}

// toggleScreenDraw starts drawing on the chosen display, or stops taking the mouse while keeping what was drawn.
func (a *App) toggleScreenDraw() {
	sc := &a.over
	if sc.draw != nil {
		a.setScreenDrawing(!sc.draw.drawing)
		return
	}
	d, ok := a.chosenDisplay()
	if !ok {
		a.toast(a.u.t("No screen to record was found"), true)
		return
	}
	s, err := a.openSurface(OverlayOptions{Bounds: displayBounds(d)})
	if err != nil {
		a.toast(a.u.tf("Cannot draw on the screen: %s", a.describe(err)), true)
		return
	}
	doc, err := paint.NewFramed(d.Width, d.Height, "", color.NRGBA{})
	if err != nil {
		s.close()
		a.toast(a.u.tf("Cannot draw on the screen: %s", a.describe(err)), true)
		return
	}
	sc.draw = &screenDraw{s: s, d: d, doc: doc, drawing: true}
	s.o.TakeKeyboard(true)
	// The toolbar opens again above the new window.
	if sc.bar != nil {
		sc.bar.s.close()
		sc.bar = nil
	}
}

func (a *App) setScreenDrawing(on bool) {
	sd := a.over.draw
	if sd == nil || sd.drawing == on {
		return
	}
	if sd.stroke != nil {
		sd.stroke.End()
		sd.stroke = nil
	}
	sd.drawing = on
	sd.s.o.SetClickThrough(!on)
	// While drawing the window takes the keyboard too, for the brush keys; then it goes back where it was.
	sd.s.o.TakeKeyboard(on)
	sd.s.shown = false // shown again: the near-transparent floor that catches the mouse comes or goes
}

// clearScreenDraw wipes the drawing, which can be undone.
func (a *App) clearScreenDraw() {
	sd := a.over.draw
	if sd == nil {
		return
	}
	sd.doc.Select(image.Rectangle{})
	sd.doc.Clear()
	sd.s.shown = false
}

// closeScreenDraw removes the drawing from the screen.
func (a *App) closeScreenDraw() {
	sc := &a.over
	if sc.draw != nil {
		sc.draw.s.o.TakeKeyboard(false)
		sc.draw.s.close()
		sc.draw = nil
	}
}

func (a *App) drawTick(sd *screenDraw) {
	in, changed := a.beginSurface(sd.s)
	u := &sd.s.u
	if sd.s.o.Closed() {
		a.closeScreenDraw()
		return
	}
	area := gfx.Rect{W: float32(sd.d.Width), H: float32(sd.d.Height)}
	if sd.drawing {
		for _, k := range in.Keys {
			if larger, ok := a.brushSizeKey(k); ok {
				a.stepBrushSize(larger)
			}
		}
		u.cursor = CursorCross
		if u.rightAt != nil {
			a.setScreenDrawing(false) // the right button gives the mouse back; the drawing stays
		}
		if u.press("draw", area) {
			s, r, err := sd.doc.BeginStroke(a.brush, sd.eraser, float64(u.mx), float64(u.my))
			if err == nil {
				sd.stroke = s
				sd.dirty = sd.dirty.Union(r)
			}
		}
		if u.dragging("draw") && sd.stroke != nil {
			sd.dirty = sd.dirty.Union(sd.stroke.To(float64(u.mx), float64(u.my)))
		}
		if u.letGo("draw") && sd.stroke != nil {
			sd.stroke.End()
			sd.stroke = nil
		}
	}
	if !changed && sd.dirty.Empty() && sd.s.shown {
		return
	}
	dirty := sd.dirty
	if !sd.s.shown {
		dirty = image.Rect(0, 0, sd.d.Width, sd.d.Height)
	}
	sd.dirty = image.Rectangle{}
	if !dirty.Empty() {
		pic := sd.doc.Composite(dirty)
		draw.Draw(sd.s.c.Img, dirty, pic, dirty.Min, draw.Src)
		if sd.drawing {
			// A floor of one in 255 is invisible but makes the whole window take the mouse.
			img := sd.s.c.Img
			for y := dirty.Min.Y; y < dirty.Max.Y; y++ {
				i := img.PixOffset(dirty.Min.X, y)
				for x := dirty.Min.X; x < dirty.Max.X; x, i = x+1, i+4 {
					if img.Pix[i+3] == 0 {
						img.Pix[i+3] = 1
					}
				}
			}
		}
	}
	sd.s.end(dirty)
}

// ---- The floating toolbar

// toolbar sits at the top right of the recorded or drawn-on display, left out of recordings: the recording's
// time, pause and stop, and the drawing's tools — pen and eraser, the brush tip, the colour and the size. The tip
// and colour buttons open a panel under the bar, which grows to hold it.
type toolbar struct {
	s        *surface
	d        capture.Display
	dragFrom image.Point // the pointer on the desktop where a drag of the toolbar began
	barFrom  image.Point
	drawnAt  time.Time
	panel    barPanel
	shows    barLook // what the bar showed of the brush when last drawn
}

// barPanel is which panel is open under the toolbar.
type barPanel int

const (
	barNoPanel barPanel = iota
	barTips
	barColors
)

// barLook is what the toolbar shows of the brush, so it is drawn again when the brush changes from elsewhere: the
// brush keys over the drawing, the canvas, an MCP tool.
type barLook struct {
	size   float64
	tip    paint.Tip
	preset string
	color  color.NRGBA
}

// toolbarColors are the quick colours on the toolbar.
var toolbarColors = []color.NRGBA{
	{R: 0xf0, G: 0x40, B: 0x40, A: 255}, {R: 0xff, G: 0xc8, B: 0x2e, A: 255}, {R: 0x34, G: 0xd6, B: 0x8a, A: 255},
	{R: 0x3d, G: 0x8b, B: 0xff, A: 255}, {R: 0xff, G: 0xff, B: 0xff, A: 255},
}

// toolbarPalette are the colours of the toolbar's colour panel.
var toolbarPalette = []color.NRGBA{
	{A: 255}, {R: 0x5f, G: 0x63, B: 0x68, A: 255}, {R: 0xb0, G: 0xb4, B: 0xb8, A: 255}, {R: 0xff, G: 0xff, B: 0xff, A: 255},
	{R: 0x8d, G: 0x5a, B: 0x3b, A: 255}, {R: 0xe5, G: 0x39, B: 0x35, A: 255}, {R: 0xfb, G: 0x8c, B: 0x00, A: 255},
	{R: 0xfd, G: 0xd8, B: 0x35, A: 255}, {R: 0x7c, G: 0xb3, B: 0x42, A: 255}, {R: 0x2e, G: 0x7d, B: 0x32, A: 255},
	{R: 0x00, G: 0xac, B: 0xc1, A: 255}, {R: 0x1e, G: 0x88, B: 0xe5, A: 255}, {R: 0x39, G: 0x49, B: 0xab, A: 255},
	{R: 0x8e, G: 0x24, B: 0xaa, A: 255}, {R: 0xd8, G: 0x1b, B: 0x60, A: 255}, {R: 0xf4, G: 0x8f, B: 0xb1, A: 255},
}

// The toolbar's measures, in logical pixels.
const (
	barHeight     = 52
	barButton     = 42 // an icon button and the gap after it
	barSwatch     = 24
	barSizeSlider = 96
	barSizeLabel  = 52
)

func (a *App) openToolbar() {
	sc := &a.over
	d, ok := a.toolbarDisplay()
	if !ok {
		return
	}
	scale := float32(1)
	if host, ok := a.host.(interface{ Scale() float32 }); ok {
		scale = host.Scale()
	}
	w, h := int(a.toolbarWidth()*scale), int(barHeight*scale)
	pos := image.Pt(d.Width-w-int(24*scale), int(24*scale))
	if sc.barPos != (image.Point{}) {
		pos = sc.barPos
	}
	b := image.Rectangle{Min: image.Pt(d.X+pos.X, d.Y+pos.Y), Max: image.Pt(d.X+pos.X+w, d.Y+pos.Y+h)}
	s, err := a.openSurface(OverlayOptions{Bounds: b, Excluded: true})
	if err != nil {
		return
	}
	sc.bar = &toolbar{s: s, d: d}
}

// toolbarDisplay is the display being recorded, or drawn on.
func (a *App) toolbarDisplay() (capture.Display, bool) {
	if a.rec.rec != nil {
		return a.rec.rec.Options().Display, true
	}
	if sd := a.over.draw; sd != nil {
		return sd.d, true
	}
	return a.chosenDisplay()
}

// toolbarWidth is how wide the toolbar's bar is, in logical pixels: the grip, the recording's part, pen and eraser,
// the brush's tip, colours and size while the pen is chosen, and the drawing's commands once there is a drawing.
func (a *App) toolbarWidth() float32 {
	w := float32(28 + 2*barButton + 8)
	if a.penChosen() {
		w += barButton + 4 + float32(len(toolbarColors)*barSwatch) + 2 + 26 + 10 + barSizeSlider + barSizeLabel
	}
	if a.rec.rec != nil {
		w += 72 + 2*barButton + 10
	}
	if a.over.draw != nil {
		w += 3 * barButton
	}
	return w
}

// penChosen reports whether the pen draws on the screen now, when the toolbar offers the brush's settings.
func (a *App) penChosen() bool {
	sd := a.over.draw
	return sd != nil && sd.drawing && !sd.eraser
}

// barLookOf is what the toolbar shows of the brush now.
func (a *App) barLookOf() barLook {
	return barLook{size: a.brush.Size, tip: a.brush.Tip, preset: a.brushKey(), color: a.brush.Color}
}

// toolbarPanel lays out the open panel's cells across the toolbar's width w, under the bar; height is how much
// taller the panel makes the toolbar. Tips come first, then the brush presets; or the palette's colours.
func (a *App) toolbarPanel(u *ctx, tb *toolbar, w float32) (cells []gfx.Rect, height float32) {
	var widths []float32
	cell := u.px(34)
	switch tb.panel {
	case barTips:
		for range paint.Tips {
			widths = append(widths, cell)
		}
		for range paint.Presets() {
			widths = append(widths, u.px(76))
		}
	case barColors:
		cell = u.px(barSwatch)
		for range toolbarPalette {
			widths = append(widths, cell)
		}
	default:
		return nil, 0
	}
	pad, gap := u.px(8), u.px(4)
	top := u.px(barHeight)
	x, y := pad, top
	for _, cw := range widths {
		if x+cw > w-pad && x > pad {
			x, y = pad, y+cell+gap
		}
		cells = append(cells, gfx.Rect{X: x, Y: y, W: cw, H: cell})
		x += cw + gap
	}
	return cells, y + cell + pad - top
}

func (a *App) toolbarTick(tb *toolbar) {
	_, changed := a.beginSurface(tb.s)
	u := &tb.s.u
	if !a.penChosen() {
		tb.panel = barNoPanel // the brush's panels go with its settings
	}
	// The toolbar grows and shrinks with what it holds, keeping its top right corner where it is, and moves up when
	// that would take it past the bottom of its display.
	if b := tb.s.o.Bounds(); u.s > 0 && u.active == "" {
		w := float32(int(a.toolbarWidth() * u.s))
		_, ph := a.toolbarPanel(u, tb, w)
		size := image.Pt(int(w), int(u.px(barHeight)+ph))
		if size != b.Size() {
			nb := image.Rectangle{Min: image.Pt(b.Max.X-size.X, b.Min.Y), Max: image.Pt(b.Max.X, b.Min.Y+size.Y)}
			if over := nb.Max.Y - (tb.d.Y + tb.d.Height); over > 0 {
				nb = nb.Sub(image.Pt(0, min(over, nb.Min.Y-tb.d.Y)))
			}
			tb.s.o.Move(nb)
			tb.s.c.Resize(nb.Dx(), nb.Dy())
			changed = true
		}
	}
	r := &a.rec
	sd := a.over.draw
	look := a.barLookOf()
	// The recording's time changes every second.
	if !changed && look == tb.shows && !(r.rec != nil && time.Since(tb.drawnAt) > 500*time.Millisecond) {
		return
	}
	tb.drawnAt, tb.shows = time.Now(), look
	c := tb.s.c
	c.ClearTo(color.RGBA{})
	full := gfx.Rect{W: float32(c.Img.Rect.Dx()), H: float32(c.Img.Rect.Dy())}
	c.Fill(full, gfx.Alpha(colPanel, 0.96))
	c.Outline(full, max(1, u.px(1)), colBorder)
	h := u.px(barHeight - 12)
	y := u.px(6)
	x := u.px(8)
	// The grip drags the toolbar.
	grip := gfx.Rect{X: x, Y: y, W: u.px(14), H: h}
	for i := 0; i < 3; i++ {
		for j := 0; j < 2; j++ {
			c.Circle(grip.X+u.px(4)+float32(j)*u.px(6), grip.Y+h/2-u.px(6)+float32(i)*u.px(6), u.px(1.4), colDim)
		}
	}
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
	button := func(id, glyph, hint string, on bool) bool {
		b := gfx.Rect{X: x, Y: y, W: h, H: h}
		x += u.px(barButton)
		return u.iconButton("bar/"+id, b, glyph, hint, on, true)
	}
	if r.rec != nil {
		st := r.rec.Status()
		dot := colDanger
		if st.Paused {
			dot = colDim
		}
		c.Circle(x+u.px(8), y+h/2, u.px(5), dot)
		u.label(u.bold(13), gfx.Rect{X: x + u.px(18), Y: y, W: u.px(52), H: h}, shortClock(st.Elapsed.Seconds()), colText, 0)
		x += u.px(72)
		glyph, hint := icPause, u.t("Pause the recording")
		if st.Paused {
			glyph, hint = icRecord, u.t("Resume the recording")
		}
		if button("pause", glyph, withKey(hint, a.hotkeyLabel(hotkeyPause)), false) && !r.busy && !r.stopping {
			a.pauseRecording(!st.Paused, nil)
		}
		if button("stop", icStop, withKey(u.t("Stop and edit the recording"), a.hotkeyLabel(hotkeyRecord)), false) && !r.stopping {
			a.stopRecording(nil)
		}
		c.Fill(gfx.Rect{X: x + u.px(4), Y: y + u.px(8), W: max(1, u.px(1)), H: h - u.px(16)}, colBorder)
		x += u.px(10)
	}
	// pen draws with the brush, starting a drawing when there is none.
	pen := func() {
		if sd == nil {
			a.toggleScreenDraw()
			sd = a.over.draw
			return
		}
		sd.eraser = false
		a.setScreenDrawing(true)
	}
	drawing := sd != nil && sd.drawing
	if button("pen", icBrush, withKey(u.t("Draw on the screen"), a.hotkeyLabel(hotkeyDraw)), drawing && !sd.eraser) {
		pen()
	}
	eraserAt := gfx.Rect{X: x, Y: y, W: h, H: h}
	erasing := drawing && sd.eraser
	clicked := button("eraser", "", u.t("Eraser"), erasing)
	drawEraserGlyph(u, eraserAt, erasing)
	if clicked {
		if sd == nil {
			a.toggleScreenDraw()
			sd = a.over.draw
		}
		if sd != nil {
			sd.eraser = true
			a.setScreenDrawing(true)
		}
	}
	if a.penChosen() {
		x = a.drawBarBrush(u, tb, x, y, h, pen)
	}
	if sd != nil {
		if button("undo", icUndo, u.t("Undo"), false) && sd.stroke == nil {
			sd.doc.Undo()
			sd.s.shown = false
		}
		if button("clear", icDelete, u.t("Clear the drawing"), false) {
			a.clearScreenDraw()
		}
		if button("close", icClose, u.t("Stop drawing and remove the drawing"), false) {
			a.closeScreenDraw()
		}
	}
	a.drawToolbarPanel(u, tb, full.W, pen)
	tb.s.end(image.Rectangle{})
}

// drawBarBrush draws the brush's settings on the bar from x: the tip, the quick colours, the colour button and the
// size; it returns where they end.
func (a *App) drawBarBrush(u *ctx, tb *toolbar, x, y, h float32, pen func()) float32 {
	c := u.c
	toggle := func(p barPanel) {
		if tb.panel == p {
			tb.panel = barNoPanel
		} else {
			tb.panel = p
		}
	}
	// The tip button shows the brush's tip and opens the tips and presets.
	tipAt := gfx.Rect{X: x, Y: y, W: h, H: h}
	if u.iconButton("bar/tip", tipAt, "", u.t("Brush tip"), tb.panel == barTips, true) {
		toggle(barTips)
	}
	a.drawBarTip(u, tipAt.Inset(u.px(9)))
	x += u.px(barButton + 4)
	for i, col := range toolbarColors {
		cell := gfx.Rect{X: x + u.px(3), Y: y + h/2 - u.px(9), W: u.px(18), H: u.px(18)}
		if a.barSwatch(u, fmt.Sprintf("bar/color/%d", i), cell, col) {
			pen()
		}
		x += u.px(barSwatch)
	}
	// The colour button shows the brush's colour and opens the palette.
	more := gfx.Rect{X: x + u.px(2), Y: y + h/2 - u.px(13), W: u.px(26), H: u.px(26)}
	if tb.panel == barColors {
		c.RoundRect(more, u.px(5), colSelected)
	} else if u.hover("bar/colors", more) {
		c.RoundRect(more, u.px(5), colSurfaceHi)
	}
	drawColourSwatch(u, more.Inset(u.px(6)), a.brush.Color)
	u.hint("bar/colors", u.t("More colours"), more)
	if u.clicked("bar/colors", more) {
		toggle(barColors)
	}
	x = more.Right() + u.px(10)
	// The size, on a scale even in the brush keys' steps: small sizes get as much room as large ones.
	smaller, larger := a.brushSizeKeyLabels()
	sizeAt := gfx.Rect{X: x, Y: y + h/2 - u.px(12), W: u.px(barSizeSlider), H: u.px(24)}
	t := math.Log(max(a.brush.Size, 1)) / math.Log(maxBrushSize)
	moved, done := u.slider("bar/size", sizeAt, &t, 0, 1, true)
	if moved {
		a.brush.Size = min(max(math.Round(math.Exp(t*math.Log(maxBrushSize))), 1), maxBrushSize)
	}
	if done {
		a.saveBrush()
	}
	u.hint("bar/size", u.tf("Brush size: %s smaller, %s larger", smaller, larger), sizeAt)
	u.label(u.face(12), gfx.Rect{X: sizeAt.Right() + u.px(4), Y: y, W: u.px(barSizeLabel - 4), H: h}, fmt.Sprintf("%.0f px", a.brush.Size), colText, 0)
	return sizeAt.Right() + u.px(barSizeLabel)
}

// drawBarTip draws the brush's tip in r: the preset's stroke, a picture tip's shape, or the plain tip.
func (a *App) drawBarTip(u *ctx, r gfx.Rect) {
	switch p, ok := a.chosenPreset(); {
	case ok:
		drawTipShape(u, p.Brush.Shape, r)
	case a.brush.Tip == paint.TipShaped:
		drawTipShape(u, a.brush.Shape, r)
	default:
		drawTip(u, a.brush.Tip, r, colText)
	}
}

// barSwatch is a colour to click on the toolbar, outlined when it is the brush's; it reports a click, having made
// it the brush's colour.
func (a *App) barSwatch(u *ctx, id string, cell gfx.Rect, col color.NRGBA) bool {
	u.c.Fill(cell, color.RGBA{col.R, col.G, col.B, 255})
	if a.brush.Color == col {
		u.c.Outline(cell.Inset(-u.px(3)), u.px(2), colText)
	} else {
		u.c.Outline(cell, max(1, u.px(1)), colBorder)
	}
	if u.hover(id, cell) {
		u.cursor = CursorHand
	}
	if !u.clicked(id, cell.Inset(-u.px(3))) {
		return false
	}
	a.setBrushColor(col)
	return true
}

// drawToolbarPanel draws the open panel under the bar; choosing a tip, a preset or a colour draws with it.
func (a *App) drawToolbarPanel(u *ctx, tb *toolbar, w float32, pen func()) {
	cells, height := a.toolbarPanel(u, tb, w)
	if height == 0 {
		return
	}
	u.c.Fill(gfx.Rect{X: u.px(8), Y: u.px(barHeight - 1), W: w - u.px(16), H: max(1, u.px(1))}, colBorder)
	b := &a.brush
	switch tb.panel {
	case barTips:
		presets := paint.Presets()
		chosen, isPreset := a.chosenPreset()
		for i, cell := range cells {
			id := fmt.Sprintf("bar/tip/%d", i)
			plain := i < len(paint.Tips)
			var on bool
			var name string
			if plain {
				tip := paint.Tips[i]
				on, name = b.Tip == tip, u.t(tipNames[tip])
			} else {
				p := presets[i-len(paint.Tips)]
				on, name = isPreset && p.ID == chosen.ID, u.t(p.Name)
			}
			bg := colSurface
			switch {
			case on:
				bg = colSelected
			case u.hover(id, cell):
				bg = colSurfaceHi
			}
			u.c.RoundRect(cell, u.px(5), bg)
			if plain {
				drawTip(u, paint.Tips[i], cell, colText)
			} else {
				sr := cell.Inset(u.px(3))
				drawCoverage(u, presetSample(presets[i-len(paint.Tips)], int(sr.W), int(sr.H), u.s), sr)
			}
			u.hint(id, name, cell)
			if !u.clicked(id, cell) {
				continue
			}
			if plain {
				size := b.Size
				b.Tip = paint.Tips[i]
				a.chosenBrush(size)
				a.saveBrush()
			} else {
				a.choosePreset(presets[i-len(paint.Tips)])
			}
			pen()
		}
	case barColors:
		for i, cell := range cells {
			if a.barSwatch(u, fmt.Sprintf("bar/palette/%d", i), cell.Inset(u.px(3)), toolbarPalette[i]) {
				pen()
			}
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
