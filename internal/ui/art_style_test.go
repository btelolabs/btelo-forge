package ui

import (
	"image"
	"image/color"
	"testing"
)

func TestCanvasLayerStyles(t *testing.T) {
	x, ad := canvasHarness(t)
	a := x.a
	d := ad.d
	n := d.AddLayer("Square")
	d.Select(image.Rect(120, 90, 280, 210))
	d.Fill(color.NRGBA{R: 40, G: 120, B: 230, A: 255})
	d.Deselect()
	ad.changedAll()
	a.openStyle(ad)
	f := a.style
	if f == nil {
		t.Fatal("no Layer Style dialog")
	}
	// Turning effects on shows them at once; Cancel takes them back.
	f.toggle(effectDropShadow, true)
	f.st.DropShadow.Distance, f.st.DropShadow.Size, f.st.DropShadow.Opacity = 12, 8, 1
	f.toggle(effectStroke, true)
	f.st.Stroke.Size, f.st.Stroke.Color = 6, color.NRGBA{R: 255, G: 200, A: 255}
	f.page = effectStroke
	x.frame(Input{})
	x.frame(Input{})
	x.shot("canvas-layer-style")
	if c := ad.look(117, 150); c != (color.RGBA{255, 200, 0, 255}) {
		t.Errorf("the stroke shows %v", c)
	}
	if c := ad.look(276, 216); c.R > 60 {
		t.Errorf("the shadow shows %v", c)
	}
	x.key(keyEscape, false)
	if d.Node(n.ID).Style != nil || ad.look(117, 150) != (color.RGBA{255, 255, 255, 255}) {
		t.Fatal("cancel kept the effects")
	}
	// OK keeps them as one step; turning one off in the dialog and on again keeps its settings.
	a.openStyle(ad)
	f = a.style
	f.toggle(effectStroke, true)
	f.st.Stroke.Size = 9
	f.toggle(effectStroke, false)
	f.toggle(effectStroke, true)
	if f.st.Stroke.Size != 9 {
		t.Errorf("the stroke came back at %v", f.st.Stroke.Size)
	}
	x.frame(Input{})
	x.key(keyEnter, false)
	if s := d.Node(n.ID).Style; s == nil || s.Stroke == nil || s.Stroke.Size != 9 || d.UndoName() != "Layer style" {
		t.Fatalf("after OK: %+v (%q)", s, d.UndoName())
	}
	// Painting on the styled layer redraws its effects around the stroke too.
	x.key('B', false)
	a.brush.Color, a.brush.Size, a.brush.Hardness = color.NRGBA{R: 40, G: 120, B: 230, A: 255}, 20, 1
	x.dragWith(ad, [2]float64{60, 40}, [2]float64{100, 40}, false, false)
	x.frame(Input{})
	if c := ad.look(80, 26); c != (color.RGBA{255, 0, 0, 255}) {
		t.Errorf("the new stroke has no effect stroke: %v", c)
	}
	x.shot("canvas-layer-style-painted")
}
