package ui

import (
	"image/color"
	"math"
	"testing"

	"github.com/btelolabs/btelo-forge/internal/paint"
)

// With a vector layer chosen, the colours are its fill and stroke: the colour panel and the eyedropper colour the
// one being edited, X switches between them and Shift+X swaps them, each change one step.
func TestVectorLayerColours(t *testing.T) {
	x, ad := canvasHarness(t)
	a, d := x.a, ad.d
	n := d.AddVector(solidSquare(100, 100, 100, red), "Shape")
	x.frame(Input{})
	if c := a.pickerColor(); c != red {
		t.Fatalf("the colour panel shows %v, not the fill", c)
	}
	brush := a.brush.Color
	steps, _ := d.History()
	a.setPickerColor(blue)
	x.frame(Input{})
	if v := d.Node(n.ID).Vector; v.Fill.Color != blue || a.brush.Color != brush {
		t.Fatalf("the panel's colour went to %v; the brush's is %v", v.Fill.Color, a.brush.Color)
	}
	if now, _ := d.History(); len(now) != len(steps)+1 || now[len(now)-1] != "Fill" {
		t.Errorf("colouring the fill is one step: %v", now)
	}
	// X: the stroke, which a colour turns on.
	x.key('X', false)
	if !a.pickBackground {
		t.Fatal("X should switch to the stroke")
	}
	a.setPickerColor(color.NRGBA{G: 255, A: 255})
	x.frame(Input{})
	if v := d.Node(n.ID).Vector; v.Stroke.Paint.Kind != paint.PaintSolid || v.Stroke.Paint.Color.G != 255 || v.Stroke.Width <= 0 {
		t.Fatalf("the stroke %+v", v.Stroke)
	}
	// Shift+X swaps them.
	x.frame(Input{Keys: []Key{{Code: 'X', Shift: true}}})
	if v := d.Node(n.ID).Vector; v.Fill.Color.G != 255 || v.Stroke.Paint.Color != blue {
		t.Errorf("after Shift+X: fill %v, stroke %v", v.Fill.Color, v.Stroke.Paint.Color)
	}
	// The eyedropper takes the background's white straight onto the stroke being coloured.
	x.key('I', false)
	fx, fy := x.viewPoint(ad, 20, 20)
	x.drag(fx, fy, fx, fy)
	if v := d.Node(n.ID).Vector; v.Stroke.Paint.Color != (color.NRGBA{R: 255, G: 255, B: 255, A: 255}) || d.Active != n.ID {
		t.Errorf("the eyedropper gave the stroke %v", v.Stroke.Paint.Color)
	}
	// With no vector layer chosen the colours are the brush's again.
	d.Choose(d.Layers()[len(d.Layers())-1].ID)
	a.pickBackground = false
	if c := a.pickerColor(); c != a.brush.Color {
		t.Errorf("the panel shows %v away from the vector layer", c)
	}
}

// The gradient tool on a vector layer lays a gradient over its fill from the drag, with handles to drag; the
// options bar changes its kind, and stops are added and removed.
func TestVectorGradientTool(t *testing.T) {
	x, ad := canvasHarness(t)
	a, d := x.a, ad.d
	n := d.AddVector(paint.Vector{Kind: paint.VectorRect, Size: paint.Pt{X: 200, Y: 100}, M: paint.Affine{A: 2, E: 2, C: 50, F: 50},
		Fill: solidPaint(red)}, "Shape") // its own space is half the canvas's
	x.key('G', false)
	x.dragWith(ad, [2]float64{100, 150}, [2]float64{300, 150}, false, false)
	v := d.Node(n.ID).Vector
	if v.Fill.Kind != paint.PaintLinear || len(v.Fill.Stops) != 2 || v.Fill.Stops[0].Color != red {
		t.Fatalf("the fill after the drag %+v", v.Fill)
	}
	if from, to := v.M.Map(v.Fill.From), v.M.Map(v.Fill.To); from.Dist(paint.Pt{X: 100, Y: 150}) > 1 || to.Dist(paint.Pt{X: 300, Y: 150}) > 1 {
		t.Errorf("the gradient runs from %v to %v", from, to)
	}
	ad.changedAll()
	left, right := ad.look(110, 150), ad.look(290, 150)
	if left.R < 230 || right.G < 230 {
		t.Errorf("the gradient shows %v at its start and %v at its end", left, right)
	}
	// Its end handle drags.
	x.dragWith(ad, [2]float64{300, 150}, [2]float64{250, 200}, false, false)
	if to := d.Node(n.ID).Vector.M.Map(d.Node(n.ID).Vector.Fill.To); to.Dist(paint.Pt{X: 250, Y: 200}) > 1 {
		t.Errorf("the end handle went to %v", to)
	}
	// Elliptical: the handle across squashes it.
	a.setPaintKind(ad, d.Node(n.ID), paint.PaintRadial)
	v = d.Node(n.ID).Vector
	if v.Fill.Kind != paint.PaintRadial {
		t.Fatalf("the kind %v", v.Fill.Kind)
	}
	across := v.M.Map(v.Fill.Across())
	mid := v.M.Map(v.Fill.From).Lerp(across, 0.5)
	x.dragWith(ad, pt2(across), pt2(mid), false, false)
	if asp := d.Node(n.ID).Vector.Fill.Aspect; math.Abs(asp-0.5) > 0.02 {
		t.Errorf("dragging the handle across halfway gave an aspect of %v", asp)
	}
	// A double-click on the line adds a stop, which Delete removes.
	v = d.Node(n.ID).Vector
	at := v.M.Map(v.Fill.From).Lerp(v.M.Map(v.Fill.To), 0.5)
	sx, sy := x.viewPoint(ad, at.X, at.Y)
	x.mx, x.my = sx, sy
	x.frame(Input{})
	x.frame(Input{Down: true, Pointer: []Pointer{{X: sx, Y: sy, Down: true, Double: true}}})
	x.frame(Input{Pointer: []Pointer{{X: sx, Y: sy}}})
	if stops := d.Node(n.ID).Vector.Fill.Stops; len(stops) != 3 || math.Abs(stops[1].At-0.5) > 0.02 || ad.gradStop != 1 {
		t.Fatalf("after the double-click: %+v, chosen %d", stops, ad.gradStop)
	}
	a.setPickerColor(blue)
	x.frame(Input{})
	if c := d.Node(n.ID).Vector.Fill.Stops[1].Color; c != blue {
		t.Errorf("the chosen stop is %v", c)
	}
	x.key(keyDelete, false)
	if stops := d.Node(n.ID).Vector.Fill.Stops; len(stops) != 2 {
		t.Errorf("Delete left %d stops", len(stops))
	}
	x.key(keyDelete, false)
	if stops := d.Node(n.ID).Vector.Fill.Stops; len(stops) != 2 {
		t.Errorf("a gradient keeps two stops: %d", len(stops))
	}
	x.frame(Input{})
	x.shot("canvas-vector-gradient")
	// No paint, from the options bar.
	a.setPaintKind(ad, d.Node(n.ID), paint.PaintNone)
	if d.Node(n.ID).Vector.Fill.Kind != paint.PaintNone {
		t.Error("no paint")
	}
}

// Y fades the chosen layer out with a black and white gradient on its mask, which it is given, as one step; it
// works on vector layers too.
func TestMaskGradientTool(t *testing.T) {
	x, ad := canvasHarness(t)
	d := ad.d
	n := d.AddVector(solidSquare(100, 100, 200, blue), "Shape")
	x.key('Y', false)
	if x.a.artTool != toolMaskGradient {
		t.Fatalf("Y chose %v", x.a.artTool)
	}
	steps, _ := d.History()
	x.dragWith(ad, [2]float64{120, 200}, [2]float64{280, 200}, false, false)
	m := d.Node(n.ID).Mask
	if m == nil || !d.EditMask {
		t.Fatal("the layer should have a mask, being edited")
	}
	if now, _ := d.History(); len(now) != len(steps)+1 || now[len(now)-1] != "Mask gradient" {
		t.Errorf("one step: %v", now)
	}
	if l, r := m.At(110, 200), m.At(290, 200); l != 255 || r != 0 {
		t.Errorf("the mask is %d at the start and %d at the end", l, r)
	}
	ad.changedAll()
	if c := ad.look(290, 200); c != (color.RGBA{255, 255, 255, 255}) {
		t.Errorf("the hidden end shows %v", c)
	}
	x.frame(Input{})
	x.shot("canvas-mask-gradient")
	x.key('Z', true)
	if d.Node(n.ID).Mask != nil {
		t.Error("undo should take the mask away with the gradient")
	}
}
