package printing

import "math"

// Flattening and stroking: what the screen preview, the transparency flattener and a printer's dashed lines work from.

// polygon is a flattened contour.
type polygon struct {
	pts    []Point
	closed bool
}

// flatten turns a path into polygons through m, whose units are pixels: curves are divided to suit.
func flatten(p *Path, m Matrix) []polygon {
	if p == nil {
		return nil
	}
	var out []polygon
	var cur []Point
	var start, at Point
	flush := func(closed bool) {
		if len(cur) > 1 || closed && len(cur) > 0 {
			out = append(out, polygon{cur, closed})
		}
		cur = nil
	}
	i := 0
	for _, op := range p.Ops {
		switch op {
		case 'M':
			flush(false)
			x, y := m.Apply(p.Pts[i].X, p.Pts[i].Y)
			i++
			start, at = Point{x, y}, Point{x, y}
			cur = append(cur, at)
		case 'L':
			x, y := m.Apply(p.Pts[i].X, p.Pts[i].Y)
			i++
			at = Point{x, y}
			if len(cur) == 0 {
				start = at
			}
			cur = append(cur, at)
		case 'C':
			x1, y1 := m.Apply(p.Pts[i].X, p.Pts[i].Y)
			x2, y2 := m.Apply(p.Pts[i+1].X, p.Pts[i+1].Y)
			x3, y3 := m.Apply(p.Pts[i+2].X, p.Pts[i+2].Y)
			i += 3
			if len(cur) == 0 {
				cur = append(cur, at)
				start = at
			}
			d := math.Hypot(x1-at.X, y1-at.Y) + math.Hypot(x2-x1, y2-y1) + math.Hypot(x3-x2, y3-y2)
			n := min(max(int(math.Sqrt(d)*1.6), 2), 200)
			for k := 1; k <= n; k++ {
				t := float64(k) / float64(n)
				u := 1 - t
				a, b, c, e := u*u*u, 3*u*u*t, 3*u*t*t, t*t*t
				cur = append(cur, Point{a*at.X + b*x1 + c*x2 + e*x3, a*at.Y + b*y1 + c*y2 + e*y3})
			}
			at = Point{x3, y3}
		case 'Z':
			flush(true)
			at = start
		}
	}
	flush(false)
	return out
}

// StrokeOutline is the shape a stroke covers: convex pieces all wound one way, to be filled by the non-zero rule. Its
// curves are divided for resolution pixels a point, and the result is in the stroke's own space.
func StrokeOutline(s *Stroke, resolution float64) *Path {
	lines := flatten(s.Path, Scale(resolution, resolution))
	if len(s.Dash) > 0 {
		lines = dashLines(lines, s.Dash, s.Phase, resolution)
	}
	b := &outliner{out: &Path{}, hw: s.Width / 2 * resolution, cap: s.Cap, join: s.Join, miter: s.Miter, k: 1 / resolution}
	if b.miter == 0 {
		b.miter = 10
	}
	for _, l := range lines {
		b.line(l)
	}
	return b.out
}

type outliner struct {
	out   *Path
	hw    float64
	cap   Cap
	join  Join
	miter float64
	k     float64
}

// piece adds a convex polygon, turned to wind the same way as every other.
func (o *outliner) piece(pts ...Point) {
	area := 0.0
	for i := range pts {
		j := (i + 1) % len(pts)
		area += pts[i].X*pts[j].Y - pts[j].X*pts[i].Y
	}
	if area < 0 {
		for i, j := 0, len(pts)-1; i < j; i, j = i+1, j-1 {
			pts[i], pts[j] = pts[j], pts[i]
		}
	}
	for i, p := range pts {
		if i == 0 {
			o.out.MoveTo(p.X*o.k, p.Y*o.k)
		} else {
			o.out.LineTo(p.X*o.k, p.Y*o.k)
		}
	}
	o.out.Close()
}

func (o *outliner) disc(c Point) {
	n := min(max(int(o.hw*2)+8, 8), 48)
	pts := make([]Point, n)
	for i := range pts {
		a := float64(i) * 2 * math.Pi / float64(n)
		pts[i] = Point{c.X + o.hw*math.Cos(a), c.Y + o.hw*math.Sin(a)}
	}
	o.piece(pts...)
}

func (o *outliner) segment(a, b Point) {
	dx, dy := b.X-a.X, b.Y-a.Y
	l := math.Hypot(dx, dy)
	if l == 0 {
		return
	}
	nx, ny := -dy/l*o.hw, dx/l*o.hw
	o.piece(Point{a.X + nx, a.Y + ny}, Point{b.X + nx, b.Y + ny}, Point{b.X - nx, b.Y - ny}, Point{a.X - nx, a.Y - ny})
}

func (o *outliner) corner(a, b, c Point) {
	d0x, d0y := b.X-a.X, b.Y-a.Y
	d1x, d1y := c.X-b.X, c.Y-b.Y
	l0, l1 := math.Hypot(d0x, d0y), math.Hypot(d1x, d1y)
	if l0 == 0 || l1 == 0 {
		return
	}
	d0x, d0y, d1x, d1y = d0x/l0, d0y/l0, d1x/l1, d1y/l1
	cross := d0x*d1y - d0y*d1x
	if math.Abs(cross) < 1e-6 && d0x*d1x+d0y*d1y > 0 {
		return
	}
	if o.join == JoinRound {
		o.disc(b)
		return
	}
	side := 1.0
	if cross > 0 {
		side = -1
	}
	p0 := Point{b.X - d0y*o.hw*side, b.Y + d0x*o.hw*side}
	p1 := Point{b.X - d1y*o.hw*side, b.Y + d1x*o.hw*side}
	if o.join == JoinMiter {
		half := math.Sqrt(max((1+d0x*d1x+d0y*d1y)/2, 0))
		if half > 1e-3 && 1/half <= o.miter {
			mx, my := (p0.X+p1.X)/2-b.X, (p0.Y+p1.Y)/2-b.Y
			if ml := math.Hypot(mx, my); ml > 0 {
				k := o.hw / half / ml
				o.piece(b, p0, Point{b.X + mx*k, b.Y + my*k}, p1)
				return
			}
		}
	}
	o.piece(b, p0, p1)
}

func (o *outliner) end(tip, inner Point) {
	switch o.cap {
	case CapRound:
		o.disc(tip)
	case CapSquare:
		dx, dy := tip.X-inner.X, tip.Y-inner.Y
		if l := math.Hypot(dx, dy); l > 0 {
			o.segment(tip, Point{tip.X + dx/l*o.hw, tip.Y + dy/l*o.hw})
		}
	}
}

func (o *outliner) line(l polygon) {
	pts := make([]Point, 0, len(l.pts))
	for _, p := range l.pts {
		if n := len(pts); n == 0 || math.Hypot(p.X-pts[n-1].X, p.Y-pts[n-1].Y) > 1e-6 {
			pts = append(pts, p)
		}
	}
	if l.closed && len(pts) > 1 && math.Hypot(pts[0].X-pts[len(pts)-1].X, pts[0].Y-pts[len(pts)-1].Y) <= 1e-6 {
		pts = pts[:len(pts)-1]
	}
	n := len(pts)
	if n == 0 {
		return
	}
	if n == 1 {
		if o.cap == CapRound {
			o.disc(pts[0])
		}
		return
	}
	for i := 0; i+1 < n; i++ {
		o.segment(pts[i], pts[i+1])
	}
	for i := 1; i+1 < n; i++ {
		o.corner(pts[i-1], pts[i], pts[i+1])
	}
	if l.closed && n > 2 {
		o.segment(pts[n-1], pts[0])
		o.corner(pts[n-2], pts[n-1], pts[0])
		o.corner(pts[n-1], pts[0], pts[1])
		return
	}
	o.end(pts[0], pts[1])
	o.end(pts[n-1], pts[n-2])
}

// maxDashes bounds the pieces of one dashed line, which a hostile file could make endless.
const maxDashes = 200000

// dashLines cuts polygons into the pattern's dashes; the pattern is in points, the polygons at k pixels a point.
func dashLines(lines []polygon, pattern []float64, phase, k float64) []polygon {
	total := 0.0
	for _, v := range pattern {
		total += max(v, 0)
	}
	if total <= 0 {
		return lines
	}
	var out []polygon
	for _, l := range lines {
		pts := l.pts
		if l.closed && len(pts) > 0 {
			pts = append(pts[:len(pts):len(pts)], pts[0])
		}
		idx, on := 0, true
		left := pattern[0]
		for skip := math.Mod(max(phase, 0), total*2); skip > 0; {
			if skip < left {
				left -= skip
				break
			}
			skip -= left
			idx = (idx + 1) % len(pattern)
			left, on = pattern[idx], !on
		}
		left *= k
		var cur []Point
		for i := 0; i+1 < len(pts) && len(out) < maxDashes; i++ {
			a, b := pts[i], pts[i+1]
			seg := math.Hypot(b.X-a.X, b.Y-a.Y)
			if seg == 0 {
				continue
			}
			pos := 0.0
			if on && len(cur) == 0 {
				cur = append(cur, a)
			}
			for seg-pos > left && len(out) < maxDashes {
				pos += left
				p := Point{a.X + (b.X-a.X)*pos/seg, a.Y + (b.Y-a.Y)*pos/seg}
				if on {
					out = append(out, polygon{pts: append(cur, p)})
					cur = nil
				} else {
					cur = []Point{p}
				}
				on = !on
				idx = (idx + 1) % len(pattern)
				left = max(pattern[idx]*k, 1e-3)
			}
			left -= seg - pos
			if on {
				cur = append(cur, b)
			}
		}
		if len(cur) > 1 {
			out = append(out, polygon{pts: cur})
		}
	}
	return out
}
