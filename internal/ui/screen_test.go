package ui

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/btelolabs/btelo-forge/internal/media"
	"github.com/btelolabs/btelo-forge/internal/paint"
)

// overlayHarness is a recording harness whose window can open overlays.
func overlayHarness(t *testing.T) *harness {
	t.Helper()
	x := recordingHarness(t)
	x.h.overlaysOn = true
	return x
}

// shotOverlay saves what an overlay shows, for looking at.
func (x *harness) shotOverlay(o *fakeOverlay, name string) {
	dir := os.Getenv("FORGE_SHOTS")
	if dir == "" || o.shown == nil {
		return
	}
	f, err := os.Create(filepath.Join(dir, name+".png"))
	if err != nil {
		x.t.Fatal(err)
	}
	defer f.Close()
	png.Encode(f, o.shown)
}

func TestPickingARegion(t *testing.T) {
	x := overlayHarness(t)
	a, h := x.a, x.h
	d, _ := a.chosenDisplay()
	var got []image.Rectangle
	var oks []bool
	pick := func() *fakeOverlay {
		a.pickRegion(d, func(r image.Rectangle, ok bool) { got, oks = append(got, r), append(oks, ok) })
		open := h.open()
		o := open[len(open)-1]
		if !o.opts.Activate || !o.opts.Excluded || o.bounds != displayBounds(d) {
			t.Fatalf("picker %+v", o.opts)
		}
		return o
	}
	o := pick()
	x.frame(Input{})
	x.shotOverlay(o, "overlay-pick")
	o.queued = append(o.queued, Input{X: 100, Y: 80})
	o.drag(100, 80, 500, 380)
	// The picture shows the region clear and the rest dimmed, while dragging: one queued input a frame, so after the
	// hover, the press and the six moves the drag has reached its end but not let go.
	for i := 0; i < 9; i++ {
		x.frame(Input{})
	}
	if c := o.shown.RGBAAt(300, 200); c.A > 1 {
		t.Errorf("inside the region the picker shows %v", c)
	}
	if c := o.shown.RGBAAt(50, 50); c.A < 80 {
		t.Errorf("outside the region the picker shows %v", c)
	}
	x.shotOverlay(o, "overlay-pick-drag")
	x.pump(5*time.Second, func() bool { return len(got) == 1 })
	if !oks[0] || got[0] != image.Rect(100, 80, 500, 380) || !o.closed {
		t.Fatalf("drag picked %v %v, closed %v", got[0], oks[0], o.closed)
	}
	// A click picks the whole display; Esc cancels.
	o = pick()
	o.click(200, 200)
	x.pump(5*time.Second, func() bool { return len(got) == 2 })
	if !oks[1] || !got[1].Empty() {
		t.Errorf("click picked %v %v", got[1], oks[1])
	}
	o = pick()
	o.queued = append(o.queued, Input{Keys: []Key{{Code: keyEscape}}})
	x.pump(5*time.Second, func() bool { return len(got) == 3 })
	if oks[2] {
		t.Error("Esc did not cancel")
	}
}

func TestScreenshotOfARegion(t *testing.T) {
	x := overlayHarness(t)
	a, h := x.a, x.h
	a.Tick(Input{Hotkeys: []int{hotkeyScreenshot}})
	open := h.open()
	if len(open) == 0 {
		t.Fatal("the screenshot hotkey did not open the picker")
	}
	o := open[len(open)-1]
	o.drag(40, 30, 40+321, 30+201)
	x.pump(30*time.Second, func() bool { return o.closed && !a.rec.shooting })
	x.pump(30*time.Second, func() bool { return !a.rec.shooting })
	shots, _ := filepath.Glob(filepath.Join(a.settings.ExportDir, "Screenshot *.png"))
	if len(shots) != 1 {
		t.Fatalf("screenshots %v; toasts %+v", shots, a.toasts)
	}
	f, _ := os.Open(shots[0])
	defer f.Close()
	cfg, err := png.DecodeConfig(f)
	if err != nil || cfg.Width != 320 || cfg.Height != 200 {
		t.Errorf("screenshot %dx%d (%v), want the region rounded to 320x200", cfg.Width, cfg.Height, err)
	}
}

func TestDrawingOnTheScreen(t *testing.T) {
	x := overlayHarness(t)
	a, h := x.a, x.h
	a.brush = paint.Brush{Size: 16, Hardness: 1, Opacity: 1, Spacing: 0.1, Color: color.NRGBA{R: 255, A: 255}}
	a.Tick(Input{Hotkeys: []int{hotkeyDraw}})
	sd := a.over.draw
	if sd == nil || !sd.drawing {
		t.Fatal("the draw hotkey did not start drawing")
	}
	x.frame(Input{})
	var canvas, bar *fakeOverlay
	for _, o := range h.open() {
		if o.opts.Excluded {
			bar = o
		} else {
			canvas = o
		}
	}
	if canvas == nil || bar == nil || canvas.clickThrough {
		t.Fatalf("drawing window %+v, toolbar %+v", canvas, bar)
	}
	// The toolbar sits at the top right of the display.
	d := sd.d
	if bar.bounds.Max.X > d.X+d.Width || bar.bounds.Max.X < d.X+d.Width-100 || bar.bounds.Min.Y > d.Y+60 {
		t.Errorf("toolbar at %v on a display at %d,%d %dx%d", bar.bounds, d.X, d.Y, d.Width, d.Height)
	}
	canvas.drag(100, 100, 400, 100)
	for i := 0; i < 12; i++ {
		x.frame(Input{})
	}
	if c := canvas.shown.RGBAAt(250, 100); c != (color.RGBA{255, 0, 0, 255}) {
		t.Errorf("the stroke shows as %v", c)
	}
	// Untouched pixels are invisible but still catch the mouse while drawing.
	if c := canvas.shown.RGBAAt(250, 300); c.A != 1 || c.R != 0 {
		t.Errorf("an untouched pixel is %v", c)
	}
	x.shotOverlay(bar, "overlay-toolbar-draw")
	// The right button gives the mouse back; the drawing stays, without the floor.
	canvas.queued = append(canvas.queued, Input{X: 10, Y: 10, Pointer: []Pointer{{X: 10, Y: 10, Right: true, Down: true}}})
	for i := 0; i < 3; i++ {
		x.frame(Input{})
	}
	if sd.drawing || !canvas.clickThrough {
		t.Fatalf("after the right button: drawing %v, click-through %v", sd.drawing, canvas.clickThrough)
	}
	if c := canvas.shown.RGBAAt(250, 300); c.A != 0 {
		t.Errorf("an untouched pixel keeps %v while not drawing", c)
	}
	if c := canvas.shown.RGBAAt(250, 100); c.A != 255 {
		t.Error("the drawing went away")
	}
	// Clearing wipes it and can be undone; closing removes the drawing and the toolbar.
	a.clearScreenDraw()
	x.frame(Input{})
	if c := canvas.shown.RGBAAt(250, 100); c.A != 0 {
		t.Errorf("after clearing %v", c)
	}
	sd.doc.Undo()
	sd.s.shown = false
	x.frame(Input{})
	if c := canvas.shown.RGBAAt(250, 100); c.A != 255 {
		t.Error("undo did not bring the drawing back")
	}
	a.closeScreenDraw()
	x.frame(Input{})
	if !canvas.closed || !bar.closed || a.over.bar != nil {
		t.Errorf("closed: drawing %v, toolbar %v", canvas.closed, bar.closed)
	}
}

func TestRecordingARegionWithTheToolbar(t *testing.T) {
	x := overlayHarness(t)
	a, h := x.a, x.h
	a.settings.Record.Region, a.settings.Record.Minimize, a.settings.Record.SystemAudio = true, false, false
	started := make(chan error, 1)
	a.startRecording(func(_ string, err error) { started <- err })
	open := h.open()
	if len(open) != 1 || !open[0].opts.Activate {
		t.Fatalf("recording a region did not open the picker: %+v", open)
	}
	open[0].drag(200, 100, 200+641, 100+361)
	x.pump(time.Minute, func() bool { return len(started) > 0 })
	if err := <-started; err != nil {
		t.Fatal(err)
	}
	if got := a.rec.rec.Options().Region; got != image.Rect(200, 100, 841, 461) {
		t.Errorf("recording region %v", got)
	}
	x.frame(Input{})
	var frame, bar *fakeOverlay
	for _, o := range h.open() {
		switch {
		case o.opts.ClickThrough:
			frame = o
		case o.opts.Excluded:
			bar = o
		}
	}
	if frame == nil || !frame.opts.Excluded || bar == nil {
		t.Fatalf("frame %+v, toolbar %+v", frame, bar)
	}
	if c := frame.shown.RGBAAt(199, 300); c.A != 255 {
		t.Errorf("the region's outline is %v", c)
	}
	time.Sleep(1500 * time.Millisecond)
	x.frame(Input{})
	x.shotOverlay(bar, "overlay-toolbar-recording")
	saved := make(chan string, 1)
	a.stopRecording(func(path string, err error) {
		if err != nil {
			t.Error(err)
		}
		saved <- path
	})
	x.pump(time.Minute, func() bool { return len(saved) > 0 && a.opening == "" })
	path := <-saved
	x.frame(Input{})
	if !frame.closed || !bar.closed {
		t.Errorf("after stopping: outline closed %v, toolbar closed %v", frame.closed, bar.closed)
	}
	cmd, _ := media.Command(t.Context(), "ffprobe", "-v", "error", "-select_streams", "v:0", "-show_entries", "stream=width,height", "-of", "csv=p=0", path)
	size, _ := cmd.Output()
	if strings.TrimSpace(string(size)) != "640,360" {
		t.Errorf("recorded %q, want 640,360", strings.TrimSpace(string(size)))
	}
}
