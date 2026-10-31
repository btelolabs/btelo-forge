// Package flow lays out rich paragraphs, for the word processor's pages and the presentation's text boxes alike:
// runs of styled words and pictures broken into lines, with the positions a caret and a selection need. Everything
// is in points, at no particular zoom, so a page breaks the same way however large it is shown; Draw scales.
package flow

import (
	"image"
	"image/color"
	"math"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/btelolabs/btelo-forge/internal/gfx"
)

// Style is how a run looks.
type Style struct {
	Family    string
	Size      float32 // points
	Bold      bool
	Italic    bool
	Underline bool
	Strike    bool
	Color     color.RGBA
	Highlight color.RGBA // behind the words, when its alpha is not zero
	Baseline  int8       // 1 raised, -1 lowered
}

// Inline is a picture in the flow of the words; it takes the place of one character.
type Inline struct {
	W, H float32 // points
	Key  string  // what the caller finds the picture by
}

// Run is words in one style, or one picture. '\n' in Text breaks the line; '\t' goes to the next tab stop.
type Run struct {
	Text   string
	Style  Style
	Inline *Inline
}

// Length is how many caret places the run takes.
func (r *Run) Length() int {
	if r.Inline != nil {
		return 1
	}
	return utf8.RuneCountInString(r.Text)
}

// Align is how lines sit between the indents.
type Align uint8

const (
	Left Align = iota
	Center
	Right
	Justify
)

// LineRule says how Para.Line is read.
type LineRule uint8

const (
	Multiple LineRule = iota // Line times the font's line height
	Exact                    // Line points, whatever the words
	AtLeast                  // at least Line points
)

// TabKind is how the words after a tab stand against its stop.
type TabKind uint8

const (
	TabLeft TabKind = iota
	TabCenter
	TabRight
	TabDecimal
)

// TabStop is a stop with an alignment and a leader, the character that fills the gap.
type TabStop struct {
	Pos    float32
	Kind   TabKind
	Leader rune
}

// Para is one paragraph.
type Para struct {
	Runs        []Run
	Align       Align
	Left, Right float32 // indents
	First       float32 // the first line's further indent; negative hangs
	Before      float32 // space above
	After       float32 // space below
	Line        float32 // see Rule; 0 means single
	Rule        LineRule
	Tabs        []float32 // tab stops from the left edge; others every TabEvery
	TabEvery    float32   // 0 means 36pt
	Stops       []TabStop // stops with alignments and leaders, in order; when set, Tabs is not used
	Bullet      string    // the list marker, drawn in the hanging indent
	BulletStyle Style
	Empty       Style // the style of an empty paragraph, for its height and the first character typed
}

// Frag is a piece of one line in one style.
type Frag struct {
	X, W       float32 // from the text area's left
	Text       string
	Style      Style
	Inline     *Inline
	Start, End int // caret places in the paragraph
	Offsets    []float32
	Space      float32 // what Justify adds at each space in the piece
	Baseline   float32 // the piece's own shift from the line's baseline, up
	Size       float32 // the size it is drawn at
	Leader     rune    // a tab's filling
}

// Line is one laid out line.
type Line struct {
	Y          float32 // top, from the paragraph's top
	H          float32
	Base       float32 // the baseline, from the line's top
	Start, End int
	Frags      []Frag
	Right      float32 // where the last piece ends
}

// Laid is a paragraph broken into lines.
type Laid struct {
	Para   *Para
	Width  float32
	Lines  []Line
	Height float32 // with the space above and below
	Count  int     // caret places
	Marker *Frag   // the list marker, placed
	fonts  Fonts
}

// fonts is what Layout and Draw find typefaces with.
type Fonts interface {
	Font(family string, bold, italic bool) *gfx.Font
}

// measureSize is the size widths are read at: without hinting they scale exactly, and one size means one cache.
const measureSize = 128

// widths caches character widths across paragraphs, by font.
type widths struct {
	mu sync.RWMutex
	m  map[*gfx.Font]map[rune]float32
}

var charWidths = widths{m: map[*gfx.Font]map[rune]float32{}}

func (w *widths) of(f *gfx.Font, r rune) float32 {
	w.mu.RLock()
	v, ok := w.m[f][r]
	w.mu.RUnlock()
	if ok {
		return v
	}
	v = f.Face(measureSize).Measure(string(r)) / measureSize
	w.mu.Lock()
	if w.m[f] == nil {
		w.m[f] = map[rune]float32{}
	}
	w.m[f][r] = v
	w.mu.Unlock()
	return v
}

func metrics(f *gfx.Font) (ascent, descent float32) {
	face := f.Face(measureSize)
	return face.Ascent / measureSize, face.Descent / measureSize
}

// item is one caret place being laid out.
type item struct {
	r      rune
	style  *Style
	inline *Inline
	w      float32
	font   *gfx.Font
	size   float32
	shift  float32
	asc    float32
	desc   float32
}

// shrink is what a raised or lowered run is scaled by.
const shrink = 0.66

func collect(p *Para, fonts Fonts) []item {
	n := 0
	for i := range p.Runs {
		n += p.Runs[i].Length()
	}
	items := make([]item, 0, n)
	for i := range p.Runs {
		run := &p.Runs[i]
		if run.Inline != nil {
			items = append(items, item{r: 0xfffc, style: &run.Style, inline: run.Inline, w: run.Inline.W, asc: run.Inline.H})
			continue
		}
		s := &run.Style
		f := fonts.Font(s.Family, s.Bold, s.Italic)
		size := s.Size
		shift := float32(0)
		if s.Baseline != 0 {
			shift = float32(s.Baseline) * size * 0.33
			size *= shrink
		}
		a, d := metrics(f)
		for _, r := range run.Text {
			w := float32(0)
			if r != '\n' && r != '\t' {
				w = charWidths.of(f, r) * size
			}
			items = append(items, item{r: r, style: s, w: w, font: f, size: size, shift: shift, asc: a * s.Size, desc: d * s.Size})
		}
	}
	return items
}

// noStart cannot begin a line, noEnd cannot end one: the CJK line-breaking rules every word processor applies.
const (
	noStart = "，。、；：？！）］｝》〉」』】〕”’…—～·%,.;:?!)]}"
	noEnd   = "（［｛《〈「『【〔“‘$(["
)

// breakable reports whether a line may break between a and b.
func breakable(a, b rune) bool {
	if strings.ContainsRune(noStart, b) || strings.ContainsRune(noEnd, a) {
		return false
	}
	if a == ' ' || a == '\t' || a == '-' {
		return true
	}
	return wide(a) || wide(b)
}

func wide(r rune) bool {
	return unicode.Is(unicode.Han, r) || unicode.Is(unicode.Hiragana, r) || unicode.Is(unicode.Katakana, r) ||
		unicode.Is(unicode.Hangul, r) || r >= 0x3000 && r <= 0x303f || r >= 0xff00 && r <= 0xffef
}

// Layout breaks the paragraph into lines width points wide.
func Layout(p *Para, width float32, fonts Fonts) *Laid {
	items := collect(p, fonts)
	l := &Laid{Para: p, Width: width, Count: len(items), fonts: fonts}
	left := p.Left
	avail := func(first bool) float32 {
		w := width - p.Left - p.Right
		if first {
			w -= p.First
		}
		return max(w, 1)
	}
	y := p.Before
	start := 0
	first := true
	for {
		end, hard := fit(p, items, start, avail(first), first)
		line := place(p, items, start, end, left, first, avail(first))
		line.Y = y
		l.setHeight(&line, items, start, end)
		l.Lines = append(l.Lines, line)
		y += line.H
		first = false
		if hard {
			start = end + 1 // the break itself sits at the end of its line
			if start >= len(items) {
				// A line break at the very end leaves a line to type on.
				if len(items) > 0 && items[len(items)-1].r == '\n' {
					empty := place(p, items, len(items), len(items), left, false, avail(false))
					empty.Y = y
					l.setHeight(&empty, items, len(items), len(items))
					l.Lines = append(l.Lines, empty)
					y += empty.H
				}
				break
			}
			continue
		}
		start = end
		if start >= len(items) {
			break
		}
	}
	if p.Align == Justify {
		for i := range l.Lines {
			hardEnd := l.Lines[i].End < len(items) && items[l.Lines[i].End].r == '\n'
			if i < len(l.Lines)-1 && !hardEnd {
				justify(&l.Lines[i], left+firstOf(p, i == 0), avail(i == 0))
			}
		}
	}
	if p.Bullet != "" && len(l.Lines) > 0 {
		s := p.BulletStyle
		if s.Size == 0 {
			s = firstStyle(p)
		}
		f := fonts.Font(s.Family, s.Bold, s.Italic)
		w := float32(0)
		for _, r := range p.Bullet {
			w += charWidths.of(f, r) * s.Size
		}
		l.Marker = &Frag{X: p.Left + p.First, W: w, Text: p.Bullet, Style: s, Size: s.Size}
	}
	l.Height = y + p.After
	return l
}

func firstOf(p *Para, first bool) float32 {
	if first {
		return p.First
	}
	return 0
}

// firstStyle is the style the paragraph starts in.
func firstStyle(p *Para) Style {
	for i := range p.Runs {
		if p.Runs[i].Inline == nil && p.Runs[i].Text != "" {
			return p.Runs[i].Style
		}
	}
	return p.Empty
}

// nextStop is the stop a tab at x goes to.
func nextStop(p *Para, x float32) TabStop {
	for _, t := range p.Stops {
		if t.Pos > x+0.01 {
			return t
		}
	}
	return TabStop{Pos: tabStop(p, x)}
}

// tabWidth is how far the tab at items[i], at x, moves on: a right or centred stop keeps room for the words that
// follow it, as far as the next tab or the end of the line. Words too wide for the room push on past the stop.
func tabWidth(p *Para, items []item, i int, x float32) (float32, TabStop) {
	t := nextStop(p, x)
	gap := t.Pos - x
	if t.Kind == TabLeft {
		return gap, t
	}
	after := float32(0)
	for j := i + 1; j < len(items); j++ {
		r := items[j].r
		if r == '\t' || r == '\n' || t.Kind == TabDecimal && (r == '.' || r == ',') {
			break
		}
		after += items[j].w
	}
	if t.Kind == TabCenter {
		after /= 2
	}
	return max(gap-after, 0), t
}

// tabStop is where a tab at x goes among the plain stops.
func tabStop(p *Para, x float32) float32 {
	for _, t := range p.Tabs {
		if t > x+0.01 {
			return t
		}
	}
	every := p.TabEvery
	if every <= 0 {
		every = 36
	}
	return (float32(math.Floor(float64(x/every))) + 1) * every
}

// fit finds where the line starting at start ends: its end, and whether a '\n' ended it.
func fit(p *Para, items []item, start int, width float32, first bool) (end int, hard bool) {
	x := p.Left + firstOf(p, first)
	line0 := x
	lastBreak := -1
	for i := start; i < len(items); i++ {
		it := &items[i]
		if it.r == '\n' {
			return i, true
		}
		w := it.w
		if it.r == '\t' {
			w = tabStop(p, x) - x
		}
		// Spaces hang past the end of a line rather than wrap it.
		if x-line0+w > width && it.r != ' ' && it.r != 0x3000 {
			if i == start {
				return start + 1, false // one thing wider than the line goes on it
			}
			if lastBreak > start {
				return lastBreak, false
			}
			return i, false
		}
		x += w
		if i+1 < len(items) && breakable(it.r, items[i+1].r) {
			lastBreak = i + 1
		}
	}
	return len(items), false
}

// place makes the line's pieces. The pieces split at each change of style and at tabs.
func place(p *Para, items []item, start, end int, left float32, first bool, width float32) Line {
	line := Line{Start: start, End: end}
	x := left + firstOf(p, first)
	i := start
	for i < end {
		it := &items[i]
		if it.r == '\t' {
			w, stop := tabWidth(p, items, i, x)
			line.Frags = append(line.Frags, Frag{X: x, W: w, Style: *it.style, Start: i, End: i + 1, Offsets: []float32{0, w}, Leader: stop.Leader})
			x += w
			i++
			continue
		}
		if it.inline != nil {
			line.Frags = append(line.Frags, Frag{X: x, W: it.w, Style: *it.style, Inline: it.inline, Start: i, End: i + 1, Offsets: []float32{0, it.w}})
			x += it.w
			i++
			continue
		}
		j := i
		var b strings.Builder
		f := Frag{X: x, Style: *it.style, Start: i, Baseline: it.shift, Size: it.size}
		f.Offsets = append(f.Offsets, 0)
		w := float32(0)
		for j < end && items[j].style == it.style && items[j].r != '\t' && items[j].inline == nil {
			b.WriteRune(items[j].r)
			w += items[j].w
			f.Offsets = append(f.Offsets, w)
			j++
		}
		f.Text, f.W, f.End = b.String(), w, j
		line.Frags = append(line.Frags, f)
		x += w
		i = j
	}
	line.Right = x
	// Trailing spaces hang past the edge and are not counted when a line is centred or set right.
	extra := width + left + firstOf(p, first) - trimmedRight(items, start, end, x)
	shift := float32(0)
	switch p.Align {
	case Center:
		shift = extra / 2
	case Right:
		shift = extra
	}
	if shift != 0 {
		for k := range line.Frags {
			line.Frags[k].X += shift
		}
		line.Right += shift
	}
	return line
}

func trimmedRight(items []item, start, end int, x float32) float32 {
	for i := end - 1; i >= start && (items[i].r == ' ' || items[i].r == 0x3000); i-- {
		x -= items[i].w
	}
	return x
}

// justify spreads the line's spaces, or, in a line with none, its CJK characters, to fill width.
func justify(line *Line, x0, width float32) {
	right := x0
	n := 0
	for k := range line.Frags {
		f := &line.Frags[k]
		text := strings.TrimRight(f.Text, " ")
		if k < len(line.Frags)-1 {
			text = f.Text
		}
		right = f.X + f.Offsets[utf8.RuneCountInString(text)]
		n += strings.Count(text, " ")
	}
	extra := x0 + width - right
	if extra <= 0 || extra > width/3 {
		return
	}
	cjk := n == 0
	if cjk {
		for k := range line.Frags {
			n += utf8.RuneCountInString(line.Frags[k].Text)
		}
		n--
	}
	if n <= 0 {
		return
	}
	gap := extra / float32(n)
	moved := float32(0)
	for k := range line.Frags {
		f := &line.Frags[k]
		f.X += moved
		added := float32(0)
		i := 0
		for _, r := range f.Text {
			f.Offsets[i] += added
			if r == ' ' || cjk {
				added += gap
			}
			i++
		}
		f.Offsets[i] += added
		f.W = f.Offsets[i]
		if cjk {
			f.Space = gap
		}
		moved += added
	}
}

func (l *Laid) setHeight(line *Line, items []item, start, end int) {
	p := l.Para
	var asc, desc float32
	for i := start; i < end; i++ {
		asc = max(asc, items[i].asc)
		desc = max(desc, items[i].desc)
	}
	if end < len(items) && items[end].r == '\n' {
		asc, desc = max(asc, items[end].asc), max(desc, items[end].desc)
	}
	if asc == 0 && desc == 0 {
		if start > 0 && len(items) > 0 {
			it := items[min(start, len(items))-1]
			asc, desc = it.asc, it.desc
		} else {
			s := p.Empty
			if s.Size == 0 {
				s.Size = 11
			}
			a, d := metrics(l.fonts.Font(s.Family, s.Bold, s.Italic))
			asc, desc = a*s.Size, d*s.Size
		}
	}
	natural := asc + desc
	h := natural
	switch p.Rule {
	case Multiple:
		if p.Line > 0 {
			h = natural * p.Line
		}
	case Exact:
		if p.Line > 0 {
			h = p.Line
		}
	case AtLeast:
		h = max(natural, p.Line)
	}
	line.H = h
	// Extra space goes above the words, as Word places it.
	line.Base = h - desc
	if p.Rule == Multiple && h < natural {
		line.Base = asc * h / natural
	}
}

// Place is a caret position in the paragraph.
type Place struct {
	Line    int
	X, Y, H float32 // from the top left of the paragraph's text area
}

// Caret is where the caret at place i sits. A place at the end of a wrapped line that is also the next line's
// start shows at the next line's start; trailing says to prefer the end of the earlier line.
func (l *Laid) Caret(i int, trailing bool) Place {
	for n := range l.Lines {
		line := &l.Lines[n]
		last := n == len(l.Lines)-1
		if i < line.Start || !last && i > line.End {
			continue
		}
		if i == line.End && !last && !trailing && l.Lines[n+1].Start == i {
			continue
		}
		return Place{Line: n, X: line.xOf(i, l), Y: line.Y, H: line.H}
	}
	line := &l.Lines[len(l.Lines)-1]
	return Place{Line: len(l.Lines) - 1, X: line.Right, Y: line.Y, H: line.H}
}

func (line *Line) xOf(i int, l *Laid) float32 {
	for k := range line.Frags {
		f := &line.Frags[k]
		if i >= f.Start && i < f.End {
			return f.X + f.Offsets[i-f.Start]
		}
	}
	if n := len(line.Frags); n > 0 {
		return line.Frags[n-1].X + line.Frags[n-1].W
	}
	p := l.Para
	x := p.Left
	if line.Start == 0 {
		x += p.First
	}
	if p.Align == Center {
		return x + (l.Width-p.Left-p.Right-firstOf(p, line.Start == 0))/2
	}
	if p.Align == Right {
		return l.Width - p.Right
	}
	return x
}

// Hit is the caret place nearest the point, and whether it is at the end of its line rather than the start of the
// next.
func (l *Laid) Hit(x, y float32) (place int, trailing bool) {
	n := len(l.Lines) - 1
	for i := range l.Lines {
		if y < l.Lines[i].Y+l.Lines[i].H {
			n = i
			break
		}
	}
	return l.HitLine(n, x)
}

// HitLine is the caret place nearest x on line n.
func (l *Laid) HitLine(n int, x float32) (place int, trailing bool) {
	line := &l.Lines[n]
	for k := range line.Frags {
		f := &line.Frags[k]
		if x >= f.X+f.W && k < len(line.Frags)-1 {
			continue
		}
		for i := 0; i+1 < len(f.Offsets); i++ {
			if x < f.X+(f.Offsets[i]+f.Offsets[i+1])/2 {
				return f.Start + i, false
			}
		}
		// Past the words: the end of the line, before a break or the next line's first character.
		return line.End, line.End < l.Count && n < len(l.Lines)-1 && l.Lines[n+1].Start == line.End
	}
	return line.End, false
}

// Rects are the boxes a selection from place a to b covers, in the paragraph's text area.
func (l *Laid) Rects(a, b int) []gfx.Rect {
	var out []gfx.Rect
	for n := range l.Lines {
		line := &l.Lines[n]
		if b < line.Start || a > line.End || a == b {
			continue
		}
		from, to := max(a, line.Start), min(b, line.End)
		x0, x1 := line.xOf(from, l), line.xOf(to, l)
		if to == line.End {
			x1 = line.Right
			if b > line.End {
				x1 += 4 // the break is chosen too
			}
		}
		if x1 > x0 {
			out = append(out, gfx.Rect{X: x0, Y: line.Y, W: x1 - x0, H: line.H})
		}
	}
	return out
}

// Pictures gives Draw the pictures of inline runs.
type Pictures func(key string) image.Image

// Draw paints the paragraph with its top-left at (x, y), at scale pixels a point. fg replaces the automatic colour
// (a Color with no alpha).
func (l *Laid) Draw(c *gfx.Canvas, x, y, scale float32, fonts Fonts, pics Pictures, fg color.RGBA) {
	l.DrawLines(c, 0, len(l.Lines), x, y, scale, fonts, pics, fg)
}

// DrawLines paints lines from up to to, each where Draw puts it: a paragraph that runs over a page is drawn in parts.
func (l *Laid) DrawLines(c *gfx.Canvas, from, to int, x, y, scale float32, fonts Fonts, pics Pictures, fg color.RGBA) {
	clip := c.Clip()
	if l.Marker != nil && len(l.Lines) > 0 && from == 0 {
		m := l.Marker
		top := y + (l.Lines[0].Y+l.Lines[0].Base)*scale
		drawText(c, m, x+m.X*scale, top, scale, fonts, fg)
	}
	for n := from; n < to && n < len(l.Lines); n++ {
		line := &l.Lines[n]
		top := y + line.Y*scale
		if int(top) > clip.Max.Y || int(top+line.H*scale)+1 < clip.Min.Y {
			continue
		}
		base := top + line.Base*scale
		for k := range line.Frags {
			f := &line.Frags[k]
			fx := x + f.X*scale
			if f.Style.Highlight.A != 0 {
				c.Fill(gfx.Rect{X: fx, Y: top, W: f.W * scale, H: line.H * scale}, f.Style.Highlight)
			}
			if f.Inline != nil {
				if pics != nil {
					if img := pics(f.Inline.Key); img != nil {
						c.Image(img, gfx.Rect{X: fx, Y: base - f.Inline.H*scale, W: f.Inline.W * scale, H: f.Inline.H * scale})
					}
				}
				continue
			}
			if f.Text != "" {
				drawText(c, f, fx, base, scale, fonts, fg)
			} else if f.Leader != 0 {
				drawLeader(c, f, fx, base, scale, fonts, fg)
			}
			col := f.Style.Color
			if col.A == 0 {
				col = fg
			}
			lineW := max(1, f.Style.Size*scale/16)
			if f.Style.Underline {
				c.Fill(gfx.Rect{X: fx, Y: base + f.Style.Size*scale*0.12, W: f.W * scale, H: lineW}, col)
			}
			if f.Style.Strike {
				c.Fill(gfx.Rect{X: fx, Y: base - f.Style.Size*scale*0.28, W: f.W * scale, H: lineW}, col)
			}
		}
	}
}

// drawLeader fills a tab's gap with its character, on a grid of the character's own width.
func drawLeader(c *gfx.Canvas, f *Frag, x, base, scale float32, fonts Fonts, fg color.RGBA) {
	s := &f.Style
	font := fonts.Font(s.Family, s.Bold, s.Italic)
	col := s.Color
	if col.A == 0 {
		col = fg
	}
	step := charWidths.of(font, f.Leader) * s.Size * scale
	if step < 1 {
		return
	}
	face := font.Face(s.Size * scale)
	text := string(f.Leader)
	for at := float32(math.Ceil(float64(x/step))) * step; at+step <= x+f.W*scale; at += step {
		c.Text(face, at, base-face.Ascent, text, col)
	}
}

// drawText draws a piece on its baseline. It goes word by word at the laid-out positions, so that a line's words
// land where the layout put them at any zoom, however the glyphs round.
func drawText(c *gfx.Canvas, f *Frag, x, base, scale float32, fonts Fonts, fg color.RGBA) {
	s := &f.Style
	size := f.Size
	if size == 0 {
		size = s.Size
	}
	col := s.Color
	if col.A == 0 {
		col = fg
	}
	face := fonts.Font(s.Family, s.Bold, s.Italic).Face(size * scale)
	top := base - f.Baseline*scale - face.Ascent
	if f.Offsets == nil {
		c.Text(face, x, top, f.Text, col)
		return
	}
	rs := []rune(f.Text)
	start := 0
	for i := 1; i <= len(rs); i++ {
		if i == len(rs) || rs[i-1] == ' ' || wide(rs[i-1]) || wide(rs[i]) {
			c.Text(face, x+f.Offsets[start]*scale, top, string(rs[start:i]), col)
			start = i
		}
	}
}
