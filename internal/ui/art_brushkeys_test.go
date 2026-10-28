package ui

import (
	"image"
	"image/color"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/btelolabs/btelo-forge/internal/paint"
)

// The number keys set the brush's opacity with a painting tool, else the chosen layer's; two digits typed quickly
// are the percent. D and F make the brush smaller and larger while a painting tool is chosen.
func TestBrushAndLayerKeys(t *testing.T) {
	x, ad := canvasHarness(t)
	a, d := x.a, ad.d
	x.key('B', false)
	x.key('5', false)
	if a.brush.Opacity != 0.5 {
		t.Errorf("5 with the brush gave an opacity of %v", a.brush.Opacity)
	}
	a.typedOpacity = nil
	x.key('0', false)
	if a.brush.Opacity != 1 {
		t.Errorf("0 gave %v", a.brush.Opacity)
	}
	a.typedOpacity = nil
	x.key('4', false)
	x.key(0x65, false) // 5 on the number pad, soon after
	if math.Abs(a.brush.Opacity-0.45) > 1e-9 {
		t.Errorf("4 then 5 gave %v", a.brush.Opacity)
	}
	// A digit typed after the time for a second one stands on its own.
	x.key('2', false)
	a.typedOpacity.at = time.Now().Add(-time.Second)
	x.key('3', false)
	if math.Abs(a.brush.Opacity-0.3) > 1e-9 {
		t.Errorf("3 well after 2 gave %v", a.brush.Opacity)
	}
	a.typedOpacity = nil
	size := a.brush.Size
	x.key('F', false)
	if a.brush.Size <= size {
		t.Errorf("F left the brush at %v", a.brush.Size)
	}
	x.key('D', false)
	x.key('D', false)
	if a.brush.Size >= size {
		t.Errorf("D left the brush at %v", a.brush.Size)
	}
	// Away from the painting tools: the layer's opacity, and D gives the default colours again.
	x.key('V', false)
	n := d.AddVector(solidSquare(10, 10, 50, red), "Shape")
	x.key('3', false)
	if got := d.Node(n.ID).Opacity; math.Abs(got-0.3) > 1e-9 {
		t.Errorf("3 gave the vector layer an opacity of %v", got)
	}
	a.typedOpacity = nil
	d.Choose(d.Layers()[len(d.Layers())-1].ID)
	x.key('7', false)
	if got := d.Node(d.Active).Opacity; math.Abs(got-0.7) > 1e-9 {
		t.Errorf("7 gave the pixel layer %v", got)
	}
	a.brush.Color = red
	size = a.brush.Size
	x.key('D', false)
	if a.brush.Color != (color.NRGBA{A: 255}) || a.brush.Size != size {
		t.Errorf("D away from the brush: colour %v, size %v", a.brush.Color, a.brush.Size)
	}
}

// A layer's pixels make a brush tip, which is kept, chosen, painted with, and removed.
func TestDefineBrushTip(t *testing.T) {
	x, ad := canvasHarness(t)
	a, d := x.a, ad.d
	d.AddLayer("Mark")
	d.Select(image.Rect(100, 100, 140, 110))
	d.Fill(color.NRGBA{A: 255})
	d.Deselect()
	ad.changedAll()
	a.defineTip(ad)
	p := a.settings.Paint
	if len(p.Tips) != 1 || a.brush.Tip != paint.TipShaped || a.brush.Shape == nil || a.brush.Shape.W != 40 || a.brush.Shape.H != 10 {
		t.Fatalf("tips %v, brush tip %v, shape %+v", p.Tips, a.brush.Tip, a.brush.Shape)
	}
	if _, err := os.Stat(filepath.Join(a.tipsDir(), p.Tips[0])); err != nil {
		t.Fatalf("the tip is not kept: %v", err)
	}
	// It comes back with the settings.
	a.tipShapes = nil
	if tip := a.tipShape(p.Tips[0]); tip == nil || tip.W != 40 {
		t.Error("the kept tip cannot be read back")
	}
	// Painted with, it lays the bar.
	d.AddLayer("Paint")
	a.brush.Size, a.brush.Color, a.brush.Opacity, a.brush.Spacing = 80, red, 1, 2
	x.key('B', false)
	fx, fy := x.viewPoint(ad, 250, 200)
	x.drag(fx, fy, fx, fy)
	if c := ad.look(280, 200); c != (color.RGBA{255, 0, 0, 255}) {
		t.Errorf("the bar's end %v", c)
	}
	if c := ad.look(250, 215); c == (color.RGBA{255, 0, 0, 255}) {
		t.Error("the bar is 20 px tall at this size: nothing 15 px under its middle")
	}
	x.frame(Input{})
	x.shot("canvas-brush-tip")
	a.removeTip(p.Tips[0])
	if len(a.settings.Paint.Tips) != 0 || a.brush.Tip != paint.TipRound {
		t.Error("removing the tip")
	}
}

// Shift+M gives the chosen layer or group a layer mask, from the selection; Q opens its style; Enter names it.
func TestLayerKeys(t *testing.T) {
	x, ad := canvasHarness(t)
	a, d := x.a, ad.d
	g := d.AddGroup("Group")
	d.Select(image.Rect(10, 10, 50, 50))
	x.frame(Input{Keys: []Key{{Code: 'M', Shift: true}}})
	if n := d.Node(g.ID); n.Mask == nil || n.Mask.At(20, 20) != 255 || n.Mask.At(100, 100) != 0 {
		t.Fatal("Shift+M should give the group a mask from the selection")
	}
	if a.artTool == toolMarquee {
		t.Error("Shift+M should not switch to the marquee")
	}
	d.Deselect()
	d.AddLayer("Layer")
	x.key('Q', false)
	if a.style == nil {
		t.Error("Q should open the layer style")
	}
	a.closeStyle(false)
	x.frame(Input{})
	a.u.edit = editor{text: []rune("left from another box"), caret: 3, anchor: 3}
	x.key(keyEnter, false)
	x.frame(Input{})
	x.shot("canvas-layer-rename")
	if string(a.u.edit.text) != "Layer" || a.u.edit.anchor != 0 || a.u.edit.caret != len("Layer") {
		t.Errorf("the name box starts with %q chosen %d-%d, want the layer's name all chosen", string(a.u.edit.text), a.u.edit.anchor, a.u.edit.caret)
	}
	if ad.renaming != d.Active || a.u.focus != "art/rename" {
		t.Fatalf("Enter should name the chosen layer: renaming %d, focus %q", ad.renaming, a.u.focus)
	}
	x.frame(Input{Typed: []rune("Ink")}) // the name is chosen whole: typing replaces it
	x.key(keyEnter, false)
	if n := d.Node(d.Active); n.Name != "Ink" {
		t.Errorf("the layer is called %q", n.Name)
	}
}

// Each preset keeps the size it was last used in, at first its own; with every brush the same size, choosing
// another keeps the brush's. The choice and the sizes are the app's settings, kept between runs.
func TestBrushSizesPerBrushOrTheSame(t *testing.T) {
	x, _ := canvasHarness(t)
	a := x.a
	x.key('B', false)
	preset := func(id string) paint.Preset {
		p, ok := paint.PresetByID(id)
		if !ok {
			t.Fatalf("no preset %q", id)
		}
		return p
	}
	a.brush.Size = 20
	a.saveBrush()
	a.choosePreset(preset("charcoal"))
	if a.brush.Size != 40 {
		t.Fatalf("charcoal first chosen in %v px, want its own 40", a.brush.Size)
	}
	x.key('F', false) // larger: charcoal keeps it
	charcoal := a.brush.Size
	a.choosePreset(preset("ink-pen"))
	if a.brush.Size != 5 {
		t.Fatalf("the ink pen in %v px, want its own 5", a.brush.Size)
	}
	a.choosePreset(preset("charcoal"))
	if a.brush.Size != charcoal {
		t.Errorf("charcoal back in %v px, want the %v it was left in", a.brush.Size, charcoal)
	}
	// A plain tip is a brush of its own, in the size it was left in.
	a.brush.Tip = paint.TipRound
	a.chosenBrush(a.brush.Size)
	if a.brush.Size != 20 {
		t.Errorf("the round tip in %v px, want the 20 it was left in", a.brush.Size)
	}

	a.settings.Paint.SameSize = true
	a.brush.Size = 33
	a.saveBrush()
	x.frame(Input{})
	x.shot("canvas-brush-same-size")
	for _, id := range []string{"fan", "ink-pen", "charcoal"} {
		a.choosePreset(preset(id))
		if a.brush.Size != 33 {
			t.Errorf("with every brush the same size, %s came in %v px, want 33", id, a.brush.Size)
		}
	}
	// A tip made from a layer comes in the brush's size too.
	d := a.art.d
	d.AddLayer("Tip")
	d.Select(image.Rect(10, 10, 30, 30))
	if err := d.Fill(color.NRGBA{A: 255}); err != nil {
		t.Fatal(err)
	}
	a.defineTip(a.art)
	if a.brush.Tip != paint.TipShaped || a.brush.Size != 33 {
		t.Errorf("a new tip came in %v px (tip %v), want 33", a.brush.Size, a.brush.Tip)
	}

	again := &App{host: &fakeHost{}, stateDir: a.stateDir}
	again.loadSettings()
	p := again.settings.Paint
	if !p.SameSize || p.Sizes["preset:ink-pen"] != 5 || p.Sizes["preset:charcoal"] != charcoal {
		t.Errorf("kept between runs: same size %v, sizes %v", p.SameSize, p.Sizes)
	}
	if again.settings.Paint.brush().Size != 33 {
		t.Errorf("the brush comes back in %v px", again.settings.Paint.brush().Size)
	}
}
