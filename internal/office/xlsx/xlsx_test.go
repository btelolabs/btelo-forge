package xlsx

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/btelolabs/btelo-forge/internal/office/formula"
)

func blankBook(t testing.TB) *Workbook {
	t.Helper()
	data, err := Blank("Sheet1")
	if err != nil {
		t.Fatal(err)
	}
	wb, err := Open(data)
	if err != nil {
		t.Fatal(err)
	}
	return wb
}

func save(t testing.TB, wb *Workbook) []byte {
	t.Helper()
	write, err := wb.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	data, err := write()
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func parts(t testing.TB, data []byte) map[string]string {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, f := range zr.File {
		r, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(r)
		r.Close()
		out[f.Name] = string(b)
	}
	return out
}

// book makes a file from parts over the blank workbook's.
func book(t testing.TB, over map[string]string) []byte {
	t.Helper()
	data, err := Blank("Sheet1")
	if err != nil {
		t.Fatal(err)
	}
	all := parts(t, data)
	for k, v := range over {
		all[k] = v
	}
	var b bytes.Buffer
	zw := zip.NewWriter(&b)
	for _, name := range append([]string{"[Content_Types].xml"}, keys(all)...) {
		w, _ := zw.Create(name)
		w.Write([]byte(all[name]))
	}
	zw.Close()
	return b.Bytes()
}

func keys(m map[string]string) []string {
	var out []string
	for k := range m {
		if k != "[Content_Types].xml" {
			out = append(out, k)
		}
	}
	return out
}

func TestBlankRoundTrip(t *testing.T) {
	wb := blankBook(t)
	s := wb.Sheets[0]
	wb.Begin()
	for _, in := range []struct {
		ref, text string
	}{{"A1", "Hello"}, {"B1", "12.5"}, {"C1", "=B1*2"}, {"D1", "TRUE"}, {"E1", "12%"}, {"F1", "2024-03-05"}} {
		r, c, _ := formula.ParseCell(in.ref)
		if err := s.SetInput(r, c, in.text); err != nil {
			t.Fatal(err)
		}
	}
	data := save(t, wb)
	re, err := Open(data)
	if err != nil {
		t.Fatal(err)
	}
	x := re.Sheets[0]
	if got := x.ValueAt(0, 0); got.Str != "Hello" {
		t.Fatalf("A1 %+v", got)
	}
	if got := x.ValueAt(0, 2); got.Num != 25 {
		t.Fatalf("C1 %+v", got)
	}
	if got := x.Cell(0, 2).FormulaText(); got != "B1*2" {
		t.Fatalf("formula %q", got)
	}
	if got := x.Shown(0, 4).Text; got != "12.00%" {
		t.Fatalf("E1 %q", got)
	}
	if got := x.Shown(0, 5).Text; got != "3/5/2024" {
		t.Fatalf("F1 %q", got)
	}
	if !strings.Contains(parts(t, data)["xl/workbook.xml"], `fullCalcOnLoad="1"`) {
		t.Fatal("no fullCalcOnLoad")
	}
}

const rich = `<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships" xmlns:x14ac="urn:x14ac" xmlns:mc="http://schemas.openxmlformats.org/markup-compatibility/2006" mc:Ignorable="x14ac"><sheetPr><tabColor rgb="FF00FF00"/></sheetPr><dimension ref="A1:C3"/><sheetViews><sheetView workbookViewId="0"><pane ySplit="1" topLeftCell="A2" activePane="bottomLeft" state="frozen"/></sheetView></sheetViews><sheetFormatPr defaultRowHeight="15"/><cols><col min="2" max="3" width="20" customWidth="1" bestFit="1"/></cols><sheetData><row r="1" spans="1:3" x14ac:dyDescent="0.25" ht="30" customHeight="1"><c r="A1" s="0" t="s"><v>0</v></c><c r="B1" cm="1" vm="2"><f t="shared" ref="B1:B3" si="0">A1*2</f><v>2</v><extLst><ext uri="u"><x/></ext></extLst></c></row><row r="2" x14ac:dyDescent="0.25" foo="bar"><c r="A2"><v>3</v></c><c r="B2"><f t="shared" si="0"/><v>6</v></c></row><row r="3"><c r="A3"><v>4</v></c><c r="B3"><f t="shared" si="0"/><v>8</v></c><c r="C3" t="inlineStr"><is><t>in</t></is></c></row></sheetData><mergeCells count="1"><mergeCell ref="A5:B5"/></mergeCells><conditionalFormatting sqref="A1:A3"><cfRule type="expression" dxfId="0" priority="1"><formula>A1&gt;2</formula></cfRule></conditionalFormatting><dataValidations count="1"><dataValidation type="list" sqref="C1"><formula1>"a,b"</formula1></dataValidation></dataValidations><pageMargins left="0.7" right="0.7" top="0.75" bottom="0.75" header="0.3" footer="0.3"/><drawing r:id="rId9"/><unknownThing a="b"><deep/></unknownThing><extLst><ext uri="{X}"><y/></ext></extLst></worksheet>`

func richBook(t testing.TB) []byte {
	return book(t, map[string]string{
		"xl/worksheets/sheet1.xml": rich,
		"xl/sharedStrings.xml":     `<sst xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" count="1" uniqueCount="1"><si><r><rPr><b/></rPr><t>Bo</t></r><r><t>ld</t></r></si></sst>`,
		"xl/unknown/custom.bin":    "\x00\x01\x02binary",
	})
}

func TestFidelity(t *testing.T) {
	data := richBook(t)
	wb, err := Open(data)
	if err != nil {
		t.Fatal(err)
	}
	s := wb.Sheets[0]
	if s.freezeR != 1 || s.tab != "00FF00" || !strings.Contains(s.ValueAt(0, 0).Str, "Bold") {
		t.Fatalf("read wrong: %d %q %+v", s.freezeR, s.tab, s.ValueAt(0, 0))
	}
	if got := wb.Runs(s.Cell(0, 0)); len(got) != 2 || !got[0].Bold {
		t.Fatalf("runs %+v", got)
	}
	if got := s.Cell(2, 1).FormulaText(); got != "A3*2" {
		t.Fatalf("shared %q", got)
	}
	wb.Begin()
	if err := s.SetInput(2, 0, "10"); err != nil { // A3, which B3 follows
		t.Fatal(err)
	}
	out := parts(t, save(t, wb))
	x := out["xl/worksheets/sheet1.xml"]
	for _, want := range []string{
		`<c r="B1" cm="1" vm="2"><f t="shared" ref="B1:B3" si="0">A1*2</f><v>2</v><extLst><ext uri="u"><x/></ext></extLst></c>`, // untouched cell
		`<row r="1" spans="1:3" x14ac:dyDescent="0.25" ht="30" customHeight="1">`,                                               // untouched row
		`<row r="2" x14ac:dyDescent="0.25" foo="bar">`,
		`<c r="B3"><f t="shared" si="0"/><v>20</v></c>`,
		`<c r="C3" t="inlineStr"><is><t>in</t></is></c>`,
		`<unknownThing a="b"><deep/></unknownThing>`,
		`<extLst><ext uri="{X}"><y/></ext></extLst>`,
		`<drawing r:id="rId9"/>`,
		`<cfRule type="expression" dxfId="0" priority="1"><formula>A1&gt;2</formula></cfRule>`,
		`mc:Ignorable="x14ac"`,
	} {
		if !strings.Contains(x, want) {
			t.Errorf("missing %s in\n%s", want, x)
		}
	}
	if out["xl/unknown/custom.bin"] != "\x00\x01\x02binary" {
		t.Error("an unknown part changed")
	}
	if out["xl/sharedStrings.xml"] == "" || !strings.Contains(out["xl/sharedStrings.xml"], "<r><rPr><b/></rPr><t>Bo</t></r>") {
		t.Error("rich text lost")
	}
}

func TestUnchangedSheetIsByteIdentical(t *testing.T) {
	data := book(t, map[string]string{"xl/worksheets/sheet1.xml": rich})
	wb, err := Open(data)
	if err != nil {
		t.Fatal(err)
	}
	wb.Begin()
	if _, err := wb.AddSheet("Sheet", 1); err != nil {
		t.Fatal(err)
	}
	out := parts(t, save(t, wb))
	if out["xl/worksheets/sheet1.xml"] != rich {
		t.Fatal("an untouched worksheet changed")
	}
}

func TestStructureAdjustsEverything(t *testing.T) {
	wb, err := Open(richBook(t))
	if err != nil {
		t.Fatal(err)
	}
	s := wb.Sheets[0]
	wb.Begin()
	if err := s.InsertRows(1, 2); err != nil {
		t.Fatal(err)
	}
	wb.Calc(0)
	if got := s.Cell(4, 1).FormulaText(); got != "A5*2" {
		t.Fatalf("B5 %q", got)
	}
	if got := s.ValueAt(4, 1); got.Num != 8 {
		t.Fatalf("B5 value %+v", got)
	}
	x := parts(t, save(t, wb))["xl/worksheets/sheet1.xml"]
	for _, want := range []string{`<mergeCell ref="A7:B7"/>`, `sqref="A1:A5"`, `<row r="5"`, `r="B5"`} {
		if !strings.Contains(x, want) {
			t.Errorf("missing %s in\n%s", want, x)
		}
	}
	if !wb.Undo() {
		t.Fatal("no undo")
	}
	if got := s.ValueAt(2, 0); got.Num != 4 {
		t.Fatalf("undo: A3 %+v", got)
	}
	if got := s.Cell(2, 1).FormulaText(); got != "A3*2" {
		t.Fatalf("undo: %q", got)
	}
	if len(s.merges) != 1 || s.merges[0].R1 != 4 {
		t.Fatalf("undo: merges %+v", s.merges)
	}
	wb.Redo()
	if got := s.Cell(4, 1).FormulaText(); got != "A5*2" {
		t.Fatalf("redo %q", got)
	}
}

func TestUndoRestoresFormulas(t *testing.T) {
	wb := blankBook(t)
	s := wb.Sheets[0]
	wb.Begin()
	s.SetInput(0, 0, "1")
	s.SetInput(1, 0, "=A1+1")
	wb.Calc(0)
	wb.Begin()
	s.SetInput(1, 0, "=A1+5")
	wb.Calc(0)
	if s.ValueAt(1, 0).Num != 6 {
		t.Fatal(s.ValueAt(1, 0))
	}
	wb.Undo()
	if s.ValueAt(1, 0).Num != 2 {
		t.Fatal(s.ValueAt(1, 0))
	}
	wb.Begin()
	s.SetInput(0, 0, "10")
	wb.Calc(0)
	if s.ValueAt(1, 0).Num != 11 {
		t.Fatal("the engine lost the formula undo brought back", s.ValueAt(1, 0))
	}
}

func TestSnapshotIgnoresLaterEdits(t *testing.T) {
	wb := blankBook(t)
	s := wb.Sheets[0]
	wb.Begin()
	s.SetInput(0, 0, "1")
	write, err := wb.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	s.SetInput(0, 0, "2")
	data, err := write()
	if err != nil {
		t.Fatal(err)
	}
	re, err := Open(data)
	if err != nil {
		t.Fatal(err)
	}
	if re.Sheets[0].ValueAt(0, 0).Num != 1 {
		t.Fatal("the writer saw a later edit")
	}
}

func TestSortFillPaste(t *testing.T) {
	wb := blankBook(t)
	s := wb.Sheets[0]
	wb.Begin()
	for i, v := range []string{"3", "1", "x", "", "2"} {
		s.SetInput(i, 0, v)
		s.SetInput(i, 1, fmt.Sprintf("=A%d", i+1))
	}
	if err := s.Sort(formula.Area{R2: 4, C2: 1}, []SortKey{{Col: 0}}); err != nil {
		t.Fatal(err)
	}
	wb.Calc(0)
	if got := []any{s.ValueAt(0, 0).Num, s.ValueAt(1, 0).Num, s.ValueAt(2, 0).Num, s.ValueAt(3, 0).Str, s.ValueAt(0, 1).Num}; fmt.Sprint(got) != "[1 2 3 x 1]" {
		t.Fatalf("sorted %v", got)
	}
	if got := s.Cell(0, 1).FormulaText(); got != "A1" {
		t.Fatalf("moved formula %q", got)
	}
	wb.Begin()
	s.SetInput(10, 0, "2")
	s.SetInput(11, 0, "4")
	s.SetInput(10, 1, "Item 9")
	s.SetInput(10, 2, "Mon")
	s.SetInput(10, 3, "=A11*2")
	s.Fill(formula.Area{R1: 10, C1: 0, R2: 11, C2: 0}, formula.Area{R1: 10, C1: 0, R2: 13, C2: 0})
	s.Fill(formula.Area{R1: 10, C1: 1, R2: 10, C2: 3}, formula.Area{R1: 10, C1: 1, R2: 12, C2: 3})
	wb.Calc(0)
	if got := s.ValueAt(13, 0).Num; got != 8 {
		t.Fatalf("series %v", got)
	}
	if got := s.ValueAt(12, 1).Str; got != "Item 11" {
		t.Fatalf("text series %q", got)
	}
	if got := s.ValueAt(12, 2).Str; got != "Wed" {
		t.Fatalf("list %q", got)
	}
	if got := s.ValueAt(12, 3).Num; got != 12 {
		t.Fatalf("formula fill %v", got)
	}
	wb.Begin()
	cl := s.Copy(formula.Area{R1: 10, C1: 3, R2: 10, C2: 3})
	s.Paste(cl, formula.Area{R1: 20, C1: 4, R2: 20, C2: 4}, true)
	if got := s.Cell(20, 4).FormulaText(); got != "B21*2" {
		t.Fatalf("paste %q", got)
	}
}

func TestStyleAppendsOnly(t *testing.T) {
	wb := blankBook(t)
	s := wb.Sheets[0]
	before := len(wb.Styles.xfs)
	wb.Begin()
	bold, red := true, "FF0000"
	s.ApplyStyle([]formula.Area{{R2: 1, C2: 1}}, &Change{Bold: &bold, Fill: &red})
	if len(wb.Styles.xfs) != before+1 {
		t.Fatalf("%d xfs", len(wb.Styles.xfs))
	}
	st := wb.Styles.Get(s.StyleAt(1, 1))
	if !st.Bold || st.Fill != "FF0000" {
		t.Fatalf("%+v", st)
	}
	s.ApplyStyle([]formula.Area{{R1: 5, R2: 5}}, &Change{Bold: &bold, Fill: &red})
	if len(wb.Styles.xfs) != before+1 {
		t.Fatal("an equal format was added again")
	}
	re, err := Open(save(t, wb))
	if err != nil {
		t.Fatal(err)
	}
	if st := re.Styles.Get(re.Sheets[0].StyleAt(1, 1)); !st.Bold || st.Fill != "FF0000" {
		t.Fatalf("after save %+v", st)
	}
}

func TestSheetsEditing(t *testing.T) {
	wb := blankBook(t)
	s := wb.Sheets[0]
	wb.Begin()
	o, err := wb.AddSheet("Sheet", 1)
	if err != nil {
		t.Fatal(err)
	}
	o.SetInput(0, 0, "5")
	s.SetInput(0, 0, "="+o.Name+"!A1+1")
	wb.Calc(0)
	if s.ValueAt(0, 0).Num != 6 {
		t.Fatalf("%+v", s.ValueAt(0, 0))
	}
	wb.Begin()
	if err := wb.RenameSheet(o, "Data Set"); err != nil {
		t.Fatal(err)
	}
	if got := s.Cell(0, 0).FormulaText(); got != "'Data Set'!A1+1" {
		t.Fatalf("%q", got)
	}
	data := save(t, wb)
	re, err := Open(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(re.Sheets) != 2 || re.Sheets[1].Name != "Data Set" || re.Sheets[1].ValueAt(0, 0).Num != 5 {
		t.Fatalf("%+v", re.Sheets)
	}
	wb.Begin()
	if err := wb.DeleteSheet(o); err != nil {
		t.Fatal(err)
	}
	wb.Calc(0)
	if got := s.ValueAt(0, 0); got.Str != formula.ErrRef {
		t.Fatalf("%+v", got)
	}
	re, err = Open(save(t, wb))
	if err != nil {
		t.Fatal(err)
	}
	if len(re.Sheets) != 1 {
		t.Fatal("the sheet stayed")
	}
	wb.Undo()
	if len(wb.Sheets) != 2 || s.Cell(0, 0).FormulaText() != "'Data Set'!A1+1" {
		t.Fatal("undo of delete")
	}
}

func TestHostileFiles(t *testing.T) {
	for _, x := range []string{
		`<worksheet xmlns="x"><sheetData><row r="0"/></sheetData></worksheet>`,
		`<worksheet xmlns="x"><sheetData><row r="99999999"/></sheetData></worksheet>`,
		`<worksheet xmlns="x"><sheetData><row r="1"><c r="ZZZZ1"/></row></sheetData></worksheet>`,
		`<worksheet xmlns="x"><sheetData><row r="1"><c r="A1"><v>abc</v></c></row></sheetData></worksheet>`,
		`<worksheet xmlns="x"><sheetData><row r="1"><c r="A1"`,
		`<worksheet xmlns="x"><sheetData><row r="1"></row><row r="1"></row></sheetData></worksheet>`,
	} {
		if _, err := Open(book(t, map[string]string{"xl/worksheets/sheet1.xml": x})); err == nil {
			t.Errorf("no error for %s", x)
		}
	}
	// A shared formula with no first cell, a bad style index and a string out of range are survivable.
	wb, err := Open(book(t, map[string]string{"xl/worksheets/sheet1.xml": `<worksheet xmlns="x"><sheetData><row r="1"><c r="A1" s="99" t="s"><v>7</v></c><c r="B1"><f t="shared" si="3"/><v>1</v></c></row></sheetData></worksheet>`}))
	if err != nil {
		t.Fatal(err)
	}
	if wb.Sheets[0].Shown(0, 0).Text != "" {
		t.Fatal("shown")
	}
}

func TestLargeSheetLoadsFast(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	var b strings.Builder
	b.WriteString(`<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData>`)
	const rows, cols = 100000, 10
	for r := 1; r <= rows; r++ {
		fmt.Fprintf(&b, `<row r="%d">`, r)
		for c := 0; c < cols-1; c++ {
			fmt.Fprintf(&b, `<c r="%s%d"><v>%d</v></c>`, string(rune('A'+c)), r, r*c)
		}
		fmt.Fprintf(&b, `<c r="J%d"><f>A%d+B%d</f><v>%d</v></c></row>`, r, r, r, r)
	}
	b.WriteString(`</sheetData></worksheet>`)
	data := book(t, map[string]string{"xl/worksheets/sheet1.xml": b.String()})
	start := time.Now()
	wb, err := Open(data)
	if err != nil {
		t.Fatal(err)
	}
	took := time.Since(start)
	t.Logf("1M cells, 100k formulas: open %v", took)
	if took > 10*time.Second {
		t.Fatalf("too slow: %v", took)
	}
	s := wb.Sheets[0]
	wb.Begin()
	start = time.Now()
	s.SetInput(5, 1, "1000")
	wb.Calc(0)
	if took := time.Since(start); took > 200*time.Millisecond {
		t.Fatalf("a small edit took %v", took)
	}
	if s.ValueAt(5, 9).Num != 1000 {
		t.Fatal(s.ValueAt(5, 9))
	}
	start = time.Now()
	data2 := save(t, wb)
	t.Logf("save %v, %d bytes", time.Since(start), len(data2))
}

func TestPrefixedParts(t *testing.T) {
	sheet := `<x:worksheet xmlns:x="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><x:sheetData><x:row r="1"><x:c r="A1"><x:v>4</x:v></x:c><x:c r="B1"><x:f>A1*2</x:f><x:v>8</x:v></x:c></x:row></x:sheetData></x:worksheet>`
	wb, err := Open(book(t, map[string]string{"xl/worksheets/sheet1.xml": sheet}))
	if err != nil {
		t.Fatal(err)
	}
	s := wb.Sheets[0]
	wb.Begin()
	if err := s.SetInput(0, 0, "5"); err != nil {
		t.Fatal(err)
	}
	re, err := Open(save(t, wb))
	if err != nil {
		t.Fatal(err)
	}
	if got := re.Sheets[0].ValueAt(0, 1).Num; got != 10 {
		t.Fatalf("%v", got)
	}
}

func TestRowChildrenSurvive(t *testing.T) {
	sheet := `<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData><row r="1" custom="x"><c r="A1"><v>1</v></c><extLst><ext uri="r"/></extLst></row></sheetData></worksheet>`
	wb, err := Open(book(t, map[string]string{"xl/worksheets/sheet1.xml": sheet}))
	if err != nil {
		t.Fatal(err)
	}
	wb.Begin()
	wb.Sheets[0].SetInput(0, 0, "2")
	x := parts(t, save(t, wb))["xl/worksheets/sheet1.xml"]
	for _, want := range []string{`custom="x"`, `<extLst><ext uri="r"/></extLst></row>`, `<v>2</v>`} {
		if !strings.Contains(x, want) {
			t.Fatalf("%s missing in %s", want, x)
		}
	}
}

func TestThemeTint(t *testing.T) {
	wb := blankBook(t)
	// Office's "Accent 1, Lighter 40%" and "Darker 25%".
	for _, c := range []struct {
		tint float64
		want string
	}{{0.39997558519241921, "8EA9DB"}, {-0.249977111117893, "305496"}} {
		// Office rounds on its own 0 to 240 scale, so a channel can differ by a step or two.
		got := wb.Styles.ThemeColor(4, c.tint)
		for i := 0; i < 6; i += 2 {
			a, _ := strconv.ParseInt(got[i:i+2], 16, 32)
			b, _ := strconv.ParseInt(c.want[i:i+2], 16, 32)
			if a-b > 2 || b-a > 2 {
				t.Errorf("tint %v: %s, want %s", c.tint, got, c.want)
			}
		}
	}
}
