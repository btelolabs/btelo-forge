package docx

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

// The view over the trees: a story is a run of text with its own flow (the body, a header, a footer, a text box), a
// container holds its blocks, and a block is a paragraph or a table. Blocks hold the nodes they stand for, so that
// the tree stays the only thing that is saved; a block's properties are worked out when first needed and forgotten
// when its paragraph changes.

// Story is one flow of text.
type Story struct {
	d        *Doc
	Part     *Part
	Root     *Container
	flat     []*Block // the paragraphs in reading order, those in tables too
	ReadOnly bool
	stale    bool
	byNode   map[*ox.Node]*Block // every paragraph and table, by its element
	head     *Stack              // a header or footer laid out
	headW    float32
}

// Container is what holds blocks: a story's root, or a table cell.
type Container struct {
	Story  *Story
	Node   *ox.Node // the element holding the blocks, though some may sit deeper, in a content control
	Blocks []*Block
	Cell   *Cell // nil at a story's root
	Base   *Base // what a table's style gives its paragraphs, nil for none
}

// Block is a paragraph or a table.
type Block struct {
	Node  *ox.Node
	In    *Container
	Index int // in the container
	Flat  int // in the story's paragraphs, -1 for a table
	P     *Para
	T     *Table
	stale bool
	Sect  *Section // top-level blocks: the section it is in
}

// Text is a paragraph's words, one rune for each caret place.
func (b *Block) Text() string {
	var s strings.Builder
	for i := range b.para().Segs {
		seg := &b.P.Segs[i]
		switch seg.Kind {
		case SegText:
			s.WriteString(seg.S)
		case SegTab:
			s.WriteByte('\t')
		case SegBreak, SegPage, SegColumn:
			s.WriteByte('\n')
		default:
			s.WriteRune(0xfffc)
		}
	}
	return s.String()
}

// top is the top-level block holding b.
func (b *Block) top() *Block {
	for b.In.Cell != nil {
		b = b.In.Cell.Row.Table.Block
	}
	return b
}

// SegKind is what a segment of a paragraph stands for.
type SegKind uint8

const (
	SegText    SegKind = iota
	SegTab             // w:tab
	SegBreak           // a line break
	SegPage            // a page break
	SegColumn          // a column break
	SegPicture         // a picture or another object in the line
	SegAtom            // something shown that is not text: a footnote mark, a symbol
)

// Seg is a stretch of a paragraph's caret places and the XML under it.
type Seg struct {
	Start, N int
	Kind     SegKind
	R        *ox.Node // the w:r, or for a math or other atom the element itself
	T        *ox.Node // the w:t, w:tab, w:br, w:drawing...
	S        string   // the text as shown
	Locked   bool     // tracked changes and the like: shown but not edited
	Field    int      // 1 + the index in Para.Fields of a field it is in, in this paragraph
	Link     *ox.Node // the w:hyperlink it is in
}

// End is the place after the segment.
func (s *Seg) End() int { return s.Start + s.N }

// fieldInfo is a field wholly in one paragraph.
type fieldInfo struct {
	Instr      string
	Begin, End *ox.Node // the first and last nodes: two runs, or the w:fldSimple twice
	From, To   int      // the caret places of its result
}

// Para is what a paragraph resolves to.
type Para struct {
	PP      PP
	Mark    RP // the paragraph mark's properties, which an empty paragraph shows
	Flow    flow.Para
	Segs    []Seg
	Len     int
	Fields  []fieldInfo
	Anchors []*Anchor
	Label   string // the list marker
	// Spaces above and below, in points.
	Before, After float32

	laid    *flow.Laid
	laidW   float32
	marker  *flow.Laid
	markerX float32
}

func (d *Doc) newStory(p *Part, root *ox.Node) *Story {
	s := &Story{d: d, Part: p}
	s.Root = &Container{Story: s, Node: root}
	s.stale = true
	return s
}

// first is the story's first paragraph.
func (s *Story) first() *Block {
	if len(s.flat) == 0 {
		return nil
	}
	return s.flat[0]
}

// Paragraphs are the story's paragraphs in reading order.
func (s *Story) Paragraphs() []*Block { return s.flat }

// rebuild brings the blocks of what changed in step with the trees.
func (d *Doc) rebuild() {
	for _, s := range d.allStories() {
		if s.stale {
			s.sync()
		}
	}
	if d.lay.sectsStale || d.sects == nil {
		d.readSections()
		d.lay.sectsStale = false
	}
	if d.lay.numStale {
		d.labels()
		d.lay.numStale = false
	}
	d.Sel.Anchor = d.current(d.Sel.Anchor)
	d.Sel.Caret = d.current(d.Sel.Caret)
}

// current is a place with its block as the tree now has it: a table read again has new blocks for the same elements.
func (d *Doc) current(p Pos) Pos {
	if p.B == nil {
		return p
	}
	s := p.B.In.Story
	if b := s.byNode[p.B.Node]; b != nil && b.T == nil {
		p.B = b
		p.Off = min(p.Off, b.para().Len)
		return p
	}
	// The paragraph is gone: the nearest one left.
	at := min(max(p.B.Flat, 0), len(s.flat)-1)
	if at >= 0 {
		return Pos{B: s.flat[at]}
	}
	return Pos{B: d.Body.first()}
}

func (d *Doc) allStories() []*Story {
	out := []*Story{d.Body}
	for _, s := range d.stories {
		out = append(out, s)
	}
	return out
}

// sync walks the story's tree, keeping the blocks that did not change.
func (s *Story) sync() {
	s.stale = false
	s.flat = s.flat[:0]
	s.Root.sync(0)
	// A document always has a paragraph to put the caret in.
	if len(s.flat) == 0 && !s.ReadOnly {
		p := ox.New("w:p")
		if last := s.Root.Node.Child("w:sectPr"); last != nil {
			s.Root.Node.Insert(s.Root.Node.Index(last), p)
		} else {
			s.Root.Node.Append(p)
		}
		s.flat = s.flat[:0]
		s.Root.sync(0)
	}
	s.index()
	s.d.lay.numStale = true
	s.d.lay.sectsStale = true
}

// index lists the blocks by their elements, which is how a place survives a rebuild.
func (s *Story) index() {
	s.byNode = make(map[*ox.Node]*Block, len(s.flat)+8)
	for _, b := range s.flat {
		s.byNode[b.Node] = b
		for c := b.In.Cell; c != nil; c = c.Row.Table.Block.In.Cell {
			s.byNode[c.Row.Table.Block.Node] = c.Row.Table.Block
		}
	}
}

// sync reads a container's blocks from its node, reusing those that did not change.
func (c *Container) sync(depth int) {
	old := make(map[*ox.Node]*Block, len(c.Blocks))
	for _, b := range c.Blocks {
		old[b.Node] = b
	}
	c.Blocks = c.Blocks[:0]
	c.gather(c.Node, old, depth, 0)
}

// gather adds the blocks in node, going into the wrappers that hold blocks.
func (c *Container) gather(node *ox.Node, old map[*ox.Node]*Block, depth, wrap int) {
	if wrap > maxDepth {
		return
	}
	for _, n := range node.Children {
		if n.Kind != ox.Element {
			continue
		}
		switch n.Name {
		case "w:p":
			b := old[n]
			if b == nil || b.T != nil {
				b = &Block{Node: n, In: c}
			}
			b.stale = false
			b.Index, b.Flat = len(c.Blocks), len(c.Story.flat)
			c.Blocks = append(c.Blocks, b)
			c.Story.flat = append(c.Story.flat, b)
		case "w:tbl":
			b := old[n]
			if b == nil || b.T == nil {
				b = &Block{Node: n, In: c}
				b.T = &Table{Block: b}
				b.stale = true
			}
			b.Index, b.Flat = len(c.Blocks), -1
			c.Blocks = append(c.Blocks, b)
			if b.stale {
				b.stale = false
				b.T.read(depth + 1)
			} else {
				b.T.addFlat()
			}
		case "w:sdt":
			c.gather(n.Child("w:sdtContent"), old, depth, wrap+1)
		case "w:customXml", "w:ins", "w:moveTo", "w:smartTag":
			c.gather(n, old, depth, wrap+1)
		case "mc:AlternateContent":
			if ch := n.Child("mc:Choice"); ch != nil {
				c.gather(ch, old, depth, wrap+1)
			}
		}
	}
}

// markStale says the block's tree changed, up to the story, so that a rebuild goes down this path.
func (b *Block) markStale() {
	for {
		b.stale = true
		if b.In.Cell == nil {
			b.In.Story.stale = true
			return
		}
		b = b.In.Cell.Row.Table.Block
	}
}

// para is the paragraph's data, worked out when it is first wanted.
func (b *Block) para() *Para {
	if b.P == nil {
		b.In.Story.d.buildPara(b, nil)
	}
	return b.P
}

// fieldValues are what a page's own fields say.
type fieldValues struct{ page, pages string }

// paragraph is being read into a Para.
type paraBuild struct {
	d      *Doc
	part   *Part
	p      *Para
	base   *Base // the paragraph's style, under the runs
	at     int
	fields []*openField // the complex fields open at this point
	vals   *fieldValues
	depth  int
}

type openField struct {
	instr   strings.Builder
	begin   *ox.Node
	from    int
	result  bool // past the separator
	local   bool // began in this paragraph
	dynamic bool
	said    bool // a dynamic field's value has been written
}

// walkState is what the elements around a run say about it.
type walkState struct {
	link   *ox.Node
	del    bool
	ins    bool
	field  int
	simple string
}

// buildPara reads the paragraph into its data. With vals set it is a throwaway copy for one page, its fields worked
// out, and the caller keeps the result.
func (d *Doc) buildPara(b *Block, vals *fieldValues) *Para {
	p := &Para{}
	styles := d.Styles
	pPr := b.Node.Child("w:pPr")
	style := pPr.Child("w:pStyle").Attr("w:val")
	base := styles.Para(b.In.Base, style)
	p.PP = base.P
	p.Mark = base.R
	if pPr != nil {
		if p.PP.NumID != 0 || pPr.Child("w:numPr") != nil {
			// The list level's indents lie between the style and the paragraph's own.
			probe := p.PP
			styles.applyP(&probe, pPr)
			if lvl := d.Num.level(probe.NumID, probe.Ilvl); lvl != nil {
				styles.applyP(&p.PP, lvl.PPr)
			}
		}
		saved := p.PP.Style
		styles.applyP(&p.PP, pPr)
		p.PP.Style = saved
		styles.applyR(&p.Mark, pPr.Child("w:rPr"))
	} else if lvl := d.Num.level(p.PP.NumID, p.PP.Ilvl); lvl != nil {
		styles.applyP(&p.PP, lvl.PPr)
	}
	build := &paraBuild{d: d, part: b.In.Story.Part, p: p, base: base, vals: vals}
	build.walk(b.Node, walkState{}, 0)
	p.Len = build.at
	p.finish(d)
	if old := b.P; old != nil {
		p.Label = old.Label
	}
	if vals == nil {
		b.P = p
	}
	return p
}

// finish makes the flow paragraph's own properties.
func (p *Para) finish(d *Doc) {
	pp := &p.PP
	size := p.Mark.Size
	if size <= 0 {
		size = 10
	}
	f := &p.Flow
	f.Align = pp.Align
	f.Left, f.Right, f.First = pp.Left, pp.Right, pp.First
	if pp.LeftCh != 0 {
		f.Left = float32(pp.LeftCh) * size / 100
	}
	if pp.RightCh != 0 {
		f.Right = float32(pp.RightCh) * size / 100
	}
	if pp.FirstCh != 0 {
		f.First = float32(pp.FirstCh) * size / 100
	}
	if pp.Hang != 0 {
		f.First = -float32(pp.Hang) * size / 100
	}
	f.Line, f.Rule = pp.Line, pp.Rule
	f.TabEvery = d.defTab
	for _, t := range pp.Tabs {
		f.Stops = append(f.Stops, flow.TabStop{Pos: t.Pos, Kind: t.Kind, Leader: t.Leader})
	}
	if f.First < 0 {
		// A hanging indent is a tab stop.
		at := len(f.Stops)
		for i, t := range f.Stops {
			if t.Pos >= f.Left {
				at = i
				break
			}
		}
		f.Stops = append(f.Stops, flow.TabStop{})
		copy(f.Stops[at+1:], f.Stops[at:])
		f.Stops[at] = flow.TabStop{Pos: f.Left}
	}
	f.Empty = p.markStyle()
	p.Before, p.After = pp.Before, pp.After
	if pp.BeforeLn != 0 {
		p.Before = float32(pp.BeforeLn) * size * 1.2 / 100
	}
	if pp.AfterLn != 0 {
		p.After = float32(pp.AfterLn) * size * 1.2 / 100
	}
}

func (p *Para) markStyle() flow.Style { return rpStyle(&p.Mark, p.Mark.HAnsi) }

// rpStyle is the flow style of a run's properties in a family.
func rpStyle(rp *RP, family string) flow.Style {
	return flow.Style{Family: family, Size: rp.Size, Bold: rp.Bold, Italic: rp.Italic, Underline: rp.Under != "", Strike: rp.Strike || rp.DStrike,
		Color: rp.Color, Highlight: rp.Highlight, Baseline: rp.Vert}
}

func (pb *paraBuild) walk(n *ox.Node, st walkState, depth int) {
	if depth > maxDepth {
		return
	}
	for _, c := range n.Children {
		if c.Kind != ox.Element {
			continue
		}
		switch c.Name {
		case "w:pPr":
		case "w:r":
			pb.run(c, st)
		case "w:hyperlink":
			next := st
			next.link = c
			pb.walk(c, next, depth+1)
		case "w:ins", "w:moveTo":
			next := st
			next.ins = true
			pb.walk(c, next, depth+1)
		case "w:del", "w:moveFrom":
			next := st
			next.del = true
			pb.walk(c, next, depth+1)
		case "w:sdt":
			pb.walk(c.Child("w:sdtContent"), st, depth+1)
		case "w:smartTag", "w:customXml", "w:dir", "w:bdo":
			pb.walk(c, st, depth+1)
		case "w:fldSimple":
			pb.simpleField(c, st, depth)
		case "m:oMath", "m:oMathPara":
			pb.atomInline(c, st, "?"+mathText(c), 0)
		case "mc:AlternateContent":
			pb.walk(chosen(c), st, depth+1)
		}
	}
}

// chosen is the branch of an mc:AlternateContent to read.
func chosen(n *ox.Node) *ox.Node {
	if c := n.Child("mc:Choice"); c != nil {
		return c
	}
	return n.Child("mc:Fallback")
}

func (pb *paraBuild) simpleField(c *ox.Node, st walkState, depth int) {
	f := &openField{begin: c, from: pb.at, result: true, local: true}
	f.instr.WriteString(c.Attr("w:instr"))
	f.dynamic = pb.vals != nil && dynamicField(f.instr.String()) != ""
	pb.fields = append(pb.fields, f)
	i := len(pb.p.Fields)
	pb.p.Fields = append(pb.p.Fields, fieldInfo{Instr: f.instr.String(), Begin: c, End: c, From: pb.at})
	next := st
	next.field = i + 1
	pb.walk(c, next, depth+1)
	pb.p.Fields[i].To = pb.at
	pb.fields = pb.fields[:len(pb.fields)-1]
}

// dynamicField is the kind of a field a page works out for itself, "" for others.
func dynamicField(instr string) string {
	fs := strings.Fields(instr)
	if len(fs) == 0 {
		return ""
	}
	switch strings.ToUpper(fs[0]) {
	case "PAGE":
		return "page"
	case "NUMPAGES":
		return "pages"
	}
	return ""
}

// hidden reports whether what is at this point is a field's code rather than its result.
func (pb *paraBuild) hidden() bool {
	for _, f := range pb.fields {
		if !f.result {
			return true
		}
	}
	return false
}

func (pb *paraBuild) run(r *ox.Node, st walkState) {
	d := pb.d
	rp := pb.base.R
	rPr := r.Child("w:rPr")
	if rPr != nil {
		d.Styles.applyR(&rp, rPr)
	}
	if st.del {
		rp.Strike, rp.Color = true, gfx.Hex(0xc00000)
	} else if st.ins {
		rp.Under, rp.Color = "single", gfx.Hex(0xc00000)
	}
	locked := st.del || st.ins
	if rp.Vanish {
		pb.fieldMarks(r) // a hidden field is still a field
		return
	}
	for _, c := range r.Children {
		if c.Kind != ox.Element {
			continue
		}
		switch c.Name {
		case "w:rPr":
		case "w:fldChar":
			pb.fieldChar(c, r, st)
		case "w:instrText":
			if n := len(pb.fields); n > 0 && !pb.fields[n-1].result {
				pb.fields[n-1].instr.WriteString(c.InnerText())
			}
		}
		if pb.hidden() {
			continue
		}
		switch c.Name {
		case "w:t", "w:delText":
			pb.text(c, r, &rp, st, locked)
		case "w:tab", "w:ptab":
			pb.place(&Seg{N: 1, Kind: SegTab, R: r, T: c, Locked: locked, Link: st.link}, flow.Run{Text: "\t", Style: rpStyle(&rp, rp.HAnsi)})
		case "w:cr":
			pb.place(&Seg{N: 1, Kind: SegBreak, R: r, T: c, Locked: locked, Link: st.link}, flow.Run{Text: "\n", Style: rpStyle(&rp, rp.HAnsi)})
		case "w:br":
			kind := SegBreak
			switch c.Attr("w:type") {
			case "page":
				kind = SegPage
			case "column":
				kind = SegColumn
			}
			pb.place(&Seg{N: 1, Kind: kind, R: r, T: c, Locked: locked, Link: st.link}, flow.Run{Text: "\n", Style: rpStyle(&rp, rp.HAnsi)})
		case "w:noBreakHyphen":
			pb.atomText(c, r, &rp, st, "‑")
		case "w:softHyphen":
		case "w:sym":
			pb.atomText(c, r, &rp, st, symbolText(c))
		case "w:footnoteReference", "w:endnoteReference":
			mark := rp
			mark.Vert = 1
			pb.atomText(c, r, &mark, st, c.Attr("w:id"))
		case "w:pgNum":
			if pb.vals != nil {
				pb.atomText(c, r, &rp, st, pb.vals.page)
			} else {
				pb.atomText(c, r, &rp, st, "1")
			}
		case "w:drawing", "w:pict", "w:object":
			pb.drawing(c, r, &rp, st, locked)
		case "mc:AlternateContent":
			if ch := chosen(c); ch != nil {
				for _, k := range ch.Elements("w:drawing", "w:pict", "w:object") {
					pb.drawing(k, r, &rp, st, locked)
				}
			}
		}
	}
}

// fieldMarks follows the field marks of a hidden run.
func (pb *paraBuild) fieldMarks(r *ox.Node) {
	for _, c := range r.Children {
		if c.Kind == ox.Element && c.Name == "w:fldChar" {
			pb.fieldChar(c, r, walkState{})
		}
	}
}

func (pb *paraBuild) fieldChar(c, r *ox.Node, st walkState) {
	switch c.Attr("w:fldCharType") {
	case "begin":
		pb.fields = append(pb.fields, &openField{begin: r, from: pb.at, local: true})
	case "separate":
		if n := len(pb.fields); n > 0 {
			f := pb.fields[n-1]
			f.result = true
			f.from = pb.at
			f.dynamic = pb.vals != nil && dynamicField(f.instr.String()) != ""
		}
	case "end":
		if n := len(pb.fields); n > 0 {
			f := pb.fields[n-1]
			pb.fields = pb.fields[:n-1]
			if f.local {
				pb.p.Fields = append(pb.p.Fields, fieldInfo{Instr: f.instr.String(), Begin: f.begin, End: r, From: f.from, To: pb.at})
				// The segments of the result point at their field.
				for i := len(pb.p.Segs) - 1; i >= 0 && pb.p.Segs[i].Start >= f.from; i-- {
					pb.p.Segs[i].Field = len(pb.p.Fields)
				}
			}
		}
	}
}

// place adds a segment and what it shows.
func (pb *paraBuild) place(s *Seg, run flow.Run) {
	s.Start = pb.at
	pb.at += s.N
	pb.p.Segs = append(pb.p.Segs, *s)
	pb.p.Flow.Runs = append(pb.p.Flow.Runs, run)
}

// atomText is a single place shown as text.
func (pb *paraBuild) atomText(c, r *ox.Node, rp *RP, st walkState, text string) {
	if text == "" {
		text = "□"
	}
	// One caret place: only the first character's width is the place, the rest of a number shows in the same piece.
	style := rpStyle(rp, familyOf(rp, []rune(text)[0]))
	seg := &Seg{N: utf8.RuneCountInString(text), Kind: SegAtom, R: r, T: c, S: text, Locked: true, Link: st.link}
	pb.place(seg, flow.Run{Text: text, Style: style})
}

// atomInline is an inline box standing for what cannot be shown as words: its key starts with ? and holds the label.
func (pb *paraBuild) atomInline(n *ox.Node, st walkState, key string, _ int) {
	rp := pb.base.R
	text := strings.TrimPrefix(key, "?")
	w := float32(utf8.RuneCountInString(text))*rp.Size*0.6 + 8
	inline := &flow.Inline{W: min(w, 400), H: rp.Size * 1.4, Key: key}
	pb.place(&Seg{N: 1, Kind: SegPicture, R: n, T: n, Locked: true, Link: st.link}, flow.Run{Inline: inline, Style: rpStyle(&rp, rp.HAnsi)})
}

// symbolText is what a w:sym shows.
func symbolText(c *ox.Node) string {
	code, err := strconv.ParseUint(c.Attr("w:char"), 16, 32)
	if err != nil {
		return "□"
	}
	if code >= 0xf000 && code <= 0xf0ff {
		return bulletText(string(rune(code)))
	}
	return string(rune(code))
}

// text adds a w:t.
func (pb *paraBuild) text(t, r *ox.Node, rp *RP, st walkState, locked bool) {
	s := t.InnerText()
	if t.Attr("xml:space") != "preserve" {
		s = strings.Trim(s, " \t\r\n")
	}
	if s == "" {
		return
	}
	// A field a page works out shows its value once, in place of what the file cached.
	if n := len(pb.fields); n > 0 && pb.fields[n-1].dynamic {
		f := pb.fields[n-1]
		if f.said {
			return
		}
		f.said = true
		if dynamicField(f.instr.String()) == "page" {
			s = pb.vals.page
		} else {
			s = pb.vals.pages
		}
	}
	s = strings.Map(func(c rune) rune {
		if c == '\n' || c == '\r' || c == '\t' {
			return ' '
		}
		if c < 0x20 {
			return ' '
		}
		return c
	}, s)
	seg := Seg{Start: pb.at, N: utf8.RuneCountInString(s), Kind: SegText, R: r, T: t, S: s, Locked: locked, Link: st.link, Field: st.field}
	pb.p.Segs = append(pb.p.Segs, seg)
	pb.at += seg.N
	pb.styled(s, rp)
}

// styled adds text as flow runs, one for each stretch that keeps its font: Latin and CJK characters in one run of the
// file use its two fonts.
func (pb *paraBuild) styled(s string, rp *RP) {
	rs := []rune(s)
	shown := rs
	if rp.Caps || rp.SmallCaps {
		shown = make([]rune, len(rs))
		for i, r := range rs {
			shown[i] = unicode.ToUpper(r)
		}
	}
	start := 0
	for i := 1; i <= len(rs); i++ {
		if i < len(rs) && familyOf(rp, rs[i]) == familyOf(rp, rs[start]) && (!rp.SmallCaps || unicode.IsLower(rs[i]) == unicode.IsLower(rs[start])) {
			continue
		}
		style := rpStyle(rp, familyOf(rp, rs[start]))
		if rp.SmallCaps && unicode.IsLower(rs[start]) {
			style.Size *= 0.8
		}
		pb.p.Flow.Runs = append(pb.p.Flow.Runs, flow.Run{Text: string(shown[start:i]), Style: style})
		start = i
	}
}

// familyOf is the font a character of the run takes, by its script.
func familyOf(rp *RP, r rune) string {
	switch {
	case eastAsian(r), rp.Hint == "eastAsia" && ambiguousEast(r):
		if rp.East != "" {
			return rp.East
		}
	case r >= 0x590 && r <= 0x8ff:
		if rp.CS != "" {
			return rp.CS
		}
	case r < 0x80:
		if rp.Ascii != "" {
			return rp.Ascii
		}
	}
	if rp.HAnsi != "" {
		return rp.HAnsi
	}
	if rp.Ascii != "" {
		return rp.Ascii
	}
	return rp.East
}

func eastAsian(r rune) bool {
	return unicode.Is(unicode.Han, r) || unicode.Is(unicode.Hiragana, r) || unicode.Is(unicode.Katakana, r) ||
		unicode.Is(unicode.Hangul, r) || r >= 0x3000 && r <= 0x303f || r >= 0xff00 && r <= 0xffef || r >= 0x2e80 && r <= 0x2fdf
}

// ambiguousEast are punctuation shared by Latin and CJK text, which w:hint sends to the East Asian font.
func ambiguousEast(r rune) bool {
	return r >= 0x2010 && r <= 0x2044 || r >= 0x2190 && r <= 0x2bff || r == 0xb7 || r == 0xd7 || r == 0xf7
}

// laidAt is the paragraph broken into lines for a width.
func (b *Block) laidAt(w float32) *flow.Laid {
	p := b.para()
	if p.laid != nil && p.laidW == w {
		return p.laid
	}
	d := b.In.Story.d
	p.marker = nil
	p.Flow.Empty = p.markStyle()
	first, line, rule := p.Flow.First, p.Flow.Line, p.Flow.Rule
	if p.Label != "" {
		d.placeMarker(p, b)
	}
	if pitch := b.gridPitch(); pitch > 0 && rule == flow.Multiple && !p.PP.NoGrid {
		// A line grid sets the height of a single line, and taller text takes what it needs.
		p.Flow.Line, p.Flow.Rule = pitch*max(line, 1), flow.AtLeast
	}
	p.laid = flow.Layout(&p.Flow, w, d.Fonts)
	p.Flow.First, p.Flow.Line, p.Flow.Rule = first, line, rule
	p.laidW = w
	return p.laid
}

// gridPitch is the height of the line grid over a paragraph, 0 for none.
func (b *Block) gridPitch() float32 {
	if b.In.Story != b.In.Story.d.Body {
		return 0
	}
	if s := b.top().Sect; s != nil {
		return s.Pitch
	}
	return 0
}

// placeMarker lays out a list label and moves the first line past it.
func (d *Doc) placeMarker(p *Para, b *Block) {
	rp := p.Mark
	rp.Under = ""
	rp.Highlight = color.RGBA{}
	lvl := d.Num.level(p.PP.NumID, p.PP.Ilvl)
	suff, jc := "tab", ""
	if lvl != nil {
		d.Styles.applyR(&rp, lvl.RPr)
		suff, jc = lvl.Suff, lvl.Jc
	}
	style := rpStyle(&rp, familyOf(&rp, []rune(p.Label)[0]))
	mp := &flow.Para{Runs: []flow.Run{{Text: p.Label, Style: style}}}
	p.marker = flow.Layout(mp, 4000, d.Fonts)
	mw := p.marker.Lines[0].Right
	left, first := p.Flow.Left, p.Flow.First
	x := left + first
	switch jc {
	case "right", "end":
		x -= mw
	case "center":
		x -= mw / 2
	}
	p.markerX = x
	end := x + mw
	start := end
	switch suff {
	case "space":
		start = end + style.Size/4
	case "nothing":
	default:
		// The next stop past the label: the hanging indent, or a tab stop.
		start = left
		if start <= end {
			every := max(p.Flow.TabEvery, 1)
			start = 0
			for start <= end {
				start += every
			}
			for _, t := range p.Flow.Stops {
				if t.Pos > end {
					start = min(start, t.Pos)
					break
				}
			}
		}
	}
	p.Flow.First = start - left
}

// changed says the paragraph's tree changed; it is read again and laid out again.
func (d *Doc) changed(b *Block) {
	var label string
	if b.P != nil {
		label = b.P.Label
	}
	had := b.P != nil && b.P.PP.NumID != 0
	b.P = nil
	d.buildPara(b, nil)
	b.P.Label = label
	if had || b.P.PP.NumID != 0 {
		d.lay.numStale = true
	}
	d.lay.dirty(b)
}

// allChanged says styles or numbering changed: every paragraph is read again.
func (d *Doc) allChanged() {
	for _, s := range d.allStories() {
		for _, b := range s.flat {
			b.P = nil
		}
		s.Root.clearLaid()
		s.stale = true
	}
	d.lay.numStale = true
	d.lay.restart()
}

func (c *Container) clearLaid() {
	for _, b := range c.Blocks {
		if b.T == nil {
			b.P = nil
			continue
		}
		b.stale = true
		b.T.cache = nil
		for _, r := range b.T.Rows {
			for _, cell := range r.Cells {
				cell.stack = nil
				cell.C.clearLaid()
			}
		}
	}
}

// mathText is the words of an equation, for its placeholder.
func mathText(n *ox.Node) string {
	var b strings.Builder
	for _, t := range n.All("m:t") {
		b.WriteString(t.InnerText())
		if b.Len() > 40 {
			break
		}
	}
	if b.Len() == 0 {
		return "equation"
	}
	return b.String()
}
