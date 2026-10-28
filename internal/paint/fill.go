package paint

import (
	"image"
	"image/color"
	"math"
)

// paintArea lays colour over the chosen layer in pieces (its work area when nil), as a brush does: src gives each
// pixel's colour, premultiplied, and how much it covers (0 to 255), which the selection lessens further. On the
// layer mask it paints the colour's brightness instead. It is recorded as one step called name.
func (d *Doc) paintArea(name string, pieces []image.Rectangle, src func(x, y int) (c [4]uint32, cover uint32)) error {
	l, err := d.pixelLayer()
	if err != nil {
		return err
	}
	m := d.Mark()
	d.paintAreaOn(l, pieces, src)
	d.Commit(name, m)
	return nil
}

// paintAreaOn lays colour over layer l, or its mask while it is edited, as paintArea does, without recording it.
func (d *Doc) paintAreaOn(l *Node, pieces []image.Rectangle, src func(x, y int) (c [4]uint32, cover uint32)) {
	toMask := d.EditMask && l.Mask != nil
	if pieces == nil {
		pieces = d.areaOf(l, toMask, 0)
	}
	if !toMask {
		d.dropText(l)
	}
	for _, r := range pieces {
		sel := d.selectionIn(r)
		if toMask {
			l.Mask.Tiles(r, func(pix []uint8, part image.Rectangle, corner image.Point) {
				for y := part.Min.Y; y < part.Max.Y; y++ {
					for x := part.Min.X; x < part.Max.X; x++ {
						c, k := src(x, y)
						k = mul8(mul8(k, c[3]), coverage(sel, x, y))
						if k == 0 {
							continue
						}
						// The colour unpremultiplied, then its brightness.
						v := uint32(luma(color.NRGBA{R: uint8(c[0] * 255 / c[3]), G: uint8(c[1] * 255 / c[3]), B: uint8(c[2] * 255 / c[3]), A: 255}))
						i := (y-corner.Y)*TileSize + x - corner.X
						pix[i] = uint8(mul8(uint32(pix[i]), 255-k) + mul8(v, k))
					}
				}
			})
			continue
		}
		l.Pixels.Tiles(r, func(pix []uint8, part image.Rectangle, corner image.Point) {
			for y := part.Min.Y; y < part.Max.Y; y++ {
				for x := part.Min.X; x < part.Max.X; x++ {
					c, k := src(x, y)
					k = mul8(k, coverage(sel, x, y))
					if k == 0 {
						continue
					}
					i := ((y-corner.Y)*TileSize + x - corner.X) * 4
					p := pix[i : i+4 : i+4]
					oldA := uint32(p[3])
					keep := 255 - mul8(c[3], k) // what is left of the pixel under the colour
					for ch := range 4 {
						p[ch] = uint8(min(mul8(uint32(p[ch]), keep)+mul8(c[ch], k), 255))
					}
					if l.LockAlpha {
						keepAlpha(p, oldA)
					}
				}
			}
		})
	}
}

// piecesOf are the canvas areas of a mask's tiles: where it can be other than its fill.
func piecesOf(m *Mask) []image.Rectangle {
	out := make([]image.Rectangle, 0, len(m.p.tiles))
	for k := range m.p.tiles {
		out = append(out, tileRect(k).Add(m.p.off))
	}
	return out
}

// premul is a colour premultiplied by its alpha.
func premul(c color.NRGBA) [4]uint32 {
	a := uint32(c.A)
	return [4]uint32{mul8(uint32(c.R), a), mul8(uint32(c.G), a), mul8(uint32(c.B), a), a}
}

// Bucket fills the area around (x, y) whose colour is within tolerance of it, as Photoshop's Paint Bucket does:
// only the connected part when contiguous, from the picture as it looks with allLayers.
func (d *Doc) Bucket(x, y int, c color.NRGBA, opacity float64, tolerance int, contiguous, allLayers bool) error {
	if _, err := d.pixelLayer(); err != nil {
		return err
	}
	shape, err := d.WandMask(x, y, tolerance, contiguous, allLayers)
	if err != nil {
		return err
	}
	pc, op := premul(c), uint32(math.Round(min(max(opacity, 0), 1)*255))
	return d.paintArea("Paint bucket", piecesOf(shape), func(x, y int) ([4]uint32, uint32) {
		return pc, mul8(uint32(shape.At(x, y)), op)
	})
}

// GradientKind is a gradient's shape, as Photoshop's five.
type GradientKind int

const (
	GradientLinear GradientKind = iota
	GradientRadial
	GradientAngle
	GradientReflected
	GradientDiamond
)

// GradientKinds are the shapes, in Photoshop's order.
var GradientKinds = []GradientKind{GradientLinear, GradientRadial, GradientAngle, GradientReflected, GradientDiamond}

// Gradient is a blend from one colour to another along a line drawn on the picture.
type Gradient struct {
	Kind     GradientKind
	From, To [2]float64 // the line, in picture pixels
	Start    color.NRGBA
	End      color.NRGBA
	Opacity  float64
	Reverse  bool
	Dither   bool // breaks up banding, as Photoshop's Dither
}

// At is where along the gradient (0 to 1) the point (x, y) falls.
func (g Gradient) At(x, y float64) float64 {
	dx, dy := g.To[0]-g.From[0], g.To[1]-g.From[1]
	px, py := x-g.From[0], y-g.From[1]
	length2 := dx*dx + dy*dy
	var t float64
	switch g.Kind {
	case GradientRadial:
		t = math.Sqrt((px*px + py*py) / length2)
	case GradientAngle:
		// Round from the line, clockwise, as Photoshop's angle gradient.
		a := math.Atan2(py, px) - math.Atan2(dy, dx)
		t = math.Mod(a/(2*math.Pi)+1, 1)
	case GradientReflected:
		t = math.Abs((px*dx + py*dy) / length2)
	case GradientDiamond:
		// The line is a corner of a square turned with it.
		l := math.Sqrt(length2)
		ux, uy := dx/l, dy/l
		t = (math.Abs(px*ux+py*uy) + math.Abs(-px*uy+py*ux)) / l
	default:
		t = (px*dx + py*dy) / length2
	}
	t = min(max(t, 0), 1)
	if g.Reverse {
		t = 1 - t
	}
	return t
}

// bayer4 is an ordered dither, in sixteenths.
var bayer4 = [4][4]float64{{0, 8, 2, 10}, {12, 4, 14, 6}, {3, 11, 1, 9}, {15, 7, 13, 5}}

// DrawGradient lays a gradient over the chosen layer, within the selection.
func (d *Doc) DrawGradient(g Gradient) error {
	if math.Hypot(g.To[0]-g.From[0], g.To[1]-g.From[1]) < 1 {
		return nil // a click without a drag draws nothing, as in Photoshop
	}
	return d.paintArea("Gradient", nil, g.source())
}

// MaskGradient fades the chosen layer or group out along a gradient from white, showing it, to black, hiding it,
// laid on its mask, which it is given when it has none, as one step. It works over what the layer shows, within the
// selection, and leaves the mask being edited.
func (d *Doc) MaskGradient(g Gradient) error {
	if math.Hypot(g.To[0]-g.From[0], g.To[1]-g.From[1]) < 1 {
		return nil
	}
	n := d.Node(d.Active)
	if n == nil {
		return ErrNoLayer
	}
	m := d.Mark()
	if n.Mask == nil {
		n.Mask, n.MaskOff = NewMask(255), false
	}
	d.EditMask = true
	g.Start, g.End = color.NRGBA{R: 255, G: 255, B: 255, A: 255}, color.NRGBA{A: 255}
	g.Opacity = 1
	d.paintAreaOn(n, nil, g.source())
	d.Commit("Mask gradient", m)
	return nil
}

// source is the gradient as paintArea's colour at each pixel.
func (g Gradient) source() func(x, y int) ([4]uint32, uint32) {
	op := min(max(g.Opacity, 0), 1)
	s, e := g.Start, g.End
	return func(x, y int) ([4]uint32, uint32) {
		t := g.At(float64(x)+0.5, float64(y)+0.5)
		mix := func(a, b uint8) float64 { return float64(a) + (float64(b)-float64(a))*t }
		alpha := mix(s.A, e.A)
		var c [4]float64
		// Colours blend unpremultiplied, so a fade to transparency keeps its colour.
		c[0], c[1], c[2], c[3] = mix(s.R, e.R), mix(s.G, e.G), mix(s.B, e.B), alpha
		jitter := 0.5
		if g.Dither {
			jitter = (bayer4[y&3][x&3] + 0.5) / 16
		}
		var out [4]uint32
		a := math.Floor(c[3] + jitter)
		for ch := range 3 {
			out[ch] = uint32(math.Floor(c[ch]*a/255 + jitter))
		}
		out[3] = uint32(a)
		for ch := range 3 {
			out[ch] = min(out[ch], out[3])
		}
		return out, uint32(math.Round(op * 255))
	}
}
