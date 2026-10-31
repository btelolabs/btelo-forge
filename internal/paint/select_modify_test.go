package paint

import (
	"image"
	"image/color"
	"math"
	"testing"
)

func selectedDoc(t *testing.T, r image.Rectangle) *Doc {
	t.Helper()
	d := newDoc(t, 200, 200, color.NRGBA{A: 255})
	d.Select(r)
	return d
}

func TestExpandMovesTheEdgeOutWithRoundCorners(t *testing.T) {
	d := selectedDoc(t, image.Rect(50, 50, 100, 100))
	d.ExpandSelection(10)
	s := d.Selection
	if s.At(40, 75) != 255 || s.At(39, 75) != 0 || s.At(109, 75) != 255 || s.At(110, 75) != 0 {
		t.Errorf("left %d, %d; right %d, %d", s.At(40, 75), s.At(39, 75), s.At(109, 75), s.At(110, 75))
	}
	// Round: a square's corner would reach (40, 40), a radius of 10 reaches 7 along each side.
	if s.At(40, 40) != 0 || s.At(44, 44) != 255 {
		t.Errorf("corner %d, inside the round %d", s.At(40, 40), s.At(44, 44))
	}
	if d.UndoName() != "Expand" {
		t.Errorf("step %q", d.UndoName())
	}
	d.Undo()
	if d.Selection.At(49, 75) != 0 {
		t.Error("undo did not bring the selection back")
	}
}

func TestContractMovesTheEdgeIn(t *testing.T) {
	d := selectedDoc(t, image.Rect(50, 50, 100, 100))
	d.ContractSelection(10)
	if b := d.SelectionBounds(); b != image.Rect(60, 60, 90, 90) {
		t.Errorf("bounds %v", b)
	}
	// Too far: nothing is left, and nothing is selected.
	d.ContractSelection(100)
	if d.Selection != nil {
		t.Error("contracting everything away left a selection")
	}
}

func TestExpandAndContractOfAnInvertedSelection(t *testing.T) {
	d := selectedDoc(t, image.Rect(50, 50, 100, 100))
	d.InvertSelection()
	// Selecting all around a hole: the hole shrinks by what the selection grows.
	d.ExpandSelection(5)
	if s := d.Selection; s.At(52, 75) != 255 || s.At(56, 75) != 0 || s.At(5000, 5000) != 255 {
		t.Errorf("hole's edge %d, %d, far away %d", s.At(52, 75), s.At(56, 75), s.At(5000, 5000))
	}
	d.ContractSelection(5)
	if s := d.Selection; s.At(48, 75) != 255 || s.At(52, 75) != 0 {
		t.Errorf("after contracting %d, %d", s.At(48, 75), s.At(52, 75))
	}
}

func TestExpandKeepsAFeatheredEdgeSoft(t *testing.T) {
	d := selectedDoc(t, image.Rect(50, 50, 150, 150))
	d.FeatherSelection(8)
	before := make([]uint8, 41)
	for i := range before {
		before[i] = d.Selection.At(30+i, 100)
	}
	d.ExpandSelection(6)
	for i, want := range before {
		if got := d.Selection.At(30+i-6, 100); !near(got, want, 9) {
			t.Fatalf("at %d the moved edge is %d, was %d", i, got, want)
		}
	}
}

func TestContractLeavesARoundSelectionRound(t *testing.T) {
	d := newDoc(t, 200, 200, color.NRGBA{A: 255})
	d.SelectShape(d.EllipseMask(image.Rect(40, 40, 160, 160)), SelectNew)
	d.ContractSelection(20)
	for a := 0.0; a < 2*math.Pi; a += 0.2 {
		in, out := d.Selection.At(100+int(35*math.Cos(a)), 100+int(35*math.Sin(a))), d.Selection.At(100+int(44*math.Cos(a)), 100+int(44*math.Sin(a)))
		if in != 255 || out != 0 {
			t.Fatalf("at %.1f: %d at 35, %d at 44", a, in, out)
		}
	}
}

func TestSmoothTakesOffBumpsAndClosesHoles(t *testing.T) {
	d := selectedDoc(t, image.Rect(50, 50, 150, 150))
	d.SelectShape(d.RectMask(image.Rect(150, 100, 152, 102)), SelectAdd)      // a bump
	d.SelectShape(d.RectMask(image.Rect(100, 100, 102, 102)), SelectSubtract) // a pinhole
	d.SmoothSelection(4)
	if s := d.Selection; s.At(151, 101) != 0 || s.At(101, 101) != 255 || s.At(140, 120) != 255 {
		t.Errorf("bump %d, hole %d, inside %d", s.At(151, 101), s.At(101, 101), s.At(140, 120))
	}
}

func TestBorderSelectsABandAlongTheEdge(t *testing.T) {
	d := selectedDoc(t, image.Rect(50, 50, 150, 150))
	d.BorderSelection(10)
	s := d.Selection
	if s.At(45, 100) != 255 || s.At(54, 100) != 255 || s.At(44, 100) != 0 || s.At(55, 100) != 0 || s.At(100, 100) != 0 {
		t.Errorf("band %d %d, outside it %d %d, middle %d", s.At(45, 100), s.At(54, 100), s.At(44, 100), s.At(55, 100), s.At(100, 100))
	}
	// A width of one has no half to take away from inside.
	d = selectedDoc(t, image.Rect(50, 50, 150, 150))
	d.BorderSelection(1)
	if s := d.Selection; s.At(49, 100) != 255 || s.At(50, 100) != 0 {
		t.Errorf("thin band %d, %d", s.At(49, 100), s.At(50, 100))
	}
}

func TestModifyWithoutASelectionDoesNothing(t *testing.T) {
	d := newDoc(t, 20, 20, color.NRGBA{A: 255})
	steps := d.CanUndo()
	d.ExpandSelection(3)
	d.BorderSelection(3)
	if d.Selection != nil || d.CanUndo() != steps {
		t.Error("modifying no selection changed something")
	}
}

func BenchmarkExpand(b *testing.B) {
	img := image.NewAlpha(image.Rect(0, 0, 4000, 3000))
	for i := range img.Pix {
		img.Pix[i] = 255 * uint8((i>>9)&1)
	}
	for b.Loop() {
		dilate(img, 100)
	}
}
