// Package paint is a small layered painting engine in the manner of Photoshop: round, square, flat and chalk brushes
// with size, hardness, opacity and spacing, an eraser that shares the brush, layers and groups, a rectangular
// selection, undo and PNG export. The editor's canvas, the drawing on screen and the drawn overlays of a video all
// paint through it.
package paint

import (
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
	Color    color.NRGBA // the alpha is ignored; Opacity is the brush's transparency
}

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
	if b.Tip < 0 || b.Tip >= tipCount {
		b.Tip = TipRound
	}
	return b
}

// coverage is how much of the dab centred at (cx, cy) covers the pixel whose centre is (px, py), from 0 to 1.
func (b Brush) coverage(px, py, cx, cy float64) float64 {
	r := b.Size / 2
	// The tip is turned by its angle and squashed by its roundness; the flat tip is a narrow ellipse at 45° more.
	angle, round := b.Angle, b.Round
	if b.Tip == TipFlat {
		angle, round = angle+45, round*0.3
	}
	dx, dy := px-cx, py-cy
	if angle != 0 {
		sn, cs := math.Sincos(-angle * math.Pi / 180)
		dx, dy = dx*cs-dy*sn, dx*sn+dy*cs
	}
	dy /= round
	var dist float64
	switch b.Tip {
	case TipSquare:
		dist = max(math.Abs(dx), math.Abs(dy))
	default:
		dist = math.Hypot(dx, dy)
	}
	// The soft part of the edge grows with softness; at least a pixel of it keeps hard brushes smooth.
	soft := max(r*(1-b.Hardness), 1)
	c := min(max((r-dist)/soft, 0), 1)
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
