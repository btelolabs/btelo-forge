package ui

import (
	"image/color"
	"testing"
)

func TestCanvasTextTool(t *testing.T) {
	x, ad := canvasHarness(t)
	a := x.a
	d := ad.d
	a.brush.Color = color.NRGBA{R: 200, A: 255}
	a.settings.Paint.TextSize = 40
	x.key('T', false)
	if a.artTool != toolText {
		t.Fatalf("T chose tool %d", a.artTool)
	}
	px, py := x.viewPoint(ad, 40, 100)
	x.click(rectAt(px-1, py-1, 2, 2))
	if ad.text == nil || a.u.focus != textFocus {
		t.Fatalf("a click did not start typing: %v %q", ad.text, a.u.focus)
	}
	x.frame(Input{})
	if !a.u.blinking || !a.u.editing(textFocus) {
		t.Error("the text tool's caret should blink, as every box's does")
	}
	x.frame(Input{Typed: []rune("Hello")})
	x.frame(Input{Keys: []Key{{Code: keyEnter}}})
	x.frame(Input{Typed: []rune("世界")})
	l := ad.text.s.Layer()
	if l.Text.Text != "Hello\n世界" || l.Pixels.Bounds().Empty() {
		t.Fatalf("typing gave %q over %v", l.Text.Text, l.Pixels.Bounds())
	}
	// The first line sits on the point clicked.
	if top := l.Pixels.Bounds().Min.Y; top > 100 || top < 40 {
		t.Errorf("the text starts at y %d", top)
	}
	x.frame(Input{})
	x.shot("canvas-text-typing")
	x.frame(Input{Keys: []Key{{Code: keyEnter, Ctrl: true}}})
	if ad.text != nil || d.UndoName() != "Text" {
		t.Fatalf("Ctrl+Enter did not commit: %q", d.UndoName())
	}
	l = d.Node(d.Active)
	if l.Text == nil || l.Name != "Hello" {
		t.Fatalf("the text layer: %+v named %q", l.Text, l.Name)
	}
	red := 0
	for y := l.Pixels.Bounds().Min.Y; y < l.Pixels.Bounds().Max.Y; y++ {
		for xx := l.Pixels.Bounds().Min.X; xx < l.Pixels.Bounds().Max.X; xx++ {
			if c := ad.look(xx, y); c.R > 150 && c.G < 80 {
				red++
			}
		}
	}
	if red < 50 {
		t.Errorf("the text barely shows: %d red pixels", red)
	}
	// A click on the text edits it again; a bigger size from the options bar re-draws it.
	tx, ty := x.viewPoint(ad, 50, 90)
	x.click(rectAt(tx-1, ty-1, 2, 2))
	if ad.text == nil || ad.text.s.Layer().ID != l.ID {
		t.Fatal("a click on the text did not edit it")
	}
	x.frame(Input{Keys: []Key{{Code: keyEnd}}})
	x.frame(Input{Typed: []rune("!")})
	a.settings.Paint.TextSize = 60
	x.frame(Input{})
	before := ad.text.s.Layer().Pixels.Bounds()
	// Letters type while typing; a click away from the text keeps what was typed.
	x.key('V', false)
	if a.artTool != toolText {
		t.Fatal("a letter typed while typing chose a tool")
	}
	px, py = x.viewPoint(ad, 380, 280)
	x.click(rectAt(px-1, py-1, 2, 2))
	if ad.text != nil || d.UndoName() != "Edit text" {
		t.Fatalf("a new tool did not commit the edit: %q", d.UndoName())
	}
	if got := d.Node(l.ID).Text; got.Text != "Hello!\n世界" || got.Size != 60 || before.Dy() < 100 {
		t.Errorf("edited text %+v, %v", got, before)
	}
	if name := d.Node(l.ID).Name; name != "Hello!" {
		t.Errorf("the layer's name did not follow its text: %q", name)
	}
	// Esc while typing a new text leaves nothing behind.
	layers := len(d.Layers())
	x.key('T', false)
	px, py = x.viewPoint(ad, 300, 250)
	x.click(rectAt(px-1, py-1, 2, 2))
	x.frame(Input{Typed: []rune("gone")})
	x.frame(Input{Keys: []Key{{Code: keyEscape}}})
	if ad.text != nil || len(d.Layers()) != layers {
		t.Errorf("Esc kept the text: %d layers", len(d.Layers()))
	}
	// Painting on the text layer turns it into pixels; undo brings it back.
	d.Choose(l.ID)
	x.key('B', false)
	x.dragWith(ad, [2]float64{60, 60}, [2]float64{80, 60}, false, false)
	if d.Node(l.ID).Text != nil {
		t.Error("painting kept the layer as text")
	}
	x.key('Z', true)
	if d.Node(l.ID).Text == nil {
		t.Error("undo did not bring the text back")
	}
	x.frame(Input{})
	a.u.lang = Chinese
	x.key('T', false)
	x.frame(Input{})
	x.shot("canvas-text-zh")
	a.u.lang = English
}
