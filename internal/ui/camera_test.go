package ui

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/btelolabs/btelo-forge/internal/capture"
)

func TestCameraShapes(t *testing.T) {
	// A circle with a border: opaque in the middle, nothing in the corners, smooth at the edge, white on the border.
	cover, inner := shapeMasks(cameraLook{size: image.Pt(100, 100), radius: 50, border: 4})
	at := func(m []uint8, x, y int) uint8 { return m[y*100+x] }
	if at(cover, 50, 50) != 255 || at(inner, 50, 50) != 255 {
		t.Error("the middle is not the picture")
	}
	if at(cover, 0, 0) != 0 || at(cover, 99, 99) != 0 || at(cover, 3, 3) != 0 {
		t.Error("the corners of a circle are shown")
	}
	if edge := at(cover, 50, 0); edge == 0 || edge == 255 {
		// The top middle pixel is crossed by the edge.
		t.Errorf("the edge is not smoothed: %d", edge)
	}
	if at(cover, 50, 2) != 255 || at(inner, 50, 2) != 0 {
		t.Error("the border is not white")
	}
	// Square corners and no border: everything is the picture.
	cover, inner = shapeMasks(cameraLook{size: image.Pt(64, 36)})
	for i := range cover {
		if cover[i] != 255 || inner[i] != 255 {
			t.Fatal("a plain rectangle is not shown whole")
		}
	}
	for _, c := range []struct {
		s    cameraSettings
		want image.Point
	}{
		{cameraSettings{Width: 280, Shape: 0}, image.Pt(420, 420)},
		{cameraSettings{Width: 320, Shape: 2}, image.Pt(480, 270)},
		{cameraSettings{Width: 5000, Shape: 4}, image.Pt(1440, 2560)},
		{cameraSettings{Width: 10, Shape: 9}, image.Pt(180, 320)},
	} {
		if got := c.s.size(1.5); got != c.want {
			t.Errorf("%+v at 150%%: %v, want %v", c.s, got, c.want)
		}
	}
}

// bubbleShown is what the camera window shows, read under its lock since the camera's goroutine draws it.
func (x *harness) bubbleShown() (*fakeOverlay, *image.RGBA, int) {
	b := x.a.cam.bubble
	if b == nil {
		return nil, nil, 0
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	o := b.o.(*fakeOverlay)
	if o.shown == nil {
		return o, nil, o.presents
	}
	img := image.NewRGBA(o.shown.Rect)
	copy(img.Pix, o.shown.Pix)
	return o, img, o.presents
}

// dragBubble drags the camera window from a point in it by d, giving the pointer in the window's own pixels at each
// step, as Windows does while the window follows it.
func (x *harness) dragBubble(from image.Point, d image.Point) {
	o, _, _ := x.bubbleShown()
	start := o.Bounds().Min.Add(from)
	feed := func(in Input, desk image.Point) {
		local := desk.Sub(o.Bounds().Min)
		in.X, in.Y = float32(local.X), float32(local.Y)
		for i := range in.Pointer {
			in.Pointer[i].X, in.Pointer[i].Y = in.X, in.Y
		}
		o.queued = append(o.queued, in)
		x.frame(Input{})
	}
	feed(Input{Down: true, Pointer: []Pointer{{Down: true}}}, start)
	for i := 1; i <= 5; i++ {
		feed(Input{Down: true}, start.Add(d.Mul(i).Div(5)))
	}
	feed(Input{Pointer: []Pointer{{}}}, start.Add(d))
}

func TestTheCameraOnTheScreen(t *testing.T) {
	if !capture.Supported() {
		t.Skip(capture.ErrUnsupported)
	}
	x := newHarness(t)
	x.h.overlaysOn = true
	a := x.a
	done := make(chan error, 1)
	a.showCamera(true, func(err error) { done <- err })
	x.pump(time.Minute, func() bool { return len(done) > 0 })
	if err := <-done; err != nil {
		t.Skip(err) // no camera, or one in use
	}
	if !a.settings.Camera.On {
		t.Error("the camera being shown is not remembered")
	}
	waitFrames := func(what string, size image.Point) *image.RGBA {
		t.Helper()
		var img *image.RGBA
		x.pump(20*time.Second, func() bool {
			_, img, _ = x.bubbleShown()
			b := a.cam.bubble
			b.mu.Lock()
			fresh := b.lastAt == size
			b.mu.Unlock()
			return img != nil && img.Rect.Size() == size && fresh
		})
		_, img, _ = x.bubbleShown()
		if img == nil || img.Rect.Size() != size {
			t.Fatalf("%s: the window shows %v, want %v", what, img.Rect, size)
		}
		return img
	}
	size := a.settings.Camera.size(1)
	img := waitFrames("opening", size)
	x.shotImage("camera-circle", img)
	// A circle: the corners clear, the middle the camera, opaque.
	if img.RGBAAt(0, 0).A != 0 || img.RGBAAt(size.X/2, size.Y/2).A != 255 {
		t.Errorf("corner %v, middle %v", img.RGBAAt(0, 0), img.RGBAAt(size.X/2, size.Y/2))
	}
	o, _, _ := x.bubbleShown()
	if o.opts.Excluded || o.opts.ClickThrough {
		t.Errorf("the camera window is %+v; it must be recorded and take the mouse", o.opts)
	}

	// Dragged, it moves and remembers where.
	before := o.Bounds()
	x.dragBubble(image.Pt(size.X/2, size.Y/2), image.Pt(-120, -80))
	if got := o.Bounds(); got != before.Add(image.Pt(-120, -80)) {
		t.Fatalf("moved to %v from %v", got, before)
	}
	if s := a.settings.Camera; !s.Placed || s.X != before.Min.X-120 || s.Y != before.Min.Y-80 {
		t.Errorf("the place is not remembered: %+v", s)
	}

	// Its corner dragged, it grows, and the camera is opened again at the new size once the drag ends.
	x.dragBubble(image.Pt(size.X-4, size.Y-4), image.Pt(100, 100))
	if a.settings.Camera.Width <= 280 {
		t.Fatalf("width %d after growing", a.settings.Camera.Width)
	}
	grown := a.settings.Camera.size(1)
	if o.Bounds().Size() != grown {
		t.Errorf("the window is %v, want %v", o.Bounds().Size(), grown)
	}
	waitFrames("resizing", grown)
	x.shot("camera-dialog")

	// Through MCP: a rounded 16:9 picture with a thin border.
	s := connectMCPAt(t, x, mcpDomainPaths[domainRecording])
	var view struct {
		Shown  bool  `json:"shown"`
		Pixels []int `json:"pixels"`
	}
	s.ok("set_camera", map[string]any{"shape": "16:9", "corners_percent": 12, "width": 320, "border": 2}, &view)
	if !view.Shown || view.Pixels[0] != 320 || view.Pixels[1] != 180 {
		t.Fatalf("after set_camera: %+v", view)
	}
	img = waitFrames("16:9", image.Pt(320, 180))
	x.shotImage("camera-rounded", img)
	if img.RGBAAt(0, 0).A != 0 || img.RGBAAt(30, 0).A == 0 || img.RGBAAt(160, 90).A != 255 {
		t.Errorf("the rounded rectangle: corner %v, top %v, middle %v", img.RGBAAt(0, 0), img.RGBAAt(30, 0), img.RGBAAt(160, 90))
	}
	a.cam.open = true
	x.frame(Input{})
	x.frame(Input{})
	x.shot("camera-dialog-open")
	a.cam.open = false

	s.ok("set_camera", map[string]any{"shown": false}, &view)
	if view.Shown || a.cam.bubble != nil || !o.closed || a.settings.Camera.On {
		t.Errorf("the camera was not taken off the screen: %+v", view)
	}
}

// shotImage saves a picture as a screen shot is saved, over a checkerboard so that its transparent parts show.
func (x *harness) shotImage(name string, img *image.RGBA) {
	dir := os.Getenv("FORGE_SHOTS")
	if dir == "" {
		return
	}
	out := image.NewRGBA(img.Rect)
	for y := range img.Rect.Dy() {
		for xx := range img.Rect.Dx() {
			bg := uint32(200)
			if (xx/12+y/12)%2 == 0 {
				bg = 140
			}
			c := img.RGBAAt(xx, y) // premultiplied
			blend := func(v uint8) uint8 { return uint8(uint32(v) + bg*(255-uint32(c.A))/255) }
			out.SetRGBA(xx, y, color.RGBA{blend(c.R), blend(c.G), blend(c.B), 255})
		}
	}
	f, err := os.Create(filepath.Join(dir, name+".png"))
	if err != nil {
		x.t.Fatal(err)
	}
	defer f.Close()
	png.Encode(f, out)
}
