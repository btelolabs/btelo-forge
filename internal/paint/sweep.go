package paint

import (
	"math"
	"slices"
	"sync"
)

// The vector brush draws with a brush's tip as a vector layer's shapes: the tip, as the convex outline of its dab,
// is swept along the line the pointer draws, and the area it passes over becomes a closed outline, fitted with
// curves. Each stroke is a shape added to the layer's others, so a drawing made of many strokes is one layer.

// TipOutline is the outline of the brush's dab in canvas pixels about its centre, turned and squashed as the
// brush is: the convex hull of where it covers half or more, anticlockwise. Its tips' outlines are worked out once.
func TipOutline(b Brush) []Pt {
	b = b.normalized()
	key := outlineKey{tip: b.Tip, shape: b.Shape, angle: b.Angle, round: b.Round}
	unit := tipOutlines.of(key, func() []Pt { return unitOutline(b) })
	// The unit outline is for a brush 1 pixel across: scaled to the size.
	out := make([]Pt, len(unit))
	for i, p := range unit {
		out[i] = p.Mul(b.Size)
	}
	return out
}

type outlineKey struct {
	tip          Tip
	shape        *TipShape
	angle, round float64
}

// outlineCache keeps the outlines of the tips drawn with, by tip, picture, angle and roundness.
type outlineCache struct {
	sync.Mutex
	outlines map[outlineKey][]Pt
}

var tipOutlines = outlineCache{outlines: map[outlineKey][]Pt{}}

// of is the outline kept for k, worked out by work when there is none.
func (c *outlineCache) of(k outlineKey, work func() []Pt) []Pt {
	c.Lock()
	defer c.Unlock()
	if o, ok := c.outlines[k]; ok {
		return o
	}
	if len(c.outlines) > 256 {
		clear(c.outlines) // brushes changed many times: start again rather than grow
	}
	o := work()
	c.outlines[k] = o
	return o
}

// unitOutline is the outline of b's dab for a brush 1 pixel across: worked out on a dab 64 pixels across.
func unitOutline(b Brush) []Pt {
	const across = 64.0
	b.Size, b.Hardness = across, 1
	if b.Tip == TipChalk {
		b.Tip = TipRound // its grain is texture, not shape
	}
	s := b.shape()
	half := int(math.Ceil(b.reach()))
	var pts []Pt
	for y := -half; y <= half; y++ {
		for x := -half; x <= half; x++ {
			if s.at(float64(x)+0.5, float64(y)+0.5, 0, 0) >= 0.5 {
				// The pixel's corners, so the hull takes in whole pixels.
				for _, c := range [4]Pt{{0, 0}, {1, 0}, {1, 1}, {0, 1}} {
					pts = append(pts, Pt{float64(x) + c.X, float64(y) + c.Y})
				}
			}
		}
	}
	hull := convexHull(pts)
	if len(hull) < 3 {
		hull = disc(Pt{}, across/2) // too thin to have an inside: a dot
	}
	for i := range hull {
		hull[i] = hull[i].Mul(1 / across)
	}
	return hull
}

// convexHull is the convex hull of points, anticlockwise on the screen (with y down), by the monotone chain.
func convexHull(pts []Pt) []Pt {
	if len(pts) < 3 {
		return pts
	}
	pts = slices.Clone(pts)
	slices.SortFunc(pts, func(a, b Pt) int {
		if a.X != b.X {
			return cmpFloat(a.X, b.X)
		}
		return cmpFloat(a.Y, b.Y)
	})
	pts = slices.Compact(pts)
	cross := func(o, a, b Pt) float64 { return a.Sub(o).Cross(b.Sub(o)) }
	var hull []Pt
	for _, p := range pts {
		for len(hull) >= 2 && cross(hull[len(hull)-2], hull[len(hull)-1], p) <= 0 {
			hull = hull[:len(hull)-1]
		}
		hull = append(hull, p)
	}
	lower := len(hull) + 1
	for i := len(pts) - 2; i >= 0; i-- {
		p := pts[i]
		for len(hull) >= lower && cross(hull[len(hull)-2], hull[len(hull)-1], p) <= 0 {
			hull = hull[:len(hull)-1]
		}
		hull = append(hull, p)
	}
	return hull[:len(hull)-1]
}

// support is the point of a convex outline farthest along direction d.
func support(outline []Pt, d Pt) Pt {
	best, bestV := outline[0], math.Inf(-1)
	for _, p := range outline {
		if v := p.Dot(d); v > bestV {
			best, bestV = p, v
		}
	}
	return best
}

// Sweep is the outline of the area a convex tip outline passes over as its centre goes along line: its left edge
// forward, the tip's end round, its right edge back and the tip's start round, as one closed ring wound
// positively. A line of one point is the tip itself.
func Sweep(line []Pt, tip []Pt) []Pt {
	line = dedupe(line, false)
	if len(line) == 0 || len(tip) < 3 {
		return nil
	}
	if len(line) == 1 {
		out := make([]Pt, len(tip))
		for i, t := range tip {
			out[i] = line[0].Add(t)
		}
		return positive(out)
	}
	// The way the line goes at each point: along its segment, between two at a bend.
	dirs := make([]Pt, len(line))
	for i := range line {
		var d Pt
		if i > 0 {
			d = d.Add(line[i].Sub(line[i-1]).Unit())
		}
		if i+1 < len(line) {
			d = d.Add(line[i+1].Sub(line[i]).Unit())
		}
		if d.Len() == 0 {
			d = line[min(i+1, len(line)-1)].Sub(line[max(i-1, 0)]).Unit()
		}
		dirs[i] = d.Unit()
	}
	left := func(d Pt) Pt { return Pt{d.Y, -d.X} } // with y down, the left of going along d
	var ring []Pt
	for i, p := range line {
		ring = append(ring, p.Add(support(tip, left(dirs[i]))))
	}
	ring = append(ring, capOf(line[len(line)-1], tip, left(dirs[len(dirs)-1]), dirs[len(dirs)-1])...)
	for i := len(line) - 1; i >= 0; i-- {
		ring = append(ring, line[i].Add(support(tip, left(dirs[i]).Mul(-1))))
	}
	start := dirs[0].Mul(-1)
	ring = append(ring, capOf(line[0], tip, left(start), start)...)
	return positive(ring)
}

// capOf is the tip's outline at p from its point farthest to side round its front, the way out going along d, to
// its point farthest the other side: the end of a stroke.
func capOf(p Pt, tip []Pt, side, d Pt) []Pt {
	var out []Pt
	for _, t := range tip {
		if t.Dot(d) > 0 {
			out = append(out, t)
		}
	}
	// Ordered round from the side, through the front, to the other side.
	slices.SortFunc(out, func(a, b Pt) int { return cmpFloat(-a.Dot(side), -b.Dot(side)) })
	for i := range out {
		out[i] = p.Add(out[i])
	}
	return out
}
