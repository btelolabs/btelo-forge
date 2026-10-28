package ui

import (
	"image/color"
	"testing"

	"github.com/btelolabs/btelo-video-cut/internal/paint"
)

func TestCanvasAdjustmentLayers(t *testing.T) {
	x, ad := canvasHarness(t)
	a := x.a
	d := ad.d
	d.SelectAll()
	d.Fill(color.NRGBA{R: 200, G: 120, B: 40, A: 255})
	d.Deselect()
	ad.changedAll()
	l := a.artLayout(a.layout().body)
	open := func(label string) {
		x.click(x.menuTitle(l, menuLayer))
		x.clickSubmenuItem(a.u.t("New adjustment layer"), label)
	}
	// Hue/Saturation as a layer: the change shows as the settings move, and Cancel takes the layer away.
	before := len(d.Layers())
	open(a.u.t("Hue/Saturation…"))
	if a.adjust == nil || a.adjust.layer == nil || len(d.Layers()) != before+1 {
		t.Fatalf("no adjustment layer dialog: %+v", a.adjust)
	}
	a.adjust.values[1] = -100 // no saturation
	x.frame(Input{})
	if c := ad.look(100, 100); c.R != c.G || c.G != c.B {
		t.Errorf("desaturating shows %v", c)
	}
	x.key(keyEscape, false)
	if len(d.Layers()) != before || ad.look(100, 100) != (color.RGBA{200, 120, 40, 255}) {
		t.Fatalf("cancel left %d layers, %v", len(d.Layers()), ad.look(100, 100))
	}
	// Levels, kept with OK: the background's pixels stay as they were.
	open(a.u.t("Levels…"))
	a.adjust.values[4] = 128 // output white at half
	x.frame(Input{})
	x.frame(Input{})
	x.shot("canvas-adjustment-levels")
	x.key(keyEnter, false)
	n := d.Node(d.Active)
	if n == nil || n.Adjust == nil || n.Adjust.Kind != paint.AdjustLevels || d.UndoName() != "New adjustment layer" {
		t.Fatalf("the levels layer %+v (%q)", n, d.UndoName())
	}
	if c := ad.look(100, 100); c.R > 110 {
		t.Errorf("levels shows %v", c)
	}
	if bg := d.Layers()[len(d.Layers())-1].Pixels.At(100, 100); bg != (color.RGBA{200, 120, 40, 255}) {
		t.Errorf("the background changed to %v", bg)
	}
	// Its settings open again and change as one step.
	a.openAdjustmentSettings(ad, n.ID)
	a.adjust.values[4] = 255
	x.frame(Input{})
	x.key(keyEnter, false)
	if c := ad.look(100, 100); c.R != 200 || d.UndoName() != "Adjustment" {
		t.Errorf("after the change %v (%q)", c, d.UndoName())
	}
	// Invert has no settings: it is made at once, and painting black on its mask hides it there.
	x.click(x.menuTitle(l, menuLayer))
	x.clickSubmenuItem(a.u.t("New adjustment layer"), a.u.t("Invert"))
	if c := ad.look(100, 100); c != (color.RGBA{55, 135, 215, 255}) {
		t.Errorf("inverted %v", c)
	}
	if !d.EditMask {
		t.Fatal("the new adjustment layer's mask is not chosen")
	}
	x.key('B', false)
	a.brush.Color, a.brush.Size, a.brush.Hardness = color.NRGBA{A: 255}, 60, 1
	x.dragWith(ad, [2]float64{20, 150}, [2]float64{120, 150}, false, false)
	if c := ad.look(70, 150); c != (color.RGBA{200, 120, 40, 255}) {
		t.Errorf("under the black on the mask %v", c)
	}
	x.frame(Input{})
	x.shot("canvas-adjustment-invert")
}
