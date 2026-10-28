package ui

import (
	"image"
	"image/color"
	"math"
	"testing"

	"github.com/btelolabs/btelo-video-cut/internal/gfx"
	"github.com/btelolabs/btelo-video-cut/internal/paint"
)

// dialogOK clicks the open dialog's OK button, as drawArtDialog lays it out.
func (x *harness) dialogOK(height float32) {
	u := &x.a.u
	box := rectCentered(x.a.screen, u.px(360), u.px(height)).Inset(u.px(22))
	x.click(gfx.Rect{X: box.Right() - u.px(100), Y: box.Bottom() - u.px(34), W: u.px(100), H: u.px(34)})
}

func TestCanvasFreeTransform(t *testing.T) {
	x, ad := canvasHarness(t)
	a := x.a
	d := ad.d
	l := d.AddLayer("Square")
	d.Select(image.Rect(100, 100, 200, 200))
	d.Fill(color.NRGBA{B: 255, A: 255})
	d.Deselect()
	ad.changedAll()
	blue := color.RGBA{0, 0, 255, 255}

	x.key('T', true)
	if ad.xf == nil {
		t.Fatal("Ctrl+T did not start Free Transform")
	}
	if ad.xf.box != (xbox{cx: 150, cy: 150, w: 100, h: 100}) {
		t.Fatalf("the transform box is %+v", ad.xf.box)
	}
	// A corner keeps the proportions: dragged to (300, 250) from the anchor at (100, 100) it goes 1.75 times as far.
	x.dragWith(ad, [2]float64{200, 200}, [2]float64{300, 250}, false, false)
	if b := ad.xf.box; math.Abs(b.w-175) > 0.01 || math.Abs(b.h-175) > 0.01 || math.Abs(b.cx-187.5) > 0.01 {
		t.Fatalf("after dragging the corner the box is %+v", b)
	}
	if c := ad.look(260, 260); c != blue {
		t.Errorf("the preview does not show the scaled square: %v", c)
	}
	x.frame(Input{})
	x.shot("canvas-transform")
	// Shift frees the proportions; a side handle stretches one way only.
	x.dragWith(ad, [2]float64{275, 187.5}, [2]float64{300, 187.5}, false, false)
	if b := ad.xf.box; math.Abs(b.w-200) > 0.01 || math.Abs(b.h-175) > 0.01 {
		t.Errorf("after dragging the side the box is %+v", b)
	}
	x.key(keyEnter, false)
	if ad.xf != nil || d.UndoName() != "Free transform" {
		t.Fatalf("Enter did not apply the transform: %q", d.UndoName())
	}
	if c := ad.look(290, 270); c != blue {
		t.Errorf("after applying the square does not reach 290,270: %v", c)
	}
	if c := ad.look(105, 105); c != blue {
		t.Errorf("the square's corner moved: %v", c)
	}

	// Dragging outside the box turns it; Shift snaps to 15°. Esc puts everything back.
	x.key('T', true)
	b := ad.xf.box
	x.dragWith(ad, [2]float64{b.cx + 150, b.cy}, [2]float64{b.cx + 3, b.cy + 150}, true, false)
	if got := ad.xf.box.angle; math.Abs(got-90) > 0.01 {
		t.Errorf("turned to %.2f°, want 90°", got)
	}
	x.frame(Input{})
	x.shot("canvas-transform-turned")
	x.key(keyEscape, false)
	if ad.xf != nil || d.UndoName() != "Free transform" || ad.look(290, 270) != blue {
		t.Errorf("Esc did not cancel: %q %v", d.UndoName(), ad.look(290, 270))
	}

	// The options bar's numbers: 50% wide, then Ctrl+Z, which applies and takes it back as Photoshop does.
	x.key('T', true)
	ad.xf.box.w = ad.xf.box.w / 2
	x.frame(Input{})
	x.key('Z', true)
	if ad.xf != nil || ad.look(290, 270) != blue {
		t.Errorf("undo during the transform: %v", ad.look(290, 270))
	}
	// Choosing another tool applies the transform, as does a command from the menus.
	x.key('T', true)
	ad.xf.box.cx -= 50
	x.frame(Input{})
	x.key('B', false)
	if ad.xf != nil || d.UndoName() != "Free transform" || a.artTool != toolBrush {
		t.Errorf("a new tool did not apply the transform: %q", d.UndoName())
	}
	if _, err := d.BeginTransform(); err != nil {
		t.Errorf("a transformed layer cannot be transformed again: %v", err)
	}
	_ = l
}

func TestCanvasCropTool(t *testing.T) {
	x, ad := canvasHarness(t)
	d := ad.d
	d.AddLayer("Ink")
	d.Select(image.Rect(50, 40, 60, 50))
	d.Fill(color.NRGBA{R: 255, A: 255})
	d.Deselect()
	ad.changedAll()
	x.key('C', false)
	x.frame(Input{})
	if ad.crop == nil || ad.crop.box.rect() != image.Rect(0, 0, 400, 300) {
		t.Fatalf("the crop box does not start on the whole picture: %+v", ad.crop)
	}
	x.dragWith(ad, [2]float64{0, 0}, [2]float64{50, 40}, false, false)
	if r := ad.crop.box.rect(); r != image.Rect(50, 40, 400, 300) {
		t.Fatalf("the crop box after dragging its corner: %v", r)
	}
	x.frame(Input{})
	x.shot("canvas-crop")
	x.key(keyEnter, false)
	if d.Frame.Dx() != 350 || d.Frame.Dy() != 260 || d.UndoName() != "Crop" {
		t.Fatalf("cropped to %d×%d (%q)", d.Frame.Dx(), d.Frame.Dy(), d.UndoName())
	}
	// The frame moved; nothing moved on the canvas, and what was cropped off is gone.
	if d.Frame != image.Rect(50, 40, 400, 300) || ad.look(55, 45) != (color.RGBA{255, 0, 0, 255}) {
		t.Errorf("cropped to %v, its corner %v", d.Frame, ad.look(55, 45))
	}
	if c := d.Node(d.Active).Pixels.At(20, 20); c.A != 0 {
		t.Errorf("a cropped-off pixel is kept: %v", c)
	}
	x.frame(Input{})
	if ad.crop == nil || ad.crop.box.rect() != d.Frame {
		t.Errorf("the crop box did not start again on the cropped picture: %+v", ad.crop)
	}
	// A square ratio keeps the box square.
	x.a.settings.Paint.CropRatio = 2
	ad.crop = nil
	x.frame(Input{})
	if r := ad.crop.box.rect(); r.Dx() != r.Dy() {
		t.Errorf("the square crop box is %v", r)
	}
	x.key(keyEscape, false)
	x.key('Z', true)
	if d.Frame.Dx() != 400 || d.Frame.Dy() != 300 {
		t.Errorf("undo gives %d×%d", d.Frame.Dx(), d.Frame.Dy())
	}
}

func TestCanvasImageMenu(t *testing.T) {
	x, ad := canvasHarness(t)
	a := x.a
	d := ad.d
	l := d.AddLayer("Ink")
	d.Select(image.Rect(0, 0, 10, 10))
	d.Fill(color.NRGBA{R: 255, A: 255})
	d.Deselect()
	ad.changedAll()
	x.frame(Input{})
	lay := a.artLayout(a.layout().body)
	x.click(x.menuTitle(lay, menuImage))
	if a.menu == nil {
		t.Fatal("the Image menu did not open")
	}
	x.shot("canvas-menu-image")
	// Image Size keeps the proportions as the width is typed.
	x.clickMenuItem(a.u.t("Image size…"))
	if a.dialog.kind != dialogImageSize || a.dialog.a != "400" {
		t.Fatalf("the image size dialog: %+v", a.dialog)
	}
	x.frame(Input{})
	in := rectCentered(a.screen, a.u.px(360), a.u.px(250)).Inset(a.u.px(22))
	x.click(gfx.Rect{X: in.X + a.u.px(116), Y: in.Y + a.u.px(40), W: a.u.px(90), H: a.u.px(32)})
	x.frame(Input{Keys: []Key{{Code: keyBack}, {Code: keyBack}, {Code: keyBack}}})
	x.frame(Input{Typed: []rune("200")})
	if a.dialog.b != "150" {
		t.Errorf("the height did not follow the width: %q", a.dialog.b)
	}
	x.shot("canvas-image-size")
	x.dialogOK(250)
	if d.Frame.Dx() != 200 || d.Frame.Dy() != 150 || d.UndoName() != "Image size" {
		t.Fatalf("image size gives %d×%d (%q)", d.Frame.Dx(), d.Frame.Dy(), d.UndoName())
	}
	// Canvas Size anchored at the top left adds room on the right and below.
	a.openDialog(dialogCanvasSize)
	a.dialog.a, a.dialog.b, a.dialog.anchor = "300", "200", 0
	x.frame(Input{})
	x.shot("canvas-canvas-size")
	x.dialogOK(300)
	if d.Frame.Dx() != 300 || d.Frame.Dy() != 200 {
		t.Fatalf("canvas size gives %d×%d", d.Frame.Dx(), d.Frame.Dy())
	}
	if c := d.Node(l.ID).Pixels.At(2, 2); c.R < 250 {
		t.Errorf("anchored at the top left the pixels moved: %v", c)
	}
	// Turning the canvas a quarter swaps its sides.
	x.frame(Input{})
	x.click(x.menuTitle(lay, menuImage))
	x.clickSubmenuItem(a.u.t("Image rotation"), a.u.t("90° clockwise"))
	if d.Frame.Dx() != 200 || d.Frame.Dy() != 300 || d.UndoName() != "Rotate canvas" {
		t.Errorf("turned canvas %d×%d (%q)", d.Frame.Dx(), d.Frame.Dy(), d.UndoName())
	}
	// The layer's flips from the Edit menu.
	x.frame(Input{})
	x.click(x.menuTitle(lay, menuEdit))
	x.clickSubmenuItem(a.u.t("Transform"), a.u.t("Flip horizontal"))
	if d.UndoName() != "Flip" {
		t.Errorf("flip from the Edit menu: %q", d.UndoName())
	}
	// Chinese, with Free Transform's options bar.
	a.u.lang = Chinese
	x.key('T', true)
	x.frame(Input{})
	x.shot("canvas-transform-zh")
	a.u.lang = English
	_ = paint.Ext
}
