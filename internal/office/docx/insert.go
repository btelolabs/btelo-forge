package docx

import (
	"errors"
	"strconv"
	"strings"

	"github.com/btelolabs/btelo-forge/internal/office/opc"
	"github.com/btelolabs/btelo-forge/internal/office/ox"
)

// Inserting things: tables and what is done to them, pictures, links, fields, headers and footers.

// The namespaces a drawing's elements declare for themselves: a file may not have them on its root.
const (
	nsWP  = "http://schemas.openxmlformats.org/drawingml/2006/wordprocessingDrawing"
	nsA   = "http://schemas.openxmlformats.org/drawingml/2006/main"
	nsPic = "http://schemas.openxmlformats.org/drawingml/2006/picture"
	nsR   = "http://schemas.openxmlformats.org/officeDocument/2006/relationships"
)

// emptyCell is a w:tc of a width.
func emptyCell(w float32) *ox.Node {
	tc := ox.New("w:tc")
	pr := ox.New("w:tcPr")
	pr.Append(ox.New("w:tcW", "w:w", twips(w), "w:type", "dxa"))
	tc.Append(pr, ox.New("w:p"))
	return tc
}

// insertBlockAtCaret puts a new block at the caret: between the two halves of a paragraph, or before an empty one.
// It returns the paragraph after it.
func (d *Doc) insertBlockAtCaret(n *ox.Node) *ox.Node {
	c := d.Sel.Caret
	b := c.B
	if c.Off > 0 && c.Off < b.para().Len {
		d.splitParagraph()
		c = d.Sel.Caret
		b = c.B
		b.Node.Parent.Insert(b.Node.Parent.Index(b.Node), n)
		d.structural(b)
		return b.Node
	}
	parent := b.Node.Parent
	d.saveNode(b.In.Story.Part, b.Node)
	at := parent.Index(b.Node)
	if c.Off == 0 {
		parent.Insert(at, n)
		d.structural(b)
		return b.Node
	}
	after := freshP(b.Node)
	parent.Insert(at+1, n)
	parent.Insert(at+2, after)
	d.structural(b)
	return after
}

// InsertTable puts a table of rows by cols at the caret, the width of the text.
func (d *Doc) InsertTable(rows, cols int) error {
	if !d.editable() {
		return errors.New("this text cannot be edited")
	}
	if rows < 1 || cols < 1 || rows > 2000 || cols > 63 {
		return errors.New("a table has 1 to 2000 rows and 1 to 63 columns")
	}
	d.Begin("")
	defer d.Commit()
	if !d.Sel.Empty() {
		d.deleteSelection()
	}
	d.EnsureStyle("TableGrid")
	c := d.Sel.Caret
	width := d.textWidthAt(c.B)
	w := width / float32(cols)
	tbl := ox.New("w:tbl")
	pr := ox.New("w:tblPr")
	if d.Styles.ByID["TableGrid"] != nil {
		pr.Append(ox.New("w:tblStyle", "w:val", "TableGrid"))
	}
	pr.Append(ox.New("w:tblW", "w:w", "0", "w:type", "auto"))
	pr.Append(ox.New("w:tblLook", "w:val", "04A0", "w:firstRow", "1", "w:lastRow", "0", "w:firstColumn", "1", "w:lastColumn", "0", "w:noHBand", "0", "w:noVBand", "1"))
	grid := ox.New("w:tblGrid")
	for i := 0; i < cols; i++ {
		grid.Append(ox.New("w:gridCol", "w:w", twips(w)))
	}
	tbl.Append(pr, grid)
	for r := 0; r < rows; r++ {
		tr := ox.New("w:tr")
		for i := 0; i < cols; i++ {
			tr.Append(emptyCell(w))
		}
		tbl.Append(tr)
	}
	st := c.B.In.Story
	after := d.insertBlockAtCaret(tbl)
	d.rebuild()
	d.dirtyNodes(after, tbl)
	if b := st.byNode[tbl]; b != nil {
		at := Pos{B: b.firstPara()}
		d.Sel = Selection{Anchor: at, Caret: at}
	}
	return nil
}

// textWidthAt is the width of the text at a paragraph.
func (d *Doc) textWidthAt(b *Block) float32 {
	if c := b.In.Cell; c != nil {
		return c.stackW
	}
	top := b.top()
	if top.Sect != nil {
		return top.Sect.TextW()
	}
	return d.sects[0].TextW()
}

// caretCell is the cell around the caret, nil outside a table. Nested tables give the inner one.
func (d *Doc) caretCell() *Cell { return d.Sel.Caret.B.In.Cell }

// CaretInTable reports whether the caret is in a table.
func (d *Doc) CaretInTable() bool { return d.Sel.Caret.B != nil && d.Sel.Caret.B.In.Cell != nil }

// NextCell moves to the next cell, adding a row after the last; back goes the other way.
func (d *Doc) NextCell(back bool) {
	c := d.caretCell()
	if c == nil {
		return
	}
	t := c.Row.Table
	r, i := c.Row.Index, 0
	for k, x := range c.Row.Cells {
		if x == c {
			i = k
		}
	}
	step := 1
	if back {
		step = -1
	}
	for {
		i += step
		if i < 0 {
			r--
			if r < 0 {
				return
			}
			i = len(t.Rows[r].Cells) - 1
		}
		if i >= len(t.Rows[r].Cells) {
			r++
			i = 0
			if r >= len(t.Rows) {
				if d.AddRow(false) == nil {
					d.Layout(0)
				}
				return
			}
		}
		if n := t.Rows[r].Cells[i]; n.VMerge != 2 {
			flat := n.C.Blocks[0].firstPara()
			last := flat
			for _, b := range n.C.Blocks {
				if b.T == nil {
					last = b
				}
			}
			d.Sel = Selection{Anchor: Pos{B: flat}, Caret: Pos{B: last, Off: last.para().Len}}
			if flat == last && last.para().Len == 0 {
				d.Sel.Anchor = d.Sel.Caret
			}
			return
		}
	}
}

// resetCell empties a cell of a new row: its first paragraph's look stays, without its words.
func resetCell(tc *ox.Node) {
	first := tc.Child("w:p")
	for _, k := range append([]*ox.Node(nil), tc.Children...) {
		if k.Kind == ox.Element && k.Name != "w:tcPr" {
			k.Remove()
		}
	}
	p := ox.New("w:p")
	if first != nil {
		if pr := first.Child("w:pPr"); pr != nil {
			p.Append(pr.Clone())
			dropProp(p.Child("w:pPr"), "w:sectPr", "w:pPrChange")
		}
	}
	tc.Append(p)
}

func stripIDs(n *ox.Node) {
	n.Walk(func(e *ox.Node) bool {
		for _, a := range append([]ox.Attr(nil), e.Attrs...) {
			if strings.HasPrefix(a.Name, "w14:") || a.Name == "w:rsidR" || a.Name == "w:rsidTr" || a.Name == "w:rsidRPr" || a.Name == "w:rsidP" {
				e.DelAttr(a.Name)
			}
		}
		return true
	})
}

// AddRow adds a row above or below the caret's. Merged columns run on through a row added inside them.
func (d *Doc) AddRow(above bool) error {
	c := d.caretCell()
	if c == nil {
		return errNoTable
	}
	d.Begin("")
	defer d.Commit()
	row := c.Row
	t := row.Table
	d.saveBlock(t.Block)
	tr := row.Node.Clone()
	stripIDs(tr)
	var cells []*ox.Node
	cellsIn(tr, &cells, 0)
	for _, tc := range cells {
		resetCell(tc)
		pr := tc.Child("w:tcPr")
		if m := pr.Child("w:vMerge"); m != nil {
			restart := m.Attr("w:val") == "restart"
			if restart && above {
				m.Remove()
			} else if restart {
				m.DelAttr("w:val")
			}
		}
	}
	parent := row.Node.Parent
	at := parent.Index(row.Node)
	if !above {
		at++
	}
	parent.Insert(at, tr)
	d.tableChanged(t)
	d.placeAtRow(t, row.Index+map[bool]int{true: 0, false: 1}[above])
	return nil
}

var errNoTable = errors.New("the caret is not in a table")

// tableChanged reads a table again.
func (d *Doc) tableChanged(t *Table) {
	b := t.Block
	st := b.In.Story
	node := b.Node
	b.markStale()
	d.touched(st.Part)
	d.lay.dirty(b)
	d.rebuild()
	d.dirtyNodes(node)
}

func (d *Doc) placeAtRow(t *Table, r int) {
	b := t.Block.In.Story.byNode[t.Block.Node]
	if b == nil || len(b.T.Rows) == 0 {
		return
	}
	row := b.T.Rows[min(max(r, 0), len(b.T.Rows)-1)]
	if len(row.Cells) == 0 {
		return
	}
	at := Pos{B: row.Cells[0].C.Blocks[0].firstPara()}
	d.Sel = Selection{Anchor: at, Caret: at}
}

// removeTable takes a table from the tree, and the caret to what follows it.
func (d *Doc) removeTable(t *Table) {
	b := t.Block
	in := b.In
	st := in.Story
	var next *ox.Node
	if b.Index+1 < len(in.Blocks) {
		next = in.Blocks[b.Index+1].Node
	} else if b.Index > 0 {
		next = in.Blocks[b.Index-1].Node
	}
	prevFlat := max(b.firstPara().Flat-1, 0)
	if in.Cell != nil && len(in.Blocks) == 1 {
		// A cell cannot be left with no paragraph.
		p := ox.New("w:p")
		b.Node.Parent.Insert(b.Node.Parent.Index(b.Node)+1, p)
		next = p
	}
	b.Node.Remove()
	b.markStale()
	d.touched(st.Part)
	d.lay.dirty(b)
	d.rebuild()
	d.dirtyNodes(next)
	if nb := st.byNode[next]; nb != nil {
		at := Pos{B: nb.firstPara()}
		d.Sel = Selection{Anchor: at, Caret: at}
	} else if prevFlat < len(st.flat) {
		at := Pos{B: st.flat[prevFlat]}
		d.Sel = Selection{Anchor: at, Caret: at}
	}
}

// DeleteTable removes the table around the caret.
func (d *Doc) DeleteTable() error {
	c := d.caretCell()
	if c == nil {
		return errNoTable
	}
	d.Begin("")
	defer d.Commit()
	t := c.Row.Table
	d.saveBlock(t.Block)
	d.removeTable(t)
	return nil
}

// DeleteRow removes the rows the selection touches.
func (d *Doc) DeleteRow() error {
	c := d.caretCell()
	if c == nil {
		return errNoTable
	}
	d.Begin("")
	defer d.Commit()
	t := c.Row.Table
	d.saveBlock(t.Block)
	from, to := c.Row.Index, c.Row.Index
	if a := d.Sel.Anchor.B.In.Cell; a != nil && a.Row.Table == t {
		from, to = min(from, a.Row.Index), max(to, a.Row.Index)
	}
	if to-from+1 >= len(t.Rows) {
		d.removeTable(t)
		return nil
	}
	for r := from; r <= to; r++ {
		row := t.Rows[r]
		// What a row holds of a vertical merge starting in it passes to the next row.
		for _, cell := range row.Cells {
			if cell.VMerge == 1 && r+1 < len(t.Rows) {
				for _, below := range t.Rows[r+1].Cells {
					if below.Col == cell.Col && below.VMerge == 2 {
						if m := below.Node.Child("w:tcPr").Child("w:vMerge"); m != nil {
							m.SetAttr("w:val", "restart")
						}
					}
				}
			}
		}
		row.Node.Remove()
	}
	d.tableChanged(t)
	d.placeAtRow(t, from)
	return nil
}

// cellCovering is the cell of a row over a grid column.
func cellCovering(r *Row, col int) *Cell {
	for _, c := range r.Cells {
		if col >= c.Col && col < c.Col+c.Span {
			return c
		}
	}
	return nil
}

// setGridSpan writes a cell's span.
func setGridSpan(tcPr *ox.Node, span int) {
	if span <= 1 {
		dropProp(tcPr, "w:gridSpan")
		return
	}
	setProp(tcPr, orderTcPr, "w:gridSpan", "w:val", strconv.Itoa(span))
}

// retune writes the cell widths after the grid changed.
func (t *Table) retune() {
	grid := t.Block.Node.Child("w:tblGrid")
	if grid == nil {
		return
	}
	cols := grid.Elements("w:gridCol")
	for _, tr := range t.rowNodes() {
		var cells []*ox.Node
		cellsIn(tr, &cells, 0)
		k := atoi(tr.Child("w:trPr").Child("w:gridBefore").Attr("w:val"))
		for _, tc := range cells {
			pr := tc.Child("w:tcPr")
			span := max(atoi(pr.Child("w:gridSpan").Attr("w:val")), 1)
			w := float32(0)
			for i := k; i < k+span && i < len(cols); i++ {
				w += measure(cols[i].Attr("w:w"))
			}
			k += span
			if pr == nil {
				pr = ox.New("w:tcPr")
				tc.Insert(0, pr)
			}
			setProp(pr, orderTcPr, "w:tcW", "w:w", twips(w), "w:type", "dxa")
		}
	}
}

func (t *Table) rowNodes() []*ox.Node {
	var rows []*ox.Node
	rowsIn(t.Block.Node, &rows, 0)
	return rows
}

// AddColumn adds a column left or right of the caret's. The table keeps its width: columns give way in proportion.
func (d *Doc) AddColumn(left bool) error {
	c := d.caretCell()
	if c == nil {
		return errNoTable
	}
	t := c.Row.Table
	if len(t.Grid) >= 63 {
		return errors.New("a table has at most 63 columns")
	}
	d.Begin("")
	defer d.Commit()
	d.saveBlock(t.Block)
	at := c.Col + c.Span
	if left {
		at = c.Col
	}
	grid := t.Block.Node.Child("w:tblGrid")
	if grid == nil {
		grid = ox.New("w:tblGrid")
		t.Block.Node.Insert(t.Block.Node.Index(t.Block.Node.Child("w:tblPr"))+1, grid)
	}
	cols := grid.Elements("w:gridCol")
	total := float32(0)
	for _, g := range cols {
		total += measure(g.Attr("w:w"))
	}
	if total <= 0 {
		total = 432
	}
	width := total / float32(len(cols)+1)
	for _, g := range cols {
		g.SetAttr("w:w", twips(measure(g.Attr("w:w"))*(total-width)/total))
	}
	neu := ox.New("w:gridCol", "w:w", twips(width))
	if at < len(cols) {
		grid.Insert(grid.Index(cols[at]), neu)
	} else {
		grid.Append(neu)
	}
	for _, r := range t.Rows {
		var near *Cell
		for _, cell := range r.Cells {
			if at > cell.Col && at < cell.Col+cell.Span {
				near = cell
			}
		}
		if near != nil {
			// The new column falls inside a merged cell, which simply takes it in.
			setGridSpan(ensureTcPr(near.Node), near.Span+1)
			continue
		}
		tc := emptyCell(width)
		if len(r.Cells) > 0 {
			model := r.Cells[0]
			for _, cell := range r.Cells {
				if cell.Col+cell.Span == at || cell.Col == at {
					model = cell
					if cell.Col+cell.Span == at {
						break
					}
				}
			}
			if m := model.Node.Child("w:tcPr"); m != nil {
				pr := m.Clone()
				dropProp(pr, "w:gridSpan", "w:vMerge", "w:tcPrChange")
				tc.Child("w:tcPr").Replace(pr)
			}
		}
		placed := false
		for _, cell := range r.Cells {
			if cell.Col >= at {
				cell.Node.Parent.Insert(cell.Node.Parent.Index(cell.Node), tc)
				placed = true
				break
			}
		}
		if !placed {
			r.Node.Append(tc)
		}
	}
	t.retune()
	row, col := c.Row.Index, c.Col
	if !left {
		col = c.Col + c.Span
	}
	d.tableChanged(t)
	d.placeAtCell(t, row, col)
	return nil
}

func ensureTcPr(tc *ox.Node) *ox.Node {
	if p := tc.Child("w:tcPr"); p != nil {
		return p
	}
	p := ox.New("w:tcPr")
	tc.Insert(0, p)
	return p
}

func (d *Doc) placeAtCell(t *Table, r, col int) {
	b := t.Block.In.Story.byNode[t.Block.Node]
	if b == nil || r >= len(b.T.Rows) {
		return
	}
	if c := cellCovering(b.T.Rows[r], col); c != nil {
		at := Pos{B: c.C.Blocks[0].firstPara()}
		d.Sel = Selection{Anchor: at, Caret: at}
	}
}

// DeleteColumn removes the columns the selection touches.
func (d *Doc) DeleteColumn() error {
	c := d.caretCell()
	if c == nil {
		return errNoTable
	}
	t := c.Row.Table
	from, to := c.Col, c.Col+c.Span-1
	if a := d.Sel.Anchor.B.In.Cell; a != nil && a.Row.Table == t {
		from, to = min(from, a.Col), max(to, a.Col+a.Span-1)
	}
	d.Begin("")
	defer d.Commit()
	d.saveBlock(t.Block)
	if to-from+1 >= len(t.Grid) {
		d.removeTable(t)
		return nil
	}
	grid := t.Block.Node.Child("w:tblGrid")
	cols := grid.Elements("w:gridCol")
	for _, r := range t.Rows {
		for _, cell := range r.Cells {
			gone := 0
			for k := cell.Col; k < cell.Col+cell.Span; k++ {
				if k >= from && k <= to {
					gone++
				}
			}
			switch {
			case gone == 0:
			case gone >= cell.Span:
				cell.Node.Remove()
			default:
				setGridSpan(ensureTcPr(cell.Node), cell.Span-gone)
			}
		}
	}
	for k := to; k >= from; k-- {
		if k < len(cols) {
			cols[k].Remove()
		}
	}
	t.retune()
	row := c.Row.Index
	d.tableChanged(t)
	d.placeAtCell(t, row, max(from-1, 0))
	return nil
}

// MergeCells joins the cells between the selection's two ends. They must make a rectangle.
func (d *Doc) MergeCells() error {
	a, c := d.Sel.Anchor.B.In.Cell, d.Sel.Caret.B.In.Cell
	if c == nil || a == nil || a.Row.Table != c.Row.Table {
		return errors.New("select the cells to merge")
	}
	t := c.Row.Table
	r0, r1 := min(a.Row.Index, c.Row.Index), max(a.Row.Index, c.Row.Index)
	c0, c1 := min(a.Col, c.Col), max(a.Col+a.Span, c.Col+c.Span)
	return d.mergeRect(t, r0, r1, c0, c1)
}

// mergeRect joins rows r0 to r1, grid columns c0 up to c1.
func (d *Doc) mergeRect(t *Table, r0, r1, c0, c1 int) error {
	if r0 == r1 && c1-c0 <= 1 {
		return errors.New("select the cells to merge")
	}
	// A cell reaching out of the rectangle means there is no rectangle.
	for r := r0; r <= r1; r++ {
		for _, cell := range t.Rows[r].Cells {
			inside := cell.Col < c1 && cell.Col+cell.Span > c0
			if inside && (cell.Col < c0 || cell.Col+cell.Span > c1) {
				return errors.New("those cells do not make a rectangle")
			}
			if inside && cell.Origin != cell && cell.Origin.Row.Index < r0 {
				return errors.New("those cells do not make a rectangle")
			}
		}
	}
	d.Begin("")
	defer d.Commit()
	d.saveBlock(t.Block)
	first := cellCovering(t.Rows[r0], c0)
	if first == nil {
		return errors.New("those cells do not make a rectangle")
	}
	into := first.Node
	move := func(from *ox.Node) {
		for _, k := range append([]*ox.Node(nil), from.Children...) {
			if k.Kind != ox.Element || k.Name == "w:tcPr" {
				continue
			}
			if k.Name == "w:p" && strings.TrimSpace(k.InnerText()) == "" && len(k.All("w:drawing")) == 0 {
				continue // an empty paragraph adds nothing
			}
			k.Remove()
			into.Append(k)
		}
	}
	for r := r0; r <= r1; r++ {
		var head *Cell
		for _, cell := range t.Rows[r].Cells {
			if cell.Col < c0 || cell.Col >= c1 {
				continue
			}
			if head == nil {
				head = cell
				if r > r0 {
					move(cell.Node)
					resetEmpty(cell.Node)
				}
				continue
			}
			move(cell.Node)
			cell.Node.Remove()
		}
		if head == nil {
			continue
		}
		pr := ensureTcPr(head.Node)
		setGridSpan(pr, c1-c0)
		switch {
		case r1 == r0:
		case r == r0:
			setProp(pr, orderTcPr, "w:vMerge", "w:val", "restart")
		default:
			setProp(pr, orderTcPr, "w:vMerge")
		}
	}
	// The first paragraph left over may be the empty one that started the cell: words first.
	ps := into.Elements("w:p")
	if len(ps) > 1 && strings.TrimSpace(ps[0].InnerText()) == "" && len(ps[0].All("w:drawing")) == 0 {
		ps[0].Remove()
	}
	t.retune()
	node := first.Node
	d.tableChanged(t)
	st := t.Block.In.Story
	if nb := st.byNode[t.Block.Node]; nb != nil {
		for _, r := range nb.T.Rows {
			for _, cell := range r.Cells {
				if cell.Node == node {
					at := Pos{B: cell.C.Blocks[0].firstPara()}
					d.Sel = Selection{Anchor: at, Caret: at}
				}
			}
		}
	}
	return nil
}

// resetEmpty leaves a cell its one required paragraph.
func resetEmpty(tc *ox.Node) {
	if tc.Child("w:p") == nil {
		tc.Append(ox.New("w:p"))
	}
}

// nextDrawingID is a wp:docPr id not used in the part.
func nextDrawingID(root *ox.Node) int {
	top := 0
	root.Walk(func(e *ox.Node) bool {
		if e.Name == "wp:docPr" {
			top = max(top, atoi(e.Attr("id")))
		}
		return true
	})
	return top + 1
}

// InsertPicture puts a picture at the caret, in its natural size at 96 dpi or smaller to fit the text. w and h in
// points override that when not zero.
func (d *Doc) InsertPicture(data []byte, name string, w, h float32) error {
	if !d.editable() {
		return errors.New("this text cannot be edited")
	}
	bytes, ext, ctype, px, py, err := encodedPicture(data)
	if err != nil {
		return errors.New("this is not a picture that can go in a document")
	}
	d.Begin("")
	defer d.Commit()
	if !d.Sel.Empty() {
		d.deleteSelection()
	}
	c := d.Sel.Caret
	limit := d.textWidthAt(c.B)
	if limit <= 0 {
		limit = d.sects[0].TextW()
	}
	switch {
	case w > 0 && h <= 0:
		h = w * float32(py) / float32(px)
	case h > 0 && w <= 0:
		w = h * float32(px) / float32(py)
	case w <= 0:
		w, h = float32(px)*0.75, float32(py)*0.75
		if w > limit {
			w, h = limit, h*limit/w
		}
	}
	part := c.B.In.Story.Part
	img := d.Pkg.AddImage("word/media", ext, ctype, bytes)
	rid := ""
	for _, r := range part.Rels {
		if r.Type == opc.RelImage && !r.External && relResolve(part.Name, r.Target) == img {
			rid = r.ID
		}
	}
	if rid == "" {
		rid = d.addRel(part, opc.RelImage, relTarget(part.Name, img), false)
	}
	id := strconv.Itoa(nextDrawingID(part.X.Root))
	if name == "" {
		name = "Picture " + id
	}
	n := pictureXML(rid, id, name, w, h)
	d.saveBlock(c.B)
	d.putNode(c.B, c.Off, n)
	d.finish(c.B)
	at := Pos{B: c.B, Off: c.Off + 1}
	d.Sel = Selection{Anchor: at, Caret: at}
	return nil
}

// pictureXML is an inline picture's w:drawing.
func pictureXML(rid, id, name string, w, h float32) *ox.Node {
	cx, cy := strconv.Itoa(int(w*emuPerPoint)), strconv.Itoa(int(h*emuPerPoint))
	drawing := ox.New("w:drawing")
	inline := ox.New("wp:inline", "xmlns:wp", nsWP, "distT", "0", "distB", "0", "distL", "0", "distR", "0")
	inline.Append(ox.New("wp:extent", "cx", cx, "cy", cy), ox.New("wp:effectExtent", "l", "0", "t", "0", "r", "0", "b", "0"))
	inline.Append(ox.New("wp:docPr", "id", id, "name", name))
	frame := ox.New("wp:cNvGraphicFramePr")
	frame.Append(ox.New("a:graphicFrameLocks", "xmlns:a", nsA, "noChangeAspect", "1"))
	inline.Append(frame)
	graphic := ox.New("a:graphic", "xmlns:a", nsA)
	data := ox.New("a:graphicData", "uri", nsPic)
	pic := ox.New("pic:pic", "xmlns:pic", nsPic)
	nv := ox.New("pic:nvPicPr")
	nv.Append(ox.New("pic:cNvPr", "id", id, "name", name), ox.New("pic:cNvPicPr"))
	fill := ox.New("pic:blipFill")
	stretch := ox.New("a:stretch")
	stretch.Append(ox.New("a:fillRect"))
	fill.Append(ox.New("a:blip", "xmlns:r", nsR, "r:embed", rid), stretch)
	sp := ox.New("pic:spPr")
	xfrm := ox.New("a:xfrm")
	xfrm.Append(ox.New("a:off", "x", "0", "y", "0"), ox.New("a:ext", "cx", cx, "cy", cy))
	geom := ox.New("a:prstGeom", "prst", "rect")
	geom.Append(ox.New("a:avLst"))
	sp.Append(xfrm, geom)
	pic.Append(nv, fill, sp)
	data.Append(pic)
	graphic.Append(data)
	inline.Append(graphic)
	drawing.Append(inline)
	return drawing
}

// SetLink makes the selection a link to url, or inserts the words text as one at the caret. An empty url takes the
// link away.
func (d *Doc) SetLink(url, text string) error {
	if !d.editable() {
		return errors.New("this text cannot be edited")
	}
	a, c := d.Sel.Range()
	if a.B != c.B {
		return errors.New("a link stays in one paragraph")
	}
	b := a.B
	d.Begin("")
	defer d.Commit()
	if url == "" {
		d.saveBlock(b)
		p := b.para()
		for i := range p.Segs {
			s := &p.Segs[i]
			if s.Link == nil || s.Link.Parent == nil || (a.Off != c.Off && (s.End() <= a.Off || s.Start >= c.Off)) || (a.Off == c.Off && (s.End() < a.Off || s.Start > a.Off)) {
				continue
			}
			link := s.Link
			parent := link.Parent
			at := parent.Index(link)
			for _, k := range append([]*ox.Node(nil), link.Children...) {
				k.Remove()
				parent.Insert(at, k)
				at++
			}
			link.Remove()
		}
		d.finish(b)
		return nil
	}
	if a.Off == c.Off {
		if text == "" {
			text = url
		}
		if p := b.para(); len(text) > 0 {
			d.saveBlock(b)
			d.insertPlain(b, a.Off, cleanLine(text))
			c.Off = a.Off + len([]rune(cleanLine(text)))
			_ = p
		}
	}
	d.saveBlock(b)
	d.EnsureStyle("Hyperlink")
	from := d.cutAt(b, a.Off)
	to := d.cutAt(b, c.Off)
	if to <= from {
		return nil
	}
	part := b.In.Story.Part
	rid := d.addRel(part, opc.RelHyperlink, url, true)
	link := ox.New("w:hyperlink", "xmlns:r", nsR, "r:id", rid, "w:history", "1")
	kids := append([]*ox.Node(nil), b.Node.Children[from:to]...)
	b.Node.Insert(from, link)
	for _, k := range kids {
		k.Remove()
		link.Append(k)
		if k.Kind == ox.Element && k.Name == "w:r" && d.Styles.ByID["Hyperlink"] != nil {
			setProp(ensureRPr(k), orderRPr, "w:rStyle", "w:val", "Hyperlink")
		}
	}
	d.finish(b)
	at := Pos{B: b, Off: c.Off}
	d.Sel = Selection{Anchor: at, Caret: at}
	return nil
}

func cleanLine(s string) string {
	return strings.NewReplacer("\n", " ", "\t", " ").Replace(cleanText(s))
}

// LinkAt is the address of the link at a place.
func (d *Doc) LinkAt(p Pos) string {
	left, right := p.B.para().segAt(p.Off)
	s := right
	if s == nil || s.Link == nil {
		s = left
	}
	if s == nil || s.Link == nil {
		return ""
	}
	rel, ok := opc.RelByID(p.B.In.Story.Part.Rels, s.Link.Attr("r:id"))
	if ok && rel.External {
		return rel.Target
	}
	return ""
}

// InsertField puts a field at the caret with its cached result.
func (d *Doc) InsertField(instr, result string) {
	if !d.editable() {
		return
	}
	d.Begin("")
	defer d.Commit()
	c := d.Sel.Caret
	b := c.B
	d.saveBlock(b)
	d.splitRunAt(b, c.Off)
	r, i := d.runPoint(b, c.Off)
	var rPr *ox.Node
	if rPr = r.Child("w:rPr"); rPr != nil {
		rPr = rPr.Clone()
	}
	mark := func(n *ox.Node) *ox.Node {
		k := newRunIn(rPr)
		k.Append(n)
		return k
	}
	instrNode := ox.New("w:instrText", "xml:space", "preserve")
	instrNode.SetText(" " + instr + " ")
	nodes := []*ox.Node{
		mark(ox.New("w:fldChar", "w:fldCharType", "begin")),
		mark(instrNode),
		mark(ox.New("w:fldChar", "w:fldCharType", "separate")),
		mark(newT(result)),
		mark(ox.New("w:fldChar", "w:fldCharType", "end")),
	}
	// The field goes after the run edge at the caret.
	parent := r.Parent
	at := parent.Index(r)
	if i > firstContent(r) {
		at++
	}
	if !hasContent(r) {
		// A run made only to find the place.
		r.Remove()
	}
	for k, n := range nodes {
		parent.Insert(at+k, n)
	}
	d.finish(b)
	end := Pos{B: b, Off: c.Off + len([]rune(result))}
	d.Sel = Selection{Anchor: end, Caret: end}
}

func hasContent(r *ox.Node) bool {
	return len(r.Elements()) > firstContent(r)
}

// HeaderStory is the header or footer of a page for editing, made when it has none.
func (d *Doc) HeaderStory(pg *Page, kind string) *Story {
	s := pg.Sect
	first := pg.First && s.TitlePg
	even := d.evenOdd && pg.Number%2 == 0
	if st := s.story(kind, first, even); st != nil {
		return st
	}
	which := "default"
	if first {
		which = "first"
	} else if even {
		which = "even"
	}
	d.Begin("")
	defer d.Commit()
	root, ct, pattern, relType, ref := "w:hdr", ctHeader, "word/header%d.xml", relHeader, "w:headerReference"
	if kind == "footer" {
		root, ct, pattern, relType, ref = "w:ftr", ctFooter, "word/footer%d.xml", relFooter, "w:footerReference"
	}
	name := d.Pkg.UniqueName(pattern)
	style := "Header"
	if kind == "footer" {
		style = "Footer"
	}
	d.EnsureStyle(style)
	body := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + "\n<" + root + ` xmlns:w="` + nsW + `" xmlns:r="` + nsR + `"><w:p><w:pPr><w:pStyle w:val="` + style + `"/></w:pPr></w:p></` + root + ">"
	d.Pkg.Put(name, ct, []byte(body))
	rid := d.addRel(d.Main, relType, relTarget(d.Main.Name, name), false)
	sect := s.Node
	if sect == nil {
		sect = ox.New("w:sectPr")
		d.saveRange(d.Main, d.Body.Root.Node, len(d.Body.Root.Node.Children), 0)
		d.Body.Root.Node.Append(sect)
	}
	d.saveNode(d.Main, sect)
	e := ox.New(ref, "w:type", which, "r:id", rid)
	if d.Main.X.Root.Attr("xmlns:r") == "" {
		e.SetAttr("xmlns:r", nsR)
	}
	// Header and footer references come first in a section's properties.
	at := 0
	for i, c := range sect.Children {
		if c.Kind == ox.Element && (c.Name == "w:headerReference" || c.Name == "w:footerReference") {
			at = i + 1
		}
	}
	sect.Insert(at, e)
	d.touched(d.Main)
	d.lay.sectsNew, d.lay.sectsStale = true, true
	d.rebuild()
	d.lay.restart()
	for _, sec := range d.sects {
		if sec.Node == sect || sec.Node == nil {
			return sec.Refs[kind+":"+which]
		}
	}
	return d.stories[name]
}

// ReadPart is a package part's bytes, for tools that look inside.
func (d *Doc) ReadPart(name string) []byte {
	d.flush()
	if p := d.Pkg.Part(name); p != nil {
		return p.Data
	}
	return nil
}
