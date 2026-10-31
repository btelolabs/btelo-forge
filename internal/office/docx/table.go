package docx

import (
	"image/color"
	"strings"

	"github.com/btelolabs/btelo-forge/internal/gfx"
	"github.com/btelolabs/btelo-forge/internal/office/ox"
)

// Tables: the rows and cells of a w:tbl, with what its style and the cell's own properties give them, and how a row
// is cut to fit a page.

// Edge names the sides of a cell and a table's inner lines.
const (
	edgeTop = iota
	edgeLeft
	edgeBottom
	edgeRight
	edgeInsideH
	edgeInsideV
	edgeCount
)

var edgeNames = []string{"w:top", "w:left", "w:bottom", "w:right", "w:insideH", "w:insideV"}

// edgeOf is the edge an element of a borders or margins list stands for.
func edgeOf(name string) int {
	switch name {
	case "w:top":
		return edgeTop
	case "w:left", "w:start":
		return edgeLeft
	case "w:bottom":
		return edgeBottom
	case "w:right", "w:end":
		return edgeRight
	case "w:insideH":
		return edgeInsideH
	case "w:insideV":
		return edgeInsideV
	}
	return -1
}

// Table is a table.
type Table struct {
	Block   *Block
	Rows    []*Row
	Grid    []float32 // column widths as the file has them
	W       float32   // the width asked for, in points; 0 when it is automatic
	Pct     float32   // or the share of the text width, 0 to 1
	Ind     float32
	Jc      string
	Borders [edgeCount]Border
	Mar     [4]float32 // top, left, bottom, right
	Style   string
	cache   *tableLayout
}

// Row is a table row.
type Row struct {
	Table     *Table
	Node      *ox.Node
	Cells     []*Cell
	H         float32
	Exact     bool
	CantSplit bool
	Header    bool
	Index     int
}

// Cell is a table cell.
type Cell struct {
	Row     *Row
	Node    *ox.Node
	C       *Container
	Col     int // the grid column it starts in
	Span    int
	VMerge  int   // 0 none, 1 the first of a merged column, 2 merged into the cell above
	Origin  *Cell // the first cell of a vertical merge, itself when there is none
	Rows    int   // how many rows it runs over, in an origin
	Borders [edgeCount]Border
	HasEdge [edgeCount]bool
	Fill    color.RGBA
	Mar     [4]float32
	VAlign  string
	stack   *Stack
	stackW  float32
	WantedW float32
	condKey string
	conds   []string
}

// read takes the table from its tree. depth is how many tables are around it.
func (t *Table) read(depth int) {
	n := t.Block.Node
	d := t.Block.In.Story.d
	t.Rows, t.Grid, t.cache = nil, nil, nil
	if depth > maxTableDepth {
		return
	}
	tblPr := n.Child("w:tblPr")
	t.Style = tblPr.Child("w:tblStyle").Attr("w:val")
	t.Mar = [4]float32{0, 5.4, 0, 5.4}
	t.Borders = [edgeCount]Border{}
	t.W, t.Pct, t.Ind, t.Jc = 0, 0, 0, ""
	if t.Style == "" {
		t.Style = d.Styles.tableDef
	}
	chain := d.Styles.chain(t.Style)
	for _, st := range chain {
		if st.Type == "table" {
			t.applyTblPr(st.Node.Child("w:tblPr"), d)
		}
	}
	t.applyTblPr(tblPr, d)
	for _, g := range n.Child("w:tblGrid").Elements("w:gridCol") {
		if len(t.Grid) >= maxGridCols {
			break
		}
		t.Grid = append(t.Grid, max(measure(g.Attr("w:w")), 0))
	}
	look := readLook(tblPr.Child("w:tblLook"))
	var rows []*ox.Node
	rowsIn(n, &rows, 0)
	for _, rn := range rows {
		r := &Row{Table: t, Node: rn, Index: len(t.Rows)}
		trPr := rn.Child("w:trPr")
		if h := trPr.Child("w:trHeight"); h != nil {
			r.H = measure(h.Attr("w:val"))
			r.Exact = h.Attr("w:hRule") == "exact"
		}
		if c := trPr.Child("w:cantSplit"); c != nil {
			r.CantSplit = toggle(c)
		}
		if c := trPr.Child("w:tblHeader"); c != nil {
			r.Header = toggle(c)
		}
		col := atoi(trPr.Child("w:gridBefore").Attr("w:val"))
		var cells []*ox.Node
		cellsIn(rn, &cells, 0)
		for _, cn := range cells {
			c := &Cell{Row: r, Node: cn, Col: col, Span: 1}
			tcPr := cn.Child("w:tcPr")
			if v := atoi(tcPr.Child("w:gridSpan").Attr("w:val")); v > 1 && v <= maxGridCols {
				c.Span = v
			}
			if w := tcPr.Child("w:tcW"); w != nil && w.Attr("w:type") != "pct" && w.Attr("w:type") != "auto" {
				c.WantedW = measure(w.Attr("w:w"))
			}
			if m := tcPr.Child("w:vMerge"); m != nil {
				c.VMerge = 2
				if m.Attr("w:val") == "restart" {
					c.VMerge = 1
				}
			}
			c.Origin = c
			c.Rows = 1
			r.Cells = append(r.Cells, c)
			col += c.Span
		}
		for len(t.Grid) < col && col <= maxGridCols {
			t.Grid = append(t.Grid, 0)
		}
		t.Rows = append(t.Rows, r)
	}
	t.fillGrid()
	t.linkMerges()
	for _, r := range t.Rows {
		for _, c := range r.Cells {
			t.dressCell(c, chain, look, d)
			c.read(depth)
		}
	}
}

// rowsIn gathers the w:tr of a table, past the content controls that may wrap them.
func rowsIn(n *ox.Node, out *[]*ox.Node, depth int) {
	if depth > maxDepth {
		return
	}
	for _, c := range n.Elements() {
		switch c.Name {
		case "w:tr":
			*out = append(*out, c)
		case "w:sdt":
			rowsIn(c.Child("w:sdtContent"), out, depth+1)
		case "w:customXml", "w:ins", "w:moveTo":
			rowsIn(c, out, depth+1)
		}
	}
}

func cellsIn(n *ox.Node, out *[]*ox.Node, depth int) {
	if depth > maxDepth {
		return
	}
	for _, c := range n.Elements() {
		switch c.Name {
		case "w:tc":
			*out = append(*out, c)
		case "w:sdt":
			cellsIn(c.Child("w:sdtContent"), out, depth+1)
		case "w:customXml":
			cellsIn(c, out, depth+1)
		}
	}
}

// read reads the cell's blocks.
func (c *Cell) read(depth int) {
	story := c.Row.Table.Block.In.Story
	if c.C == nil {
		c.C = &Container{Story: story, Node: c.Node, Cell: c}
	}
	if c.Node.Child("w:p") == nil && c.Node.Child("w:sdt") == nil {
		c.Node.Append(ox.New("w:p")) // the schema requires one, and a caret needs one
	}
	c.C.Base = c.Row.Table.cellBase(c)
	c.C.sync(depth)
}

// addFlat adds the paragraphs of a table that did not change to its story's list.
func (t *Table) addFlat() {
	for _, r := range t.Rows {
		for _, c := range r.Cells {
			for _, b := range c.C.Blocks {
				if b.T != nil {
					if b.stale {
						b.stale = false
						b.T.read(1)
					} else {
						b.T.addFlat()
					}
					continue
				}
				b.Flat = len(c.C.Story.flat)
				c.C.Story.flat = append(c.C.Story.flat, b)
			}
		}
	}
}

func (t *Table) applyTblPr(n *ox.Node, d *Doc) {
	if n == nil {
		return
	}
	for _, c := range n.Elements() {
		switch c.Name {
		case "w:tblBorders":
			for _, b := range c.Elements() {
				if e := edgeOf(b.Name); e >= 0 {
					t.Borders[e] = d.Theme.border(b)
				}
			}
		case "w:tblCellMar":
			for _, m := range c.Elements() {
				if e := edgeOf(m.Name); e >= 0 && e < 4 {
					t.Mar[e] = measure(m.Attr("w:w"))
				}
			}
		case "w:tblW":
			switch c.Attr("w:type") {
			case "pct":
				v := strings.TrimSuffix(c.Attr("w:w"), "%")
				f := float32(atoi(v))
				if strings.HasSuffix(c.Attr("w:w"), "%") {
					t.Pct = f / 100
				} else {
					t.Pct = f / 5000
				}
				t.W = 0
			case "dxa":
				t.W, t.Pct = measure(c.Attr("w:w")), 0
			default:
				t.W, t.Pct = 0, 0
			}
		case "w:tblInd":
			if c.Attr("w:type") != "pct" {
				t.Ind = measure(c.Attr("w:w"))
			}
		case "w:jc":
			t.Jc = c.Attr("w:val")
		}
	}
}

type tblLook struct{ firstRow, lastRow, firstCol, lastCol, noH, noV bool }

func readLook(n *ox.Node) tblLook {
	l := tblLook{firstRow: true, firstCol: true, noV: true}
	if n == nil {
		return l
	}
	if v := n.Attr("w:val"); v != "" {
		var bits int
		for _, r := range strings.ToLower(v) {
			bits <<= 4
			switch {
			case r >= '0' && r <= '9':
				bits |= int(r - '0')
			case r >= 'a' && r <= 'f':
				bits |= int(r-'a') + 10
			}
		}
		l = tblLook{firstRow: bits&0x20 != 0, lastRow: bits&0x40 != 0, firstCol: bits&0x80 != 0, lastCol: bits&0x100 != 0, noH: bits&0x200 != 0, noV: bits&0x400 != 0}
	}
	read := func(name string, field *bool) {
		if v, ok := n.Lookup(name); ok {
			*field = on(v)
		}
	}
	read("w:firstRow", &l.firstRow)
	read("w:lastRow", &l.lastRow)
	read("w:firstColumn", &l.firstCol)
	read("w:lastColumn", &l.lastCol)
	read("w:noHBand", &l.noH)
	read("w:noVBand", &l.noV)
	return l
}

// conditions are the parts of the table style that apply to a cell, in the order they are laid over each other.
func (t *Table) conditions(c *Cell, look tblLook) []string {
	var out []string
	r := c.Row
	rows, cols := len(t.Rows), len(t.Grid)
	if !look.noV {
		k := c.Col
		if look.firstCol {
			k--
		}
		if k >= 0 {
			out = append(out, []string{"band1Vert", "band2Vert"}[k%2])
		}
	}
	if !look.noH {
		k := r.Index
		if look.firstRow {
			k--
		}
		if k >= 0 {
			out = append(out, []string{"band1Horz", "band2Horz"}[k%2])
		}
	}
	first, last := look.firstCol && c.Col == 0, look.lastCol && c.Col+c.Span >= cols
	top, bottom := look.firstRow && r.Index == 0, look.lastRow && r.Index == rows-1
	if first {
		out = append(out, "firstCol")
	}
	if last {
		out = append(out, "lastCol")
	}
	if top {
		out = append(out, "firstRow")
	}
	if bottom {
		out = append(out, "lastRow")
	}
	switch {
	case top && first:
		out = append(out, "nwCell")
	case top && last:
		out = append(out, "neCell")
	case bottom && first:
		out = append(out, "swCell")
	case bottom && last:
		out = append(out, "seCell")
	}
	return out
}

// dressCell works out the cell's borders, fill and margins: table, style's conditions, then the cell's own.
func (t *Table) dressCell(c *Cell, chain []*Style, look tblLook, d *Doc) {
	c.Mar = t.Mar
	r := c.Row
	lastRow := r.Index+max(c.Rows, 1) >= len(t.Rows)
	c.Borders = [edgeCount]Border{}
	c.Borders[edgeTop], c.Borders[edgeBottom] = t.Borders[edgeInsideH], t.Borders[edgeInsideH]
	c.Borders[edgeLeft], c.Borders[edgeRight] = t.Borders[edgeInsideV], t.Borders[edgeInsideV]
	if r.Index == 0 {
		c.Borders[edgeTop] = t.Borders[edgeTop]
	}
	if lastRow {
		c.Borders[edgeBottom] = t.Borders[edgeBottom]
	}
	if c.Col == 0 {
		c.Borders[edgeLeft] = t.Borders[edgeLeft]
	}
	if c.Col+c.Span >= len(t.Grid) {
		c.Borders[edgeRight] = t.Borders[edgeRight]
	}
	apply := func(tcPr *ox.Node) {
		if tcPr == nil {
			return
		}
		if b := tcPr.Child("w:tcBorders"); b != nil {
			for _, e := range b.Elements() {
				k := edgeOf(e.Name)
				switch {
				case k == edgeInsideH:
					if r.Index > 0 {
						c.Borders[edgeTop] = d.Theme.border(e)
					}
					if !lastRow {
						c.Borders[edgeBottom] = d.Theme.border(e)
					}
				case k == edgeInsideV:
					if c.Col > 0 {
						c.Borders[edgeLeft] = d.Theme.border(e)
					}
					if c.Col+c.Span < len(t.Grid) {
						c.Borders[edgeRight] = d.Theme.border(e)
					}
				case k >= 0:
					c.Borders[k] = d.Theme.border(e)
				}
			}
		}
		if s := tcPr.Child("w:shd"); s != nil {
			c.Fill = d.Theme.fill(s)
		}
		if m := tcPr.Child("w:tcMar"); m != nil {
			for _, e := range m.Elements() {
				if k := edgeOf(e.Name); k >= 0 && k < 4 {
					c.Mar[k] = measure(e.Attr("w:w"))
				}
			}
		}
		if v := tcPr.Child("w:vAlign"); v != nil {
			c.VAlign = v.Attr("w:val")
		}
	}
	conds := t.conditions(c, look)
	for _, st := range chain {
		if st.Type != "table" {
			continue
		}
		apply(st.Node.Child("w:tcPr"))
	}
	for _, st := range chain {
		if st.Type != "table" {
			continue
		}
		for _, name := range conds {
			for _, e := range st.Node.Elements("w:tblStylePr") {
				if e.Attr("w:type") == name {
					apply(e.Child("w:tcPr"))
				}
			}
		}
	}
	apply(c.Node.Child("w:tcPr"))
	c.condKey = t.Style + "|" + strings.Join(conds, "|")
	c.conds = conds
}

// cellBase is what the table's style gives the paragraphs of a cell; nil where it gives nothing.
func (t *Table) cellBase(c *Cell) *Base {
	d := t.Block.In.Story.d
	s := d.Styles
	if t.Style == "" || s.ByID[t.Style] == nil {
		return nil
	}
	if b, ok := s.cond[c.condKey]; ok {
		return b
	}
	b := s.defaults
	for _, st := range s.chain(t.Style) {
		if st.Type == "table" {
			s.applyP(&b.P, st.Node.Child("w:pPr"))
			s.applyR(&b.R, st.Node.Child("w:rPr"))
		}
	}
	for _, st := range s.chain(t.Style) {
		if st.Type != "table" {
			continue
		}
		for _, name := range c.conds {
			for _, e := range st.Node.Elements("w:tblStylePr") {
				if e.Attr("w:type") == name {
					s.applyP(&b.P, e.Child("w:pPr"))
					s.applyR(&b.R, e.Child("w:rPr"))
				}
			}
		}
	}
	s.cond[c.condKey] = &b
	return &b
}

// fillGrid gives columns with no width one, from the cells or an equal share.
func (t *Table) fillGrid() {
	sum := float32(0)
	for _, g := range t.Grid {
		sum += g
	}
	if sum > 0 {
		return
	}
	for _, r := range t.Rows {
		for _, c := range r.Cells {
			if c.Span == 1 && c.Col < len(t.Grid) && c.WantedW > 0 && t.Grid[c.Col] == 0 {
				t.Grid[c.Col] = c.WantedW
			}
		}
	}
}

// linkMerges joins the vertically merged cells to the first of each.
func (t *Table) linkMerges() {
	above := map[int]*Cell{}
	for _, r := range t.Rows {
		next := map[int]*Cell{}
		for _, c := range r.Cells {
			if c.VMerge == 2 {
				if o := above[c.Col]; o != nil {
					c.Origin = o
					o.Rows++
					next[c.Col] = o
					continue
				}
				c.VMerge = 1 // a continuation with nothing above starts a merge
			}
			if c.VMerge == 1 {
				next[c.Col] = c
			}
		}
		above = next
	}
}

// tableLayout is a table at a width.
type tableLayout struct {
	w     float32
	edges []float32 // the columns' left edges, and the last one's right
	h     []float32 // the rows' heights
}

// layout is the table's geometry for a width of text.
func (t *Table) layout(avail float32, depth int) *tableLayout {
	if t.cache != nil && t.cache.w == avail {
		return t.cache
	}
	d := t.Block.In.Story.d
	tl := &tableLayout{w: avail}
	n := max(len(t.Grid), 1)
	widths := make([]float32, n)
	sum := float32(0)
	for i := range t.Grid {
		widths[i] = t.Grid[i]
		sum += widths[i]
	}
	want := t.W
	if t.Pct > 0 {
		want = avail * t.Pct
	}
	if sum <= 0 {
		if want <= 0 {
			want = avail
		}
		for i := range widths {
			widths[i] = want / float32(n)
		}
	} else {
		// Zero columns take an average one; a width asked for scales what the grid says.
		zero := 0
		for _, w := range widths {
			if w == 0 {
				zero++
			}
		}
		if zero > 0 {
			avg := sum / float32(n-zero)
			for i := range widths {
				if widths[i] == 0 {
					widths[i] = avg
				}
			}
			sum += avg * float32(zero)
		}
		if t.Pct > 0 {
			for i := range widths {
				widths[i] *= want / sum
			}
		}
	}
	tl.edges = make([]float32, n+1)
	for i, w := range widths {
		tl.edges[i+1] = tl.edges[i] + w
	}
	tl.h = make([]float32, len(t.Rows))
	for ri, r := range t.Rows {
		h := r.H
		for _, c := range r.Cells {
			if c.Origin != c {
				continue
			}
			s := d.cellStack(c, tl, depth)
			if c.Rows <= 1 {
				h = maxUnlessExact(r, h, c.Mar[edgeTop]+s.H+c.Mar[edgeBottom])
			}
		}
		tl.h[ri] = h
	}
	// A merged cell's words may need more than the rows it covers: the last of them grows.
	for ri, r := range t.Rows {
		for _, c := range r.Cells {
			if c.Origin != c || c.Rows <= 1 {
				continue
			}
			last := min(ri+c.Rows-1, len(t.Rows)-1)
			have := float32(0)
			for k := ri; k <= last; k++ {
				have += tl.h[k]
			}
			if need := c.Mar[edgeTop] + c.stack.H + c.Mar[edgeBottom]; need > have && !t.Rows[last].Exact {
				tl.h[last] += need - have
			}
		}
	}
	t.cache = tl
	return tl
}

func maxUnlessExact(r *Row, h, need float32) float32 {
	if r.Exact {
		return h
	}
	return max(h, need)
}

// x is the table's left edge in a text area of a width.
func (t *Table) x(tl *tableLayout) float32 {
	w := tl.edges[len(tl.edges)-1]
	switch t.Jc {
	case "center":
		return (tl.w - w) / 2
	case "right", "end":
		return tl.w - w
	}
	return t.Ind
}

func (d *Doc) cellStack(c *Cell, tl *tableLayout, depth int) *Stack {
	end := min(c.Col+c.Span, len(tl.edges)-1)
	w := tl.edges[end] - tl.edges[min(c.Col, end)] - c.Mar[edgeLeft] - c.Mar[edgeRight]
	w = max(w, 4)
	if c.stack == nil || c.stackW != w {
		c.stack = d.stackOf(c.C, w, depth+1)
		c.stackW = w
	}
	return c.stack
}

// placeRow puts row ri of the table at (x, y) in s, in at most limit points. offs are how far each grid column's
// words have been shown. It says how high the row came out, whether it is finished, and whether it could be placed.
func (t *Table) placeRow(s *Stack, ri int, x, y, limit float32, offs []float32, tl *tableLayout, force bool) (used float32, done, ok bool) {
	r := t.Rows[ri]
	natural := tl.h[ri]
	need := float32(0)
	fresh := true
	for _, c := range r.Cells {
		o := c.Origin
		if offs[c.Col] > 0 && o.Rows <= 1 {
			fresh = false
		}
	}
	if fresh {
		need = natural
	} else {
		for _, c := range r.Cells {
			if c.Origin.Rows <= 1 {
				need = max(need, c.Mar[edgeTop]+c.stack.H-offs[c.Col]+c.Mar[edgeBottom])
			}
		}
	}
	whole := need <= limit+0.01
	if !whole && !force && (r.CantSplit || r.Exact || r.Header) {
		return 0, false, false
	}
	ends := make([]float32, len(r.Cells))
	h := need
	if !whole {
		moved := false
		h = limit
		tallest := float32(0)
		for i, c := range r.Cells {
			o := c.Origin
			room := limit - c.Mar[edgeTop] - c.Mar[edgeBottom]
			end := o.stack.breakAt(offs[c.Col] + room)
			if end > offs[c.Col] {
				moved = true
				tallest = max(tallest, end-offs[c.Col]+c.Mar[edgeTop]+c.Mar[edgeBottom])
			}
			ends[i] = max(end, offs[c.Col])
		}
		if !moved {
			if !force {
				return 0, false, false
			}
			// Nothing fits on an empty page: it is shown cut off rather than never.
			for i, c := range r.Cells {
				ends[i] = c.Origin.stack.H
			}
			whole = true
		} else {
			h = min(tallest, limit)
		}
	}
	for i, c := range r.Cells {
		o := c.Origin
		cx := x + tl.edges[min(c.Col, len(tl.edges)-1)]
		cw := tl.edges[min(c.Col+c.Span, len(tl.edges)-1)] - tl.edges[min(c.Col, len(tl.edges)-1)]
		box := gfx.Rect{X: cx, Y: y, W: cw, H: h}
		if o.Fill.A != 0 {
			s.Decor = append(s.Decor, Decor{Rect: box, Col: o.Fill})
		}
		end := ends[i]
		if whole {
			end = offs[c.Col] + max(h-c.Mar[edgeTop]-c.Mar[edgeBottom], 0)
			if o.Rows <= 1 && !r.Exact {
				end = o.stack.H
			}
		}
		dy := y + o.Mar[edgeTop] - offs[c.Col]
		if whole && o.Rows <= 1 && fresh {
			free := h - o.Mar[edgeTop] - o.Mar[edgeBottom] - o.stack.H
			if free > 0 {
				switch o.VAlign {
				case "center":
					dy += free / 2
				case "bottom":
					dy += free
				}
			}
		}
		o.stack.emit(s, offs[c.Col], end, cx+o.Mar[edgeLeft], dy)
		offs[c.Col] = end
		borders := c.Borders
		cellBorders(s, box, &borders)
	}
	if whole {
		for _, c := range r.Cells {
			if c.Origin.Rows <= 1 || ri >= c.Origin.Row.Index+c.Origin.Rows-1 {
				offs[c.Col] = 0
			}
			for k := c.Col + 1; k < c.Col+c.Span && k < len(offs); k++ {
				offs[k] = 0
			}
		}
	}
	return h, whole, true
}

// cellBorders draws a cell's edges.
func cellBorders(s *Stack, r gfx.Rect, b *[edgeCount]Border) {
	line := func(e Border, x, y, w, h float32) {
		if !e.shows() {
			return
		}
		col := e.Color
		if col.A == 0 {
			col = gfx.Hex(0)
		}
		s.Decor = append(s.Decor, Decor{Rect: gfx.Rect{X: x, Y: y, W: w, H: h}, Col: col, Kind: e.Kind, Line: true})
	}
	top, left, bottom, right := b[edgeTop].Width, b[edgeLeft].Width, b[edgeBottom].Width, b[edgeRight].Width
	line(b[edgeTop], r.X-left/2, r.Y-top/2, r.W+left/2+right/2, top)
	line(b[edgeBottom], r.X-left/2, r.Y+r.H-bottom/2, r.W+left/2+right/2, bottom)
	line(b[edgeLeft], r.X-left/2, r.Y-top/2, left, r.H+top/2+bottom/2)
	line(b[edgeRight], r.X+r.W-right/2, r.Y-top/2, right, r.H+top/2+bottom/2)
}

// Cell is the cell holding a block, nil outside tables.
func (b *Block) Cell() *Cell { return b.In.Cell }
