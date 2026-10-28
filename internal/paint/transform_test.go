package paint

import (
	"errors"
	"image"
	"image/color"
	"testing"
)

var (
	blue = color.NRGBA{B: 255, A: 255}
	none = color.RGBA{}
)

// marked is a 40×30 picture on white with a layer holding a red block at (2,3)-(12,8) and a blue pixel at (2,3),
// so every mirror and turn can be told apart.
func marked(t *testing.T) (*Doc, *Node) {
	t.Helper()
	d := newDoc(t, 40, 30, color.NRGBA{R: 255, G: 255, B: 255, A: 255})
	l := d.AddLayer("Ink")
	d.Select(image.Rect(2, 3, 12, 8))
	d.Fill(red)
	d.Select(image.Rect(2, 3, 3, 4))
	d.Fill(blue)
	d.Deselect()
	return d, l
}

func layerAt(l *Node, x, y int) color.RGBA {
	if !(image.Point{x, y}).In(l.Pixels.Bounds()) {
		return none
	}
	return l.Pixels.At(x, y)
}

func TestFlipAndRotateLayerAreExact(t *testing.T) {
	d, l := marked(t)
	if err := d.FlipLayer(true); err != nil {
		t.Fatal(err)
	}
	l = d.Node(l.ID)
	if layerAt(l, 11, 3) != (color.RGBA{0, 0, 255, 255}) || layerAt(l, 2, 3) != (color.RGBA{255, 0, 0, 255}) {
		t.Errorf("flipped across: %v %v", layerAt(l, 11, 3), layerAt(l, 2, 3))
	}
	if b := l.Pixels.Bounds(); b != image.Rect(2, 3, 12, 8) {
		t.Errorf("flipping moved the block to %v", b)
	}
	d.Undo()
	l = d.Node(l.ID)
	if layerAt(l, 2, 3) != (color.RGBA{0, 0, 255, 255}) {
		t.Errorf("undo left %v", layerAt(l, 2, 3))
	}
	// 10×5 turned clockwise about its middle (7, 5.5): 5×10, with the blue top-left corner now top right.
	if err := d.RotateLayer(1); err != nil {
		t.Fatal(err)
	}
	l = d.Node(l.ID)
	b := l.Pixels.Bounds()
	if b.Dx() != 5 || b.Dy() != 10 {
		t.Fatalf("turned block is %v", b)
	}
	if c := layerAt(l, b.Max.X-1, b.Min.Y); c != (color.RGBA{0, 0, 255, 255}) {
		t.Errorf("turned corner %v", c)
	}
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if layerAt(l, x, y).A != 255 {
				t.Fatalf("a quarter turn left a soft pixel at %d,%d: %v", x, y, layerAt(l, x, y))
			}
		}
	}
	// Four quarter turns of a block whose sides add up to an even number come back exactly to the start; an odd one
	// moves by half a pixel a turn, as it must to stay on whole pixels.
	d2 := newDoc(t, 40, 30, color.NRGBA{})
	l2 := d2.Node(d2.Active)
	d2.Select(image.Rect(2, 3, 12, 9))
	d2.Fill(red)
	d2.Select(image.Rect(2, 3, 3, 4))
	d2.Fill(blue)
	d2.Deselect()
	for range 4 {
		if err := d2.RotateLayer(1); err != nil {
			t.Fatal(err)
		}
	}
	l2 = d2.Node(l2.ID)
	if b := l2.Pixels.Bounds(); b != image.Rect(2, 3, 12, 9) || layerAt(l2, 2, 3) != (color.RGBA{0, 0, 255, 255}) {
		t.Errorf("after a full turn the block is %v with %v", b, layerAt(l2, 2, 3))
	}
}

func TestFreeTransformScalesTheSelectionOnly(t *testing.T) {
	d, l := marked(t)
	d.Select(image.Rect(2, 3, 7, 8)) // the left half of the block
	tr, err := d.BeginTransform()
	if err != nil {
		t.Fatal(err)
	}
	if tr.Bounds() != image.Rect(2, 3, 7, 8) {
		t.Fatalf("transform bounds %v", tr.Bounds())
	}
	// Twice as wide and tall, from the top-left corner, dragged roughly first and then applied.
	tr.Set(Scale(2, 2, 2, 3), true)
	tr.Apply()
	l = d.Node(l.ID)
	if c := layerAt(l, 11, 12); c.R < 250 || c.A < 250 {
		t.Errorf("the scaled half does not reach 11,12: %v", c)
	}
	if c := layerAt(l, 9, 5); c.R < 250 {
		t.Errorf("the unselected half was lost: %v", c)
	}
	if b := d.SelectionBounds(); b != image.Rect(2, 3, 12, 13) {
		t.Errorf("the selection follows the transform to %v", b)
	}
	if d.UndoName() != "Free transform" {
		t.Errorf("recorded as %q", d.UndoName())
	}
	d.Undo()
	l = d.Node(l.ID)
	if layerAt(l, 11, 12) != none || d.SelectionBounds() != image.Rect(2, 3, 7, 8) {
		t.Errorf("undo left %v and selection %v", layerAt(l, 11, 12), d.SelectionBounds())
	}
	// Cancelling records nothing.
	steps := len(d.history.done)
	tr, _ = d.BeginTransform()
	tr.Set(Rotate(30, 5, 5), true)
	tr.Cancel()
	if len(d.history.done) != steps || layerAt(d.Node(l.ID), 2, 3) != (color.RGBA{0, 0, 255, 255}) {
		t.Error("cancelling a transform changed the picture")
	}
	// An empty layer has nothing to transform.
	d.Deselect()
	d.AddLayer("Empty")
	if _, err := d.BeginTransform(); !errors.Is(err, ErrEmptyLayer) {
		t.Errorf("transforming an empty layer: %v", err)
	}
}

func TestCanvasTurnsFlipsAndSizes(t *testing.T) {
	d, l := marked(t)
	d.Select(image.Rect(0, 0, 5, 5))
	// A quarter turn clockwise about the frame's middle (20, 15): the frame turns with the picture.
	d.RotateCanvas(1)
	l = d.Node(l.ID)
	if d.Frame != image.Rect(5, -5, 35, 35) {
		t.Fatalf("turned frame %v", d.Frame)
	}
	// (2,3) is 26 across and 2 down the turned frame, as in a 30×40 picture.
	if layerAt(l, d.Frame.Min.X+26, d.Frame.Min.Y+2) != (color.RGBA{0, 0, 255, 255}) {
		t.Errorf("turned blue pixel: %v", layerAt(l, d.Frame.Min.X+26, d.Frame.Min.Y+2))
	}
	if b := d.SelectionBounds(); b != image.Rect(25, 0, 30, 5).Add(d.Frame.Min) {
		t.Errorf("turned selection %v", b)
	}
	d.Undo()
	if d.Frame != image.Rect(0, 0, 40, 30) || layerAt(d.Node(l.ID), 2, 3) != (color.RGBA{0, 0, 255, 255}) {
		t.Errorf("undo gives %v", d.Frame)
	}
	d.Deselect()
	d.FlipCanvas(false)
	if layerAt(d.Node(l.ID), 2, 26) != (color.RGBA{0, 0, 255, 255}) {
		t.Errorf("flipped upside down: %v", layerAt(d.Node(l.ID), 2, 26))
	}
	d.Undo()
	// Image size scales everything, the frame exactly.
	if err := d.ResizeImage(80, 60); err != nil {
		t.Fatal(err)
	}
	if d.Frame != image.Rect(0, 0, 80, 60) {
		t.Errorf("scaled frame %v", d.Frame)
	}
	if c := d.Composite(image.Rect(0, 0, 80, 60)).RGBAAt(12, 12); c.R < 250 || c.G > 5 {
		t.Errorf("scaled block: %v", c)
	}
	if err := d.ResizeImage(0, 10); !errors.Is(err, ErrTooBig) {
		t.Errorf("a zero size: %v", err)
	}
	d.Undo()
	// Canvas size, anchored in the middle, grows the frame by 10 each side; nothing moves on the canvas.
	if err := d.ResizeCanvas(60, 50, 0.5, 0.5); err != nil {
		t.Fatal(err)
	}
	if d.Frame != image.Rect(-10, -10, 50, 40) || layerAt(d.Node(l.ID), 2, 3) != (color.RGBA{0, 0, 255, 255}) {
		t.Errorf("canvas size: frame %v, %v", d.Frame, layerAt(d.Node(l.ID), 2, 3))
	}
	if c := d.Composite(image.Rect(-10, -10, 50, 40)).RGBAAt(-5, -5); c.A != 0 {
		t.Errorf("the added canvas is %v", c)
	}
	d.Undo()
	// Arbitrary rotation grows the frame to hold the corners.
	if err := d.RotateCanvasBy(45); err != nil {
		t.Fatal(err)
	}
	if d.Frame.Dx() < 49 || d.Frame.Dx() > 51 || d.Frame.Dy() < 49 || d.Frame.Dy() > 51 {
		t.Errorf("a 45° turn of 40×30 is %v", d.Frame)
	}
	d.Undo()
}

func TestCropAndRevealAll(t *testing.T) {
	d, l := marked(t)
	d.Select(image.Rect(1, 1, 20, 20))
	if err := d.Crop(image.Rect(2, 3, 12, 8), true); err != nil {
		t.Fatal(err)
	}
	l = d.Node(l.ID)
	if d.Frame != image.Rect(2, 3, 12, 8) || l.Pixels.Bounds() != image.Rect(2, 3, 12, 8) {
		t.Fatalf("cropped to %v, layer %v", d.Frame, l.Pixels.Bounds())
	}
	if layerAt(l, 2, 3) != (color.RGBA{0, 0, 255, 255}) {
		t.Errorf("cropped corner %v", layerAt(l, 2, 3))
	}
	if p, err := d.Picture(); err != nil || p.Rect != d.Frame || p.RGBAAt(2, 3) != (color.RGBA{0, 0, 255, 255}) {
		t.Errorf("the exported picture: %v %v", err, p.Rect)
	}
	d.Undo()
	// Without deleting, what is past the frame stays on the canvas and Reveal All brings the frame round it.
	d.Deselect()
	if err := d.Crop(image.Rect(5, 5, 15, 15), false); err != nil {
		t.Fatal(err)
	}
	if layerAt(d.Node(l.ID), 2, 3) != (color.RGBA{0, 0, 255, 255}) {
		t.Errorf("the cropped-off pixel was not kept: %v", layerAt(d.Node(l.ID), 2, 3))
	}
	if err := d.RevealAll(); err != nil {
		t.Fatal(err)
	}
	if d.Frame != image.Rect(0, 0, 40, 30) {
		t.Errorf("reveal all gives %v", d.Frame)
	}
	if err := d.Crop(image.Rectangle{}, true); !errors.Is(err, ErrTooBig) {
		t.Errorf("an empty crop: %v", err)
	}
	// Positions do not move, so a selection from before a crop still fits after it.
	d.Select(image.Rect(0, 0, 4, 4))
	d.Deselect()
	_ = d.Crop(image.Rect(0, 0, 20, 20), true)
	d.Reselect()
	if d.SelectionBounds() != image.Rect(0, 0, 4, 4) {
		t.Errorf("reselect after a crop gives %v", d.SelectionBounds())
	}
}

func TestMaskFollowsTransforms(t *testing.T) {
	d, _ := marked(t)
	if err := d.AddMask(); err != nil {
		t.Fatal(err)
	}
	d.EditMask = true
	d.Select(image.Rect(2, 3, 4, 8))
	d.Fill(color.NRGBA{A: 255}) // hide the left two columns
	d.Deselect()
	d.EditMask = false
	if err := d.FlipLayer(true); err != nil {
		t.Fatal(err)
	}
	c := d.Composite(image.Rect(0, 0, 40, 30))
	if v := c.RGBAAt(11, 5); v.G != 255 { // hidden: white shows through
		t.Errorf("the mask did not flip with the layer: %v", v)
	}
	if v := c.RGBAAt(3, 5); v != (color.RGBA{255, 0, 0, 255}) {
		t.Errorf("the flipped layer is hidden at 3,5: %v", v)
	}
}
