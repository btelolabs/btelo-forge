package paint

import (
	"errors"
	"image"
	"math"
)

// Distort, Perspective and Warp, as Photoshop's: the content is mapped by a mesh, a grid over its bounds whose points
// have been moved. Each cell maps by the projective transform (homography) that takes its corners to where they
// went, which is exact for Distort and Perspective (a single cell) and, with a fine grid, follows Warp's curved
// surface. Pixels are sampled bilinearly, one source pixel beyond the edge so the edges are smooth; a vector
// object's nodes are mapped, its segments first split so they bend with the surface.

// Mesh is where a grid over the content's bounds lands: Cols × Rows cells, so (Cols+1) × (Rows+1) points, row by row
// from the top left.
type Mesh struct {
	Cols, Rows int
	Pts        []Pt
}

// homography maps (u, v) to ((a u + b v + c) / w, (d u + e v + f) / w) with w = g u + h v + 1, row-major.
type homography [9]float64

// squareToQuad is the homography taking the unit square's corners (0,0), (1,0), (1,1), (0,1) to q, in that order.
func squareToQuad(q [4]Pt) homography {
	x0, y0, x1, y1, x2, y2, x3, y3 := q[0].X, q[0].Y, q[1].X, q[1].Y, q[2].X, q[2].Y, q[3].X, q[3].Y
	dx3, dy3 := x0-x1+x2-x3, y0-y1+y2-y3
	if math.Abs(dx3) < 1e-12 && math.Abs(dy3) < 1e-12 {
		return homography{x1 - x0, x3 - x0, x0, y1 - y0, y3 - y0, y0, 0, 0, 1}
	}
	dx1, dx2, dy1, dy2 := x1-x2, x3-x2, y1-y2, y3-y2
	det := dx1*dy2 - dx2*dy1
	if det == 0 {
		return homography{x1 - x0, x3 - x0, x0, y1 - y0, y3 - y0, y0, 0, 0, 1}
	}
	g := (dx3*dy2 - dx2*dy3) / det
	h := (dx1*dy3 - dx3*dy1) / det
	return homography{x1 - x0 + g*x1, x3 - x0 + h*x3, x0, y1 - y0 + g*y1, y3 - y0 + h*y3, y0, g, h, 1}
}

// apply maps p; ok is false where the map goes through infinity.
func (m homography) apply(p Pt) (Pt, bool) {
	w := m[6]*p.X + m[7]*p.Y + m[8]
	if math.Abs(w) < 1e-12 {
		return Pt{}, false
	}
	return Pt{(m[0]*p.X + m[1]*p.Y + m[2]) / w, (m[3]*p.X + m[4]*p.Y + m[5]) / w}, w > 0
}

// inverse is the map that undoes m.
func (m homography) inverse() (homography, bool) {
	a, b, c, d, e, f, g, h, i := m[0], m[1], m[2], m[3], m[4], m[5], m[6], m[7], m[8]
	det := a*(e*i-f*h) - b*(d*i-f*g) + c*(d*h-e*g)
	if math.Abs(det) < 1e-18 {
		return homography{}, false
	}
	return homography{(e*i - f*h) / det, (c*h - b*i) / det, (b*f - c*e) / det,
		(f*g - d*i) / det, (a*i - c*g) / det, (c*d - a*f) / det,
		(d*h - e*g) / det, (b*g - a*h) / det, (a*e - b*d) / det}, true
}

// QuadMesh is the single-cell mesh taking the bounds' corners to q (top left, top right, bottom right, bottom
// left), as Distort and Perspective are.
func QuadMesh(q [4]Pt) Mesh { return Mesh{Cols: 1, Rows: 1, Pts: []Pt{q[0], q[1], q[3], q[2]}} }

// WarpMesh is the mesh Warp makes of its 4 × 4 control points (row by row, as Photoshop's warp grid): the bicubic
// Bézier surface through them, sampled cells×cells.
func WarpMesh(ctrl [16]Pt, cells int) Mesh {
	cells = min(max(cells, 1), 64)
	bern := func(t float64) [4]float64 {
		s := 1 - t
		return [4]float64{s * s * s, 3 * s * s * t, 3 * s * t * t, t * t * t}
	}
	m := Mesh{Cols: cells, Rows: cells}
	for j := 0; j <= cells; j++ {
		bv := bern(float64(j) / float64(cells))
		for i := 0; i <= cells; i++ {
			bu := bern(float64(i) / float64(cells))
			var p Pt
			for r := range 4 {
				for c := range 4 {
					p = p.Add(ctrl[r*4+c].Mul(bv[r] * bu[c]))
				}
			}
			m.Pts = append(m.Pts, p)
		}
	}
	return m
}

// WarpGrid is the warp control points that leave the bounds as they are.
func WarpGrid(r image.Rectangle) [16]Pt {
	var g [16]Pt
	for row := range 4 {
		for c := range 4 {
			g[row*4+c] = Pt{float64(r.Min.X) + float64(r.Dx())*float64(c)/3, float64(r.Min.Y) + float64(r.Dy())*float64(row)/3}
		}
	}
	return g
}

var errMesh = errors.New("the mesh does not fit its grid")

func (m Mesh) valid() bool {
	if m.Cols < 1 || m.Rows < 1 || m.Cols > 256 || m.Rows > 256 || len(m.Pts) != (m.Cols+1)*(m.Rows+1) {
		return false
	}
	for _, p := range m.Pts {
		if math.IsNaN(p.X) || math.IsNaN(p.Y) || math.Abs(p.X) > 1e7 || math.Abs(p.Y) > 1e7 {
			return false
		}
	}
	return true
}

func (m Mesh) pt(i, j int) Pt { return m.Pts[j*(m.Cols+1)+i] }

// cell is cell (i, j)'s corners where they landed: top left, top right, bottom right, bottom left.
func (m Mesh) cell(i, j int) [4]Pt {
	return [4]Pt{m.pt(i, j), m.pt(i+1, j), m.pt(i+1, j+1), m.pt(i, j+1)}
}

// bounds is the canvas area the mesh covers.
func (m Mesh) bounds() image.Rectangle {
	b := boundsOf(m.Pts)
	return image.Rect(int(math.Floor(b.Min.X))-1, int(math.Floor(b.Min.Y))-1, int(math.Ceil(b.Max.X))+1, int(math.Ceil(b.Max.Y))+1)
}

// Map is where point p of the bounds src lands.
func (m Mesh) Map(src image.Rectangle, p Pt) Pt {
	cw, ch := float64(src.Dx())/float64(m.Cols), float64(src.Dy())/float64(m.Rows)
	fu, fv := (p.X-float64(src.Min.X))/cw, (p.Y-float64(src.Min.Y))/ch
	i, j := min(max(int(math.Floor(fu)), 0), m.Cols-1), min(max(int(math.Floor(fv)), 0), m.Rows-1)
	q, _ := squareToQuad(m.cell(i, j)).apply(Pt{fu - float64(i), fv - float64(j)})
	return q
}

// each calls fn for each canvas pixel within clip that the mesh covers, with where in src it comes from. A pixel on
// the edge between two cells belongs to one of them; along the outside, the cells reach a source pixel further, so
// sampling there fades the edge.
func (m Mesh) each(src, clip image.Rectangle, fn func(x, y int, sx, sy float64)) {
	cw, ch := float64(src.Dx())/float64(m.Cols), float64(src.Dy())/float64(m.Rows)
	eu, ev := 1/max(cw, 1e-9), 1/max(ch, 1e-9)
	for j := range m.Rows {
		for i := range m.Cols {
			q := m.cell(i, j)
			inv, ok := squareToQuad(q).inverse()
			if !ok {
				continue
			}
			b := boundsOf(q[:])
			box := image.Rect(int(math.Floor(b.Min.X))-1, int(math.Floor(b.Min.Y))-1, int(math.Ceil(b.Max.X))+1, int(math.Ceil(b.Max.Y))+1).Intersect(clip)
			ulo, uhi, vlo, vhi := 0.0, 1.0, 0.0, 1.0
			if i == 0 {
				ulo = -eu
			}
			if i == m.Cols-1 {
				uhi = 1 + eu
			}
			if j == 0 {
				vlo = -ev
			}
			if j == m.Rows-1 {
				vhi = 1 + ev
			}
			for y := box.Min.Y; y < box.Max.Y; y++ {
				for x := box.Min.X; x < box.Max.X; x++ {
					uv, ok := inv.apply(Pt{float64(x) + 0.5, float64(y) + 0.5})
					if !ok || uv.X < ulo || uv.X >= uhi || uv.Y < vlo || uv.Y >= vhi {
						continue
					}
					fn(x, y, float64(src.Min.X)+(float64(i)+uv.X)*cw, float64(src.Min.Y)+(float64(j)+uv.Y)*ch)
				}
			}
		}
	}
}

// SetMesh shows the content mapped by the mesh m (over the content's bounds), as Distort, Perspective and Warp
// do, and returns the canvas area that changed; Apply then records it as the step called name.
func (t *TransformSession) SetMesh(m Mesh, name string) (image.Rectangle, error) {
	if !m.valid() {
		return image.Rectangle{}, errMesh
	}
	t.mesh, t.name = &m, name
	if t.nodes != nil {
		return t.setNodes(func(p Pt) Pt { return m.Map(t.bounds, p) }), nil
	}
	out := meshClip(m, t.bounds)
	if t.clip != nil && t.base == nil {
		clip := meshVector(*t.clip, t.bounds, m) // it bends with the whole layer
		t.l.Clip = &clip
	}
	switch {
	case t.vector != nil:
		v := meshVector(*t.vector, t.bounds, m)
		t.l.Vector = &v
		if t.mask != nil {
			t.l.Mask = meshMask(t.mask, t.mask.Bounds().Union(t.bounds), m)
		}
	case t.base == nil:
		t.l.Pixels = RasterFrom(meshRGBA(t.layer.Read(t.bounds), t.bounds, m))
		if t.mask != nil {
			t.l.Mask = meshMask(t.mask, t.mask.Bounds().Union(t.bounds), m)
		}
	default:
		pix := t.base.Snapshot()
		moved := meshRGBA(t.source, t.bounds, m)
		under := pix.Read(moved.Rect)
		over(under, moved, moved.Rect, 1)
		pix.Write(under)
		t.l.Pixels = pix
		t.d.Selection = meshMask(t.sel, t.sel.Bounds().Union(t.bounds), m)
	}
	changed := t.shown.Union(out)
	t.shown = out
	return changed, nil
}

// meshClip keeps a mesh's output to a sensible size round its source, however far a point is dragged.
func meshClip(m Mesh, src image.Rectangle) image.Rectangle {
	return m.bounds().Intersect(src.Inset(-maxSide))
}

// meshRGBA is src's area r mapped by the mesh, sampled bilinearly, transparent beyond r.
func meshRGBA(src *image.RGBA, r image.Rectangle, m Mesh) *image.RGBA {
	out := image.NewRGBA(meshClip(m, r))
	m.each(r, out.Rect, func(x, y int, sx, sy float64) {
		fx, fy := sx-0.5, sy-0.5
		x0, y0 := int(math.Floor(fx)), int(math.Floor(fy))
		tx, ty := fx-float64(x0), fy-float64(y0)
		var acc [4]float64
		for k, w := range [4]float64{(1 - tx) * (1 - ty), tx * (1 - ty), (1 - tx) * ty, tx * ty} {
			px, py := x0+k%2, y0+k/2
			if w == 0 || !(image.Point{px, py}).In(r) {
				continue
			}
			i := src.PixOffset(px, py)
			for c := range 4 {
				acc[c] += w * float64(src.Pix[i+c])
			}
		}
		o := out.PixOffset(x, y)
		for c := range 4 {
			out.Pix[o+c] = uint8(min(math.Round(acc[c]), 255))
		}
	})
	return out
}

// meshMask is a mask mapped by the mesh: what differs from its fill is mapped, the fill stays everywhere else.
func meshMask(mask *Mask, r image.Rectangle, m Mesh) *Mask {
	out := NewMask(mask.Fill())
	src := mask.Read(r)
	fill := float64(mask.Fill())
	img := image.NewAlpha(meshClip(m, r))
	for i := range img.Pix {
		img.Pix[i] = mask.Fill()
	}
	m.each(r, img.Rect, func(x, y int, sx, sy float64) {
		fx, fy := sx-0.5, sy-0.5
		x0, y0 := int(math.Floor(fx)), int(math.Floor(fy))
		tx, ty := fx-float64(x0), fy-float64(y0)
		acc := 0.0
		for k, w := range [4]float64{(1 - tx) * (1 - ty), tx * (1 - ty), (1 - tx) * ty, tx * ty} {
			px, py := x0+k%2, y0+k/2
			v := fill
			if (image.Point{px, py}).In(r) {
				v = float64(src.Pix[src.PixOffset(px, py)])
			}
			acc += w * v
		}
		img.Pix[img.PixOffset(x, y)] = uint8(min(math.Round(acc), 255))
	})
	out.Write(img)
	return out
}

// meshVector is a vector object mapped by the mesh: a curve with each segment split in four, so it bends with the
// surface, and every node and handle mapped.
func meshVector(v Vector, src image.Rectangle, m Mesh) Vector {
	c := v.ToCurve()
	// The curve in canvas space, so the mesh's map applies to it directly.
	var paths []Subpath
	for _, s := range c.Path {
		ns := Subpath{Closed: s.Closed, Op: s.Op}
		if len(s.Nodes) == 1 {
			p := c.M.Map(s.Nodes[0].P) // mapped by the mesh below, with the rest
			ns.Nodes = []PathNode{{P: p, In: p, Out: p}}
		}
		for k := range s.Segments() {
			p0, c1, c2, p3 := segment(s, k)
			rest := [4]Pt{c.M.Map(p0), c.M.Map(c1), c.M.Map(c2), c.M.Map(p3)}
			for part := range 4 {
				// The next quarter of the segment, split off what is left of it.
				piece, after := splitCubic(rest[0], rest[1], rest[2], rest[3], 1/float64(4-part))
				rest = after
				if len(ns.Nodes) == 0 {
					ns.Nodes = append(ns.Nodes, PathNode{P: piece[0], In: piece[0], Out: piece[1]})
				} else {
					ns.Nodes[len(ns.Nodes)-1].Out = piece[1]
				}
				ns.Nodes = append(ns.Nodes, PathNode{P: piece[3], In: piece[2], Out: piece[3]})
			}
		}
		if s.Closed && len(ns.Nodes) > 1 {
			// The last node is the first again.
			ns.Nodes[0].In = ns.Nodes[len(ns.Nodes)-1].In
			ns.Nodes = ns.Nodes[:len(ns.Nodes)-1]
		}
		for i := range ns.Nodes {
			n := &ns.Nodes[i]
			n.P, n.In, n.Out = m.Map(src, n.P), m.Map(src, n.In), m.Map(src, n.Out)
		}
		paths = append(paths, ns)
	}
	c.Path, c.M = paths, Identity
	// A gradient placed in the object's own space keeps its place on the canvas.
	c.Fill, c.Stroke.Paint = mapPaint(c.Fill, v.M), mapPaint(c.Stroke.Paint, v.M)
	return c
}
