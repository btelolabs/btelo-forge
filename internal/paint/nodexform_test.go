package paint

import (
	"errors"
	"testing"
)

// Free Transform on chosen nodes moves only them, about the box round them, as one step; cancelling puts them back.
func TestNodeTransformMovesOnlyTheChosenNodes(t *testing.T) {
	d := New("Background")
	square := Vector{Kind: VectorRect, Size: Pt{X: 100, Y: 100}, M: Translate(50, 50), Fill: solidRed}
	n := d.AddVector(square, "Square")
	if _, err := d.BeginNodeTransform(n.ID, nil); !errors.Is(err, ErrNoNodes) {
		t.Fatalf("no nodes: %v", err)
	}
	// The right side: nodes 1 (top right) and 2 (bottom right) of the square's outline.
	right := []NodeRef{{Sub: 0, Node: 1}, {Sub: 0, Node: 2}}
	s, err := d.BeginNodeTransform(n.ID, right)
	if err != nil {
		t.Fatal(err)
	}
	if b := s.Bounds(); b.Min.X != 150 || b.Max.X != 151 || b.Min.Y != 50 || b.Max.Y != 150 {
		t.Fatalf("the box is %v, want round the right side", b)
	}
	s.Set(Translate(30, 0), false)
	s.Apply()
	v := d.Node(n.ID).Vector
	at := func(i int) Pt { return v.M.Map(v.Path[0].Nodes[i].P) }
	if at(0) != (Pt{50, 50}) || at(3) != (Pt{50, 150}) {
		t.Errorf("the left side moved: %v %v", at(0), at(3))
	}
	if at(1) != (Pt{180, 50}) || at(2) != (Pt{180, 150}) {
		t.Errorf("the right side went to %v %v, want 30 to the right", at(1), at(2))
	}
	if d.UndoName() != "Transform nodes" {
		t.Errorf("recorded as %q", d.UndoName())
	}

	// A mesh moves the chosen nodes alone too, each through the mesh.
	s, err = d.BeginNodeTransform(n.ID, []NodeRef{{Sub: 0, Node: 0}})
	if err != nil {
		t.Fatal(err)
	}
	b := s.Bounds()
	m := Mesh{Cols: 1, Rows: 1, Pts: []Pt{
		{float64(b.Min.X) - 10, float64(b.Min.Y) - 10}, {float64(b.Max.X) - 10, float64(b.Min.Y) - 10},
		{float64(b.Min.X) - 10, float64(b.Max.Y) - 10}, {float64(b.Max.X) - 10, float64(b.Max.Y) - 10},
	}}
	if _, err := s.SetMesh(m, "Distort"); err != nil {
		t.Fatal(err)
	}
	v = d.Node(n.ID).Vector
	if p := v.M.Map(v.Path[0].Nodes[0].P); p != (Pt{40, 40}) {
		t.Errorf("the mesh put the node at %v", p)
	}
	s.Cancel()
	v = d.Node(n.ID).Vector
	if p := v.M.Map(v.Path[0].Nodes[0].P); p != (Pt{50, 50}) {
		t.Errorf("cancelling left the node at %v", p)
	}
}
