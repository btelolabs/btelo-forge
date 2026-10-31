package ui

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/btelolabs/btelo-forge/internal/gfx"
	"github.com/btelolabs/btelo-forge/internal/office/pptx"
)

func redPNG() []byte {
	var b bytes.Buffer
	c := gfx.NewCanvas(40, 20)
	c.Clear(color.RGBA{220, 30, 30, 255})
	png.Encode(&b, c.Img)
	return b.Bytes()
}

func TestSlidesTextBoxIsPlacedAndEmptyOnesGo(t *testing.T) {
	x := newHarness(t)
	e := slidesDoc(t, x)
	s := e.slide()
	n := len(e.d.Shapes(s))
	v := e.view
	place := func() {
		e.placing = pptx.ShapeTextBox
		x.drag(float32(100*v.Scale+v.X), float32(400*v.Scale+v.Y), float32(400*v.Scale+v.X), float32(440*v.Scale+v.Y))
		x.frame(Input{})
	}
	place()
	if e.text == nil || len(e.d.Shapes(s)) != n+1 {
		t.Fatalf("no text box; text %v shapes %d", e.text, len(e.d.Shapes(s)))
	}
	x.frame(Input{Keys: []Key{{Code: keyEscape}}})
	if len(e.d.Shapes(s)) != n {
		t.Fatal("a text box left empty stayed")
	}
	place()
	x.typeWords("Some words that go on long enough to need a second line in a box this narrow, and then a third line too, more or less")
	x.frame(Input{})
	id := e.text.t.ID
	x.frame(Input{Keys: []Key{{Code: keyEscape}}})
	k := e.d.Find(s, id)
	if k == nil || k.Box.H < 60 {
		t.Fatalf("the box did not grow to its words: %+v", k)
	}
}

func TestSlidesPictures(t *testing.T) {
	x := newHarness(t)
	e := slidesDoc(t, x)
	s := e.slide()
	dir := x.mediaDir()
	file := filepath.Join(dir, "a.png")
	os.WriteFile(file, redPNG(), 0o644)
	x.frame(Input{Dropped: []string{file}})
	x.pump(5*time.Second, func() bool { return len(e.sel) == 1 })
	if k := e.d.Find(s, e.sel[0]); k == nil || k.Kind != pptx.KindPicture {
		t.Fatalf("drop gave %+v", k)
	}
	// A picture on the clipboard from elsewhere.
	x.a.u.focus = ""
	e.sel = nil
	img, _, err := image.Decode(bytes.NewReader(redPNG()))
	if err != nil {
		t.Fatal(err)
	}
	x.h.pasteImage = img
	x.frame(Input{Keys: []Key{{Code: 'V', Ctrl: true}}})
	x.pump(5*time.Second, func() bool { return len(e.sel) == 1 })
	x.h.pasteImage = nil
	// Words on the clipboard from elsewhere.
	e.sel = nil
	x.h.pasteText = "Pasted words"
	x.frame(Input{Keys: []Key{{Code: 'V', Ctrl: true}}})
	if len(e.sel) != 1 {
		t.Fatal("words made no box")
	}
	if got := e.shapeWords(e.d.Find(s, e.sel[0])); got != "Pasted words" {
		t.Fatalf("pasted %q", got)
	}
}

func TestSlidesTableCellsAndTurnedWords(t *testing.T) {
	x := newHarness(t)
	e := slidesDoc(t, x)
	s := e.slide()
	id, err := e.d.AddTable(s, 2, 2, pptx.Box{X: 100, Y: 300, W: 400, H: 80}, [][]string{{"a", "b"}, {"c", "d"}})
	if err != nil {
		t.Fatal(err)
	}
	x.frame(Input{})
	v := e.view
	px, py := float32(150*v.Scale+v.X), float32(320*v.Scale+v.Y)
	x.click(gfx.Rect{X: px, Y: py})
	x.frame(Input{Down: true, Pointer: []Pointer{{X: px, Y: py, Down: true, Double: true}}})
	x.frame(Input{Pointer: []Pointer{{X: px, Y: py}}})
	if e.text == nil || !e.text.t.Cell || e.text.t.Row != 0 || e.text.t.Col != 0 {
		t.Fatalf("no cell: %+v", e.text)
	}
	x.frame(Input{Keys: []Key{{Code: keyTab}}})
	if e.text.t.Col != 1 {
		t.Fatalf("Tab went to %+v", e.text.t)
	}
	x.typeWords("X and a lot more words so that the row has to grow, on and on and on and on for several lines in the cell")
	x.frame(Input{})
	body, _ := e.d.Body(s, e.text.t)
	if txt := pptx.PlainText(body); txt[0] != 'X' {
		t.Fatalf("typed %q (the Tab chose the cell's words)", txt)
	}
	x.frame(Input{Keys: []Key{{Code: keyEscape}}})
	if k := e.d.Find(s, id); k.Box.H <= 80 {
		t.Fatalf("the frame did not grow: %+v", k.Box)
	}
	// A caret in turned words lands by the turned layout.
	sh, _ := e.d.AddShape(s, "rect", pptx.Box{X: 500, Y: 100, W: 200, H: 100, Rot: 90})
	e.setWords(pptx.Target{ID: sh}, "abcdefghij")
	e.sel = []int{sh}
	x.frame(Input{})
	k := e.d.Find(s, sh)
	mx, my := slideMid(e, k)
	x.click(gfx.Rect{X: mx, Y: my})
	x.frame(Input{})
	if e.text == nil {
		t.Fatal("the second click did not start typing")
	}
	if o := e.text.caret.Off; o < 3 || o > 7 {
		t.Fatalf("a click in the middle of the turned words went to %d", o)
	}
}

func TestSlidesToolbarSetsRunsAndPopupsWork(t *testing.T) {
	x := newHarness(t)
	e := slidesDoc(t, x)
	s := e.slide()
	id, _ := e.d.AddShape(s, "rect", pptx.Box{X: 100, Y: 300, W: 200, H: 80})
	e.setWords(pptx.Target{ID: id}, "Words")
	e.sel = []int{id}
	x.frame(Input{})
	picked := color.RGBA{}
	e.openPopup(&slidesPopup{kind: popupColor, anchor: gfx.Rect{X: 600, Y: 100}, none: true, pick: func(c color.RGBA) {
		picked = c
		e.setRun(pptx.SetColor(c))
	}})
	x.frame(Input{})
	box := e.popupRect(e.popup, 0, 0)
	// A standard colour: the pick runs and the panel goes.
	x.click(gfx.Rect{X: box.X + 12, Y: box.Y + 200, W: 8, H: 8})
	x.frame(Input{})
	if e.popup != nil || picked.A == 0 {
		t.Fatalf("popup %v picked %v", e.popup, picked)
	}
	if got := e.currentRun().Color; got != picked {
		t.Fatalf("colour %v, picked %v", got, picked)
	}
	// A click outside closes it, and does nothing to the slide.
	e.openPopup(&slidesPopup{kind: popupTable, anchor: gfx.Rect{X: 600, Y: 100}})
	x.frame(Input{})
	x.click(gfx.Rect{X: 800, Y: 800, W: 4, H: 4})
	if e.popup != nil || len(e.sel) != 1 {
		t.Fatalf("popup %v selection %v", e.popup, e.sel)
	}
	e.setRun(pptx.SetBool("b", true))
	if !e.currentRun().Bold {
		t.Fatal("not bold")
	}
	e.setPara(pptx.SetAlign("r"))
	if e.currentPara().Align != "r" {
		t.Fatal("not aligned")
	}
	e.reorder(pptx.ToBack)
	if e.top()[0].ID != id {
		t.Fatal("not sent back")
	}
}
