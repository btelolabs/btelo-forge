package paint

import (
	"image"
	"math"
	"math/rand/v2"
	"sort"
)

// Filter is an adjustment or a filter: it makes new pixels for an area from the pixels as they were there and up to
// reach pixels around (which blurs read). Pixels are premultiplied.
type Filter struct {
	reach int
	apply func(src *image.RGBA, r image.Rectangle) *image.RGBA
}

// FilterSession is an adjustment or filter being tried on the chosen layer (or its mask): Preview shows a filter
// from the pixels as they were, Apply records it as one step, Cancel puts the pixels back, as Photoshop's
// adjustment dialogs do with Preview on. It works tile by tile over the layer's work area, so a layer with
// pixels far apart costs what its pixels do.
type FilterSession struct {
	d          *Doc
	l          *Node
	mark       Mark
	toMask     bool
	beforePix  *Raster
	beforeMask *Mask
	shown      []image.Rectangle // what the last Show changed
}

// Result is a filter worked out by Compute, ready to Show: a piece for each tile it changes.
type Result []*image.RGBA

// BeginFilter starts trying adjustments or filters on the chosen layer, within the selection.
func (d *Doc) BeginFilter() (*FilterSession, error) {
	l, err := d.pixelLayer()
	if err != nil {
		return nil, err
	}
	f := &FilterSession{d: d, l: l, mark: d.Mark(), toMask: d.EditMask && l.Mask != nil}
	if f.toMask {
		f.beforeMask = l.Mask.Snapshot()
		return f, nil
	}
	d.dropText(l)
	f.beforePix = l.Pixels.Snapshot()
	return f, nil
}

// own is the plane being filtered, as it was.
func (f *FilterSession) own() *plane {
	if f.toMask {
		return &f.beforeMask.p
	}
	return &f.beforePix.p
}

// Area spans what a filter reading nothing around a pixel can change, for a histogram of it.
func (f *FilterSession) Area() image.Rectangle { return spanOf(f.d.workArea(f.own(), 0)) }

// Pieces are the areas Area spans, one a tile, for reading what is there without reading what is not.
func (f *FilterSession) Pieces() []image.Rectangle { return f.d.workArea(f.own(), 0) }

// source is the pixels as they were over r, a mask as grey. Past a frame's edge the edge goes on, as Photoshop's
// filters see the canvas's edge, so a blur does not darken it.
func (f *FilterSession) source(r image.Rectangle) *image.RGBA {
	inner := r
	if !f.d.Frame.Empty() {
		inner = r.Intersect(f.d.Frame)
	}
	var img *image.RGBA
	if !f.toMask {
		img = f.beforePix.Read(inner)
	} else {
		m := f.beforeMask.Read(inner)
		img = image.NewRGBA(inner)
		for i, v := range m.Pix {
			img.Pix[4*i], img.Pix[4*i+1], img.Pix[4*i+2], img.Pix[4*i+3] = v, v, v, 255
		}
	}
	if inner == r || inner.Empty() {
		return img
	}
	out := image.NewRGBA(r)
	for y := r.Min.Y; y < r.Max.Y; y++ {
		sy := min(max(y, inner.Min.Y), inner.Max.Y-1)
		for x := r.Min.X; x < r.Max.X; x++ {
			sx := min(max(x, inner.Min.X), inner.Max.X-1)
			copy(out.Pix[out.PixOffset(x, y):][:4], img.Pix[img.PixOffset(sx, sy):][:4])
		}
	}
	return out
}

// Compute works fn out on the pixels as they were, without showing it; it only reads, so it may run on another
// goroutine while the picture is drawn.
func (f *FilterSession) Compute(fn Filter) Result {
	pieces := f.d.workArea(f.own(), fn.reach)
	out := make(Result, 0, len(pieces))
	for _, r := range pieces {
		out = append(out, fn.apply(f.source(r.Inset(-fn.reach)), r))
	}
	return out
}

// Preview shows fn's result; partly selected pixels take it partly. It returns the area that changed.
func (f *FilterSession) Preview(fn Filter) image.Rectangle { return f.Show(f.Compute(fn)) }

// Show shows a result from Compute, or with nil the pixels as they were; it returns the area that changed.
func (f *FilterSession) Show(res Result) image.Rectangle {
	changed := spanOf(f.shown)
	for _, r := range f.shown {
		f.putBack(r)
	}
	f.shown = f.shown[:0]
	for _, out := range res {
		f.showPiece(out)
		f.shown = append(f.shown, out.Rect)
		changed = changed.Union(out.Rect)
	}
	return changed
}

// showPiece lays one piece of a result over the pixels as they were, as selected.
func (f *FilterSession) showPiece(out *image.RGBA) {
	r := out.Rect
	before := f.source(r)
	sel := f.d.selectionIn(r)
	if f.toMask {
		f.l.Mask.Tiles(r, func(pix []uint8, part image.Rectangle, corner image.Point) {
			for y := part.Min.Y; y < part.Max.Y; y++ {
				for x := part.Min.X; x < part.Max.X; x++ {
					k := coverage(sel, x, y)
					v := uint32(luma8(out.Pix[out.PixOffset(x, y):][:4]))
					pix[(y-corner.Y)*TileSize+x-corner.X] = uint8(mul8(v, k) + mul8(uint32(before.Pix[before.PixOffset(x, y)]), 255-k))
				}
			}
		})
		return
	}
	f.l.Pixels.Tiles(r, func(pix []uint8, part image.Rectangle, corner image.Point) {
		for y := part.Min.Y; y < part.Max.Y; y++ {
			for x := part.Min.X; x < part.Max.X; x++ {
				k := coverage(sel, x, y)
				i, j := before.PixOffset(x, y), out.PixOffset(x, y)
				p := pix[((y-corner.Y)*TileSize+x-corner.X)*4:][:4]
				for c := range 4 {
					p[c] = uint8(mul8(uint32(out.Pix[j+c]), k) + mul8(uint32(before.Pix[i+c]), 255-k))
				}
				if f.l.LockAlpha {
					keepAlpha(p, uint32(before.Pix[i+3]))
				}
			}
		}
	})
}

// putBack writes the pixels as they were over r.
func (f *FilterSession) putBack(r image.Rectangle) {
	if f.toMask {
		f.l.Mask.Write(f.beforeMask.Read(r))
		return
	}
	f.l.Pixels.Write(f.beforePix.Read(r))
}

// Apply records fn as one step called name.
func (f *FilterSession) Apply(name string, fn Filter) image.Rectangle {
	changed := f.Preview(fn)
	f.d.Commit(name, f.mark)
	return changed
}

// Cancel puts the pixels back as they were.
func (f *FilterSession) Cancel() image.Rectangle {
	changed := spanOf(f.shown)
	f.d.restore(f.mark.s)
	return changed
}

// Run applies fn to the chosen layer at once, for adjustments without settings such as Invert.
func (d *Doc) Run(name string, fn Filter) error {
	f, err := d.BeginFilter()
	if err != nil {
		return err
	}
	f.Apply(name, fn)
	return nil
}

func luma8(p []byte) uint8 {
	return uint8((299*uint32(p[0]) + 587*uint32(p[1]) + 114*uint32(p[2]) + 500) / 1000)
}

// ---- Adjustments: colour changes pixel by pixel, on unpremultiplied colour, keeping transparency.

// perPixel makes a Filter from a change of one unpremultiplied colour.
func perPixel(fn func(c [3]uint8) [3]uint8) Filter {
	return Filter{apply: func(src *image.RGBA, r image.Rectangle) *image.RGBA {
		out := image.NewRGBA(r)
		for y := r.Min.Y; y < r.Max.Y; y++ {
			for x := r.Min.X; x < r.Max.X; x++ {
				i, j := src.PixOffset(x, y), out.PixOffset(x, y)
				a := uint32(src.Pix[i+3])
				if a == 0 {
					continue
				}
				var c [3]uint8
				for k := range 3 {
					c[k] = uint8(min(uint32(src.Pix[i+k])*255/a, 255))
				}
				c = fn(c)
				for k := range 3 {
					out.Pix[j+k] = uint8(mul8(uint32(c[k]), a))
				}
				out.Pix[j+3] = uint8(a)
			}
		}
		return out
	}}
}

// lut is a per-channel table of new values.
type lut [3][256]uint8

func (t *lut) filter() Filter {
	return perPixel(func(c [3]uint8) [3]uint8 { return [3]uint8{t[0][c[0]], t[1][c[1]], t[2][c[2]]} })
}

// sameLUT is a table that does the same to each channel.
func sameLUT(fn func(v float64) float64) Filter {
	var t lut
	for i := range 256 {
		v := uint8(math.Round(min(max(fn(float64(i)/255), 0), 1) * 255))
		t[0][i], t[1][i], t[2][i] = v, v, v
	}
	return t.filter()
}

// Invert turns every colour into its opposite, as Image › Adjustments › Invert.
func Invert() Filter { return sameLUT(func(v float64) float64 { return 1 - v }) }

// BrightnessContrast changes brightness and contrast, each -100 to 100, in the manner of Photoshop's (not legacy)
// Brightness/Contrast: brightness bends the tones while black and white stay, contrast stretches them about the
// middle.
func BrightnessContrast(brightness, contrast float64) Filter {
	b := brightness / 100 * 0.6 // Photoshop's full brightness is about the third of a stop
	c := contrast / 100
	gain := math.Tan((min(c, 0.99) + 1) * math.Pi / 4)
	return sameLUT(func(v float64) float64 {
		// Brightness lifts or lowers through a curve that keeps black and white where they are.
		if b > 0 {
			v = 1 - math.Pow(1-v, 1+b*2)
		} else if b < 0 {
			v = math.Pow(v, 1-b*2)
		}
		return (v-0.5)*gain + 0.5
	})
}

// Levels maps input black to white onto output black to white, with gamma bending the middle, as
// Image › Adjustments › Levels does; levels are 0 to 255 and gamma 0.1 to 9.99, 1 leaving the middle.
func Levels(inBlack, inWhite, gamma, outBlack, outWhite float64) Filter {
	inWhite = max(inWhite, inBlack+1)
	gamma = min(max(gamma, 0.1), 9.99)
	return sameLUT(func(v float64) float64 {
		t := min(max((v*255-inBlack)/(inWhite-inBlack), 0), 1)
		t = math.Pow(t, 1/gamma)
		return (outBlack + t*(outWhite-outBlack)) / 255
	})
}

// CurvePoint is a point of a curve: an input level and the output it gets, 0 to 255.
type CurvePoint struct{ In, Out float64 }

// Curve is a smooth curve through the points, as Photoshop's Curves draws it: a monotone cubic, so it never
// overshoots between points. It is flat beyond the first and last points.
func Curve(points []CurvePoint) func(v float64) float64 {
	p := append([]CurvePoint(nil), points...)
	sort.Slice(p, func(i, j int) bool { return p[i].In < p[j].In })
	if len(p) == 0 {
		return func(v float64) float64 { return v }
	}
	if len(p) == 1 {
		out := p[0].Out / 255
		return func(float64) float64 { return out }
	}
	n := len(p)
	// Fritsch–Carlson tangents.
	d := make([]float64, n-1)
	for i := range n - 1 {
		d[i] = (p[i+1].Out - p[i].Out) / max(p[i+1].In-p[i].In, 1e-9)
	}
	m := make([]float64, n)
	m[0], m[n-1] = d[0], d[n-2]
	for i := 1; i < n-1; i++ {
		if d[i-1]*d[i] <= 0 {
			m[i] = 0
		} else {
			m[i] = 2 / (1/d[i-1] + 1/d[i]) // the harmonic mean keeps it monotone
		}
	}
	return func(v float64) float64 {
		x := v * 255
		switch {
		case x <= p[0].In:
			return p[0].Out / 255
		case x >= p[n-1].In:
			return p[n-1].Out / 255
		}
		i := sort.Search(n, func(i int) bool { return p[i].In >= x }) - 1
		h := max(p[i+1].In-p[i].In, 1e-9)
		t := (x - p[i].In) / h
		t2, t3 := t*t, t*t*t
		y := (2*t3-3*t2+1)*p[i].Out + (t3-2*t2+t)*h*m[i] + (-2*t3+3*t2)*p[i+1].Out + (t3-t2)*h*m[i+1]
		return y / 255
	}
}

// Curves maps every channel through a curve.
func Curves(points []CurvePoint) Filter { return sameLUT(Curve(points)) }

// Threshold turns pixels white at level and above, black below, by brightness.
func Threshold(level float64) Filter {
	return perPixel(func(c [3]uint8) [3]uint8 {
		if float64(luma8([]byte{c[0], c[1], c[2], 255})) >= level {
			return [3]uint8{255, 255, 255}
		}
		return [3]uint8{}
	})
}

// Posterize keeps levels (2 to 255) of each channel.
func Posterize(levels int) Filter {
	n := float64(min(max(levels, 2), 255) - 1)
	return sameLUT(func(v float64) float64 { return math.Round(v*n) / n })
}

// Desaturate takes the colour away, keeping brightness, as Image › Adjustments › Desaturate.
func Desaturate() Filter { return HueSaturation(0, -100, 0, false) }

// HueSaturation turns hue by degrees (-180 to 180), and changes saturation and lightness (-100 to 100), as
// Photoshop's Hue/Saturation does on the master channel; colorize paints every pixel with the hue instead.
func HueSaturation(hue, saturation, lightness float64, colorize bool) Filter {
	return perPixel(func(c [3]uint8) [3]uint8 {
		r, g, b := float64(c[0])/255, float64(c[1])/255, float64(c[2])/255
		h, s, l := toHSL(r, g, b)
		if colorize {
			h, s = math.Mod(hue+360, 360)/360, max(saturation, 0)/100
			if saturation == 0 {
				s = 0.25 // Photoshop's colorize starts at 25%
			}
		} else {
			h = math.Mod(h+hue/360+1, 1)
			if saturation < 0 {
				s *= 1 + saturation/100
			} else {
				s += (1 - s) * saturation / 100 * s // more saturation where there is some, as Photoshop does
			}
		}
		if lightness < 0 {
			l *= 1 + lightness/100
		} else {
			l += (1 - l) * lightness / 100
		}
		r, g, b = fromHSL(h, min(max(s, 0), 1), min(max(l, 0), 1))
		return [3]uint8{uint8(math.Round(r * 255)), uint8(math.Round(g * 255)), uint8(math.Round(b * 255))}
	})
}

func toHSL(r, g, b float64) (h, s, l float64) {
	mx, mn := max(r, g, b), min(r, g, b)
	l = (mx + mn) / 2
	if mx == mn {
		return 0, 0, l
	}
	d := mx - mn
	if l > 0.5 {
		s = d / (2 - mx - mn)
	} else {
		s = d / (mx + mn)
	}
	switch mx {
	case r:
		h = (g - b) / d
		if g < b {
			h += 6
		}
	case g:
		h = (b-r)/d + 2
	default:
		h = (r-g)/d + 4
	}
	return h / 6, s, l
}

func fromHSL(h, s, l float64) (r, g, b float64) {
	if s == 0 {
		return l, l, l
	}
	q := l * (1 + s)
	if l >= 0.5 {
		q = l + s - l*s
	}
	p := 2*l - q
	hue := func(t float64) float64 {
		t = math.Mod(t+1, 1)
		switch {
		case t < 1.0/6:
			return p + (q-p)*6*t
		case t < 0.5:
			return q
		case t < 2.0/3:
			return p + (q-p)*(2.0/3-t)*6
		}
		return p
	}
	return hue(h + 1.0/3), hue(h), hue(h - 1.0/3)
}

// ---- Filters, which read pixels around each one.

// blurRGBA blurs r of src (and reads up to the blur's reach around it): three box blurs each way, close to a
// Gaussian of the radius, on premultiplied channels so transparent edges do not darken.
func blurRGBA(src *image.RGBA, r image.Rectangle, radius float64) *image.RGBA {
	area := r.Inset(-blurReach(radius)).Intersect(src.Rect)
	w, h := area.Dx(), area.Dy()
	buf := make([]float32, w*h)
	tmp := make([]float32, max(w, h))
	box := max(int(math.Round(radius/math.Sqrt(3))), 1)
	out := image.NewRGBA(r)
	for c := range 4 {
		for y := range h {
			row := src.Pix[src.PixOffset(area.Min.X, area.Min.Y+y):]
			for x := range w {
				buf[y*w+x] = float32(row[4*x+c])
			}
		}
		for range 3 {
			for y := range h {
				boxLine(buf[y*w:(y+1)*w], 1, w, box, tmp)
			}
			for x := range w {
				boxLine(buf[x:], w, h, box, tmp)
			}
		}
		for y := r.Min.Y; y < r.Max.Y; y++ {
			for x := r.Min.X; x < r.Max.X; x++ {
				out.Pix[out.PixOffset(x, y)+c] = uint8(min(max(buf[(y-area.Min.Y)*w+x-area.Min.X]+0.5, 0), 255))
			}
		}
	}
	return out
}

// GaussianBlur softens by radius pixels, as Filter › Blur › Gaussian Blur.
func GaussianBlur(radius float64) Filter {
	return Filter{reach: blurReach(radius), apply: func(src *image.RGBA, r image.Rectangle) *image.RGBA {
		if radius < 0.1 {
			return copyArea(src, r)
		}
		return blurRGBA(src, r, radius)
	}}
}

// blurReach is how far blurRGBA reads around a pixel.
func blurReach(radius float64) int { return int(math.Ceil(radius*3)) + 1 }

func copyArea(src *image.RGBA, r image.Rectangle) *image.RGBA {
	out := image.NewRGBA(r)
	for y := r.Min.Y; y < r.Max.Y; y++ {
		copy(out.Pix[out.PixOffset(r.Min.X, y):out.PixOffset(r.Max.X, y)], src.Pix[src.PixOffset(r.Min.X, y):])
	}
	return out
}

// UnsharpMask sharpens edges as Filter › Sharpen › Unsharp Mask: amount in percent, radius in pixels, and
// differences under threshold levels left alone.
func UnsharpMask(amount, radius, threshold float64) Filter {
	return Filter{reach: blurReach(max(radius, 0.1)), apply: func(src *image.RGBA, r image.Rectangle) *image.RGBA {
		blur := blurRGBA(src, r, max(radius, 0.1))
		out := image.NewRGBA(r)
		k := amount / 100
		for y := r.Min.Y; y < r.Max.Y; y++ {
			for x := r.Min.X; x < r.Max.X; x++ {
				i, j := src.PixOffset(x, y), out.PixOffset(x, y)
				a := src.Pix[i+3]
				out.Pix[j+3] = a
				for c := range 3 {
					v, b := float64(src.Pix[i+c]), float64(blur.Pix[j+c])
					if math.Abs(v-b) < threshold {
						out.Pix[j+c] = src.Pix[i+c]
						continue
					}
					out.Pix[j+c] = uint8(min(max(math.Round(v+(v-b)*k), 0), float64(a)))
				}
			}
		}
		return out
	}}
}

// AddNoise adds grain of amount percent, the same in every channel when monochromatic, as Filter › Noise ›
// Add Noise with a Gaussian spread. The same seed gives the same grain, so a preview and the result match.
func AddNoise(amount float64, monochromatic bool, seed uint64) Filter {
	return Filter{apply: func(src *image.RGBA, r image.Rectangle) *image.RGBA {
		rng := rand.New(rand.NewPCG(seed, uint64(r.Min.X)<<32|uint64(uint32(r.Min.Y))))
		sigma := amount / 100 * 255 / 2
		out := image.NewRGBA(r)
		for y := r.Min.Y; y < r.Max.Y; y++ {
			for x := r.Min.X; x < r.Max.X; x++ {
				i, j := src.PixOffset(x, y), out.PixOffset(x, y)
				a := src.Pix[i+3]
				out.Pix[j+3] = a
				n := rng.NormFloat64() * sigma
				for c := range 3 {
					if !monochromatic && c > 0 {
						n = rng.NormFloat64() * sigma
					}
					out.Pix[j+c] = uint8(min(max(math.Round(float64(src.Pix[i+c])+n*float64(a)/255), 0), float64(a)))
				}
			}
		}
		return out
	}}
}

// Mosaic averages square cells of size pixels, as Filter › Pixelate › Mosaic; cells line up with the picture's
// corner.
func Mosaic(size int) Filter {
	size = max(size, 1)
	return Filter{reach: size, apply: func(src *image.RGBA, r image.Rectangle) *image.RGBA {
		out := image.NewRGBA(r)
		floor := func(v int) int { return int(math.Floor(float64(v)/float64(size))) * size }
		for cy := floor(r.Min.Y); cy < r.Max.Y; cy += size {
			for cx := floor(r.Min.X); cx < r.Max.X; cx += size {
				cell := image.Rect(cx, cy, cx+size, cy+size).Intersect(src.Rect)
				var sum [4]uint64
				for y := cell.Min.Y; y < cell.Max.Y; y++ {
					for x := cell.Min.X; x < cell.Max.X; x++ {
						i := src.PixOffset(x, y)
						for c := range 4 {
							sum[c] += uint64(src.Pix[i+c])
						}
					}
				}
				n := uint64(max(cell.Dx()*cell.Dy(), 1))
				dst := cell.Intersect(r)
				for y := dst.Min.Y; y < dst.Max.Y; y++ {
					for x := dst.Min.X; x < dst.Max.X; x++ {
						j := out.PixOffset(x, y)
						for c := range 4 {
							out.Pix[j+c] = uint8((sum[c] + n/2) / n)
						}
					}
				}
			}
		}
		return out
	}}
}
