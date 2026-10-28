package ui

import (
	"image"
	"image/color"
	"testing"

	"github.com/btelolabs/btelo-forge/internal/paint"
)

func solidSquare(x, y, side float64, c color.NRGBA) paint.Vector {
	return paint.Vector{Kind: paint.VectorRect, Size: paint.Pt{X: side, Y: side}, M: paint.Translate(x, y), Fill: solidPaint(c)}
}

var (
	red  = color.NRGBA{R: 255, A: 255}
	blue = color.NRGBA{B: 255, A: 255}
)

// Copied vectors paste into a vector layer as a shape that combines with it, and into a pixel layer as its vector
// mask; cutting takes the layer, or with the node tool only the chosen shape.
func TestCanvasVectorClipboard(t *testing.T) {
	x, ad := canvasHarness(t)
	a, d := x.a, ad.d
	bg := d.Root.Children[0]
	base := d.AddVector(solidSquare(50, 50, 100, red), "Base")
	other := d.AddVector(solidSquare(100, 100, 100, blue), "Other")
	ad.changedAll()

	// Copy the other square and paste it, subtracted, into the base.
	x.key('C', true)
	if a.vecClip == nil {
		t.Fatal("copying a vector layer keeps its object")
	}
	if x.h.clipImage == nil {
		t.Error("the copied shape's picture goes on the system clipboard too")
	}
	d.Choose(base.ID)
	a.artPaste(ad, paint.OpSubtract, false)
	v := d.Node(base.ID).Vector
	if !v.Compound() || v.ShapeOpOf(1) != paint.OpSubtract || d.UndoName() != "Subtract shape" {
		t.Fatalf("pasting into a vector layer: %d shapes, %q", len(v.Shapes()), d.UndoName())
	}
	d.SetVisible(other.ID, false)
	ad.changedAll()
	if c := ad.look(75, 75); c != (color.RGBA{255, 0, 0, 255}) {
		t.Errorf("outside the subtracted square %v", c)
	}
	if c := ad.look(125, 125); c != (color.RGBA{255, 255, 255, 255}) {
		t.Errorf("the subtracted square shows the white under it: %v", c)
	}
	// The pasted shape's nodes are chosen: the node tool's buttons change how it combines.
	x.key('A', false)
	if got := ad.chosenShape(v); got != 1 {
		t.Fatalf("the pasted shape's nodes are not chosen: %d", got)
	}
	a.artRun(ad, func() error { return d.SetShapeOp(base.ID, 1, paint.OpIntersect) })
	if c := ad.look(75, 75); c != (color.RGBA{255, 255, 255, 255}) {
		t.Errorf("intersected, outside the overlap shows nothing of the shape: %v", c)
	}
	// Cutting the chosen shape takes it out of the object only.
	x.key('X', true)
	if v := d.Node(base.ID).Vector; v.Compound() || d.UndoName() != "Cut" {
		t.Fatalf("cutting the shape: compound %v, %q", v.Compound(), d.UndoName())
	}
	// Pasted onto the background's pixels, it becomes their vector mask.
	d.Choose(bg.ID)
	x.key('V', true)
	if n := d.Node(bg.ID); n.Clip == nil || d.UndoName() != "Add vector mask" {
		t.Fatalf("pasting onto a pixel layer: %q", d.UndoName())
	}
	ad.changedAll()
	if c := ad.look(20, 20); c.A != 0 {
		t.Errorf("outside the vector mask the background shows %v", c)
	}
	if c := ad.look(160, 160); c != (color.RGBA{255, 255, 255, 255}) {
		t.Errorf("inside the vector mask %v", c)
	}
	// Ctrl+Shift+V pastes on a layer of its own.
	layers := len(d.Layers())
	x.frame(Input{Keys: []Key{{Code: 'V', Ctrl: true, Shift: true}}})
	if len(d.Layers()) != layers+1 || d.Node(d.Active).Vector == nil {
		t.Error("Ctrl+Shift+V should paste on a new vector layer")
	}
	// Cutting a whole vector layer takes the layer.
	x.key('V', false)
	d.Choose(other.ID)
	layers = len(d.Layers())
	x.key('X', true)
	if len(d.Layers()) != layers-1 || d.Node(other.ID) != nil {
		t.Error("cutting a vector layer should take it")
	}
	// A picture copied since takes over the clipboard.
	x.h.pasteImage = image.NewRGBA(image.Rect(0, 0, 8, 8))
	a.vecClip.system = true
	layers = len(d.Layers())
	x.key('V', true)
	if n := d.Node(d.Active); len(d.Layers()) != layers+1 || n.Pixels == nil {
		t.Error("a picture copied after the shape should paste as a picture")
	}
}

// Painting a vector layer is refused with a button that rasterizes it.
func TestPaintingAVectorLayerOffersToRasterizeIt(t *testing.T) {
	x, ad := canvasHarness(t)
	d := ad.d
	n := d.AddVector(solidSquare(50, 50, 100, red), "Shape")
	x.key('B', false)
	fx, fy := x.viewPoint(ad, 60, 60)
	x.drag(fx, fy, fx+20, fy)
	if len(x.a.toasts) == 0 || len(x.a.toasts[len(x.a.toasts)-1].actions) != 1 {
		t.Fatalf("toasts %+v", x.a.toasts)
	}
	x.a.toasts[len(x.a.toasts)-1].actions[0].run()
	if got := d.Node(n.ID); got.Vector != nil || got.Pixels == nil {
		t.Fatal("the toast's button should rasterize the layer")
	}
	x.a.brush.Color = blue
	x.drag(fx, fy, fx+20, fy)
	if c := ad.look(70, 60); c != (color.RGBA{0, 0, 255, 255}) {
		t.Errorf("the brush did not paint the rasterized layer: %v", c)
	}
}
