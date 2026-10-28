package ui

import (
	"image"
	"image/color"
	"testing"

	"github.com/btelolabs/btelo-forge/internal/paint"
)

// Leaving a canvas tab in the middle of anything — a stroke, a drag, a transform, typing, the pen's curve, a menu,
// a held key — never brings the app down, there or back in the canvas: every state of the canvas editor is left
// with a new tab, and the pointer and keys go on as a person's would.
func TestLeavingACanvasMidwayNeverCrashes(t *testing.T) {
	for _, c := range []struct {
		name  string
		start func(x *harness, ad *artDoc)
	}{
		{"brush stroke", func(x *harness, ad *artDoc) { x.key('B', false); x.pressAt(ad, 100, 100); x.moveTo(ad, 150, 120, true) }},
		{"eraser", func(x *harness, ad *artDoc) { x.key('E', false); x.pressAt(ad, 100, 100); x.moveTo(ad, 150, 120, true) }},
		{"move", func(x *harness, ad *artDoc) { x.key('V', false); x.pressAt(ad, 100, 100); x.moveTo(ad, 150, 120, true) }},
		{"marquee", func(x *harness, ad *artDoc) { x.key('M', false); x.pressAt(ad, 50, 50); x.moveTo(ad, 150, 120, true) }},
		{"lasso", func(x *harness, ad *artDoc) { x.key('L', false); x.pressAt(ad, 50, 50); x.moveTo(ad, 150, 120, true) }},
		{"shape", func(x *harness, ad *artDoc) { x.key('U', false); x.pressAt(ad, 50, 50); x.moveTo(ad, 150, 120, true) }},
		{"pen", func(x *harness, ad *artDoc) {
			x.key('C', false)
			x.clickCanvas(ad, 50, 50, false)
			x.clickCanvas(ad, 150, 80, false)
		}},
		{"pencil", func(x *harness, ad *artDoc) { x.key('P', false); x.pressAt(ad, 50, 50); x.moveTo(ad, 150, 120, true) }},
		{"gradient", func(x *harness, ad *artDoc) { x.key('G', false); x.pressAt(ad, 50, 50); x.moveTo(ad, 150, 120, true) }},
		{"vector gradient", func(x *harness, ad *artDoc) {
			ad.d.AddVector(solidSquare(40, 40, 200, red), "Shape")
			x.key('G', false)
			x.pressAt(ad, 50, 50)
			x.moveTo(ad, 150, 120, true)
		}},
		{"mask gradient", func(x *harness, ad *artDoc) { x.key('Y', false); x.pressAt(ad, 50, 50); x.moveTo(ad, 150, 120, true) }},
		{"artboard", func(x *harness, ad *artDoc) { x.key('J', false); x.pressAt(ad, -50, 50); x.moveTo(ad, -150, 120, true) }},
		{"node drag", func(x *harness, ad *artDoc) {
			ad.d.AddVector(solidSquare(40, 40, 200, red), "Shape")
			x.key('A', false)
			x.pressAt(ad, 40, 40)
			x.moveTo(ad, 60, 60, true)
		}},
		{"free transform", func(x *harness, ad *artDoc) {
			ad.d.SelectAll()
			x.key('T', true)
		}},
		{"text", func(x *harness, ad *artDoc) {
			x.key('T', false)
			x.clickCanvas(ad, 100, 100, false)
			x.frame(Input{Typed: []rune("Hi")})
		}},
		{"colour drag", func(x *harness, ad *artDoc) {
			ad.d.AddVector(solidSquare(40, 40, 200, red), "Shape")
			x.a.setPickerColor(blue) // a live change, recorded when the mouse is let go
			x.frame(Input{Down: true})
		}},
		{"space held", func(x *harness, ad *artDoc) {
			x.frame(Input{Keys: []Key{{Code: keySpace}}, Up: []uint16{}})
			x.pressAt(ad, 100, 100)
			x.moveTo(ad, 150, 150, true)
		}},
		{"zoom key held", func(x *harness, ad *artDoc) {
			x.key('B', false)
			x.frame(Input{Keys: []Key{{Code: 'Z'}}, Up: []uint16{}})
			x.pressAt(ad, 100, 100)
			x.moveTo(ad, 150, 100, true)
		}},
		{"menu open", func(x *harness, ad *artDoc) {
			l := x.a.artLayout(x.a.layout().body)
			x.click(x.menuTitle(l, menuLayer))
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			x, ad := canvasHarness(t)
			ad.d.AddLayer("Ink")
			ad.d.Select(image.Rect(20, 20, 200, 200))
			ad.d.Fill(color.NRGBA{R: 200, A: 255})
			ad.d.Deselect()
			ad.changedAll()
			x.frame(Input{})
			c.start(x, ad)
			canvas := x.a.current()
			// A new tab, with the mouse still down and moving, then let go, and keys.
			x.a.newTab()
			x.frame(Input{Down: x.a.u.down})
			if x.a.art != nil {
				t.Fatal("a new tab should leave the canvas")
			}
			for i := range 5 {
				x.mx, x.my = 300+float32(i)*20, 400
				x.frame(Input{Down: true})
			}
			x.frame(Input{Pointer: []Pointer{{X: x.mx, Y: x.my}}, Up: []uint16{keySpace, 'Z'}})
			for _, k := range []uint16{'B', 'V', 'Z', keySpace, keyEnter, keyEscape, keyDelete, '5'} {
				x.key(k, false)
			}
			x.frame(Input{Wheel: 1})
			// Back to the canvas, which goes on working.
			x.a.switchTab(canvas)
			x.frame(Input{})
			x.key('B', false)
			x.pressAt(ad, 120, 120)
			x.moveTo(ad, 160, 160, true)
			x.frame(Input{Pointer: []Pointer{{X: x.mx, Y: x.my}}})
			x.frame(Input{})
			if len(ad.d.Layers()) == 0 {
				t.Error("the canvas lost its layers")
			}
		})
	}
}

// Closing a canvas tab in the middle of a stroke or drag is safe too.
func TestClosingACanvasMidwayNeverCrashes(t *testing.T) {
	x, ad := canvasHarness(t)
	x.key('B', false)
	x.pressAt(ad, 100, 100)
	x.moveTo(ad, 150, 120, true)
	x.a.closeTab(x.a.current())
	for range 3 {
		x.frame(Input{Down: true})
	}
	x.frame(Input{Pointer: []Pointer{{X: x.mx, Y: x.my}}})
	x.frame(Input{})
	_ = paint.Ext
}

// pressAt presses the mouse at canvas pixel (cx, cy) and holds it.
func (x *harness) pressAt(ad *artDoc, cx, cy float64) {
	x.mx, x.my = x.viewPoint(ad, cx, cy)
	x.frame(Input{})
	x.frame(Input{Down: true, Pointer: []Pointer{{X: x.mx, Y: x.my, Down: true}}})
}

// moveTo moves the mouse to canvas pixel (cx, cy), held or not.
func (x *harness) moveTo(ad *artDoc, cx, cy float64, down bool) {
	x.mx, x.my = x.viewPoint(ad, cx, cy)
	x.frame(Input{Down: down})
}
