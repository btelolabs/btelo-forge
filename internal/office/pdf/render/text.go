package render

import (
	"math"
	"strings"
	"unicode"

	"github.com/btelolabs/btelo-forge/internal/office/pdf"
	"github.com/btelolabs/btelo-forge/internal/office/pdf/font"
)

// Char is one character on a page, placed in the page's default user space.
type Char struct {
	Text         string
	Quad         [4][2]float64 // lower left, lower right, upper right, upper left, as the text runs
	Box          pdf.Rect      // around Quad
	Size         float64       // the height of an em on the page
	Base         [2]float64    // where its baseline starts
	Font         string
	Bold, Italic bool
	RGB          [3]uint8
	Line         int  // which of Text.Lines holds it
	Synthetic    bool // a space or line end worked out from the layout, not in the content
}

// Line is a run of characters along one baseline.
type Line struct {
	First, End int // the characters, Chars[First:End]
	Box        pdf.Rect
}

// Text is a page's text in the order of its content.
type Text struct {
	Chars []Char
	Lines []Line
}

func (r *ctx) moveText(x, y float64) {
	r.tlm = pdf.Matrix{1, 0, 0, 1, x, y}.Mul(r.tlm)
	r.tm = r.tlm
}

func (r *ctx) adjustText(n float64) {
	ts := &r.gs.text
	if ts.font != nil && ts.font.vertical {
		r.tm = pdf.Matrix{1, 0, 0, 1, 0, -n / 1000 * ts.size}.Mul(r.tm)
		return
	}
	r.tm = pdf.Matrix{1, 0, 0, 1, -n / 1000 * ts.size * ts.scale, 0}.Mul(r.tm)
}

func (r *ctx) showText(s []byte) {
	gs := r.gs
	ts := &gs.text
	f := ts.font
	if f == nil {
		f = r.cache.fallbackFont()
	}
	for len(s) > 0 && r.op() {
		g := f.next(s)
		if g.n <= 0 {
			g.n = 1
		}
		space := g.n == 1 && s[0] == 32
		s = s[g.n:]
		trm := pdf.Matrix{ts.size * ts.scale, 0, 0, ts.size, 0, ts.rise}.Mul(r.tm)
		var ox, oy float64
		if f.vertical {
			ox, oy = -g.width/2, -0.88
		}
		if f.type3 {
			r.drawType3(f, &g, trm)
		} else if g.prog != nil && g.gid >= 0 {
			r.drawGlyph(f, &g, pdf.Matrix{1, 0, 0, 1, ox, oy}.Mul(trm))
		}
		if r.out != nil && r.hidden == 0 {
			r.emitChar(f, &g, pdf.Matrix{1, 0, 0, 1, ox, oy}.Mul(trm))
		}
		extra := ts.charSpace
		if space {
			extra += ts.wordSpace
		}
		if f.vertical {
			r.tm = pdf.Matrix{1, 0, 0, 1, 0, -(ts.size + extra)}.Mul(r.tm)
		} else {
			r.tm = pdf.Matrix{1, 0, 0, 1, (g.width*ts.size + extra) * ts.scale, 0}.Mul(r.tm)
		}
	}
	if r.vec != nil {
		r.vecTextDone()
	}
}

func (r *ctx) drawGlyph(f *pdfFont, g *glyph, trm pdf.Matrix) {
	gs := r.gs
	mode := gs.text.mode
	clipping := mode >= 4 && mode <= 7
	if mode == 3 || (!r.drawing() && !clipping) || r.noDraw {
		return
	}
	o := f.outline(g.prog, g.gid)
	if len(o) == 0 {
		return
	}
	m := g.prog.Matrix()
	gm := pdf.Matrix{m[0] * g.xscale, m[1], m[2] * g.xscale, m[3], m[4], m[5]}
	if mode != 7 {
		dev := gm.Mul(trm).Mul(gs.ctm)
		p := &poly{}
		outlinePath(o, dev, p)
		if r.drawing() {
			bounds := gs.clip.r.Intersect(r.layer.r)
			if mode == 0 || mode == 2 || mode == 4 || mode == 6 {
				r.paintMask(r.ra.fill(p, false, bounds, 5), &gs.fill, gs.ca)
			}
			if mode == 1 || mode == 2 || mode == 5 || mode == 6 {
				up := path{}
				toPath(o, gm.Mul(trm), &up)
				r.paintMask(r.ra.fill(stroke(&up, &gs.line, gs.ctm), false, bounds, 4), &gs.stroke, gs.CA)
			}
		}
		if r.vecOn() {
			r.vecGlyph(f, g, o, gm, trm, mode == 0 || mode == 2 || mode == 4 || mode == 6, mode == 1 || mode == 2 || mode == 5 || mode == 6)
		}
		if clipping {
			if r.vec != nil {
				r.vecAddTextClip(o, gm.Mul(trm))
			}
			if r.textClip == nil {
				r.textClip = &poly{}
			}
			r.textClip.pts = append(r.textClip.pts, p.pts...)
			base := len(r.textClip.pts) - len(p.pts)
			for _, st := range p.starts {
				r.textClip.starts = append(r.textClip.starts, base+st)
			}
		}
	} else { // clip only
		if r.textClip == nil {
			r.textClip = &poly{}
		}
		outlinePath(o, gm.Mul(trm).Mul(gs.ctm), r.textClip)
		if r.vec != nil {
			r.vecAddTextClip(o, gm.Mul(trm))
		}
	}
}

func toPath(o font.Path, m pdf.Matrix, p *path) {
	var cur point
	for _, s := range o {
		switch s.Op {
		case 'M':
			x, y := m.Apply(s.X, s.Y)
			p.moveTo(x, y)
			cur = point{x, y}
		case 'L':
			x, y := m.Apply(s.X, s.Y)
			p.lineTo(x, y)
			cur = point{x, y}
		case 'Q':
			cx, cy := m.Apply(s.X1, s.Y1)
			x, y := m.Apply(s.X, s.Y)
			p.curveTo(cur.x+2.0/3*(cx-cur.x), cur.y+2.0/3*(cy-cur.y), x+2.0/3*(cx-x), y+2.0/3*(cy-y), x, y)
			cur = point{x, y}
		case 'C':
			x1, y1 := m.Apply(s.X1, s.Y1)
			x2, y2 := m.Apply(s.X2, s.Y2)
			x, y := m.Apply(s.X, s.Y)
			p.curveTo(x1, y1, x2, y2, x, y)
			cur = point{x, y}
		case 'Z':
			p.close()
		}
	}
}

func (r *ctx) endText() {
	if r.textClip != nil {
		c := r.textClip
		r.textClip = nil
		if r.vec != nil && r.vec.textClip != nil {
			r.vecClip(r.vec.textClip, false)
			r.vec.textClip = nil
		}
		r.clipTo(c, false)
	}
}

func (r *ctx) drawType3(f *pdfFont, g *glyph, trm pdf.Matrix) {
	if r.depth >= maxFormDepth || g.name == "" || r.noDraw {
		return
	}
	s, _ := r.doc.Resolve(f.charProcs[pdf.Name(g.name)]).(*pdf.Stream)
	if s == nil {
		return
	}
	body, err := r.doc.Decode(s)
	if err != nil && len(body) == 0 {
		return
	}
	res := f.resources
	if res == nil {
		res = r.res
	}
	r.depth++
	r.save()
	saveUn, savePath := r.uncolored, r.path
	saveTm, saveTlm := r.tm, r.tlm
	r.path = path{}
	r.uncolored = false
	gs := r.gs
	gs.ctm = f.fontMatrix.Mul(trm).Mul(gs.ctm)
	gs.stroke = gs.fill
	gs.text = textState{scale: 1}
	r.interpret(body, res)
	r.uncolored, r.path = saveUn, savePath
	r.tm, r.tlm = saveTm, saveTlm
	r.restore()
	r.depth--
}

func (r *ctx) emitChar(f *pdfFont, g *glyph, trm pdf.Matrix) {
	gs := r.gs
	text := g.text
	if text == "" || text == "\x00" {
		return
	}
	full := trm.Mul(gs.ctm).Mul(r.pageInv)
	w := g.width
	if w <= 0 {
		w = 0.01
	}
	a, d := f.ascent, f.descent
	if f.type3 {
		a, d = 0.8, -0.2
	}
	corner := func(x, y float64) [2]float64 {
		px, py := full.Apply(x, y)
		return [2]float64{px, py}
	}
	c := Char{Text: text, Font: f.name, Bold: f.bold, Italic: f.italic, RGB: gs.fill.rgb}
	c.Quad = [4][2]float64{corner(0, d), corner(w, d), corner(w, a), corner(0, a)}
	c.Box = pdf.Rect{X0: c.Quad[0][0], Y0: c.Quad[0][1], X1: c.Quad[0][0], Y1: c.Quad[0][1]}
	for _, q := range c.Quad[1:] {
		c.Box = c.Box.Union(pdf.Rect{X0: q[0], Y0: q[1], X1: q[0], Y1: q[1]})
	}
	c.Base = corner(0, 0)
	c.Size = math.Hypot(c.Quad[3][0]-c.Quad[0][0], c.Quad[3][1]-c.Quad[0][1]) / (a - d)
	r.out.Chars = append(r.out.Chars, c)
}

// finish works out lines and adds the spaces and line ends the content only implies.
func (t *Text) finish() {
	in := t.Chars
	if len(in) == 0 {
		return
	}
	out := make([]Char, 0, len(in)+len(in)/8)
	var last *Char
	lineStart := 0
	endLine := func() {
		if len(out) == lineStart {
			return
		}
		box := out[lineStart].Box
		for i := lineStart + 1; i < len(out); i++ {
			box = box.Union(out[i].Box)
		}
		t.Lines = append(t.Lines, Line{First: lineStart, End: len(out), Box: box})
		for i := lineStart; i < len(out); i++ {
			out[i].Line = len(t.Lines) - 1
		}
		lineStart = len(out)
	}
	for i := range in {
		c := &in[i]
		if last != nil {
			ux, uy := unit(last.Quad[1][0]-last.Quad[0][0], last.Quad[1][1]-last.Quad[0][1])
			if ux == 0 && uy == 0 {
				ux = 1
			}
			// How far along the text's direction, and across it, this character is from the end of the last.
			dx, dy := c.Quad[0][0]-last.Quad[1][0], c.Quad[0][1]-last.Quad[1][1]
			along := dx*ux + dy*uy
			across := -dx*uy + dy*ux
			size := math.Max(last.Size, 1)
			switch {
			case math.Abs(across) > size*0.5 || along < -size*1.5 || along > size*6:
				nl := Char{Text: "\n", Quad: last.Quad, Box: last.Box, Size: last.Size, Synthetic: true}
				out = append(out, nl)
				endLine()
			case along > size*0.18 && !isSpaceText(last.Text) && !isSpaceText(c.Text):
				sp := Char{Text: " ", Size: last.Size, Font: last.Font, Synthetic: true}
				sp.Quad = [4][2]float64{last.Quad[1], c.Quad[0], c.Quad[3], last.Quad[2]}
				sp.Box = pdf.Rect{X0: last.Box.X1, Y0: last.Box.Y0, X1: c.Box.X0, Y1: last.Box.Y1}.Normal()
				out = append(out, sp)
			}
		}
		out = append(out, *c)
		last = &out[len(out)-1]
	}
	endLine()
	t.Chars = out
}

func unit(x, y float64) (float64, float64) {
	l := math.Hypot(x, y)
	if l == 0 {
		return 0, 0
	}
	return x / l, y / l
}

func isSpaceText(s string) bool {
	return strings.TrimSpace(s) == ""
}

// Hit is the character nearest a point in user space; ok is false when no line is near.
func (t *Text) Hit(x, y float64) (index int, ok bool) {
	best, bestD := -1, math.Inf(1)
	for li := range t.Lines {
		l := &t.Lines[li]
		dy := math.Max(math.Max(l.Box.Y0-y, y-l.Box.Y1), 0)
		dx := math.Max(math.Max(l.Box.X0-x, x-l.Box.X1), 0)
		d := dy*4 + dx
		if d < bestD {
			bestD, best = d, li
		}
	}
	if best < 0 {
		return 0, false
	}
	l := &t.Lines[best]
	idx, nearest := l.First, math.Inf(1)
	for i := l.First; i < l.End; i++ {
		c := &t.Chars[i]
		if c.Text == "\n" {
			continue
		}
		cx := (c.Box.X0 + c.Box.X1) / 2
		cy := (c.Box.Y0 + c.Box.Y1) / 2
		dist := math.Hypot(cx-x, cy-y)
		if dist < nearest {
			nearest = dist
			idx = i
			if x > cx && l.Box.W() >= l.Box.H() {
				idx = i + 1 // the caret goes after a character whose right half was hit
			}
		}
	}
	return idx, bestD < 40
}

// InText reports whether a point is on a line of text, as a pointer over words is.
func (t *Text) InText(x, y float64) bool {
	for i := range t.Lines {
		if t.Lines[i].Box.Contains(x, y) {
			return true
		}
	}
	return false
}

// LineAt is the line a point is on, -1 for none.
func (t *Text) LineAt(x, y float64) int {
	for i := range t.Lines {
		if t.Lines[i].Box.Inset(-1).Contains(x, y) {
			return i
		}
	}
	return -1
}

// String is the text between two character indexes.
func (t *Text) String(from, to int) string {
	if from > to {
		from, to = to, from
	}
	var b strings.Builder
	for i := max(from, 0); i < min(to, len(t.Chars)); i++ {
		b.WriteString(t.Chars[i].Text)
	}
	return b.String()
}

// Full is all the text of the page.
func (t *Text) Full() string { return t.String(0, len(t.Chars)) }

// Quads are the boxes to shade for a selection, one a line. Each runs corner to corner in user space, in QuadPoints
// order: upper left, upper right, lower left, lower right.
func (t *Text) Quads(from, to int) [][8]float64 {
	if from > to {
		from, to = to, from
	}
	var out [][8]float64
	for li := range t.Lines {
		l := &t.Lines[li]
		a, b := max(from, l.First), min(to, l.End)
		for a < b && t.Chars[a].Text == "\n" {
			a++
		}
		for a < b && t.Chars[b-1].Text == "\n" {
			b--
		}
		if a >= b {
			continue
		}
		f, e := &t.Chars[a], &t.Chars[b-1]
		out = append(out, [8]float64{f.Quad[3][0], f.Quad[3][1], e.Quad[2][0], e.Quad[2][1], f.Quad[0][0], f.Quad[0][1], e.Quad[1][0], e.Quad[1][1]})
	}
	return out
}

// Match is a hit of a search.
type Match struct{ From, To int }

func foldText(s string) []rune {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		if unicode.IsSpace(r) {
			r = ' '
		}
		out = append(out, unicode.ToLower(r))
	}
	return out
}

// Search finds a string ignoring case, a run of spaces matching any run of white space or a line end.
func (t *Text) Search(q string) []Match {
	needle := foldText(strings.Join(strings.Fields(q), " "))
	if len(needle) == 0 {
		return nil
	}
	type at struct {
		r    rune
		char int
	}
	var hay []at
	for i := range t.Chars {
		for _, r := range foldText(t.Chars[i].Text) {
			if r == ' ' && len(hay) > 0 && hay[len(hay)-1].r == ' ' {
				continue
			}
			hay = append(hay, at{r, i})
		}
	}
	var out []Match
	for i := 0; i+len(needle) <= len(hay); i++ {
		ok := true
		for j, r := range needle {
			if hay[i+j].r != r {
				ok = false
				break
			}
		}
		if ok {
			out = append(out, Match{hay[i].char, hay[i+len(needle)-1].char + 1})
			i += len(needle) - 1
		}
	}
	return out
}

// LineText is what a line says without its line end.
func (t *Text) LineText(i int) string {
	l := t.Lines[i]
	return strings.TrimRight(t.String(l.First, l.End), "\n")
}
