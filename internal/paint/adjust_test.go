package paint

import (
	"image"
	"image/color"
	"math"
	"testing"
)

// pixelAfter is the colour of one pixel of a solid c after fn.
func pixelAfter(t *testing.T, c color.NRGBA, fn Filter) color.RGBA {
	t.Helper()
	d := newDoc(t, 8, 8, c)
	if err := d.Run("Test", fn); err != nil {
		t.Fatal(err)
	}
	return d.Node(d.Active).Pixels.At(4, 4)
}

func TestAdjustments(t *testing.T) {
	grey := color.NRGBA{R: 100, G: 150, B: 200, A: 255}
	if c := pixelAfter(t, grey, Invert()); c != (color.RGBA{155, 105, 55, 255}) {
		t.Errorf("invert %v", c)
	}
	if c := pixelAfter(t, grey, Desaturate()); c.R != c.G || c.G != c.B || !near(c.R, 150, 1) {
		t.Errorf("desaturate %v", c)
	}
	if c := pixelAfter(t, grey, Threshold(128)); c != (color.RGBA{255, 255, 255, 255}) {
		t.Errorf("threshold %v", c)
	}
	if c := pixelAfter(t, grey, Posterize(2)); c != (color.RGBA{0, 255, 255, 255}) {
		t.Errorf("posterize %v", c)
	}
	// Levels: input 50–200 stretched to 0–255.
	if c := pixelAfter(t, grey, Levels(50, 200, 1, 0, 255)); !near(c.R, 85, 1) || c.B != 255 {
		t.Errorf("levels %v", c)
	}
	// A gamma above 1 lightens the middle.
	if c := pixelAfter(t, color.NRGBA{R: 128, G: 128, B: 128, A: 255}, Levels(0, 255, 2, 0, 255)); c.R < 170 {
		t.Errorf("levels gamma %v", c)
	}
	// A curve through its ends and the middle lifted.
	lift := []CurvePoint{{0, 0}, {128, 180}, {255, 255}}
	if c := pixelAfter(t, color.NRGBA{R: 128, G: 0, B: 255, A: 255}, Curves(lift)); !near(c.R, 180, 1) || c.G != 0 || c.B != 255 {
		t.Errorf("curves %v", c)
	}
	// The curve never leaves 0–1 between its points and rises with its input.
	f := Curve(lift)
	prev := -1.0
	for i := range 256 {
		v := f(float64(i) / 255)
		if v < prev-1e-9 || v < 0 || v > 1 {
			t.Fatalf("the curve is not monotone at %d: %v after %v", i, v, prev)
		}
		prev = v
	}
	// Hue/Saturation: red turned 120° is green; -100 saturation is grey; colorize paints the hue.
	red := color.NRGBA{R: 255, A: 255}
	if c := pixelAfter(t, red, HueSaturation(120, 0, 0, false)); c != (color.RGBA{0, 255, 0, 255}) {
		t.Errorf("hue %v", c)
	}
	if c := pixelAfter(t, grey, HueSaturation(0, 0, 100, false)); c != (color.RGBA{255, 255, 255, 255}) {
		t.Errorf("lightness +100 %v", c)
	}
	if c := pixelAfter(t, grey, HueSaturation(240, 50, 0, true)); c.B <= c.R || c.B <= c.G {
		t.Errorf("colorize blue %v", c)
	}
	// Brightness raises the middle and keeps black and white; contrast spreads the tones.
	if c := pixelAfter(t, color.NRGBA{R: 128, G: 0, B: 255, A: 255}, BrightnessContrast(50, 0)); c.R <= 140 || c.G != 0 || c.B != 255 {
		t.Errorf("brightness %v", c)
	}
	if c := pixelAfter(t, color.NRGBA{R: 100, G: 128, B: 156, A: 255}, BrightnessContrast(0, 50)); c.R >= 100 || c.B <= 156 || !near(c.G, 128, 1) {
		t.Errorf("contrast %v", c)
	}
	// Transparent pixels stay transparent, and half transparent keep their alpha.
	d := newDoc(t, 4, 4, color.NRGBA{})
	d.Select(image.Rect(0, 0, 2, 4))
	d.Fill(color.NRGBA{R: 200, A: 128})
	d.Deselect()
	d.Run("Invert", Invert())
	if c := unpremul(d.Node(d.Active).Pixels.At(0, 0)); !near(c.R, 55, 2) || !near(c.A, 128, 1) {
		t.Errorf("invert half transparent %v", c)
	}
	if c := d.Node(d.Active).Pixels.At(3, 3); c.A != 0 {
		t.Errorf("invert made %v from nothing", c)
	}
}

func unpremul(c color.RGBA) color.NRGBA { return color.NRGBAModel.Convert(c).(color.NRGBA) }

func TestFilterSessionsPreviewApplyCancel(t *testing.T) {
	d := newDoc(t, 40, 40, white)
	d.Select(image.Rect(10, 10, 30, 30))
	d.Fill(color.NRGBA{A: 255})
	d.Select(image.Rect(0, 0, 20, 40)) // the left half
	f, err := d.BeginFilter()
	if err != nil {
		t.Fatal(err)
	}
	f.Preview(Invert())
	layer := func() *Raster { return d.Node(d.Active).Pixels } // undo and cancel bring back other nodes
	if c := layer().At(15, 15); c != (color.RGBA{255, 255, 255, 255}) {
		t.Errorf("preview inside the selection %v", c)
	}
	if c := layer().At(25, 15); c != (color.RGBA{0, 0, 0, 255}) {
		t.Errorf("preview outside the selection %v", c)
	}
	// A second preview starts again from the pixels as they were, not from the first preview.
	f.Preview(Levels(0, 255, 1, 0, 255))
	if c := layer().At(15, 15); c != (color.RGBA{0, 0, 0, 255}) {
		t.Errorf("previews piled up: %v", c)
	}
	f.Cancel()
	if c := layer().At(5, 5); c != (color.RGBA{255, 255, 255, 255}) || d.UndoName() == "Invert" {
		t.Errorf("cancel left %v (%q)", c, d.UndoName())
	}
	f, _ = d.BeginFilter()
	f.Apply("Invert", Invert())
	if c := layer().At(5, 5); c != (color.RGBA{0, 0, 0, 255}) || d.UndoName() != "Invert" {
		t.Errorf("apply %v (%q)", c, d.UndoName())
	}
	d.Undo()
	if c := layer().At(5, 5); c != (color.RGBA{255, 255, 255, 255}) {
		t.Errorf("undo %v", c)
	}
	// On a mask being edited, filters change the mask.
	d.Deselect()
	d.AddMask()
	d.EditMask = true
	d.Run("Invert", Invert())
	if v := d.Node(d.Active).Mask.At(5, 5); v != 0 {
		t.Errorf("inverted mask %d", v)
	}
}

func TestFilters(t *testing.T) {
	d := newDoc(t, 60, 20, white)
	d.Select(image.Rect(30, 0, 60, 20))
	d.Fill(color.NRGBA{A: 255})
	d.Deselect()
	l := func() *Node { return d.Node(d.Active) } // undo brings back other nodes
	// A blur makes a soft edge, and keeps flat areas flat.
	d.Run("Gaussian blur", GaussianBlur(4))
	if c := l().Pixels.At(30, 10); c.R < 60 || c.R > 196 {
		t.Errorf("blurred edge %v", c)
	}
	if c := l().Pixels.At(2, 10); c.R < 250 {
		t.Errorf("blurred flat area %v", c)
	}
	d.Undo()
	// Sharpening makes the edge overshoot on both sides.
	d.Run("Unsharp mask", UnsharpMask(150, 3, 0))
	if l().Pixels.At(31, 10).R != 0 || l().Pixels.At(28, 10).R != 255 {
		t.Errorf("sharpened edge %v %v", l().Pixels.At(28, 10), l().Pixels.At(31, 10))
	}
	d.Undo()
	// Mosaic averages cells: a cell across the edge is grey.
	d.Run("Mosaic", Mosaic(4))
	if c := l().Pixels.At(27, 5); c.R != 255 { // the cell 24–28
		t.Errorf("mosaic cell left of the edge %v", c)
	}
	d.Undo()
	d.Run("Mosaic", Mosaic(8))
	if c := l().Pixels.At(26, 5); !near(c.R, 191, 1) { // 24–32: six white, two black
		t.Errorf("mosaic cell across the edge %v", c)
	}
	d.Undo()
	// Noise changes pixels around their colour, the same with the same seed.
	grey := newDoc(t, 64, 64, color.NRGBA{R: 128, G: 128, B: 128, A: 255})
	f, _ := grey.BeginFilter()
	f.Preview(AddNoise(20, true, 7))
	first := append([]byte(nil), framePix(grey, grey.Node(grey.Active))...)
	f.Preview(AddNoise(20, true, 7))
	if string(first) != string(framePix(grey, grey.Node(grey.Active))) {
		t.Error("the same seed made different noise")
	}
	f.Apply("Add noise", AddNoise(20, true, 7))
	sum, diff := 0.0, 0
	p := grey.Node(grey.Active).Pixels
	for y := range 64 {
		for x := range 64 {
			c := p.At(x, y)
			sum += float64(c.R)
			if c.R != 128 {
				diff++
			}
			if c.R != c.G || c.G != c.B {
				t.Fatalf("monochromatic noise is coloured: %v", c)
			}
		}
	}
	if mean := sum / 4096; math.Abs(mean-128) > 3 || diff < 3000 {
		t.Errorf("noise mean %.1f, %d changed", mean, diff)
	}
}

func TestHistoryListAndGoTo(t *testing.T) {
	d := newDoc(t, 10, 10, white)
	d.AddLayer("A")
	d.AddLayer("B")
	d.Choose(d.Layers()[2].ID) // the white background, so inverting changes something
	d.Run("Invert", Invert())
	steps, done := d.History()
	if len(steps) != 3 || done != 3 || steps[2] != "Invert" {
		t.Fatalf("history %v at %d", steps, done)
	}
	d.GoTo(1)
	steps, done = d.History()
	if done != 1 || len(steps) != 3 || len(d.Layers()) != 2 || steps[1] != "New layer" {
		t.Errorf("after going back: %v at %d with %d layers", steps, done, len(d.Layers()))
	}
	d.GoTo(3)
	if _, done = d.History(); done != 3 || len(d.Layers()) != 3 {
		t.Errorf("after going forward: %d with %d layers", done, len(d.Layers()))
	}
	d.GoTo(0)
	if _, done = d.History(); done != 0 || len(d.Layers()) != 1 {
		t.Errorf("back to the start: %d", done)
	}
}
