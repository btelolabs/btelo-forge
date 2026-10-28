package ui

import (
	"image/color"
	"testing"

	"github.com/btelolabs/btelo-video-cut/internal/paint"
)

func TestCanvasBucketGradientShapesAndClone(t *testing.T) {
	x, ad := canvasHarness(t)
	a := x.a
	d := ad.d
	a.brush.Color = color.NRGBA{R: 255, A: 255}
	a.settings.Paint.Background = "#0000ff"

	// G picks the gradient, Shift+G the paint bucket in the same place.
	x.key('G', false)
	if a.artTool != toolGradient {
		t.Fatalf("G chose tool %d", a.artTool)
	}
	x.frame(Input{Keys: []Key{{Code: 'G', Shift: true}}})
	if a.artTool != toolBucket {
		t.Fatalf("Shift+G chose tool %d", a.artTool)
	}
	px, py := x.viewPoint(ad, 200, 150)
	x.click(rectAt(px-1, py-1, 2, 2))
	if c := ad.look(10, 10); c != (color.RGBA{255, 0, 0, 255}) || d.UndoName() != "Paint bucket" {
		t.Fatalf("the bucket filled %v (%q)", c, d.UndoName())
	}
	x.frame(Input{})
	x.shot("canvas-bucket")
	// G now brings back the bucket; Shift+G goes on to the gradient.
	x.key('B', false)
	x.key('G', false)
	if a.artTool != toolBucket {
		t.Errorf("G did not keep the bucket: %d", a.artTool)
	}
	x.frame(Input{Keys: []Key{{Code: 'G', Shift: true}}})
	// A linear gradient from the foreground to the background, left to right.
	d.AddLayer("Gradient")
	x.dragWith(ad, [2]float64{0, 150}, [2]float64{400, 150}, false, false)
	if d.UndoName() != "Gradient" {
		t.Fatalf("the gradient is %q", d.UndoName())
	}
	left, right := ad.look(2, 50), ad.look(397, 50)
	if left.R < 250 || left.B > 5 || right.B < 250 || right.R > 5 {
		t.Errorf("gradient ends: %v %v", left, right)
	}
	x.frame(Input{})
	x.shot("canvas-gradient")

	// A rectangle on a layer of its own, from the shape tool.
	x.key('U', false)
	a.settings.Paint.ShapeStroke, a.settings.Paint.ShapeWidth = true, 4
	before := len(d.Layers())
	x.dragWith(ad, [2]float64{100, 100}, [2]float64{200, 180}, false, false)
	if len(d.Layers()) != before+1 || d.UndoName() != "Add shape" || d.Node(d.Active).Vector == nil {
		t.Fatalf("no shape layer: %d layers, %q", len(d.Layers()), d.UndoName())
	}
	if c := ad.look(150, 140); c != (color.RGBA{255, 0, 0, 255}) {
		t.Errorf("the shape's fill %v", c)
	}
	if c := ad.look(101, 140); c != (color.RGBA{0, 0, 255, 255}) {
		t.Errorf("the shape's stroke %v", c)
	}
	// Shift keeps it square.
	x.dragWith(ad, [2]float64{250, 50}, [2]float64{300, 120}, true, false)
	if v := d.Node(d.Active).Vector; v == nil || v.Size.X != v.Size.Y || v.Size.X < 60 {
		t.Errorf("Shift did not keep it square: %+v", v)
	}
	// A star, from the shape tool's options.
	a.settings.Paint.ShapeKind = shapeStar
	x.dragWith(ad, [2]float64{20, 200}, [2]float64{120, 290}, false, false)
	if v := d.Node(d.Active).Vector; v == nil || v.Kind != paint.VectorStar || v.Sides != 5 {
		t.Errorf("the star %+v", v)
	}
	a.settings.Paint.ShapeKind = shapeRect
	x.frame(Input{})
	x.shot("canvas-shapes")

	// The clone stamp asks for a source first, then paints from it.
	x.key('S', false)
	d.AddLayer("Patch") // on top, above the rectangles
	a.settings.Paint.CloneAllLayers = true
	a.brush.Size, a.brush.Hardness, a.brush.Opacity, a.brush.Flow = 20, 1, 1, 1
	x.dragWith(ad, [2]float64{60, 250}, [2]float64{70, 250}, false, false)
	if d.UndoName() == "Clone stamp" {
		t.Fatal("the clone stamp painted without a source")
	}
	sx, sy := x.viewPoint(ad, 150, 140) // inside the red rectangle
	x.mx, x.my = sx, sy
	x.frame(Input{Alt: true})
	x.frame(Input{Down: true, Alt: true, Pointer: []Pointer{{X: sx, Y: sy, Down: true}}})
	x.frame(Input{Alt: true, Pointer: []Pointer{{X: sx, Y: sy}}})
	if !ad.cloneSet {
		t.Fatal("Alt+click did not set the clone source")
	}
	x.dragWith(ad, [2]float64{350, 250}, [2]float64{355, 250}, false, false)
	if d.UndoName() != "Clone stamp" {
		t.Fatalf("the clone stamp did not paint: %q", d.UndoName())
	}
	// It painted the rectangle's red, 200 to the left and 110 up.
	if c := d.Node(d.Active).Pixels.At(352, 250); c != (color.RGBA{255, 0, 0, 255}) {
		t.Errorf("cloned %v", c)
	}
	x.frame(Input{})
	x.shot("canvas-clone")
}
