package paint

import (
	"image"
	"sync/atomic"
)

// A graphics card may do the slowest filters, when the machine has one and it is free: Compute asks it first and goes on
// the processor's way whenever it cannot, so a filter's result never depends on a card being there. The card's shaders
// are the same steps as the code here, so the two agree to a level or two.

// ToneJob is Shadows/Highlights or Develop for a card: the numbers localFilter's function works with.
type ToneJob struct {
	Radius               float64 // of the surroundings; 0 for none
	Shadows, Highlights  float64 // -1 to 1
	Clarity              float64 // -1 to 1
	Develop              bool    // the rest applies
	Gain                 [3]float64
	Multiply             float64
	Black, Span, Steep   float64
	Vibrance, Saturation float64 // vibrate's amount; saturate's factor
}

// Accelerator does filters on a graphics card. src reaches as far around r as the filter reads; an error means do it
// on the processor.
type Accelerator interface {
	Blur(src *image.RGBA, r image.Rectangle, radius float64) (*image.RGBA, error)
	Tones(src *image.RGBA, r image.Rectangle, job ToneJob) (*image.RGBA, error)
}

var accelerator atomic.Pointer[Accelerator]

// SetAccelerator says what does filters on a graphics card; nil is nothing.
func SetAccelerator(a Accelerator) {
	if a == nil {
		accelerator.Store(nil)
		return
	}
	accelerator.Store(&a)
}

const (
	// minCardPixels is the least a filter works on before a card is worth the trip there and back.
	minCardPixels = 1 << 20
	// cardSide is the side of what goes to a card at once, reach and all: 16 bytes a pixel stays within what a card
	// must bind at once, 128 MB, and a piece this size within the driver's patience.
	cardSide = 2800
)

// onCard works fn out on a card over the pieces; false when it cannot, which leaves the work to the processor.
func (f *FilterSession) onCard(fn Filter, pieces []image.Rectangle) (Result, bool) {
	p := accelerator.Load()
	if p == nil || fn.card == nil {
		return nil, false
	}
	span, total := image.Rectangle{}, 0
	for _, r := range pieces {
		span = span.Union(r)
		total += r.Dx() * r.Dy()
	}
	if total < minCardPixels {
		return nil, false
	}
	side := cardSide - 2*fn.reach
	if side < TileSize {
		return nil, false
	}
	side = side / TileSize * TileSize
	out := make(Result, len(pieces))
	for i := range out {
		out[i] = image.NewRGBA(pieces[i])
	}
	for y := span.Min.Y; y < span.Max.Y; y += side {
		for x := span.Min.X; x < span.Max.X; x += side {
			c := image.Rect(x, y, x+side, y+side).Intersect(span)
			wanted := false
			for _, r := range pieces {
				wanted = wanted || r.Overlaps(c)
			}
			if !wanted {
				continue
			}
			done, err := fn.card(*p, f.source(c.Inset(-fn.reach)), c)
			if err != nil {
				return nil, false
			}
			for i, r := range pieces {
				if part := r.Intersect(c); !part.Empty() {
					copyRect(out[i], done, part)
				}
			}
		}
	}
	return out, true
}
