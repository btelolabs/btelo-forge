package scene3d

import (
	"image"
	"math"
	"slices"
)

// AutoUnwrap lays the mesh out in the unit square, as Blender's Smart UV Project does: faces join a chart while they turn
// less than limit degrees from it, each chart is laid flat along its mean normal, and the charts are packed on shelves,
// with margin of the square between them. A chart keeps every face's size against the others'.
func (m *EditMesh) AutoUnwrap(limit, margin float32) {
	type chart struct {
		faces          []int32
		u, v           Vec3
		x0, y0, x1, y1 float32
		px, py         float32
		turned         bool
	}
	cos := float32(math.Cos(float64(limit) * math.Pi / 180))
	around := m.EdgeFaces()
	taken := make([]bool, len(m.Faces))
	var charts []*chart
	for f := range m.Faces {
		if taken[f] || m.Faces[f].Count == 0 {
			continue
		}
		seed := m.FaceNormal(f)
		c := &chart{faces: []int32{int32(f)}}
		taken[f] = true
		sum := seed.Scale(m.faceArea(f))
		for at := 0; at < len(c.faces); at++ {
			corners := m.FaceCorners(int(c.faces[at]))
			for i := range corners {
				for _, g := range around[NewEdge(corners[i].V, corners[(i+1)%len(corners)].V)] {
					if !taken[g] && m.Faces[g].Count != 0 && m.FaceNormal(int(g)).Dot(seed) >= cos {
						taken[g] = true
						c.faces = append(c.faces, g)
						sum = sum.Add(m.FaceNormal(int(g)).Scale(m.faceArea(int(g))))
					}
				}
			}
		}
		n := sum.Norm()
		if n == (Vec3{}) {
			n = seed
		}
		side := Vec3{0, 1, 0}
		if math.Abs(float64(n[1])) > .9 {
			side = Vec3{1, 0, 0}
		}
		c.u = side.Cross(n).Norm()
		c.v = n.Cross(c.u)
		c.x0, c.y0, c.x1, c.y1 = float32(math.Inf(1)), float32(math.Inf(1)), float32(math.Inf(-1)), float32(math.Inf(-1))
		for _, g := range c.faces {
			for _, k := range m.FaceCorners(int(g)) {
				p := m.Pos[k.V]
				x, y := p.Dot(c.u), p.Dot(c.v)
				c.x0, c.y0, c.x1, c.y1 = min(c.x0, x), min(c.y0, y), max(c.x1, x), max(c.y1, y)
			}
		}
		charts = append(charts, c)
	}
	if len(charts) == 0 {
		return
	}
	// Tall charts first, each lying on its long side, on shelves about as wide as the whole is high.
	var area float64
	for _, c := range charts {
		w, h := c.x1-c.x0, c.y1-c.y0
		c.turned = w < h
		area += float64((w + margin) * (h + margin))
	}
	size := func(c *chart) (float32, float32) {
		if c.turned {
			return c.y1 - c.y0, c.x1 - c.x0
		}
		return c.x1 - c.x0, c.y1 - c.y0
	}
	slices.SortFunc(charts, func(a, b *chart) int {
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
	side := float32(math.Sqrt(area) * 1.1)
	for _, c := range charts {
		w, _ := size(c)
		side = max(side, w)
	}
	// The gap is a share of the result, which depends on the gap: work in the mesh's units, sized by the first guess.
	gap := side * margin
	var x, y, shelf, wide float32
	for _, c := range charts {
		w, h := size(c)
		if x > 0 && x+w > side {
			x, y, shelf = 0, y+shelf+gap, 0
		}
		c.px, c.py = x, y
		x += w + gap
		shelf = max(shelf, h)
		wide = max(wide, x-gap)
	}
	scale := 1 / max(wide, y+shelf)
	for _, c := range charts {
		for _, g := range c.faces {
			corners := m.FaceCorners(int(g))
			for i := range corners {
				p := m.Pos[corners[i].V]
				x, y := p.Dot(c.u)-c.x0, p.Dot(c.v)-c.y0
				if c.turned {
					x, y = y, x
				}
				corners[i].UV = Vec2{(c.px + x) * scale, (c.py + y) * scale}
			}
		}
	}
}

func (m *EditMesh) faceArea(f int) float32 {
	c := m.FaceCorners(f)
	var sum Vec3
	for i := 1; i+1 < len(c); i++ {
		sum = sum.Add(m.Pos[c[i].V].Sub(m.Pos[c[0].V]).Cross(m.Pos[c[i+1].V].Sub(m.Pos[c[0].V])))
	}
	return sum.Length() / 2
}

// Painter finds the texels of a picture around a point in the world, for a brush that goes over a mesh as a ball, so that
// a stroke does not break where the UV charts do.
type Painter struct {
	tris    []paintTri
	cell    float32
	grid    map[[3]int32][]int32
	covered []bool // the texels some triangle has
	W, H    int
}

type paintTri struct {
	p [3]Vec3
	n Vec3
	t [3]Vec2 // in texels
}

// NewPainter prepares a picture of w×h for the surfaces drawn with material.
func NewPainter(surfaces []Surface, material, w, h int) *Painter {
	p := &Painter{W: w, H: h, grid: map[[3]int32][]int32{}, covered: make([]bool, w*h)}
	var length float64
	for i := range surfaces {
		s := &surfaces[i]
		if s.Material != material || s.UV == nil {
			continue
		}
		for t := 0; t+2 < len(s.Indices); t += 3 {
			a, b, c := s.Indices[t], s.Indices[t+1], s.Indices[t+2]
			q := paintTri{p: [3]Vec3{s.Pos[a], s.Pos[b], s.Pos[c]}}
			q.n = q.p[1].Sub(q.p[0]).Cross(q.p[2].Sub(q.p[0])).Norm()
			for k, v := range []uint32{a, b, c} {
				q.t[k] = Vec2{wrapUV(s.UV[v][0]) * float32(w), wrapUV(s.UV[v][1]) * float32(h)}
			}
			length += float64(q.p[1].Sub(q.p[0]).Length())
			p.tris = append(p.tris, q)
		}
	}
	if len(p.tris) == 0 {
		return p
	}
	p.cell = max(float32(length/float64(len(p.tris))), 1e-5)
	for i := range p.tris {
		q := &p.tris[i]
		box := EmptyBox()
		for _, v := range q.p {
			box.Grow(v)
		}
		lo, hi := p.key(box.Min), p.key(box.Max)
		for x := lo[0]; x <= hi[0]; x++ {
			for y := lo[1]; y <= hi[1]; y++ {
				for z := lo[2]; z <= hi[2]; z++ {
					k := [3]int32{x, y, z}
					p.grid[k] = append(p.grid[k], int32(i))
				}
			}
		}
		q.raster(w, h, func(x, y int, _ Vec3) { p.covered[y*w+x] = true })
	}
	return p
}

// wrapUV brings a coordinate of a repeating picture into the unit, with exactly 1 staying the far edge.
func wrapUV(v float32) float32 {
	if v >= 0 && v <= 1 {
		return v
	}
	return v - float32(math.Floor(float64(v)))
}

func (p *Painter) key(v Vec3) [3]int32 {
	return [3]int32{int32(math.Floor(float64(v[0] / p.cell))), int32(math.Floor(float64(v[1] / p.cell))), int32(math.Floor(float64(v[2] / p.cell)))}
}

// raster visits the texels of the triangle, with the place in the world of each.
func (q *paintTri) raster(w, h int, visit func(x, y int, at Vec3)) {
	a, b, c := q.t[0], q.t[1], q.t[2]
	x0, x1 := max(int(min(a[0], b[0], c[0])), 0), min(int(max(a[0], b[0], c[0]))+1, w-1)
	y0, y1 := max(int(min(a[1], b[1], c[1])), 0), min(int(max(a[1], b[1], c[1]))+1, h-1)
	area := (b[0]-a[0])*(c[1]-a[1]) - (b[1]-a[1])*(c[0]-a[0])
	if area == 0 {
		return
	}
	for y := y0; y <= y1; y++ {
		py := float32(y) + .5
		for x := x0; x <= x1; x++ {
			px := float32(x) + .5
			u := ((b[0]-px)*(c[1]-py) - (b[1]-py)*(c[0]-px)) / area
			v := ((c[0]-px)*(a[1]-py) - (c[1]-py)*(a[0]-px)) / area
			t := 1 - u - v
			if u < -.02 || v < -.02 || t < -.02 {
				continue
			}
			visit(x, y, q.p[0].Scale(u).Add(q.p[1].Scale(v)).Add(q.p[2].Scale(t)))
		}
	}
}

// Dab visits every texel within radius of center on a surface facing the way back along view, with its distance.
func (p *Painter) Dab(center, view Vec3, radius float32, visit func(x, y int, distance float32)) {
	if p.cell == 0 {
		return
	}
	lo, hi := p.key(center.Sub(Vec3{radius, radius, radius})), p.key(center.Add(Vec3{radius, radius, radius}))
	var seen []int32
	for x := lo[0]; x <= hi[0]; x++ {
		for y := lo[1]; y <= hi[1]; y++ {
			for z := lo[2]; z <= hi[2]; z++ {
				seen = append(seen, p.grid[[3]int32{x, y, z}]...)
			}
		}
	}
	slices.Sort(seen)
	for _, i := range slices.Compact(seen) {
		q := &p.tris[i]
		if q.n.Dot(view) >= 0 {
			continue
		}
		q.raster(p.W, p.H, func(x, y int, at Vec3) {
			if d := at.Sub(center).Length(); d <= radius {
				visit(x, y, d)
			}
		})
	}
}

// Covered reports whether a triangle has the texel: those no triangle has take colour from their neighbours, for the
// picture's smaller levels to find at the edges of the charts.
func (p *Painter) Covered(x, y int) bool { return p.covered[y*p.W+x] }

// TransferUV draws a picture again for a new UV layout: from and to are the same surfaces with the old and new UVs, the
// same triangles in the same order. What each texel of the new layout shows comes from where its point of the mesh was on
// the old, and the charts' edges are grown a little, for the smaller levels to find no background.
func TransferUV(from, to []Surface, material int, src *image.NRGBA) *image.NRGBA {
	w, h := src.Rect.Dx(), src.Rect.Dy()
	out := image.NewNRGBA(src.Rect)
	covered := make([]bool, w*h)
	sample := func(u, v float32) [4]uint8 {
		x := min(int(wrapUV(u)*float32(w)), w-1)
		y := min(int(wrapUV(v)*float32(h)), h-1)
		p := src.Pix[src.PixOffset(x, y):]
		return [4]uint8{p[0], p[1], p[2], p[3]}
	}
	for i := range to {
		a, b := &from[i], &to[i]
		if b.Material != material || a.UV == nil || b.UV == nil || len(a.Indices) != len(b.Indices) {
			continue
		}
		for t := 0; t+2 < len(b.Indices); t += 3 {
			var q paintTri
			for k := range 3 {
				old := a.UV[a.Indices[t+k]]
				q.p[k] = Vec3{old[0], old[1], 0} // the triangle carries its old UVs where a brush reads places
				next := b.UV[b.Indices[t+k]]
				q.t[k] = Vec2{wrapUV(next[0]) * float32(w), wrapUV(next[1]) * float32(h)}
			}
			q.raster(w, h, func(x, y int, at Vec3) {
				c := sample(at[0], at[1])
				copy(out.Pix[out.PixOffset(x, y):], c[:])
				covered[y*w+x] = true
			})
		}
	}
	for range 4 {
		var grown []int
		for y := range h {
			for x := range w {
				if covered[y*w+x] {
					continue
				}
				for _, d := range [...][2]int{{-1, 0}, {1, 0}, {0, -1}, {0, 1}} {
					nx, ny := x+d[0], y+d[1]
					if nx >= 0 && ny >= 0 && nx < w && ny < h && covered[ny*w+nx] {
						copy(out.Pix[out.PixOffset(x, y):out.PixOffset(x, y)+4], out.Pix[out.PixOffset(nx, ny):])
						grown = append(grown, y*w+x)
						break
					}
				}
			}
		}
		for _, i := range grown {
			covered[i] = true
		}
	}
	return out
}
