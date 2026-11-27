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

// syntheticPickerHarness builds a headless harness with a synthetic display and overlays enabled, so picker UX tests
// run everywhere without needing live screen-recording permission.
func syntheticPickerHarness(t *testing.T) (*harness, capture.Display) {
	t.Helper()
	x := newHarness(t)
	x.h.overlaysOn = true
	d := capture.Display{Device: "TEST-1", Name: "Synthetic", X: 0, Y: 0, Width: 1280, Height: 720, Primary: true}
	x.a.rec.displays = []capture.Display{d}
	x.a.rec.listed = true
	x.a.rec.listErr = nil
	x.a.settings.ResourceDir = x.mediaDir()
	return x, d
}

// solidSnap makes an opaque WxH RGBA image filled with bg, with an optional spot color at spot.
func solidSnap(w, h int, bg color.RGBA, spot image.Rectangle, fg color.RGBA) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		row := img.Pix[y*img.Stride : y*img.Stride+w*4]
		for x := 0; x < w*4; x += 4 {
			row[x], row[x+1], row[x+2], row[x+3] = bg.R, bg.G, bg.B, 255
		}
	}
	spot = spot.Intersect(img.Rect)
	for y := spot.Min.Y; y < spot.Max.Y; y++ {
		for x := spot.Min.X; x < spot.Max.X; x++ {
			img.SetRGBA(x, y, color.RGBA{fg.R, fg.G, fg.B, 255})
		}
	}
	return img
}

func TestFrozenPickerDirtyRectsLoupeAndColorCopy(t *testing.T) {
	x, d := syntheticPickerHarness(t)
	a, h := x.a, x.h
	bg := color.RGBA{34, 102, 204, 255}
	spot := color.RGBA{0xFF, 0x33, 0x66, 255}
	snap := solidSnap(d.Width, d.Height, bg, image.Rect(190, 140, 210, 160), spot)
	snaps := map[string]*image.RGBA{d.Key(): snap}
	dimmed := dimSnapshots(snaps)

	var doneCalled bool
	var doneOK bool
	a.pickRegionWith(a.shotPick(shotClipboard), pickOptions{snaps: snaps, dimmed: dimmed}, func(_ capture.Display, _ image.Rectangle, ok bool) {
		doneCalled = true
		doneOK = ok
	})
	open := h.open()
	if len(open) != 1 {
		t.Fatalf("got %d overlays, want 1", len(open))
	}
	o := open[0]

	// First frame uploads the full frozen backdrop.
	x.frame(Input{})
	if o.lastDirty != image.Rect(0, 0, d.Width, d.Height) {
		t.Fatalf("first present dirty = %v, want full display", o.lastDirty)
	}
	// Dimmed pixel outside active area should be darkened and opaque.
	dimPx := o.shown.RGBAAt(10, 10)
	if dimPx.A != 255 || dimPx.B >= bg.B {
		t.Fatalf("expected opaque darkened background pixel, got %v (orig %v)", dimPx, bg)
	}

	// Hover move updates incrementally with a small dirty rect (far smaller than full screen).
	o.queued = append(o.queued, Input{X: 400, Y: 400})
	x.frame(Input{})
	o.queued = append(o.queued, Input{X: 420, Y: 410})
	x.frame(Input{})
	fullArea := d.Width * d.Height
	if dirtyArea := o.lastDirty.Dx() * o.lastDirty.Dy(); dirtyArea == 0 || dirtyArea > fullArea/4 {
		t.Errorf("incremental hover dirty rect %v (%d px) should be < 25%% of display (%d px)", o.lastDirty, dirtyArea, fullArea)
	}

	// Moving into the bottom-right corner must edge-flip the loupe cleanly within bounds.
	o.queued = append(o.queued, Input{X: float32(d.Width - 10), Y: float32(d.Height - 10)})
	x.frame(Input{})
	if !o.lastDirty.In(image.Rect(0, 0, d.Width, d.Height)) {
		t.Errorf("edge-flipped loupe dirty rect %v escaped display bounds", o.lastDirty)
	}

	// Pressing C over the spot copies #FF3366 and cancels the screenshot cleanly.
	o.queued = append(o.queued, Input{X: 200, Y: 150, Keys: []Key{{Code: 'C'}}})
	x.frame(Input{})
	if !doneCalled || doneOK || !o.closed {
		t.Fatalf("C copy: doneCalled=%v doneOK=%v closed=%v", doneCalled, doneOK, o.closed)
	}
	if h.clip != "#FF3366" {
		t.Errorf("clipboard = %q, want #FF3366", h.clip)
	}
}

func TestPickerHoverWindowSnapAndTabToggle(t *testing.T) {
	x, d := syntheticPickerHarness(t)
	a, h := x.a, x.h
	bg := color.RGBA{40, 80, 160, 255}
	snap := solidSnap(d.Width, d.Height, bg, image.Rectangle{}, color.RGBA{})
	snaps := map[string]*image.RGBA{d.Key(): snap}
	dimmed := dimSnapshots(snaps)
	popup := image.Rect(300, 200, 480, 320)
	backWin := image.Rect(100, 100, 900, 600)
	windows := []capture.WindowRect{
		{Rect: popup, Title: "Dropdown Menu"},
		{Rect: backWin, Title: "Editor"},
	}

	type picked struct {
		r  image.Rectangle
		ok bool
	}
	var got []picked
	openPicker := func() *fakeOverlay {
		a.pickRegionWith(a.shotPick(shotClipboard), pickOptions{snaps: snaps, dimmed: dimmed, windows: windows}, func(_ capture.Display, r image.Rectangle, ok bool) {
			got = append(got, picked{r, ok})
		})
		return h.open()[0]
	}

	// Hover inside popup highlights it un-dimmed, and single click captures the exact popup rect.
	o := openPicker()
	o.queued = append(o.queued, Input{X: 350, Y: 250})
	x.frame(Input{})
	// Sample inside popup away from the cursor crosshair at (350, 250).
	if c := o.shown.RGBAAt(315, 215); c != bg {
		t.Errorf("snapped popup interior = %v, want un-dimmed %v", c, bg)
	}
	if c := o.shown.RGBAAt(50, 50); c.B >= bg.B {
		t.Errorf("outside snapped popup = %v, want dimmed", c)
	}
	o.click(350, 250)
	x.pump(5*time.Second, func() bool { return len(got) == 1 })
	if !got[0].ok || got[0].r != popup {
		t.Fatalf("click on hovered popup got %+v, want %v", got[0], popup)
	}

	// Tab disables hover snap so single click captures the whole display.
	o = openPicker()
	o.queued = append(o.queued, Input{X: 350, Y: 250})
	x.frame(Input{})
	o.queued = append(o.queued, Input{X: 350, Y: 250, Keys: []Key{{Code: keyTab}}})
	x.frame(Input{})
	o.click(350, 250)
	x.pump(5*time.Second, func() bool { return len(got) == 2 })
	if !got[1].ok || !got[1].r.Empty() {
		t.Fatalf("click after Tab toggle got %+v, want empty (whole display)", got[1])
	}

	// Dragging >= 8px overrides window snap with the custom rectangle.
	o = openPicker()
	o.drag(350, 250, 600, 450)
	x.pump(5*time.Second, func() bool { return len(got) == 3 })
	wantDrag := image.Rect(350, 250, 600, 450)
	if !got[2].ok || got[2].r != wantDrag {
		t.Fatalf("drag over hovered window got %+v, want %v", got[2], wantDrag)
	}
}

func TestPickerSpacePanAndAltAdjustHandlesAndArrows(t *testing.T) {
	x, d := syntheticPickerHarness(t)
	a, h := x.a, x.h
	snap := solidSnap(d.Width, d.Height, color.RGBA{50, 60, 70, 255}, image.Rectangle{}, color.RGBA{})
	snaps := map[string]*image.RGBA{d.Key(): snap}
	dimmed := dimSnapshots(snaps)

	var gotRect image.Rectangle
	var gotOK bool
	var done bool
	a.pickRegionWith(a.shotPick(shotFile), pickOptions{snaps: snaps, dimmed: dimmed}, func(_ capture.Display, r image.Rectangle, ok bool) {
		gotRect, gotOK, done = r, ok, true
	})
	o := h.open()[0]

	// Press at (100,100), drag to (300,250), then hold Space and move by (+50,+40) -> (150,140)-(350,290),
	// then release with Alt held to enter adjust mode.
	o.queued = append(o.queued,
		Input{X: 100, Y: 100},
		Input{X: 100, Y: 100, Down: true, Pointer: []Pointer{{X: 100, Y: 100, Down: true}}},
		Input{X: 300, Y: 250, Down: true},
		Input{X: 300, Y: 250, Down: true, Keys: []Key{{Code: keySpace}}},
		Input{X: 350, Y: 290, Down: true},
		Input{X: 350, Y: 290, Alt: true, Up: []uint16{keySpace}, Pointer: []Pointer{{X: 350, Y: 290}}},
	)
	for range 6 {
		x.frame(Input{})
	}
	if done || a.over.pick == nil || !a.over.pick.adjusting {
		t.Fatalf("Alt release should enter adjust mode: done=%v pick=%v", done, a.over.pick)
	}
	if want := image.Rect(150, 140, 350, 290); a.over.pick.adjRect != want {
		t.Fatalf("after Space-pan adjRect = %v, want %v", a.over.pick.adjRect, want)
	}

	// Arrow Right (+1) and Shift+Down (+10).
	o.queued = append(o.queued,
		Input{X: 350, Y: 290, Keys: []Key{{Code: keyRight}, {Code: keyDown, Shift: true}}},
	)
	x.frame(Input{})
	if want := image.Rect(151, 150, 351, 300); a.over.pick.adjRect != want {
		t.Fatalf("after arrow nudge adjRect = %v, want %v", a.over.pick.adjRect, want)
	}

	// Drag SE corner handle from (351,300) to (401,340).
	o.drag(351, 300, 401, 340)
	for range 9 {
		x.frame(Input{})
	}
	if want := image.Rect(151, 150, 401, 340); a.over.pick.adjRect != want {
		t.Fatalf("after SE handle drag adjRect = %v, want %v", a.over.pick.adjRect, want)
	}

	// Drag interior from (250,220) to (270,230) to move the box.
	o.drag(250, 220, 270, 230)
	for range 9 {
		x.frame(Input{})
	}
	if want := image.Rect(171, 160, 421, 350); a.over.pick.adjRect != want {
		t.Fatalf("after move drag adjRect = %v, want %v", a.over.pick.adjRect, want)
	}

	// Enter confirms the adjusted region.
	o.queued = append(o.queued, Input{X: 270, Y: 230, Keys: []Key{{Code: keyEnter}}})
	x.frame(Input{})
	if !done || !gotOK || gotRect != image.Rect(171, 160, 421, 350) {
		t.Fatalf("Enter confirm got done=%v ok=%v rect=%v", done, gotOK, gotRect)
	}
}

func TestFinishFrozenScreenshotAsyncClipboardAndFile(t *testing.T) {
	x, d := syntheticPickerHarness(t)
	a, h := x.a, x.h
	snap := solidSnap(d.Width, d.Height, color.RGBA{12, 34, 56, 255}, image.Rect(40, 30, 361, 231), color.RGBA{200, 100, 50, 255})

	// 1. Clipboard target: even-rounded 321x201 -> 320x200, copied asynchronously off UI thread.
	var clipShot shotTaken
	a.finishFrozenScreenshot(d, snap, image.Rect(40, 30, 40+321, 30+201), shotClipboard, func(shot shotTaken, err error) {
		if err != nil {
			t.Errorf("clipboard screenshot failed: %v", err)
		}
		clipShot = shot
	})
	x.pump(10*time.Second, func() bool { return !a.rec.shooting && clipShot.img != nil })
	if h.clipImage == nil || h.clipImage.Bounds().Dx() != 320 || h.clipImage.Bounds().Dy() != 200 {
		t.Fatalf("clipboard image = %v, want 320x200", h.clipImage)
	}
	if c := h.clipImage.At(10, 10); c != (color.RGBA{200, 100, 50, 255}) {
		t.Errorf("cropped pixel = %v, want {200 100 50 255}", c)
	}

	// 2. Path target: writes PNG asynchronously, copies path, opens shotNotice whose Copy Picture button also works async.
	h.clipImage = nil
	h.clip = ""
	a.finishFrozenScreenshot(d, snap, image.Rect(40, 30, 40+321, 30+201), shotPath, func(shot shotTaken, err error) {
		if err != nil {
			t.Errorf("path screenshot failed: %v", err)
			return
		}
		a.showShotNotice(d, shotPath, shot)
	})
	x.pump(10*time.Second, func() bool { return !a.rec.shooting && a.over.notice != nil })
	if h.clip == "" {
		t.Fatal("path target did not copy file path to clipboard")
	}
	f, err := os.Open(h.clip)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := png.DecodeConfig(f)
	f.Close()
	if err != nil || cfg.Width != 320 || cfg.Height != 200 {
		t.Fatalf("saved PNG = %dx%d (%v), want 320x200", cfg.Width, cfg.Height, err)
	}
	if filepath.Dir(h.clip) != filepath.Join(a.settings.ResourceDir, "Tools") {
		t.Errorf("saved in unexpected dir: %s", h.clip)
	}
	// Clicking notice's "Copy the picture" button copies asynchronously off the UI thread.
	x.frame(Input{})
	clickNoticeButton(t, x, a.over.notice)
	if h.clipImage == nil || h.clipImage.Bounds().Dx() != 320 {
		t.Errorf("notice copy-picture put %v on clipboard", h.clipImage)
	}
}

func TestRecordBarClampingAndMixedDPIScale(t *testing.T) {
	screen := image.Rect(0, 0, 1920, 1080)
	if got := clampBarRect(image.Rect(-100, -50, 200, 2), screen); got != image.Rect(0, 0, 300, 52) {
		t.Errorf("clamp top-left = %v", got)
	}
	if got := clampBarRect(image.Rect(1800, 1050, 2100, 1102), screen); got != image.Rect(1620, 1028, 1920, 1080) {
		t.Errorf("clamp bottom-right = %v", got)
	}

	x, d := syntheticPickerHarness(t)
	x.h.overlayScale = 2
	s, err := x.a.openScaledSurface(OverlayOptions{Excluded: true}, func(scale float32) image.Rectangle {
		w, h := int(200*scale), int(40*scale)
		return image.Rect(d.X, d.Y, d.X+w, d.Y+h)
	})
	if err != nil {
		t.Fatal(err)
	}
	defer s.close()
	if got := s.o.Bounds().Size(); got != image.Pt(400, 80) {
		t.Errorf("mixed-DPI overlay size = %v, want 400x80", got)
	}
	if got := s.c.Img.Rect.Size(); got != image.Pt(400, 80) {
		t.Errorf("mixed-DPI canvas size = %v, want 400x80", got)
	}
}
