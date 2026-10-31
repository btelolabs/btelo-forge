package paint

import (
	"image"
	"math"
)

// The colour adjustments of Image › Adjustments beyond the plain curves: Color Balance, Vibrance, Selective Color,
// Photo Filter, Gradient Map and Exposure, which can be adjustment layers; and Shadows/Highlights and Develop, which
// read the brightness around each pixel and so, as in Photoshop, are applied to pixels.

// brightness weighs the channels as luma8 does.
func brightness(r, g, b float64) float64 { return 0.299*r + 0.587*g + 0.114*b }

func clamp01(v float64) float64 { return min(max(v, 0), 1) }

// toLinear and toGamma turn sRGB values into light and back.
func toLinear(v float64) float64 {
	if v <= 0.04045 {
		return v / 12.92
	}
	return math.Pow((v+0.055)/1.055, 2.4)
}

func toGamma(v float64) float64 {
	if v <= 0.0031308 {
		return v * 12.92
	}
	return 1.055*math.Pow(v, 1/2.4) - 0.055
}

// floatPixel makes a Filter from a change of one unpremultiplied colour in values of 0 to 1.
func floatPixel(fn func(r, g, b float64) (float64, float64, float64)) Filter {
	return perPixel(func(c [3]uint8) [3]uint8 {
		r, g, b := fn(float64(c[0])/255, float64(c[1])/255, float64(c[2])/255)
		return [3]uint8{uint8(clamp01(r)*255 + 0.5), uint8(clamp01(g)*255 + 0.5), uint8(clamp01(b)*255 + 0.5)}
	})
}

// balanceWeights say how much a level (0 to 1) is shadow, midtone and highlight; the bands overlap, so a change has
// no edge in the picture.
func balanceWeights(l float64) (shadows, midtones, highlights float64) {
	const width, centre, scale = 0.25, 1.0 / 3, 0.7
	shadows = clamp01((l-centre)/-width+0.5) * scale
	highlights = clamp01((l+centre-1)/width+0.5) * scale
	midtones = clamp01((l-centre)/width+0.5) * clamp01((l+centre-1)/-width+0.5) * scale
	return
}

// ColorBalance shifts colours by tone: v holds cyan–red, magenta–green and yellow–blue, -100 to 100, for the shadows,
// then the midtones, then the highlights. With preserve, each pixel keeps its lightness.
func ColorBalance(v [9]float64, preserve bool) Filter {
	var t lut
	for c := range 3 {
		for i := range 256 {
			l := float64(i) / 255
			s, m, h := balanceWeights(l)
			out := l + (s*v[c]+m*v[3+c]+h*v[6+c])/100
			t[c][i] = uint8(clamp01(out)*255 + 0.5)
		}
	}
	if !preserve {
		return t.filter()
	}
	return floatPixel(func(r, g, b float64) (float64, float64, float64) {
		_, _, l := toHSL(r, g, b)
		h, s, _ := toHSL(float64(t[0][uint8(r*255+0.5)])/255, float64(t[1][uint8(g*255+0.5)])/255, float64(t[2][uint8(b*255+0.5)])/255)
		return fromHSL(h, s, l)
	})
}

// saturate moves a colour away from its grey by factor, keeping brightness.
func saturate(r, g, b, factor float64) (float64, float64, float64) {
	l := brightness(r, g, b)
	return l + (r-l)*factor, l + (g-l)*factor, l + (b-l)*factor
}

// vibrate raises saturation most where there is least, and leaves skin tones more alone than other colours, so
// faces do not burn; amount is -1 to 1.
func vibrate(r, g, b, amount float64) (float64, float64, float64) {
	if amount == 0 {
		return r, g, b
	}
	mx, mn := max(r, g, b), min(r, g, b)
	s := 0.0
	if mx > 0 {
		s = (mx - mn) / mx
	}
	h, _, _ := toHSL(r, g, b)
	off := (math.Mod(h*360-25+540, 360) - 180) / 30 // from the hue of skin, in thirty degrees
	k := amount * (1 - s) * (1 - 0.5*math.Exp(-off*off))
	return saturate(r, g, b, 1+k)
}

// Vibrance changes saturation where it shows least, and saturation everywhere, each -100 to 100.
func Vibrance(vibrance, saturation float64) Filter {
	return floatPixel(func(r, g, b float64) (float64, float64, float64) {
		r, g, b = vibrate(r, g, b, vibrance/100)
		return saturate(r, g, b, 1+saturation/100)
	})
}

// The colours Selective Color works on, in the order of its values.
const (
	selectiveReds = iota
	selectiveYellows
	selectiveGreens
	selectiveCyans
	selectiveBlues
	selectiveMagentas
	selectiveWhites
	selectiveNeutrals
	selectiveBlacks
	selectiveCount
)

// selectiveWeight is how much a pixel with these channels belongs to a colour, 0 to 1: how far its two strongest
// (or weakest) channels stand apart for a primary (or secondary) colour, and how far it is from the middle for
// whites, neutrals and blacks.
func selectiveWeight(colour int, r, g, b, mx, mid, mn float64) float64 {
	switch colour {
	case selectiveReds:
		if r == mx {
			return mx - mid
		}
	case selectiveGreens:
		if g == mx {
			return mx - mid
		}
	case selectiveBlues:
		if b == mx {
			return mx - mid
		}
	case selectiveYellows:
		if b == mn {
			return mid - mn
		}
	case selectiveCyans:
		if r == mn {
			return mid - mn
		}
	case selectiveMagentas:
		if g == mn {
			return mid - mn
		}
	case selectiveWhites:
		if mn > 0.5 {
			return (mn - 0.5) * 2
		}
	case selectiveBlacks:
		if mx < 0.5 {
			return (0.5 - mx) * 2
		}
	case selectiveNeutrals:
		if mx > 0 && mn < 1 {
			return max(1-(math.Abs(mx-0.5)+math.Abs(mn-0.5)), 0)
		}
	}
	return 0
}

// selectiveShift is what one ink's setting does to a channel of value v: relative, in proportion to the room there is.
func selectiveShift(v, ink, black float64, relative bool) float64 {
	shift := (-1-ink)*black - ink
	if relative {
		shift *= 1 - v
	}
	return min(max(shift, -v), 1-v)
}

// SelectiveColor changes how much cyan, magenta, yellow and black each colour holds: v has those four, -100 to 100,
// for each of the nine colours. Relative changes in proportion to what is there, otherwise by the amount itself.
func SelectiveColor(v [selectiveCount * 4]float64, relative bool) Filter {
	var active []int
	for c := range selectiveCount {
		if v[c*4] != 0 || v[c*4+1] != 0 || v[c*4+2] != 0 || v[c*4+3] != 0 {
			active = append(active, c)
		}
	}
	return floatPixel(func(r, g, b float64) (float64, float64, float64) {
		mx, mn := max(r, g, b), min(r, g, b)
		mid := r + g + b - mx - mn
		nr, ng, nb := r, g, b
		for _, c := range active {
			w := selectiveWeight(c, r, g, b, mx, mid, mn)
			if w == 0 {
				continue
			}
			k := v[c*4+3] / 100
			nr += w * selectiveShift(r, v[c*4]/100, k, relative)
			ng += w * selectiveShift(g, v[c*4+1]/100, k, relative)
			nb += w * selectiveShift(b, v[c*4+2]/100, k, relative)
		}
		return nr, ng, nb
	})
}

// PhotoFilter lays a coloured filter over the picture, density 0 to 100, colour 0 to 255 a channel; with preserve the
// picture keeps its brightness.
func PhotoFilter(density float64, red, green, blue float64, preserve bool) Filter {
	d := density / 100
	fr, fg, fb := red/255, green/255, blue/255
	return floatPixel(func(r, g, b float64) (float64, float64, float64) {
		nr, ng, nb := r+(r*fr-r)*d, g+(g*fg-g)*d, b+(b*fb-b)*d
		if l, nl := brightness(r, g, b), brightness(nr, ng, nb); preserve && nl > 0 {
			nr, ng, nb = nr*l/nl, ng*l/nl, nb*l/nl
		}
		return nr, ng, nb
	})
}

// GradientMap paints each pixel by its brightness on the way from one colour to another; midpoint, 1 to 99, is the
// brightness halfway between them.
func GradientMap(midpoint float64, from, to [3]float64) Filter {
	bend := math.Log(0.5) / math.Log(min(max(midpoint, 1), 99)/100)
	return floatPixel(func(r, g, b float64) (float64, float64, float64) {
		t := math.Pow(brightness(r, g, b), bend)
		return (from[0] + (to[0]-from[0])*t) / 255, (from[1] + (to[1]-from[1])*t) / 255, (from[2] + (to[2]-from[2])*t) / 255
	})
}

// Exposure changes the light itself, in stops, then lifts or lowers it by offset (-0.5 to 0.5) and bends the result
// by gamma (0.01 to 9.99, 1 leaving it), as Image › Adjustments › Exposure.
func Exposure(stops, offset, gamma float64) Filter {
	gain := math.Pow(2, stops)
	bend := 1 / min(max(gamma, 0.01), 9.99)
	return sameLUT(func(v float64) float64 {
		return math.Pow(clamp01(toGamma(clamp01(toLinear(v)*gain+offset))), bend)
	})
}

// surroundingsEdge is how much variation, as a variance of brightness, counts as an edge to surroundings: an edge
// stronger than this stays, detail weaker is smoothed away.
const surroundingsEdge = 0.003

// surroundingsReach is how far surroundings reads around a pixel.
func surroundingsReach(radius float64) int { return 2*int(math.Ceil(radius)) + 1 }

// surroundings is the brightness each pixel of r stands in, 0 to 1, judged over radius: a guided filter of the picture
// by itself (He, Sun and Tang), which smooths detail like a blur but stops at strong edges. Lifting the tones by a
// blur instead would leave a halo along every edge, each side reading the other's brightness.
func surroundings(src *image.RGBA, r image.Rectangle, radius float64) []float64 {
	area := r.Inset(-surroundingsReach(radius)).Intersect(src.Rect)
	w, h := area.Dx(), area.Dy()
	box := int(math.Ceil(radius))
	tmp := make([]float32, max(w, h))
	boxed := func(v []float32) []float32 {
		for y := range h {
			boxLine(v[y*w:(y+1)*w], 1, w, box, tmp)
		}
		for x := range w {
			boxLine(v[x:], w, h, box, tmp)
		}
		return v
	}
	light := make([]float32, w*h)
	square := make([]float32, w*h)
	for y := range h {
		row := src.Pix[src.PixOffset(area.Min.X, area.Min.Y+y):]
		for x := range w {
			p := row[4*x:][:4]
			if p[3] != 0 {
				l := float32(brightness(float64(p[0]), float64(p[1]), float64(p[2])) / float64(p[3]))
				light[y*w+x], square[y*w+x] = l, l*l
			}
		}
	}
	mean := boxed(append([]float32(nil), light...))
	boxed(square)
	gain := make([]float32, w*h)
	base := make([]float32, w*h)
	for i, m := range mean {
		variance := max(square[i]-m*m, 0)
		g := variance / (variance + surroundingsEdge)
		gain[i], base[i] = g, (1-g)*m
	}
	boxed(gain)
	boxed(base)
	out := make([]float64, r.Dx()*r.Dy())
	n := 0
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			i := (y-area.Min.Y)*w + x - area.Min.X
			out[n] = clamp01(float64(gain[i]*light[i] + base[i]))
			n++
		}
	}
	return out
}

// lifted opens the darks of a value (amount 0 to 1) or, negative, closes them; black stays black and white white.
func lifted(v, amount float64) float64 { return math.Pow(v, math.Pow(2, -amount)) }

// liftTones lifts shadows and pulls down highlights, each -1 to 1, by how dark or light the surroundings are, around
// being their brightness. The curve is bent for the surroundings alone and each pixel is scaled by what that did to
// them: a pixel keeps its difference from its neighbours and its hue, which bending every pixel's own value would
// flatten and shift.
func liftTones(r, g, b, around, shadows, highlights float64) (float64, float64, float64) {
	if around < 1.0/255 {
		return r, g, b
	}
	dark, light := (1-around)*(1-around), around*around
	bent := 1 - lifted(1-lifted(around, shadows*dark), highlights*light)
	k := min(bent/around, 4)
	return r * k, g * k, b * k
}

// localFilter makes a Filter from a change of one unpremultiplied colour with the brightness of its surroundings;
// with no radius around is 0.
func localFilter(radius float64, fn func(r, g, b, around float64) (float64, float64, float64)) Filter {
	reach := 0
	if radius > 0 {
		reach = surroundingsReach(radius)
	}
	return Filter{reach: reach, apply: func(src *image.RGBA, r image.Rectangle) *image.RGBA {
		var around []float64
		if radius > 0 {
			around = surroundings(src, r, radius)
		}
		out := image.NewRGBA(r)
		n := 0
		for y := r.Min.Y; y < r.Max.Y; y++ {
			for x := r.Min.X; x < r.Max.X; x++ {
				k := n
				n++
				i, j := src.PixOffset(x, y), out.PixOffset(x, y)
				a := float64(src.Pix[i+3])
				if a == 0 {
					continue
				}
				near := 0.0
				if around != nil {
					near = around[k]
				}
				nr, ng, nb := fn(float64(src.Pix[i])/a, float64(src.Pix[i+1])/a, float64(src.Pix[i+2])/a, near)
				out.Pix[j] = uint8(clamp01(nr)*a + 0.5)
				out.Pix[j+1] = uint8(clamp01(ng)*a + 0.5)
				out.Pix[j+2] = uint8(clamp01(nb)*a + 0.5)
				out.Pix[j+3] = src.Pix[i+3]
			}
		}
		return out
	}}
}

// ShadowsHighlights lightens dark areas and darkens light ones, each 0 to 100, judging areas over radius pixels.
func ShadowsHighlights(shadows, highlights, radius float64) Filter {
	radius = max(radius, 1)
	f := localFilter(radius, func(r, g, b, around float64) (float64, float64, float64) {
		return liftTones(r, g, b, around, shadows/100, highlights/100)
	})
	job := ToneJob{Radius: radius, Shadows: shadows / 100, Highlights: highlights / 100}
	f.card = func(a Accelerator, src *image.RGBA, r image.Rectangle) (*image.RGBA, error) {
		return a.Tones(src, r, job)
	}
	return f
}

// Develop is one panel's worth of the usual corrections of a photograph, in the order they are best applied.
type Develop struct {
	Temperature, Tint  float64 // -100 to 100: warmer, more magenta
	Exposure           float64 // stops, -5 to 5
	Contrast           float64 // -100 to 100
	Highlights         float64 // -100 to 100
	Shadows            float64 // -100 to 100
	Whites, Blacks     float64 // -100 to 100: where white and black lie
	Clarity            float64 // -100 to 100: contrast of the midtones' detail
	Vibrance, Saturate float64 // -100 to 100
	radius             float64 // how far it looks around a pixel, when not developRadius: at a reduced level
}

// developRadius is how far Highlights, Shadows and Clarity look around a pixel.
const developRadius = 24

// Filter is the corrections as a filter.
func (p Develop) Filter() Filter {
	gain := math.Pow(2, p.Exposure)
	warm, tint := p.Temperature/100*0.3, p.Tint/100*0.2
	rg, gg, bg := 1+warm+tint/2, 1-tint, 1-warm+tint/2
	black, white := p.Blacks/100*-0.2, 1-p.Whites/100*0.2
	span := max(white-black, 0.01)
	steep := math.Tan((min(p.Contrast/100*0.5, 0.49) + 0.5) * math.Pi / 2)
	local := p.Highlights != 0 || p.Shadows != 0 || p.Clarity != 0
	radius := 0.0
	if local {
		radius = developRadius
		if p.radius > 0 {
			radius = max(p.radius, 1)
		}
	}
	job := ToneJob{Radius: radius, Shadows: p.Shadows / 100, Highlights: p.Highlights / 100, Clarity: p.Clarity / 100, Develop: true,
		Gain: [3]float64{rg, gg, bg}, Multiply: gain, Black: black, Span: span, Steep: steep, Vibrance: p.Vibrance / 100, Saturation: 1 + p.Saturate/100}
	f := localFilter(radius, func(r, g, b, around float64) (float64, float64, float64) {
		// Highlights and Shadows come first, on tones Exposure and Contrast have not yet clipped: what is lost there
		// cannot be brought back.
		if local {
			r, g, b = liftTones(r, g, b, around, p.Shadows/100, p.Highlights/100)
			if p.Clarity != 0 {
				l := brightness(r, g, b)
				detail := (l - around) * (1 - math.Abs(2*l-1)) * p.Clarity / 100
				r, g, b = clamp01(r+detail), clamp01(g+detail), clamp01(b+detail)
			}
		}
		light := func(v, wb float64) float64 {
			v = toGamma(clamp01(toLinear(v) * wb * gain))
			v = clamp01((v - black) / span)
			return clamp01((v-0.5)*steep + 0.5)
		}
		r, g, b = light(r, rg), light(g, gg), light(b, bg)
		r, g, b = vibrate(clamp01(r), clamp01(g), clamp01(b), p.Vibrance/100)
		return saturate(r, g, b, 1+p.Saturate/100)
	})
	f.card = func(a Accelerator, src *image.RGBA, r image.Rectangle) (*image.RGBA, error) {
		return a.Tones(src, r, job)
	}
	return f
}
