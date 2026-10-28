package paint

import (
	"math"
	"testing"
)

// farthest is how far the fitted path strays from the points, and they from it.
func farthest(t *testing.T, s Subpath, pts []Pt) float64 {
	t.Helper()
	v := Vector{Kind: VectorCurve, Path: []Subpath{s}, M: Identity}
	worst := 0.0
	for _, p := range pts {
		_, _, d := v.NearestOnSegment(p)
		worst = max(worst, d)
	}
	return worst
}

func TestFitPath(t *testing.T) {
	// A circle sampled finely fits in a few smooth nodes.
	var circle []Pt
	for i := range 200 {
		a := 2 * math.Pi * float64(i) / 200
		circle = append(circle, Pt{100 + 50*math.Cos(a), 100 + 50*math.Sin(a)})
	}
	s := FitPath(circle, true, 0.2)
	if !s.Closed || len(s.Nodes) > 8 || len(s.Nodes) < 2 {
		t.Fatalf("a circle fitted in %d nodes", len(s.Nodes))
	}
	for _, n := range s.Nodes {
		if !n.Smooth {
			t.Errorf("a corner on a circle: %+v", n)
		}
	}
	if d := farthest(t, s, circle); d > 0.25 {
		t.Errorf("the circle's fit strays %.3f", d)
	}
	// A square keeps its four corners, sharp, and straight sides.
	var square []Pt
	for _, c := range [][2]Pt{{{0, 0}, {80, 0}}, {{80, 0}, {80, 80}}, {{80, 80}, {0, 80}}, {{0, 80}, {0, 0}}} {
		for k := range 10 {
			square = append(square, c[0].Lerp(c[1], float64(k)/10))
		}
	}
	s = FitPath(square, true, 0.2)
	if len(s.Nodes) != 4 {
		t.Fatalf("a square fitted in %d nodes: %+v", len(s.Nodes), s.Nodes)
	}
	for _, n := range s.Nodes {
		if n.Smooth {
			t.Errorf("a smooth corner %+v", n)
		}
	}
	if d := farthest(t, s, square); d > 0.05 {
		t.Errorf("the square's fit strays %.3f", d)
	}
	// An open wave keeps its ends and stays close.
	var wave []Pt
	for i := 0; i <= 300; i++ {
		x := float64(i)
		wave = append(wave, Pt{x, 40 * math.Sin(x/30)})
	}
	s = FitPath(wave, false, 0.3)
	if s.Closed || s.Nodes[0].P != wave[0] || s.Nodes[len(s.Nodes)-1].P != wave[len(wave)-1] || len(s.Nodes) > 12 {
		t.Errorf("the wave: %d nodes, ends %v %v", len(s.Nodes), s.Nodes[0].P, s.Nodes[len(s.Nodes)-1].P)
	}
	if d := farthest(t, s, wave); d > 0.35 {
		t.Errorf("the wave's fit strays %.3f", d)
	}
	// Too few points are kept as they are.
	if s := FitPath([]Pt{{1, 1}, {1, 1}}, false, 0.2); len(s.Nodes) != 1 {
		t.Errorf("a single point: %+v", s)
	}
}
