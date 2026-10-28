package paint

import (
	"image"
	"math"
	"testing"
)

// A round tip swept along a line covers a stadium: the line's length times the tip's width, and a disc for its
// ends.
func TestSweepARoundTip(t *testing.T) {
	tip := TipOutline(Brush{Size: 20, Round: 1, Tip: TipRound, Hardness: 1, Spacing: 0.1})
	if len(tip) < 8 {
		t.Fatalf("a round tip of %d points", len(tip))
	}
	ring := Sweep([]Pt{{50, 50}, {100, 50}, {150, 50}}, tip)
	v := Vector{Kind: VectorCurve, M: Identity, Path: []Subpath{polyline(true, ring...)}, Fill: solidRed}
	a := coveredArea(rendered(v, image.Rect(0, 0, 200, 100)))
	want := 100*20 + math.Pi*100
	if math.Abs(a-want)/want > 0.04 {
		t.Errorf("the stroke covers %.0f, want about %.0f", a, want)
	}
}

// A flat tip swept across its width paints wide, along it narrow, as a calligraphy pen does.
func TestSweepAFlatTip(t *testing.T) {
	tip := TipOutline(Brush{Size: 40, Round: 0.25, Tip: TipSquare, Hardness: 1, Spacing: 0.1})
	across := Sweep([]Pt{{50, 20}, {50, 120}}, tip) // down: across the tip's 40 px width
	along := Sweep([]Pt{{20, 50}, {120, 50}}, tip)  // sideways: along it, 10 px tall
	area := func(ring []Pt) float64 {
		v := Vector{Kind: VectorCurve, M: Identity, Path: []Subpath{polyline(true, ring...)}, Fill: solidRed}
		return coveredArea(rendered(v, image.Rect(0, 0, 200, 200)))
	}
	if a, b := area(across), area(along); a < 3*b {
		t.Errorf("across %.0f, along %.0f: a flat pen should paint much wider across", a, b)
	}
}

// Many strokes added to one layer are covered together, as each would be on its own.
func TestManyStrokesCoverTogether(t *testing.T) {
	tip := TipOutline(Brush{Size: 10, Round: 1, Tip: TipRound, Hardness: 1, Spacing: 0.1})
	v := Vector{Kind: VectorCurve, M: Identity, Fill: solidRed}
	for i := range 40 {
		y := float64(10 + i*4)
		s := Vector{Kind: VectorCurve, M: Identity, Path: []Subpath{polyline(true, Sweep([]Pt{{10, y}, {190, y + 3}}, tip)...)}}
		if i == 0 {
			v.Path = s.Path
			continue
		}
		v = v.WithShape(s, OpAdd)
	}
	img := rendered(v, image.Rect(0, 0, 200, 200))
	for _, p := range []image.Point{{100, 12}, {100, 90}, {100, 170}} {
		if img.RGBAAt(p.X, p.Y).A != 255 {
			t.Errorf("overlapping strokes leave %v uncovered", p)
		}
	}
	if img.RGBAAt(100, 195).A != 0 {
		t.Error("below the strokes")
	}
	// With a shape taken away after them, as before.
	v = v.WithShape(Vector{Kind: VectorRect, Size: Pt{40, 40}, M: Translate(80, 60)}, OpSubtract)
	if rendered(v, image.Rect(0, 0, 200, 200)).RGBAAt(100, 80).A != 0 {
		t.Error("the shape subtracted after the strokes")
	}
}

func BenchmarkManyStrokes(b *testing.B) {
	tip := TipOutline(Brush{Size: 12, Round: 1, Tip: TipRound, Hardness: 1, Spacing: 0.1})
	v := Vector{Kind: VectorCurve, M: Identity, Fill: solidRed}
	for i := range 300 {
		var line []Pt
		for k := range 60 {
			line = append(line, Pt{float64(k * 15), float64(i*3) + 20*math.Sin(float64(k)/5)})
		}
		s := Vector{Kind: VectorCurve, M: Identity, Path: []Subpath{FitPath(Sweep(line, tip), true, 0.3)}}
		if i == 0 {
			v.Path = s.Path
			continue
		}
		v = v.WithShape(s, OpAdd)
	}
	r := image.Rect(0, 0, 900, 900)
	dst := image.NewRGBA(r)
	b.ResetTimer()
	for range b.N {
		v.Render(dst, r, 1)
	}
}
