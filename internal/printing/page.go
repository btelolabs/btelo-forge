// Package printing is what every format prints through: a display list of pages in points, with paths, text as
// outlines, pictures and gradients, and the backends that take it to a PDF file, a Windows printer or the screen. The
// preview, the printer and the exported file all read the same list, so they cannot differ.
package printing

import (
	"image"
	"image/color"
	"math"
)

// MinLine is the thinnest line that is sure to print, in points. A thinner one is drawn this wide.
const MinLine = 0.25

// Point is a position on a page, in points from its top left.
type Point struct{ X, Y float64 }

// Matrix is an affine map: x' = a·x + c·y + e, y' = b·x + d·y + f.
type Matrix [6]float64

// Identity leaves points where they are.
var Identity = Matrix{1, 0, 0, 1, 0, 0}

// Translate moves points.
func Translate(x, y float64) Matrix { return Matrix{1, 0, 0, 1, x, y} }

// Scale scales points about the origin.
func Scale(x, y float64) Matrix { return Matrix{x, 0, 0, y, 0, 0} }

// Apply maps a point.
func (m Matrix) Apply(x, y float64) (float64, float64) {
	return m[0]*x + m[2]*y + m[4], m[1]*x + m[3]*y + m[5]
}

// Mul is the map that applies n first and then m.
func (m Matrix) Mul(n Matrix) Matrix {
	return Matrix{
		m[0]*n[0] + m[2]*n[1], m[1]*n[0] + m[3]*n[1],
		m[0]*n[2] + m[2]*n[3], m[1]*n[2] + m[3]*n[3],
		m[0]*n[4] + m[2]*n[5] + m[4], m[1]*n[4] + m[3]*n[5] + m[5],
	}
}

// Inverse is the map back, and false when m flattens the plane.
func (m Matrix) Inverse() (Matrix, bool) {
	det := m[0]*m[3] - m[1]*m[2]
	if det == 0 || math.IsNaN(det) || math.IsInf(det, 0) {
		return Identity, false
	}
	return Matrix{m[3] / det, -m[1] / det, -m[2] / det, m[0] / det,
		(m[2]*m[5] - m[3]*m[4]) / det, (m[1]*m[4] - m[0]*m[5]) / det}, true
}

// Size is how much m scales lengths, on average.
func (m Matrix) Size() float64 { return math.Sqrt(math.Abs(m[0]*m[3] - m[1]*m[2])) }

// Path is a run of segments. Ops holds one letter a segment: 'M' and 'L' take one point of Pts, 'C' three, 'Z' none.
type Path struct {
	Ops []byte
	Pts []Point
}

// MoveTo starts a contour.
func (p *Path) MoveTo(x, y float64) {
	p.Ops = append(p.Ops, 'M')
	p.Pts = append(p.Pts, Point{x, y})
}

// LineTo adds a straight segment.
func (p *Path) LineTo(x, y float64) {
	p.Ops = append(p.Ops, 'L')
	p.Pts = append(p.Pts, Point{x, y})
}

// CurveTo adds a cubic Bézier.
func (p *Path) CurveTo(x1, y1, x2, y2, x3, y3 float64) {
	p.Ops = append(p.Ops, 'C')
	p.Pts = append(p.Pts, Point{x1, y1}, Point{x2, y2}, Point{x3, y3})
}

// Close ends a contour by joining it to its start.
func (p *Path) Close() { p.Ops = append(p.Ops, 'Z') }

// Empty reports that the path has no segments.
func (p *Path) Empty() bool { return p == nil || len(p.Ops) == 0 }

// RectPath is a rectangle.
func RectPath(x, y, w, h float64) *Path {
	p := &Path{}
	p.MoveTo(x, y)
	p.LineTo(x+w, y)
	p.LineTo(x+w, y+h)
	p.LineTo(x, y+h)
	p.Close()
	return p
}

// kappa places a cubic's handles to follow a quarter circle closely.
const kappa = 0.5522847498

// RoundRectPath is a rectangle with corners of the given radius.
func RoundRectPath(x, y, w, h, radius float64) *Path {
	radius = min(radius, w/2, h/2)
	if radius <= 0 {
		return RectPath(x, y, w, h)
	}
	k := radius * (1 - kappa)
	r, b := x+w, y+h
	p := &Path{}
	p.MoveTo(x+radius, y)
	p.LineTo(r-radius, y)
	p.CurveTo(r-k, y, r, y+k, r, y+radius)
	p.LineTo(r, b-radius)
	p.CurveTo(r, b-k, r-k, b, r-radius, b)
	p.LineTo(x+radius, b)
	p.CurveTo(x+k, b, x, b-k, x, b-radius)
	p.LineTo(x, y+radius)
	p.CurveTo(x, y+k, x+k, y, x+radius, y)
	p.Close()
	return p
}

// EllipsePath is the ellipse in a box.
func EllipsePath(x, y, w, h float64) *Path {
	rx, ry := w/2, h/2
	cx, cy := x+rx, y+ry
	kx, ky := rx*kappa, ry*kappa
	p := &Path{}
	p.MoveTo(cx+rx, cy)
	p.CurveTo(cx+rx, cy+ky, cx+kx, cy+ry, cx, cy+ry)
	p.CurveTo(cx-kx, cy+ry, cx-rx, cy+ky, cx-rx, cy)
	p.CurveTo(cx-rx, cy-ky, cx-kx, cy-ry, cx, cy-ry)
	p.CurveTo(cx+kx, cy-ry, cx+rx, cy-ky, cx+rx, cy)
	p.Close()
	return p
}

// Transformed is a copy of the path through m.
func (p *Path) Transformed(m Matrix) *Path {
	if p == nil {
		return nil
	}
	out := &Path{Ops: p.Ops, Pts: make([]Point, len(p.Pts))}
	for i, q := range p.Pts {
		out.Pts[i].X, out.Pts[i].Y = m.Apply(q.X, q.Y)
	}
	return out
}

// Append adds q's segments to p.
func (p *Path) Append(q *Path) {
	if q != nil {
		p.Ops = append(p.Ops, q.Ops...)
		p.Pts = append(p.Pts, q.Pts...)
	}
}

// Box is a rectangle by its edges. An empty one has X0 past X1.
type Box struct{ X0, Y0, X1, Y1 float64 }

func emptyBox() Box { return Box{math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)} }

func (b Box) empty() bool { return !(b.X0 < b.X1 && b.Y0 < b.Y1) }

func (b Box) add(x, y float64) Box {
	return Box{min(b.X0, x), min(b.Y0, y), max(b.X1, x), max(b.Y1, y)}
}

func (b Box) union(o Box) Box {
	return Box{min(b.X0, o.X0), min(b.Y0, o.Y0), max(b.X1, o.X1), max(b.Y1, o.Y1)}
}

func (b Box) intersect(o Box) Box {
	return Box{max(b.X0, o.X0), max(b.Y0, o.Y0), min(b.X1, o.X1), min(b.Y1, o.Y1)}
}

func (b Box) overlaps(o Box) bool {
	return b.X0 < o.X1 && o.X0 < b.X1 && b.Y0 < o.Y1 && o.Y0 < b.Y1
}

func (b Box) grow(d float64) Box { return Box{b.X0 - d, b.Y0 - d, b.X1 + d, b.Y1 + d} }

func (b Box) contains(o Box) bool {
	return b.X0 <= o.X0 && b.Y0 <= o.Y0 && b.X1 >= o.X1 && b.Y1 >= o.Y1
}

// Bounds is the box around the path's points, handles included.
func (p *Path) Bounds() Box {
	b := emptyBox()
	if p != nil {
		for _, q := range p.Pts {
			b = b.add(q.X, q.Y)
		}
	}
	return b
}

// Cap is how an open line ends.
type Cap uint8

// Join is how a line turns a corner.
type Join uint8

const (
	CapButt Cap = iota
	CapRound
	CapSquare
)

const (
	JoinMiter Join = iota
	JoinRound
	JoinBevel
)

// Op is one thing drawn on a page: *Fill, *Stroke, *Clip, *Unclip, *Picture, *Text or *Shade.
type Op interface{ op() }

// Fill paints the inside of a path.
type Fill struct {
	Path    *Path
	EvenOdd bool
	Color   color.NRGBA
}

// Stroke draws a line along a path, Width points wide.
type Stroke struct {
	Path  *Path
	Width float64
	Cap   Cap
	Join  Join
	Miter float64 // the longest a miter may be, in widths; 0 means 10
	Dash  []float64
	Phase float64
	Color color.NRGBA
}

// Clip narrows what is drawn to the inside of a path, until the Unclip that matches it.
type Clip struct {
	Path    *Path
	EvenOdd bool
}

// Unclip undoes the last Clip.
type Unclip struct{}

// Picture is an image. M maps the unit square onto the page, its (0, 0) on the image's top left corner. Img is
// premultiplied; JPEG, when set, is the same picture as a file a backend can pass on as it is, W × H pixels, and Img
// may then be nil.
type Picture struct {
	Img   *image.RGBA
	JPEG  []byte
	W, H  int
	M     Matrix
	Alpha float64 // 0 means opaque
}

// Outline is a character's shape, in ems with y going up, and how far it moves the pen.
type Outline struct {
	Path    *Path
	Advance float64
}

// Glyph is an outline at a place in its Text's space, in ems along the baseline, with the words it stands for.
type Glyph struct {
	Outline *Outline
	X, Y    float64
	Text    string
}

// Text is a run of characters. M maps its space, which has y going up, onto the page.
type Text struct {
	Glyphs []Glyph
	M      Matrix
	Color  color.NRGBA
}

// Stop is a colour of a gradient at a place along it, 0 to 1. Gradients are opaque; Shade.Alpha fades one as a whole.
type Stop struct {
	At    float64
	Color color.NRGBA
}

// Shade fills a path with a gradient. An axial one runs from (X0, Y0) to (X1, Y1), a radial one from the circle at
// (X0, Y0) of radius R0 to that at (X1, Y1) of R1, both in the space M maps onto the page.
type Shade struct {
	Path       *Path
	EvenOdd    bool
	Radial     bool
	X0, Y0, R0 float64
	X1, Y1, R1 float64
	Stops      []Stop
	Extend     [2]bool // goes on past each end
	M          Matrix
	Alpha      float64 // 0 means opaque
}

func (*Fill) op()    {}
func (*Stroke) op()  {}
func (*Clip) op()    {}
func (*Unclip) op()  {}
func (*Picture) op() {}
func (*Text) op()    {}
func (*Shade) op()   {}

// Page is one page: its size in points and what is on it, bottom first, over white.
type Page struct {
	W, H float64
	Ops  []Op
	// Fit asks a printer to scale the page to fill the paper; otherwise it prints at its size unless it does not fit.
	Fit bool
}

// Doc is pages with a name.
type Doc struct {
	Title string
	Pages []*Page
}

// Add puts an op on the page. Lines and thin bars that would not print are made wide enough to.
func (p *Page) Add(op Op) {
	switch o := op.(type) {
	case *Stroke:
		o.Width = max(o.Width, MinLine)
	case *Fill:
		o.Path = thicken(o.Path)
	}
	p.Ops = append(p.Ops, op)
}

// thicken widens a rectangle thinner than a printable line, as a rule drawn as a filled bar is.
func thicken(p *Path) *Path {
	if p == nil || len(p.Pts) != 4 && len(p.Pts) != 5 {
		return p
	}
	for _, o := range p.Ops {
		if o == 'C' {
			return p
		}
	}
	for i, q := range p.Pts[:4] {
		n := p.Pts[(i+1)%4]
		if q.X != n.X && q.Y != n.Y {
			return p
		}
	}
	b := p.Bounds()
	w, h := b.X1-b.X0, b.Y1-b.Y0
	if (w >= MinLine && h >= MinLine) || w == 0 && h == 0 {
		return p
	}
	if w < MinLine {
		c := (b.X0 + b.X1) / 2
		b.X0, b.X1 = c-MinLine/2, c+MinLine/2
	}
	if h < MinLine {
		c := (b.Y0 + b.Y1) / 2
		b.Y0, b.Y1 = c-MinLine/2, c+MinLine/2
	}
	return RectPath(b.X0, b.Y0, b.X1-b.X0, b.Y1-b.Y0)
}

// ClipRect is a rectangular Clip.
func (p *Page) ClipRect(x, y, w, h float64) { p.Add(&Clip{Path: RectPath(x, y, w, h)}) }

// Place draws everything on q onto p through m.
func (p *Page) Place(q *Page, m Matrix) {
	k := m.Size()
	for _, op := range q.Ops {
		switch o := op.(type) {
		case *Fill:
			c := *o
			c.Path = o.Path.Transformed(m)
			p.Add(&c)
		case *Stroke:
			c := *o
			c.Path = o.Path.Transformed(m)
			c.Width *= k
			c.Phase *= k
			if o.Dash != nil {
				c.Dash = make([]float64, len(o.Dash))
				for i, v := range o.Dash {
					c.Dash[i] = v * k
				}
			}
			p.Add(&c)
		case *Clip:
			c := *o
			c.Path = o.Path.Transformed(m)
			p.Add(&c)
		case *Unclip:
			p.Add(o)
		case *Picture:
			c := *o
			c.M = m.Mul(o.M)
			p.Add(&c)
		case *Text:
			c := *o
			c.M = m.Mul(o.M)
			p.Add(&c)
		case *Shade:
			c := *o
			c.Path = o.Path.Transformed(m)
			c.M = m.Mul(o.M)
			p.Add(&c)
		}
	}
}

// bounds is the box on the page an op can mark, unclipped.
func bounds(op Op) Box {
	switch o := op.(type) {
	case *Fill:
		return o.Path.Bounds()
	case *Stroke:
		miter := o.Miter
		if miter == 0 {
			miter = 10
		}
		reach := o.Width / 2 * max(miter, 1.5)
		if o.Join != JoinMiter {
			reach = o.Width
		}
		return o.Path.Bounds().grow(reach)
	case *Picture:
		return unitBox(o.M)
	case *Text:
		return textBounds(o)
	case *Shade:
		return o.Path.Bounds()
	}
	return emptyBox()
}

// unitBox is the box around the unit square through m.
func unitBox(m Matrix) Box {
	b := emptyBox()
	for _, c := range [4][2]float64{{0, 0}, {1, 0}, {1, 1}, {0, 1}} {
		x, y := m.Apply(c[0], c[1])
		b = b.add(x, y)
	}
	return b
}

func textBounds(t *Text) Box {
	b := emptyBox()
	for i := range t.Glyphs {
		g := &t.Glyphs[i]
		if g.Outline == nil || g.Outline.Path.Empty() {
			continue
		}
		o := g.Outline.Path.Bounds()
		for _, c := range [4][2]float64{{o.X0, o.Y0}, {o.X1, o.Y0}, {o.X1, o.Y1}, {o.X0, o.Y1}} {
			x, y := t.M.Apply(c[0]+g.X, c[1]+g.Y)
			b = b.add(x, y)
		}
	}
	return b
}

// TextPath is the outlines of a run of text on the page.
func TextPath(t *Text) *Path {
	out := &Path{}
	for i := range t.Glyphs {
		g := &t.Glyphs[i]
		if g.Outline != nil && !g.Outline.Path.Empty() {
			out.Append(g.Outline.Path.Transformed(t.M.Mul(Translate(g.X, g.Y))))
		}
	}
	return out
}

// shadeColor is the gradient's colour at t.
func shadeColor(stops []Stop, t float64) color.NRGBA {
	if len(stops) == 0 {
		return color.NRGBA{A: 255}
	}
	if t <= stops[0].At {
		return stops[0].Color
	}
	for i := 1; i < len(stops); i++ {
		if t <= stops[i].At {
			a, b := stops[i-1], stops[i]
			u := 0.0
			if b.At > a.At {
				u = (t - a.At) / (b.At - a.At)
			}
			mix := func(x, y uint8) uint8 { return uint8(float64(x)*(1-u) + float64(y)*u + 0.5) }
			return color.NRGBA{mix(a.Color.R, b.Color.R), mix(a.Color.G, b.Color.G), mix(a.Color.B, b.Color.B), mix(a.Color.A, b.Color.A)}
		}
	}
	return stops[len(stops)-1].Color
}
