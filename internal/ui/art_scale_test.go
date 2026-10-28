package ui

import (
	"image"
	"image/color"
	"math/rand/v2"
	"testing"

	"golang.org/x/image/draw"
	"golang.org/x/image/math/f64"

	"github.com/btelolabs/btelo-forge/internal/paint"
)

// Zoomed in, the view's scaling places every pixel as the library's nearest-neighbour scaling does, and lays it
// over what is there the same way, give or take rounding.
func TestScaleOverMatchesNearest(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	src := image.NewRGBA(image.Rect(-7, 3, 41, 37))
	for i := 0; i < len(src.Pix); i += 4 {
		a := uint8(rng.IntN(256))
		for c := range 3 {
			src.Pix[i+c] = uint8(rng.IntN(int(a) + 1))
		}
		src.Pix[i+3] = a
	}
	for _, scale := range []float64{1, 1.5, 2.75, 8} {
		want := image.NewRGBA(image.Rect(0, 0, 300, 200))
		got := image.NewRGBA(want.Rect)
		for i := range want.Pix {
			want.Pix[i], got.Pix[i] = uint8(i*7), uint8(i*7)
		}
		ox, oy := 12.23, -5.37 // off any tie, which the two round apart
		area := image.Rect(10, 10, 290, 190)
		paint.Resample(draw.NearestNeighbor, want.SubImage(area).(*image.RGBA), f64.Aff3{scale, 0, ox, 0, scale, oy}, src, src.Rect, draw.Over)
		scaleOver(got, area, src, scale, ox, oy, true)
		for y := area.Min.Y; y < area.Max.Y; y++ {
			for x := area.Min.X; x < area.Max.X; x++ {
				// Only where the source is: beyond it the library leaves the view, while the view's scaling repeats
				// the edge, which never shows as the picture is read a pixel beyond what is in view.
				sx, sy := (float64(x)+0.5-ox)/scale, (float64(y)+0.5-oy)/scale
				if sx < float64(src.Rect.Min.X) || sy < float64(src.Rect.Min.Y) || sx >= float64(src.Rect.Max.X) || sy >= float64(src.Rect.Max.Y) {
					continue
				}
				w, g := want.RGBAAt(x, y), got.RGBAAt(x, y)
				if diff(w.R, g.R) > 1 || diff(w.G, g.G) > 1 || diff(w.B, g.B) > 1 || diff(w.A, g.A) > 1 {
					t.Fatalf("scale %v at %d,%d: %v, want %v", scale, x, y, g, w)
				}
			}
		}
	}
}

func diff(a, b uint8) int {
	if a > b {
		return int(a - b)
	}
	return int(b - a)
}

// Zoomed out, each pixel is mixed from its neighbours: an even colour stays even and a ramp stays a ramp.
func TestScaleOverSmooth(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 100, 4))
	for x := range 100 {
		for y := range 4 {
			src.SetRGBA(x, y, color.RGBA{uint8(x * 2), 80, 80, 255})
		}
	}
	dst := image.NewRGBA(image.Rect(0, 0, 70, 3))
	scaleOver(dst, dst.Rect, src, 0.7, 0, 0, false)
	last := -1
	for x := range 70 {
		c := dst.RGBAAt(x, 1)
		if c.G != 80 || c.A != 255 || int(c.R) < last {
			t.Fatalf("at %d: %v after %d", x, c, last)
		}
		last = int(c.R)
	}
	if last < 190 {
		t.Errorf("the ramp ends at %d", last)
	}
}
