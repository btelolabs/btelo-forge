package ui

import (
	"image/color"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/btelolabs/btelo-forge/internal/paint"
)

// The vector brush: its strokes are shapes of one vector layer, round by default, sized by D and F; each stroke is
// one step.
func TestVectorBrush(t *testing.T) {
	x, ad := canvasHarness(t)
	a, d := x.a, ad.d
	a.brush.Color = color.NRGBA{B: 255, A: 255}
	x.key('P', false)
	if a.artTool != toolPencil || a.vectorBrush().Tip != paint.TipRound {
		t.Fatalf("P chose %v with tip %v", a.artTool, a.vectorBrush().Tip)
	}
	a.setVectorBrushSize(10)
	layers := len(d.Layers())
	stroke := func(x0, y0, x1, y1 float64) {
		fx, fy := x.viewPoint(ad, x0, y0)
		tx, ty := x.viewPoint(ad, x1, y1)
		x.drag(fx, fy, tx, ty)
	}
	stroke(50, 50, 300, 60)
	if len(d.Layers()) != layers+1 {
		t.Fatalf("the first stroke should make a vector layer: %d layers", len(d.Layers()))
	}
	n := d.Node(d.Active)
	if n.Vector == nil || d.UndoName() != "Vector brush" {
		t.Fatalf("the stroke's layer %+v, %q", n, d.UndoName())
	}
	stroke(50, 150, 300, 160)
	if len(d.Layers()) != layers+1 || len(d.Node(n.ID).Vector.Shapes()) != 2 {
		t.Fatalf("the second stroke should join the first's layer: %d layers, %d shapes", len(d.Layers()), len(d.Node(n.ID).Vector.Shapes()))
	}
	ad.changedAll()
	if c := ad.look(175, 55); c.B < 250 || c.R > 5 {
		t.Errorf("the first stroke shows %v", c)
	}
	if c := ad.look(175, 100); c != (color.RGBA{255, 255, 255, 255}) {
		t.Errorf("between the strokes %v", c)
	}
	// About as wide as the brush: 10 px.
	thick := 0
	for y := 40; y < 75; y++ {
		if ad.look(175, y).B > 128 && ad.look(175, y).R < 128 {
			thick++
		}
	}
	if thick < 9 || thick > 12 {
		t.Errorf("the stroke is %d px thick", thick)
	}
	// D and F size it.
	x.key('F', false)
	if s := a.vectorBrush().Size; s <= 10 {
		t.Errorf("F left the vector brush at %v", s)
	}
	x.key('D', false)
	x.key('D', false)
	if s := a.vectorBrush().Size; s >= 10 {
		t.Errorf("D left it at %v", s)
	}
	// Each stroke is a step.
	x.key('Z', true)
	if len(d.Node(n.ID).Vector.Shapes()) != 1 {
		t.Error("undo should take the last stroke away")
	}
	x.frame(Input{})
	x.shot("canvas-vector-brush")
}

// A double-click on a tab names its canvas's file; an edit's is saved under the name.
func TestRenamingATab(t *testing.T) {
	x, ad := canvasHarness(t)
	a := x.a
	d := a.docs[a.current()]
	a.renameTab(d, "My poster")
	if filepath.Base(ad.path) != "My poster"+paint.Ext {
		t.Fatalf("the canvas is at %s", ad.path)
	}
	if _, err := os.Stat(ad.path); err != nil {
		t.Fatalf("the renamed file: %v", err)
	}
	if !strings.Contains(d.title(a), "My poster") {
		t.Errorf("the tab is called %q", d.title(a))
	}
	// A name taken is refused, saying so.
	other := filepath.Join(filepath.Dir(ad.path), "Taken"+paint.Ext)
	os.WriteFile(other, []byte("x"), 0o644)
	a.renameTab(d, "Taken")
	if filepath.Base(ad.path) != "My poster"+paint.Ext || len(a.toasts) == 0 {
		t.Error("renaming onto another file should be refused")
	}
}

// N adds a pixel layer and Shift+N an empty vector layer, which the vector brush (P) then draws on.
func TestNewLayerKeys(t *testing.T) {
	x, ad := canvasHarness(t)
	a, d := x.a, ad.d
	layers := len(d.Layers())
	x.key('N', false)
	if n := d.Node(d.Active); len(d.Layers()) != layers+1 || n.Pixels == nil || d.UndoName() != "New layer" {
		t.Fatalf("N: %d layers, chosen %+v", len(d.Layers()), n)
	}
	if a.artTool == toolPencil {
		t.Error("N should no longer choose the vector brush")
	}
	x.frame(Input{Keys: []Key{{Code: 'N', Shift: true}}})
	v := d.Node(d.Active)
	if len(d.Layers()) != layers+2 || v.Vector == nil || len(v.Vector.Path) != 0 || d.UndoName() != "New vector layer" {
		t.Fatalf("Shift+N: %d layers, chosen %+v, step %q", len(d.Layers()), v, d.UndoName())
	}
	x.key('P', false)
	if a.artTool != toolPencil {
		t.Fatalf("P chose %v", a.artTool)
	}
	fx, fy := x.viewPoint(ad, 50, 50)
	tx, ty := x.viewPoint(ad, 200, 60)
	x.drag(fx, fy, tx, ty)
	if len(d.Layers()) != layers+2 || d.Active != v.ID || len(d.Node(v.ID).Vector.Shapes()) != 1 {
		t.Errorf("the stroke should go on the new vector layer: %d layers, chosen %d", len(d.Layers()), d.Active)
	}
}
