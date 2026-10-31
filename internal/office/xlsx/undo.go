package xlsx

import (
	"slices"

	"github.com/btelolabs/btelo-forge/internal/office/formula"
	"github.com/btelolabs/btelo-forge/internal/office/ox"
)

// History: a step is the old rows and trees that its edits replaced. As rows are not changed once a step closes, the
// old ones stay good. Undoing swaps them back and keeps what it took out, which is the step of the other direction.

// History's bounds.
const (
	maxSteps       = 500
	maxHistoryCost = 1 << 30
)

type entry interface {
	// swap puts this entry's state back, and returns the entry that returns to the state it replaced.
	swap(wb *Workbook) entry
	cost() int64
}

type step struct {
	entries []entry
	spent   int64
	trees   map[*Sheet]bool
	rows    map[*Sheet]bool
	book    bool
}

func newStep() *step { return &step{trees: map[*Sheet]bool{}, rows: map[*Sheet]bool{}} }

func (st *step) add(e entry) {
	st.entries = append(st.entries, e)
	st.spent += e.cost()
}

type rowEntry struct {
	s   *Sheet
	r   int
	row *Row
}

func (e *rowEntry) swap(wb *Workbook) entry {
	s := e.s
	if e.r >= len(s.rows) {
		s.rows = append(s.rows, make([]*Row, e.r+1-len(s.rows))...)
	}
	old := s.rows[e.r]
	s.rows[e.r] = e.row
	s.touched = true
	wb.resync = append(wb.resync, resyncRow{s, e.r, old, e.row})
	return &rowEntry{s, e.r, old}
}

func (e *rowEntry) cost() int64 {
	if e.row == nil {
		return 32
	}
	return 96 + int64(len(e.row.Cells))*56
}

type rowsEntry struct {
	s    *Sheet
	rows []*Row
}

func (e *rowsEntry) swap(wb *Workbook) entry {
	old := e.s.rows
	e.s.rows = e.rows
	e.s.touched = true
	wb.rebuild = true
	return &rowsEntry{e.s, old}
}

func (e *rowsEntry) cost() int64 { return int64(len(e.rows)) * 8 }

type treeEntry struct {
	s   *Sheet
	doc *ox.Doc
}

func (e *treeEntry) swap(wb *Workbook) entry {
	old := e.s.doc
	e.s.doc = e.doc
	e.s.touched = true
	e.s.sync()
	return &treeEntry{e.s, old}
}

func (e *treeEntry) cost() int64 { return 4096 }

type bookEntry struct {
	doc    *ox.Doc
	sheets []*Sheet
	names  []string
}

func (e *bookEntry) swap(wb *Workbook) entry {
	old := &bookEntry{wb.doc, wb.Sheets, sheetNames(wb)}
	wb.doc, wb.Sheets = e.doc, e.sheets
	for i, s := range wb.Sheets {
		s.Name = e.names[i]
	}
	wb.syncNames()
	wb.rebuild = true
	return old
}

func (e *bookEntry) cost() int64 { return 4096 }

func sheetNames(wb *Workbook) []string {
	out := make([]string, len(wb.Sheets))
	for i, s := range wb.Sheets {
		out[i] = s.Name
	}
	return out
}

// partEntry is a part changed along with cells: a table, a drawing, comments, a chart.
type partEntry struct {
	name string
	data []byte
}

func (e *partEntry) swap(wb *Workbook) entry {
	old := wb.pkg.Part(e.name).Data
	wb.pkg.SetData(e.name, e.data)
	return &partEntry{e.name, old}
}

func (e *partEntry) cost() int64 { return int64(len(e.data)) }

// resyncRow notes a row undo replaced, so the formula engine learns of it.
type resyncRow struct {
	s        *Sheet
	r        int
	old, new *Row
}

// Begin opens a step of history; every edit happens inside one. A step stays open until the next, so that results
// worked out later belong to it.
func (wb *Workbook) Begin() {
	wb.calc.Recalc(0)
	wb.gen++
	if n := len(wb.undo); n > 0 && len(wb.undo[n-1].entries) == 0 {
		wb.undo = wb.undo[:n-1]
	}
	wb.cur = newStep()
	wb.undo = append(wb.undo, wb.cur)
	wb.redo = nil
	wb.version++
	if len(wb.undo) > maxSteps {
		wb.undo = slices.Delete(wb.undo, 0, len(wb.undo)-maxSteps)
	}
	spent := int64(0)
	for i := len(wb.undo) - 1; i >= 0; i-- {
		spent += wb.undo[i].spent
		if spent > maxHistoryCost && i > 0 {
			wb.undo = slices.Delete(wb.undo, 0, i)
			break
		}
	}
}

// saveTree keeps the sheet's tree for this step before it is changed.
func (s *Sheet) saveTree() {
	s.touched = true
	st := s.wb.cur
	if st == nil || st.trees[s] {
		return
	}
	st.trees[s] = true
	st.add(&treeEntry{s, s.doc.Clone()})
}

// saveRows keeps the sheet's row table for this step before rows move.
func (s *Sheet) saveRows() {
	s.touched = true
	st := s.wb.cur
	if st == nil || st.rows[s] {
		return
	}
	st.rows[s] = true
	st.add(&rowsEntry{s, slices.Clone(s.rows)})
}

// saveBook keeps the workbook's tree and sheet list for this step.
func (wb *Workbook) saveBook() {
	st := wb.cur
	if st == nil || st.book {
		return
	}
	st.book = true
	st.add(&bookEntry{wb.doc.Clone(), slices.Clone(wb.Sheets), sheetNames(wb)})
}

// setPart replaces a part as a part of the step.
func (wb *Workbook) setPart(name string, data []byte) {
	if wb.cur != nil {
		wb.cur.add(&partEntry{name, wb.pkg.Part(name).Data})
	}
	wb.pkg.SetData(name, data)
}

// CanUndo reports whether there is a step to undo.
func (wb *Workbook) CanUndo() bool {
	return slices.ContainsFunc(wb.undo, func(st *step) bool { return len(st.entries) > 0 })
}

// CanRedo reports whether there is a step to redo.
func (wb *Workbook) CanRedo() bool { return len(wb.redo) > 0 }

// run applies a step backwards and returns its inverse.
func (wb *Workbook) run(st *step) *step {
	inv := newStep()
	for i := len(st.entries) - 1; i >= 0; i-- {
		inv.add(st.entries[i].swap(wb))
	}
	if wb.rebuild {
		wb.fillEngine(false)
	} else {
		for _, r := range wb.resync {
			wb.syncRow(r)
		}
	}
	wb.resync, wb.rebuild = wb.resync[:0], false
	wb.version++
	return inv
}

// syncRow tells the engine of the formulas that differ between two versions of a row.
func (wb *Workbook) syncRow(r resyncRow) {
	si := r.s.index()
	if si < 0 {
		return
	}
	at := func(c int32) formula.Cell { return formula.Cell{Sheet: int32(si), Row: int32(r.r), Col: c} }
	var was, now []Cell
	if r.old != nil {
		was = r.old.Cells
	}
	if r.new != nil {
		now = r.new.Cells
	}
	i, j := 0, 0
	for i < len(was) || j < len(now) {
		switch {
		case j >= len(now) || i < len(was) && was[i].Col < now[j].Col:
			if was[i].F != nil {
				wb.calc.Set(at(was[i].Col), nil, 0, 0, false)
			}
			i++
		case i >= len(was) || now[j].Col < was[i].Col:
			wb.setEngine(at(now[j].Col), &now[j])
			j++
		default:
			if was[i].F != now[j].F {
				wb.setEngine(at(now[j].Col), &now[j])
			}
			i++
			j++
		}
	}
}

func (wb *Workbook) setEngine(at formula.Cell, c *Cell) {
	if c.F == nil || c.F.Expr == nil {
		wb.calc.Set(at, nil, 0, 0, false)
		return
	}
	wb.calc.Set(at, c.F.Expr, int(c.F.DR), int(c.F.DC), c.F.Array != "")
}

// Undo takes back the last step.
func (wb *Workbook) Undo() bool {
	wb.calc.Recalc(0)
	for n := len(wb.undo); n > 0 && len(wb.undo[n-1].entries) == 0; n = len(wb.undo) {
		wb.undo = wb.undo[:n-1]
	}
	n := len(wb.undo)
	if n == 0 {
		return false
	}
	st := wb.undo[n-1]
	wb.undo = wb.undo[:n-1]
	wb.gen++
	wb.cur = nil
	wb.redo = append(wb.redo, wb.run(st))
	return true
}

// Redo does a step again.
func (wb *Workbook) Redo() bool {
	wb.calc.Recalc(0)
	n := len(wb.redo)
	if n == 0 {
		return false
	}
	st := wb.redo[n-1]
	wb.redo = wb.redo[:n-1]
	wb.gen++
	wb.cur = nil
	wb.undo = append(wb.undo, wb.run(st))
	return true
}
