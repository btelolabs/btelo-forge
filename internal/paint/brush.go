// Package paint is a small layered painting engine in the manner of Photoshop: round, square, flat, chalk and picture
// brushes with size, hardness, opacity, spacing and shape dynamics, ready-made brush presets, an eraser that shares
// the brush, layers and groups, a rectangular selection, undo and PNG export. The editor's canvas, the drawing on
// screen and the drawn overlays of a video all paint through it.
package paint

import (
	"image"
	"image/color"
	"math"
)

// Tip is the shape of the brush.
type Tip int

const (
	TipRound Tip = iota
	TipSquare
	TipFlat  // a narrow ellipse at 45°, like a calligraphy pen
	TipChalk // round, broken up by grain
	tipCount

	// TipShaped is a tip made from a picture, the brush's Shape.
	TipShaped Tip = 100
)

// Tips lists every tip, in the order the interface offers them.
var Tips = []Tip{TipRound, TipSquare, TipFlat, TipChalk}

// Brush is how strokes are painted; the eraser uses the same settings.
type Brush struct {
	Size     float64 // diameter in canvas pixels
	Hardness float64 // 0 (soft edge) to 1 (hard edge)
	Opacity  float64 // 0 to 1: the most one stroke adds, however often it crosses itself
	Spacing  float64 // distance between dabs as a fraction of the size
	Flow     float64 // 0 to 1: how much each dab adds, building up to Opacity where dabs overlap
	Smooth   float64 // 0 to 1: how far the brush trails the pointer, steadying the line (Photoshop's smoothing)
	Angle    float64 // degrees the tip is turned
	Round    float64 // 0 to 1: the tip's height against its width
	Tip      Tip
	Shape    *TipShape   // the picture of a TipShaped tip; never changed in place
	Color    color.NRGBA // the alpha is ignored; Opacity is the brush's transparency

	// Shape dynamics, as Photoshop's: each dab varies at random, so a textured tip is not stamped the same way
	// over and over.
	SizeJitter  float64 // 0 to 1: how much smaller than the size a dab may be
	AngleJitter float64 // 0 to 1: how far a dab may be turned either way, 1 being half a turn
	Scatter     float64 // 0 to maxScatter: how far from the line a dab may be thrown, as a share of the size
	Follow      bool    // the tip turns with the stroke's direction, its angle added
}

// maxScatter is the farthest a dab may be thrown from the line, as a share of the brush's size.
const maxScatter = 3

// DefaultBrush is a medium, fairly hard red round brush.
func DefaultBrush() Brush {
	return Brush{Size: 12, Hardness: 0.85, Opacity: 1, Spacing: 0.1, Flow: 1, Smooth: 0.1, Round: 1, Tip: TipRound,
		Color: color.NRGBA{R: 0xf0, G: 0x40, B: 0x40, A: 0xff}}
}

// normalized keeps the settings within their ranges.
func (b Brush) normalized() Brush {
	b.Size = min(max(b.Size, 1), 2000)
	b.Hardness = min(max(b.Hardness, 0), 1)
	b.Opacity = min(max(b.Opacity, 0), 1)
	b.Spacing = min(max(b.Spacing, 0.01), 2)
	if b.Flow <= 0 {
		b.Flow = 1 // brushes saved before flow existed
	}
	b.Flow = min(b.Flow, 1)
	b.Smooth = min(max(b.Smooth, 0), 1)
	if b.Round <= 0 {
		b.Round = 1
	}
	b.Round = min(max(b.Round, 0.05), 1)
	b.Angle = math.Mod(b.Angle, 360)
	b.SizeJitter = min(max(b.SizeJitter, 0), 1)
	b.AngleJitter = min(max(b.AngleJitter, 0), 1)
	b.Scatter = min(max(b.Scatter, 0), maxScatter)
	switch {
	case b.Tip == TipShaped && b.Shape.valid():
	case b.Tip < 0 || b.Tip >= tipCount:
		b.Tip = TipRound
	}
	return b
}

// dynamic is whether the brush's dabs vary.
func (b Brush) dynamic() bool {
	return b.SizeJitter > 0 || b.AngleJitter > 0 || b.Scatter > 0 || b.Follow
}

// reach is how far from its centre a dab can cover anything: a turned square's corners, and a picture's, reach past
// the circle.
func (b Brush) reach() float64 {
	if b.Tip == TipSquare || b.Tip == TipShaped {
		return b.Size/2*math.Sqrt2 + 1
	}
	return b.Size/2 + 1
}

// coverage is how much of the dab centred at (cx, cy) covers the pixel whose centre is (px, py), from 0 to 1.
func (b Brush) coverage(px, py, cx, cy float64) float64 {
	s := b.shape()
	return s.at(px, py, cx, cy)
}

// dabShape is a brush's tip made ready for working its coverage out pixel by pixel: what does not change from one
// pixel to the next, its turn above all, worked out once a dab.
type dabShape struct {
	b        Brush
	r, soft  float64
	sn, cs   float64 // the turn, as its sine and cosine
	turned   bool
	invRound float64
	tipScale float64 // a picture tip's pixels to a canvas pixel
}

// shape is the brush made ready for its dabs.
func (b Brush) shape() dabShape {
	s := dabShape{b: b, r: b.Size / 2}
	angle, round := b.turn()
	if angle != 0 {
		s.sn, s.cs = math.Sincos(-angle * math.Pi / 180)
		s.turned = true
	}
	s.invRound = 1 / round
	// The soft part of the edge grows with softness; at least a pixel of it keeps hard brushes smooth.
	s.soft = max(s.r*(1-b.Hardness), 1)
	if b.Tip == TipShaped && b.Shape != nil {
		s.tipScale = float64(max(b.Shape.W, b.Shape.H)) / b.Size
	}
	return s
}

// turn is how far the tip is turned, in degrees, and squashed: by its angle and roundness, the flat tip being a
// narrow ellipse at 45° more.
func (b Brush) turn() (angle, round float64) {
	if b.Tip == TipFlat {
		return b.Angle + 45, b.Round * 0.3
	}
	return b.Angle, b.Round
}

// at is how much of the dab centred at (cx, cy) covers the pixel whose centre is (px, py), from 0 to 1.
func (s *dabShape) at(px, py, cx, cy float64) float64 {
	dx, dy := px-cx, py-cy
	if s.turned {
		dx, dy = dx*s.cs-dy*s.sn, dx*s.sn+dy*s.cs
	}
	dy *= s.invRound
	b := &s.b
	if b.Tip == TipShaped {
		return b.Shape.atScaled(dx, dy, s.tipScale)
	}
	var dist float64
	switch b.Tip {
	case TipSquare:
		dist = max(math.Abs(dx), math.Abs(dy))
	default:
		dist = math.Sqrt(dx*dx + dy*dy)
	}
	c := min(max((s.r-dist)/s.soft, 0), 1)
	c = c * c * (3 - 2*c) // smoothstep
	if b.Tip == TipChalk && c > 0 {
		c *= grain(int(math.Floor(px)), int(math.Floor(py)))
	}
	return c
}

// grain is a fixed per-pixel texture between 0 and 1, so chalk looks the same wherever it is laid down.
func grain(x, y int) float64 {
	h := uint32(x)*374761393 + uint32(y)*668265263
	h = (h ^ h>>13) * 1274126177
	h ^= h >> 16
	v := float64(h&0xffff) / 0xffff
	// Mostly solid, with gaps: the paper showing through.
	return min(max((v-0.25)/0.5, 0), 1)
}

// TipCoverage is how much of a dab centred at (cx, cy) covers the pixel centred at (px, py), 0 to 1, for drawing a
// preview of the brush's tip.
func TipCoverage(b Brush, px, py, cx, cy float64) float64 {
	return b.normalized().coverage(px, py, cx, cy)
}

// Footprint is the brush's dab drawn at scale (screen pixels to a canvas pixel), centred in a square picture: how
// much it covers each pixel, for showing the brush's true shape at the pointer.
func Footprint(b Brush, scale float64) *image.Alpha {
	b = b.normalized()
	if b.Tip == TipChalk {
		b.Tip = TipRound // its grain is texture, not shape
	}
	half := int(math.Ceil(b.reach()*scale)) + 1
	img := image.NewAlpha(image.Rect(-half, -half, half, half))
	s := b.shape()
	for y := -half; y < half; y++ {
		for x := -half; x < half; x++ {
			// The canvas point under this screen pixel's centre, the dab centred on the canvas's origin.
			c := s.at((float64(x)+0.5)/scale, (float64(y)+0.5)/scale, 0, 0)
			img.Pix[img.PixOffset(x, y)] = uint8(min(max(c, 0), 1)*255 + 0.5)
		}
	}
	return img
}
