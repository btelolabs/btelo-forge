package docx

import (
	"image/color"
	"strconv"
	"time"

	"github.com/btelolabs/btelo-forge/internal/gfx"
	"github.com/btelolabs/btelo-forge/internal/office/flow"
	"github.com/btelolabs/btelo-forge/internal/office/ox"
)

// Pagination. The body is poured into pages block by block; a page remembers where it started, so after an edit the
// pour starts again from the page before the change, and stops as soon as a new page starts at the same place an old
// one did: the pages after that are kept whole. A long document is poured a slice of time at a time.

// Piece is lines of a paragraph at a place.
type Piece struct {
	B        *Block
	L        *flow.Laid
	From, To int     // the lines shown
	X, Y     float32 // the text area's left, and the top of line From, on the page
}

// Decor is a fill or a line under the words.
type Decor struct {
	Rect gfx.Rect
	Col  color.RGBA
	Line bool
	Kind string // a line's style
}

// Stack is things placed one under the other, with the page as the top-left or a cell's corner.
type Stack struct {
	Pieces []Piece
	Decor  []Decor
	H      float32
}

// breakAt is the bottom of the last line that ends at or above y, the place to cut a stack.
func (s *Stack) breakAt(y float32) float32 {
	best := float32(0)
	for i := range s.Pieces {
		p := &s.Pieces[i]
		top := p.Y - p.L.Lines[p.From].Y
		for k := p.From; k < p.To; k++ {
			line := &p.L.Lines[k]
			if bottom := top + line.Y + line.H; bottom <= y+0.01 && bottom > best {
				best = bottom
			}
		}
	}
	return best
}

// emit copies what lies between from and to into s, moved by (dx, dy).
func (st *Stack) emit(s *Stack, from, to, dx, dy float32) {
	for i := range st.Pieces {
		p := st.Pieces[i]
		top := p.Y - p.L.Lines[p.From].Y
		a, b := -1, -1
		for k := p.From; k < p.To; k++ {
			line := &p.L.Lines[k]
			if top+line.Y >= from-0.01 && top+line.Y+line.H <= to+0.01 {
				if a < 0 {
					a = k
				}
				b = k + 1
			}
		}
		if a < 0 {
			continue
		}
		p.Y = top + p.L.Lines[a].Y + dy
		p.X += dx
		p.From, p.To = a, b
		s.Pieces = append(s.Pieces, p)
	}
	for _, e := range st.Decor {
		y0, y1 := max(e.Rect.Y, from), min(e.Rect.Y+e.Rect.H, to)
		if y1 <= y0 && e.Rect.H > 0 {
			continue
		}
		e.Rect.X += dx
		e.Rect.Y, e.Rect.H = y0+dy, y1-y0
		s.Decor = append(s.Decor, e)
	}
}

// Placed is a floating thing on a page.
type Placed struct {
	A    *Anchor
	Rect gfx.Rect // on the page
}

// cursor is a place in the body, where a page starts.
type cursor struct {
	b     *Block
	line  int       // the paragraph's line
	row   int       // the table's row
	offs  []float32 // how far a table's columns have been shown
	sect  *Section
	hard  bool // the page was started on purpose, so space above its first paragraph stays
	first bool // it is the first page of its section
}

func (c *cursor) same(o *cursor) bool {
	if c.b != o.b || c.line != o.line || c.row != o.row || c.sect != o.sect {
		return false
	}
	for i := range c.offs {
		v := float32(0)
		if i < len(o.offs) {
			v = o.offs[i]
		}
		if c.offs[i] != v {
			return false
		}
	}
	return true
}

// Page is a laid out page.
type Page struct {
	Stack
	Index   int
	Number  int
	Total   int
	W, H    float32
	Sect    *Section
	First   bool // the first page of its section
	Anchors []Placed
	start   cursor
	Rev     uint64 // changes when what is on the page changes
	head    *headFoot
	foot    *headFoot
	Top     float32 // where the body starts, after a tall header
	Bottom  float32
}

// paginator is the state of the pouring.
type paginator struct {
	pages      []*Page
	f          *flower
	old        []*Page // pages after the one being made, which may still be kept
	dirtyTops  []dirtyMark
	all        bool
	numStale   bool
	sectsStale bool
	sectsNew   bool // sections' own properties changed: all are read again
	highest    int  // the last changed block, past which old pages may be kept
	highRow    int  // and in it the last changed row
	seq        uint64
	stories    uint64 // counts changes to headers and footers, which all pages show
}

// dirtyMark is a changed top-level block and, in a table, the row.
type dirtyMark struct {
	b   *Block
	row int // -1 for all of it
}

// dirty notes that a paragraph changed.
func (p *paginator) dirty(b *Block) {
	if b.T == nil {
		b.para().laid = nil
	}
	row := -1
	for b.In.Cell != nil {
		c := b.In.Cell
		c.stack = nil
		c.Origin.stack = nil
		t := c.Row.Table
		t.cache = nil
		row = c.Row.Index
		if c.Origin != c {
			row = c.Origin.Row.Index
		}
		b = t.Block
	}
	if b.In.Story.Part != b.In.Story.d.Main {
		b.In.Story.head = nil
		p.stories++
		return
	}
	p.dirtyTops = append(p.dirtyTops, dirtyMark{b, row})
}

// restart pours the whole document again.
func (p *paginator) restart() { p.all = true }

// Pages are the pages laid out so far.
func (d *Doc) Pages() []*Page { return d.lay.pages }

// Done reports that every page is laid out.
func (d *Doc) Done() bool {
	p := &d.lay
	return p.f == nil && !p.all && len(p.dirtyTops) == 0
}

// Layout pours for at most budget, nothing if the layout is up to date, and says whether it finished.
func (d *Doc) Layout(budget time.Duration) bool {
	d.rebuild()
	p := &d.lay
	if p.all || len(p.dirtyTops) > 0 {
		d.beginPour()
	}
	if p.f == nil {
		return true
	}
	deadline := time.Now().Add(budget)
	blocks := d.Body.Root.Blocks
	for n := 0; p.f != nil; n++ {
		if p.f.step(blocks) {
			d.finishPour()
			return true
		}
		if budget > 0 && n%8 == 7 && time.Now().After(deadline) {
			return false
		}
	}
	return true
}

// alive reports whether the block is still in the body where it says.
func (d *Doc) alive(b *Block) bool {
	bs := d.Body.Root.Blocks
	return b != nil && b.Index >= 0 && b.Index < len(bs) && bs[b.Index] == b
}

// beginPour works out where to pour from.
func (d *Doc) beginPour() {
	p := &d.lay
	lowest, highest := -1, -1
	lowRow, highRow := -1, 1<<30
	for _, m := range p.dirtyTops {
		i := min(m.b.Index, len(d.Body.Root.Blocks))
		if lowest < 0 || i < lowest {
			lowest, lowRow = i, m.row
		} else if i == lowest && m.row >= 0 && lowRow >= 0 {
			lowRow = min(lowRow, m.row)
		} else if i == lowest {
			lowRow = -1
		}
		switch {
		case i > highest:
			highest, highRow = i, 1<<30
			if m.row >= 0 {
				highRow = m.row
			}
		case i == highest && m.row >= 0 && highRow != 1<<30:
			highRow = max(highRow, m.row)
		case i == highest:
			highRow = 1 << 30
		}
	}
	p.dirtyTops = p.dirtyTops[:0]
	if p.all {
		lowest, highest, lowRow, highRow = 0, len(d.Body.Root.Blocks), -1, 1<<30
	}
	p.all = false
	if p.f != nil && lowest >= p.f.i && !p.f.started(lowest) {
		if highest > p.highest {
			p.highest, p.highRow = highest, highRow
		}
		return // the pour has not got there yet
	}
	keep := len(p.pages)
	if p.f != nil {
		keep-- // the page being made goes in any case
	}
	// The page before the one with the change: a change can pull lines back over the edge.
	k := 0
	for i := 0; i < keep; i++ {
		s := &p.pages[i].start
		if !d.alive(s.b) {
			break
		}
		if s.b.Index > lowest || s.b.Index == lowest && (lowRow < 0 || s.row > lowRow) {
			break
		}
		k = i
	}
	if lowest == 0 {
		k = 0
	}
	if k > 0 {
		k--
	}
	var tail []*Page
	if k < len(p.pages) {
		tail = append(tail, p.pages[k:]...)
	}
	if p.f != nil && len(tail) > 0 {
		tail = tail[:len(tail)-1] // the page being made is not whole
	}
	tail = append(tail, p.old...)
	var start cursor
	if k < len(p.pages) && lowest != 0 {
		start = p.pages[k].start
	}
	p.pages = p.pages[:k]
	if !d.alive(start.b) || lowest == 0 {
		start = cursor{}
		p.pages = p.pages[:0]
	}
	p.old = tail
	p.highest, p.highRow = highest, highRow
	p.f = newFlower(d, start)
}

// finishPour numbers the pages.
func (d *Doc) finishPour() {
	p := &d.lay
	p.f = nil
	p.old = nil
	number := 0
	var sect *Section
	for i, pg := range p.pages {
		pg.Index = i
		number++
		if pg.First && pg.Sect != sect && pg.Sect.PageStart >= 0 {
			number = pg.Sect.PageStart
		}
		sect = pg.Sect
		pg.Number = number
		pg.Total = len(p.pages)
	}
}

// Finish lays out everything, for tools that need the whole document.
func (d *Doc) Finish() {
	for !d.Layout(0) {
	}
}

// flower pours blocks into pages.
type flower struct {
	d      *Doc
	i      int // the block being placed
	line   int
	row    int
	offs   []float32
	sect   *Section
	page   *Page
	col    int
	x, w   float32 // the column
	top    float32 // the top of the columns of this section on this page
	y      float32
	bottom float32
	maxY   float32 // the lowest point of the section's earlier columns
	empty  bool    // nothing is in this column yet
	fresh  bool    // the column's top came by itself: space above is dropped
	prev   *Block
	pages  int
}

func newFlower(d *Doc, c cursor) *flower {
	f := &flower{d: d}
	if c.b != nil {
		f.i, f.line, f.row, f.sect = c.b.Index, c.line, c.row, c.sect
		f.offs = append([]float32(nil), c.offs...)
	} else {
		f.sect = d.sects[0]
	}
	f.newPage(c.first || c.b == nil, c.hard)
	return f
}

// started reports whether block i has been given a place already.
func (f *flower) started(i int) bool { return i == f.i && (f.line > 0 || f.row > 0) }

// cursorHere is the place the pour is at.
func (f *flower) cursorHere(hard bool) cursor {
	c := cursor{line: f.line, row: f.row, sect: f.sect, hard: hard}
	blocks := f.d.Body.Root.Blocks
	if f.i < len(blocks) {
		c.b = blocks[f.i]
	}
	if f.offs != nil {
		c.offs = append([]float32(nil), f.offs...)
	}
	return c
}

// newPage starts a page. first says it is the first of its section.
func (f *flower) newPage(first, hard bool) {
	s := f.sect
	d := f.d
	d.lay.seq++
	pg := &Page{W: s.W, H: s.H, Sect: s, First: first, Rev: d.lay.seq, Index: len(d.lay.pages), start: f.cursorHere(hard)}
	pg.Number = pg.Index + 1
	pg.start.first = first
	pg.Top, pg.Bottom = s.MT, s.H-s.MB
	f.fixMargins(pg)
	d.lay.pages = append(d.lay.pages, pg)
	f.page = pg
	f.top = pg.Top
	f.startColumn(0, !hard)
}

// fixMargins moves the body down or up for a header or footer taller than the margin.
func (f *flower) fixMargins(pg *Page) {
	d := f.d
	s := pg.Sect
	even := d.evenOdd && (pg.Number%2 == 0)
	if st := s.story("header", pg.First && s.TitlePg, even); st != nil {
		if h := d.storyStack(st, s.TextW()).H; s.Hdr+h > pg.Top && !s.ExactTop {
			pg.Top = s.Hdr + h
		}
	}
	if st := s.story("footer", pg.First && s.TitlePg, even); st != nil {
		if h := d.storyStack(st, s.TextW()).H; s.H-s.Ftr-h < pg.Bottom && !s.ExactBottom {
			pg.Bottom = s.H - s.Ftr - h
		}
	}
	if pg.Bottom-pg.Top < 36 {
		pg.Bottom = pg.Top + 36
	}
}

func (f *flower) startColumn(col int, natural bool) {
	s := f.sect
	f.col = col
	f.x, f.w = s.colAt(col)
	f.y, f.bottom = f.top, f.page.Bottom
	f.empty, f.fresh = true, natural
}

// nextColumn goes on in the next column, or on a new page; the caller has set the place to go on from.
func (f *flower) nextColumn(hard bool) {
	if f.col+1 < f.sect.Cols {
		f.maxY = max(f.maxY, f.y)
		f.startColumn(f.col+1, !hard)
		return
	}
	f.maxY = 0
	f.pageEnd()
	f.newPage(false, hard)
	f.spliceOld()
}

// pageEnd closes the page being made.
func (f *flower) pageEnd() {
	f.d.lay.seq++
	f.page.Rev = f.d.lay.seq
}

// spliceOld keeps the old pages when the page just started begins where one of them did: nothing further can differ,
// as no changed block lies after this point.
func (f *flower) spliceOld() {
	p := &f.d.lay
	here := &f.page.start
	if len(p.old) == 0 || len(p.dirtyTops) > 0 || here.b == nil || here.b.Index < p.highest || here.b.Index == p.highest && here.row <= p.highRow {
		return
	}
	for len(p.old) > 0 {
		s := &p.old[0].start
		if !f.d.alive(s.b) {
			p.old = p.old[1:]
			continue
		}
		if here.b == nil || s.b.Index > here.b.Index {
			return
		}
		if s.b == here.b && s.same(here) {
			break
		}
		p.old = p.old[1:]
	}
	if len(p.old) == 0 {
		return
	}
	p.pages = append(p.pages[:len(p.pages)-1], p.old...)
	f.done()
}

// done ends the pour: step sees i past the end.
func (f *flower) done() { f.i = 1 << 30 }

// step places one block, or a piece of one, and says whether all is placed.
func (f *flower) step(blocks []*Block) bool {
	if f.i >= len(blocks) {
		return true
	}
	b := blocks[f.i]
	if b.Sect != f.sect && b.Sect != nil {
		f.changeSection(b.Sect)
	}
	if b.T != nil {
		f.table(b)
	} else {
		f.para(b, blocks)
	}
	return f.i >= len(blocks)
}

// changeSection starts the next section as its type says.
func (f *flower) changeSection(s *Section) {
	prev := f.sect
	f.sect = s
	switch s.Type {
	case "continuous":
		if s.W == prev.W && s.H == prev.H {
			y := max(f.y, f.maxY)
			f.maxY = 0
			f.top = y
			f.startColumn(0, false)
			f.y = y
			f.empty = false
			return
		}
	case "nextColumn":
		if f.col+1 < prev.Cols {
			f.maxY = max(f.maxY, f.y)
			f.sect = prev
			f.startColumn(f.col+1, false)
			f.sect = s
			return
		}
	}
	f.maxY = 0
	wasEmpty := f.empty && f.page.start.sect == prev && len(f.page.Pieces) == 0
	f.pageEnd()
	if wasEmpty && f.d.lay.pages[len(f.d.lay.pages)-1] == f.page {
		f.d.lay.pages = f.d.lay.pages[:len(f.d.lay.pages)-1]
	} else if s.Type == "oddPage" || s.Type == "evenPage" {
		// A blank page keeps the parity: pages count from one here, sections with their own numbering aside.
		even := len(f.d.lay.pages)%2 == 1
		if even != (s.Type == "evenPage") {
			f.newPage(false, true)
			f.pageEnd()
		}
	}
	f.newPage(true, true)
}

// keepHeight is how much of the blocks after b must share its page, for keep with next; zero when b has no such need.
func (f *flower) keepHeight(b *Block, blocks []*Block) float32 {
	total := float32(0)
	for k := b.Index; k < len(blocks) && k < b.Index+16; k++ {
		n := blocks[k]
		if n.T != nil {
			if k > b.Index {
				tl := n.T.layout(f.w, 0)
				if len(tl.h) > 0 {
					total += tl.h[0]
				}
			}
			break
		}
		p := n.para()
		l := n.laidAt(f.w)
		if !p.PP.KeepNext {
			if k > b.Index {
				// The first two lines of what follows, or all of a shorter paragraph.
				for i := 0; i < len(l.Lines) && i < 2; i++ {
					total += l.Lines[i].H
				}
				total += p.Before
			}
			break
		}
		total += l.Height + p.Before + p.After
	}
	return total
}

// para places as much of a paragraph as fits, and moves on when it is all placed.
func (f *flower) para(b *Block, blocks []*Block) {
	p := b.para()
	l := b.laidAt(f.w)
	if f.line == 0 {
		if p.PP.PageBefore && !f.empty {
			f.nextPageForce()
			return
		}
		if p.PP.PageBefore && f.empty && f.page.Index > 0 && !f.page.start.hard && len(f.page.Pieces) == 0 {
			f.page.start.hard = true
			f.fresh = false
		}
		if !f.empty && p.PP.KeepNext {
			if h := f.keepHeight(b, blocks); h > 0 && h < f.bottom-f.top && f.y+h > f.bottom {
				f.nextColumn(false)
				return
			}
		}
	}
	n := len(l.Lines)
	hide := false
	stop := -1 // the line a page or column break ends
	stopKind := SegPage
	for i := range p.Segs {
		s := &p.Segs[i]
		if s.Kind != SegPage && s.Kind != SegColumn {
			continue
		}
		for k := f.line; k < n; k++ {
			if l.Lines[k].End == s.Start {
				stop, stopKind = k, s.Kind
				break
			}
		}
		if stop >= 0 {
			if stop == n-2 && l.Lines[n-1].Start >= p.Len {
				hide = true // the break is the paragraph's last thing: nothing is left for the next page
			}
			break
		}
	}
	end := n
	if stop >= 0 {
		end = stop + 1
	}
	before := float32(0)
	if f.line == 0 {
		before = p.Before
		if f.fresh && f.empty {
			before = 0
		}
		if f.contextual(b, blocks, true) {
			before = 0
		}
		before += p.PP.Top.outer()
	}
	y := f.y + before
	// Lines that fit, by the rules.
	k := f.line
	for k < end && y+lineSum(l, f.line, k+1) <= f.bottom+0.01 {
		k++
	}
	if k < end {
		fits := k - f.line
		rest := len(l.Lines) - k
		if p.PP.KeepLines && f.line == 0 {
			fits = 0
		}
		if p.PP.Widow {
			if rest == 1 && fits > 0 {
				fits--
				rest++
			}
			if f.line == 0 && fits == 1 {
				fits = 0
			}
		}
		if fits <= 0 {
			if !f.empty {
				f.nextColumn(false)
				return
			}
			fits = max(k-f.line, 1) // an empty page takes what it can, at least a line
		}
		k = f.line + fits
	}
	h := lineSum(l, f.line, k)
	piece := Piece{B: b, L: l, From: f.line, To: k, X: f.x, Y: y}
	f.page.Pieces = append(f.page.Pieces, piece)
	f.decorate(&f.page.Stack, &piece, y, h, f.x, f.w, f.line == 0, k >= n || hide && k >= n-1)
	f.anchors(b, f.line == 0, y)
	f.y = y + h
	f.empty = false
	if k < n && !(hide && k >= n-1) {
		f.line = k
		if stop >= 0 && k == stop+1 {
			if stopKind == SegPage {
				f.nextPageForce()
			} else {
				f.nextColumn(true)
			}
			return
		}
		f.nextColumn(false)
		return
	}
	// The paragraph is placed.
	after := p.After
	if f.contextual(b, blocks, false) {
		after = 0
	}
	f.y += after + p.PP.Bottom.outer()
	f.prev = b
	f.line = 0
	f.i++
	if stop >= 0 {
		if stopKind == SegPage {
			f.nextPageForce()
		} else {
			f.nextColumn(true)
		}
	}
}

// nextPageForce goes to a new page whatever columns are left.
func (f *flower) nextPageForce() {
	f.maxY = 0
	f.pageEnd()
	f.newPage(false, true)
	f.spliceOld()
}

func lineSum(l *flow.Laid, a, b int) float32 {
	if a >= b {
		return 0
	}
	return l.Lines[b-1].Y + l.Lines[b-1].H - l.Lines[a].Y
}

// contextual reports that the space between b and its neighbour goes, for two paragraphs of a style that says so.
func (f *flower) contextual(b *Block, blocks []*Block, before bool) bool {
	p := b.para()
	if !p.PP.Contextual {
		return false
	}
	k := b.Index + 1
	if before {
		k = b.Index - 1
	}
	if k < 0 || k >= len(blocks) || blocks[k].T != nil {
		return false
	}
	return blocks[k].para().PP.Style == p.PP.Style
}

// outer is what a border adds outside the words.
func (b Border) outer() float32 {
	if !b.shows() {
		return 0
	}
	return b.Width + b.Space
}

// decorate adds a paragraph's shading and borders around its lines at y, h high.
func (f *flower) decorate(s *Stack, piece *Piece, y, h, x, w float32, first, last bool) {
	decorate(s, piece.B.para(), y, h, x, w, first, last)
}

func decorate(s *Stack, p *Para, y, h, x, w float32, first, last bool) {
	pp := &p.PP
	left := x + p.Flow.Left + min(p.Flow.First, 0)
	right := x + w - p.Flow.Right
	if pp.Fill.A != 0 {
		s.Decor = append(s.Decor, Decor{Rect: gfx.Rect{X: left, Y: y, W: right - left, H: h}, Col: pp.Fill})
	}
	line := func(e Border, x, y, w, h float32) {
		col := e.Color
		if col.A == 0 {
			col = gfx.Hex(0)
		}
		s.Decor = append(s.Decor, Decor{Rect: gfx.Rect{X: x, Y: y, W: w, H: h}, Col: col, Kind: e.Kind, Line: true})
	}
	if pp.Lft.shows() {
		line(pp.Lft, left-pp.Lft.Space-pp.Lft.Width, y, pp.Lft.Width, h)
	}
	if pp.Rgt.shows() {
		line(pp.Rgt, right+pp.Rgt.Space, y, pp.Rgt.Width, h)
	}
	if first && pp.Top.shows() {
		line(pp.Top, left, y-pp.Top.Space-pp.Top.Width, right-left, pp.Top.Width)
	}
	if last && pp.Bottom.shows() {
		line(pp.Bottom, left, y+h+pp.Bottom.Space, right-left, pp.Bottom.Width)
	}
}

// table places rows until the table is done or the page is full.
func (f *flower) table(b *Block) {
	t := b.T
	if len(t.Rows) == 0 {
		f.i++
		return
	}
	tl := t.layout(f.w, 0)
	if f.offs == nil || len(f.offs) < len(tl.edges) {
		f.offs = make([]float32, len(tl.edges))
	}
	x := f.x + t.x(tl)
	limit := f.bottom - f.y
	if f.empty && f.row > 0 {
		f.y += t.repeatHeaders(&f.page.Stack, x, f.y, tl, limit)
		limit = f.bottom - f.y
	}
	mark := len(f.page.Pieces)
	used, done, ok := t.placeRow(&f.page.Stack, f.row, x, f.y, limit, f.offs, tl, f.empty)
	if !ok {
		f.nextColumn(false)
		return
	}
	f.y += used
	f.empty = false
	for i := mark; i < len(f.page.Pieces); i++ {
		pc := &f.page.Pieces[i]
		if pc.From == 0 {
			f.anchors(pc.B, true, pc.Y)
		}
	}
	if !done {
		f.nextColumn(false)
		return
	}
	f.row++
	if f.row >= len(t.Rows) {
		f.row, f.offs = 0, nil
		f.y += 0
		f.prev = b
		f.i++
	}
}

// repeatHeaders draws the table's heading rows again at the top of a page and is how high they are.
func (t *Table) repeatHeaders(s *Stack, x, y float32, tl *tableLayout, limit float32) float32 {
	used := float32(0)
	offs := make([]float32, len(tl.edges))
	for ri, r := range t.Rows {
		if !r.Header || used+tl.h[ri] > limit/2 {
			break
		}
		h, _, ok := t.placeRow(s, ri, x, y+used, tl.h[ri]+1, offs, tl, true)
		if !ok {
			break
		}
		used += h
	}
	return used
}

// anchors places the paragraph's floating things on the page.
func (f *flower) anchors(b *Block, first bool, y float32) {
	if !first {
		return
	}
	for _, a := range b.para().Anchors {
		f.page.Anchors = append(f.page.Anchors, Placed{A: a, Rect: a.place(f.page, f.sect, f.x, f.w, y)})
	}
}

// stackOf lays out a container on an endless sheet.
func (d *Doc) stackOf(c *Container, w float32, depth int) *Stack {
	s := &Stack{}
	if depth > maxTableDepth {
		return s
	}
	y := float32(0)
	for i, b := range c.Blocks {
		if b.T != nil {
			t := b.T
			tl := t.layout(w, depth)
			offs := make([]float32, len(tl.edges))
			x := t.x(tl)
			for ri := range t.Rows {
				h, _, _ := t.placeRow(s, ri, x, y, 1e6, offs, tl, true)
				y += h
			}
			continue
		}
		p := b.para()
		l := b.laidAt(w)
		sameNext := p.PP.Contextual && i+1 < len(c.Blocks) && c.Blocks[i+1].T == nil && c.Blocks[i+1].para().PP.Style == p.PP.Style
		samePrev := p.PP.Contextual && i > 0 && c.Blocks[i-1].T == nil && c.Blocks[i-1].para().PP.Style == p.PP.Style
		if !samePrev {
			y += p.Before
		}
		y += p.PP.Top.outer()
		s.Pieces = append(s.Pieces, Piece{B: b, L: l, To: len(l.Lines), Y: y})
		decorate(s, p, y, l.Height, 0, w, true, true)
		y += l.Height + p.PP.Bottom.outer()
		if !sameNext {
			y += p.After
		}
	}
	s.H = y
	return s
}

// Section is a section of the document.
type Section struct {
	Node           *ox.Node
	W, H           float32
	MT, MB, ML, MR float32
	Hdr, Ftr       float32
	Gutter         float32
	ExactTop       bool
	ExactBottom    bool
	Cols           int
	Gap            float32
	Widths         []float32 // the columns' widths when they differ
	Type           string
	TitlePg        bool
	PageStart      int     // the number of its first page, -1 to go on
	Pitch          float32 // the line grid's height, 0 for none
	Refs           map[string]*Story
	d              *Doc
}

// TextW is the width between the margins.
func (s *Section) TextW() float32 { return s.W - s.ML - s.MR - s.Gutter }

// colAt is a column's left edge and width.
func (s *Section) colAt(i int) (x, w float32) {
	left := s.ML + s.Gutter
	if len(s.Widths) == s.Cols && s.Cols > 1 {
		for k := 0; k < i; k++ {
			left += s.Widths[k] + s.Gap
		}
		return left, s.Widths[i]
	}
	w = (s.TextW() - s.Gap*float32(s.Cols-1)) / float32(s.Cols)
	return left + float32(i)*(w+s.Gap), w
}

// story is the header or footer a page of the section shows.
func (s *Section) story(kind string, first, even bool) *Story {
	switch {
	case first:
		return s.Refs[kind+":first"]
	case even:
		return s.Refs[kind+":even"]
	}
	return s.Refs[kind+":default"]
}

// readSections finds the sections and which blocks are in them.
func (d *Doc) readSections() {
	known := make(map[*ox.Node]*Section, len(d.sects))
	for _, s := range d.sects {
		known[s.Node] = s
	}
	d.sects = d.sects[:0]
	var run []*Block
	var prev *Section
	end := func(n *ox.Node) {
		// A section that is still there stays the same object, as pages compare them.
		s := known[n]
		if s == nil || d.lay.sectsNew {
			s = d.readSection(n, prev)
		}
		for _, b := range run {
			b.Sect = s
		}
		run = run[:0]
		d.sects = append(d.sects, s)
		prev = s
	}
	for _, b := range d.Body.Root.Blocks {
		run = append(run, b)
		if b.T == nil {
			if n := b.Node.Child("w:pPr").Child("w:sectPr"); n != nil {
				end(n)
			}
		}
	}
	last := d.Body.Root.Node.Child("w:sectPr")
	if last != nil || len(run) > 0 || len(d.sects) == 0 {
		end(last)
	}
	d.lay.sectsNew = false
}

func (d *Doc) readSection(n *ox.Node, prev *Section) *Section {
	// A file that does not say gets the same page as a new document: A4, with its margins.
	s := &Section{Node: n, d: d, W: 595.3, H: 841.9, MT: 72, MB: 72, ML: 90, MR: 90, Hdr: 42.55, Ftr: 49.6, Cols: 1, Gap: 36, PageStart: -1, Refs: map[string]*Story{}, Type: "nextPage"}
	if prev != nil {
		for k, v := range prev.Refs {
			s.Refs[k] = v
		}
	}
	if n == nil {
		return s
	}
	if e := n.Child("w:pgSz"); e != nil {
		if v := measure(e.Attr("w:w")); v > 0 {
			s.W = v
		}
		if v := measure(e.Attr("w:h")); v > 0 {
			s.H = v
		}
		s.W, s.H = min(max(s.W, 72), maxPageSide), min(max(s.H, 72), maxPageSide)
	}
	if e := n.Child("w:pgMar"); e != nil {
		read := func(name string, field *float32) {
			if v, ok := e.Lookup(name); ok {
				*field = measure(v)
			}
		}
		read("w:top", &s.MT)
		read("w:bottom", &s.MB)
		read("w:left", &s.ML)
		read("w:right", &s.MR)
		read("w:header", &s.Hdr)
		read("w:footer", &s.Ftr)
		read("w:gutter", &s.Gutter)
		// A negative margin is one the header may not push.
		s.ExactTop, s.ExactBottom = s.MT < 0, s.MB < 0
		s.MT, s.MB = abs(s.MT), abs(s.MB)
		s.ML, s.MR = max(s.ML, 0), max(s.MR, 0)
		if s.ML+s.MR+s.Gutter > s.W-36 {
			s.ML, s.MR, s.Gutter = (s.W-36)/2, (s.W-36)/2, 0
		}
		if s.MT+s.MB > s.H-36 {
			s.MT, s.MB = (s.H-36)/2, (s.H-36)/2
		}
	}
	if e := n.Child("w:cols"); e != nil {
		if v := atoi(e.Attr("w:num")); v > 1 && v <= 45 {
			s.Cols = v
		}
		if v, ok := e.Lookup("w:space"); ok {
			s.Gap = measure(v)
		}
		for _, c := range e.Elements("w:col") {
			s.Widths = append(s.Widths, measure(c.Attr("w:w")))
			if v, ok := c.Lookup("w:space"); ok && len(s.Widths) == 1 {
				s.Gap = measure(v)
			}
		}
		if len(s.Widths) > 1 && e.Attr("w:equalWidth") == "0" {
			s.Cols = min(len(s.Widths), 45)
		} else {
			s.Widths = nil
		}
		if s.TextW()-s.Gap*float32(s.Cols-1) < float32(s.Cols)*18 {
			s.Cols, s.Widths = 1, nil
		}
	}
	if e := n.Child("w:type"); e != nil {
		if v := e.Attr("w:val"); v != "" {
			s.Type = v
		}
	}
	if e := n.Child("w:docGrid"); e != nil && (e.Attr("w:type") == "lines" || e.Attr("w:type") == "linesAndChars") {
		s.Pitch = min(max(measure(e.Attr("w:linePitch")), 0), 200)
	}
	if e := n.Child("w:titlePg"); e != nil {
		s.TitlePg = toggle(e)
	}
	if e := n.Child("w:pgNumType"); e != nil {
		if v, ok := e.Lookup("w:start"); ok {
			s.PageStart = atoi(v)
		}
	}
	for _, e := range n.Elements("w:headerReference", "w:footerReference") {
		kind := "header"
		if e.Name == "w:footerReference" {
			kind = "footer"
		}
		which := e.Attr("w:type")
		if which == "" {
			which = "default"
		}
		if st := d.storyByRel(e.Attr("r:id")); st != nil {
			s.Refs[kind+":"+which] = st
		}
	}
	return s
}

func abs(v float32) float32 {
	if v < 0 {
		return -v
	}
	return v
}

// storyByRel is the header or footer part a relationship of the main part names.
func (d *Doc) storyByRel(id string) *Story {
	for _, r := range d.Main.Rels {
		if r.ID == id && !r.External {
			return d.storyOf(relResolve(d.Main.Name, r.Target))
		}
	}
	return nil
}

// storyOf is the story of a header or footer part.
func (d *Doc) storyOf(name string) *Story {
	if s, ok := d.stories[name]; ok {
		return s
	}
	p, err := d.part(name)
	if err != nil || (p.X.Root.Name != "w:hdr" && p.X.Root.Name != "w:ftr") {
		d.stories[name] = nil
		delete(d.stories, name)
		return nil
	}
	s := d.newStory(p, p.X.Root)
	d.stories[name] = s
	s.sync()
	d.lay.numStale = true
	return s
}

// storyStack is a header or footer laid out for a width.
func (d *Doc) storyStack(s *Story, w float32) *Stack {
	if s.head == nil || s.headW != w {
		s.head, s.headW = d.stackOf(s.Root, w, 0), w
	}
	return s.head
}

// headFoot is a header or footer as one page shows it.
type headFoot struct {
	stack  *Stack
	story  *Story
	number int
	total  int
	stamp  uint64
}

// pageStory gives the page's header or footer, with its fields worked out for the page.
func (d *Doc) pageStack(pg *Page, kind string, keep **headFoot) *headFoot {
	s := pg.Sect
	st := s.story(kind, pg.First && s.TitlePg, d.evenOdd && pg.Number%2 == 0)
	if st == nil {
		*keep = nil
		return nil
	}
	h := *keep
	if h != nil && h.story == st && h.number == pg.Number && h.total == pg.Total && h.stamp == d.lay.stories {
		return h
	}
	base := d.storyStack(st, s.TextW())
	h = &headFoot{story: st, number: pg.Number, total: pg.Total, stamp: d.lay.stories, stack: base}
	// Pieces with a page's own fields are laid out again for this page.
	vals := &fieldValues{page: strconv.Itoa(pg.Number), pages: strconv.Itoa(pg.Total)}
	if d.Sel.Caret.B != nil && d.Sel.Caret.B.In.Story == st {
		*keep = h // while it is being typed in, a header shows what is in the file
		return h
	}
	copied := *base
	copied.Pieces = append([]Piece(nil), base.Pieces...)
	for i := range copied.Pieces {
		pc := &copied.Pieces[i]
		dynamic := false
		for _, f := range pc.B.para().Fields {
			if dynamicField(f.Instr) != "" {
				dynamic = true
			}
		}
		for j := range pc.B.P.Segs {
			if pc.B.P.Segs[j].T != nil && pc.B.P.Segs[j].T.Name == "w:pgNum" {
				dynamic = true
			}
		}
		if dynamic {
			one := d.buildPara(pc.B, vals)
			one.Flow.Empty = one.markStyle()
			laid := flow.Layout(&one.Flow, pc.L.Width, d.Fonts)
			if len(laid.Lines) == len(pc.L.Lines) {
				pc.L = laid
			}
		}
	}
	h.stack = &copied
	*keep = h
	return h
}
