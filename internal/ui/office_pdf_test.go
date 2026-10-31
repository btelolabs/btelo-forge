package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/btelolabs/btelo-forge/internal/gfx"
	"github.com/btelolabs/btelo-forge/internal/office/pdf"
	"github.com/btelolabs/btelo-forge/internal/office/pdf/pdftest"
)

// pdfHarness opens the sample document in a tab.
func pdfHarness(t *testing.T, pages int) (*harness, *pdfEditor) {
	t.Helper()
	x := newHarness(t)
	path := filepath.Join(x.dir, "sample.pdf")
	if err := os.WriteFile(path, pdftest.Sample(pages), 0o644); err != nil {
		t.Fatal(err)
	}
	x.a.openOfficeFile(path)
	x.pump(10*time.Second, func() bool { return x.a.office != nil })
	e := x.a.office.ed.(*pdfEditor)
	settlePDF(x, e)
	return x, e
}

func settlePDF(x *harness, e *pdfEditor) {
	x.t.Helper()
	calm := 0
	x.pump(20*time.Second, func() bool {
		if e.busy() {
			calm = 0
		} else {
			calm++
		}
		return calm > 3
	})
}

// pointAt is where a user space point of a page is in the window.
func pointAt(e *pdfEditor, page int, ux, uy float64) (float32, float32) {
	return e.toScreen(page, ux, uy)
}

func (x *harness) clickAt(px, py float32) {
	x.click(gfx.Rect{X: px, Y: py})
}

func TestPDFViewsAndSelects(t *testing.T) {
	x, e := pdfHarness(t, 3)
	if len(e.pages) != 3 {
		t.Fatalf("pages %d", len(e.pages))
	}
	text := e.textOf(0)
	if text == nil || !strings.Contains(text.Full(), "quick brown fox") {
		t.Fatalf("text %v", text)
	}
	// Dragging across words selects them; Ctrl+C copies them.
	ax, ay := pointAt(e, 0, 72, 674)
	bx, by := pointAt(e, 0, 170, 674)
	x.drag(ax, ay, bx, by)
	if got := e.selectionText(); !strings.HasPrefix(got, "The quick") {
		t.Fatalf("selected %q", got)
	}
	x.key('C', true)
	if !strings.HasPrefix(x.h.clip, "The quick") {
		t.Fatalf("clip %q", x.h.clip)
	}
	// A link goes to the last page.
	lx, ly := pointAt(e, 0, 100, 648)
	x.clickAt(lx, ly)
	settlePDF(x, e)
	if e.currentPage() != 2 {
		t.Fatalf("after the link, page %d", e.currentPage()+1)
	}
}

func TestPDFSearch(t *testing.T) {
	x, e := pdfHarness(t, 4)
	x.key('F', true)
	if !e.searchOpen {
		t.Fatal("Ctrl+F did not open the search")
	}
	e.query = "lazy dog"
	e.search()
	settlePDF(x, e)
	if len(e.matches) != 4 {
		t.Fatalf("%d matches", len(e.matches))
	}
}

func TestPDFAnnotateUndoSave(t *testing.T) {
	x, e := pdfHarness(t, 2)
	original, _ := os.ReadFile(e.od.path)
	e.fit = pdfFitPage
	settlePDF(x, e)
	e.tool = pdfToolRect
	ax, ay := pointAt(e, 0, 300, 400)
	bx, by := pointAt(e, 0, 400, 300)
	x.drag(ax, ay, bx, by)
	if n := len(e.doc.Annots(0)); n != 8 {
		t.Fatalf("%d annotations", n)
	}
	e.undo()
	if n := len(e.doc.Annots(0)); n != 7 {
		t.Fatalf("after undo %d annotations", n)
	}
	e.redo()
	settlePDF(x, e)
	// Move it.
	e.tool = pdfToolSelect
	cx, cy := pointAt(e, 0, 350, 350)
	x.drag(cx, cy, cx+40, cy)
	if e.sel == nil {
		t.Fatal("the rectangle was not selected")
	}
	if e.sel.rect.X0 < 310 {
		t.Fatalf("did not move: %+v", e.sel.rect)
	}
	x.key('S', true)
	saved, err := os.ReadFile(e.od.path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(saved), string(original)) {
		t.Fatal("the save did not keep the file's bytes")
	}
	d, err := pdf.Open(saved, "")
	if err != nil {
		t.Fatal(err)
	}
	if n := len(d.Annots(0)); n != 8 {
		t.Fatalf("saved %d annotations", n)
	}
}

func TestPDFForm(t *testing.T) {
	x, e := pdfHarness(t, 1)
	cx, cy := pointAt(e, 0, 81, 539)
	x.clickAt(cx, cy)
	for _, f := range e.doc.Fields() {
		if f.Name == "agree" && f.Value != "Yes" {
			t.Fatalf("check box %q", f.Value)
		}
	}
	nx, ny := pointAt(e, 0, 100, 570)
	x.clickAt(nx, ny)
	if e.active == nil {
		t.Fatal("the text field did not open")
	}
	e.active.value = "Grace 中文"
	e.commitField()
	for _, f := range e.doc.Fields() {
		if f.Name == "name" && f.Value != "Grace 中文" {
			t.Fatalf("name %q", f.Value)
		}
	}
}

func TestPDFWrongFileDoesNotOpen(t *testing.T) {
	x := newHarness(t)
	f := officeFormatOf("x.pdf")
	if _, err := x.a.openOffice(f, "x.pdf", []byte("not a pdf at all")); err == nil {
		t.Fatal("no error")
	}
}

func TestPDFShots(t *testing.T) {
	if os.Getenv("FORGE_SHOTS") == "" {
		t.Skip("set FORGE_SHOTS")
	}
	x, e := pdfHarness(t, 5)
	for _, lang := range []Language{English, Chinese} {
		x.a.u.lang = lang
		e.tool = pdfToolSelect
		x.frame(Input{})
		x.frame(Input{})
		x.shot(string(lang) + "-pdf-view")
		e.tool = pdfToolHighlight
		x.frame(Input{})
		x.frame(Input{})
		x.shot(string(lang) + "-pdf-tool-options")
		e.tool = pdfToolSelect
		e.sideTab = 2
		e.outlineOpen = map[string]bool{}
		e.searchOpen = true
		e.query = "fox"
		e.search()
		settlePDF(x, e)
		x.shot(string(lang) + "-pdf-outline-search")
		e.closeSearch()
		e.sideTab = 1
		e.openSignPad()
		x.frame(Input{})
		x.shot(string(lang) + "-pdf-sign")
		e.sign = nil
		e.openSaveAs(nil, true)
		x.frame(Input{})
		x.shot(string(lang) + "-pdf-saveas")
		e.saveAs = nil
		x.frame(Input{})
	}
}
