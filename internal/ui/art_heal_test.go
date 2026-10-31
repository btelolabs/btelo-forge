package ui

import (
	"image"
	"image/color"
	"testing"

	"github.com/btelolabs/btelo-forge/internal/gfx"
)

// healDoc is a canvas with a grey picture and a dark mark on it.
func healDoc(t *testing.T) (*harness, *artDoc) {
	t.Helper()
	x, ad := canvasHarness(t)
	d := ad.d
	d.AddLayer("Photo")
	d.Select(image.Rect(0, 0, 400, 300))
	if err := d.Fill(color.NRGBA{R: 150, G: 140, B: 120, A: 255}); err != nil {
		t.Fatal(err)
	}
	d.Select(image.Rect(195, 145, 205, 155))
	if err := d.Fill(color.NRGBA{R: 10, G: 10, B: 10, A: 255}); err != nil {
		t.Fatal(err)
	}
	d.Deselect()
	return x, ad
}

func TestTheHealingToolsShareThePlaceOfTheCloneStamp(t *testing.T) {
	x, _ := healDoc(t)
	a := x.a
	x.key('S', false)
	if a.artTool != toolClone {
		t.Fatalf("S chose %d", a.artTool)
	}
	for _, want := range []artTool{toolSpot, toolHeal, toolPatch, toolClone} {
		a.setArtTool(nextInSlot(a.artTool))
		if a.artTool != want {
			t.Errorf("the next in the place is %d, want %d", a.artTool, want)
		}
	}
}

func TestSpotHealingFromTheCanvas(t *testing.T) {
	x, ad := healDoc(t)
	a := x.a
	a.brush.Size = 24
	ad.changedAll()
	for tool, name := range map[artTool]string{toolHeal: "canvas-heal", toolPatch: "canvas-patch", toolSpot: "canvas-spot-heal"} {
		a.setArtTool(tool)
		x.frame(Input{})
		x.shot(name)
	}
	a.setArtTool(toolSpot)
	x.dragWith(ad, [2]float64{198, 150}, [2]float64{202, 150}, false, false)
	if c := ad.look(200, 150); c.R < 130 {
		t.Errorf("the mark is still there: %v", c)
	}
	if ad.d.UndoName() != "Spot healing" {
		t.Errorf("step %q", ad.d.UndoName())
	}
}

func TestHealingBrushAsksForASourceFirst(t *testing.T) {
	x, ad := healDoc(t)
	a := x.a
	a.brush.Size = 24
	a.setArtTool(toolHeal)
	x.dragWith(ad, [2]float64{198, 150}, [2]float64{202, 150}, false, false)
	if ad.d.UndoName() == "Healing brush" {
		t.Fatal("it healed without a source")
	}
	x.dragWith(ad, [2]float64{100, 150}, [2]float64{100, 150}, false, true) // Alt+click
	if !ad.cloneSet {
		t.Fatal("Alt+click set no source")
	}
	x.dragWith(ad, [2]float64{198, 150}, [2]float64{202, 150}, false, false)
	if ad.d.UndoName() != "Healing brush" {
		t.Errorf("step %q", ad.d.UndoName())
	}
	if c := ad.look(200, 150); c.R < 130 {
		t.Errorf("the mark is still there: %v", c)
	}
}

func TestPatchToolSelectsThenPatches(t *testing.T) {
	x, ad := healDoc(t)
	a := x.a
	a.setArtTool(toolPatch)
	// Outside any selection it draws one, as the lasso does.
	pts := [][2]float64{{185, 135}, {215, 135}, {215, 165}, {185, 165}}
	fx, fy := x.viewPoint(ad, pts[0][0], pts[0][1])
	x.mx, x.my = fx, fy
	x.frame(Input{})
	x.frame(Input{Down: true, Pointer: []Pointer{{X: fx, Y: fy, Down: true}}})
	for _, p := range pts[1:] {
		x.mx, x.my = x.viewPoint(ad, p[0], p[1])
		x.frame(Input{Down: true})
	}
	x.frame(Input{Pointer: []Pointer{{X: x.mx, Y: x.my}}})
	if ad.d.Selection == nil || ad.d.Selection.At(200, 150) != 255 {
		t.Fatal("it drew no selection")
	}
	// Inside it, a drag takes what it goes to.
	x.dragWith(ad, [2]float64{200, 150}, [2]float64{100, 150}, false, false)
	if ad.d.UndoName() != "Patch" {
		t.Errorf("step %q", ad.d.UndoName())
	}
	if c := ad.look(200, 150); c.R < 130 {
		t.Errorf("the mark is still there: %v", c)
	}
	if ad.patching {
		t.Error("still patching")
	}
}

func TestContentFillFromTheEditMenu(t *testing.T) {
	x, ad := healDoc(t)
	a := x.a
	d := ad.d
	d.Select(image.Rect(180, 130, 220, 170))
	var run func()
	for _, c := range a.artCommands(ad, gfx.Rect{W: 800, H: 600}) {
		if c.id == "draw.content-aware-fill" {
			if !c.enabled() {
				t.Fatal("it is off with a selection")
			}
			run = c.run
		}
	}
	if run == nil {
		t.Fatal("no such command")
	}
	run()
	if !ad.filling {
		t.Fatal("it did not start")
	}
	a.running.Wait()
	for range 3 {
		x.frame(Input{})
	}
	if ad.filling || d.UndoName() != "Content-aware fill" {
		t.Fatalf("filling %v, step %q", ad.filling, d.UndoName())
	}
	if c := ad.look(200, 150); c.R < 130 {
		t.Errorf("the mark is still there: %v", c)
	}
}
