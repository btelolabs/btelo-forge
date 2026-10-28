package ui

import (
	"image/color"
	"slices"
	"testing"

	"github.com/btelolabs/btelo-forge/internal/gfx"
	"github.com/btelolabs/btelo-forge/internal/paint"
)

// Every preset's name is offered for translation.
func TestBrushPresetNamesAreTranslated(t *testing.T) {
	for _, p := range paint.Presets() {
		if !slices.Contains(brushPresetWords, p.Name) {
			t.Errorf("preset %s: %q is not among brushPresetWords", p.ID, p.Name)
		}
		if _, ok := zh[p.Name]; !ok {
			t.Errorf("preset %s: %q has no Chinese name", p.ID, p.Name)
		}
	}
}

// The presets button opens the panel; a preset chosen there becomes the brush in the brush's colour, is painted
// with, and comes back with the settings, its shape dynamics too; the tip panel shows and changes those.
func TestBrushPresets(t *testing.T) {
	x, ad := canvasHarness(t)
	a, d := x.a, ad.d
	u := &a.u
	x.key('B', false)
	a.brush.Color = red
	bar := x.barArea()
	button := gfx.Rect{X: bar.X, Y: bar.Y, W: u.px(46), H: bar.H}
	x.click(button)
	if !a.menuOpen("art/presets") {
		t.Fatal("the presets button did not open the panel")
	}
	x.frame(Input{})
	x.shot("canvas-brush-presets")
	_, cells := presetsLayout(u, button, len(paint.Presets()))
	want, _ := paint.PresetByID("ink-brush")
	i := slices.IndexFunc(paint.Presets(), func(p paint.Preset) bool { return p.ID == want.ID })
	x.click(cells[i])
	if a.menu != nil {
		t.Error("choosing a preset leaves the panel open")
	}
	b := a.brush
	if b.Shape != want.Brush.Shape || b.Size != want.Brush.Size || b.Spacing != want.Brush.Spacing || !b.Follow || b.Color != red {
		t.Fatalf("the brush after choosing the ink brush: %+v", b)
	}
	if p := a.settings.Paint; p.Preset != "ink-brush" || p.ShapeTip != "" || !p.Follow || p.Tip != int(paint.TipShaped) {
		t.Errorf("kept as preset %q, shape tip %q, follow %v, tip %d", p.Preset, p.ShapeTip, p.Follow, p.Tip)
	}
	// It paints, in red.
	d.AddLayer("Paint")
	fx, fy := x.viewPoint(ad, 100, 150)
	tx, ty := x.viewPoint(ad, 300, 150)
	x.drag(fx, fy, tx, ty)
	if c := ad.look(200, 150); c != (color.RGBA{255, 0, 0, 255}) {
		t.Errorf("the middle of the ink stroke is %v", c)
	}
	x.frame(Input{})
	x.shot("canvas-brush-preset-stroke")
	// Its tip and dynamics come back with the settings, as when the app starts again.
	again := a.settings.Paint.brush()
	if again.SizeJitter != b.SizeJitter || again.AngleJitter != b.AngleJitter || again.Scatter != b.Scatter || !again.Follow {
		t.Errorf("the dynamics read back: %+v", again)
	}
	if p, ok := paint.PresetByID(a.settings.Paint.Preset); !ok || p.Brush.Shape != b.Shape {
		t.Error("the preset's tip is not found again by its id")
	}
	// The tip panel shows the dynamics.
	a.brush.Scatter = 0.5
	a.openMenu("art/tipshape", gfx.Rect{X: bar.X + u.px(300), Y: bar.Y, W: u.px(30), H: bar.H}, nil)
	x.frame(Input{})
	x.shot("canvas-brush-dynamics")
	a.menu = nil
	// A round tip chosen afterwards is no preset.
	a.brush.Tip = paint.TipRound
	a.saveBrush()
	if a.settings.Paint.Preset != "" {
		t.Errorf("a round tip is kept as preset %q", a.settings.Paint.Preset)
	}
}
