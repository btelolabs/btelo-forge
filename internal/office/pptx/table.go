package pptx

import (
	"strconv"
	"sync"

	"github.com/btelolabs/btelo-forge/internal/office/ox"
)

// Limits on a table: a hostile file could claim millions of cells.
const (
	maxTableRows = 2000
	maxTableCols = 512
)

// Cell is a table cell laid out.
type Cell struct {
	Node         *ox.Node
	Row, Col     int
	RowSpan      int
	ColSpan      int
	X, Y, W, H   float64 // in the table's coordinates
	Frame        *TextFrame
	Fill         *Fill
	Left, Top    *Line
	Right, Under *Line
	Own          [4]bool // the cell sets that border itself, which beats the style along a shared edge
}

// Table is a table laid out: the positions of its grid and its visible cells.
type Table struct {
	ColX  []float64 // len cols+1
	RowY  []float64 // len rows+1
	Cells []*Cell
}

// tableNS and friends name the two built-in styles that mean no style.
const (
	styleNoGrid    = "{2D5ABB26-0587-4C30-8999-92F81FD0307C}"
	styleTableGrid = "{5940675A-B579-460E-94D1-54222C63F5DA}"
	styleMedium2   = "{5C22544A-7EE6-4342-B048-85BDC9FD1C3A}"
)

// medium2 is PowerPoint's default table style, which a file that uses it need not carry: a plain file made elsewhere
// names it and leaves the rest to the application.
const medium2 = `<a:tblStyle xmlns:a="` + nsA + `" styleId="` + styleMedium2 + `" styleName="Medium Style 2 - Accent 1">
<a:wholeTbl><a:tcTxStyle><a:fontRef idx="minor"/><a:schemeClr val="dk1"/></a:tcTxStyle><a:tcStyle><a:tcBdr>
<a:left><a:ln w="12700"><a:solidFill><a:schemeClr val="lt1"/></a:solidFill></a:ln></a:left>
<a:right><a:ln w="12700"><a:solidFill><a:schemeClr val="lt1"/></a:solidFill></a:ln></a:right>
<a:top><a:ln w="12700"><a:solidFill><a:schemeClr val="lt1"/></a:solidFill></a:ln></a:top>
<a:bottom><a:ln w="12700"><a:solidFill><a:schemeClr val="lt1"/></a:solidFill></a:ln></a:bottom>
<a:insideH><a:ln w="12700"><a:solidFill><a:schemeClr val="lt1"/></a:solidFill></a:ln></a:insideH>
<a:insideV><a:ln w="12700"><a:solidFill><a:schemeClr val="lt1"/></a:solidFill></a:ln></a:insideV>
</a:tcBdr><a:fill><a:solidFill><a:schemeClr val="accent1"><a:tint val="20000"/></a:schemeClr></a:solidFill></a:fill></a:tcStyle></a:wholeTbl>
<a:band1H><a:tcStyle><a:tcBdr/><a:fill><a:solidFill><a:schemeClr val="accent1"><a:tint val="40000"/></a:schemeClr></a:solidFill></a:fill></a:tcStyle></a:band1H>
<a:band1V><a:tcStyle><a:tcBdr/><a:fill><a:solidFill><a:schemeClr val="accent1"><a:tint val="40000"/></a:schemeClr></a:solidFill></a:fill></a:tcStyle></a:band1V>
<a:lastCol><a:tcTxStyle b="on"><a:fontRef idx="minor"/><a:schemeClr val="lt1"/></a:tcTxStyle><a:tcStyle><a:tcBdr/><a:fill><a:solidFill><a:schemeClr val="accent1"/></a:solidFill></a:fill></a:tcStyle></a:lastCol>
<a:firstCol><a:tcTxStyle b="on"><a:fontRef idx="minor"/><a:schemeClr val="lt1"/></a:tcTxStyle><a:tcStyle><a:tcBdr/><a:fill><a:solidFill><a:schemeClr val="accent1"/></a:solidFill></a:fill></a:tcStyle></a:firstCol>
<a:lastRow><a:tcTxStyle b="on"><a:fontRef idx="minor"/><a:schemeClr val="lt1"/></a:tcTxStyle><a:tcStyle><a:tcBdr><a:top><a:ln w="38100"><a:solidFill><a:schemeClr val="lt1"/></a:solidFill></a:ln></a:top></a:tcBdr><a:fill><a:solidFill><a:schemeClr val="accent1"/></a:solidFill></a:fill></a:tcStyle></a:lastRow>
<a:firstRow><a:tcTxStyle b="on"><a:fontRef idx="minor"/><a:schemeClr val="lt1"/></a:tcTxStyle><a:tcStyle><a:tcBdr><a:bottom><a:ln w="38100"><a:solidFill><a:schemeClr val="lt1"/></a:solidFill></a:ln></a:bottom></a:tcBdr><a:fill><a:solidFill><a:schemeClr val="accent1"/></a:solidFill></a:fill></a:tcStyle></a:firstRow>
</a:tblStyle>`

const tableGrid = `<a:tblStyle xmlns:a="` + nsA + `" styleId="` + styleTableGrid + `" styleName="No Style, Table Grid">
<a:wholeTbl><a:tcTxStyle><a:fontRef idx="minor"/><a:schemeClr val="tx1"/></a:tcTxStyle><a:tcStyle><a:tcBdr>
<a:left><a:ln w="12700"><a:solidFill><a:schemeClr val="tx1"/></a:solidFill></a:ln></a:left>
<a:right><a:ln w="12700"><a:solidFill><a:schemeClr val="tx1"/></a:solidFill></a:ln></a:right>
<a:top><a:ln w="12700"><a:solidFill><a:schemeClr val="tx1"/></a:solidFill></a:ln></a:top>
<a:bottom><a:ln w="12700"><a:solidFill><a:schemeClr val="tx1"/></a:solidFill></a:ln></a:bottom>
<a:insideH><a:ln w="12700"><a:solidFill><a:schemeClr val="tx1"/></a:solidFill></a:ln></a:insideH>
<a:insideV><a:ln w="12700"><a:solidFill><a:schemeClr val="tx1"/></a:solidFill></a:ln></a:insideV>
</a:tcBdr><a:fill><a:noFill/></a:fill></a:tcStyle></a:wholeTbl></a:tblStyle>`

var (
	builtinOnce   sync.Once
	builtinStyles map[string]*ox.Node
)

func builtinStyle(id string) *ox.Node {
	builtinOnce.Do(func() {
		builtinStyles = map[string]*ox.Node{}
		for _, src := range []string{medium2, tableGrid} {
			if d, err := ox.Parse([]byte(src)); err == nil {
				builtinStyles[d.Root.Attr("styleId")] = d.Root
			}
		}
	})
	return builtinStyles[id]
}

// tableStyle finds the style a table names.
func (d *Doc) tableStyle(id string) *ox.Node {
	if id == "" || id == styleNoGrid {
		return nil
	}
	if d.tableStyles != nil {
		for _, s := range d.tableStyles.Root.Elements("a:tblStyle") {
			if s.Attr("styleId") == id {
				return s
			}
		}
	}
	if s := builtinStyle(id); s != nil {
		return s
	}
	return builtinStyle(styleMedium2) // one of the built-in ones the file does not carry
}

// layoutTable works out the grid and the cells' words. Rows grow to hold their text.
func (r *Renderer) layoutTable(sh *Shape, x *scope, l *Layout, number int) *Table {
	tbl := sh.Node.Path("a:graphic", "a:graphicData", "a:tbl")
	grid := tbl.Child("a:tblGrid").Elements("a:gridCol")
	rows := tbl.Elements("a:tr")
	if len(grid) > maxTableCols {
		grid = grid[:maxTableCols]
	}
	if len(rows) > maxTableRows {
		rows = rows[:maxTableRows]
	}
	t := &Table{ColX: make([]float64, len(grid)+1), RowY: make([]float64, len(rows)+1)}
	for i, g := range grid {
		t.ColX[i+1] = t.ColX[i] + attrFloat(g, "w", 0)/emuPerPt
	}
	// A table scaled in its frame stays at its grid's size in PowerPoint's own model; a frame that differs is scaled
	// to it, as happens to tables in groups.
	kx := 1.0
	if w := t.ColX[len(grid)]; w > 0 && sh.Box.W > 0 {
		kx = sh.Box.W / w
		for i := range t.ColX {
			t.ColX[i] *= kx
		}
	}
	pr := tbl.Child("a:tblPr")
	style := r.D.tableStyle(pr.Child("a:tableStyleId").InnerText())
	flags := tableFlags{
		firstRow: attrBool(pr, "firstRow", false), lastRow: attrBool(pr, "lastRow", false),
		firstCol: attrBool(pr, "firstCol", false), lastCol: attrBool(pr, "lastCol", false),
		bandRow: attrBool(pr, "bandRow", false), bandCol: attrBool(pr, "bandCol", false),
		rows: len(rows), cols: len(grid),
	}
	lists := r.D.tableChain(x)
	heights := make([]float64, len(rows))
	for i, row := range rows {
		heights[i] = attrFloat(row, "h", 0) / emuPerPt
	}
	type pending struct {
		c  *Cell
		tc *ox.Node
	}
	var cells []pending
	for ri, row := range rows {
		tcs := row.Elements("a:tc")
		for ci := 0; ci < len(tcs) && ci < len(grid); ci++ {
			tc := tcs[ci]
			if attrBool(tc, "hMerge", false) || attrBool(tc, "vMerge", false) {
				continue
			}
			c := &Cell{Node: tc, Row: ri, Col: ci, RowSpan: max(int(attrInt(tc, "rowSpan", 1)), 1), ColSpan: max(int(attrInt(tc, "gridSpan", 1)), 1)}
			c.RowSpan = min(c.RowSpan, len(rows)-ri)
			c.ColSpan = min(c.ColSpan, len(grid)-ci)
			c.X, c.W = t.ColX[ci], t.ColX[ci+c.ColSpan]-t.ColX[ci]
			parts := flags.partsFor(ri, ci, c)
			run, fill, edges := r.cellStyle(style, parts, x, flags, ri, ci, c)
			c.Fill = fill
			c.Left, c.Top, c.Right, c.Under = edges[0], edges[1], edges[2], edges[3]
			tp := tc.Child("a:tcPr")
			if f := x.readFill(fillChild(tp)); f != nil {
				c.Fill = f
			}
			for i, side := range []struct {
				name string
				to   **Line
			}{{"a:lnL", &c.Left}, {"a:lnT", &c.Top}, {"a:lnR", &c.Right}, {"a:lnB", &c.Under}} {
				if ln := tp.Child(side.name); ln != nil {
					v := x.readLine(ln, Line{})
					*side.to = &v
					c.Own[i] = true
				}
			}
			src := &textSource{tx: tc.Child("a:txBody"), lists: lists, x: x, extra: run, number: number}
			if src.tx != nil {
				src.own = listRef{src.tx.Child("a:lstStyle"), x}
				c.Frame = r.layoutFrame(src, cellBody(tp), frameArgs{0, 0, c.W, 0, Identity})
				if c.RowSpan == 1 {
					ins := attrFloat(tp, "marT", 45720)/emuPerPt + attrFloat(tp, "marB", 45720)/emuPerPt
					heights[ri] = max(heights[ri], c.Frame.Height+ins)
				}
			}
			cells = append(cells, pending{c, tc})
		}
	}
	for i, h := range heights {
		t.RowY[i+1] = t.RowY[i] + h
	}
	for _, p := range cells {
		c := p.c
		c.Y, c.H = t.RowY[c.Row], t.RowY[c.Row+c.RowSpan]-t.RowY[c.Row]
		if c.Frame != nil {
			offsetFrame(c.Frame, c.X, c.Y, c.H, p.tc.Child("a:tcPr"))
		}
		t.Cells = append(t.Cells, c)
	}
	return t
}

// offsetFrame puts a cell's frame, laid out at the origin, in its place in the table.
func offsetFrame(f *TextFrame, x, y, h float64, tp *ox.Node) {
	top := attrFloat(tp, "marT", 45720) / emuPerPt
	bottom := attrFloat(tp, "marB", 45720) / emuPerPt
	dy := y + top
	room := h - top - bottom
	switch tp.Attr("anchor") {
	case "ctr":
		dy += (room - f.Height) / 2
	case "b":
		dy += room - f.Height
	}
	f.Rect[0] += x
	f.Rect[1] = dy
	f.Rect[3] = room
	for i := range f.Paras {
		for k := range f.Paras[i].Spans {
			f.Paras[i].Spans[k].X += x
			f.Paras[i].Spans[k].Y += dy
		}
	}
}

// cellBody turns a cell's margins into what a frame reads.
func cellBody(tp *ox.Node) bodyProps {
	n := ox.New("a:bodyPr",
		"lIns", attrStr(tp, "marL", "91440"), "rIns", attrStr(tp, "marR", "91440"),
		"tIns", "0", "bIns", "0")
	if v := tp.Attr("vert"); v != "" {
		n.SetAttr("vert", v)
	}
	return bodyProps{chain: []*ox.Node{n}}
}

func attrStr(n *ox.Node, name, fallback string) string {
	if v, ok := n.Lookup(name); ok {
		return v
	}
	return fallback
}

// tableChain is what a table's text inherits: the presentation's default text style.
func (d *Doc) tableChain(x *scope) []listRef {
	return []listRef{{d.defaultText, x}}
}

type tableFlags struct {
	firstRow, lastRow, firstCol, lastCol, bandRow, bandCol bool
	rows, cols                                             int
}

// partsFor names the parts of a table style that apply to a cell, least specific first.
func (f tableFlags) partsFor(r, c int, cell *Cell) []string {
	out := []string{"a:wholeTbl"}
	row, col := r, c
	if f.firstRow {
		row--
	}
	if f.firstCol {
		col--
	}
	if f.bandCol && col >= 0 && !(f.lastCol && c == f.cols-1) {
		if col%2 == 0 {
			out = append(out, "a:band1V")
		} else {
			out = append(out, "a:band2V")
		}
	}
	if f.bandRow && row >= 0 && !(f.lastRow && r == f.rows-1) {
		if row%2 == 0 {
			out = append(out, "a:band1H")
		} else {
			out = append(out, "a:band2H")
		}
	}
	if f.firstCol && c == 0 {
		out = append(out, "a:firstCol")
	}
	if f.lastCol && c+cell.ColSpan == f.cols {
		out = append(out, "a:lastCol")
	}
	if f.lastRow && r+cell.RowSpan == f.rows {
		out = append(out, "a:lastRow")
	}
	if f.firstRow && r == 0 {
		out = append(out, "a:firstRow")
	}
	return out
}

// cellStyle reads what a table style gives a cell: its words, fill and borders.
func (r *Renderer) cellStyle(style *ox.Node, parts []string, x *scope, f tableFlags, ri, ci int, cell *Cell) (*RunProps, *Fill, [4]*Line) {
	run := &RunProps{}
	var fill *Fill
	var edges [4]*Line
	if style == nil {
		return run, fill, edges
	}
	for _, name := range parts {
		p := style.Child(name)
		if p == nil {
			continue
		}
		if tx := p.Child("a:tcTxStyle"); tx != nil {
			if tx.Attr("b") == "on" {
				run.Bold = true
			}
			if tx.Attr("i") == "on" {
				run.Italic = true
			}
			for _, c := range tx.Elements() {
				if v, ok := x.col.resolve(c); ok {
					run.Color, run.HasColor = v, true
				}
			}
		}
		tc := p.Child("a:tcStyle")
		if fl := tc.Child("a:fill"); fl != nil {
			if v := x.readFill(fillChild(fl)); v != nil {
				fill = v
			}
		}
		if ref := tc.Child("a:fillRef"); ref != nil {
			if it, kx := x.themeItem(ref, x.col.theme.Fills); it != nil {
				fill = kx.readFill(it)
			}
		}
		b := tc.Child("a:tcBdr")
		if b == nil {
			continue
		}
		line := func(name string) *Line {
			e := b.Child(name)
			if e == nil {
				return nil
			}
			if ln := e.Child("a:ln"); ln != nil {
				v := x.readLine(ln, Line{})
				return &v
			}
			if ref := e.Child("a:lnRef"); ref != nil {
				if it, kx := x.themeItem(ref, x.col.theme.Lines); it != nil {
					v := kx.readLine(it, Line{})
					return &v
				}
			}
			return nil
		}
		side := func(i int, outer bool, outerName, innerName string) {
			name := innerName
			if outer {
				name = outerName
			}
			if v := line(name); v != nil {
				edges[i] = v
			}
		}
		side(0, ci == 0, "a:left", "a:insideV")
		side(1, ri == 0, "a:top", "a:insideH")
		side(2, ci+cell.ColSpan == f.cols, "a:right", "a:insideV")
		side(3, ri+cell.RowSpan == f.rows, "a:bottom", "a:insideH")
	}
	return run, fill, edges
}

// Table lays a table on a slide out, for the editor.
func (r *Renderer) Table(s *Slide, sh *Shape) *Table {
	if sh.Kind != KindTable {
		return nil
	}
	return r.layoutTable(sh, r.D.slideScope(s), s.Layout, r.D.Number(s))
}

// FitTable writes the height rows grew to, for what is typed in them, into the table and its frame.
func (r *Renderer) FitTable(s *Slide, sh *Shape) {
	t := r.Table(s, sh)
	if t == nil {
		return
	}
	rows := sh.Node.Path("a:graphic", "a:graphicData", "a:tbl").Elements("a:tr")
	for i := 0; i < len(rows) && i+1 < len(t.RowY); i++ {
		if h := emu(t.RowY[i+1] - t.RowY[i]); h > attrInt(rows[i], "h", 0) {
			rows[i].SetAttr("h", strconv.FormatInt(h, 10))
		}
	}
	box := sh.Box
	box.H = t.RowY[len(t.RowY)-1]
	if box.H > sh.Box.H {
		writeBox(sh, box)
		sh.Box = box
	}
}

// table draws a table.
func (d *drawer) table(sh *Shape) {
	if !d.visible(sh.Box, 40) {
		return
	}
	t := d.r.layoutTable(sh, d.x, d.l, d.number)
	base := sh.Box.Upright()
	m := d.view.Mul(base)
	for _, c := range t.Cells {
		if c.Fill == nil || c.Fill.None {
			continue
		}
		lines := flatten(rectOps(c.W, c.H), m.Mul(Translate(c.X, c.Y)))
		d.fillLines(lines, c.Fill, Box{W: c.W, H: c.H}, m.Mul(Translate(c.X, c.Y)))
	}
	edge := func(ln *Line, x0, y0, x1, y1 float64) {
		if ln == nil {
			return
		}
		st, ok := d.strokeOf(ln)
		if !ok {
			return
		}
		st.cap = "sq"
		ax, ay := m.Apply(x0, y0)
		bx, by := m.Apply(x1, y1)
		d.strokeLines([]polyline{{pts: []pt{{ax, ay}, {bx, by}}}}, &st, ln.Fill, Box{W: 1, H: 1}, m)
	}
	for _, own := range []bool{false, true} {
		for _, c := range t.Cells {
			if c.Own[0] == own {
				edge(c.Left, c.X, c.Y, c.X, c.Y+c.H)
			}
			if c.Own[1] == own {
				edge(c.Top, c.X, c.Y, c.X+c.W, c.Y)
			}
			if c.Own[2] == own {
				edge(c.Right, c.X+c.W, c.Y, c.X+c.W, c.Y+c.H)
			}
			if c.Own[3] == own {
				edge(c.Under, c.X, c.Y+c.H, c.X+c.W, c.Y+c.H)
			}
		}
	}
	ink := d.defaultInk()
	for _, c := range t.Cells {
		if c.Frame != nil && len(c.Frame.Paras) > 0 {
			c.Frame.Matrix = base.Mul(c.Frame.Matrix)
			d.text(c.Frame, ink)
		}
	}
}
