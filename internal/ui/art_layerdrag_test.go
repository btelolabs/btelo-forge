package ui

import (
	"slices"
	"testing"

	"github.com/btelolabs/btelo-forge/internal/paint"
)

// rootNames are the names of the picture's top-level nodes, top first.
func rootNames(d *paint.Doc) []string {
	var out []string
	for _, n := range d.Root.Children {
		out = append(out, n.Name)
	}
	return out
}

// shiftClickRow clicks row i of the layers panel with Shift held, choosing that layer besides the chosen one.
func (x *harness) shiftClickRow(i int) {
	r := x.layerRowAt(i)
	cx, cy := r.X+r.W*0.7, r.Y+r.H/2
	x.mx, x.my = cx, cy
	x.frame(Input{Shift: true})
	x.frame(Input{Down: true, Shift: true, Pointer: []Pointer{{X: cx, Y: cy, Down: true}}})
	x.frame(Input{Shift: true, Pointer: []Pointer{{X: cx, Y: cy}}})
}

// Layers are dragged in the panel to a new place: one, or all the chosen ones together; a group takes them in the
// middle of its row. Ctrl+G then groups all the chosen layers, not only the active one.
func TestDragLayersInThePanel(t *testing.T) {
	x, ad := canvasHarness(t)
	d := ad.d
	for _, name := range []string{"C", "B", "A"} {
		d.AddLayer(name)
	}
	x.frame(Input{})
	if got := rootNames(d); !slices.Equal(got, []string{"A", "B", "C", "Background"}) {
		t.Fatalf("the layers are %v", got)
	}
	// A, dragged by its name to the lower half of C's row, goes under C.
	from, to := x.layerRowAt(0), x.layerRowAt(2)
	x.drag(from.X+from.W*0.7, from.Y+from.H/2, to.X+to.W*0.7, to.Y+to.H*0.8)
	if got := rootNames(d); !slices.Equal(got, []string{"B", "C", "A", "Background"}) {
		t.Fatalf("after dragging A under C: %v", got)
	}
	if ad.layerDrag != nil || d.UndoName() != "Move layer" || d.Node(d.Active).Name != "A" {
		t.Errorf("the drag left %+v, step %q, chosen %q", ad.layerDrag, d.UndoName(), d.Node(d.Active).Name)
	}
	// B chosen besides A; both, dragged past the last row, go to the bottom in their order, and stay chosen.
	x.shiftClickRow(0)
	b := d.Root.Children[0].ID
	if !slices.Equal(ad.picked, []int{b}) {
		t.Fatalf("Shift+click chose %v", ad.picked)
	}
	from, last := x.layerRowAt(2), x.layerRowAt(3)
	x.drag(from.X+from.W*0.7, from.Y+from.H/2, last.X+last.W*0.7, last.Bottom()+last.H*1.5)
	if got := rootNames(d); !slices.Equal(got, []string{"C", "Background", "B", "A"}) {
		t.Fatalf("after dragging A and B to the bottom: %v", got)
	}
	if !slices.Equal(ad.picked, []int{b}) || d.Node(d.Active).Name != "A" {
		t.Errorf("after the drag %v are chosen besides %q", ad.picked, d.Node(d.Active).Name)
	}
	// Ctrl+G puts both into one group, in their order, where B was.
	x.key('G', true)
	g := d.Node(d.Active)
	if got := rootNames(d); !g.IsGroup() || !slices.Equal(got, []string{"C", "Background", g.Name}) || len(ad.picked) != 0 {
		t.Fatalf("Ctrl+G gave %v, %v still chosen", got, ad.picked)
	}
	if len(g.Children) != 2 || g.Children[0].Name != "B" || g.Children[1].Name != "A" {
		t.Fatalf("the group holds %v", g.Children)
	}
	// C, dropped in the middle of the group's row, goes into it, at the top.
	x.frame(Input{})
	from, to = x.layerRowAt(0), x.layerRowAt(2)
	x.drag(from.X+from.W*0.7, from.Y+from.H/2, to.X+to.W*0.7, to.Y+to.H/2)
	if len(g.Children) != 3 || g.Children[0].Name != "C" {
		t.Errorf("after dropping C on the group: %v", rootNames(d))
	}
	// A plain click on one of several chosen layers, without a drag, chooses it alone: the rows are now the
	// background, the group, and C, B and A in it.
	x.frame(Input{})
	x.shiftClickRow(3)
	if len(ad.picked) != 1 || d.Node(ad.picked[0]).Name != "B" {
		t.Fatalf("Shift+click on B chose %v", ad.picked)
	}
	x.click(x.layerRowAt(3))
	if len(ad.picked) != 0 || d.Node(d.Active).Name != "B" {
		t.Errorf("a click on B left %v chosen besides %q", ad.picked, d.Node(d.Active).Name)
	}
}

// A group dragged onto a layer inside it does not go into itself: nothing moves and no step is made.
func TestDragLayersRefusesWhereTheyCannotGo(t *testing.T) {
	x, ad := canvasHarness(t)
	d := ad.d
	d.AddLayer("Layer")
	g, _ := d.Group("Group", []int{d.Active})
	x.frame(Input{})
	before := rootNames(d)
	steps, _ := d.History()
	// The group's row, dragged onto its own layer's row, stays.
	from, to := x.layerRowAt(0), x.layerRowAt(1)
	x.drag(from.X+from.W*0.7, from.Y+from.H/2, to.X+to.W*0.7, to.Y+to.H/2)
	if now, _ := d.History(); !slices.Equal(rootNames(d), before) || len(now) != len(steps) || len(g.Children) != 1 {
		t.Errorf("dragging a group into itself gave %v", rootNames(d))
	}
}
