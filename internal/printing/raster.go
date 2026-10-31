package printing

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"math"
	"slices"
	"sort"
)

// Drawing a page into pixels: the print dialog's preview, and what the flattener lays under what a printer cannot
// blend. It reads the same list as the other backends, so what it shows is what they print.

// maxRaster bounds the pixels of one page drawn at once.
const maxRaster = 200_000_000

// Render draws the part of a page in region (in pixels at scale pixels a point; empty means all of it) over white.
func Render(p *Page, scale float64, region image.Rectangle) *image.RGBA {
	w, h := int(math.Ceil(p.W*scale)), int(math.Ceil(p.H*scale))
	box := image.Rect(0, 0, w, h)
	if !region.Empty() {
		box = box.Intersect(region)
	}
	if box.Empty() || box.Dx()*box.Dy() > maxRaster {
		return image.NewRGBA(image.Rect(0, 0, 1, 1))
	}
	img := image.NewRGBA(box)
	for i := range img.Pix {
		img.Pix[i] = 255
	}
	r := &raster{img: img, view: Scale(scale, scale), k: scale}
	r.clips = []clipState{{box: box}}
	for _, op := range p.Ops {
		r.draw(op)
	}
	return img
}

// clipState is the clip in force: a box and, when it is not a plain rectangle, the share of each pixel inside it.
type clipState struct {
	box  image.Rectangle
	mask []uint8 // over box
}

func (c *clipState) at(x, y int) uint8 {
	if c.mask == nil {
		return 255
	}
	return c.mask[(y-c.box.Min.Y)*c.box.Dx()+x-c.box.Min.X]
}

type raster struct {
	img   *image.RGBA
	view  Matrix
	k     float64
	clips []clipState
	edges []edge
	acc   []float32
	cross []crossing
}

func (r *raster) clip() *clipState { return &r.clips[len(r.clips)-1] }

func (r *raster) draw(op Op) {
	switch o := op.(type) {
	case *Fill:
		r.fill(o.Path, r.view, o.EvenOdd, solid(o.Color), 1)
	case *Stroke:
		c := *o
		c.Width = max(o.Width*r.k, 1)
		c.Phase = o.Phase * r.k
		c.Path = o.Path.Transformed(r.view)
		if o.Dash != nil {
			c.Dash = make([]float64, len(o.Dash))
			for i, v := range o.Dash {
				c.Dash[i] = v * r.k
			}
		}
		r.fill(StrokeOutline(&c, 1), Identity, false, solid(o.Color), 1)
	case *Clip:
		r.pushClip(o.Path, o.EvenOdd)
	case *Unclip:
		if len(r.clips) > 1 {
			r.clips = r.clips[:len(r.clips)-1]
		}
	case *Picture:
		r.picture(o)
	case *Text:
		for i := range o.Glyphs {
			g := &o.Glyphs[i]
			if g.Outline != nil && !g.Outline.Path.Empty() {
				r.fill(g.Outline.Path, r.view.Mul(o.M).Mul(Translate(g.X, g.Y)), false, solid(o.Color), 1)
			}
		}
	case *Shade:
		if fn := shadePaint(o, r.view); fn != nil {
			alpha := o.Alpha
			if alpha == 0 {
				alpha = 1
			}
			r.fill(o.Path, r.view, o.EvenOdd, fn, alpha)
		}
	}
}

// paintFn is the colour at the middle of a pixel; A is 0 where nothing is painted.
type paintFn func(x, y int) color.NRGBA

func solid(c color.NRGBA) paintFn { return func(int, int) color.NRGBA { return c } }

func (r *raster) fill(p *Path, m Matrix, evenOdd bool, paint paintFn, alpha float64) {
	polys := flatten(p, m)
	b := polyBounds(polys).Intersect(r.clip().box)
	if b.Empty() {
		return
	}
	r.scan(polys, evenOdd, b, func(y int, cover []float32) {
		r.lay(y, b.Min.X, cover, paint, alpha)
	})
}

// lay blends a row of coverage onto the picture, through the clip. The page is opaque, so straight mixing is exact.
func (r *raster) lay(y, x0 int, cover []float32, paint paintFn, alpha float64) {
	cl := r.clip()
	out := r.img.Pix[r.img.PixOffset(x0, y):]
	for i, c := range cover {
		if c <= 0 {
			continue
		}
		x := x0 + i
		a := min(c, 1) * float32(alpha)
		if cl.mask != nil {
			a *= float32(cl.at(x, y)) / 255
		}
		col := paint(x, y)
		a *= float32(col.A) / 255
		if a <= 0 {
			continue
		}
		q := out[i*4 : i*4+3 : i*4+3]
		q[0] = uint8(float32(q[0]) + (float32(col.R)-float32(q[0]))*a + 0.5)
		q[1] = uint8(float32(q[1]) + (float32(col.G)-float32(q[1]))*a + 0.5)
		q[2] = uint8(float32(q[2]) + (float32(col.B)-float32(q[2]))*a + 0.5)
	}
}

func (r *raster) pushClip(p *Path, evenOdd bool) {
	cur := r.clip()
	polys := flatten(p, r.view)
	if b, ok := pixelRect(polys); ok {
		next := clipState{box: b.Intersect(cur.box)}
		if cur.mask != nil && !next.box.Empty() {
			next.mask = make([]uint8, next.box.Dx()*next.box.Dy())
			for y := next.box.Min.Y; y < next.box.Max.Y; y++ {
				for x := next.box.Min.X; x < next.box.Max.X; x++ {
					next.mask[(y-next.box.Min.Y)*next.box.Dx()+x-next.box.Min.X] = cur.at(x, y)
				}
			}
		}
		r.clips = append(r.clips, next)
		return
	}
	b := polyBounds(polys).Intersect(cur.box)
	next := clipState{box: b}
	if !b.Empty() {
		next.mask = make([]uint8, b.Dx()*b.Dy())
		r.scan(polys, evenOdd, b, func(y int, cover []float32) {
			row := next.mask[(y-b.Min.Y)*b.Dx():]
			for i, c := range cover {
				row[i] = uint8(min(c, 1)*float32(cur.at(b.Min.X+i, y)) + 0.5)
			}
		})
	}
	r.clips = append(r.clips, next)
}

// pixelRect reports polygons that are one axis-aligned rectangle, rounded to pixels: neighbours then meet exactly.
func pixelRect(polys []polygon) (image.Rectangle, bool) {
	if len(polys) != 1 {
		return image.Rectangle{}, false
	}
	pts := polys[0].pts
	if n := len(pts); n == 5 && pts[0] == pts[4] {
		pts = pts[:4]
	}
	if len(pts) != 4 {
		return image.Rectangle{}, false
	}
	for i := range 4 {
		a, b := pts[i], pts[(i+1)%4]
		if math.Abs(a.X-b.X) > 1e-6 && math.Abs(a.Y-b.Y) > 1e-6 {
			return image.Rectangle{}, false
		}
	}
	x0, x1 := min(pts[0].X, pts[2].X), max(pts[0].X, pts[2].X)
	y0, y1 := min(pts[0].Y, pts[2].Y), max(pts[0].Y, pts[2].Y)
	lim := func(v float64) int { return int(min(max(v, -1<<30), 1<<30)) }
	return image.Rect(lim(math.Round(x0)), lim(math.Round(y0)), lim(math.Round(x1)), lim(math.Round(y1))), true
}

func polyBounds(polys []polygon) image.Rectangle {
	b := emptyBox()
	for _, l := range polys {
		for _, q := range l.pts {
			b = b.add(q.X, q.Y)
		}
	}
	if b.X0 > b.X1 || math.IsNaN(b.X0+b.X1+b.Y0+b.Y1) {
		return image.Rectangle{}
	}
	lim := func(v float64) int { return int(min(max(v, -1<<30), 1<<30)) }
	return image.Rect(lim(math.Floor(b.X0)), lim(math.Floor(b.Y0)), lim(math.Ceil(b.X1)), lim(math.Ceil(b.Y1)))
}

type edge struct {
	x0, y0, x1, y1 float64
	dir            int
}

type crossing struct {
	x   float64
	dir int
}

// subRows is how many lines each row of pixels is sampled on; across a row, coverage is exact.
const subRows = 5

// scan works out the share of each pixel of box inside the polygons, a row at a time. Each row of emit is reused.
func (r *raster) scan(polys []polygon, evenOdd bool, box image.Rectangle, emit func(y int, cover []float32)) {
	es := r.edges[:0]
	top, bottom := float64(box.Min.Y), float64(box.Max.Y)
	for _, l := range polys {
		n := len(l.pts)
		if n < 2 {
			continue
		}
		for i := range n {
			a, b := l.pts[i], l.pts[(i+1)%n]
			if a.Y == b.Y || math.IsNaN(a.X+a.Y+b.X+b.Y) {
				continue
			}
			e := edge{a.X, a.Y, b.X, b.Y, 1}
			if e.y0 > e.y1 {
				e = edge{b.X, b.Y, a.X, a.Y, -1}
			}
			if e.y1 > top && e.y0 < bottom {
				es = append(es, e)
			}
		}
	}
	r.edges = es
	sort.Slice(es, func(i, j int) bool { return es[i].y0 < es[j].y0 })
	w := box.Dx()
	if cap(r.acc) < w+2 {
		r.acc = make([]float32, w+2)
	}
	acc := r.acc[:w+2]
	cover := make([]float32, w)
	active := make([]edge, 0, 64)
	next := 0
	const share = float32(1) / subRows
	for y := box.Min.Y; y < box.Max.Y; y++ {
		clear(acc)
		used := false
		for s := range subRows {
			sy := float64(y) + (float64(s)+0.5)/subRows
			for next < len(es) && es[next].y0 <= sy {
				active = append(active, es[next])
				next++
			}
			active = slices.DeleteFunc(active, func(e edge) bool { return e.y1 <= sy })
			if len(active) == 0 {
				continue
			}
			xs := r.cross[:0]
			for i := range active {
				e := &active[i]
				xs = append(xs, crossing{e.x0 + (sy-e.y0)*(e.x1-e.x0)/(e.y1-e.y0), e.dir})
			}
			r.cross = xs
			slices.SortFunc(xs, func(a, b crossing) int {
				switch {
				case a.x < b.x:
					return -1
				case a.x > b.x:
					return 1
				}
				return 0
			})
			wind := 0
			for i := 0; i+1 < len(xs); i++ {
				if evenOdd {
					wind ^= 1
				} else {
					wind += xs[i].dir
				}
				if wind != 0 && xs[i+1].x > xs[i].x {
					span(acc, xs[i].x-float64(box.Min.X), xs[i+1].x-float64(box.Min.X), w, share)
					used = true
				}
			}
		}
		if !used {
			continue
		}
		run := float32(0)
		for i := 0; i < w; i++ {
			run += acc[i]
			cover[i] = run
		}
		emit(y, cover)
	}
}

// span adds a piece of a row to acc, which holds the change in coverage from each pixel to the next.
func span(acc []float32, a, b float64, w int, share float32) {
	a, b = max(a, 0), min(b, float64(w))
	if b <= a {
		return
	}
	ia, ib := int(a), int(b)
	fa, fb := float32(a-float64(ia)), float32(b-float64(ib))
	if ia == ib {
		acc[ia] += (fb - fa) * share
		acc[ia+1] -= (fb - fa) * share
		return
	}
	acc[ia] += (1 - fa) * share
	acc[ia+1] += fa * share
	acc[ib] -= (1 - fb) * share
	acc[ib+1] -= fb * share
}

// shadePaint is a gradient's colour at each pixel.
func shadePaint(s *Shade, view Matrix) paintFn {
	inv, ok := view.Mul(s.M).Inverse()
	if !ok {
		return nil
	}
	var lut [512]color.NRGBA
	for i := range lut {
		lut[i] = shadeColor(s.Stops, float64(i)/float64(len(lut)-1))
	}
	pick := func(t float64, ok bool) color.NRGBA {
		switch {
		case !ok:
			return color.NRGBA{}
		case t < 0 && !s.Extend[0], t > 1 && !s.Extend[1]:
			return color.NRGBA{}
		}
		return lut[int(min(max(t, 0), 1)*float64(len(lut)-1)+0.5)]
	}
	if !s.Radial {
		dx, dy := s.X1-s.X0, s.Y1-s.Y0
		d := dx*dx + dy*dy
		return func(x, y int) color.NRGBA {
			px, py := inv.Apply(float64(x)+0.5, float64(y)+0.5)
			if d == 0 {
				return pick(0, true)
			}
			return pick(((px-s.X0)*dx+(py-s.Y0)*dy)/d, true)
		}
	}
	dx, dy, dr := s.X1-s.X0, s.Y1-s.Y0, s.R1-s.R0
	a := dx*dx + dy*dy - dr*dr
	return func(x, y int) color.NRGBA {
		px, py := inv.Apply(float64(x)+0.5, float64(y)+0.5)
		t, ok := radialAt(px-s.X0, py-s.Y0, dx, dy, dr, a, s.R0, s.Extend)
		return pick(t, ok)
	}
}

// radialAt is the largest t whose circle passes through a point, its radius not negative.
func radialAt(cx, cy, dx, dy, dr, a, r0 float64, extend [2]bool) (float64, bool) {
	b := cx*dx + cy*dy + r0*dr
	c := cx*cx + cy*cy - r0*r0
	var roots [2]float64
	n := 0
	if a == 0 {
		if b == 0 {
			return 0, false
		}
		roots[0], n = c/(2*b), 1
	} else {
		disc := b*b - a*c
		if disc < 0 {
			return 0, false
		}
		q := math.Sqrt(disc)
		roots[0], roots[1], n = (b+q)/a, (b-q)/a, 2
		if roots[0] < roots[1] {
			roots[0], roots[1] = roots[1], roots[0]
		}
	}
	for _, t := range roots[:n] {
		if r0+t*dr < 0 || t < 0 && !extend[0] || t > 1 && !extend[1] {
			continue
		}
		return t, true
	}
	return 0, false
}

// pictureImage is a picture's pixels, decoded when it is only a JPEG.
func pictureImage(p *Picture) *image.RGBA {
	if p.Img != nil {
		return p.Img
	}
	dec, err := jpeg.Decode(bytes.NewReader(p.JPEG))
	if err != nil {
		return nil
	}
	img := image.NewRGBA(dec.Bounds())
	draw.Draw(img, img.Rect, dec, dec.Bounds().Min, draw.Src)
	return img
}

// picture lays an image through its matrix. A much reduced one is averaged first, so that it does not shimmer.
func (r *raster) picture(p *Picture) {
	img := pictureImage(p)
	if img == nil || img.Rect.Empty() {
		return
	}
	m := r.view.Mul(p.M)
	sw, sh := float64(img.Rect.Dx()), float64(img.Rect.Dy())
	devW, devH := math.Hypot(m[0], m[1]), math.Hypot(m[2], m[3])
	if f := int(min(sw/max(devW, 1), sh/max(devH, 1))); f >= 2 {
		img = reduce(img, f)
		sw, sh = float64(img.Rect.Dx()), float64(img.Rect.Dy())
	}
	inv, ok := m.Inverse()
	if !ok {
		return
	}
	alpha := p.Alpha
	if alpha == 0 {
		alpha = 1
	}
	base := img.Rect.Min
	smooth := sw < devW*2 || sh < devH*2
	paint := func(x, y int) color.NRGBA {
		u, v := inv.Apply(float64(x)+0.5, float64(y)+0.5)
		if u < 0 || v < 0 || u >= 1 || v >= 1 {
			return color.NRGBA{}
		}
		px, py := u*sw-0.5, v*sh-0.5
		if !smooth {
			return straight(img, base.X+int(px+0.5), base.Y+int(py+0.5))
		}
		x0, y0 := int(math.Floor(px)), int(math.Floor(py))
		tx, ty := px-float64(x0), py-float64(y0)
		a := straight(img, base.X+x0, base.Y+y0)
		b := straight(img, base.X+x0+1, base.Y+y0)
		c := straight(img, base.X+x0, base.Y+y0+1)
		e := straight(img, base.X+x0+1, base.Y+y0+1)
		mix := func(p, q, s, t uint8) uint8 {
			top := float64(p)*(1-tx) + float64(q)*tx
			bot := float64(s)*(1-tx) + float64(t)*tx
			return uint8(top*(1-ty) + bot*ty + 0.5)
		}
		return color.NRGBA{mix(a.R, b.R, c.R, e.R), mix(a.G, b.G, c.G, e.G), mix(a.B, b.B, c.B, e.B), mix(a.A, b.A, c.A, e.A)}
	}
	r.fill(RectPath(0, 0, 1, 1), m, false, paint, alpha)
}

// straight reads a premultiplied pixel, staying within the picture, as a plain colour.
func straight(img *image.RGBA, x, y int) color.NRGBA {
	x, y = min(max(x, img.Rect.Min.X), img.Rect.Max.X-1), min(max(y, img.Rect.Min.Y), img.Rect.Max.Y-1)
	q := img.Pix[img.PixOffset(x, y):]
	a := q[3]
	switch a {
	case 255:
		return color.NRGBA{q[0], q[1], q[2], 255}
	case 0:
		return color.NRGBA{}
	}
	un := func(v uint8) uint8 { return uint8(min(int(v)*255/int(a), 255)) }
	return color.NRGBA{un(q[0]), un(q[1]), un(q[2]), a}
}

// reduce averages each f × f square of pixels into one.
func reduce(img *image.RGBA, f int) *image.RGBA {
	w, h := max(img.Rect.Dx()/f, 1), max(img.Rect.Dy()/f, 1)
	out := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			var sum [4]int
			n := 0
			for j := 0; j < f && y*f+j < img.Rect.Dy(); j++ {
				q := img.Pix[(y*f+j)*img.Stride+x*f*4:]
				for i := 0; i < f && x*f+i < img.Rect.Dx(); i++ {
					sum[0] += int(q[i*4])
					sum[1] += int(q[i*4+1])
					sum[2] += int(q[i*4+2])
					sum[3] += int(q[i*4+3])
					n++
				}
			}
			d := out.Pix[y*out.Stride+x*4:]
			for k := range 4 {
				d[k] = uint8(sum[k] / max(n, 1))
			}
		}
	}
	return out
}
