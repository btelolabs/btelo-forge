package mind

import (
	"image"
	"image/color"
	"image/draw"
	"math"
	"sync"

	"golang.org/x/image/vector"

	"github.com/btelolabs/btelo-forge/internal/gfx"
)

// Drawing what gfx has no shape for: filled outlines, lines, bands and arrowheads. Every one is a path filled with
// the exact share of each pixel it covers (golang.org/x/image/vector), so curves and slopes are as smooth as text,
// and a line, its joints and its round ends are one shape, without seams where pieces meet. Only the part of a
// path's box inside the clip is rasterized, so a path costs what it covers on the screen.

type pt struct{ x, y float32 }

// rasterizers are kept for reuse: each holds a buffer the size of what it last drew.
var rasterizers = sync.Pool{New: func() any { return vector.NewRasterizer(0, 0) }}

// fillPath fills the closed contours with col, by the non-zero rule: a contour going the other way cuts a hole.
func fillPath(c *gfx.Canvas, contours [][]pt, col color.RGBA) {
	if col.A == 0 {
		return
	}
	if rec := c.Recorder(); rec != nil {
		out := make([][][2]float32, len(contours))
		for i, ct := range contours {
			out[i] = make([][2]float32, len(ct))
			for j, p := range ct {
				out[i][j] = [2]float32{p.x, p.y}
			}
		}
		rec.Polygon(out, col)
		return
	}
	box := image.Rectangle{}
	first := true
	for _, ct := range contours {
		for _, p := range ct {
			q := image.Rect(int(math.Floor(float64(p.x))), int(math.Floor(float64(p.y))), int(math.Ceil(float64(p.x)))+1, int(math.Ceil(float64(p.y)))+1)
			if first {
				box, first = q, false
			} else {
				box = box.Union(q)
			}
		}
	}
	box = box.Intersect(c.Clip())
	if box.Empty() {
		return
	}
	z := rasterizers.Get().(*vector.Rasterizer)
	defer rasterizers.Put(z)
	z.Reset(box.Dx(), box.Dy())
	z.DrawOp = draw.Over
	ox, oy := float32(box.Min.X), float32(box.Min.Y)
	for _, ct := range contours {
		if len(ct) < 3 {
			continue
		}
		z.MoveTo(ct[0].x-ox, ct[0].y-oy)
		for _, p := range ct[1:] {
			z.LineTo(p.x-ox, p.y-oy)
		}
		z.ClosePath()
	}
	z.Draw(c.Img, box, image.NewUniform(color.NRGBA{col.R, col.G, col.B, col.A}), image.Point{})
}

// fillPoly fills a closed polygon with col.
func fillPoly(c *gfx.Canvas, poly []pt, col color.RGBA) { fillPath(c, [][]pt{poly}, col) }

// capSteps is how many pieces a round end is made of.
const capSteps = 10

// band is the outline of a line through pts whose width at the share t of the way along it is width(t), with
// round ends: its sides follow the line and meet at each corner in a mitre (cut off past twice the width), and a
// half circle closes each end.
func band(pts []pt, width func(t float32) float32) []pt {
	// Points that do not move give no direction: they are dropped.
	clean := make([]pt, 0, len(pts))
	for _, p := range pts {
		if len(clean) == 0 || math.Hypot(float64(p.x-clean[len(clean)-1].x), float64(p.y-clean[len(clean)-1].y)) > 0.01 {
			clean = append(clean, p)
		}
	}
	if len(clean) == 0 {
		return nil
	}
	if len(clean) == 1 {
		return circlePoly(clean[0], width(0)/2)
	}
	pts = clean
	n := len(pts)
	dir := func(i int) (float32, float32) {
		dx, dy := pts[i+1].x-pts[i].x, pts[i+1].y-pts[i].y
		l := float32(math.Hypot(float64(dx), float64(dy)))
		return dx / l, dy / l
	}
	half := func(i int) float32 { return width(float32(i)/float32(n-1)) / 2 }
	left, right := make([]pt, n), make([]pt, n)
	for i := range pts {
		var nx, ny float32
		switch {
		case i == 0:
			dx, dy := dir(0)
			nx, ny = -dy, dx
		case i == n-1:
			dx, dy := dir(n - 2)
			nx, ny = -dy, dx
		default:
			ax, ay := dir(i - 1)
			bx, by := dir(i)
			mx, my := -ay-by, ax+bx
			l := float32(math.Hypot(float64(mx), float64(my)))
			if l < 1e-4 {
				nx, ny = -ay, ax
			} else {
				mx, my = mx/l, my/l
				// The mitre is longer than the half width by 1/cos of half the turn, up to twice.
				cos := mx*(-ay) + my*ax
				nx, ny = mx/max(cos, 0.5), my/max(cos, 0.5)
			}
		}
		hw := half(i)
		left[i] = pt{pts[i].x + nx*hw, pts[i].y + ny*hw}
		right[i] = pt{pts[i].x - nx*hw, pts[i].y - ny*hw}
	}
	out := make([]pt, 0, 2*n+2*capSteps)
	out = append(out, left...)
	out = append(out, arc(pts[n-1], left[n-1], half(n-1))...)
	for i := n - 1; i >= 0; i-- {
		out = append(out, right[i])
	}
	return append(out, arc(pts[0], right[0], half(0))...)
}

// turns are the cosines and sines of the steps round a circle, worked out once: a round end turns the point it
// starts from by them, with no trigonometry of its own.
var turns = func() (t [2 * capSteps][2]float32) {
	for k := range t {
		a := math.Pi * float64(k) / capSteps
		t[k] = [2]float32{float32(math.Cos(a)), float32(math.Sin(a))}
	}
	return t
}()

// arc is the points of the half circle about c going round from the point from towards its opposite, both left
// out; its radius is from's distance from c.
func arc(c, from pt, _ float32) []pt {
	fx, fy := from.x-c.x, from.y-c.y
	out := make([]pt, 0, capSteps-1)
	for k := 1; k < capSteps; k++ {
		cs, sn := turns[k][0], turns[k][1]
		out = append(out, pt{c.x + fx*cs - fy*sn, c.y + fx*sn + fy*cs})
	}
	return out
}

// circlePoly is a circle of radius r about c.
func circlePoly(c pt, r float32) []pt {
	out := make([]pt, 2*capSteps)
	for k := range out {
		out[k] = pt{c.x + r*turns[k][0], c.y + r*turns[k][1]}
	}
	return out
}

// outside reports whether the points, widened by m, lie wholly outside the clip: then nothing is built for them.
func outside(c *gfx.Canvas, pts []pt, m float32) bool {
	cl := c.Clip()
	minX, minY, maxX, maxY := pts[0].x, pts[0].y, pts[0].x, pts[0].y
	for _, q := range pts[1:] {
		minX, minY, maxX, maxY = min(minX, q.x), min(minY, q.y), max(maxX, q.x), max(maxY, q.y)
	}
	return maxX+m < float32(cl.Min.X) || minX-m > float32(cl.Max.X) || maxY+m < float32(cl.Min.Y) || minY-m > float32(cl.Max.Y)
}

// strokeLine draws a polyline w wide with round ends and mitred corners. Only the stretches that come near the
// clip are drawn, each cut where it leaves the clip widened by more than the line's width (where its cut ends do
// not show): a long line crossing many tiles costs each only what crosses it.
func strokeLine(c *gfx.Canvas, pts []pt, w float32, col color.RGBA) {
	if len(pts) == 0 || col.A == 0 || outside(c, pts, w+1) {
		return
	}
	if len(pts) == 1 {
		fillPoly(c, circlePoly(pts[0], w/2), col)
		return
	}
	cl := c.Clip()
	m := w + 4
	box := [4]float32{float32(cl.Min.X) - m, float32(cl.Min.Y) - m, float32(cl.Max.X) + m, float32(cl.Max.Y) + m}
	var run []pt
	draw := func() {
		if len(run) > 1 {
			fillPoly(c, band(run, func(float32) float32 { return w }), col)
		}
		run = run[:0]
	}
	for i := 1; i < len(pts); i++ {
		a, b, ok := clipSegment(pts[i-1], pts[i], box)
		if !ok {
			draw()
			continue
		}
		if len(run) == 0 || run[len(run)-1] != a {
			draw()
			run = append(run, a)
		}
		run = append(run, b)
		if b != pts[i] {
			draw() // it left the box: what follows starts again where it comes back
		}
	}
	draw()
}

// clipSegment is the part of the segment a–b inside the box (x0, y0, x1, y1), and whether there is one
// (Liang–Barsky).
func clipSegment(a, b pt, box [4]float32) (pt, pt, bool) {
	t0, t1 := float32(0), float32(1)
	dx, dy := b.x-a.x, b.y-a.y
	for _, e := range [4][2]float32{{-dx, a.x - box[0]}, {dx, box[2] - a.x}, {-dy, a.y - box[1]}, {dy, box[3] - a.y}} {
		p, q := e[0], e[1]
		if p == 0 {
			if q < 0 {
				return a, b, false
			}
			continue
		}
		r := q / p
		if p < 0 {
			t0 = max(t0, r)
		} else {
			t1 = min(t1, r)
		}
		if t0 > t1 {
			return a, b, false
		}
	}
	na, nb := a, b
	if t0 > 0 {
		na = pt{a.x + dx*t0, a.y + dy*t0}
	}
	if t1 < 1 {
		nb = pt{a.x + dx*t1, a.y + dy*t1}
	}
	return na, nb, true
}

// segment draws a straight line w wide with round ends.
func segment(c *gfx.Canvas, a, b pt, w float32, col color.RGBA) { strokeLine(c, []pt{a, b}, w, col) }

// strokeClosed draws the outline of a closed polygon w wide, centred on it: the band between the polygon grown and
// shrunk by half the width, the inner side cut out.
func strokeClosed(c *gfx.Canvas, poly []pt, w float32, col color.RGBA) {
	n := len(poly)
	if n < 3 || col.A == 0 {
		return
	}
	outer, inner := make([]pt, n), make([]pt, n)
	// The polygon's turn decides which side is out.
	area := float32(0)
	for i := range poly {
		a, b := poly[i], poly[(i+1)%n]
		area += a.x*b.y - b.x*a.y
	}
	sign := float32(1)
	if area < 0 {
		sign = -1
	}
	for i := range poly {
		p, q, r := poly[(i+n-1)%n], poly[i], poly[(i+1)%n]
		ax, ay := q.x-p.x, q.y-p.y
		bx, by := r.x-q.x, r.y-q.y
		la, lb := float32(math.Hypot(float64(ax), float64(ay))), float32(math.Hypot(float64(bx), float64(by)))
		if la == 0 || lb == 0 {
			outer[i], inner[i] = q, q
			continue
		}
		ax, ay, bx, by = ax/la, ay/la, bx/lb, by/lb
		// The outward normals of the two sides, and the mitre between them.
		n1x, n1y := ay*sign, -ax*sign
		n2x, n2y := by*sign, -bx*sign
		mx, my := n1x+n2x, n1y+n2y
		l := float32(math.Hypot(float64(mx), float64(my)))
		if l < 1e-4 {
			mx, my, l = n1x, n1y, 1
		}
		mx, my = mx/l, my/l
		k := w / 2 / max(mx*n1x+my*n1y, 0.5)
		outer[i] = pt{q.x + mx*k, q.y + my*k}
		inner[i] = pt{q.x - mx*k, q.y - my*k}
	}
	// The inner side runs the other way, so the non-zero fill leaves it open.
	for i, j := 0, n-1; i < j; i, j = i+1, j-1 {
		inner[i], inner[j] = inner[j], inner[i]
	}
	fillPath(c, [][]pt{outer, inner}, col)
}

// dashed draws a polyline as dashes on and off long, each with round ends. The dashes keep their places along the
// whole line, but only the part of each piece inside the clip (widened by more than a dash's width) is cut into
// dashes, the pattern being moved on over the rest: a long dashed line costs each tile what crosses it.
func dashed(c *gfx.Canvas, pts []pt, w float32, col color.RGBA, on, off float32) {
	if len(pts) < 2 || outside(c, pts, w+1) {
		return
	}
	cl := c.Clip()
	m := w + 4
	box := [4]float32{float32(cl.Min.X) - m, float32(cl.Min.Y) - m, float32(cl.Max.X) + m, float32(cl.Max.Y) + m}
	drawing, left := true, on
	var dash []pt
	var bands [][]pt // every dash in the clip, filled together in one pass
	flush := func() {
		if len(dash) > 1 {
			bands = append(bands, band(dash, func(float32) float32 { return w }))
		}
		dash = dash[:0]
	}
	// skip moves the pattern on by d without drawing.
	skip := func(d float32) {
		flush()
		if d > on+off {
			d = float32(math.Mod(float64(d), float64(on+off))) // whole periods change nothing
		}
		for d >= left {
			d -= left
			drawing = !drawing
			left = on
			if !drawing {
				left = off
			}
		}
		left -= d
	}
	for i := 1; i < len(pts); i++ {
		a, b := pts[i-1], pts[i]
		d := float32(math.Hypot(float64(b.x-a.x), float64(b.y-a.y)))
		if d == 0 {
			continue
		}
		ca, cb, ok := clipSegment(a, b, box)
		if !ok {
			skip(d)
			continue
		}
		s0 := float32(math.Hypot(float64(ca.x-a.x), float64(ca.y-a.y)))
		s1 := float32(math.Hypot(float64(cb.x-a.x), float64(cb.y-a.y)))
		if s0 > 0 {
			skip(s0)
		}
		pos := s0
		for pos < s1 {
			step := min(left, s1-pos)
			if drawing {
				p0 := pt{a.x + (b.x-a.x)*pos/d, a.y + (b.y-a.y)*pos/d}
				p1 := pt{a.x + (b.x-a.x)*(pos+step)/d, a.y + (b.y-a.y)*(pos+step)/d}
				if len(dash) == 0 {
					dash = append(dash, p0)
				}
				dash = append(dash, p1)
			}
			pos += step
			left -= step
			if left <= 0 {
				if drawing {
					flush()
				}
				drawing = !drawing
				left = on
				if !drawing {
					left = off
				}
			}
		}
		if s1 < d {
			skip(d - s1)
		}
	}
	flush()
	fillPath(c, bands, col)
}

// ellipsePoly is an ellipse inside r as a polygon, with enough sides to look round at its size.
func ellipsePoly(r gfx.Rect) []pt {
	n := int(min(max(float64(r.W+r.H)/3, 24), 160))
	cx, cy, rx, ry := r.X+r.W/2, r.Y+r.H/2, r.W/2, r.H/2
	out := make([]pt, n)
	for i := range out {
		a := 2 * math.Pi * float64(i) / float64(n)
		out[i] = pt{cx + rx*float32(math.Cos(a)), cy + ry*float32(math.Sin(a))}
	}
	return out
}

// shapePoly is the outline of a shape drawn with polygons, inside r; nil for those gfx draws itself.
func shapePoly(s Shape, r gfx.Rect) []pt {
	x0, y0, x1, y1 := r.X, r.Y, r.X+r.W, r.Y+r.H
	cx, cy := r.X+r.W/2, r.Y+r.H/2
	switch s {
	case ShapeEllipse:
		return ellipsePoly(r)
	case ShapeDiamond:
		return []pt{{cx, y0}, {x1, cy}, {cx, y1}, {x0, cy}}
	case ShapeParallelogram:
		k := min(r.H*0.35, r.W/4)
		return []pt{{x0 + k, y0}, {x1, y0}, {x1 - k, y1}, {x0, y1}}
	case ShapeHexagon:
		k := min(r.H*0.4, r.W/4)
		return []pt{{x0 + k, y0}, {x1 - k, y0}, {x1, cy}, {x1 - k, y1}, {x0 + k, y1}, {x0, cy}}
	}
	return nil
}

// arrowHead fills an arrowhead with its tip at tip, pointing along dx, dy (a unit vector), size long, its back
// slightly hollowed as a drawn arrow's is.
func arrowHead(c *gfx.Canvas, tip pt, dx, dy, size float32, col color.RGBA) {
	bx, by := tip.x-dx*size, tip.y-dy*size
	nx, ny := -dy*size*0.45, dx*size*0.45
	notch := pt{tip.x - dx*size*0.78, tip.y - dy*size*0.78}
	fillPoly(c, []pt{tip, {bx + nx, by + ny}, notch, {bx - nx, by - ny}}, col)
}
