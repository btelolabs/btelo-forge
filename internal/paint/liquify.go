package paint

import (
	"image"
	"math"
)

// Liquify, as a retouching brush: Forward pushes the picture along the stroke, Pucker draws it in to the brush's centre
// and Bloat pushes it out. A stroke keeps a field saying where each pixel reads the picture from as it was when the
// stroke began, and each dab bends the field, so a pixel pushed a hundred times is resampled once and stays sharp.

// LiquifyMode is what the liquify brush does.
type LiquifyMode int

const (
	LiquifyForward LiquifyMode = iota
	LiquifyPucker
	LiquifyBloat
)

// liquifyRate is how far Pucker and Bloat move a pixel in one dab, as a part of its distance from the centre, at
// full strength.
const liquifyRate = 0.02

// fieldAt is where (x, y) reads from, relative to itself.
func (s *Stroke) fieldAt(x, y int) (float32, float32) {
	t := s.field[image.Pt(x>>tileShift, y>>tileShift)]
	if t == nil {
		return 0, 0
	}
	i := ((y&tileMask)*TileSize + x&tileMask) * 2
	return t[i], t[i+1]
}

// setField sets where (x, y) reads from.
func (s *Stroke) setField(x, y int, dx, dy float32) {
	k := image.Pt(x>>tileShift, y>>tileShift)
	t := s.field[k]
	if t == nil {
		t = make([]float32, TileSize*TileSize*2)
		s.field[k] = t
	}
	i := ((y&tileMask)*TileSize + x&tileMask) * 2
	t[i], t[i+1] = dx, dy
}

// liquifyDab bends the field under a dab at (cx, cy) and shows what that does.
func (s *Stroke) liquifyDab(cx, cy float64) image.Rectangle {
	b := s.brush
	r := b.reach()
	box := image.Rect(int(math.Floor(cx-r)), int(math.Floor(cy-r)), int(math.Ceil(cx+r)), int(math.Ceil(cy+r))).Intersect(s.clip)
	if box.Empty() {
		return box
	}
	if s.field == nil {
		s.field = map[image.Point][]float32{}
	}
	mx, my := (cx-s.lastDab[0])*b.Opacity, (cy-s.lastDab[1])*b.Opacity
	if !s.dabbed {
		mx, my = 0, 0
	}
	s.lastDab, s.dabbed = [2]float64{cx, cy}, true
	mode := s.retouch.Liquify
	rate := liquifyRate * b.Opacity
	if mode == LiquifyBloat {
		rate = -rate
	}
	sel := s.doc.selectionIn(box)
	type moved struct {
		x, y   int
		dx, dy float32
	}
	next := make([]moved, 0, box.Dx()*box.Dy())
	for y := box.Min.Y; y < box.Max.Y; y++ {
		for x := box.Min.X; x < box.Max.X; x++ {
			px, py := float64(x)+0.5, float64(y)+0.5
			cov := b.coverage(px, py, cx, cy) * float64(coverage(sel, x, y)) / 255
			if cov <= 0 {
				continue
			}
			vx, vy := mx*cov, my*cov
			if mode != LiquifyForward {
				vx, vy = (cx-px)*rate*cov, (cy-py)*rate*cov
			}
			// What lands here is what the field shows a push's length back.
			ox, oy := s.fieldBetween(float64(x)-vx, float64(y)-vy)
			next = append(next, moved{x, y, ox - float32(vx), oy - float32(vy)})
		}
	}
	var furthest float32
	for _, m := range next {
		furthest = max(furthest, max(max(m.dx, -m.dx), max(m.dy, -m.dy)))
		s.setField(m.x, m.y, m.dx, m.dy)
	}
	reach := int(math.Ceil(float64(furthest))) + 3
	area := box.Inset(-reach)
	from := s.beforePix.Read(area)
	cur := s.layer.Pixels.Read(box)
	frame := s.doc.Frame
	lock := s.layer.LockAlpha
	for _, m := range next {
		fx, fy := float64(m.x)+float64(m.dx), float64(m.y)+float64(m.dy)
		if !frame.Empty() {
			// Past the picture's edge the edge goes on.
			fx = min(max(fx, float64(frame.Min.X)), float64(frame.Max.X-1))
			fy = min(max(fy, float64(frame.Min.Y)), float64(frame.Max.Y-1))
		}
		i := cur.PixOffset(m.x, m.y)
		alpha := cur.Pix[i+3]
		cubicInto(cur.Pix[i:i+4:i+4], from, fx, fy)
		if lock {
			keepAlpha(cur.Pix[i:i+4], uint32(alpha))
		}
	}
	s.layer.Pixels.Write(cur)
	return box
}

// fieldBetween is the field at a point between pixels.
func (s *Stroke) fieldBetween(x, y float64) (float32, float32) {
	x0, y0 := int(math.Floor(x)), int(math.Floor(y))
	tx, ty := float32(x-float64(x0)), float32(y-float64(y0))
	ax, ay := s.fieldAt(x0, y0)
	bx, by := s.fieldAt(x0+1, y0)
	cx, cy := s.fieldAt(x0, y0+1)
	dx, dy := s.fieldAt(x0+1, y0+1)
	top, topY := ax+(bx-ax)*tx, ay+(by-ay)*tx
	low, lowY := cx+(dx-cx)*tx, cy+(dy-cy)*tx
	return top + (low-top)*ty, topY + (lowY-topY)*ty
}

// cubicInto is src at a point between pixels (Catmull–Rom, which keeps edges); colour stays within its alpha.
func cubicInto(dst []uint8, src *image.RGBA, x, y float64) {
	x0, y0 := int(math.Floor(x)), int(math.Floor(y))
	wx, wy := cubicWeights(x-float64(x0)), cubicWeights(y-float64(y0))
	b := src.Rect
	var sum [4]float64
	for j := range 4 {
		py := min(max(y0+j-1, b.Min.Y), b.Max.Y-1)
		for i := range 4 {
			px := min(max(x0+i-1, b.Min.X), b.Max.X-1)
			p := src.Pix[src.PixOffset(px, py):][:4]
			w := wx[i] * wy[j]
			for c := range 4 {
				sum[c] += float64(p[c]) * w
			}
		}
	}
	a := min(max(sum[3], 0), 255)
	dst[3] = uint8(a + 0.5)
	for c := range 3 {
		dst[c] = uint8(min(max(sum[c], 0), a) + 0.5)
	}
}

func cubicWeights(t float64) [4]float64 {
	t2, t3 := t*t, t*t*t
	return [4]float64{(-t3 + 2*t2 - t) / 2, (3*t3 - 5*t2 + 2) / 2, (-3*t3 + 4*t2 + t) / 2, (t3 - t2) / 2}
}

// Hold lays another dab where the brush is, as Pucker and Bloat go on working while the button stays down.
func (s *Stroke) Hold() image.Rectangle {
	if s.retouch == nil || s.retouch.Kind != RetouchLiquify || s.retouch.Liquify == LiquifyForward {
		return image.Rectangle{}
	}
	return s.laid(s.dab(s.x, s.y))
}
