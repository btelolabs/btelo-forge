package mind

import (
	"math"
	"slices"
	"time"
)

// Connectors: each runs from a side of one node to a side of another, chosen to face each other unless the user
// chose them, along a curve, right angles or a straight line. Its middle can be dragged (Bend), and the path then
// passes there. A connector the user has not shaped finds its own way round the nodes between its ends: when the
// straight-forward path would cross one, other sides and detours are tried and the shortest that crosses none is
// taken. Paths are worked out again when their ends move or something changes where they run, and kept in a grid
// of their own for drawing and pointing.

// edgeGeo is a connector's path on the board.
type edgeGeo struct {
	ok       bool
	pts      []Vec // a curve's four control points, or the corners of the other routes
	from, to Side
	box      Rect
	label    Vec // where its label is centred: the path's middle, which a drag moves
	base     Vec // where the middle would be with no bend
	bend     Vec // the bend the path takes: the user's, or the detour found round the nodes in its way
	key      edgeKey
	pending  bool // its way round the nodes is still to be found: it shows its plain path meanwhile
}

// edgeKey is what a connector's path depends on besides the nodes in its way.
type edgeKey struct {
	a, b     Rect
	from, to Side
	route    Route
	bend     Vec
	label    bool
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

// route works out a connector's path between boxes a and b, leaving and entering by the sides from and to (auto
// for facing), its middle moved by bend.
func route(r Route, from, to Side, bend Vec, label bool, a, b Rect) edgeGeo {
	g := edgeGeo{ok: true, from: from, to: to, bend: bend}
	if g.from == SideAuto {
		g.from = facing(a, b)
	}
	if g.to == SideAuto {
		g.to = facing(b, a)
	}
	p0, n0 := sidePoint(a, g.from)
	p3, n3 := sidePoint(b, g.to)
	switch r {
	case RouteStraight:
		g.pts = []Vec{p0, p3}
		g.base = Vec{(p0.X + p3.X) / 2, (p0.Y + p3.Y) / 2}
		if bend != (Vec{}) {
			g.pts = []Vec{p0, g.base.Add(bend), p3}
		}
	case RouteElbow:
		g.pts = elbow(p0, n0, p3, n3, Vec{})
		g.base = pathMiddle(r, g.pts)
		if bend != (Vec{}) {
			g.pts = elbow(p0, n0, p3, n3, bend)
		}
	default:
		d := math.Max(stub*2, math.Hypot(p3.X-p0.X, p3.Y-p0.Y)*0.4)
		g.pts = []Vec{p0, {p0.X + n0.X*d, p0.Y + n0.Y*d}, {p3.X + n3.X*d, p3.Y + n3.Y*d}, p3}
		g.base = bezier(g.pts, 0.5)
		// The curve's middle is (p0 + 3·p1 + 3·p2 + p3) / 8: moving both inner points by 4/3 of the bend moves it
		// by the bend.
		g.pts[1] = g.pts[1].Add(Vec{bend.X * 4 / 3, bend.Y * 4 / 3})
		g.pts[2] = g.pts[2].Add(Vec{bend.X * 4 / 3, bend.Y * 4 / 3})
	}
	g.label = g.base.Add(bend)
	if r == RouteElbow && bend != (Vec{}) {
		g.label = pathMiddle(r, g.pts)
	}
	g.box = span(polyline(r, g.pts, 24)...).Grow(arrowLen + 2)
	if label {
		g.box = g.box.Union(R(g.label.X-120, g.label.Y-30, 240, 60))
	}
	return g
}

// elbow is a path of horizontal and vertical runs from p0, leaving along n0, to p3, entering against n3; bend
// moves its middle run (or, with one corner, opens it into a step) by that much.
func elbow(p0, n0, p3, n3, bend Vec) []Vec {
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
		x += bend.X
		mid = []Vec{{x, s.Y}, {x, t.Y}}
	case !h0 && !h3:
		y := (s.Y + t.Y) / 2
		if n0.Y == n3.Y {
			y = math.Max(s.Y, t.Y)
			if n0.Y < 0 {
				y = math.Min(s.Y, t.Y)
			}
		}
		y += bend.Y
		mid = []Vec{{s.X, y}, {t.X, y}}
	case h0:
		if bend == (Vec{}) {
			mid = []Vec{{t.X, s.Y}}
		} else {
			x, y := t.X+bend.X, s.Y+bend.Y
			mid = []Vec{{x, s.Y}, {x, y}, {t.X, y}}
		}
	default:
		if bend == (Vec{}) {
			mid = []Vec{{s.X, t.Y}}
		} else {
			x, y := s.X+bend.X, t.Y+bend.Y
			mid = []Vec{{s.X, y}, {x, y}, {x, t.Y}}
		}
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

// layEdges works out the connectors' paths again where they may have changed: those changed themselves, those
// running through the areas changed (regions, or everywhere when all is true) — where an end moved, or a node may
// now be in their way — and those not shown or still being routed. It looks only at those, found through the
// connectors' grid, however many the map holds, and notes the paths that changed.
func (m *Map) layEdges(regions []Rect, all bool) {
	if !m.lay.edgesStale {
		return
	}
	l := &m.lay
	l.edgesStale = false
	if m.edgeGrid.cells == nil {
		m.edgeGrid.init()
	}
	look := map[EdgeID]bool{}
	if all {
		m.EachEdge(func(e *Edge) bool { look[e.ID] = true; return true })
	} else {
		for _, set := range []map[EdgeID]bool{l.edgeDirty, l.edgeOff, l.edgePending} {
			for id := range set {
				look[id] = true
			}
		}
		for _, r := range regions {
			m.edgeGrid.query(r, func(id uint32, _ Rect) { look[EdgeID(id)] = true })
		}
	}
	clear(l.edgeDirty)
	ids := make([]EdgeID, 0, len(look))
	for id := range look {
		ids = append(ids, id)
	}
	slices.Sort(ids)
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
	deadline := time.Time{}
	if l.budget > 0 {
		began := time.Now()
		deadline = began.Add(l.budget - l.spent)
		defer func() { l.spent += time.Since(began) }()
	}
	l.routing = false
	for _, id := range ids {
		e := m.Edge(id)
		if e == nil {
			// Deleted: gone from the grid and the sets.
			m.edgeGrid.remove(uint32(id))
			delete(l.edgeOff, id)
			delete(l.edgePending, id)
			continue
		}
		a, okA := box(e.From)
		b, okB := box(e.To)
		old := e.geo
		if !okA || !okB {
			e.geo = edgeGeo{}
		} else {
			key := edgeKey{a, b, e.FromSide, e.ToSide, e.Route, e.Bend, e.Label != ""}
			if !old.ok || old.key != key || old.pending || all || pathTouches(e.Route, old.pts, regions) {
				if !deadline.IsZero() && time.Now().After(deadline) {
					// Out of time this frame: the plain path for now, its way round found in the next.
					e.geo = route(e.Route, e.FromSide, e.ToSide, e.Bend, e.Label != "", a, b)
					e.geo.pending = true
				} else {
					e.geo = m.routeAround(e, a, b)
				}
				e.geo.key = key
			}
		}
		if e.geo.ok {
			delete(l.edgeOff, id)
		} else {
			l.edgeOff[id] = true
		}
		if e.geo.pending {
			l.edgePending[id], l.routing = true, true
		} else {
			delete(l.edgePending, id)
		}
		if !sameGeo(old, e.geo) {
			if old.ok {
				l.region(old.box)
			}
			if e.geo.ok {
				l.region(e.geo.box)
				m.edgeGrid.put(uint32(e.ID), e.geo.box)
			} else {
				m.edgeGrid.remove(uint32(e.ID))
			}
		}
	}
	if l.routing {
		l.edgesStale = true // the next frame goes on with them
	}
}

// pathTouches reports whether a path passes through, or near, one of the regions: only then may something there
// have come into its way, or moved out of it.
func pathTouches(r Route, pts []Vec, regions []Rect) bool {
	line := polyline(r, pts, 16)
	box := span(line...).Grow(arrowLen + 2)
	for _, c := range regions {
		if !c.Overlaps(box) {
			continue
		}
		c = c.Grow(arrowLen + 2)
		for i := 1; i < len(line); i++ {
			if segmentCrosses(line[i-1], line[i], c) {
				return true
			}
		}
	}
	return false
}

// Routing reports whether some connectors still look for their way round the nodes: the window draws again soon,
// for the next frame's layout (Changed) to go on with them.
func (m *Map) Routing() bool { return m.lay.routing }

// SetRouteBudget bounds how long a frame (from one Changed to the next) spends finding connectors' ways round the
// nodes; the rest is left to the frames after. Zero, the default, finds them all at once.
func (m *Map) SetRouteBudget(d time.Duration) { m.lay.budget = d }

// routeAround is the path of e between boxes a and b: the one the user shaped, or else the plainest that crosses
// no other node, when there is one within reach.
func (m *Map) routeAround(e *Edge, a, b Rect) edgeGeo {
	label := e.Label != ""
	g := route(e.Route, e.FromSide, e.ToSide, e.Bend, label, a, b)
	if e.Bend != (Vec{}) || e.FromSide != SideAuto && e.ToSide != SideAuto {
		return g // shaped by the user
	}
	hits := m.pathHits(e.Route, g.pts, e.From, e.To)
	if hits == 0 {
		return g
	}
	best, bestScore := g, score(e.Route, g, hits, true)
	stop := time.Now().Add(routeSearch)
	try := func(from, to Side, bend Vec) {
		if time.Now().After(stop) {
			return // the search has had its time: the best so far is kept
		}
		c := route(e.Route, from, to, bend, label, a, b)
		h := m.pathHits(e.Route, c.pts, e.From, e.To)
		if s := score(e.Route, c, h, from == g.from && to == g.to); s < bestScore {
			best, bestScore = c, s
		}
	}
	sides := []Side{SideTop, SideRight, SideBottom, SideLeft}
	for _, from := range sides {
		if e.FromSide != SideAuto && from != e.FromSide {
			continue
		}
		for _, to := range sides {
			if e.ToSide != SideAuto && to != e.ToSide {
				continue
			}
			try(from, to, Vec{})
		}
	}
	if bestScore < detourCost || hits >= hopeless {
		return best // clear; or so tangled among nodes that no detour would clear it
	}
	// Still in the way of something: detours to either side of the line between the ends, further and further.
	dx, dy := b.CX()-a.CX(), b.CY()-a.CY()
	l := math.Hypot(dx, dy)
	if l == 0 {
		return best
	}
	nx, ny := -dy/l, dx/l
	for _, d := range []float64{60, 120, 200, 320, 480, 720, 1080} {
		for _, sgn := range []float64{1, -1} {
			for _, from := range []Side{best.from, e.FromSide} {
				try(from, best.to, Vec{nx * d * sgn, ny * d * sgn})
			}
		}
		if bestScore < detourCost {
			break
		}
	}
	return best
}

// routeSearch is the longest one connector looks for its way round the nodes.
const routeSearch = time.Millisecond

// hopeless is how many nodes a plain path crosses for detours not to be tried: it runs through the thick of a
// map, where none would find a clear way.
const hopeless = 8

// detourCost is what one node in the way costs a path, against its length.
const detourCost = 1e6

// score ranks a path: each node it crosses outweighs any length, then shorter is better, and the sides that face
// each other are a little better than others of the same length.
func score(r Route, g edgeGeo, hits int, facingSides bool) float64 {
	line := polyline(r, g.pts, 24)
	l := 0.0
	for i := 1; i < len(line); i++ {
		l += math.Hypot(line[i].X-line[i-1].X, line[i].Y-line[i-1].Y)
	}
	if !facingSides {
		l += 60
	}
	return float64(hits)*detourCost + l
}

// pathHits counts the nodes, other than its ends, that the path crosses or comes within a few units of; it stops
// counting at eight. Each piece of the path descends only the subtrees it passes through, however long it is.
func (m *Map) pathHits(r Route, pts []Vec, from, to ID) int {
	const margin, most = 6, 8
	line := polyline(r, pts, 16)
	seen := map[ID]bool{}
	var walk func(n *Node, at Vec, p, q Vec)
	walk = func(n *Node, at Vec, p, q Vec) {
		if len(seen) >= most || !segmentCrosses(p, q, n.lay.sub.Add(at).Grow(margin)) {
			return
		}
		if n.ID != from && n.ID != to && !seen[n.ID] && segmentCrosses(p, q, R(0, 0, n.lay.w, n.lay.h).Add(at).Grow(margin)) {
			seen[n.ID] = true
		}
		seg := span(p, q).Grow(margin)
		for side, list := range n.lay.stacks {
			if len(list) == 0 {
				continue
			}
			dir := stackDir(n, side)
			lo, hi := seg.Y0-at.Y, seg.Y1-at.Y
			if !alongY(dir) {
				lo, hi = seg.X0-at.X, seg.X1-at.X
			}
			i, j := childRange(list, dir, lo, hi)
			for _, c := range list[i:j] {
				walk(c, at.Add(c.lay.rel), p, q)
			}
		}
	}
	for i := 1; i < len(line) && len(seen) < most; i++ {
		p, q := line[i-1], line[i]
		m.grid.query(span(p, q).Grow(margin), func(id uint32, _ Rect) {
			if root := m.Node(ID(id)); root != nil {
				walk(root, Vec{root.X, root.Y}, p, q)
			}
		})
	}
	return len(seen)
}

// segmentCrosses reports whether the segment p–q passes through the box b.
func segmentCrosses(p, q Vec, b Rect) bool {
	t0, t1 := 0.0, 1.0
	dx, dy := q.X-p.X, q.Y-p.Y
	for _, e := range [4][2]float64{{-dx, p.X - b.X0}, {dx, b.X1 - p.X}, {-dy, p.Y - b.Y0}, {dy, b.Y1 - p.Y}} {
		if e[0] == 0 {
			if e[1] < 0 {
				return false
			}
			continue
		}
		r := e[1] / e[0]
		if e[0] < 0 {
			t0 = math.Max(t0, r)
		} else {
			t1 = math.Min(t1, r)
		}
		if t0 > t1 {
			return false
		}
	}
	return true
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

// EdgeBase is where a connector's middle would be with no bend: a drag of its middle to p sets the bend to p
// less this.
func (m *Map) EdgeBase(e *Edge) (Vec, bool) {
	m.Layout()
	return e.geo.base, e.geo.ok
}

// EdgeShape is the shape a connector shows now: the sides it leaves and enters by and its bend, whether the user
// gave them or it found them round the nodes in its way. Given to the connector, they keep it as it is shown.
func (m *Map) EdgeShape(e *Edge) (from, to Side, bend Vec) {
	m.Layout()
	return e.geo.from, e.geo.to, e.geo.bend
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
