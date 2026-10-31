package gpufx

import (
	"image"
	"image/color"
	"math/rand/v2"
	"sync"
	"testing"
	"time"

	gpuwgpu "github.com/btelolabs/btelo-forge/internal/gpu/wgpu"
	"github.com/btelolabs/btelo-forge/internal/paint"
)

var turn sync.Mutex

// picture is a canvas with smooth light, hard edges, grain and transparent corners, of w×h.
func picture(t testing.TB, w, h int) *paint.Doc {
	t.Helper()
	d, err := paint.NewFramed(w, h, "Background", color.NRGBA{})
	if err != nil {
		t.Fatal(err)
	}
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	rng := rand.New(rand.NewPCG(8, 9))
	for y := range h {
		for x := range w {
			v := 40 + 170*float64(x)/float64(w) + rng.Float64()*14
			c := color.RGBA{uint8(v), uint8(v * 0.7), uint8(255 - v), 255}
			if (x/300+y/200)%2 == 0 {
				c = color.RGBA{uint8(v * 0.3), uint8(v * 0.9), uint8(v * 0.5), 255}
			}
			if x < 40 && y < 40 {
				c = color.RGBA{}
			}
			img.SetRGBA(x, y, c)
		}
	}
	d.Node(d.Active).Pixels.Write(img)
	return d
}

func result(t *testing.T, d *paint.Doc, f paint.Filter) *image.RGBA {
	t.Helper()
	s, err := d.BeginFilter()
	if err != nil {
		t.Fatal(err)
	}
	s.Preview(f)
	out := d.Composite(d.Frame)
	s.Cancel()
	return out
}

func worst(a, b *image.RGBA) (diff int, at image.Point) {
	for y := a.Rect.Min.Y; y < a.Rect.Max.Y; y++ {
		for x := a.Rect.Min.X; x < a.Rect.Max.X; x++ {
			i, j := a.PixOffset(x, y), b.PixOffset(x, y)
			for c := range 4 {
				if v := int(a.Pix[i+c]) - int(b.Pix[j+c]); v > diff || -v > diff {
					diff, at = max(v, -v), image.Pt(x, y)
				}
			}
		}
	}
	return diff, at
}

func TestTheCardAndTheProcessorAgree(t *testing.T) {
	card := New(gpuwgpu.Shared, &turn)
	if _, err := card.with(func(*kit) (*image.RGBA, error) { return nil, nil }); err != nil {
		t.Skip(err)
	}
	defer paint.SetAccelerator(nil)
	d := picture(t, 1600, 1000)
	for name, f := range map[string]paint.Filter{
		"blur":    paint.GaussianBlur(40),
		"tones":   paint.ShadowsHighlights(50, 40, 30),
		"develop": paint.Develop{Temperature: 20, Tint: -10, Exposure: 0.4, Contrast: 20, Highlights: 40, Shadows: 30, Whites: 10, Blacks: -10, Clarity: 30, Vibrance: 40, Saturate: 10}.Filter(),
		"flat":    paint.Develop{Exposure: 0.5, Contrast: 10, Vibrance: 20}.Filter(),
	} {
		t.Run(name, func(t *testing.T) {
			paint.SetAccelerator(nil)
			start := time.Now()
			cpu := result(t, d, f)
			cpuTime := time.Since(start)
			paint.SetAccelerator(card)
			before := card.done.Load()
			start = time.Now()
			gpu := result(t, d, f)
			gpuTime := time.Since(start)
			if card.failed.Load() {
				t.Fatal("the card failed")
			}
			if card.done.Load() == before {
				t.Fatal("the card did nothing: the processor did it again")
			}
			t.Logf("processor %v, card %v", cpuTime, gpuTime)
			if diff, at := worst(cpu, gpu); diff > 3 {
				t.Errorf("they differ by %d at %v: %v and %v", diff, at, cpu.RGBAAt(at.X, at.Y), gpu.RGBAAt(at.X, at.Y))
			}
		})
	}
}

func TestALargePictureGoesInPieces(t *testing.T) {
	card := New(gpuwgpu.Shared, &turn)
	if _, err := card.with(func(*kit) (*image.RGBA, error) { return nil, nil }); err != nil {
		t.Skip(err)
	}
	defer paint.SetAccelerator(nil)
	d := picture(t, 6000, 4000)
	f := paint.GaussianBlur(100)
	paint.SetAccelerator(nil)
	start := time.Now()
	cpu := result(t, d, f)
	cpuTime := time.Since(start)
	paint.SetAccelerator(card)
	start = time.Now()
	gpu := result(t, d, f)
	t.Logf("processor %v, card %v, %d pieces", cpuTime, time.Since(start), card.done.Load())
	if card.done.Load() < 2 {
		t.Fatal("it did not go in pieces on the card")
	}
	if diff, at := worst(cpu, gpu); diff > 3 {
		t.Errorf("they differ by %d at %v", diff, at)
	}
}

// fails is a card that never works.
type fails struct{}

func (fails) Blur(*image.RGBA, image.Rectangle, float64) (*image.RGBA, error) {
	return nil, ErrBusy
}
func (fails) Tones(*image.RGBA, image.Rectangle, paint.ToneJob) (*image.RGBA, error) {
	return nil, ErrBusy
}

func TestWithoutACardTheProcessorDoesIt(t *testing.T) {
	d := picture(t, 1400, 900)
	f := paint.GaussianBlur(30)
	paint.SetAccelerator(nil)
	want := result(t, d, f)
	paint.SetAccelerator(fails{})
	defer paint.SetAccelerator(nil)
	if diff, _ := worst(want, result(t, d, f)); diff != 0 {
		t.Errorf("a card that failed changed the result by %d", diff)
	}
}

func TestABusyCardIsNotWaitedFor(t *testing.T) {
	var held sync.Mutex
	held.Lock()
	defer held.Unlock()
	c := New(gpuwgpu.Shared, &held)
	if _, err := c.Blur(image.NewRGBA(image.Rect(0, 0, 8, 8)), image.Rect(0, 0, 8, 8), 2); err != ErrBusy {
		t.Errorf("%v", err)
	}
	if c.failed.Load() {
		t.Error("being busy counted as failing")
	}
}
