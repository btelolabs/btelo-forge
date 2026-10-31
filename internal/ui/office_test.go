package ui

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestOfficeCardsGoInTheOrderPeopleKnow(t *testing.T) {
	var got []string
	for _, f := range officeFormats {
		got = append(got, f.id)
	}
	if !slices.Equal(got, officeOrder) {
		t.Fatal(got)
	}
}

func TestSavingToAFolderGoesOnInThatFile(t *testing.T) {
	x := newHarness(t)
	od, err := x.a.newOffice(officeFormatOf("a.docx"))
	if err != nil {
		t.Fatal(err)
	}
	from := od.path
	dir := t.TempDir()
	if err := x.a.saveOfficeTo(od, dir); err != nil {
		t.Fatal(err)
	}
	if od.path != filepath.Join(dir, filepath.Base(from)) {
		t.Fatal(od.path)
	}
	if _, err := os.Stat(od.path); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(from); err != nil {
		t.Fatal("the file it was in went away")
	}
	other, err := x.a.newOffice(officeFormatOf("a.docx"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, filepath.Base(other.path)), []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := x.a.saveOfficeTo(other, dir); err != errNameTaken {
		t.Fatal(err)
	}
}

func TestLegacyFormatsSayWhatToDo(t *testing.T) {
	x := newHarness(t)
	x.a.openOfficeFile(`old.doc`)
	if len(x.a.toasts) == 0 {
		t.Fatal("nothing was said")
	}
}

// Words being composed in the input method are shown as large as the words around them, at any zoom.
func TestInputMethodFollowsTheDocumentsWords(t *testing.T) {
	x := newHarness(t)
	e := wordOpen(t, x)
	x.frame(Input{})
	x.frame(Input{})
	u := &x.a.u
	if e.scale <= 0 || u.imeFont != e.d.CaretRun().Size*e.scale {
		t.Fatalf("at the first zoom the words are %v, the input method's %v", e.d.CaretRun().Size*e.scale, u.imeFont)
	}
	before := u.imeFont
	e.setZoom(e.zoom * 2)
	x.frame(Input{})
	x.frame(Input{})
	if u.imeFont < before*1.9 {
		t.Fatalf("zooming in took the input method from %v to %v", before, u.imeFont)
	}
	if u.imeAt[2] <= 0 || u.imeAt[2] < u.imeFont {
		t.Fatalf("its line %v is less tall than its words %v", u.imeAt[2], u.imeFont)
	}
}
