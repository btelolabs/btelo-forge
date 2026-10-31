// Package xlsx is a spreadsheet kept in its own file format. A worksheet's sheetData can be huge, so its rows are held
// in a compact model that remembers where each cell's XML is in the file: a cell nobody touched goes back out as the
// bytes it came in as, and a touched one keeps every attribute and child that is not its value, formula or style.
// Everything else in a worksheet part, the workbook part and the styles part is an ox tree, changed where it is
// edited, so what is not understood here survives. Parts that were not changed are written back unchanged.
//
// A Row is never changed once a step of history or a save can see it: edits clone it (see writable), which makes undo
// and saving on another goroutine cheap.
package xlsx

import (
	"slices"

	"github.com/btelolabs/btelo-forge/internal/office/dml"
	"github.com/btelolabs/btelo-forge/internal/office/formula"
	"github.com/btelolabs/btelo-forge/internal/office/opc"
	"github.com/btelolabs/btelo-forge/internal/office/ox"
)

// The sheet's limits.
const (
	MaxRows = formula.MaxRows
	MaxCols = formula.MaxCols
)

// maxDenseRows bounds the row tables of a whole workbook: a hostile file cannot make each of many sheets claim the last
// row.
const maxDenseRows = 64 << 20

// Kind is what a cell holds.
type Kind uint8

const (
	Empty    Kind = iota // only a style
	Number               // Num
	SharedS              // Num is the index in the shared strings
	InlineS              // Text, in the cell's own XML
	FormulaS             // Text: a formula's text result
	Bool                 // Num is 0 or 1
	ErrorV               // Text is the code
)

// Flags of a cell: what about it differs from its XML in the file.
const (
	flagValue   uint8 = 1 << iota // the value
	flagFormula                   // the formula
	flagStyle                     // the style
	flagMoved                     // its address
)

// Cell is one cell. Size matters: a file can have millions.
type Cell struct {
	Col    int32
	Style  int32 // index in cellXfs
	Num    float64
	Text   string
	F      *Formula
	raw    uint32 // where its XML is in the sheet's source
	rawLen uint32 // zero for a cell that is not in the file
	Kind   Kind
	flags  uint8
}

// Formula is a cell's formula. Cells of a shared formula share Expr; DR, DC is how far the cell is from where Expr was
// written.
type Formula struct {
	Expr   *formula.Expr // nil when the text does not parse; the cached value then stays
	Text   string        // as written; "" for a shared formula's other cells
	DR, DC int32
	Shared int32  // the group of a shared formula, or -1
	Array  string // the range of an array formula
}

// Row is one row.
type Row struct {
	Cells   []Cell
	Height  float64 // in points; 0 is the sheet's default
	Style   int32   // with Custom, the style of the row's empty cells
	Hidden  bool
	Custom  bool // customFormat
	Manual  bool // customHeight
	dirty   bool // differs from its XML
	gen     uint32
	orig    int32  // its number in the file, -1 if none
	raw     uint32 // the whole <row> element in the source
	rawLen  uint32
	headLen uint32 // the length of its start tag
}

// Col is a run of columns with one <col> element.
type Col struct {
	Min, Max int // zero-based, inclusive
	Width    float64
	Hidden   bool
	Style    int32
	Custom   bool // the width was set
}

// Sheet is one worksheet.
type Sheet struct {
	wb   *Workbook
	Name string
	part string // the part; "" until a new sheet is first saved
	rid  string
	id   string // sheetId

	src  []byte  // the sheetData's text in the file, which raw offsets point into
	doc  *ox.Doc // the worksheet with its sheetData cut out
	rows []*Row
	// What the tree says, ready to use; sync makes these from it.
	cols     []Col
	merges   []formula.Area
	mergeIdx map[int32][]int32 // row>>6 to the merges touching that block
	defH     float64           // default row height, points
	defW     float64           // default column width, characters
	freezeR  int
	freezeC  int
	tab      string
	hidden   bool
	touched  bool // the part must be written
	shared   map[int32]*Formula
	nextSI   int32
	boundC   int // how many columns are in use, once boundOK
	boundOK  bool
	chart    bool // a chart sheet: its part stays as it is and it is not shown
}

// Workbook is an open file.
type Workbook struct {
	pkg     *opc.Package
	part    string
	doc     *ox.Doc
	Sheets  []*Sheet
	Styles  *Styles
	strs    *strTable
	date    bool // date1904
	calc    *formula.Engine
	names   map[string]string // lower-case name, or "index!name" for a local one, to its text
	version uint64
	gen     uint32
	cur     *step
	undo    []*step
	redo    []*step
	spent   int64 // an estimate of what the history holds, in bytes
	resync  []resyncRow
	rebuild bool // undo moved rows: the engine starts over
	scheme  dml.Scheme
	charts  map[string]*chartEntry
}

// Version changes with every change.
func (wb *Workbook) Version() uint64 { return wb.version }

// Date1904 reports the workbook's date system.
func (wb *Workbook) Date1904() bool { return wb.date }

// Sheet is the sheet of the name, nil if none.
func (wb *Workbook) Sheet(name string) *Sheet {
	if i := wb.SheetIndex(name); i >= 0 {
		return wb.Sheets[i]
	}
	return nil
}

// index is the sheet's place.
func (s *Sheet) index() int { return slices.Index(s.wb.Sheets, s) }

// RowCount is how many rows have a table entry: the last used row plus one.
func (s *Sheet) RowCount() int {
	n := len(s.rows)
	for n > 0 && s.rows[n-1] == nil {
		n--
	}
	return n
}

// RowAt is the row, nil if it has nothing.
func (s *Sheet) RowAt(r int) *Row {
	if r < 0 || r >= len(s.rows) {
		return nil
	}
	return s.rows[r]
}

// find is the cell's place in the row and whether it is there.
func (r *Row) find(col int32) (int, bool) {
	lo, hi := 0, len(r.Cells)
	for lo < hi {
		m := int(uint(lo+hi) >> 1)
		if r.Cells[m].Col < col {
			lo = m + 1
		} else {
			hi = m
		}
	}
	return lo, lo < len(r.Cells) && r.Cells[lo].Col == col
}

// Cell is the cell, nil if it has no entry. It must not be changed.
func (s *Sheet) Cell(r, c int) *Cell {
	row := s.RowAt(r)
	if row == nil {
		return nil
	}
	i, ok := row.find(int32(c))
	if !ok {
		return nil
	}
	return &row.Cells[i]
}

// value is what a cell holds, a formula's result for a formula.
func (wb *Workbook) value(c *Cell) formula.Value {
	switch c.Kind {
	case Number:
		return formula.Num(c.Num)
	case SharedS:
		return formula.Str(wb.strs.text(int(c.Num)))
	case InlineS, FormulaS:
		return formula.Str(c.Text)
	case Bool:
		return formula.BoolOf(c.Num != 0)
	case ErrorV:
		return formula.Err(c.Text)
	}
	return formula.Value{}
}

// ValueAt is the cell's value; Blank when it is empty.
func (s *Sheet) ValueAt(r, c int) formula.Value {
	if cell := s.Cell(r, c); cell != nil {
		return s.wb.value(cell)
	}
	return formula.Value{}
}

// FormulaText is the cell's formula without its =, "" when it has none.
func (c *Cell) FormulaText() string {
	if c.F == nil {
		return ""
	}
	if c.F.Text != "" || c.F.Expr == nil {
		return c.F.Text
	}
	return c.F.Expr.Text(int(c.F.DR), int(c.F.DC))
}

// The rest is the workbook as the formula engine sees it.

// SheetIndex implements formula.Book.
func (wb *Workbook) SheetIndex(name string) int {
	for i, s := range wb.Sheets {
		if equalFold(s.Name, name) {
			return i
		}
	}
	return -1
}

// SheetName implements formula.Book.
func (wb *Workbook) SheetName(i int) string {
	if i < 0 || i >= len(wb.Sheets) {
		return ""
	}
	return wb.Sheets[i].Name
}

// Value implements formula.Book.
func (wb *Workbook) Value(sheet, row, col int) formula.Value {
	if sheet < 0 || sheet >= len(wb.Sheets) {
		return formula.Value{}
	}
	return wb.Sheets[sheet].ValueAt(row, col)
}

// Each implements formula.Book.
func (wb *Workbook) Each(sheet, r1, c1, r2, c2 int, fn func(row, col int, v formula.Value) bool) {
	if sheet < 0 || sheet >= len(wb.Sheets) {
		return
	}
	s := wb.Sheets[sheet]
	r2 = min(r2, len(s.rows)-1)
	for r := max(r1, 0); r <= r2; r++ {
		row := s.rows[r]
		if row == nil {
			continue
		}
		i, _ := row.find(int32(c1))
		for ; i < len(row.Cells) && int(row.Cells[i].Col) <= c2; i++ {
			c := &row.Cells[i]
			if c.Kind == Empty {
				continue
			}
			if !fn(r, int(c.Col), wb.value(c)) {
				return
			}
		}
	}
}

// Bounds implements formula.Book.
func (wb *Workbook) Bounds(sheet int) (rows, cols int) {
	if sheet < 0 || sheet >= len(wb.Sheets) {
		return 0, 0
	}
	s := wb.Sheets[sheet]
	if !s.boundOK {
		s.boundC, s.boundOK = 0, true
		for _, row := range s.rows {
			if row != nil && len(row.Cells) > 0 {
				s.boundC = max(s.boundC, int(row.Cells[len(row.Cells)-1].Col)+1)
			}
		}
	}
	return s.RowCount(), s.boundC
}

// noteCol widens the known extent; it may be wider than the truth, which only costs a formula a little time.
func (s *Sheet) noteCol(c int) { s.boundC = max(s.boundC, c+1) }

// Name implements formula.Book.
func (wb *Workbook) Name(name string, sheet int) string {
	key := lower(name)
	if v, ok := wb.names[localName(sheet, key)]; ok {
		return v
	}
	return wb.names[key]
}

// SetResult implements formula.Book.
func (wb *Workbook) SetResult(c formula.Cell, v formula.Value) {
	if int(c.Sheet) >= len(wb.Sheets) {
		return
	}
	s := wb.Sheets[c.Sheet]
	if v.Kind == formula.Array {
		cell := s.Cell(int(c.Row), int(c.Col))
		if cell == nil || cell.F == nil || v.Arr == nil || v.Arr.Rows == 0 || v.Arr.Cols == 0 {
			return
		}
		a, ok := formula.ParseArea(cell.F.Array)
		if !ok {
			a = formula.Area{R1: c.Row, C1: c.Col, R2: c.Row, C2: c.Col}
		}
		for r := a.R1; r <= a.R2; r++ {
			for col := a.C1; col <= a.C2; col++ {
				i, j := int(r-a.R1), int(col-a.C1)
				var e formula.Value
				switch {
				case i < v.Arr.Rows && j < v.Arr.Cols:
					e = v.Arr.V[i*v.Arr.Cols+j]
				case v.Arr.Rows == 1 && j < v.Arr.Cols:
					e = v.Arr.V[j]
				case v.Arr.Cols == 1 && i < v.Arr.Rows:
					e = v.Arr.V[i*v.Arr.Cols]
				case v.Arr.Rows == 1 && v.Arr.Cols == 1:
					e = v.Arr.V[0]
				default:
					e = formula.Err(formula.ErrNA)
				}
				s.setResult(int(r), int(col), e)
			}
		}
		return
	}
	s.setResult(int(c.Row), int(c.Col), v)
}

// setResult stores a computed value when it differs.
func (s *Sheet) setResult(r, c int, v formula.Value) {
	if v.Kind == formula.Blank {
		v = formula.Num(0)
	}
	if old := s.Cell(r, c); old != nil {
		switch {
		case v.Kind == formula.Number && old.Kind == Number && old.Num == v.Num,
			v.Kind == formula.Text && old.Kind == FormulaS && old.Text == v.Str,
			v.Kind == formula.Error && old.Kind == ErrorV && old.Text == v.Str,
			v.Kind == formula.Bool && old.Kind == Bool && old.Num == v.Num:
			return
		}
	}
	row := s.writable(r)
	i := row.ensure(int32(c))
	s.noteCol(c)
	cell := &row.Cells[i]
	switch v.Kind {
	case formula.Number:
		cell.Kind, cell.Num, cell.Text = Number, v.Num, ""
	case formula.Text:
		cell.Kind, cell.Num, cell.Text = FormulaS, 0, v.Str
	case formula.Bool:
		cell.Kind, cell.Num, cell.Text = Bool, v.Num, ""
	case formula.Error:
		cell.Kind, cell.Num, cell.Text = ErrorV, 0, v.Str
	}
	cell.flags |= flagValue
	s.touched = true
}

// ensure is the place of the cell in a writable row, adding it when missing.
func (r *Row) ensure(col int32) int {
	i, ok := r.find(col)
	if !ok {
		r.Cells = slices.Insert(r.Cells, i, Cell{Col: col, Style: r.defaultStyle()})
		r.Cells[i].flags = flagValue
	}
	return i
}

func (r *Row) defaultStyle() int32 {
	if r.Custom {
		return r.Style
	}
	return 0
}

// writable is the row to change: a private copy, with the old one kept for history unless this step already has one.
func (s *Sheet) writable(r int) *Row {
	wb := s.wb
	if r >= len(s.rows) {
		s.rows = append(s.rows, make([]*Row, r+1-len(s.rows))...)
	}
	old := s.rows[r]
	if old != nil && old.gen == wb.gen {
		old.dirty = true
		return old
	}
	row := &Row{orig: -1, gen: wb.gen, dirty: true}
	if old != nil {
		*row = *old
		row.gen, row.dirty = wb.gen, true
		row.Cells = slices.Clone(old.Cells)
	}
	if wb.cur != nil {
		wb.cur.add(&rowEntry{s, r, old})
	}
	s.rows[r] = row
	s.touched = true
	return row
}

// used is the area with cells in it.
func (s *Sheet) Used() formula.Area {
	a := formula.Area{R1: -1}
	for r, row := range s.rows {
		if row == nil || len(row.Cells) == 0 {
			continue
		}
		if a.R1 < 0 {
			a = formula.Area{R1: int32(r), C1: row.Cells[0].Col, R2: int32(r), C2: row.Cells[len(row.Cells)-1].Col}
			continue
		}
		a.R2 = int32(r)
		a.C1 = min(a.C1, row.Cells[0].Col)
		a.C2 = max(a.C2, row.Cells[len(row.Cells)-1].Col)
	}
	if a.R1 < 0 {
		return formula.Area{}
	}
	return a
}
