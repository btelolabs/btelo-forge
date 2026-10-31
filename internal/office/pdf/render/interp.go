package render

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"math"

	"github.com/btelolabs/btelo-forge/internal/office/pdf"
)

// Limits of one page.
const (
	maxOps       = 40_000_000 // operators, forms and tiles included
	maxFormDepth = 28
	maxQStack    = 8192
	maxPathOps   = 8_000_000
	maxPixels    = 100_000_000
	maxBBoxPage  = 16000 // the widest canvas a page gets, in pixels
)

// paint is a fill or stroke colour.
type paint struct {
	cs      colorSpace
	comps   []float64
	rgb     [3]uint8
	pattern pdf.Object // set for a pattern colour
}

type textState struct {
	font                 *pdfFont
	size                 float64
	charSpace, wordSpace float64
	scale                float64
	leading              float64
	rise                 float64
	mode                 int
}

type clipState struct {
	r image.Rectangle
	m *mask // nil: the whole rectangle
}

// gstate is the graphics state. Saving copies it; what it points to never changes.
type gstate struct {
	ctm          pdf.Matrix
	clip         clipState
	fill, stroke paint
	line         strokeStyle
	ca, CA       float64
	blend        blendMode
	soft         *softMask
	text         textState
	alphaShape   bool
	vclip        *vclip // the clip as a printed page has it
}

// ctx interprets one content stream tree.
type ctx struct {
	c       context.Context
	doc     *pdf.Doc
	cache   *Cache
	opts    Options
	ra      raster
	ops     int
	err     error
	layer   *layer
	gs      *gstate
	stack   []*gstate
	res     pdf.Dict
	base    pdf.Matrix // the page's matrix, or the form's for patterns inside it
	depth   int
	inGlyph map[*pdf.Stream]bool // Type 3 glyphs being drawn
	hidden  int
	mc      []bool
	noDraw  bool
	// Text out.
	tm, tlm  pdf.Matrix
	textClip *poly
	out      *Text
	// Type 3 glyphs painted with a d1 take the text's colour.
	uncolored bool
	softCache map[softKey]*softMask
	tiles     map[tileKey]*layer
	pageInv   pdf.Matrix // device back to the page's user space
	path      path
	pendClip  int     // 0 none, 1 non-zero, 2 even-odd
	vec       *vecOut // when set, the page also goes to a printer's display list
}

var errBudget = errors.New("render: the page takes too much work")

// op counts work; it reports false when the page must stop.
func (r *ctx) op() bool {
	r.ops++
	if r.ops&0x3FF == 0 {
		if err := r.c.Err(); err != nil {
			r.err = err
			return false
		}
	}
	if r.ops > maxOps {
		r.err = errBudget
		return false
	}
	return r.err == nil
}

func newGState(ctm pdf.Matrix, clip image.Rectangle) *gstate {
	gs := &gstate{ctm: ctm, clip: clipState{r: clip}, ca: 1, CA: 1}
	gs.fill = paint{cs: deviceGray{}, comps: []float64{0}}
	gs.stroke = gs.fill
	gs.line = strokeStyle{width: 1, miter: 10}
	gs.text.scale = 1
	return gs
}

func (r *ctx) save() {
	if len(r.stack) >= maxQStack {
		return
	}
	c := *r.gs
	r.stack = append(r.stack, r.gs)
	r.gs = &c
}

func (r *ctx) restore() {
	if n := len(r.stack); n > 0 {
		r.gs = r.stack[n-1]
		r.stack = r.stack[:n-1]
	}
}

// interpret runs a content stream with res as its resources.
func (r *ctx) interpret(body []byte, res pdf.Dict) {
	saveRes := r.res
	r.res = res
	defer func() { r.res = saveRes }()
	l := pdf.NewLexer(body)
	var args []pdf.Object
	floor := len(r.stack)
	for r.op() {
		o, ok := l.Next()
		if !ok {
			break
		}
		kw, isOp := o.(pdf.Keyword)
		if !isOp {
			if len(args) < 512 {
				args = append(args, o)
			}
			continue
		}
		if kw == "BI" {
			r.inlineImage(l)
		} else if kw == "q" {
			r.save()
		} else if kw == "Q" {
			if len(r.stack) > floor { // a stream cannot pop what its caller saved
				r.restore()
			}
		} else {
			r.operator(string(kw), args)
		}
		args = args[:0]
	}
	for len(r.stack) > floor {
		r.restore()
	}
}

func nums(args []pdf.Object, n int) ([]float64, bool) {
	if len(args) < n {
		return nil, false
	}
	args = args[len(args)-n:]
	out := make([]float64, n)
	for i, a := range args {
		v, ok := pdf.Num(a)
		if !ok {
			return nil, false
		}
		out[i] = v
	}
	return out, true
}

func (r *ctx) operator(op string, args []pdf.Object) {
	gs := r.gs
	switch op {
	case "cm":
		if v, ok := nums(args, 6); ok {
			gs.ctm = pdf.Matrix{v[0], v[1], v[2], v[3], v[4], v[5]}.Mul(gs.ctm)
		}
	case "w":
		if v, ok := nums(args, 1); ok {
			gs.line.width = math.Abs(v[0])
		}
	case "J":
		if v, ok := nums(args, 1); ok {
			gs.line.cap = int(v[0])
		}
	case "j":
		if v, ok := nums(args, 1); ok {
			gs.line.join = int(v[0])
		}
	case "M":
		if v, ok := nums(args, 1); ok {
			gs.line.miter = v[0]
		}
	case "d":
		if len(args) >= 2 {
			r.setDash(args[len(args)-2], args[len(args)-1])
		}
	case "gs":
		if len(args) >= 1 {
			if n, ok := args[0].(pdf.Name); ok {
				r.extGState(r.doc.Dict(r.doc.Dict(r.res["ExtGState"])[n]))
			}
		}
	case "m":
		if v, ok := nums(args, 2); ok {
			r.path.moveTo(v[0], v[1])
		}
	case "l":
		if v, ok := nums(args, 2); ok {
			r.path.lineTo(v[0], v[1])
		}
	case "c":
		if v, ok := nums(args, 6); ok {
			r.path.curveTo(v[0], v[1], v[2], v[3], v[4], v[5])
		}
	case "v":
		if v, ok := nums(args, 4); ok {
			r.path.curveTo(r.path.cx.x, r.path.cx.y, v[0], v[1], v[2], v[3])
		}
	case "y":
		if v, ok := nums(args, 4); ok {
			r.path.curveTo(v[0], v[1], v[2], v[3], v[2], v[3])
		}
	case "h":
		r.path.close()
	case "re":
		if v, ok := nums(args, 4); ok {
			r.path.rect(v[0], v[1], v[2], v[3])
		}
	case "S":
		r.paintPath(false, false, true)
	case "s":
		r.path.close()
		r.paintPath(false, false, true)
	case "f", "F":
		r.paintPath(true, false, false)
	case "f*":
		r.paintPath(true, true, false)
	case "B":
		r.paintPath(true, false, true)
	case "B*":
		r.paintPath(true, true, true)
	case "b":
		r.path.close()
		r.paintPath(true, false, true)
	case "b*":
		r.path.close()
		r.paintPath(true, true, true)
	case "n":
		r.paintPath(false, false, false)
	case "W":
		r.pendClip = 1
	case "W*":
		r.pendClip = 2
	case "CS":
		if len(args) >= 1 {
			r.setSpace(&gs.stroke, args[0])
		}
	case "cs":
		if len(args) >= 1 {
			r.setSpace(&gs.fill, args[0])
		}
	case "SC", "SCN":
		r.setColor(&gs.stroke, args)
	case "sc", "scn":
		r.setColor(&gs.fill, args)
	case "G":
		r.setDevice(&gs.stroke, deviceGray{}, args, 1)
	case "g":
		r.setDevice(&gs.fill, deviceGray{}, args, 1)
	case "RG":
		r.setDevice(&gs.stroke, deviceRGB{}, args, 3)
	case "rg":
		r.setDevice(&gs.fill, deviceRGB{}, args, 3)
	case "K":
		r.setDevice(&gs.stroke, deviceCMYK{}, args, 4)
	case "k":
		r.setDevice(&gs.fill, deviceCMYK{}, args, 4)
	case "sh":
		if len(args) >= 1 {
			if n, ok := args[0].(pdf.Name); ok {
				r.paintShading(r.doc.Dict(r.res["Shading"])[n])
			}
		}
	case "Do":
		if len(args) >= 1 {
			if n, ok := args[0].(pdf.Name); ok {
				r.doXObject(r.doc.Dict(r.res["XObject"])[n])
			}
		}
	case "BT":
		r.tm, r.tlm = pdf.Identity, pdf.Identity
		r.textClip = nil
	case "ET":
		r.endText()
	case "Tc":
		if v, ok := nums(args, 1); ok {
			gs.text.charSpace = v[0]
		}
	case "Tw":
		if v, ok := nums(args, 1); ok {
			gs.text.wordSpace = v[0]
		}
	case "Tz":
		if v, ok := nums(args, 1); ok {
			gs.text.scale = v[0] / 100
		}
	case "TL":
		if v, ok := nums(args, 1); ok {
			gs.text.leading = v[0]
		}
	case "Ts":
		if v, ok := nums(args, 1); ok {
			gs.text.rise = v[0]
		}
	case "Tr":
		if v, ok := nums(args, 1); ok {
			gs.text.mode = int(v[0])
		}
	case "Tf":
		if len(args) >= 2 {
			if n, ok := args[len(args)-2].(pdf.Name); ok {
				gs.text.font = r.cache.font(r.doc.Dict(r.res["Font"])[n], r)
			}
			if v, ok := pdf.Num(args[len(args)-1]); ok {
				gs.text.size = v
			}
		}
	case "Td":
		if v, ok := nums(args, 2); ok {
			r.moveText(v[0], v[1])
		}
	case "TD":
		if v, ok := nums(args, 2); ok {
			gs.text.leading = -v[1]
			r.moveText(v[0], v[1])
		}
	case "Tm":
		if v, ok := nums(args, 6); ok {
			r.tm = pdf.Matrix{v[0], v[1], v[2], v[3], v[4], v[5]}
			r.tlm = r.tm
		}
	case "T*":
		r.moveText(0, -gs.text.leading)
	case "Tj":
		if len(args) >= 1 {
			if s, ok := args[len(args)-1].(pdf.String); ok {
				r.showText([]byte(s))
			}
		}
	case "'":
		r.moveText(0, -gs.text.leading)
		if len(args) >= 1 {
			if s, ok := args[len(args)-1].(pdf.String); ok {
				r.showText([]byte(s))
			}
		}
	case "\"":
		if len(args) >= 3 {
			if v, ok := pdf.Num(args[0]); ok {
				gs.text.wordSpace = v
			}
			if v, ok := pdf.Num(args[1]); ok {
				gs.text.charSpace = v
			}
			r.moveText(0, -gs.text.leading)
			if s, ok := args[2].(pdf.String); ok {
				r.showText([]byte(s))
			}
		}
	case "TJ":
		if len(args) >= 1 {
			if a, ok := args[len(args)-1].(pdf.Array); ok {
				for _, e := range a {
					switch v := e.(type) {
					case pdf.String:
						r.showText([]byte(v))
					case int64, float64:
						n, _ := pdf.Num(v)
						r.adjustText(n)
					}
				}
			}
		}
	case "d0":
		// Coloured Type 3 glyph.
	case "d1":
		r.uncolored = true
	case "BMC":
		r.mc = append(r.mc, false)
	case "BDC":
		hide := false
		if len(args) >= 2 && args[0] == pdf.Name("OC") {
			var oc pdf.Object = args[1]
			if n, ok := oc.(pdf.Name); ok {
				oc = r.doc.Dict(r.res["Properties"])[n]
			}
			hide = !r.cache.visible(oc)
		}
		r.mc = append(r.mc, hide)
		if hide {
			r.hidden++
		}
	case "EMC":
		if n := len(r.mc); n > 0 {
			if r.mc[n-1] {
				r.hidden--
			}
			r.mc = r.mc[:n-1]
		}
	}
}

func (r *ctx) setDash(arr, phase pdf.Object) {
	a, ok := arr.(pdf.Array)
	if !ok {
		return
	}
	var dash []float64
	total := 0.0
	for _, e := range a[:min(len(a), 64)] {
		v, _ := pdf.Num(e)
		dash = append(dash, math.Max(v, 0))
		total += math.Max(v, 0)
	}
	if total <= 0 {
		dash = nil
	}
	p, _ := pdf.Num(phase)
	r.gs.line.dash, r.gs.line.phase = dash, p
}

func (r *ctx) extGState(dict pdf.Dict) {
	d := r.doc
	gs := r.gs
	for k, v := range dict {
		switch k {
		case "LW":
			gs.line.width = math.Abs(d.Float(v, gs.line.width))
		case "LC":
			gs.line.cap = d.Int(v, gs.line.cap)
		case "LJ":
			gs.line.join = d.Int(v, gs.line.join)
		case "ML":
			gs.line.miter = d.Float(v, gs.line.miter)
		case "D":
			if a := d.Array(v); len(a) >= 2 {
				r.setDash(d.Resolve(a[0]), d.Resolve(a[1]))
			}
		case "Font":
			if a := d.Array(v); len(a) >= 2 {
				gs.text.font = r.cache.font(a[0], r)
				gs.text.size = d.Float(a[1], gs.text.size)
			}
		case "ca":
			gs.ca = clamp1(d.Float(v, 1))
		case "CA":
			gs.CA = clamp1(d.Float(v, 1))
		case "BM":
			gs.blend = parseBlend(d, v)
		case "SMask":
			if s := d.Dict(v); s != nil {
				if r.layer != nil {
					gs.soft = r.softMaskOf(v, s)
				}
			} else {
				gs.soft = nil
			}
		}
	}
}

func (r *ctx) setSpace(p *paint, o pdf.Object) {
	cs := r.parseColorSpace(o, r.res, 0)
	p.cs = cs
	p.comps = cs.initial()
	p.pattern = nil
	if len(p.comps) > 0 {
		p.rgb = toBytes(cs.rgb(p.comps))
	}
}

func toBytes(c [3]float64) [3]uint8 {
	return [3]uint8{uint8(c[0]*255 + 0.5), uint8(c[1]*255 + 0.5), uint8(c[2]*255 + 0.5)}
}

func (r *ctx) setColor(p *paint, args []pdf.Object) {
	if r.uncolored {
		return
	}
	var vals []float64
	var name pdf.Name
	for _, a := range args {
		if v, ok := pdf.Num(a); ok {
			vals = append(vals, v)
		} else if n, ok := a.(pdf.Name); ok {
			name = n
		}
	}
	if name != "" {
		p.pattern = r.doc.Dict(r.res["Pattern"])[name]
		p.comps = vals
		if ps, ok := p.cs.(patternSpace); ok && ps.under != nil && len(vals) >= ps.under.comps() {
			p.rgb = toBytes(ps.under.rgb(vals))
		}
		return
	}
	if p.cs == nil || len(vals) < p.cs.comps() || p.cs.comps() == 0 {
		// Too few values for the space, or colour set with no space: guess by count.
		switch len(vals) {
		case 1:
			p.cs = deviceGray{}
		case 3:
			p.cs = deviceRGB{}
		case 4:
			p.cs = deviceCMYK{}
		default:
			return
		}
	}
	p.comps, p.pattern = vals, nil
	p.rgb = toBytes(p.cs.rgb(vals))
}

func (r *ctx) setDevice(p *paint, cs colorSpace, args []pdf.Object, n int) {
	if r.uncolored {
		return
	}
	v, ok := nums(args, n)
	if !ok {
		return
	}
	p.cs, p.comps, p.pattern = cs, v, nil
	p.rgb = toBytes(cs.rgb(v))
}

func (r *ctx) drawing() bool { return !r.noDraw && r.hidden == 0 && r.layer != nil }

func (r *ctx) paintPath(fill, evenOdd, strokeIt bool) {
	defer func() {
		r.path.ops = r.path.ops[:0]
		r.path.cx, r.path.sx = point{}, point{}
		r.pendClip = 0
	}()
	if len(r.path.ops) > maxPathOps {
		return
	}
	gs := r.gs
	if r.drawing() && (fill || strokeIt) {
		bounds := gs.clip.r.Intersect(r.layer.r)
		if fill {
			m := r.ra.fill(thicken(r.path.flatten(gs.ctm)), evenOdd, bounds, 4)
			r.paintMask(m, &gs.fill, gs.ca)
		}
		if strokeIt {
			m := r.ra.fill(stroke(&r.path, &gs.line, gs.ctm), false, bounds, 4)
			r.paintMask(m, &gs.stroke, gs.CA)
		}
		if r.vecOn() {
			if fill {
				r.vecFill(evenOdd)
			}
			if strokeIt {
				r.vecStroke()
			}
		}
	}
	if r.pendClip != 0 {
		if r.vec != nil {
			r.vecClip(r.pagePath(&r.path), r.pendClip == 2)
		}
		r.clipTo(r.path.flatten(gs.ctm), r.pendClip == 2)
	}
}

// thicken widens a filled shape thinner than a pixel, as a rule drawn as a thin rectangle, so it does not vanish.
func thicken(p *poly) *poly {
	if len(p.pts) < 2 || len(p.pts) > 6 {
		return p
	}
	minX, minY, maxX, maxY := math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)
	for _, q := range p.pts {
		minX, maxX = math.Min(minX, q.x), math.Max(maxX, q.x)
		minY, maxY = math.Min(minY, q.y), math.Max(maxY, q.y)
	}
	w, h := maxX-minX, maxY-minY
	if w >= 1 && h >= 1 || w == 0 && h == 0 || w < 1 && h < 1 && w*h == 0 {
		return p
	}
	if w < 1 {
		c := (minX + maxX) / 2
		minX, maxX = c-0.5, c+0.5
	}
	if h < 1 {
		c := (minY + maxY) / 2
		minY, maxY = c-0.5, c+0.5
	}
	out := &poly{}
	out.moveTo(minX, minY)
	out.lineTo(maxX, minY)
	out.lineTo(maxX, maxY)
	out.lineTo(minX, maxY)
	return out
}

// clipTo narrows the clip to a polygon. A pixel-aligned axis rectangle stays a rectangle, which is the common case and
// keeps the edges between neighbouring clipped images free of seams.
func (r *ctx) clipTo(p *poly, evenOdd bool) {
	gs := r.gs
	if r.layer == nil {
		return
	}
	if rc, ok := axisRect(p); ok {
		gs.clip = narrow(gs.clip, rc)
		return
	}
	bounds := gs.clip.r.Intersect(r.layer.bounds())
	m := r.ra.fill(p, evenOdd, bounds, 4)
	if m == nil {
		gs.clip = clipState{}
		return
	}
	if old := gs.clip.m; old != nil {
		for y := m.r.Min.Y; y < m.r.Max.Y; y++ {
			row := m.pix[(y-m.r.Min.Y)*m.r.Dx():]
			for x := m.r.Min.X; x < m.r.Max.X; x++ {
				if v := row[x-m.r.Min.X]; v != 0 {
					row[x-m.r.Min.X] = uint8(int(v) * int(old.at(x, y)) / 255)
				}
			}
		}
	}
	gs.clip = clipState{r: m.r, m: m}
}

func narrow(c clipState, rc image.Rectangle) clipState {
	return clipState{r: c.r.Intersect(rc), m: c.m}
}

// axisRect reports a polygon that is one axis-aligned rectangle, rounded to pixels.
func axisRect(p *poly) (image.Rectangle, bool) {
	if len(p.starts) == 0 {
		return image.Rectangle{}, true
	}
	var sub []point
	for i := range p.starts {
		if s := p.sub(i); len(s) >= 3 {
			if sub != nil {
				return image.Rectangle{}, false
			}
			sub = s
		}
	}
	if sub == nil {
		return image.Rectangle{}, true
	}
	if n := len(sub); n == 5 && sub[0] == sub[4] {
		sub = sub[:4]
	}
	if len(sub) != 4 {
		return image.Rectangle{}, false
	}
	for i := range 4 {
		a, b := sub[i], sub[(i+1)%4]
		if math.Abs(a.x-b.x) > 1e-6 && math.Abs(a.y-b.y) > 1e-6 {
			return image.Rectangle{}, false
		}
	}
	x0, x1 := math.Min(sub[0].x, sub[2].x), math.Max(sub[0].x, sub[2].x)
	y0, y1 := math.Min(sub[0].y, sub[2].y), math.Max(sub[0].y, sub[2].y)
	if x1-x0 > 1<<24 || y1-y0 > 1<<24 || math.Abs(x0) > 1<<24 || math.Abs(y0) > 1<<24 {
		return image.Rect(-1<<24, -1<<24, 1<<24, 1<<24), true
	}
	return image.Rect(int(math.Round(x0)), int(math.Round(y0)), int(math.Round(x1)), int(math.Round(y1))), true
}

func (r *ctx) doXObject(o pdf.Object) {
	d := r.doc
	s, _ := d.Resolve(o).(*pdf.Stream)
	if s == nil {
		return
	}
	if oc := s.Dict["OC"]; oc != nil && !r.cache.visible(oc) {
		return
	}
	switch d.Name(s.Dict["Subtype"]) {
	case "Image":
		if r.drawing() {
			num := 0
			if ref, ok := o.(pdf.Ref); ok {
				num = ref.Num
			}
			r.drawImage(s, num, nil)
		}
	case "Form":
		r.drawForm(s)
	}
}

// drawForm runs a form XObject in the current graphics state.
func (r *ctx) drawForm(s *pdf.Stream) {
	if r.depth >= maxFormDepth || !r.op() {
		return
	}
	d := r.doc
	body, err := d.Decode(s)
	if err != nil && len(body) == 0 {
		return
	}
	res := d.Dict(s.Dict["Resources"])
	if res == nil {
		res = r.res
	}
	r.depth++
	r.save()
	savePath, saveBase := r.path, r.base
	r.path = path{}
	gs := r.gs
	gs.ctm = d.MatrixOf(s.Dict["Matrix"]).Mul(gs.ctm)
	r.base = gs.ctm
	if bbox, ok := d.RectOf(s.Dict["BBox"]); ok {
		p := path{}
		p.rect(bbox.X0, bbox.Y0, bbox.W(), bbox.H())
		if r.vec != nil {
			r.vecClip(r.pagePath(&p), false)
		}
		r.clipTo(p.flatten(gs.ctm), false)
	}
	group := d.Dict(s.Dict["Group"])
	transparent := group != nil && d.Name(group["S"]) == "Transparency"
	isolated := transparent && d.Bool(group["I"], false)
	if transparent && r.vecOn() && r.drawing() {
		// A group that only keeps its contents together needs nothing done; any other is drawn by the ordinary
		// renderer as one object.
		if gs.ca < 1 || gs.blend != blendNormal || gs.soft != nil {
			r.vec.need = true
			if !gs.clip.r.Empty() {
				box := gs.clip.r
				r.vec.off++
				r.drawGroup(body, res)
				r.vec.off--
				r.vecRegion(nil, false, box)
			}
		} else {
			r.interpret(body, res)
		}
	} else if transparent && r.drawing() && !gs.clip.r.Empty() && (isolated || gs.ca < 1 || gs.blend != blendNormal || gs.soft != nil) {
		r.drawGroup(body, res)
	} else {
		r.interpret(body, res)
	}
	r.restore()
	r.path, r.base = savePath, saveBase
	r.depth--
}

// drawGroup paints a group on a layer of its own and composites the layer as one object.
func (r *ctx) drawGroup(body []byte, res pdf.Dict) {
	gs := r.gs
	outer := r.layer
	box := gs.clip.r.Intersect(outer.bounds())
	if box.Empty() {
		return
	}
	g := newLayer(box)
	alpha, blend, soft, clip := gs.ca, gs.blend, gs.soft, gs.clip
	gs.ca, gs.CA, gs.blend, gs.soft = 1, 1, blendNormal, nil
	gs.clip = clipState{r: box}
	r.layer = g
	r.interpret(body, res)
	r.layer = outer
	gs.ca, gs.blend, gs.soft, gs.clip = alpha, blend, soft, clip
	outer.compositeLayer(g, &clip, soft, alpha, blend)
}

// softKey identifies a soft mask: the same group seen under the same matrix is the same mask.
type softKey struct {
	num int
	ctm pdf.Matrix
}

func (r *ctx) softMaskOf(o pdf.Object, dict pdf.Dict) *softMask {
	d := r.doc
	group, _ := d.Resolve(dict["G"]).(*pdf.Stream)
	if group == nil || r.depth >= maxFormDepth {
		return nil
	}
	key := softKey{ctm: r.gs.ctm}
	if ref, ok := dict["G"].(pdf.Ref); ok {
		key.num = ref.Num
		if m, ok := r.softCache[key]; ok {
			return m
		}
	}
	box := r.layer.bounds()
	lum := d.Name(dict["S"]) != "Alpha"
	g := newLayer(box)
	var bc [3]float64
	if lum {
		cs := colorSpace(deviceGray{})
		if gd := d.Dict(group.Dict["Group"]); gd["CS"] != nil {
			cs = r.parseColorSpace(gd["CS"], d.Dict(group.Dict["Resources"]), 0)
		}
		if v := d.Floats(dict["BC"]); len(v) >= cs.comps() && cs.comps() > 0 {
			bc = cs.rgb(v)
		} else if ps := cs.initial(); len(ps) > 0 {
			bc = cs.rgb(ps)
		}
	}
	saveLayer, saveGS, saveStack := r.layer, r.gs, r.stack
	saveHidden := r.hidden
	r.layer, r.hidden = g, 0
	r.stack = nil
	r.gs = newGState(saveGS.ctm, box)
	r.gs.text = saveGS.text
	if r.vec != nil {
		r.vec.off++
	}
	r.drawForm(group)
	if r.vec != nil {
		r.vec.off--
	}
	r.layer, r.gs, r.stack, r.hidden = saveLayer, saveGS, saveStack, saveHidden
	m := g.toSoftMask(lum, bc)
	if tr := d.Resolve(dict["TR"]); tr != nil && tr != pdf.Name("Identity") {
		if f := parseFunction(d, dict["TR"], 0); f != nil {
			var lut [256]uint8
			for i := range 256 {
				if v := f.eval([]float64{float64(i) / 255}); len(v) > 0 {
					lut[i] = uint8(clamp1(v[0])*255 + 0.5)
				}
			}
			for i, v := range m.pix {
				m.pix[i] = lut[v]
			}
			m.outside = lut[m.outside]
		}
	}
	if key.num != 0 {
		if r.softCache == nil {
			r.softCache = map[softKey]*softMask{}
		}
		r.softCache[key] = m
	}
	return m
}

// inlineImage reads BI ... ID data EI.
func (r *ctx) inlineImage(l *pdf.Lexer) {
	dict := pdf.Dict{}
	for r.op() {
		o, ok := l.Next()
		if !ok {
			return
		}
		if k, isKw := o.(pdf.Keyword); isKw {
			if k == "ID" {
				break
			}
			continue
		}
		key, isName := o.(pdf.Name)
		if !isName {
			continue
		}
		v, ok := l.Next()
		if !ok {
			return
		}
		dict[expandInlineKey(key)] = v
	}
	rest := l.Bytes()
	if len(rest) > 0 && (rest[0] == ' ' || rest[0] == '\n' || rest[0] == '\r' || rest[0] == '\t') {
		if rest[0] == '\r' && len(rest) > 1 && rest[1] == '\n' {
			l.Pos++
			rest = rest[1:]
		}
		l.Pos++
		rest = rest[1:]
	}
	n := r.inlineLength(dict, rest)
	if n < 0 {
		l.Pos += len(rest)
		return
	}
	data := rest[:n]
	l.Pos += n
	if at := bytes.Index(l.Bytes()[:min(len(l.Bytes()), 16)], []byte("EI")); at >= 0 {
		l.Pos += at + 2
	}
	if r.drawing() {
		r.drawImage(pdf.NewStream(dict, data), 0, r.res)
	}
}

func expandInlineKey(k pdf.Name) pdf.Name {
	switch k {
	case "BPC":
		return "BitsPerComponent"
	case "CS":
		return "ColorSpace"
	case "D":
		return "Decode"
	case "DP":
		return "DecodeParms"
	case "F":
		return "Filter"
	case "H":
		return "Height"
	case "IM":
		return "ImageMask"
	case "I":
		return "Interpolate"
	case "W":
		return "Width"
	}
	return k
}

// inlineLength finds where an inline image's data ends: by its size when it is unfiltered, else at an EI that stands
// alone and is followed by something that reads as content.
func (r *ctx) inlineLength(dict pdf.Dict, b []byte) int {
	if dict["Filter"] == nil {
		w, h := r.doc.Int(dict["Width"], 0), r.doc.Int(dict["Height"], 0)
		bpc := r.doc.Int(dict["BitsPerComponent"], 8)
		comps := 1
		if r.doc.Bool(dict["ImageMask"], false) {
			bpc = 1
		} else if cs := dict["ColorSpace"]; cs != nil {
			comps = max(r.parseColorSpace(cs, r.res, 0).comps(), 1)
		}
		if n := (w*bpc*comps + 7) / 8 * h; n > 0 && n <= len(b) {
			return n
		}
	}
	for i := 0; i+1 < len(b); i++ {
		at := bytes.Index(b[i:], []byte("EI"))
		if at < 0 {
			return len(b)
		}
		i += at
		before := i == 0 || b[i-1] == ' ' || b[i-1] == '\n' || b[i-1] == '\r' || b[i-1] == '\t'
		after := i+2 >= len(b) || b[i+2] == ' ' || b[i+2] == '\n' || b[i+2] == '\r' || b[i+2] == '\t' || b[i+2] == 'Q'
		if !before || !after {
			continue
		}
		// Binary data rarely has only printable bytes in what follows.
		tail := b[min(i+2, len(b)):min(i+12, len(b))]
		text := true
		for _, c := range tail {
			if c >= 0x80 || c < 0x09 {
				text = false
				break
			}
		}
		if text {
			end := i
			if end > 0 && (b[end-1] == '\n' || b[end-1] == ' ' || b[end-1] == '\r') {
				end--
			}
			return end
		}
	}
	return len(b)
}

// RenderError says a page drew only in part.
type RenderError struct{ Err error }

func (e *RenderError) Error() string { return fmt.Sprintf("render: %v", e.Err) }
func (e *RenderError) Unwrap() error { return e.Err }
