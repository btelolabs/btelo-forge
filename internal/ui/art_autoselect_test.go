package ui

import (
	"image"
	"testing"

	"github.com/btelolabs/btelo-forge/internal/gfx"
)

// Auto-select: a press chooses the outermost of what is there and a drag moves it, an artboard with the move tool
// too; clicks go a level deeper each; a press at the chosen layer's depth chooses there; off, the chosen layer
// moves whatever is pressed.
func TestMoveToolAutoSelect(t *testing.T) {
	x, ad := canvasHarness(t)
	d := ad.d
	d.Frame = image.Rectangle{}
	board, _ := d.AddBoard(image.Rect(-400, 0, -100, 250), "Board")
	g := d.AddGroup("Group")
	a := d.AddVector(solidSquare(-380, 20, 60, red), "A")
	d.Choose(g.ID)
	b := d.AddVector(solidSquare(-280, 20, 60, blue), "B")
	ad.zoom, ad.panX, ad.panY, ad.fit = 1, -250, 125, false
	ad.changedAll()
	d.Choose(d.Layers()[len(d.Layers())-1].ID) // the background, outside the board
	x.key('V', false)
	click := func(cx, cy float64) {
		sx, sy := x.viewPoint(ad, cx, cy)
		x.drag(sx, sy, sx, sy)
	}
	click(-350, 50)
	if d.Active != board.ID {
		t.Fatalf("the first click chose %d, not the board", d.Active)
	}
	// A drag moves the board, with what is in it.
	x.dragWith(ad, [2]float64{-350, 50}, [2]float64{-340, 60}, false, false)
	if got, _ := d.Board(board.ID); got.Rect != image.Rect(-390, 10, -90, 260) || d.UndoName() != "Move artboard" {
		t.Fatalf("the board went to %v (%q)", got.Rect, d.UndoName())
	}
	click(-340, 60)
	if d.Active != g.ID {
		t.Fatalf("a click on the chosen board chose %d, not the group", d.Active)
	}
	click(-340, 60)
	if d.Active != a.ID {
		t.Fatalf("a click on the chosen group chose %d, not the layer", d.Active)
	}
	// At the layer's depth: a press on the other square chooses it, and a drag moves it alone.
	x.dragWith(ad, [2]float64{-240, 60}, [2]float64{-230, 60}, false, false)
	if d.Active != b.ID || d.Node(b.ID).Vector.M.C != -260 || d.Node(a.ID).Vector.M.C != -370 {
		t.Errorf("after pressing the other square: active %d, at %v and %v", d.Active, d.Node(b.ID).Vector.M.C, d.Node(a.ID).Vector.M.C)
	}
	// Off: the chosen layer moves, wherever the press is.
	x.a.settings.Paint.NoAutoSelect = true
	x.dragWith(ad, [2]float64{-340, 60}, [2]float64{-330, 60}, false, false)
	if d.Active != b.ID || d.Node(b.ID).Vector.M.C != -250 {
		t.Errorf("without auto-select: active %d, the chosen square at %v", d.Active, d.Node(b.ID).Vector.M.C)
	}
	x.frame(Input{})
	x.shot("canvas-auto-select")
}

// A press with the brush on another artboard paints on that board's layer, the one last chosen there; an empty
// board gets a layer of its own.
func TestPaintingGoesToTheBoardUnderThePointer(t *testing.T) {
	x, ad := canvasHarness(t)
	d := ad.d
	d.Frame = image.Rectangle{}
	d.AddBoard(image.Rect(-400, 0, -250, 150), "B1")
	l1 := d.AddLayer("L1")
	d.AddBoard(image.Rect(-200, 0, -50, 150), "B2")
	l2 := d.AddLayer("L2")
	b3, _ := d.AddBoard(image.Rect(-400, 200, -250, 350), "B3")
	ad.zoom, ad.panX, ad.panY, ad.fit = 1, -220, 170, false
	d.Choose(l1.ID)
	x.key('B', false)
	paint := func(cx, cy float64) {
		sx, sy := x.viewPoint(ad, cx, cy)
		x.drag(sx, sy, sx+5, sy)
	}
	paint(-120, 70)
	if d.Active != l2.ID || d.Node(l2.ID).Pixels.Empty() || !d.Node(l1.ID).Pixels.Empty() {
		t.Fatalf("painting on B2 chose %d", d.Active)
	}
	paint(-320, 70)
	if d.Active != l1.ID || d.Node(l1.ID).Pixels.Empty() {
		t.Fatalf("painting back on B1 chose %d", d.Active)
	}
	layers := len(d.Layers())
	paint(-320, 270)
	if len(d.Layers()) != layers+1 || d.BoardOf(d.Active) != b3.ID || d.Node(d.Active).Pixels.Empty() {
		t.Errorf("painting on the empty B3: %d layers, chosen in board %d", len(d.Layers()), d.BoardOf(d.Active))
	}
}

// layerRowAt is where the layers panel puts row i, while its list is not scrolled and a node is chosen: below the
// colour panel, the tabs, the blend mode and the opacity, rows 36 high.
func (x *harness) layerRowAt(i int) gfx.Rect {
	u := &x.a.u
	_, layers := x.a.sideLayout(x.a.artLayout(x.a.layout().body).side)
	top := layers.Y + u.px(26) + u.px(34) + u.px(32)
	return gfx.Rect{X: layers.X, Y: top + float32(i)*u.px(36), W: layers.W, H: u.px(36)}
}

// Ctrl+click on a layer in the panel selects what it shows; with Alt, takes that from the selection. Shift+click
// chooses layers besides the chosen one.
func TestCtrlClickLoadsALayersSelection(t *testing.T) {
	x, ad := canvasHarness(t)
	d := ad.d
	top := d.AddVector(solidSquare(10, 10, 50, red), "Top")
	d.AddVector(solidSquare(100, 10, 50, blue), "Second")
	d.Choose(top.ID)
	x.frame(Input{})
	row := x.layerRowAt(1) // Top, under Second
	press := func(ctrl, shift, alt bool) {
		cx, cy := row.X+row.W/2, row.Y+row.H/2
		x.mx, x.my = cx, cy
		x.frame(Input{})
		x.frame(Input{Down: true, Ctrl: ctrl, Shift: shift, Alt: alt, Pointer: []Pointer{{X: cx, Y: cy, Down: true}}})
		x.frame(Input{Pointer: []Pointer{{X: cx, Y: cy}}})
	}
	press(true, false, false)
	if d.Selection == nil || d.Selection.At(30, 30) != 255 || d.Selection.At(120, 30) != 0 {
		t.Fatalf("Ctrl+click did not select the layer's content: %v", d.Selection)
	}
	press(true, false, true)
	if d.Selection != nil && d.Selection.At(30, 30) != 0 {
		t.Error("Ctrl+Alt+click should take it away")
	}
	d.Choose(d.Layers()[0].ID)
	press(false, true, false)
	if len(ad.picked) != 1 || ad.picked[0] != top.ID {
		t.Errorf("Shift+click picked %v", ad.picked)
	}
}

// Ctrl+click on a layer's thumbnail selects what it shows too, as on its name, without choosing the layer; on its
// mask's thumbnail, selects by the mask.
func TestCtrlClickOnThumbnailsLoadsSelections(t *testing.T) {
	x, ad := canvasHarness(t)
	d := ad.d
	u := &x.a.u
	top := d.AddVector(solidSquare(10, 10, 50, red), "Top")
	d.Select(image.Rect(0, 0, 140, 140)) // the mask shows all the square, and more
	if err := d.AddMask(); err != nil {
		t.Fatal(err)
	}
	d.Deselect()
	second := d.AddVector(solidSquare(100, 10, 50, blue), "Second")
	x.frame(Input{})
	row := x.layerRowAt(1) // Top, under Second
	press := func(cx float32, shift bool) {
		cy := row.Y + row.H/2
		x.mx, x.my = cx, cy
		x.frame(Input{})
		x.frame(Input{Down: true, Ctrl: true, Shift: shift, Pointer: []Pointer{{X: cx, Y: cy, Down: true}}})
		x.frame(Input{Pointer: []Pointer{{X: cx, Y: cy}}})
	}
	thumb := row.X + u.px(4+24+2) + u.px(34)/2 // after the eye
	press(thumb, false)
	if d.Selection == nil || d.Selection.At(30, 30) != 255 || d.Selection.At(120, 30) != 0 || d.Selection.At(100, 100) != 0 {
		t.Fatalf("Ctrl+click on the thumbnail did not select the layer's content")
	}
	if d.Active != second.ID || d.EditMask {
		t.Errorf("Ctrl+click on the thumbnail chose layer %d (mask %v), want %d kept", d.Active, d.EditMask, second.ID)
	}
	mask := thumb + u.px(34+6)
	press(mask, true) // with Shift, added to the selection
	if d.Selection == nil || d.Selection.At(30, 30) != 255 || d.Selection.At(100, 100) != 255 || d.Selection.At(200, 200) != 0 {
		t.Fatalf("Ctrl+Shift+click on the mask thumbnail did not add the mask to the selection")
	}
	if d.Active != second.ID || d.EditMask {
		t.Errorf("Ctrl+click on the mask thumbnail chose layer %d (mask %v)", d.Active, d.EditMask)
	}
	d.Select(image.Rect(300, 200, 320, 220))
	press(mask, false)
	if d.Selection == nil || d.Selection.At(310, 210) != 0 || d.Selection.At(100, 100) != 255 || d.Selection.At(200, 200) != 0 {
		t.Errorf("Ctrl+click on the mask thumbnail should select by the mask alone")
	}
	x.click(gfx.Rect{X: thumb - 2, Y: row.Y + row.H/2 - 2, W: 4, H: 4})
	if d.Active != top.ID {
		t.Errorf("a plain click on the thumbnail should still choose the layer: %d", d.Active)
	}
}
