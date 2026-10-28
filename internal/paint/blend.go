package paint

import (
	"image"
	"math"
)

// Blend is how a layer's colours combine with what is under it, as Photoshop's blend modes do.
type Blend int

const (
	BlendNormal Blend = iota
	BlendDarken
	BlendMultiply
	BlendColorBurn
	BlendLinearBurn
	BlendDarkerColor
	BlendLighten
	BlendScreen
	BlendColorDodge
	BlendLinearDodge
	BlendLighterColor
	BlendOverlay
	BlendSoftLight
	BlendHardLight
	BlendVividLight
	BlendLinearLight
	BlendPinLight
	BlendHardMix
	BlendDifference
	BlendExclusion
	BlendSubtract
	BlendDivide
	BlendHue
	BlendSaturation
	BlendColor
	BlendLuminosity
	BlendPassThrough // groups only: their layers blend with what is under the group directly
	blendCount
)

// Blends lists the layer blend modes in Photoshop's order, with a separator before each family.
var Blends = []Blend{
	BlendNormal,
	BlendDarken, BlendMultiply, BlendColorBurn, BlendLinearBurn, BlendDarkerColor,
	BlendLighten, BlendScreen, BlendColorDodge, BlendLinearDodge, BlendLighterColor,
	BlendOverlay, BlendSoftLight, BlendHardLight, BlendVividLight, BlendLinearLight, BlendPinLight, BlendHardMix,
	BlendDifference, BlendExclusion, BlendSubtract, BlendDivide,
	BlendHue, BlendSaturation, BlendColor, BlendLuminosity,
}

// blendPixels composites src onto dst within r: src is weighted by opacity and, when mask is not nil, by the mask
// (whose pixels outside its rectangle count as maskFill). Both images are premultiplied.
func blendPixels(dst, src *image.RGBA, r image.Rectangle, opacity float64, mode Blend, mask *image.Alpha, maskFill uint8) {
	r = r.Intersect(src.Rect).Intersect(dst.Rect)
	if r.Empty() || opacity <= 0 {
		return
	}
	op := uint32(min(opacity, 1)*255 + 0.5)
	normal := mode == BlendNormal || mode == BlendPassThrough
	for y := r.Min.Y; y < r.Max.Y; y++ {
		si := src.PixOffset(r.Min.X, y)
		di := dst.PixOffset(r.Min.X, y)
		// The mask's row where it covers this one, from mx0 to mx1; its fill beyond.
		var mrow []uint8
		mx0, mx1 := 0, 0
		if mask != nil && y >= mask.Rect.Min.Y && y < mask.Rect.Max.Y {
			mx0, mx1 = max(r.Min.X, mask.Rect.Min.X), min(r.Max.X, mask.Rect.Max.X)
			if mx0 < mx1 {
				mrow = mask.Pix[mask.PixOffset(mx0, y):][:mx1-mx0]
			}
		}
		for x := r.Min.X; x < r.Max.X; x, si, di = x+1, si+4, di+4 {
			sa := uint32(src.Pix[si+3])
			if sa == 0 {
				continue
			}
			k := op
			if mask != nil {
				m := uint32(maskFill)
				if x >= mx0 && x < mx1 {
					m = uint32(mrow[x-mx0])
				}
				k = mul8(k, m)
				if k == 0 {
					continue
				}
			}
			if k == 255 && sa == 255 && normal {
				copy(dst.Pix[di:di+4], src.Pix[si:si+4]) // opaque over anything is itself
				continue
			}
			s0, s1, s2 := uint32(src.Pix[si]), uint32(src.Pix[si+1]), uint32(src.Pix[si+2])
			if k < 255 {
				s0, s1, s2, sa = mul8(s0, k), mul8(s1, k), mul8(s2, k), mul8(sa, k)
			}
			if normal || dst.Pix[di+3] == 0 {
				inv := 255 - sa
				dst.Pix[di] = uint8(s0 + mul8(uint32(dst.Pix[di]), inv))
				dst.Pix[di+1] = uint8(s1 + mul8(uint32(dst.Pix[di+1]), inv))
				dst.Pix[di+2] = uint8(s2 + mul8(uint32(dst.Pix[di+2]), inv))
				dst.Pix[di+3] = uint8(sa + mul8(uint32(dst.Pix[di+3]), inv))
				continue
			}
			blendOne(dst.Pix[di:di+4], [4]uint32{s0, s1, s2, sa}, mode)
		}
	}
}

// blendOne composites one premultiplied source pixel onto a destination pixel with a separable or non-separable
// mode, following the W3C compositing model that Photoshop's modes match: the blended colour is mixed in where both
// are opaque, and each shows through alone where the other is not.
func blendOne(d []byte, s [4]uint32, mode Blend) {
	as, ab := float64(s[3])/255, float64(d[3])/255
	var cs, cb [3]float64
	for i := range 3 {
		cs[i] = float64(s[i]) / 255 / as
		cb[i] = float64(d[i]) / 255 / ab
	}
	var bl [3]float64
	switch mode {
	case BlendDarkerColor:
		bl = cb
		if lum(cs) < lum(cb) {
			bl = cs
		}
	case BlendLighterColor:
		bl = cb
		if lum(cs) > lum(cb) {
			bl = cs
		}
	case BlendHue:
		bl = setLum(setSat(cs, sat(cb)), lum(cb))
	case BlendSaturation:
		bl = setLum(setSat(cb, sat(cs)), lum(cb))
	case BlendColor:
		bl = setLum(cs, lum(cb))
	case BlendLuminosity:
		bl = setLum(cb, lum(cs))
	default:
		for i := range 3 {
			bl[i] = separable(mode, cb[i], cs[i])
		}
	}
	ao := as + ab*(1-as)
	for i := range 3 {
		// Premultiplied result: source over backdrop, the overlap taking the blended colour.
		c := as*(1-ab)*cs[i] + as*ab*min(max(bl[i], 0), 1) + (1-as)*ab*cb[i]
		d[i] = uint8(min(max(c*255+0.5, 0), 255))
	}
	d[3] = uint8(ao*255 + 0.5)
}

// separable blends one channel of backdrop b with source s, both 0 to 1.
func separable(mode Blend, b, s float64) float64 {
	switch mode {
	case BlendDarken:
		return min(b, s)
	case BlendMultiply:
		return b * s
	case BlendColorBurn:
		switch {
		case b >= 1:
			return 1
		case s <= 0:
			return 0
		}
		return 1 - min(1, (1-b)/s)
	case BlendLinearBurn:
		return max(b+s-1, 0)
	case BlendLighten:
		return max(b, s)
	case BlendScreen:
		return b + s - b*s
	case BlendColorDodge:
		switch {
		case b <= 0:
			return 0
		case s >= 1:
			return 1
		}
		return min(1, b/(1-s))
	case BlendLinearDodge:
		return min(b+s, 1)
	case BlendOverlay:
		return separable(BlendHardLight, s, b)
	case BlendSoftLight:
		if s <= 0.5 {
			return b - (1-2*s)*b*(1-b)
		}
		var dd float64
		if b <= 0.25 {
			dd = ((16*b-12)*b + 4) * b
		} else {
			dd = math.Sqrt(b)
		}
		return b + (2*s-1)*(dd-b)
	case BlendHardLight:
		if s <= 0.5 {
			return b * 2 * s
		}
		return separable(BlendScreen, b, 2*s-1)
	case BlendVividLight:
		if s <= 0.5 {
			return separable(BlendColorBurn, b, 2*s)
		}
		return separable(BlendColorDodge, b, 2*s-1)
	case BlendLinearLight:
		return min(max(b+2*s-1, 0), 1)
	case BlendPinLight:
		if s <= 0.5 {
			return min(b, 2*s)
		}
		return max(b, 2*s-1)
	case BlendHardMix:
		if separable(BlendVividLight, b, s) < 0.5 {
			return 0
		}
		return 1
	case BlendDifference:
		return math.Abs(b - s)
	case BlendExclusion:
		return b + s - 2*b*s
	case BlendSubtract:
		return max(b-s, 0)
	case BlendDivide:
		if s <= 0 {
			if b <= 0 {
				return 0
			}
			return 1
		}
		return min(b/s, 1)
	}
	return s
}

func lum(c [3]float64) float64 { return 0.3*c[0] + 0.59*c[1] + 0.11*c[2] }

func clipColor(c [3]float64) [3]float64 {
	l := lum(c)
	n, x := min(c[0], c[1], c[2]), max(c[0], c[1], c[2])
	for i := range 3 {
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
	d := l - lum(c)
	return clipColor([3]float64{c[0] + d, c[1] + d, c[2] + d})
}

func sat(c [3]float64) float64 { return max(c[0], c[1], c[2]) - min(c[0], c[1], c[2]) }

func setSat(c [3]float64, s float64) [3]float64 {
	mx, mn := max(c[0], c[1], c[2]), min(c[0], c[1], c[2])
	var out [3]float64
	if mx > mn {
		for i := range 3 {
			out[i] = (c[i] - mn) * s / (mx - mn)
		}
	}
	return out
}
