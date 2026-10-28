package paint

import (
	"image"
	"image/color"
	"testing"
)

// A tip is the picture's opacity, or its darkness when it has no transparency, cut to what it covers.
func TestTipFromAPicture(t *testing.T) {
	// A red ring of opacity on transparency: its opacity.
	img := image.NewRGBA(image.Rect(10, 10, 60, 40))
	for y := 20; y < 30; y++ {
		for x := 20; x < 40; x++ {
			img.SetRGBA(x, y, color.RGBA{R: 128, A: 128})
		}
	}
	tip, err := TipFrom(img, nil)
	if err != nil || tip.W != 20 || tip.H != 10 || tip.Pix[0] != 128 {
		t.Fatalf("tip %dx%d %v (%v)", tip.W, tip.H, tip.Pix[:1], err)
	}
	// An opaque picture: its darkness, so black lays all down and white nothing.
	opaque := image.NewRGBA(image.Rect(0, 0, 4, 1))
	for x, v := range []uint8{0, 255, 128, 0} {
		opaque.SetRGBA(x, 0, color.RGBA{v, v, v, 255})
	}
	tip, err = TipFrom(opaque, nil)
	if err != nil || tip.W != 4 || tip.Pix[0] != 255 || tip.Pix[1] != 0 || tip.Pix[2] != 127 {
		t.Fatalf("tip of an opaque picture %v (%v)", tip.Pix, err)
	}
	// A selection fades it, and nothing selected is an error.
	sel := image.NewAlpha(opaque.Rect)
	if _, err := TipFrom(opaque, sel); err != ErrEmptyTip {
		t.Errorf("an empty selection: %v", err)
	}
	// A large picture is scaled down.
	big := image.NewRGBA(image.Rect(0, 0, 2000, 1000))
	for i := range big.Pix {
		big.Pix[i] = 255
	}
	big.SetRGBA(0, 0, color.RGBA{}) // not opaque
	if tip, err := TipFrom(big, nil); err != nil || tip.W > maxTipSide || tip.H != tip.W/2 {
		t.Errorf("a large tip is %dx%d (%v)", tip.W, tip.H, err)
	}
}

// A brush with a picture tip paints the picture's shape at its size.
func TestShapedBrushPaintsItsShape(t *testing.T) {
	d := New("Layer")
	d.Paper = false
	// A tip that is a bar three times as wide as it is tall.
	tip := &TipShape{W: 30, H: 10, Pix: make([]uint8, 300)}
	for i := range tip.Pix {
		tip.Pix[i] = 255
	}
	b := Brush{Size: 60, Opacity: 1, Spacing: 1, Flow: 1, Round: 1, Tip: TipShaped, Shape: tip, Color: color.NRGBA{B: 255, A: 255}}
	s, _, err := d.BeginStroke(b, false, 100, 100)
	if err != nil {
		t.Fatal(err)
	}
	s.End()
	at := func(x, y int) uint8 { return d.Composite(image.Rect(x, y, x+1, y+1)).RGBAAt(x, y).A }
	if at(75, 100) != 255 || at(125, 100) != 255 {
		t.Error("the bar's ends, 25 px either way, should be painted")
	}
	if at(100, 115) != 0 || at(135, 100) != 0 {
		t.Error("beyond the bar, 10 px tall and 60 wide, nothing")
	}
	// Turned a quarter, it stands up, its corners within the dab.
	b.Angle = 90
	s, _, _ = d.BeginStroke(b, false, 300, 100)
	s.End()
	if at(300, 75) != 255 || at(325, 100) != 0 {
		t.Error("the turned bar")
	}
	// Without a picture it falls back to the round tip.
	if (Brush{Tip: TipShaped}).normalized().Tip != TipRound {
		t.Error("a picture tip without its picture")
	}
}
