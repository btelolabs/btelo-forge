package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/btelolabs/btelo-forge/internal/gfx"
	"github.com/btelolabs/btelo-forge/internal/office/formula"
	"github.com/btelolabs/btelo-forge/internal/office/xlsx"
)

func sheetFormat(t *testing.T) *officeFormat {
	t.Helper()
	f := officeFormatOf("a.xlsx")
	if f == nil || f.id != "sheet" {
		t.Fatal("no spreadsheet format")
	}
	return f
}

func newSheetTab(t *testing.T, x *harness) *sheetEd {
	t.Helper()
	od, err := x.a.newOffice(sheetFormat(t))
	if err != nil {
		t.Fatal(err)
	}
	x.frame(Input{})
	x.frame(Input{})
	return od.ed.(*sheetEd)
}

// sheetType types text into a cell the way a person does.
func (x *harness) sheetType(s string) { x.frame(Input{Typed: []rune(s)}) }

func (x *harness) sheetKey(code uint16) { x.frame(Input{Keys: []Key{{Code: code}}}) }

func (x *harness) goCell(ed *sheetEd, ref string) {
	r, c, _ := formula.ParseCell(ref)
	ed.view().moveTo(r, c)
}

func (x *harness) enter(ed *sheetEd, ref, text string) {
	x.goCell(ed, ref)
	x.sheetType(text)
	x.sheetKey(keyEnter)
}

// demoSheet fills a workbook with a bit of everything.
func demoSheet(t *testing.T, x *harness, ed *sheetEd) {
	t.Helper()
	wb := ed.wb
	s := ed.sheet()
	wb.Begin()
	rows := [][]string{
		{"Quarterly sales 季度销售", "", "", "", "", "", ""},
		{"Region", "Product", "Units", "Price", "Revenue", "Share", "Date"},
		{"North 北区", "Widget", "120", "19.99", "=C3*D3", "=E3/SUM($E$3:$E$7)", "2024-01-15"},
		{"South 南区", "Gadget", "85", "34.5", "=C4*D4", "=E4/SUM($E$3:$E$7)", "2024-02-20"},
		{"East 东区", "Widget", "240", "19.99", "=C5*D5", "=E5/SUM($E$3:$E$7)", "2024-03-05"},
		{"West 西区", "Doohickey with a very long name that overflows", "", "", "", "", ""},
		{"Central 中区", "Gadget", "64", "-34.5", "=C7*D7", "=E7/SUM($E$3:$E$7)", "2024-03-30"},
		{"Total", "", "=SUM(C3:C7)", "=AVERAGE(D3:D7)", "=SUM(E3:E7)", "=SUM(F3:F7)", "=MAX(G3:G7)"},
	}
	for i, row := range rows {
		for j, v := range row {
			if v != "" {
				if err := s.SetInput(i, j, v); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	yes, navy, white, pale, band := true, "1F3864", "FFFFFF", "DDEBF7", "F2F2F2"
	center, money, pct, date, size := "center", `#,##0.00;[Red]-#,##0.00`, "0.0%", "yyyy-mm-dd", 16.0
	area := func(ref string) []formula.Area {
		a, ok := formula.ParseArea(ref)
		if !ok {
			t.Fatal(ref)
		}
		return []formula.Area{a}
	}
	if err := s.Merge(area("A1:G1")[0]); err != nil {
		t.Fatal(err)
	}
	s.ApplyStyle(area("A1:G1"), &xlsx.Change{Bold: &yes, Size: &size, Color: &white, Fill: &navy, HAlign: &center})
	s.ApplyStyle(area("A2:G2"), &xlsx.Change{Bold: &yes, Fill: &pale, HAlign: &center})
	s.ApplyStyle(area("D3:E8"), &xlsx.Change{NumFmt: &money})
	s.ApplyStyle(area("F3:F8"), &xlsx.Change{NumFmt: &pct})
	s.ApplyStyle(area("G3:G8"), &xlsx.Change{NumFmt: &date})
	s.ApplyStyle(area("A8:G8"), &xlsx.Change{Bold: &yes, Fill: &band})
	thin := xlsx.Edge{Style: "thin", Color: "7F7F7F"}
	s.ApplyStyle(area("A2:G8"), &xlsx.Change{Left: &thin, Right: &thin, Top: &thin, Bottom: &thin})
	double := xlsx.Edge{Style: "double"}
	s.ApplyStyle(area("A8:G8"), &xlsx.Change{Top: &double})
	s.SetColWidth(0, 0, 16)
	s.SetColWidth(1, 1, 12)
	s.SetColWidth(4, 6, 13)
	s.SetInput(10, 0, "Wrapped words go over several lines in one cell, 自动换行也可以。")
	s.ApplyStyle(area("A11"), &xlsx.Change{Wrap: &yes})
	s.SetInput(10, 2, "1234567890123456")
	s.SetInput(10, 3, "=1/0")
	s.SetInput(10, 4, "TRUE")
	s.SetInput(10, 5, "0.000012")
	ed.fitRow(ed.view(), 0)
	ed.fitRow(ed.view(), 10)
	wb.Calc(0)
	wb.Touch()
	x.frame(Input{})
}

func TestSheetTyping(t *testing.T) {
	x := newHarness(t)
	ed := newSheetTab(t, x)
	s := ed.sheet()
	x.enter(ed, "A1", "12")
	if got := ed.view().active; got != [2]int{1, 0} {
		t.Fatalf("Enter went to %v", got)
	}
	x.enter(ed, "A2", "30")
	x.enter(ed, "A3", "=SUM(A1:A2)")
	if got := s.ValueAt(2, 0); got.Num != 42 {
		t.Fatalf("sum %+v", got)
	}
	// A formula missing its bracket is closed.
	x.enter(ed, "B1", "=SUM(A1:A2")
	if got := s.Cell(0, 1).FormulaText(); got != "SUM(A1:A2)" {
		t.Fatalf("%q", got)
	}
	// Esc leaves the cell as it was; F2 edits, and Delete empties.
	x.goCell(ed, "A1")
	x.sheetType("99")
	x.sheetKey(keyEscape)
	if s.ValueAt(0, 0).Num != 12 {
		t.Fatal("Esc kept the words")
	}
	x.sheetKey(keyF2)
	if !ed.editing {
		t.Fatal("F2")
	}
	x.sheetType("5")
	x.sheetKey(keyEnter)
	if s.ValueAt(0, 0).Num != 125 {
		t.Fatalf("F2 appended %+v", s.ValueAt(0, 0))
	}
	if got := s.ValueAt(2, 0); got.Num != 155 {
		t.Fatalf("recalculated %+v", got)
	}
	x.goCell(ed, "A1")
	x.sheetKey(keyDelete)
	if s.ValueAt(0, 0).Kind != formula.Blank {
		t.Fatal("Delete")
	}
	x.a.officeKeys(ed.od)
	ed.undo()
	if s.ValueAt(0, 0).Num != 125 {
		t.Fatal("undo")
	}
	ed.redo()
	if s.ValueAt(0, 0).Kind != formula.Blank {
		t.Fatal("redo")
	}
	// A cell that refers to itself shows 0 and is named in the status line, as in Excel.
	x.enter(ed, "F1", "=F1+1")
	if ed.circular != "Sheet1!F1" || s.ValueAt(0, 5).Num != 0 {
		t.Fatalf("circular %q %+v", ed.circular, s.ValueAt(0, 5))
	}
}

func TestSheetNavigation(t *testing.T) {
	x := newHarness(t)
	ed := newSheetTab(t, x)
	for i := 1; i <= 5; i++ {
		x.enter(ed, fmt.Sprintf("B%d", i+2), "1")
	}
	x.enter(ed, "B20", "x")
	v := ed.view()
	x.goCell(ed, "B3")
	x.frame(Input{Keys: []Key{{Code: keyDown, Ctrl: true}}})
	if v.active != [2]int{6, 1} {
		t.Fatalf("Ctrl+Down: %v", v.active)
	}
	x.frame(Input{Keys: []Key{{Code: keyDown, Ctrl: true}}})
	if v.active != [2]int{19, 1} {
		t.Fatalf("Ctrl+Down over a gap: %v", v.active)
	}
	x.frame(Input{Keys: []Key{{Code: keyDown, Ctrl: true}}})
	if v.active != [2]int{xlsx.MaxRows - 1, 1} {
		t.Fatalf("Ctrl+Down to the end: %v", v.active)
	}
	x.frame(Input{Keys: []Key{{Code: keyHome, Ctrl: true}}})
	if v.active != [2]int{0, 0} {
		t.Fatalf("Ctrl+Home: %v", v.active)
	}
	x.frame(Input{Keys: []Key{{Code: keyEnd, Ctrl: true}}})
	if v.active != [2]int{19, 1} {
		t.Fatalf("Ctrl+End: %v", v.active)
	}
	x.goCell(ed, "A1")
	x.frame(Input{Keys: []Key{{Code: keyDown, Shift: true}, {Code: keyRight, Shift: true}}})
	if got := formula.AreaName(v.area()); got != "A1:B2" {
		t.Fatalf("Shift: %s", got)
	}
	x.frame(Input{Keys: []Key{{Code: 'A', Ctrl: true}}})
	if !wholeCols(v.area()) || !wholeRows(v.area()) {
		t.Fatal("Ctrl+A")
	}
	// A click, and Shift and Ctrl clicks, choose cells.
	box := ed.cellBox(v, cellArea(2, 1))
	x.click(box)
	if v.active != [2]int{2, 1} || len(v.sel) != 1 {
		t.Fatalf("click: %v", v.active)
	}
	box2 := ed.cellBox(v, cellArea(4, 3))
	x.mx, x.my = box2.X+4, box2.Y+4
	x.frame(Input{Shift: true})
	x.frame(Input{Shift: true, Down: true, Pointer: []Pointer{{X: x.mx, Y: x.my, Down: true}}})
	x.frame(Input{Shift: true, Pointer: []Pointer{{X: x.mx, Y: x.my}}})
	if got := formula.AreaName(v.area()); got != "B3:D5" {
		t.Fatalf("Shift+click: %s", got)
	}
	// Page down, then the view follows the active cell.
	x.goCell(ed, "A1")
	x.sheetKey(keyPageDn)
	x.frame(Input{})
	if v.active[0] < 10 {
		t.Fatalf("PageDown: %v", v.active)
	}
}

func TestSheetClipboardAndFill(t *testing.T) {
	x := newHarness(t)
	ed := newSheetTab(t, x)
	s := ed.sheet()
	v := ed.view()
	x.enter(ed, "A1", "2")
	x.enter(ed, "B1", "=A1*2")
	x.goCell(ed, "A1")
	x.frame(Input{Keys: []Key{{Code: keyRight, Shift: true}}})
	x.frame(Input{Keys: []Key{{Code: 'C', Ctrl: true}}})
	if !strings.Contains(x.h.clip, "2\t4") {
		t.Fatalf("clipboard %q", x.h.clip)
	}
	x.goCell(ed, "A3")
	x.frame(Input{Keys: []Key{{Code: 'V', Ctrl: true}}})
	if got := s.Cell(2, 1).FormulaText(); got != "A3*2" {
		t.Fatalf("internal paste %q", got)
	}
	// Text from elsewhere goes in as if typed.
	x.h.clip = "a\tb\r\n1\t=A6+1\r\n"
	x.goCell(ed, "A5")
	x.frame(Input{Keys: []Key{{Code: 'V', Ctrl: true}}})
	if s.ValueAt(5, 1).Num != 2 || s.ValueAt(4, 1).Str != "b" {
		t.Fatalf("text paste %+v", s.ValueAt(5, 1))
	}
	// The fill handle: drag from the corner of A10:A11 down.
	x.enter(ed, "D1", "1")
	x.enter(ed, "D2", "3")
	x.goCell(ed, "D1")
	x.frame(Input{Keys: []Key{{Code: keyDown, Shift: true}}})
	box := ed.cellBox(v, v.area())
	to := ed.cellBox(v, cellArea(5, 3))
	x.drag(box.Right()-1, box.Bottom()-1, to.X+5, to.Y+5)
	if s.ValueAt(5, 3).Num != 11 {
		t.Fatalf("fill %+v", s.ValueAt(5, 3))
	}
	if got := formula.AreaName(v.area()); got != "D1:D6" {
		t.Fatalf("after fill %s", got)
	}
}

func TestSheetColumnsAndTabs(t *testing.T) {
	x := newHarness(t)
	ed := newSheetTab(t, x)
	s := ed.sheet()
	v := ed.view()
	x.enter(ed, "A1", "a much longer text than the column shows")
	// Double click on the edge of column A fits it.
	b := &ed.body
	edge := b.panes.X + float32(v.colX[1]*ed.scale())
	x.mx, x.my = edge, b.cols.Y+5
	x.frame(Input{})
	x.frame(Input{Down: true, Pointer: []Pointer{{X: edge, Y: x.my, Down: true, Double: true}}})
	x.frame(Input{Pointer: []Pointer{{X: edge, Y: x.my}}})
	x.frame(Input{})
	if w := v.colX[1]; w < 200 {
		t.Fatalf("autofit %v", w)
	}
	// Dragging an edge sizes it, and it undoes as one step.
	edge = b.panes.X + float32(v.colX[1]*ed.scale())
	before := v.colX[1]
	x.drag(edge, b.cols.Y+5, edge+40, b.cols.Y+5)
	if d := v.colX[1] - before; d < 30 || d > 50 {
		t.Fatalf("drag changed the width by %v", d)
	}
	ed.undo()
	x.frame(Input{})
	if v.colX[1] != before {
		t.Fatalf("undo of the drag: %v", v.colX[1])
	}
	// Tabs.
	ed.addSheet(1)
	x.frame(Input{})
	if len(ed.wb.Sheets) != 2 || ed.cur != 1 {
		t.Fatalf("add sheet: %d", len(ed.wb.Sheets))
	}
	ed.structure(func() error { return ed.wb.RenameSheet(ed.sheet(), "Data") })
	ed.structure(func() error { ed.wb.MoveSheet(ed.sheet(), 0); ed.cur = 0; return nil })
	if ed.wb.Sheets[0].Name != "Data" || ed.wb.Sheets[1] != s {
		t.Fatal("move sheet")
	}
	ed.freezeAt(ed.view(), 1, 1)
	x.frame(Input{})
	if r, c := ed.sheet().Freeze(); r != 1 || c != 1 {
		t.Fatal("freeze")
	}
}

func TestSheetSaves(t *testing.T) {
	x := newHarness(t)
	ed := newSheetTab(t, x)
	demoSheet(t, x, ed)
	od := ed.od
	od.dirty = true
	if err := x.a.saveOffice(od, true); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(od.path)
	if err != nil {
		t.Fatal(err)
	}
	wb, err := xlsx.Open(data)
	if err != nil {
		t.Fatal(err)
	}
	if got := wb.Sheets[0].Shown(7, 4).Text; got == "" {
		t.Fatal("no total")
	}
}

// TestSheetScreens draws the tab into FORGE_SHOTS.
func TestSheetScreens(t *testing.T) {
	dir := os.Getenv("FORGE_SHOTS")
	if dir == "" {
		t.Skip("set FORGE_SHOTS to a folder to render the screens")
	}
	x := newHarness(t)
	ed := newSheetTab(t, x)
	demoSheet(t, x, ed)
	v := ed.view()
	for _, lang := range []Language{English, Chinese} {
		x.a.u.lang = lang
		name := func(s string) string { return string(lang) + "-" + s }
		v.moveTo(2, 2)
		x.frame(Input{})
		x.shot(name("grid"))
		v.selectArea(formula.Area{R1: 2, C1: 2, R2: 6, C2: 4})
		x.frame(Input{})
		x.shot(name("range"))
		v.moveTo(9, 0)
		x.sheetType("=SUM(C3:C7)")
		x.frame(Input{})
		x.shot(name("editing"))
		x.sheetKey(keyEscape)
		ed.find.what = "Widget"
		ed.openFind(true)
		x.frame(Input{})
		ed.findNext(v, 1)
		x.frame(Input{})
		x.shot(name("find"))
		ed.find.open = false
		x.a.u.focus = sheetFocus
		x.frame(Input{})
		ed.pop = &sheetPop{kind: sheetPopFill, at: gfx.Rect{X: 300, Y: 40, W: 28, H: 38}}
		x.frame(Input{})
		x.frame(Input{})
		x.shot(name("colours"))
		ed.pop = nil
		x.frame(Input{})
		ed.toggleFilter2(v, "A2:G8")
		x.frame(Input{})
		ed.openFilter(v, 1, formula.Area{R1: 1, C1: 0, R2: 7, C2: 6})
		x.frame(Input{})
		x.frame(Input{})
		x.shot(name("filter"))
		ed.pop = nil
		ed.undo()
		x.frame(Input{})
		ed.freezeAt(v, 2, 1)
		ed.scrollBy(v, 90, 80)
		x.frame(Input{})
		x.shot(name("frozen"))
		ed.undo()
		v.scrollX, v.scrollY = 0, 0
		ed.zoom = 1.5
		x.frame(Input{})
		x.shot(name("zoom"))
		ed.zoom = 1
		x.frame(Input{})
		ed.rightClick(v, ed.body.panes.X+100, ed.body.panes.Y+60)
		x.frame(Input{})
		x.frame(Input{})
		x.shot(name("menu"))
		x.a.menu = nil
		x.frame(Input{})
	}
	data, err := ed.renderPNG(ed.sheet(), formula.Area{R2: 10, C2: 6}, 1600, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "render.png"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	x.settle(5 * time.Second)
}

// toggleFilter2 puts a filter on a range, for the screens.
func (ed *sheetEd) toggleFilter2(v *sheetView, ref string) {
	a, _ := formula.ParseArea(ref)
	ed.wb.Begin()
	v.s.SetAutoFilter(a, true)
}

// TestMCPBuildsASheet drives the tools as an AI would.
func TestMCPBuildsASheet(t *testing.T) {
	x := newHarness(t)
	s := connectMCPAt(t, x, "/office")
	path := filepath.Join(x.dir, "book.xlsx")
	var st sheetStateOut
	s.ok("sheet_new", map[string]any{"path": path}, &st)
	if len(st.Sheets) != 1 || st.Path != path {
		t.Fatalf("sheet_new: %+v", st)
	}
	var wrote map[string]any
	s.ok("sheet_write", map[string]any{"start": "A1", "rows": [][]any{{"Item", "Cost"}, {"Pens", 3.5}, {"Ink", 12}, {"Total", "=SUM(B2:B3)"}}}, &wrote)
	s.ok("sheet_write", map[string]any{"cells": []map[string]any{{"ref": "C2", "value": "=not a formula"}, {"ref": "D2", "input": "15%"}}}, &wrote)
	var read struct {
		Values   [][]any    `json:"values"`
		Shown    [][]string `json:"shown"`
		Formulas [][]string `json:"formulas"`
	}
	s.ok("sheet_read", map[string]any{"range": "A1:D4"}, &read)
	if read.Values[3][1] != 15.5 || read.Formulas[3][1] != "=SUM(B2:B3)" || read.Shown[1][3] != "15.00%" || read.Values[1][2] != "=not a formula" {
		t.Fatalf("sheet_read: %+v", read)
	}
	s.ok("sheet_format", map[string]any{"ranges": []string{"A1:B1"}, "bold": true, "fill": "#DDEBF7", "border": map[string]any{"sides": "all"}}, nil)
	s.ok("sheet_format", map[string]any{"ranges": []string{"B2:B4"}, "number_format": "#,##0.00", "column_width": 14}, nil)
	s.ok("sheet_structure", map[string]any{"action": "insert_rows", "at": "2", "count": 1}, &st)
	s.ok("sheet_read", map[string]any{"range": "B5"}, &read)
	if read.Formulas[0][0] != "=SUM(B3:B4)" {
		t.Fatalf("after insert: %+v", read)
	}
	s.ok("sheet_structure", map[string]any{"action": "add_sheet", "name": "Notes"}, &st)
	if len(st.Sheets) != 2 || st.Active != "Notes" {
		t.Fatalf("add_sheet: %+v", st)
	}
	s.ok("sheet_write", map[string]any{"sheet": "Sheet1", "cells": []map[string]any{{"ref": "D3", "clear": true}}}, nil)
	s.ok("sheet_sort", map[string]any{"sheet": "Sheet1", "range": "A3:B4", "keys": []map[string]any{{"column": "B", "desc": true}}}, nil)
	s.ok("sheet_read", map[string]any{"sheet": "Sheet1", "range": "A3"}, &read)
	if read.Values[0][0] != "Ink" {
		t.Fatalf("sorted: %+v", read)
	}
	res := s.call("sheet_render", map[string]any{"sheet": "Sheet1"})
	if len(res.Content) != 1 {
		t.Fatalf("render: %+v", res)
	}
	if _, ok := res.Content[0].(*mcp.ImageContent); !ok {
		t.Fatalf("render gave %T", res.Content[0])
	}
	s.ok("sheet_save", map[string]any{}, nil)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	wb, err := xlsx.Open(data)
	if err != nil {
		t.Fatal(err)
	}
	if got := wb.Sheet("Sheet1").Shown(4, 1).Text; got != "15.50" {
		t.Fatalf("saved %q", got)
	}
	if x.a.office == nil {
		t.Fatal("no tab")
	}
	// A tool on a file by name opens it.
	x.a.closeOffice(x.a.office)
	s.ok("sheet_open", map[string]any{"path": path}, &st)
}

// TestSheetLargeFileIsSmooth opens a large file through the window and scrolls it, timing the frames.
func TestSheetLargeFileIsSmooth(t *testing.T) {
	if testing.Short() {
		t.Skip("slow")
	}
	x := newHarness(t)
	data, err := xlsx.Blank("Big")
	if err != nil {
		t.Fatal(err)
	}
	wb, err := xlsx.Open(data)
	if err != nil {
		t.Fatal(err)
	}
	s := wb.Sheets[0]
	wb.Begin()
	for r := 0; r < 200000; r++ {
		for c := 0; c < 8; c++ {
			s.SetValue(r, c, formula.Num(float64(r*c)))
		}
		s.SetValue(r, 8, formula.Str(fmt.Sprintf("row %d", r)))
		s.SetFormula(r, 9, fmt.Sprintf("A%d+B%d", r+1, r+1))
	}
	wb.Calc(0)
	write, err := wb.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if data, err = write(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(x.dir, "big.xlsx")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if len(data) <= sheetSyncSize {
		t.Fatalf("the file is only %d bytes: this test wants a background load", len(data))
	}
	start := time.Now()
	x.a.openOfficeFile(path)
	x.pump(time.Minute, func() bool {
		if x.a.office == nil {
			return false
		}
		return x.a.office.ed.(*sheetEd).wb != nil
	})
	t.Logf("%d bytes, 2M cells, opened in %v", len(data), time.Since(start))
	ed := x.a.office.ed.(*sheetEd)
	v := ed.view()
	start = time.Now()
	const frames = 120
	for i := 0; i < frames; i++ {
		ed.scrollBy(v, 3, 200)
		x.frame(Input{})
	}
	per := time.Since(start) / frames
	t.Logf("%v a frame while scrolling", per)
	if per > 60*time.Millisecond {
		t.Fatalf("%v a frame is too slow", per)
	}
	// An edit that feeds one formula is instant.
	x.goCell(ed, "A100000")
	start = time.Now()
	x.sheetType("7")
	x.sheetKey(keyEnter)
	if took := time.Since(start); took > 200*time.Millisecond {
		t.Fatalf("an edit took %v", took)
	}
	if got := ed.sheet().ValueAt(99999, 9).Num; got != 7+99999 {
		t.Fatalf("%v", got)
	}
}
