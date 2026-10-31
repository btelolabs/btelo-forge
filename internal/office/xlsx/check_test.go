package xlsx

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/btelolabs/btelo-forge/internal/office/formula"
)

// TestOfficeCheck writes the files tools/officecheck/sheet_check.py reads with another library. It runs only for that
// script.
func TestOfficeCheck(t *testing.T) {
	dir := os.Getenv("FORGE_XLSX_CHECK")
	if dir == "" {
		t.Skip("run by tools/officecheck/sheet_check.py")
	}
	write := func(name string, data []byte) {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	blank, err := Blank("Sheet1")
	if err != nil {
		t.Fatal(err)
	}
	write("blank.xlsx", blank)
	write("made.xlsx", madeBook(t))
	in, err := os.ReadFile(filepath.Join(dir, "in.xlsx"))
	if err != nil {
		t.Fatal(err)
	}
	wb, err := Open(in)
	if err != nil {
		t.Fatal(err)
	}
	wb.Begin()
	if err := wb.Sheet("Data").SetInput(2, 1, "50"); err != nil {
		t.Fatal(err)
	}
	wb.Calc(0)
	write("out.xlsx", save(t, wb))
	wb, err = Open(in)
	if err != nil {
		t.Fatal(err)
	}
	wb.Begin()
	if err := wb.Sheet("Data").InsertRows(2, 2); err != nil {
		t.Fatal(err)
	}
	wb.Calc(0)
	write("rows.xlsx", save(t, wb))
}

func madeBook(t *testing.T) []byte {
	wb := blankBook(t)
	s := wb.Sheets[0]
	wb.Begin()
	for _, c := range []struct{ ref, text string }{{"A1", "Hello 世界"}, {"B1", "2.5"}, {"C1", "=B1*2"}, {"D2", "x"}, {"A3", "m"}, {"A7", "gone"}, {"H1", "a"}, {"I1", "b"}, {"H2", "1"}} {
		r, c2, _ := formula.ParseCell(c.ref)
		if err := s.SetInput(r, c2, c.text); err != nil {
			t.Fatal(err)
		}
	}
	yes, size, ink, fill, pct, center := true, 14.0, "112233", "FFFF00", "0.00%", "center"
	one := func(ref string) []formula.Area {
		a, _ := formula.ParseArea(ref)
		return []formula.Area{a}
	}
	s.ApplyStyle(one("A1"), &Change{Bold: &yes, Size: &size, Color: &ink, Fill: &fill})
	s.ApplyStyle(one("B1"), &Change{NumFmt: &pct})
	edge := Edge{Style: "thin"}
	s.ApplyStyle(one("D2"), &Change{Left: &edge, HAlign: &center, Wrap: &yes})
	if err := s.Merge(one("A3:B4")[0]); err != nil {
		t.Fatal(err)
	}
	s.SetFreeze(1, 1)
	s.SetTabColor("00B050")
	s.SetColWidth(5, 5, 30)
	s.SetRowHeight(5, 5, 40)
	s.SetRowsHidden(6, 6, true)
	s.SetAutoFilter(one("H1:I4")[0], true)
	o, err := wb.AddSheet("Sheet", 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := wb.RenameSheet(o, "Second"); err != nil {
		t.Fatal(err)
	}
	if err := o.SetInput(0, 0, "=Sheet1!C1*2"); err != nil {
		t.Fatal(err)
	}
	wb.Calc(0)
	return save(t, wb)
}
