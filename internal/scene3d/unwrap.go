package scene3d

import (
	"math"
	"slices"
)

// Unwrap lays the mesh out along its seams: the faces joined without a seam make a chart, each is flattened keeping angles
// as well as it can (least squares conformal maps, as Blender's Unwrap does), and the charts are packed in the unit
// square, sized as they are on the mesh, with margin of it between them.
func (m *EditMesh) Unwrap(margin float32) {
	m.openClosedCharts(margin)
	charts := m.seamCharts()
	placed := make([]*chartLayout, 0, len(charts))
	for _, c := range charts {
		placed = append(placed, m.flatten(c))
	}
	packCharts(placed, margin)
	for _, c := range placed {
		c.write(m)
	}
}

// openClosedCharts cuts what has no edge to flatten from, such as a whole ball: a closed chart would collapse on a line.
// It gets the seams that a layout by angles has.
func (m *EditMesh) openClosedCharts(margin float32) {
	around := m.EdgeFaces()
	open := func(c *seamChart) bool {
		for _, f := range c.faces {
			corners := m.FaceCorners(int(f))
			for i := range corners {
				e := NewEdge(corners[i].V, corners[(i+1)%len(corners)].V)
				if m.Seams[e] || len(around[e]) != 2 {
					return true
				}
			}
		}
		return false
	}
	var closed []*seamChart
	for _, c := range m.seamCharts() {
		if !open(c) {
			closed = append(closed, c)
		}
	}
	if len(closed) == 0 {
		return
	}
	cut := m.Clone()
	cut.AutoUnwrap(66, margin)
	cut.DeriveSeams()
	if m.Seams == nil {
		m.Seams = map[Edge]bool{}
	}
	for _, c := range closed {
		for _, f := range c.faces {
			corners := m.FaceCorners(int(f))
			for i := range corners {
				if e := NewEdge(corners[i].V, corners[(i+1)%len(corners)].V); cut.Seams[e] {
					m.Seams[e] = true
				}
			}
		}
	}
}

// seamChart is faces joined without a seam, and for each of their corners the point of the chart it stands on: a vertex
// splits in two where a seam runs into a chart and stops.
type seamChart struct {
	faces  []int32
	points map[int32]int32 // from the corner's index in Corners
	count  int32
}

func (m *EditMesh) seamCharts() []*seamChart {
	parent := make([]int32, len(m.Corners))
	for i := range parent {
		parent[i] = int32(i)
	}
	var find func(int32) int32
	find = func(x int32) int32 {
		for parent[x] != x {
			parent[x] = parent[parent[x]]
			x = parent[x]
		}
		return x
	}
	join := func(a, b int32) { parent[find(a)] = find(b) }
	type side struct {
		face, from, to int32 // the corners at the edge's two vertices
	}
	edges := map[Edge][]side{}
	for f := range m.Faces {
		first, count := m.Faces[f].First, m.Faces[f].Count
		for i := int32(0); i < count; i++ {
			a, b := first+i, first+(i+1)%count
			e := NewEdge(m.Corners[a].V, m.Corners[b].V)
			if m.Corners[a].V != e.A {
				a, b = b, a
			}
			edges[e] = append(edges[e], side{int32(f), a, b})
		}
	}
	faceRoot := make([]int32, len(m.Faces))
	for f := range faceRoot {
		faceRoot[f] = int32(f)
	}
	var findFace func(int32) int32
	findFace = func(x int32) int32 {
		for faceRoot[x] != x {
			faceRoot[x] = faceRoot[faceRoot[x]]
			x = faceRoot[x]
		}
		return x
	}
	for e, sides := range edges {
		if m.Seams[e] || len(sides) != 2 {
			continue
		}
		join(sides[0].from, sides[1].from)
		join(sides[0].to, sides[1].to)
		faceRoot[findFace(sides[0].face)] = findFace(sides[1].face)
	}
	byRoot := map[int32]*seamChart{}
	var out []*seamChart
	for f := range m.Faces {
		if m.Faces[f].Count == 0 {
			continue
		}
		r := findFace(int32(f))
		c := byRoot[r]
		if c == nil {
			c = &seamChart{points: map[int32]int32{}}
			byRoot[r] = c
			out = append(out, c)
		}
		c.faces = append(c.faces, int32(f))
	}
	for _, c := range out {
		ids := map[int32]int32{}
		for _, f := range c.faces {
			first, count := m.Faces[f].First, m.Faces[f].Count
			for k := first; k < first+count; k++ {
				r := find(k)
				id, ok := ids[r]
				if !ok {
					id = c.count
					ids[r] = id
					c.count++
				}
				c.points[k] = id
			}
		}
	}
	return out
}

// chartLayout is a flattened chart on its way to the square.
type chartLayout struct {
	source *seamChart
	uv     []Vec2 // by point
	turned bool
	x, y   float32
	w, h   float32
	scale  float32
}

// flatten solves for the chart's points, pinning the two that lie farthest apart.
func (m *EditMesh) flatten(c *seamChart) *chartLayout {
	n := int(c.count)
	pos := make([]Vec3, n)
	var tris [][3]int32
	var split [][3]int32
	var area float64
	for _, f := range c.faces {
		first := m.Faces[f].First
		corners := m.FaceCorners(int(f))
		for i := range corners {
			pos[c.points[first+int32(i)]] = m.Pos[corners[i].V]
		}
		split = m.Triangles(int(f), split[:0])
		for _, t := range split {
			q := [3]int32{c.points[first+t[0]], c.points[first+t[1]], c.points[first+t[2]]}
			tris = append(tris, q)
			area += float64(pos[q[1]].Sub(pos[q[0]]).Cross(pos[q[2]].Sub(pos[q[0]])).Length()) / 2
		}
	}
	a, b := farthestPair(pos)
	uv := conformal(pos, tris, a, b)
	// A chart that came out mirrored would show its picture backwards.
	var signed, flat float64
	for _, t := range tris {
		p, q, r := uv[t[0]], uv[t[1]], uv[t[2]]
		cross := float64((q[0]-p[0])*(r[1]-p[1]) - (q[1]-p[1])*(r[0]-p[0]))
		signed += cross
		flat += math.Abs(cross) / 2
	}
	if signed < 0 {
		for i := range uv {
			uv[i][1] = -uv[i][1]
		}
	}
	scale := float32(1)
	if flat > 0 {
		scale = float32(math.Sqrt(area / flat))
	}
	box := EmptyBox()
	for i := range uv {
		uv[i] = Vec2{uv[i][0] * scale, uv[i][1] * scale}
		box.Grow(Vec3{uv[i][0], uv[i][1], 0})
	}
	if n > 0 {
		for i := range uv {
			uv[i] = Vec2{uv[i][0] - box.Min[0], uv[i][1] - box.Min[1]}
		}
	}
	out := &chartLayout{source: c, uv: uv, w: box.Max[0] - box.Min[0], h: box.Max[1] - box.Min[1]}
	if n == 0 {
		out.w, out.h = 0, 0
	}
	return out
}

func farthestPair(pos []Vec3) (int32, int32) {
	if len(pos) < 2 {
		return 0, 0
	}
	var a, b int32
	for range 3 { // walking to the farthest point and back finds a long span in a pass or two
		best := float32(-1)
		for i, p := range pos {
			if d := p.Sub(pos[a]).Length(); d > best {
				best, b = d, int32(i)
			}
		}
		a, b = b, a
	}
	return a, b
}

// conformal is the least squares conformal map of a chart: u and v of each point, with a at (0, 0) and b at (1, 0), by
// conjugate gradients on the normal equations.
func conformal(pos []Vec3, tris [][3]int32, a, b int32) []Vec2 {
	n := len(pos)
	type entry struct {
		at    int32 // 2 per point: u then v
		value float64
	}
	var rows [][]entry
	for _, t := range tris {
		p0, p1, p2 := pos[t[0]], pos[t[1]], pos[t[2]]
		e1 := p1.Sub(p0)
		x1 := float64(e1.Length())
		if x1 == 0 {
			continue
		}
		ex := e1.Norm()
		e2 := p2.Sub(p0)
		nv := e1.Cross(e2)
		if nv.Length() == 0 {
			continue
		}
		ey := nv.Norm().Cross(ex)
		x2, y2 := float64(e2.Dot(ex)), float64(e2.Dot(ey))
		root := math.Sqrt(x1 * y2)
		if root == 0 || math.IsNaN(root) {
			continue
		}
		// In the triangle's plane, with the first point at the origin and the second on the x axis.
		w := [3][2]float64{{x2 - x1, y2}, {-x2, -y2}, {x1, 0}}
		var re, im []entry
		for k := range 3 {
			at := t[k] * 2
			re = append(re, entry{at, w[k][0] / root}, entry{at + 1, -w[k][1] / root})
			im = append(im, entry{at, w[k][1] / root}, entry{at + 1, w[k][0] / root})
		}
		rows = append(rows, re, im)
	}
	x := make([]float64, n*2)
	for i, p := range pos {
		// Start from the longest flat view, which leaves little for the solver to do.
		x[i*2], x[i*2+1] = float64(p[0]), float64(p[1])
	}
	pinned := make([]bool, n*2)
	pinned[a*2], pinned[a*2+1], pinned[b*2], pinned[b*2+1] = true, true, true, true
	x[a*2], x[a*2+1], x[b*2], x[b*2+1] = 0, 0, 1, 0
	apply := func(v, out []float64) {
		clear(out)
		for _, r := range rows {
			var dot float64
			for _, e := range r {
				dot += e.value * v[e.at]
			}
			for _, e := range r {
				out[e.at] += e.value * dot
			}
		}
	}
	m := len(x)
	residual, step, turned, scaled := make([]float64, m), make([]float64, m), make([]float64, m), make([]float64, m)
	diag := make([]float64, m)
	for _, r := range rows {
		for _, e := range r {
			diag[e.at] += e.value * e.value
		}
	}
	apply(x, residual)
	for i := range residual {
		residual[i] = -residual[i]
		if pinned[i] {
			residual[i] = 0
		}
	}
	precondition := func() float64 {
		var rz float64
		for i := range residual {
			scaled[i] = 0
			if diag[i] > 0 {
				scaled[i] = residual[i] / diag[i]
			}
			rz += residual[i] * scaled[i]
		}
		return rz
	}
	rz := precondition()
	copy(step, scaled)
	first := rz
	for iteration := 0; iteration < 20*n+200 && rz > first*1e-14 && rz > 0; iteration++ {
		apply(step, turned)
		var pap float64
		for i := range turned {
			if pinned[i] {
				turned[i] = 0
			}
			pap += step[i] * turned[i]
		}
		if pap <= 0 {
			break
		}
		alpha := rz / pap
		for i := range x {
			x[i] += alpha * step[i]
			residual[i] -= alpha * turned[i]
		}
		next := precondition()
		beta := next / rz
		rz = next
		for i := range step {
			step[i] = scaled[i] + beta*step[i]
		}
	}
	out := make([]Vec2, n)
	for i := range out {
		out[i] = Vec2{float32(x[i*2]), float32(x[i*2+1])}
	}
	return out
}

// packCharts places the charts on shelves in the unit square, tall ones first.
func packCharts(charts []*chartLayout, margin float32) {
	if len(charts) == 0 {
		return
	}
	size := func(c *chartLayout) (float32, float32) {
		if c.turned {
			return c.h, c.w
		}
		return c.w, c.h
	}
	var area float64
	for _, c := range charts {
		c.turned = c.w < c.h
		area += float64((c.w + margin) * (c.h + margin))
	}
	order := slices.Clone(charts)
	slices.SortFunc(order, func(a, b *chartLayout) int {
		_, x := size(a)
		_, y := size(b)
		switch {
		case x > y:
			return -1
		case x < y:
			return 1
		}
		return 0
	})
	narrowest := float32(0)
	for _, c := range order {
		w, _ := size(c)
		narrowest = max(narrowest, w)
	}
	// However wide the shelves are, the whole has to fit a square: try a range and keep the one that needs the smallest.
	place := func(side float32) (float32, func()) {
		gap := float32(math.Sqrt(area)) * margin
		type spot struct{ x, y float32 }
		spots := make([]spot, len(order))
		var x, y, shelf, wide float32
		for i, c := range order {
			w, h := size(c)
			if x > 0 && x+w > side {
				x, y, shelf = 0, y+shelf+gap, 0
			}
			spots[i] = spot{x, y}
			x += w + gap
			shelf = max(shelf, h)
			wide = max(wide, x-gap)
		}
		whole := max(wide, y+shelf, 1e-6)
		return whole, func() {
			for i, c := range order {
				c.x, c.y, c.scale = spots[i].x, spots[i].y, 1/whole
			}
		}
	}
	root := float32(math.Sqrt(area))
	best, apply := place(max(root, narrowest))
	for step := 7; step <= 16; step++ {
		if whole, use := place(max(root*float32(step)/10, narrowest)); whole < best {
			best, apply = whole, use
		}
	}
	apply()
}

func (c *chartLayout) write(m *EditMesh) {
	for _, f := range c.source.faces {
		first := m.Faces[f].First
		corners := m.FaceCorners(int(f))
		for i := range corners {
			q := c.uv[c.source.points[first+int32(i)]]
			u, v := q[0], q[1]
			if c.turned {
				u, v = v, u
			}
			corners[i].UV = Vec2{(c.x + u) * c.scale, (c.y + v) * c.scale}
		}
	}
}

// DeriveSeams marks as seams the edges across which the corners' UVs part, which is all a file says of them.
func (m *EditMesh) DeriveSeams() {
	m.Seams = map[Edge]bool{}
	type end struct{ a, b Vec2 }
	seen := map[Edge]end{}
	for f := range m.Faces {
		corners := m.FaceCorners(f)
		for i := range corners {
			p, q := corners[i], corners[(i+1)%len(corners)]
			e := NewEdge(p.V, q.V)
			if p.V != e.A {
				p, q = q, p
			}
			here := end{p.UV, q.UV}
			if was, ok := seen[e]; !ok {
				seen[e] = here
			} else if was != here {
				m.Seams[e] = true
			}
		}
	}
}

// IslandOf is the faces joined to face f across edges where their UVs meet.
func (m *EditMesh) IslandOf(f int32) []int32 {
	type end struct {
		v  int32
		uv Vec2
	}
	around := map[end][]int32{}
	for g := range m.Faces {
		for _, c := range m.FaceCorners(g) {
			k := end{c.V, c.UV}
			around[k] = append(around[k], int32(g))
		}
	}
	seen := map[int32]bool{f: true}
	out := []int32{f}
	for at := 0; at < len(out); at++ {
		for _, c := range m.FaceCorners(int(out[at])) {
			for _, g := range around[end{c.V, c.UV}] {
				if !seen[g] {
					seen[g] = true
					out = append(out, g)
				}
			}
		}
	}
	return out
}

// TransformUV moves the corners of faces by a turn and scale about center, then along shift, in UV space.
func (m *EditMesh) TransformUV(faces []int32, center Vec2, angle, scale float32, shift Vec2) {
	s, c := math.Sincos(float64(angle))
	sin, cos := float32(s)*scale, float32(c)*scale
	for _, f := range faces {
		corners := m.FaceCorners(int(f))
		for i := range corners {
			x, y := corners[i].UV[0]-center[0], corners[i].UV[1]-center[1]
			corners[i].UV = Vec2{center[0] + x*cos - y*sin + shift[0], center[1] + x*sin + y*cos + shift[1]}
		}
	}
}
