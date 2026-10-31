package render

import (
	"math"

	"github.com/btelolabs/btelo-forge/internal/office/pdf"
)

// colorSpace turns components into sRGB in 0..1.
type colorSpace interface {
	comps() int
	rgb(c []float64) [3]float64
	initial() []float64
	// decode is the default Decode range of a component at a bit depth.
	decode(i, bpc int) (lo, hi float64)
}

type unitRange struct{}

func (unitRange) decode(int, int) (float64, float64) { return 0, 1 }

type deviceGray struct{ unitRange }
type deviceRGB struct{ unitRange }
type deviceCMYK struct{ unitRange }

func (deviceGray) comps() int { return 1 }
func (deviceRGB) comps() int  { return 3 }
func (deviceCMYK) comps() int { return 4 }

func (deviceGray) initial() []float64 { return []float64{0} }
func (deviceRGB) initial() []float64  { return []float64{0, 0, 0} }
func (deviceCMYK) initial() []float64 { return []float64{0, 0, 0, 1} }

func clamp1(v float64) float64 {
	if v < 0 || math.IsNaN(v) {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func (deviceGray) rgb(c []float64) [3]float64 {
	g := clamp1(c[0])
	return [3]float64{g, g, g}
}

func (deviceRGB) rgb(c []float64) [3]float64 {
	return [3]float64{clamp1(c[0]), clamp1(c[1]), clamp1(c[2])}
}

func (deviceCMYK) rgb(c []float64) [3]float64 { return cmykToRGB(c[0], c[1], c[2], c[3]) }

// cmykCorners are what the sixteen full-ink mixtures look like on coated paper. Colours between are interpolated,
// which keeps black text from going pure and cyan from going neon, as press inks do.
var cmykCorners = [16][3]float64{
	{1, 1, 1}, {0.1373, 0.1216, 0.1255}, {1, 0.9490, 0}, {0.1098, 0.1020, 0},
	{0.9255, 0, 0.5490}, {0.1412, 0, 0}, {0.9294, 0.1098, 0.1412}, {0.1333, 0, 0},
	{0, 0.6784, 0.9373}, {0, 0.0588, 0.1412}, {0, 0.6510, 0.3137}, {0, 0.0745, 0},
	{0.1804, 0.1922, 0.5725}, {0, 0, 0.0078}, {0.2118, 0.2119, 0.2235}, {0, 0, 0},
}

func cmykToRGB(c, m, y, k float64) [3]float64 {
	c, m, y, k = clamp1(c), clamp1(m), clamp1(y), clamp1(k)
	var out [3]float64
	for i := range 16 {
		w := 1.0
		if i&8 != 0 {
			w *= c
		} else {
			w *= 1 - c
		}
		if i&4 != 0 {
			w *= m
		} else {
			w *= 1 - m
		}
		if i&2 != 0 {
			w *= y
		} else {
			w *= 1 - y
		}
		if i&1 != 0 {
			w *= k
		} else {
			w *= 1 - k
		}
		if w > 0 {
			corner := &cmykCorners[i]
			out[0] += w * corner[0]
			out[1] += w * corner[1]
			out[2] += w * corner[2]
		}
	}
	return out
}

func srgbGamma(v float64) float64 {
	v = clamp1(v)
	if v <= 0.0031308 {
		return 12.92 * v
	}
	return 1.055*math.Pow(v, 1/2.4) - 0.055
}

// xyzToRGB converts D65-ish XYZ to sRGB.
func xyzToRGB(x, y, z float64) [3]float64 {
	return [3]float64{
		srgbGamma(3.2406*x - 1.5372*y - 0.4986*z),
		srgbGamma(-0.9689*x + 1.8758*y + 0.0415*z),
		srgbGamma(0.0557*x - 0.2040*y + 1.0570*z),
	}
}

type calGray struct {
	unitRange
	gamma float64
}

func (calGray) comps() int         { return 1 }
func (calGray) initial() []float64 { return []float64{0} }
func (s calGray) rgb(c []float64) [3]float64 {
	v := srgbGamma(math.Pow(clamp1(c[0]), s.gamma))
	return [3]float64{v, v, v}
}

type calRGB struct {
	unitRange
	gamma  [3]float64
	matrix [9]float64
	white  [3]float64
}

func (calRGB) comps() int         { return 3 }
func (calRGB) initial() []float64 { return []float64{0, 0, 0} }
func (s calRGB) rgb(c []float64) [3]float64 {
	a, b, d := math.Pow(clamp1(c[0]), s.gamma[0]), math.Pow(clamp1(c[1]), s.gamma[1]), math.Pow(clamp1(c[2]), s.gamma[2])
	m := &s.matrix
	x := m[0]*a + m[3]*b + m[6]*d
	y := m[1]*a + m[4]*b + m[7]*d
	z := m[2]*a + m[5]*b + m[8]*d
	// Scale the white point to D65, so a white stays white.
	return xyzToRGB(x/s.white[0]*0.9505, y/s.white[1], z/s.white[2]*1.0890)
}

type labSpace struct {
	white [3]float64
	rng   [4]float64
}

func (labSpace) comps() int         { return 3 }
func (labSpace) initial() []float64 { return []float64{0, 0, 0} }
func (s labSpace) decode(i, _ int) (float64, float64) {
	switch i {
	case 0:
		return 0, 100
	case 1:
		return s.rng[0], s.rng[1]
	}
	return s.rng[2], s.rng[3]
}
func (s labSpace) rgb(c []float64) [3]float64 {
	l := (c[0] + 16) / 116
	g := func(v float64) float64 {
		if v >= 6.0/29 {
			return v * v * v
		}
		return 108.0 / 841 * (v - 4.0/29)
	}
	// The white of the space maps to the white of sRGB.
	x := 0.9505 * g(l+c[1]/500)
	y := g(l)
	z := 1.0890 * g(l-c[2]/200)
	return xyzToRGB(x, y, z)
}

type indexed struct {
	base  colorSpace
	high  int
	table []byte
}

func (s *indexed) comps() int         { return 1 }
func (s *indexed) initial() []float64 { return []float64{0} }
func (s *indexed) decode(_, bpc int) (float64, float64) {
	return 0, float64(int(1)<<min(bpc, 16) - 1)
}
func (s *indexed) rgb(c []float64) [3]float64 {
	n := s.base.comps()
	i := min(max(int(c[0]+0.5), 0), s.high)
	buf := make([]float64, n)
	for j := range n {
		v := 0.0
		if p := i*n + j; p < len(s.table) {
			v = float64(s.table[p]) / 255
		}
		lo, hi := s.base.decode(j, 8)
		buf[j] = lo + v*(hi-lo)
	}
	return s.base.rgb(buf)
}

// tint is Separation and DeviceN: components through a tint transform into another space.
type tint struct {
	unitRange
	n    int
	alt  colorSpace
	fn   function
	none bool // all colorants /None: it marks nothing
}

func (s *tint) comps() int { return s.n }
func (s *tint) initial() []float64 {
	out := make([]float64, s.n)
	for i := range out {
		out[i] = 1
	}
	return out
}
func (s *tint) rgb(c []float64) [3]float64 {
	if s.fn == nil {
		g := 1 - clamp1(c[0])
		return [3]float64{g, g, g}
	}
	out := s.fn.eval(c)
	want := s.alt.comps()
	for len(out) < want {
		out = append(out, 0)
	}
	return s.alt.rgb(out[:want])
}

type patternSpace struct {
	unitRange
	under colorSpace
}

func (s patternSpace) comps() int {
	if s.under != nil {
		return s.under.comps()
	}
	return 0
}
func (s patternSpace) initial() []float64 { return nil }
func (s patternSpace) rgb(c []float64) [3]float64 {
	if s.under != nil && len(c) >= s.under.comps() {
		return s.under.rgb(c)
	}
	return [3]float64{}
}

// parseColorSpace reads a colour space object; res is where names are looked up.
func (r *ctx) parseColorSpace(o pdf.Object, res pdf.Dict, depth int) colorSpace {
	d := r.doc
	if depth > 8 {
		return deviceGray{}
	}
	if ref, ok := o.(pdf.Ref); ok {
		if cs, ok := r.cache.space(ref.Num); ok {
			return cs
		}
		cs := r.parseColorSpace(d.Resolve(o), res, depth+1)
		r.cache.setSpace(ref.Num, cs)
		return cs
	}
	switch v := o.(type) {
	case pdf.Name:
		switch v {
		case "DeviceGray", "G", "CalGray":
			if def := d.Dict(res["ColorSpace"])["DefaultGray"]; def != nil && v != "CalGray" {
				return r.parseColorSpace(def, nil, depth+1)
			}
			return deviceGray{}
		case "DeviceRGB", "RGB", "CalRGB":
			if def := d.Dict(res["ColorSpace"])["DefaultRGB"]; def != nil && v != "CalRGB" {
				return r.parseColorSpace(def, nil, depth+1)
			}
			return deviceRGB{}
		case "DeviceCMYK", "CMYK":
			if def := d.Dict(res["ColorSpace"])["DefaultCMYK"]; def != nil {
				return r.parseColorSpace(def, nil, depth+1)
			}
			return deviceCMYK{}
		case "Pattern":
			return patternSpace{}
		}
		if res != nil {
			if cs, ok := d.Dict(res["ColorSpace"])[v]; ok {
				return r.parseColorSpace(cs, res, depth+1)
			}
		}
		return deviceGray{}
	case pdf.Array:
		if len(v) == 0 {
			return deviceGray{}
		}
		switch d.Name(v[0]) {
		case "DeviceGray", "G", "DeviceRGB", "RGB", "DeviceCMYK", "CMYK":
			return r.parseColorSpace(d.Name(v[0]), res, depth+1)
		case "CalGray":
			if len(v) > 1 {
				return calGray{gamma: d.Float(d.Dict(v[1])["Gamma"], 1)}
			}
			return deviceGray{}
		case "CalRGB":
			if len(v) > 1 {
				return r.calRGB(d.Dict(v[1]))
			}
			return deviceRGB{}
		case "Lab":
			if len(v) > 1 {
				s := labSpace{rng: [4]float64{-100, 100, -100, 100}}
				if a := d.Floats(d.Dict(v[1])["Range"]); len(a) >= 4 {
					copy(s.rng[:], a)
				}
				return s
			}
			return deviceRGB{}
		case "ICCBased":
			if len(v) < 2 {
				return deviceGray{}
			}
			dict := d.Dict(v[1])
			n := d.Int(dict["N"], 0)
			if alt := dict["Alternate"]; alt != nil {
				if cs := r.parseColorSpace(alt, res, depth+1); cs.comps() == n || n == 0 {
					if _, isLab := cs.(labSpace); !isLab {
						return cs
					}
				}
			}
			switch n {
			case 1:
				return deviceGray{}
			case 4:
				return deviceCMYK{}
			}
			return deviceRGB{}
		case "Indexed", "I":
			if len(v) < 4 {
				return deviceGray{}
			}
			s := &indexed{base: r.parseColorSpace(v[1], res, depth+1), high: min(max(d.Int(v[2], 255), 0), 255)}
			switch t := d.Resolve(v[3]).(type) {
			case pdf.String:
				s.table = []byte(t)
			case *pdf.Stream:
				s.table, _ = d.Decode(t)
			}
			return s
		case "Separation":
			if len(v) < 4 {
				return deviceGray{}
			}
			return &tint{n: 1, alt: r.parseColorSpace(v[2], res, depth+1), fn: parseFunction(d, v[3], 0), none: d.Name(v[1]) == "None"}
		case "DeviceN":
			if len(v) < 4 {
				return deviceGray{}
			}
			names := d.Array(v[1])
			none := len(names) > 0
			for _, n := range names {
				none = none && d.Name(n) == "None"
			}
			return &tint{n: min(max(len(names), 1), 32), alt: r.parseColorSpace(v[2], res, depth+1), fn: parseFunction(d, v[3], 0), none: none}
		case "Pattern":
			if len(v) > 1 {
				return patternSpace{under: r.parseColorSpace(v[1], res, depth+1)}
			}
			return patternSpace{}
		}
		return r.parseColorSpace(v[0], res, depth+1)
	}
	return deviceGray{}
}

func (r *ctx) calRGB(dict pdf.Dict) colorSpace {
	d := r.doc
	s := calRGB{gamma: [3]float64{1, 1, 1}, matrix: [9]float64{1, 0, 0, 0, 1, 0, 0, 0, 1}, white: [3]float64{0.9505, 1, 1.089}}
	if g := d.Floats(dict["Gamma"]); len(g) >= 3 {
		copy(s.gamma[:], g)
	}
	if m := d.Floats(dict["Matrix"]); len(m) >= 9 {
		copy(s.matrix[:], m)
	}
	if w := d.Floats(dict["WhitePoint"]); len(w) >= 3 && w[0] > 0 && w[1] > 0 && w[2] > 0 {
		copy(s.white[:], w)
	}
	return s
}
