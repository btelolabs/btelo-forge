package paint

import (
	"image"
	"math"
	"slices"
)

// Pt is a point on the canvas, or in an object's own space, in pixels.
type Pt struct{ X, Y float64 }

func (p Pt) Add(q Pt) Pt             { return Pt{p.X + q.X, p.Y + q.Y} }
func (p Pt) Sub(q Pt) Pt             { return Pt{p.X - q.X, p.Y - q.Y} }
func (p Pt) Mul(k float64) Pt        { return Pt{p.X * k, p.Y * k} }
func (p Pt) Dot(q Pt) float64        { return p.X*q.X + p.Y*q.Y }
func (p Pt) Cross(q Pt) float64      { return p.X*q.Y - p.Y*q.X }
func (p Pt) Len() float64            { return math.Hypot(p.X, p.Y) }
func (p Pt) Dist(q Pt) float64       { return p.Sub(q).Len() }
func (p Pt) Lerp(q Pt, t float64) Pt { return Pt{p.X + (q.X-p.X)*t, p.Y + (q.Y-p.Y)*t} }

// Unit is the point scaled to length 1, or zero for zero.
func (p Pt) Unit() Pt {
	if l := p.Len(); l > 0 {
		return Pt{p.X / l, p.Y / l}
	}
	return Pt{}
}

// Perp is the point turned a quarter clockwise on the canvas (whose y grows down).
func (p Pt) Perp() Pt { return Pt{-p.Y, p.X} }

// Map maps a point through an affine map.
func (m Affine) Map(p Pt) Pt {
	x, y := m.Apply(p.X, p.Y)
	return Pt{x, y}
}

// ScaleFactor is how much the map stretches lengths, on average: the square root of its area factor.
func (m Affine) ScaleFactor() float64 { return math.Sqrt(math.Abs(m.A*m.E - m.B*m.D)) }

// Rect is a box with float sides.
type Rect struct{ Min, Max Pt }

// Empty reports whether the box holds nothing.
func (r Rect) Empty() bool { return r.Max.X <= r.Min.X || r.Max.Y <= r.Min.Y }

// Union is the box holding both.
func (r Rect) Union(o Rect) Rect {
	switch {
	case r.Empty() && r.Min == r.Max && r.Min == (Pt{}):
		return o
	case o.Empty() && o.Min == o.Max && o.Min == (Pt{}):
		return r
	}
	return Rect{Pt{min(r.Min.X, o.Min.X), min(r.Min.Y, o.Min.Y)}, Pt{max(r.Max.X, o.Max.X), max(r.Max.Y, o.Max.Y)}}
}

// boundsOf is the box holding points.
func boundsOf(pts []Pt) Rect {
	if len(pts) == 0 {
		return Rect{}
	}
	r := Rect{pts[0], pts[0]}
	for _, p := range pts[1:] {
		r.Min.X, r.Min.Y = min(r.Min.X, p.X), min(r.Min.Y, p.Y)
		r.Max.X, r.Max.Y = max(r.Max.X, p.X), max(r.Max.Y, p.Y)
	}
	return r
}

// cubicAt is a point of the cubic Bézier p0 c1 c2 p3 at t.
func cubicAt(p0, c1, c2, p3 Pt, t float64) Pt {
	u := 1 - t
	return p0.Mul(u * u * u).Add(c1.Mul(3 * u * u * t)).Add(c2.Mul(3 * u * t * t)).Add(p3.Mul(t * t * t))
}

// splitCubic splits a cubic at t into two.
func splitCubic(p0, c1, c2, p3 Pt, t float64) (a, b [4]Pt) {
	p01, p12, p23 := p0.Lerp(c1, t), c1.Lerp(c2, t), c2.Lerp(p3, t)
	p012, p123 := p01.Lerp(p12, t), p12.Lerp(p23, t)
	m := p012.Lerp(p123, t)
	return [4]Pt{p0, p01, p012, m}, [4]Pt{m, p123, p23, p3}
}

// flattenCubic appends points along a cubic, after p0, so that no chord strays from the curve by more than tol.
func flattenCubic(out []Pt, p0, c1, c2, p3 Pt, tol float64) []Pt {
	// The number of pieces from the curve's second differences, which bound how far a chord can stray.
	dd := max(p0.Sub(c1.Mul(2)).Add(c2).Len(), c1.Sub(c2.Mul(2)).Add(p3).Len())
	n := int(math.Ceil(math.Sqrt(3 * dd / (4 * tol))))
	n = min(max(n, 1), 1000)
	for i := 1; i <= n; i++ {
		out = append(out, cubicAt(p0, c1, c2, p3, float64(i)/float64(n)))
	}
	return out
}

// ---- Rasterizing: coverage of polygons, anti-aliased.

// FillRule says which areas a path's overlapping parts fill.
type FillRule int

const (
	NonZero FillRule = iota // filled where the outline winds round at all, as most paths want
	EvenOdd                 // filled where it winds round an odd number of times, so overlaps make holes
)

// polyEdge is one line of a polygon, with its direction for winding.
type polyEdge struct {
	x0, y0, x1, y1 float64
	dir            int // +1 going down, −1 going up
}

// coverageOf covers closed polygons (in the pixels of area) with anti-aliased edges: each pixel row is sampled on
// sub lines, and each line's spans are covered exactly across the pixels they cross. It returns the coverage, 0 to
// 255, of area.
func coverageOf(polys [][]Pt, rule FillRule, area image.Rectangle, sub int) []uint8 {
	w, h := area.Dx(), area.Dy()
	out := make([]uint8, w*h)
	if w <= 0 || h <= 0 {
		return out
	}
	var edges []polyEdge
	for _, poly := range polys {
		for i := range poly {
			a, b := poly[i], poly[(i+1)%len(poly)]
			if a.Y == b.Y {
				continue
			}
			e := polyEdge{a.X - float64(area.Min.X), a.Y - float64(area.Min.Y), b.X - float64(area.Min.X), b.Y - float64(area.Min.Y), 1}
			if e.y0 > e.y1 {
				e = polyEdge{e.x1, e.y1, e.x0, e.y0, -1}
			}
			if e.y1 <= 0 || e.y0 >= float64(h) {
				continue
			}
			edges = append(edges, e)
		}
	}
	if len(edges) == 0 {
		return out
	}
	slices.SortFunc(edges, func(a, b polyEdge) int {
		switch {
		case a.y0 < b.y0:
			return -1
		case a.y0 > b.y0:
			return 1
		}
		return 0
	})
	type crossing struct {
		x   float64
		dir int
	}
	cover := make([]float64, w+1)
	var xs []crossing
	var active []polyEdge
	next := 0
	inv := 1 / float64(sub)
	for y := range h {
		// Edges come in as the row reaches them and go when it passes them.
		for next < len(edges) && edges[next].y0 < float64(y+1) {
			active = append(active, edges[next])
			next++
		}
		active = slices.DeleteFunc(active, func(e polyEdge) bool { return e.y1 <= float64(y) })
		if len(active) == 0 {
			continue
		}
		clear(cover)
		touched := false
		for s := range sub {
			sy := float64(y) + (float64(s)+0.5)*inv
			xs = xs[:0]
			for _, e := range active {
				if e.y0 <= sy && sy < e.y1 {
					xs = append(xs, crossing{e.x0 + (sy-e.y0)*(e.x1-e.x0)/(e.y1-e.y0), e.dir})
				}
			}
			if len(xs) < 2 {
				continue
			}
			slices.SortFunc(xs, func(a, b crossing) int {
				switch {
				case a.x < b.x:
					return -1
				case a.x > b.x:
					return 1
				}
				return 0
			})
			wind := 0
			for i := 0; i+1 < len(xs); i++ {
				wind += xs[i].dir
				in := wind != 0
				if rule == EvenOdd {
					in = (i+1)%2 == 1
				}
				if !in {
					continue
				}
				l, r := max(xs[i].x, 0), min(xs[i+1].x, float64(w))
				if r <= l {
					continue
				}
				touched = true
				li, ri := int(l), int(r)
				if li == ri {
					cover[li] += (r - l) * inv
					continue
				}
				cover[li] += (float64(li+1) - l) * inv
				for x := li + 1; x < ri; x++ {
					cover[x] += inv
				}
				if ri < w {
					cover[ri] += (r - float64(ri)) * inv
				}
			}
		}
		if !touched {
			continue
		}
		row := out[y*w : (y+1)*w]
		for x := range row {
			row[x] = uint8(min(cover[x], 1)*255 + 0.5)
		}
	}
	return out
}
