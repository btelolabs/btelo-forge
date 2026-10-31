package paint

import (
	"image"
	"image/color"
	"math"
	"testing"
)

// A round tip swept along a line covers a stadium: the line's length times the width the tip paints, half a pixel
// inside its size each way, and a disc for its ends.
func TestSweepARoundTip(t *testing.T) {
	tip := tipOutline(Brush{Size: 20, Round: 1, Tip: TipRound, Hardness: 1, Spacing: 0.1})
	if len(tip) < 8 {
		t.Fatalf("a round tip of %d points", len(tip))
	}
	ring := sweep([]Pt{{50, 50}, {100, 50}, {150, 50}}, tip)
	v := Vector{Kind: VectorCurve, M: Identity, Path: []Subpath{polyline(true, ring...)}, Fill: solidRed}
	a := coveredArea(rendered(v, image.Rect(0, 0, 200, 100)))
	want := 100*19 + math.Pi*9.5*9.5
	if math.Abs(a-want)/want > 0.01 {
		t.Errorf("the stroke covers %.0f, want about %.0f", a, want)
	}
}

// A flat tip swept across its width paints wide, along it narrow, as a calligraphy pen does.
func TestSweepAFlatTip(t *testing.T) {
	tip := tipOutline(Brush{Size: 40, Round: 0.25, Tip: TipSquare, Hardness: 1, Spacing: 0.1})
	across := sweep([]Pt{{50, 20}, {50, 120}}, tip) // down: across the tip's 40 px width
	along := sweep([]Pt{{20, 50}, {120, 50}}, tip)  // sideways: along it, 10 px tall
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
	tip := tipOutline(Brush{Size: 10, Round: 1, Tip: TipRound, Hardness: 1, Spacing: 0.1})
	v := Vector{Kind: VectorCurve, M: Identity, Fill: solidRed}
	for i := range 40 {
		y := float64(10 + i*4)
		s := Vector{Kind: VectorCurve, M: Identity, Path: []Subpath{polyline(true, sweep([]Pt{{10, y}, {190, y + 3}}, tip)...)}}
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

// mouseLine is a line as a mouse draws it: whole pixels, a point every two or more.
func mouseLine(f func(t float64) Pt) []Pt {
	var line []Pt
	for t := 0.0; t <= 1; t += 0.001 {
		p := f(t)
		p = Pt{math.Round(p.X), math.Round(p.Y)}
		if len(line) == 0 || line[len(line)-1].Dist(p) >= 2 {
			line = append(line, p)
		}
	}
	return line
}

// A vector stroke covers what the same brush paints along the same line, for every kind of tip and on a line with
// sharp bends, its dabs laid as close as they go, for a sweep is continuous where dabs would leave scallops.
func TestVectorStrokeMatchesTheBrush(t *testing.T) {
	star := &TipShape{W: 64, H: 64, Pix: make([]uint8, 64*64)}
	for y := range 64 {
		for x := range 64 {
			dx, dy := float64(x)-31.5, float64(y)-31.5
			if math.Hypot(dx, dy) < 16+14*math.Cos(5*math.Atan2(dy, dx)) {
				star.Pix[y*64+x] = 255
			}
		}
	}
	lines := map[string][]Pt{
		"wave": mouseLine(func(t float64) Pt { return Pt{60 + 380*t, 150 + 70*math.Sin(t*9)} }),
		"zig":  mouseLine(func(t float64) Pt { return Pt{60 + 380*t, 105 + 90*math.Abs(math.Mod(t*4, 2)-1)} }),
	}
	base := Brush{Hardness: 1, Opacity: 1, Flow: 1, Spacing: 0.01, Round: 1, Color: color.NRGBA{R: 255, A: 255}}
	for name, tip := range map[string]func(b *Brush){
		"round":  func(b *Brush) { b.Size = 20 },
		"large":  func(b *Brush) { b.Size = 80 },
		"square": func(b *Brush) { b.Size, b.Tip = 40, TipSquare },
		"turned": func(b *Brush) { b.Size, b.Tip, b.Angle, b.Round = 50, TipSquare, 30, 0.4 },
		"flat":   func(b *Brush) { b.Size, b.Tip = 60, TipFlat },
		"star":   func(b *Brush) { b.Size, b.Tip, b.Shape = 60, TipShaped, star },
	} {
		b := base
		tip(&b)
		for shape, line := range lines {
			r := image.Rect(0, 0, 500, 300)
			d := newDoc(t, r.Dx(), r.Dy(), color.NRGBA{})
			s, _, err := d.BeginStroke(b, false, line[0].X, line[0].Y)
			if err != nil {
				t.Fatal(err)
			}
			for _, p := range line[1:] {
				s.To(p.X, p.Y)
			}
			s.End()
			painted := d.Layers()[0].Pixels
			sw := NewBrushSweep(b, line[0])
			for _, p := range line[1:] {
				sw.To(p)
			}
			sw.End(line[len(line)-1])
			for _, fitted := range []bool{false, true} {
				got := rendered(Vector{Kind: VectorCurve, M: Identity, Path: sw.Outline(fitted), Fill: solidRed}, r)
				common, differ := 0, 0
				for y := r.Min.Y; y < r.Max.Y; y++ {
					for x := r.Min.X; x < r.Max.X; x++ {
						w, g := at(painted, x, y).A >= 128, got.RGBAAt(x, y).A >= 128
						if w != g {
							differ++
						} else if w {
							common++
						}
					}
				}
				if common == 0 || float64(differ) > 0.03*float64(common) {
					t.Errorf("%s on the %s, fitted %v: %d pixels differ of %d", name, shape, fitted, differ, common)
				}
			}
		}
	}
}

// A sweep's fitted outline is one that does not cross itself, however sharply the line turns back.
func TestSweepTurningBack(t *testing.T) {
	sw := NewBrushSweep(Brush{Size: 30, Round: 1, Tip: TipRound}, Pt{50, 50})
	for _, p := range []Pt{{150, 50}, {60, 56}, {160, 70}} {
		sw.To(p)
	}
	r := image.Rect(0, 0, 220, 120)
	got := rendered(Vector{Kind: VectorCurve, M: Identity, Path: sw.Outline(true), Fill: solidRed}, r)
	for _, p := range []image.Point{{100, 52}, {100, 60}, {150, 50}, {60, 56}} {
		if got.RGBAAt(p.X, p.Y).A != 255 {
			t.Errorf("%v is not covered", p)
		}
	}
}

// A click is a dab: the tip's own shape, at its size, a picture's too.
func TestSweepOfAClick(t *testing.T) {
	sw := NewBrushSweep(Brush{Size: 20, Round: 0.5, Angle: 90, Tip: TipRound}, Pt{50, 50})
	got := rendered(Vector{Kind: VectorCurve, M: Identity, Path: sw.Outline(true), Fill: solidRed}, image.Rect(0, 0, 100, 100))
	if got.RGBAAt(50, 42).A != 255 || got.RGBAAt(42, 50).A != 0 {
		t.Error("the tip's roundness should squash it across its angle")
	}
	square := &TipShape{W: 8, H: 8, Pix: make([]uint8, 64)}
	for i := range square.Pix {
		square.Pix[i] = 255
	}
	sw = NewBrushSweep(Brush{Size: 20, Round: 1, Spacing: 0.1, Tip: TipShaped, Shape: square}, Pt{50, 50})
	got = rendered(Vector{Kind: VectorCurve, M: Identity, Path: sw.Outline(true), Fill: solidRed}, image.Rect(0, 0, 100, 100))
	if a := coveredArea(got); math.Abs(a-400) > 12 {
		t.Errorf("a 20 px square picture covers %.0f", a)
	}
}

// A fit never throws a curve far from its points, even when they double back.
func TestFitStaysNearItsPoints(t *testing.T) {
	pts := []Pt{{0, 0}, {4, 2}, {6, 3}, {4, 2}, {3, 2}, {0, 0}, {-1, -1}, {-7, -3}, {-8, -3}, {-10, -4}, {-11, -5}, {-13, -6},
		{-14, -7}, {-16, -9}, {-17, -9}, {-18, -9}, {-16, -8}, {-14, -8}, {-12, -8}, {-10, -8}, {-6, -6}, {-8, -6}, {-9, -6},
		{-10, -6}, {-11, -6}, {-10, -6}, {-11, -5}, {-12, -5}, {-8, -4}, {-11, -3}, {-14, -3}, {-15, -3}, {-16, -2}}
	polys, _ := flatten([]Subpath{FitPath(pts, false, 0.25)}, Identity, 0.25)
	b, r := boundsOf(pts), boundsOf(polys[0])
	if r.Min.X < b.Min.X-3 || r.Min.Y < b.Min.Y-3 || r.Max.X > b.Max.X+3 || r.Max.Y > b.Max.Y+3 {
		t.Errorf("the fit reaches %v, outside the points' %v", r, b)
	}
}

func BenchmarkManyStrokes(b *testing.B) {
	tip := tipOutline(Brush{Size: 12, Round: 1, Tip: TipRound, Hardness: 1, Spacing: 0.1})
	v := Vector{Kind: VectorCurve, M: Identity, Fill: solidRed}
	for i := range 300 {
		var line []Pt
		for k := range 60 {
			line = append(line, Pt{float64(k * 15), float64(i*3) + 20*math.Sin(float64(k)/5)})
		}
		s := Vector{Kind: VectorCurve, M: Identity, Path: []Subpath{FitPath(sweep(line, tip), true, 0.3)}}
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
