package pptx

import (
	"math"
	"strconv"

	"github.com/btelolabs/btelo-forge/internal/office/ox"
)

// Kind is what a shape is.
type Kind uint8

const (
	KindShape Kind = iota
	KindPicture
	KindGroup
	KindConnector
	KindTable
	KindGraphic // a chart, diagram, embedded object or something else of a frame's
	KindOther   // an element the tree may hold that is not understood, drawn as nothing
)

// Box is where a shape is on the slide, in points: the unrotated box, turned Rot degrees clockwise around its centre,
// after being mirrored.
type Box struct {
	X, Y, W, H   float64
	Rot          float64
	FlipH, FlipV bool
}

// Ph names a placeholder.
type Ph struct {
	Type string // as the file has it; "" where it leaves the default, an object placeholder
	Idx  int
}

// Shape is an element of a slide's, layout's or master's shape tree, seen through the inheritance.
type Shape struct {
	Node   *ox.Node // the element: p:sp, p:pic, p:grpSp, p:cxnSp or p:graphicFrame
	Alt    *ox.Node // the mc:AlternateContent it stands in, if any
	Kind   Kind
	ID     int
	Name   string
	Hidden bool
	Box    Box
	Ph     *Ph
	Parent *Shape
	Kids   []*Shape
	Chain  []*Shape // the placeholders it inherits from, the layout's then the master's
}

// Matrix maps x, y to a*x + c*y + e, b*x + d*y + f.
type Matrix [6]float64

// Identity leaves points where they are.
var Identity = Matrix{1, 0, 0, 1, 0, 0}

// Mul is the matrix that applies k and then m.
func (m Matrix) Mul(k Matrix) Matrix {
	return Matrix{
		m[0]*k[0] + m[2]*k[1], m[1]*k[0] + m[3]*k[1],
		m[0]*k[2] + m[2]*k[3], m[1]*k[2] + m[3]*k[3],
		m[0]*k[4] + m[2]*k[5] + m[4], m[1]*k[4] + m[3]*k[5] + m[5],
	}
}

// Apply maps a point.
func (m Matrix) Apply(x, y float64) (float64, float64) {
	return m[0]*x + m[2]*y + m[4], m[1]*x + m[3]*y + m[5]
}

// Inverse undoes the matrix; a matrix that cannot be undone gives the identity.
func (m Matrix) Inverse() Matrix {
	det := m[0]*m[3] - m[1]*m[2]
	if det == 0 {
		return Identity
	}
	return Matrix{m[3] / det, -m[1] / det, -m[2] / det, m[0] / det,
		(m[2]*m[5] - m[3]*m[4]) / det, (m[1]*m[4] - m[0]*m[5]) / det}
}

// Translate moves points.
func Translate(x, y float64) Matrix { return Matrix{1, 0, 0, 1, x, y} }

// Scale scales points about the origin.
func Scale(x, y float64) Matrix { return Matrix{x, 0, 0, y, 0, 0} }

// Rotate turns points clockwise, y being down, by deg degrees.
func Rotate(deg float64) Matrix {
	s, c := math.Sincos(deg * math.Pi / 180)
	return Matrix{c, s, -s, c, 0, 0}
}

// Local maps the shape's own coordinates, 0..W by 0..H with the mirror and rotation, onto the slide.
func (b Box) Local() Matrix {
	fx, fy := 1.0, 1.0
	if b.FlipH {
		fx = -1
	}
	if b.FlipV {
		fy = -1
	}
	return Translate(b.X+b.W/2, b.Y+b.H/2).Mul(Rotate(b.Rot)).Mul(Scale(fx, fy)).Mul(Translate(-b.W/2, -b.H/2))
}

// Upright maps the shape's box as its words see it onto the slide: words turn with a shape but do not mirror, and a
// shape flipped upside down has them upside down.
func (b Box) Upright() Matrix {
	rot := b.Rot
	if b.FlipV {
		rot += 180
	}
	return Translate(b.X+b.W/2, b.Y+b.H/2).Mul(Rotate(rot)).Mul(Translate(-b.W/2, -b.H/2))
}

// Bounds is the box around the turned shape, as x, y, width and height.
func (b Box) Bounds() (x, y, w, h float64) {
	m := b.Local()
	x0, y0, x1, y1 := math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)
	for _, p := range [4][2]float64{{0, 0}, {b.W, 0}, {b.W, b.H}, {0, b.H}} {
		px, py := m.Apply(p[0], p[1])
		x0, y0, x1, y1 = min(x0, px), min(y0, py), max(x1, px), max(y1, py)
	}
	return x0, y0, x1 - x0, y1 - y0
}

// Contains reports that the point on the slide is inside the box, with a margin in points.
func (b Box) Contains(x, y, margin float64) bool {
	lx, ly := b.Local().Inverse().Apply(x, y)
	return lx >= -margin && ly >= -margin && lx <= b.W+margin && ly <= b.H+margin
}

// xform is an a:xfrm or p:xfrm read, in points.
type xform struct {
	box    Box
	chX    float64
	chY    float64
	chW    float64
	chH    float64
	exists bool
}

func readXfrm(n *ox.Node) xform {
	if n == nil {
		return xform{}
	}
	off, ext := n.Child("a:off"), n.Child("a:ext")
	x := xform{exists: off != nil && ext != nil}
	x.box = Box{X: attrFloat(off, "x", 0) / emuPerPt, Y: attrFloat(off, "y", 0) / emuPerPt,
		W: attrFloat(ext, "cx", 0) / emuPerPt, H: attrFloat(ext, "cy", 0) / emuPerPt,
		Rot: attrFloat(n, "rot", 0) / 60000, FlipH: attrBool(n, "flipH", false), FlipV: attrBool(n, "flipV", false)}
	ch, che := n.Child("a:chOff"), n.Child("a:chExt")
	x.chX, x.chY = attrFloat(ch, "x", 0)/emuPerPt, attrFloat(ch, "y", 0)/emuPerPt
	x.chW, x.chH = attrFloat(che, "cx", 0)/emuPerPt, attrFloat(che, "cy", 0)/emuPerPt
	return x
}

// groupFrame carries a group's mapping from its children's coordinates onto the slide.
type groupFrame struct {
	sx, sy   float64 // scale
	ox, oy   float64 // the group's child origin
	box      Box     // the group, already placed on the slide
	centered bool    // false at the top of the tree, where nothing maps
}

// place puts a child's box on the slide.
func (g groupFrame) place(b Box) Box {
	if !g.centered {
		return b
	}
	cx := (b.X+b.W/2-g.ox)*g.sx - g.box.W/2
	cy := (b.Y+b.H/2-g.oy)*g.sy - g.box.H/2
	b.W, b.H = b.W*g.sx, b.H*g.sy
	if g.box.FlipH {
		cx = -cx
		b.FlipH = !b.FlipH
		b.Rot = -b.Rot
	}
	if g.box.FlipV {
		cy = -cy
		b.FlipV = !b.FlipV
		b.Rot = -b.Rot
	}
	px, py := Rotate(g.box.Rot).Apply(cx, cy)
	b.X, b.Y = g.box.X+g.box.W/2+px-b.W/2, g.box.Y+g.box.H/2+py-b.H/2
	b.Rot = math.Mod(b.Rot+g.box.Rot+720, 360)
	return b
}

// inheritFn finds what a placeholder inherits from.
type inheritFn func(*Ph) []*Shape

// treeShapes reads a layout's or master's tree; a layout passes its master.
func treeShapes(tree *ox.Node, m *Master) []*Shape {
	var inherit inheritFn
	if m != nil {
		inherit = func(ph *Ph) []*Shape {
			if k := findPh(m.shapes, ph, true); k != nil {
				return []*Shape{k}
			}
			return nil
		}
	}
	return readTree(tree, nil, groupFrame{}, inherit, 0)
}

func readTree(tree *ox.Node, parent *Shape, g groupFrame, inherit inheritFn, depth int) []*Shape {
	var out []*Shape
	if tree == nil || depth > maxGroups {
		return nil
	}
	for _, n := range tree.Elements() {
		var alt *ox.Node
		if n.Name == "mc:AlternateContent" {
			// The fallback is what any reader can draw: a choice needs what its Requires names.
			alt = n
			n = altBranch(n)
			if n == nil {
				continue
			}
		}
		s := readShape(n, parent, g, inherit, depth)
		if s != nil {
			s.Alt = alt
			out = append(out, s)
		}
	}
	return out
}

// altBranch is the element of an mc:AlternateContent to show.
func altBranch(alt *ox.Node) *ox.Node {
	if f := alt.Child("mc:Fallback").Elements(); len(f) > 0 {
		return f[0]
	}
	if c := alt.Child("mc:Choice").Elements(); len(c) > 0 {
		return c[0]
	}
	return nil
}

func readShape(n *ox.Node, parent *Shape, g groupFrame, inherit inheritFn, depth int) *Shape {
	s := &Shape{Node: n, Parent: parent}
	var nv *ox.Node
	var xf *ox.Node
	switch n.Name {
	case "p:sp":
		s.Kind, nv, xf = KindShape, n.Child("p:nvSpPr"), n.Path("p:spPr", "a:xfrm")
	case "p:pic":
		s.Kind, nv, xf = KindPicture, n.Child("p:nvPicPr"), n.Path("p:spPr", "a:xfrm")
	case "p:cxnSp":
		s.Kind, nv, xf = KindConnector, n.Child("p:nvCxnSpPr"), n.Path("p:spPr", "a:xfrm")
	case "p:grpSp":
		s.Kind, nv, xf = KindGroup, n.Child("p:nvGrpSpPr"), n.Path("p:grpSpPr", "a:xfrm")
	case "p:graphicFrame":
		s.Kind, nv, xf = KindGraphic, n.Child("p:nvGraphicFramePr"), n.Child("p:xfrm")
		if n.Path("a:graphic", "a:graphicData", "a:tbl") != nil {
			s.Kind = KindTable
		}
	default:
		return nil // p:nvGrpSpPr, p:grpSpPr, p:extLst and p:contentPart hold no shape to draw
	}
	cNv := nv.Elements()
	if len(cNv) > 0 {
		c := cNv[0]
		s.ID, _ = strconv.Atoi(c.Attr("id"))
		s.Name = c.Attr("name")
		s.Hidden = attrBool(c, "hidden", false)
	}
	if ph := nv.Path("p:nvPr", "p:ph"); ph != nil {
		s.Ph = &Ph{Type: ph.Attr("type"), Idx: int(attrInt(ph, "idx", 0))}
		if inherit != nil {
			s.Chain = inherit(s.Ph)
		}
	}
	x := readXfrm(xf)
	if !x.exists {
		for _, k := range s.Chain {
			if k.Box.W != 0 || k.Box.H != 0 {
				x.box = k.Box
				x.exists = true
				break
			}
		}
	}
	s.Box = g.place(x.box)
	if s.Kind == KindGroup {
		f := groupFrame{sx: 1, sy: 1, ox: x.chX, oy: x.chY, box: s.Box, centered: true}
		if x.chW > 0 {
			f.sx = s.Box.W / x.chW
		}
		if x.chH > 0 {
			f.sy = s.Box.H / x.chH
		}
		s.Kids = readTree(n, s, f, inherit, depth+1)
	}
	return s
}

// family is a placeholder's type as the master sees it.
func family(t string) string {
	switch t {
	case "title", "ctrTitle":
		return "title"
	case "dt", "ftr", "sldNum", "pic", "hdr", "sldImg":
		return t
	}
	return "body"
}

// findPh is the placeholder of list that ph inherits from. A master has one of each family; a layout's are told
// apart by idx.
func findPh(list []*Shape, ph *Ph, master bool) *Shape {
	if master {
		fam := family(ph.Type)
		if fam == "pic" {
			fam = "body"
		}
		for _, s := range list {
			if s.Ph != nil && family(s.Ph.Type) == fam {
				return s
			}
		}
		return nil
	}
	for _, s := range list {
		if s.Ph != nil && s.Ph.Idx == ph.Idx && (ph.Idx != 0 || family(s.Ph.Type) == family(ph.Type)) {
			return s
		}
	}
	for _, s := range list {
		if s.Ph != nil && s.Ph.Type == ph.Type && ph.Type != "" {
			return s
		}
	}
	return nil
}

// Shapes are the slide's shapes, bottom first, with their inheritance; the list is read again after a change.
func (d *Doc) Shapes(s *Slide) []*Shape {
	if s.shapesAt == s.Rev && s.shapesAt != 0 {
		return s.shapes
	}
	return d.reread(s)
}

func (d *Doc) reread(s *Slide) []*Shape {
	l := s.Layout
	inherit := func(ph *Ph) []*Shape {
		var chain []*Shape
		if k := findPh(l.shapes, ph, false); k != nil {
			chain = append(chain, k)
			chain = append(chain, k.Chain...)
		} else if k := findPh(l.Master.shapes, ph, true); k != nil {
			chain = append(chain, k)
		}
		return chain
	}
	s.shapes = readTree(s.tree().Root.Path("p:cSld", "p:spTree"), nil, groupFrame{}, inherit, 0)
	s.shapesAt = s.Rev
	return s.shapes
}

// Find is the shape with the id, at any depth, or nil.
func (d *Doc) Find(s *Slide, id int) *Shape {
	return findID(d.Shapes(s), id)
}

func findID(list []*Shape, id int) *Shape {
	for _, k := range list {
		if k.ID == id {
			return k
		}
		if f := findID(k.Kids, id); f != nil {
			return f
		}
	}
	return nil
}

// Walk calls fn for every shape, children after their group.
func Walk(list []*Shape, fn func(*Shape)) {
	for _, k := range list {
		fn(k)
		Walk(k.Kids, fn)
	}
}

// Top is the outermost group holding the shape, or the shape.
func (s *Shape) Top() *Shape {
	for s.Parent != nil {
		s = s.Parent
	}
	return s
}

// Text body element of a shape, nil when it has none.
func (s *Shape) body() *ox.Node {
	switch s.Kind {
	case KindShape, KindConnector:
		return s.Node.Child("p:txBody")
	}
	return nil
}

// HasText reports that the shape holds words.
func (s *Shape) HasText() bool {
	b := s.body()
	if b == nil {
		return false
	}
	for _, t := range b.All("a:t") {
		if t.InnerText() != "" {
			return true
		}
	}
	return len(b.All("a:fld")) > 0 || len(b.All("a:br")) > 0
}

// EmptyPlaceholder reports a placeholder with nothing in it, which shows its prompt in the editor alone.
func (s *Shape) EmptyPlaceholder() bool {
	return s.Ph != nil && s.Kind == KindShape && !s.HasText()
}
