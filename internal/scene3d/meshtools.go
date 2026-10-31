package scene3d

import (
	"fmt"
	"math"
	"slices"
)

// EdgeLoop is the line of edges through start: at each end it goes on straight across a vertex where four edges meet, until
// it comes round or stops.
func (m *EditMesh) EdgeLoop(start Edge) []Edge {
	around := m.EdgeFaces()
	atVertex := map[int32][]Edge{}
	for e := range around {
		atVertex[e.A] = append(atVertex[e.A], e)
		atVertex[e.B] = append(atVertex[e.B], e)
	}
	seen := map[Edge]bool{start: true}
	out := []Edge{start}
	shareFace := func(a, b Edge) bool {
		return slices.ContainsFunc(around[a], func(f int32) bool { return slices.Contains(around[b], f) })
	}
	walk := func(from int32) {
		e := start
		v := from
		for {
			edges := atVertex[v]
			if len(edges) != 4 {
				return
			}
			next := -1
			for i, o := range edges {
				if o != e && !shareFace(o, e) {
					next = i
				}
			}
			if next < 0 || seen[edges[next]] {
				return
			}
			e = edges[next]
			seen[e] = true
			out = append(out, e)
			if e.A == v {
				v = e.B
			} else {
				v = e.A
			}
		}
	}
	walk(start.A)
	walk(start.B)
	return out
}

// MoveSoft moves the vertices by t, and those around them within radius by a share of it that fades with the distance
// to the nearest, as Blender's proportional editing does.
func (m *EditMesh) MoveSoft(vertices []int32, t Mat4, radius float32) {
	chosen := make(map[int32]bool, len(vertices))
	places := make([]Vec3, len(vertices))
	for i, v := range vertices {
		chosen[v] = true
		places[i] = m.Pos[v]
	}
	for v := range m.Pos {
		p := m.Pos[v]
		if chosen[int32(v)] {
			m.Pos[v] = t.Point(p)
			continue
		}
		near := float32(math.Inf(1))
		for _, c := range places {
			near = min(near, p.Sub(c).Length())
		}
		if near >= radius {
			continue
		}
		x := 1 - near/radius
		share := x * x * (3 - 2*x)
		m.Pos[v] = p.Add(t.Point(p).Sub(p).Scale(share))
	}
}

// ConnectVertices splits the face that holds both vertices in two along a new edge between them.
func (m *EditMesh) ConnectVertices(a, b int32) error {
	for f := range m.Faces {
		c := m.FaceCorners(f)
		i := slices.IndexFunc(c, func(k Corner) bool { return k.V == a })
		j := slices.IndexFunc(c, func(k Corner) bool { return k.V == b })
		if i < 0 || j < 0 {
			continue
		}
		n := len(c)
		if d := (j - i + n) % n; d == 1 || d == n-1 {
			continue // they are an edge of this face already
		}
		var one, two []Corner
		for k := i; ; k = (k + 1) % n {
			one = append(one, c[k])
			if k == j {
				break
			}
		}
		for k := j; ; k = (k + 1) % n {
			two = append(two, c[k])
			if k == i {
				break
			}
		}
		m.Faces[f].Count = 0
		m.addLike(one, int32(f))
		m.addLike(two, int32(f))
		return nil
	}
	return fmt.Errorf("the vertices are not across a face")
}

// edgeChains orders edges into the lines they make.
func edgeChains(edges []Edge) [][]int32 {
	next := map[int32][]int32{}
	for _, e := range edges {
		next[e.A] = append(next[e.A], e.B)
		next[e.B] = append(next[e.B], e.A)
	}
	used := map[Edge]bool{}
	var chains [][]int32
	walk := func(from int32) {
		chain := []int32{from}
		for at := from; ; {
			moved := false
			for _, n := range next[at] {
				if e := NewEdge(at, n); !used[e] {
					used[e] = true
					chain = append(chain, n)
					at = n
					moved = true
					break
				}
			}
			if !moved {
				break
			}
		}
		if len(chain) > 1 {
			chains = append(chains, chain)
		}
	}
	keys := make([]int32, 0, len(next))
	for v := range next {
		keys = append(keys, v)
	}
	slices.Sort(keys)
	for _, v := range keys { // open lines from their ends, then what is left goes round
		if len(next[v]) == 1 {
			walk(v)
		}
	}
	for _, v := range keys {
		walk(v)
	}
	return chains
}

// Bridge joins two lines of edges with a band of quads, and gives its faces. The lines need as many vertices each.
func (m *EditMesh) Bridge(edges []Edge) ([]int32, error) {
	chains := edgeChains(edges)
	if len(chains) != 2 || len(chains[0]) != len(chains[1]) {
		return nil, fmt.Errorf("choose two lines of edges of the same length")
	}
	a, b := chains[0], chains[1]
	closed := a[0] == a[len(a)-1] && b[0] == b[len(b)-1]
	if closed {
		a, b = a[:len(a)-1], b[:len(b)-1]
	}
	cost := func(c []int32, shift int) float32 {
		var sum float32
		for i := range a {
			sum += m.Pos[a[i]].Sub(m.Pos[c[(i+shift)%len(c)]]).Length()
		}
		return sum
	}
	reversed := slices.Clone(b)
	slices.Reverse(reversed)
	best, line, offset := float32(math.Inf(1)), b, 0
	for _, c := range [][]int32{b, reversed} {
		shifts := 1
		if closed {
			shifts = len(c)
		}
		for s := range shifts {
			if d := cost(c, s); d < best {
				best, line, offset = d, c, s
			}
		}
	}
	corner := map[int32]Corner{}
	material := int32(0)
	for f := range m.Faces {
		for _, c := range m.FaceCorners(f) {
			if _, ok := corner[c.V]; !ok {
				c.Loaded = false
				corner[c.V] = c
				material = m.Faces[f].Material
			}
		}
	}
	n := len(a)
	steps := n - 1
	if closed {
		steps = n
	}
	var out []int32
	for i := range steps {
		p, q := a[i], a[(i+1)%n]
		r, s := line[(i+1+offset)%n], line[(i+offset)%n]
		out = append(out, int32(m.AddFace([]Corner{corner[p], corner[q], corner[r], corner[s]}, material)))
	}
	// A band that came out inside out faces the other way.
	box := EmptyBox()
	for _, p := range m.Pos {
		box.Grow(p)
	}
	var outward float32
	for _, f := range out {
		c := m.FaceCorners(int(f))
		middle := m.Pos[c[0].V].Add(m.Pos[c[2].V]).Scale(.5)
		outward += m.FaceNormal(int(f)).Dot(middle.Sub(box.Center()))
	}
	if outward < 0 {
		m.FlipFaces(out)
	}
	return out, nil
}
