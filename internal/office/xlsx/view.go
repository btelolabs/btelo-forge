package xlsx

import (
	"slices"
	"strconv"

	"github.com/btelolabs/btelo-forge/internal/office/formula"
)

// What the editor reads, and small edits of its own.

// Find is the place of a column in the row, or where it would go.
func (r *Row) Find(col int32) int {
	i, _ := r.find(col)
	return i
}

// ValueOf is what a cell holds.
func (wb *Workbook) ValueOf(c *Cell) formula.Value { return wb.value(c) }

// IsRich reports a shared string with formatted stretches.
func (wb *Workbook) IsRich(c *Cell) bool {
	if c.Kind != SharedS {
		return false
	}
	i := int(c.Num)
	return i >= 0 && i < len(wb.strs.items) && wb.strs.items[i].rich
}

// Touch notes a change the editor made in pieces, as a column being dragged wider.
func (wb *Workbook) Touch() { wb.version++ }

// ActiveTab is the sheet the file was left on.
func (wb *Workbook) ActiveTab() int {
	n, err := strconv.Atoi(wb.doc.Root.Path("bookViews", "workbookView").Attr("activeTab"))
	if err != nil {
		return 0
	}
	return n
}

// HasColumnStyles reports columns with formats of their own, so that empty cells can show one.
func (s *Sheet) HasColumnStyles() bool {
	return slices.ContainsFunc(s.cols, func(c Col) bool { return c.Style != 0 })
}

// ShowGrid reports whether gridlines are on.
func (s *Sheet) ShowGrid() bool {
	v, ok := s.doc.Root.Path("sheetViews", "sheetView").Lookup("showGridLines")
	return !ok || v != "0" && v != "false"
}

// EachInColumn visits a column's cells.
func (s *Sheet) EachInColumn(col int, fn func(r int, c *Cell)) {
	for r, row := range s.rows {
		if row == nil {
			continue
		}
		if i, ok := row.find(int32(col)); ok {
			fn(r, &row.Cells[i])
		}
	}
}

// CopyDown copies src over the rest of dst, as Ctrl+D and Ctrl+R do: no series, formulas moved as if copied.
func (s *Sheet) CopyDown(src, dst formula.Area) {
	cl := s.Copy(src)
	s.Paste(cl, dst, true)
}

// DefaultHeight is the height of rows that do not say, in points.
func (s *Sheet) DefaultHeight() float64 { return s.defH }
