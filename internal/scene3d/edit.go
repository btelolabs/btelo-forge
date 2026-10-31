package scene3d

import (
	"errors"
	"maps"
	"math"
	"slices"
)

// Corner is a face's use of a vertex: what can differ between faces meeting there, such as a seam in the texture.
type Corner struct {
	V       int32
	Normal  Vec3
	Tangent Vec4
	UV, UV2 Vec2
	Color   Vec4
	// Home is where the vertex was in the file, for a corner that came from one: while it stays there the file's normal
	// holds, which may have been set by hand.
	Home   Vec3
	Loaded bool
}

// Face is a polygon: Count corners from First in EditMesh.Corners, counter-clockwise from the outside.
type Face struct {
	First, Count int32
	Material     int32
}

// Edge is two vertices, the smaller first, so that an edge is the same seen from either face.
type Edge struct{ A, B int32 }

// NewEdge makes the edge of two vertices.
func NewEdge(a, b int32) Edge {
	if a > b {
		a, b = b, a
	}
	return Edge{a, b}
}

// EditMesh is a mesh as polygons, with vertices welded where the file split them for its seams: what the modelling
// tools work on. Faces with Count 0 have been deleted, until Compact.
type EditMesh struct {
	Pos     []Vec3
	Joints  [][4]uint16 // nil without a skin
	Weights []Vec4
	Corners []Corner
	Faces   []Face
	// Flat marks the faces shaded flat rather than as one surface with their neighbours; nil when none is.
	Flat []bool
	// Morphs are the mesh's shape keys, for each vertex here; a new vertex takes after the one it came from.
	Morphs []Morph
	// Seams are the edges a chart of the UV layout is cut along.
	Seams map[Edge]bool
}

// Clone is an independent copy, for undo.
func (m *EditMesh) Clone() *EditMesh {
	c := &EditMesh{Pos: slices.Clone(m.Pos), Joints: slices.Clone(m.Joints), Weights: slices.Clone(m.Weights), Corners: slices.Clone(m.Corners), Faces: slices.Clone(m.Faces), Flat: slices.Clone(m.Flat)}
	for i := range m.Morphs {
		c.Morphs = append(c.Morphs, Morph{slices.Clone(m.Morphs[i].Pos), slices.Clone(m.Morphs[i].Normal)})
	}
	c.Seams = maps.Clone(m.Seams)
	return c
}

// FaceCorners is the corners of face f, to read or change.
func (m *EditMesh) FaceCorners(f int) []Corner {
	return m.Corners[m.Faces[f].First : m.Faces[f].First+m.Faces[f].Count]
}

// AddFace appends a polygon and returns its index.
func (m *EditMesh) AddFace(corners []Corner, material int32) int {
	m.Faces = append(m.Faces, Face{First: int32(len(m.Corners)), Count: int32(len(corners)), Material: material})
	m.Corners = append(m.Corners, corners...)
	if m.Flat != nil {
		m.Flat = append(m.Flat, false)
	}
	return len(m.Faces) - 1
}

// addLike appends a polygon made from face from, with its material and shading.
func (m *EditMesh) addLike(corners []Corner, from int32) int {
	f := m.AddFace(corners, m.Faces[from].Material)
	if m.Flat != nil {
		m.Flat[f] = m.Flat[from]
	}
	return f
}

// addVertex appends a copy of vertex from, at p.
func (m *EditMesh) addVertex(from int32, p Vec3) int32 {
	m.Pos = append(m.Pos, p)
	if m.Joints != nil {
		m.Joints = append(m.Joints, m.Joints[from])
		m.Weights = append(m.Weights, m.Weights[from])
	}
	for i := range m.Morphs {
		k := &m.Morphs[i]
		k.Pos = append(k.Pos, k.Pos[from])
		if k.Normal != nil {
			k.Normal = append(k.Normal, k.Normal[from])
		}
	}
	return int32(len(m.Pos) - 1)
}

// weldKey is what must match for two file vertices to be one vertex.
type weldKey struct {
	p Vec3
	j [4]uint16
	w Vec4
	m uint64 // the shape keys: lips shut on one place still part
}

// morphHash tells apart the ways shape keys move vertex v of p.
func morphHash(p *Prim, v int) uint64 {
	h := uint64(14695981039346656037)
	for i := range p.Morphs {
		for _, f := range p.Morphs[i].Pos[v] {
			h = (h ^ uint64(math.Float32bits(f))) * 1099511628211
		}
	}
	return h
}

// EditMeshOf welds the parts of mesh into one polygon mesh of triangles. Materials are by part.
func EditMeshOf(mesh *Mesh) *EditMesh {
	m := &EditMesh{}
	skinned := false
	for i := range mesh.Prims {
		skinned = skinned || mesh.Prims[i].Joints != nil
	}
	seen := map[weldKey]int32{}
	if skinned {
		m.Joints, m.Weights = [][4]uint16{}, []Vec4{}
	}
	for k := range mesh.KeyCount() {
		m.Morphs = append(m.Morphs, Morph{})
		for i := range mesh.Prims {
			if k < len(mesh.Prims[i].Morphs) && mesh.Prims[i].Morphs[k].Normal != nil {
				m.Morphs[k].Normal = []Vec3{}
			}
		}
	}
	for i := range mesh.Prims {
		p := &mesh.Prims[i]
		index := make([]int32, len(p.Pos))
		for v := range p.Pos {
			k := weldKey{p: p.Pos[v], m: morphHash(p, v)}
			if p.Joints != nil {
				k.j, k.w = p.Joints[v], p.Weights[v]
			}
			at, ok := seen[k]
			if !ok {
				at = int32(len(m.Pos))
				seen[k] = at
				m.Pos = append(m.Pos, p.Pos[v])
				if skinned {
					m.Joints, m.Weights = append(m.Joints, k.j), append(m.Weights, k.w)
				}
				for i := range m.Morphs {
					to := &m.Morphs[i]
					var pos, normal Vec3
					if i < len(p.Morphs) {
						pos = p.Morphs[i].Pos[v]
						if p.Morphs[i].Normal != nil {
							normal = p.Morphs[i].Normal[v]
						}
					}
					to.Pos = append(to.Pos, pos)
					if to.Normal != nil {
						to.Normal = append(to.Normal, normal)
					}
				}
			}
			index[v] = at
		}
		corner := func(v uint32) Corner {
			c := Corner{V: index[v], Normal: p.Normal[v], Color: Vec4{1, 1, 1, 1}, Home: p.Pos[v], Loaded: true}
			if p.Tangent != nil {
				c.Tangent = p.Tangent[v]
			}
			if p.UV != nil {
				c.UV = p.UV[v]
			}
			if p.UV2 != nil {
				c.UV2 = p.UV2[v]
			}
			if p.Color != nil {
				c.Color = p.Color[v]
			}
			return c
		}
		for t := 0; t+2 < len(p.Indices); t += 3 {
			a, b, c := corner(p.Indices[t]), corner(p.Indices[t+1]), corner(p.Indices[t+2])
			if a.V != b.V && b.V != c.V && c.V != a.V { // a triangle with no area welds away
				m.AddFace([]Corner{a, b, c}, int32(p.Material))
			}
		}
	}
	return m
}

// Triangles appends the triangles of face f to out, as places in its corners.
func (m *EditMesh) Triangles(f int, out [][3]int32) [][3]int32 { return m.triangulate(f, out) }

// BoundaryLoop puts vertices in order around a hole, along the open edges between them; vertices that do not make one
// come back as they were.
func (m *EditMesh) BoundaryLoop(vertices []int32) []int32 {
	in := make(map[int32]bool, len(vertices))
	for _, v := range vertices {
		in[v] = true
	}
	next := map[int32]int32{}
	for e, faces := range m.EdgeFaces() {
		if len(faces) == 1 && in[e.A] && in[e.B] {
			c := m.FaceCorners(int(faces[0]))
			for i := range c {
				if c[i].V == e.A && c[(i+1)%len(c)].V == e.B {
					next[e.A] = e.B
				}
				if c[i].V == e.B && c[(i+1)%len(c)].V == e.A {
					next[e.B] = e.A
				}
			}
		}
	}
	if len(next) != len(vertices) {
		return vertices
	}
	loop := make([]int32, 0, len(vertices))
	at := vertices[0]
	for range vertices {
		loop = append(loop, at)
		at = next[at]
	}
	if at != vertices[0] {
		return vertices
	}
	return loop
}

// triangulate splits a polygon's corners into triangles, as indices into them: ear clipping, which holds for the concave
// polygons that insetting and cutting can make.
func (m *EditMesh) triangulate(f int, out [][3]int32) [][3]int32 {
	c := m.FaceCorners(f)
	n := len(c)
	if n == 3 {
		return append(out, [3]int32{0, 1, 2})
	}
	normal := Vec3{}
	for i := range n {
		normal = normal.Add(m.Pos[c[i].V].Cross(m.Pos[c[(i+1)%n].V]))
	}
	left := make([]int32, n)
	for i := range left {
		left[i] = int32(i)
	}
	for len(left) > 3 {
		clipped := false
		for i := range left {
			a, b, d := left[(i+len(left)-1)%len(left)], left[i], left[(i+1)%len(left)]
			pa, pb, pd := m.Pos[c[a].V], m.Pos[c[b].V], m.Pos[c[d].V]
			if pb.Sub(pa).Cross(pd.Sub(pb)).Dot(normal) <= 0 {
				continue // a reflex corner
			}
			inside := false
			for _, o := range left {
				if o != a && o != b && o != d && inTriangle(m.Pos[c[o].V], pa, pb, pd, normal) {
					inside = true
					break
				}
			}
			if inside {
				continue
			}
			out = append(out, [3]int32{a, b, d})
			left = slices.Delete(left, i, i+1)
			clipped = true
			break
		}
		if !clipped { // flat or twisted: a fan shows something rather than nothing
			for i := 1; i+1 < len(left); i++ {
				out = append(out, [3]int32{left[0], left[i], left[i+1]})
			}
			return out
		}
	}
	return append(out, [3]int32{left[0], left[1], left[2]})
}

func inTriangle(p, a, b, c, n Vec3) bool {
	return b.Sub(a).Cross(p.Sub(a)).Dot(n) >= 0 && c.Sub(b).Cross(p.Sub(b)).Dot(n) >= 0 && a.Sub(c).Cross(p.Sub(c)).Dot(n) >= 0
}

// FaceNormal is the unit normal of face f, by Newell's method, which holds for any polygon.
func (m *EditMesh) FaceNormal(f int) Vec3 {
	c := m.FaceCorners(f)
	var n Vec3
	for i := range c {
		n = n.Add(m.Pos[c[i].V].Cross(m.Pos[c[(i+1)%len(c)].V]))
	}
	return n.Norm()
}

// FaceCenter is the mean of face f's corners.
func (m *EditMesh) FaceCenter(f int) Vec3 {
	c := m.FaceCorners(f)
	var p Vec3
	for i := range c {
		p = p.Add(m.Pos[c[i].V])
	}
	return p.Scale(1 / float32(len(c)))
}

// moved marks the vertices whose normals no longer hold: those of every face with a corner that left its place in the
// file or never had one.
func (m *EditMesh) moved() []bool {
	out := make([]bool, len(m.Pos))
	for f := range m.Faces {
		c := m.FaceCorners(f)
		if slices.ContainsFunc(c, func(k Corner) bool { return !k.Loaded || m.Pos[k.V] != k.Home }) {
			for _, k := range c {
				out[k.V] = true
			}
		}
	}
	return out
}

// Bake turns the mesh back into a part for each material, with normals as the file had them where nothing moved and
// worked out from the faces elsewhere, and tells which face each triangle of each part came from. Faces come back as
// triangles, the only thing a mesh file holds.
func (m *EditMesh) Bake() ([]Prim, [][]int32) {
	normals := m.vertexNormals()
	moved := m.moved()
	byMaterial := map[int32]int{}
	var prims []Prim
	var faces [][]int32
	var tris [][3]int32
	// Corners alike share a vertex, as they did in the file.
	type shared struct {
		part    int
		v       int32
		normal  Vec3
		tangent Vec4
		uv, uv2 Vec2
		color   Vec4
	}
	seen := make(map[shared]uint32, len(m.Corners))
	var index []uint32
	for f := range m.Faces {
		if m.Faces[f].Count == 0 {
			continue
		}
		at, ok := byMaterial[m.Faces[f].Material]
		if !ok {
			at = len(prims)
			byMaterial[m.Faces[f].Material] = at
			prims = append(prims, Prim{Material: int(m.Faces[f].Material)})
			faces = append(faces, nil)
		}
		p := &prims[at]
		c := m.FaceCorners(f)
		index = index[:0]
		flat := m.Flat != nil && m.Flat[f]
		n := m.FaceNormal(f)
		for i := range c {
			v := c[i].V
			normal := c[i].Normal
			switch {
			case flat:
				normal = n
			case moved[v]:
				normal = normals[v]
			}
			k := shared{at, v, normal, c[i].Tangent, c[i].UV, c[i].UV2, c[i].Color}
			if done, ok := seen[k]; ok {
				index = append(index, done)
				continue
			}
			seen[k] = uint32(len(p.Pos))
			index = append(index, uint32(len(p.Pos)))
			p.Pos = append(p.Pos, m.Pos[v])
			p.Normal = append(p.Normal, normal)
			p.Tangent = append(p.Tangent, c[i].Tangent)
			p.UV = append(p.UV, c[i].UV)
			p.UV2 = append(p.UV2, c[i].UV2)
			p.Color = append(p.Color, c[i].Color)
			if m.Joints != nil {
				p.Joints, p.Weights = append(p.Joints, m.Joints[v]), append(p.Weights, m.Weights[v])
			}
			if len(m.Morphs) > 0 && p.Morphs == nil {
				p.Morphs = make([]Morph, len(m.Morphs))
			}
			for k := range m.Morphs {
				to, from := &p.Morphs[k], &m.Morphs[k]
				to.Pos = append(to.Pos, from.Pos[v])
				if from.Normal != nil {
					to.Normal = append(to.Normal, from.Normal[v])
				}
			}
		}
		tris = m.triangulate(f, tris[:0])
		for _, t := range tris {
			p.Indices = append(p.Indices, index[t[0]], index[t[1]], index[t[2]])
			faces[at] = append(faces[at], int32(f))
		}
	}
	return prims, faces
}

// vertexNormals is one normal for each vertex, the mean of its smooth faces' by their corner angle.
func (m *EditMesh) vertexNormals() []Vec3 {
	out := make([]Vec3, len(m.Pos))
	for f := range m.Faces {
		if m.Faces[f].Count == 0 || (m.Flat != nil && m.Flat[f]) {
			continue
		}
		n := m.FaceNormal(f)
		c := m.FaceCorners(f)
		for i := range c {
			p := m.Pos[c[i].V]
			a, b := m.Pos[c[(i+len(c)-1)%len(c)].V].Sub(p).Norm(), m.Pos[c[(i+1)%len(c)].V].Sub(p).Norm()
			angle := float32(math.Acos(float64(min(max(a.Dot(b), -1), 1))))
			out[c[i].V] = out[c[i].V].Add(n.Scale(angle))
		}
	}
	for i := range out {
		out[i] = out[i].Norm()
	}
	return out
}

// EdgeFaces is every edge with the faces around it, in order of use.
func (m *EditMesh) EdgeFaces() map[Edge][]int32 {
	out := make(map[Edge][]int32, len(m.Corners))
	for f := range m.Faces {
		c := m.FaceCorners(f)
		for i := range c {
			e := NewEdge(c[i].V, c[(i+1)%len(c)].V)
			out[e] = append(out[e], int32(f))
		}
	}
	return out
}

// Compact drops deleted faces and the vertices no face uses, returning where each old vertex went (-1 if gone) and each
// old face.
func (m *EditMesh) Compact() (vertices, faces []int32) {
	used := make([]bool, len(m.Pos))
	for f := range m.Faces {
		for _, c := range m.FaceCorners(f) {
			used[c.V] = true
		}
	}
	vertices = make([]int32, len(m.Pos))
	n := int32(0)
	for i, u := range used {
		vertices[i] = -1
		if u {
			vertices[i] = n
			m.Pos[n] = m.Pos[i]
			if m.Joints != nil {
				m.Joints[n], m.Weights[n] = m.Joints[i], m.Weights[i]
			}
			for k := range m.Morphs {
				m.Morphs[k].Pos[n] = m.Morphs[k].Pos[i]
				if m.Morphs[k].Normal != nil {
					m.Morphs[k].Normal[n] = m.Morphs[k].Normal[i]
				}
			}
			n++
		}
	}
	m.Pos = m.Pos[:n]
	if m.Joints != nil {
		m.Joints, m.Weights = m.Joints[:n], m.Weights[:n]
	}
	if m.Seams != nil {
		seams := map[Edge]bool{}
		for e := range m.Seams {
			if a, b := vertices[e.A], vertices[e.B]; a >= 0 && b >= 0 {
				seams[NewEdge(a, b)] = true
			}
		}
		m.Seams = seams
	}
	for k := range m.Morphs {
		m.Morphs[k].Pos = m.Morphs[k].Pos[:n]
		if m.Morphs[k].Normal != nil {
			m.Morphs[k].Normal = m.Morphs[k].Normal[:n]
		}
	}
	faces = make([]int32, len(m.Faces))
	corners := make([]Corner, 0, len(m.Corners))
	kept := m.Faces[:0]
	var flat []bool
	for f, face := range m.Faces {
		faces[f] = -1
		if face.Count == 0 {
			continue
		}
		faces[f] = int32(len(kept))
		first := int32(len(corners))
		for _, c := range m.Corners[face.First : face.First+face.Count] {
			c.V = vertices[c.V]
			corners = append(corners, c)
		}
		if m.Flat != nil {
			flat = append(flat, m.Flat[f])
		}
		kept = append(kept, Face{First: first, Count: face.Count, Material: face.Material})
	}
	m.Corners, m.Faces = corners, kept
	if m.Flat != nil {
		m.Flat = flat
	}
	return vertices, faces
}

// Move transforms the vertices chosen.
func (m *EditMesh) Move(vertices []int32, t Mat4) {
	for _, v := range vertices {
		m.Pos[v] = t.Point(m.Pos[v])
	}
}

// DeleteFaces removes faces, and vertices left with none.
func (m *EditMesh) DeleteFaces(faces []int32) {
	for _, f := range faces {
		m.Faces[f].Count = 0
	}
	m.Compact()
}

// DeleteVertices removes vertices with the faces that use them.
func (m *EditMesh) DeleteVertices(vertices []int32) {
	gone := make(map[int32]bool, len(vertices))
	for _, v := range vertices {
		gone[v] = true
	}
	for f := range m.Faces {
		if slices.ContainsFunc(m.FaceCorners(f), func(c Corner) bool { return gone[c.V] }) {
			m.Faces[f].Count = 0
		}
	}
	m.Compact()
}

// Merge makes the vertices chosen one, at their centre, dropping the edges and faces that collapse.
func (m *EditMesh) Merge(vertices []int32) error {
	if len(vertices) < 2 {
		return errors.New("choose at least two vertices")
	}
	var mid Vec3
	for _, v := range vertices {
		mid = mid.Add(m.Pos[v])
	}
	keep := vertices[0]
	m.Pos[keep] = mid.Scale(1 / float32(len(vertices)))
	merged := make(map[int32]bool, len(vertices))
	for _, v := range vertices {
		merged[v] = true
	}
	for f := range m.Faces {
		c := m.FaceCorners(f)
		out := c[:0]
		for i := range c {
			k := c[i]
			if merged[k.V] {
				k.V = keep
			}
			if len(out) == 0 || out[len(out)-1].V != k.V {
				out = append(out, k)
			}
		}
		if len(out) > 1 && out[0].V == out[len(out)-1].V {
			out = out[:len(out)-1]
		}
		if len(out) < 3 {
			out = out[:0]
		}
		m.Faces[f].Count = int32(len(out))
	}
	m.Compact()
	return nil
}

// FlipFaces turns the faces inside out.
func (m *EditMesh) FlipFaces(faces []int32) {
	for _, f := range faces {
		slices.Reverse(m.FaceCorners(int(f)))
	}
}

// SetFlat chooses flat or smooth shading for faces.
func (m *EditMesh) SetFlat(faces []int32, flat bool) {
	if m.Flat == nil {
		m.Flat = make([]bool, len(m.Faces))
	}
	for _, f := range faces {
		m.Flat[f] = flat
	}
}

// lerpCorner is the corner part way from a to b, on vertex v.
func lerpCorner(a, b Corner, t float32, v int32) Corner {
	mix := func(x, y float32) float32 { return x + (y-x)*t }
	return Corner{V: v, Normal: Vec3{mix(a.Normal[0], b.Normal[0]), mix(a.Normal[1], b.Normal[1]), mix(a.Normal[2], b.Normal[2])},
		Tangent: Vec4{mix(a.Tangent[0], b.Tangent[0]), mix(a.Tangent[1], b.Tangent[1]), mix(a.Tangent[2], b.Tangent[2]), a.Tangent[3]},
		UV:      Vec2{mix(a.UV[0], b.UV[0]), mix(a.UV[1], b.UV[1])}, UV2: Vec2{mix(a.UV2[0], b.UV2[0]), mix(a.UV2[1], b.UV2[1])},
		Color: Vec4{mix(a.Color[0], b.Color[0]), mix(a.Color[1], b.Color[1]), mix(a.Color[2], b.Color[2]), mix(a.Color[3], b.Color[3])}}
}

// midVertex is a new vertex on edge (a, b) at t from a, its skin blended as far as a weight list allows.
func (m *EditMesh) midVertex(a, b int32, t float32) int32 {
	p := m.Pos[a].Add(m.Pos[b].Sub(m.Pos[a]).Scale(t))
	from := a
	if t > .5 {
		from = b
	}
	return m.addVertex(from, p)
}

// Extrude pulls the faces out as one region: their vertices are doubled, the old ones staying behind, and walls join
// the two along the region's edge. It returns the vertices that move, and the faces of the region, as they are now.
func (m *EditMesh) Extrude(faces []int32) (moved []int32, region []int32) {
	inRegion := make(map[int32]bool, len(faces))
	for _, f := range faces {
		inRegion[f] = true
	}
	uses := map[Edge]int{}
	for _, f := range faces {
		c := m.FaceCorners(int(f))
		for i := range c {
			uses[NewEdge(c[i].V, c[(i+1)%len(c)].V)]++
		}
	}
	double := map[int32]int32{}
	dup := func(v int32) int32 {
		d, ok := double[v]
		if !ok {
			d = m.addVertex(v, m.Pos[v])
			double[v] = d
			moved = append(moved, d)
		}
		return d
	}
	type wall struct {
		a, b Corner
		from int32
	}
	var walls []wall
	for _, f := range faces {
		c := m.FaceCorners(int(f))
		for i := range c {
			a, b := c[i], c[(i+1)%len(c)]
			if uses[NewEdge(a.V, b.V)] == 1 {
				walls = append(walls, wall{a, b, f})
			}
		}
	}
	for _, f := range faces {
		c := m.FaceCorners(int(f))
		for i := range c {
			c[i].V = dup(c[i].V)
		}
	}
	for _, w := range walls {
		top, bottom := w.a, w.b
		top.V, bottom.V = double[w.a.V], double[w.b.V]
		m.addLike([]Corner{w.a, w.b, bottom, top}, w.from)
	}
	return moved, faces
}

// Inset shrinks each face into itself by share (0 to 1) of the way to its centre, with a ring of faces around it. It
// returns the inner faces.
func (m *EditMesh) Inset(faces []int32, share float32) []int32 {
	for _, f := range faces {
		center := m.FaceCenter(int(f))
		c := slices.Clone(m.FaceCorners(int(f)))
		inner := make([]Corner, len(c))
		for i := range c {
			p := m.Pos[c[i].V]
			v := m.addVertex(c[i].V, p.Add(center.Sub(p).Scale(share)))
			inner[i] = c[i]
			inner[i].V = v
		}
		copy(m.FaceCorners(int(f)), inner)
		for i := range c {
			j := (i + 1) % len(c)
			m.addLike([]Corner{c[i], c[j], inner[j], inner[i]}, f)
		}
	}
	return faces
}

// Subdivide splits each face into quads around its centre, adding the middle of every edge; neighbours that are not
// chosen get the new vertices on their edges too, so that no crack opens.
func (m *EditMesh) Subdivide(faces []int32) []int32 {
	mids := map[Edge]int32{}
	mid := func(a, b int32) int32 {
		e := NewEdge(a, b)
		v, ok := mids[e]
		if !ok {
			v = m.midVertex(e.A, e.B, .5)
			mids[e] = v
		}
		return v
	}
	chosen := make(map[int32]bool, len(faces))
	for _, f := range faces {
		chosen[f] = true
	}
	var result []int32
	count := len(m.Faces)
	for _, f := range faces {
		c := slices.Clone(m.FaceCorners(int(f)))
		center := m.FaceCenter(int(f))
		middle := m.addVertex(c[0].V, center)
		var hub Corner
		for i := range c {
			hub = lerpCorner(hub, c[i], 1/float32(i+1), middle)
		}
		m.Faces[f].Count = 0
		for i := range c {
			prev, next := c[(i+len(c)-1)%len(c)], c[(i+1)%len(c)]
			in := lerpCorner(prev, c[i], .5, mid(prev.V, c[i].V))
			out := lerpCorner(c[i], next, .5, mid(c[i].V, next.V))
			result = append(result, int32(m.addLike([]Corner{c[i], out, hub, in}, f)))
		}
	}
	// A neighbour takes the new vertex into its outline.
	for f := range count {
		if chosen[int32(f)] || m.Faces[f].Count == 0 {
			continue
		}
		c := m.FaceCorners(f)
		var out []Corner
		changed := false
		for i := range c {
			next := c[(i+1)%len(c)]
			out = append(out, c[i])
			if v, ok := mids[NewEdge(c[i].V, next.V)]; ok {
				out = append(out, lerpCorner(c[i], next, .5, v))
				changed = true
			}
		}
		if changed {
			m.Faces[f].Count = 0
			m.addLike(out, int32(f))
		}
	}
	return result
}

// LoopOf is the ring of edges across quads from e: through each quad to the edge opposite, both ways, until the ring
// closes or reaches something that is not a quad.
func (m *EditMesh) LoopOf(e Edge) []Edge {
	around := m.EdgeFaces()
	seen := map[Edge]bool{e: true}
	ring := []Edge{e}
	walk := func(start Edge) {
		at := start
		for {
			var next Edge
			found := false
			for _, f := range around[at] {
				c := m.FaceCorners(int(f))
				if len(c) != 4 {
					continue
				}
				for i := range c {
					if NewEdge(c[i].V, c[(i+1)%4].V) == at {
						if o := NewEdge(c[(i+2)%4].V, c[(i+3)%4].V); !seen[o] {
							next, found = o, true
						}
					}
				}
				if found {
					break
				}
			}
			if !found {
				return
			}
			seen[next] = true
			ring = append(ring, next)
			at = next
		}
	}
	walk(e)
	walk(e)
	return ring
}

// LoopCut splits the quads across the ring of edges from e, adding a line of vertices through them. It returns the new
// vertices.
func (m *EditMesh) LoopCut(e Edge) []int32 {
	ring := m.LoopOf(e)
	mids := make(map[Edge]int32, len(ring))
	var added []int32
	for _, r := range ring {
		v := m.midVertex(r.A, r.B, .5)
		mids[r] = v
		added = append(added, v)
	}
	for f := range m.Faces {
		if m.Faces[f].Count == 0 {
			continue
		}
		c := slices.Clone(m.FaceCorners(f))
		var hits []int
		for i := range c {
			if _, ok := mids[NewEdge(c[i].V, c[(i+1)%len(c)].V)]; ok {
				hits = append(hits, i)
			}
		}
		switch {
		case len(c) == 4 && len(hits) == 2 && hits[1]-hits[0] == 2:
			i := hits[0]
			a, b, d, g := c[i], c[(i+1)%4], c[(i+2)%4], c[(i+3)%4]
			ab := lerpCorner(a, b, .5, mids[NewEdge(a.V, b.V)])
			dg := lerpCorner(d, g, .5, mids[NewEdge(d.V, g.V)])
			m.Faces[f].Count = 0
			m.addLike([]Corner{a, ab, dg, g}, int32(f))
			m.addLike([]Corner{ab, b, d, dg}, int32(f))
		case len(hits) > 0: // the ring ends here: its vertex joins the outline, leaving no crack
			var out []Corner
			for i := range c {
				next := c[(i+1)%len(c)]
				out = append(out, c[i])
				if v, ok := mids[NewEdge(c[i].V, next.V)]; ok {
					out = append(out, lerpCorner(c[i], next, .5, v))
				}
			}
			m.Faces[f].Count = 0
			m.addLike(out, int32(f))
		}
	}
	return added
}

// Fill makes a face of the vertices chosen, in the order given, facing the side that agrees with what is next to it.
func (m *EditMesh) Fill(vertices []int32) (int, error) {
	if len(vertices) < 3 {
		return 0, errors.New("choose at least three vertices")
	}
	around := m.EdgeFaces()
	corners := make([]Corner, len(vertices))
	flip := false
	for i, v := range vertices {
		next := vertices[(i+1)%len(vertices)]
		corners[i] = Corner{V: v, Color: Vec4{1, 1, 1, 1}}
		// A neighbour runs along a shared edge the other way round.
		for _, f := range around[NewEdge(v, next)] {
			c := m.FaceCorners(int(f))
			for k := range c {
				if c[k].V == v && c[(k+1)%len(c)].V == next {
					flip = true
				}
			}
		}
	}
	if flip {
		slices.Reverse(corners)
	}
	material := int32(-1)
	if len(m.Faces) > 0 {
		material = m.Faces[0].Material
	}
	return m.AddFace(corners, material), nil
}

// Bevel cuts a flat strip of the given width along each edge that has two faces. Near a beveled edge's ends each face's
// corner slides: along the face's other edge, or into the face where two beveled edges meet in it. A face that only
// touches the end gets the slid points in its outline, so no crack opens; what is left around a vertex of many edges is
// closed with a face. It returns the strips.
func (m *EditMesh) Bevel(edges []Edge, width float32) []int32 {
	around := m.EdgeFaces()
	chosen := make(map[Edge]bool, len(edges))
	touched := map[int32]bool{}
	for _, e := range edges {
		if len(around[e]) == 2 {
			chosen[e] = true
			touched[e.A], touched[e.B] = true, true
		}
	}
	if len(chosen) == 0 {
		return nil
	}
	// The points on the edges beside beveled ones, one for each end.
	type end struct {
		e Edge
		v int32
	}
	slid := map[end]float32{}
	for f := range m.Faces {
		c := m.FaceCorners(f)
		for i := range c {
			if !touched[c[i].V] {
				continue
			}
			p, n := NewEdge(c[(i+len(c)-1)%len(c)].V, c[i].V), NewEdge(c[i].V, c[(i+1)%len(c)].V)
			if chosen[p] != chosen[n] {
				e := p
				if chosen[p] {
					e = n
				}
				length := m.Pos[e.A].Sub(m.Pos[e.B]).Length()
				slid[end{e, c[i].V}] = min(width/max(length, 1e-6), .45)
			}
		}
	}
	points := make(map[end]int32, len(slid))
	point := func(e Edge, v int32) (int32, float32) {
		t := slid[end{e, v}]
		at, ok := points[end{e, v}]
		if !ok {
			other := e.A
			if other == v {
				other = e.B
			}
			at = m.midVertex(v, other, t)
			points[end{e, v}] = at
		}
		return at, t
	}
	type place struct {
		face int32
		v    int32
	}
	moved := map[place]Corner{}
	type side struct {
		face int32
		a, b int32 // the edge's ends, in the face's direction
	}
	sides := map[Edge][]side{}
	count := len(m.Faces)
	for f := range count {
		c := slices.Clone(m.FaceCorners(f))
		if !slices.ContainsFunc(c, func(k Corner) bool { return touched[k.V] }) {
			continue
		}
		out := make([]Corner, 0, len(c)+2)
		for i := range c {
			prev, cur, next := c[(i+len(c)-1)%len(c)], c[i], c[(i+1)%len(c)]
			p, n := NewEdge(prev.V, cur.V), NewEdge(cur.V, next.V)
			if chosen[n] {
				sides[n] = append(sides[n], side{int32(f), cur.V, next.V})
			}
			if !touched[cur.V] {
				out = append(out, cur)
				continue
			}
			_, onPrev := slid[end{p, cur.V}]
			_, onNext := slid[end{n, cur.V}]
			switch {
			case chosen[p] && chosen[n]:
				k := cur
				k.V = m.addVertex(cur.V, m.Pos[cur.V].Add(m.Pos[prev.V].Sub(m.Pos[cur.V]).Norm().Add(m.Pos[next.V].Sub(m.Pos[cur.V]).Norm()).Scale(width)))
				moved[place{int32(f), cur.V}] = k
				out = append(out, k)
			case chosen[p]:
				at, t := point(n, cur.V)
				k := lerpCorner(cur, next, t, at)
				moved[place{int32(f), cur.V}] = k
				out = append(out, k)
			case chosen[n]:
				at, t := point(p, cur.V)
				k := lerpCorner(cur, prev, t, at)
				moved[place{int32(f), cur.V}] = k
				out = append(out, k)
			case onPrev || onNext:
				if onPrev {
					at, t := point(p, cur.V)
					out = append(out, lerpCorner(cur, prev, t, at))
				}
				if onNext {
					at, t := point(n, cur.V)
					out = append(out, lerpCorner(cur, next, t, at))
				}
			default:
				out = append(out, cur)
			}
		}
		m.Faces[f].Count = 0
		m.addLike(out, int32(f))
	}
	var strips []int32
	for _, e := range edges {
		s := sides[e]
		if !chosen[e] || len(s) != 2 {
			continue
		}
		x, y := s[0], s[1]
		strips = append(strips, int32(m.addLike([]Corner{moved[place{x.face, x.b}], moved[place{x.face, x.a}], moved[place{y.face, y.b}], moved[place{y.face, y.a}]}, x.face)))
	}
	m.closeHoles()
	_, faces := m.Compact()
	out := strips[:0]
	for _, f := range strips {
		out = append(out, faces[f])
	}
	return out
}

// closeHoles fills the small loops of open edges a bevel leaves around a vertex.
func (m *EditMesh) closeHoles() {
	directed := make(map[[2]int32]bool, len(m.Corners))
	material := int32(-1)
	for f := range m.Faces {
		c := m.FaceCorners(f)
		for i := range c {
			directed[[2]int32{c[i].V, c[(i+1)%len(c)].V}] = true
			material = m.Faces[f].Material
		}
	}
	next := map[int32]int32{}
	for e := range directed {
		if !directed[[2]int32{e[1], e[0]}] {
			next[e[1]] = e[0] // a hole runs against its neighbours
		}
	}
	for len(next) > 0 {
		var start int32
		for k := range next {
			start = k
			break
		}
		loop := []int32{start}
		closedLoop := false
		for at := start; ; {
			to, ok := next[at]
			if !ok {
				break
			}
			delete(next, at)
			if to == start {
				closedLoop = true
				break
			}
			loop = append(loop, to)
			at = to
		}
		if closedLoop && len(loop) <= 8 {
			corners := make([]Corner, len(loop))
			for i, v := range loop {
				corners[i] = Corner{V: v, Color: Vec4{1, 1, 1, 1}}
			}
			m.AddFace(corners, material)
		}
	}
}

// Primitive is a shape to start from.
type Primitive int

const (
	Cube Primitive = iota
	Plane
	Sphere
	Cylinder
)

// NewPrimitive makes a shape of radius 1 standing around the origin, of quads where it can.
func NewPrimitive(p Primitive) *EditMesh {
	m := &EditMesh{}
	corner := func(v int32, u, t float32) Corner { return Corner{V: v, UV: Vec2{u, t}, Color: Vec4{1, 1, 1, 1}} }
	quad := func(a, b, c, d int32) {
		m.AddFace([]Corner{corner(a, 0, 1), corner(b, 1, 1), corner(c, 1, 0), corner(d, 0, 0)}, -1)
	}
	switch p {
	case Cube:
		for i := range 8 {
			m.Pos = append(m.Pos, Vec3{float32(i&1)*2 - 1, float32(i>>1&1)*2 - 1, float32(i>>2&1)*2 - 1})
		}
		quad(4, 5, 7, 6)
		quad(1, 0, 2, 3)
		quad(5, 1, 3, 7)
		quad(0, 4, 6, 2)
		quad(2, 6, 7, 3)
		quad(0, 1, 5, 4)
		m.SetFlat([]int32{0, 1, 2, 3, 4, 5}, true)
	case Plane:
		m.Pos = []Vec3{{-1, 0, 1}, {1, 0, 1}, {1, 0, -1}, {-1, 0, -1}}
		quad(0, 1, 2, 3)
	case Sphere:
		const rings, sides = 12, 24
		m.Pos = append(m.Pos, Vec3{0, 1, 0})
		for y := 1; y < rings; y++ {
			s, c := math.Sincos(float64(y) / rings * math.Pi)
			for x := range sides {
				sx, cx := math.Sincos(float64(x) / sides * 2 * math.Pi)
				m.Pos = append(m.Pos, Vec3{float32(s * sx), float32(c), float32(s * cx)})
			}
		}
		m.Pos = append(m.Pos, Vec3{0, -1, 0})
		at := func(ring, x int) int32 { return int32(1 + (ring-1)*sides + x%sides) }
		for x := range sides {
			u0, u1 := float32(x)/sides, float32(x+1)/sides
			m.AddFace([]Corner{corner(0, u0, 0), corner(at(1, x), u0, 1.0/rings), corner(at(1, x+1), u1, 1.0/rings)}, -1)
			for r := 1; r < rings-1; r++ {
				v0, v1 := float32(r)/rings, float32(r+1)/rings
				m.AddFace([]Corner{corner(at(r, x), u0, v0), corner(at(r+1, x), u0, v1), corner(at(r+1, x+1), u1, v1), corner(at(r, x+1), u1, v0)}, -1)
			}
			m.AddFace([]Corner{corner(at(rings-1, x), u0, 1-1.0/rings), corner(int32(len(m.Pos)-1), u0, 1), corner(at(rings-1, x+1), u1, 1-1.0/rings)}, -1)
		}
	case Cylinder:
		const sides = 24
		for x := range sides {
			s, c := math.Sincos(float64(x) / sides * 2 * math.Pi)
			m.Pos = append(m.Pos, Vec3{float32(s), 1, float32(c)}, Vec3{float32(s), -1, float32(c)})
		}
		top, bottom := make([]Corner, sides), make([]Corner, sides)
		for x := range sides {
			a, b := int32(x*2), int32((x+1)%sides*2)
			u0, u1 := float32(x)/sides, float32(x+1)/sides
			m.AddFace([]Corner{corner(a, u0, 0), corner(a+1, u0, 1), corner(b+1, u1, 1), corner(b, u1, 0)}, -1)
			s, c := math.Sincos(float64(x) / sides * 2 * math.Pi)
			top[x] = corner(a, float32(s)*.5+.5, float32(c)*.5+.5)
			bottom[sides-1-x] = corner(a+1, float32(s)*.5+.5, float32(c)*.5+.5)
		}
		caps := m.AddFace(top, -1)
		m.AddFace(bottom, -1)
		m.SetFlat([]int32{int32(caps), int32(caps + 1)}, true)
	}
	return m
}
