package paint

import (
	"image"
	"image/color"
	"testing"
)

func TestCloneStampCopiesFromTheSource(t *testing.T) {
	d := newDoc(t, 100, 60, white)
	d.Select(image.Rect(10, 10, 20, 20))
	d.Fill(red)
	d.Deselect()
	b := Brush{Size: 8, Hardness: 1, Opacity: 1, Flow: 1, Spacing: 0.1, Round: 1, Color: blue}
	// Paint 50 pixels to the right of the square, from the square.
	s, _, err := d.BeginClone(b, -50, 0, false, 65, 15)
	if err != nil {
		t.Fatal(err)
	}
	s.To(66, 15)
	s.End()
	l := d.Node(d.Active)
	if c := l.Pixels.At(65, 15); c != (color.RGBA{255, 0, 0, 255}) {
		t.Errorf("cloned pixel %v", c)
	}
	if c := l.Pixels.At(65, 30); c != (color.RGBA{255, 255, 255, 255}) {
		t.Errorf("beyond the brush %v", c)
	}
	if d.UndoName() != "Clone stamp" {
		t.Errorf("recorded as %q", d.UndoName())
	}
	// From all layers onto an empty one; the source is taken when the stroke begins.
	top := d.AddLayer("Patch")
	s, _, _ = d.BeginClone(b, -50, 0, true, 65, 45)
	s.End()
	if c := d.Node(top.ID).Pixels.At(65, 45); c != (color.RGBA{255, 255, 255, 255}) {
		t.Errorf("all layers: %v", c)
	}
	s, _, _ = d.BeginClone(b, -50, 0, false, 65, 45)
	s.End()
	if c := d.Node(top.ID).Pixels.At(65, 45); c != (color.RGBA{255, 255, 255, 255}) {
		t.Errorf("an empty layer's own pixels cleared it: %v", c)
	}
}
