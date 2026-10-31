package ui

import (
	"bytes"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/btelolabs/btelo-forge/internal/gfx"
	"github.com/btelolabs/btelo-forge/internal/office/opc"
	"github.com/btelolabs/btelo-forge/internal/office/pptx"
)

// slidesDoc opens a new presentation in the harness.
func slidesDoc(t *testing.T, x *harness) *slidesEditor {
	t.Helper()
	var f *officeFormat
	for _, k := range officeFormats {
		if k.id == "slides" {
			f = k
		}
	}
	od, err := x.a.newOffice(f)
	if err != nil {
		t.Fatal(err)
	}
	x.frame(Input{})
	x.frame(Input{})
	return od.ed.(*slidesEditor)
}

// typeWords types into the frame's keyboard input.
func (x *harness) typeWords(s string) { x.frame(Input{Typed: []rune(s)}) }

// slideMid is a point on the shape, in window pixels.
func slideMid(e *slidesEditor, sh *pptx.Shape) (float32, float32) {
	v := e.view
	return float32((sh.Box.X+sh.Box.W/2)*v.Scale + v.X), float32((sh.Box.Y+sh.Box.H/2)*v.Scale + v.Y)
}

func TestSlidesTyping(t *testing.T) {
	x := newHarness(t)
	e := slidesDoc(t, x)
	s := e.slide()
	title := e.d.Shapes(s)[0]
	mx, my := slideMid(e, title)
	x.click(gfx.Rect{X: mx, Y: my})
	if e.text == nil {
		t.Fatal("a click on an empty placeholder does not put the caret in it")
	}
	x.frame(Input{})
	if x.a.u.focus != slidesTextFocus {
		t.Fatalf("focus %q", x.a.u.focus)
	}
	x.typeWords("Hello")
	x.frame(Input{Keys: []Key{{Code: keyEnter}}})
	x.typeWords("world")
	x.frame(Input{})
	body, _ := e.d.Body(s, pptx.Target{ID: title.ID})
	if got := pptx.PlainText(body); got != "Hello\nworld" {
		t.Fatalf("typed %q", got)
	}
	// Shift+Left twice and bold: only those letters change.
	x.frame(Input{Keys: []Key{{Code: keyLeft, Shift: true}, {Code: keyLeft, Shift: true}}})
	x.frame(Input{Keys: []Key{{Code: 'B', Ctrl: true}}})
	x.frame(Input{})
	last := body.Elements("a:p")[1]
	if rs := last.Elements("a:r"); len(rs) != 2 || rs[1].Child("a:rPr").Attr("b") != "1" {
		t.Fatalf("runs not split: %d", len(rs))
	}
	if e.currentRun().Size != 60 {
		t.Fatalf("size %v", e.currentRun().Size)
	}
	// Undo takes the typing back as steps, not letters.
	x.frame(Input{Keys: []Key{{Code: keyEscape}}})
	if e.text != nil {
		t.Fatal("Esc left the caret in")
	}
	for i := 0; i < 8 && e.d.CanUndo(); i++ {
		e.undo()
	}
	x.frame(Input{})
	if e.d.Find(s, title.ID).HasText() {
		t.Fatal("undo did not clear the words")
	}
}

func TestSlidesDragResizeRotate(t *testing.T) {
	x := newHarness(t)
	e := slidesDoc(t, x)
	s := e.slide()
	id, err := e.d.AddShape(s, "rect", pptx.Box{X: 100, Y: 100, W: 200, H: 100})
	if err != nil {
		t.Fatal(err)
	}
	x.frame(Input{})
	sh := e.d.Find(s, id)
	mx, my := slideMid(e, sh)
	x.drag(mx, my, mx+float32(50*e.view.Scale), my+float32(30*e.view.Scale))
	got := e.d.Find(s, id).Box
	if got.X < 146 || got.X > 154 || got.Y < 126 || got.Y > 134 { // it may snap to the placeholders
		t.Fatalf("moved to %+v", got)
	}
	if err := e.d.Undo(); err != nil {
		t.Fatal(err)
	}
	if b := e.d.Find(s, id).Box; b.X != 100 {
		t.Fatalf("a drag is more than one undo: %+v", b)
	}
	x.frame(Input{})
	// The bottom right handle stretches, the top left stays.
	e.sel = []int{id}
	x.frame(Input{})
	pts, turn, _, ok := e.handlePoints()
	if !ok {
		t.Fatal("no handles")
	}
	x.drag(pts[4][0], pts[4][1], pts[4][0]+float32(100*e.view.Scale), pts[4][1]+float32(50*e.view.Scale))
	b := e.d.Find(s, id).Box
	if b.X != 100 || b.Y != 100 || b.W < 299 || b.W > 301 || b.H < 149 || b.H > 151 {
		t.Fatalf("resized to %+v", b)
	}
	x.frame(Input{})
	pts, turn, _, _ = e.handlePoints()
	cx, cy := (pts[0][0]+pts[4][0])/2, (pts[0][1]+pts[4][1])/2
	x.drag(turn[0], turn[1], cx+200, cy)
	if r := e.d.Find(s, id).Box.Rot; r < 89 || r > 91 {
		t.Fatalf("rotation %v", r)
	}
	// Arrow keys nudge, Delete deletes.
	x.frame(Input{Keys: []Key{{Code: keyRight}}})
	if e.d.Find(s, id).Box.X <= 100 {
		t.Fatal("no nudge")
	}
	x.frame(Input{Keys: []Key{{Code: keyDelete}}})
	if e.d.Find(s, id) != nil {
		t.Fatal("not deleted")
	}
}

func TestSlidesMarqueeGroupAndAlign(t *testing.T) {
	x := newHarness(t)
	e := slidesDoc(t, x)
	s := e.slide()
	if err := e.d.DeleteShapes(s, []int{2, 3}); err != nil {
		t.Fatal(err)
	}
	a, _ := e.d.AddShape(s, "rect", pptx.Box{X: 100, Y: 100, W: 50, H: 50})
	b, _ := e.d.AddShape(s, "ellipse", pptx.Box{X: 300, Y: 200, W: 50, H: 50})
	c, _ := e.d.AddShape(s, "diamond", pptx.Box{X: 700, Y: 300, W: 50, H: 50})
	x.frame(Input{})
	v := e.view
	x.drag(float32(50*v.Scale+v.X), float32(50*v.Scale+v.Y), float32(450*v.Scale+v.X), float32(300*v.Scale+v.Y))
	if len(e.sel) != 2 {
		t.Fatalf("marquee chose %v", e.sel)
	}
	x.frame(Input{Keys: []Key{{Code: 'G', Ctrl: true}}})
	if len(e.sel) != 1 || e.d.Find(s, e.sel[0]).Kind != pptx.KindGroup {
		t.Fatalf("not grouped: %v", e.sel)
	}
	x.frame(Input{Keys: []Key{{Code: 'G', Ctrl: true, Shift: true}}})
	if len(e.sel) != 2 {
		t.Fatalf("ungroup chose %v", e.sel)
	}
	e.sel = []int{a, b, c}
	e.distribute(true)
	if m := e.d.Find(s, b).Box.X; m < 399 || m > 401 {
		t.Fatalf("distributed to %v", m)
	}
	e.align("t")
	if y := e.d.Find(s, c).Box.Y; y != 100 {
		t.Fatalf("aligned to %v", y)
	}
	// Copy and paste, into another presentation with its picture.
	var pic bytes.Buffer
	cv := gfx.NewCanvas(20, 20)
	cv.Clear(color.RGBA{200, 10, 10, 255})
	png.Encode(&pic, cv.Img)
	e.sel = nil
	e.addPicture(pic.Bytes(), "red")
	x.frame(Input{Keys: []Key{{Code: 'C', Ctrl: true}}})
	x.h.pasteText = x.h.clip
	e2 := slidesDoc(t, x)
	x.frame(Input{Keys: []Key{{Code: 'V', Ctrl: true}}})
	if len(e2.sel) != 1 || e2.d.Find(e2.slide(), e2.sel[0]).Kind != pptx.KindPicture {
		t.Fatalf("pasted %v", e2.sel)
	}
	data, err := e2.d.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	pkg, _ := opc.Open(data)
	found := false
	for _, n := range pkg.Names() {
		found = found || strings.HasPrefix(n, "ppt/media/")
	}
	if !found {
		t.Fatal("the picture did not come along")
	}
}

func TestSlidesShow(t *testing.T) {
	x := newHarness(t)
	e := slidesDoc(t, x)
	l := e.d.Masters[0].Layouts[1]
	a, _ := e.d.AddSlide(l, 1)
	b, _ := e.d.AddSlide(l, 2)
	e.d.SetHidden(a, true)
	x.frame(Input{Keys: []Key{{Code: 0x74}}})
	if e.show == nil || e.show.at != 0 {
		t.Fatalf("F5 did not start from the first: %+v", e.show)
	}
	x.frame(Input{})
	// No tab strip shows: the top left of the window is the slide's white.
	if got := x.c.Img.RGBAAt(2, 60); got.R != 255 && got.R != 0 {
		t.Fatalf("not full window: %v", got)
	}
	x.frame(Input{Keys: []Key{{Code: keyRight}}})
	if e.show.at != e.d.Number(b)-1 {
		t.Fatalf("the hidden slide was not skipped: %d", e.show.at)
	}
	x.frame(Input{Keys: []Key{{Code: keyRight}}})
	if e.show.at != 3 {
		t.Fatalf("no end: %d", e.show.at)
	}
	x.frame(Input{Keys: []Key{{Code: keyEscape}}})
	if e.show != nil {
		t.Fatal("Esc did not leave")
	}
	if e.cur != 0 {
		t.Fatalf("back at %d", e.cur)
	}
	e.goTo(2)
	x.frame(Input{Keys: []Key{{Code: 0x74, Shift: true}}})
	if e.show == nil || e.show.at != 2 {
		t.Fatal("Shift+F5 did not start here")
	}
	x.click(gfx.Rect{X: 300, Y: 300, W: 10, H: 10})
	x.frame(Input{})
	if e.show == nil {
		t.Fatal("a click left the show instead of going on")
	}
	x.click(gfx.Rect{X: 300, Y: 300, W: 10, H: 10})
	x.frame(Input{})
	if e.show != nil {
		t.Fatal("a click at the end did not leave")
	}
}

func TestSlidesThumbnailsAndReorder(t *testing.T) {
	x := newHarness(t)
	e := slidesDoc(t, x)
	l := e.d.Masters[0].Layouts[1]
	e.d.AddSlide(l, 1)
	first := e.d.Slides[0]
	x.pump(10*time.Second, func() bool {
		t0, t1 := e.thumbs[e.d.Slides[0].Part], e.thumbs[e.d.Slides[1].Part]
		return t0 != nil && t1 != nil && t0.img != nil && t1.img != nil
	})
	// Dragging the first thumbnail past the second.
	r := gfx.Rect{Y: x.a.layout().body.Y + x.a.u.px(slidesRowH*2), W: x.a.u.px(slidesThumbsW), H: 700}
	w, h := e.thumbSize(r)
	y0 := r.Y + 14 + h/2
	y1 := r.Y + 14 + 2*(h+14)
	x.drag(r.X+40+w/2, y0, r.X+40+w/2, y1)
	x.frame(Input{})
	if e.d.Slides[1] != first {
		t.Fatalf("not reordered; drop at %d", e.dragTo)
	}
}

func TestSlidesFile(t *testing.T) {
	x := newHarness(t)
	e := slidesDoc(t, x)
	if err := e.setWords(pptx.Target{ID: 2}, "Saved words"); err != nil {
		t.Fatal(err)
	}
	e.od.dirty = true
	if err := x.a.saveOffice(e.od, true); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(e.od.path)
	if err != nil {
		t.Fatal(err)
	}
	d, err := pptx.Open(data)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := d.Body(d.Slides[0], pptx.Target{ID: 2})
	if pptx.PlainText(body) != "Saved words" {
		t.Fatalf("saved %q", pptx.PlainText(body))
	}
}

func TestMCPBuildsADeck(t *testing.T) {
	x := newHarness(t)
	sess := connectMCPAt(t, x, "/office")
	var st slidesState
	sess.ok("slides_new", map[string]any{}, &st)
	if len(st.Slides) != 1 || len(st.Layouts) != 6 {
		t.Fatalf("slides_new: %+v", st)
	}
	var added slideView
	sess.ok("slides_add_slide", map[string]any{"layout": "Title and Content"}, &added)
	if added.Number != 2 || len(added.Shapes) != 2 {
		t.Fatalf("add: %+v", added)
	}
	var made struct {
		ID int `json:"id"`
	}
	sess.ok("slides_add_shape", map[string]any{"slide": 2, "shape": "roundRect", "box": []float64{500, 300, 200, 100}, "text": "Hello", "fill": "#208040", "size": 24}, &made)
	if made.ID == 0 {
		t.Fatal("no id")
	}
	sess.ok("slides_set_text", map[string]any{"slide": 2, "id": added.Shapes[0].ID, "text": "A title"}, nil)
	sess.ok("slides_update_shape", map[string]any{"slide": 2, "id": made.ID, "rotation": 15, "opacity": 0.5, "bold": true, "align": "right"}, nil)
	var table struct {
		ID int `json:"id"`
	}
	sess.ok("slides_add_table", map[string]any{"slide": 2, "cells": [][]string{{"a", "b"}, {"1", "2"}}, "box": []float64{60, 300, 300, 80}}, &table)
	dir := x.mediaDir()
	file := filepath.Join(dir, "p.png")
	cv := gfx.NewCanvas(30, 30)
	cv.Clear(color.RGBA{20, 20, 240, 255})
	f, _ := os.Create(file)
	png.Encode(f, cv.Img)
	f.Close()
	sess.ok("slides_add_picture", map[string]any{"slide": 2, "file": file, "box": []float64{800, 40, 100, 100}}, nil)
	var notes struct {
		Notes string `json:"notes"`
	}
	sess.ok("slides_notes", map[string]any{"slide": 2, "text": "Say this"}, &notes)
	if notes.Notes != "Say this" {
		t.Fatalf("notes %+v", notes)
	}
	res := sess.call("slides_render", map[string]any{"slide": 2, "width": 960})
	if res.IsError || len(res.Content) != 1 {
		t.Fatalf("render: %+v", res)
	}
	sess.ok("slides_add_slide", map[string]any{"layout": "Blank", "at": 1}, nil)
	sess.ok("slides_move_slide", map[string]any{"from": 1, "to": 3}, nil)
	sess.ok("slides_delete_slide", map[string]any{"slide": 3}, nil)
	sess.ok("slides_state", map[string]any{}, &st)
	if len(st.Slides) != 2 || st.Slides[1].Notes != "Say this" || len(st.Slides[1].Shapes) != 5 {
		t.Fatalf("state: %+v", st.Slides)
	}
	if st.Slides[1].Shapes[0].Text != "A title" {
		t.Fatalf("title %q", st.Slides[1].Shapes[0].Text)
	}
	sess.ok("slides_save", map[string]any{}, nil)
	data, err := os.ReadFile(st.Path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pptx.Open(data); err != nil {
		t.Fatal(err)
	}
}

// TestSlidesScreens renders the tab into FORGE_SHOTS.
func TestSlidesScreens(t *testing.T) {
	dir := os.Getenv("FORGE_SHOTS")
	if dir == "" {
		t.Skip("set FORGE_SHOTS to a folder to render the screens")
	}
	x := newHarness(t)
	var e *slidesEditor
	for _, lang := range []Language{English, Chinese} {
		x.a.u.lang = lang
		prefix := "ui-" + string(lang) + "-"
		e = slidesDoc(t, x)
		e.setWords(pptx.Target{ID: 2}, "Quarterly review 季度回顾")
		s2, _ := e.d.AddSlide(e.d.Masters[0].Layouts[1], 1)
		e.goTo(1)
		x.frame(Input{})
		shapes := e.d.Shapes(s2)
		e.setWords(pptx.Target{ID: shapes[0].ID}, "What changed")
		e.setWords(pptx.Target{ID: shapes[1].ID}, "Faster start\nFewer steps\n中文段落也可以换行")
		box := pptx.Box{X: 560, Y: 150, W: 300, H: 180, Rot: 12}
		id, _ := e.d.AddShape(s2, "roundRect", box)
		e.setWords(pptx.Target{ID: id}, "A turned shape")
		e.sel = []int{id}
		e.d.SetNotes(s2, "Mention the numbers here.", "")
		e.notesFor = ""
		x.pump(5*time.Second, func() bool {
			t0 := e.thumbs[s2.Part]
			return t0 != nil && t0.img != nil && t0.rev == s2.Rev
		})
		x.frame(Input{})
		x.shot(prefix + "editor")
		e.sel = nil
		x.frame(Input{})
		k := e.d.Shapes(s2)[1]
		mx, my := slideMid(e, k)
		x.click(gfx.Rect{X: mx, Y: my})
		x.frame(Input{})
		x.click(gfx.Rect{X: mx, Y: my})
		x.frame(Input{})
		x.frame(Input{Keys: []Key{{Code: 'A', Ctrl: true}}})
		x.frame(Input{})
		x.frame(Input{})
		x.shot(prefix + "text")
		x.frame(Input{Keys: []Key{{Code: keyEscape}}})
		x.frame(Input{})
		for _, p := range []struct {
			name string
			pop  *slidesPopup
		}{{"colors", &slidesPopup{kind: popupColor, none: true, anchor: gfx.Rect{X: 600, Y: 100}}},
			{"layouts", &slidesPopup{kind: popupLayouts, anchor: gfx.Rect{X: 200, Y: 80}}},
			{"shapes", &slidesPopup{kind: popupShapes, anchor: gfx.Rect{X: 300, Y: 80}}},
			{"table", &slidesPopup{kind: popupTable, anchor: gfx.Rect{X: 340, Y: 80}}}} {
			e.popup = p.pop
			x.mx, x.my = 800, 700
			x.frame(Input{})
			x.frame(Input{})
			x.shot(prefix + p.name)
			e.popup = nil
		}
		e.startShow(true)
		x.frame(Input{})
		x.shot(prefix + "show")
		e.show.leave()
		x.frame(Input{})
	}
}
