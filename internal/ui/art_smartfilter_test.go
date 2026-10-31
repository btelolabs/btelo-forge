package ui

import (
	"image"
	"image/color"
	"testing"

	"github.com/btelolabs/btelo-forge/internal/paint"
)

func heldCanvas(t *testing.T) (*harness, *artDoc) {
	t.Helper()
	x, ad := canvasHarness(t)
	d := ad.d
	d.AddLayer("Photo")
	d.Select(image.Rect(100, 80, 300, 220))
	if err := d.Fill(color.NRGBA{B: 200, A: 255}); err != nil {
		t.Fatal(err)
	}
	d.Deselect()
	if _, err := d.ConvertToSmart("Smart"); err != nil {
		t.Fatal(err)
	}
	ad.changedAll()
	return x, ad
}

func TestAFilterOnASmartObjectIsHeldNotApplied(t *testing.T) {
	x, ad := heldCanvas(t)
	a := x.a
	d := ad.d
	n := d.Node(d.Active)
	before := n.Children[0].Pixels.At(100, 150)
	// The Filter menu's command for a smart object.
	a.openAdjust(ad, filterBlur)
	if a.adjust == nil || a.adjust.smart == nil {
		t.Fatal("no smart filter dialog")
	}
	a.adjust.values[0] = 9
	x.frame(Input{})
	x.shot("canvas-smart-filter")
	x.frame(Input{Keys: []Key{{Code: keyEnter}}})
	n = d.Node(d.Active)
	if n.Filters == nil || len(n.Filters.List) != 1 || n.Filters.List[0].Values[0] != 9 || d.UndoName() != "Smart filter" {
		t.Fatalf("filters %+v, step %q", n.Filters, d.UndoName())
	}
	if n.Children[0].Pixels.At(100, 150) != before {
		t.Error("the pixels changed")
	}
	if c := d.Composite(d.Frame).RGBAAt(100, 150); c.B == 200 {
		t.Errorf("the edge is not blurred: %v", c)
	}
	// Cancel takes a new one away again.
	a.openAdjust(ad, adjustVibrance)
	x.frame(Input{Keys: []Key{{Code: keyEscape}}})
	if got := len(d.Node(d.Active).Filters.List); got != 1 {
		t.Errorf("%d filters after Cancel", got)
	}
	// The list: change it, turn it off, take it away.
	a.smartList = ad
	x.frame(Input{})
	x.shot("canvas-smart-filter-list")
	a.smartList = nil
	a.editSmartFilter(ad, 0)
	if a.adjust.values[0] != 9 {
		t.Fatalf("its settings came back as %v", a.adjust.values)
	}
	a.adjust.values[0] = 3
	x.frame(Input{Keys: []Key{{Code: keyEnter}}})
	if v := d.Node(d.Active).Filters.List[0].Values[0]; v != 3 {
		t.Errorf("radius %v", v)
	}
	a.editSmartFilter(ad, 0)
	a.adjust.values[0] = 40
	x.frame(Input{})
	x.frame(Input{Keys: []Key{{Code: keyEscape}}})
	if v := d.Node(d.Active).Filters.List[0].Values[0]; v != 3 {
		t.Errorf("Cancel left radius %v", v)
	}
}

func TestEveryDialogMakesASmartFilterThatComesBack(t *testing.T) {
	x, ad := heldCanvas(t)
	a := x.a
	for kind := range a.adjustKinds() {
		a.openSmartFilter(ad, kind)
		if a.adjust == nil {
			t.Fatalf("kind %d did not open", kind)
		}
		x.frame(Input{Keys: []Key{{Code: keyEnter}}})
		f := ad.d.Node(ad.d.Active).Filters
		if dialogKindOf(f.List[len(f.List)-1]) != kind {
			t.Errorf("kind %d came back as %d", kind, dialogKindOf(f.List[len(f.List)-1]))
		}
		if err := ad.d.RemoveSmartFilter(ad.d.Active, len(f.List)-1); err != nil {
			t.Fatal(err)
		}
	}
}

func TestAPlainLayerStillGetsTheFilterOnItsPixels(t *testing.T) {
	x, ad := canvasHarness(t)
	ad.d.AddLayer("Photo")
	x.a.openAdjust(ad, filterBlur)
	if x.a.adjust == nil || x.a.adjust.smart != nil || x.a.adjust.s == nil {
		t.Fatal("not a pixel filter")
	}
	_ = paint.SmartBlur
}
