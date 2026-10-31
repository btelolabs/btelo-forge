package ui

import (
	"image"
	"image/color"
	"testing"

	"github.com/btelolabs/btelo-forge/internal/paint"
)

func TestSelectModifyFromItsDialogs(t *testing.T) {
	x, ad := canvasHarness(t)
	a := x.a
	d := ad.d
	d.Select(image.Rect(100, 100, 200, 200))
	box := rectCentered(a.screen, 360, 170).Inset(22)
	ok := rectAt(box.Right()-100, box.Bottom()-34, 100, 34)
	a.openDialog(dialogExpand)
	x.frame(Input{})
	if a.dialog.a != "5" {
		t.Fatalf("the dialog opened with %q", a.dialog.a)
	}
	a.dialog.a = "10"
	x.frame(Input{})
	x.shot("canvas-expand")
	x.click(ok)
	if s := d.Selection; s.At(90, 150) != 255 || s.At(89, 150) != 0 || d.UndoName() != "Expand" {
		t.Errorf("expanded: %d, %d, step %q", s.At(90, 150), s.At(89, 150), d.UndoName())
	}
	// A size that is not a whole number of pixels leaves OK off.
	a.openDialog(dialogBorder)
	a.dialog.a = "2.5"
	x.frame(Input{})
	x.click(ok)
	if a.dialog.kind != dialogBorder {
		t.Error("the dialog took a width of 2.5")
	}
	a.dialog.a = "10"
	x.frame(Input{})
	x.click(ok)
	if s := d.Selection; s.At(90, 150) != 255 || s.At(150, 150) != 0 {
		t.Errorf("border: %d, %d", s.At(90, 150), s.At(150, 150))
	}
}

func TestEveryNewAdjustmentOpensAppliesAndCancels(t *testing.T) {
	x, ad := canvasHarness(t)
	a := x.a
	d := ad.d
	l := d.AddLayer("Photo")
	d.Select(image.Rect(0, 0, 400, 300))
	if err := d.Fill(color.NRGBA{R: 150, G: 90, B: 60, A: 255}); err != nil {
		t.Fatal(err)
	}
	d.Deselect()
	before := l.Pixels.At(50, 50)
	for kind, name := range map[int]string{adjustColorBalance: "color-balance", adjustVibrance: "vibrance", adjustSelective: "selective",
		adjustPhotoFilter: "photo-filter", adjustGradientMap: "gradient-map", adjustExposure: "exposure", adjustShadowsHighlights: "shadows-highlights", filterDevelop: "develop"} {
		a.openAdjust(ad, kind)
		k := a.adjustKinds()[kind]
		if want := max(len(k.pages), 1)*len(k.params) + 3*len(k.colours); len(a.adjust.values) != want {
			t.Fatalf("%s has %d values, want %d", name, len(a.adjust.values), want)
		}
		x.frame(Input{})
		x.shot("canvas-adjust-" + name)
		x.frame(Input{Keys: []Key{{Code: keyEscape}}})
		if a.adjust != nil || l.Pixels.At(50, 50) != before {
			t.Fatalf("%s did not cancel", name)
		}
	}
	// A page's sliders are its own: warming the shadows leaves the highlights' values alone.
	a.openAdjust(ad, adjustColorBalance)
	f := a.adjust
	f.page = 2
	x.frame(Input{})
	f.values[6] = 80
	x.frame(Input{Keys: []Key{{Code: keyEnter}}})
	if f.values[0] != 0 || d.UndoName() != "Color balance" {
		t.Errorf("values %v, step %q", f.values, d.UndoName())
	}
}

func TestNewAdjustmentLayersKeepTheirSettings(t *testing.T) {
	x, ad := canvasHarness(t)
	a := x.a
	d := ad.d
	for kind, pk := range map[int]paint.AdjustKind{adjustColorBalance: paint.AdjustColorBalance, adjustVibrance: paint.AdjustVibrance,
		adjustSelective: paint.AdjustSelectiveColor, adjustPhotoFilter: paint.AdjustPhotoFilter, adjustGradientMap: paint.AdjustGradientMap, adjustExposure: paint.AdjustExposure} {
		a.openAdjustLayer(ad, kind)
		f := a.adjust
		if f == nil {
			t.Fatalf("kind %d did not open", kind)
		}
		f.values[0] = 1
		x.frame(Input{Keys: []Key{{Code: keyEnter}}})
		n := d.Node(d.Active)
		if n.Adjust == nil || n.Adjust.Kind != pk || n.Adjust.Values[0] != 1 {
			t.Fatalf("kind %d: %+v", kind, n.Adjust)
		}
		// Opening its settings finds them again.
		a.openAdjustmentSettings(ad, n.ID)
		if a.adjust == nil || a.adjust.kind != kind || a.adjust.values[0] != 1 {
			t.Fatalf("kind %d's settings did not reopen", kind)
		}
		x.frame(Input{Keys: []Key{{Code: keyEscape}}})
	}
}

func TestAnAdjustmentSwatchTakesTheForegroundColour(t *testing.T) {
	x, ad := canvasHarness(t)
	a := x.a
	a.brush.Color = color.NRGBA{R: 10, G: 20, B: 30, A: 255}
	ad.d.AddLayer("Photo")
	a.openAdjust(ad, adjustPhotoFilter)
	f := a.adjust
	x.frame(Input{})
	u := &a.u
	k := a.adjustKinds()[adjustPhotoFilter]
	h := u.px(130) + float32(len(k.params))*u.px(40) + u.px(36) + u.px(36)
	box := rectCentered(a.screen, u.px(420), h)
	box.X = min(a.screen.W-box.W-u.px(330), box.X+u.px(200))
	in := box.Inset(u.px(22))
	x.click(rectAt(in.X+u.px(112), in.Y+u.px(40)+u.px(40)+u.px(4), u.px(40), u.px(20)))
	if v := f.values[1:4]; v[0] != 10 || v[1] != 20 || v[2] != 30 {
		t.Errorf("the swatch holds %v", v)
	}
}

func TestAnAdjustmentsPagesAreChosenFromItsList(t *testing.T) {
	x, ad := canvasHarness(t)
	a := x.a
	ad.d.AddLayer("Photo")
	a.openAdjust(ad, adjustSelective)
	f := a.adjust
	x.frame(Input{})
	u := &a.u
	k := a.adjustKinds()[adjustSelective]
	h := u.px(130) + float32(len(k.params))*u.px(40) + u.px(40) + u.px(36)
	box := rectCentered(a.screen, u.px(420), h)
	box.X = min(a.screen.W-box.W-u.px(330), box.X+u.px(200))
	in := box.Inset(u.px(22))
	x.click(rectAt(in.X+u.px(120), in.Y+u.px(42), u.px(100), u.px(24)))
	if !a.menuOpen("adjust/page") {
		t.Fatal("the list did not open")
	}
	x.frame(Input{})
	x.shot("canvas-adjust-pages")
	x.clickMenuItem("Blues")
	if f.page != 4 {
		t.Fatalf("page %d", f.page)
	}
	x.frame(Input{})
	f.values[4*4] = 30
	x.frame(Input{Keys: []Key{{Code: keyEnter}}})
	if a.adjust != nil {
		t.Error("the dialog stayed open")
	}
}
