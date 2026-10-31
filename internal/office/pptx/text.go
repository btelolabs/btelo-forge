package pptx

import (
	"image/color"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/btelolabs/btelo-forge/internal/gfx"
	"github.com/btelolabs/btelo-forge/internal/office/flow"
	"github.com/btelolabs/btelo-forge/internal/office/ox"
)

// Pos is a caret place: a paragraph, and how many characters into it.
type Pos struct {
	Para, Off int
}

// ParaFrame is a laid out paragraph of a text frame.
type ParaFrame struct {
	Node   *ox.Node // the a:p
	Laid   *flow.Laid
	Props  ParaProps
	Spans  []Span // where its lines went: one, unless columns split it
	Length int
}

// Span is a run of a paragraph's lines at a place in the frame.
type Span struct {
	From, To int     // lines
	X, Y     float64 // where the paragraph's top left is, for these lines
}

// TextFrame is a body of text laid out in a shape or cell.
type TextFrame struct {
	Rect   [4]float64 // the area text is set in, x, y, w, h, in the frame's coordinates
	Paras  []ParaFrame
	Matrix Matrix // from the frame's coordinates to the slide's
	Height float64
	Width  float64 // the widest line, when the words do not wrap
	Fit    string
}

// textSource is what a frame is laid out from.
type textSource struct {
	tx     *ox.Node
	lists  []listRef
	own    listRef   // the body's own list style, over the rest
	font   *ox.Node  // a style's a:fontRef
	major  bool      // the text is in the theme's heading font by default
	x      *scope    // the scope of the words
	extra  *RunProps // what a table style adds over the lists
	number int       // the slide's number, for fields
}

// maxParas bounds a body's paragraphs: a hostile file could hold millions, and each is laid out on the window's
// goroutine.
const maxParas = 20000

// layoutParas lays the body's paragraphs out at a width, at a font scale and with the line spacing cut as autofit says.
func (r *Renderer) layoutParas(src *textSource, width, scale, cut float64, major bool) []ParaFrame {
	paras := src.tx.Elements("a:p")
	if len(paras) > maxParas {
		paras = paras[:maxParas]
	}
	out := make([]ParaFrame, 0, len(paras))
	counters := [10]int{}
	lastScheme := [10]string{}
	theme := src.x.col.theme
	for i, p := range paras {
		pr := p.Child("a:pPr")
		level := min(max(int(attrInt(pr, "lvl", 0)), 0), 8)
		lists := src.lists
		st := styleOf(lists, level)
		// The style's font reference sits between the inherited lists and the shape's own.
		if src.font != nil {
			if c, ok := src.x.col.resolve(first(src.font)); ok {
				st.Run.Color, st.Run.HasColor = c, true
			}
			switch src.font.Attr("idx") {
			case "major":
				st.Run.Latin = "+mj-lt"
			case "minor":
				st.Run.Latin = "+mn-lt"
			}
		}
		if src.extra != nil {
			mergeRun(&st.Run, src.extra)
		}
		st.apply(levelOf(src.own.list, level), src.own.x)
		st.apply(pr, src.x)
		fl := flow.Para{}
		fl.Left, fl.Right = float32(st.MarL), float32(st.MarR)
		fl.First = float32(st.Indent)
		switch st.Align {
		case "ctr":
			fl.Align = flow.Center
		case "r":
			fl.Align = flow.Right
		case "just", "dist", "justLow", "thaiDist":
			fl.Align = flow.Justify
		}
		size := float64(0)
		// Runs.
		var endStyle = st.Run
		if e := p.Child("a:endParaRPr"); e != nil {
			endStyle.apply(e, src.x)
		}
		length := 0
		for _, c := range p.Elements() {
			var rp RunProps
			text := ""
			switch c.Name {
			case "a:r":
				rp = st.Run
				rp.apply(c.Child("a:rPr"), src.x)
				text = c.Child("a:t").InnerText()
			case "a:br":
				rp = st.Run
				rp.apply(c.Child("a:rPr"), src.x)
				text = "\n"
			case "a:fld":
				rp = st.Run
				rp.apply(c.Child("a:rPr"), src.x)
				text = fieldText(c, src.number)
			default:
				continue
			}
			if text == "" {
				continue
			}
			if rp.Cap == "all" || rp.Cap == "small" {
				text = strings.Map(unicode.ToUpper, text)
			}
			if hl := c.Path("a:rPr", "a:hlinkClick"); hl != nil {
				if h, ok := src.x.col.scheme("hlink"); ok {
					rp.Color, rp.HasColor = h, true
				}
				rp.Under = true
			}
			length += utf8.RuneCountInString(text)
			size = max(size, rp.Size)
			appendRuns(&fl, text, &rp, theme, major, scale)
		}
		fl.Empty = flowStyle(&endStyle, theme, major, scale, "")
		if size == 0 {
			size = endStyle.Size
		}
		size *= scale
		fl.Line, fl.Rule = 0, flow.Multiple
		if st.LinePts > 0 {
			fl.Line, fl.Rule = float32(st.LinePts*scale), flow.Exact
		} else if st.LinePct > 0 {
			fl.Line = float32(max(st.LinePct-cut, 0.1))
		} else if cut > 0 {
			fl.Line = float32(1 - cut)
		}
		fl.After = float32(st.AfterPts*scale + st.AfterPct*size*1.2)
		if i > 0 {
			fl.Before = float32(st.BeforePts*scale + st.BeforePct*size*1.2)
		}
		for _, t := range st.Tabs {
			fl.Tabs = append(fl.Tabs, float32(t))
		}
		// Numbering counts up through paragraphs of a level, and restarts where the list does.
		marker := ""
		switch st.BuKind {
		case "char", "blip":
			marker = st.BuChar
			for k := level; k < len(counters); k++ {
				counters[k], lastScheme[k] = 0, ""
			}
		case "auto":
			if lastScheme[level] != st.BuAuto {
				counters[level] = st.BuStart - 1
				lastScheme[level] = st.BuAuto
			}
			counters[level]++
			marker = autoNumber(st.BuAuto, counters[level])
			for k := level + 1; k < len(counters); k++ {
				counters[k], lastScheme[k] = 0, ""
			}
		default:
			for k := level; k < len(counters); k++ {
				counters[k], lastScheme[k] = 0, ""
			}
		}
		if length == 0 {
			marker = "" // an empty paragraph shows no bullet
		}
		var bulletAt float32
		hasMarker := marker != ""
		if hasMarker {
			bs := flowStyle(&st.Run, theme, major, scale, "")
			if len(fl.Runs) > 0 {
				bs = fl.Runs[0].Style
				bs.Underline, bs.Strike = false, false
			}
			if st.BuFont != "" {
				ch, moved := bulletText(marker, st.BuFont)
				marker = ch
				if !moved {
					bs.Family = fontName(st.BuFont, theme, major)
				}
			}
			switch {
			case st.BuPts > 0:
				bs.Size = float32(st.BuPts * scale)
			case st.BuPct > 0:
				bs.Size *= float32(st.BuPct)
			}
			if st.HasBuColor {
				bs.Color = st.BuColor
			}
			bulletAt = float32(st.MarL + st.Indent)
			fl.Bullet, fl.BulletStyle = marker, bs
			// The words of the first line start at the left margin; the mark hangs in the indent.
			fl.First = 0
		}
		pf := ParaFrame{Node: p, Props: st, Length: length}
		paraCopy := fl
		pf.Laid = flow.Layout(&paraCopy, float32(width), r.Fonts)
		if hasMarker && pf.Laid.Marker != nil {
			pf.Laid.Marker.X = bulletAt
			if st.Indent >= 0 || bulletAt+pf.Laid.Marker.W > float32(st.MarL) {
				// No room in the indent: the words follow the mark, on the first line.
				paraCopy.First = bulletAt + pf.Laid.Marker.W + float32(size)*0.25 - float32(st.MarL)
				pf.Laid = flow.Layout(&paraCopy, float32(width), r.Fonts)
				pf.Laid.Marker.X = bulletAt
			}
		}
		out = append(out, pf)
	}
	return out
}

func mergeRun(dst, over *RunProps) {
	if over.Bold {
		dst.Bold = true
	}
	if over.Italic {
		dst.Italic = true
	}
	if over.HasColor {
		dst.Color, dst.HasColor = over.Color, true
	}
}

// fieldText is what a field shows.
func fieldText(n *ox.Node, number int) string {
	if strings.HasPrefix(n.Attr("type"), "slidenum") && number > 0 {
		return strconv.Itoa(number)
	}
	return n.Child("a:t").InnerText()
}

// flowStyle turns run properties into flow's.
func flowStyle(rp *RunProps, t *Theme, major bool, scale float64, family string) flow.Style {
	if family == "" {
		family = fontName(rp.Latin, t, major)
	}
	s := flow.Style{Family: family, Size: float32(max(rp.Size*scale, 1)), Bold: rp.Bold, Italic: rp.Italic,
		Underline: rp.Under, Strike: rp.Strike, Highlight: rp.Mark}
	if rp.HasColor {
		s.Color = rp.Color
	}
	switch {
	case rp.Baseline > 0:
		s.Baseline = 1
	case rp.Baseline < 0:
		s.Baseline = -1
	}
	return s
}

// appendRuns adds words to a paragraph, split where the East Asian font takes over from the Latin one. Each word
// keeps its caret places, one to a character, whichever run it lands in.
func appendRuns(p *flow.Para, text string, rp *RunProps, t *Theme, major bool, scale float64) {
	latin := flowStyle(rp, t, major, scale, "")
	ea := t.Minor.EA
	if major {
		ea = t.Major.EA
	}
	if rp.EA != "" {
		ea = fontName(rp.EA, t, major)
	}
	if ea == "" || ea == latin.Family {
		p.Runs = append(p.Runs, flow.Run{Text: text, Style: latin})
		return
	}
	east := latin
	east.Family = ea
	start := 0
	isEast := false
	for i, r := range text {
		e := isWide(r)
		if i == 0 {
			isEast = e
			continue
		}
		if e != isEast && !unicode.IsSpace(r) {
			st := latin
			if isEast {
				st = east
			}
			p.Runs = append(p.Runs, flow.Run{Text: text[start:i], Style: st})
			start, isEast = i, e
		}
	}
	st := latin
	if isEast {
		st = east
	}
	p.Runs = append(p.Runs, flow.Run{Text: text[start:], Style: st})
}

func isWide(r rune) bool {
	return unicode.Is(unicode.Han, r) || unicode.Is(unicode.Hiragana, r) || unicode.Is(unicode.Katakana, r) ||
		unicode.Is(unicode.Hangul, r) || r >= 0x3000 && r <= 0x303f || r >= 0xff00 && r <= 0xffef
}

// frameArgs are the geometry of a frame: the area text may use, in shape coordinates, and the shape's matrix.
type frameArgs struct {
	x, y, w, h float64
	matrix     Matrix
}

// layoutFrame lays a body out in an area.
func (r *Renderer) layoutFrame(src *textSource, bp bodyProps, a frameArgs) *TextFrame {
	f := &TextFrame{}
	ins := func(name string, def float64) float64 { return bp.num(name, def) / emuPerPt }
	l, t, rr, b := ins("lIns", 91440), ins("tIns", 45720), ins("rIns", 91440), ins("bIns", 45720)
	x, y, w, h := a.x+l, a.y+t, a.w-l-rr, a.h-t-b
	w, h = max(w, 1), max(h, 0)
	m := a.matrix
	// Text set sideways is laid out in the area turned, and the frame's matrix turns it back.
	vert, _ := bp.attr("vert")
	rot := bp.num("rot", 0) / 60000
	turn := 0.0
	switch vert {
	case "vert", "eaVert", "mongolianVert", "wordArtVert", "wordArtVertRtl":
		turn = 90
	case "vert270":
		turn = 270
	}
	if turn+rot != 0 {
		cx, cy := x+w/2, y+h/2
		if turn != 0 {
			w, h = h, w
		}
		x, y = cx-w/2, cy-h/2
		m = m.Mul(Translate(cx, cy)).Mul(Rotate(turn + rot)).Mul(Translate(-cx, -cy))
	}
	f.Matrix = m
	f.Rect = [4]float64{x, y, w, h}
	kind, scale, cut := bp.fit()
	f.Fit = kind
	wrap, _ := bp.attr("wrap")
	cols := max(int(bp.num("numCol", 1)), 1)
	cols = min(cols, 16)
	gap := bp.num("spcCol", 0) / emuPerPt
	cw := w
	if cols > 1 {
		cw = max((w-gap*float64(cols-1))/float64(cols), 1)
	}
	if wrap == "none" {
		cw = 1 << 20
	}
	f.Paras = r.layoutParas(src, cw, scale, cut, src.major)
	total := 0.0
	for i := range f.Paras {
		total += float64(f.Paras[i].Laid.Height)
	}
	f.Height = total
	if wrap == "none" {
		// Laid out without an edge: the block then sits by its first paragraph's alignment.
		widest := 0.0
		for i := range f.Paras {
			for _, line := range f.Paras[i].Laid.Lines {
				widest = max(widest, float64(line.Right))
			}
		}
		f.Width = widest
		dx := 0.0
		if len(f.Paras) > 0 {
			switch f.Paras[0].Props.Align {
			case "ctr":
				dx = (w - widest) / 2
			case "r":
				dx = w - widest
			}
		}
		// Each paragraph is laid out again to the block's width, so its own alignment works inside it.
		f.Paras = r.layoutParas(src, max(widest, 1), scale, cut, src.major)
		x += dx
		f.Rect[0], f.Rect[2] = x, widest
	}
	top := y
	switch v, _ := bp.attr("anchor"); v {
	case "ctr":
		top = y + (h-total)/2
	case "b":
		top = y + h - total
	}
	if ac, ok := bp.attr("anchorCtr"); ok && (ac == "1" || ac == "true") && wrap != "none" {
		widest := 0.0
		for i := range f.Paras {
			for _, line := range f.Paras[i].Laid.Lines {
				widest = max(widest, float64(line.Right))
			}
		}
		x += (w - widest) / 2
	}
	if cols == 1 {
		yy := top
		for i := range f.Paras {
			p := &f.Paras[i]
			p.Spans = []Span{{0, len(p.Laid.Lines), x, yy}}
			yy += float64(p.Laid.Height)
		}
		return f
	}
	// Columns take lines until they are full. A line never breaks across them.
	col, yy := 0, y
	for i := range f.Paras {
		p := &f.Paras[i]
		start := 0
		base := yy // where the paragraph's top would be, in this column
		cx := x + float64(col)*(cw+gap)
		for n := range p.Laid.Lines {
			line := &p.Laid.Lines[n]
			bottom := base + float64(line.Y+line.H)
			if bottom > y+h+0.5 && n > start && col+1 < cols || bottom > y+h+0.5 && yy > y && col+1 < cols && n == start {
				if n > start {
					p.Spans = append(p.Spans, Span{start, n, cx, base})
				}
				col++
				start = n
				cx = x + float64(col)*(cw+gap)
				base = y - float64(line.Y)
			}
			yy = base + float64(line.Y+line.H)
		}
		p.Spans = append(p.Spans, Span{start, len(p.Laid.Lines), cx, base})
		yy = base + float64(p.Laid.Height)
	}
	return f
}

// draw paints the frame's words into c, whose pixels are the frame's coordinates through view.
func (f *TextFrame) draw(c *gfx.Canvas, view Matrix, scale float64, r *Renderer, fg color.RGBA) {
	for i := range f.Paras {
		p := &f.Paras[i]
		for _, s := range p.Spans {
			x, y := view.Apply(s.X, s.Y)
			l := p.Laid
			if s.From != 0 || s.To != len(l.Lines) {
				part := *l
				part.Lines = l.Lines[s.From:s.To]
				if s.From != 0 {
					part.Marker = nil
				}
				l = &part
			}
			l.Draw(c, float32(x), float32(y), float32(scale), r.Fonts, nil, fg)
		}
	}
}

// Caret is where the caret stands, in the frame's coordinates: the top of the line and its height.
func (f *TextFrame) Caret(pos Pos, trailing bool) (x, y, h float64) {
	if len(f.Paras) == 0 {
		return f.Rect[0], f.Rect[1], 18
	}
	p := &f.Paras[clampI(pos.Para, 0, len(f.Paras)-1)]
	c := p.Laid.Caret(clampI(pos.Off, 0, p.Laid.Count), trailing)
	s := spanOf(p, c.Line)
	return s.X + float64(c.X), s.Y + float64(c.Y), float64(c.H)
}

func spanOf(p *ParaFrame, line int) Span {
	for _, s := range p.Spans {
		if line >= s.From && line < s.To {
			return s
		}
	}
	if len(p.Spans) > 0 {
		return p.Spans[len(p.Spans)-1]
	}
	return Span{}
}

func clampI(v, lo, hi int) int { return min(max(v, lo), hi) }

// Hit is the caret place nearest a point in the frame's coordinates.
func (f *TextFrame) Hit(x, y float64) (Pos, bool) {
	if len(f.Paras) == 0 {
		return Pos{}, false
	}
	bestPara, bestSpan := 0, 0
	best := 1e18
	for i := range f.Paras {
		p := &f.Paras[i]
		for k, s := range p.Spans {
			if s.To <= s.From {
				continue
			}
			first, last := &p.Laid.Lines[s.From], &p.Laid.Lines[s.To-1]
			top, bottom := s.Y+float64(first.Y), s.Y+float64(last.Y+last.H)
			d := 0.0
			if y < top {
				d = top - y
			} else if y > bottom {
				d = y - bottom
			}
			d += max(s.X-x, 0)
			if d < best {
				best, bestPara, bestSpan = d, i, k
			}
		}
	}
	p := &f.Paras[bestPara]
	s := p.Spans[bestSpan]
	n := s.To - 1
	for i := s.From; i < s.To; i++ {
		l := &p.Laid.Lines[i]
		if y < s.Y+float64(l.Y+l.H) {
			n = i
			break
		}
	}
	off, tr := p.Laid.HitLine(max(n, 0), float32(x-s.X))
	return Pos{bestPara, off}, tr
}

// Rects are the boxes of a selection, in the frame's coordinates, as x, y, w, h.
func (f *TextFrame) Rects(a, b Pos) [][4]float64 {
	var out [][4]float64
	for i := a.Para; i <= b.Para && i < len(f.Paras); i++ {
		if i < 0 {
			continue
		}
		p := &f.Paras[i]
		from, to := 0, p.Laid.Count+1
		if i == a.Para {
			from = a.Off
		}
		if i == b.Para {
			to = b.Off
		}
		for _, r := range p.Laid.Rects(from, to) {
			line := lineAt(p.Laid, r.Y)
			s := spanOf(p, line)
			out = append(out, [4]float64{s.X + float64(r.X), s.Y + float64(r.Y), float64(r.W), float64(r.H)})
		}
	}
	return out
}

func lineAt(l *flow.Laid, y float32) int {
	for i := range l.Lines {
		if y < l.Lines[i].Y+l.Lines[i].H {
			return i
		}
	}
	return len(l.Lines) - 1
}

// VerticalMove is the caret place a line up (dir -1) or down (dir 1) from pos, at the same x.
func (f *TextFrame) VerticalMove(pos Pos, dir int, trailing bool) Pos {
	if len(f.Paras) == 0 {
		return pos
	}
	p := &f.Paras[clampI(pos.Para, 0, len(f.Paras)-1)]
	c := p.Laid.Caret(clampI(pos.Off, 0, p.Laid.Count), trailing)
	line, para := c.Line+dir, pos.Para
	for line < 0 || line >= len(p.Laid.Lines) {
		para += dir
		if para < 0 || para >= len(f.Paras) {
			return pos
		}
		p = &f.Paras[para]
		if dir < 0 {
			line = len(p.Laid.Lines) - 1
		} else {
			line = 0
		}
	}
	off, _ := p.Laid.HitLine(line, c.X)
	return Pos{para, off}
}

// LineEnds are the first and last places of the line the caret is on.
func (f *TextFrame) LineEnds(pos Pos, trailing bool) (Pos, Pos) {
	if len(f.Paras) == 0 {
		return pos, pos
	}
	para := clampI(pos.Para, 0, len(f.Paras)-1)
	l := f.Paras[para].Laid
	line := &l.Lines[l.Caret(clampI(pos.Off, 0, l.Count), trailing).Line]
	return Pos{para, line.Start}, Pos{para, line.End}
}
