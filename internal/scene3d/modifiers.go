package scene3d

import (
	"math"
	"slices"
)

// Modifiers change how a mesh comes out without changing it: the mesh modelled stays the plain cage, and what shows is
// made from it again each time.
type Modifiers struct {
	Mirror    [3]bool // across the mesh's origin on each axis
	Subdivide int     // Catmull-Clark levels
}

// Any reports whether the modifiers change anything.
func (o Modifiers) Any() bool { return o.Mirror != [3]bool{} || o.Subdivide > 0 }

// Modified is the mesh with the modifiers applied, and the face of m each of its faces came from.
func (m *EditMesh) Modified(o Modifiers) (*EditMesh, []int32) {
	out := m.Clone()
	origin := make([]int32, len(out.Faces))
	for i := range origin {
		origin[i] = int32(i)
	}
	for axis, on := range o.Mirror {
		if on {
			origin = out.mirror(axis, origin)
		}
	}
	for range o.Subdivide {
		out, origin = out.catmullClark(origin)
		out.Flat = nil // smoothing is what it is for
	}
	return out, origin
}

// TrisToQuads joins pairs of triangles into quads, as a file's triangles lost the quads they were: flattest pairs first,
// and only those that turn less than limit degrees and make a quad with no corner bent far from square. It returns how
// many it joined. The diagonal has to have no seam, and the corners either side the same UVs.
func (m *EditMesh) TrisToQuads(limit float32) int {
	type pair struct {
		a, b  int32
		e     Edge
		score float32
	}
	cos := float32(math.Cos(float64(limit) * math.Pi / 180))
	var pairs []pair
	for e, faces := range m.EdgeFaces() {
		if len(faces) != 2 || m.Seams[e] {
			continue
		}
		a, b := faces[0], faces[1]
		if m.Faces[a].Count != 3 || m.Faces[b].Count != 3 || m.Faces[a].Material != m.Faces[b].Material {
			continue
		}
		d := m.FaceNormal(int(a)).Dot(m.FaceNormal(int(b)))
		if d < cos {
			continue
		}
		q, ok := m.joined(a, b, e)
		if !ok {
			continue
		}
		bend := float32(0)
		for i := range q {
			p, here, n := m.Pos[q[(i+3)%4].V], m.Pos[q[i].V], m.Pos[q[(i+1)%4].V]
			bend = max(bend, float32(math.Abs(float64(p.Sub(here).Norm().Dot(n.Sub(here).Norm())))))
		}
		if bend < .75 {
			pairs = append(pairs, pair{a, b, e, d - bend})
		}
	}
	slices.SortFunc(pairs, func(x, y pair) int {
		switch {
		case x.score > y.score:
			return -1
		case x.score < y.score:
			return 1
		case x.a != y.a:
			return int(x.a - y.a)
		}
		return int(x.b - y.b)
	})
	used := map[int32]bool{}
	done := 0
	for _, p := range pairs {
		if used[p.a] || used[p.b] {
			continue
		}
		used[p.a], used[p.b] = true, true
		q, _ := m.joined(p.a, p.b, p.e)
		quad := slices.Clone(q[:])
		m.addLike(quad, p.a)
		m.Faces[p.a].Count, m.Faces[p.b].Count = 0, 0
		done++
	}
	if done > 0 {
		m.Compact()
	}
	return done
}

// joined is the quad of two triangles sharing edge e, if their corners agree along it.
func (m *EditMesh) joined(a, b int32, e Edge) ([4]Corner, bool) {
	var out [4]Corner
	ca, cb := m.FaceCorners(int(a)), m.FaceCorners(int(b))
	i := slices.IndexFunc(ca, func(c Corner) bool { return c.V != e.A && c.V != e.B }) // the corner away from the edge
	k := slices.IndexFunc(cb, func(c Corner) bool { return c.V != e.A && c.V != e.B })
	if i < 0 || k < 0 {
		return out, false
	}
	first, second := ca[(i+1)%3], ca[(i+2)%3]
	at := func(v int32) Corner { return cb[slices.IndexFunc(cb, func(c Corner) bool { return c.V == v })] }
	if at(first.V).UV != first.UV || at(second.V).UV != second.UV {
		return out, false
	}
	return [4]Corner{ca[i], first, cb[k], second}, true
}

// mirror adds the mesh's reflection across the plane through the origin, welded where a vertex lies on the plane, and
// gives the origin of the faces after it.
func (m *EditMesh) mirror(axis int, origin []int32) []int32 {
	box := EmptyBox()
	for _, p := range m.Pos {
		box.Grow(p)
	}
	near := box.Size().Length() * 1e-4
	count := len(m.Pos)
	twin := make([]int32, count)
	for v := range count {
		p := m.Pos[v]
		if p[axis] >= -near && p[axis] <= near {
			twin[v] = int32(v)
			continue
		}
		p[axis] = -p[axis]
		twin[v] = m.addVertex(int32(v), p)
		for k := range m.Morphs {
			m.Morphs[k].Pos[twin[v]][axis] *= -1
		}
	}
	faces := len(m.Faces)
	for f := range faces {
		c := m.FaceCorners(f)
		if len(c) == 0 {
			continue
		}
		flipped := make([]Corner, len(c))
		for i := range c {
			k := c[len(c)-1-i]
			k.V, k.Loaded = twin[k.V], false
			k.Normal[axis] = -k.Normal[axis]
			flipped[i] = k
		}
		m.addLike(flipped, int32(f))
		origin = append(origin, origin[f])
	}
	return origin
}

func mixCorner(c []Corner, shares []float32) Corner {
	var out Corner
	out.Color = Vec4{}
	for i := range c {
		w := shares[i]
		out.Normal = out.Normal.Add(c[i].Normal.Scale(w))
		out.UV = Vec2{out.UV[0] + c[i].UV[0]*w, out.UV[1] + c[i].UV[1]*w}
		out.UV2 = Vec2{out.UV2[0] + c[i].UV2[0]*w, out.UV2[1] + c[i].UV2[1]*w}
		for k := range 4 {
			out.Color[k] += c[i].Color[k] * w
		}
	}
	return out
}

// catmullClark is the mesh one level smoother: every face becomes quads, one for each of its corners. Open edges keep their
// line, as they do in Blender. What a vertex is bound to it takes from the old vertex nearest.
func (m *EditMesh) catmullClark(origin []int32) (*EditMesh, []int32) {
	out := &EditMesh{}
	if m.Joints != nil {
		out.Joints, out.Weights = [][4]uint16{}, []Vec4{}
	}
	if m.Flat != nil {
		out.Flat = []bool{}
	}
	for k := range m.Morphs {
		out.Morphs = append(out.Morphs, Morph{})
		if m.Morphs[k].Normal != nil {
			out.Morphs[k].Normal = []Vec3{}
		}
	}
	add := func(p Vec3, from int32) int32 {
		out.Pos = append(out.Pos, p)
		if m.Joints != nil {
			out.Joints, out.Weights = append(out.Joints, m.Joints[from]), append(out.Weights, m.Weights[from])
		}
		for k := range m.Morphs {
			out.Morphs[k].Pos = append(out.Morphs[k].Pos, m.Morphs[k].Pos[from])
			if m.Morphs[k].Normal != nil {
				out.Morphs[k].Normal = append(out.Morphs[k].Normal, m.Morphs[k].Normal[from])
			}
		}
		return int32(len(out.Pos) - 1)
	}
	around := m.EdgeFaces()
	// The middle of each face.
	middle := make([]Vec3, len(m.Faces))
	for f := range m.Faces {
		c := m.FaceCorners(f)
		for _, k := range c {
			middle[f] = middle[f].Add(m.Pos[k.V])
		}
		if len(c) > 0 {
			middle[f] = middle[f].Scale(1 / float32(len(c)))
		}
	}
	// The new place of each old vertex, from the faces and edges around it.
	faceSum := make([]Vec3, len(m.Pos))
	faceCount := make([]float32, len(m.Pos))
	edgeSum := make([]Vec3, len(m.Pos))
	edgeCount := make([]float32, len(m.Pos))
	openSum := make([]Vec3, len(m.Pos))
	openCount := make([]float32, len(m.Pos))
	for f := range m.Faces {
		for _, k := range m.FaceCorners(f) {
			faceSum[k.V] = faceSum[k.V].Add(middle[f])
			faceCount[k.V]++
		}
	}
	for e, faces := range around {
		mid := m.Pos[e.A].Add(m.Pos[e.B]).Scale(.5)
		for _, v := range []int32{e.A, e.B} {
			edgeSum[v] = edgeSum[v].Add(mid)
			edgeCount[v]++
			if len(faces) == 1 {
				openSum[v] = openSum[v].Add(mid)
				openCount[v]++
			}
		}
	}
	vertices := make([]int32, len(m.Pos))
	for v := range m.Pos {
		p := m.Pos[v]
		switch {
		case openCount[v] > 0:
			p = p.Scale(.5).Add(openSum[v].Scale(.5 / openCount[v]))
		case faceCount[v] > 0:
			n := faceCount[v]
			p = faceSum[v].Scale(1 / n).Add(edgeSum[v].Scale(2 / edgeCount[v])).Add(p.Scale(n - 3)).Scale(1 / n)
		}
		vertices[v] = add(p, int32(v))
	}
	edges := make(map[Edge]int32, len(around))
	keys := make([]Edge, 0, len(around))
	for e := range around {
		keys = append(keys, e)
	}
	slices.SortFunc(keys, func(a, b Edge) int {
		if a.A != b.A {
			return int(a.A - b.A)
		}
		return int(a.B - b.B)
	})
	for _, e := range keys {
		faces := around[e]
		p := m.Pos[e.A].Add(m.Pos[e.B]).Scale(.5)
		if len(faces) == 2 {
			p = m.Pos[e.A].Add(m.Pos[e.B]).Add(middle[faces[0]]).Add(middle[faces[1]]).Scale(.25)
		}
		edges[e] = add(p, e.A)
	}
	if m.Seams != nil {
		out.Seams = map[Edge]bool{}
	}
	for e := range m.Seams {
		if mid, ok := edges[e]; ok {
			out.Seams[NewEdge(vertices[e.A], mid)] = true
			out.Seams[NewEdge(vertices[e.B], mid)] = true
		}
	}
	var next []int32
	for f := range m.Faces {
		c := m.FaceCorners(f)
		n := len(c)
		if n == 0 {
			continue
		}
		center := add(middle[f], c[0].V)
		even := make([]float32, n)
		for i := range even {
			even[i] = 1 / float32(n)
		}
		hub := mixCorner(c, even)
		hub.V = center
		for i := range c {
			before, after := c[(i+n-1)%n], c[(i+1)%n]
			a := mixCorner([]Corner{c[i], after}, []float32{.5, .5})
			b := mixCorner([]Corner{before, c[i]}, []float32{.5, .5})
			a.V, b.V = edges[NewEdge(c[i].V, after.V)], edges[NewEdge(before.V, c[i].V)]
			corner := c[i]
			corner.V, corner.Loaded = vertices[c[i].V], false
			out.AddFace([]Corner{corner, a, hub, b}, m.Faces[f].Material)
			if m.Flat != nil {
				out.Flat = append(out.Flat[:len(out.Faces)-1], m.Flat[f])
			}
			next = append(next, origin[f])
		}
	}
	return out, next
}

// DuplicateNode adds a copy of node beside it, with a mesh of its own, and gives its number. What hangs from the node is
// not copied.
func (a *Asset) DuplicateNode(node int) int {
	a.Nodes = slices.Clone(a.Nodes)
	n := a.Nodes[node]
	n.Children = nil
	if n.Name != "" {
		n.Name += " copy"
	}
	if n.Mesh >= 0 {
		a.Meshes = append(slices.Clone(a.Meshes), a.Meshes[n.Mesh])
		n.Mesh = len(a.Meshes) - 1
		a.Meshes[n.Mesh].Prims = slices.Clone(a.Meshes[n.Mesh].Prims)
	}
	at := len(a.Nodes)
	a.Nodes = append(a.Nodes, n)
	if p := n.Parent; p >= 0 {
		a.Nodes[p].Children = append(slices.Clone(a.Nodes[p].Children), at)
	} else {
		a.Roots = append(slices.Clone(a.Roots), at)
	}
	return at
}
