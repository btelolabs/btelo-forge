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
