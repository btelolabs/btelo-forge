package docx

import (
	"strings"
	"unicode/utf8"

	"github.com/btelolabs/btelo-forge/internal/office/ox"
)

// Editing the trees: all changes to text go through here. A place in a paragraph is a caret place; segments say what
// XML lies under it, and runs are split where a change needs an edge. Nothing a change does not touch moves: bookmarks,
// comment marks, field marks and elements this package has never heard of stay among the runs.

// Pos is a caret place.
type Pos struct {
	B     *Block
	Off   int
	Trail bool // at the end of a wrapped line rather than the start of the next
}

// Selection is the selection; Anchor and Caret are the same when nothing is chosen.
type Selection struct{ Anchor, Caret Pos }

// Empty reports that nothing is chosen.
func (s Selection) Empty() bool { return s.Anchor.B == s.Caret.B && s.Anchor.Off == s.Caret.Off }

// less orders places in one story.
func less(a, b Pos) bool {
	return a.B.Flat < b.B.Flat || a.B.Flat == b.B.Flat && a.Off < b.Off
}

// Range is the selection in reading order.
func (s Selection) Range() (Pos, Pos) {
	if less(s.Caret, s.Anchor) {
		return s.Caret, s.Anchor
	}
	return s.Anchor, s.Caret
}

// SetCaret puts the caret at p; with extend the selection stretches to it.
func (d *Doc) SetCaret(p Pos, extend bool) {
	if extend && d.Sel.Anchor.B != nil && d.Sel.Anchor.B.In.Story == p.B.In.Story {
		d.Sel.Caret = p
		return
	}
	d.Sel = Selection{Anchor: p, Caret: p}
}

// setT writes a w:t's text, keeping its spaces.
func setT(t *ox.Node, s string) {
	t.SetText(s)
	if s != strings.TrimSpace(s) || strings.Contains(s, "  ") {
		t.SetAttr("xml:space", "preserve")
	}
}

// newT is a w:t holding s.
func newT(s string) *ox.Node {
	t := ox.New("w:t")
	setT(t, s)
	return t
}

// reread reads a paragraph's segments again after its tree changed, without telling the layout: that comes once, at
// the end of a change.
func (d *Doc) reread(b *Block) {
	label := ""
	if b.P != nil {
		label = b.P.Label
	}
	b.P = nil
	d.buildPara(b, nil)
	b.P.Label = label
}

// firstContent is the place in a run after its properties.
func firstContent(r *ox.Node) int {
	if len(r.Children) > 0 && r.Children[0].Kind == ox.Element && r.Children[0].Name == "w:rPr" {
		return 1
	}
	return 0
}

// topChild is the child of p that holds n.
func topChild(p, n *ox.Node) *ox.Node {
	for n != nil && n.Parent != p {
		n = n.Parent
	}
	return n
}

// segAt finds the segment before a place and the one after it.
func (p *Para) segAt(off int) (left, right *Seg) {
	for i := range p.Segs {
		s := &p.Segs[i]
		if s.N == 0 {
			continue
		}
		switch {
		case s.End() <= off:
			left = s
		case s.Start >= off:
			return left, s
		default:
			return s, s
		}
	}
	return left, nil
}

// newRunIn makes an empty run with the properties of rPr (nil for none).
func newRunIn(rPr *ox.Node) *ox.Node {
	r := ox.New("w:r")
	if rPr != nil {
		r.Append(rPr.Clone())
	}
	return r
}

// markRPr is a copy of the paragraph mark's properties as run properties, nil when it has none.
func markRPr(p *ox.Node) *ox.Node {
	rPr := p.Child("w:pPr").Child("w:rPr")
	if rPr == nil {
		return nil
	}
	c := rPr.Clone()
	for _, k := range c.Elements("w:ins", "w:del", "w:moveFrom", "w:moveTo", "w:rPrChange") {
		k.Remove()
	}
	if len(c.Elements()) == 0 {
		return nil
	}
	return c
}

// runPoint finds, or makes, the place in a run where a new child lands at off: the run and the child index.
func (d *Doc) runPoint(b *Block, off int) (*ox.Node, int) {
	p := b.para()
	left, right := p.segAt(off)
	if left != nil && left == right && left.Kind == SegText && !left.Locked {
		// Inside a text: it becomes two, with the place between them.
		rs := []rune(left.S)
		k := off - left.Start
		setT(left.T, string(rs[:k]))
		rest := newT(string(rs[k:]))
		i := left.R.Index(left.T)
		left.R.Insert(i+1, rest)
		d.reread(b)
		return left.R, i + 1
	}
	if left != nil && !left.Locked && left.R.Name == "w:r" {
		return left.R, left.R.Index(left.T) + 1
	}
	if right != nil && !right.Locked && right.R.Name == "w:r" {
		return right.R, right.R.Index(right.T)
	}
	// Beside something locked, or in an empty paragraph: a run of its own.
	r := newRunIn(markRPr(b.Node))
	switch {
	case left != nil:
		c := topChild(b.Node, left.R)
		b.Node.Insert(b.Node.Index(c)+1, r)
		if lr := left.R; lr.Name == "w:r" {
			copyProps(lr, r)
		}
	case right != nil:
		c := topChild(b.Node, right.R)
		b.Node.Insert(b.Node.Index(c), r)
		if rr := right.R; rr.Name == "w:r" {
			copyProps(rr, r)
		}
	default:
		b.Node.Append(r)
	}
	return r, firstContent(r)
}

// copyProps gives run to the properties of run from.
func copyProps(from, to *ox.Node) {
	if p := to.Child("w:rPr"); p != nil {
		p.Remove()
	}
	if p := from.Child("w:rPr"); p != nil {
		to.Insert(0, p.Clone())
	}
}

// putText adds plain words, without tabs or breaks, at off.
func (d *Doc) putText(b *Block, off int, s string) {
	p := b.para()
	left, right := p.segAt(off)
	switch {
	case left != nil && left.Kind == SegText && !left.Locked:
		rs := []rune(left.S)
		k := off - left.Start
		setT(left.T, string(rs[:k])+s+string(rs[k:]))
	case right != nil && right.Kind == SegText && !right.Locked && right.Start == off:
		setT(right.T, s+right.S)
	default:
		r, i := d.runPoint(b, off)
		r.Insert(i, newT(s))
	}
	d.reread(b)
}

// putNode adds a child of a run at off: a tab, a break, a drawing.
func (d *Doc) putNode(b *Block, off int, n *ox.Node) {
	r, i := d.runPoint(b, off)
	r.Insert(i, n)
	d.reread(b)
}

// insertPlain adds text with tabs at off, in one paragraph, and returns the place after it.
func (d *Doc) insertPlain(b *Block, off int, s string) int {
	for len(s) > 0 {
		i := strings.IndexByte(s, '\t')
		part := s
		if i >= 0 {
			part = s[:i]
		}
		if part != "" {
			d.putText(b, off, part)
			off += utf8.RuneCountInString(part)
		}
		if i < 0 {
			break
		}
		d.putNode(b, off, ox.New("w:tab"))
		off++
		s = s[i+1:]
	}
	return off
}

func cleanText(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	return strings.Map(func(r rune) rune {
		if r < 0x20 && r != '\n' && r != '\t' || r == 0xfffc || r == 0xfffe || r == 0xffff {
			return -1
		}
		return r
	}, s)
}

// editable reports whether the caret's story can be changed.
func (d *Doc) editable() bool {
	b := d.Sel.Caret.B
	return b != nil && !b.In.Story.ReadOnly
}

// finish tells the layout a paragraph's change is complete.
func (d *Doc) finish(b *Block) {
	d.touched(b.In.Story.Part)
	d.changed(b)
}

// Type puts typed words at the caret, in place of the selection; words typed one after another undo together.
func (d *Doc) Type(s string) {
	if !d.editable() || s == "" {
		return
	}
	d.Begin("type")
	defer d.Commit()
	d.insertAtCaret(s)
}

// Insert puts text at the caret in place of the selection, as one step: new lines make paragraphs.
func (d *Doc) Insert(s string) {
	if !d.editable() || s == "" {
		return
	}
	d.Begin("")
	defer d.Commit()
	d.insertAtCaret(s)
}

func (d *Doc) insertAtCaret(s string) {
	s = cleanText(s)
	if !d.Sel.Empty() {
		d.deleteSelection()
	}
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		if i > 0 {
			d.splitParagraph()
		}
		c := d.Sel.Caret
		if line == "" {
			continue
		}
		d.saveBlock(c.B)
		if pend := d.Pending; pend != nil && pend.At == c {
			// The look goes with the words up to a tab; after that the new run's look carries on by itself.
			head, rest, _ := strings.Cut(line, "	")
			if head != "" {
				d.typeStyled(c, head, pend)
				c = d.Sel.Caret
				if !strings.Contains(line, "	") {
					continue
				}
				line = "	" + rest
			}
		}
		off := d.insertPlain(c.B, c.Off, line)
		d.finish(c.B)
		d.Sel = Selection{Anchor: Pos{B: c.B, Off: off}, Caret: Pos{B: c.B, Off: off}}
	}
}

// deleteIn removes places a to c of a paragraph, except what is locked.
func (d *Doc) deleteIn(b *Block, a, c int) {
	if a >= c {
		return
	}
	p := b.para()
	// Whole fields go whole: a field left with no result would come back at the next update.
	for i := len(p.Fields) - 1; i >= 0; i-- {
		f := &p.Fields[i]
		if f.From >= a && f.To <= c && f.To > f.From && f.Begin.Parent == f.End.Parent && f.Begin.Parent != nil {
			parent := f.Begin.Parent
			from, to := parent.Index(f.Begin), parent.Index(f.End)
			for k := to; k >= from; k-- {
				parent.Children[k].Remove()
			}
		}
	}
	d.reread(b)
	p = b.para()
	had := firstRPr(p, a, c)
	touched := map[*ox.Node]bool{}
	for i := len(p.Segs) - 1; i >= 0; i-- {
		s := &p.Segs[i]
		if s.Locked || s.End() <= a || s.Start >= c || s.N == 0 {
			continue
		}
		touched[s.R] = true
		from, to := max(a, s.Start)-s.Start, min(c, s.End())-s.Start
		if s.Kind == SegText {
			rs := []rune(s.S)
			if from == 0 && to == len(rs) {
				s.T.Remove()
			} else {
				setT(s.T, string(rs[:from])+string(rs[to:]))
			}
			continue
		}
		s.T.Remove()
	}
	for r := range touched {
		removeEmpty(r, b.Node)
	}
	d.reread(b)
	if b.P.Len == 0 && had != nil {
		// An emptied paragraph keeps the look of its words for what is typed next.
		pPr := ensurePPr(b.Node)
		if old := pPr.Child("w:rPr"); old != nil {
			old.Remove()
		}
		pPr.InsertOrdered(had, orderPPr)
		d.reread(b)
	}
}

// firstRPr is a copy of the properties of the first run in a stretch, nil when it has none.
func firstRPr(p *Para, a, c int) *ox.Node {
	for i := range p.Segs {
		s := &p.Segs[i]
		if s.End() > a && s.Start < c && s.R.Name == "w:r" {
			if r := s.R.Child("w:rPr"); r != nil {
				return r.Clone()
			}
			return nil
		}
	}
	return nil
}

// removeEmpty takes away a run left with nothing, and a hyperlink or the like left with no runs.
func removeEmpty(r, para *ox.Node) {
	n := r
	for n != nil && n != para {
		parent := n.Parent
		if n.Name == "w:r" {
			if len(n.Elements()) > firstContent(n) {
				return
			}
		} else if n.Name == "w:hyperlink" || n.Name == "w:smartTag" {
			if len(n.Elements()) > 0 {
				return
			}
		} else {
			return
		}
		n.Remove()
		n = parent
	}
}

// ensurePPr is the paragraph's w:pPr, made when missing: it is always the first child.
func ensurePPr(p *ox.Node) *ox.Node {
	if n := p.Child("w:pPr"); n != nil {
		return n
	}
	n := ox.New("w:pPr")
	p.Insert(0, n)
	return n
}

// deleteSelection removes what is chosen and puts the caret where it was.
func (d *Doc) deleteSelection() {
	a, c := d.Sel.Range()
	if a.B == c.B {
		d.saveBlock(a.B)
		d.deleteIn(a.B, a.Off, c.Off)
		d.finish(a.B)
		d.Sel = Selection{Anchor: Pos{B: a.B, Off: a.Off}, Caret: Pos{B: a.B, Off: a.Off}}
		return
	}
	if a.B.In != c.B.In {
		// Across cells, or out of a table: words go, the table stays.
		st := a.B.In.Story
		for i := a.B.Flat; i <= c.B.Flat; i++ {
			b := st.flat[i]
			from, to := 0, b.para().Len
			if b == a.B {
				from = a.Off
			}
			if b == c.B {
				to = c.Off
			}
			d.saveBlock(b)
			d.deleteIn(b, from, to)
			d.finish(b)
		}
		d.Sel = Selection{Anchor: Pos{B: a.B, Off: a.Off}, Caret: Pos{B: a.B, Off: a.Off}}
		return
	}
	// In one container: what lies between goes, and the two ends join.
	an, cn := a.B.Node, c.B.Node
	blocks := a.B.In.Blocks
	var between []*Block
	for i := a.B.Index + 1; i < c.B.Index; i++ {
		between = append(between, blocks[i])
	}
	d.saveSpan(blocks[a.B.Index : c.B.Index+1])
	d.deleteIn(a.B, a.Off, a.B.para().Len)
	d.deleteIn(c.B, 0, c.Off)
	for _, b := range between {
		b.Node.Remove()
	}
	off := a.Off
	keep := d.join(a.B, c.B)
	d.structural(a.B)
	d.rebuild()
	d.dirtyNodes(an, cn)
	d.placeNode(a.B.In.Story, keep, off)
}

// join puts paragraph c's content on the end of paragraph a and removes c. An empty a goes instead, c keeping its own
// look, as in Word. It returns the paragraph left.
func (d *Doc) join(a, c *Block) *ox.Node {
	empty := true
	for _, k := range a.Node.Elements() {
		if k.Name != "w:pPr" {
			empty = false
			break
		}
	}
	if empty && a.Node.Parent != nil {
		// A section break in the paragraph that goes stays in what is left only when it was the later one's.
		a.Node.Remove()
		return c.Node
	}
	for _, k := range append([]*ox.Node(nil), c.Node.Children...) {
		if k.Kind == ox.Element && k.Name == "w:pPr" {
			continue
		}
		k.Remove()
		a.Node.Append(k)
	}
	aPr := a.Node.Child("w:pPr")
	if s := aPr.Child("w:sectPr"); s != nil {
		s.Remove() // the break between them is what went
		d.lay.sectsNew = true
	}
	if s := c.Node.Child("w:pPr").Child("w:sectPr"); s != nil {
		s.Remove()
		ensurePPr(a.Node).InsertOrdered(s, orderPPr)
	}
	c.Node.Remove()
	return a.Node
}

// structural says blocks came or went around b.
func (d *Doc) structural(b *Block) {
	d.touched(b.In.Story.Part)
	b.markStale()
	d.lay.dirty(b)
}

// dirtyNodes tells the layout that these elements, as the tree now has them, changed. Those no longer in it are
// passed over.
func (d *Doc) dirtyNodes(st ...*ox.Node) {
	for _, s := range d.allStories() {
		for _, n := range st {
			if b := s.byNode[n]; b != nil {
				if b.T != nil {
					b = b.firstPara()
				}
				if b.T == nil {
					d.reread(b)
				}
				d.lay.dirty(b)
			}
		}
	}
}

// placeNode puts the caret in the paragraph that is element n.
func (d *Doc) placeNode(s *Story, n *ox.Node, off int) {
	b := s.byNode[n]
	if b == nil || b.T != nil {
		return
	}
	p := Pos{B: b, Off: min(off, b.para().Len)}
	d.Sel = Selection{Anchor: p, Caret: p}
}

// DeleteSelection removes what is chosen.
func (d *Doc) DeleteSelection() {
	if !d.editable() || d.Sel.Empty() {
		return
	}
	d.Begin("")
	defer d.Commit()
	d.deleteSelection()
}

// Backspace deletes before the caret, or the selection; with word, back to the start of the word.
func (d *Doc) Backspace(word bool) {
	if !d.editable() {
		return
	}
	if !d.Sel.Empty() {
		d.DeleteSelection()
		return
	}
	c := d.Sel.Caret
	b := c.B
	if c.Off == 0 {
		d.joinBack(b)
		return
	}
	from := c.Off - 1
	if word {
		from = wordStart(b, c.Off)
	} else if left, _ := b.para().segAt(c.Off); left != nil && left.Locked {
		d.Sel = Selection{Anchor: Pos{B: b, Off: left.Start}, Caret: Pos{B: b, Off: left.Start}}
		return // locked text is stepped over
	}
	kind := "delete"
	if word {
		kind = ""
	}
	d.Begin(kind)
	defer d.Commit()
	d.saveBlock(b)
	d.deleteIn(b, from, c.Off)
	d.finish(b)
	d.Sel = Selection{Anchor: Pos{B: b, Off: from}, Caret: Pos{B: b, Off: from}}
}

// joinBack is Backspace at a paragraph's start.
func (d *Doc) joinBack(b *Block) {
	pp := &b.para().PP
	direct := b.Node.Child("w:pPr")
	if pp.NumID != 0 && direct.Child("w:numPr") != nil {
		// At the start of a list item Backspace ends the list there first.
		d.Begin("")
		defer d.Commit()
		d.saveBlock(b)
		direct.Child("w:numPr").Remove()
		d.finish(b)
		return
	}
	if b.Flat == 0 || b.Index == 0 {
		return
	}
	prev := b.In.Blocks[b.Index-1]
	if prev.T != nil {
		// Off the top of a paragraph under a table: the caret goes in; an empty paragraph goes.
		last := prev.T.lastPara()
		if b.para().Len == 0 && b.Index+1 < len(b.In.Blocks) {
			d.Begin("")
			defer d.Commit()
			next := b.In.Blocks[b.Index+1]
			d.saveBlock(b)
			b.Node.Remove()
			d.structural(next)
			d.rebuild()
			d.dirtyNodes(next.Node)
		}
		if last != nil {
			p := Pos{B: d.current(Pos{B: last}).B, Off: last.para().Len}
			d.Sel = Selection{Anchor: p, Caret: p}
		}
		return
	}
	d.joinParas(prev, b)
}

// joinParas makes paragraphs a and the one after it, b, one, with the caret where they meet.
func (d *Doc) joinParas(a, b *Block) {
	d.Begin("")
	defer d.Commit()
	off := a.para().Len
	d.saveBlock(a)
	d.saveBlock(b)
	an, cn := a.Node, b.Node
	s := b.In.Story
	keep := d.join(a, b)
	d.structural(a)
	d.rebuild()
	d.dirtyNodes(an, cn)
	d.placeNode(s, keep, off)
}

// lastPara is the last paragraph of a table.
func (t *Table) lastPara() *Block {
	for ri := len(t.Rows) - 1; ri >= 0; ri-- {
		cells := t.Rows[ri].Cells
		for ci := len(cells) - 1; ci >= 0; ci-- {
			bs := cells[ci].C.Blocks
			for i := len(bs) - 1; i >= 0; i-- {
				if bs[i].T == nil {
					return bs[i]
				}
				if p := bs[i].T.lastPara(); p != nil {
					return p
				}
			}
		}
	}
	return nil
}

// Delete deletes after the caret, or the selection; with word, to the end of the word.
func (d *Doc) Delete(word bool) {
	if !d.editable() {
		return
	}
	if !d.Sel.Empty() {
		d.DeleteSelection()
		return
	}
	c := d.Sel.Caret
	b := c.B
	p := b.para()
	if c.Off >= p.Len {
		if b.Index+1 >= len(b.In.Blocks) {
			return
		}
		next := b.In.Blocks[b.Index+1]
		if next.T != nil {
			if p.Len == 0 {
				// An empty paragraph above a table goes; the caret moves into the table.
				d.Begin("")
				defer d.Commit()
				d.saveBlock(b)
				b.Node.Remove()
				d.structural(next)
				d.rebuild()
				d.dirtyNodes(next.Node)
				if nb := d.Body.byNode[next.Node]; nb != nil {
					at := Pos{B: nb.firstPara()}
					d.Sel = Selection{Anchor: at, Caret: at}
				}
				return
			}
			at := Pos{B: next.firstPara()}
			d.Sel = Selection{Anchor: at, Caret: at}
			return
		}
		d.joinParas(b, next)
		return
	}
	to := c.Off + 1
	if word {
		to = wordEnd(b, c.Off)
	} else if _, right := p.segAt(c.Off); right != nil && right.Locked {
		d.Sel = Selection{Anchor: Pos{B: b, Off: right.End()}, Caret: Pos{B: b, Off: right.End()}}
		return
	}
	kind := "delete"
	if word {
		kind = ""
	}
	d.Begin(kind)
	defer d.Commit()
	d.saveBlock(b)
	d.deleteIn(b, c.Off, to)
	d.finish(b)
	d.Sel = Selection{Anchor: Pos{B: b, Off: c.Off}, Caret: Pos{B: b, Off: c.Off}}
}

// splitRunAt makes a run edge at off, leaving wrappers such as a hyperlink alone.
func (d *Doc) splitRunAt(b *Block, off int) {
	p := b.para()
	left, right := p.segAt(off)
	if left != nil && left == right && left.Kind == SegText {
		rs := []rune(left.S)
		k := off - left.Start
		setT(left.T, string(rs[:k]))
		left.R.Insert(left.R.Index(left.T)+1, newT(string(rs[k:])))
		d.reread(b)
		p = b.para()
		_, right = p.segAt(off)
	}
	if right == nil || right.R.Name != "w:r" || right.Start != off {
		return
	}
	r := right.R
	i := r.Index(right.T)
	if i <= firstContent(r) {
		return // already at the run's start
	}
	if !hasShown(r, i) {
		return
	}
	next := ox.New("w:r")
	next.Attrs = append(next.Attrs, r.Attrs...)
	if rPr := r.Child("w:rPr"); rPr != nil {
		next.Append(rPr.Clone())
	}
	for _, c := range append([]*ox.Node(nil), r.Children[i:]...) {
		c.Remove()
		next.Append(c)
	}
	r.Parent.Insert(r.Parent.Index(r)+1, next)
	d.reread(b)
}

// hasShown reports that the run has something before child i.
func hasShown(r *ox.Node, i int) bool {
	for _, c := range r.Children[firstContent(r):i] {
		if c.Kind == ox.Element {
			return true
		}
	}
	return false
}

// splitUp makes the child node of p start a top-level child, dividing the wrappers around it, and returns that child.
func splitUp(n, p *ox.Node) *ox.Node {
	for n.Parent != p {
		w := n.Parent
		copyW := ox.New(w.Name)
		copyW.Attrs = append(copyW.Attrs, w.Attrs...)
		// What wraps a content control's content is its properties too.
		holder := w
		i := holder.Index(n)
		if i == firstContent(holder) && i == 0 {
			n = holder
			continue
		}
		for _, c := range append([]*ox.Node(nil), holder.Children[i:]...) {
			c.Remove()
			copyW.Append(c)
		}
		if w.Name == "w:sdtContent" {
			sdt := w.Parent
			nw := ox.New("w:sdt")
			if pr := sdt.Child("w:sdtPr"); pr != nil {
				c := pr.Clone()
				if id := c.Child("w:id"); id != nil {
					id.Remove()
				}
				nw.Append(c)
			}
			nw.Append(copyW)
			sdt.Parent.Insert(sdt.Parent.Index(sdt)+1, nw)
			n = nw
			continue
		}
		w.Parent.Insert(w.Parent.Index(w)+1, copyW)
		n = copyW
	}
	return n
}

// cutAt divides the paragraph at off and returns the index of the first child after the cut.
func (d *Doc) cutAt(b *Block, off int) int {
	d.splitRunAt(b, off)
	p := b.para()
	_, right := p.segAt(off)
	if right == nil || right.Start != off {
		// At the end: the run edges that are not words stay with the first part.
		left, _ := p.segAt(off)
		if left == nil {
			return len(b.Node.Children)
		}
		return b.Node.Index(topChild(b.Node, left.R)) + 1
	}
	top := splitUp(right.R, b.Node)
	d.reread(b)
	return b.Node.Index(top)
}

// freshP is a new paragraph with the properties of src as Enter passes them on.
func freshP(src *ox.Node) *ox.Node {
	n := ox.New("w:p")
	if pPr := src.Child("w:pPr"); pPr != nil {
		c := pPr.Clone()
		for _, k := range c.Elements("w:sectPr", "w:pPrChange") {
			k.Remove()
		}
		if len(c.Children) > 0 {
			n.Append(c)
		}
	}
	return n
}

// splitParagraph is Enter's work, in a step already open.
func (d *Doc) splitParagraph() {
	c := d.Sel.Caret
	b := c.B
	st := b.In.Story
	d.saveBlock(b)
	p := b.para()
	nw := freshP(b.Node)
	// What the style says comes next applies at a paragraph's end.
	if c.Off >= p.Len && p.Len > 0 {
		if s := d.Styles.ByID[p.PP.Style]; s != nil && s.Next != "" && s.Next != s.ID {
			pr := ensurePPr(nw)
			if e := pr.Child("w:pStyle"); e != nil {
				e.SetAttr("w:val", s.Next)
			} else {
				pr.InsertOrdered(ox.New("w:pStyle", "w:val", s.Next), orderPPr)
			}
			if e := pr.Child("w:numPr"); e != nil {
				e.Remove()
			}
		}
	}
	at := d.cutAt(b, c.Off)
	for _, k := range append([]*ox.Node(nil), b.Node.Children[min(at, len(b.Node.Children)):]...) {
		if k.Kind == ox.Element && k.Name == "w:pPr" {
			continue
		}
		k.Remove()
		nw.Append(k)
	}
	// A section break belongs to the paragraph that now ends the section.
	if s := b.Node.Child("w:pPr").Child("w:sectPr"); s != nil {
		s.Remove()
		ensurePPr(nw).InsertOrdered(s, orderPPr)
	}
	parent := b.Node.Parent
	parent.Insert(parent.Index(b.Node)+1, nw)
	d.structural(b)
	d.rebuild()
	d.dirtyNodes(b.Node, nw)
	d.placeNode(st, nw, 0)
}

// Enter breaks the paragraph at the caret. In an empty list item it ends the list instead.
func (d *Doc) Enter() {
	if !d.editable() {
		return
	}
	d.Begin("")
	defer d.Commit()
	if !d.Sel.Empty() {
		d.deleteSelection()
	}
	c := d.Sel.Caret
	p := c.B.para()
	if p.Len == 0 && p.PP.NumID != 0 {
		if n := c.B.Node.Child("w:pPr").Child("w:numPr"); n != nil {
			d.saveBlock(c.B)
			n.Remove()
			d.finish(c.B)
			return
		}
	}
	d.splitParagraph()
}

// LineBreak puts a line break at the caret.
func (d *Doc) LineBreak() { d.breakOf(ox.New("w:br")) }

// PageBreak puts a page break at the caret.
func (d *Doc) PageBreak() { d.breakOf(ox.New("w:br", "w:type", "page")) }

func (d *Doc) breakOf(n *ox.Node) {
	if !d.editable() {
		return
	}
	d.Begin("")
	defer d.Commit()
	if !d.Sel.Empty() {
		d.deleteSelection()
	}
	c := d.Sel.Caret
	d.saveBlock(c.B)
	d.putNode(c.B, c.Off, n)
	d.finish(c.B)
	at := Pos{B: c.B, Off: c.Off + 1}
	d.Sel = Selection{Anchor: at, Caret: at}
}

func isWordRune(r rune) bool {
	return r == '_' || r == '-' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r > 0x7f && !isCJKPunct(r) && r != 0xfffc && r != 0xa0 && r != 0x3000
}

func isCJKPunct(r rune) bool {
	return r >= 0x3000 && r <= 0x303f || r >= 0xff00 && r <= 0xff0f || r >= 0xff1a && r <= 0xff20 || r >= 0x2010 && r <= 0x2027
}

// sameClass reports that two characters belong to one word. Chinese has no spaces: each script run counts as a word
// only as far as a pair of characters; a run of Han characters is one for double-clicks as in Word's coarse rule.
func wordStart(b *Block, i int) int {
	rs := []rune(b.Text())
	i = min(i, len(rs))
	for i > 0 && !isWordRune(rs[i-1]) {
		i--
	}
	for i > 0 && isWordRune(rs[i-1]) {
		i--
	}
	return i
}

func wordEnd(b *Block, i int) int {
	rs := []rune(b.Text())
	for i < len(rs) && !isWordRune(rs[i]) {
		i++
	}
	for i < len(rs) && isWordRune(rs[i]) {
		i++
	}
	return i
}

// WordAround is the word at a place.
func WordAround(b *Block, i int) (int, int) {
	rs := []rune(b.Text())
	x, y := min(i, len(rs)), min(i, len(rs))
	for x > 0 && isWordRune(rs[x-1]) {
		x--
	}
	for y < len(rs) && isWordRune(rs[y]) {
		y++
	}
	if x == y && y < len(rs) {
		y++
	}
	return x, y
}

// WordStart and WordEnd are where Ctrl+arrows go in a paragraph.
func WordStart(b *Block, i int) int { return wordStart(b, i) }

// WordEnd is where Ctrl+Right goes in a paragraph.
func WordEnd(b *Block, i int) int { return wordEnd(b, i) }
