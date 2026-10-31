// Package render draws PDF pages to pictures and reads their text. Both come from one interpreter of the content
// stream, so what is selected is what is shown. Every limit a hostile file could push against is in here: a budget of
// operators, nesting depth, picture sizes, and the caller's context.
package render

import (
	"image"
	"math"
	"slices"

	"github.com/btelolabs/btelo-forge/internal/office/pdf"
	"github.com/btelolabs/btelo-forge/internal/office/pdf/font"
)

// point is a place in device pixels.
type point struct{ x, y float64 }

// poly is a path flattened to device space: closed polygons.
type poly struct {
	pts    []point
	starts []int // where each subpath begins in pts
}

func (p *poly) moveTo(x, y float64) {
	p.starts = append(p.starts, len(p.pts))
	p.pts = append(p.pts, point{x, y})
}

func (p *poly) lineTo(x, y float64) {
	if len(p.starts) == 0 {
		p.moveTo(x, y)
		return
	}
	p.pts = append(p.pts, point{x, y})
}

func (p *poly) empty() bool { return len(p.pts) == 0 }

func (p *poly) sub(i int) []point {
	end := len(p.pts)
	if i+1 < len(p.starts) {
		end = p.starts[i+1]
	}
	return p.pts[p.starts[i]:end]
}

// pathOp records a path in user space, so a stroke can work there.
type pathOp struct {
	op  byte // M L C Z
	pts [3]point
}

type path struct {
	ops []pathOp
	cx  point // the current point
	sx  point // where the subpath began
}

func (p *path) moveTo(x, y float64) {
	if n := len(p.ops); n > 0 && p.ops[n-1].op == 'M' {
		p.ops = p.ops[:n-1]
	}
	p.ops = append(p.ops, pathOp{op: 'M', pts: [3]point{{x, y}}})
	p.cx, p.sx = point{x, y}, point{x, y}
}

func (p *path) lineTo(x, y float64) {
	if len(p.ops) == 0 {
		p.moveTo(x, y)
		return
	}
	p.ops = append(p.ops, pathOp{op: 'L', pts: [3]point{{x, y}}})
	p.cx = point{x, y}
}

func (p *path) curveTo(x1, y1, x2, y2, x3, y3 float64) {
	if len(p.ops) == 0 {
		p.moveTo(x1, y1)
	}
	p.ops = append(p.ops, pathOp{op: 'C', pts: [3]point{{x1, y1}, {x2, y2}, {x3, y3}}})
	p.cx = point{x3, y3}
}

func (p *path) close() {
	if len(p.ops) == 0 {
		return
	}
	p.ops = append(p.ops, pathOp{op: 'Z'})
	p.cx = p.sx
}

func (p *path) rect(x, y, w, h float64) {
	p.moveTo(x, y)
	p.lineTo(x+w, y)
	p.lineTo(x+w, y+h)
	p.lineTo(x, y+h)
	p.close()
}

// flattenCurve adds a cubic's points after a, tolerance in device pixels.
func flattenCurve(a, b, c, d point, add func(point)) {
	dd := math.Max(math.Hypot(a.x-2*b.x+c.x, a.y-2*b.y+c.y), math.Hypot(b.x-2*c.x+d.x, b.y-2*c.y+d.y))
	n := int(math.Ceil(math.Sqrt(dd * 3 / 4 / 0.15)))
	n = min(max(n, 1), 96)
	for i := 1; i <= n; i++ {
		t := float64(i) / float64(n)
		s := 1 - t
		w0, w1, w2, w3 := s*s*s, 3*s*s*t, 3*s*t*t, t*t*t
		add(point{w0*a.x + w1*b.x + w2*c.x + w3*d.x, w0*a.y + w1*b.y + w2*c.y + w3*d.y})
	}
}

// flatten turns a path into device polygons.
func (p *path) flatten(m pdf.Matrix) *poly {
	out := &poly{}
	dev := func(q point) point {
		x, y := m.Apply(q.x, q.y)
		return point{x, y}
	}
	var cur point
	for _, op := range p.ops {
		switch op.op {
		case 'M':
			cur = dev(op.pts[0])
			out.moveTo(cur.x, cur.y)
		case 'L':
			cur = dev(op.pts[0])
			out.lineTo(cur.x, cur.y)
		case 'C':
			b, c, d := dev(op.pts[0]), dev(op.pts[1]), dev(op.pts[2])
			flattenCurve(cur, b, c, d, func(q point) { out.lineTo(q.x, q.y) })
			cur = d
		case 'Z':
			if n := len(out.starts); n > 0 {
				s := out.pts[out.starts[n-1]]
				out.moveTo(s.x, s.y) // what follows without a moveto starts here
				cur = s
			}
		}
	}
	return out
}

// mask is coverage over a rectangle of device pixels.
type mask struct {
	r   image.Rectangle
	pix []uint8
}

func (m *mask) at(x, y int) uint8 {
	if !(image.Point{x, y}).In(m.r) {
		return 0
	}
	return m.pix[(y-m.r.Min.Y)*m.r.Dx()+x-m.r.Min.X]
}

type edge struct {
	x0, y0, x1, y1 float64 // y0 < y1
	dir            int
}

// raster holds the rasteriser's scratch space.
type raster struct {
	edges  []edge
	active []int
	xs     []crossing
	acc    []float32
	diff   []float32
}

type crossing struct {
	x   float64
	dir int
}

// fill scan converts polygons within bounds with sub lines a pixel. It keeps exact horizontal coverage.
func (ra *raster) fill(p *poly, evenOdd bool, bounds image.Rectangle, sub int) *mask {
	if p.empty() || bounds.Empty() {
		return nil
	}
	minX, minY, maxX, maxY := math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)
	for _, q := range p.pts {
		minX, maxX = math.Min(minX, q.x), math.Max(maxX, q.x)
		minY, maxY = math.Min(minY, q.y), math.Max(maxY, q.y)
	}
	if math.IsNaN(minX) || math.IsNaN(minY) || math.IsInf(minX, 0) || math.IsInf(maxY, 0) || math.IsInf(maxX, 0) || math.IsInf(minY, 0) {
		return nil
	}
	if minX > float64(bounds.Max.X) || maxX < float64(bounds.Min.X) || minY > float64(bounds.Max.Y) || maxY < float64(bounds.Min.Y) {
		return nil
	}
	box := image.Rect(int(math.Floor(max(minX, float64(bounds.Min.X)))), int(math.Floor(max(minY, float64(bounds.Min.Y)))),
		int(math.Ceil(min(maxX, float64(bounds.Max.X))))+1, int(math.Ceil(min(maxY, float64(bounds.Max.Y))))+1).Intersect(bounds)
	if box.Empty() {
		return nil
	}
	ra.edges = ra.edges[:0]
	for i := range p.starts {
		pts := p.sub(i)
		if len(pts) < 2 {
			continue
		}
		a := pts[len(pts)-1]
		for _, b := range pts {
			if a.y != b.y {
				e := edge{a.x, a.y, b.x, b.y, 1}
				if a.y > b.y {
					e = edge{b.x, b.y, a.x, a.y, -1}
				}
				if e.y1 > float64(box.Min.Y) && e.y0 < float64(box.Max.Y) {
					ra.edges = append(ra.edges, e)
				}
			}
			a = b
		}
	}
	if len(ra.edges) == 0 {
		return nil
	}
	slices.SortFunc(ra.edges, func(a, b edge) int {
		switch {
		case a.y0 < b.y0:
			return -1
		case a.y0 > b.y0:
			return 1
		}
		return 0
	})
	w, h := box.Dx(), box.Dy()
	out := &mask{r: box, pix: make([]uint8, w*h)}
	if len(ra.acc) < w+2 {
		ra.acc = make([]float32, w+2)
		ra.diff = make([]float32, w+2)
	}
	acc, diff := ra.acc[:w+2], ra.diff[:w+2]
	ra.active = ra.active[:0]
	next := 0
	step := 1 / float64(sub)
	x0 := float64(box.Min.X)
	norm := float32(255) / float32(sub)
	fw := float64(w)
	for py := 0; py < h; py++ {
		row := out.pix[py*w : py*w+w]
		touched := false
		for s := 0; s < sub; s++ {
			y := float64(box.Min.Y+py) + (float64(s)+0.5)*step
			for next < len(ra.edges) && ra.edges[next].y0 <= y {
				ra.active = append(ra.active, next)
				next++
			}
			k := 0
			ra.xs = ra.xs[:0]
			for _, i := range ra.active {
				e := &ra.edges[i]
				if e.y1 <= y {
					continue
				}
				ra.active[k] = i
				k++
				ra.xs = append(ra.xs, crossing{e.x0 + (y-e.y0)*(e.x1-e.x0)/(e.y1-e.y0), e.dir})
			}
			ra.active = ra.active[:k]
			if len(ra.xs) < 2 {
				continue
			}
			slices.SortFunc(ra.xs, func(a, b crossing) int {
				switch {
				case a.x < b.x:
					return -1
				case a.x > b.x:
					return 1
				}
				return 0
			})
			wind := 0
			for j := 0; j+1 < len(ra.xs); j++ {
				if evenOdd {
					wind ^= 1
				} else {
					wind += ra.xs[j].dir
				}
				if wind == 0 {
					continue
				}
				a, b := min(max(ra.xs[j].x-x0, 0), fw), min(max(ra.xs[j+1].x-x0, 0), fw)
				if b <= a {
					continue
				}
				touched = true
				ia, ib := int(a), int(b)
				if ia == ib {
					acc[ia] += float32(b - a)
					continue
				}
				acc[ia] += float32(float64(ia+1) - a)
				acc[ib] += float32(b - float64(ib))
				diff[ia+1]++
				diff[ib]--
			}
		}
		if touched {
			run := float32(0)
			for x := 0; x < w; x++ {
				run += diff[x]
				v := (acc[x] + run) * norm
				if v >= 254.5 {
					row[x] = 255
				} else {
					row[x] = uint8(v + 0.5)
				}
				acc[x], diff[x] = 0, 0
			}
			acc[w], diff[w], acc[w+1], diff[w+1] = 0, 0, 0, 0
		}
	}
	return out
}

// strokeStyle is what the graphics state says about lines.
type strokeStyle struct {
	width     float64
	cap, join int
	miter     float64
	dash      []float64
	phase     float64
}

// stroke turns a user space path into device polygons covering its stroke; every piece winds the same way, so a
// non-zero fill unites them. The line is offset in user space, where it has its width; a thin line stays one
// device pixel wide.
func stroke(p *path, st *strokeStyle, ctm pdf.Matrix) *poly {
	det := math.Abs(ctm[0]*ctm[3] - ctm[1]*ctm[2])
	if det == 0 {
		return &poly{}
	}
	scale := math.Sqrt(det)
	width := st.width
	if width*scale < 1 {
		width = 1 / scale
	}
	// Curves flatten in device space for accuracy, then come back to user space to be offset.
	inv, ok := ctm.Invert()
	if !ok {
		return &poly{}
	}
	dev := p.flatten(ctm)
	subs := make([][]point, 0, len(dev.starts))
	closed := closedFlags(p)
	for i := range dev.starts {
		pts := dev.sub(i)
		user := make([]point, 0, len(pts))
		for _, q := range pts {
			x, y := inv.Apply(q.x, q.y)
			if n := len(user); n > 0 && user[n-1].x == x && user[n-1].y == y {
				continue
			}
			user = append(user, point{x, y})
		}
		subs = append(subs, user)
	}
	out := &poly{}
	s := stroker{out: out, ctm: ctm, half: width / 2, st: st, flip: ctm[0]*ctm[3]-ctm[1]*ctm[2] < 0, curveSteps: curveSteps(width / 2 * scale)}
	for i, pts := range subs {
		isClosed := i < len(closed) && closed[i]
		if isClosed && len(pts) > 1 && pts[0] == pts[len(pts)-1] {
			pts = pts[:len(pts)-1]
		}
		if len(st.dash) > 0 {
			for _, seg := range dashed(pts, isClosed, st) {
				s.polyline(seg, false)
			}
			continue
		}
		s.polyline(pts, isClosed)
	}
	return out
}

func curveSteps(r float64) int { return min(max(int(math.Ceil(math.Sqrt(r)*2.5)), 4), 48) }

// closedFlags says which of the subpaths flatten makes end in a close.
func closedFlags(p *path) []bool {
	var out []bool
	for i, op := range p.ops {
		switch op.op {
		case 'M':
			out = append(out, false)
		case 'Z':
			if len(out) > 0 {
				out[len(out)-1] = true
			}
			if i+1 < len(p.ops) && p.ops[i+1].op != 'M' || i+1 == len(p.ops) {
				out = append(out, false) // flatten starts a subpath at the closing point
			}
		}
	}
	return out
}

func dashed(pts []point, closed bool, st *strokeStyle) [][]point {
	total := 0.0
	for _, d := range st.dash {
		total += math.Max(d, 0)
	}
	if total <= 0 || len(pts) < 2 {
		return [][]point{pts}
	}
	if closed {
		pts = append(slices.Clone(pts), pts[0])
	}
	length := 0.0
	for i := 1; i < len(pts); i++ {
		length += math.Hypot(pts[i].x-pts[i-1].x, pts[i].y-pts[i-1].y)
	}
	if length/total*float64(len(st.dash)) > 200000 {
		return [][]point{pts} // a dash finer than anything shown
	}
	idx, on, left := 0, true, st.dash[0]
	phase := math.Mod(st.phase, total*2)
	if phase < 0 {
		phase += total * 2
	}
	for phase > 0 {
		if phase >= left {
			phase -= left
			idx = (idx + 1) % len(st.dash)
			left = st.dash[idx]
			on = !on
			if left <= 0 && phase <= 0 {
				break
			}
		} else {
			left -= phase
			phase = 0
		}
	}
	var out [][]point
	cur := []point{}
	if on {
		cur = append(cur, pts[0])
	}
	for i := 1; i < len(pts); i++ {
		a, b := pts[i-1], pts[i]
		l := math.Hypot(b.x-a.x, b.y-a.y)
		pos := 0.0
		for l-pos > left {
			pos += left
			t := pos / l
			q := point{a.x + (b.x-a.x)*t, a.y + (b.y-a.y)*t}
			if on {
				out = append(out, append(cur, q))
				cur = nil
			} else {
				cur = []point{q}
			}
			on = !on
			idx = (idx + 1) % len(st.dash)
			left = math.Max(st.dash[idx], 1e-9)
		}
		left -= l - pos
		if on {
			cur = append(cur, b)
		}
	}
	if on && len(cur) > 0 {
		out = append(out, cur)
	}
	return out
}

type stroker struct {
	out        *poly
	ctm        pdf.Matrix
	half       float64
	st         *strokeStyle
	flip       bool
	curveSteps int
}

// emit adds a user space polygon in device space, wound counter-clockwise in device terms.
func (s *stroker) emit(pts ...point) {
	dev := make([]point, len(pts))
	area := 0.0
	for i, q := range pts {
		x, y := s.ctm.Apply(q.x, q.y)
		dev[i] = point{x, y}
	}
	for i := range dev {
		j := (i + 1) % len(dev)
		area += dev[i].x*dev[j].y - dev[j].x*dev[i].y
	}
	if area < 0 {
		slices.Reverse(dev)
	}
	s.out.moveTo(dev[0].x, dev[0].y)
	for _, q := range dev[1:] {
		s.out.lineTo(q.x, q.y)
	}
}

func (s *stroker) disc(c point) {
	n := s.curveSteps * 2
	pts := make([]point, n)
	for i := range n {
		a := 2 * math.Pi * float64(i) / float64(n)
		pts[i] = point{c.x + s.half*math.Cos(a), c.y + s.half*math.Sin(a)}
	}
	s.emit(pts...)
}

func (s *stroker) polyline(pts []point, closed bool) {
	if len(pts) == 0 {
		return
	}
	if len(pts) == 1 {
		// A zero length line shows only through its caps.
		c := pts[0]
		switch s.st.cap {
		case 1:
			s.disc(c)
		case 2:
			h := s.half
			s.emit(point{c.x - h, c.y - h}, point{c.x + h, c.y - h}, point{c.x + h, c.y + h}, point{c.x - h, c.y + h})
		}
		return
	}
	n := len(pts)
	segs := n - 1
	if closed {
		segs = n
	}
	for i := range segs {
		a, b := pts[i], pts[(i+1)%n]
		dx, dy := b.x-a.x, b.y-a.y
		l := math.Hypot(dx, dy)
		if l == 0 {
			continue
		}
		nx, ny := -dy/l*s.half, dx/l*s.half
		sa, sb := a, b
		if !closed && s.st.cap == 2 {
			if i == 0 {
				sa = point{a.x - dx/l*s.half, a.y - dy/l*s.half}
			}
			if i == segs-1 {
				sb = point{b.x + dx/l*s.half, b.y + dy/l*s.half}
			}
		}
		s.emit(point{sa.x + nx, sa.y + ny}, point{sb.x + nx, sb.y + ny}, point{sb.x - nx, sb.y - ny}, point{sa.x - nx, sa.y - ny})
		if closed || i < segs-1 {
			s.joinAt(a, b, pts[(i+2)%n])
		}
	}
	if !closed && s.st.cap == 1 {
		s.disc(pts[0])
		s.disc(pts[n-1])
	}
}

func (s *stroker) joinAt(a, b, c point) {
	ux, uy := b.x-a.x, b.y-a.y
	vx, vy := c.x-b.x, c.y-b.y
	lu, lv := math.Hypot(ux, uy), math.Hypot(vx, vy)
	if lu == 0 || lv == 0 {
		return
	}
	ux, uy, vx, vy = ux/lu, uy/lu, vx/lv, vy/lv
	cross := ux*vy - uy*vx
	dot := ux*vx + uy*vy
	if math.Abs(cross) < 1e-9 && dot > 0 {
		return
	}
	if s.st.join == 1 {
		s.disc(b)
		return
	}
	// The outer side of the turn.
	sign := 1.0
	if cross > 0 {
		sign = -1
	}
	p1 := point{b.x - uy*s.half*sign, b.y + ux*s.half*sign}
	p2 := point{b.x - vy*s.half*sign, b.y + vx*s.half*sign}
	if s.st.join == 0 {
		// The miter ratio is 1/sin(angle/2); cos of the turn gives it without the angle.
		ratio := 1 / math.Max(math.Sqrt(math.Max((1+dot)/2, 0)), 1e-9)
		if ratio <= s.st.miter {
			tip := point{b.x + (p1.x-b.x+p2.x-b.x)/(1+dot), b.y + (p1.y-b.y+p2.y-b.y)/(1+dot)}
			s.emit(b, p1, tip, p2)
			return
		}
	}
	s.emit(b, p1, p2)
}

// outlinePath converts a font outline with a transform to device polygons.
func outlinePath(o font.Path, m pdf.Matrix, out *poly) {
	var cur point
	dev := func(x, y float64) point {
		a, b := m.Apply(x, y)
		return point{a, b}
	}
	for _, s := range o {
		switch s.Op {
		case 'M':
			cur = dev(s.X, s.Y)
			out.moveTo(cur.x, cur.y)
		case 'L':
			cur = dev(s.X, s.Y)
			out.lineTo(cur.x, cur.y)
		case 'Q':
			c, e := dev(s.X1, s.Y1), dev(s.X, s.Y)
			flattenCurve(cur, point{cur.x + 2.0/3*(c.x-cur.x), cur.y + 2.0/3*(c.y-cur.y)},
				point{e.x + 2.0/3*(c.x-e.x), e.y + 2.0/3*(c.y-e.y)}, e, func(q point) { out.lineTo(q.x, q.y) })
			cur = e
		case 'C':
			b, c, e := dev(s.X1, s.Y1), dev(s.X2, s.Y2), dev(s.X, s.Y)
			flattenCurve(cur, b, c, e, func(q point) { out.lineTo(q.x, q.y) })
			cur = e
		}
	}
}
