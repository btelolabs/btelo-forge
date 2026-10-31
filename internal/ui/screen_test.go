package ui

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/btelolabs/btelo-forge/internal/capture"
	"github.com/btelolabs/btelo-forge/internal/gfx"
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

// hasColor reports whether any pixel of the top of an overlay's picture is exactly c.
func hasColor(img *image.RGBA, c color.RGBA, rows int) bool {
	b := img.Bounds()
	for y := b.Min.Y; y < min(b.Max.Y, b.Min.Y+rows); y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if img.RGBAAt(x, y) == c {
				return true
			}
		}
	}
	return false
}

func TestPickingARegion(t *testing.T) {
	x := overlayHarness(t)
	a, h := x.a, x.h
	d, _ := a.chosenDisplay()
	// Another screen right of every real one, whatever this computer has.
	right := 0
	for _, s := range a.rec.displays {
		right = max(right, s.X+s.Width)
	}
	other := capture.Display{Device: `\\.\TESTDISPLAY`, X: right, Y: 0, Width: 1280, Height: 720}
	a.rec.displays = append(a.rec.displays, other)
	type picked struct {
		d  capture.Display
		r  image.Rectangle
		ok bool
	}
	var got []picked
	// pick opens the picker and gives its windows by display: the chosen one is opened last, in front.
	pick := func() map[string]*fakeOverlay {
		a.pickRegion(a.shotPick(shotFile), func(d capture.Display, r image.Rectangle, ok bool) { got = append(got, picked{d, r, ok}) })
		open := h.open()
		if len(open) != len(a.rec.displays) || open[len(open)-1].bounds != displayBounds(d) {
			t.Fatalf("%d pickers for %d displays", len(open), len(a.rec.displays))
		}
		by := map[string]*fakeOverlay{}
		for _, o := range open {
			if !o.opts.Activate || !o.opts.Excluded {
				t.Fatalf("picker %+v", o.opts)
			}
			for _, s := range a.rec.displays {
				if displayBounds(s) == o.bounds {
					by[s.Key()] = o
				}
			}
		}
		return by
	}
	allClosed := func() {
		t.Helper()
		if open := h.open(); len(open) != 0 {
			t.Errorf("%d pickers still open", len(open))
		}
	}
	o := pick()[d.Key()]
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
	if g := got[0]; !g.ok || g.d.Key() != d.Key() || g.r != image.Rect(100, 80, 500, 380) {
		t.Fatalf("drag picked %v of %s, %v", g.r, g.d.Key(), g.ok)
	}
	allClosed()
	// Any screen can be picked from: a drag on another picks a region of it, a click all of it.
	o = pick()[other.Key()]
	o.drag(40, 50, 640, 450)
	x.pump(5*time.Second, func() bool { return len(got) == 2 })
	if g := got[1]; !g.ok || g.d.Key() != other.Key() || g.r != image.Rect(40, 50, 640, 450) {
		t.Errorf("drag on the other screen picked %v of %s, %v", g.r, g.d.Key(), g.ok)
	}
	allClosed()
	o = pick()[other.Key()]
	o.click(200, 200)
	x.pump(5*time.Second, func() bool { return len(got) == 3 })
	if g := got[2]; !g.ok || g.d.Key() != other.Key() || !g.r.Empty() {
		t.Errorf("click on the other screen picked %v of %s, %v", g.r, g.d.Key(), g.ok)
	}
	allClosed()
	// Esc on any of the windows cancels.
	o = pick()[other.Key()]
	o.queued = append(o.queued, Input{Keys: []Key{{Code: keyEscape}}})
	x.pump(5*time.Second, func() bool { return len(got) == 4 })
	if got[3].ok {
		t.Error("Esc did not cancel")
	}
	allClosed()
}

// A screenshot's picker and a recording's say which they are, each with its own symbol, colour and words.
func TestPickerSaysWhatItIsFor(t *testing.T) {
	x := overlayHarness(t)
	a, h := x.a, x.h
	shown := func(what pickPurpose) *fakeOverlay {
		a.pickRegion(what, func(capture.Display, image.Rectangle, bool) {})
		x.frame(Input{})
		open := h.open()
		o := open[len(open)-1]
		for _, p := range open {
			p.queued = append(p.queued, Input{Keys: []Key{{Code: keyEscape}}})
		}
		x.pump(5*time.Second, func() bool { return a.over.pick == nil })
		return o
	}
	for _, lang := range []Language{English, Chinese} {
		a.u.lang = lang
		shot, record := shown(a.shotPick(shotClipboard)), shown(a.recordPick())
		if !hasColor(shot.shown, colAccent, 200) || hasColor(shot.shown, colDanger, 200) {
			t.Errorf("%s: the screenshot's bar is not in the screenshot's colour", lang)
		}
		if !hasColor(record.shown, colDanger, 200) || hasColor(record.shown, colAccent, 200) {
			t.Errorf("%s: the recording's bar is not in the recording's colour", lang)
		}
		x.shotOverlay(shot, "overlay-pick-shot-"+string(lang))
		x.shotOverlay(record, "overlay-pick-record-"+string(lang))
	}
	for _, to := range shotTargets {
		for _, other := range shotTargets {
			if to != other && a.shotPick(to).title == a.shotPick(other).title {
				t.Errorf("screenshots to %d and %d have the same title", to, other)
			}
		}
	}
}

func TestScreenshotOfARegion(t *testing.T) {
	x := overlayHarness(t)
	a, h := x.a, x.h
	a.Tick(Input{Hotkeys: []int{hotkeyScreenshotFile}})
	open := h.open()
	if len(open) == 0 {
		t.Fatal("the screenshot hotkey did not open the picker")
	}
	o := open[len(open)-1]
	o.drag(40, 30, 40+321, 30+201)
	x.pump(30*time.Second, func() bool { return o.closed && !a.rec.shooting })
	x.pump(30*time.Second, func() bool { return !a.rec.shooting })
	shots, _ := filepath.Glob(filepath.Join(a.settings.ResourceDir, "Tools", "Screenshot *.png"))
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

// screenWindows are the drawing's windows: the picture, the marks over it and the panel.
func screenWindows(t *testing.T, h *fakeHost) (pic, marks, panel *fakeOverlay) {
	t.Helper()
	for _, o := range h.open() {
		switch {
		case !o.opts.Excluded:
			pic = o
		case o.opts.ClickThrough:
			marks = o
		default:
			panel = o
		}
	}
	if pic == nil || marks == nil || panel == nil {
		t.Fatalf("picture %+v, marks %+v, panel %+v", pic, marks, panel)
	}
	return pic, marks, panel
}

// screenPanelAt is where the panel is at first on a display, in its pixels: in its top right corner.
func screenPanelAt(d capture.Display) gfx.Rect {
	r := gfx.Rect{W: screenPanelW * screenPanelScale, H: min(screenPanelH*screenPanelScale, float32(d.Height)-24)}
	r.X, r.Y = float32(int(float32(d.Width)-r.W-12)), 12
	return r
}

// screenRowY is the middle of the panel's row of commands; screenEndX the middle of the i-th button at its right end
// (the mouse, a screenshot, closing), screenWipeX that of the button wiping the drawing.
func screenRowY(p gfx.Rect) float32  { return p.Y + (screenMenusH+screenCommandH/2)*screenPanelScale }
func screenWipeX(p gfx.Rect) float32 { return p.X + (8+14+6+2*34+16)*screenPanelScale }
func screenEndX(p gfx.Rect, i int) float32 {
	return p.Right() + (-8-3*34+float32(i)*34+16)*screenPanelScale
}

// screenTool is the middle of the button of the tool box's place with key.
func screenTool(p gfx.Rect, key uint16) (x, y float32) {
	var places []uint16
	for _, e := range artTools {
		if !slices.Contains(screenView, e.tool) && !slices.Contains(places, e.key) {
			places = append(places, e.key)
		}
	}
	i := slices.Index(places, key)
	const k = screenPanelScale
	cols := int((p.W - 16*k) / (screenToolStep * k))
	return p.X + (8+float32(i%cols)*screenToolStep+16)*k, p.Y + (screenMenusH+screenCommandH+4+float32(i/cols)*screenToolStep+16)*k
}

func TestDrawingOnTheScreen(t *testing.T) {
	x := overlayHarness(t)
	a, h := x.a, x.h
	a.brush = paint.Brush{Size: 16, Hardness: 1, Opacity: 1, Flow: 1, Spacing: 0.1, Color: color.NRGBA{R: 255, A: 255}}
	a.Tick(Input{Hotkeys: []int{hotkeyDraw}})
	sd := a.over.draw
	if sd == nil || !sd.drawing {
		t.Fatal("the draw hotkey did not start drawing")
	}
	x.frame(Input{})
	pic, marks, panel := screenWindows(t, h)
	// The picture is opened first, behind the others, and never takes the keyboard, so it never comes in front of
	// them: the panel has the keyboard.
	if slices.Index(h.overlays, pic) > slices.Index(h.overlays, panel) || pic.keyboard || !panel.keyboard || pic.clickThrough {
		t.Fatalf("picture first %v, keyboard %v, panel's keyboard %v, click-through %v",
			slices.Index(h.overlays, pic) < slices.Index(h.overlays, panel), pic.keyboard, panel.keyboard, pic.clickThrough)
	}
	d := sd.d
	for _, o := range []*fakeOverlay{pic, marks, panel} {
		if o.bounds != displayBounds(d) {
			t.Errorf("a window at %v on a display at %v", o.bounds, displayBounds(d))
		}
	}
	// The panel is opaque where it is and clear elsewhere, so the mouse goes through it to the picture there.
	p := screenPanelAt(d)
	if c := panel.shown.RGBAAt(int(p.X+p.W/2), int(p.Y+p.H-20)); c.A < 240 {
		t.Errorf("the panel shows %v", c)
	}
	if c := panel.shown.RGBAAt(250, 300); c.A != 0 {
		t.Errorf("away from the panel it shows %v", c)
	}
	pic.drag(100, 100, 400, 100)
	for i := 0; i < 4; i++ {
		x.frame(Input{})
	}
	if panel.raised != 0 {
		t.Error("the panel was raised during the press, which can end the picture's hold on the mouse")
	}
	for i := 0; i < 8; i++ {
		x.frame(Input{})
	}
	if c := pic.shown.RGBAAt(250, 100); c != (color.RGBA{255, 0, 0, 255}) {
		t.Errorf("the stroke shows as %v", c)
	}
	if pic.raised != 0 || panel.raised == 0 {
		t.Errorf("after a press on the drawing the picture was raised %d times, the panel %d", pic.raised, panel.raised)
	}
	// Untouched pixels are invisible but still catch the mouse while drawing.
	if c := pic.shown.RGBAAt(250, 300); c.A != 1 || c.R != 0 {
		t.Errorf("an untouched pixel is %v", c)
	}
	// The brush's outline follows the pointer on the marks, out of the picture.
	pic.mx, pic.my = 600, 500
	x.frame(Input{})
	x.frame(Input{})
	if !anythingIn(marks.shown, image.Rect(580, 480, 620, 520)) {
		t.Error("no brush outline on the marks")
	}
	x.shotOverlay(panel, "overlay-screen-panel")
	// The right button gives the mouse and the keyboard back; the drawing stays, without the floor.
	pic.queued = append(pic.queued, Input{X: 10, Y: 10, Pointer: []Pointer{{X: 10, Y: 10, Right: true, Down: true}}})
	for i := 0; i < 3; i++ {
		x.frame(Input{})
	}
	if sd.drawing || !pic.clickThrough || panel.keyboard {
		t.Fatalf("after the right button: drawing %v, click-through %v, panel's keyboard %v", sd.drawing, pic.clickThrough, panel.keyboard)
	}
	if c := pic.shown.RGBAAt(250, 300); c.A != 0 {
		t.Errorf("an untouched pixel keeps %v while not drawing", c)
	}
	if c := pic.shown.RGBAAt(250, 100); c.A != 255 {
		t.Error("the drawing went away")
	}
	// Choosing a tool on the panel draws again.
	panel.click(screenTool(p, 'B'))
	for i := 0; i < 10; i++ {
		x.frame(Input{})
	}
	if !sd.drawing || !panel.keyboard {
		t.Fatalf("choosing the brush: drawing %v, keyboard %v", sd.drawing, panel.keyboard)
	}
	// Wiping clears it all and can be undone, with the canvas's key.
	panel.click(screenWipeX(p), screenRowY(p))
	for i := 0; i < 10; i++ {
		x.frame(Input{})
	}
	if c := pic.shown.RGBAAt(250, 100); c.A > 1 {
		t.Errorf("after wiping %v", c)
	}
	panel.queued = append(panel.queued, Input{Ctrl: true, Keys: []Key{{Code: 'Z', Ctrl: true}}})
	for i := 0; i < 3; i++ {
		x.frame(Input{})
	}
	if c := pic.shown.RGBAAt(250, 100); c.A != 255 {
		t.Error("undo did not bring the drawing back")
	}
	// Closing removes the drawing's windows and leaves it in a canvas's file, which the canvas opens.
	path := sd.ad.path
	panel.click(screenEndX(p, 2), screenRowY(p))
	for i := 0; i < 10; i++ {
		x.frame(Input{})
	}
	if !pic.closed || !marks.closed || !panel.closed || a.over.draw != nil {
		t.Errorf("closed: picture %v, marks %v, panel %v", pic.closed, marks.closed, panel.closed)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		t.Fatal(err)
	}
	saved, err := paint.Load(f, st.Size())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(path, paint.Ext) || saved.Composite(image.Rect(250, 100, 251, 101)).RGBAAt(250, 100).A != 255 {
		t.Errorf("the file %s does not hold the drawing", path)
	}
}

// anythingIn reports whether anything shows within r.
func anythingIn(img *image.RGBA, r image.Rectangle) bool {
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			if img.RGBAAt(x, y).A > 0 {
				return true
			}
		}
	}
	return false
}

// However Windows orders the windows, the panel is the panel: a press over it that the picture's window got works
// the panel and paints nothing, with the brush chosen or any other tool.
func TestThePanelTakesWhatThePictureGot(t *testing.T) {
	x := overlayHarness(t)
	a, h := x.a, x.h
	a.brush = paint.Brush{Size: 16, Hardness: 1, Opacity: 1, Flow: 1, Spacing: 0.1, Color: color.NRGBA{R: 255, A: 255}}
	a.Tick(Input{Hotkeys: []int{hotkeyDraw}})
	x.frame(Input{})
	sd := a.over.draw
	pic, _, _ := screenWindows(t, h)
	p := screenPanelAt(sd.d)
	frames := func(n int) {
		for range n {
			x.frame(Input{})
		}
	}
	cx, cy := screenTool(p, 'E')
	pic.click(cx, cy)
	frames(10)
	if sd.tool != toolEraser {
		t.Errorf("a press over the panel in the picture's window chose %v", sd.tool)
	}
	if sd.ad.d.CanUndo() {
		t.Error("a press over the panel painted")
	}
	// A drag over it paints nothing either, with the pointer shown as over the panel.
	cx, cy = screenTool(p, 'B')
	pic.click(cx, cy)
	frames(10)
	pic.drag(p.X+40, p.Bottom()-30, p.X+200, p.Bottom()-30)
	frames(10)
	if sd.ad.d.CanUndo() {
		t.Error("a drag over the panel painted")
	}
	// A stroke that began on the drawing goes on over the panel.
	pic.drag(p.X-100, 400, p.X+100, 400)
	frames(10)
	if !sd.ad.d.CanUndo() {
		t.Error("the stroke did not paint")
	}
	if c := pic.shown.RGBAAt(int(p.X)+60, 400); c.A != 255 {
		t.Errorf("under the panel the stroke shows %v", c)
	}
	a.closeScreenDraw()
}

// The panel holds the canvas's tools, options, colours and layers, and the canvas's keys work on the screen as the
// user chose them, but for the two tools that look around the canvas.
func TestScreenPanelIsTheCanvas(t *testing.T) {
	x := overlayHarness(t)
	a, h := x.a, x.h
	a.brush = paint.Brush{Size: 16, Hardness: 1, Opacity: 1, Flow: 1, Spacing: 0.1, Color: color.NRGBA{R: 255, A: 255}}
	a.setKeys(toolID(toolEraser), []Key{{Code: 'K'}})
	a.Tick(Input{Hotkeys: []int{hotkeyDraw}})
	x.frame(Input{})
	sd := a.over.draw
	pic, _, panel := screenWindows(t, h)
	p := screenPanelAt(sd.d)
	frames := func(n int) {
		for range n {
			x.frame(Input{})
		}
	}
	press := func(keys ...Key) {
		for _, k := range keys {
			panel.queued = append(panel.queued, Input{Ctrl: k.Ctrl, Shift: k.Shift, Keys: []Key{k}})
		}
		frames(len(keys) + 2)
	}
	// The eraser's key as chosen, not E.
	press(Key{Code: 'E'})
	if sd.tool != toolBrush {
		t.Errorf("E chose %v; the eraser's key is K", sd.tool)
	}
	press(Key{Code: 'K'})
	if sd.tool != toolEraser {
		t.Errorf("K chose %v, want the eraser", sd.tool)
	}
	// The screen's tool is its own: the canvas tab's stays.
	if a.artTool == toolEraser {
		t.Error("the screen's tool changed the canvas's")
	}
	// The usual tools' keys, and the views' do nothing.
	for _, c := range []struct {
		key  uint16
		want artTool
	}{{'T', toolText}, {'C', toolPen}, {'M', toolMarquee}, {'L', toolLasso}, {'W', toolWand}, {'I', toolPicker}, {'S', toolClone}, {'H', toolPicker}, {'U', toolShape}} {
		if c.key == 'H' {
			press(Key{Code: 'I'}) // the tool H leaves alone
		}
		press(Key{Code: c.key})
		if sd.tool != c.want {
			t.Errorf("%c chose %v, want %v", c.key, sd.tool, c.want)
		}
	}
	press(Key{Code: 'Z'}, Key{Code: 'B'})
	// The brush keys, and the colours' swap, as on the canvas.
	size := a.brush.Size
	for _, dk := range drawingKeys {
		if dk.id == "draw.brush-larger" {
			press(dk.defaults[0])
		}
	}
	if a.brush.Size <= size {
		t.Errorf("the larger brush key left the size at %v", a.brush.Size)
	}
	fg, bg := a.brush.Color, a.backgroundColor()
	press(Key{Code: 'X'})
	if a.brush.Color != bg || a.backgroundColor() != fg {
		t.Errorf("X left the colours at %v and %v", a.brush.Color, a.backgroundColor())
	}
	press(Key{Code: 'X'})
	// A tool from the panel, with its options under it.
	panel.click(screenTool(p, 'U'))
	frames(10)
	if sd.tool != toolShape {
		t.Errorf("the panel's shape button chose %v", sd.tool)
	}
	x.shotOverlay(panel, "overlay-screen-panel-shape")
	panel.click(screenTool(p, 'B'))
	frames(10)
	// Layers, by the canvas's key.
	press(Key{Code: 'N', Ctrl: true, Shift: true})
	if n := len(sd.ad.d.Layers()); n != 2 {
		t.Errorf("Ctrl+Shift+N left %d layers", n)
	}
	// The selection, and a dialog, which is the screen's own: the main window knows nothing of it.
	press(Key{Code: 'A', Ctrl: true})
	if sd.ad.d.Selection == nil {
		t.Error("Ctrl+A selected nothing")
	}
	press(Key{Code: 'D', Ctrl: true}, Key{Code: 'L', Ctrl: true})
	if sd.adjust == nil || a.adjust != nil {
		t.Fatalf("Ctrl+L left the screen's dialog %v, the main window's %v", sd.adjust, a.adjust)
	}
	x.shotOverlay(panel, "overlay-screen-dialog")
	press(Key{Code: keyEscape})
	if sd.adjust != nil {
		t.Error("Esc left the dialog open")
	}
	// The menus are the canvas's.
	panel.click(p.X+(8+24)*screenPanelScale, p.Y+15*screenPanelScale)
	frames(10)
	if sd.menu == nil || a.menu != nil {
		t.Fatalf("the File menu: the screen's %v, the main window's %v", sd.menu, a.menu)
	}
	x.shotOverlay(panel, "overlay-screen-menu")
	press(Key{Code: keyEscape})
	frames(2)
	// Text, typed in place with the IME on, and committed as on the canvas.
	press(Key{Code: 'T'})
	pic.click(300, 300)
	frames(10)
	if !panel.typing || sd.ad.text == nil {
		t.Fatalf("clicking with the text tool: typing %v, text %v", panel.typing, sd.ad.text)
	}
	panel.queued = append(panel.queued, Input{Typed: []rune("hi")})
	frames(3)
	x.shotOverlay(panel, "overlay-screen-text")
	press(Key{Code: keyEnter, Ctrl: true})
	if panel.typing || sd.ad.text != nil {
		t.Fatalf("after committing: typing %v, text %v", panel.typing, sd.ad.text)
	}
	found := false
	for _, n := range sd.ad.d.Layers() {
		found = found || n.Text != nil && n.Text.Text == "hi"
	}
	if !found {
		t.Error("no text layer says what was typed")
	}
	// Ctrl+S saves a canvas's file.
	press(Key{Code: 'S', Ctrl: true})
	if st, err := os.Stat(sd.ad.path); err != nil || st.Size() == 0 {
		t.Errorf("Ctrl+S: %v", err)
	}
	a.closeScreenDraw()
	frames(1)
}

// Every tool's options stay in the panel, rows taking what the canvas's one row has, and leave room for the layers.
func TestEveryToolFitsThePanel(t *testing.T) {
	x := overlayHarness(t)
	a, h := x.a, x.h
	a.Tick(Input{Hotkeys: []int{hotkeyDraw}})
	x.frame(Input{})
	sd := a.over.draw
	_, _, panel := screenWindows(t, h)
	p := screenPanelAt(sd.d)
	for _, e := range artTools {
		if slices.Contains(screenView, e.tool) {
			continue
		}
		sd.tool = e.tool
		for range 4 {
			x.frame(Input{})
		}
		outside := panel.shown.Bounds()
		for _, r := range []image.Rectangle{{outside.Min, image.Pt(outside.Max.X, int(p.Y))}, {image.Pt(0, int(p.Y)), image.Pt(int(p.X), int(p.Bottom()))}, {image.Pt(0, int(p.Bottom())), outside.Max}} {
			if anythingIn(panel.shown, r) {
				t.Errorf("the options of %s spill out of the panel in %v", artToolNames[e.tool], r)
			}
		}
		if sd.rows > 8 {
			t.Errorf("the options of %s take %d rows", artToolNames[e.tool], sd.rows)
		}
		x.shotOverlay(panel, "overlay-screen-tool-"+toolID(e.tool))
	}
	a.closeScreenDraw()
}

func TestRecordingARegionWithTheToolbar(t *testing.T) {
	x := overlayHarness(t)
	a, h := x.a, x.h
	a.settings.Record.Region, a.settings.Record.Minimize, a.settings.Record.SystemAudio = true, false, false
	started := make(chan error, 1)
	a.startRecording(func(_ string, err error) { started <- err })
	open := h.open()
	if len(open) != len(a.rec.displays) || !open[len(open)-1].opts.Activate {
		t.Fatalf("recording a region did not open the picker on every display: %+v", open)
	}
	chosen := open[len(open)-1]
	x.frame(Input{})
	if !hasColor(chosen.shown, colDanger, 200) {
		t.Error("the picker does not say it is for a recording")
	}
	chosen.drag(200, 100, 200+641, 100+361)
	x.pump(time.Minute, func() bool { return len(started) > 0 })
	if err := <-started; err != nil {
		t.Fatal(err)
	}
	d, _ := a.chosenDisplay()
	if o := a.rec.rec.Options(); o.Region != image.Rect(200, 100, 841, 461) || o.Display.Key() != d.Key() {
		t.Errorf("recording region %v of %s", o.Region, o.Display.Key())
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
