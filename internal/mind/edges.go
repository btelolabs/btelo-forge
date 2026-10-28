package mind

import "math"

// Connectors: each runs from a side of one node to a side of another, chosen to face each other unless the user
// chose them, along a curve, right angles or a straight line. Their paths are worked out whenever a node may have
// moved, and kept in a grid of their own for drawing and pointing.

// edgeGeo is a connector's path on the board.
type edgeGeo struct {
	ok       bool
	pts      []Vec // a curve's four control points, or the corners of the other routes
	from, to Side
	box      Rect
	label    Vec // where its label is centred
}

// stub is how far a connector leaves a side straight before it turns.
const stub = 24

// arrowLen is an arrowhead's length; the path stops short of the node by it.
const arrowLen = 11

// sidePoint is the middle of side s of box b and the way out of it.
func sidePoint(b Rect, s Side) (Vec, Vec) {
	switch s {
	case SideTop:
		return Vec{b.CX(), b.Y0}, Vec{0, -1}
	case SideBottom:
		return Vec{b.CX(), b.Y1}, Vec{0, 1}
	case SideLeft:
		return Vec{b.X0, b.CY()}, Vec{-1, 0}
	}
	return Vec{b.X1, b.CY()}, Vec{1, 0}
}

// facing is the side of a that faces b.
func facing(a, b Rect) Side {
	dx, dy := b.CX()-a.CX(), b.CY()-a.CY()
	// Compare the gaps between the boxes, not their centres, so wide boxes side by side meet sideways.
	gx := math.Max(b.X0-a.X1, a.X0-b.X1)
	gy := math.Max(b.Y0-a.Y1, a.Y0-b.Y1)
	if gx >= gy {
		if dx >= 0 {
			return SideRight
		}
		return SideLeft
	}
	if dy >= 0 {
		return SideBottom
	}
	return SideTop
}

// route works out a connector's path between boxes a and b.
func route(e *Edge, a, b Rect) edgeGeo {
	g := edgeGeo{ok: true, from: e.FromSide, to: e.ToSide}
	if g.from == SideAuto {
		g.from = facing(a, b)
	}
	if g.to == SideAuto {
		g.to = facing(b, a)
	}
	p0, n0 := sidePoint(a, g.from)
	p3, n3 := sidePoint(b, g.to)
	switch e.Route {
	case RouteStraight:
		g.pts = []Vec{p0, p3}
	case RouteElbow:
		g.pts = elbow(p0, n0, p3, n3)
	default:
		d := math.Max(stub*2, math.Hypot(p3.X-p0.X, p3.Y-p0.Y)*0.4)
		g.pts = []Vec{p0, {p0.X + n0.X*d, p0.Y + n0.Y*d}, {p3.X + n3.X*d, p3.Y + n3.Y*d}, p3}
	}
	g.box = span(g.pts...).Grow(arrowLen + 2)
	g.label = pathMiddle(e.Route, g.pts)
	if e.Label != "" {
		g.box = g.box.Union(R(g.label.X-120, g.label.Y-30, 240, 60))
	}
	return g
}

// elbow is a path of horizontal and vertical runs from p0, leaving along n0, to p3, entering against n3.
func elbow(p0, n0, p3, n3 Vec) []Vec {
	s := Vec{p0.X + n0.X*stub, p0.Y + n0.Y*stub}
	t := Vec{p3.X + n3.X*stub, p3.Y + n3.Y*stub}
	h0, h3 := n0.Y == 0, n3.Y == 0
	var mid []Vec
	switch {
	case h0 && h3:
		x := (s.X + t.X) / 2
		if n0.X == n3.X { // both leave the same way: go round beyond the further one
			x = math.Max(s.X, t.X)
			if n0.X < 0 {
				x = math.Min(s.X, t.X)
			}
		}
		mid = []Vec{{x, s.Y}, {x, t.Y}}
	case !h0 && !h3:
		y := (s.Y + t.Y) / 2
		if n0.Y == n3.Y {
			y = math.Max(s.Y, t.Y)
			if n0.Y < 0 {
				y = math.Min(s.Y, t.Y)
			}
		}
		mid = []Vec{{s.X, y}, {t.X, y}}
	case h0:
		mid = []Vec{{t.X, s.Y}}
	default:
		mid = []Vec{{s.X, t.Y}}
	}
	pts := append([]Vec{p0, s}, mid...)
	pts = append(pts, t, p3)
	// Drop corners that do not turn.
	out := []Vec{pts[0]}
	for i := 1; i < len(pts)-1; i++ {
		a, b, c := out[len(out)-1], pts[i], pts[i+1]
		if (a.X == b.X && b.X == c.X) || (a.Y == b.Y && b.Y == c.Y) || (a == b) {
			continue
		}
		out = append(out, b)
	}
	return append(out, pts[len(pts)-1])
}

// bezier is the point at t on the cubic curve through four control points.
func bezier(p []Vec, t float64) Vec {
	u := 1 - t
	a, b, c, d := u*u*u, 3*u*u*t, 3*u*t*t, t*t*t
	return Vec{a*p[0].X + b*p[1].X + c*p[2].X + d*p[3].X, a*p[0].Y + b*p[1].Y + c*p[2].Y + d*p[3].Y}
}

// Polyline is a connector's path as straight pieces, a curve divided into steps.
func polyline(r Route, pts []Vec, steps int) []Vec {
	if r != RouteCurve || len(pts) != 4 {
		return pts
	}
	out := make([]Vec, 0, steps+1)
	for i := 0; i <= steps; i++ {
		out = append(out, bezier(pts, float64(i)/float64(steps)))
	}
	return out
}

// pathMiddle is the point halfway along a path.
func pathMiddle(r Route, pts []Vec) Vec {
	line := polyline(r, pts, 24)
	total := 0.0
	for i := 1; i < len(line); i++ {
		total += math.Hypot(line[i].X-line[i-1].X, line[i].Y-line[i-1].Y)
	}
	half := total / 2
	for i := 1; i < len(line); i++ {
		d := math.Hypot(line[i].X-line[i-1].X, line[i].Y-line[i-1].Y)
		if d >= half && d > 0 {
			t := half / d
			return Vec{line[i-1].X + (line[i].X-line[i-1].X)*t, line[i-1].Y + (line[i].Y-line[i-1].Y)*t}
		}
		half -= d
	}
	return line[0]
}

// layEdges works out the connectors' paths again when nodes may have moved, noting those that changed.
func (m *Map) layEdges() {
	if !m.lay.edgesStale {
		return
	}
	m.lay.edgesStale = false
	if m.edgeGrid.cells == nil {
		m.edgeGrid.init()
	}
	boxes := map[ID]Rect{}
	box := func(id ID) (Rect, bool) {
		if b, ok := boxes[id]; ok {
			return b, true
		}
		n := m.Node(id)
		if n == nil || m.Hidden(n) {
			return Rect{}, false
		}
		b := m.Box(n)
		boxes[id] = b
		return b, true
	}
	m.EachEdge(func(e *Edge) bool {
		a, okA := box(e.From)
		b, okB := box(e.To)
		old := e.geo
		if okA && okB {
			e.geo = route(e, a, b)
		} else {
			e.geo = edgeGeo{}
		}
		if !sameGeo(old, e.geo) {
			if old.ok {
				m.lay.region(old.box)
			}
			if e.geo.ok {
				m.lay.region(e.geo.box)
				m.edgeGrid.put(uint32(e.ID), e.geo.box)
			} else {
				m.edgeGrid.remove(uint32(e.ID))
			}
		}
		return true
	})
	// Connectors deleted since are gone from the grid.
	var drop []uint32
	m.edgeGrid.each(func(id uint32, _ Rect) {
		if e := m.Edge(EdgeID(id)); e == nil || !e.geo.ok {
			drop = append(drop, id)
		}
	})
	for _, id := range drop {
		m.edgeGrid.remove(id)
	}
}

func sameGeo(a, b edgeGeo) bool {
	if a.ok != b.ok || a.box != b.box || len(a.pts) != len(b.pts) || a.label != b.label {
		return false
	}
	for i := range a.pts {
		if a.pts[i] != b.pts[i] {
			return false
		}
	}
	return true
}

// EdgesIn lists the connectors whose paths may cross r, in id order.
func (m *Map) EdgesIn(r Rect) []*Edge {
	m.Layout()
	var out []*Edge
	m.edgeGrid.query(r, func(id uint32, _ Rect) {
		if e := m.Edge(EdgeID(id)); e != nil && e.geo.ok {
			out = append(out, e)
		}
	})
	sortEdges(out)
	return out
}

func sortEdges(list []*Edge) {
	for i := 1; i < len(list); i++ {
		for j := i; j > 0 && list[j].ID < list[j-1].ID; j-- {
			list[j], list[j-1] = list[j-1], list[j]
		}
	}
}

// EdgeAt is the connector passing within tol of the board point x, y (or its label holding it), or nil.
func (m *Map) EdgeAt(x, y, tol float64) *Edge {
	list := m.EdgesIn(R(x-tol, y-tol, 2*tol, 2*tol))
	for i := len(list) - 1; i >= 0; i-- {
		e := list[i]
		if e.Label != "" && R(e.geo.label.X-40, e.geo.label.Y-12, 80, 24).Contains(x, y) {
			return e
		}
		line := polyline(e.Route, e.geo.pts, 32)
		for k := 1; k < len(line); k++ {
			if segDist(Vec{x, y}, line[k-1], line[k]) <= tol {
				return e
			}
		}
	}
	return nil
}

// segDist is the distance from p to the segment a–b.
func segDist(p, a, b Vec) float64 {
	dx, dy := b.X-a.X, b.Y-a.Y
	t := 0.0
	if l := dx*dx + dy*dy; l > 0 {
		t = math.Max(0, math.Min(1, ((p.X-a.X)*dx+(p.Y-a.Y)*dy)/l))
	}
	return math.Hypot(p.X-a.X-t*dx, p.Y-a.Y-t*dy)
}

// EdgePath is a connector's path on the board as straight pieces, and where its label is centred.
func (m *Map) EdgePath(e *Edge) ([]Vec, Vec, bool) {
	m.Layout()
	if !e.geo.ok {
		return nil, Vec{}, false
	}
	return polyline(e.Route, e.geo.pts, 32), e.geo.label, true
}

// Anchor is the middle of side s of node n on the board, and the way out of it.
func (m *Map) Anchor(n *Node, s Side) (Vec, Vec) {
	m.Layout()
	return sidePoint(m.Box(n), s)
}
