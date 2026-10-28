package paint

import (
	"image"
	"image/color"
	"math"
	"testing"
)

func TestBucketFillsTheConnectedArea(t *testing.T) {
	d := newDoc(t, 40, 20, white)
	// A black wall down the middle splits the picture in two.
	d.Select(image.Rect(19, 0, 21, 20))
	d.Fill(color.NRGBA{A: 255})
	d.Deselect()
	if err := d.Bucket(5, 5, red, 1, 32, true, false); err != nil {
		t.Fatal(err)
	}
	c := d.Composite(image.Rect(0, 0, 40, 20))
	if c.RGBAAt(2, 2) != (color.RGBA{255, 0, 0, 255}) || c.RGBAAt(30, 2) != (color.RGBA{255, 255, 255, 255}) {
		t.Errorf("contiguous: left %v, right %v", c.RGBAAt(2, 2), c.RGBAAt(30, 2))
	}
	if c.RGBAAt(19, 5) != (color.RGBA{0, 0, 0, 255}) {
		t.Errorf("the wall was filled: %v", c.RGBAAt(19, 5))
	}
	if d.UndoName() != "Paint bucket" {
		t.Errorf("recorded as %q", d.UndoName())
	}
	d.Undo()
	// Not contiguous: both sides; at half opacity, half way to red.
	if err := d.Bucket(5, 5, red, 0.5, 32, false, false); err != nil {
		t.Fatal(err)
	}
	c = d.Composite(image.Rect(0, 0, 40, 20))
	if v := c.RGBAAt(30, 2); v.R != 255 || !near(v.G, 128, 2) {
		t.Errorf("half opacity elsewhere: %v", v)
	}
	// Within a selection only.
	d.Undo()
	d.Select(image.Rect(0, 0, 10, 20))
	d.Bucket(5, 5, red, 1, 32, true, false)
	c = d.Composite(image.Rect(0, 0, 40, 20))
	if c.RGBAAt(15, 5) != (color.RGBA{255, 255, 255, 255}) || c.RGBAAt(5, 5) != (color.RGBA{255, 0, 0, 255}) {
		t.Errorf("selection: %v %v", c.RGBAAt(15, 5), c.RGBAAt(5, 5))
	}
	// On an empty layer, sampling all layers fills by what shows.
	d.Deselect()
	d.Undo()
	l := d.AddLayer("Paint")
	if err := d.Bucket(5, 5, blue, 1, 32, true, true); err != nil {
		t.Fatal(err)
	}
	if v := d.Node(l.ID).Pixels.At(30, 5); v.A != 0 {
		t.Errorf("all layers: the other side was filled: %v", v)
	}
	if v := d.Node(l.ID).Pixels.At(5, 5); v != (color.RGBA{0, 0, 255, 255}) {
		t.Errorf("all layers: %v", v)
	}
}

func TestGradientShapes(t *testing.T) {
	black, wht := color.NRGBA{A: 255}, color.NRGBA{R: 255, G: 255, B: 255, A: 255}
	at := func(g Gradient, x, y int) color.RGBA {
		d := newDoc(t, 101, 101, color.NRGBA{})
		g.Start, g.End, g.Opacity = black, wht, 1
		if err := d.DrawGradient(g); err != nil {
			t.Fatal(err)
		}
		return d.Composite(image.Rect(0, 0, 101, 101)).RGBAAt(x, y)
	}
	line := Gradient{From: [2]float64{0, 50}, To: [2]float64{100, 50}}
	if v := at(line, 0, 10); v.R > 2 {
		t.Errorf("linear start %v", v)
	}
	if v := at(line, 50, 90); !near(v.R, 128, 2) {
		t.Errorf("linear middle %v", v)
	}
	if v := at(line, 100, 10); v.R < 253 {
		t.Errorf("linear end %v", v)
	}
	radial := Gradient{Kind: GradientRadial, From: [2]float64{50.5, 50.5}, To: [2]float64{100.5, 50.5}}
	if v, w := at(radial, 50, 25), at(radial, 75, 50); !near(v.R, w.R, 2) || !near(v.R, 128, 3) {
		t.Errorf("radial is not round: %v %v", v, w)
	}
	reflected := Gradient{Kind: GradientReflected, From: [2]float64{50.5, 50.5}, To: [2]float64{100.5, 50.5}}
	if v, w := at(reflected, 25, 10), at(reflected, 75, 10); !near(v.R, w.R, 2) {
		t.Errorf("reflected is not mirrored: %v %v", v, w)
	}
	angle := Gradient{Kind: GradientAngle, From: [2]float64{50.5, 50.5}, To: [2]float64{100.5, 50.5}}
	if v := at(angle, 50, 90); !near(v.R, 64, 3) { // a quarter turn clockwise from the line
		t.Errorf("angle a quarter round %v", v)
	}
	diamond := Gradient{Kind: GradientDiamond, From: [2]float64{50.5, 50.5}, To: [2]float64{100.5, 50.5}}
	if v, w := at(diamond, 75, 50), at(diamond, 63, 62); !near(v.R, w.R, 4) {
		t.Errorf("diamond: along the line %v, along the edge %v", v, w)
	}
	if v := at(Gradient{From: line.From, To: line.To, Reverse: true}, 0, 10); v.R < 253 {
		t.Errorf("reversed start %v", v)
	}
	// A fade to transparency keeps its colour as it fades.
	d := newDoc(t, 101, 10, color.NRGBA{})
	d.DrawGradient(Gradient{From: [2]float64{0, 5}, To: [2]float64{100, 5}, Start: red, End: color.NRGBA{R: 255}, Opacity: 1})
	p := d.Node(d.Active).Pixels.At(50, 5)
	if !near(p.A, 128, 2) || p.R != p.A || p.G != 0 {
		t.Errorf("fading red: %v", p)
	}
	// Dither keeps the average while breaking up bands.
	d = newDoc(t, 64, 64, color.NRGBA{})
	d.DrawGradient(Gradient{From: [2]float64{0, 0}, To: [2]float64{6400, 0}, Start: black, End: wht, Opacity: 1, Dither: true})
	sum, seen := 0.0, map[uint8]bool{}
	for y := range 64 {
		for x := range 64 {
			v := d.Node(d.Active).Pixels.At(x, y).R
			sum += float64(v)
			seen[v] = true
		}
	}
	if mean := sum / 4096; math.Abs(mean-1.6) > 0.5 || len(seen) < 2 {
		t.Errorf("dithered gradient mean %.2f with %d levels", mean, len(seen))
	}
	// A click without a drag draws nothing.
	steps := len(d.history.done)
	d.DrawGradient(Gradient{From: [2]float64{5, 5}, To: [2]float64{5, 5}, Start: black, End: wht, Opacity: 1})
	if len(d.history.done) != steps {
		t.Error("a gradient without a line was drawn")
	}
}
