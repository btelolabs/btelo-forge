package paint

import (
	"image"
	"math"
	"testing"
)

// wavyRing is a closed curve of n sharp nodes round (cx, cy), its radius r rippling, as a long freehand outline is.
func wavyRing(cx, cy, r float64, n int) Vector {
	sub := Subpath{Closed: true}
	for i := range n {
		a := 2 * math.Pi * float64(i) / float64(n)
		rr := r * (1 + 0.04*math.Sin(37*a) + 0.02*math.Sin(113*a))
		p := Pt{X: cx + rr*math.Cos(a), Y: cy + rr*math.Sin(a)}
		sub.Nodes = append(sub.Nodes, PathNode{P: p, In: p, Out: p})
	}
	return Vector{Kind: VectorCurve, Path: []Subpath{sub}, M: Identity, Fill: solidRed}
}

func BenchmarkCombineLargeOutlines(b *testing.B) {
	a, c := wavyRing(2000, 2000, 1500, 4000), wavyRing(2600, 2300, 1400, 4000)
	for b.Loop() {
		if len(Combine(BoolSubtract, []Vector{a, c})) == 0 {
			b.Fatal("nothing left")
		}
	}
}

func BenchmarkCombineShapesLargeLayers(b *testing.B) {
	for b.Loop() {
		b.StopTimer()
		d := New("Background")
		bottom := d.AddVector(wavyRing(2000, 2000, 1500, 4000), "A")
		top := d.AddVector(wavyRing(2600, 2300, 1400, 4000), "B")
		b.StartTimer()
		if _, err := d.CombineShapes(OpSubtract, []int{bottom.ID, top.ID}); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRenderCombinedShapesTile(b *testing.B) {
	v := wavyRing(2000, 2000, 1500, 4000).WithShape(wavyRing(2600, 2300, 1400, 4000), OpSubtract)
	r := image.Rect(1800, 1800, 2312, 2312)
	img := image.NewRGBA(r)
	for b.Loop() {
		v.Render(img, r, 1)
	}
}

// The combined layer shows what the baked boolean does: the shapes' coverage and the outline Combine works out
// agree away from the edges.
func TestCombineShapesMatchesTheBakedOutline(t *testing.T) {
	a, c := wavyRing(200, 200, 150, 400), wavyRing(260, 230, 140, 400)
	for _, op := range []ShapeOp{OpAdd, OpSubtract, OpIntersect, OpExclude} {
		live := a.WithShape(c, op)
		baked := Vector{Kind: VectorCurve, Path: Combine(op.boolOf(), []Vector{a, c}), M: Identity, Rule: NonZero}
		r := image.Rect(0, 0, 450, 450)
		lc, bc := live.fillCoverage(r, 1), baked.fillCoverage(r, 1)
		off := 0
		for i := range lc {
			if d := int(lc[i]) - int(bc[i]); d > 64 || d < -64 {
				off++
			}
		}
		if off > 40 { // a few pixels along the outlines, where the fitted curves stray a little
			t.Errorf("op %d: %d pixels differ between the live shapes and the baked outline", op, off)
		}
	}
}
