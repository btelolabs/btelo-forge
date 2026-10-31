package ui

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/btelolabs/btelo-forge/internal/gfx"
	"github.com/btelolabs/btelo-forge/internal/office/docx"
)

// The page setup panel: the paper, its orientation and margins of the whole document or one section, and the page and
// section breaks that decide where pages begin. A preset applies at once; typed sizes apply with Enter or Apply.

// wordPaper is a paper size, in millimetres, upright.
type wordPaper struct {
	name string
	w, h float64
}

var wordPapers = []wordPaper{{"A3", 297, 420}, {"A4", 210, 297}, {"A5", 148, 210}, {"B5", 182, 257}, {"16K", 184, 260},
	{"Letter", 215.9, 279.4}, {"Legal", 215.9, 355.6}}

// on is the setup with this paper, keeping its orientation.
func (w wordPaper) on(p docx.PageSetup) docx.PageSetup {
	long, short := wordPoints(w.h), wordPoints(w.w)
	if p.Landscape() {
		p.W, p.H = long, short
	} else {
		p.W, p.H = short, long
	}
	return p
}

// wordMargins are the margin presets, in millimetres: top and bottom, left and right; named by wordMarginNames.
var wordMargins = [][2]float64{{25.4, 31.8}, {12.7, 12.7}, {25.4, 19.1}, {25.4, 50.8}}

var wordMarginNames = msgs("Standard", "Narrow", "Moderate", "Wide")

func wordPoints(mm float64) float32 { return float32(mm * 72 / 25.4) }

func wordMM(pt float32) float64 { return math.Round(float64(pt)*25.4/72*10) / 10 }

// wordPagePanel is what is being typed into the panel.
type wordPagePanel struct {
	fields  [6]string // width, height, top, bottom, left, right, in millimetres
	section bool      // only the caret's section
}

// currentPage is the setup of the section at the caret.
func (e *wordEditor) currentPage() (docx.PageSetup, bool) {
	s := e.d.SectionAtCaret()
	if s == nil {
		return docx.PageSetup{}, false
	}
	return docx.PageSetupOf(s), true
}

// loadPagePanel fills the boxes from the document.
func (e *wordEditor) loadPagePanel() {
	p, ok := e.currentPage()
	if !ok {
		return
	}
	for i, v := range []float32{p.W, p.H, p.MT, p.MB, p.ML, p.MR} {
		e.page.fields[i] = strconv.FormatFloat(wordMM(v), 'f', -1, 64)
	}
}

// applyPage changes the document, and the boxes follow.
func (e *wordEditor) applyPage(p docx.PageSetup) {
	e.d.SetPageSetup(p, !e.page.section)
	e.loadPagePanel()
}

// typedPage is the setup the boxes hold; ok is false while one of them is not a size.
func (e *wordEditor) typedPage() (docx.PageSetup, bool) {
	var v [6]float32
	for i, s := range e.page.fields {
		n, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
		if err != nil || n < 0 || i < 2 && n <= 0 {
			return docx.PageSetup{}, false
		}
		v[i] = wordPoints(n)
	}
	return docx.PageSetup{W: v[0], H: v[1], MT: v[2], MB: v[3], ML: v[4], MR: v[5]}, true
}

// pagePanelBox is where the panel goes.
func (e *wordEditor) pagePanelBox() gfx.Rect {
	u := &e.a.u
	h := float32(382)
	if len(e.d.Sections()) > 1 {
		h += 36
	}
	return gfx.Rect{X: e.popAt.X, Y: e.popAt.Bottom() + u.px(2), W: u.px(400), H: u.px(h)}
}

// paperIndex is which paper the page is, either way up, -1 for a size of its own.
func paperIndex(p docx.PageSetup) int {
	long, short := wordMM(max(p.W, p.H)), wordMM(min(p.W, p.H))
	for i, w := range wordPapers {
		if math.Abs(short-w.w) < 0.6 && math.Abs(long-w.h) < 0.6 {
			return i
		}
	}
	return -1
}

// marginIndex is which preset the margins are, -1 for none.
func marginIndex(p docx.PageSetup) int {
	near := func(a float32, b float64) bool { return math.Abs(wordMM(a)-b) < 0.3 }
	for i, m := range wordMargins {
		if near(p.MT, m[0]) && near(p.MB, m[0]) && near(p.ML, m[1]) && near(p.MR, m[1]) {
			return i
		}
	}
	return -1
}

// drawPagePanel draws the panel.
func (e *wordEditor) drawPagePanel(box gfx.Rect) {
	u := &e.a.u
	p, ok := e.currentPage()
	if !ok {
		return
	}
	pad, rowH := u.px(12), u.px(28)
	x, w := box.X+pad, box.W-2*pad
	y := box.Y + u.px(8)
	heading := func(s string) {
		u.label(u.face(12), gfx.Rect{X: x, Y: y, W: w, H: u.px(20)}, s, colDim, 0)
		y += u.px(21)
	}
	row := func() gfx.Rect {
		r := gfx.Rect{X: x, Y: y, W: w, H: rowH}
		y += rowH + u.px(8)
		return r
	}
	heading(u.t("Paper"))
	names := make([]string, len(wordPapers))
	for i, paper := range wordPapers {
		names[i] = paper.name
	}
	at := paperIndex(p)
	if pick := u.chips("office/word/page/paper", row(), names, at, true); pick != at {
		e.applyPage(wordPapers[pick].on(p))
		return
	}
	heading(u.t("Orientation"))
	turn := 0
	if p.Landscape() {
		turn = 1
	}
	if pick := u.chips("office/word/page/turn", row(), u.ts(msgs("Portrait", "Landscape")), turn, true); pick != turn {
		e.applyPage(p.Turned())
		return
	}
	heading(u.t("Margins"))
	at = marginIndex(p)
	if pick := u.chips("office/word/page/margins", row(), u.ts(wordMarginNames), at, true); pick != at {
		m := wordMargins[pick]
		p.MT, p.MB, p.ML, p.MR = wordPoints(m[0]), wordPoints(m[0]), wordPoints(m[1]), wordPoints(m[1])
		e.applyPage(p)
		return
	}
	heading(u.t("Custom size (mm)"))
	labels := msgs("Width", "Height", "Top", "Bottom", "Left", "Right")
	colW := w / 2
	entered := false
	for i := range e.page.fields {
		r := gfx.Rect{X: x + float32(i%2)*colW, Y: y + float32(i/2)*(rowH+u.px(4)), W: colW - u.px(8), H: rowH}
		u.label(u.face(12), gfx.Rect{X: r.X, Y: r.Y, W: u.px(48), H: r.H}, u.t(labels[i]), colDim, 0)
		id := fmt.Sprintf("office/word/page/field%d", i)
		if u.focus == id {
			for _, k := range u.keys {
				entered = entered || k.Code == keyEnter
			}
		}
		u.textBox(id, gfx.Rect{X: r.X + u.px(50), Y: r.Y, W: r.W - u.px(50), H: r.H}, &e.page.fields[i], "", false)
	}
	y += 3*(rowH+u.px(4)) + u.px(4)
	if len(e.d.Sections()) > 1 {
		pick := 0
		if e.page.section {
			pick = 1
		}
		e.page.section = u.chips("office/word/page/scope", row(), u.ts(msgs("Whole document", "This section")), pick, true) == 1
	}
	typed, valid := e.typedPage()
	if u.button("office/word/page/apply", gfx.Rect{X: x + w - u.px(90), Y: y, W: u.px(90), H: rowH}, "", u.t("Apply"), plain, valid) || entered && valid {
		e.applyPage(typed)
	}
	y += rowH + u.px(10)
	u.c.Fill(gfx.Rect{X: x, Y: y - u.px(5), W: w, H: max(1, u.px(1))}, colBorder)
	half := w / 2
	if u.button("office/word/page/break", gfx.Rect{X: x, Y: y, W: half - u.px(3), H: rowH}, "", u.t("Insert a page break"), ghost, true) {
		e.pop = ""
		e.d.PageBreak()
		e.follow = true
	}
	if u.button("office/word/page/section", gfx.Rect{X: x + half + u.px(3), Y: y, W: half - u.px(3), H: rowH}, "", u.t("New section"), ghost, e.d.CanSectionBreak()) {
		e.pop = ""
		e.d.SectionBreak()
		e.follow = true
	}
}
