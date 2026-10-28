package paint

import (
	"errors"
	"image"
	"image/color"
	"slices"
	"testing"
)

func TestPickAt(t *testing.T) {
	d := New("Background")
	d.Paper = false
	board, _ := d.AddBoard(image.Rect(0, 0, 300, 200), "Board")
	g := d.AddGroup("Group")
	inner := d.AddVector(Vector{Kind: VectorRect, Size: Pt{50, 50}, M: Translate(20, 20), Fill: solidRed}, "Square")
	d.Choose(board.ID)
	d.AddLayer("Ink")
	ink := d.Node(d.Active)
	d.Select(image.Rect(200, 100, 280, 180))
	d.Fill(color.NRGBA{B: 255, A: 255})
	d.Deselect()
	for _, c := range []struct {
		at   image.Point
		want []int
	}{
		{image.Pt(30, 30), []int{board.ID, g.ID, inner.ID}},
		{image.Pt(250, 150), []int{board.ID, ink.ID}},
		{image.Pt(150, 150), []int{board.ID}}, // its background
		{image.Pt(400, 150), nil},
	} {
		if got := d.PickAt(c.at); !slices.Equal(got, c.want) {
			t.Errorf("at %v: %v, want %v", c.at, got, c.want)
		}
	}
	// A hidden layer is passed over; a masked part is too.
	d.SetVisible(inner.ID, false)
	if got := d.PickAt(image.Pt(30, 30)); !slices.Equal(got, []int{board.ID}) {
		t.Errorf("with the square hidden: %v", got)
	}
	d.Choose(ink.ID)
	d.Select(image.Rect(240, 100, 280, 180))
	d.AddMask()
	d.Deselect()
	if got := d.PickAt(image.Pt(250, 150)); !slices.Equal(got, []int{board.ID, ink.ID}) {
		t.Errorf("where the mask shows the ink: %v", got)
	}
	if got := d.PickAt(image.Pt(210, 150)); !slices.Equal(got, []int{board.ID}) {
		t.Errorf("where the mask hides the ink: %v", got)
	}
}

// Ctrl+click on a layer selects how opaque it is; on a group, what its layers show together.
func TestSelectContent(t *testing.T) {
	d := New("Background")
	d.Paper = false
	g := d.AddGroup("Group")
	a := d.AddVector(Vector{Kind: VectorRect, Size: Pt{50, 50}, M: Translate(0, 0), Fill: VectorPaint{Kind: PaintSolid, Color: color.NRGBA{R: 255, A: 128}}}, "Half")
	d.Choose(g.ID)
	d.AddVector(Vector{Kind: VectorRect, Size: Pt{50, 50}, M: Translate(100, 0), Fill: solidRed}, "Solid")
	d.SetOpacity(a.ID, 0.2) // its opacity is left out
	if err := d.SelectContent(a.ID, SelectNew); err != nil {
		t.Fatal(err)
	}
	if v := d.Selection.At(25, 25); v < 126 || v > 130 {
		t.Errorf("the half opaque layer selects %d", v)
	}
	if d.Selection.At(125, 25) != 0 {
		t.Error("only the layer clicked")
	}
	d.SelectContent(g.ID, SelectNew)
	if d.Selection.At(125, 25) != 255 || d.Selection.At(25, 25) == 0 || d.Selection.At(75, 25) != 0 {
		t.Error("the group selects both its layers")
	}
	d.SelectContent(g.ID, SelectSubtract)
	if d.Selection != nil && d.Selection.At(125, 25) != 0 {
		t.Error("subtracting the group")
	}
}

// Ctrl+click on a mask's thumbnail selects by the layer mask, or inside the vector mask, whether or not it is on.
func TestSelectByMasks(t *testing.T) {
	d := New("Layer 1")
	id := d.Active
	if err := d.SelectMask(id, SelectNew); !errors.Is(err, ErrNoMask) {
		t.Errorf("no mask: %v", err)
	}
	if err := d.SelectVectorMask(id, SelectNew); !errors.Is(err, ErrNoClip) {
		t.Errorf("no vector mask: %v", err)
	}
	d.Select(image.Rect(10, 10, 20, 20))
	if err := d.AddMask(); err != nil {
		t.Fatal(err)
	}
	d.SetMaskOff(id, true)
	d.Deselect()
	if err := d.SelectMask(id, SelectNew); err != nil || d.Selection == nil || d.Selection.At(15, 15) != 255 || d.Selection.At(25, 25) != 0 {
		t.Fatalf("selecting by the mask: %v", err)
	}
	if err := d.AddShape(id, Vector{Kind: VectorRect, Size: Pt{X: 10, Y: 10}, M: Translate(30, 30)}, OpAdd); err != nil {
		t.Fatal(err)
	}
	if err := d.SelectVectorMask(id, SelectAdd); err != nil || d.Selection.At(15, 15) != 255 || d.Selection.At(35, 35) != 255 || d.Selection.At(45, 45) != 0 {
		t.Fatalf("adding inside the vector mask: %v", err)
	}
	if d.UndoName() != "Select" {
		t.Errorf("one step each, called %q", d.UndoName())
	}
}
