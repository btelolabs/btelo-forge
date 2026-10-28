package paint

import "math"

// Fitting curves to points, as a pencil stroke or a boolean's outline needs: Schneider's method (Graphics Gems,
// "An algorithm for automatically fitting digitized curves"), which fits one cubic by least squares with the
// tangents at its ends fixed, refines the points' parameters by Newton's method, and splits where the fit is worst
// until every point is within the tolerance. Corners, where the polyline turns sharply, end one run of curves and
// start the next, so they stay sharp nodes.

// cornerAngle is the turn, in radians, at which a polyline's vertex is kept as a corner rather than smoothed.
const cornerAngle = 35 * math.Pi / 180

// FitPath is a subpath of cubic segments within tol of the polyline pts, sharp at its corners and smooth elsewhere.
// A closed polyline makes a closed subpath.
func FitPath(pts []Pt, closed bool, tol float64) Subpath {
	pts = dedupe(pts, closed)
	if len(pts) < 2 {
		return polyline(closed, pts...)
	}
	corners := cornersOf(pts, closed)
	var out Subpath
	out.Closed = closed
	add := func(n PathNode) {
		if k := len(out.Nodes); k > 0 && out.Nodes[k-1].P.Dist(n.P) < 1e-9 {
			out.Nodes[k-1].Out, out.Nodes[k-1].Smooth = n.Out, false
			return
		}
		out.Nodes = append(out.Nodes, n)
	}
	// The runs between corners; a closed polyline without corners is one run round from its first point.
	runs := runsBetween(pts, corners, closed)
	for _, run := range runs {
		curves := fitRun(run, tol)
		for i, c := range curves {
			if i == 0 {
				add(PathNode{P: c[0], In: c[0], Out: c[1]})
			} else {
				// Joins inside a run are smooth: the fit keeps the tangents in line there.
				out.Nodes[len(out.Nodes)-1].Out, out.Nodes[len(out.Nodes)-1].Smooth = c[1], true
			}
			add(PathNode{P: c[3], In: c[2], Out: c[3]})
		}
	}
	if closed && len(out.Nodes) > 1 {
		// The last node is the first one again: fold it in.
		last := out.Nodes[len(out.Nodes)-1]
		if last.P.Dist(out.Nodes[0].P) < 1e-9 {
			out.Nodes[0].In = last.In
			out.Nodes[0].Smooth = len(corners) == 0
			out.Nodes = out.Nodes[:len(out.Nodes)-1]
		}
	}
	return out
}

// dedupe drops repeated points, and a closed polyline's last point when it is its first.
func dedupe(pts []Pt, closed bool) []Pt {
	var out []Pt
	for _, p := range pts {
		if len(out) == 0 || out[len(out)-1].Dist(p) > 1e-9 {
			out = append(out, p)
		}
	}
	if closed && len(out) > 1 && out[0].Dist(out[len(out)-1]) <= 1e-9 {
		out = out[:len(out)-1]
	}
	return out
}

// cornersOf are the indices where the polyline turns by more than cornerAngle; an open one's ends always count.
func cornersOf(pts []Pt, closed bool) []int {
	var out []int
	n := len(pts)
	for i := range n {
		if !closed && (i == 0 || i == n-1) {
			out = append(out, i)
			continue
		}
		a, b := pts[(i-1+n)%n], pts[(i+1)%n]
		d0, d1 := pts[i].Sub(a).Unit(), b.Sub(pts[i]).Unit()
		if math.Acos(min(max(d0.Dot(d1), -1), 1)) > cornerAngle {
			out = append(out, i)
		}
	}
	return out
}

// runsBetween cuts the polyline at its corners into runs that each start and end at one; a closed polyline's runs
// go round, the last one back to the first corner.
func runsBetween(pts []Pt, corners []int, closed bool) [][]Pt {
	n := len(pts)
	if !closed {
		var runs [][]Pt
		for i := 1; i < len(corners); i++ {
			runs = append(runs, pts[corners[i-1]:corners[i]+1])
		}
		return runs
	}
	if len(corners) == 0 {
		return [][]Pt{append(append([]Pt(nil), pts...), pts[0])}
	}
	var runs [][]Pt
	for i, c := range corners {
		next := corners[(i+1)%len(corners)]
		var run []Pt
		for k := c; ; k = (k + 1) % n {
			run = append(run, pts[k])
			if k == next && len(run) > 1 {
				break
			}
		}
		runs = append(runs, run)
	}
	return runs
}

// fitRun fits cubics to a run of points with free tangents at its ends; each cubic is its four control points.
func fitRun(pts []Pt, tol float64) [][4]Pt {
	if len(pts) == 2 {
		return [][4]Pt{line(pts[0], pts[1])}
	}
	t0 := pts[1].Sub(pts[0]).Unit()
	t1 := pts[len(pts)-2].Sub(pts[len(pts)-1]).Unit()
	if pts[0].Dist(pts[len(pts)-1]) < 1e-9 {
		// A run round a closed shape: one tangent through the start, both ways.
		t := pts[1].Sub(pts[len(pts)-2]).Unit()
		t0, t1 = t, t.Mul(-1)
	}
	return fitCubics(pts, t0, t1, tol, 0)
}

// line is a straight cubic from a to b.
func line(a, b Pt) [4]Pt { return [4]Pt{a, a, b, b} }

// fitCubics fits pts with one cubic whose end tangents are t0 and t1, or splits at the worst point and fits each
// half, joined smoothly.
func fitCubics(pts []Pt, t0, t1 Pt, tol float64, depth int) [][4]Pt {
	if len(pts) == 2 {
		d := pts[0].Dist(pts[1]) / 3
		return [][4]Pt{{pts[0], pts[0].Add(t0.Mul(d)), pts[1].Add(t1.Mul(d)), pts[1]}}
	}
	u := chordParams(pts)
	c := fitOne(pts, u, t0, t1)
	worst, at := maxError(pts, u, c)
	if worst <= tol {
		return [][4]Pt{c}
	}
	if worst <= tol*4 {
		for range 6 {
			u = reparam(pts, u, c)
			c = fitOne(pts, u, t0, t1)
			if worst, at = maxError(pts, u, c); worst <= tol {
				return [][4]Pt{c}
			}
		}
	}
	if depth > 24 {
		// Too deep to be a curve worth fitting: keep the points as lines.
		var out [][4]Pt
		for i := 1; i < len(pts); i++ {
			out = append(out, line(pts[i-1], pts[i]))
		}
		return out
	}
	at = min(max(at, 1), len(pts)-2)
	mid := pts[at-1].Sub(pts[at+1]).Unit()
	if mid == (Pt{}) {
		mid = pts[at-1].Sub(pts[at]).Unit()
	}
	left := fitCubics(pts[:at+1], t0, mid, tol, depth+1)
	right := fitCubics(pts[at:], mid.Mul(-1), t1, tol, depth+1)
	return append(left, right...)
}

// chordParams places each point along the run by the length of the polyline up to it, from 0 to 1.
func chordParams(pts []Pt) []float64 {
	u := make([]float64, len(pts))
	for i := 1; i < len(pts); i++ {
		u[i] = u[i-1] + pts[i].Dist(pts[i-1])
	}
	for i := range u {
		u[i] /= u[len(u)-1]
	}
	return u
}

// fitOne is the least-squares cubic through the run's ends with handles along t0 and t1.
func fitOne(pts []Pt, u []float64, t0, t1 Pt) [4]Pt {
	p0, p3 := pts[0], pts[len(pts)-1]
	var c00, c01, c11, x0, x1 float64
	for i, t := range u {
		s := 1 - t
		b0, b1, b2, b3 := s*s*s, 3*s*s*t, 3*s*t*t, t*t*t
		a0, a1 := t0.Mul(b1), t1.Mul(b2)
		c00 += a0.Dot(a0)
		c01 += a0.Dot(a1)
		c11 += a1.Dot(a1)
		tmp := pts[i].Sub(p0.Mul(b0 + b1)).Sub(p3.Mul(b2 + b3))
		x0 += a0.Dot(tmp)
		x1 += a1.Dot(tmp)
	}
	det := c00*c11 - c01*c01
	var l0, l1 float64
	if math.Abs(det) > 1e-12 {
		l0, l1 = (x0*c11-x1*c01)/det, (c00*x1-c01*x0)/det
	}
	seg := p0.Dist(p3)
	if l0 < seg*1e-6 || l1 < seg*1e-6 {
		// A degenerate fit: the usual third of the chord along each tangent.
		l0, l1 = seg/3, seg/3
	}
	return [4]Pt{p0, p0.Add(t0.Mul(l0)), p3.Add(t1.Mul(l1)), p3}
}

// maxError is how far the furthest point is from where the cubic puts it, and which point that is.
func maxError(pts []Pt, u []float64, c [4]Pt) (float64, int) {
	worst, at := 0.0, len(pts)/2
	for i := 1; i < len(pts)-1; i++ {
		if d := cubicAt(c[0], c[1], c[2], c[3], u[i]).Dist(pts[i]); d > worst {
			worst, at = d, i
		}
	}
	return worst, at
}

// reparam moves each point's parameter to where the cubic passes nearest it, by a step of Newton's method.
func reparam(pts []Pt, u []float64, c [4]Pt) []float64 {
	out := make([]float64, len(u))
	for i, t := range u {
		p := cubicAt(c[0], c[1], c[2], c[3], t)
		s := 1 - t
		d1 := c[1].Sub(c[0]).Mul(3 * s * s).Add(c[2].Sub(c[1]).Mul(6 * s * t)).Add(c[3].Sub(c[2]).Mul(3 * t * t))
		d2 := c[2].Sub(c[1].Mul(2)).Add(c[0]).Mul(6 * s).Add(c[3].Sub(c[2].Mul(2)).Add(c[1]).Mul(6 * t))
		diff := p.Sub(pts[i])
		den := d1.Dot(d1) + diff.Dot(d2)
		out[i] = t
		if math.Abs(den) > 1e-12 {
			out[i] = min(max(t-diff.Dot(d1)/den, 0), 1)
		}
	}
	return out
}
