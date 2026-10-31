package printing

import (
	"image"
	"image/color"
	"math"
)

// Most printer drivers do not blend. Flatten turns a page into one with nothing translucent and no gradients, for
// them. Translucent paint over one plain colour becomes the colour it mixes to, and stays a path. Over anything else
// the page is drawn once at the printer's resolution, and only the translucent shapes take their pixels from that,
// so everything around them stays vector. Gradients become bands of plain colour.

// Flatten is the page as a printer without blending can take it; dpi is how finely blended areas are drawn.
func Flatten(p *Page, dpi float64) *Page {
	f := &flattener{p: p, k: dpi / 72}
	f.boxes()
	out := &Page{W: p.W, H: p.H, Fit: p.Fit}
	for i, op := range p.Ops {
		f.op(out, i, op)
	}
	return out
}

type flattener struct {
	p      *Page
	k      float64
	marked []Box  // where each op can mark, within its clip
	solid  []bool // the op paints all of marked in one opaque colour
	col    []color.NRGBA
	whole  *image.RGBA
}

// boxes works out where each op lands. A clip that is not a rectangle leaves its ops not solid.
func (f *flattener) boxes() {
	ops := f.p.Ops
	f.marked = make([]Box, len(ops))
	f.solid = make([]bool, len(ops))
	f.col = make([]color.NRGBA, len(ops))
	type level struct {
		box   Box
		plain bool
	}
	page := Box{0, 0, f.p.W, f.p.H}
	stack := []level{{page, true}}
	for i, op := range ops {
		top := stack[len(stack)-1]
		switch o := op.(type) {
		case *Clip:
			r, ok := axisBox(o.Path)
			box := o.Path.Bounds()
			if ok {
				box = r
			}
			stack = append(stack, level{top.box.intersect(box), top.plain && ok})
			f.marked[i] = emptyBox()
			continue
		case *Unclip:
			if len(stack) > 1 {
				stack = stack[:len(stack)-1]
			}
			f.marked[i] = emptyBox()
			continue
		case *Fill:
			if r, ok := axisBox(o.Path); ok && o.Color.A == 255 && top.plain {
				f.solid[i], f.col[i] = true, o.Color
				f.marked[i] = r.intersect(top.box)
				continue
			}
		}
		f.marked[i] = bounds(op).intersect(top.box)
	}
}

// axisBox reports a path that is one axis-aligned rectangle.
func axisBox(p *Path) (Box, bool) {
	if p == nil || len(p.Pts) != 4 && len(p.Pts) != 5 {
		return Box{}, false
	}
	for _, o := range p.Ops[1:] {
		if o == 'M' || o == 'C' {
			return Box{}, false
		}
	}
	if len(p.Pts) == 5 && p.Pts[0] != p.Pts[4] {
		return Box{}, false
	}
	for i := range 4 {
		a, b := p.Pts[i], p.Pts[(i+1)%4]
		if a.X != b.X && a.Y != b.Y {
			return Box{}, false
		}
	}
	return p.Bounds(), true
}

// backdrop is the one colour under box at op i, and false when there is more than one.
func (f *flattener) backdrop(i int, box Box) (color.NRGBA, bool) {
	for j := i - 1; j >= 0; j-- {
		m := f.marked[j]
		if m.empty() || !m.overlaps(box) {
			continue
		}
		if f.solid[j] && m.contains(box) {
			return f.col[j], true
		}
		return color.NRGBA{}, false
	}
	return color.NRGBA{255, 255, 255, 255}, true
}

// over is a translucent colour on an opaque one.
func over(c, under color.NRGBA) color.NRGBA {
	a := float64(c.A) / 255
	mix := func(s, d uint8) uint8 { return uint8(float64(s)*a + float64(d)*(1-a) + 0.5) }
	return color.NRGBA{mix(c.R, under.R), mix(c.G, under.G), mix(c.B, under.B), 255}
}

// area puts pixels of the page's own drawing where the path is.
func (f *flattener) area(out *Page, path *Path, evenOdd bool, box Box) {
	if f.whole == nil {
		f.whole = Render(f.p, f.k, image.Rectangle{})
	}
	r := image.Rect(int(math.Floor(box.X0*f.k)), int(math.Floor(box.Y0*f.k)), int(math.Ceil(box.X1*f.k)), int(math.Ceil(box.Y1*f.k))).Intersect(f.whole.Rect)
	if r.Empty() {
		return
	}
	sub := f.whole.SubImage(r).(*image.RGBA)
	x, y := float64(r.Min.X)/f.k, float64(r.Min.Y)/f.k
	out.Add(&Clip{Path: path, EvenOdd: evenOdd})
	out.Add(&Picture{Img: sub, M: Translate(x, y).Mul(Scale(float64(r.Dx())/f.k, float64(r.Dy())/f.k))})
	out.Add(&Unclip{})
}

func (f *flattener) op(out *Page, i int, op Op) {
	box := f.marked[i]
	switch o := op.(type) {
	case *Fill:
		if o.Color.A == 255 {
			out.Add(o)
			return
		}
		if o.Color.A == 0 || box.empty() {
			return
		}
		if under, ok := f.backdrop(i, box); ok {
			c := *o
			c.Color = over(o.Color, under)
			out.Add(&c)
			return
		}
		f.area(out, o.Path, o.EvenOdd, box)
	case *Stroke:
		if o.Color.A == 255 {
			out.Add(o)
			return
		}
		if o.Color.A == 0 || box.empty() {
			return
		}
		if under, ok := f.backdrop(i, box); ok {
			c := *o
			c.Color = over(o.Color, under)
			out.Add(&c)
			return
		}
		f.area(out, StrokeOutline(o, max(f.k, 2)), false, box)
	case *Text:
		if o.Color.A == 255 {
			out.Add(o)
			return
		}
		if o.Color.A == 0 || box.empty() {
			return
		}
		if under, ok := f.backdrop(i, box); ok {
			c := *o
			c.Color = over(o.Color, under)
			out.Add(&c)
			return
		}
		f.area(out, TextPath(o), false, box)
	case *Picture:
		if alphaByte(o.Alpha) == 255 && (o.Img == nil || opaque(o.Img)) {
			out.Add(o)
			return
		}
		if box.empty() {
			return
		}
		if under, ok := f.backdrop(i, box); ok && o.Img != nil {
			c := *o
			c.Img, c.Alpha = onColor(o.Img, o.Alpha, under), 0
			out.Add(&c)
			return
		}
		f.area(out, RectPath(0, 0, 1, 1).Transformed(o.M), false, box)
	case *Shade:
		if box.empty() {
			return
		}
		under := color.NRGBA{255, 255, 255, 255}
		opaqueShade := alphaByte(o.Alpha) == 255
		if !opaqueShade {
			var ok bool
			if under, ok = f.backdrop(i, box); !ok {
				f.area(out, o.Path, o.EvenOdd, box)
				return
			}
		}
		// Bands cover all the shape, so a gradient that stops short of it is left to the page's own drawing.
		if o.Extend != [2]bool{true, true} && (o.Radial || !coversAxis(o)) {
			f.area(out, o.Path, o.EvenOdd, box)
			return
		}
		bands(out, o, under, box)
	default:
		out.Add(op)
	}
}

// coversAxis reports that a gradient that stops at its ends still covers its shape, so the ends do not matter.
func coversAxis(s *Shade) bool {
	inv, ok := s.M.Inverse()
	if !ok {
		return false
	}
	dx, dy := s.X1-s.X0, s.Y1-s.Y0
	d := dx*dx + dy*dy
	if d == 0 {
		return false
	}
	b := s.Path.Bounds()
	for _, c := range [4][2]float64{{b.X0, b.Y0}, {b.X1, b.Y0}, {b.X1, b.Y1}, {b.X0, b.Y1}} {
		x, y := inv.Apply(c[0], c[1])
		t := ((x-s.X0)*dx + (y-s.Y0)*dy) / d
		if t < -1e-6 || t > 1+1e-6 {
			return false
		}
	}
	return true
}

func opaque(img *image.RGBA) bool {
	w := img.Rect.Dx()
	for y := range img.Rect.Dy() {
		row := img.Pix[y*img.Stride : y*img.Stride+w*4]
		for x := 3; x < len(row); x += 4 {
			if row[x] != 255 {
				return false
			}
		}
	}
	return true
}

// onColor is the image laid on a colour.
func onColor(img *image.RGBA, alpha float64, under color.NRGBA) *image.RGBA {
	if alpha == 0 {
		alpha = 1
	}
	out := image.NewRGBA(image.Rect(0, 0, img.Rect.Dx(), img.Rect.Dy()))
	k := int(alpha*256 + 0.5)
	for y := range img.Rect.Dy() {
		src := img.Pix[y*img.Stride:]
		dst := out.Pix[y*out.Stride:]
		for x := range img.Rect.Dx() {
			s := src[x*4 : x*4+4]
			inv := 255 - int(s[3])*k>>8
			dst[x*4] = uint8((int(s[0]) * k >> 8) + (int(under.R)*inv+127)/255)
			dst[x*4+1] = uint8((int(s[1]) * k >> 8) + (int(under.G)*inv+127)/255)
			dst[x*4+2] = uint8((int(s[2]) * k >> 8) + (int(under.B)*inv+127)/255)
			dst[x*4+3] = 255
		}
	}
	return out
}

// bands draws a gradient as steps of plain colour, each laid from where it starts to the far end, so no seam shows.
func bands(out *Page, s *Shade, under color.NRGBA, box Box) {
	inv, ok := s.M.Inverse()
	if !ok {
		return
	}
	alpha := alphaByte(s.Alpha)
	col := func(t float64) color.NRGBA {
		c := shadeColor(s.Stops, t)
		if alpha != 255 {
			c.A = uint8(alpha)
			return over(c, under)
		}
		c.A = 255
		return c
	}
	var dist float64
	for i := 1; i < len(s.Stops); i++ {
		a, b := s.Stops[i-1].Color, s.Stops[i].Color
		dist += max(math.Abs(float64(a.R)-float64(b.R)), math.Abs(float64(a.G)-float64(b.G)), math.Abs(float64(a.B)-float64(b.B)))
	}
	n := min(max(int(dist), 2), 256)
	out.Add(&Clip{Path: s.Path, EvenOdd: s.EvenOdd})
	defer out.Add(&Unclip{})
	if s.Radial {
		far := s.Path.Bounds().grow(1)
		out.Add(&Fill{Path: RectPath(far.X0, far.Y0, far.X1-far.X0, far.Y1-far.Y0), Color: col(1)})
		// Largest circle first, when the gradient grows outward, as it nearly always does.
		for i := n; i >= 0; i-- {
			t := float64(i) / float64(n)
			if s.R1 < s.R0 {
				t = 1 - t
			}
			x, y, r := s.X0+(s.X1-s.X0)*t, s.Y0+(s.Y1-s.Y0)*t, s.R0+(s.R1-s.R0)*t
			if r <= 0 {
				continue
			}
			out.Add(&Fill{Path: EllipsePath(x-r, y-r, 2*r, 2*r).Transformed(s.M), Color: col(t)})
		}
		return
	}
	// The stretch of the gradient across the shape, in its own space.
	dx, dy := s.X1-s.X0, s.Y1-s.Y0
	d := dx*dx + dy*dy
	if d == 0 {
		far := s.Path.Bounds().grow(1)
		out.Add(&Fill{Path: RectPath(far.X0, far.Y0, far.X1-far.X0, far.Y1-far.Y0), Color: col(1)})
		return
	}
	tMin, tMax := math.Inf(1), math.Inf(-1)
	across := 0.0
	l := math.Sqrt(d)
	nx, ny := -dy/l, dx/l
	for _, c := range [4][2]float64{{box.X0, box.Y0}, {box.X1, box.Y0}, {box.X1, box.Y1}, {box.X0, box.Y1}} {
		x, y := inv.Apply(c[0], c[1])
		t := ((x-s.X0)*dx + (y-s.Y0)*dy) / d
		tMin, tMax = min(tMin, t), max(tMax, t)
		across = max(across, math.Abs((x-s.X0)*nx+(y-s.Y0)*ny))
	}
	across += 1
	strip := func(from float64, c color.NRGBA) {
		end := tMax + 1/l
		ax, ay := s.X0+dx*from, s.Y0+dy*from
		bx, by := s.X0+dx*end, s.Y0+dy*end
		p := &Path{}
		p.MoveTo(ax+nx*across, ay+ny*across)
		p.LineTo(bx+nx*across, by+ny*across)
		p.LineTo(bx-nx*across, by-ny*across)
		p.LineTo(ax-nx*across, ay-ny*across)
		p.Close()
		out.Add(&Fill{Path: p.Transformed(s.M), Color: c})
	}
	strip(tMin-1/l, col(0))
	for i := 1; i < n; i++ {
		t := (float64(i) + 0.5) / float64(n)
		if t >= tMin && t <= tMax {
			strip(float64(i)/float64(n), col(t))
		}
	}
	if tMax >= 1 {
		strip(1, col(1))
	}
}

// Paper is a sheet: its size and how far from each edge a printer can mark, in points.
type Paper struct {
	W, H                     float64
	Left, Top, Right, Bottom float64
}

// Placement maps a page onto paper. A page turned the other way from the paper is turned to suit. It prints at its
// size, centred on the sheet, unless it asks to fill the paper or is too large for it: then it is scaled to what
// the printer can reach.
func Placement(pageW, pageH float64, fit bool, paper Paper) Matrix {
	w, h := pageW, pageH
	turn := (pageW > pageH) != (paper.W > paper.H) && pageW != pageH
	if turn {
		w, h = h, w
	}
	availW, availH := paper.W-paper.Left-paper.Right, paper.H-paper.Top-paper.Bottom
	scale := 1.0
	switch {
	case w <= 0 || h <= 0 || availW <= 0 || availH <= 0:
	case fit:
		scale = min(availW/w, availH/h)
	case w > paper.W+1 || h > paper.H+1:
		scale = min(availW/w, availH/h)
	}
	cx, cy := paper.W/2, paper.H/2
	if fit || scale != 1 {
		// Scaled pages sit in the middle of what can be marked.
		cx, cy = paper.Left+availW/2, paper.Top+availH/2
	}
	m := Translate(cx, cy).Mul(Scale(scale, scale))
	if turn {
		m = m.Mul(Matrix{0, 1, -1, 0, 0, 0})
	}
	return m.Mul(Translate(-pageW/2, -pageH/2))
}
