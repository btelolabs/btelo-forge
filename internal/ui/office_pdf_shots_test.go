package ui

import (
	"image"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/btelolabs/btelo-forge/internal/gfx"
	"github.com/btelolabs/btelo-forge/internal/office/pdf"
)

// TestPDFWorkShots shows the tools at work, and a real file when FORGE_PDF_REAL names one.
func TestPDFWorkShots(t *testing.T) {
	if os.Getenv("FORGE_SHOTS") == "" {
		t.Skip("set FORGE_SHOTS")
	}
	x, e := pdfHarness(t, 3)
	fitPage(x, e)
	add := func(s pdf.AnnotSpec) {
		if _, err := e.doc.AddAnnot(0, s); err != nil {
			t.Fatal(err)
		}
	}
	text := e.textOf(0)
	m := text.Search("quick brown fox")[0]
	add(pdf.AnnotSpec{Kind: "Highlight", Quads: text.Quads(m.From, m.To), Color: [3]float64{1, 0.9, 0.2}})
	m = text.Search("lazy dog")[0]
	add(pdf.AnnotSpec{Kind: "Underline", Quads: text.Quads(m.From, m.To), Color: [3]float64{0.9, 0.2, 0.2}})
	m = text.Search("plain words")[0]
	add(pdf.AnnotSpec{Kind: "StrikeOut", Quads: text.Quads(m.From, m.To), Color: [3]float64{0.9, 0.2, 0.2}})
	add(pdf.AnnotSpec{Kind: "Ink", Ink: [][]float64{{330, 420, 360, 470, 390, 420, 420, 470, 450, 420}}, Color: [3]float64{0.2, 0.3, 0.9}, Width: 2.5})
	add(pdf.AnnotSpec{Kind: "Square", Rect: pdf.Rect{X0: 80, Y0: 300, X1: 200, Y1: 380}, Color: [3]float64{0.9, 0.2, 0.2}, Width: 2})
	add(pdf.AnnotSpec{Kind: "Circle", Rect: pdf.Rect{X0: 220, Y0: 300, X1: 320, Y1: 380}, Color: [3]float64{0.1, 0.6, 0.3}, Width: 2})
	add(pdf.AnnotSpec{Kind: "Line", Line: [4]float64{340, 310, 500, 370}, Arrow: true, Color: [3]float64{0.2, 0.3, 0.9}, Width: 2})
	add(pdf.AnnotSpec{Kind: "Text", Rect: pdf.Rect{X0: 520, Y0: 700, X1: 540, Y1: 720}, Contents: "A note", Color: [3]float64{1, 0.85, 0.3}})
	add(pdf.AnnotSpec{Kind: "FreeText", Rect: pdf.Rect{X0: 80, Y0: 220, X1: 300, Y1: 280}, Contents: "A text box that wraps over more than one line of the box. 中文也可以。", Color: [3]float64{0, 0, 0.5}, Size: 13})
	if err := e.doc.AddText(0, "新增的页面文字 Added words", 80, 180, 0, pdf.TextStyle{Family: "arial", Size: 16, Color: [3]float64{0.5, 0, 0.5}}); err != nil {
		t.Fatal(err)
	}
	if err := e.doc.CoverRect(0, pdf.Rect{X0: 72, Y0: 660, X1: 120, Y1: 682}, [3]float64{1, 1, 1}); err != nil {
		t.Fatal(err)
	}
	for _, f := range e.doc.Fields() {
		switch f.Name {
		case "name":
			e.doc.SetField(&f, "填好的名字 Filled")
		case "agree":
			e.doc.SetField(&f, "Yes")
		case "colour":
			e.doc.SetField(&f, "blue")
		case "size":
			e.doc.SetField(&f, "l")
		}
	}
	e.touched(0)
	e.rev++
	settlePDF(x, e)
	x.shot("en-pdf-annotated")
	// Choosing an annotation.
	e.sel = &pdfSel{ref: e.doc.Annots(0)[11].Ref, page: 0, kind: "Square", rect: e.doc.Annots(0)[11].Rect}
	x.frame(Input{})
	x.frame(Input{})
	x.shot("en-pdf-annotation-chosen")
	e.sel = nil
	// A selection, with the menu on it.
	text = e.textOf(0)
	m = text.Search("Page 1 of 3")[0]
	e.selPage, e.selFrom, e.selTo = 0, m.From, m.To
	px, py := pointAt(e, 0, 150, 710)
	x.mx, x.my = px, py
	x.frame(Input{Pointer: []Pointer{{X: px, Y: py, Right: true, Down: true}}})
	x.frame(Input{Pointer: []Pointer{{X: px, Y: py, Right: true}}})
	x.frame(Input{})
	x.shot("en-pdf-menu")
	x.a.menu = nil
	e.clearSelection()
	// Writing and placing.
	e.tool = pdfToolText
	e.startPendingText(0, 80, 140, pdfPendText)
	e.pending.text = "Text being written"
	x.frame(Input{})
	x.frame(Input{})
	x.shot("en-pdf-write")
	e.cancelPending()
	e.tool = pdfToolEditText
	e.startReplace(0, 100, 674)
	x.frame(Input{})
	x.frame(Input{})
	x.shot("en-pdf-replace")
	e.cancelPending()
	e.tool = pdfToolSelect
	img := image.NewRGBA(image.Rect(0, 0, 200, 120))
	for i := range img.Pix {
		if i%4 == 3 || i%4 == 2 {
			img.Pix[i] = 230
		}
	}
	e.placePicture(img, nil)
	x.frame(Input{})
	x.frame(Input{})
	x.shot("en-pdf-place-image")
	e.cancelPending()
	e.picked = map[int]bool{1: true, 2: true}
	x.a.openMenu("office/pdf/pages", gfx1600(), e.pagesMenu(e.chosenPages()))
	x.frame(Input{})
	x.frame(Input{})
	x.shot("en-pdf-pages-menu")
	x.a.menu = nil
	x.a.u.lang = Chinese
	x.a.openMenu("office/pdf/pages", gfx1600(), e.pagesMenu(e.chosenPages()))
	x.frame(Input{})
	x.frame(Input{})
	x.shot("zh-pdf-pages-menu")
	x.a.menu = nil
	x.a.u.lang = English
	if real := os.Getenv("FORGE_PDF_REAL"); real != "" {
		data, err := os.ReadFile(real)
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(x.dir, filepath.Base(real))
		os.WriteFile(path, data, 0o644)
		x.a.openOfficeFile(path)
		x.pump(10*time.Second, func() bool { return x.a.office != nil && x.a.office.path == path })
		r := x.a.office.ed.(*pdfEditor)
		settlePDF(x, r)
		x.shot("en-pdf-real")
		r.setZoom(3, r.area.X+r.area.W/2, r.area.Y)
		settlePDF(x, r)
		x.shot("en-pdf-real-zoomed")
		r.setZoom(12, r.area.X+r.area.W/2, r.area.Y)
		settlePDF(x, r)
		x.shot("en-pdf-real-zoomed-more")
	}
}

func gfx1600() gfx.Rect { return gfx.Rect{X: 1400, Y: 76} }
