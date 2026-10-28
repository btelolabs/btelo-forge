package paint

import (
	"errors"
	"math"
	"slices"
)

// Booleans combine vector objects into one, as Affinity Designer's Add, Subtract, Intersect and Exclude do. The
// outlines are flattened in canvas space, every edge is split where any other crosses or overlaps it, and an edge
// is kept, turned so the result's inside is on its left, when the result is inside on one side of it and outside
// on the other. The kept edges are joined into rings and fitted with curves again, so the result is a curve of a
// few smooth and sharp nodes rather than a polygon.

// BoolOp is how a boolean combines objects.
type BoolOp int

const (
	BoolAdd       BoolOp = iota // everything any object covers
	BoolSubtract                // the bottom object less the ones above it
	BoolIntersect               // only what every object covers
	BoolExclude                 // what an odd number of objects cover
)

// ErrBoolean means a boolean was asked of fewer than two vector layers.
var ErrBoolean = errors.New("choose two or more vector layers to combine")

// boolGrid is the grid, in canvas pixels, the outlines' points are snapped to, so points that meet are equal.
const boolGrid = 1.0 / 256

// boolTolerance is how far the flattened outlines may stray from the curves, and boolFit how far the fitted
// result may stray from them in turn: together well under a pixel's tenth and a half.
const (
	boolTolerance = 0.03
	boolFit       = 0.1
)

func snapPt(p Pt) Pt {
	return Pt{math.Round(p.X/boolGrid) * boolGrid, math.Round(p.Y/boolGrid) * boolGrid}
}

// operand is one object's filled area: its outline cut into the pieces the boolean works with, in canvas space,
// and its fill rule. Its coverage is reckoned from those pieces, so it agrees with them exactly.
type operand struct {
	edges []boolEdge
	rule  FillRule
}

// inside reports whether the operand covers p.
func (o operand) inside(p Pt) bool {
	w := 0
	for _, e := range o.edges {
		a, b := e.a, e.b
		if (a.Y <= p.Y) != (b.Y <= p.Y) {
			if x := a.X + (p.Y-a.Y)*(b.X-a.X)/(b.Y-a.Y); x > p.X {
				if b.Y > a.Y {
					w++
				} else {
					w--
				}
			}
		}
	}
	if o.rule == EvenOdd {
		return w%2 != 0
	}
	return w != 0
}

// covered reports whether the result of op covers a point that each operand covers or not as in.
func (op BoolOp) covered(in []bool) bool {
	switch op {
	case BoolSubtract:
		return in[0] && !slices.Contains(in[1:], true)
	case BoolIntersect:
		return !slices.Contains(in, false)
	case BoolExclude:
		n := 0
		for _, b := range in {
			if b {
				n++
			}
		}
		return n%2 == 1
	}
	return slices.Contains(in, true)
}

// boolEdge is a piece of an object's outline, from a to b, and which object it is of.
type boolEdge struct {
	a, b Pt
	of   int
}

// Combine is the outline that op makes of objects, bottom first, as rings of curves in canvas space; it is empty
// when nothing is left.
func Combine(op BoolOp, objects []Vector) []Subpath {
	ops := make([]operand, len(objects))
	var edges []boolEdge
	for i, v := range objects {
		polys, _ := flatten(v.Outline(), v.M, boolTolerance)
		ops[i].rule = v.Rule
		for _, p := range polys {
			ring := make([]Pt, 0, len(p))
			for _, q := range p {
				q = snapPt(q)
				if len(ring) == 0 || ring[len(ring)-1] != q {
					ring = append(ring, q)
				}
			}
			for len(ring) > 1 && ring[0] == ring[len(ring)-1] {
				ring = ring[:len(ring)-1]
			}
			if len(ring) < 3 {
				continue
			}
			for k := range ring {
				edges = append(edges, boolEdge{ring[k], ring[(k+1)%len(ring)], i})
			}
		}
	}
	edges = splitEdges(edges)
	for _, e := range edges {
		ops[e.of].edges = append(ops[e.of].edges, e)
	}
	// Keep the edges the result has inside on one side only, turned to have it on the left.
	var kept []boolEdge
	in := make([]bool, len(ops))
	for _, e := range edges {
		d := e.b.Sub(e.a)
		n := d.Perp().Unit().Mul(boolGrid / 8)
		m := e.a.Add(d.Mul(0.5))
		for i, o := range ops {
			in[i] = o.inside(m.Add(n))
		}
		left := op.covered(in)
		for i, o := range ops {
			in[i] = o.inside(m.Sub(n))
		}
		right := op.covered(in)
		switch {
		case left && !right:
			kept = append(kept, boolEdge{e.a, e.b, 0})
		case right && !left:
			kept = append(kept, boolEdge{e.b, e.a, 0})
		}
	}
	// Two objects' edges along the same line are kept once.
	seen := map[[2]Pt]bool{}
	kept = slices.DeleteFunc(kept, func(e boolEdge) bool {
		k := [2]Pt{e.a, e.b}
		if seen[k] {
			return true
		}
		seen[k] = true
		return false
	})
	var out []Subpath
	for _, ring := range joinRings(kept) {
		out = append(out, FitPath(ring, true, boolFit))
	}
	return out
}

// splitEdges cuts every edge where another crosses it or where one ends on it.
func splitEdges(edges []boolEdge) []boolEdge {
	type box struct{ x0, x1, y0, y1 float64 }
	boxes := make([]box, len(edges))
	order := make([]int, len(edges))
	for i, e := range edges {
		boxes[i] = box{min(e.a.X, e.b.X), max(e.a.X, e.b.X), min(e.a.Y, e.b.Y), max(e.a.Y, e.b.Y)}
		order[i] = i
	}
	slices.SortFunc(order, func(a, b int) int { return cmpFloat(boxes[a].x0, boxes[b].x0) })
	cuts := make([][]Pt, len(edges)) // where each edge is cut, the same point for both edges that meet there
	for oi, i := range order {
		for _, j := range order[oi+1:] {
			if boxes[j].x0 > boxes[i].x1 {
				break
			}
			if boxes[j].y0 > boxes[i].y1 || boxes[j].y1 < boxes[i].y0 {
				continue
			}
			pi, pj := crossings(edges[i], edges[j])
			cuts[i] = append(cuts[i], pi...)
			cuts[j] = append(cuts[j], pj...)
		}
	}
	var out []boolEdge
	for i, e := range edges {
		// The cuts in order along the edge, then its end.
		d := e.b.Sub(e.a)
		pts := cuts[i]
		slices.SortFunc(pts, func(p, q Pt) int { return cmpFloat(p.Sub(e.a).Dot(d), q.Sub(e.a).Dot(d)) })
		pts = append(pts, e.b)
		prev := e.a
		for _, p := range pts {
			if p == prev {
				continue
			}
			out = append(out, boolEdge{prev, p, e.of})
			prev = p
		}
	}
	return out
}

func cmpFloat(a, b float64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// crossings are the points, snapped to the grid, where e and f are cut: where the other crosses them away from
// their ends, or ends on them; two edges along the same line cut each other where each ends.
func crossings(e, f boolEdge) (pe, pf []Pt) {
	r, s := e.b.Sub(e.a), f.b.Sub(f.a)
	den := r.Cross(s)
	qp := f.a.Sub(e.a)
	const eps = 1e-12
	inner := func(t float64) bool { return t > 1e-9 && t < 1-1e-9 }
	if math.Abs(den) < eps*max(r.Len()*s.Len(), 1) {
		if math.Abs(qp.Cross(r)) > boolGrid/4*r.Len() {
			return nil, nil // parallel, apart
		}
		// Along one line: where each one's ends fall on the other.
		at := func(p Pt, a Pt, d Pt) float64 { return p.Sub(a).Dot(d) / d.Dot(d) }
		for _, p := range []Pt{f.a, f.b} {
			if inner(at(p, e.a, r)) {
				pe = append(pe, p)
			}
		}
		for _, p := range []Pt{e.a, e.b} {
			if inner(at(p, f.a, s)) {
				pf = append(pf, p)
			}
		}
		return pe, pf
	}
	t, u := qp.Cross(s)/den, qp.Cross(r)/den
	if t < -1e-9 || t > 1+1e-9 || u < -1e-9 || u > 1+1e-9 {
		return nil, nil
	}
	// One point for both, so the pieces of each meet there exactly. An end of one on the other cuts only the other.
	p := snapPt(e.a.Lerp(e.b, t))
	switch {
	case !inner(t):
		p = e.a
		if t > 0.5 {
			p = e.b
		}
	case !inner(u):
		p = f.a
		if u > 0.5 {
			p = f.b
		}
	}
	if inner(t) && p != e.a && p != e.b {
		pe = append(pe, p)
	}
	if inner(u) && p != f.a && p != f.b {
		pf = append(pf, p)
	}
	return pe, pf
}

// joinRings joins edges end to start into closed rings; at a point where several leave, the one turning most to
// the right is taken, which keeps rings that only touch apart.
func joinRings(edges []boolEdge) [][]Pt {
	from := map[Pt][]int{}
	for i, e := range edges {
		from[e.a] = append(from[e.a], i)
	}
	used := make([]bool, len(edges))
	var rings [][]Pt
	for start := range edges {
		if used[start] {
			continue
		}
		used[start] = true
		ring := []Pt{edges[start].a}
		cur := start
		for {
			e := edges[cur]
			if e.b == edges[start].a {
				break
			}
			ring = append(ring, e.b)
			next, best := -1, math.Inf(-1)
			d := e.b.Sub(e.a)
			for _, j := range from[e.b] {
				if used[j] {
					continue
				}
				o := edges[j].b.Sub(edges[j].a)
				// The angle turned from d to o, rightward positive in a y-down space.
				turn := -math.Atan2(d.Cross(o), d.Dot(o))
				if turn > best {
					next, best = j, turn
				}
			}
			if next < 0 {
				ring = nil // an end that leads nowhere: not a ring
				break
			}
			used[next] = true
			cur = next
		}
		if len(ring) >= 3 {
			rings = append(rings, ring)
		}
	}
	return rings
}

// Boolean replaces the chosen vector layers (ids, any order) with one vector layer holding op's result, in the
// place of the bottom one, styled as it is, as one step; it returns the new layer.
func (d *Doc) Boolean(op BoolOp, ids []int) (*Node, error) {
	var nodes []*Node
	for _, l := range d.Layers() { // top first
		if slices.Contains(ids, l.ID) && l.Vector != nil {
			nodes = append(nodes, l)
		}
	}
	if len(nodes) < 2 {
		return nil, ErrBoolean
	}
	slices.Reverse(nodes) // bottom first
	objects := make([]Vector, len(nodes))
	for i, n := range nodes {
		objects[i] = *n.Vector
	}
	bottom := nodes[0]
	res := *bottom.Vector
	res.Kind, res.Path, res.M, res.Rule = VectorCurve, Combine(op, objects), Identity, NonZero
	res.Size, res.Radius, res.Sides, res.Inner = Pt{}, 0, 0, 0
	// The fill's gradient was placed in the bottom object's own space; the result's is the canvas.
	res.Fill = mapPaint(res.Fill, bottom.Vector.M)
	res.Stroke.Paint = mapPaint(res.Stroke.Paint, bottom.Vector.M)
	m := d.Mark()
	for _, n := range nodes[1:] {
		_, parent, i := d.find(n.ID)
		parent.Children = slices.Delete(parent.Children, i, i+1)
	}
	n := d.Node(bottom.ID)
	n.Vector = &res
	d.Active = n.ID
	d.Commit("Boolean", m)
	return n, nil
}

// mapPaint is a paint placed in a space that m maps to the canvas, placed in the canvas instead.
func mapPaint(p VectorPaint, m Affine) VectorPaint {
	if p.Kind == PaintLinear || p.Kind == PaintRadial {
		p.From, p.To = m.Map(p.From), m.Map(p.To)
		p.Stops = slices.Clone(p.Stops)
	}
	return p
}
