package paint

import (
	"image"
	"image/color"
	"testing"
)

// A vector layer's mask is painted and filled like a pixel layer's, and hides the object where it is black.
func TestVectorLayerMask(t *testing.T) {
	d := New("Background")
	d.Paper = false
	d.AddVector(Vector{Kind: VectorRect, Size: Pt{100, 100}, M: Translate(0, 0), Fill: solidRed}, "Shape")
	if err := d.AddMask(); err != nil {
		t.Fatal(err)
	}
	if !d.EditMask {
		t.Fatal("adding a mask edits it")
	}
	// The brush paints the mask black on the left.
	s, _, err := d.BeginStroke(Brush{Size: 20, Hardness: 1, Opacity: 1, Spacing: 0.1, Color: color.NRGBA{A: 255}}, false, 10, 10)
	if err != nil {
		t.Fatalf("painting a vector layer's mask: %v", err)
	}
	s.To(10, 90)
	s.End()
	at := func(x, y int) color.RGBA { return d.Composite(image.Rect(x, y, x+1, y+1)).RGBAAt(x, y) }
	if c := at(10, 50); c.A != 0 {
		t.Errorf("the mask's black should hide the object: %v", c)
	}
	if c := at(60, 50); c != (color.RGBA{255, 0, 0, 255}) {
		t.Errorf("the mask's white shows it: %v", c)
	}
	// A gradient on the mask fades the object across it.
	if err := d.DrawGradient(Gradient{From: [2]float64{0, 0}, To: [2]float64{100, 0}, Start: color.NRGBA{R: 255, G: 255, B: 255, A: 255},
		End: color.NRGBA{A: 255}, Opacity: 1}); err != nil {
		t.Fatalf("a gradient on a vector layer's mask: %v", err)
	}
	if a, b := at(20, 50).A, at(80, 50).A; a <= b || a < 150 || b > 100 {
		t.Errorf("the gradient should fade the object: %d at the left, %d at the right", a, b)
	}
	// Painting the object's pixels is still refused: it has none.
	d.EditMask = false
	if _, _, err := d.BeginStroke(DefaultBrush(), false, 10, 10); err != ErrVectorLayer {
		t.Errorf("painting a vector layer: %v", err)
	}
}

// A mask's work stays within what its layer shows, however far the view or the canvas reaches.
func TestMaskWorkFollowsTheLayer(t *testing.T) {
	d := New("Layer")
	d.View = image.Rect(-20000, -20000, 20000, 20000)
	l := d.Root.Children[0]
	img := image.NewRGBA(image.Rect(0, 0, 50, 50))
	for i := range img.Pix {
		img.Pix[i] = 255
	}
	l.Pixels.Write(img)
	if err := d.AddMask(); err != nil {
		t.Fatal(err)
	}
	if err := d.Fill(color.NRGBA{A: 255}); err != nil {
		t.Fatal(err)
	}
	if got := len(l.Mask.p.tiles); got != 1 {
		t.Errorf("filling the mask wrote %d tiles; the layer has one", got)
	}
	if c := d.Composite(image.Rect(10, 10, 11, 11)).RGBAAt(10, 10); c.A != 0 {
		t.Errorf("the black mask shows %v", c)
	}
	pieces := d.maskArea(l, 0)
	if len(pieces) != 1 || pieces[0] != image.Rect(0, 0, TileSize, TileSize) {
		t.Errorf("the mask's work area %v", pieces)
	}
}

// Compositing reads a layer only where it has anything: a small layer on a huge area costs its own size.
func TestCompositeKeepsToTheLayer(t *testing.T) {
	d := New("Layer")
	d.Paper = false
	img := image.NewRGBA(image.Rect(10, 10, 20, 20))
	for i := range img.Pix {
		img.Pix[i] = 255
	}
	d.Root.Children[0].Pixels.Write(img)
	out := d.Composite(image.Rect(-4000, -4000, 4000, 4000))
	if c := out.RGBAAt(15, 15); c != (color.RGBA{255, 255, 255, 255}) {
		t.Errorf("inside the layer %v", c)
	}
	if c := out.RGBAAt(25, 15); c.A != 0 {
		t.Errorf("beside it %v", c)
	}
}
