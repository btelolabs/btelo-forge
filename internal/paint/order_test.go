package paint

import (
	"errors"
	"image"
	"slices"
	"testing"
)

// names lists the names under g, top first, a group's own children in brackets after it.
func names(g *Node) []string {
	var out []string
	for _, c := range g.Children {
		out = append(out, c.Name)
		if c.IsGroup() {
			out = append(out, "[")
			out = append(out, names(c)...)
			out = append(out, "]")
		}
	}
	return out
}

// stack is a picture with layers A to D, A at the top, over the background.
func stack(t *testing.T) (*Doc, map[string]*Node) {
	t.Helper()
	d := New("Background")
	byName := map[string]*Node{"Background": d.Node(d.Active)}
	for _, name := range []string{"D", "C", "B", "A"} {
		byName[name] = d.AddLayer(name)
	}
	if got := names(d.Root); !slices.Equal(got, []string{"A", "B", "C", "D", "Background"}) {
		t.Fatalf("the stack is %v", got)
	}
	return d, byName
}

// Several chosen layers go into one group, in the place of the topmost of them, in their order, as one step.
func TestGroupSeveralLayers(t *testing.T) {
	d, n := stack(t)
	steps, _ := d.History()
	g, err := d.Group("G", []int{n["D"].ID, n["B"].ID, 9999})
	if err != nil {
		t.Fatal(err)
	}
	if got := names(d.Root); !slices.Equal(got, []string{"A", "G", "[", "B", "D", "]", "C", "Background"}) {
		t.Errorf("after grouping B and D: %v", got)
	}
	if d.Active != g.ID || g.Blend != BlendPassThrough || !g.Open {
		t.Errorf("the group: chosen %d, blend %v, open %v", d.Active, g.Blend, g.Open)
	}
	if now, _ := d.History(); len(now) != len(steps)+1 || d.UndoName() != "Group" {
		t.Errorf("steps %v", now)
	}
	d.Undo()
	if got := names(d.Root); !slices.Equal(got, []string{"A", "B", "C", "D", "Background"}) {
		t.Errorf("undo gave %v", got)
	}
	// A layer in a chosen group goes along with the group; a layer from another group joins it.
	inner, _ := d.Group("Inner", []int{n["B"].ID})
	outer, err := d.Group("Outer", []int{inner.ID, n["B"].ID, n["D"].ID})
	if err != nil {
		t.Fatal(err)
	}
	if got := names(d.Root); !slices.Equal(got, []string{"A", "Outer", "[", "Inner", "[", "B", "]", "D", "]", "C", "Background"}) {
		t.Errorf("grouping a group with a layer in it: %v", got)
	}
	if _, err := d.Group("None", []int{9999}); !errors.Is(err, ErrNoLayer) {
		t.Errorf("grouping nothing: %v", err)
	}
	d.Choose(outer.ID)
	if g, err := d.GroupChosen("Again"); err != nil || g.Children[0] != outer {
		t.Errorf("grouping the chosen group: %v", err)
	}
}

// Dragged layers go above or below another, or into a group at its top, keeping their order, as one step.
func TestMoveNodes(t *testing.T) {
	d, n := stack(t)
	move := func(ids []int, target int, where Place, want ...string) {
		t.Helper()
		if err := d.MoveNodes(ids, target, where); err != nil {
			t.Fatal(err)
		}
		if got := names(d.Root); !slices.Equal(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	}
	move([]int{n["A"].ID}, n["C"].ID, PlaceBelow, "B", "C", "A", "D", "Background")
	if d.UndoName() != "Move layer" {
		t.Errorf("the step is %q", d.UndoName())
	}
	d.Undo()
	move([]int{n["D"].ID, n["A"].ID}, n["B"].ID, PlaceAbove, "A", "D", "B", "C", "Background")
	move([]int{n["Background"].ID}, n["A"].ID, PlaceAbove, "Background", "A", "D", "B", "C")
	d.Undo()
	d.Undo()
	g, _ := d.Group("G", []int{n["C"].ID})
	move([]int{n["A"].ID, n["D"].ID}, g.ID, PlaceInto, "B", "G", "[", "A", "D", "C", "]", "Background")
	move([]int{n["C"].ID}, n["B"].ID, PlaceAbove, "C", "B", "G", "[", "A", "D", "]", "Background")
	// Moving a group moves what is in it.
	move([]int{g.ID, n["A"].ID}, n["Background"].ID, PlaceBelow, "C", "B", "Background", "G", "[", "A", "D", "]")

	// Where they cannot go: into or beside themselves, into a layer.
	steps, _ := d.History()
	for _, c := range []struct {
		ids    []int
		target int
		where  Place
		err    error
	}{
		{[]int{g.ID}, n["A"].ID, PlaceAbove, ErrMoveInside},
		{[]int{g.ID}, g.ID, PlaceInto, ErrMoveInside},
		{[]int{n["B"].ID, n["C"].ID}, n["C"].ID, PlaceBelow, ErrMoveInside},
		{[]int{n["B"].ID}, n["C"].ID, PlaceInto, ErrNotGroup},
		{[]int{9999}, n["C"].ID, PlaceAbove, ErrNoLayer},
		{[]int{n["B"].ID}, 9999, PlaceAbove, ErrNoLayer},
	} {
		if err := d.CanMoveNodes(c.ids, c.target, c.where); !errors.Is(err, c.err) {
			t.Errorf("moving %v to %d (%d): %v, want %v", c.ids, c.target, c.where, err, c.err)
		}
		if err := d.MoveNodes(c.ids, c.target, c.where); !errors.Is(err, c.err) {
			t.Errorf("moving %v to %d (%d): %v, want %v", c.ids, c.target, c.where, err, c.err)
		}
	}
	// Dropped where it already is, it makes no step.
	if err := d.MoveNodes([]int{n["C"].ID}, n["B"].ID, PlaceAbove); err != nil {
		t.Fatal(err)
	}
	if now, _ := d.History(); len(now) != len(steps) {
		t.Errorf("a move to the same place made a step: %v", now)
	}
}

// An artboard stays at the top level, and a smart object does not go into a copy of itself.
func TestMoveNodesKeepsBoardsAndSmartObjectsWhole(t *testing.T) {
	d, n := stack(t)
	b, err := d.AddBoard(image.Rect(0, 0, 100, 100), "Board")
	if err != nil {
		t.Fatal(err)
	}
	g, _ := d.Group("G", []int{n["C"].ID})
	if err := d.MoveNodes([]int{b.ID}, g.ID, PlaceInto); !errors.Is(err, ErrBoardInside) {
		t.Errorf("a board into a group: %v", err)
	}
	if err := d.MoveNodes([]int{b.ID}, n["C"].ID, PlaceAbove); !errors.Is(err, ErrBoardInside) {
		t.Errorf("a board beside a layer in a group: %v", err)
	}
	if err := d.MoveNodes([]int{b.ID}, n["Background"].ID, PlaceBelow); err != nil || d.Root.Children[len(d.Root.Children)-1].ID != b.ID {
		t.Errorf("a board to the bottom: %v", err)
	}
	if _, err := d.Group("Boards", []int{b.ID, n["A"].ID}); !errors.Is(err, ErrBoardInside) {
		t.Errorf("grouping a board: %v", err)
	}

	sd, a, c := smartPicture(t)
	sd.SetOpenGroup(c.ID, true)
	if err := sd.MoveNodes([]int{a.ID}, c.ID, PlaceInto); !errors.Is(err, ErrSmartInside) {
		t.Errorf("a smart object into its copy: %v", err)
	}
	if err := sd.MoveNodes([]int{a.ID}, c.Children[0].ID, PlaceBelow); !errors.Is(err, ErrSmartInside) {
		t.Errorf("a smart object beside a layer in its copy: %v", err)
	}
}
