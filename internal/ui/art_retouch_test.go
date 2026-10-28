package ui

import (
	"image/color"
	"testing"
)

func TestCanvasRetouchTools(t *testing.T) {
	x, ad := canvasHarness(t)
	a := x.a
	d := ad.d
	d.SelectAll()
	d.Fill(color.NRGBA{R: 120, G: 120, B: 120, A: 255})
	d.Deselect()
	ad.changedAll()
	a.brush.Size, a.brush.Hardness = 40, 0.8
	// O picks dodge, Shift+O burn then sponge. Blur, sharpen and smudge share a place without a key: R names the layer.
	x.key('O', false)
	if a.artTool != toolDodge {
		t.Fatalf("O chose %d", a.artTool)
	}
	x.frame(Input{Keys: []Key{{Code: 'O', Shift: true}}})
	if a.artTool != toolBurn {
		t.Fatalf("Shift+O chose %d", a.artTool)
	}
	x.key('R', false)
	if a.artTool != toolBurn || ad.renaming != d.Active || a.u.focus != "art/rename" {
		t.Fatalf("R chose %d and renaming %d, want the burn tool kept and the layer named", a.artTool, ad.renaming)
	}
	x.key(keyEscape, false)
	if nextInSlot(toolBlur) != toolSharpen || nextInSlot(toolSharpen) != toolSmudge || len(toolDefaultKeys(toolBlur)) != 0 {
		t.Error("blur, sharpen and smudge should share a place with no key")
	}
	// Dodge lightens where it is dragged.
	x.key('O', false)
	x.key('O', false)
	for a.artTool != toolDodge {
		x.frame(Input{Keys: []Key{{Code: 'O', Shift: true}}})
	}
	x.dragWith(ad, [2]float64{60, 100}, [2]float64{300, 100}, false, false)
	if c := ad.look(180, 100); c.R <= 140 || d.UndoName() != "Dodge" {
		t.Errorf("dodged %v (%q)", c, d.UndoName())
	}
	x.frame(Input{})
	x.shot("canvas-dodge")
	// Burn darkens.
	x.frame(Input{Keys: []Key{{Code: 'O', Shift: true}}})
	x.dragWith(ad, [2]float64{60, 200}, [2]float64{300, 200}, false, false)
	if c := ad.look(180, 200); c.R >= 100 || d.UndoName() != "Burn" {
		t.Errorf("burnt %v (%q)", c, d.UndoName())
	}
	// Smudge drags the dodged band down into the grey.
	a.setArtTool(toolSmudge)
	x.dragWith(ad, [2]float64{180, 100}, [2]float64{180, 150}, false, false)
	if c := ad.look(180, 130); c.R <= 125 || d.UndoName() != "Smudge" {
		t.Errorf("smudged %v (%q)", c, d.UndoName())
	}
	x.frame(Input{})
	x.shot("canvas-smudge")
}
