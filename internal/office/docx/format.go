package docx

import (
	"strconv"

	"github.com/btelolabs/btelo-forge/internal/office/flow"
	"github.com/btelolabs/btelo-forge/internal/office/ox"
)

// Formatting: changes to w:rPr and w:pPr, with every child put where the schema wants it.

// RunChange is a change to runs; nil fields stay as they are.
type RunChange struct {
	Bold, Italic, Underline, Strike *bool
	Family                          *string
	Size                            *float32
	Color                           *string // RRGGBB, or "" for automatic
	Highlight                       *string // a highlight name, or "" for none
	Vert                            *int8
	Clear                           bool // all direct formatting goes
}

// Pending is formatting chosen with nothing selected: it applies to what is typed next at that place.
type Pending struct {
	At Pos
	C  RunChange
}

func merged(a, b RunChange) RunChange {
	if b.Bold != nil {
		a.Bold = b.Bold
	}
	if b.Italic != nil {
		a.Italic = b.Italic
	}
	if b.Underline != nil {
		a.Underline = b.Underline
	}
	if b.Strike != nil {
		a.Strike = b.Strike
	}
	if b.Family != nil {
		a.Family = b.Family
	}
	if b.Size != nil {
		a.Size = b.Size
	}
	if b.Color != nil {
		a.Color = b.Color
	}
	if b.Highlight != nil {
		a.Highlight = b.Highlight
	}
	if b.Vert != nil {
		a.Vert = b.Vert
	}
	a.Clear = a.Clear || b.Clear
	return a
}

// setProp puts an element among the rPr's children in schema order, replacing what is there.
func setProp(pr *ox.Node, order []string, name string, attrs ...string) *ox.Node {
	e := pr.Child(name)
	if e == nil {
		e = ox.New(name)
		pr.InsertOrdered(e, order)
	}
	e.Attrs = nil
	for i := 0; i+1 < len(attrs); i += 2 {
		e.SetAttr(attrs[i], attrs[i+1])
	}
	return e
}

func dropProp(pr *ox.Node, names ...string) {
	for _, name := range names {
		if e := pr.Child(name); e != nil {
			e.Remove()
		}
	}
}

// ensureRPr is the run's w:rPr, always its first child.
func ensureRPr(r *ox.Node) *ox.Node {
	if n := r.Child("w:rPr"); n != nil {
		return n
	}
	n := ox.New("w:rPr")
	r.Insert(0, n)
	return n
}

// applyChange makes the change in rPr. inherited is what the styles under the run give, so that switching a thing
// off where a style has it on is written out.
func applyChange(pr *ox.Node, ch *RunChange, inherited *RP) {
	if ch.Clear {
		for _, k := range append([]*ox.Node(nil), pr.Children...) {
			if k.Kind != ox.Element || k.Name != "w:rPrChange" {
				k.Remove()
			}
		}
	}
	flag := func(v *bool, base bool, names ...string) {
		if v == nil {
			return
		}
		dropProp(pr, names...)
		switch {
		case *v && !base:
			for _, n := range names {
				setProp(pr, orderRPr, n)
			}
		case !*v && base:
			for _, n := range names {
				setProp(pr, orderRPr, n, "w:val", "0")
			}
		}
	}
	flag(ch.Bold, inherited.Bold, "w:b", "w:bCs")
	flag(ch.Italic, inherited.Italic, "w:i", "w:iCs")
	flag(ch.Strike, inherited.Strike, "w:strike")
	if ch.Underline != nil {
		switch {
		case *ch.Underline:
			setProp(pr, orderRPr, "w:u", "w:val", "single")
		case inherited.Under != "":
			setProp(pr, orderRPr, "w:u", "w:val", "none")
		default:
			dropProp(pr, "w:u")
		}
	}
	if ch.Family != nil {
		dropProp(pr, "w:rFonts")
		if f := *ch.Family; f != "" {
			setProp(pr, orderRPr, "w:rFonts", "w:ascii", f, "w:hAnsi", f, "w:eastAsia", f, "w:cs", f)
		}
	}
	if ch.Size != nil {
		v := strconv.Itoa(int(*ch.Size*2 + 0.5))
		setProp(pr, orderRPr, "w:sz", "w:val", v)
		setProp(pr, orderRPr, "w:szCs", "w:val", v)
	}
	if ch.Color != nil {
		if *ch.Color == "" {
			dropProp(pr, "w:color")
		} else {
			setProp(pr, orderRPr, "w:color", "w:val", *ch.Color)
		}
	}
	if ch.Highlight != nil {
		if *ch.Highlight == "" {
			dropProp(pr, "w:highlight")
		} else {
			setProp(pr, orderRPr, "w:highlight", "w:val", *ch.Highlight)
		}
	}
	if ch.Vert != nil {
		switch *ch.Vert {
		case 1:
			setProp(pr, orderRPr, "w:vertAlign", "w:val", "superscript")
		case -1:
			setProp(pr, orderRPr, "w:vertAlign", "w:val", "subscript")
		default:
			dropProp(pr, "w:vertAlign")
		}
	}
}

// FormatRuns changes the selection's look; with nothing chosen it holds the change for what is typed next.
func (d *Doc) FormatRuns(ch RunChange) {
	if !d.editable() {
		return
	}
	if d.Sel.Empty() {
		if d.Pending != nil && d.Pending.At == d.Sel.Caret {
			d.Pending.C = merged(d.Pending.C, ch)
		} else {
			d.Pending = &Pending{At: d.Sel.Caret, C: ch}
		}
		d.rev++ // the toolbar shows it
		return
	}
	d.Begin("")
	defer d.Commit()
	a, c := d.Sel.Range()
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
		d.formatIn(b, from, to, &ch)
	}
}

// formatIn changes the runs of places from to to in a paragraph.
func (d *Doc) formatIn(b *Block, from, to int, ch *RunChange) {
	if from >= to {
		return
	}
	d.saveBlock(b)
	d.splitRunAt(b, from)
	d.splitRunAt(b, to)
	p := b.para()
	done := map[*ox.Node]bool{}
	base := d.Styles.Para(b.In.Base, p.PP.Style).R
	for i := range p.Segs {
		s := &p.Segs[i]
		if s.Locked && s.Kind != SegAtom || s.End() <= from || s.Start >= to || s.R.Name != "w:r" || done[s.R] {
			continue
		}
		done[s.R] = true
		under := base
		if rs := s.R.Child("w:rPr").Child("w:rStyle"); rs != nil {
			d.Styles.Char(&under, rs.Attr("w:val"))
		}
		pr := ensureRPr(s.R)
		applyChange(pr, ch, &under)
		if len(pr.Children) == 0 {
			pr.Remove()
		}
	}
	d.finish(b)
}

// typeStyled adds words in a run of their own with the pending look.
func (d *Doc) typeStyled(c Pos, s string, pend *Pending) {
	b := c.B
	d.splitRunAt(b, c.Off)
	r := d.runAfter(b, c.Off)
	under := d.Styles.Para(b.In.Base, b.para().PP.Style).R
	pr := ensureRPr(r)
	applyChange(pr, &pend.C, &under)
	if len(pr.Children) == 0 {
		pr.Remove()
	}
	d.reread(b)
	setT(r.Child("w:t"), s)
	d.reread(b)
	off := c.Off + len([]rune(s))
	d.finish(b)
	at := Pos{B: b, Off: off}
	d.Sel = Selection{Anchor: at, Caret: at}
	d.Pending = nil
}

// runAfter adds a run at a run edge, as a sibling of the run before it, with the same properties and a w:t.
func (d *Doc) runAfter(b *Block, off int) *ox.Node {
	p := b.para()
	left, right := p.segAt(off)
	r := ox.New("w:r")
	t := ox.New("w:t")
	switch {
	case left != nil && left.R.Name == "w:r" && !left.Locked:
		copyProps(left.R, r)
		left.R.Parent.Insert(left.R.Parent.Index(left.R)+1, r)
	case right != nil && right.R.Name == "w:r" && !right.Locked:
		copyProps(right.R, r)
		right.R.Parent.Insert(right.R.Parent.Index(right.R), r)
	default:
		if rp := markRPr(b.Node); rp != nil {
			r.Append(rp)
		}
		if left != nil {
			c := topChild(b.Node, left.R)
			b.Node.Insert(b.Node.Index(c)+1, r)
		} else {
			b.Node.Append(r)
		}
	}
	r.Append(t)
	return r
}

// CaretRun is the look at the caret: of the character before it, or the paragraph mark in an empty paragraph, with
// what is pending laid over.
func (d *Doc) CaretRun() (rp RP) {
	c := d.Sel.Caret
	if c.B == nil {
		return rp
	}
	if !d.Sel.Empty() {
		c, _ = d.Sel.Range()
		c.Off++ // the first chosen character
	}
	p := c.B.para()
	rp = p.Mark
	left, right := p.segAt(c.Off)
	s := left
	if s == nil {
		s = right
	}
	if s != nil && s.R.Name == "w:r" {
		rp = d.Styles.Para(c.B.In.Base, p.PP.Style).R
		d.Styles.applyR(&rp, s.R.Child("w:rPr"))
	}
	if pend := d.Pending; pend != nil && pend.At == d.Sel.Caret {
		ch := pend.C
		if ch.Clear {
			rp = d.Styles.Para(c.B.In.Base, p.PP.Style).R
		}
		set := func(dst *bool, v *bool) {
			if v != nil {
				*dst = *v
			}
		}
		set(&rp.Bold, ch.Bold)
		set(&rp.Italic, ch.Italic)
		set(&rp.Strike, ch.Strike)
		if ch.Underline != nil {
			rp.Under = ""
			if *ch.Underline {
				rp.Under = "single"
			}
		}
		if ch.Family != nil {
			rp.Ascii, rp.HAnsi, rp.East = *ch.Family, *ch.Family, *ch.Family
		}
		if ch.Size != nil {
			rp.Size = *ch.Size
		}
		if ch.Vert != nil {
			rp.Vert = *ch.Vert
		}
		if ch.Color != nil {
			rp.Color = hexColor(*ch.Color, rp.Color)
		}
		if ch.Highlight != nil {
			rp.Highlight = highlightColor(*ch.Highlight)
		}
	}
	return rp
}

// CaretPara is the properties of the caret's paragraph.
func (d *Doc) CaretPara() *Para {
	if d.Sel.Caret.B == nil {
		return nil
	}
	return d.Sel.Caret.B.para()
}

// chosen paragraphs of the selection.
func (d *Doc) chosenParas() []*Block {
	a, c := d.Sel.Range()
	st := a.B.In.Story
	return st.flat[a.B.Flat : c.B.Flat+1]
}

// formatParas runs fn on the pPr of each chosen paragraph, in one step.
func (d *Doc) formatParas(fn func(b *Block, pPr *ox.Node)) {
	if !d.editable() {
		return
	}
	d.Begin("")
	defer d.Commit()
	for _, b := range append([]*Block(nil), d.chosenParas()...) {
		d.saveBlock(b)
		pPr := ensurePPr(b.Node)
		fn(b, pPr)
		if len(pPr.Children) == 0 {
			pPr.Remove()
		}
		d.finish(b)
	}
}

// SetStyle gives the chosen paragraphs a style.
func (d *Doc) SetStyle(id string) {
	if !d.editable() {
		return
	}
	d.Begin("")
	defer d.Commit()
	d.EnsureStyle(id)
	d.formatParas(func(b *Block, pPr *ox.Node) {
		if id == "" {
			dropProp(pPr, "w:pStyle")
		} else {
			setProp(pPr, orderPPr, "w:pStyle", "w:val", id)
		}
	})
	d.lay.numStale = true
}

// SetAlign sets the alignment.
func (d *Doc) SetAlign(a flow.Align) {
	name := map[flow.Align]string{flow.Left: "left", flow.Center: "center", flow.Right: "right", flow.Justify: "both"}[a]
	d.formatParas(func(b *Block, pPr *ox.Node) { setProp(pPr, orderPPr, "w:jc", "w:val", name) })
}

// SetLineSpacing sets lines of spacing as a multiple of single.
func (d *Doc) SetLineSpacing(m float32) {
	d.formatParas(func(b *Block, pPr *ox.Node) {
		e := pPr.Child("w:spacing")
		if e == nil {
			e = ox.New("w:spacing")
			pPr.InsertOrdered(e, orderPPr)
		}
		e.SetAttr("w:line", strconv.Itoa(int(m*240+0.5)))
		e.SetAttr("w:lineRule", "auto")
	})
}

// SetSpacing sets the space before and after, in points; a negative value leaves it.
func (d *Doc) SetSpacing(before, after float32) {
	d.formatParas(func(b *Block, pPr *ox.Node) {
		e := pPr.Child("w:spacing")
		if e == nil {
			e = ox.New("w:spacing")
			pPr.InsertOrdered(e, orderPPr)
		}
		if before >= 0 {
			e.SetAttr("w:before", twips(before))
			e.DelAttr("w:beforeLines")
		}
		if after >= 0 {
			e.SetAttr("w:after", twips(after))
			e.DelAttr("w:afterLines")
		}
	})
}

// SetIndent sets the paragraph's indents in points; NaN-free: a negative left leaves it.
func (d *Doc) SetIndent(left, first float32, setFirst bool) {
	d.formatParas(func(b *Block, pPr *ox.Node) {
		e := pPr.Child("w:ind")
		if e == nil {
			e = ox.New("w:ind")
			pPr.InsertOrdered(e, orderPPr)
		}
		if left >= 0 {
			e.SetAttr("w:left", twips(left))
			e.DelAttr("w:leftChars")
			e.DelAttr("w:start")
		}
		if setFirst {
			e.DelAttr("w:firstLine")
			e.DelAttr("w:hanging")
			e.DelAttr("w:firstLineChars")
			e.DelAttr("w:hangingChars")
			if first >= 0 {
				e.SetAttr("w:firstLine", twips(first))
			} else {
				e.SetAttr("w:hanging", twips(-first))
			}
		}
	})
}

// Indent moves the chosen paragraphs by one step in or out: a level in a list, a tab's width elsewhere.
func (d *Doc) Indent(in bool) {
	step := d.defTab
	d.formatParas(func(b *Block, pPr *ox.Node) {
		p := b.para()
		if p.PP.NumID != 0 && d.Num.level(p.PP.NumID, p.PP.Ilvl) != nil {
			lvl := p.PP.Ilvl + 1
			if !in {
				lvl = p.PP.Ilvl - 1
			}
			if lvl < 0 || lvl > 8 {
				return
			}
			num := pPr.Child("w:numPr")
			if num == nil {
				num = ox.New("w:numPr")
				pPr.InsertOrdered(num, orderPPr)
				num.Append(ox.New("w:numId", "w:val", strconv.Itoa(p.PP.NumID)))
			}
			if e := num.Child("w:ilvl"); e != nil {
				e.SetAttr("w:val", strconv.Itoa(lvl))
			} else {
				num.Insert(0, ox.New("w:ilvl", "w:val", strconv.Itoa(lvl)))
			}
			if ind := pPr.Child("w:ind"); ind != nil {
				ind.Remove() // the level's own indent shows
			}
			return
		}
		left := p.Flow.Left
		if in {
			left += step
		} else {
			left = max(left-step, 0)
		}
		e := pPr.Child("w:ind")
		if e == nil {
			e = ox.New("w:ind")
			pPr.InsertOrdered(e, orderPPr)
		}
		e.SetAttr("w:left", twips(left))
		e.DelAttr("w:leftChars")
		e.DelAttr("w:start")
	})
	d.lay.numStale = true
}

// listKindOf says what kind of list a paragraph is in, ok false for none.
func (d *Doc) listKindOf(p *Para) (ListKind, bool) {
	if p.PP.NumID == 0 {
		return 0, false
	}
	lvl := d.Num.level(p.PP.NumID, 0)
	if lvl == nil {
		return 0, false
	}
	switch {
	case lvl.Fmt == "bullet":
		return ListBullet, true
	case lvl.Fmt == "chineseCounting" || lvl.Fmt == "chineseCountingThousand":
		return ListChinese, true
	}
	if l1 := d.Num.level(p.PP.NumID, 1); l1 != nil && l1.Fmt == "decimal" && len(l1.Text) > 2 && l1.Text[:2] == "%1" {
		return ListMulti, true
	}
	return ListNumber, true
}

// CaretList says what kind of list the caret is in.
func (d *Doc) CaretList() (ListKind, bool) {
	p := d.CaretPara()
	if p == nil {
		return 0, false
	}
	return d.listKindOf(p)
}

// ToggleList puts the chosen paragraphs in a list of the kind, or takes them out when they are all in one.
func (d *Doc) ToggleList(kind ListKind) {
	if !d.editable() {
		return
	}
	bs := d.chosenParas()
	all := true
	for _, b := range bs {
		if k, ok := d.listKindOf(b.para()); !ok || k != kind {
			all = false
		}
	}
	d.Begin("")
	defer d.Commit()
	if all {
		d.formatParas(func(b *Block, pPr *ox.Node) {
			if e := pPr.Child("w:numPr"); e != nil {
				e.Remove()
			}
			dropProp(pPr, "w:ind")
			if b.para().PP.NumID != 0 && d.Styles.Para(b.In.Base, b.para().PP.Style).P.NumID != 0 {
				num := ox.New("w:numPr")
				num.Append(ox.New("w:numId", "w:val", "0"))
				pPr.InsertOrdered(num, orderPPr)
			}
		})
		d.lay.numStale = true
		return
	}
	// A list just above goes on; otherwise this one is new.
	numID := 0
	first := bs[0]
	if first.Flat > 0 {
		if prev := first.In.Story.flat[first.Flat-1]; prev.In == first.In {
			if k, ok := d.listKindOf(prev.para()); ok && k == kind {
				numID = prev.para().PP.NumID
			}
		}
	}
	if numID == 0 {
		numID = d.newList(kind)
	}
	if numID == 0 {
		return
	}
	// A new list may have changed the definitions, and with them the paragraphs' levels.
	d.formatParas(func(b *Block, pPr *ox.Node) {
		ilvl := "0"
		if old := pPr.Child("w:numPr").Child("w:ilvl"); old != nil {
			ilvl = old.Attr("w:val")
		}
		e := pPr.Child("w:numPr")
		if e != nil {
			e.Remove()
		}
		dropProp(pPr, "w:ind")
		num := ox.New("w:numPr")
		num.Append(ox.New("w:ilvl", "w:val", ilvl), ox.New("w:numId", "w:val", strconv.Itoa(numID)))
		pPr.InsertOrdered(num, orderPPr)
	})
	d.lay.numStale = true
}

// ClearFormatting takes all direct formatting from the selection, and with a whole paragraph its style too.
func (d *Doc) ClearFormatting() {
	if !d.editable() {
		return
	}
	d.Begin("")
	defer d.Commit()
	d.FormatRuns(RunChange{Clear: true})
	d.formatParas(func(b *Block, pPr *ox.Node) {
		for _, k := range append([]*ox.Node(nil), pPr.Children...) {
			if k.Kind == ox.Element && k.Name != "w:sectPr" && k.Name != "w:pPrChange" {
				k.Remove()
			}
		}
	})
	d.lay.numStale = true
}
