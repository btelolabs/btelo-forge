package scene3d

import "slices"

// Picker finds what a ray hits among an asset's surfaces, for the camera and the tools.
type Picker struct {
	nodes []pickNode
	order []uint32 // triangles in tree order
	tris  []Vec3   // their corners, three for each, as first read
	first []uint32 // the first triangle of each surface
}

type pickNode struct {
	box         Box
	first, n    uint32 // a leaf's triangles; n is 0 for an inner node
	left, right uint32
}

const (
	pickBins = 16
	pickLeaf = 4
)

// NewPicker builds the search tree over surfaces.
func NewPicker(surfaces []Surface) *Picker {
	p := &Picker{}
	for i := range surfaces {
		s := &surfaces[i]
		p.first = append(p.first, uint32(len(p.tris)/3))
		for _, v := range s.Indices {
			p.tris = append(p.tris, s.Pos[v])
		}
	}
	n := len(p.tris) / 3
	if n == 0 {
		return p
	}
	p.order = make([]uint32, n)
	boxes := make([]Box, n)
	centers := make([]Vec3, n)
	for i := range n {
		p.order[i] = uint32(i)
		b := EmptyBox()
		b.Grow(p.tris[i*3])
		b.Grow(p.tris[i*3+1])
		b.Grow(p.tris[i*3+2])
		boxes[i], centers[i] = b, b.Center()
	}
	p.nodes = make([]pickNode, 1, n)
	p.build(0, 0, n, boxes, centers)
	return p
}

// build makes node at over order[first:first+count], split where the surface area heuristic says.
func (p *Picker) build(at, first, count int, boxes []Box, centers []Vec3) {
	all, mids := EmptyBox(), EmptyBox()
	for _, t := range p.order[first : first+count] {
		all.Join(boxes[t])
		mids.Grow(centers[t])
	}
	p.nodes[at].box = all
	axis, split := -1, 0
	cost := float32(count) * all.area()
	type bin struct {
		box Box
		n   int
	}
	binOf := func(t uint32, a int) int {
		return min(int((centers[t][a]-mids.Min[a])/(mids.Max[a]-mids.Min[a])*pickBins), pickBins-1)
	}
	for a := range 3 {
		if mids.Max[a] <= mids.Min[a] {
			continue
		}
		var bins [pickBins]bin
		for i := range bins {
			bins[i].box = EmptyBox()
		}
		for _, t := range p.order[first : first+count] {
			b := &bins[binOf(t, a)]
			b.n++
			b.box.Join(boxes[t])
		}
		var rightArea [pickBins]float32
		var rightN [pickBins]int
		r, rn := EmptyBox(), 0
		for i := pickBins - 1; i > 0; i-- {
			r.Join(bins[i].box)
			rn += bins[i].n
			rightArea[i], rightN[i] = r.area(), rn
		}
		l, ln := EmptyBox(), 0
		for i := range pickBins - 1 {
			l.Join(bins[i].box)
			ln += bins[i].n
			if ln == 0 || rightN[i+1] == 0 {
				continue
			}
			if c := float32(ln)*l.area() + float32(rightN[i+1])*rightArea[i+1]; c < cost {
				axis, split, cost = a, i, c
			}
		}
	}
	mid := first + count/2 // triangles all in one place have no good split, and may still be too many for a leaf
	switch {
	case axis >= 0:
		i, j := first, first+count-1
		for i <= j {
			if binOf(p.order[i], axis) <= split {
				i++
			} else {
				p.order[i], p.order[j] = p.order[j], p.order[i]
				j--
			}
		}
		mid = i
	case count <= pickLeaf:
		p.nodes[at].first, p.nodes[at].n = uint32(first), uint32(count)
		return
	}
	left := len(p.nodes)
	p.nodes = append(p.nodes, pickNode{}, pickNode{})
	p.nodes[at].left, p.nodes[at].right = uint32(left), uint32(left+1)
	p.build(left, first, mid-first, boxes, centers)
	p.build(left+1, mid, first+count-mid, boxes, centers)
}

// hitBox is where a ray with inverse direction inv enters b, or a negative number if it misses before limit.
func hitBox(b *Box, o, inv Vec3, limit float32) float32 {
	near, far := float32(0), limit
	for i := range 3 {
		t0, t1 := (b.Min[i]-o[i])*inv[i], (b.Max[i]-o[i])*inv[i]
		if t0 > t1 {
			t0, t1 = t1, t0
		}
		near, far = max(near, t0), min(far, t1)
	}
	// A little room for rounding: a flat box, or a ray along its edge, has no width to spare.
	if near > far*1.00001 {
		return -1
	}
	return near
}

// Ray is the distance along a unit direction dir from origin to the nearest triangle, seen from either side.
func (p *Picker) Ray(origin, dir Vec3) (float32, bool) {
	d, _, _, ok := p.Hit(origin, dir)
	return d, ok
}

// Hit is Ray with what was hit: the surface, and the triangle within it.
func (p *Picker) Hit(origin, dir Vec3) (distance float32, surface, triangle int, ok bool) {
	if len(p.nodes) == 0 {
		return 0, 0, 0, false
	}
	const far = 1e30
	inv := Vec3{1 / dir[0], 1 / dir[1], 1 / dir[2]}
	best := float32(far)
	hit := uint32(0)
	stack := make([]uint32, 1, 64)
	for len(stack) > 0 {
		n := &p.nodes[stack[len(stack)-1]]
		stack = stack[:len(stack)-1]
		if hitBox(&n.box, origin, inv, best) < 0 {
			continue
		}
		if n.n == 0 {
			stack = append(stack, n.left, n.right)
			continue
		}
		for _, t := range p.order[n.first : n.first+n.n] {
			if d, ok := hitTriangle(origin, dir, p.tris[t*3], p.tris[t*3+1], p.tris[t*3+2]); ok && d < best {
				best, hit = d, t
			}
		}
	}
	if best >= far {
		return 0, 0, 0, false
	}
	at, _ := slices.BinarySearch(p.first, hit+1)
	return best, at - 1, int(hit - p.first[at-1]), true
}

// hitTriangle is Möller and Trumbore's intersection.
func hitTriangle(o, d, a, b, c Vec3) (float32, bool) {
	e1, e2 := b.Sub(a), c.Sub(a)
	pv := d.Cross(e2)
	det := e1.Dot(pv)
	if det > -1e-12 && det < 1e-12 {
		return 0, false
	}
	inv := 1 / det
	tv := o.Sub(a)
	u := tv.Dot(pv) * inv
	if u < 0 || u > 1 {
		return 0, false
	}
	qv := tv.Cross(e1)
	v := d.Dot(qv) * inv
	if v < 0 || u+v > 1 {
		return 0, false
	}
	t := e2.Dot(qv) * inv
	return t, t > 0
}

// FlatNode is a node of the tree as a shader reads it: A is a leaf's first triangle or an inner node's right child, the
// left being the next node, and Count a leaf's triangles, or 0.
type FlatNode struct {
	Low   Vec3
	A     uint32
	High  Vec3
	Count uint32
}

// TriangleRef is a triangle of a surface.
type TriangleRef struct{ Surface, Triangle uint32 }

// Flat is the tree in depth first order, with the triangles in the order its leaves count them from.
func (p *Picker) Flat() ([]FlatNode, []TriangleRef) {
	if len(p.nodes) == 0 {
		return nil, nil
	}
	nodes := make([]FlatNode, 0, len(p.nodes))
	refs := make([]TriangleRef, 0, len(p.order))
	var walk func(at uint32)
	walk = func(at uint32) {
		n := &p.nodes[at]
		here := len(nodes)
		nodes = append(nodes, FlatNode{Low: n.box.Min, High: n.box.Max})
		if n.n > 0 {
			nodes[here].A, nodes[here].Count = uint32(len(refs)), n.n
			for _, t := range p.order[n.first : n.first+n.n] {
				s, i := p.locate(t)
				refs = append(refs, TriangleRef{s, i})
			}
			return
		}
		walk(n.left)
		nodes[here].A = uint32(len(nodes))
		walk(n.right)
	}
	walk(0)
	return nodes, refs
}

// locate finds the surface of a triangle and its number there.
func (p *Picker) locate(t uint32) (surface, triangle uint32) {
	s := uint32(len(p.first) - 1)
	for s > 0 && p.first[s] > t {
		s--
	}
	return s, t - p.first[s]
}
