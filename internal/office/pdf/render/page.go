package render

import (
	"context"
	"fmt"
	"image"
	"math"
	"sync"

	"github.com/btelolabs/btelo-forge/internal/office/pdf"
	"github.com/btelolabs/btelo-forge/internal/printing"
)

// Options say how to draw a page.
type Options struct {
	Scale float64 // pixels a point
	View  int     // degrees turned on top of the page's own rotation
	// Skip leaves annotations out of the picture: a filter of the ones to leave, by object number.
	Skip func(num int) bool
	// NoAnnots draws the content alone.
	NoAnnots bool
	// Region limits the picture to this part of the page, in pixels from the top left of the whole page at Scale. The
	// result's Rect says where it is. Empty means the whole page.
	Region image.Rectangle
}

// Result is what came of drawing a page.
type Result struct {
	Img  *image.RGBA
	Text *Text
	Err  error // the page drew only in part
}

// Cache is what pages of one document share: fonts, colour spaces, decoded images. It is safe for concurrent use.
type Cache struct {
	doc *pdf.Doc
	src FontSource

	mu       sync.Mutex
	fonts    map[uintptr]*pdfFont
	prints   map[printImage]*image.RGBA
	glyphs   map[glyphKey]*printing.Outline
	spaces   map[int]colorSpace
	pics     map[picKey]*picEntry
	picBytes int
	clock    int
	fallback *pdfFont
	ocOff    map[int]bool
	ocBase   bool
	ocDone   bool
}

type picKey struct{ num, factor int }

type picEntry struct {
	p     *picture
	size  int
	stamp int
}

// NewCache makes a cache for a document; src finds substitutes for fonts it does not embed, and may be nil.
func NewCache(d *pdf.Doc, src FontSource) *Cache {
	return &Cache{doc: d, src: src, fonts: map[uintptr]*pdfFont{}, spaces: map[int]colorSpace{}, pics: map[picKey]*picEntry{}}
}

func (c *Cache) space(num int) (colorSpace, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	s, ok := c.spaces[num]
	return s, ok
}

func (c *Cache) setSpace(num int, s colorSpace) {
	c.mu.Lock()
	c.spaces[num] = s
	c.mu.Unlock()
}

func (c *Cache) fallbackFont() *pdfFont {
	c.mu.Lock()
	f := c.fallback
	c.mu.Unlock()
	if f != nil {
		return f
	}
	f = c.parseFont(pdf.Dict{"Subtype": pdf.Name("Type1"), "BaseFont": pdf.Name("Helvetica")})
	c.mu.Lock()
	c.fallback = f
	c.mu.Unlock()
	return f
}

func (c *Cache) picture(r *ctx, s *pdf.Stream, num, factor int, res pdf.Dict) *picture {
	if num == 0 {
		return r.loadPicture(s, factor, res, 0)
	}
	key := picKey{num, factor}
	c.mu.Lock()
	// A sharper version already decoded serves as well.
	for f := factor; f >= 1 && f >= factor/2; f-- {
		if e, ok := c.pics[picKey{num, f}]; ok {
			c.clock++
			e.stamp = c.clock
			c.mu.Unlock()
			return e.p
		}
	}
	c.mu.Unlock()
	p := r.loadPicture(s, factor, res, 0)
	if p == nil {
		return nil
	}
	size := len(p.pix)
	if p.alpha != nil {
		size += len(p.alpha.pix)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.clock++
	if old, ok := c.pics[key]; ok {
		c.picBytes -= old.size
	}
	c.pics[key] = &picEntry{p, size, c.clock}
	c.picBytes += size
	for c.picBytes > imageCacheBytes && len(c.pics) > 1 {
		var oldest picKey
		stamp := math.MaxInt
		for k, e := range c.pics {
			if e.stamp < stamp && k != key {
				oldest, stamp = k, e.stamp
			}
		}
		c.picBytes -= c.pics[oldest].size
		delete(c.pics, oldest)
	}
	return p
}

// visible says whether an optional content group or membership dictionary is on in the default configuration.
func (c *Cache) visible(o pdf.Object) bool {
	d := c.doc
	c.mu.Lock()
	if !c.ocDone {
		c.ocDone = true
		c.ocOff = map[int]bool{}
		c.ocBase = true
		props := d.Dict(d.Catalog()["OCProperties"])
		def := d.Dict(props["D"])
		if d.Name(def["BaseState"]) == "OFF" {
			c.ocBase = false
		}
		for _, e := range d.Array(def["OFF"]) {
			if r, ok := e.(pdf.Ref); ok {
				c.ocOff[r.Num] = true
			}
		}
		if !c.ocBase {
			for _, e := range d.Array(props["OCGs"]) {
				if r, ok := e.(pdf.Ref); ok {
					c.ocOff[r.Num] = true
				}
			}
			for _, e := range d.Array(def["ON"]) {
				if r, ok := e.(pdf.Ref); ok {
					delete(c.ocOff, r.Num)
				}
			}
		}
	}
	c.mu.Unlock()
	on := func(e pdf.Object) bool {
		r, ok := e.(pdf.Ref)
		return !ok || !c.ocOff[r.Num]
	}
	dict := d.Dict(o)
	if dict == nil {
		return true
	}
	if d.Name(dict["Type"]) != "OCMD" {
		return on(o)
	}
	list := d.Array(dict["OCGs"])
	if list == nil && dict["OCGs"] != nil {
		list = pdf.Array{dict["OCGs"]}
	}
	if len(list) == 0 {
		return true
	}
	anyOn, allOn := false, true
	for _, e := range list {
		v := on(e)
		anyOn = anyOn || v
		allOn = allOn && v
	}
	switch d.Name(dict["P"]) {
	case "AllOn":
		return allOn
	case "AnyOff":
		return !allOn
	case "AllOff":
		return !anyOn
	}
	return anyOn
}

// Page draws a page. A failure leaves what was drawn so far in the result, with Err set.
func (c *Cache) Page(ctxt context.Context, p pdf.Page, o Options) (res *Result) {
	d := c.doc
	scale := o.Scale
	if scale <= 0 {
		scale = 1
	}
	w, h := d.ViewSize(p, o.View)
	for math.Ceil(w*scale) > maxBBoxPage || math.Ceil(h*scale) > maxBBoxPage || w*h*scale*scale > maxPixels {
		scale *= 0.8
	}
	pw, ph := max(int(math.Ceil(w*scale-1e-6)), 1), max(int(math.Ceil(h*scale-1e-6)), 1)
	view := d.ViewMatrix(p, scale, o.View)
	box := image.Rect(0, 0, pw, ph)
	if !o.Region.Empty() {
		if part := o.Region.Intersect(box); !part.Empty() {
			box = part
		}
	}
	l := newLayer(box)
	for i := range l.pix {
		l.pix[i] = 255
	}
	r := &ctx{c: ctxt, doc: d, cache: c, opts: o, layer: l, base: view, out: &Text{}}
	r.pageInv, _ = view.Invert()
	r.gs = newGState(view, box)
	res = &Result{Text: r.out}
	defer func() {
		if e := recover(); e != nil {
			res.Err = &RenderError{Err: fmt.Errorf("%v", e)}
		}
		res.Text.finish()
		res.Img = &image.RGBA{Pix: l.pix, Stride: 4 * box.Dx(), Rect: box}
	}()
	r.runPage(p)
	if !o.NoAnnots {
		r.out = nil
		r.drawAnnots(p)
	}
	if r.err != nil {
		res.Err = &RenderError{Err: r.err}
	}
	return res
}

// Text reads a page's text without drawing it.
func (c *Cache) Text(ctxt context.Context, p pdf.Page) (t *Text, err error) {
	d := c.doc
	view := d.ViewMatrix(p, 1, 0)
	r := &ctx{c: ctxt, doc: d, cache: c, noDraw: true, base: view, out: &Text{}}
	r.pageInv, _ = view.Invert()
	r.gs = newGState(view, image.Rect(0, 0, 1, 1))
	t = r.out
	defer func() {
		if e := recover(); e != nil {
			err = &RenderError{Err: fmt.Errorf("%v", e)}
		}
		t.finish()
	}()
	r.runPage(p)
	if r.err != nil {
		err = &RenderError{Err: r.err}
	}
	return t, err
}

func (r *ctx) runPage(p pdf.Page) {
	d := r.doc
	res := d.Dict(p.Dict["Resources"])
	if res == nil {
		res = pdf.Dict{}
	}
	var body []byte
	switch v := d.Resolve(p.Dict["Contents"]).(type) {
	case *pdf.Stream:
		body, _ = d.Decode(v)
	case pdf.Array:
		// The streams of an array are one content stream, split anywhere between tokens.
		for _, e := range v[:min(len(v), 1<<16)] {
			if s, ok := d.Resolve(e).(*pdf.Stream); ok {
				b, _ := d.Decode(s)
				body = append(body, b...)
				body = append(body, '\n')
			}
		}
	}
	r.interpret(body, res)
}

func (r *ctx) drawAnnots(p pdf.Page) {
	d := r.doc
	annots := d.Array(p.Dict["Annots"])
	for _, a := range annots[:min(len(annots), 1<<16)] {
		if !r.op() {
			return
		}
		if ref, ok := a.(pdf.Ref); ok && r.opts.Skip != nil && r.opts.Skip(ref.Num) {
			continue
		}
		dict := d.Dict(a)
		if dict == nil || d.Name(dict["Subtype"]) == "Popup" {
			continue
		}
		flags := d.Int(dict["F"], 0)
		if r.vec != nil {
			if flags&2 != 0 || flags&4 == 0 { // hidden, or not meant for paper
				continue
			}
		} else if flags&(2|32) != 0 { // hidden, no view
			continue
		}
		if oc := dict["OC"]; oc != nil && !r.cache.visible(oc) {
			continue
		}
		rect, ok := d.RectOf(dict["Rect"])
		if !ok {
			continue
		}
		s := pdf.AppearanceOf(d, dict)
		if s == nil {
			continue
		}
		bbox, ok := d.RectOf(s.Dict["BBox"])
		if !ok {
			continue
		}
		m := d.MatrixOf(s.Dict["Matrix"])
		tb := m.BoxOf(bbox)
		if tb.W() == 0 || tb.H() == 0 {
			continue
		}
		sx, sy := rect.W()/tb.W(), rect.H()/tb.H()
		a := pdf.Matrix{sx, 0, 0, sy, rect.X0 - tb.X0*sx, rect.Y0 - tb.Y0*sy}
		r.save()
		r.gs.ctm = a.Mul(r.gs.ctm)
		r.drawForm(s)
		r.restore()
	}
}
