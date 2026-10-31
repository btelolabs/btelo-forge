package pptx

import (
	"errors"
	"fmt"
	"image/color"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/btelolabs/btelo-forge/internal/office/opc"
	"github.com/btelolabs/btelo-forge/internal/office/ox"
)

// Schema order of a run's and a paragraph's properties.
var (
	rPrOrder = []string{"a:ln", "a:noFill", "a:solidFill", "a:gradFill", "a:blipFill", "a:pattFill", "a:grpFill", "a:effectLst", "a:effectDag", "a:highlight",
		"a:uLnTx", "a:uLn", "a:uFillTx", "a:uFill", "a:latin", "a:ea", "a:cs", "a:sym", "a:hlinkClick", "a:hlinkMouseOver", "a:rtl", "a:extLst"}
	pPrOrder = []string{"a:lnSpc", "a:spcBef", "a:spcAft", "a:buClrTx", "a:buClr", "a:buSzTx", "a:buSzPct", "a:buSzPts", "a:buFontTx", "a:buFont", "a:buNone", "a:buAutoNum", "a:buChar",
		"a:buBlip", "a:tabLst", "a:defRPr", "a:extLst"}
)

// ensurePPr is a paragraph's properties, added first among its children when missing.
func ensurePPr(p *ox.Node) *ox.Node {
	if c := p.Child("a:pPr"); c != nil {
		return c
	}
	c := ox.New("a:pPr")
	p.Insert(0, c)
	return c
}

// ensureRPr is a run's properties, added first among its children when missing.
func ensureRPr(r *ox.Node) *ox.Node {
	if c := r.Child("a:rPr"); c != nil {
		return c
	}
	c := ox.New("a:rPr", "lang", "en-US")
	r.Insert(0, c)
	return c
}

// Target is a body of text: a shape's, or a table cell's.
type Target struct {
	ID       int
	Row, Col int // the cell, when Cell is true
	Cell     bool
}

// Body finds a target's text body, adding one to a shape or cell without.
func (d *Doc) Body(s *Slide, t Target) (*ox.Node, *Shape) {
	sh := d.Find(s, t.ID)
	if sh == nil {
		return nil, nil
	}
	if t.Cell {
		tc := cellAt(sh, t.Row, t.Col)
		if tc == nil {
			return nil, sh
		}
		return tc.Child("a:txBody"), sh
	}
	return sh.body(), sh
}

// HasBody reports that the target can hold words.
func (d *Doc) HasBody(s *Slide, t Target) bool {
	b, _ := d.Body(s, t)
	return b != nil
}

func cellAt(sh *Shape, row, col int) *ox.Node {
	if sh.Kind != KindTable {
		return nil
	}
	rows := sh.Node.Path("a:graphic", "a:graphicData", "a:tbl").Elements("a:tr")
	if row < 0 || row >= len(rows) {
		return nil
	}
	tcs := rows[row].Elements("a:tc")
	if col < 0 || col >= len(tcs) {
		return nil
	}
	return tcs[col]
}

// EnsureBody gives a shape without a text body one, in schema order. It belongs inside an Edit.
func EnsureBody(sh *Shape) *ox.Node {
	if b := sh.Node.Child("p:txBody"); b != nil {
		return b
	}
	b := newElement(`<p:txBody><a:bodyPr rtlCol="0" anchor="ctr"/><a:lstStyle/><a:p><a:pPr algn="ctr"/><a:endParaRPr lang="en-US"/></a:p></p:txBody>`)
	sh.Node.InsertOrdered(b, []string{"p:nvSpPr", "p:spPr", "p:style", "p:txBody", "p:extLst"})
	return b
}

// piece is an element of a paragraph, and the caret places it holds.
type piece struct {
	n      *ox.Node
	at, of int
}

func pieces(p *ox.Node) []piece {
	var out []piece
	at := 0
	for _, c := range p.Elements("a:r", "a:br", "a:fld") {
		k := 1
		if c.Name != "a:br" {
			k = utf8.RuneCountInString(c.Child("a:t").InnerText())
		}
		out = append(out, piece{c, at, k})
		at += k
	}
	return out
}

// ParaLength is how many caret places a paragraph has.
func ParaLength(p *ox.Node) int {
	n := 0
	for _, k := range pieces(p) {
		n += k.of
	}
	return n
}

// splitAt makes a run boundary at off, and returns the index among p's children where it is.
func splitAt(p *ox.Node, off int) int {
	for _, k := range pieces(p) {
		if off <= k.at {
			return p.Index(k.n)
		}
		if off < k.at+k.of {
			if k.n.Name != "a:r" {
				return p.Index(k.n) // a field is one thing: the place before it
			}
			rs := []rune(k.n.Child("a:t").InnerText())
			cut := off - k.at
			right := k.n.Clone()
			setRunText(k.n, string(rs[:cut]))
			setRunText(right, string(rs[cut:]))
			at := p.Index(k.n) + 1
			p.Insert(at, right)
			return at
		}
	}
	// Past the words: before the end mark.
	if e := p.Child("a:endParaRPr"); e != nil {
		return p.Index(e)
	}
	return len(p.Children)
}

func setRunText(r *ox.Node, s string) {
	t := r.Child("a:t")
	if t == nil {
		t = ox.New("a:t")
		r.Append(t)
	}
	t.Children = nil
	if s != "" {
		t.Append(ox.NewText(s))
	}
}

// typingProps is the run properties new words at off take: the run before them, or the end mark's.
func typingProps(p *ox.Node, off int) *ox.Node {
	var prev *ox.Node
	for _, k := range pieces(p) {
		if k.at < off && k.n.Name != "a:br" {
			prev = k.n
		}
		if k.at == off && k.n.Name != "a:br" && off == 0 {
			prev = k.n
		}
	}
	if prev != nil {
		return prev.Child("a:rPr")
	}
	if e := p.Child("a:endParaRPr"); e != nil {
		c := e.Clone()
		c.Name = "a:rPr"
		return c
	}
	return nil
}

func addWords(p *ox.Node, off int, text string) {
	if text == "" {
		return
	}
	// In a run already: the words simply join it.
	for _, k := range pieces(p) {
		if k.n.Name == "a:r" && off > k.at && off <= k.at+k.of {
			rs := []rune(k.n.Child("a:t").InnerText())
			setRunText(k.n, string(rs[:off-k.at])+text+string(rs[off-k.at:]))
			return
		}
	}
	props := typingProps(p, off)
	at := splitAt(p, off)
	r := ox.New("a:r")
	if props != nil {
		r.Append(props.Clone())
	} else {
		r.Append(ox.New("a:rPr", "lang", "en-US"))
	}
	r.Append(ox.New("a:t"))
	setRunText(r, text)
	p.Insert(at, r)
}

// insertBreak adds a line break at off.
func insertBreak(p *ox.Node, off int) {
	props := typingProps(p, off)
	at := splitAt(p, off)
	br := ox.New("a:br")
	if props != nil {
		br.Append(props.Clone())
	}
	p.Insert(at, br)
}

// TypeText puts words at a place: newlines make paragraphs, vertical tabs line breaks. It returns the place after
// them. Nothing here starts a step of the history: the caller wraps it in Edit.
func TypeText(body *ox.Node, at Pos, text string) Pos {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	paras := body.Elements("a:p")
	if len(paras) == 0 {
		body.Append(newElement(`<a:p><a:endParaRPr lang="en-US"/></a:p>`))
		paras = body.Elements("a:p")
	}
	at.Para = clampI(at.Para, 0, len(paras)-1)
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if i > 0 {
			at = SplitPara(body, at)
		}
		p := body.Elements("a:p")[at.Para]
		for k, part := range strings.Split(line, "\v") {
			if k > 0 {
				insertBreak(p, at.Off)
				at.Off++
			}
			addWords(p, at.Off, part)
			at.Off += utf8.RuneCountInString(part)
		}
	}
	return at
}

// SplitPara breaks a paragraph at a place into two, the second keeping its settings.
func SplitPara(body *ox.Node, at Pos) Pos {
	paras := body.Elements("a:p")
	p := paras[clampI(at.Para, 0, len(paras)-1)]
	cut := splitAt(p, at.Off)
	next := ox.New("a:p")
	if pr := p.Child("a:pPr"); pr != nil {
		next.Append(pr.Clone())
	}
	rest := append([]*ox.Node(nil), p.Children[cut:]...)
	for _, c := range rest {
		if c.Kind == ox.Element && c.Name == "a:endParaRPr" {
			continue
		}
		c.Remove()
		next.Append(c)
	}
	// Both ends of the split keep the look of the words at the caret.
	props := typingProps(p, at.Off)
	if end := p.Child("a:endParaRPr"); end != nil {
		next.Append(end.Clone())
	} else if props != nil {
		c := props.Clone()
		c.Name = "a:endParaRPr"
		next.Append(c)
	}
	if p.Child("a:endParaRPr") == nil && props != nil {
		c := props.Clone()
		c.Name = "a:endParaRPr"
		p.Append(c)
	}
	body.Insert(body.Index(p)+1, next)
	return Pos{at.Para + 1, 0}
}

// DeleteRange removes the words from a to b, joining paragraphs it runs across.
func DeleteRange(body *ox.Node, a, b Pos) {
	paras := body.Elements("a:p")
	if len(paras) == 0 || a == b {
		return
	}
	a.Para, b.Para = clampI(a.Para, 0, len(paras)-1), clampI(b.Para, 0, len(paras)-1)
	first := paras[a.Para]
	if a.Para == b.Para {
		removeWords(first, a.Off, b.Off)
		return
	}
	last := paras[b.Para]
	removeWords(first, a.Off, ParaLength(first))
	removeWords(last, 0, b.Off)
	for i := a.Para + 1; i < b.Para; i++ {
		paras[i].Remove()
	}
	at := first.Index(first.Child("a:endParaRPr"))
	for _, c := range append([]*ox.Node(nil), last.Children...) {
		if c.Kind != ox.Element || c.Name == "a:pPr" {
			continue
		}
		if c.Name == "a:endParaRPr" {
			if e := first.Child("a:endParaRPr"); e != nil {
				e.Remove()
				at = len(first.Children)
			}
			c.Remove()
			first.Append(c)
			continue
		}
		c.Remove()
		if at < 0 || at > len(first.Children) {
			first.Append(c)
		} else {
			first.Insert(at, c)
			at++
		}
	}
	last.Remove()
}

func removeWords(p *ox.Node, from, to int) {
	if from >= to {
		return
	}
	// An end mark keeps the look of the run removed last, for an emptied paragraph.
	lone := len(pieces(p)) > 0 && from <= 0 && to >= ParaLength(p)
	var keep *ox.Node
	if lone {
		keep = typingProps(p, 0)
	}
	for _, k := range pieces(p) {
		if k.at >= to || k.at+k.of <= from {
			continue
		}
		if k.n.Name != "a:r" || from <= k.at && to >= k.at+k.of {
			k.n.Remove()
			continue
		}
		rs := []rune(k.n.Child("a:t").InnerText())
		lo, hi := max(from-k.at, 0), min(to-k.at, len(rs))
		setRunText(k.n, string(rs[:lo])+string(rs[hi:]))
	}
	if lone && keep != nil && p.Child("a:endParaRPr") == nil {
		c := keep.Clone()
		c.Name = "a:endParaRPr"
		p.Append(c)
	}
}

// EachRun applies fn to the properties of every run in a range, splitting runs at its ends. An empty paragraph in the
// range has its end mark set.
func EachRun(body *ox.Node, a, b Pos, fn func(rPr *ox.Node)) {
	paras := body.Elements("a:p")
	for i := a.Para; i <= b.Para && i < len(paras); i++ {
		if i < 0 {
			continue
		}
		p := paras[i]
		n := ParaLength(p)
		from, to := 0, n
		if i == a.Para {
			from = a.Off
		}
		if i == b.Para {
			to = b.Off
		}
		if n == 0 || from == 0 && to == n {
			e := p.Child("a:endParaRPr")
			if e == nil {
				e = ox.New("a:endParaRPr", "lang", "en-US")
				p.Append(e)
			}
			fn(e)
		}
		if from >= to {
			continue
		}
		splitAt(p, from)
		splitAt(p, to)
		for _, k := range pieces(p) {
			if k.at >= from && k.at+k.of <= to {
				fn(ensureRPr(k.n))
			}
		}
	}
}

// EachPara applies fn to the properties of the paragraphs from a to b.
func EachPara(body *ox.Node, a, b int, fn func(pPr *ox.Node)) {
	paras := body.Elements("a:p")
	for i := max(a, 0); i <= b && i < len(paras); i++ {
		fn(ensurePPr(paras[i]))
	}
}

// Whole is the range of all a body's words.
func Whole(body *ox.Node) (Pos, Pos) {
	paras := body.Elements("a:p")
	if len(paras) == 0 {
		return Pos{}, Pos{}
	}
	return Pos{}, Pos{len(paras) - 1, ParaLength(paras[len(paras)-1])}
}

// PlainText is a body's words, paragraphs on lines of their own.
func PlainText(body *ox.Node) string {
	var b strings.Builder
	for i, p := range body.Elements("a:p") {
		if i > 0 {
			b.WriteByte('\n')
		}
		for _, k := range pieces(p) {
			if k.n.Name == "a:br" {
				b.WriteByte('\v')
			} else {
				b.WriteString(k.n.Child("a:t").InnerText())
			}
		}
	}
	return b.String()
}

// RangeText is the words of a range.
func RangeText(body *ox.Node, a, b Pos) string {
	var out strings.Builder
	paras := body.Elements("a:p")
	for i := a.Para; i <= b.Para && i < len(paras); i++ {
		if i > a.Para {
			out.WriteByte('\n')
		}
		from, to := 0, 1<<30
		if i == a.Para {
			from = a.Off
		}
		if i == b.Para {
			to = b.Off
		}
		for _, k := range pieces(paras[i]) {
			if k.n.Name == "a:br" {
				if k.at >= from && k.at < to {
					out.WriteByte('\n')
				}
				continue
			}
			rs := []rune(k.n.Child("a:t").InnerText())
			lo, hi := clampI(from-k.at, 0, len(rs)), clampI(to-k.at, 0, len(rs))
			if lo < hi {
				out.WriteString(string(rs[lo:hi]))
			}
		}
	}
	return out.String()
}

// SetBodyText replaces a body's words, keeping the look of its first paragraph and run.
func SetBodyText(body *ox.Node, text string) {
	paras := body.Elements("a:p")
	var pPr, rPr *ox.Node
	if len(paras) > 0 {
		if v := paras[0].Child("a:pPr"); v != nil {
			pPr = v.Clone()
		}
		rPr = typingProps(paras[0], 0)
		if rPr != nil {
			rPr = rPr.Clone()
		}
	}
	for _, p := range paras {
		p.Remove()
	}
	first := ox.New("a:p")
	if pPr != nil {
		first.Append(pPr)
	}
	if rPr != nil {
		e := rPr.Clone()
		e.Name = "a:endParaRPr"
		first.Append(e)
	} else {
		first.Append(ox.New("a:endParaRPr", "lang", "en-US"))
	}
	if ext := body.Child("a:extLst"); ext != nil {
		body.Insert(body.Index(ext), first)
	} else {
		body.Append(first)
	}
	TypeText(body, Pos{}, text)
}

// Run property setters, for the toolbar and the tools.

// SetBool sets a run's b, i or the like.
func SetBool(name string, on bool) func(*ox.Node) {
	return func(r *ox.Node) {
		if on {
			r.SetAttr(name, "1")
		} else {
			r.SetAttr(name, "0")
		}
	}
}

// SetUnderline underlines a run or stops.
func SetUnderline(on bool) func(*ox.Node) {
	return func(r *ox.Node) {
		if on {
			r.SetAttr("u", "sng")
		} else {
			r.SetAttr("u", "none")
		}
	}
}

// SetSize sets a run's size in points.
func SetSize(pt float64) func(*ox.Node) {
	return func(r *ox.Node) { r.SetAttr("sz", strconv.Itoa(int(clampF(pt, 1, 4000)*100+0.5))) }
}

// SetFont sets a run's typeface, for Latin and East Asian words alike.
func SetFont(name string) func(*ox.Node) {
	return func(r *ox.Node) {
		for _, tag := range []string{"a:latin", "a:ea"} {
			if c := r.Child(tag); c != nil {
				c.Remove()
			}
			r.InsertOrdered(ox.New(tag, "typeface", name), rPrOrder)
		}
	}
}

// SetColor sets a run's colour.
func SetColor(c color.RGBA) func(*ox.Node) {
	return func(r *ox.Node) {
		for _, name := range fillNames {
			if k := r.Child(name); k != nil {
				k.Remove()
			}
		}
		r.InsertOrdered(solidXML(c), rPrOrder)
	}
}

// SetAlign sets a paragraph's alignment: l, ctr, r or just.
func SetAlign(a string) func(*ox.Node) { return func(p *ox.Node) { p.SetAttr("algn", a) } }

// SetLevel sets a paragraph's level, from 0 to 8.
func SetLevel(n int) func(*ox.Node) {
	return func(p *ox.Node) {
		if n <= 0 {
			p.DelAttr("lvl")
		} else {
			p.SetAttr("lvl", strconv.Itoa(min(n, 8)))
		}
	}
}

// SetBullet sets a paragraph's marker: "none", "bullet" or "number".
func SetBullet(kind string) func(*ox.Node) {
	return func(p *ox.Node) {
		for _, name := range []string{"a:buNone", "a:buAutoNum", "a:buChar", "a:buBlip", "a:buFont"} {
			if c := p.Child(name); c != nil {
				c.Remove()
			}
		}
		switch kind {
		case "none":
			p.InsertOrdered(ox.New("a:buNone"), pPrOrder)
			p.SetAttr("marL", "0")
			p.SetAttr("indent", "0")
		case "bullet":
			p.InsertOrdered(ox.New("a:buFont", "typeface", "Arial"), pPrOrder)
			p.InsertOrdered(ox.New("a:buChar", "char", "•"), pPrOrder)
			p.SetAttr("marL", "285750")
			p.SetAttr("indent", "-285750")
		case "number":
			p.InsertOrdered(ox.New("a:buFont", "typeface", "+mj-lt"), pPrOrder)
			p.InsertOrdered(ox.New("a:buAutoNum", "type", "arabicPeriod"), pPrOrder)
			p.SetAttr("marL", "342900")
			p.SetAttr("indent", "-342900")
		}
	}
}

// RunAt is how the words at a place are set, through the inheritance, for the toolbar.
func (r *Renderer) RunAt(s *Slide, t Target, at Pos) RunProps {
	body, sh := r.D.Body(s, t)
	if body == nil {
		return RunProps{Size: 18}
	}
	x := r.D.slideScope(s)
	src := r.sourceFor(sh, t, body, x, s)
	paras := body.Elements("a:p")
	if len(paras) == 0 {
		return RunProps{Size: 18}
	}
	p := paras[clampI(at.Para, 0, len(paras)-1)]
	level := min(max(int(attrInt(p.Child("a:pPr"), "lvl", 0)), 0), 8)
	st := styleOf(src.lists, level)
	if src.font != nil {
		if c, ok := x.col.resolve(first(src.font)); ok {
			st.Run.Color, st.Run.HasColor = c, true
		}
	}
	if src.extra != nil {
		mergeRun(&st.Run, src.extra)
	}
	st.apply(levelOf(src.own.list, level), x)
	st.apply(p.Child("a:pPr"), x)
	run := st.Run
	if rp := typingProps(p, at.Off); rp != nil {
		run.apply(rp, x)
	}
	theme := x.col.theme
	run.Latin = fontName(run.Latin, theme, src.major)
	if !run.HasColor {
		if c, ok := x.col.scheme("tx1"); ok {
			run.Color = c
		}
	}
	return run
}

// ParaAt is how the paragraph at a place is set.
func (r *Renderer) ParaAt(s *Slide, t Target, at Pos) ParaProps {
	body, sh := r.D.Body(s, t)
	if body == nil {
		return ParaProps{}
	}
	x := r.D.slideScope(s)
	src := r.sourceFor(sh, t, body, x, s)
	paras := body.Elements("a:p")
	if len(paras) == 0 {
		return ParaProps{}
	}
	p := paras[clampI(at.Para, 0, len(paras)-1)]
	level := min(max(int(attrInt(p.Child("a:pPr"), "lvl", 0)), 0), 8)
	st := styleOf(src.lists, level)
	st.apply(levelOf(src.own.list, level), x)
	st.apply(p.Child("a:pPr"), x)
	return st
}

// Level is a paragraph's level.
func Level(p *ox.Node) int { return min(max(int(attrInt(p.Child("a:pPr"), "lvl", 0)), 0), 8) }

func (r *Renderer) sourceFor(sh *Shape, t Target, body *ox.Node, x *scope, s *Slide) *textSource {
	if t.Cell {
		src := &textSource{tx: body, lists: r.D.tableChain(x), x: x}
		src.own = listRef{body.Child("a:lstStyle"), x}
		return src
	}
	return r.source(sh, x, s.Layout, r.D.Number(s))
}

// CellFrame is the layout of a cell's words, its matrix going to the slide.
func (r *Renderer) CellFrame(s *Slide, sh *Shape, row, col int) (*TextFrame, *Cell) {
	t := r.Table(s, sh)
	if t == nil {
		return nil, nil
	}
	for _, c := range t.Cells {
		if c.Row == row && c.Col == col && c.Frame != nil {
			c.Frame.Matrix = sh.Box.Upright().Mul(c.Frame.Matrix)
			return c.Frame, c
		}
	}
	return nil, nil
}

// fontSteps are what normal autofit tries: a font scale, with the line spacing it takes off.
var fontSteps = []struct{ scale, cut float64 }{{1, 0}, {0.925, 0.1}, {0.85, 0.1}, {0.775, 0.1}, {0.7, 0.2}, {0.625, 0.2}, {0.55, 0.2}, {0.475, 0.2}, {0.4, 0.2}, {0.325, 0.2}, {0.25, 0.2}}

// FitText makes a shape's box or its words suit each other after an edit, as its autofit says: a box that fits its
// shape grows or shrinks to the words, and a box that fits its words works out a new font scale.
func (r *Renderer) FitText(s *Slide, sh *Shape) {
	if sh.body() == nil {
		return
	}
	bp := bodyChain(sh)
	kind, _, _ := bp.fit()
	x := r.D.slideScope(s)
	g := geometryOf(sh)
	switch kind {
	case "shape":
		src := r.source(sh, x, s.Layout, r.D.Number(s))
		if sh.Ph != nil && !sh.HasText() {
			return
		}
		f := r.frame(sh, g, src)
		t, b := bp.num("tIns", 45720)/emuPerPt, bp.num("bIns", 45720)/emuPerPt
		box := sh.Box
		box.H = max(f.Height+t+b, 4)
		if v, _ := bp.attr("anchor"); v == "b" {
			box.Y = sh.Box.Y + sh.Box.H - box.H
		} else if v == "ctr" {
			box.Y = sh.Box.Y + (sh.Box.H-box.H)/2
		}
		if wrap, _ := bp.attr("wrap"); wrap == "none" {
			l, rr := bp.num("lIns", 91440)/emuPerPt, bp.num("rIns", 91440)/emuPerPt
			w := f.Width + l + rr
			switch algn := firstAlign(f); algn {
			case "ctr":
				box.X += (box.W - w) / 2
			case "r":
				box.X += box.W - w
			}
			box.W = w
		}
		if box != sh.Box {
			r.D.place(s, sh, box)
		}
	case "norm":
		own := sh.body().Child("a:bodyPr")
		fit := own.Child("a:normAutofit")
		if fit == nil {
			if own.Child("a:noAutofit") != nil || own.Child("a:spAutoFit") != nil {
				return
			}
			fit = ox.New("a:normAutofit")
			// The bodyPr's children: prstTxWarp, then the autofit.
			if w := own.Child("a:prstTxWarp"); w != nil {
				own.Insert(own.Index(w)+1, fit)
			} else {
				own.Insert(0, fit)
			}
		}
		chosen := fontSteps[len(fontSteps)-1]
		for _, st := range fontSteps {
			fit.SetAttr("fontScale", strconv.Itoa(int(st.scale*100000+0.5)))
			fit.SetAttr("lnSpcReduction", strconv.Itoa(int(st.cut*100000+0.5)))
			f := r.frame(sh, g, r.source(sh, x, s.Layout, r.D.Number(s)))
			if f.Height <= f.Rect[3]+0.5 {
				chosen = st
				break
			}
		}
		fit.DelAttr("fontScale")
		fit.DelAttr("lnSpcReduction")
		if chosen.scale < 1 {
			fit.SetAttr("fontScale", strconv.Itoa(int(chosen.scale*100000+0.5)))
			if chosen.cut > 0 {
				fit.SetAttr("lnSpcReduction", strconv.Itoa(int(chosen.cut*100000+0.5)))
			}
		}
	}
}

func firstAlign(f *TextFrame) string {
	if len(f.Paras) > 0 {
		return f.Paras[0].Props.Align
	}
	return ""
}

// notes.

// notesTree is the slide's notes part, parsed.
func (d *Doc) notesTree(s *Slide) *ox.Doc {
	if s.notes == nil && s.notesPart != "" {
		doc, err := parsePart(d.Pkg, s.notesPart)
		if err == nil {
			s.notes = doc
		}
	}
	return s.notes
}

func notesBody(doc *ox.Doc) *ox.Node {
	if doc == nil {
		return nil
	}
	var out *ox.Node
	for _, sp := range doc.Root.Path("p:cSld", "p:spTree").Elements("p:sp") {
		ph := sp.Path("p:nvSpPr", "p:nvPr", "p:ph")
		if ph != nil && ph.Attr("type") == "body" {
			out = sp.Child("p:txBody")
			break
		}
	}
	return out
}

// Notes are the slide's speaker notes.
func (d *Doc) Notes(s *Slide) string {
	b := notesBody(d.notesTree(s))
	if b == nil {
		return ""
	}
	return PlainText(b)
}

// SetNotes replaces the slide's speaker notes, making its notes page, and the notes master, if it has none. key joins
// the steps of what is being typed.
func (d *Doc) SetNotes(s *Slide, text, key string) error {
	needMaster := s.notesPart == "" && d.notesMaster() == ""
	return d.Edit(key, []*Slide{s}, needMaster, func() error {
		if s.notesPart == "" {
			if text == "" {
				return nil
			}
			if err := d.makeNotes(s); err != nil {
				return err
			}
		}
		b := notesBody(d.notesTree(s))
		if b == nil {
			return errors.New("the slide's notes page has no text placeholder")
		}
		SetBodyText(b, text)
		s.notesDirty = true
		return nil
	})
}

func (d *Doc) notesMaster() string {
	if r, ok := opc.RelOfType(d.presRels, relNotesMaster); ok {
		return opc.Resolve(d.Part, r.Target)
	}
	return ""
}

func (d *Doc) ensureNotesMaster() string {
	if m := d.notesMaster(); m != "" {
		return m
	}
	part := d.Pkg.UniqueName("ppt/notesMasters/notesMaster%d.xml")
	d.Pkg.Put(part, ctNotesMaster, []byte(notesMasterXML()))
	theme := d.Pkg.UniqueName("ppt/theme/theme%d.xml")
	d.Pkg.Put(theme, ctTheme, []byte(officeTheme))
	d.Pkg.AddRel(part, relTheme, opc.RelativeTarget(part, theme), false)
	rid := d.Pkg.AddRel(d.Part, relNotesMaster, opc.RelativeTarget(d.Part, part), false)
	d.presRels, _ = d.Pkg.Rels(d.Part)
	list := d.Pres.Root.Ensure("p:notesMasterIdLst", presOrder)
	list.Append(ox.New("p:notesMasterId", "r:id", rid))
	d.presDirty = true
	return part
}

func notesMasterXML() string {
	shape := func(id int, name, ph, xf, body string) string {
		return fmt.Sprintf(`<p:sp><p:nvSpPr><p:cNvPr id="%d" name="%s"/><p:cNvSpPr><a:spLocks noGrp="1"%s/></p:cNvSpPr><p:nvPr><p:ph %s/></p:nvPr></p:nvSpPr><p:spPr><a:xfrm>%s</a:xfrm><a:prstGeom prst="rect"><a:avLst/></a:prstGeom>%s`,
			id, name, map[bool]string{true: ` noRot="1" noChangeAspect="1"`, false: ""}[strings.Contains(ph, "sldImg")], ph, xf, body)
	}
	return xmlHead + `<p:notesMaster ` + nsDecl + `><p:cSld><p:bg><p:bgRef idx="1001"><a:schemeClr val="bg1"/></p:bgRef></p:bg><p:spTree>` + treeHead +
		shape(2, "Slide Image Placeholder", `type="sldImg" idx="2"`, box(685800, 1143000, 5486400, 3086100), `<a:noFill/><a:ln w="12700"><a:solidFill><a:prstClr val="black"/></a:solidFill></a:ln></p:spPr></p:sp>`) +
		shape(3, "Notes Placeholder", `type="body" sz="quarter" idx="3"`, box(685800, 4400550, 5486400, 3600450), `</p:spPr><p:txBody><a:bodyPr/><a:lstStyle/><a:p><a:pPr lvl="0"/><a:r><a:rPr lang="en-US"/><a:t>Click to edit Master text styles</a:t></a:r></a:p></p:txBody></p:sp>`) +
		`</p:spTree></p:cSld><p:clrMap bg1="lt1" tx1="dk1" bg2="lt2" tx2="dk2" accent1="accent1" accent2="accent2" accent3="accent3" accent4="accent4" accent5="accent5" accent6="accent6" hlink="hlink" folHlink="folHlink"/>` +
		`<p:notesStyle>` + notesLevels() + `</p:notesStyle></p:notesMaster>`
}

func notesLevels() string {
	var b strings.Builder
	for i := 0; i < 9; i++ {
		fmt.Fprintf(&b, `<a:lvl%dpPr marL="%d" algn="l" defTabSz="914400" rtl="0" eaLnBrk="1" latinLnBrk="0" hangingPunct="1"><a:defRPr sz="1200" kern="1200"><a:solidFill><a:schemeClr val="tx1"/></a:solidFill><a:latin typeface="+mn-lt"/><a:ea typeface="+mn-ea"/><a:cs typeface="+mn-cs"/></a:defRPr></a:lvl%dpPr>`, i+1, 457200*i, i+1)
	}
	return b.String()
}

func (d *Doc) makeNotes(s *Slide) error {
	master := d.ensureNotesMaster()
	part := d.Pkg.UniqueName("ppt/notesSlides/notesSlide%d.xml")
	xml := xmlHead + `<p:notes ` + nsDecl + `><p:cSld><p:spTree>` + treeHead +
		`<p:sp><p:nvSpPr><p:cNvPr id="2" name="Slide Image Placeholder 1"/><p:cNvSpPr><a:spLocks noGrp="1" noRot="1" noChangeAspect="1"/></p:cNvSpPr><p:nvPr><p:ph type="sldImg"/></p:nvPr></p:nvSpPr><p:spPr/></p:sp>` +
		`<p:sp><p:nvSpPr><p:cNvPr id="3" name="Notes Placeholder 2"/><p:cNvSpPr><a:spLocks noGrp="1"/></p:cNvSpPr><p:nvPr><p:ph type="body" idx="1"/></p:nvPr></p:nvSpPr><p:spPr/><p:txBody><a:bodyPr/><a:lstStyle/><a:p><a:endParaRPr lang="en-US"/></a:p></p:txBody></p:sp>` +
		`</p:spTree></p:cSld><p:clrMapOvr><a:masterClrMapping/></p:clrMapOvr></p:notes>`
	d.Pkg.Put(part, ctNotes, []byte(xml))
	d.Pkg.AddRel(part, relNotesMaster, opc.RelativeTarget(part, master), false)
	d.Pkg.AddRel(part, relSlide, opc.RelativeTarget(part, s.Part), false)
	d.addRel(s, relNotes, opc.RelativeTarget(s.Part, part), false)
	s.notesPart = part
	s.notes = nil
	return nil
}
