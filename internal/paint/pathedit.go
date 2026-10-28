package paint

import (
	"image"
	"math"
)

// Editing a curve's nodes, as Affinity Designer's node tool does. Each edit returns a new object; the one given is
// never changed.

// NodeRef names a node of an object: its subpath and its place in it.
type NodeRef struct{ Sub, Node int }

// ToCurve is the object as a curve with the same outline, as Layer › Convert to Curves makes a smart shape editable
// node by node. A curve is returned as it is.
func (v Vector) ToCurve() Vector {
	if v.Kind == VectorCurve {
		return v
	}
	out := v
	out.Kind, out.Path = VectorCurve, cloneSubpaths(v.Outline())
	out.Size, out.Radius, out.Sides, out.Inner = Pt{}, 0, 0, 0
	return out
}

func cloneSubpaths(in []Subpath) []Subpath {
	out := make([]Subpath, len(in))
	for i, s := range in {
		out[i] = Subpath{Nodes: append([]PathNode(nil), s.Nodes...), Closed: s.Closed}
	}
	return out
}

// node is the node at r, and whether there is one.
func (v Vector) node(r NodeRef) (PathNode, bool) {
	if r.Sub < 0 || r.Sub >= len(v.Path) || r.Node < 0 || r.Node >= len(v.Path[r.Sub].Nodes) {
		return PathNode{}, false
	}
	return v.Path[r.Sub].Nodes[r.Node], true
}

// WithNode is the object with node r replaced by n.
func (v Vector) WithNode(r NodeRef, n PathNode) Vector {
	res := v.ToCurve()
	res.Path = cloneSubpaths(res.Path)
	if _, ok := res.node(r); ok {
		res.Path[r.Sub].Nodes[r.Node] = n
	}
	return res
}

// AppendNode is the object with n added at the end of subpath sub, as the pen adds each node.
func (v Vector) AppendNode(sub int, n PathNode) Vector {
	res := v.ToCurve()
	res.Path = cloneSubpaths(res.Path)
	if sub >= 0 && sub < len(res.Path) {
		res.Path[sub].Nodes = append(res.Path[sub].Nodes, n)
	}
	return res
}

// Close is the object with subpath sub closed, as the pen closes a curve on its first node.
func (v Vector) Close(sub int) Vector {
	res := v.ToCurve()
	res.Path = cloneSubpaths(res.Path)
	if sub >= 0 && sub < len(res.Path) {
		res.Path[sub].Closed = true
	}
	return res
}

// MoveNodes moves nodes, with their handles, by d in the object's own space.
func (v Vector) MoveNodes(refs []NodeRef, d Pt) Vector {
	out := v.ToCurve()
	out.Path = cloneSubpaths(out.Path)
	for _, r := range refs {
		if n, ok := out.node(r); ok {
			n.P, n.In, n.Out = n.P.Add(d), n.In.Add(d), n.Out.Add(d)
			out.Path[r.Sub].Nodes[r.Node] = n
		}
	}
	return out
}

// MoveHandle puts a node's handle (out when out is true, else in) at p, in the object's own space. A smooth node
// keeps its other handle in line, at its own length, unless breaking, which makes the node sharp, as dragging with
// Alt does.
func (v Vector) MoveHandle(r NodeRef, out bool, p Pt, breaking bool) Vector {
	res := v.ToCurve()
	res.Path = cloneSubpaths(res.Path)
	n, ok := res.node(r)
	if !ok {
		return res
	}
	if out {
		n.Out = p
	} else {
		n.In = p
	}
	if breaking {
		n.Smooth = false
	}
	if n.Smooth {
		moved, other := n.Out, &n.In
		if !out {
			moved, other = n.In, &n.Out
		}
		dir := n.P.Sub(moved).Unit()
		length := other.Sub(n.P).Len()
		if length == 0 {
			length = moved.Sub(n.P).Len() // a node without the other handle gets a matching one
		}
		*other = n.P.Add(dir.Mul(length))
	}
	res.Path[r.Sub].Nodes[r.Node] = n
	return res
}

// SetSmooth makes a node smooth, with handles in line along the direction its neighbours give, or sharp, keeping
// its handles where they are.
func (v Vector) SetSmooth(r NodeRef, smooth bool) Vector {
	res := v.ToCurve()
	res.Path = cloneSubpaths(res.Path)
	n, ok := res.node(r)
	if !ok {
		return res
	}
	n.Smooth = smooth
	if smooth {
		s := res.Path[r.Sub]
		prev, next := neighbours(s, r.Node)
		dir := next.Sub(prev).Unit()
		if dir == (Pt{}) {
			dir = Pt{1, 0}
		}
		// Handles a third of the way to the neighbours, as Affinity gives a node made smooth.
		in, outLen := n.P.Dist(prev)/3, n.P.Dist(next)/3
		n.In, n.Out = n.P.Sub(dir.Mul(in)), n.P.Add(dir.Mul(outLen))
	}
	res.Path[r.Sub].Nodes[r.Node] = n
	return res
}

// neighbours are the points before and after node i of a subpath, the node itself at an open end.
func neighbours(s Subpath, i int) (prev, next Pt) {
	n := len(s.Nodes)
	prev, next = s.Nodes[i].P, s.Nodes[i].P
	switch {
	case i > 0:
		prev = s.Nodes[i-1].P
	case s.Closed && n > 1:
		prev = s.Nodes[n-1].P
	}
	switch {
	case i < n-1:
		next = s.Nodes[i+1].P
	case s.Closed && n > 1:
		next = s.Nodes[0].P
	}
	return prev, next
}

// segment is the cubic from node i of a subpath to the next.
func segment(s Subpath, i int) (p0, c1, c2, p3 Pt) {
	a, b := s.Nodes[i], s.Nodes[(i+1)%len(s.Nodes)]
	return a.P, a.Out, b.In, b.P
}

// Segments is how many segments a subpath has: one after each node, but none after an open one's last.
func (s Subpath) Segments() int { return segments(s) }

func segments(s Subpath) int {
	switch {
	case len(s.Nodes) < 2:
		return 0
	case s.Closed:
		return len(s.Nodes)
	}
	return len(s.Nodes) - 1
}

// InsertNode splits the segment after node r at t (0 to 1), adding a smooth node there without changing the
// outline, as a click on a segment with the node tool does. It returns the new node.
func (v Vector) InsertNode(r NodeRef, t float64) (Vector, NodeRef) {
	res := v.ToCurve()
	res.Path = cloneSubpaths(res.Path)
	if r.Sub < 0 || r.Sub >= len(res.Path) || r.Node < 0 || r.Node >= segments(res.Path[r.Sub]) {
		return res, r
	}
	s := res.Path[r.Sub]
	j := (r.Node + 1) % len(s.Nodes)
	p0, c1, c2, p3 := segment(s, r.Node)
	a, b := splitCubic(p0, c1, c2, p3, min(max(t, 0), 1))
	s.Nodes[r.Node].Out = a[1]
	s.Nodes[j].In = b[2]
	mid := PathNode{P: a[3], In: a[2], Out: b[1], Smooth: c1 != p0 || c2 != p3}
	at := r.Node + 1
	s.Nodes = append(s.Nodes[:at], append([]PathNode{mid}, s.Nodes[at:]...)...)
	res.Path[r.Sub] = s
	return res, NodeRef{r.Sub, at}
}

// DeleteNodes removes nodes; a subpath left with fewer than two goes, as Delete with the node tool does.
func (v Vector) DeleteNodes(refs []NodeRef) Vector {
	res := v.ToCurve()
	gone := map[NodeRef]bool{}
	for _, r := range refs {
		gone[r] = true
	}
	var paths []Subpath
	for si, s := range res.Path {
		var kept []PathNode
		for ni, n := range s.Nodes {
			if !gone[NodeRef{si, ni}] {
				kept = append(kept, n)
			}
		}
		if len(kept) >= 2 {
			paths = append(paths, Subpath{Nodes: kept, Closed: s.Closed})
		}
	}
	res.Path = paths
	return res
}

// NearestOnSegment is where a curve passes closest to p (in the object's own space): the segment after node r,
// the place t along it and the distance, as hit-testing a click on an outline needs.
func (v Vector) NearestOnSegment(p Pt) (r NodeRef, t, dist float64) {
	dist = math.Inf(1)
	for si, s := range v.Outline() {
		for i := range segments(s) {
			p0, c1, c2, p3 := segment(s, i)
			// Coarse samples, then a finer look around the best.
			const n = 32
			best, bt := math.Inf(1), 0.0
			for k := 0; k <= n; k++ {
				tt := float64(k) / n
				if d := cubicAt(p0, c1, c2, p3, tt).Dist(p); d < best {
					best, bt = d, tt
				}
			}
			for step := 1.0 / n; step > 1e-5; step /= 2 {
				for _, tt := range []float64{bt - step, bt + step} {
					if tt < 0 || tt > 1 {
						continue
					}
					if d := cubicAt(p0, c1, c2, p3, tt).Dist(p); d < best {
						best, bt = d, tt
					}
				}
			}
			if best < dist {
				dist, t, r = best, bt, NodeRef{si, i}
			}
		}
	}
	return r, t, dist
}

// PointAt is the point t (0 to 1) along the segment after node r of the object's outline, in its own space.
func (v Vector) PointAt(r NodeRef, t float64) Pt {
	out := v.Outline()
	if r.Sub < 0 || r.Sub >= len(out) || r.Node < 0 || r.Node >= segments(out[r.Sub]) {
		return Pt{}
	}
	p0, c1, c2, p3 := segment(out[r.Sub], r.Node)
	return cubicAt(p0, c1, c2, p3, t)
}

// VectorAt is the topmost shown vector layer at canvas point p: one that paints there, or whose outline passes
// within slack canvas pixels, as a click with the node tool picks an object. It is 0 when there is none.
func (d *Doc) VectorAt(p Pt, slack float64) int {
	var found int
	var walk func(g *Node) bool
	walk = func(g *Node) bool {
		for _, n := range g.Children {
			if !n.Visible {
				continue
			}
			if n.IsGroup() {
				if walk(n) {
					return true
				}
				continue
			}
			if n.Vector != nil && vectorHit(n.Vector, p, slack) {
				found = n.ID
				return true
			}
		}
		return false
	}
	walk(d.Root)
	return found
}

// vectorHit reports whether an object paints at p or its outline passes within slack of it.
func vectorHit(v *Vector, p Pt, slack float64) bool {
	px := image.Pt(int(math.Floor(p.X)), int(math.Floor(p.Y)))
	if !px.In(v.PixelBounds().Inset(-int(math.Ceil(slack)))) {
		return false
	}
	dot := image.NewRGBA(image.Rectangle{Min: px, Max: px.Add(image.Pt(1, 1))})
	v.Render(dot, dot.Rect, 1)
	if dot.Pix[3] > 0 {
		return true
	}
	inv, ok := v.M.Inverse()
	if !ok {
		return false
	}
	r, t, _ := v.NearestOnSegment(inv.Map(p))
	return v.M.Map(v.PointAt(r, t)).Dist(p) <= slack
}
