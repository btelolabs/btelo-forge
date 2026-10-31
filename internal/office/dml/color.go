// Package dml holds what the Office formats share of DrawingML: colours and the theme's colour scheme.
package dml

import (
	"image/color"
	"math"
	"strconv"
	"strings"

	"golang.org/x/image/colornames"

	"github.com/btelolabs/btelo-forge/internal/office/ox"
)

// Scheme is a theme's colours by slot: dk1, lt1, dk2, lt2, accent1..6, hlink, folHlink.
type Scheme map[string]color.RGBA

// defaultColors are the Office theme's, for a theme that leaves one out.
var defaultColors = map[string]uint32{"dk1": 0x000000, "lt1": 0xffffff, "dk2": 0x44546a, "lt2": 0xe7e6e6, "accent1": 0x4472c4,
	"accent2": 0xed7d31, "accent3": 0xa5a5a5, "accent4": 0xffc000, "accent5": 0x5b9bd5, "accent6": 0x70ad47, "hlink": 0x0563c1, "folHlink": 0x954f72}

// DefaultScheme is a new copy of the Office theme's colours.
func DefaultScheme() Scheme {
	s := Scheme{}
	for k, v := range defaultColors {
		s[k] = color.RGBA{uint8(v >> 16), uint8(v >> 8), uint8(v), 255}
	}
	return s
}

// ParseScheme reads the colours of a theme part; what it does not say keeps the Office theme's, so a broken theme
// still gives colours.
func ParseScheme(theme []byte) Scheme {
	s := DefaultScheme()
	if len(theme) == 0 {
		return s
	}
	doc, err := ox.Parse(theme)
	if err != nil {
		return s
	}
	s.Read(doc.Root.Path("a:themeElements", "a:clrScheme"))
	return s
}

// Read takes the colours of an a:clrScheme.
func (s Scheme) Read(scheme *ox.Node) {
	if scheme == nil {
		return
	}
	for _, n := range scheme.Elements() {
		if els := n.Elements(); len(els) > 0 {
			if c, ok := Resolve(els[0], s.Lookup); ok {
				s[strings.TrimPrefix(n.Name, "a:")] = c
			}
		}
	}
}

// Lookup is a scheme colour's value, the names a slide's colour map would stand for included.
func (s Scheme) Lookup(name string) (color.RGBA, bool) {
	switch name {
	case "bg1":
		name = "lt1"
	case "tx1":
		name = "dk1"
	case "bg2":
		name = "lt2"
	case "tx2":
		name = "dk2"
	case "phClr":
		return color.RGBA{0, 0, 0, 255}, true
	}
	c, ok := s[name]
	return c, ok
}

// Resolve is the colour of a colour element (a:srgbClr, a:schemeClr and so on), with its transforms applied. scheme
// says what a scheme colour's name stands for.
func Resolve(n *ox.Node, scheme func(string) (color.RGBA, bool)) (color.RGBA, bool) {
	if n == nil {
		return color.RGBA{}, false
	}
	var c color.RGBA
	switch n.Name {
	case "a:srgbClr":
		v, ok := ParseHex(n.Attr("val"))
		if !ok {
			return c, false
		}
		c = v
	case "a:scrgbClr":
		c = color.RGBA{GammaByte(attrFloat(n, "r") / 100000), GammaByte(attrFloat(n, "g") / 100000), GammaByte(attrFloat(n, "b") / 100000), 255}
	case "a:hslClr":
		c = HSL(attrFloat(n, "hue")/60000, attrFloat(n, "sat")/100000, attrFloat(n, "lum")/100000)
	case "a:sysClr":
		v, ok := ParseHex(n.Attr("lastClr"))
		if !ok {
			v = color.RGBA{0, 0, 0, 255}
			if strings.HasPrefix(n.Attr("val"), "window") && n.Attr("val") != "windowText" {
				v = color.RGBA{255, 255, 255, 255}
			}
		}
		c = v
	case "a:prstClr":
		name := strings.ToLower(n.Attr("val"))
		for _, r := range [][2]string{{"dk", "dark"}, {"lt", "light"}, {"med", "medium"}} {
			if strings.HasPrefix(name, r[0]) && !strings.HasPrefix(name, r[1]) {
				name = r[1] + strings.TrimPrefix(name, r[0])
			}
		}
		v, ok := colornames.Map[name]
		if !ok {
			return c, false
		}
		c = v
	case "a:schemeClr":
		v, ok := scheme(n.Attr("val"))
		if !ok {
			return c, false
		}
		c = v
	default:
		return c, false
	}
	return Transform(c, n), true
}

func attrFloat(n *ox.Node, name string) float64 {
	v, err := strconv.ParseFloat(strings.TrimSpace(n.Attr(name)), 64)
	if err != nil {
		return 0
	}
	return v
}

func clamp(v, lo, hi float64) float64 { return min(max(v, lo), hi) }

// ParseHex reads six hex digits.
func ParseHex(s string) (color.RGBA, bool) {
	v, err := strconv.ParseUint(s, 16, 32)
	if err != nil || len(s) != 6 {
		return color.RGBA{}, false
	}
	return color.RGBA{uint8(v >> 16), uint8(v >> 8), uint8(v), 255}, true
}

func linear(v float64) float64 {
	if v <= 0.04045 {
		return v / 12.92
	}
	return math.Pow((v+0.055)/1.055, 2.4)
}

func gamma(v float64) float64 {
	if v <= 0.0031308 {
		return v * 12.92
	}
	return 1.055*math.Pow(v, 1/2.4) - 0.055
}

// GammaByte turns a linear channel into a byte.
func GammaByte(v float64) uint8 { return uint8(clamp(gamma(clamp(v, 0, 1)), 0, 1)*255 + 0.5) }

// ToHSL is a colour's hue in degrees, and its saturation and lightness from 0 to 1.
func ToHSL(c color.RGBA) (h, s, l float64) {
	r, g, b := float64(c.R)/255, float64(c.G)/255, float64(c.B)/255
	hi, lo := max(r, g, b), min(r, g, b)
	l = (hi + lo) / 2
	d := hi - lo
	if d == 0 {
		return 0, 0, l
	}
	s = d / (1 - math.Abs(2*l-1))
	switch hi {
	case r:
		h = math.Mod((g-b)/d, 6)
	case g:
		h = (b-r)/d + 2
	default:
		h = (r-g)/d + 4
	}
	h *= 60
	if h < 0 {
		h += 360
	}
	return h, s, l
}

// HSL is the opaque colour of a hue in degrees and a saturation and lightness from 0 to 1.
func HSL(h, s, l float64) color.RGBA {
	h = math.Mod(h, 360)
	if h < 0 {
		h += 360
	}
	s, l = clamp(s, 0, 1), clamp(l, 0, 1)
	k := (1 - math.Abs(2*l-1)) * s
	x := k * (1 - math.Abs(math.Mod(h/60, 2)-1))
	m := l - k/2
	var r, g, b float64
	switch int(h / 60) {
	case 0:
		r, g = k, x
	case 1:
		r, g = x, k
	case 2:
		g, b = k, x
	case 3:
		g, b = x, k
	case 4:
		r, b = x, k
	default:
		r, b = k, x
	}
	to := func(v float64) uint8 { return uint8(clamp(v+m, 0, 1)*255 + 0.5) }
	return color.RGBA{to(r), to(g), to(b), 255}
}

// Transform applies a colour element's children in order. The alpha stays straight, as flow and gfx take it.
func Transform(c color.RGBA, n *ox.Node) color.RGBA {
	for _, t := range n.Elements() {
		v := attrFloat(t, "val") / 100000
		switch t.Name {
		case "a:alpha":
			c.A = uint8(clamp(v, 0, 1)*255 + 0.5)
		case "a:alphaMod":
			c.A = uint8(clamp(float64(c.A)*v, 0, 255) + 0.5)
		case "a:alphaOff":
			c.A = uint8(clamp(float64(c.A)+v*255, 0, 255) + 0.5)
		case "a:lumMod", "a:lumOff", "a:satMod", "a:satOff", "a:hueMod", "a:hueOff", "a:lum", "a:sat", "a:hue":
			h, s, l := ToHSL(c)
			switch t.Name {
			case "a:lumMod":
				l *= v
			case "a:lumOff":
				l += v
			case "a:lum":
				l = v
			case "a:satMod":
				s *= v
			case "a:satOff":
				s += v
			case "a:sat":
				s = v
			case "a:hueMod":
				h *= v
			case "a:hueOff":
				h += attrFloat(t, "val") / 60000
			case "a:hue":
				h = attrFloat(t, "val") / 60000
			}
			a := c.A
			c = HSL(h, s, l)
			c.A = a
		case "a:tint", "a:shade":
			f := func(b uint8) uint8 {
				l := linear(float64(b) / 255)
				if t.Name == "a:tint" {
					l = l*v + (1 - v)
				} else {
					l *= v
				}
				return GammaByte(l)
			}
			c.R, c.G, c.B = f(c.R), f(c.G), f(c.B)
		case "a:gamma":
			c.R, c.G, c.B = GammaByte(float64(c.R)/255), GammaByte(float64(c.G)/255), GammaByte(float64(c.B)/255)
		case "a:invGamma":
			f := func(b uint8) uint8 { return uint8(linear(float64(b)/255)*255 + 0.5) }
			c.R, c.G, c.B = f(c.R), f(c.G), f(c.B)
		case "a:inv":
			c.R, c.G, c.B = 255-c.R, 255-c.G, 255-c.B
		case "a:comp":
			h, s, l := ToHSL(c)
			a := c.A
			c = HSL(h+180, s, l)
			c.A = a
		case "a:gray":
			g := uint8(0.299*float64(c.R) + 0.587*float64(c.G) + 0.114*float64(c.B) + 0.5)
			c.R, c.G, c.B = g, g, g
		case "a:red":
			c.R = uint8(clamp(v, 0, 1)*255 + 0.5)
		case "a:green":
			c.G = uint8(clamp(v, 0, 1)*255 + 0.5)
		case "a:blue":
			c.B = uint8(clamp(v, 0, 1)*255 + 0.5)
		case "a:redMod":
			c.R = uint8(clamp(float64(c.R)*v, 0, 255) + 0.5)
		case "a:greenMod":
			c.G = uint8(clamp(float64(c.G)*v, 0, 255) + 0.5)
		case "a:blueMod":
			c.B = uint8(clamp(float64(c.B)*v, 0, 255) + 0.5)
		}
	}
	return c
}
