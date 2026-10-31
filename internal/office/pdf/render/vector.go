package render

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"math"
	"sync"

	"github.com/btelolabs/btelo-forge/internal/office/pdf"
	"github.com/btelolabs/btelo-forge/internal/office/pdf/font"
	"github.com/btelolabs/btelo-forge/internal/printing"
)

// Printing a PDF: the interpreter drives the display list instead of leaving pixels, so paths, words, clips, pictures
// and gradients stay what they are. What a printer has no way to do (blend modes, soft masks, group opacity, tiling
// patterns and mesh gradients) is drawn by the ordinary renderer: the page is first run with a tiny picture just to
// learn whether it holds any of that, and only then again at printer resolution, when each such object is copied
// from the picture, within its own outline, over whatever vector work lies under it.

// PrintOptions say how to print a page.
type PrintOptions struct {
	Annots bool               // print the annotations that ask to be
	Skip   func(num int) bool // annotations left out, by object number
	DPI    float64            // the resolution of what has to be pixels
}

// vclip is one step of the clip as the page has it; a graphics state points at its top step, so saving and restoring
// the state saves and restores the clip.
type vclip struct {
	parent  *vclip
	depth   int
	path    *printing.Path
	evenOdd bool
}

// vecOut is where an interpreter sends a page.
type vecOut struct {
	page     *printing.Page
	toPage   pdf.Matrix // from device pixels to points
	k        float64    // pixels a point
	off      int        // above 0 while drawing into something that is not the page: a tile, a group, a soft mask
	need     bool       // the page holds something that must be pixels
	hires    bool       // the picture is at printer resolution, so such things are copied from it
	clip     *vclip     // what the page's clips add up to now
	run      *printing.Text
	textClip *printing.Path
	pending  image.Rectangle // device pixels of text waiting to be copied from the picture
}

func (r *ctx) vecOn() bool { return r.vec != nil && r.vec.off == 0 && r.hidden == 0 && !r.noDraw }

// sync brings the page's clips to want, going back to where the two have a step in common.
func (v *vecOut) sync(want *vclip) {
	depth := func(c *vclip) int {
		if c == nil {
			return 0
		}
		return c.depth
	}
	var push []*vclip
	a, b := v.clip, want
	for depth(a) > depth(b) {
		v.page.Add(&printing.Unclip{})
		a = a.parent
	}
	for depth(b) > depth(a) {
		push = append(push, b)
		b = b.parent
	}
	for a != b {
		v.page.Add(&printing.Unclip{})
		a = a.parent
		push = append(push, b)
		b = b.parent
	}
	for i := len(push) - 1; i >= 0; i-- {
		v.page.Add(&printing.Clip{Path: push[i].path, EvenOdd: push[i].evenOdd})
	}
	v.clip = want
}

func printPath(p *path, m pdf.Matrix) *printing.Path {
	out := &printing.Path{}
	for i := range p.ops {
		op := &p.ops[i]
		switch op.op {
		case 'M':
			x, y := m.Apply(op.pts[0].x, op.pts[0].y)
			out.MoveTo(x, y)
		case 'L':
			x, y := m.Apply(op.pts[0].x, op.pts[0].y)
			out.LineTo(x, y)
		case 'C':
			x1, y1 := m.Apply(op.pts[0].x, op.pts[0].y)
			x2, y2 := m.Apply(op.pts[1].x, op.pts[1].y)
			x3, y3 := m.Apply(op.pts[2].x, op.pts[2].y)
			out.CurveTo(x1, y1, x2, y2, x3, y3)
		case 'Z':
			out.Close()
		}
	}
	return out
}

// vecClip narrows the page's clip as the picture's has been.
func (r *ctx) vecClip(p *printing.Path, evenOdd bool) {
	if r.vec == nil || r.vec.off != 0 {
		return
	}
	d := 1
	if r.gs.vclip != nil {
		d = r.gs.vclip.depth + 1
	}
	r.gs.vclip = &vclip{parent: r.gs.vclip, depth: d, path: p, evenOdd: evenOdd}
}

func (r *ctx) pagePath(p *path) *printing.Path { return printPath(p, r.gs.ctm.Mul(r.vec.toPage)) }

func colorOf(rgb [3]uint8, alpha float64) color.NRGBA {
	return color.NRGBA{rgb[0], rgb[1], rgb[2], uint8(clamp1(alpha)*255 + 0.5)}
}

func noPaint(p *paint) bool {
	none, ok := p.cs.(*tint)
	return ok && none.none
}

func (r *ctx) plain() bool { return r.gs.blend == blendNormal && r.gs.soft == nil }

// vecRegion copies what the ordinary renderer has made of an object, within the path, onto the page. Empty path
// means all of the box.
func (r *ctx) vecRegion(p *printing.Path, evenOdd bool, box image.Rectangle) {
	v := r.vec
	v.need = true
	if !v.hires {
		return
	}
	box = box.Intersect(r.gs.clip.r).Intersect(r.layer.r)
	if box.Empty() {
		return
	}
	img := image.NewRGBA(image.Rect(0, 0, box.Dx(), box.Dy()))
	w := box.Dx()
	for y := range box.Dy() {
		from := r.layer.offset(box.Min.X, box.Min.Y+y)
		copy(img.Pix[y*img.Stride:y*img.Stride+w*4], r.layer.pix[from:from+w*4])
	}
	v.sync(r.gs.vclip)
	if !p.Empty() {
		v.page.Add(&printing.Clip{Path: p, EvenOdd: evenOdd})
	}
	k := v.k
	v.page.Add(&printing.Picture{Img: img, M: printing.Matrix{float64(box.Dx()) / k, 0, 0, float64(box.Dy()) / k, float64(box.Min.X) / k, float64(box.Min.Y) / k}})
	if !p.Empty() {
		v.page.Add(&printing.Unclip{})
	}
}

// pathBox is the device pixels around a path on the page, with a margin.
func (v *vecOut) pathBox(p *printing.Path, grow float64) image.Rectangle {
	b := p.Bounds()
	if b.X0 > b.X1 {
		return image.Rectangle{}
	}
	lim := func(f float64) int { return int(min(max(f, -1<<28), 1<<28)) }
	return image.Rect(lim(math.Floor((b.X0-grow)*v.k)), lim(math.Floor((b.Y0-grow)*v.k)), lim(math.Ceil((b.X1+grow)*v.k)), lim(math.Ceil((b.Y1+grow)*v.k)))
}

// vecFill puts the current path's fill on the page.
func (r *ctx) vecFill(evenOdd bool) {
	gs, v := r.gs, r.vec
	if noPaint(&gs.fill) {
		return
	}
	p := r.pagePath(&r.path)
	if r.plain() {
		if gs.fill.pattern == nil {
			v.sync(gs.vclip)
			v.page.Add(&printing.Fill{Path: p, EvenOdd: evenOdd, Color: colorOf(gs.fill.rgb, gs.ca)})
			return
		}
		if sh := r.vecPatternShade(&gs.fill, p, evenOdd); sh != nil {
			v.sync(gs.vclip)
			v.page.Add(sh)
			return
		}
	}
	r.vecRegion(p, evenOdd, v.pathBox(p, 1))
}

// vecLine is the page's stroke of a path in user space, and false when the matrix stretches it unevenly: a scalar
// width cannot say that, and the stroke then goes as its outline.
func (r *ctx) vecLine(p *path, col color.NRGBA) (*printing.Stroke, *printing.Path) {
	gs := r.gs
	m := gs.ctm.Mul(r.vec.toPage)
	sx, sy := math.Hypot(m[0], m[1]), math.Hypot(m[2], m[3])
	in := printing.Stroke{Width: gs.line.width, Cap: printing.Cap(gs.line.cap), Join: printing.Join(gs.line.join), Miter: gs.line.miter, Phase: gs.line.phase, Color: col}
	if len(gs.line.dash) > 0 {
		in.Dash = gs.line.dash
	}
	if sx > 0 && math.Abs(sx-sy) <= 0.01*sx {
		in.Path = printPath(p, m)
		in.Width *= sx
		in.Phase *= sx
		if in.Dash != nil {
			in.Dash = make([]float64, len(gs.line.dash))
			for i, d := range gs.line.dash {
				in.Dash[i] = d * sx
			}
		}
		return &in, nil
	}
	in.Path = printPath(p, pdf.Identity)
	in.Width = max(in.Width, printing.MinLine/max(sx, sy, 1e-3))
	return nil, printing.StrokeOutline(&in, max(sx, sy, 1e-3)*4).Transformed(printing.Matrix(m))
}

func (r *ctx) vecStroke() {
	gs, v := r.gs, r.vec
	if noPaint(&gs.stroke) {
		return
	}
	s, outline := r.vecLine(&r.path, colorOf(gs.stroke.rgb, gs.CA))
	if r.plain() && gs.stroke.pattern == nil {
		v.sync(gs.vclip)
		if s != nil {
			v.page.Add(s)
		} else {
			v.page.Add(&printing.Fill{Path: outline, Color: colorOf(gs.stroke.rgb, gs.CA)})
		}
		return
	}
	if s != nil {
		outline = printing.StrokeOutline(s, 4)
		r.vecRegion(outline, false, v.pathBox(s.Path, s.Width))
		return
	}
	r.vecRegion(outline, false, v.pathBox(outline, 1))
}

// vecPatternShade is a shading pattern as a gradient of the page, nil when it is not one the page has.
func (r *ctx) vecPatternShade(p *paint, shape *printing.Path, evenOdd bool) *printing.Shade {
	d := r.doc
	dict := d.Dict(p.pattern)
	if dict == nil || d.Int(dict["PatternType"], 0) != 2 {
		return nil
	}
	return r.vecShade(dict["Shading"], d.MatrixOf(dict["Matrix"]).Mul(r.base), shape, evenOdd, true)
}

func (r *ctx) vecShade(o pdf.Object, m pdf.Matrix, shape *printing.Path, evenOdd, background bool) *printing.Shade {
	src, _ := r.shadingSource(o, m, image.Rect(0, 0, 1, 1), background).(*lutSource)
	if src == nil || src.function != nil || src.lut == nil || src.bg != nil || src.bbox != nil {
		return nil
	}
	const samples = 64
	stops := make([]printing.Stop, 0, samples+1)
	for i := 0; i <= samples; i++ {
		c := src.lut[i*(len(src.lut)-1)/samples]
		s := printing.Stop{At: float64(i) / samples, Color: color.NRGBA{c[0], c[1], c[2], 255}}
		if n := len(stops); n >= 2 && stops[n-1].Color == s.Color && stops[n-2].Color == s.Color {
			stops[n-1] = s
			continue
		}
		stops = append(stops, s)
	}
	s := &printing.Shade{Path: shape, EvenOdd: evenOdd, Stops: stops, Extend: [2]bool{src.ext0, src.ext1}, M: printing.Matrix(m.Mul(r.vec.toPage)), Alpha: 1}
	if r.gs.ca < 1 {
		s.Alpha = max(r.gs.ca, 1e-3)
	}
	s.X0, s.Y0, s.R0 = src.x0, src.y0, src.r0
	s.X1, s.Y1, s.R1 = src.x0+src.dx, src.y0+src.dy, src.r1
	s.Radial = !src.axial
	return s
}

// vecShading is what the sh operator does: a gradient over all that the clip allows.
func (r *ctx) vecShading(o pdf.Object) {
	gs, v := r.gs, r.vec
	whole := printing.RectPath(0, 0, v.page.W, v.page.H)
	if r.plain() {
		if s := r.vecShade(o, gs.ctm, whole, false, false); s != nil {
			v.sync(gs.vclip)
			v.page.Add(s)
			return
		}
	}
	r.vecRegion(nil, false, gs.clip.r)
}

// printImages keeps pictures as the page needs them.
type printImage struct {
	p   *picture
	rgb [3]uint8
}

// imageOf is a picture as premultiplied pixels.
func (c *Cache) imageOf(p *picture, rgb [3]uint8) *image.RGBA {
	key := printImage{p, rgb}
	c.mu.Lock()
	img := c.prints[key]
	c.mu.Unlock()
	if img != nil {
		return img
	}
	img = image.NewRGBA(image.Rect(0, 0, p.w, p.h))
	for y := range p.h {
		for x := range p.w {
			q := p.pix[(y*p.w+x)*4 : (y*p.w+x)*4+4 : (y*p.w+x)*4+4]
			a := int(q[3])
			cr, cg, cb := q[0], q[1], q[2]
			if p.stencil {
				cr, cg, cb = rgb[0], rgb[1], rgb[2]
			} else if p.alpha != nil {
				_, _, _, ma := p.alpha.sample((float64(x)+0.5)/float64(p.w), (float64(y)+0.5)/float64(p.h), true)
				a = a * int(ma) / 255
			}
			o := img.Pix[y*img.Stride+x*4 : y*img.Stride+x*4+4 : y*img.Stride+x*4+4]
			o[0], o[1], o[2], o[3] = uint8(int(cr)*a/255), uint8(int(cg)*a/255), uint8(int(cb)*a/255), uint8(a)
		}
	}
	c.mu.Lock()
	if c.prints == nil {
		c.prints = map[printImage]*image.RGBA{}
	}
	c.prints[key] = img
	c.mu.Unlock()
	return img
}

// vecImage puts an image on the page, at all the pixels it has.
func (r *ctx) vecImage(s *pdf.Stream, num int, res pdf.Dict) {
	gs, v := r.gs, r.vec
	if res == nil {
		res = r.res
	}
	m := gs.ctm.Mul(v.toPage)
	box := printing.RectPath(0, 0, 1, 1).Transformed(printing.Matrix(m))
	if !r.plain() {
		r.vecRegion(box, false, v.pathBox(box, 1))
		return
	}
	pic := r.cache.picture(r, s, num, 1, res)
	if pic == nil || pic.w == 0 || pic.h == 0 {
		return
	}
	if pic.stencil && gs.fill.pattern != nil {
		r.vecRegion(box, false, v.pathBox(box, 1))
		return
	}
	v.sync(gs.vclip)
	out := &printing.Picture{Img: r.cache.imageOf(pic, gs.fill.rgb), M: printing.Matrix(pdf.Matrix{1, 0, 0, -1, 0, 1}.Mul(m))}
	if gs.ca < 1 {
		out.Alpha = max(gs.ca, 1e-3)
	}
	v.page.Add(out)
}

type glyphKey struct {
	f      *pdfFont
	prog   font.Program
	gid    int
	xscale float64
}

// outlineOf is a glyph's outline, with the font's matrix applied, as the page holds it.
func (c *Cache) outlineOf(f *pdfFont, g *glyph, o font.Path, gm pdf.Matrix) *printing.Outline {
	key := glyphKey{f, g.prog, g.gid, g.xscale}
	c.mu.Lock()
	out := c.glyphs[key]
	c.mu.Unlock()
	if out != nil {
		return out
	}
	p := path{}
	toPath(o, gm, &p)
	out = &printing.Outline{Path: printPath(&p, pdf.Identity), Advance: g.width}
	c.mu.Lock()
	if c.glyphs == nil {
		c.glyphs = map[glyphKey]*printing.Outline{}
	}
	if len(c.glyphs) < 1<<18 {
		c.glyphs[key] = out
	}
	c.mu.Unlock()
	return out
}

// vecGlyph puts a glyph on the page; trm places an em of it.
func (r *ctx) vecGlyph(f *pdfFont, g *glyph, o font.Path, gm, trm pdf.Matrix, fill, strokeIt bool) {
	gs, v := r.gs, r.vec
	m := trm.Mul(gs.ctm).Mul(v.toPage)
	if strokeIt && !noPaint(&gs.stroke) {
		up := path{}
		toPath(o, gm.Mul(trm), &up)
		s, outline := r.vecLine(&up, colorOf(gs.stroke.rgb, gs.CA))
		if r.plain() && gs.stroke.pattern == nil {
			v.flushRun()
			v.sync(gs.vclip)
			if s != nil {
				v.page.Add(s)
			} else {
				v.page.Add(&printing.Fill{Path: outline, Color: colorOf(gs.stroke.rgb, gs.CA)})
			}
		} else {
			v.need = true
		}
	}
	if !fill || noPaint(&gs.fill) {
		return
	}
	outl := r.cache.outlineOf(f, g, o, gm)
	if !r.plain() || gs.fill.pattern != nil {
		// Only the pixels of the text, once it is all shown.
		v.need = true
		box := v.pathBox(outl.Path.Transformed(printing.Matrix(m)), 1)
		if v.pending.Empty() {
			v.pending = box
		} else {
			v.pending = v.pending.Union(box)
		}
		return
	}
	col := colorOf(gs.fill.rgb, gs.ca)
	pm := printing.Matrix(m)
	if run := v.run; run != nil && run.Color == col && sameLinear(run.M, pm) {
		if inv, ok := run.M.Inverse(); ok {
			x, y := inv.Apply(pm[4], pm[5])
			run.Glyphs = append(run.Glyphs, printing.Glyph{Outline: outl, X: x, Y: y, Text: g.text})
			return
		}
	}
	v.flushRun()
	v.sync(gs.vclip)
	v.run = &printing.Text{M: pm, Color: col, Glyphs: []printing.Glyph{{Outline: outl, Text: g.text}}}
}

func sameLinear(a, b printing.Matrix) bool {
	for i := range 4 {
		if math.Abs(a[i]-b[i]) > 1e-9 {
			return false
		}
	}
	return true
}

func (v *vecOut) flushRun() {
	if v.run != nil {
		v.page.Add(v.run)
		v.run = nil
	}
}

// vecTextDone finishes what a run of shown text left waiting.
func (r *ctx) vecTextDone() {
	v := r.vec
	v.flushRun()
	if !v.pending.Empty() {
		box := v.pending
		v.pending = image.Rectangle{}
		r.vecRegion(nil, false, box)
	}
}

// vecAddTextClip keeps a glyph's outline for the clip the text will end in.
func (r *ctx) vecAddTextClip(o font.Path, m pdf.Matrix) {
	v := r.vec
	if v.off != 0 {
		return
	}
	if v.textClip == nil {
		v.textClip = &printing.Path{}
	}
	p := path{}
	toPath(o, m.Mul(r.gs.ctm).Mul(v.toPage), &p)
	v.textClip.Append(printPath(&p, pdf.Identity))
}

// PrintPage turns a page into the display list. A failure leaves what was reached so far on the page, and says so.
func (c *Cache) PrintPage(ctxt context.Context, p pdf.Page, o PrintOptions) (*printing.Page, error) {
	d := c.doc
	w, h := d.ViewSize(p, 0)
	dpi := o.DPI
	if dpi <= 0 {
		dpi = 300
	}
	out, need, err := c.vecRun(ctxt, p, o, w, h, 1.0/8, false)
	if !need || err != nil {
		return out, err
	}
	k := dpi / 72
	for w*h*k*k > maxPixels || w*k > maxBBoxPage || h*k > maxBBoxPage {
		k *= 0.8
	}
	out, _, err = c.vecRun(ctxt, p, o, w, h, k, true)
	return out, err
}

func (c *Cache) vecRun(ctxt context.Context, p pdf.Page, o PrintOptions, w, h, k float64, hires bool) (pg *printing.Page, need bool, err error) {
	d := c.doc
	pw, ph := max(int(math.Ceil(w*k-1e-6)), 1), max(int(math.Ceil(h*k-1e-6)), 1)
	view := d.ViewMatrix(p, k, 0)
	box := image.Rect(0, 0, pw, ph)
	l := newLayer(box)
	for i := range l.pix {
		l.pix[i] = 255
	}
	pg = &printing.Page{W: w, H: h}
	v := &vecOut{page: pg, toPage: pdf.Matrix{1 / k, 0, 0, 1 / k, 0, 0}, k: k, hires: hires}
	r := &ctx{c: ctxt, doc: d, cache: c, opts: Options{Scale: k, Skip: o.Skip}, layer: l, base: view, vec: v}
	r.pageInv, _ = view.Invert()
	r.gs = newGState(view, box)
	defer func() {
		if e := recover(); e != nil {
			err = &RenderError{Err: fmt.Errorf("%v", e)}
		}
		v.flushRun()
		v.sync(nil)
		need = v.need
	}()
	r.runPage(p)
	if o.Annots {
		r.drawAnnots(p)
	}
	if r.err != nil {
		err = &RenderError{Err: r.err}
	}
	return pg, v.need, err
}

// printsWell keeps sync.Mutex used by the caches above in one place for vet.
var _ sync.Locker = (*sync.Mutex)(nil)
