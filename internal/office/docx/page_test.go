package docx

import (
	"testing"
	"time"
)

func TestPageSetupChangesThePagesAndUndoes(t *testing.T) {
	d := blankDoc(t)
	d.Layout(time.Minute)
	before := PageSetupOf(d.SectionAtCaret())
	p := before
	p.W, p.H = 842, 1191
	d.SetPageSetup(p.Turned(), true)
	d.Layout(time.Minute)
	got := PageSetupOf(d.SectionAtCaret())
	if got.W != 1191 || got.H != 842 || !got.Landscape() {
		t.Fatalf("%+v", got)
	}
	if pg := d.Pages()[0]; pg.W != 1191 || pg.H != 842 {
		t.Fatalf("the page is %v × %v", pg.W, pg.H)
	}
	sz := d.Sections()[0].Node.Child("w:pgSz")
	if sz.Attr("w:orient") != "landscape" || sz.Attr("w:w") != "23820" {
		t.Fatalf("%+v", sz.Attrs)
	}
	d.Undo()
	d.Layout(time.Minute)
	if PageSetupOf(d.SectionAtCaret()) != before {
		t.Fatalf("undo left %+v", PageSetupOf(d.SectionAtCaret()))
	}
}

func TestPageSetupComesBackFromTheFile(t *testing.T) {
	d := blankDoc(t)
	d.Layout(time.Minute)
	p := PageSetupOf(d.SectionAtCaret())
	p.W, p.H, p.MT, p.ML = 595.3, 419.5, 30, 40
	d.SetPageSetup(p, true)
	data, err := d.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	again, err := Open(data, testFonts(t))
	if err != nil {
		t.Fatal(err)
	}
	again.Layout(time.Minute)
	got := PageSetupOf(again.SectionAtCaret())
	if got.W != 595.3 || got.H != 419.5 || got.MT != 30 || got.ML != 40 {
		t.Fatalf("%+v", got)
	}
}

func TestMarginsLeaveRoomForText(t *testing.T) {
	p := PageSetup{W: 200, H: 200, MT: 500, MB: 500, ML: 500, MR: 500}.Fit()
	if p.H-p.MT-p.MB < minPageText-0.01 || p.W-p.ML-p.MR < minPageText-0.01 {
		t.Fatalf("%+v", p)
	}
}

func TestTurningAPageTwiceGivesItBack(t *testing.T) {
	p := PageSetup{W: 100, H: 200, MT: 1, MB: 2, ML: 3, MR: 4}
	if got := p.Turned().Turned(); got != p {
		t.Fatalf("%+v", got)
	}
}

func TestSectionBreakLetsPagesDiffer(t *testing.T) {
	d := blankDoc(t)
	d.Layout(time.Minute)
	d.Type("one")
	d.SectionBreak()
	d.Type("two")
	d.Layout(time.Minute)
	if n := len(d.Sections()); n != 2 {
		t.Fatalf("%d sections", n)
	}
	d.SetPageSetup(PageSetupOf(d.SectionAtCaret()).Turned(), false)
	d.Layout(time.Minute)
	pages := d.Pages()
	if len(pages) != 2 || pages[0].W > pages[0].H || pages[1].W < pages[1].H {
		t.Fatalf("%d pages, the first %v × %v", len(pages), pages[0].W, pages[0].H)
	}
	for d.Undo() {
	}
	d.Layout(time.Minute)
	if n := len(d.Sections()); n != 1 {
		t.Fatalf("undo left %d sections", n)
	}
}

func TestANewDocumentIsA4WithItsMargins(t *testing.T) {
	d := blankDoc(t)
	d.Layout(time.Minute)
	if got := PageSetupOf(d.SectionAtCaret()); got != (PageSetup{W: 595.3, H: 841.9, MT: 72, MB: 72, ML: 90, MR: 90}) {
		t.Fatalf("%+v", got)
	}
}

func TestAFileWithoutAPageSizeIsA4(t *testing.T) {
	d := blankDoc(t)
	s := d.readSection(nil, nil)
	if s.W != 595.3 || s.H != 841.9 || s.ML != 90 || s.MT != 72 {
		t.Fatalf("%+v", s)
	}
}
