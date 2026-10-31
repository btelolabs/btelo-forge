package ui

import (
	"testing"

	"github.com/btelolabs/btelo-forge/internal/gfx"
	"github.com/btelolabs/btelo-forge/internal/office/docx"
)

func TestPaperKeepsItsOrientation(t *testing.T) {
	a4 := wordPapers[1]
	up := a4.on(docx.PageSetup{W: 100, H: 200})
	if up.W >= up.H || paperIndex(up) != 1 {
		t.Fatalf("%+v", up)
	}
	flat := a4.on(docx.PageSetup{W: 200, H: 100})
	if flat.W <= flat.H || paperIndex(flat) != 1 {
		t.Fatalf("%+v", flat)
	}
	if got := paperIndex(docx.PageSetup{W: 123, H: 456}); got != -1 {
		t.Fatal(got)
	}
}

func TestPagePanelChangesTheDocument(t *testing.T) {
	x := newHarness(t)
	e := wordOpen(t, x)
	x.frame(Input{})
	x.frame(Input{})
	e.openPop("page", gfx.Rect{X: 300, Y: 30, W: 90, H: 30})
	e.loadPagePanel()
	x.frame(Input{})
	x.shot("word-page-setup")
	if e.page.fields[0] != "210" {
		t.Fatalf("the width box says %q", e.page.fields[0])
	}
	if p, ok := e.typedPage(); !ok || paperIndex(p) != 1 {
		t.Fatalf("%+v %v", p, ok)
	}
	e.page.fields[0], e.page.fields[1] = "148", "210"
	p, ok := e.typedPage()
	if !ok {
		t.Fatal("the boxes hold sizes")
	}
	e.applyPage(p)
	x.frame(Input{})
	x.frame(Input{})
	x.shot("word-page-a5")
	if got := paperIndex(docx.PageSetupOf(e.d.SectionAtCaret())); got != 2 {
		t.Fatal(got)
	}
	e.page.fields[0] = "wide"
	if _, ok := e.typedPage(); ok {
		t.Fatal("words were taken for a size")
	}
}
