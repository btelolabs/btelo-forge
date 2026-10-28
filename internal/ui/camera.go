package ui

import (
	"context"
	"errors"
	"image"
	"math"
	"strconv"
	"sync"
	"time"

	"golang.org/x/image/draw"

	"github.com/btelolabs/btelo-forge/internal/capture"
	"github.com/btelolabs/btelo-forge/internal/gfx"
)

// The camera is shown in a window of its own on the desktop, above every other, cropped to a rounded rectangle or
// a circle. Being on the screen, it is in every recording and stream of the screen, as a streamer's face in a
// corner is. The camera's frames are drawn into the window by the camera's own goroutine, so that the main window
// is not redrawn thirty times a second for them; the main goroutine only moves and resizes the window.

// cameraSettings are the camera window's choices, kept between runs.
type cameraSettings struct {
	On     bool   `json:"on"`
	Camera string `json:"camera"` // capture.Camera.ID; "" or a missing one shows the first camera
	Width  int    `json:"width"`  // logical pixels
	Shape  int    `json:"shape"`  // index into cameraShapes
	Radius int    `json:"radius"` // percent of the shorter side: 50 makes a square a circle
	Border int    `json:"border"` // logical pixels
	Mirror bool   `json:"mirror"`
	Placed bool   `json:"placed"` // X and Y say where the user put it
	X      int    `json:"x"`      // its top left on the desktop, in physical pixels
	Y      int    `json:"y"`
}

// cameraShapes are the shapes offered, as width:height.
var cameraShapes = []image.Point{{1, 1}, {4, 3}, {16, 9}, {3, 4}, {9, 16}}

var cameraShapeNames = msgs("Square", "4:3", "16:9", "3:4 upright", "9:16 upright")

const (
	cameraMinWidth, cameraMaxWidth = 120, 960
	cameraMaxBorder                = 12
)

func defaultCameraSettings() cameraSettings {
	return cameraSettings{Width: 280, Radius: 50, Border: 3, Mirror: true}
}

// size is the window's size in physical pixels at a scale, both sides even.
func (s cameraSettings) size(scale float32) image.Point {
	shape := cameraShapes[min(max(s.Shape, 0), len(cameraShapes)-1)]
	w := min(max(s.Width, cameraMinWidth), cameraMaxWidth)
	pw := int(float32(w)*scale+0.5) &^ 1
	return image.Pt(pw, max(int(float64(pw)*float64(shape.Y)/float64(shape.X)+0.5)&^1, 2))
}

// cameraState is the camera window and its dialog.
type cameraState struct {
	listed  bool
	listing bool
	cams    []capture.Camera
	listErr error
	waiting []func() // to call once the cameras are listed
	open    bool     // the dialog
	bubble  *cameraBubble
	scroll  float32
	content float32
}

// cameraBubble is the camera's window on the desktop.
type cameraBubble struct {
	mu      sync.Mutex // guards the window and what is drawn into it, from the camera's goroutine and the main one
	o       Overlay
	closed  bool
	look    cameraLook
	img     *image.RGBA // what is shown
	last    []byte      // the latest frame, kept to redraw at a new size until the camera delivers it
	lastAt  image.Point // its size
	mask    []uint8     // how much of each pixel is inside the shape
	inner   []uint8     // how much is inside the border
	maskFor cameraLook
	feed    *capture.CameraFeed
	starts  int // camera starts asked for; only the latest one's feed is kept

	// The main goroutine's own: a drag in progress, and a camera start waiting for the size to settle.
	drag      int // 0, dragMove or dragResize
	from      image.Point
	fromBound image.Rectangle
	restartAt time.Time
	drawnAt   time.Time
}

const (
	dragMove = 1 + iota
	dragResize
)

// cameraLook is how the window draws: its size, corners and border, in physical pixels.
type cameraLook struct {
	size   image.Point
	radius float64
	border float64
}

func (a *App) cameraScale() float32 {
	if host, ok := a.host.(interface{ Scale() float32 }); ok {
		return host.Scale()
	}
	return 1
}

func (a *App) cameraLook() cameraLook {
	s := a.settings.Camera
	scale := a.cameraScale()
	size := s.size(scale)
	short := float64(min(size.X, size.Y))
	return cameraLook{size: size, radius: short * float64(min(max(s.Radius, 0), 50)) / 100,
		border: min(float64(min(max(s.Border, 0), cameraMaxBorder))*float64(scale), short/4)}
}

// listCameras looks up the cameras, once, and then calls done, when not nil, on the main goroutine; when they
// cannot be looked up yet (ffmpeg is not ready) it calls done at once with listErr saying why.
func (a *App) listCameras(done func()) {
	c := &a.cam
	if done != nil {
		c.waiting = append(c.waiting, done)
	}
	switch {
	case c.listing:
		return
	case c.listed:
	case !a.tools.ready:
		c.listErr = errToolsNotReady
	default:
		c.listing, c.listErr = true, nil
		a.background(func(ctx context.Context) {
			cams, err := capture.Cameras(ctx)
			a.post(func() {
				c.listing, c.listed, c.cams, c.listErr = false, err == nil, cams, err
				a.camerasListed()
			})
		})
		return
	}
	a.camerasListed()
}

func (a *App) camerasListed() {
	waiting := a.cam.waiting
	a.cam.waiting = nil
	for _, fn := range waiting {
		fn()
	}
}

// chosenCamera is the camera the settings name, or the first.
func (a *App) chosenCamera() (capture.Camera, bool) {
	cams := a.cam.cams
	if len(cams) == 0 {
		return capture.Camera{}, false
	}
	for _, c := range cams {
		if c.ID == a.settings.Camera.Camera {
			return c, true
		}
	}
	return cams[0], true
}

// showCamera puts the camera on the screen, or takes it off; done, when not nil, hears whether that worked.
func (a *App) showCamera(on bool, done func(error)) {
	report := func(err error) {
		if done != nil {
			done(err)
		}
	}
	if !on {
		a.closeCamera()
		a.settings.Camera.On = false
		a.saveSettings()
		report(nil)
		return
	}
	if a.cam.bubble != nil {
		report(nil)
		return
	}
	if !capture.Supported() {
		report(capture.ErrUnsupported)
		return
	}
	if _, ok := a.host.(overlayHost); !ok {
		report(capture.ErrUnsupported)
		return
	}
	if !a.tools.ready {
		report(errToolsNotReady)
		return
	}
	a.listCameras(func() {
		if a.cam.listErr != nil {
			report(a.cam.listErr)
			return
		}
		if _, ok := a.chosenCamera(); !ok {
			report(errors.New(a.u.t("No camera was found")))
			return
		}
		if err := a.openCamera(); err != nil {
			report(err)
			return
		}
		a.settings.Camera.On = true
		a.saveSettings()
		report(nil)
	})
}

// cameraBounds is where the window goes: where the user put it, kept on a display, or the bottom right of the main
// display.
func (a *App) cameraBounds(size image.Point) image.Rectangle {
	s := a.settings.Camera
	var screens []image.Rectangle
	for _, d := range a.rec.displays {
		screens = append(screens, displayBounds(d))
	}
	main := image.Rect(0, 0, 1920, 1080)
	if d, ok := a.chosenDisplay(); ok {
		main = displayBounds(d)
	}
	if s.Placed {
		r := image.Rectangle{Min: image.Pt(s.X, s.Y), Max: image.Pt(s.X+size.X, s.Y+size.Y)}
		for _, sc := range screens {
			if sc.Overlaps(r) {
				return r
			}
		}
		if len(screens) == 0 {
			return r
		}
	}
	margin := int(48 * a.cameraScale())
	min := image.Pt(main.Max.X-size.X-margin, main.Max.Y-size.Y-2*margin)
	return image.Rectangle{Min: min, Max: min.Add(size)}
}

// openCamera opens the window and starts the camera.
func (a *App) openCamera() error {
	look := a.cameraLook()
	ov, err := a.host.(overlayHost).OpenOverlay(OverlayOptions{Bounds: a.cameraBounds(look.size)})
	if err != nil {
		return err
	}
	b := &cameraBubble{o: ov, look: look}
	b.mu.Lock()
	b.drawLocked()
	b.mu.Unlock()
	a.cam.bubble = b
	a.restartCamera()
	return nil
}

// restartCamera opens the chosen camera at the window's size, in the background; the window shows the last
// picture meanwhile. A camera that cannot be opened closes the window and says why.
func (a *App) restartCamera() {
	b := a.cam.bubble
	cam, ok := a.chosenCamera()
	if b == nil || !ok {
		return
	}
	b.mu.Lock()
	b.starts++
	start, old := b.starts, b.feed
	b.feed = nil
	size := b.look.size
	b.mu.Unlock()
	mirror := a.settings.Camera.Mirror
	a.background(func(context.Context) {
		if old != nil {
			old.Stop() // a camera is opened by one program, and one reader, at a time
		}
		b.mu.Lock()
		stale := b.closed || b.starts != start
		b.mu.Unlock()
		if stale {
			return
		}
		var feed *capture.CameraFeed
		feed, err := capture.StartCamera(capture.CameraOptions{Camera: cam, Size: size, Mirror: mirror}, func(rgba []byte) {
			b.frame(rgba, size)
		})
		b.mu.Lock()
		stale = b.closed || b.starts != start
		if !stale && err == nil {
			b.feed = feed
		}
		b.mu.Unlock()
		if stale {
			if feed != nil {
				feed.Stop()
			}
			return
		}
		if err == nil {
			// The window is looked after when the main goroutine runs; a camera that stops wakes it.
			go func() {
				<-feed.Done()
				a.host.Wake()
			}()
		}
		if err != nil {
			a.post(func() {
				if a.cam.bubble == b {
					a.toast(a.u.tf("The camera could not be shown: %s", a.describe(err)), true)
					a.showCamera(false, nil)
				}
			})
		}
	})
}

// closeCamera takes the window off the screen and lets go of the camera.
func (a *App) closeCamera() {
	b := a.cam.bubble
	if b == nil {
		return
	}
	a.cam.bubble = nil
	b.mu.Lock()
	b.closed = true
	feed := b.feed
	b.feed = nil
	b.o.Close()
	b.mu.Unlock()
	if feed != nil {
		go feed.Stop()
	}
}

// frame shows a picture from the camera; it runs on the camera's goroutine.
func (b *cameraBubble) frame(rgba []byte, size image.Point) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	b.last = append(b.last[:0], rgba...)
	b.lastAt = size
	b.drawLocked()
}

// drawLocked draws the latest picture, scaled to the window when the camera has not caught up with a new size, into
// the shape, with the border, and shows it.
func (b *cameraBubble) drawLocked() {
	look := b.look
	if b.img == nil || b.img.Rect.Size() != look.size {
		b.img = image.NewRGBA(image.Rectangle{Max: look.size})
	}
	if b.maskFor != look {
		b.mask, b.inner = shapeMasks(look)
		b.maskFor = look
	}
	pix := b.img.Pix
	switch {
	case b.last == nil:
		for i := 0; i < len(pix); i += 4 {
			pix[i], pix[i+1], pix[i+2], pix[i+3] = colSurface.R, colSurface.G, colSurface.B, 255
		}
	case b.lastAt == look.size:
		copy(pix, b.last)
	default:
		src := &image.RGBA{Pix: b.last, Stride: b.lastAt.X * 4, Rect: image.Rectangle{Max: b.lastAt}}
		draw.ApproxBiLinear.Scale(b.img, b.img.Rect, src, src.Rect, draw.Src, nil)
	}
	// Inside the border the picture; on it white; outside, nothing. The window takes premultiplied pixels.
	for i, j := 0, 0; i < len(pix); i, j = i+4, j+1 {
		cover, in := uint32(b.mask[j]), uint32(b.inner[j])
		for k := 0; k < 3; k++ {
			c := (uint32(pix[i+k])*in + 255*(255-in) + 127) / 255
			pix[i+k] = uint8((c*cover + 127) / 255)
		}
		pix[i+3] = uint8(cover)
	}
	b.o.Present(b.img, image.Rectangle{})
	b.drawnAt = time.Now()
}

// shapeMasks are, for each pixel of the window, how much of it is inside the rounded rectangle, and how much is
// inside the border within it, from 0 to 255, smoothed over the pixel's edge.
func shapeMasks(look cameraLook) (cover, inner []uint8) {
	w, h := look.size.X, look.size.Y
	cover, inner = make([]uint8, w*h), make([]uint8, w*h)
	hw, hh := float64(w)/2, float64(h)/2
	radius := min(look.radius, hw, hh)
	innerRadius := max(radius-look.border, 0)
	for y := range h {
		for x := range w {
			px, py := math.Abs(float64(x)+0.5-hw), math.Abs(float64(y)+0.5-hh)
			cover[y*w+x] = coverage(roundedDistance(px, py, hw, hh, radius))
			if look.border <= 0 {
				inner[y*w+x] = 255
			} else {
				inner[y*w+x] = coverage(roundedDistance(px, py, hw-look.border, hh-look.border, innerRadius))
			}
		}
	}
	return cover, inner
}

// roundedDistance is how far a point, taken from the centre, lies outside a rounded rectangle of half sizes hw, hh
// and corner radius r: negative inside.
func roundedDistance(px, py, hw, hh, r float64) float64 {
	qx, qy := px-(hw-r), py-(hh-r)
	outside := math.Hypot(max(qx, 0), max(qy, 0))
	return outside + min(max(qx, qy), 0) - r
}

func coverage(d float64) uint8 { return uint8(min(max(0.5-d, 0), 1)*255 + 0.5) }

// cameraTick looks after the camera window every frame: dragging it to move it, its bottom right corner or the
// wheel to resize it, a double click to open its settings, and a camera that stopped by itself.
func (a *App) cameraTick() {
	b := a.cam.bubble
	if b == nil {
		return
	}
	b.mu.Lock()
	feed, closedByUser := b.feed, b.o.Closed()
	b.mu.Unlock()
	if closedByUser {
		a.showCamera(false, nil)
		return
	}
	if feed != nil {
		select {
		case <-feed.Done():
			a.toast(a.u.tf("The camera stopped: %s", a.describe(feed.Err())), true)
			a.showCamera(false, nil)
			return
		default:
		}
	}
	in := b.o.Input()
	bounds := b.o.Bounds()
	at := image.Pt(bounds.Min.X+int(in.X), bounds.Min.Y+int(in.Y))
	corner := float32(min(bounds.Dx(), bounds.Dy())) * 0.22
	inCorner := in.X > float32(bounds.Dx())-corner && in.Y > float32(bounds.Dy())-corner
	for _, p := range in.Pointer {
		pos := image.Pt(bounds.Min.X+int(p.X), bounds.Min.Y+int(p.Y))
		switch {
		case p.Down && b.drag == 0:
			if p.Double {
				a.cam.open = true
				if host := a.recorder(); host != nil {
					host.Restore()
				}
				continue
			}
			b.drag, b.from, b.fromBound = dragMove, pos, bounds
			if !p.Right && p.X > float32(bounds.Dx())-corner && p.Y > float32(bounds.Dy())-corner {
				b.drag = dragResize
			}
		case !p.Down && b.drag != 0:
			a.cameraDragged(b, pos)
			if b.drag == dragResize {
				b.restartAt = time.Now()
			}
			b.drag = 0
			a.keepCameraPlace()
		}
	}
	if b.drag != 0 && in.Down {
		a.cameraDragged(b, at)
	}
	if in.Wheel != 0 && b.drag == 0 {
		s := &a.settings.Camera
		s.Width = min(max(int(float32(s.Width)*(1+0.1*in.Wheel)), cameraMinWidth), cameraMaxWidth)
		a.applyCameraLook(true)
		b.restartAt = time.Now().Add(400 * time.Millisecond)
	}
	if !b.restartAt.IsZero() && time.Now().After(b.restartAt) && b.drag == 0 {
		b.restartAt = time.Time{}
		a.restartCamera()
		a.saveSettings()
	}
	switch {
	case b.drag == dragResize || b.drag == 0 && inCorner:
		b.o.SetCursor(CursorResizeCorner)
	default:
		b.o.SetCursor(CursorMove)
	}
}

// cameraDragged follows a drag to where the pointer is now.
func (a *App) cameraDragged(b *cameraBubble, at image.Point) {
	d := at.Sub(b.from)
	switch b.drag {
	case dragMove:
		b.mu.Lock()
		b.o.Move(b.fromBound.Add(d))
		b.mu.Unlock()
	case dragResize:
		s := &a.settings.Camera
		grow := max(d.X, int(float64(d.Y)*float64(b.fromBound.Dx())/float64(b.fromBound.Dy())))
		s.Width = min(max(int(float32(b.fromBound.Dx()+grow)/a.cameraScale()), cameraMinWidth), cameraMaxWidth)
		a.applyCameraLook(false)
	}
}

// applyCameraLook redraws the window after its size, corners or border changed, keeping its top left in place, or
// its centre when centred; the camera catches up with a new size when restartCamera is called.
func (a *App) applyCameraLook(centred bool) {
	b := a.cam.bubble
	if b == nil {
		return
	}
	look := a.cameraLook()
	b.mu.Lock()
	defer b.mu.Unlock()
	r := b.o.Bounds()
	nr := image.Rectangle{Min: r.Min, Max: r.Min.Add(look.size)}
	if centred {
		c := r.Min.Add(r.Size().Div(2))
		nr = image.Rectangle{Min: c.Sub(look.size.Div(2)), Max: c.Sub(look.size.Div(2)).Add(look.size)}
	}
	b.look = look
	b.o.Move(nr)
	b.drawLocked()
}

// keepCameraPlace remembers where the window is.
func (a *App) keepCameraPlace() {
	b := a.cam.bubble
	if b == nil {
		return
	}
	r := b.o.Bounds()
	s := &a.settings.Camera
	s.Placed, s.X, s.Y = true, r.Min.X, r.Min.Y
	a.saveSettings()
}

// changeCamera applies a change of the settings to the window: another camera, shape or mirroring opens the
// camera again at once, a new size once it has settled, and corners and border only redraw.
func (a *App) changeCamera(before cameraSettings) {
	a.saveSettings()
	s := a.settings.Camera
	b := a.cam.bubble
	if b == nil {
		return
	}
	a.applyCameraLook(false)
	switch {
	case s.Camera != before.Camera || s.Shape != before.Shape || s.Mirror != before.Mirror:
		b.restartAt = time.Time{}
		a.restartCamera()
	case s.Width != before.Width:
		b.restartAt = time.Now().Add(400 * time.Millisecond)
	}
}

// drawCameraDialog is the camera's settings.
func (a *App) drawCameraDialog() {
	c := &a.cam
	if !c.open {
		return
	}
	u := &a.u
	u.c.Fill(a.screen, gfxShade)
	pad, header, footer := u.px(24), u.px(44), u.px(56)
	content := c.content
	if content == 0 {
		content = u.px(420)
	}
	w := min(u.px(600), a.screen.W-u.px(40))
	h := min(pad+header+content+footer+pad+1, a.screen.H-u.px(40))
	box := rectCentered(a.screen, w, h)
	panel(u, box)
	in := box.Inset(pad)
	u.label(u.bold(17), rectAt(in.X, in.Y, in.W-u.px(40), u.px(26)), u.t("Camera on the screen"), colText, 0)
	if u.iconButton("camera/close", rectAt(in.Right()-u.px(30), in.Y-u.px(2), u.px(30), u.px(30)), icClose, u.t("Close"), false, true) {
		c.open = false
	}
	body := rectAt(in.X, in.Y+header, in.W+u.px(8), in.H-header-footer)
	a.scrolledBy("camera/scroll", body, &c.scroll, &c.content, a.cameraBody)
	foot := in.Bottom() - u.px(38)
	if u.button("camera/done", rectAt(in.Right()-u.px(316), foot, u.px(110), u.px(38)), "", u.t("Close"), ghost, true) {
		c.open = false
	}
	label, glyph := u.t("Show the camera"), icVideo
	if c.bubble != nil {
		label, glyph = u.t("Hide the camera"), icClose
	}
	_, found := a.chosenCamera()
	if u.button("camera/toggle", rectAt(in.Right()-u.px(198), foot, u.px(198), u.px(38)), glyph, label, primary, c.bubble != nil || found) {
		a.showCamera(c.bubble == nil, func(err error) {
			if err != nil {
				a.toast(a.u.tf("The camera could not be shown: %s", a.describe(err)), true)
			}
		})
	}
	for _, k := range u.keys {
		if k.Code == keyEscape && u.focus == "" {
			c.open = false
		}
	}
	u.keys = nil
}

// cameraBody draws the camera's choices and returns the bottom of them.
func (a *App) cameraBody(in gfx.Rect) float32 {
	c := &a.cam
	u := &a.u
	s := &a.settings.Camera
	before := *s
	y := in.Y
	y = a.paragraph(in.X, y, in.W-u.px(8), u.t("The camera shows in a window of its own above everything, so recordings and live streams of the screen have it. Drag it to move it, drag its bottom right corner or turn the wheel over it to resize it, and double-click it for these settings."), 12.5, colDim) + u.px(12)
	u.heading(rectAt(in.X, y, in.W, u.px(20)), u.t("Camera"))
	y += u.px(26)
	switch {
	case c.listing:
		u.label(u.face(12.5), rectAt(in.X, y, in.W, u.px(30)), u.t("Looking for cameras…"), colDim, 0)
		y += u.px(30)
	case c.listErr != nil:
		y = a.paragraph(in.X, y, in.W-u.px(8), a.describe(c.listErr), 12.5, colDangerText)
	case len(c.cams) == 0:
		y = a.paragraph(in.X, y, in.W-u.px(8), u.t("No camera was found"), 12.5, colWarning)
	default:
		names := make([]string, len(c.cams))
		current := 0
		chosen, _ := a.chosenCamera()
		for i, cam := range c.cams {
			names[i] = cam.Name
			if cam.ID == chosen.ID {
				current = i
			}
		}
		pick, used := u.flowChips("camera/device", rectAt(in.X, y, in.W-u.px(8), u.px(30)), names, current)
		if pick != current {
			s.Camera = c.cams[pick].ID
		}
		y += used
	}
	y += u.px(14)
	u.heading(rectAt(in.X, y, in.W, u.px(20)), u.tc("camera", "Shape"))
	y += u.px(26)
	shape := min(max(s.Shape, 0), len(cameraShapes)-1)
	if pick, used := u.flowChips("camera/shape", rectAt(in.X, y, in.W-u.px(8), u.px(30)), u.ts(cameraShapeNames), shape); pick != shape {
		s.Shape = pick
		y += used
	} else {
		y += used
	}
	y += u.px(10)
	for _, sl := range []struct {
		id, name string
		value    *int
		lo, hi   int
		unit     string
	}{
		{"camera/radius", u.t("Corners"), &s.Radius, 0, 50, "%"},
		{"camera/width", u.tc("camera", "Size"), &s.Width, cameraMinWidth, cameraMaxWidth, " px"},
		{"camera/border", u.t("Border"), &s.Border, 0, cameraMaxBorder, " px"},
	} {
		u.label(u.face(12.5), rectAt(in.X, y, u.px(90), u.px(28)), sl.name, colText, 0)
		v := float64(*sl.value)
		if changed, _ := u.slider(sl.id, rectAt(in.X+u.px(96), y+u.px(4), in.W-u.px(96)-u.px(80), u.px(20)), &v, float64(sl.lo), float64(sl.hi), true); changed {
			*sl.value = int(math.Round(v))
		}
		u.label(u.face(12), rectAt(in.Right()-u.px(70), y, u.px(60), u.px(28)), strconv.Itoa(*sl.value)+sl.unit, colDim, 1)
		y += u.px(34)
	}
	if s.Radius == 50 && shape == 0 {
		u.label(u.face(11.5), rectAt(in.X+u.px(96), y-u.px(6), in.W, u.px(18)), u.t("A square with the largest corners is a circle"), colFaint, 0)
		y += u.px(16)
	}
	y += u.px(6)
	if on := u.checkbox("camera/mirror", rectAt(in.X, y, in.W, u.px(26)), u.t("Mirror, as people see themselves"), s.Mirror); on != s.Mirror {
		s.Mirror = on
	}
	y += u.px(30)
	if *s != before {
		a.changeCamera(before)
	}
	return y
}

// cameraSettling reports whether the camera waits for its size to settle before it is opened again, which needs
// the window to look in a moment without input.
func (a *App) cameraSettling() bool { return a.cam.bubble != nil && !a.cam.bubble.restartAt.IsZero() }
