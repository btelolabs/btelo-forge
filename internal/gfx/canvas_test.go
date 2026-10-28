package gfx

import (
	"image"
	"image/color"
	"math"
	"math/rand/v2"
	"testing"
)

// referenceRound is the plain per-pixel signed-distance rounded rectangle roundShape must reproduce.
func referenceRound(c *Canvas, r Rect, radius, width float32, col color.RGBA) {
	radius = min(radius, r.W/2, r.H/2)
	b := image.Rect(int(math.Floor(float64(r.X))), int(math.Floor(float64(r.Y))), int(math.Ceil(float64(r.X+r.W))), int(math.Ceil(float64(r.Y+r.H)))).Intersect(c.clip)
	cx, cy := r.X+r.W/2, r.Y+r.H/2
	hx, hy := r.W/2-radius, r.H/2-radius
	for y := b.Min.Y; y < b.Max.Y; y++ {
		qy := float32(math.Abs(float64(float32(y)+0.5-cy))) - hy
		for x := b.Min.X; x < b.Max.X; x++ {
			qx := float32(math.Abs(float64(float32(x)+0.5-cx))) - hx
			ox, oy := max(qx, 0), max(qy, 0)
			d := float32(math.Sqrt(float64(ox*ox+oy*oy))) + min(max(qx, qy), 0) - radius
			cov := clamp01(0.5 - d)
			if width >= 0 {
				cov = min(cov, clamp01(d+width+0.5))
			}
			if cov > 0 {
				c.blend(c.Img.PixOffset(x, y), col, cov)
			}
		}
	}
}

func TestRoundShapeMatchesThePerPixelReference(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	for i := 0; i < 400; i++ {
		r := Rect{X: rng.Float32()*60 - 10, Y: rng.Float32()*60 - 10, W: rng.Float32() * 90, H: rng.Float32() * 70}
		radius := rng.Float32() * 30
		width := float32(-1)
		if i%2 == 1 {
			width = rng.Float32() * 4
		}
		col := color.RGBA{uint8(rng.IntN(256)), uint8(rng.IntN(256)), uint8(rng.IntN(256)), uint8(128 + rng.IntN(128))}
		if i%3 == 0 {
			col.A = 255
		}
		got, want := NewCanvas(80, 70), NewCanvas(80, 70)
		got.Clear(Hex(0x203040))
		want.Clear(Hex(0x203040))
		got.roundShape(r, radius, width, col)
		referenceRound(want, r, radius, width, col)
		for k := range got.Img.Pix {
			if d := int(got.Img.Pix[k]) - int(want.Img.Pix[k]); d > 1 || d < -1 {
				t.Fatalf("case %d %+v radius %v width %v: pixel byte %d is %d, want %d", i, r, radius, width, k, got.Img.Pix[k], want.Img.Pix[k])
			}
		}
	}
}

func TestScalerReusesItsResult(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 40, 20))
	var s Scaler
	a := s.Scaled(src, 20, 10)
	if b := s.Scaled(src, 20, 10); b != a {
		t.Fatal("same source and size resampled again")
	}
	if c := s.Scaled(src, 40, 20); c != src {
		t.Fatal("natural size should return the source itself")
	}
	if d := s.Scaled(src, 30, 15); d == src || d.Rect.Dx() != 30 {
		t.Fatal("new size must not write into the source")
	}
}

func TestRoundCornersClearsOnlyTheCorners(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 40, 30))
	for i := range img.Pix {
		img.Pix[i] = 200
	}
	RoundCorners(img, 8)
	alpha := func(x, y int) uint8 { return img.RGBAAt(x, y).A }
	for _, c := range [][2]int{{0, 0}, {39, 0}, {0, 29}, {39, 29}} {
		if alpha(c[0], c[1]) != 0 {
			t.Fatalf("corner %v kept alpha %d", c, alpha(c[0], c[1]))
		}
	}
	for _, c := range [][2]int{{20, 0}, {0, 15}, {39, 15}, {20, 29}, {20, 15}, {5, 5}} {
		if alpha(c[0], c[1]) != 200 {
			t.Fatalf("pixel %v changed to %d", c, alpha(c[0], c[1]))
		}
	}
	if a := alpha(2, 2); a == 0 || a == 200 {
		t.Fatalf("the curve is not anti-aliased: %d", a)
	}
	for y := 0; y < 30; y++ {
		for x := 0; x < 40; x++ {
			p := img.RGBAAt(x, y)
			if p.R > p.A {
				t.Fatalf("pixel %d,%d is not premultiplied: %v", x, y, p)
			}
		}
	}
}

func TestClearToKeepsTransparency(t *testing.T) {
	c := NewCanvas(3, 2)
	c.ClearTo(color.RGBA{0, 0, 0, 110})
	if got := c.Img.RGBAAt(2, 1); got != (color.RGBA{0, 0, 0, 110}) {
		t.Errorf("ClearTo gives %v", got)
	}
	c.Clear(color.RGBA{10, 20, 30, 40})
	if got := c.Img.RGBAAt(2, 1); got.A != 255 {
		t.Errorf("Clear gives %v, want opaque", got)
	}
}

// TestRoundRectClippedOnTheLeft: a rounded rectangle whose straight middle lies wholly left of the clip draws only
// its part inside the clip, never on the row before.
func TestRoundRectClippedOnTheLeft(t *testing.T) {
	c := NewCanvas(64, 64)
	c.Clear(color.RGBA{0, 0, 0, 255})
	c.Push(Rect{X: 0, Y: 0, W: 64, H: 64})
	c.RoundRect(Rect{X: -60, Y: 10, W: 66, H: 40}, 8, color.RGBA{255, 0, 0, 255})
	c.Pop()
	for y := range 64 {
		for x := 8; x < 64; x++ {
			if p := c.Img.RGBAAt(x, y); p.R != 0 {
				t.Fatalf("drawn at %d,%d, outside the rectangle", x, y)
			}
		}
	}
	if c.Img.RGBAAt(2, 30).R == 0 {
		t.Fatal("the part inside the clip was not drawn")
	}
}

func TestPieWedgesGoClockwiseFromTwelveWithAGapBetween(t *testing.T) {
	bg, red, blue := Hex(0x101010), Hex(0xff0000), Hex(0x0000ff)
	c := NewCanvas(100, 100)
	c.Clear(bg)
	// The right half, then the left half reaching further out.
	c.Pie(50, 50, []Wedge{{From: 0, To: math.Pi, Radius: 30, Col: red}, {From: math.Pi, To: 2 * math.Pi, Radius: 40, Col: blue}}, 4)
	at := func(x, y int) color.RGBA { return c.Img.RGBAAt(x, y) }
	for _, p := range []struct {
		x, y int
		want color.RGBA
		what string
	}{
		{70, 50, red, "right of the centre"},
		{30, 50, blue, "left of the centre"},
		{12, 50, blue, "left, beyond the first wedge's reach"},
		{88, 50, bg, "right, beyond the first wedge's reach"},
		{50, 30, bg, "straight up, in the gap"},
		{50, 75, bg, "straight down, in the gap"},
		{50, 5, bg, "above the pie"},
	} {
		if got := at(p.x, p.y); got != p.want {
			t.Errorf("%s (%d,%d) is %v, want %v", p.what, p.x, p.y, got, p.want)
		}
	}
	// The edge is anti-aliased: a pixel straddling the rim is partly covered.
	if got := at(79, 50); got == red || got == bg {
		t.Errorf("the rim pixel is %v, not a blend", got)
	}
}

func TestPieOfOneWholeTurnHasNoSeam(t *testing.T) {
	c := NewCanvas(60, 60)
	c.Clear(Hex(0x101010))
	c.Pie(30, 30, []Wedge{{From: 1, To: 1 + 2*math.Pi, Radius: 20, Col: Hex(0x00ff00)}}, 4)
	for y := 15; y < 45; y++ {
		for x := 15; x < 45; x++ {
			if dx, dy := float64(x)+0.5-30, float64(y)+0.5-30; math.Hypot(dx, dy) < 19 && c.Img.RGBAAt(x, y) != Hex(0x00ff00) {
				t.Fatalf("(%d,%d) inside the disc is %v", x, y, c.Img.RGBAAt(x, y))
			}
		}
	}
}
