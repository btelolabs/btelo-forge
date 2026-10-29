package ui

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/btelolabs/btelo-forge/internal/gfx"
	"github.com/btelolabs/btelo-forge/internal/paint"
)

// canvasHarness is an editor with a 400×300 canvas open, framed at the origin with a white background layer, as a
// screenshot opens; it saves into a test folder.
func canvasHarness(t testing.TB) (*harness, *artDoc) {
	t.Helper()
	x := newHarness(t)
	x.a.settings.ExportDir = x.mediaDir()
	d, err := paint.NewFramed(400, 300, "Background", color.NRGBA{R: 255, G: 255, B: 255, A: 255})
	if err != nil {
		t.Fatal(err)
	}
	x.a.openArt(d, filepath.Join(x.mediaDir(), "test"+paint.Ext))
	x.a.art.dirty = true
	if err := x.a.saveArt(x.a.art, true); err != nil {
		t.Fatal(err)
	}
	x.frame(Input{})
	x.frame(Input{})
	return x, x.a.art
}

// viewPoint is where canvas pixel (cx, cy) is on the window.
func (x *harness) viewPoint(ad *artDoc, cx, cy float64) (float32, float32) {
	l := x.a.artLayout(x.a.layout().body)
	sx, sy := ad.origin(l.view)
	return float32(sx + cx*ad.zoom), float32(sy + cy*ad.zoom)
}

// shown is the picture as the view shows it over the frame, paper and all.
func shown(ad *artDoc) []byte {
	img := image.NewRGBA(ad.d.Frame)
	ad.d.Render(img, img.Rect, 0)
	return img.Pix
}

func TestCanvasPaintsWithTheMouse(t *testing.T) {
	x, ad := canvasHarness(t)
	a := x.a
	if a.art == nil || ad.d.Frame.Dx() != 400 || !strings.HasSuffix(ad.path, paint.Ext) {
		t.Fatalf("canvas %+v", ad)
	}
	if _, err := os.Stat(ad.path); err != nil {
		t.Fatalf("a new canvas is saved at once: %v", err)
	}
	a.brush = paint.Brush{Size: 20, Hardness: 1, Opacity: 1, Spacing: 0.1, Color: color.NRGBA{B: 255, A: 255}}
	x.key('B', false)
	fx, fy := x.viewPoint(ad, 50, 150)
	tx, ty := x.viewPoint(ad, 350, 150)
	x.drag(fx, fy, tx, ty)
	if c := ad.look(200, 150); c != (color.RGBA{0, 0, 255, 255}) {
		t.Fatalf("the stroke did not reach the picture: %v", c)
	}
	if c := ad.look(200, 100); c != (color.RGBA{255, 255, 255, 255}) {
		t.Errorf("the stroke spread: %v", c)
	}
	x.shot("canvas-painted")

	// The eraser takes it off down to transparency.
	x.key('E', false)
	if a.artTool != toolEraser {
		t.Fatal("E did not choose the eraser")
	}
	x.drag(fx, fy, tx, ty)
	if c := ad.look(200, 150); c.A != 0 {
		t.Errorf("erased pixel %v", c)
	}
	// Undo brings the paint back, then the white.
	x.key('Z', true)
	if c := ad.look(200, 150); c != (color.RGBA{0, 0, 255, 255}) {
		t.Errorf("after undoing the eraser %v", c)
	}
	x.key('Z', true)
	if c := ad.look(200, 150); c != (color.RGBA{255, 255, 255, 255}) {
		t.Errorf("after undoing the brush %v", c)
	}
	x.frame(Input{Keys: []Key{{Code: 'Z', Ctrl: true, Shift: true}}})
	if c := ad.look(200, 150); c != (color.RGBA{0, 0, 255, 255}) {
		t.Errorf("after redo %v", c)
	}
}

func TestCanvasSelectionCopiesToTheClipboard(t *testing.T) {
	x, ad := canvasHarness(t)
	a, h := x.a, x.h
	x.key('M', false)
	fx, fy := x.viewPoint(ad, 100, 50)
	tx, ty := x.viewPoint(ad, 220, 130)
	x.drag(fx, fy, tx, ty)
	sel := ad.d.SelectionBounds()
	if sel.Dx() < 115 || sel.Dx() > 125 || sel.Dy() < 75 || sel.Dy() > 85 {
		t.Fatalf("selection %v", sel)
	}
	x.key('C', true)
	if h.clipImage == nil || h.clipImage.Bounds().Size() != sel.Size() {
		t.Fatalf("clipboard %v", h.clipImage)
	}
	// Paint is kept within the selection.
	a.brush.Size, a.brush.Opacity = 30, 1
	x.key('B', false)
	ox, oy := x.viewPoint(ad, 10, 90)
	ex, ey := x.viewPoint(ad, 390, 90)
	x.drag(ox, oy, ex, ey)
	if c := ad.look(20, 90); c != (color.RGBA{255, 255, 255, 255}) {
		t.Errorf("paint outside the selection: %v", c)
	}
	if c := ad.look(150, 90); c == (color.RGBA{255, 255, 255, 255}) {
		t.Error("no paint inside the selection")
	}
	x.shot("canvas-selection")
	// Ctrl+D lets go of the selection; Ctrl+V pastes the copy as a new layer.
	x.key('D', true)
	if ad.d.Selection != nil {
		t.Error("Ctrl+D kept the selection")
	}
	h.pasteImage = h.clipImage
	x.key('V', true)
	if len(ad.d.Layers()) != 2 || ad.d.Node(ad.d.Active).Name != "Pasted" {
		t.Errorf("paste made %d layers, chosen %q", len(ad.d.Layers()), ad.d.Node(ad.d.Active).Name)
	}
}

func TestCanvasLayerShortcutsAndPanel(t *testing.T) {
	x, ad := canvasHarness(t)
	d := ad.d
	x.frame(Input{Keys: []Key{{Code: 'N', Ctrl: true, Shift: true}}})
	if len(d.Layers()) != 2 || d.Node(d.Active).Name != "Layer 2" {
		t.Fatalf("new layer: %d layers, %q", len(d.Layers()), d.Node(d.Active).Name)
	}
	x.key('J', true)
	if d.Node(d.Active).Name != "Layer 2 copy" {
		t.Errorf("duplicate named %q", d.Node(d.Active).Name)
	}
	x.key('G', true)
	if n := d.Node(d.Active); !n.IsGroup() || n.Name != "Group 1" {
		t.Errorf("Ctrl+G made %+v", n)
	}
	x.frame(Input{})
	x.shot("canvas-layers")
	x.frame(Input{Keys: []Key{{Code: 'G', Ctrl: true, Shift: true}}})
	x.key('E', true)
	if len(d.Layers()) != 2 {
		t.Errorf("after ungroup and merge down: %d layers", len(d.Layers()))
	}
	x.a.u.lang = Chinese
	x.frame(Input{})
	x.shot("canvas-zh")
	x.a.u.lang = English
}

func TestCanvasIsSavedAndReopened(t *testing.T) {
	x, ad := canvasHarness(t)
	a := x.a
	a.brush = paint.Brush{Size: 40, Hardness: 0.5, Opacity: 0.8, Spacing: 0.1, Color: color.NRGBA{G: 200, A: 255}}
	x.key('B', false)
	fx, fy := x.viewPoint(ad, 60, 60)
	tx, ty := x.viewPoint(ad, 300, 220)
	x.drag(fx, fy, tx, ty)
	look := shown(ad)
	path := ad.path
	a.closeTab(a.current())
	if a.art != nil {
		t.Fatal("the canvas tab stayed")
	}
	x.frame(Input{Dropped: []string{path}})
	x.pump(10*time.Second, func() bool { return a.art != nil })
	if !bytes.Equal(shown(a.art), look) {
		t.Fatal("the reopened canvas looks different")
	}
	// Opening the same file again switches to its tab.
	tabs := len(a.docs)
	x.frame(Input{Dropped: []string{path}})
	if len(a.docs) != tabs {
		t.Error("the same canvas opened twice")
	}
}

func TestPictureDroppedOnAnEmptyTabOpensAsACanvas(t *testing.T) {
	x := newHarness(t)
	dir := x.mediaDir()
	img := image.NewNRGBA(image.Rect(0, 0, 64, 48))
	img.SetNRGBA(10, 10, color.NRGBA{R: 255, A: 255})
	path := filepath.Join(dir, "shot.png")
	f, _ := os.Create(path)
	png.Encode(f, img)
	f.Close()
	x.frame(Input{Dropped: []string{path}})
	x.pump(10*time.Second, func() bool { return x.a.art != nil })
	ad := x.a.art
	if ad.d.Frame.Dx() != 64 || ad.look(10, 10) != (color.RGBA{255, 0, 0, 255}) || filepath.Dir(ad.path) != dir {
		t.Errorf("canvas %d×%d at %s", ad.d.Frame.Dx(), ad.d.Frame.Dy(), ad.path)
	}
}

// A new canvas has no size: it opens at once, empty, on white paper, with a 1024×1024 artboard holding its one
// layer, and grows with what is drawn on it.
func TestNewCanvasHasNoSize(t *testing.T) {
	x := newHarness(t)
	a := x.a
	a.settings.ExportDir = x.mediaDir()
	a.newCanvasNow()
	x.frame(Input{})
	ad := a.art
	if ad == nil || !ad.d.Frame.Empty() || !ad.d.Paper || len(ad.d.Layers()) != 1 {
		t.Fatalf("a new canvas %+v", ad)
	}
	boards := ad.d.Boards()
	if len(boards) != 1 || boards[0].Rect != image.Rect(0, 0, 1024, 1024) || ad.d.BoardOf(ad.d.Active) != boards[0].ID || ad.d.CanUndo() {
		t.Fatalf("a new canvas's artboards %+v, its layer in %d", boards, ad.d.BoardOf(ad.d.Active))
	}
	// With the artboard taken away, keeping its layer, the layer grows with what is drawn on it.
	if err := ad.d.DeleteBoard(boards[0].ID); err != nil {
		t.Fatal(err)
	}
	if c := ad.look(-5000, 7000); c != (color.RGBA{255, 255, 255, 255}) {
		t.Errorf("the paper is %v far out", c)
	}
	x.shot("canvas-new")
	// A stroke well outside where the window first looked is kept.
	a.brush = paint.Brush{Size: 20, Hardness: 1, Opacity: 1, Flow: 1, Spacing: 0.1, Round: 1, Color: color.NRGBA{B: 255, A: 255}}
	x.key('B', false)
	ad.zoomAt(a.artLayout(a.layout().body).view, 500, 500, 0.1)
	x.frame(Input{})
	x.dragWith(ad, [2]float64{-3000, -2000}, [2]float64{3000, 2000}, false, false)
	if c := ad.look(-3000, -2000); c != (color.RGBA{0, 0, 255, 255}) {
		t.Errorf("the stroke's start %v", c)
	}
	if b := ad.d.Bounds(); b.Dx() < 6000 {
		t.Errorf("the canvas holds %v", b)
	}
	x.frame(Input{})
	x.shot("canvas-zoomed-out")
	// Ctrl+N adds a layer; Ctrl+Alt+N makes another canvas, in a tab of its own.
	layers := len(ad.d.Layers())
	x.key('N', true)
	if len(ad.d.Layers()) != layers+1 {
		t.Error("Ctrl+N did not add a layer")
	}
	tabs := len(a.docs)
	x.frame(Input{Keys: []Key{{Code: 'N', Ctrl: true, Alt: true}}})
	if len(a.docs) != tabs+1 || a.art == ad {
		t.Error("Ctrl+Alt+N did not open another canvas")
	}
}

// A click on a text box gives it the keyboard at once: the frame it is clicked in counts as drawing it focused.
func TestClickingATextBoxFocusesIt(t *testing.T) {
	x, _ := canvasHarness(t)
	a := x.a
	a.openDialog(dialogImageSize)
	x.frame(Input{})
	in := rectCentered(a.screen, a.u.px(360), a.u.px(250)).Inset(a.u.px(22))
	x.click(gfx.Rect{X: in.X + a.u.px(116), Y: in.Y + a.u.px(40), W: a.u.px(90), H: a.u.px(32)})
	if a.u.focus != "dialog/a" {
		t.Fatalf("the clicked box has no focus: %q", a.u.focus)
	}
	x.frame(Input{Keys: []Key{{Code: keyEnd}, {Code: keyBack}, {Code: keyBack}, {Code: keyBack}}})
	x.frame(Input{Typed: []rune("640")})
	if a.dialog.a != "640" {
		t.Errorf("typing gave %q", a.dialog.a)
	}
}

// An open dialog gets Esc: the window's shortcuts, which would take every key, wait while it is open.
func TestDialogsTakeEscape(t *testing.T) {
	x, _ := canvasHarness(t)
	a := x.a
	a.openDialog(dialogImageSize)
	x.frame(Input{})
	x.frame(Input{Keys: []Key{{Code: keyEscape}}})
	if a.dialog.kind != dialogNone {
		t.Error("Esc did not close the image size dialog")
	}
	a.rec.open = true
	x.frame(Input{})
	x.frame(Input{Keys: []Key{{Code: keyEscape}}})
	if a.rec.open {
		t.Error("Esc did not close the recorder")
	}
}
