package pptx

import (
	"image/color"
	"sort"
	"strings"

	"github.com/btelolabs/btelo-forge/internal/office/opc"
	"github.com/btelolabs/btelo-forge/internal/office/ox"
)

// scope is the part a piece of XML sits in: what its colours mean and where its relationships lead.
type scope struct {
	col  *colorCtx
	part string
	rels []opc.Rel
}

// Stop is a gradient stop.
type Stop struct {
	Pos   float64 // 0 to 1
	Color color.RGBA
}

// Fill is a fill read out of the XML.
type Fill struct {
	None    bool
	Color   color.RGBA
	Stops   []Stop
	Path    string  // "" linear; "circle", "rect" or "shape"
	Angle   float64 // degrees clockwise, for a linear gradient
	Scaled  bool
	Focus   [4]float64 // a path gradient's fillToRect: left, top, right, bottom in fractions
	Rotates bool       // the gradient turns with the shape
	Pic     string     // the picture's part
	Crop    [4]float64 // srcRect: left, top, right, bottom in fractions
	Stretch [4]float64 // fillRect, the same way
	Tile    *Tile
	Alpha   float64 // what the picture's own alpha is multiplied by
}

// Tile is how a picture repeats.
type Tile struct {
	X, Y, SX, SY float64 // offset in points, scale as a fraction
}

var fillNames = []string{"a:noFill", "a:solidFill", "a:gradFill", "a:blipFill", "a:pattFill", "a:grpFill"}

// fillChild is the fill element among n's children.
func fillChild(n *ox.Node) *ox.Node {
	for _, c := range n.Elements() {
		for _, name := range fillNames {
			if c.Name == name || c.Name == "p:blipFill" {
				return c
			}
		}
	}
	return nil
}

// readFill reads a fill element; nil is no opinion, a Fill with None set is a fill of nothing.
func (x *scope) readFill(n *ox.Node) *Fill {
	if n == nil {
		return nil
	}
	switch n.Name {
	case "a:noFill":
		return &Fill{None: true}
	case "a:solidFill":
		els := n.Elements()
		if len(els) == 0 {
			return &Fill{None: true}
		}
		c, ok := x.col.resolve(els[0])
		if !ok {
			return &Fill{None: true}
		}
		return &Fill{Color: c}
	case "a:gradFill":
		f := &Fill{Rotates: attrBool(n, "rotWithShape", true)}
		for _, g := range n.Child("a:gsLst").Elements("a:gs") {
			if els := g.Elements(); len(els) > 0 {
				if c, ok := x.col.resolve(els[0]); ok {
					f.Stops = append(f.Stops, Stop{clampF(attrFloat(g, "pos", 0)/100000, 0, 1), c})
				}
			}
		}
		if len(f.Stops) == 0 {
			return &Fill{None: true}
		}
		sort.SliceStable(f.Stops, func(i, j int) bool { return f.Stops[i].Pos < f.Stops[j].Pos })
		f.Color = f.Stops[0].Color
		if lin := n.Child("a:lin"); lin != nil {
			f.Angle, f.Scaled = attrFloat(lin, "ang", 0)/60000, attrBool(lin, "scaled", false)
		}
		if p := n.Child("a:path"); p != nil {
			f.Path = p.Attr("path")
			if f.Path == "" {
				f.Path = "circle"
			}
			r := p.Child("a:fillToRect")
			f.Focus = [4]float64{attrFloat(r, "l", 0) / 100000, attrFloat(r, "t", 0) / 100000, attrFloat(r, "r", 0) / 100000, attrFloat(r, "b", 0) / 100000}
		}
		return f
	case "a:blipFill", "p:blipFill":
		return x.readBlip(n)
	case "a:pattFill":
		// A pattern is shown as the tint it comes to.
		fg, _ := x.col.resolve(first(n.Child("a:fgClr")))
		bg, _ := x.col.resolve(first(n.Child("a:bgClr")))
		k := patternShare(n.Attr("prst"))
		l := func(a, b uint8) uint8 { return uint8(float64(a)*k + float64(b)*(1-k) + 0.5) }
		return &Fill{Color: color.RGBA{l(fg.R, bg.R), l(fg.G, bg.G), l(fg.B, bg.B), l(fg.A, bg.A)}}
	}
	return nil
}

func first(n *ox.Node) *ox.Node {
	if els := n.Elements(); len(els) > 0 {
		return els[0]
	}
	return nil
}

func patternShare(prst string) float64 {
	if strings.HasPrefix(prst, "pct") {
		return attrFloat(&ox.Node{Attrs: []ox.Attr{{Name: "v", Value: prst[3:]}}}, "v", 50) / 100
	}
	return 0.5
}

func (x *scope) readBlip(n *ox.Node) *Fill {
	b := n.Child("a:blip")
	id := b.Attr("r:embed")
	f := &Fill{Alpha: 1, Rotates: attrBool(n, "rotWithShape", true)}
	if r, ok := opc.RelByID(x.rels, id); ok && !r.External {
		f.Pic = opc.Resolve(x.part, r.Target)
	}
	if am := b.Child("a:alphaModFix"); am != nil {
		f.Alpha = clampF(attrFloat(am, "amt", 100000)/100000, 0, 1)
	}
	if s := n.Child("a:srcRect"); s != nil {
		f.Crop = [4]float64{attrFloat(s, "l", 0) / 100000, attrFloat(s, "t", 0) / 100000, attrFloat(s, "r", 0) / 100000, attrFloat(s, "b", 0) / 100000}
	}
	if r := n.Path("a:stretch", "a:fillRect"); r != nil {
		f.Stretch = [4]float64{attrFloat(r, "l", 0) / 100000, attrFloat(r, "t", 0) / 100000, attrFloat(r, "r", 0) / 100000, attrFloat(r, "b", 0) / 100000}
	}
	if t := n.Child("a:tile"); t != nil {
		f.Tile = &Tile{X: attrFloat(t, "tx", 0) / emuPerPt, Y: attrFloat(t, "ty", 0) / emuPerPt,
			SX: max(attrFloat(t, "sx", 100000)/100000, 0.001), SY: max(attrFloat(t, "sy", 100000)/100000, 0.001)}
	}
	return f
}

// Line is a line read out of the XML, in points.
type Line struct {
	Fill  *Fill
	Width float64
	Dash  []float64 // on, off, … in multiples of the width
	Cap   string
	Join  string
	Miter float64
	Head  Arrow // at the start of the path
	Tail  Arrow // at its end
}

// Arrow is an arrowhead; Kind is "" for none.
type Arrow struct {
	Kind string
	W, L float64 // multiples of the line's width
}

var presetDashes = map[string][]float64{
	"dash": {4, 3}, "sysDash": {3, 1}, "dot": {1, 3}, "sysDot": {1, 1}, "lgDash": {8, 3},
	"dashDot": {4, 3, 1, 3}, "sysDashDot": {3, 1, 1, 1}, "lgDashDot": {8, 3, 1, 3},
	"lgDashDotDot": {8, 3, 1, 3, 1, 3}, "sysDashDotDot": {3, 1, 1, 1, 1, 1},
}

// readLine lays an a:ln over a line read so far.
func (x *scope) readLine(n *ox.Node, l Line) Line {
	if n == nil {
		return l
	}
	if w, ok := n.Lookup("w"); ok && w != "" {
		l.Width = attrFloat(n, "w", 0) / emuPerPt
	}
	if c := n.Attr("cap"); c != "" {
		l.Cap = c
	}
	if f := x.readFill(fillChild(n)); f != nil {
		l.Fill = f
	}
	if d := n.Child("a:prstDash"); d != nil {
		l.Dash = presetDashes[d.Attr("val")]
	}
	if d := n.Child("a:custDash"); d != nil {
		l.Dash = nil
		for _, ds := range d.Elements("a:ds") {
			l.Dash = append(l.Dash, attrFloat(ds, "d", 100000)/100000, attrFloat(ds, "sp", 100000)/100000)
		}
	}
	switch {
	case n.Child("a:round") != nil:
		l.Join = "round"
	case n.Child("a:bevel") != nil:
		l.Join = "bevel"
	case n.Child("a:miter") != nil:
		l.Join, l.Miter = "miter", max(attrFloat(n.Child("a:miter"), "lim", 800000)/100000, 1)
	}
	if h := n.Child("a:headEnd"); h != nil {
		l.Head = readArrow(h)
	}
	if t := n.Child("a:tailEnd"); t != nil {
		l.Tail = readArrow(t)
	}
	return l
}

func readArrow(n *ox.Node) Arrow {
	k := n.Attr("type")
	if k == "none" {
		k = ""
	}
	size := func(s string) float64 {
		switch s {
		case "sm":
			return 2
		case "lg":
			return 5
		}
		return 3
	}
	return Arrow{Kind: k, W: size(n.Attr("w")), L: size(n.Attr("len"))}
}

// Shadow is an outer shadow, in points.
type Shadow struct {
	Blur, Dist, Dir float64
	Color           color.RGBA
}

// readShadow is the outer shadow of an a:effectLst, nil when it has none.
func (x *scope) readShadow(n *ox.Node) *Shadow {
	s := n.Child("a:outerShdw")
	if s == nil {
		return nil
	}
	c, ok := x.col.resolve(first(s))
	if !ok {
		return nil
	}
	return &Shadow{Blur: attrFloat(s, "blurRad", 0) / emuPerPt, Dist: attrFloat(s, "dist", 0) / emuPerPt, Dir: attrFloat(s, "dir", 0) / 60000, Color: c}
}

// themeItem is a style reference's entry in a theme list, as an element with phClr bound to the reference's colour.
func (x *scope) themeItem(ref *ox.Node, list []*ox.Node) (*ox.Node, *scope) {
	idx := int(attrInt(ref, "idx", 0))
	if idx <= 0 || idx > len(list) {
		return nil, nil
	}
	k := *x
	if c, ok := x.col.resolve(first(ref)); ok {
		k.col = x.col.withPh(c)
	}
	k.rels = x.col.theme.Rels
	k.part = x.col.theme.Part
	return list[idx-1], &k
}

// resolved is how a shape looks.
type resolved struct {
	fill   *Fill
	line   Line
	hasLn  bool
	shadow *Shadow
}

// slideScope is the scope of a slide's own XML: its colour map may be overridden, and a layout's likewise.
func (d *Doc) slideScope(s *Slide) *scope {
	l := s.Layout
	cm := l.Master.ClrMap
	if o := l.Doc.Root.Path("p:clrMapOvr", "a:overrideClrMapping"); o != nil {
		cm = clrMapOf(o)
	}
	if o := s.tree().Root.Path("p:clrMapOvr", "a:overrideClrMapping"); o != nil {
		cm = clrMapOf(o)
	}
	return &scope{col: &colorCtx{theme: l.Master.Theme, cmap: cm}, part: s.Part, rels: s.rels}
}

func layoutScope(l *Layout) *scope {
	cm := l.Master.ClrMap
	if o := l.Doc.Root.Path("p:clrMapOvr", "a:overrideClrMapping"); o != nil {
		cm = clrMapOf(o)
	}
	return &scope{col: &colorCtx{theme: l.Master.Theme, cmap: cm}, part: l.Part, rels: l.Rels}
}

func masterScope(m *Master) *scope {
	return &scope{col: &colorCtx{theme: m.Theme, cmap: m.ClrMap}, part: m.Part, rels: m.Rels}
}

// spPr is the shape's properties element.
func spPr(s *Shape) *ox.Node {
	if s.Kind == KindGroup {
		return s.Node.Child("p:grpSpPr")
	}
	return s.Node.Child("p:spPr")
}

// chainScope is the scope of a placeholder the shape inherits from.
func chainScope(k *Shape, l *Layout) *scope {
	for _, c := range l.shapes {
		if c == k {
			return layoutScope(l)
		}
	}
	return masterScope(l.Master)
}

// look works out a shape's fill, line and shadow: its own, then its placeholder's, then its style's.
func (d *Doc) look(sh *Shape, x *scope, l *Layout) resolved {
	var out resolved
	pr := spPr(sh)
	out.fill = x.readFill(fillChild(pr))
	if fc := fillChild(pr); fc != nil && fc.Name == "a:grpFill" {
		for p := sh.Parent; p != nil; p = p.Parent {
			if f := x.readFill(fillChild(spPr(p))); f != nil {
				out.fill = f
				break
			}
		}
		if out.fill == nil {
			out.fill = &Fill{None: true}
		}
	}
	ln := pr.Child("a:ln")
	effects := pr.Child("a:effectLst")
	if out.fill == nil || ln == nil || effects == nil {
		for _, k := range sh.Chain {
			kx := x
			if l != nil {
				kx = chainScope(k, l)
			}
			kp := spPr(k)
			if out.fill == nil {
				out.fill = kx.readFill(fillChild(kp))
			}
			if ln == nil && kp.Child("a:ln") != nil {
				out.line = kx.readLine(kp.Child("a:ln"), out.line)
				out.hasLn = true
			}
			if effects == nil && kp.Child("a:effectLst") != nil {
				out.shadow = kx.readShadow(kp.Child("a:effectLst"))
				effects = kp.Child("a:effectLst")
			}
		}
	}
	st := sh.Node.Child("p:style")
	if st != nil {
		theme := x.col.theme
		if out.fill == nil {
			if it, kx := x.themeItem(st.Child("a:fillRef"), theme.Fills); it != nil {
				out.fill = kx.readFill(it)
			}
		}
		if !out.hasLn {
			if it, kx := x.themeItem(st.Child("a:lnRef"), theme.Lines); it != nil {
				out.line = kx.readLine(it, out.line)
				out.hasLn = true
			}
		}
		if effects == nil {
			if it, kx := x.themeItem(st.Child("a:effectRef"), theme.Effects); it != nil {
				out.shadow = kx.readShadow(it.Child("a:effectLst"))
			}
		}
	}
	if ln != nil {
		out.line = x.readLine(ln, out.line)
		out.hasLn = true
	}
	if pr.Child("a:effectLst") != nil {
		out.shadow = x.readShadow(pr.Child("a:effectLst"))
	}
	if out.fill == nil {
		out.fill = &Fill{None: true}
	}
	return out
}
