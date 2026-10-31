package docx

import "github.com/btelolabs/btelo-forge/internal/office/ox"

// PageSetup is a section's paper and margins, in points.
type PageSetup struct {
	W, H           float32
	MT, MB, ML, MR float32
}

// Landscape reports that the page is wider than it is tall.
func (p PageSetup) Landscape() bool { return p.W > p.H }

// Turned is the page the other way round, its margins going with it; turned twice it is as it was.
func (p PageSetup) Turned() PageSetup {
	return PageSetup{W: p.H, H: p.W, MT: p.ML, MB: p.MR, ML: p.MT, MR: p.MB}
}

// minPageText is the least a page keeps between its margins, in points; less and no line fits.
const minPageText = 36

// Fit keeps the setup within what a page can be: a size Word accepts, and room for text between the margins.
func (p PageSetup) Fit() PageSetup {
	p.W, p.H = min(max(p.W, 72), maxPageSide), min(max(p.H, 72), maxPageSide)
	shrink := func(a, b *float32, side float32) {
		*a, *b = max(*a, 0), max(*b, 0)
		if room := side - minPageText; *a+*b > room {
			k := room / (*a + *b)
			*a, *b = *a*k, *b*k
		}
	}
	shrink(&p.MT, &p.MB, p.H)
	shrink(&p.ML, &p.MR, p.W)
	return p
}

// SectionAtCaret is the section the caret is in; in a header, footer or table it is the one of the block around it.
func (d *Doc) SectionAtCaret() *Section {
	if len(d.sects) == 0 {
		d.rebuild()
	}
	for b := d.Sel.Caret.B; b != nil; {
		if b.Sect != nil {
			return b.Sect
		}
		if b.In == nil || b.In.Cell == nil {
			break
		}
		b = b.In.Cell.Row.Table.Block
	}
	if len(d.sects) == 0 {
		return nil
	}
	return d.sects[len(d.sects)-1]
}

// PageSetupOf is the section's setup.
func PageSetupOf(s *Section) PageSetup {
	return PageSetup{W: s.W, H: s.H, MT: s.MT, MB: s.MB, ML: s.ML, MR: s.MR}
}

// SetPageSetup gives the section at the caret, or every section, the setup: one undo step.
func (d *Doc) SetPageSetup(p PageSetup, all bool) {
	p = p.Fit()
	s := d.SectionAtCaret()
	if s == nil {
		return
	}
	list := []*Section{s}
	if all {
		list = d.sects
	}
	d.Begin("")
	defer d.Commit()
	for _, sec := range list {
		d.applySetup(sec, p)
	}
	d.touched(d.Main)
	d.lay.sectsNew, d.lay.sectsStale = true, true
	d.rebuild()
	d.lay.restart()
}

func (d *Doc) applySetup(s *Section, p PageSetup) {
	sect := s.Node
	if sect == nil {
		sect = ox.New("w:sectPr")
		d.saveRange(d.Main, d.Body.Root.Node, len(d.Body.Root.Node.Children), 0)
		d.Body.Root.Node.Append(sect)
	}
	d.saveNode(d.Main, sect)
	sz := sect.Ensure("w:pgSz", orderSectPr)
	sz.SetAttr("w:w", twips(p.W))
	sz.SetAttr("w:h", twips(p.H))
	sz.DelAttr("w:code") // the printer's number for the paper it had
	if p.Landscape() {
		sz.SetAttr("w:orient", "landscape")
	} else {
		sz.DelAttr("w:orient")
	}
	mar := sect.Child("w:pgMar")
	if mar == nil {
		mar = ox.New("w:pgMar", "w:header", twips(36), "w:footer", twips(36), "w:gutter", "0")
		sect.InsertOrdered(mar, orderSectPr)
	}
	// A negative top or bottom is a margin the header may not push, and stays one.
	signed := func(name string, v float32, exact bool) {
		if exact {
			v = -v
		}
		mar.SetAttr(name, twips(v))
	}
	signed("w:top", p.MT, s.ExactTop)
	signed("w:bottom", p.MB, s.ExactBottom)
	mar.SetAttr("w:left", twips(p.ML))
	mar.SetAttr("w:right", twips(p.MR))
}

// CanSectionBreak reports that the caret is where a section can end: between the body's paragraphs, not in a table,
// header or footer.
func (d *Doc) CanSectionBreak() bool {
	return d.editable() && d.Sel.Caret.B.In == d.Body.Root
}

// SectionBreak ends the section at the caret and starts the next on a new page, so that the pages after it can have
// another size or orientation. The new section has the same setup, headers and footers.
func (d *Doc) SectionBreak() {
	if !d.CanSectionBreak() {
		return
	}
	s := d.SectionAtCaret()
	d.Begin("")
	defer d.Commit()
	if !d.Sel.Empty() {
		d.deleteSelection()
	}
	first := d.Sel.Caret.B
	d.splitParagraph()
	d.saveBlock(first)
	props := ox.New("w:sectPr")
	if s != nil && s.Node != nil {
		props = s.Node.Clone()
		if t := props.Child("w:type"); t != nil {
			t.Remove() // the kind of break that started this section is its own
		}
	}
	ensurePPr(first.Node).InsertOrdered(props, orderPPr)
	d.lay.sectsNew, d.lay.sectsStale = true, true
	d.finish(first)
	d.rebuild()
	d.lay.restart()
}
