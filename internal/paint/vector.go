package paint

import (
	"image"
	"image/color"
	"math"
)

// Vector layers hold one object each, as Affinity Designer's curves and shapes are each a layer: a curve made of
// Bézier nodes, or a smart shape (rectangle, ellipse, polygon, star) that keeps its parameters until it is turned
// into a curve. An object is drawn at whatever scale it is shown, so it is sharp at every zoom and in export. Objects
// are values never changed in place: an edit makes a new one, so the history and snapshots can share them.

// PathNode is a node of a curve: its point and the control points of the segments before and after it, in the
// object's own space. A node with a handle equal to its point has none on that side; a smooth node keeps its two
// handles in line, as dragging one with the node tool does.
type PathNode struct {
	P      Pt   `json:"p"`
	In     Pt   `json:"in"`
	Out    Pt   `json:"out"`
	Smooth bool `json:"smooth,omitempty"`
}

// Subpath is a run of nodes, open or closed back to its first.
type Subpath struct {
	Nodes  []PathNode `json:"nodes"`
	Closed bool       `json:"closed,omitempty"`
}

// VectorKind is what kind of object a vector layer holds.
type VectorKind int

const (
	VectorCurve VectorKind = iota
	VectorRect
	VectorEllipse
	VectorPolygon
	VectorStar
)

// PaintKind is how a fill or a stroke is coloured.
type PaintKind int

const (
	PaintNone PaintKind = iota
	PaintSolid
	PaintLinear
	PaintRadial
)

// Stop is a colour of a gradient at a place along it, 0 to 1.
type Stop struct {
	At    float64     `json:"at"`
	Color color.NRGBA `json:"color"`
}

// VectorPaint colours a fill or a stroke: a solid colour, or a gradient through stops from From to To (in the
// object's own space; a radial gradient's To is on its edge).
type VectorPaint struct {
	Kind  PaintKind   `json:"kind"`
	Color color.NRGBA `json:"color"`
	Stops []Stop      `json:"stops,omitempty"`
	From  Pt          `json:"from,omitempty"`
	To    Pt          `json:"to,omitempty"`
}

// StrokeAlign is where a stroke lies against its outline.
type StrokeAlign int

const (
	AlignCentre StrokeAlign = iota
	AlignInside
	AlignOutside
)

// LineCap and LineJoin finish a stroke's ends and corners.
type (
	LineCap  int
	LineJoin int
)

const (
	CapButt LineCap = iota
	CapRound
	CapSquare
)

const (
	JoinMiter LineJoin = iota
	JoinRound
	JoinBevel
)

// VectorStroke is an object's outline: its paint, width (canvas pixels), alignment, ends, corners and dashes.
type VectorStroke struct {
	Paint VectorPaint `json:"paint"`
	Width float64     `json:"width"`
	Align StrokeAlign `json:"align,omitempty"`
	Cap   LineCap     `json:"cap,omitempty"`
	Join  LineJoin    `json:"join,omitempty"`
	Miter float64     `json:"miter,omitempty"` // the longest a miter may be, in widths; 0 is the usual 4
	Dash  []float64   `json:"dash,omitempty"`  // on, off, on, off… in widths
}

// Vector is a vector layer's object.
type Vector struct {
	Kind   VectorKind   `json:"kind"`
	Path   []Subpath    `json:"path,omitempty"`   // a curve's
	Size   Pt           `json:"size,omitempty"`   // a smart shape's box, from its own origin
	Radius float64      `json:"radius,omitempty"` // a rectangle's corners
	Sides  int          `json:"sides,omitempty"`  // a polygon's or star's points
	Inner  float64      `json:"inner,omitempty"`  // a star's inner radius, as a share of its outer
	M      Affine       `json:"m"`                // from its own space to the canvas
	Fill   VectorPaint  `json:"fill"`
	Stroke VectorStroke `json:"stroke"`
	Rule   FillRule     `json:"rule,omitempty"`
}

// kappa places a cubic's handles to follow a quarter circle closely.
const kappa = 0.5522847498

// Outline is the object's outline in its own space: a curve's path, or a smart shape drawn as curves.
func (v *Vector) Outline() []Subpath {
	w, h := v.Size.X, v.Size.Y
	switch v.Kind {
	case VectorRect:
		r := min(max(v.Radius, 0), w/2, h/2)
		if r <= 0 {
			return []Subpath{polyline(true, Pt{0, 0}, Pt{w, 0}, Pt{w, h}, Pt{0, h})}
		}
		k := r * kappa
		corner := func(p, in, out Pt) PathNode { return PathNode{P: p, In: in, Out: out} }
		return []Subpath{{Closed: true, Nodes: []PathNode{
			corner(Pt{r, 0}, Pt{r - k, 0}, Pt{r, 0}), corner(Pt{w - r, 0}, Pt{w - r, 0}, Pt{w - r + k, 0}),
			corner(Pt{w, r}, Pt{w, r - k}, Pt{w, r}), corner(Pt{w, h - r}, Pt{w, h - r}, Pt{w, h - r + k}),
			corner(Pt{w - r, h}, Pt{w - r + k, h}, Pt{w - r, h}), corner(Pt{r, h}, Pt{r, h}, Pt{r - k, h}),
			corner(Pt{0, h - r}, Pt{0, h - r + k}, Pt{0, h - r}), corner(Pt{0, r}, Pt{0, r}, Pt{0, r - k}),
		}}}
	case VectorEllipse:
		cx, cy, rx, ry := w/2, h/2, w/2, h/2
		kx, ky := rx*kappa, ry*kappa
		return []Subpath{{Closed: true, Nodes: []PathNode{
			{P: Pt{cx + rx, cy}, In: Pt{cx + rx, cy - ky}, Out: Pt{cx + rx, cy + ky}, Smooth: true},
			{P: Pt{cx, cy + ry}, In: Pt{cx + kx, cy + ry}, Out: Pt{cx - kx, cy + ry}, Smooth: true},
			{P: Pt{cx - rx, cy}, In: Pt{cx - rx, cy + ky}, Out: Pt{cx - rx, cy - ky}, Smooth: true},
			{P: Pt{cx, cy - ry}, In: Pt{cx - kx, cy - ry}, Out: Pt{cx + kx, cy - ry}, Smooth: true},
		}}}
	case VectorPolygon, VectorStar:
		n := max(v.Sides, 3)
		cx, cy, rx, ry := w/2, h/2, w/2, h/2
		var pts []Pt
		for i := range n {
			a := -math.Pi/2 + 2*math.Pi*float64(i)/float64(n)
			pts = append(pts, Pt{cx + rx*math.Cos(a), cy + ry*math.Sin(a)})
			if v.Kind == VectorStar {
				inner := min(max(v.Inner, 0.05), 1)
				a += math.Pi / float64(n)
				pts = append(pts, Pt{cx + rx*inner*math.Cos(a), cy + ry*inner*math.Sin(a)})
			}
		}
		return []Subpath{polyline(true, pts...)}
	}
	return v.Path
}

// polyline is a subpath of sharp nodes with no handles.
func polyline(closed bool, pts ...Pt) Subpath {
	s := Subpath{Closed: closed}
	for _, p := range pts {
		s.Nodes = append(s.Nodes, PathNode{P: p, In: p, Out: p})
	}
	return s
}

// flatten is the outline in the space m maps to, as polylines that stray from the curves by less than tol.
func flatten(paths []Subpath, m Affine, tol float64) (polys [][]Pt, closed []bool) {
	for _, s := range paths {
		if len(s.Nodes) == 0 {
			continue
		}
		pts := []Pt{m.Map(s.Nodes[0].P)}
		seg := func(a, b PathNode) {
			p0, c1, c2, p3 := m.Map(a.P), m.Map(a.Out), m.Map(b.In), m.Map(b.P)
			if c1 == p0 && c2 == p3 {
				pts = append(pts, p3)
				return
			}
			pts = flattenCubic(pts, p0, c1, c2, p3, tol)
		}
		for i := 1; i < len(s.Nodes); i++ {
			seg(s.Nodes[i-1], s.Nodes[i])
		}
		if s.Closed && len(s.Nodes) > 1 {
			seg(s.Nodes[len(s.Nodes)-1], s.Nodes[0])
			pts = pts[:len(pts)-1] // back at the first point
		}
		polys, closed = append(polys, pts), append(closed, s.Closed)
	}
	return polys, closed
}

// Bounds is the canvas area the object covers, its stroke included.
func (v *Vector) Bounds() Rect {
	polys, _ := flatten(v.Outline(), v.M, 0.25)
	var b Rect
	first := true
	for _, p := range polys {
		r := boundsOf(p)
		if first {
			b, first = r, false
			continue
		}
		b = b.Union(r)
	}
	if v.Stroke.Paint.Kind != PaintNone && v.Stroke.Width > 0 {
		grow := v.Stroke.Width * strokeReach(v.Stroke)
		b = Rect{b.Min.Sub(Pt{grow, grow}), b.Max.Add(Pt{grow, grow})}
	}
	return b
}

// strokeReach is how far, in widths, a stroke can reach past its outline: half a width, more at miters and square
// ends.
func strokeReach(s VectorStroke) float64 {
	reach := 0.5
	if s.Align == AlignOutside {
		reach = 1
	}
	if s.Cap == CapSquare {
		reach *= math.Sqrt2
	}
	if s.Join == JoinMiter {
		reach *= miterLimit(s)
	}
	return reach + 0.5
}

func miterLimit(s VectorStroke) float64 {
	if s.Miter <= 0 {
		return 4
	}
	return s.Miter
}

// PixelBounds is Bounds in whole pixels.
func (v *Vector) PixelBounds() image.Rectangle {
	b := v.Bounds()
	if b.Empty() && b.Min == b.Max {
		return image.Rectangle{}
	}
	return image.Rect(int(math.Floor(b.Min.X))-1, int(math.Floor(b.Min.Y))-1, int(math.Ceil(b.Max.X))+1, int(math.Ceil(b.Max.Y))+1)
}

// Render draws the object over transparency into dst within r, at a scale of the canvas (a detail level's, 2^−l):
// its fill, then its stroke over it. dst is premultiplied.
func (v *Vector) Render(dst *image.RGBA, r image.Rectangle, scale float64) {
	m := v.M.Then(Affine{A: scale, E: scale})
	inv, ok := m.Inverse()
	if !ok || r.Empty() {
		return
	}
	const tol, sub = 0.2, 8
	polys, closed := flatten(v.Outline(), m, tol)
	var fill []uint8
	needFill := v.Fill.Kind != PaintNone || v.Stroke.Align != AlignCentre
	if needFill {
		var shapes [][]Pt
		for i, p := range polys {
			if closed[i] || len(p) > 2 {
				shapes = append(shapes, p) // an open curve is filled as if closed, as in Affinity
			}
		}
		fill = coverageOf(shapes, v.Rule, r, sub)
	}
	if v.Fill.Kind != PaintNone {
		paintCoverage(dst, r, fill, v.Fill, inv)
	}
	s := v.Stroke
	if s.Paint.Kind == PaintNone || s.Width <= 0 {
		return
	}
	width := s.Width * scale
	allClosed := true
	for _, c := range closed {
		allClosed = allClosed && c
	}
	align := s.Align
	if !allClosed {
		align = AlignCentre // only a closed outline has an inside
	}
	if align != AlignCentre {
		width *= 2 // a doubled stroke, then the half inside or outside
	}
	var outline [][]Pt
	for i, p := range polys {
		for _, piece := range dashes(p, closed[i], s.Dash, width) {
			outline = append(outline, strokePolys(piece.pts, piece.closed, width, s.Cap, s.Join, miterLimit(s))...)
		}
	}
	stroke := coverageOf(outline, NonZero, r, sub)
	switch align {
	case AlignInside:
		for i := range stroke {
			stroke[i] = uint8(mul8(uint32(stroke[i]), uint32(fill[i])))
		}
	case AlignOutside:
		for i := range stroke {
			stroke[i] = uint8(mul8(uint32(stroke[i]), 255-uint32(fill[i])))
		}
	}
	paintCoverage(dst, r, stroke, s.Paint, inv)
}

// paintCoverage lays a paint over dst within r where cover says, normal blending; inv maps dst's space back to the
// object's own, where gradients are placed.
func paintCoverage(dst *image.RGBA, r image.Rectangle, cover []uint8, p VectorPaint, inv Affine) {
	w := r.Dx()
	solid := premul(p.Color)
	for y := r.Min.Y; y < r.Max.Y; y++ {
		row := cover[(y-r.Min.Y)*w : (y-r.Min.Y+1)*w]
		for x := r.Min.X; x < r.Max.X; x++ {
			k := uint32(row[x-r.Min.X])
			if k == 0 {
				continue
			}
			c := solid
			if p.Kind == PaintLinear || p.Kind == PaintRadial {
				c = premul(p.colorAt(inv.Map(Pt{float64(x) + 0.5, float64(y) + 0.5})))
			}
			i := dst.PixOffset(x, y)
			px := dst.Pix[i : i+4 : i+4]
			keep := 255 - mul8(c[3], k)
			for ch := range 4 {
				px[ch] = uint8(min(mul8(uint32(px[ch]), keep)+mul8(c[ch], k), 255))
			}
		}
	}
}

// colorAt is a gradient's colour at a point of the object's own space.
func (p VectorPaint) colorAt(q Pt) color.NRGBA {
	d := p.To.Sub(p.From)
	var t float64
	if p.Kind == PaintRadial {
		t = q.Dist(p.From) / max(d.Len(), 1e-9)
	} else {
		t = q.Sub(p.From).Dot(d) / max(d.Dot(d), 1e-9)
	}
	t = min(max(t, 0), 1)
	stops := p.Stops
	if len(stops) == 0 {
		return p.Color
	}
	if t <= stops[0].At {
		return stops[0].Color
	}
	for i := 1; i < len(stops); i++ {
		a, b := stops[i-1], stops[i]
		if t <= b.At {
			f := (t - a.At) / max(b.At-a.At, 1e-9)
			mix := func(x, y uint8) uint8 { return uint8(math.Round(float64(x) + (float64(y)-float64(x))*f)) }
			return color.NRGBA{mix(a.Color.R, b.Color.R), mix(a.Color.G, b.Color.G), mix(a.Color.B, b.Color.B), mix(a.Color.A, b.Color.A)}
		}
	}
	return stops[len(stops)-1].Color
}

// ---- Stroking: an outline turned into polygons, all wound the same way, so filling them non-zero joins them.

type piece struct {
	pts    []Pt
	closed bool
}

// dashes cuts a polyline into the dash pattern's on parts (lengths in widths), or keeps it whole without one.
func dashes(pts []Pt, closed bool, pattern []float64, width float64) []piece {
	total := 0.0
	for _, d := range pattern {
		total += max(d, 0)
	}
	if len(pattern) == 0 || total <= 0 || len(pts) < 2 {
		return []piece{{pts, closed}}
	}
	if closed {
		pts = append(append([]Pt(nil), pts...), pts[0])
	}
	var out []piece
	idx, left, on := 0, max(pattern[0], 0)*width, true
	cur := []Pt{pts[0]}
	for i := 1; i < len(pts); i++ {
		a, b := pts[i-1], pts[i]
		seg := b.Dist(a)
		for seg > 0 {
			if seg < left {
				left -= seg
				if on {
					cur = append(cur, b)
				}
				break
			}
			cut := a.Lerp(b, left/seg)
			if on {
				cur = append(cur, cut)
				out = append(out, piece{cur, false})
			}
			seg -= left
			a = cut
			on = !on
			idx = (idx + 1) % len(pattern)
			left = max(pattern[idx], 0.01) * width
			cur = []Pt{cut}
		}
	}
	if on && len(cur) > 1 {
		out = append(out, piece{cur, false})
	}
	return out
}

// positive returns a polygon wound so its area is positive, which every stroke piece shares.
func positive(p []Pt) []Pt {
	area := 0.0
	for i := range p {
		area += p[i].Cross(p[(i+1)%len(p)])
	}
	if area < 0 {
		for i, j := 0, len(p)-1; i < j; i, j = i+1, j-1 {
			p[i], p[j] = p[j], p[i]
		}
	}
	return p
}

// disc is a circle as a polygon fine enough to look round at its size.
func disc(c Pt, r float64) []Pt {
	n := 8
	if r > 0.25 {
		n = max(8, int(math.Ceil(math.Pi/math.Acos(max(1-0.1/r, -1)))))
	}
	n = min(n, 256)
	out := make([]Pt, n)
	for i := range n {
		a := 2 * math.Pi * float64(i) / float64(n)
		out[i] = Pt{c.X + r*math.Cos(a), c.Y + r*math.Sin(a)}
	}
	return out
}

// strokePolys is the outline of a stroke of width w along a polyline: a band for each segment, a wedge or disc at
// each corner and the ends' caps, each wound positively.
func strokePolys(in []Pt, closed bool, w float64, cap LineCap, join LineJoin, miter float64) [][]Pt {
	pts := make([]Pt, 0, len(in))
	for _, p := range in {
		if len(pts) == 0 || p.Dist(pts[len(pts)-1]) > 1e-9 {
			pts = append(pts, p)
		}
	}
	if closed && len(pts) > 1 && pts[0].Dist(pts[len(pts)-1]) <= 1e-9 {
		pts = pts[:len(pts)-1]
	}
	h := w / 2
	var out [][]Pt
	if len(pts) == 1 {
		switch cap {
		case CapRound:
			out = append(out, disc(pts[0], h))
		case CapSquare:
			p := pts[0]
			out = append(out, positive([]Pt{{p.X - h, p.Y - h}, {p.X + h, p.Y - h}, {p.X + h, p.Y + h}, {p.X - h, p.Y + h}}))
		}
		return out
	}
	n := len(pts) - 1
	if closed {
		n = len(pts)
	}
	for i := range n {
		a, b := pts[i], pts[(i+1)%len(pts)]
		d := b.Sub(a).Unit()
		nrm := d.Perp().Mul(h)
		if !closed {
			if i == 0 && cap == CapSquare {
				a = a.Sub(d.Mul(h))
			}
			if i == n-1 && cap == CapSquare {
				b = b.Add(d.Mul(h))
			}
		}
		out = append(out, positive([]Pt{a.Add(nrm), b.Add(nrm), b.Sub(nrm), a.Sub(nrm)}))
	}
	// Corners.
	corner := func(prev, v, next Pt) {
		d1, d2 := v.Sub(prev).Unit(), next.Sub(v).Unit()
		turn := d1.Cross(d2)
		if math.Abs(turn) < 1e-9 && d1.Dot(d2) > 0 {
			return // straight on
		}
		if join == JoinRound {
			out = append(out, disc(v, h))
			return
		}
		// The outer side is away from the turn.
		side := 1.0
		if turn > 0 {
			side = -1
		}
		n1, n2 := d1.Perp().Mul(h*side), d2.Perp().Mul(h*side)
		if join == JoinMiter {
			// Where the two outer edges meet.
			cos := d1.Dot(d2)
			if ml := 1 / math.Sqrt(max((1+cos)/2, 1e-12)); ml <= miter {
				bis := n1.Add(n2).Unit().Mul(h * ml)
				out = append(out, positive([]Pt{v, v.Add(n1), v.Add(bis), v.Add(n2)}))
				return
			}
		}
		out = append(out, positive([]Pt{v, v.Add(n1), v.Add(n2)}))
	}
	for i := 1; i < len(pts)-1; i++ {
		corner(pts[i-1], pts[i], pts[i+1])
	}
	if closed && len(pts) > 2 {
		corner(pts[len(pts)-2], pts[len(pts)-1], pts[0])
		corner(pts[len(pts)-1], pts[0], pts[1])
	}
	if !closed && cap == CapRound {
		out = append(out, disc(pts[0], h), disc(pts[len(pts)-1], h))
	}
	return out
}
