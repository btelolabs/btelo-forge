package ui

import (
	"image"
	"image/color"
	"testing"

	"github.com/btelolabs/btelo-video-cut/internal/gfx"
	"github.com/btelolabs/btelo-video-cut/internal/paint"
)

// dragWith drags on the canvas from one picture point to another with modifier keys held throughout.
func (x *harness) dragWith(ad *artDoc, from, to [2]float64, shift, alt bool) {
	fx, fy := x.viewPoint(ad, from[0], from[1])
	tx, ty := x.viewPoint(ad, to[0], to[1])
	x.mx, x.my = fx, fy
	x.frame(Input{Shift: shift, Alt: alt})
	x.frame(Input{Down: true, Shift: shift, Alt: alt, Pointer: []Pointer{{X: fx, Y: fy, Down: true}}})
	for i := 1; i <= 8; i++ {
		f := float32(i) / 8
		x.mx, x.my = fx+(tx-fx)*f, fy+(ty-fy)*f
		x.frame(Input{Down: true, Shift: shift, Alt: alt})
	}
	x.frame(Input{Shift: shift, Alt: alt, Pointer: []Pointer{{X: tx, Y: ty}}})
}

func TestCanvasSelectionTools(t *testing.T) {
	x, ad := canvasHarness(t)
	a := x.a
	d := ad.d
	sel := func(px, py int) uint8 {
		if d.Selection == nil {
			return 0
		}
		return d.Selection.At(px, py)
	}
	// Shift+M switches the marquee to ellipses.
	x.frame(Input{Keys: []Key{{Code: 'M', Shift: true}}})
	if a.artTool != toolMarquee || !a.marqueeEllipse {
		t.Fatalf("Shift+M: tool %d, ellipse %v", a.artTool, a.marqueeEllipse)
	}
	x.dragWith(ad, [2]float64{100, 50}, [2]float64{300, 250}, false, false)
	if sel(200, 150) != 255 || sel(105, 55) != 0 {
		t.Fatalf("ellipse: centre %d, corner %d", sel(200, 150), sel(105, 55))
	}
	x.shot("canvas-ellipse")
	// Alt subtracts a rectangle across it, Shift adds one.
	x.key('M', false)
	a.marqueeEllipse = false
	x.dragWith(ad, [2]float64{0, 140}, [2]float64{400, 160}, false, true)
	if sel(200, 150) != 0 || sel(200, 100) != 255 {
		t.Errorf("after Alt: %d %d", sel(200, 150), sel(200, 100))
	}
	x.dragWith(ad, [2]float64{10, 10}, [2]float64{50, 50}, true, false)
	if sel(30, 30) != 255 || sel(200, 100) != 255 {
		t.Errorf("after Shift: %d %d", sel(30, 30), sel(200, 100))
	}
	x.shot("canvas-selection-modes")
	// The lasso.
	x.key('L', false)
	ad.d.Deselect()
	// Round a triangle: the lasso closes the outline back to where it began.
	pts := [][2]float64{{20, 200}, {380, 200}, {200, 290}}
	fx, fy := x.viewPoint(ad, pts[0][0], pts[0][1])
	x.mx, x.my = fx, fy
	x.frame(Input{})
	x.frame(Input{Down: true, Pointer: []Pointer{{X: fx, Y: fy, Down: true}}})
	for i := 1; i < len(pts); i++ {
		for s := 1; s <= 8; s++ {
			f := float64(s) / 8
			x.mx, x.my = x.viewPoint(ad, pts[i-1][0]+(pts[i][0]-pts[i-1][0])*f, pts[i-1][1]+(pts[i][1]-pts[i-1][1])*f)
			x.frame(Input{Down: true})
		}
	}
	x.frame(Input{Pointer: []Pointer{{X: x.mx, Y: x.my}}})
	if sel(200, 220) != 255 || sel(30, 280) != 0 {
		t.Fatalf("lasso: inside %d, outside %d", sel(200, 220), sel(30, 280))
	}
	x.shot("canvas-lasso")
	// The magic wand on a filled square, contiguous: only that square.
	d.Deselect()
	l := d.AddLayer("Shapes")
	d.Select(image.Rect(40, 40, 80, 80))
	d.Fill(color.NRGBA{R: 255, A: 255})
	d.Deselect()
	a.settings.Paint.WandAllLayers = false
	d.Choose(l.ID)
	x.key('W', false)
	px, py := x.viewPoint(ad, 60, 60)
	x.click(rectAt(px-1, py-1, 2, 2))
	if b := d.SelectionBounds(); b != image.Rect(40, 40, 80, 80) {
		t.Errorf("magic wand selected %v", b)
	}
	// Feather from the Select menu's dialog.
	a.openDialog(dialogFeather)
	a.dialog.a = "6"
	x.frame(Input{Keys: []Key{{Code: keyEnter}}})
	x.frame(Input{})
	box := rectCentered(a.screen, 360, 170).Inset(22)
	x.click(rectAt(box.Right()-100, box.Bottom()-34, 100, 34))
	if v := sel(40, 60); v == 0 || v == 255 {
		t.Errorf("after feathering the edge is %d", v)
	}
}

func TestCanvasMoveToolColoursAndMasks(t *testing.T) {
	x, ad := canvasHarness(t)
	a := x.a
	d := ad.d
	l := d.AddLayer("Ink")
	d.Select(image.Rect(10, 10, 60, 60))
	d.Fill(color.NRGBA{B: 255, A: 255})
	d.Deselect()
	steps := len(d.Layers())
	x.key('V', false)
	before := d.UndoName()
	x.dragWith(ad, [2]float64{30, 30}, [2]float64{130, 80}, false, false)
	if c := ad.look(120, 80); c != (color.RGBA{0, 0, 255, 255}) {
		t.Errorf("after moving the square is not at its new place: %v", c)
	}
	if c := ad.look(20, 20); c != (color.RGBA{255, 255, 255, 255}) {
		t.Errorf("the square's old place shows %v", c)
	}
	if d.UndoName() != "Move" || before == "Move" || len(d.Layers()) != steps {
		t.Errorf("the move is %q", d.UndoName())
	}
	// D resets the colours, X swaps them.
	x.key('D', false)
	if a.brush.Color != (color.NRGBA{A: 255}) || a.backgroundColor() != (color.NRGBA{R: 255, G: 255, B: 255, A: 255}) {
		t.Errorf("D: %v %v", a.brush.Color, a.backgroundColor())
	}
	x.key('X', false)
	if a.brush.Color != (color.NRGBA{R: 255, G: 255, B: 255, A: 255}) || a.backgroundColor() != (color.NRGBA{A: 255}) {
		t.Errorf("X: %v %v", a.brush.Color, a.backgroundColor())
	}
	x.key('X', false)
	// A mask from the Layer menu; black paint hides the layer there.
	x.frame(Input{})
	l2 := a.artLayout(x.a.layout().body)
	x.click(x.menuTitle(l2, menuLayer))
	if a.menu == nil {
		t.Fatal("the Layer menu did not open")
	}
	x.shot("canvas-menu-layer")
	x.clickMenuItem(a.u.t("Add layer mask"))
	if d.Node(l.ID).Mask == nil || !d.EditMask {
		t.Fatalf("no mask was added: %+v", d.Node(l.ID))
	}
	a.brush = paint.Brush{Size: 30, Hardness: 1, Opacity: 1, Spacing: 0.1, Flow: 1, Round: 1, Color: color.NRGBA{A: 255}}
	x.key('B', false)
	x.dragWith(ad, [2]float64{110, 70}, [2]float64{140, 70}, false, false)
	if c := ad.look(125, 70); c != (color.RGBA{255, 255, 255, 255}) {
		t.Errorf("the mask did not hide the layer: %v", c)
	}
	// The blend mode from the panel's drop-down.
	x.frame(Input{})
	x.shot("canvas-mask")
	a.dropdownPick(x, "art/layer/blend", a.u.tc("blend", "Multiply"))
	if d.Node(l.ID).Blend != paint.BlendMultiply {
		t.Errorf("blend mode %d", d.Node(l.ID).Blend)
	}
	x.frame(Input{})
	a.u.lang = Chinese
	x.frame(Input{})
	x.frame(Input{})
	x.shot("canvas-ps-zh")
	a.u.lang = English
}

// menuTitle is where the menu bar puts a menu's title, as drawArtMenus lays it out.
func (x *harness) menuTitle(l artLayout, menu int) gfx.Rect {
	u := &x.a.u
	f := u.face(13)
	px := l.menus.X + u.px(8)
	for m, name := range artMenuNames {
		w := f.Measure(u.tc("menu", name)) + u.px(20)
		if m == menu {
			return gfx.Rect{X: px, Y: l.menus.Y + u.px(2), W: w, H: l.menus.H - u.px(4)}
		}
		px += w + u.px(2)
	}
	return gfx.Rect{}
}

// clickMenuItem clicks the open menu's item with that label, as drawMenu lays it out.
func (x *harness) clickMenuItem(label string) {
	x.t.Helper()
	m := x.a.menu
	if m == nil {
		x.t.Fatal("no menu is open")
	}
	rows := x.a.u.menuRows(m.items, x.a.menuBelow(m.items, m.anchor))
	for i, it := range m.items {
		if !it.sep && it.label == label {
			x.click(rows[i])
			return
		}
	}
	x.t.Fatalf("the menu has no %q", label)
}

// clickSubmenuItem points at the open menu's item parent, which opens its submenu, and clicks label in it.
func (x *harness) clickSubmenuItem(parent, label string) {
	x.t.Helper()
	m := x.a.menu
	if m == nil {
		x.t.Fatal("no menu is open")
	}
	rows := x.a.u.menuRows(m.items, x.a.menuBelow(m.items, m.anchor))
	for i, it := range m.items {
		if it.sep || it.label != parent {
			continue
		}
		x.mx, x.my = rows[i].X+rows[i].W/2, rows[i].Y+rows[i].H/2
		x.frame(Input{})
		x.frame(Input{})
		if m.sub != i {
			x.t.Fatalf("pointing at %q did not open its submenu", parent)
		}
		subRows := x.a.u.menuRows(it.sub, x.a.menuBeside(it.sub, rows[i]))
		for j, s := range it.sub {
			if !s.sep && s.label == label {
				// Move across along the row first, as a pointer does, so the submenu stays open.
				x.mx = subRows[j].X + subRows[j].W/2
				x.frame(Input{})
				x.click(subRows[j])
				return
			}
		}
		x.t.Fatalf("the submenu %q has no %q", parent, label)
	}
	x.t.Fatalf("the menu has no %q", parent)
}

// dropdownPick opens the drop-down drawn with id and picks an option, by running the menu's item directly.
func (a *App) dropdownPick(x *harness, id, label string) {
	x.t.Helper()
	x.frame(Input{})
	// The drop-down is a button: find its rectangle the way the layers panel lays it out.
	l := a.artLayout(a.layout().body)
	in := l.side.Inset(a.u.px(14))
	colourEnd := in.Y + a.u.px(250)
	r := rectAt(in.X, colourEnd+a.u.px(14)+a.u.px(26), in.W-a.u.px(40), a.u.px(28))
	x.click(r)
	if !a.menuOpen(id) {
		x.t.Fatalf("the drop-down %s did not open", id)
	}
	x.clickMenuItem(label)
}
