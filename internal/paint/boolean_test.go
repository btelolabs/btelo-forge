package paint

import (
	"image"
	"testing"
)

// coverageAt draws the object and reads how much of pixel (x, y) it covers.
func coverageAt(v Vector, x, y int) uint8 {
	img := image.NewRGBA(image.Rect(x, y, x+1, y+1))
	v.Render(img, img.Rect, 1)
	return img.Pix[3]
}

func TestBooleans(t *testing.T) {
	rect := func(x, y, w, h float64) Vector {
		return Vector{Kind: VectorRect, Size: Pt{w, h}, M: Translate(x, y), Fill: solidRed}
	}
	circle := func(cx, cy, r float64) Vector {
		return Vector{Kind: VectorEllipse, Size: Pt{2 * r, 2 * r}, M: Translate(cx-r, cy-r), Fill: solidRed}
	}
	a, b := rect(0, 0, 100, 100), rect(50, 50, 100, 100)
	inA := func(x, y float64) bool { return x < 100 && y < 100 }
	inB := func(x, y float64) bool { return x > 50 && y > 50 }
	for _, c := range []struct {
		name string
		op   BoolOp
		want func(x, y float64) bool
	}{
		{"add", BoolAdd, func(x, y float64) bool { return inA(x, y) || inB(x, y) }},
		{"subtract", BoolSubtract, func(x, y float64) bool { return inA(x, y) && !inB(x, y) }},
		{"intersect", BoolIntersect, func(x, y float64) bool { return inA(x, y) && inB(x, y) }},
		{"exclude", BoolExclude, func(x, y float64) bool { return inA(x, y) != inB(x, y) }},
	} {
		res := Vector{Kind: VectorCurve, Path: Combine(c.op, []Vector{a, b}), M: Identity, Fill: solidRed}
		for y := 5; y < 150; y += 10 {
			for x := 5; x < 150; x += 10 {
				got := coverageAt(res, x, y) > 128
				if want := c.want(float64(x)+0.5, float64(y)+0.5); got != want {
					t.Errorf("%s at (%d, %d): covered %v, want %v", c.name, x, y, got, want)
				}
			}
		}
	}
	// Straight edges stay straight: two squares added make an L of six sharp corners.
	l := Combine(BoolAdd, []Vector{a, b})
	if len(l) != 1 || len(l[0].Nodes) != 8 {
		t.Errorf("the added squares have %d rings, %d nodes", len(l), len(l[0].Nodes))
	}
	// Two squares side by side, sharing an edge, become one square of four corners.
	side := Combine(BoolAdd, []Vector{rect(0, 0, 50, 50), rect(50, 0, 50, 50)})
	if len(side) != 1 || len(side[0].Nodes) != 4 {
		t.Errorf("squares sharing an edge: %d rings, %+v", len(side), side)
	}
	// A hole: a small square subtracted from inside a large one leaves a ring.
	hole := Vector{Kind: VectorCurve, Path: Combine(BoolSubtract, []Vector{rect(0, 0, 100, 100), rect(30, 30, 40, 40)}), M: Identity, Fill: solidRed}
	if len(hole.Path) != 2 || coverageAt(hole, 50, 50) != 0 || coverageAt(hole, 10, 10) != 255 {
		t.Errorf("the hole: %d rings, centre %d, edge %d", len(hole.Path), coverageAt(hole, 50, 50), coverageAt(hole, 10, 10))
	}
	// Circles come back as smooth curves of a few nodes that follow the circle.
	two := Combine(BoolAdd, []Vector{circle(100, 100, 50), circle(160, 100, 50)})
	if len(two) != 1 || len(two[0].Nodes) > 16 {
		t.Fatalf("two circles added: %d rings, %+v", len(two), two)
	}
	res := Vector{Kind: VectorCurve, Path: two, M: Identity}
	for _, p := range []Pt{{50, 100}, {100, 50}, {210, 100}, {160, 150}} {
		if _, _, d := res.NearestOnSegment(p); d > 0.2 {
			t.Errorf("the outline misses %v by %.3f", p, d)
		}
	}
	// Objects that do not meet intersect to nothing.
	if got := Combine(BoolIntersect, []Vector{rect(0, 0, 10, 10), rect(20, 20, 10, 10)}); len(got) != 0 {
		t.Errorf("apart, intersected: %+v", got)
	}
}

func TestBooleanLayers(t *testing.T) {
	d := New("Layer 1")
	grad := VectorPaint{Kind: PaintLinear, From: Pt{0, 0}, To: Pt{100, 0}, Stops: []Stop{{At: 0, Color: solidRed.Color}, {At: 1, Color: solidRed.Color}}}
	bottom := d.AddVector(Vector{Kind: VectorRect, Size: Pt{100, 100}, M: Translate(10, 10), Fill: grad, Stroke: VectorStroke{Paint: solidRed, Width: 2}}, "Bottom")
	top := d.AddVector(Vector{Kind: VectorEllipse, Size: Pt{60, 60}, M: Translate(80, 80), Fill: solidRed}, "Top")
	if _, err := d.Boolean(BoolAdd, []int{bottom.ID}); err != ErrBoolean {
		t.Errorf("one layer: %v", err)
	}
	n, err := d.Boolean(BoolSubtract, []int{top.ID, bottom.ID})
	if err != nil {
		t.Fatal(err)
	}
	if n.ID != bottom.ID || d.Node(top.ID) != nil || len(d.Layers()) != 2 || d.Active != bottom.ID {
		t.Fatalf("after the boolean: %d layers, active %d", len(d.Layers()), d.Active)
	}
	v := n.Vector
	if v.Kind != VectorCurve || v.Stroke.Width != 2 || v.Fill.From != (Pt{10, 10}) || v.Fill.To != (Pt{110, 10}) {
		t.Errorf("the result's style %+v", v)
	}
	img := d.Composite(image.Rect(0, 0, 150, 150))
	if img.RGBAAt(50, 50).R < 250 || img.RGBAAt(105, 105).A != 0 {
		t.Errorf("the result covers %v at (50, 50) and %v in the bite", img.RGBAAt(50, 50), img.RGBAAt(105, 105))
	}
	if steps, _ := d.History(); steps[len(steps)-1] != "Boolean" {
		t.Errorf("steps %v", steps)
	}
	d.Undo()
	if d.Node(top.ID) == nil || d.Node(bottom.ID).Vector.Kind != VectorRect {
		t.Error("undo did not bring both back")
	}
}
