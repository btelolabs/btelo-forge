package ui

import (
	"math"
	"strings"
	"unicode/utf8"

	"github.com/btelolabs/btelo-forge/internal/gfx"
	"github.com/btelolabs/btelo-forge/internal/office/formula"
	"github.com/btelolabs/btelo-forge/internal/office/xlsx"
)

// What is done in the grid: the mouse, the keys and typing into cells.

// What a mouse drag on the grid is doing.
type sheetDragKind int

const (
	sheetDragNone sheetDragKind = iota
	sheetDragCells
	sheetDragRows
	sheetDragCols
	sheetDragFill
	sheetDragColSize
	sheetDragRowSize
	sheetDragVBar
	sheetDragHBar
	sheetDragPoint
)

type sheetDrag struct {
	kind   sheetDragKind
	at     int     // the column or row being sized
	start  float64 // its size when the drag began, or the scroll
	fillTo formula.Area
	begun  bool // a sizing drag has opened its step of history
}

const sheetGridID = "sheet/grid"

// isFormula reports that the text being typed is a formula.
func (ed *sheetEd) isFormula() bool {
	e := &ed.a.u.edit
	return ed.editing && len(e.text) > 0 && e.text[0] == '='
}

// pointing reports that a press on a cell now puts its reference in the formula, rather than leaving it.
func (ed *sheetEd) pointing() bool {
	if !ed.isFormula() {
		return false
	}
	e := &ed.a.u.edit
	if ed.pointFrom >= 0 && e.caret == ed.pointTo {
		return true
	}
	if e.caret == 0 {
		return false
	}
	return strings.ContainsRune("=+-*/^&(,<>:;%", e.text[e.caret-1])
}

// putRef puts a reference to an area in the formula, over the one being pointed at.
func (ed *sheetEd) putRef(v *sheetView, a formula.Area) {
	e := &ed.a.u.edit
	if ed.pointFrom < 0 || e.caret != ed.pointTo {
		ed.pointFrom, ed.pointTo = e.caret, e.caret
	}
	name := formula.AreaName(a)
	if v.s != ed.editSheet {
		name = formula.QuoteSheet(v.s.Name) + "!" + name
	}
	e.anchor, e.caret = ed.pointFrom, ed.pointTo
	e.insert(name)
	ed.pointTo = e.caret
}

func (ed *sheetEd) gridMouse(v *sheetView) {
	a := ed.a
	u := &a.u
	b := &ed.body
	if u.hover(sheetGridID, b.grid) && u.wheel != 0 {
		switch {
		case u.ctrl:
			ed.setZoom(ed.zoom * math.Pow(1.1, float64(u.wheel)))
		case u.shift:
			ed.scrollBy(v, -float64(u.wheel)*80, 0)
		default:
			ed.scrollBy(v, 0, -float64(u.wheel)*60)
		}
		u.wheel = 0
	}
	if ed.drag.kind != sheetDragNone {
		ed.dragStep(v)
		return
	}
	hotCol, hotRow := ed.sizeHandles(v)
	mx, my := u.mx, u.my
	if u.hover(sheetGridID, b.grid) {
		switch {
		case b.vbar.Contains(mx, my), b.hbar.Contains(mx, my):
		case hotCol >= 0 || hotRow >= 0:
			u.cursor = CursorResize
		case b.panes.Contains(mx, my) && ed.onFillHandle(v, mx, my):
			u.cursor = CursorCross
		case b.panes.Contains(mx, my):
			u.cursor = CursorCross
		}
	}
	if at := u.rightAt; at != nil && b.grid.Contains(at.X, at.Y) && !u.blocked {
		ed.finishEdit()
		ed.rightClick(v, at.X, at.Y)
		return
	}
	if !u.press(sheetGridID, b.grid) {
		return
	}
	x, y := u.pressX, u.pressY
	u.focus, u.focusDrawn = sheetFocus, true
	switch {
	case b.vbar.Contains(x, y):
		ed.drag = sheetDrag{kind: sheetDragVBar, start: v.scrollY}
		return
	case b.hbar.Contains(x, y):
		ed.drag = sheetDrag{kind: sheetDragHBar, start: v.scrollX}
		return
	}
	if ed.editing && !ed.pointing() {
		if !ed.commit(0, 0) {
			return
		}
	}
	switch {
	case hotCol >= 0:
		if u.double {
			ed.autofit(v, hotCol)
			return
		}
		ed.drag = sheetDrag{kind: sheetDragColSize, at: hotCol, start: v.colX[hotCol+1] - v.colX[hotCol]}
	case hotRow >= 0:
		if u.double {
			ed.wb.Begin()
			v.s.SetRowHeight(hotRow, hotRow, 0)
			ed.fitRow(v, hotRow)
			return
		}
		ed.drag = sheetDrag{kind: sheetDragRowSize, at: hotRow, start: v.rowTop(hotRow+1) - v.rowTop(hotRow)}
	case b.cols.Contains(x, y):
		_, c := ed.cellAt(v, x, b.panes.Y+1)
		ed.startHeading(v, true, c)
	case b.rows.Contains(x, y):
		r, _ := ed.cellAt(v, b.panes.X+1, y)
		ed.startHeading(v, false, r)
	case b.panes.Contains(x, y):
		ed.pressCell(v, x, y)
	case x < b.panes.X && y < b.panes.Y:
		v.selectArea(formula.Area{R2: xlsx.MaxRows - 1, C2: xlsx.MaxCols - 1})
	}
}

// selectArea makes one range the selection, the active cell its first.
func (v *sheetView) selectArea(a formula.Area) {
	v.active, v.anchor, v.tip = [2]int{int(a.R1), int(a.C1)}, [2]int{int(a.R1), int(a.C1)}, [2]int{int(a.R2), int(a.C2)}
	v.sel = []formula.Area{a}
}

func (ed *sheetEd) startHeading(v *sheetView, cols bool, at int) {
	u := &ed.a.u
	a := cellArea(0, at)
	if cols {
		a.R2 = xlsx.MaxRows - 1
		ed.drag = sheetDrag{kind: sheetDragCols, at: at}
	} else {
		a = formula.Area{R1: int32(at), R2: int32(at), C2: xlsx.MaxCols - 1}
		ed.drag = sheetDrag{kind: sheetDragRows, at: at}
	}
	if u.ctrl {
		v.sel = append(v.sel, a)
		v.active = [2]int{int(a.R1), int(a.C1)}
		return
	}
	v.selectArea(a)
}

func (ed *sheetEd) pressCell(v *sheetView, x, y float32) {
	u := &ed.a.u
	r, c := ed.cellAt(v, x, y)
	if ed.pointing() {
		ed.pointStart = [2]int{r, c}
		ed.putRef(v, cellArea(r, c))
		ed.drag = sheetDrag{kind: sheetDragPoint}
		return
	}
	if ed.onFillHandle(v, x, y) {
		ed.drag = sheetDrag{kind: sheetDragFill, fillTo: v.area()}
		return
	}
	if af, ok := v.s.AutoFilter(); ok && r == int(af.R1) && int(af.C1) <= c && c <= int(af.C2) && ed.filterButton(v, r, c).Contains(x, y) {
		ed.openFilter(v, c, af)
		return
	}
	if m, merged := v.s.MergeAt(r, c); merged {
		r, c = int(m.R1), int(m.C1)
	}
	switch {
	case u.double:
		ed.startEdit(v, r, c, v.s.InputText(r, c), false)
	case u.shift:
		v.stretch(r, c)
		ed.drag = sheetDrag{kind: sheetDragCells}
	case u.ctrl:
		v.active, v.anchor, v.tip = [2]int{r, c}, [2]int{r, c}, [2]int{r, c}
		v.sel = append(v.sel, v.mergedArea(cellArea(r, c)))
		ed.drag = sheetDrag{kind: sheetDragCells}
	default:
		v.moveTo(r, c)
		ed.drag = sheetDrag{kind: sheetDragCells}
	}
}

// stretch moves the free corner of the last range.
func (v *sheetView) stretch(r, c int) {
	v.tip = [2]int{r, c}
	v.sel[len(v.sel)-1] = v.mergedArea(spanArea(v.anchor, v.tip))
}

// onFillHandle reports a point on the handle at the selection's corner.
func (ed *sheetEd) onFillHandle(v *sheetView, x, y float32) bool {
	if len(v.sel) != 1 || ed.editing {
		return false
	}
	a := v.area()
	if wholeRows(a) || wholeCols(a) {
		return false
	}
	box := ed.cellBox(v, a)
	return math.Abs(float64(x-box.Right())) <= 5 && math.Abs(float64(y-box.Bottom())) <= 5
}

// sizeHandles finds the column or row edge under the mouse in the headings.
func (ed *sheetEd) sizeHandles(v *sheetView) (col, row int) {
	u := &ed.a.u
	b := &ed.body
	col, row = -1, -1
	x, y := u.mx, u.my
	if u.pressed {
		x, y = u.pressX, u.pressY
	}
	k := ed.scale()
	if b.cols.Contains(x, y) {
		_, c := ed.cellAt(v, x, b.panes.Y+1)
		for _, i := range []int{c - 1, c} {
			if i < 0 {
				continue
			}
			sx, _ := ed.screenX(v, i)
			e := sx + (v.colX[i+1]-v.colX[i])*k
			if math.Abs(float64(x)-e) <= 4 {
				col = i
			}
		}
	}
	if b.rows.Contains(x, y) {
		r, _ := ed.cellAt(v, b.panes.X+1, y)
		for _, i := range []int{r - 1, r} {
			if i < 0 {
				continue
			}
			sy, _ := ed.screenY(v, i)
			e := sy + (v.rowTop(i+1)-v.rowTop(i))*k
			if math.Abs(float64(y)-e) <= 3 {
				row = i
			}
		}
	}
	return col, row
}

func (ed *sheetEd) dragStep(v *sheetView) {
	a := ed.a
	u := &a.u
	b := &ed.body
	k := ed.scale()
	if !u.dragging(sheetGridID) {
		ed.dragEnd(v)
		u.letGo(sheetGridID)
		ed.drag = sheetDrag{}
		return
	}
	dx, dy := float64(u.mx-u.drag.x), float64(u.my-u.drag.y)
	// Near an edge a drag of cells scrolls the sheet on.
	edge := func() {
		if u.mx > b.panes.Right() {
			ed.scrollBy(v, 24, 0)
		} else if u.mx < b.panes.X {
			ed.scrollBy(v, -24, 0)
		}
		if u.my > b.panes.Bottom() {
			ed.scrollBy(v, 0, 20)
		} else if u.my < b.panes.Y {
			ed.scrollBy(v, 0, -20)
		}
	}
	clamp := func(x, y float32) (float32, float32) {
		return min(max(x, b.panes.X+1), b.panes.Right()-1), min(max(y, b.panes.Y+1), b.panes.Bottom()-1)
	}
	switch ed.drag.kind {
	case sheetDragVBar:
		_, h := ed.sheetExtent(v)
		v.scrollY = max(0, ed.drag.start+dy/float64(b.vbar.H)*h)
		ed.snapRows(v)
	case sheetDragHBar:
		w, _ := ed.sheetExtent(v)
		v.scrollX = max(0, ed.drag.start+dx/float64(b.hbar.W)*w)
	case sheetDragCells:
		edge()
		if u.drag.moved {
			x, y := clamp(u.mx, u.my)
			r, c := ed.cellAt(v, x, y)
			v.stretch(r, c)
		}
	case sheetDragPoint:
		edge()
		x, y := clamp(u.mx, u.my)
		r, c := ed.cellAt(v, x, y)
		ed.putRef(v, spanArea(ed.pointStart, [2]int{r, c}))
	case sheetDragCols:
		x, _ := clamp(u.mx, b.panes.Y+1)
		_, c := ed.cellAt(v, x, b.panes.Y+1)
		v.sel[len(v.sel)-1] = formula.Area{R2: xlsx.MaxRows - 1, C1: int32(min(c, ed.drag.at)), C2: int32(max(c, ed.drag.at))}
	case sheetDragRows:
		_, y := clamp(b.panes.X+1, u.my)
		r, _ := ed.cellAt(v, b.panes.X+1, y)
		v.sel[len(v.sel)-1] = formula.Area{C2: xlsx.MaxCols - 1, R1: int32(min(r, ed.drag.at)), R2: int32(max(r, ed.drag.at))}
	case sheetDragFill:
		edge()
		x, y := clamp(u.mx, u.my)
		r, c := ed.cellAt(v, x, y)
		ed.drag.fillTo = fillTarget(v.area(), r, c)
	case sheetDragColSize:
		// Shown as the drag goes: drawn from the sheet, so it is an edit, and each frame replaces the one before.
		w := max(ed.drag.start+dx/k, 0)
		ed.setColsPreview(v, ed.drag.at, w)
	case sheetDragRowSize:
		h := max(ed.drag.start+dy/k, 0)
		ed.setRowsPreview(v, ed.drag.at, h)
	}
}

// fillTarget is the range a fill reaches, along the axis the mouse has gone furthest from the source.
func fillTarget(src formula.Area, r, c int) formula.Area {
	down, right := 0, 0
	switch {
	case r > int(src.R2):
		down = r - int(src.R2)
	case r < int(src.R1):
		down = int(src.R1) - r
	}
	switch {
	case c > int(src.C2):
		right = c - int(src.C2)
	case c < int(src.C1):
		right = int(src.C1) - c
	}
	out := src
	if down >= right {
		out.R1, out.R2 = min(src.R1, int32(r)), max(src.R2, int32(r))
	} else {
		out.C1, out.C2 = min(src.C1, int32(c)), max(src.C2, int32(c))
	}
	return out
}

func (ed *sheetEd) dragEnd(v *sheetView) {
	switch ed.drag.kind {
	case sheetDragFill:
		if to := ed.drag.fillTo; to != v.area() {
			ed.wb.Begin()
			v.s.Fill(v.area(), to)
			v.sel = []formula.Area{to}
		}
	}
}

// setColsPreview sets the width of a column being dragged, and of the other chosen columns with it.
func (ed *sheetEd) setColsPreview(v *sheetView, c int, px float64) {
	if !ed.drag.begun {
		ed.wb.Begin()
		ed.drag.begun = true
	}
	c1, c2 := c, c
	for _, a := range v.sel {
		if wholeCols(a) && int(a.C1) <= c && c <= int(a.C2) {
			c1, c2 = int(a.C1), int(a.C2)
		}
	}
	v.s.SetColWidth(c1, c2, xlsx.WidthOfPixels(px))
	ed.wb.Touch()
}

func (ed *sheetEd) setRowsPreview(v *sheetView, r int, px float64) {
	if !ed.drag.begun {
		ed.wb.Begin()
		ed.drag.begun = true
	}
	r1, r2 := r, r
	for _, a := range v.sel {
		if wholeRows(a) && int(a.R1) <= r && r <= int(a.R2) {
			r1, r2 = int(a.R1), int(a.R2)
		}
	}
	v.s.SetRowHeight(r1, r2, math.Round(px*0.75*4)/4)
	ed.wb.Touch()
}

func (ed *sheetEd) scrollBy(v *sheetView, dx, dy float64) {
	v.scrollX = max(v.scrollX+dx, 0)
	v.scrollY = max(v.scrollY+dy, 0)
	if dy != 0 {
		ed.snapRows(v)
	}
	ed.a.requestFrame()
}

// snapRows keeps the top row whole.
func (ed *sheetEd) snapRows(v *sheetView) {
	v.scrollY = v.rowTop(v.rowAtY(v.scrollY + 0.5))
}

func (ed *sheetEd) setZoom(z float64) {
	ed.zoom = min(max(math.Round(z*100)/100, sheetMinZoom), sheetMaxZoom)
	ed.a.requestFrame()
}

// reveal scrolls until a cell shows.
func (ed *sheetEd) reveal(v *sheetView, r, c int) {
	k := ed.scale()
	p := ed.body.panes
	viewW, viewH := float64(p.W)/k, float64(p.H)/k
	fw, fh := v.colX[v.frozenC], v.rowTop(v.frozenR)
	if r >= v.frozenR {
		top, bottom := v.rowTop(r), v.rowTop(r+1)
		if top < v.scrollY+fh {
			v.scrollY = max(top-fh, 0)
		} else if bottom > v.scrollY+viewH {
			v.scrollY = bottom - viewH
			ed.snapUp(v)
		}
	}
	if c >= v.frozenC {
		left, right := v.colX[c], v.colX[c+1]
		if left < v.scrollX+fw {
			v.scrollX = max(left-fw, 0)
		} else if right > v.scrollX+viewW {
			v.scrollX = right - viewW
		}
	}
}

func (ed *sheetEd) snapUp(v *sheetView) {
	r := v.rowAtY(v.scrollY)
	if v.rowTop(r) < v.scrollY {
		r++
	}
	v.scrollY = v.rowTop(r)
}

// startEdit begins typing into a cell. typing starts it by a key, so arrows leave the cell again.
func (ed *sheetEd) startEdit(v *sheetView, r, c int, text string, typing bool) {
	u := &ed.a.u
	ed.editing, ed.typing, ed.inBar = true, typing, false
	ed.editSheet, ed.editR, ed.editC = v.s, r, c
	ed.pointFrom = -1
	n := utf8.RuneCountInString(text)
	u.beginEdit(sheetFocus, text, true, n, n)
	ed.reveal(v, r, c)
}

func (ed *sheetEd) endEdit() {
	ed.editing, ed.inBar, ed.typing = false, false, false
	ed.editSheet = nil
}

func (ed *sheetEd) cancelEdit() {
	ed.endEdit()
	ed.a.requestFrame()
}

// store puts the typed text into the cell, or the whole selection for Ctrl+Enter; a formula missing its last brackets
// gets them, as in Excel.
func (ed *sheetEd) store(text string) error {
	s := ed.editSheet
	if s == nil {
		return nil
	}
	ed.wb.Begin()
	err := s.SetInput(ed.editR, ed.editC, text)
	if err != nil && strings.HasPrefix(text, "=") {
		if n := strings.Count(text, "(") - strings.Count(text, ")"); n > 0 {
			err = s.SetInput(ed.editR, ed.editC, text+strings.Repeat(")", n))
		}
	}
	if err == nil {
		ed.fitRow(ed.viewOf(s), ed.editR)
	}
	return err
}

// commit stores what was typed and moves by (dr, dc); it reports false when the text is a broken formula, which stays
// open for mending.
func (ed *sheetEd) commit(dr, dc int) bool {
	u := &ed.a.u
	s := ed.editSheet
	if err := ed.store(string(u.edit.text)); err != nil {
		ed.a.toast(u.tf("That formula has an error: %s", err.Error()), true)
		return false
	}
	ed.endEdit()
	if s != nil {
		if i := ed.wb.SheetIndex(s.Name); i >= 0 {
			ed.cur = i
		}
		ed.noteAfterEdit()
	}
	v := ed.view()
	if dr != 0 || dc != 0 {
		ed.cycle(v, dr, dc)
	}
	return true
}

// noteAfterEdit works out formulas now for a small sheet; a large one goes on over the next frames.
func (ed *sheetEd) noteAfterEdit() {
	ed.wb.Calc(sheetCalcBudget)
	ed.noteCircular()
}

// cycle moves inside a chosen range, or simply on from a single cell.
func (ed *sheetEd) cycle(v *sheetView, dr, dc int) {
	a := v.area()
	if len(v.sel) > 1 || a.R1 == a.R2 && a.C1 == a.C2 || v.mergedArea(cellArea(v.active[0], v.active[1])) == a {
		ed.step(v, dr, dc, false, false)
		return
	}
	r, c := v.active[0]+dr, v.active[1]+dc
	switch {
	case c > int(a.C2):
		c, r = int(a.C1), r+1
	case c < int(a.C1):
		c, r = int(a.C2), r-1
	case r > int(a.R2):
		r, c = int(a.R1), c+1
	case r < int(a.R1):
		r, c = int(a.R2), c-1
	}
	if r > int(a.R2) {
		r, c = int(a.R1), int(a.C1)
	}
	if c > int(a.C2) {
		r, c = int(a.R1), int(a.C1)
	}
	if r < int(a.R1) || c < int(a.C1) {
		r, c = int(a.R2), int(a.C2)
	}
	v.active = [2]int{r, c}
	ed.reveal(v, r, c)
}

// step moves the active cell, or stretches the range with Shift; Ctrl goes to the edge of the data.
func (ed *sheetEd) step(v *sheetView, dr, dc int, shift, ctrl bool) {
	from := v.active
	if shift {
		from = v.tip
	}
	to := from
	switch {
	case ctrl:
		to = ed.dataEdge(v, from, dr, dc)
	default:
		if m, ok := v.s.MergeAt(from[0], from[1]); ok && !shift {
			if dr > 0 {
				from[0] = int(m.R2)
			}
			if dc > 0 {
				from[1] = int(m.C2)
			}
		}
		to = [2]int{from[0] + dr, from[1] + dc}
		for to[0] >= 0 && to[0] < xlsx.MaxRows && dr != 0 && dr*dr == 1 && v.rowTop(to[0]+1) == v.rowTop(to[0]) {
			to[0] += dr
		}
		for to[1] >= 0 && to[1] < xlsx.MaxCols && dc != 0 && v.colX[to[1]+1] == v.colX[to[1]] {
			to[1] += dc
		}
	}
	to[0], to[1] = min(max(to[0], 0), xlsx.MaxRows-1), min(max(to[1], 0), xlsx.MaxCols-1)
	if shift {
		v.stretch(to[0], to[1])
	} else {
		v.moveTo(to[0], to[1])
	}
	ed.reveal(v, to[0], to[1])
	ed.a.requestFrame()
}

// dataEdge is where Ctrl+arrow lands: the end of the run of filled cells, or the next filled cell after a gap.
func (ed *sheetEd) dataEdge(v *sheetView, at [2]int, dr, dc int) [2]int {
	filled := func(r, c int) bool { return v.s.ValueAt(r, c).Kind != formula.Blank }
	lastR, lastC := v.s.RowCount(), xlsx.MaxCols
	r, c := at[0], at[1]
	if dc != 0 {
		if row := v.s.RowAt(r); row != nil && len(row.Cells) > 0 {
			lastC = int(row.Cells[len(row.Cells)-1].Col) + 1
		} else {
			lastC = 0
		}
	}
	next := func(r, c int) (int, int) { return r + dr, c + dc }
	nr, nc := next(r, c)
	inside := func(r, c int) bool { return r >= 0 && c >= 0 && (dr == 0 || r < lastR) && (dc == 0 || c < lastC) }
	limit := func() [2]int {
		switch {
		case dr > 0:
			return [2]int{xlsx.MaxRows - 1, at[1]}
		case dc > 0:
			return [2]int{at[0], xlsx.MaxCols - 1}
		case dr < 0:
			return [2]int{0, at[1]}
		}
		return [2]int{at[0], 0}
	}
	if filled(r, c) && inside(nr, nc) && filled(nr, nc) {
		for inside(nr, nc) && filled(nr, nc) {
			r, c = nr, nc
			nr, nc = next(r, c)
		}
		return [2]int{r, c}
	}
	for r, c = nr, nc; inside(r, c); r, c = next(r, c) {
		if filled(r, c) {
			return [2]int{r, c}
		}
	}
	return limit()
}

// pageRows is how many rows a page key moves.
func (ed *sheetEd) pageRows(v *sheetView) int {
	k := ed.scale()
	h := float64(ed.body.panes.H) / k
	top := v.rowAtY(v.scrollY)
	bottom := v.rowAtY(v.scrollY + h)
	return max(bottom-top-1, 1)
}

// keys handles the keyboard, the caret being in the grid.
func (ed *sheetEd) keys(v *sheetView) {
	u := &ed.a.u
	if ed.editing {
		ed.editKeys(v)
		return
	}
	keys := u.keys
	u.keys = nil
	for _, k := range keys {
		if ed.gridKey(v, k) {
			continue
		}
		u.keys = append(u.keys, k)
	}
	if len(u.typed) > 0 {
		typed := string(u.typed)
		u.typed = nil
		ed.startEdit(v, v.active[0], v.active[1], typed, true)
	}
}

func (ed *sheetEd) gridKey(v *sheetView, k Key) bool {
	switch {
	case k.Code == keyUp:
		ed.step(v, -1, 0, k.Shift, k.Ctrl)
	case k.Code == keyDown:
		ed.step(v, 1, 0, k.Shift, k.Ctrl)
	case k.Code == keyLeft:
		ed.step(v, 0, -1, k.Shift, k.Ctrl)
	case k.Code == keyRight:
		ed.step(v, 0, 1, k.Shift, k.Ctrl)
	case k.Code == keyTab && !k.Ctrl:
		if k.Shift {
			ed.cycle(v, 0, -1)
		} else {
			ed.cycle(v, 0, 1)
		}
	case k.Code == keyEnter:
		if k.Shift {
			ed.cycle(v, -1, 0)
		} else {
			ed.cycle(v, 1, 0)
		}
	case k.Code == keyPageUp:
		ed.step(v, -ed.pageRows(v), 0, k.Shift, false)
	case k.Code == keyPageDn:
		ed.step(v, ed.pageRows(v), 0, k.Shift, false)
	case k.Code == keyHome && k.Ctrl:
		to := [2]int{v.frozenR, v.frozenC}
		if k.Shift {
			v.stretch(to[0], to[1])
		} else {
			v.moveTo(to[0], to[1])
		}
		ed.reveal(v, to[0], to[1])
	case k.Code == keyHome:
		if k.Shift {
			v.stretch(v.tip[0], 0)
		} else {
			v.moveTo(v.active[0], 0)
		}
		ed.reveal(v, v.active[0], 0)
	case k.Code == keyEnd && k.Ctrl:
		a := v.s.Used()
		if k.Shift {
			v.stretch(int(a.R2), int(a.C2))
		} else {
			v.moveTo(int(a.R2), int(a.C2))
		}
		ed.reveal(v, int(a.R2), int(a.C2))
	case k.Code == keyF2:
		ed.startEdit(v, v.active[0], v.active[1], v.s.InputText(v.active[0], v.active[1]), false)
	case k.Code == keyDelete:
		ed.clearSelection(v, true, false)
	case k.Code == keyBack:
		ed.startEdit(v, v.active[0], v.active[1], "", true)
	case k.Code == keyEscape:
		ed.clip = nil
		ed.drag = sheetDrag{}
	case k.Code == keySpace && k.Ctrl:
		a := v.area()
		v.selectArea(formula.Area{C1: a.C1, C2: a.C2, R2: xlsx.MaxRows - 1})
	case k.Code == keySpace && k.Shift:
		a := v.area()
		v.selectArea(formula.Area{R1: a.R1, R2: a.R2, C2: xlsx.MaxCols - 1})
	case k.Ctrl && !k.Alt:
		switch k.Code {
		case 'A':
			v.selectArea(formula.Area{R2: xlsx.MaxRows - 1, C2: xlsx.MaxCols - 1})
		case 'C':
			ed.copySelection(v, false)
		case 'X':
			ed.copySelection(v, true)
		case 'V':
			ed.pasteClipboard(v)
		case 'B':
			on := !ed.activeStyle(v).Bold
			ed.format(&xlsx.Change{Bold: &on})
		case 'I':
			on := !ed.activeStyle(v).Italic
			ed.format(&xlsx.Change{Italic: &on})
		case 'U':
			on := !ed.activeStyle(v).Underline
			ed.format(&xlsx.Change{Underline: &on})
		case 'D':
			ed.fillSelection(v, true)
		case 'R':
			ed.fillSelection(v, false)
		case 'F':
			ed.openFind(false)
		case 'H':
			ed.openFind(true)
		default:
			return false
		}
	default:
		return false
	}
	ed.a.requestFrame()
	return true
}

// fillSelection is Ctrl+D and Ctrl+R: the first row or column of the range copied over the rest.
func (ed *sheetEd) fillSelection(v *sheetView, down bool) {
	a := v.area()
	src := a
	if down {
		src.R2 = a.R1
		if a.R1 == a.R2 {
			if a.R1 == 0 {
				return
			}
			src.R1, src.R2 = a.R1-1, a.R1-1
		}
	} else {
		src.C2 = a.C1
		if a.C1 == a.C2 {
			if a.C1 == 0 {
				return
			}
			src.C1, src.C2 = a.C1-1, a.C1-1
		}
	}
	dst := a
	dst.R1, dst.C1 = min(a.R1, src.R1), min(a.C1, src.C1)
	ed.wb.Begin()
	v.s.CopyDown(src, dst)
}

func (ed *sheetEd) editKeys(v *sheetView) {
	u := &ed.a.u
	e := &u.edit
	keys := u.keys
	u.keys = nil
	for _, k := range keys {
		switch {
		case k.Code == keyEscape:
			ed.cancelEdit()
			return
		case k.Code == keyEnter && k.Alt:
			e.insert("\n")
		case k.Code == keyEnter && k.Ctrl:
			ed.commitAll(v)
			return
		case k.Code == keyEnter:
			dr := 1
			if k.Shift {
				dr = -1
			}
			ed.commit(dr, 0)
			return
		case k.Code == keyTab:
			dc := 1
			if k.Shift {
				dc = -1
			}
			ed.commit(0, dc)
			return
		case k.Code == keyF2:
			ed.typing = !ed.typing
		case ed.typing && !ed.inBar && !k.Ctrl && (k.Code == keyUp || k.Code == keyDown || k.Code == keyLeft || k.Code == keyRight):
			if ed.pointing() || ed.pointFrom >= 0 {
				// Arrows move the reference being pointed at.
				ed.pointKey(v, k)
				continue
			}
			dr, dc := 0, 0
			switch k.Code {
			case keyUp:
				dr = -1
			case keyDown:
				dr = 1
			case keyLeft:
				dc = -1
			default:
				dc = 1
			}
			ed.commit(dr, dc)
			return
		case k.Code == keyUp || k.Code == keyDown:
			line := -1
			if k.Code == keyDown {
				line = 1
			}
			e.caret = e.moveLines(e.caret, line, u.face(13))
			if !k.Shift {
				e.anchor = e.caret
			}
		default:
			e.key(k, u)
		}
	}
	if len(u.typed) > 0 {
		e.typeText(string(u.typed))
		u.typed = nil
		ed.pointFrom = -1
	}
}

// pointKey moves the cell a formula being typed points at.
func (ed *sheetEd) pointKey(v *sheetView, k Key) {
	if ed.pointFrom < 0 || ed.a.u.edit.caret != ed.pointTo {
		ed.pointStart = [2]int{ed.editR, ed.editC}
	}
	switch k.Code {
	case keyUp:
		ed.pointStart[0] = max(ed.pointStart[0]-1, 0)
	case keyDown:
		ed.pointStart[0] = min(ed.pointStart[0]+1, xlsx.MaxRows-1)
	case keyLeft:
		ed.pointStart[1] = max(ed.pointStart[1]-1, 0)
	default:
		ed.pointStart[1] = min(ed.pointStart[1]+1, xlsx.MaxCols-1)
	}
	ed.putRef(v, cellArea(ed.pointStart[0], ed.pointStart[1]))
	ed.reveal(v, ed.pointStart[0], ed.pointStart[1])
}

// commitAll is Ctrl+Enter: what was typed goes into every chosen cell.
func (ed *sheetEd) commitAll(v *sheetView) {
	u := &ed.a.u
	text := string(u.edit.text)
	if err := ed.store(text); err != nil {
		ed.a.toast(u.tf("That formula has an error: %s", err.Error()), true)
		return
	}
	first := v.active
	ed.endEdit()
	cl := v.s.Copy(cellArea(first[0], first[1]))
	for _, a := range v.sel {
		if int64(a.R2-a.R1+1)*int64(a.C2-a.C1+1) <= 1<<22 {
			v.s.Paste(cl, a, false)
		}
	}
	ed.noteAfterEdit()
}

func (ed *sheetEd) activeStyle(v *sheetView) *xlsx.Style {
	return ed.wb.Styles.Get(v.s.StyleAt(v.active[0], v.active[1]))
}

func (ed *sheetEd) clearSelection(v *sheetView, contents, formats bool) {
	ed.wb.Begin()
	for _, a := range v.sel {
		v.s.Clear(a, contents, formats)
	}
	ed.noteAfterEdit()
}

func (ed *sheetEd) copySelection(v *sheetView, cut bool) {
	a := v.area()
	if int64(a.R2-a.R1+1)*int64(a.C2-a.C1+1) > 1<<24 {
		u := v.s.Used()
		a.R2, a.C2 = min(a.R2, u.R2), min(a.C2, u.C2)
	}
	ed.clip = v.s.Copy(a)
	ed.clip.Cut = cut
	ed.clipText = v.s.TSV(ed.clip)
	ed.a.u.setClipText(ed.clipText)
}

// pasteClipboard pastes what was copied here with its formats and formulas, or text from elsewhere, typed in cell by cell.
func (ed *sheetEd) pasteClipboard(v *sheetView) {
	u := &ed.a.u
	text := u.clipText()
	a := v.area()
	if ed.clip != nil && text == ed.clipText {
		cl := ed.clip
		ed.wb.Begin()
		if cl.Cut {
			cl.Sheet.Clear(formula.Area{R1: int32(cl.FromRow), C1: int32(cl.FromCol), R2: int32(cl.FromRow + cl.Rows - 1), C2: int32(cl.FromCol + cl.Cols - 1)}, true, true)
		}
		if err := v.s.Paste(cl, a, true); err != nil {
			ed.a.toast(err.Error(), true)
		}
		rows, cols := int32(cl.Rows), int32(cl.Cols)
		if !cl.Cut && (a.R2-a.R1+1)%rows == 0 && (a.C2-a.C1+1)%cols == 0 {
			rows, cols = a.R2-a.R1+1, a.C2-a.C1+1
		}
		v.sel = []formula.Area{{R1: a.R1, C1: a.C1, R2: min(a.R1+rows-1, xlsx.MaxRows-1), C2: min(a.C1+cols-1, xlsx.MaxCols-1)}}
		if cl.Cut {
			ed.clip = nil
		}
		ed.noteAfterEdit()
		return
	}
	if text == "" {
		return
	}
	rows := parseTSV(text)
	ed.wb.Begin()
	for i, row := range rows {
		for j, cell := range row {
			if r, c := int(a.R1)+i, int(a.C1)+j; r < xlsx.MaxRows && c < xlsx.MaxCols {
				if v.s.SetInput(r, c, cell) != nil {
					v.s.SetValue(r, c, formula.Str(cell))
				}
			}
		}
	}
	ed.noteAfterEdit()
}

// parseTSV reads tab-separated text: rows by line, with Excel's quoting of cells that hold tabs, line ends or quotes.
func parseTSV(text string) [][]string {
	text = strings.TrimRight(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	var rows [][]string
	var row []string
	var b strings.Builder
	quoted := false
	for i := 0; i < len(text); i++ {
		c := text[i]
		switch {
		case quoted && c == '"' && i+1 < len(text) && text[i+1] == '"':
			b.WriteByte('"')
			i++
		case quoted && c == '"':
			quoted = false
		case quoted:
			b.WriteByte(c)
		case c == '"' && b.Len() == 0:
			quoted = true
		case c == '\t':
			row = append(row, b.String())
			b.Reset()
		case c == '\n':
			rows = append(rows, append(row, b.String()))
			row = nil
			b.Reset()
		default:
			b.WriteByte(c)
		}
	}
	return append(rows, append(row, b.String()))
}

// autofit sizes a column, and the other chosen columns, to what they show.
func (ed *sheetEd) autofit(v *sheetView, c int) {
	c1, c2 := c, c
	for _, a := range v.sel {
		if wholeCols(a) && int(a.C1) <= c && c <= int(a.C2) {
			c1, c2 = int(a.C1), int(a.C2)
		}
	}
	ed.wb.Begin()
	k := ed.styleK
	for col := c1; col <= c2; col++ {
		widest := float32(0)
		v.s.EachInColumn(col, func(r int, cell *xlsx.Cell) {
			if cell.Kind == xlsx.Empty {
				return
			}
			if _, merged := v.s.MergeAt(r, col); merged {
				return
			}
			st := ed.styleOf(cell.Style)
			if st.Wrap {
				return
			}
			widest = max(widest, st.face.Measure(v.s.Shown(r, col).Text))
		})
		if widest > 0 {
			v.s.SetColWidth(col, col, xlsx.WidthOfPixels(math.Ceil(float64(widest)/k)+8))
		}
	}
}

// structure runs a change of rows, columns or sheets.
func (ed *sheetEd) structure(fn func() error) {
	ed.finishEdit()
	ed.wb.Begin()
	if err := fn(); err != nil {
		ed.a.toast(err.Error(), true)
	}
	ed.noteAfterEdit()
}

// rowsChosen and colsChosen are the rows and columns the selection spans.
func rowsChosen(a formula.Area) (at, n int) { return int(a.R1), int(a.R2-a.R1) + 1 }
func colsChosen(a formula.Area) (at, n int) { return int(a.C1), int(a.C2-a.C1) + 1 }

func (ed *sheetEd) rightClick(v *sheetView, x, y float32) {
	a := ed.a
	u := &a.u
	b := &ed.body
	if b.panes.Contains(x, y) {
		r, c := ed.cellAt(v, x, y)
		if !v.inSelection(r, c) {
			v.moveTo(r, c)
		}
	}
	sel := v.area()
	rowAt, rows := rowsChosen(sel)
	colAt, cols := colsChosen(sel)
	insertRows := menuItem{label: u.tn(rows, "Insert %d row above", "Insert %d rows above", rows), run: func() { ed.structure(func() error { return v.s.InsertRows(rowAt, rows) }) }}
	deleteRows := menuItem{label: u.tn(rows, "Delete %d row", "Delete %d rows", rows), run: func() { ed.structure(func() error { return v.s.DeleteRows(rowAt, rows) }) }}
	insertCols := menuItem{label: u.tn(cols, "Insert %d column left", "Insert %d columns left", cols), run: func() { ed.structure(func() error { return v.s.InsertCols(colAt, cols) }) }}
	deleteCols := menuItem{label: u.tn(cols, "Delete %d column", "Delete %d columns", cols), run: func() { ed.structure(func() error { return v.s.DeleteCols(colAt, cols) }) }}
	var items []menuItem
	clipItems := []menuItem{
		{label: u.t("Cut"), key: "Ctrl+X", run: func() { ed.copySelection(v, true) }},
		{label: u.t("Copy"), key: "Ctrl+C", run: func() { ed.copySelection(v, false) }},
		{label: u.t("Paste"), key: "Ctrl+V", run: func() { ed.pasteClipboard(v) }},
		menuSep,
		{label: u.t("Clear contents"), key: "Del", run: func() { ed.clearSelection(v, true, false) }},
		{label: u.t("Clear formats"), run: func() { ed.clearSelection(v, false, true) }},
		menuSep,
	}
	switch {
	case b.cols.Contains(x, y):
		_, c := ed.cellAt(v, x, b.panes.Y+1)
		if !v.inSelection(0, c) || !wholeCols(sel) {
			v.selectArea(formula.Area{C1: int32(c), C2: int32(c), R2: xlsx.MaxRows - 1})
			colAt, cols = c, 1
			insertCols.label, deleteCols.label = u.tn(1, "Insert %d column left", "Insert %d columns left", 1), u.tn(1, "Delete %d column", "Delete %d columns", 1)
			insertCols.run = func() { ed.structure(func() error { return v.s.InsertCols(c, 1) }) }
			deleteCols.run = func() { ed.structure(func() error { return v.s.DeleteCols(c, 1) }) }
		}
		items = append(clipItems, insertCols, deleteCols, menuSep,
			menuItem{label: u.t("Column width…"), run: func() { ed.openSize(v, true) }},
			menuItem{label: u.t("Fit to contents"), run: func() { ed.autofit(v, colAt) }},
			menuItem{label: u.t("Hide"), run: func() {
				ed.structure(func() error {
					for _, a := range v.sel {
						v.s.SetColsHidden(int(a.C1), int(a.C2), true)
					}
					return nil
				})
			}},
			menuItem{label: u.t("Show hidden columns"), run: func() {
				ed.structure(func() error {
					s := v.area()
					v.s.SetColsHidden(max(int(s.C1)-1, 0), min(int(s.C2)+1, xlsx.MaxCols-1), false)
					return nil
				})
			}})
	case b.rows.Contains(x, y):
		r, _ := ed.cellAt(v, b.panes.X+1, y)
		if !v.inSelection(r, 0) || !wholeRows(sel) {
			v.selectArea(formula.Area{R1: int32(r), R2: int32(r), C2: xlsx.MaxCols - 1})
			insertRows.label, deleteRows.label = u.tn(1, "Insert %d row above", "Insert %d rows above", 1), u.tn(1, "Delete %d row", "Delete %d rows", 1)
			insertRows.run = func() { ed.structure(func() error { return v.s.InsertRows(r, 1) }) }
			deleteRows.run = func() { ed.structure(func() error { return v.s.DeleteRows(r, 1) }) }
		}
		items = append(clipItems, insertRows, deleteRows, menuSep,
			menuItem{label: u.t("Row height…"), run: func() { ed.openSize(v, false) }},
			menuItem{label: u.t("Hide"), run: func() {
				ed.structure(func() error {
					for _, a := range v.sel {
						v.s.SetRowsHidden(int(a.R1), int(a.R2), true)
					}
					return nil
				})
			}},
			menuItem{label: u.t("Show hidden rows"), run: func() {
				ed.structure(func() error {
					s := v.area()
					v.s.SetRowsHidden(max(int(s.R1)-1, 0), min(int(s.R2)+1, xlsx.MaxRows-1), false)
					return nil
				})
			}})
	default:
		items = append(clipItems, insertRows, deleteRows, insertCols, deleteCols, menuSep,
			menuItem{label: u.t("Sort A to Z"), run: func() { ed.sortBy(v, false) }},
			menuItem{label: u.t("Sort Z to A"), run: func() { ed.sortBy(v, true) }})
	}
	a.openMenu("sheet/context", gfx.Rect{X: x, Y: y}, items)
}

// region is the block of filled cells around the active cell, as Excel takes for sorting and filtering.
func (ed *sheetEd) region(v *sheetView) formula.Area {
	a := v.area()
	if a.R1 != a.R2 || a.C1 != a.C2 {
		if wholeCols(a) || wholeRows(a) {
			u := v.s.Used()
			a.R2, a.C2 = min(a.R2, u.R2), min(a.C2, u.C2)
		}
		return a
	}
	has := func(r, c int32) bool { return r >= 0 && c >= 0 && v.s.ValueAt(int(r), int(c)).Kind != formula.Blank }
	rowHas := func(r, c1, c2 int32) bool {
		for c := c1 - 1; c <= c2+1; c++ {
			if has(r, c) {
				return true
			}
		}
		return false
	}
	colHas := func(c, r1, r2 int32) bool {
		for r := r1 - 1; r <= r2+1; r++ {
			if has(r, c) {
				return true
			}
		}
		return false
	}
	for grown := true; grown; {
		grown = false
		if a.R1 > 0 && rowHas(a.R1-1, a.C1, a.C2) {
			a.R1--
			grown = true
		}
		if int(a.R2) < xlsx.MaxRows-1 && rowHas(a.R2+1, a.C1, a.C2) {
			a.R2++
			grown = true
		}
		if a.C1 > 0 && colHas(a.C1-1, a.R1, a.R2) {
			a.C1--
			grown = true
		}
		if int(a.C2) < xlsx.MaxCols-1 && colHas(a.C2+1, a.R1, a.R2) {
			a.C2++
			grown = true
		}
	}
	return a
}

// hasHeader guesses that the first row of a block names its columns: text over something that is not all text.
func (ed *sheetEd) hasHeader(v *sheetView, a formula.Area) bool {
	if a.R1 == a.R2 {
		return false
	}
	other := false
	for c := int(a.C1); c <= int(a.C2); c++ {
		top := v.s.ValueAt(int(a.R1), c)
		if top.Kind == formula.Number || top.Kind == formula.Blank {
			return false
		}
		if next := v.s.ValueAt(int(a.R1)+1, c); next.Kind == formula.Number || next.Kind == formula.Blank {
			other = true
		}
	}
	return other
}

func (ed *sheetEd) sortBy(v *sheetView, desc bool) {
	a := ed.region(v)
	if f, ok := v.s.AutoFilter(); ok && f.R1 <= int32(v.active[0]) && int32(v.active[0]) <= f.R2 {
		a = f
		a.R1++
	} else if ed.hasHeader(v, a) {
		a.R1++
	}
	ed.structure(func() error { return v.s.Sort(a, []xlsx.SortKey{{Col: v.active[1], Desc: desc}}) })
}

// toggleFilter puts a filter on the data around the active cell, or takes it off.
func (ed *sheetEd) toggleFilter(v *sheetView) {
	ed.finishEdit()
	if _, on := v.s.AutoFilter(); on {
		ed.wb.Begin()
		v.s.SetAutoFilter(formula.Area{}, false)
		return
	}
	a := ed.region(v)
	ed.wb.Begin()
	v.s.SetAutoFilter(a, true)
}

func (ed *sheetEd) freezeAt(v *sheetView, rows, cols int) {
	ed.finishEdit()
	ed.wb.Begin()
	v.s.SetFreeze(rows, cols)
	v.scrollX, v.scrollY = 0, 0
}
