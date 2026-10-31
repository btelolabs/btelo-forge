package paint

import (
	"image"
	"image/color"
	"math"
	"math/rand/v2"
	"testing"
)

// gradientDoc is a picture lit from left to right, with a fine noise as its texture.
func gradientDoc(t *testing.T, w, h int) *Doc {
	t.Helper()
	d := newDoc(t, w, h, color.NRGBA{A: 255})
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	rng := rand.New(rand.NewPCG(1, 2))
	for y := range h {
		for x := range w {
			v := 40 + 170*float64(x)/float64(w) + rng.Float64()*12 - 6
			img.SetRGBA(x, y, color.RGBA{uint8(v), uint8(v * 0.8), uint8(v * 0.6), 255})
		}
	}
	d.Node(d.Active).Pixels.Write(img)
	return d
}

func spotOn(d *Doc, r image.Rectangle) {
	l := d.Node(d.Active).Pixels
	l.Tiles(r, func(pix []uint8, part image.Rectangle, corner image.Point) {
		for y := part.Min.Y; y < part.Max.Y; y++ {
			for x := part.Min.X; x < part.Max.X; x++ {
				copy(pix[((y-corner.Y)*TileSize+x-corner.X)*4:], []uint8{255, 0, 255, 255})
			}
		}
	})
}

func TestMembraneMeetsItsEdges(t *testing.T) {
	const n = 64
	v := make([]float32, n*n)
	inside := make([]bool, n*n)
	for y := range n {
		for x := range n {
			v[y*n+x] = float32(x) * 2
			inside[y*n+x] = x > 4 && x < n-5 && y > 4 && y < n-5
		}
	}
	want := append([]float32(nil), v...)
	for i, in := range inside {
		if in {
			v[i] = 1000
		}
	}
	membrane(v, inside, n, n)
	for i := range v {
		if math.Abs(float64(v[i]-want[i])) > 1 {
			t.Fatalf("at %d: %.1f, want %.1f", i, v[i], want[i])
		}
	}
}

func TestPatchTakesTheTonesAround(t *testing.T) {
	d := gradientDoc(t, 300, 120)
	spotOn(d, image.Rect(200, 40, 240, 80))
	d.Select(image.Rect(195, 35, 245, 85))
	// From the dark end: its texture, the bright end's tone.
	if err := d.PatchSelection(-150, 0, false); err != nil {
		t.Fatal(err)
	}
	if d.UndoName() != "Patch" {
		t.Errorf("step %q", d.UndoName())
	}
	l := d.Node(d.Active).Pixels
	for x := 196; x < 244; x += 6 {
		want := 40 + 170*float64(x)/300
		if c := l.At(x, 60); math.Abs(float64(c.R)-want) > 14 {
			t.Fatalf("at %d: %v, want a red near %.0f", x, c, want)
		}
	}
	d.Undo()
	if c := l.At(220, 60); c.R != 255 || c.G != 0 {
		t.Log("the layer's raster is a new one after undo")
	}
	if c := d.Node(d.Active).Pixels.At(220, 60); c != (color.RGBA{255, 0, 255, 255}) {
		t.Errorf("undo left %v", c)
	}
}

func TestHealingBrushHealsWhereItWent(t *testing.T) {
	d := gradientDoc(t, 300, 120)
	spotOn(d, image.Rect(190, 56, 240, 64))
	b := Brush{Size: 24, Hardness: 1, Flow: 1, Opacity: 1, Spacing: 0.1, Color: color.NRGBA{A: 255}}
	s, _, err := d.BeginHeal(b, -120, 0, false, 186, 60)
	if err != nil {
		t.Fatal(err)
	}
	s.To(244, 60)
	if c := d.Node(d.Active).Pixels.At(215, 60); c.R > 180 {
		t.Errorf("while drawn it does not clone: %v", c)
	}
	if changed := s.End(); changed.Empty() {
		t.Error("the end of the stroke changed nothing")
	}
	if d.UndoName() != "Healing brush" {
		t.Errorf("step %q", d.UndoName())
	}
	c := d.Node(d.Active).Pixels.At(215, 60)
	if want := 40 + 170*215.0/300; math.Abs(float64(c.R)-want) > 14 || c.G == 0 {
		t.Errorf("healed %v, want a red near %.0f", c, want)
	}
}

// stripes is a picture of even diagonal stripes, which only patches can continue.
func stripes(t *testing.T, w, h int) *Doc {
	t.Helper()
	d := newDoc(t, w, h, color.NRGBA{A: 255})
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			v := uint8(60)
			if (x+y)/10%2 == 0 {
				v = 200
			}
			img.SetRGBA(x, y, color.RGBA{v, v, v, 255})
		}
	}
	d.Node(d.Active).Pixels.Write(img)
	return d
}

func TestContentFillContinuesAPattern(t *testing.T) {
	d := stripes(t, 240, 200)
	hole := image.Rect(95, 70, 145, 120)
	d.Select(hole)
	spotOn(d, hole)
	f, err := d.PrepareContentFill(false)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Compute(); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Apply(); err != nil {
		t.Fatal(err)
	}
	if d.UndoName() != "Content-aware fill" {
		t.Errorf("step %q", d.UndoName())
	}
	l := d.Node(d.Active).Pixels
	good, all := 0, 0
	for y := hole.Min.Y; y < hole.Max.Y; y++ {
		for x := hole.Min.X; x < hole.Max.X; x++ {
			want := uint8(60)
			if (x+y)/10%2 == 0 {
				want = 200
			}
			c := l.At(x, y)
			all++
			if near(c.R, want, 50) && c.R == c.G {
				good++
			}
		}
	}
	if float64(good)/float64(all) < 0.8 {
		t.Errorf("only %d of %d pixels continue the stripes", good, all)
	}
}

func TestSpotHealingRemovesAMark(t *testing.T) {
	d := gradientDoc(t, 300, 200)
	spotOn(d, image.Rect(145, 95, 155, 105))
	b := Brush{Size: 20, Hardness: 1, Flow: 1, Opacity: 1, Spacing: 0.1, Color: color.NRGBA{R: 1, A: 255}}
	s, _, err := d.BeginSpotHeal(b, false, 150, 100)
	if err != nil {
		t.Fatal(err)
	}
	s.To(150, 101)
	s.End()
	if d.UndoName() != "Spot healing" {
		t.Errorf("step %q", d.UndoName())
	}
	want := 40 + 170*150.0/300
	for _, p := range []image.Point{{150, 100}, {146, 96}, {154, 104}} {
		if c := d.Node(d.Active).Pixels.At(p.X, p.Y); math.Abs(float64(c.R)-want) > 22 || c.G == 0 {
			t.Errorf("at %v: %v, want a red near %.0f", p, c, want)
		}
	}
}

func TestHealingOntoAnEmptyLayerFromAllLayers(t *testing.T) {
	d := gradientDoc(t, 200, 120)
	spotOn(d, image.Rect(95, 55, 105, 65))
	d.AddLayer("Repairs")
	s, _, err := d.BeginSpotHeal(Brush{Size: 20, Hardness: 1, Flow: 1, Opacity: 1, Spacing: 0.1}, true, 100, 60)
	if err != nil {
		t.Fatal(err)
	}
	s.End()
	if c := d.Node(d.Active).Pixels.At(100, 60); c.A != 255 || c.G == 0 {
		t.Errorf("the repair on its own layer is %v", c)
	}
	if c := d.Composite(image.Rect(100, 60, 101, 61)).RGBAAt(100, 60); c.G == 0 {
		t.Errorf("the picture still shows %v", c)
	}
}

func TestHealingCannotSampleNothing(t *testing.T) {
	d := New("Empty")
	d.View = image.Rect(0, 0, 100, 100)
	d.Select(image.Rect(10, 10, 30, 30))
	f, err := d.PrepareContentFill(false)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Compute(); err != ErrNothingToSample {
		t.Errorf("filling from nothing: %v", err)
	}
}

func TestHealingRefusesAMask(t *testing.T) {
	d := gradientDoc(t, 60, 60)
	if err := d.AddMask(); err != nil {
		t.Fatal(err)
	}
	d.EditMask = true
	if _, _, err := d.BeginSpotHeal(Brush{Size: 10}, false, 20, 20); err != ErrHealNeedsPixels {
		t.Errorf("spot healing a mask: %v", err)
	}
}

func BenchmarkFill(b *testing.B) {
	const n = 800
	img := image.NewRGBA(image.Rect(0, 0, n, n))
	rng := rand.New(rand.NewPCG(3, 4))
	for i := 0; i < len(img.Pix); i += 4 {
		v := uint8(rng.IntN(60) + 90)
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = v, v, v, 255
	}
	hole := make([]bool, n*n)
	for y := 300; y < 500; y++ {
		for x := 300; x < 500; x++ {
			hole[y*n+x] = true
		}
	}
	for b.Loop() {
		if _, err := fillHole(img, append([]bool(nil), hole...)); err != nil {
			b.Fatal(err)
		}
	}
}
