package paint

import (
	"image"
	"image/color"
	"testing"
)

func TestHomographyCorners(t *testing.T) {
	q := [4]Pt{{10, 20}, {110, 0}, {130, 90}, {0, 70}}
	h := squareToQuad(q)
	for i, uv := range []Pt{{0, 0}, {1, 0}, {1, 1}, {0, 1}} {
		if p, ok := h.apply(uv); !ok || p.Dist(q[i]) > 1e-9 {
			t.Errorf("corner %d went to %v", i, p)
		}
	}
	inv, _ := h.inverse()
	if back, _ := inv.apply(Pt{60, 45}); back.X < 0 || back.X > 1 || back.Y < 0 || back.Y > 1 {
		t.Errorf("an inside point maps outside the square: %v", back)
	}
}

func TestMeshTransforms(t *testing.T) {
	d, err := NewFramed(200, 200, "Background", color.NRGBA{})
	if err != nil {
		t.Fatal(err)
	}
	d.Select(image.Rect(50, 50, 150, 150))
	d.Fill(color.NRGBA{R: 255, A: 255})
	d.Deselect()
	at := func(x, y int) color.RGBA { return d.Node(d.Active).Pixels.At(x, y) }
	// A mesh that leaves the corners where they are leaves the picture as it was.
	s, err := d.BeginTransform()
	if err != nil {
		t.Fatal(err)
	}
	b := s.Bounds()
	same := QuadMesh([4]Pt{{float64(b.Min.X), float64(b.Min.Y)}, {float64(b.Max.X), float64(b.Min.Y)}, {float64(b.Max.X), float64(b.Max.Y)}, {float64(b.Min.X), float64(b.Max.Y)}})
	if _, err := s.SetMesh(same, "Distort"); err != nil {
		t.Fatal(err)
	}
	for _, p := range []image.Point{{50, 50}, {149, 149}, {100, 100}} {
		if c := at(p.X, p.Y); c != (color.RGBA{255, 0, 0, 255}) {
			t.Errorf("unchanged mesh: %v at %v", c, p)
		}
	}
	if c := at(49, 100); c.A > 0 {
		t.Errorf("unchanged mesh spread to %v", c)
	}
	// Perspective: the top narrowed to half, the bottom as it was.
	if _, err := s.SetMesh(QuadMesh([4]Pt{{75, 50}, {125, 50}, {150, 150}, {50, 150}}), "Perspective"); err != nil {
		t.Fatal(err)
	}
	if c := at(60, 55); c.A > 0 {
		t.Errorf("outside the narrowed top %v", c)
	}
	if c := at(100, 55); c.A != 255 {
		t.Errorf("inside the narrowed top %v", c)
	}
	if c := at(52, 148); c.A < 200 {
		t.Errorf("the bottom corner %v", c)
	}
	s.Apply()
	if steps, _ := d.History(); steps[len(steps)-1] != "Perspective" {
		t.Errorf("steps %v", steps)
	}
	d.Undo()
	// Warp: pulling the middle of the top row up bows the top edge.
	s, _ = d.BeginTransform()
	g := WarpGrid(s.Bounds())
	g[1].Y -= 30
	g[2].Y -= 30
	if _, err := s.SetMesh(WarpMesh(g, 24), "Warp"); err != nil {
		t.Fatal(err)
	}
	if c := at(100, 40); c.A != 255 {
		t.Errorf("the bowed top %v", c)
	}
	if c := at(51, 40); c.A > 0 {
		t.Errorf("the top corner moved: %v", c)
	}
	s.Apply()
	d.Undo()
	// A vector object's nodes follow the mesh, and stay where they are with an unchanged one.
	n := d.AddVector(Vector{Kind: VectorRect, Size: Pt{100, 100}, M: Translate(50, 50), Fill: solidRed}, "Box")
	s, _ = d.BeginTransform()
	b = s.Bounds()
	q := [4]Pt{{float64(b.Min.X) + 20, float64(b.Min.Y)}, {float64(b.Max.X) - 20, float64(b.Min.Y)}, {float64(b.Max.X), float64(b.Max.Y)}, {float64(b.Min.X), float64(b.Max.Y)}}
	s.SetMesh(QuadMesh(q), "Perspective")
	s.Apply()
	v := d.Node(n.ID).Vector
	if v.Kind != VectorCurve || v.M != Identity {
		t.Fatalf("the mapped object %+v", v)
	}
	first := v.Path[0].Nodes[0].P
	if first.Dist(q[0]) > 1.5 {
		t.Errorf("the top left node went to %v, the corner to %v", first, q[0])
	}
	if _, err := s.SetMesh(Mesh{Cols: 2, Rows: 1, Pts: []Pt{{0, 0}}}, "Warp"); err == nil {
		t.Error("a mesh with too few points was taken")
	}
}
