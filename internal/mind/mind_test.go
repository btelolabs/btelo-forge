package mind

import (
	"bytes"
	"fmt"
	"math"
	"math/rand"
	"strings"
	"testing"
	"time"
)

func newMap() *Map { return New("Test", SimpleMeasurer{}) }

// tree makes a root with children named a, b, c… and returns their ids.
func tree(t testing.TB, m *Map, layout Layout, kids int) (ID, []ID) {
	t.Helper()
	root, err := m.Add(Node{Text: "Root", Layout: layout}, 0, -1)
	if err != nil {
		t.Fatal(err)
	}
	var ids []ID
	for i := range kids {
		id, err := m.Add(Node{Text: fmt.Sprintf("child %c", 'a'+i)}, root, -1)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	return root, ids
}

// noOverlap fails when two shown nodes' boxes overlap.
func noOverlap(t *testing.T, m *Map) {
	t.Helper()
	m.Layout()
	nodes := m.Visible()
	for i, a := range nodes {
		for _, b := range nodes[i+1:] {
			ra, rb := m.Box(a), m.Box(b)
			if ra.X0 < rb.X1-0.5 && rb.X0 < ra.X1-0.5 && ra.Y0 < rb.Y1-0.5 && rb.Y0 < ra.Y1-0.5 {
				t.Fatalf("%q %v overlaps %q %v", a.Text, ra, b.Text, rb)
			}
		}
	}
}

func TestMapLayoutBalancesSides(t *testing.T) {
	m := newMap()
	root, kids := tree(t, m, LayoutMap, 5)
	m.Layout()
	rb := m.Box(m.Node(root))
	right, left := 0, 0
	for _, id := range kids {
		b := m.Box(m.Node(id))
		switch {
		case b.X0 >= rb.X1:
			right++
		case b.X1 <= rb.X0:
			left++
		default:
			t.Fatalf("child %v is neither right nor left of the root %v", b, rb)
		}
	}
	if right != 3 || left != 2 {
		t.Fatalf("right %d, left %d; want 3 and 2", right, left)
	}
	noOverlap(t, m)
}

func TestLayoutsDoNotOverlap(t *testing.T) {
	for _, l := range []Layout{LayoutMap, LayoutRight, LayoutLeft, LayoutDown, LayoutUp, LayoutList} {
		t.Run(LayoutNames[l], func(t *testing.T) {
			m := newMap()
			_, kids := tree(t, m, l, 4)
			for i, id := range kids {
				for k := range i + 1 {
					g, _ := m.Add(Node{Text: fmt.Sprintf("grandchild %d.%d with longer words", i, k)}, id, -1)
					m.Add(Node{Text: "leaf"}, g, -1)
				}
			}
			noOverlap(t, m)
			// Growing one node pushes its neighbours away, still without overlaps.
			m.Update(kids[1], func(n *Node) { n.Text = "a much longer topic\nover two lines\nand a third" })
			noOverlap(t, m)
		})
	}
}

func TestDownLayoutPutsChildrenBelow(t *testing.T) {
	m := newMap()
	root, kids := tree(t, m, LayoutDown, 3)
	m.Layout()
	rb := m.Box(m.Node(root))
	prev := -1e9
	for _, id := range kids {
		b := m.Box(m.Node(id))
		if b.Y0 < rb.Y1 {
			t.Fatalf("child %v is not below the root %v", b, rb)
		}
		if b.X0 < prev {
			t.Fatalf("children are not side by side in order")
		}
		prev = b.X1
	}
}

func TestUndoRedo(t *testing.T) {
	m := newMap()
	root, kids := tree(t, m, LayoutRight, 3)
	if err := m.Delete(kids[1]); err != nil {
		t.Fatal(err)
	}
	if m.Len() != 3 || len(m.Node(root).Children) != 2 {
		t.Fatalf("after deleting: %d nodes, %d children", m.Len(), len(m.Node(root).Children))
	}
	if !m.Undo() || m.Len() != 4 || m.Node(root).Children[1] != kids[1] {
		t.Fatalf("undo did not bring the child back in its place: %v", m.Node(root).Children)
	}
	m.Redo()
	if m.Node(kids[1]) != nil {
		t.Fatal("redo did not delete again")
	}
	m.Undo()
	// A move and its undo.
	if err := m.Move(kids[2], kids[0], -1, 0, 0); err != nil {
		t.Fatal(err)
	}
	if m.Node(kids[2]).Parent != kids[0] {
		t.Fatal("not moved")
	}
	m.Undo()
	if m.Node(kids[2]).Parent != root || len(m.Node(kids[0]).Children) != 0 {
		t.Fatal("undoing the move did not put it back")
	}
	// A cycle is refused.
	if err := m.Move(root, kids[0], -1, 0, 0); err != ErrCycle {
		t.Fatalf("moving a root under its child: %v", err)
	}
	// Several changes in one step.
	m.Begin("batch")
	m.Update(kids[0], func(n *Node) { n.Text = "x" })
	m.Update(kids[1], func(n *Node) { n.Text = "y" })
	m.Commit()
	m.Undo()
	if m.Node(kids[0]).Text != "child a" || m.Node(kids[1]).Text != "child b" {
		t.Fatal("a step was not undone whole")
	}
	noOverlap(t, m)
}

func TestDeleteRemovesConnectorsAndSubtrees(t *testing.T) {
	m := newMap()
	root, kids := tree(t, m, LayoutRight, 2)
	g, _ := m.Add(Node{Text: "under a"}, kids[0], -1)
	free, _ := m.Add(Node{Text: "free", X: 600, Y: 300, Shape: ShapeRounded}, 0, -1)
	e, err := m.AddEdge(Edge{From: g, To: free})
	if err != nil {
		t.Fatal(err)
	}
	m.Layout()
	if len(m.EdgesIn(m.Bounds())) != 1 {
		t.Fatal("the connector is not found")
	}
	m.Delete(kids[0])
	if m.Node(g) != nil || m.Edge(e) != nil || m.EdgeCount() != 0 {
		t.Fatal("the subtree or its connector stayed")
	}
	m.Undo()
	if m.Edge(e) == nil || m.Node(g) == nil {
		t.Fatal("undo did not bring them back")
	}
	_ = root
}

func TestAtFindsNodes(t *testing.T) {
	m := newMap()
	root, kids := tree(t, m, LayoutMap, 6)
	m.Layout()
	for _, id := range append(kids, root) {
		n := m.Node(id)
		b := m.Box(n)
		if got, _ := m.At(b.CX(), b.CY()); got != n {
			t.Fatalf("At the centre of %q found %v", n.Text, got)
		}
	}
	if got, _ := m.At(-5000, -5000); got != nil {
		t.Fatal("found a node where there is none")
	}
}

func TestFoldHidesChildren(t *testing.T) {
	m := newMap()
	root, kids := tree(t, m, LayoutRight, 3)
	m.Update(root, func(n *Node) { n.Folded = true })
	m.Layout()
	if !m.Hidden(m.Node(kids[0])) || len(m.Visible()) != 1 {
		t.Fatal("folded children still shown")
	}
	b := m.Box(m.Node(kids[0]))
	if got, _ := m.At(b.CX(), b.CY()); got != nil && got.ID != root {
		t.Fatal("a hidden child can be pointed at")
	}
}

func TestChangedListsTheEditedArea(t *testing.T) {
	m := newMap()
	_, kids := tree(t, m, LayoutRight, 3)
	free, _ := m.Add(Node{Text: "far away", X: 5000, Y: 5000, Shape: ShapeRounded}, 0, -1)
	m.Changed()
	m.Update(kids[2], func(n *Node) { n.Text = "longer words now" })
	regions, all := m.Changed()
	if all || len(regions) == 0 {
		t.Fatalf("regions %v, all %v", regions, all)
	}
	fb := m.Box(m.Node(free))
	for _, r := range regions {
		if r.Overlaps(fb) {
			t.Fatalf("the change to a tree claims the far node changed: %v", r)
		}
	}
	nb := m.Box(m.Node(kids[2]))
	covered := false
	for _, r := range regions {
		covered = covered || r.Inside(nb)
	}
	if !covered {
		t.Fatalf("the edited node %v is in no region %v", nb, regions)
	}
}

// A free node dragged, live, frame by frame, gives up the place it was drawn at as well as the new one: else the
// tiles there go on showing it.
func TestDraggedNodeLeavesItsOldPlace(t *testing.T) {
	m := newMap()
	id, _ := m.Add(Node{Text: "dragged", X: 100, Y: 100, Shape: ShapeRounded}, 0, -1)
	root, _ := tree(t, m, LayoutMap, 2)
	m.Changed()
	for step := range 5 {
		before := m.SubBox(m.Node(id))
		m.Live(id, func(n *Node) { n.X, n.Y = 100+float64(step+1)*400, 100 })
		regions, all := m.Changed()
		if all {
			continue
		}
		covered := false
		for _, r := range regions {
			covered = covered || r.Inside(before)
		}
		if !covered {
			t.Fatalf("step %d: the old place %v is in no region %v", step, before, regions)
		}
	}
	// So does a tree's root moved.
	before := m.SubBox(m.Node(root))
	m.Live(root, func(n *Node) { n.X += 1500 })
	regions, _ := m.Changed()
	covered := false
	for _, r := range regions {
		covered = covered || r.Inside(before)
	}
	if !covered {
		t.Fatalf("the tree's old place %v is in no region %v", before, regions)
	}
}

func TestTableFormulas(t *testing.T) {
	g := &Grid{Rows: 3, Cols: 3, Cells: []string{
		"1", "2", "=A1+B1*2",
		"4", "x", "=SUM(A1:B2)",
		"=AVERAGE(A1:A2)", "=B3+1", "=1/0",
	}}
	got := evalGrid(g)
	want := []string{"1", "2", "5", "4", "x", "7", "2.5", "#CYCLE!", "#DIV/0!"}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("cell %s = %q, want %q", CellName(i/3, i%3), got[i], want[i])
		}
	}
	if CellName(0, 27) != "AB1" {
		t.Fatalf("CellName(0, 27) = %s", CellName(0, 27))
	}
	if r, c, ok := cellRef("AB12"); !ok || r != 11 || c != 27 {
		t.Fatalf("cellRef(AB12) = %d %d %v", r, c, ok)
	}
}

func TestTableGrowsAndLaysOut(t *testing.T) {
	m := newMap()
	id, _ := m.Add(Node{Kind: Table, X: 0, Y: 0}, 0, -1)
	if err := m.SetCell(id, 2, 3, "=1+1"); err != nil {
		t.Fatal(err)
	}
	n := m.Node(id)
	if n.Cells.Rows != 3 || n.Cells.Cols != 4 {
		t.Fatalf("grid %dx%d", n.Cells.Rows, n.Cells.Cols)
	}
	if CellValue(n.Cells, 2, 3) != "2" {
		t.Fatal("formula not worked out")
	}
	r, c, ok := m.CellAt(n, m.CellRect(n, 2, 3).CX(), m.CellRect(n, 2, 3).CY())
	if !ok || r != 2 || c != 3 {
		t.Fatalf("CellAt %d %d %v", r, c, ok)
	}
}

// bigMap is a map of n nodes: one tree of fan children each with fan children and so on, and free nodes besides.
func bigMap(tb testing.TB, n int) (*Map, ID) {
	m := newMap()
	root, _ := m.Add(Node{Text: "Root"}, 0, -1)
	queue := []ID{root}
	rng := rand.New(rand.NewSource(1))
	m.Begin("build")
	for k := 1; k < n; {
		p := queue[0]
		queue = queue[1:]
		for range 8 {
			if k >= n {
				break
			}
			id, _ := m.Add(Node{Text: fmt.Sprintf("topic %d %s", k, []string{"short", "a little longer", "much longer words here"}[rng.Intn(3)])}, p, -1)
			queue = append(queue, id)
			k++
		}
	}
	m.Commit()
	return m, root
}

func TestLargeMapEditsAreIncremental(t *testing.T) {
	if testing.Short() {
		t.Skip("large map")
	}
	m, root := bigMap(t, 300_000)
	start := time.Now()
	m.Layout()
	full := time.Since(start)
	// Adding a node deep down restacks only its ancestors' children.
	leaf := m.Node(root)
	for len(leaf.Children) > 0 {
		leaf = m.Node(leaf.Children[len(leaf.Children)/2])
	}
	start = time.Now()
	const edits = 20
	for i := range edits {
		m.Add(Node{Text: fmt.Sprintf("new %d", i)}, leaf.ID, -1)
		m.Layout()
	}
	each := time.Since(start) / edits
	t.Logf("300k nodes: first layout %v, then %v per edit", full, each)
	if each > 30*time.Millisecond {
		t.Fatalf("an edit took %v to lay out", each)
	}
	// Finding what is in a small view is quick however large the map.
	b := m.Box(leaf)
	start = time.Now()
	found := 0
	for range 100 {
		m.Visit(b.Grow(400), func(*Node, Vec) bool { found++; return true })
	}
	if per := time.Since(start) / 100; per > 2*time.Millisecond {
		t.Fatalf("a view query took %v", per)
	}
	if found == 0 {
		t.Fatal("found nothing around the leaf")
	}
}

func BenchmarkLayoutEdit(b *testing.B) {
	m, root := bigMap(b, 300_000)
	m.Layout()
	r := m.Node(root)
	b.ResetTimer()
	for i := range b.N {
		m.Update(r.Children[i%len(r.Children)], func(n *Node) { n.Text = fmt.Sprint(i) })
		m.Layout()
	}
}

func TestFreeSpotAvoidsNodes(t *testing.T) {
	m := newMap()
	root, _ := tree(t, m, LayoutMap, 6)
	b := m.SubBox(m.Node(root))
	p := m.FreeSpot(Vec{b.CX(), b.CY()}, 200, 100)
	if R(p.X, p.Y, 200, 100).Overlaps(b) {
		t.Fatalf("the spot %v overlaps the tree %v", p, b)
	}
	far := Vec{b.X1 + 500, b.Y1 + 500}
	if got := m.FreeSpot(far, 100, 50); got != far {
		t.Fatalf("a free place was moved: %v", got)
	}
}

// TestLoneChildContinuesTheLine: a topic's only child sits so its line runs straight on from its parent's, and two
// children sit evenly about it.
func TestLoneChildContinuesTheLine(t *testing.T) {
	m := newMap()
	root, kids := tree(t, m, LayoutRight, 2)
	only, _ := m.Add(Node{Text: "only child, smaller"}, kids[0], -1)
	m.Layout()
	p, c := m.Node(kids[0]), m.Node(only)
	py := m.Pos(p).Y + underY(p)
	cy := m.Pos(c).Y + underY(c)
	if math.Abs(py-cy) > 0.25 {
		t.Fatalf("the child's line is at %v, its parent's at %v", cy, py)
	}
	r := m.Node(root)
	ry := m.Pos(r).Y + outAnchor(r, LayoutRight).Y
	a := m.Pos(m.Node(kids[0])).Y + underY(m.Node(kids[0]))
	b := m.Pos(m.Node(kids[1])).Y + underY(m.Node(kids[1]))
	if math.Abs((a+b)/2-ry) > 0.5 {
		t.Fatalf("the children's lines %v and %v are not about the root's middle %v", a, b, ry)
	}
}

func TestConnectorBendMovesItsMiddle(t *testing.T) {
	for _, r := range []Route{RouteCurve, RouteElbow, RouteStraight} {
		m := newMap()
		a, _ := m.Add(Node{Text: "a", Shape: ShapeRounded, X: 0, Y: 0}, 0, -1)
		b, _ := m.Add(Node{Text: "b", Shape: ShapeRounded, X: 600, Y: 300}, 0, -1)
		id, _ := m.AddEdge(Edge{From: a, To: b, Route: r})
		e := m.Edge(id)
		base, _ := m.EdgeBase(e)
		m.UpdateEdge(id, func(e *Edge) { e.Bend = Vec{-80, 140} })
		_, mid, _ := m.EdgePath(e)
		if r != RouteElbow && (math.Abs(mid.X-(base.X-80)) > 0.5 || math.Abs(mid.Y-(base.Y+140)) > 0.5) {
			t.Fatalf("%s: the middle is at %v, want %v", RouteNames[r], mid, base.Add(Vec{-80, 140}))
		}
		path, _, _ := m.EdgePath(e)
		near := math.Inf(1)
		for i := 1; i < len(path); i++ {
			near = math.Min(near, segDist(base.Add(Vec{-80, 140}), path[i-1], path[i]))
		}
		if r != RouteElbow && near > 1 {
			t.Fatalf("%s: the path passes %v from where its middle was dragged", RouteNames[r], near)
		}
		data, _ := Encode(m, "")
		got, err := Load(bytes.NewReader(data), "", SimpleMeasurer{})
		if err != nil || got.Edge(id).Bend != (Vec{-80, 140}) {
			t.Fatalf("%s: the bend read back as %v (%v)", RouteNames[r], got.Edge(id).Bend, err)
		}
	}
}

// TestConnectorsGoRoundNodes: a connector whose plain path would cross a node between its ends finds a way that
// crosses none; one the user shaped keeps its shape.
func TestConnectorsGoRoundNodes(t *testing.T) {
	for _, r := range []Route{RouteCurve, RouteElbow, RouteStraight} {
		m := newMap()
		a, _ := m.Add(Node{Text: "AAA", Shape: ShapeRounded, X: 0, Y: 0}, 0, -1)
		mid, _ := m.Add(Node{Text: "Summer campaign in the way", Shape: ShapeRounded, X: 220, Y: -20}, 0, -1)
		b, _ := m.Add(Node{Text: "Trailer", Shape: ShapeRounded, X: 700, Y: 10}, 0, -1)
		id, _ := m.AddEdge(Edge{From: a, To: b, Route: r})
		m.Layout()
		e := m.Edge(id)
		if hits := m.pathHits(r, e.geo.pts, a, b); hits != 0 {
			t.Fatalf("%s: the path still crosses %d nodes: %v", RouteNames[r], hits, e.geo.pts)
		}
		// Moving the node in the way out of it brings the plain path back.
		m.Place(mid, 220, 600)
		m.Layout()
		if e.geo.from != SideRight || e.geo.to != SideLeft {
			t.Fatalf("%s: with nothing in the way it leaves %v and enters %v", RouteNames[r], SideNames[e.geo.from], SideNames[e.geo.to])
		}
		// Moved back into the way, it goes round again.
		m.Place(mid, 220, -20)
		m.Layout()
		if hits := m.pathHits(r, e.geo.pts, a, b); hits != 0 {
			t.Fatalf("%s: a node moved into its way is crossed", RouteNames[r])
		}
		// Shaped by the user, it is left as they made it.
		m.UpdateEdge(id, func(e *Edge) { e.Bend = Vec{0, 1} })
		m.Layout()
		if e.geo.from != SideRight || e.geo.to != SideLeft {
			t.Fatalf("%s: a shaped connector was rerouted", RouteNames[r])
		}
	}
}

func TestTasksCountTheirProgress(t *testing.T) {
	m := newMap()
	root, kids := tree(t, m, LayoutRight, 2)
	a1, _ := m.Add(Node{Text: "a1"}, kids[0], -1)
	m.Add(Node{Text: "a2"}, kids[0], -1)
	m.Add(Node{Text: "a3"}, kids[0], -1)
	b1, _ := m.Add(Node{Text: "b1"}, kids[1], -1)
	if err := m.SetTasks(root, true); err != nil {
		t.Fatal(err)
	}
	m.Update(a1, func(n *Node) { n.Done = true })
	m.Update(b1, func(n *Node) { n.Done = true })
	if d, n := m.Progress(m.Node(kids[0])); d != 1 || n != 3 {
		t.Fatalf("the first branch: %d of %d, want 1 of 3", d, n)
	}
	if d, n := m.Progress(m.Node(root)); d != 2 || n != 4 {
		t.Fatalf("the root: %d of %d, want 2 of 4", d, n)
	}
	// Ticking a branch ticks everything under it, as one step.
	m.ToggleDone(kids[0])
	if d, n := m.Progress(m.Node(root)); d != 4 || n != 4 {
		t.Fatalf("after ticking the first branch the root is %d of %d", d, n)
	}
	m.Undo()
	if d, _ := m.Progress(m.Node(root)); d != 2 {
		t.Fatal("undo did not take the ticks back")
	}
	// Folded, the counts still hold.
	m.Update(kids[0], func(n *Node) { n.Folded = true })
	if d, n := m.Progress(m.Node(root)); d != 2 || n != 4 {
		t.Fatalf("folded: %d of %d", d, n)
	}
	// A task's box stands before its words.
	box, ok := m.TaskBox(m.Node(a1))
	tr, _, _ := m.TextRect(m.Node(a1))
	if !ok || box.X1 > tr.X0 {
		t.Fatalf("the box %v is not before the words %v", box, tr)
	}
	// Outlines carry tasks both ways.
	out := m.Outline(root)
	if !strings.Contains(out, "- [x] a1") || !strings.Contains(out, "- [ ] a2") {
		t.Fatalf("outline:\n%s", out)
	}
	m2 := newMap()
	tops, _ := m2.AddOutline(out, 0, -1, 0, 0)
	if d, n := m2.Progress(m2.Node(tops[0])); d != 2 || n != 4 {
		t.Fatalf("read back from the outline: %d of %d", d, n)
	}
	data, _ := Encode(m, "")
	got, _ := Load(bytes.NewReader(data), "", SimpleMeasurer{})
	if !got.Node(a1).Task || !got.Node(a1).Done {
		t.Fatal("the file lost the tasks")
	}
}
