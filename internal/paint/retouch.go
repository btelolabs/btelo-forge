package paint

import (
	"errors"
	"image"
	"math"
)

// The retouching brushes of Photoshop's toolbox: dodge, burn and sponge change the pixels under the brush in tone or
// colour, blur and sharpen soften or crisp them, and smudge drags them along the stroke. All but smudge work as the
// brush does: the change is worked out from the pixels as they were when the stroke began and laid on by the
// stroke's coverage, so one stroke never goes past its strength however often it crosses itself. Smudge carries the
// colour under the brush from dab to dab, so it works on the pixels as they are.

// RetouchKind is what a retouching brush does.
type RetouchKind int

const (
	RetouchDodge RetouchKind = iota
	RetouchBurn
	RetouchSponge
	RetouchBlur
	RetouchSharpen
	RetouchSmudge
	RetouchLiquify
	retouchCount
)

// ToneRange is which tones dodge and burn change most, as Photoshop's Range.
type ToneRange int

const (
	RangeShadows ToneRange = iota
	RangeMidtones
	RangeHighlights
)

// Retouch is a retouching brush's settings: what it does, the tones dodge and burn change most, whether the sponge
// takes colour away, and its strength (Photoshop's exposure, flow or strength), 0 to 1.
type Retouch struct {
	Kind       RetouchKind
	Range      ToneRange
	Desaturate bool
	Strength   float64
	Liquify    LiquifyMode
}

var retouchNames = []string{"Dodge", "Burn", "Sponge", "Blur", "Sharpen", "Smudge", "Liquify"}

// dabsItself reports whether the brush changes the pixels itself at each dab, rather than through the stroke's coverage.
func (rt *Retouch) dabsItself() bool { return rt.Kind == RetouchSmudge || rt.Kind == RetouchLiquify }

// ErrRetouchMask means a retouching brush was used on a layer mask, which has no tones or colours to change.
var ErrRetouchMask = errors.New("the retouching tools work on a layer's pixels, not its mask")

// BeginRetouch starts a retouching stroke at (x, y) with the brush's tip, size and hardness on the chosen layer,
// within the selection.
func (d *Doc) BeginRetouch(b Brush, rt Retouch, x, y float64) (*Stroke, image.Rectangle, error) {
	if rt.Kind < 0 || rt.Kind >= retouchCount {
		return nil, image.Rectangle{}, errors.New("unknown retouching tool")
	}
	if l, err := d.pixelLayer(); err == nil && d.EditMask && l.Mask != nil {
		return nil, image.Rectangle{}, ErrRetouchMask
	}
	rt.Strength = min(max(rt.Strength, 0), 1)
	b.Opacity = rt.Strength // the strength bounds the stroke as a brush's opacity does
	b.Flow = 1
	if rt.dabsItself() {
		b.Spacing = min(b.Spacing, 0.1) // what they carry goes from dab to dab, so dabs must overlap
	}
	r := rt
	return d.beginStrokeWith(b, &r, x, y)
}

// retouchRadius is how far around a pixel blur and sharpen look, for a brush of the size given.
func retouchRadius(size float64) int { return max(1, int(math.Round(size/12))) }

// retouchTarget is what the retouching brush makes of the pixels before over r: the stroke's full effect.
func (s *Stroke) retouchTarget(before *image.RGBA, r image.Rectangle) *image.RGBA {
	rt := s.retouch
	out := image.NewRGBA(r)
	switch rt.Kind {
	case RetouchBlur, RetouchSharpen:
		rad := retouchRadius(s.brush.Size)
		wide := s.beforePix.Read(r.Inset(-3 * rad))
		blurred := boxBlur3(wide, rad)
		for y := r.Min.Y; y < r.Max.Y; y++ {
			for x := r.Min.X; x < r.Max.X; x++ {
				bi, oi, wi := before.PixOffset(x, y), out.PixOffset(x, y), blurred.PixOffset(x, y)
				for c := range 4 {
					v := float64(blurred.Pix[wi+c])
					if rt.Kind == RetouchSharpen {
						// Unsharp masking: away from the blur by as much again.
						v = 2*float64(before.Pix[bi+c]) - v
					}
					out.Pix[oi+c] = uint8(min(max(math.Round(v), 0), 255))
				}
				if rt.Kind == RetouchSharpen {
					// Premultiplied colour stays within its alpha.
					a := out.Pix[oi+3]
					for c := range 3 {
						out.Pix[oi+c] = min(out.Pix[oi+c], a)
					}
				}
			}
		}
	default:
		for y := r.Min.Y; y < r.Max.Y; y++ {
			for x := r.Min.X; x < r.Max.X; x++ {
				i, oi := before.PixOffset(x, y), out.PixOffset(x, y)
				p, o := before.Pix[i:i+4:i+4], out.Pix[oi:oi+4:oi+4]
				a := float64(p[3])
				o[3] = p[3]
				if a == 0 {
					continue
				}
				cr, cg, cb := float64(p[0])/a, float64(p[1])/a, float64(p[2])/a
				cr, cg, cb = rt.tone(cr, cg, cb)
				o[0], o[1], o[2] = uint8(math.Round(min(max(cr, 0), 1)*a)), uint8(math.Round(min(max(cg, 0), 1)*a)), uint8(math.Round(min(max(cb, 0), 1)*a))
			}
		}
	}
	return out
}

// tone is dodge's, burn's or the sponge's full effect on a colour (0 to 1 a channel).
func (rt *Retouch) tone(r, g, b float64) (float64, float64, float64) {
	l := 0.299*r + 0.587*g + 0.114*b
	switch rt.Kind {
	case RetouchSponge:
		k := 1.0 // twice as far from grey, or all the way to it
		if rt.Desaturate {
			k = -1
		}
		f := func(c float64) float64 { return l + (c-l)*(1+k) }
		return f(r), f(g), f(b)
	}
	// How much the tone at l is changed: most in the chosen range, fading out of it, as Photoshop's ranges do.
	var w float64
	switch rt.Range {
	case RangeShadows:
		w = math.Pow(1-l, 4)
	case RangeHighlights:
		w = math.Pow(l, 4)
	default:
		m := 4 * l * (1 - l)
		w = m * m
	}
	if rt.Kind == RetouchDodge {
		f := func(c float64) float64 { return c + (1-c)*w }
		return f(r), f(g), f(b)
	}
	f := func(c float64) float64 { return c * (1 - w) }
	return f(r), f(g), f(b)
}

// boxBlur3 blurs src with three passes of a box of radius rad each way, which is close to a Gaussian.
func boxBlur3(src *image.RGBA, rad int) *image.RGBA {
	r := src.Rect
	w, h := r.Dx(), r.Dy()
	a := make([]float32, w*h*4)
	for i := range a {
		a[i] = float32(src.Pix[i/4/w*src.Stride+(i/4%w)*4+i%4])
	}
	b := make([]float32, len(a))
	pass := func(from, to []float32, horizontal bool) {
		n, lines := w, h
		step := 4
		if !horizontal {
			n, lines, step = h, w, w*4
		}
		inv := float32(1) / float32(2*rad+1)
		for line := range lines {
			base := line * w * 4
			if !horizontal {
				base = line * 4
			}
			for c := range 4 {
				// A running sum, the edges repeated.
				at := func(i int) float32 { return from[base+min(max(i, 0), n-1)*step+c] }
				var sum float32
				for i := -rad; i <= rad; i++ {
					sum += at(i)
				}
				for i := range n {
					to[base+i*step+c] = sum * inv
					sum += at(i+rad+1) - at(i-rad)
				}
			}
		}
	}
	for range 3 {
		pass(a, b, true)
		pass(b, a, false)
	}
	out := image.NewRGBA(r)
	for i := range a {
		out.Pix[i/4/w*out.Stride+(i/4%w)*4+i%4] = uint8(min(max(a[i]+0.5, 0), 255))
	}
	return out
}

// smudgeDab drags the colour carried from the last dab into the pixels under the brush at (cx, cy), and picks up
// what is there for the next, as Photoshop's smudge does.
func (s *Stroke) smudgeDab(cx, cy float64) image.Rectangle {
	b := s.brush
	r := b.reach()
	box := image.Rect(int(math.Floor(cx-r)), int(math.Floor(cy-r)), int(math.Ceil(cx+r)), int(math.Ceil(cy+r))).Intersect(s.clip)
	if box.Empty() {
		return box
	}
	// The carried colour is kept relative to the dab's centre, in whole pixels.
	ox, oy := int(math.Floor(cx)), int(math.Floor(cy))
	side := int(math.Ceil(r))*2 + 2
	cur := s.layer.Pixels.Read(box)
	if s.carried == nil {
		s.carried = make([]float32, side*side*4)
		for y := box.Min.Y; y < box.Max.Y; y++ {
			for x := box.Min.X; x < box.Max.X; x++ {
				if k := s.carryIndex(x-ox, y-oy, side); k >= 0 {
					i := cur.PixOffset(x, y)
					for c := range 4 {
						s.carried[k+c] = float32(cur.Pix[i+c])
					}
				}
			}
		}
	}
	sel := s.doc.selectionIn(box)
	strength := float32(b.Opacity)
	lock := s.layer.LockAlpha
	for y := box.Min.Y; y < box.Max.Y; y++ {
		for x := box.Min.X; x < box.Max.X; x++ {
			k := s.carryIndex(x-ox, y-oy, side)
			cov := b.coverage(float64(x)+0.5, float64(y)+0.5, cx, cy) * float64(coverage(sel, x, y)) / 255
			if k < 0 || cov <= 0 {
				continue
			}
			t := float32(cov) * strength
			i := cur.PixOffset(x, y)
			alpha := cur.Pix[i+3]
			for c := range 4 {
				v := float32(cur.Pix[i+c])
				out := v + (s.carried[k+c]-v)*t
				cur.Pix[i+c] = uint8(min(max(out+0.5, 0), 255))
				// The brush picks up what it leaves, so the colour runs on and fades.
				s.carried[k+c] += (out - s.carried[k+c]) * float32(cov)
			}
			if lock {
				keepAlpha(cur.Pix[i:i+4], uint32(alpha))
			}
		}
	}
	s.layer.Pixels.Write(cur)
	return box
}

// carryIndex is where the carried colour for offset (dx, dy) from the dab's centre is, or -1 outside it.
func (s *Stroke) carryIndex(dx, dy, side int) int {
	x, y := dx+side/2, dy+side/2
	if x < 0 || y < 0 || x >= side || y >= side {
		return -1
	}
	return (y*side + x) * 4
}
