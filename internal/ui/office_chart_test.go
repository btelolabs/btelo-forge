package ui

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/btelolabs/btelo-forge/internal/printing"
)

// FORGE_CHARTS names a folder of charts.xlsx, charts.pptx and charts.docx (tools/officecheck/chart_samples.py);
// FORGE_SHOTS is where the pictures of them go.
func TestChartSamples(t *testing.T) {
	dir := os.Getenv("FORGE_CHARTS")
	if dir == "" {
		t.Skip("set FORGE_CHARTS to look at charts")
	}
	x := newHarness(t)
	open := func(name string) *officeDoc {
		t.Helper()
		path := filepath.Join(dir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		od, err := x.a.openOffice(officeFormatOf(path), path, data)
		if err != nil {
			t.Fatal(err)
		}
		for range 200 {
			x.frame(Input{})
			if s, ok := od.ed.(*sheetEd); !ok || s.wb != nil {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		x.frame(Input{})
		x.frame(Input{})
		return od
	}
	od := open("charts.xlsx")
	ed := od.ed.(*sheetEd)
	v := ed.viewOf(ed.sheet())
	if len(v.drawings) != 12 {
		t.Fatalf("%d drawings", len(v.drawings))
	}
	for i := range v.drawings {
		if ed.wb.Chart(v.drawings[i].Chart) == nil {
			t.Fatalf("chart %d of %s cannot be drawn", i, v.drawings[i].Chart)
		}
	}
	ed.zoom = 0.6
	x.frame(Input{})
	x.frame(Input{})
	x.shot("chart-sheet-1")
	v.scrollY = 800
	x.frame(Input{})
	x.shot("chart-sheet-2")
	v.scrollX, v.scrollY = 1100, 0
	x.frame(Input{})
	x.shot("chart-sheet-3")
	v.scrollY = 800
	x.frame(Input{})
	x.shot("chart-sheet-4")
	v.scrollX, v.scrollY = 0, 0
	ed.zoom = 1
	src := x.a.officePrintSource(od).(*sheetPrint)
	if n := src.count(); n < 1 {
		t.Fatal(n)
	}
	src.fit = true
	exportPDF(t, "chart-sheet", src)
	od = open("charts.pptx")
	x.frame(Input{})
	x.shot("chart-slides")
	sl := x.a.officePrintSource(od).(*slidesPrint)
	pg, err := sl.page(0)
	if err != nil {
		t.Fatal(err)
	}
	if !hasText(pg) {
		t.Fatal("a printed chart should hold its words")
	}
	exportPDF(t, "chart-slides", sl)
	od = open("charts.docx")
	for range 20 {
		x.frame(Input{})
	}
	x.shot("chart-word")
	exportPDF(t, "chart-word", x.a.officePrintSource(od))
}

// exportPDF writes what a source prints where FORGE_SHOTS says, for tools/officecheck/print_check.py.
func exportPDF(t *testing.T, name string, src printSource) {
	t.Helper()
	dir := os.Getenv("FORGE_SHOTS")
	if dir == "" {
		return
	}
	d, err := allPages(src)
	if err != nil {
		t.Fatal(err)
	}
	data, err := encodePDF(context.Background(), d, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".pdf"), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func hasText(pg *printing.Page) bool {
	for _, op := range pg.Ops {
		if _, ok := op.(*printing.Text); ok {
			return true
		}
	}
	return false
}

// A comma-separated file opens as a sheet, and an edit saves it as the same kind of file.
func TestACSVFileIsASheetThatStaysCSV(t *testing.T) {
	x := newHarness(t)
	path := filepath.Join(t.TempDir(), "list.csv")
	if err := os.WriteFile(path, []byte("\xef\xbb\xbf名称;编号\r\n苹果;007\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	x.a.openOfficeFile(path)
	var ed *sheetEd
	x.pump(5*time.Second, func() bool {
		x.frame(Input{})
		if x.a.office != nil {
			ed, _ = x.a.office.ed.(*sheetEd)
		}
		return ed != nil
	})
	if ed == nil {
		t.Fatal("not opened")
	}
	s := ed.sheet()
	if got := s.ValueAt(1, 1); got.Str != "007" {
		t.Fatalf("%+v", got)
	}
	if err := s.SetInput(1, 0, "梨"); err != nil {
		t.Fatal(err)
	}
	write, err := ed.snapshot()
	if err != nil {
		t.Fatal(err)
	}
	data, err := write()
	if err != nil {
		t.Fatal(err)
	}
	if want := "\xef\xbb\xbf名称;编号\r\n梨;007\r\n"; string(data) != want {
		t.Fatalf("%q", data)
	}
	x.frame(Input{})
	x.a.u.lang = Chinese
	x.frame(Input{})
	x.shot("csv")
}

// FORGE_REAL names a folder of real documents. Each is opened, changed in one small way and saved under FORGE_REAL_OUT,
// with a picture of it in FORGE_SHOTS, for tools/officecheck/real_check.py to compare with the original.
func TestRealDocuments(t *testing.T) {
	dir, out := os.Getenv("FORGE_REAL"), os.Getenv("FORGE_REAL_OUT")
	if dir == "" || out == "" {
		t.Skip("set FORGE_REAL and FORGE_REAL_OUT")
	}
	names, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range names {
		t.Run(e.Name(), func(t *testing.T) {
			x := newHarness(t)
			data, err := os.ReadFile(filepath.Join(dir, e.Name()))
			if err != nil {
				t.Fatal(err)
			}
			// A copy, as a tab saves itself over its file.
			path := filepath.Join(t.TempDir(), e.Name())
			if err := os.WriteFile(path, data, 0o644); err != nil {
				t.Fatal(err)
			}
			f := officeFormatOf(path)
			if f == nil {
				t.Skip("not a document")
			}
			od, err := x.a.openOffice(f, path, data)
			if err != nil {
				t.Fatal(err)
			}
			if s, ok := od.ed.(*sheetEd); ok {
				x.pump(60*time.Second, func() bool { x.frame(Input{}); return s.wb != nil })
				s.wb.Begin()
				if err := s.sheet().SetInput(1048000, 0, "edited"); err != nil {
					t.Fatal(err)
				}
			}
			if w, ok := od.ed.(*wordEditor); ok {
				w.d.Layout(time.Minute)
				w.d.Type("edited ")
			}
			if sl, ok := od.ed.(*slidesEditor); ok && len(sl.d.Slides) > 0 {
				if err := sl.d.SetNotes(sl.d.Slides[0], "edited", ""); err != nil {
					t.Fatal(err)
				}
			}
			for range 5 {
				x.frame(Input{})
			}
			x.shot("real-" + e.Name())
			write, err := od.ed.snapshot()
			if err != nil {
				t.Fatal(err)
			}
			saved, err := write()
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(out, e.Name()), saved, 0o644); err != nil {
				t.Fatal(err)
			}
		})
	}
}
