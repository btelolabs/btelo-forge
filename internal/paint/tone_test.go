package paint

import (
	"image"
	"image/color"
	"testing"
)

func TestColorBalanceWorksByTone(t *testing.T) {
	var shadows, highlights [9]float64
	shadows[0], highlights[6] = 60, 60 // more red: in the shadows, and in the highlights
	dark, light := color.NRGBA{R: 40, G: 40, B: 40, A: 255}, color.NRGBA{R: 215, G: 215, B: 215, A: 255}
	if c := pixelAfter(t, dark, ColorBalance(shadows, false)); c.R <= 60 || c.G != 40 {
		t.Errorf("shadows on a dark pixel %v", c)
	}
	if c := pixelAfter(t, light, ColorBalance(shadows, false)); c.R != 215 {
		t.Errorf("shadows on a light pixel %v", c)
	}
	if c := pixelAfter(t, light, ColorBalance(highlights, false)); c.R <= 215 {
		t.Errorf("highlights on a light pixel %v", c)
	}
	// Nothing asked, nothing changes, with lightness kept or not.
	grey := color.NRGBA{R: 90, G: 120, B: 200, A: 255}
	for _, keep := range []bool{false, true} {
		if c := pixelAfter(t, grey, ColorBalance([9]float64{}, keep)); !near(c.R, 90, 1) || !near(c.G, 120, 1) || !near(c.B, 200, 1) {
			t.Errorf("neutral %v, keeping lightness %v", c, keep)
		}
	}
	// Keeping lightness, a colour shift does not brighten.
	c := pixelAfter(t, color.NRGBA{R: 128, G: 128, B: 128, A: 255}, ColorBalance([9]float64{3: 100}, true))
	if mx, mn := max(c.R, c.G, c.B), min(c.R, c.G, c.B); c.R <= c.G || !near(uint8((int(mx)+int(mn))/2), 128, 2) {
		t.Errorf("lightness not kept: %v", c)
	}
}

func TestVibranceFavoursDullColours(t *testing.T) {
	dull, vivid := color.NRGBA{R: 110, G: 130, B: 150, A: 255}, color.NRGBA{R: 20, G: 60, B: 240, A: 255}
	gain := func(c color.RGBA, was color.NRGBA) float64 {
		return float64(int(c.B)-int(c.R)) / float64(int(was.B)-int(was.R))
	}
	if d, v := gain(pixelAfter(t, dull, Vibrance(80, 0)), dull), gain(pixelAfter(t, vivid, Vibrance(80, 0)), vivid); d <= v || d <= 1.2 {
		t.Errorf("dull gained %.2f, vivid %.2f", d, v)
	}
	// Skin gains less than a blue as dull as it.
	skin, sky := color.NRGBA{R: 205, G: 160, B: 135, A: 255}, color.NRGBA{R: 135, G: 160, B: 205, A: 255}
	if a, b := pixelAfter(t, skin, Vibrance(100, 0)), pixelAfter(t, sky, Vibrance(100, 0)); int(a.R)-int(a.B) >= int(b.B)-int(b.R) {
		t.Errorf("skin %v, sky %v", a, b)
	}
	if c := pixelAfter(t, dull, Vibrance(0, -100)); !near(c.R, c.B, 1) {
		t.Errorf("no saturation left colour in %v", c)
	}
	if c := pixelAfter(t, dull, Vibrance(0, 0)); c != (color.RGBA{110, 130, 150, 255}) {
		t.Errorf("neutral %v", c)
	}
}

func TestSelectiveColorKeepsToItsColour(t *testing.T) {
	var noCyanInReds [selectiveCount * 4]float64
	noCyanInReds[selectiveReds*4] = -100 // less cyan: more red, in reds
	red, blue := color.NRGBA{R: 180, G: 40, B: 40, A: 255}, color.NRGBA{R: 40, G: 40, B: 180, A: 255}
	if c := pixelAfter(t, red, SelectiveColor(noCyanInReds, false)); c.R <= 200 || c.G != 40 {
		t.Errorf("reds %v", c)
	}
	if c := pixelAfter(t, blue, SelectiveColor(noCyanInReds, false)); c != (color.RGBA{40, 40, 180, 255}) {
		t.Errorf("a blue changed: %v", c)
	}
	// Black in the neutrals darkens a grey; relative does so in proportion to what there is.
	var black [selectiveCount * 4]float64
	black[selectiveNeutrals*4+3] = 50
	grey := color.NRGBA{R: 128, G: 128, B: 128, A: 255}
	abs, rel := pixelAfter(t, grey, SelectiveColor(black, false)), pixelAfter(t, grey, SelectiveColor(black, true))
	if abs.R >= 128 || rel.R >= 128 || rel.R <= abs.R {
		t.Errorf("absolute %v, relative %v", abs, rel)
	}
}

func TestPhotoFilterAndGradientMapAndExposure(t *testing.T) {
	grey := color.NRGBA{R: 128, G: 128, B: 128, A: 255}
	warm := PhotoFilter(50, 255, 128, 0, true)
	c := pixelAfter(t, grey, warm)
	if c.R <= c.G || c.G <= c.B || !near(luma8([]byte{c.R, c.G, c.B, 255}), 128, 2) {
		t.Errorf("warmed %v", c)
	}
	if c := pixelAfter(t, grey, PhotoFilter(0, 255, 0, 0, false)); c != (color.RGBA{128, 128, 128, 255}) {
		t.Errorf("no density %v", c)
	}
	duo := GradientMap(50, [3]float64{0, 0, 80}, [3]float64{255, 200, 0})
	if c := pixelAfter(t, color.NRGBA{A: 255}, duo); c != (color.RGBA{0, 0, 80, 255}) {
		t.Errorf("black maps to %v", c)
	}
	if c := pixelAfter(t, color.NRGBA{R: 255, G: 255, B: 255, A: 255}, duo); c != (color.RGBA{255, 200, 0, 255}) {
		t.Errorf("white maps to %v", c)
	}
	// A midpoint below the middle reaches halfway sooner.
	if a, b := pixelAfter(t, grey, GradientMap(20, [3]float64{}, [3]float64{255, 255, 255})), pixelAfter(t, grey, GradientMap(50, [3]float64{}, [3]float64{255, 255, 255})); a.R <= b.R {
		t.Errorf("midpoint: %v, %v", a, b)
	}
	if c := pixelAfter(t, grey, Exposure(0, 0, 1)); !near(c.R, 128, 1) {
		t.Errorf("neutral exposure %v", c)
	}
	// A stop is twice the light: 188 is about twice 128 in sRGB.
	if c := pixelAfter(t, grey, Exposure(1, 0, 1)); !near(c.R, 175, 2) {
		t.Errorf("one stop %v", c)
	}
}

// halves is a picture dark on the left and light on the right, with a small step of detail in each half.
func halves(t *testing.T) *Doc {
	t.Helper()
	d := newDoc(t, 200, 100, color.NRGBA{R: 40, G: 40, B: 40, A: 255})
	d.Select(image.Rect(100, 0, 200, 100))
	if err := d.Fill(color.NRGBA{R: 220, G: 220, B: 220, A: 255}); err != nil {
		t.Fatal(err)
	}
	d.Deselect()
	return d
}

func TestShadowsHighlightsLeaveBlackWhiteAndTheOtherHalf(t *testing.T) {
	d := halves(t)
	if err := d.Run("Shadows/Highlights", ShadowsHighlights(60, 0, 10)); err != nil {
		t.Fatal(err)
	}
	l := d.Node(d.Active).Pixels
	if c := l.At(20, 50); c.R <= 60 {
		t.Errorf("the shadows stayed %v", c)
	}
	if c := l.At(180, 50); !near(c.R, 220, 3) {
		t.Errorf("the highlights moved to %v", c)
	}
	d = halves(t)
	if err := d.Run("Shadows/Highlights", ShadowsHighlights(0, 60, 10)); err != nil {
		t.Fatal(err)
	}
	l = d.Node(d.Active).Pixels
	if c, s := l.At(180, 50), l.At(20, 50); c.R >= 200 || !near(s.R, 40, 3) {
		t.Errorf("highlights %v, shadows %v", c, s)
	}
	for _, v := range []color.NRGBA{{A: 255}, {R: 255, G: 255, B: 255, A: 255}} {
		if c := pixelAfter(t, v, ShadowsHighlights(100, 100, 5)); c.R != v.R {
			t.Errorf("%v became %v", v, c)
		}
	}
}

func TestDevelop(t *testing.T) {
	grey := color.NRGBA{R: 128, G: 128, B: 128, A: 255}
	if c := pixelAfter(t, grey, Develop{}.Filter()); !near(c.R, 128, 1) || !near(c.G, 128, 1) || !near(c.B, 128, 1) {
		t.Errorf("neutral %v", c)
	}
	if c := pixelAfter(t, grey, Develop{Temperature: 60}.Filter()); c.R <= c.B {
		t.Errorf("warmer %v", c)
	}
	if c := pixelAfter(t, grey, Develop{Tint: 60}.Filter()); c.G >= c.R {
		t.Errorf("more magenta %v", c)
	}
	if c := pixelAfter(t, grey, Develop{Exposure: 1}.Filter()); !near(c.R, 175, 3) {
		t.Errorf("one stop %v", c)
	}
	dark := color.NRGBA{R: 80, G: 80, B: 80, A: 255}
	if c := pixelAfter(t, dark, Develop{Contrast: 60}.Filter()); c.R >= 80 {
		t.Errorf("contrast left a dark at %v", c)
	}
	if c := pixelAfter(t, dark, Develop{Blacks: -60}.Filter()); c.R >= 80 {
		t.Errorf("lower blacks left a dark at %v", c)
	}
	// Clarity raises fine detail: two close tones side by side move apart.
	d := newDoc(t, 200, 100, color.NRGBA{R: 120, G: 120, B: 120, A: 255})
	d.Select(image.Rect(100, 0, 200, 100))
	if err := d.Fill(color.NRGBA{R: 136, G: 136, B: 136, A: 255}); err != nil {
		t.Fatal(err)
	}
	d.Deselect()
	if err := d.Run("Develop", Develop{Clarity: 100}.Filter()); err != nil {
		t.Fatal(err)
	}
	l := d.Node(d.Active).Pixels
	if a, b := l.At(97, 50), l.At(102, 50); a.R >= 120 || b.R <= 136 {
		t.Errorf("clarity left the two sides of a soft step at %v and %v", a, b)
	}
}

// Highlights must bring back what Exposure and Contrast would otherwise clip, however hard they push.
func TestDevelopRecoversHighlightsBeforeClipping(t *testing.T) {
	sky := color.NRGBA{R: 225, G: 235, B: 240, A: 255}
	plain := pixelAfter(t, sky, Develop{Exposure: 0.3, Contrast: 15}.Filter())
	saved := pixelAfter(t, sky, Develop{Exposure: 0.3, Contrast: 15, Highlights: 100}.Filter())
	if plain.R != 255 || saved.R >= 250 {
		t.Errorf("clipped to %v, with highlights %v", plain, saved)
	}
}

// An edge strong enough to see must not leave a halo: past it each side keeps its own tone.
func TestShadowsLeaveNoHaloAlongAnEdge(t *testing.T) {
	d := halves(t)
	if err := d.Run("Shadows/Highlights", ShadowsHighlights(80, 80, 30)); err != nil {
		t.Fatal(err)
	}
	l := d.Node(d.Active).Pixels
	for _, x := range []int{115, 104, 101} {
		if a, b := l.At(x, 50), l.At(180, 50); !near(a.R, b.R, 4) {
			t.Errorf("at %d the light side is %v, far from the edge %v", x, a, b)
		}
	}
	for _, x := range []int{85, 96, 98} {
		if a, b := l.At(x, 50), l.At(20, 50); !near(a.R, b.R, 4) {
			t.Errorf("at %d the dark side is %v, far from the edge %v", x, a, b)
		}
	}
}

// Worked out tile by tile, a result must not show the tiles.
func TestSurroundingsMatchAcrossTiles(t *testing.T) {
	size := TileSize*2 + 40
	d := newDoc(t, size, 64, color.NRGBA{R: 200, G: 90, B: 60, A: 255})
	d.Select(image.Rect(TileSize-30, 0, TileSize+50, 64))
	if err := d.Fill(color.NRGBA{R: 30, G: 40, B: 80, A: 255}); err != nil {
		t.Fatal(err)
	}
	d.Deselect()
	if err := d.Run("Shadows/Highlights", ShadowsHighlights(70, 70, 25)); err != nil {
		t.Fatal(err)
	}
	l := d.Node(d.Active).Pixels
	for x := TileSize - 3; x < TileSize+3; x++ {
		if a, b := l.At(x, 20), l.At(x+1, 20); !near(a.R, b.R, 2) {
			t.Fatalf("a step between %d and %d: %v, %v", x, x+1, a, b)
		}
	}
}

func TestTheNewAdjustmentsAreAdjustmentLayers(t *testing.T) {
	d := newDoc(t, 16, 16, color.NRGBA{R: 128, G: 128, B: 128, A: 255})
	cases := []Adjustment{
		{Kind: AdjustColorBalance, Values: make([]float64, 9), On: true},
		{Kind: AdjustVibrance, Values: []float64{30, 0}},
		{Kind: AdjustSelectiveColor, Values: make([]float64, 36), On: true},
		{Kind: AdjustPhotoFilter, Values: []float64{25, 236, 138, 0}, On: true},
		{Kind: AdjustGradientMap, Values: []float64{50, 0, 0, 0, 255, 255, 255}},
		{Kind: AdjustExposure, Values: []float64{0, 0, 1}},
	}
	for _, adj := range cases {
		n, err := d.AddAdjustment(adj, "Test")
		if err != nil {
			t.Fatalf("kind %d: %v", adj.Kind, err)
		}
		if err := d.Delete(); err != nil {
			t.Fatal(err)
		}
		_ = n
	}
	if _, err := d.AddAdjustment(Adjustment{Kind: AdjustVibrance, Values: []float64{1}}, "Test"); err == nil {
		t.Error("a vibrance with one value was taken")
	}
	// A gradient map's layer changes what shows and leaves the pixels under it.
	if _, err := d.AddAdjustment(Adjustment{Kind: AdjustGradientMap, Values: []float64{50, 255, 0, 0, 255, 0, 0}}, "Red"); err != nil {
		t.Fatal(err)
	}
	if c := d.Composite(image.Rect(4, 4, 5, 5)).RGBAAt(4, 4); c != (color.RGBA{255, 0, 0, 255}) {
		t.Errorf("shown %v", c)
	}
	if c := d.Layers()[0].Pixels; c != nil && c.At(4, 4).G == 0 {
		t.Error("the pixels under the adjustment changed")
	}
}
