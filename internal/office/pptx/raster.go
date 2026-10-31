package pptx

import (
	"image"
	"image/color"
	"math"

	"golang.org/x/image/vector"
)

// pt is a point on the canvas.
type pt struct{ x, y float64 }

// polyline is a run of points; closed joins its end to its start.
type polyline struct {
	pts    []pt
	closed bool
}

// flatten turns paths into polylines through m. Curves are divided to within a quarter of a pixel.
func flatten(ops []PathOp, m Matrix) []polyline {
	var out []polyline
	var cur []pt
	flush := func(closed bool) {
		if len(cur) > 1 || closed && len(cur) > 0 {
			out = append(out, polyline{cur, closed})
		}
		cur = nil
	}
	var start, at pt
	for i := range ops {
		op := &ops[i]
		switch op.Code {
		case 'M':
			flush(false)
			x, y := m.Apply(op.X[0], op.Y[0])
			start, at = pt{x, y}, pt{x, y}
			cur = append(cur, at)
		case 'L':
			x, y := m.Apply(op.X[0], op.Y[0])
			at = pt{x, y}
			cur = append(cur, at)
		case 'C':
			x1, y1 := m.Apply(op.X[0], op.Y[0])
			x2, y2 := m.Apply(op.X[1], op.Y[1])
			x3, y3 := m.Apply(op.X[2], op.Y[2])
			d := math.Hypot(x1-at.x, y1-at.y) + math.Hypot(x2-x1, y2-y1) + math.Hypot(x3-x2, y3-y2)
			n := min(max(int(math.Sqrt(d)*1.5), 2), 256)
			for k := 1; k <= n; k++ {
				t := float64(k) / float64(n)
				u := 1 - t
				a, b, c, e := u*u*u, 3*u*u*t, 3*u*t*t, t*t*t
				cur = append(cur, pt{a*at.x + b*x1 + c*x2 + e*x3, a*at.y + b*y1 + c*y2 + e*y3})
			}
			at = pt{x3, y3}
		case 'Z':
			flush(true)
			at = start
			cur = append(cur, at)
		}
	}
	flush(false)
	return out
}

// maskArea is a coverage mask covering part of the canvas.
type maskArea struct {
	img *image.Alpha
	at  image.Point
}

// pixelBounds is the box around the polylines, grown by pad, on the canvas.
func pixelBounds(lines []polyline, pad float64, clip image.Rectangle) image.Rectangle {
	x0, y0, x1, y1 := math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)
	for _, l := range lines {
		for _, p := range l.pts {
			x0, y0, x1, y1 = min(x0, p.x), min(y0, p.y), max(x1, p.x), max(y1, p.y)
		}
	}
	if x0 > x1 || math.IsNaN(x0+x1+y0+y1) {
		return image.Rectangle{}
	}
	// The floats may be far outside what an int holds in a hostile file.
	lim := func(v float64) int { return int(clampF(v, -1<<28, 1<<28)) }
	return image.Rect(lim(math.Floor(x0-pad)), lim(math.Floor(y0-pad)), lim(math.Ceil(x1+pad))+1, lim(math.Ceil(y1+pad))+1).Intersect(clip)
}

// fillMask rasterises the outlines.
func fillMask(lines []polyline, clip image.Rectangle) maskArea {
	b := pixelBounds(lines, 1, clip)
	if b.Empty() {
		return maskArea{}
	}
	z := vector.NewRasterizer(b.Dx(), b.Dy())
	ox, oy := float64(b.Min.X), float64(b.Min.Y)
	for _, l := range lines {
		addPoly(z, l.pts, ox, oy)
	}
	return drawMask(z, b)
}

func addPoly(z *vector.Rasterizer, pts []pt, ox, oy float64) {
	if len(pts) < 2 {
		return
	}
	z.MoveTo(float32(pts[0].x-ox), float32(pts[0].y-oy))
	for _, p := range pts[1:] {
		z.LineTo(float32(p.x-ox), float32(p.y-oy))
	}
	z.ClosePath()
}

func drawMask(z *vector.Rasterizer, b image.Rectangle) maskArea {
	m := image.NewAlpha(image.Rect(0, 0, b.Dx(), b.Dy()))
	z.Draw(m, m.Rect, image.Opaque, image.Point{})
	return maskArea{m, b.Min}
}

// strokeStyle is how a line is drawn, in pixels.
type strokeStyle struct {
	width float64
	dash  []float64 // on, off, … in pixels
	cap   string    // "rnd", "sq" or "flat"
	join  string    // "round", "bevel" or "miter"
	miter float64
}

// stroker collects the convex pieces of a stroke. The rasteriser adds up signed areas, so every piece is wound the
// same way, and overlaps stay solid.
type stroker struct {
	z      *vector.Rasterizer
	ox, oy float64
}

func (s *stroker) piece(pts ...pt) {
	area := 0.0
	for i := range pts {
		j := (i + 1) % len(pts)
		area += pts[i].x*pts[j].y - pts[j].x*pts[i].y
	}
	if area < 0 {
		for i, j := 0, len(pts)-1; i < j; i, j = i+1, j-1 {
			pts[i], pts[j] = pts[j], pts[i]
		}
	}
	addPoly(s.z, pts, s.ox, s.oy)
}

func (s *stroker) disc(c pt, r float64) {
	n := min(max(int(r*2)+8, 8), 48)
	pts := make([]pt, n)
	for i := range pts {
		a := float64(i) * 2 * math.Pi / float64(n)
		pts[i] = pt{c.x + r*math.Cos(a), c.y + r*math.Sin(a)}
	}
	s.piece(pts...)
}

func (s *stroker) segment(a, b pt, hw float64) {
	dx, dy := b.x-a.x, b.y-a.y
	l := math.Hypot(dx, dy)
	if l == 0 {
		return
	}
	nx, ny := -dy/l*hw, dx/l*hw
	s.piece(pt{a.x + nx, a.y + ny}, pt{b.x + nx, b.y + ny}, pt{b.x - nx, b.y - ny}, pt{a.x - nx, a.y - ny})
}

func (s *stroker) join(a, b, c pt, hw float64, st *strokeStyle) {
	d0x, d0y := b.x-a.x, b.y-a.y
	d1x, d1y := c.x-b.x, c.y-b.y
	l0, l1 := math.Hypot(d0x, d0y), math.Hypot(d1x, d1y)
	if l0 == 0 || l1 == 0 {
		return
	}
	d0x, d0y, d1x, d1y = d0x/l0, d0y/l0, d1x/l1, d1y/l1
	cross := d0x*d1y - d0y*d1x
	if math.Abs(cross) < 1e-6 && d0x*d1x+d0y*d1y > 0 {
		return
	}
	if st.join == "round" || hw < 1 {
		s.disc(b, hw)
		return
	}
	side := 1.0
	if cross > 0 {
		side = -1
	}
	p0 := pt{b.x - d0y*hw*side, b.y + d0x*hw*side}
	p1 := pt{b.x - d1y*hw*side, b.y + d1x*hw*side}
	if st.join == "miter" {
		cosHalf := math.Sqrt(max((1+(d0x*d1x+d0y*d1y))/2, 0))
		if cosHalf > 1e-3 && 1/cosHalf <= st.miter {
			mx, my := (p0.x+p1.x)/2-b.x, (p0.y+p1.y)/2-b.y
			ml := math.Hypot(mx, my)
			if ml > 0 {
				k := hw / cosHalf / ml
				s.piece(b, p0, pt{b.x + mx*k, b.y + my*k}, p1)
				return
			}
		}
	}
	s.piece(b, p0, p1)
}

func (s *stroker) cap(end, inner pt, hw float64, kind string) {
	switch kind {
	case "rnd":
		s.disc(end, hw)
	case "sq":
		dx, dy := end.x-inner.x, end.y-inner.y
		l := math.Hypot(dx, dy)
		if l > 0 {
			s.segment(end, pt{end.x + dx/l*hw, end.y + dy/l*hw}, hw)
		}
	}
}

func (s *stroker) line(l polyline, st *strokeStyle) {
	pts := dedupe(l.pts, l.closed)
	hw := st.width / 2
	n := len(pts)
	if n == 0 {
		return
	}
	if n == 1 {
		if st.cap == "rnd" {
			s.disc(pts[0], hw)
		}
		return
	}
	for i := 0; i+1 < n; i++ {
		s.segment(pts[i], pts[i+1], hw)
	}
	for i := 1; i+1 < n; i++ {
		s.join(pts[i-1], pts[i], pts[i+1], hw, st)
	}
	if l.closed && n > 2 {
		s.segment(pts[n-1], pts[0], hw)
		s.join(pts[n-2], pts[n-1], pts[0], hw, st)
		s.join(pts[n-1], pts[0], pts[1], hw, st)
		return
	}
	s.cap(pts[0], pts[1], hw, st.cap)
	s.cap(pts[n-1], pts[n-2], hw, st.cap)
}

func dedupe(in []pt, closed bool) []pt {
	out := make([]pt, 0, len(in))
	for _, p := range in {
		if len(out) == 0 || math.Hypot(p.x-out[len(out)-1].x, p.y-out[len(out)-1].y) > 1e-3 {
			out = append(out, p)
		}
	}
	if closed && len(out) > 1 && math.Hypot(out[0].x-out[len(out)-1].x, out[0].y-out[len(out)-1].y) <= 1e-3 {
		out = out[:len(out)-1]
	}
	return out
}

// dashed cuts polylines into the pattern's dashes.
func dashed(lines []polyline, pattern []float64) []polyline {
	total := 0.0
	for _, v := range pattern {
		total += v
	}
	if total <= 0 {
		return lines
	}
	var out []polyline
	const maxDashes = 200000
	for _, l := range lines {
		pts := l.pts
		if l.closed && len(pts) > 0 {
			pts = append(pts[:len(pts):len(pts)], pts[0])
		}
		idx, left, on := 0, pattern[0], true
		var cur []pt
		for i := 0; i+1 < len(pts) && len(out) < maxDashes; i++ {
			a, b := pts[i], pts[i+1]
			seg := math.Hypot(b.x-a.x, b.y-a.y)
			if seg == 0 {
				continue
			}
			pos := 0.0
			if on && len(cur) == 0 {
				cur = append(cur, a)
			}
			for seg-pos > left && len(out) < maxDashes {
				pos += left
				p := pt{a.x + (b.x-a.x)*pos/seg, a.y + (b.y-a.y)*pos/seg}
				if on {
					cur = append(cur, p)
					out = append(out, polyline{pts: cur})
					cur = nil
				} else {
					cur = []pt{p}
				}
				on = !on
				idx = (idx + 1) % len(pattern)
				left = pattern[idx]
				if left <= 0 {
					left = 1e-3
				}
			}
			left -= seg - pos
			if on {
				cur = append(cur, b)
			}
		}
		if len(cur) > 1 {
			out = append(out, polyline{pts: cur})
		}
	}
	return out
}

// strokeMask rasterises a stroke of the polylines.
func strokeMask(lines []polyline, st *strokeStyle, clip image.Rectangle) maskArea {
	if len(st.dash) > 0 {
		lines = dashed(lines, st.dash)
	}
	pad := st.width*max(st.miter, 1)/2 + 2
	b := pixelBounds(lines, pad, clip)
	if b.Empty() {
		return maskArea{}
	}
	z := vector.NewRasterizer(b.Dx(), b.Dy())
	s := &stroker{z: z, ox: float64(b.Min.X), oy: float64(b.Min.Y)}
	for _, l := range lines {
		s.line(l, st)
	}
	return drawMask(z, b)
}

// paint gives the premultiplied colour at a pixel; a nil paint is a plain colour.
type paintFn func(x, y int) color.RGBA

// composite lays a colour or a paint through a mask, with the same arithmetic as gfx's own blending.
func composite(dst *image.RGBA, m maskArea, col color.RGBA, fn paintFn, opacity float64) {
	if m.img == nil {
		return
	}
	b := m.img.Rect.Add(m.at).Intersect(dst.Rect)
	if b.Empty() {
		return
	}
	// Premultiply once for a plain colour.
	ca := uint32(col.A)
	pr, pg, pb := uint32(col.R)*ca/255, uint32(col.G)*ca/255, uint32(col.B)*ca/255
	op := uint32(clampF(opacity, 0, 1)*256 + 0.5)
	for y := b.Min.Y; y < b.Max.Y; y++ {
		row := m.img.Pix[(y-m.at.Y)*m.img.Stride+(b.Min.X-m.at.X):]
		out := dst.Pix[dst.PixOffset(b.Min.X, y):]
		for i := 0; i < b.Dx(); i++ {
			cov := uint32(row[i])
			if cov == 0 {
				continue
			}
			r, g, bl, a := pr, pg, pb, ca
			if fn != nil {
				c := fn(b.Min.X+i, y)
				r, g, bl, a = uint32(c.R), uint32(c.G), uint32(c.B), uint32(c.A)
			}
			if op != 256 {
				cov = cov * op >> 8
			}
			// Scale by coverage, then src over.
			r, g, bl, a = r*cov/255, g*cov/255, bl*cov/255, a*cov/255
			inv := 255 - a
			p := out[i*4 : i*4+4 : i*4+4]
			p[0] = uint8(r + (uint32(p[0])*inv+127)/255)
			p[1] = uint8(g + (uint32(p[1])*inv+127)/255)
			p[2] = uint8(bl + (uint32(p[2])*inv+127)/255)
			p[3] = uint8(a + (uint32(p[3])*inv+127)/255)
		}
	}
}

// blur softens a mask with three box passes, which come close to a Gaussian of the radius r in pixels.
func blur(m *image.Alpha, r float64) {
	rad := int(r/2 + 0.5)
	if rad < 1 {
		return
	}
	w, h := m.Rect.Dx(), m.Rect.Dy()
	tmp := make([]uint8, max(w, h))
	for pass := 0; pass < 3; pass++ {
		for y := 0; y < h; y++ {
			boxLine(m.Pix[y*m.Stride:y*m.Stride+w], 1, w, rad, tmp)
		}
		for x := 0; x < w; x++ {
			boxLine(m.Pix[x:], m.Stride, h, rad, tmp)
		}
	}
}

func boxLine(p []uint8, stride, n, rad int, tmp []uint8) {
	sum := 0
	win := 2*rad + 1
	at := func(i int) int { return int(p[min(max(i, 0), n-1)*stride]) }
	for i := -rad; i <= rad; i++ {
		sum += at(i)
	}
	for i := 0; i < n; i++ {
		tmp[i] = uint8(sum / win)
		sum += at(i+rad+1) - at(i-rad)
	}
	for i := 0; i < n; i++ {
		p[i*stride] = tmp[i]
	}
}
