package ui

import (
	"image"
	"image/color"
	"testing"
)

// dragCtrl drags on the canvas with Ctrl held.
func (x *harness) dragCtrl(ad *artDoc, from, to [2]float64) {
	fx, fy := x.viewPoint(ad, from[0], from[1])
	tx, ty := x.viewPoint(ad, to[0], to[1])
	x.mx, x.my = fx, fy
	x.frame(Input{Ctrl: true})
	x.frame(Input{Ctrl: true, Down: true, Pointer: []Pointer{{X: fx, Y: fy, Down: true}}})
	for i := 1; i <= 8; i++ {
		x.mx, x.my = fx+(tx-fx)*float32(i)/8, fy+(ty-fy)*float32(i)/8
		x.frame(Input{Ctrl: true, Down: true})
	}
	x.frame(Input{Ctrl: true, Pointer: []Pointer{{X: tx, Y: ty}}})
}

func TestCanvasDistortPerspectiveAndWarp(t *testing.T) {
	x, ad := canvasHarness(t)
	a := x.a
	d := ad.d
	n := d.AddLayer("Square")
	d.Select(image.Rect(100, 75, 300, 225))
	d.Fill(color.NRGBA{R: 40, G: 120, B: 230, A: 255})
	d.Deselect()
	ad.changedAll()
	px := func(x, y int) uint8 { return d.Node(n.ID).Pixels.At(x, y).A }
	// Ctrl+drag a corner in Free Transform distorts it.
	x.key('T', true)
	if ad.xf == nil {
		t.Fatal("Ctrl+T did not start Free Transform")
	}
	x.dragCtrl(ad, [2]float64{300, 75}, [2]float64{340, 40})
	if ad.xf.mode != xfDistort {
		t.Fatalf("Ctrl+drag left the mode at %d", ad.xf.mode)
	}
	if px(330, 50) == 0 || px(300, 74) != 255 && px(320, 60) == 0 {
		t.Errorf("the corner did not move out: %d", px(330, 50))
	}
	x.key(keyEnter, false)
	if ad.xf != nil || d.UndoName() != "Distort" {
		t.Fatalf("Enter: %v (%q)", ad.xf != nil, d.UndoName())
	}
	d.Undo()
	ad.changedAll()
	// Perspective: moving the top left corner in narrows the top from both ends.
	a.setTransformMode(ad, xfPerspective)
	x.dragWith(ad, [2]float64{100, 75}, [2]float64{150, 75}, false, false)
	if q := ad.xf.quad; q[0][0] != 150 || q[1][0] != 250 || q[3][0] != 100 {
		t.Errorf("the perspective quad %v", q)
	}
	if px(110, 78) != 0 || px(200, 78) != 255 || px(105, 220) != 255 {
		t.Errorf("perspective: %d %d %d", px(110, 78), px(200, 78), px(105, 220))
	}
	x.frame(Input{})
	x.shot("canvas-perspective")
	x.key(keyEnter, false)
	if d.UndoName() != "Perspective" {
		t.Errorf("the step %q", d.UndoName())
	}
	d.Undo()
	ad.changedAll()
	// Warp: pulling the bottom row's middle points down bows the bottom.
	a.setTransformMode(ad, xfWarp)
	if ad.xf.mode != xfWarp {
		t.Fatal("no warp")
	}
	x.dragWith(ad, [2]float64{166.67, 225}, [2]float64{166.67, 270}, false, false)
	x.dragWith(ad, [2]float64{233.33, 225}, [2]float64{233.33, 270}, false, false)
	if px(200, 245) != 255 || px(102, 240) != 0 {
		t.Errorf("warp: middle %d, corner %d", px(200, 245), px(102, 240))
	}
	x.frame(Input{})
	x.shot("canvas-warp")
	// From a warp, the box modes are not offered: it stays a warp until applied.
	a.setTransformMode(ad, xfDistort)
	if ad.xf.mode != xfWarp {
		t.Error("a warp became a distort")
	}
	x.key(keyEnter, false)
	if d.UndoName() != "Warp" {
		t.Errorf("the step %q", d.UndoName())
	}
}
