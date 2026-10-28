package paint

import (
	"image"
	"math"
	"testing"
)

func TestNodeEditing(t *testing.T) {
	rect := Vector{Kind: VectorRect, Size: Pt{100, 50}, M: Identity, Fill: solidRed}
	c := rect.ToCurve()
	if c.Kind != VectorCurve || len(c.Path) != 1 || len(c.Path[0].Nodes) != 4 || !c.Path[0].Closed {
		t.Fatalf("converted rectangle %+v", c.Path)
	}
	if rect.Kind != VectorRect {
		t.Error("converting changed the original")
	}
	// Moving a node moves its handles with it; the others stay.
	moved := c.MoveNodes([]NodeRef{{0, 2}}, Pt{10, 5})
	if moved.Path[0].Nodes[2].P != (Pt{110, 55}) || moved.Path[0].Nodes[0].P != (Pt{0, 0}) || c.Path[0].Nodes[2].P != (Pt{100, 50}) {
		t.Errorf("moved nodes %+v", moved.Path[0].Nodes)
	}
	// A smooth node keeps its handles in line; Alt breaks it.
	sm := c.SetSmooth(NodeRef{0, 1}, true)
	n := sm.Path[0].Nodes[1]
	if !n.Smooth || n.In.Sub(n.P).Unit().Dot(n.Out.Sub(n.P).Unit()) > -0.999 {
		t.Fatalf("smooth node %+v", n)
	}
	dragged := sm.MoveHandle(NodeRef{0, 1}, true, Pt{140, 30}, false)
	n = dragged.Path[0].Nodes[1]
	if d := n.In.Sub(n.P).Unit().Dot(n.Out.Sub(n.P).Unit()); d > -0.999 {
		t.Errorf("the handles left line after a drag: %v", d)
	}
	broken := sm.MoveHandle(NodeRef{0, 1}, true, Pt{140, 30}, true)
	if bn := broken.Path[0].Nodes[1]; bn.Smooth || bn.In != sm.Path[0].Nodes[1].In {
		t.Errorf("Alt moved the other handle: %+v", bn)
	}
	// A node added on a segment keeps the outline.
	ell := Vector{Kind: VectorEllipse, Size: Pt{100, 100}, M: Identity}.ToCurve()
	before := ell.Bounds()
	split, ref := ell.InsertNode(NodeRef{0, 0}, 0.5)
	if len(split.Path[0].Nodes) != 5 || ref != (NodeRef{0, 1}) {
		t.Fatalf("inserted at %+v: %d nodes", ref, len(split.Path[0].Nodes))
	}
	if after := split.Bounds(); math.Abs(after.Min.X-before.Min.X) > 0.01 || math.Abs(after.Max.Y-before.Max.Y) > 0.01 {
		t.Errorf("inserting a node changed the outline: %v from %v", after, before)
	}
	mid := split.Path[0].Nodes[1].P
	if d := mid.Dist(Pt{50, 50}); math.Abs(d-50) > 0.01 {
		t.Errorf("the new node is %.3f from the middle, want 50", d)
	}
	// The nearest point of the outline to a click.
	r, tt, dist := ell.NearestOnSegment(Pt{50 + 60*math.Cos(math.Pi/4), 50 + 60*math.Sin(math.Pi/4)})
	if r.Sub != 0 || math.Abs(dist-10) > 0.05 || tt <= 0 || tt >= 1 {
		t.Errorf("nearest %+v at %v, %v away", r, tt, dist)
	}
	// Deleting nodes; a subpath left with one goes.
	if left := c.DeleteNodes([]NodeRef{{0, 0}}); len(left.Path[0].Nodes) != 3 {
		t.Errorf("after deleting one: %+v", left.Path)
	}
	if left := c.DeleteNodes([]NodeRef{{0, 0}, {0, 1}, {0, 2}}); len(left.Path) != 0 {
		t.Errorf("after deleting three of four: %+v", left.Path)
	}
}

func TestVectorSession(t *testing.T) {
	d := New("Layer 1")
	pt := func(x, y float64) PathNode { return PathNode{P: Pt{x, y}, In: Pt{x, y}, Out: Pt{x, y}} }
	line := Vector{Kind: VectorCurve, M: Identity, Path: []Subpath{{Nodes: []PathNode{pt(10, 10)}}},
		Stroke: VectorStroke{Paint: solidRed, Width: 4}}
	s, err := d.BeginVector(0, line, "Curve 2", "Pen")
	if err != nil {
		t.Fatal(err)
	}
	// One node draws nothing and does not break the drawing.
	_ = d.Composite(image.Rect(0, 0, 64, 64))
	line.Path[0].Nodes = append(line.Path[0].Nodes, pt(50, 10))
	if r := s.Set(line); !r.Overlaps(image.Rect(10, 8, 50, 12)) {
		t.Errorf("changed area %v", r)
	}
	s.End()
	if steps, _ := d.History(); len(steps) != 1 || steps[0] != "Pen" || len(d.Layers()) != 2 {
		t.Fatalf("after the pen: %v, %d layers", steps, len(d.Layers()))
	}
	if c := d.Composite(image.Rect(0, 0, 64, 64)).RGBAAt(30, 10); c.R < 200 {
		t.Errorf("the line is not drawn: %v", c)
	}
	// A pen that ends with one node leaves nothing, not even a step.
	s, _ = d.BeginVector(0, Vector{Kind: VectorCurve, M: Identity, Path: []Subpath{{Nodes: []PathNode{pt(5, 5)}}}}, "Curve 3", "Pen")
	s.Set(s.Vector().DeleteNodes([]NodeRef{{0, 0}}))
	s.End()
	if steps, _ := d.History(); len(steps) != 1 || len(d.Layers()) != 2 {
		t.Errorf("an empty pen left %v, %d layers", steps, len(d.Layers()))
	}
	// Deleting every node of a curve deletes its layer, as one step that undoes.
	id := d.Active
	s, _ = d.BeginVector(id, Vector{}, "", "Delete node")
	s.Set(s.Vector().DeleteNodes([]NodeRef{{0, 0}, {0, 1}}))
	s.End()
	if d.Node(id) != nil || len(d.Layers()) != 1 {
		t.Fatalf("the emptied curve's layer stayed")
	}
	d.Undo()
	if d.Node(id) == nil || d.Node(id).Vector == nil {
		t.Errorf("undo did not bring the curve back")
	}
	// Cancel puts it back.
	s, _ = d.BeginVector(id, Vector{}, "", "Move node")
	s.Set(s.Vector().MoveNodes([]NodeRef{{0, 0}}, Pt{20, 20}))
	s.Cancel()
	if p := d.Node(id).Vector.Path[0].Nodes[0].P; p != (Pt{10, 10}) {
		t.Errorf("cancel left the node at %v", p)
	}
	if _, err := d.BeginVector(d.Layers()[1].ID, Vector{}, "", "x"); err != ErrNotVector {
		t.Errorf("editing a pixel layer: %v", err)
	}
}

func TestVectorAt(t *testing.T) {
	d := New("Layer 1")
	ring := Vector{Kind: VectorEllipse, Size: Pt{100, 100}, M: Translate(100, 100),
		Stroke: VectorStroke{Paint: solidRed, Width: 2}}
	low := d.AddVector(ring, "Ring")
	high := d.AddVector(Vector{Kind: VectorRect, Size: Pt{40, 40}, M: Translate(180, 130), Fill: solidRed}, "Box")
	for _, c := range []struct {
		p    Pt
		want int
	}{
		{Pt{100.5, 150}, low.ID}, // on the stroke
		{Pt{96, 150}, low.ID},    // just off it, within the slack
		{Pt{150, 150}, 0},        // inside the unfilled ring
		{Pt{199, 150}, high.ID},  // the box over the ring's stroke
		{Pt{400, 400}, 0},        // nowhere
	} {
		if got := d.VectorAt(c.p, 6); got != c.want {
			t.Errorf("at %v: %d, want %d", c.p, got, c.want)
		}
	}
	high.Visible = false
	if got := d.VectorAt(Pt{199, 150}, 6); got != low.ID {
		t.Errorf("a hidden layer was hit: %d", got)
	}
}
