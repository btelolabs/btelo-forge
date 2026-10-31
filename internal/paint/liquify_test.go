package paint

import (
	"image"
	"image/color"
	"testing"
)

func liquify(t *testing.T, d *Doc, mode LiquifyMode, strength float64, pts ...[2]float64) {
	t.Helper()
	b := Brush{Size: 60, Hardness: 0.5, Flow: 1, Opacity: 1, Spacing: 0.1}
	s, _, err := d.BeginRetouch(b, Retouch{Kind: RetouchLiquify, Liquify: mode, Strength: strength}, pts[0][0], pts[0][1])
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range pts[1:] {
		s.To(p[0], p[1])
	}
	for range 40 {
		s.Hold()
	}
	s.End()
}

// barDoc is white with a black upright bar 20 wide in the middle.
func barDoc(t *testing.T) *Doc {
	t.Helper()
	d := newDoc(t, 200, 120, color.NRGBA{R: 255, G: 255, B: 255, A: 255})
	d.Select(image.Rect(90, 0, 110, 120))
	if err := d.Fill(color.NRGBA{A: 255}); err != nil {
		t.Fatal(err)
	}
	d.Deselect()
	return d
}

func TestForwardPushesThePicture(t *testing.T) {
	d := barDoc(t)
	liquify(t, d, LiquifyForward, 1, [2]float64{100, 60}, [2]float64{140, 60})
	l := d.Node(d.Active).Pixels
	// The bar's middle goes with the brush, and what it left is what lay behind it.
	if c := l.At(136, 60); c.R != 0 {
		t.Errorf("the bar did not come along: %v", c)
	}
	if c := l.At(100, 60); c.R != 255 {
		t.Errorf("the bar stayed where it was: %v", c)
	}
	if c := l.At(100, 10); c.R != 0 {
		t.Errorf("the bar moved far from the stroke: %v", c)
	}
	if d.UndoName() != "Liquify" {
		t.Errorf("step %q", d.UndoName())
	}
	d.Undo()
	if c := d.Node(d.Active).Pixels.At(120, 60); c.R != 255 {
		t.Errorf("undo left %v", c)
	}
}

func TestLiquifyStaysSharpHoweverOftenItIsPushed(t *testing.T) {
	d := barDoc(t)
	pts := [][2]float64{{100, 60}}
	for i := range 20 {
		pts = append(pts, [2]float64{100 + float64(i%2)*30 + 1, 60})
	}
	liquify(t, d, LiquifyForward, 0.5, pts...)
	// Grey comes of resampling again and again: from the stroke's start, an edge stays a pixel or two wide.
	l := d.Node(d.Active).Pixels
	soft := 0
	for x := 60; x < 170; x++ {
		if c := l.At(x, 60); c.R > 40 && c.R < 215 {
			soft++
		}
	}
	if soft > 14 {
		t.Errorf("%d grey pixels along the row", soft)
	}
}

func TestPuckerAndBloat(t *testing.T) {
	d := barDoc(t)
	liquify(t, d, LiquifyBloat, 1, [2]float64{100, 60})
	if c := d.Node(d.Active).Pixels.At(114, 60); c.R > 60 {
		t.Errorf("bloat left the bar's side at %v", c)
	}
	d = barDoc(t)
	liquify(t, d, LiquifyPucker, 1, [2]float64{100, 60})
	if c := d.Node(d.Active).Pixels.At(106, 60); c.R < 200 {
		t.Errorf("pucker left the bar's side at %v", c)
	}
}

func TestLiquifyKeepsToTheSelectionAndThePicture(t *testing.T) {
	d := barDoc(t)
	d.Select(image.Rect(0, 0, 200, 40))
	liquify(t, d, LiquifyForward, 1, [2]float64{100, 60}, [2]float64{140, 60})
	if c := d.Node(d.Active).Pixels.At(120, 60); c.R != 255 {
		t.Errorf("it changed what is not selected: %v", c)
	}
	d.Deselect()
	// Dragged in from the edge, the picture's edge goes on: nothing transparent comes in.
	d = barDoc(t)
	liquify(t, d, LiquifyForward, 1, [2]float64{5, 60}, [2]float64{60, 60})
	if c := d.Node(d.Active).Pixels.At(10, 60); c.A != 255 {
		t.Errorf("transparency came in: %v", c)
	}
}
