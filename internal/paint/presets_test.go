package paint

import (
	"image"
	"image/color"
	"testing"
)

// Every preset has a tip that lays something down, a name and an id of its own, and paints a stroke.
func TestPresets(t *testing.T) {
	ps := Presets()
	if len(ps) != 12 {
		t.Fatalf("%d presets", len(ps))
	}
	seen := map[string]bool{}
	for _, p := range ps {
		if p.ID == "" || p.Name == "" || seen[p.ID] {
			t.Errorf("preset %q %q: a missing or repeated id or name", p.ID, p.Name)
		}
		seen[p.ID] = true
		b := p.Brush
		if b.Tip != TipShaped || !b.Shape.valid() || b.normalized() != b {
			t.Errorf("%s: its brush should be a valid picture tip within its ranges", p.ID)
		}
		painted := 0
		for _, v := range Sample(b, 300, 90).Pix {
			if v > 0 {
				painted++
			}
		}
		if painted < 300 {
			t.Errorf("%s: its sample stroke covers only %d pixels", p.ID, painted)
		}
		if q, ok := PresetByID(p.ID); !ok || q.Brush.Shape != b.Shape {
			t.Errorf("%s: not found by its id", p.ID)
		}
		if q, ok := PresetOf(b.Shape); !ok || q.ID != p.ID {
			t.Errorf("%s: not found by its tip", p.ID)
		}
	}
	if _, ok := PresetOf(&TipShape{W: 1, H: 1, Pix: []uint8{255}}); ok {
		t.Error("a tip of the painter's own is no preset's")
	}
}

// paintedAcross is the first and last painted pixel of row y of the doc, from x lo to hi, or -1s.
func paintedAcross(d *Doc, y, lo, hi int) (first, last int) {
	first, last = -1, -1
	for x := lo; x < hi; x++ {
		if d.Composite(image.Rect(x, y, x+1, y+1)).RGBAAt(x, y).A > 0 {
			if first < 0 {
				first = x
			}
			last = x
		}
	}
	return first, last
}

// A tip that follows the stroke turns with the stroke's direction, its own angle added.
func TestTipFollowsTheStroke(t *testing.T) {
	bar := &TipShape{W: 20, H: 2, Pix: make([]uint8, 40)}
	for i := range bar.Pix {
		bar.Pix[i] = 255
	}
	b := Brush{Size: 20, Opacity: 1, Spacing: 0.1, Flow: 1, Round: 1, Tip: TipShaped, Shape: bar, Color: color.NRGBA{A: 255}}
	stroke := func(b Brush) *Doc {
		d := New("Layer")
		d.Paper = false
		s, _, err := d.BeginStroke(b, false, 100, 50)
		if err != nil {
			t.Fatal(err)
		}
		s.To(100, 200) // straight down
		s.End()
		return d
	}
	// Fixed, the bar lies across a stroke going down: 20 wide.
	if lo, hi := paintedAcross(stroke(b), 150, 70, 130); hi-lo < 18 {
		t.Errorf("a fixed bar paints %d..%d across the stroke", lo, hi)
	}
	// Following, it turns a quarter with the stroke, lying along it: 2 wide.
	b.Follow = true
	if lo, hi := paintedAcross(stroke(b), 150, 70, 130); lo < 0 || hi-lo > 4 {
		t.Errorf("a following bar paints %d..%d across the stroke", lo, hi)
	}
	// And at 90° more it stands across it again.
	b.Angle = 90
	if lo, hi := paintedAcross(stroke(b), 150, 70, 130); hi-lo < 18 {
		t.Errorf("a following bar at 90° paints %d..%d across the stroke", lo, hi)
	}
}

// Scatter throws dabs off the line, size jitter makes them smaller, and both are the same each time a stroke is
// painted from the same place.
func TestShapeDynamics(t *testing.T) {
	b := Brush{Size: 10, Hardness: 1, Opacity: 1, Spacing: 0.25, Flow: 1, Round: 1, Color: color.NRGBA{A: 255}}
	// stroke paints b from left to right along y = 100, and is what it covers and how many pixels.
	stroke := func(b Brush) (image.Rectangle, int) {
		d := New("Layer")
		d.Paper = false
		s, _, err := d.BeginStroke(b, false, 50, 100)
		if err != nil {
			t.Fatal(err)
		}
		s.To(250, 100)
		s.End()
		img := d.Composite(image.Rect(0, 0, 300, 200))
		var box image.Rectangle
		n := 0
		for y := range 200 {
			for x := range 300 {
				if img.RGBAAt(x, y).A > 0 {
					box = box.Union(image.Rect(x, y, x+1, y+1))
					n++
				}
			}
		}
		return box, n
	}
	box, plain := stroke(b)
	if box.Min.Y < 94 || box.Max.Y > 106 {
		t.Fatalf("an unscattered stroke covers %v", box)
	}
	s := b
	s.Scatter = 2
	box, _ = stroke(s)
	// Thrown up to twice the size from the line, a radius more.
	if box.Min.Y > 88 || box.Max.Y < 112 || box.Min.Y < 74 || box.Max.Y > 126 {
		t.Errorf("scattered dabs cover %v", box)
	}
	j := b
	j.SizeJitter = 1
	_, small := stroke(j)
	if small >= plain {
		t.Errorf("size jitter covers %d pixels, no fewer than %d", small, plain)
	}
	if _, again := stroke(j); again != small {
		t.Errorf("the same stroke jittered differently: %d and %d pixels", small, again)
	}
	// The settings are kept within their ranges.
	n := Brush{SizeJitter: 2, AngleJitter: -1, Scatter: 10}.normalized()
	if n.SizeJitter != 1 || n.AngleJitter != 0 || n.Scatter != maxScatter {
		t.Errorf("normalized dynamics %v %v %v", n.SizeJitter, n.AngleJitter, n.Scatter)
	}
}
