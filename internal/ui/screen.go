package ui

import (
	"image"
	"image/color"
	"time"

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

// openScaledSurface opens an overlay sized by layout(scale) and automatically resizes it to the target display's
// actual backing scale when it differs from the main window's scale (mixed-DPI multi-monitor setups).
func (a *App) openScaledSurface(o OverlayOptions, layout func(scale float32) image.Rectangle) (*surface, error) {
	initial := a.overlayScale()
	o.Bounds = layout(initial)
	s, err := a.openSurface(o)
	if err != nil {
		return nil, err
	}
	if actual := s.o.Scale(); actual > 0 && actual != initial {
		if nb := layout(actual); !nb.Empty() && nb != o.Bounds {
			s.o.Move(nb)
			s.c.Resize(nb.Dx(), nb.Dy())
		}
	}
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
	ask    *surface    // the question before deleting the recording, when asked from the bar
	frame  *surface    // the outline of the region being recorded
	notice *shotNotice // the line saying a screenshot was taken
	barPos image.Point // where the user dragged the recording bar, relative to its display; zero is the default place
	flow   barFlow     // the options bar being laid out in rows

	askDrawn time.Time
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
	switch want := a.rec.ask == askOverScreen && sc.bar != nil; {
	case want && sc.ask == nil:
		a.openDiscardAsk()
	case !want && sc.ask != nil:
		sc.ask.close()
		sc.ask = nil
	}
	if sc.ask != nil {
		a.discardAskTick(sc.ask)
	}
	if sc.notice != nil {
		a.shotNoticeTick(sc.notice)
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
	sc.ask.close()
	a.closeScreenDraw()
	sc.frame.close()
	a.closeShotNotice()
	*sc = screenState{}
}

// ---- The recording's bar

// recordBar sits at the top right of the recorded display, left out of the recording: the time, audio activity,
// pause, stop and discard. Drawing on the screen has its own panel; this bar is only the recording's.
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
	barMeters = 24 // compact system/mic audio level meters beside the elapsed time
	barWidth  = 8 + 14 + 6 + 72 + barMeters + 3*barButton + 2

	discardAskWidth, discardAskHeight = 330, 128 // the question before deleting the recording
)

// clampBarRect keeps a floating bar inside screen so it can never be dragged or persisted off-screen across displays.
func clampBarRect(r, screen image.Rectangle) image.Rectangle {
	w, h := r.Dx(), r.Dy()
	if w > screen.Dx() {
		w = screen.Dx()
	}
	if h > screen.Dy() {
		h = screen.Dy()
	}
	x := min(max(r.Min.X, screen.Min.X), screen.Max.X-w)
	y := min(max(r.Min.Y, screen.Min.Y), screen.Max.Y-h)
	return image.Rect(x, y, x+w, y+h)
}

func (a *App) openRecordBar() {
	sc := &a.over
	if a.rec.rec == nil {
		return
	}
	d := a.rec.rec.Options().Display
	screen := displayBounds(d)
	layout := func(scale float32) image.Rectangle {
		w, h := int(barWidth*scale), int(barHeight*scale)
		pos := image.Pt(d.Width-w-int(24*scale), int(24*scale))
		if sc.barPos != (image.Point{}) {
			pos = sc.barPos
		}
		return clampBarRect(image.Rect(d.X+pos.X, d.Y+pos.Y, d.X+pos.X+w, d.Y+pos.Y+h), screen)
	}
	s, err := a.openScaledSurface(OverlayOptions{Excluded: true}, layout)
	if err != nil {
		return
	}
	sc.bar = &recordBar{s: s, d: d}
}

func (a *App) recordBarTick(tb *recordBar) {
	_, changed := a.beginSurface(tb.s)
	u := &tb.s.u
	r := &a.rec
	if r.rec == nil {
		return
	}
	st := r.rec.Status()
	// Refresh faster while actively recording so audio level meters and the recording indicator stay responsive.
	cadence := 500 * time.Millisecond
	if !st.Paused && !r.stopping {
		cadence = 150 * time.Millisecond
	}
	if !changed && time.Since(tb.drawnAt) < cadence {
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
	// The grip drags the bar, clamped inside its display.
	grip := gfx.Rect{X: x, Y: y, W: u.px(14), H: h}
	drawGrip(u, grip)
	if u.hover("bar/grip", grip) {
		u.cursor = CursorMove
	}
	bounds := tb.s.o.Bounds()
	screen := displayBounds(tb.d)
	if u.press("bar/grip", grip) {
		tb.dragFrom, tb.barFrom = bounds.Min.Add(image.Pt(int(u.mx), int(u.my))), bounds.Min
	}
	if u.dragging("bar/grip") {
		at := bounds.Min.Add(image.Pt(int(u.mx), int(u.my)))
		minPt := tb.barFrom.Add(at.Sub(tb.dragFrom))
		tb.s.o.Move(clampBarRect(image.Rectangle{Min: minPt, Max: minPt.Add(bounds.Size())}, screen))
	}
	if u.letGo("bar/grip") {
		clamped := clampBarRect(tb.s.o.Bounds(), screen)
		a.over.barPos = clamped.Min.Sub(screen.Min)
	}
	x = grip.Right() + u.px(6)
	dot := colDanger
	switch {
	case st.Paused || r.stopping:
		dot = colDim
	case time.Now().UnixMilli()/500%2 == 0:
		dot = gfx.Mix(colDanger, colPanel, 0.45)
	}
	c.Circle(x+u.px(8), y+h/2, u.px(5), dot)
	if r.stopping {
		status := u.t("Saving…")
		if r.discarding {
			status = u.t("Deleting…")
		}
		u.label(u.bold(12), gfx.Rect{X: x + u.px(18), Y: y, W: u.px(52 + barMeters), H: h}, status, colDim, 0)
		x += u.px(72 + barMeters)
	} else {
		u.label(u.bold(13), gfx.Rect{X: x + u.px(18), Y: y, W: u.px(52), H: h}, shortClock(st.Elapsed.Seconds()), colText, 0)
		x += u.px(72)
		// Compact horizontal audio activity bars for system audio and/or microphone.
		o := r.rec.Options()
		var levels []float32
		if o.SystemAudio {
			levels = append(levels, st.System)
		}
		if o.Microphone {
			levels = append(levels, st.Mic)
		}
		if len(levels) > 0 {
			mw := u.px(barMeters - 6)
			my := y + h/2 - u.px(3)*float32(len(levels))
			for i, level := range levels {
				bar := gfx.Rect{X: x, Y: my + float32(i)*u.px(6), W: mw, H: u.px(4)}
				c.Fill(bar, colSurfaceHi)
				c.Fill(gfx.Rect{X: bar.X, Y: bar.Y, W: max(bar.W*min(level, 1), 0), H: bar.H}, colAccent)
			}
		}
		x += u.px(barMeters)
	}
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
	if button("discard", icDelete, u.t("Delete the recording")) && !r.stopping && !r.busy {
		a.askDiscard(askOverScreen)
	}
	tb.s.end(image.Rectangle{})
}

// openDiscardAsk opens the question before deleting the recording, under the bar, or over it at the display's bottom.
// When it cannot open, the window asks instead.
func (a *App) openDiscardAsk() {
	sc := &a.over
	bar := sc.bar.s.o.Bounds()
	scale := sc.bar.s.o.Scale()
	w, h := int(discardAskWidth*scale), int(discardAskHeight*scale)
	gap := int(6 * scale)
	screen := displayBounds(sc.bar.d)
	at := image.Pt(bar.Max.X-w, bar.Max.Y+gap)
	if at.Y+h > screen.Max.Y {
		at.Y = bar.Min.Y - gap - h
	}
	at.X = max(at.X, screen.Min.X)
	s, err := a.openSurface(OverlayOptions{Bounds: image.Rectangle{Min: at, Max: at.Add(image.Pt(w, h))}, Excluded: true})
	if err != nil {
		a.rec.ask = askInWindow
		if host := a.recorder(); host != nil {
			host.Restore()
		}
		return
	}
	sc.ask = s
}

// discardAskTick draws the question over the screen.
func (a *App) discardAskTick(s *surface) {
	_, changed := a.beginSurface(s)
	// The Delete button waits for a pause or resume to finish.
	if !changed && time.Since(a.over.askDrawn) < 500*time.Millisecond {
		return
	}
	a.over.askDrawn = time.Now()
	s.c.ClearTo(color.RGBA{})
	a.drawDiscardQuestion(&s.u, gfx.Rect{W: float32(s.c.Img.Rect.Dx()), H: float32(s.c.Img.Rect.Dy())})
	s.end(image.Rectangle{})
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
