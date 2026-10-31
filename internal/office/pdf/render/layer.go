package render

import (
	"image"
	"math"

	"github.com/btelolabs/btelo-forge/internal/office/pdf"
)

// layer is a picture being composited: premultiplied RGBA over a rectangle of device pixels.
type layer struct {
	r   image.Rectangle
	pix []uint8
}

func newLayer(r image.Rectangle) *layer {
	return &layer{r: r, pix: make([]uint8, 4*r.Dx()*r.Dy())}
}

func (l *layer) bounds() image.Rectangle { return l.r }

func (l *layer) offset(x, y int) int { return 4 * ((y-l.r.Min.Y)*l.r.Dx() + x - l.r.Min.X) }

// softMask holds a mask for the whole layer it was made for; outside its rectangle it has one value.
type softMask struct {
	r       image.Rectangle
	pix     []uint8
	outside uint8
}

func (s *softMask) at(x, y int) uint8 {
	if !(image.Point{x, y}).In(s.r) {
		return s.outside
	}
	return s.pix[(y-s.r.Min.Y)*s.r.Dx()+x-s.r.Min.X]
}

func (l *layer) toSoftMask(lum bool, bc [3]float64) *softMask {
	m := &softMask{r: l.r, pix: make([]uint8, l.r.Dx()*l.r.Dy())}
	bg := 0.3*bc[0]*255 + 0.59*bc[1]*255 + 0.11*bc[2]*255
	for i := range m.pix {
		p := l.pix[i*4 : i*4+4 : i*4+4]
		if !lum {
			m.pix[i] = p[3]
			continue
		}
		v := 0.3*float64(p[0]) + 0.59*float64(p[1]) + 0.11*float64(p[2]) + bg*(1-float64(p[3])/255)
		m.pix[i] = uint8(min(v+0.5, 255))
	}
	if lum {
		m.outside = uint8(bg + 0.5)
	}
	return m
}

type blendMode int

const (
	blendNormal blendMode = iota
	blendMultiply
	blendScreen
	blendOverlay
	blendDarken
	blendLighten
	blendColorDodge
	blendColorBurn
	blendHardLight
	blendSoftLight
	blendDifference
	blendExclusion
	blendHue
	blendSaturation
	blendColor
	blendLuminosity
)

func parseBlend(d *pdf.Doc, o pdf.Object) blendMode {
	v := d.Resolve(o)
	if a, ok := v.(pdf.Array); ok {
		for _, e := range a {
			if b, known := blendByName(d.Name(e)); known {
				return b
			}
		}
		return blendNormal
	}
	b, _ := blendByName(d.Name(v))
	return b
}

func blendByName(n pdf.Name) (blendMode, bool) {
	switch n {
	case "Normal", "Compatible":
		return blendNormal, true
	case "Multiply":
		return blendMultiply, true
	case "Screen":
		return blendScreen, true
	case "Overlay":
		return blendOverlay, true
	case "Darken":
		return blendDarken, true
	case "Lighten":
		return blendLighten, true
	case "ColorDodge":
		return blendColorDodge, true
	case "ColorBurn":
		return blendColorBurn, true
	case "HardLight":
		return blendHardLight, true
	case "SoftLight":
		return blendSoftLight, true
	case "Difference":
		return blendDifference, true
	case "Exclusion":
		return blendExclusion, true
	case "Hue":
		return blendHue, true
	case "Saturation":
		return blendSaturation, true
	case "Color":
		return blendColor, true
	case "Luminosity":
		return blendLuminosity, true
	}
	return blendNormal, false
}

func blendSep(m blendMode, b, s float64) float64 {
	switch m {
	case blendMultiply:
		return b * s
	case blendScreen:
		return b + s - b*s
	case blendOverlay:
		return blendSep(blendHardLight, s, b)
	case blendDarken:
		return min(b, s)
	case blendLighten:
		return max(b, s)
	case blendColorDodge:
		if b == 0 {
			return 0
		}
		if b >= 1-s {
			return 1
		}
		return b / (1 - s)
	case blendColorBurn:
		if b >= 1 {
			return 1
		}
		if 1-b >= s {
			return 0
		}
		return 1 - (1-b)/s
	case blendHardLight:
		if s <= 0.5 {
			return b * 2 * s
		}
		t := 2*s - 1
		return b + t - b*t
	case blendSoftLight:
		if s <= 0.5 {
			return b - (1-2*s)*b*(1-b)
		}
		d := math.Sqrt(b)
		if b <= 0.25 {
			d = ((16*b-12)*b + 4) * b
		}
		return b + (2*s-1)*(d-b)
	case blendDifference:
		return math.Abs(b - s)
	case blendExclusion:
		return b + s - 2*b*s
	}
	return s
}

func lumOf(c [3]float64) float64 { return 0.3*c[0] + 0.59*c[1] + 0.11*c[2] }

func clipColor(c [3]float64) [3]float64 {
	l := lumOf(c)
	n := min(c[0], c[1], c[2])
	x := max(c[0], c[1], c[2])
	for i := range c {
		if n < 0 {
			c[i] = l + (c[i]-l)*l/(l-n)
		}
		if x > 1 {
			c[i] = l + (c[i]-l)*(1-l)/(x-l)
		}
	}
	return c
}

func setLum(c [3]float64, l float64) [3]float64 {
	d := l - lumOf(c)
	return clipColor([3]float64{c[0] + d, c[1] + d, c[2] + d})
}

func satOf(c [3]float64) float64 { return max(c[0], c[1], c[2]) - min(c[0], c[1], c[2]) }

func setSat(c [3]float64, s float64) [3]float64 {
	x, n := max(c[0], c[1], c[2]), min(c[0], c[1], c[2])
	if x == n {
		return [3]float64{}
	}
	return [3]float64{(c[0] - n) * s / (x - n), (c[1] - n) * s / (x - n), (c[2] - n) * s / (x - n)}
}

func blendAll(m blendMode, b, s [3]float64) [3]float64 {
	switch m {
	case blendHue:
		return setLum(setSat(s, satOf(b)), lumOf(b))
	case blendSaturation:
		return setLum(setSat(b, satOf(s)), lumOf(b))
	case blendColor:
		return setLum(s, lumOf(b))
	case blendLuminosity:
		return setLum(b, lumOf(s))
	}
	return [3]float64{blendSep(m, b[0], s[0]), blendSep(m, b[1], s[1]), blendSep(m, b[2], s[2])}
}

// putBlend composites a colour with alpha a (0..255) with a blend mode at offset i.
func (l *layer) putBlend(i int, cr, cg, cb uint8, a int, m blendMode) {
	if a == 0 {
		return
	}
	p := l.pix[i : i+4 : i+4]
	as := float64(a) / 255
	ab := float64(p[3]) / 255
	s := [3]float64{float64(cr) / 255, float64(cg) / 255, float64(cb) / 255}
	var out [3]float64
	if ab > 0 {
		back := [3]float64{float64(p[0]) / 255 / ab, float64(p[1]) / 255 / ab, float64(p[2]) / 255 / ab}
		b := blendAll(m, [3]float64{min(back[0], 1), min(back[1], 1), min(back[2], 1)}, s)
		for k := range 3 {
			out[k] = (1-ab)*s[k] + ab*b[k]
		}
	} else {
		out = s
	}
	for k := range 3 {
		p[k] = uint8(math.Min(float64(p[k])*(1-as)+out[k]*as*255+0.5, 255))
	}
	p[3] = uint8(math.Min(float64(p[3])+float64(a)*(1-ab)+0.5, 255))
}

// source gives the colour of a device pixel, straight alpha.
type source interface {
	at(x, y int) (r, g, b, a uint8)
}

// paintMask composites a coverage mask with a paint.
func (r *ctx) paintMask(m *mask, p *paint, alpha float64) {
	if m == nil {
		return
	}
	if none, ok := p.cs.(*tint); ok && none.none {
		return
	}
	var src source
	if p.pattern != nil {
		src = r.patternSource(p, m.r)
		if src == nil {
			return
		}
	}
	r.composite(m, p.rgb, src, alpha)
}

// composite is the one place pixels land: the mask, the clip, the soft mask and the alpha multiply, and the colour
// blends in.
func (r *ctx) composite(m *mask, rgb [3]uint8, src source, alpha float64) {
	gs := r.gs
	l := r.layer
	box := m.r.Intersect(gs.clip.r).Intersect(l.r)
	if box.Empty() {
		return
	}
	ca := int(alpha*255 + 0.5)
	clipM := gs.clip.m
	soft := gs.soft
	blend := gs.blend
	mw := m.r.Dx()
	w := box.Dx()
	cr, cg, cb := uint32(rgb[0]), uint32(rgb[1]), uint32(rgb[2])
	var row []uint8
	for y := box.Min.Y; y < box.Max.Y; y++ {
		row = m.pix[(y-m.r.Min.Y)*mw+box.Min.X-m.r.Min.X:]
		i := l.offset(box.Min.X, y)
		for x := range w {
			c := int(row[x])
			if c == 0 {
				i += 4
				continue
			}
			px := box.Min.X + x
			if clipM != nil {
				c = c * int(clipM.at(px, y)) / 255
			}
			if soft != nil {
				c = c * int(soft.at(px, y)) / 255
			}
			c = c * ca / 255
			if src != nil {
				sr, sg, sb, sa := src.at(px, y)
				c = c * int(sa) / 255
				cr, cg, cb = uint32(sr), uint32(sg), uint32(sb)
			}
			if c == 0 {
				i += 4
				continue
			}
			if blend != blendNormal {
				l.putBlend(i, uint8(cr), uint8(cg), uint8(cb), c, blend)
				i += 4
				continue
			}
			p := l.pix[i : i+4 : i+4]
			if c >= 255 {
				p[0], p[1], p[2], p[3] = uint8(cr), uint8(cg), uint8(cb), 255
			} else {
				a := uint32(c)
				inv := 255 - a
				p[0] = uint8((cr*a + uint32(p[0])*inv + 127) / 255)
				p[1] = uint8((cg*a + uint32(p[1])*inv + 127) / 255)
				p[2] = uint8((cb*a + uint32(p[2])*inv + 127) / 255)
				p[3] = uint8((a*255 + uint32(p[3])*inv + 127) / 255)
			}
			i += 4
		}
	}
}

// compositeLayer draws a group's layer into this one as one object.
func (l *layer) compositeLayer(g *layer, clip *clipState, soft *softMask, alpha float64, blend blendMode) {
	box := g.r.Intersect(l.r).Intersect(clip.r)
	ca := int(alpha*255 + 0.5)
	for y := box.Min.Y; y < box.Max.Y; y++ {
		si := g.offset(box.Min.X, y)
		di := l.offset(box.Min.X, y)
		for x := box.Min.X; x < box.Max.X; x, si, di = x+1, si+4, di+4 {
			sp := g.pix[si : si+4 : si+4]
			sa := int(sp[3])
			if sa == 0 {
				continue
			}
			k := ca
			if clip.m != nil {
				k = k * int(clip.m.at(x, y)) / 255
			}
			if soft != nil {
				k = k * int(soft.at(x, y)) / 255
			}
			if k == 0 {
				continue
			}
			if blend != blendNormal {
				// Back to straight colour for the blend.
				cr := min(int(sp[0])*255/sa, 255)
				cg := min(int(sp[1])*255/sa, 255)
				cb := min(int(sp[2])*255/sa, 255)
				l.putBlend(di, uint8(cr), uint8(cg), uint8(cb), sa*k/255, blend)
				continue
			}
			dp := l.pix[di : di+4 : di+4]
			a := uint32(sa * k / 255)
			inv := 255 - a
			kk := uint32(k)
			dp[0] = uint8(min((uint32(sp[0])*kk+uint32(dp[0])*inv+127)/255, 255))
			dp[1] = uint8(min((uint32(sp[1])*kk+uint32(dp[1])*inv+127)/255, 255))
			dp[2] = uint8(min((uint32(sp[2])*kk+uint32(dp[2])*inv+127)/255, 255))
			dp[3] = uint8(min((a*255+uint32(dp[3])*inv+127)/255, 255))
		}
	}
}
