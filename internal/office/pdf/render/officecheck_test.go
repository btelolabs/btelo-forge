package render

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/btelolabs/btelo-forge/internal/office/pdf"
)

// TestOfficeCheck edits the files tools/officecheck/pdf_check.py made, for pypdf to judge. It runs only under that script.
func TestOfficeCheck(t *testing.T) {
	dir := os.Getenv("FORGE_OFFICECHECK")
	if dir == "" {
		t.Skip("run tools/officecheck/pdf_check.py")
	}
	book := testFonts(t).Book
	load := func(name string) *pdf.Doc {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		d, err := pdf.Open(data, "")
		if err != nil {
			t.Fatal(err)
		}
		d.SetFonts(func(family string, bold, italic bool, text string) string {
			return book.File("微软雅黑", false, false)
		})
		return d
	}
	write := func(name string, b []byte, err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Annotations of each kind, and text, by appending.
	d := load("rich.pdf")
	specs := []pdf.AnnotSpec{
		{Kind: "Highlight", Quads: [][8]float64{{72, 720, 200, 720, 72, 700, 200, 700}}, Color: [3]float64{1, 1, 0}},
		{Kind: "Underline", Quads: [][8]float64{{72, 690, 200, 690, 72, 670, 200, 670}}, Color: [3]float64{1, 0, 0}},
		{Kind: "StrikeOut", Quads: [][8]float64{{72, 660, 200, 660, 72, 640, 200, 640}}, Color: [3]float64{1, 0, 0}},
		{Kind: "Ink", Ink: [][]float64{{100, 100, 150, 150, 200, 100}}, Color: [3]float64{0, 0, 1}, Width: 2},
		{Kind: "Square", Rect: pdf.Rect{X0: 300, Y0: 300, X1: 400, Y1: 400}, Color: [3]float64{1, 0, 0}, Width: 2},
		{Kind: "Circle", Rect: pdf.Rect{X0: 420, Y0: 300, X1: 520, Y1: 400}, Color: [3]float64{0, 1, 0}, Width: 2},
		{Kind: "Line", Line: [4]float64{300, 250, 500, 250}, Arrow: true, Color: [3]float64{0, 0, 1}, Width: 2},
		{Kind: "Text", Rect: pdf.Rect{X0: 500, Y0: 700, X1: 520, Y1: 720}, Contents: "A note", Color: [3]float64{1, 0.9, 0.3}},
		{Kind: "FreeText", Rect: pdf.Rect{X0: 100, Y0: 300, X1: 280, Y1: 340}, Contents: "Free 文本", Color: [3]float64{0, 0, 0}, Size: 12},
	}
	for _, s := range specs {
		if _, err := d.AddAnnot(0, s); err != nil {
			t.Fatal(err)
		}
	}
	if err := d.AddText(0, "你好世界 added text", 72, 200, 0, pdf.TextStyle{Size: 14}); err != nil {
		t.Fatal(err)
	}
	if err := d.CoverRect(1, pdf.Rect{X0: 0, Y0: 0, X1: 50, Y1: 50}, [3]float64{1, 1, 1}); err != nil {
		t.Fatal(err)
	}
	if err := d.RotatePages([]int{2}, 90); err != nil {
		t.Fatal(err)
	}
	b, err := d.Bytes()
	write("edited.pdf", b, err)
	// Structural work.
	d = load("rich.pdf")
	if err := d.DeletePages([]int{1}); err != nil {
		t.Fatal(err)
	}
	b, err = d.Bytes()
	write("deleted.pdf", b, err)
	d = load("rich.pdf")
	if err := d.Merge(load("rich.pdf"), 1); err != nil {
		t.Fatal(err)
	}
	b, err = d.Bytes()
	write("merged.pdf", b, err)
	d = load("rich.pdf")
	if err := d.MovePages([]int{0}, 2); err != nil {
		t.Fatal(err)
	}
	if err := d.InsertBlank(1, 300, 400); err != nil {
		t.Fatal(err)
	}
	b, err = d.Bytes()
	write("reordered.pdf", b, err)
	d = load("rich.pdf")
	b, err = d.Snapshot().Rewrite(true)
	write("compressed.pdf", b, err)
	d = load("compressed.pdf")
	if _, err := d.AddAnnot(0, pdf.AnnotSpec{Kind: "Square", Rect: pdf.Rect{X0: 10, Y0: 10, X1: 90, Y1: 90}, Color: [3]float64{1, 0, 0}}); err != nil {
		t.Fatal(err)
	}
	b, err = d.Bytes()
	write("compressed-edited.pdf", b, err)
	b, err = load("rich.pdf").Extract([]int{0, 2})
	write("extracted.pdf", b, err)
	// Forms.
	d = load("form.pdf")
	for _, f := range d.Fields() {
		v := ""
		switch f.Kind {
		case pdf.FieldText:
			v = "filled 中文"
		case pdf.FieldCheck:
			v = string(f.Widgets[0].State)
		default:
			continue
		}
		if err := d.SetField(&f, v); err != nil {
			t.Fatal(err)
		}
	}
	b, err = d.Bytes()
	write("form-filled.pdf", b, err)
}
