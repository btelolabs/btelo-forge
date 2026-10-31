package docx

import (
	"errors"
	"image/color"
	"sort"

	"github.com/btelolabs/btelo-forge/internal/gfx"
)

// Finding places: from a caret place to a page and back, and the moves the keyboard makes.

// Spot is where a caret stands on a page.
type Spot struct {
	Page    int
	X, Y, H float32
}

// pieceOf finds the piece of the paragraph holding line n on a page, and where it is.
func (pg *Page) pieceOf(b *Block, line int) *Piece {
	var last *Piece
	for i := range pg.Pieces {
		p := &pg.Pieces[i]
		if p.B != b {
			continue
		}
		if line >= p.From && line < p.To {
			return p
		}
		last = p
	}
	if last != nil && line >= last.To && line == len(last.L.Lines)-1 && last.To == line {
		return last // the empty line after a page break at the paragraph's end is not shown
	}
	return nil
}

// pagesOf is the range of pages a top-level block may be on.
func (d *Doc) pagesOf(top *Block) (from, to int) {
	pages := d.lay.pages
	n := sort.Search(len(pages), func(i int) bool {
		b := pages[i].start.b
		return b == nil || b.Index > top.Index
	})
	from = max(n-1, 0)
	return from, len(pages)
}

// Locate is where a place is, as far as pages are laid out. It gives ok false for a place not reached yet.
func (d *Doc) Locate(p Pos) (Spot, bool) {
	if p.B == nil {
		return Spot{}, false
	}
	st := p.B.In.Story
	l := p.B.para().laid
	if st != d.Body {
		return d.locateIn(st, p)
	}
	if l == nil {
		return Spot{}, false
	}
	pl := l.Caret(p.Off, p.Trail)
	top := p.B.top()
	from, to := d.pagesOf(top)
	for i := from; i < to; i++ {
		pg := d.lay.pages[i]
		if s := pg.start.b; s != nil && s.Index > top.Index {
			break
		}
		if pc := pg.pieceOf(p.B, pl.Line); pc != nil {
			y := pc.Y + pl.Y - l.Lines[pc.From].Y
			if pl.Line >= pc.To {
				last := &l.Lines[pc.To-1]
				return Spot{Page: i, X: pc.X + last.Right, Y: pc.Y + last.Y - l.Lines[pc.From].Y, H: last.H}, true
			}
			return Spot{Page: i, X: pc.X + pl.X, Y: y, H: pl.H}, true
		}
	}
	return Spot{}, false
}

// locateIn finds a place in a header or footer, on the page being edited.
func (d *Doc) locateIn(st *Story, p Pos) (Spot, bool) {
	if d.EditPage >= len(d.lay.pages) {
		return Spot{}, false
	}
	pg := d.lay.pages[d.EditPage]
	s := pg.Sect
	stack := d.storyStack(st, s.TextW())
	ox, oy := s.ML, s.Hdr
	if st.Part.X.Root.Name == "w:ftr" {
		oy = pg.H - s.Ftr - stack.H
	}
	l := p.B.para().laid
	if l == nil {
		return Spot{}, false
	}
	pl := l.Caret(p.Off, p.Trail)
	for i := range stack.Pieces {
		pc := &stack.Pieces[i]
		if pc.B == p.B {
			return Spot{Page: d.EditPage, X: ox + pc.X + pl.X, Y: oy + pc.Y + pl.Y, H: pl.H}, true
		}
	}
	return Spot{}, false
}

// reach lays out until a place's page is known.
func (d *Doc) reach(p Pos) (Spot, bool) {
	if s, ok := d.Locate(p); ok && d.lay.f == nil {
		return s, true
	}
	for {
		s, ok := d.Locate(p)
		if ok {
			return s, true
		}
		if d.Done() {
			return s, false
		}
		d.Layout(5e6)
		if d.Done() {
			return d.Locate(p)
		}
	}
}

// Reach lays out until the caret's page is known and returns it.
func (d *Doc) Reach(p Pos) (Spot, bool) { return d.reach(p) }

// Hit is the caret place nearest a point on a page, in points.
func (d *Doc) Hit(pg *Page, x, y float32) (Pos, bool) {
	best, bestLine := -1, 0
	bestD := float32(1e18)
	for i := range pg.Pieces {
		pc := &pg.Pieces[i]
		l := pc.L
		dy := pc.Y - l.Lines[pc.From].Y
		for k := pc.From; k < pc.To; k++ {
			line := &l.Lines[k]
			top, bottom := dy+line.Y, dy+line.Y+line.H
			var dist float32
			switch {
			case y < top:
				dist = (top - y) * 2
			case y > bottom:
				dist = (y - bottom) * 2
			}
			left, right := pc.X, pc.X+l.Width
			switch {
			case x < left:
				dist += left - x
			case x > right:
				dist += x - right
			}
			if dist < bestD {
				best, bestLine, bestD = i, k, dist
			}
		}
	}
	if best < 0 {
		return Pos{}, false
	}
	pc := &pg.Pieces[best]
	off, trail := pc.L.HitLine(bestLine, x-pc.X)
	return Pos{B: pc.B, Off: off, Trail: trail}, true
}

// HitStory is the place nearest a point in a page's header or footer.
func (d *Doc) HitStory(pg *Page, st *Story, x, y float32) (Pos, bool) {
	s := pg.Sect
	stack := d.storyStack(st, s.TextW())
	oy := s.Hdr
	if st.Part.X.Root.Name == "w:ftr" {
		oy = pg.H - s.Ftr - stack.H
	}
	var view Page
	view.Stack = *stack
	return d.Hit(&view, x-s.ML, y-oy)
}

// next and prev are the neighbouring places in reading order.
func (d *Doc) next(p Pos) Pos {
	if p.Off < p.B.para().Len {
		return Pos{B: p.B, Off: p.Off + 1}
	}
	flat := p.B.In.Story.flat
	if p.B.Flat+1 < len(flat) {
		return Pos{B: flat[p.B.Flat+1]}
	}
	return p
}

func (d *Doc) prev(p Pos) Pos {
	if p.Off > 0 {
		return Pos{B: p.B, Off: p.Off - 1}
	}
	flat := p.B.In.Story.flat
	if p.B.Flat > 0 {
		b := flat[p.B.Flat-1]
		return Pos{B: b, Off: b.para().Len}
	}
	return p
}

// MoveChar moves the caret a place on or back.
func (d *Doc) MoveChar(forward, extend bool) {
	if !extend && !d.Sel.Empty() {
		a, c := d.Sel.Range()
		if forward {
			d.SetCaret(c, false)
		} else {
			d.SetCaret(a, false)
		}
		return
	}
	if forward {
		d.SetCaret(d.next(d.Sel.Caret), extend)
	} else {
		d.SetCaret(d.prev(d.Sel.Caret), extend)
	}
}

// MoveWord moves the caret by a word.
func (d *Doc) MoveWord(forward, extend bool) {
	c := d.Sel.Caret
	if forward {
		if c.Off >= c.B.para().Len {
			d.SetCaret(d.next(c), extend)
			return
		}
		d.SetCaret(Pos{B: c.B, Off: wordEnd(c.B, c.Off)}, extend)
		return
	}
	if c.Off == 0 {
		d.SetCaret(d.prev(c), extend)
		return
	}
	d.SetCaret(Pos{B: c.B, Off: wordStart(c.B, c.Off)}, extend)
}

// MoveLine goes to the start or end of the caret's line.
func (d *Doc) MoveLine(end, extend bool) {
	c := d.Sel.Caret
	l := c.B.para().laid
	if l == nil {
		return
	}
	line := &l.Lines[l.Caret(c.Off, c.Trail).Line]
	if end {
		off := line.End
		trail := line.End < l.Count && l.Lines[len(l.Lines)-1].Start != line.Start
		if line.End >= c.B.P.Len {
			off, trail = c.B.P.Len, false
		}
		d.SetCaret(Pos{B: c.B, Off: off, Trail: trail}, extend)
		return
	}
	d.SetCaret(Pos{B: c.B, Off: line.Start}, extend)
}

// MoveDoc goes to the start or end of the story.
func (d *Doc) MoveDoc(end, extend bool) {
	flat := d.Sel.Caret.B.In.Story.flat
	if end {
		b := flat[len(flat)-1]
		d.SetCaret(Pos{B: b, Off: b.para().Len}, extend)
		return
	}
	d.SetCaret(Pos{B: flat[0]}, extend)
}

// lineAt is one laid out line on a page.
type lineAt struct {
	p       *Piece
	n       int
	y, h, x float32
	w       float32
}

func (pg *Page) lines() []lineAt {
	var out []lineAt
	for i := range pg.Pieces {
		pc := &pg.Pieces[i]
		base := pc.Y - pc.L.Lines[pc.From].Y
		for k := pc.From; k < pc.To; k++ {
			line := &pc.L.Lines[k]
			out = append(out, lineAt{p: pc, n: k, y: base + line.Y, h: line.H, x: pc.X, w: pc.L.Width})
		}
	}
	return out
}

// MoveVert moves the caret a line up or down, keeping toward column x (in points on the page; pass a negative value to
// start from the caret), and returns the column. Geometry decides, so that it goes down a table's column.
func (d *Doc) MoveVert(down, extend bool, x float32) float32 {
	c := d.Sel.Caret
	if c.B.In.Story != d.Body {
		d.moveVertIn(down, extend)
		return x
	}
	s, ok := d.reach(c)
	if !ok {
		return x
	}
	if x < 0 {
		x = s.X
	}
	for page := s.Page; page >= 0 && page < len(d.lay.pages) && page < s.Page+3 && page > s.Page-3; {
		pg := d.lay.pages[page]
		best := -1
		lines := pg.lines()
		bestScore := float32(1e18)
		for i := range lines {
			ln := &lines[i]
			var dy float32
			if page == s.Page {
				if down {
					dy = ln.y - (s.Y + s.H/2)
				} else {
					dy = (s.Y + s.H/2) - (ln.y + ln.h)
				}
				if dy <= 0 {
					continue
				}
			} else if down {
				dy = ln.y
			} else {
				dy = pg.H - ln.y - ln.h
			}
			dx := float32(0)
			if x < ln.x {
				dx = ln.x - x
			} else if x > ln.x+ln.w {
				dx = x - ln.x - ln.w
			}
			if score := dy + dx*4; score < bestScore {
				best, bestScore = i, score
			}
		}
		if best >= 0 {
			ln := &lines[best]
			off, trail := ln.p.L.HitLine(ln.n, x-ln.p.X)
			d.SetCaret(Pos{B: ln.p.B, Off: off, Trail: trail}, extend)
			return x
		}
		if down {
			page++
			if page >= len(d.lay.pages) && !d.Done() {
				d.Layout(5e6)
			}
		} else {
			page--
		}
	}
	return x
}

// moveVertIn moves through a header or footer's lines.
func (d *Doc) moveVertIn(down, extend bool) {
	c := d.Sel.Caret
	l := c.B.para().laid
	if l == nil {
		return
	}
	pl := l.Caret(c.Off, c.Trail)
	if down && pl.Line+1 < len(l.Lines) {
		off, t := l.HitLine(pl.Line+1, pl.X)
		d.SetCaret(Pos{B: c.B, Off: off, Trail: t}, extend)
		return
	}
	if !down && pl.Line > 0 {
		off, t := l.HitLine(pl.Line-1, pl.X)
		d.SetCaret(Pos{B: c.B, Off: off, Trail: t}, extend)
		return
	}
	flat := c.B.In.Story.flat
	if down && c.B.Flat+1 < len(flat) {
		d.SetCaret(Pos{B: flat[c.B.Flat+1]}, extend)
	} else if !down && c.B.Flat > 0 {
		b := flat[c.B.Flat-1]
		d.SetCaret(Pos{B: b, Off: b.para().Len}, extend)
	}
}

// PageY moves the caret to the place nearest a point of the view, for Page Up and Page Down.
func (d *Doc) MoveTo(page int, x, y float32, extend bool) {
	if page < 0 || page >= len(d.lay.pages) {
		return
	}
	if p, ok := d.Hit(d.lay.pages[page], x, y); ok {
		d.SetCaret(p, extend)
	}
}

// SelectAll chooses the whole story.
func (d *Doc) SelectAll() {
	flat := d.Sel.Caret.B.In.Story.flat
	last := flat[len(flat)-1]
	d.Sel = Selection{Anchor: Pos{B: flat[0]}, Caret: Pos{B: last, Off: last.para().Len}}
}

// SelectParagraph chooses the paragraph at a place.
func (d *Doc) SelectParagraph(p Pos) {
	d.Sel = Selection{Anchor: Pos{B: p.B}, Caret: Pos{B: p.B, Off: p.B.para().Len}}
}

// SelectWord chooses the word at a place.
func (d *Doc) SelectWord(p Pos) {
	a, c := WordAround(p.B, p.Off)
	d.Sel = Selection{Anchor: Pos{B: p.B, Off: a}, Caret: Pos{B: p.B, Off: c}}
}

// SelectionRects calls fn with the boxes of the selection on a page, in points.
func (d *Doc) SelectionRects(pg *Page, fn func(gfx.Rect)) {
	if d.Sel.Empty() {
		return
	}
	a, c := d.Sel.Range()
	if a.B.In.Story != d.Body {
		d.storyRects(pg, a, c, fn)
		return
	}
	for i := range pg.Pieces {
		pc := &pg.Pieces[i]
		if pc.B.Flat < a.B.Flat || pc.B.Flat > c.B.Flat {
			continue
		}
		pieceRects(pc, a, c, 0, 0, fn)
	}
}

func pieceRects(pc *Piece, a, c Pos, ox, oy float32, fn func(gfx.Rect)) {
	l := pc.L
	from, to := 0, l.Count+1
	if pc.B == a.B {
		from = a.Off
	}
	if pc.B == c.B {
		to = c.Off
	}
	base := pc.Y - l.Lines[pc.From].Y
	top, bottom := l.Lines[pc.From].Y, l.Lines[pc.To-1].Y+l.Lines[pc.To-1].H
	for _, r := range l.Rects(from, to) {
		if r.Y < top-0.01 || r.Y >= bottom {
			continue
		}
		fn(gfx.Rect{X: ox + pc.X + r.X, Y: oy + base + r.Y, W: r.W, H: r.H})
	}
}

func (d *Doc) storyRects(pg *Page, a, c Pos, fn func(gfx.Rect)) {
	if pg.Index != d.EditPage {
		return
	}
	st := a.B.In.Story
	s := pg.Sect
	stack := d.storyStack(st, s.TextW())
	ox, oy := s.ML, s.Hdr
	if st.Part.X.Root.Name == "w:ftr" {
		oy = pg.H - s.Ftr - stack.H
	}
	for i := range stack.Pieces {
		pc := &stack.Pieces[i]
		if pc.B.Flat >= a.B.Flat && pc.B.Flat <= c.B.Flat {
			pieceRects(pc, a, c, ox, oy, fn)
		}
	}
}

// RangeRects calls fn with the boxes of a stretch of one story on a page, in points.
func (d *Doc) RangeRects(pg *Page, a, c Pos, fn func(gfx.Rect)) {
	if a.B.In.Story != d.Body {
		return
	}
	for i := range pg.Pieces {
		pc := &pg.Pieces[i]
		if pc.B.Flat >= a.B.Flat && pc.B.Flat <= c.B.Flat {
			pieceRects(pc, a, c, 0, 0, fn)
		}
	}
}

// Before reports that place a comes before place c.
func (d *Doc) Before(a, c Pos) bool { return less(a, c) }

// ParaLen is how many places a paragraph has.
func (d *Doc) ParaLen(b *Block) int { return b.para().Len }

// PaintStamp changes when something every page may show changes.
func (d *Doc) PaintStamp() uint64 {
	v := d.lay.stories
	if b := d.Sel.Caret.B; b != nil && b.In.Story != d.Body {
		v++ // a header being typed in shows its file's fields
	}
	return v
}

// TouchHeaders has the pages paint their headers again.
func (d *Doc) TouchHeaders() { d.lay.stories += 2 }

// HeaderLine is the height on a page of the line between a header or footer and the body, and which of the two st is.
func (d *Doc) HeaderLine(pg *Page, st *Story) (float32, string) {
	s := pg.Sect
	stack := d.storyStack(st, s.TextW())
	if st.Part.X.Root.Name == "w:ftr" {
		return pg.H - s.Ftr - stack.H - 4, "footer"
	}
	return s.Hdr + stack.H + 4, "header"
}

// InHeaderBand reports that height y on a page is on the side of the line where st is.
func (d *Doc) InHeaderBand(pg *Page, st *Story, y float32) bool {
	line, name := d.HeaderLine(pg, st)
	if name == "footer" {
		return y >= line
	}
	return y <= line
}

// AtListStart reports that the caret is at the start of a list item.
func (d *Doc) AtListStart() bool {
	c := d.Sel.Caret
	return d.Sel.Empty() && c.Off == 0 && c.B.para().PP.NumID != 0
}

// CaretIsEastAsian reports that the character at the caret is Chinese, Japanese or Korean.
func (d *Doc) CaretIsEastAsian() bool {
	c := d.Sel.Caret
	if c.B == nil {
		return false
	}
	left, right := c.B.para().segAt(c.Off)
	s := left
	if s == nil {
		s = right
	}
	if s == nil || s.Kind != SegText {
		return false
	}
	for _, r := range s.S {
		return eastAsian(r)
	}
	return false
}

// HighlightColor is the colour of a highlight's name.
func HighlightColor(name string) color.RGBA { return highlightColor(name) }

// ParaOf is a paragraph's resolved data.
func (d *Doc) ParaOf(b *Block) *Para { return b.para() }

// Sections are the document's sections.
func (d *Doc) Sections() []*Section { return d.sects }

// Current is a paragraph's block as the tree now has it.
func (d *Doc) Current(b *Block) *Block { return d.current(Pos{B: b}).B }

// Prev is the paragraph before b.
func (d *Doc) Prev(b *Block) *Block {
	flat := b.In.Story.flat
	return flat[max(b.Flat-1, 0)]
}

// CurrentTable is a table's block as the tree now has it, nil when it is gone.
func (d *Doc) CurrentTable(b *Block) *Table {
	if n := b.In.Story.byNode[b.Node]; n != nil {
		return n.T
	}
	return nil
}

// FirstParagraph is the index of a table's first paragraph in its story.
func (t *Table) FirstParagraph() int { return t.Block.firstPara().Flat }

// CellAt is the cell of a table at a row and grid column.
func CellAt(t *Table, row, col int) *Cell {
	if row < 0 || row >= len(t.Rows) {
		return nil
	}
	return cellCovering(t.Rows[row], col)
}

// MergeRange joins rows r0 to r1 of grid columns c0 up to c1.
func (d *Doc) MergeRange(t *Table, r0, r1, c0, c1 int) error {
	if r1 < r0 || r1 >= len(t.Rows) || c0 < 0 || c1 > len(t.Grid) || c1 <= c0 {
		return errors.New("those cells are not in the table")
	}
	return d.mergeRect(t, r0, r1, c0, c1)
}
