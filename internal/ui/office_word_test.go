package ui

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/btelolabs/btelo-forge/internal/gfx"
	"github.com/btelolabs/btelo-forge/internal/office/docx"
)

// wordOpen makes a new Word tab and draws it.
func wordOpen(t *testing.T, x *harness) *wordEditor {
	t.Helper()
	od, err := x.a.newOffice(wordFormat(t))
	if err != nil {
		t.Fatal(err)
	}
	x.settle(5 * time.Second)
	x.frame(Input{})
	return wordEditorOf(od)
}

func wordFormat(t *testing.T) *officeFormat {
	t.Helper()
	for _, f := range officeFormats {
		if f.id == "word" {
			return f
		}
	}
	t.Fatal("no word format")
	return nil
}

// typeIn types through the input path, as the window does.
func (x *harness) typeIn(s string) {
	x.frame(Input{Typed: []rune(s)})
	x.frame(Input{})
}

func (x *harness) tapKey(code uint16, ctrl, shift bool) {
	x.frame(Input{Keys: []Key{{Code: code, Ctrl: ctrl, Shift: shift}}, Ctrl: ctrl, Shift: shift})
	x.frame(Input{})
}

func TestWordTypeThroughWindow(t *testing.T) {
	x := newHarness(t)
	e := wordOpen(t, x)
	if x.a.u.focus != wordFocus {
		t.Fatalf("focus %q", x.a.u.focus)
	}
	x.typeIn("Hello")
	x.tapKey(keyEnter, false, false)
	x.typeIn("world")
	ps := e.d.Body.Paragraphs()
	if len(ps) != 2 || ps[0].Text() != "Hello" || ps[1].Text() != "world" {
		t.Fatalf("paragraphs: %d", len(ps))
	}
	x.tapKey('Z', true, false)
	x.tapKey('Z', true, false)
	if got := e.d.Body.Paragraphs()[0].Text(); got != "Hello" || len(e.d.Body.Paragraphs()) != 1 {
		t.Fatalf("undo left %q and %d", got, len(e.d.Body.Paragraphs()))
	}
	x.tapKey('B', true, false)
	x.typeIn(" bold")
	if !strings.Contains(string(e.d.ReadPart("word/document.xml")), `<w:b/>`) {
		t.Fatal("Ctrl+B did not reach the file")
	}
	x.tapKey(keyHome, false, false)
	x.tapKey(keyRight, false, true)
	x.tapKey('X', true, false)
	if x.h.clip != "H" {
		t.Fatalf("cut %q", x.h.clip)
	}
	x.h.pasteText = "H"
	x.tapKey(keyEnd, false, false)
	x.tapKey('V', true, false)
	if got := e.d.Body.Paragraphs()[0].Text(); got != "ello boldH" {
		t.Fatalf("after paste %q", got)
	}
}

func TestWordClicksChoose(t *testing.T) {
	x := newHarness(t)
	e := wordOpen(t, x)
	e.d.Insert("alpha beta gamma")
	x.frame(Input{})
	x.frame(Input{})
	s, ok := e.d.Locate(docx.Pos{B: e.d.Body.Paragraphs()[0], Off: 7})
	if !ok {
		t.Fatal("not located")
	}
	px, py := e.pageXY(s.Page)
	wx, wy := px+s.X*e.scale, py+(s.Y+s.H/2)*e.scale
	x.mx, x.my = wx, wy
	x.frame(Input{})
	x.frame(Input{Down: true, Pointer: []Pointer{{X: wx, Y: wy, Down: true}}})
	x.frame(Input{Pointer: []Pointer{{X: wx, Y: wy}}})
	x.frame(Input{Down: true, Pointer: []Pointer{{X: wx, Y: wy, Down: true, Double: true}}})
	x.frame(Input{Pointer: []Pointer{{X: wx, Y: wy}}})
	if got := e.d.SelectedText(); got != "beta" {
		t.Fatalf("double click chose %q", got)
	}
	x.frame(Input{Down: true, Pointer: []Pointer{{X: wx, Y: wy, Down: true}}})
	x.frame(Input{Pointer: []Pointer{{X: wx, Y: wy}}})
	if got := e.d.SelectedText(); got != "alpha beta gamma" {
		t.Fatalf("triple click chose %q", got)
	}
}

func wordSample(e *wordEditor) {
	d := e.d
	d.Insert("文档标题\nThe quick brown fox 敏捷的棕色狐狸\n第一章 引言\n")
	ps := d.Body.Paragraphs()
	d.Sel = docx.Selection{Anchor: docx.Pos{B: ps[0]}, Caret: docx.Pos{B: ps[0], Off: 2}}
	d.SetStyle("Title")
	ps = d.Body.Paragraphs()
	d.Sel = docx.Selection{Anchor: docx.Pos{B: ps[2]}, Caret: docx.Pos{B: ps[2], Off: 1}}
	d.SetStyle("Heading1")
	last := d.Body.Paragraphs()[3]
	d.SetCaret(docx.Pos{B: last}, false)
	d.Insert(strings.Repeat("这是一段用来检查中文换行和两端对齐的正文，混合 English words 和数字 12345。", 6) + "\n")
	yes := true
	d.Insert("加粗的文字")
	b := d.Sel.Caret.B
	d.Sel = docx.Selection{Anchor: docx.Pos{B: b}, Caret: docx.Pos{B: b, Off: 5}}
	col := "C00000"
	d.FormatRuns(docx.RunChange{Bold: &yes, Color: &col})
	d.SetCaret(docx.Pos{B: b, Off: 5}, false)
	d.Enter()
	d.Insert("项目一\n项目二\n项目三")
	end := d.Sel.Caret
	ps = d.Body.Paragraphs()
	d.Sel = docx.Selection{Anchor: docx.Pos{B: ps[end.B.Flat-2]}, Caret: end}
	d.ToggleList(docx.ListBullet)
	d.SetCaret(docx.Pos{B: d.Body.Paragraphs()[end.B.Flat], Off: 3}, false)
	d.Enter()
	d.Enter()
	d.InsertTable(3, 3)
	d.Finish()
	for _, s := range []string{"姓名", "部门", "电话", "张三", "研发", "1234"} {
		d.Type(s)
		d.NextCell(false)
	}
	d.Finish()
}

func TestWordScreens(t *testing.T) {
	dir := os.Getenv("FORGE_SHOTS")
	if dir == "" {
		t.Skip("set FORGE_SHOTS to a folder to render the screens")
	}
	x := newHarness(t)
	e := wordOpen(t, x)
	x.shot("word-blank")
	wordSample(e)
	x.settle(5 * time.Second)
	x.frame(Input{})
	x.shot("word-sample-en")
	x.a.u.lang = Chinese
	x.frame(Input{})
	x.frame(Input{})
	x.shot("word-sample-zh")
	e.outline = true
	e.openFind(true)
	e.find.query = "项目"
	x.frame(Input{})
	x.frame(Input{})
	x.frame(Input{})
	x.shot("word-find-outline-zh")
	e.closeFind()
	e.outline = false
	x.frame(Input{})
	e.enterHeader(0, "header")
	x.frame(Input{})
	x.typeIn("页眉文字")
	x.frame(Input{})
	x.shot("word-header-zh")
	e.d.InsertField("PAGE", "1")
	x.tapKey(keyEscape, false, false)
	x.settle(5 * time.Second)
	x.frame(Input{})
	x.shot("word-after-header-zh")
	e.openPop("table", gfx.Rect{X: 930, Y: 44, W: 30, H: 30})
	e.tableR, e.tableC = 3, 4
	x.frame(Input{})
	x.shot("word-table-pop-zh")
	e.openPop("color", gfx.Rect{X: 530, Y: 44, W: 30, H: 30})
	x.frame(Input{})
	x.shot("word-color-pop-zh")
	e.pop = ""
	e.d.MoveDoc(false, false)
	e.d.SelectParagraph(e.d.Sel.Caret)
	e.d.Finish()
	x.a.openMenu("office/word/lines", gfx.Rect{X: 830, Y: 44, W: 30, H: 30}, nil)
	x.a.menu = nil
	e.openLink()
	x.frame(Input{})
	x.frame(Input{})
	x.shot("word-link-zh")
	e.link = nil
	x.a.u.setFocus(wordFocus)
	x.a.u.lang = English
	e.zoom = 1.5
	x.frame(Input{})
	x.frame(Input{})
	x.shot("word-zoom-en")
	x.c = gfx.NewCanvas(760, 620)
	e.outline = true
	e.zoom = 1
	x.frame(Input{})
	x.frame(Input{})
	x.shot("word-narrow-en")
	img, err := e.renderWord(0, 1.5)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "word-render.png"), img, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestWordFindBar(t *testing.T) {
	x := newHarness(t)
	e := wordOpen(t, x)
	e.d.Insert("one two one\nthree one")
	x.frame(Input{})
	x.tapKey('H', true, false)
	x.frame(Input{})
	if e.find == nil || !e.find.replace {
		t.Fatal("Ctrl+H did not open the bar")
	}
	x.typeIn("one")
	x.frame(Input{})
	if n := len(e.find.hits); n != 3 {
		t.Fatalf("hits %d", n)
	}
	if got := e.d.SelectedText(); got != "one" {
		t.Fatalf("selected %q", got)
	}
	x.tapKey(keyEnter, false, false)
	x.tapKey(keyEnter, false, false)
	if e.find.at != 2 || x.a.u.focus != "office/word/findbox" {
		t.Fatalf("at %d focus %q", e.find.at, x.a.u.focus)
	}
	e.find.with = "1"
	e.replaceOne()
	x.frame(Input{})
	if got := e.d.Body.Paragraphs()[1].Text(); got != "three 1" {
		t.Fatalf("replaced %q", got)
	}
	x.tapKey(keyEscape, false, false)
	x.frame(Input{})
	if e.find != nil || x.a.u.focus != wordFocus {
		t.Fatalf("bar %v focus %q", e.find, x.a.u.focus)
	}
}

func TestWordDragChooses(t *testing.T) {
	x := newHarness(t)
	e := wordOpen(t, x)
	e.d.Insert("alpha beta gamma\nsecond line here")
	x.frame(Input{})
	x.frame(Input{})
	at := func(para, off int) (float32, float32) {
		s, ok := e.d.Locate(docx.Pos{B: e.d.Body.Paragraphs()[para], Off: off})
		if !ok {
			t.Fatal("not located")
		}
		px, py := e.pageXY(s.Page)
		return px + s.X*e.scale, py + (s.Y+s.H/2)*e.scale
	}
	x0, y0 := at(0, 6)
	x1, y1 := at(1, 6)
	x.drag(x0, y0, x1, y1)
	if got := e.d.SelectedText(); got != "beta gamma\nsecond" {
		t.Fatalf("chose %q", got)
	}
	x.tapKey(keyBack, false, false)
	if got := e.d.Body.Paragraphs()[0].Text(); got != "alpha  line here" {
		t.Fatalf("left %q", got)
	}
}

func TestWordOpensFromDrop(t *testing.T) {
	x := newHarness(t)
	data, err := docx.Blank()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(x.dir, "dropped.docx")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	x.frame(Input{Dropped: []string{path}})
	x.settle(10 * time.Second)
	if e := wordEditorOf(x.a.office); e == nil {
		t.Fatal("the document did not open")
	}
	pic := filepath.Join(x.dir, "p.png")
	var b bytes.Buffer
	png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 8, 8)))
	os.WriteFile(pic, b.Bytes(), 0o644)
	x.frame(Input{Dropped: []string{pic}})
	x.settle(10 * time.Second)
	if got := wordEditorOf(x.a.office).d.Body.Paragraphs()[0].Text(); got != "￼" {
		t.Fatalf("after dropping a picture %q", got)
	}
}

func TestWordMCP(t *testing.T) {
	x := newHarness(t)
	s := connectMCPAt(t, x, mcpDomainPaths[domainOffice])
	pic := filepath.Join(x.dir, "pic.png")
	var b bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 40, 30))
	for i := range img.Pix {
		img.Pix[i] = 200
	}
	img.Set(5, 5, color.RGBA{255, 0, 0, 255})
	png.Encode(&b, img)
	if err := os.WriteFile(pic, b.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	text := func(res *mcp.CallToolResult) string {
		t.Helper()
		if res.IsError {
			t.Fatalf("tool error: %+v", res.Content[0])
		}
		return res.Content[0].(*mcp.TextContent).Text
	}
	var st wordStateView
	text(s.call("word_new", map[string]any{}))
	if err := json.Unmarshal([]byte(text(s.call("word_insert", map[string]any{"text": "Report\nFirst paragraph\nSecond one", "style": "Heading1"}))), &st); err != nil {
		t.Fatal(err)
	}
	if len(st.Paragraphs) != 3 || st.Paragraphs[0].Style != "Heading1" {
		t.Fatalf("state: %+v", st.Paragraphs)
	}
	text(s.call("word_format", map[string]any{"from": 1, "text": "First", "bold": true, "color": "#ff0000"}))
	text(s.call("word_format", map[string]any{"from": 1, "to": 2, "align": "center", "style": "Normal"}))
	json.Unmarshal([]byte(text(s.call("word_table", map[string]any{"op": "new", "cells": [][]string{{"a", "b"}, {"c", "d"}}}))), &st)
	if len(st.Tables) != 1 || st.Tables[0].Rows != 2 || st.Tables[0].Cols != 2 {
		t.Fatalf("tables: %+v", st.Tables)
	}
	json.Unmarshal([]byte(text(s.call("word_table", map[string]any{"op": "add_row", "table": 0, "row": 1}))), &st)
	if st.Tables[0].Rows != 3 {
		t.Fatalf("rows: %+v", st.Tables)
	}
	text(s.call("word_table", map[string]any{"op": "merge", "table": 0, "row": 0, "col": 0, "to_row": 0, "to_col": 1}))
	text(s.call("word_picture", map[string]any{"file": pic, "width": 120}))
	rep := text(s.call("word_replace", map[string]any{"find": "Second", "with": "Other"}))
	if !strings.Contains(rep, `"replaced": 1`) {
		t.Fatal(rep)
	}
	read := text(s.call("word_read", map[string]any{}))
	if !strings.Contains(read, "Other one") || !strings.Contains(read, "[picture]") {
		t.Fatal(read)
	}
	res := s.call("word_render", map[string]any{"page": 1})
	if res.IsError {
		t.Fatalf("render: %+v", res.Content[0])
	}
	shot := res.Content[0].(*mcp.ImageContent)
	got, err := png.Decode(bytes.NewReader(shot.Data))
	if err != nil {
		t.Fatal(err)
	}
	if got.Bounds().Dx() < 800 {
		t.Fatalf("render %v", got.Bounds())
	}
	out := filepath.Join(x.dir, "saved.docx")
	text(s.call("word_save", map[string]any{"to": out}))
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	d, err := docx.Open(data, x.a.officeBook())
	if err != nil {
		t.Fatal(err)
	}
	if d.Finish(); len(d.Pages()) != 1 {
		t.Fatalf("pages %d", len(d.Pages()))
	}
}
