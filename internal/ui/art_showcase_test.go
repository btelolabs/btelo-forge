package ui

import (
	"image"
	"image/color"
	"testing"

	"github.com/btelolabs/btelo-forge/internal/paint"
)

// TestDrawingShowcase draws a canvas with the drawing's newer parts together, for a look at them: two artboards side
// by side with the pasteboard dimmed, a compound shape with an elliptical gradient and its handles, a photo-like
// layer shown through a vector mask, and the layers panel with the boards, the mask and the vector mask.
func TestDrawingShowcase(t *testing.T) {
	x, ad := canvasHarness(t)
	a, d := x.a, ad.d
	d.Frame = image.Rectangle{}
	x.key('J', false)
	if _, err := d.AddBoard(image.Rect(0, 0, 400, 300), "Poster"); err != nil {
		t.Fatal(err)
	}
	d.AddLayer("Photo")
	d.Select(image.Rect(20, 20, 380, 280))
	d.Fill(color.NRGBA{R: 40, G: 150, B: 120, A: 255})
	d.Deselect()
	photo := d.Node(d.Active)
	d.AddShape(photo.ID, paint.Vector{Kind: paint.VectorEllipse, Size: paint.Pt{X: 300, Y: 200}, M: paint.Translate(50, 50)}, paint.OpAdd)
	if _, err := d.AddBoard(image.Rect(440, 0, 840, 300), "Card"); err != nil {
		t.Fatal(err)
	}
	v := paint.Vector{Kind: paint.VectorRect, Size: paint.Pt{X: 260, Y: 180}, Radius: 24, M: paint.Translate(510, 60),
		Fill: paint.VectorPaint{Kind: paint.PaintRadial, From: paint.Pt{X: 130, Y: 90}, To: paint.Pt{X: 260, Y: 90}, Aspect: 0.6,
			Stops: []paint.Stop{{At: 0, Color: color.NRGBA{R: 255, G: 190, B: 60, A: 255}}, {At: 1, Color: color.NRGBA{R: 200, G: 40, B: 90, A: 255}}}},
		Stroke: paint.VectorStroke{Paint: solidPaint(color.NRGBA{R: 30, G: 20, B: 60, A: 255}), Width: 6}}
	v = v.WithShape(paint.Vector{Kind: paint.VectorEllipse, Size: paint.Pt{X: 80, Y: 80}, M: paint.Translate(600, 110)}, paint.OpSubtract)
	shape := d.AddVector(v, "Badge")
	d.AddMask()
	d.MaskGradient(paint.Gradient{From: [2]float64{700, 0}, To: [2]float64{800, 0}})
	d.EditMask = false
	d.Choose(shape.ID)
	ad.zoom, ad.panX, ad.panY, ad.fit = 1.1, 420, 150, false
	x.key('G', false)
	ad.changedAll()
	x.frame(Input{})
	x.frame(Input{})
	x.shot("canvas-showcase")
	if c := a.pickerColor(); c != (color.NRGBA{R: 255, G: 190, B: 60, A: 255}) {
		t.Errorf("the colour panel shows %v, not the gradient's first stop", c)
	}
}
