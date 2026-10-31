package ui

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/btelolabs/btelo-forge/internal/gfx"
	"github.com/btelolabs/btelo-forge/internal/office/pdf"
)

// The bars and panels around the pages.

// Icons of the PDF tab.
const (
	icPDFLock      = ""
	icPDFThumbs    = ""
	icPDFOutline   = ""
	icPDFZoomIn    = ""
	icPDFZoomOut   = ""
	icPDFFitWidth  = ""
	icPDFFitPage   = ""
	icPDFTurn      = ""
	icPDFSearch    = ""
	icPDFClose     = ""
	icPDFPrev      = ""
	icPDFNext      = ""
	icPDFMore      = ""
	icPDFCursor    = ""
	icPDFHand      = ""
	icPDFHighlight = ""
	icPDFUnderline = ""
	icPDFStrike    = ""
	icPDFInk       = ""
	icPDFRect      = ""
	icPDFEllipse   = ""
	icPDFLine      = ""
	icPDFArrow     = ""
	icPDFNote      = ""
	icPDFFreeText  = ""
	icPDFText      = ""
	icPDFImage     = ""
	icPDFCover     = ""
	icPDFEditText  = ""
	icPDFSign      = ""
	icPDFPages     = ""
)

type pdfToolButton struct {
	tool  pdfTool
	glyph string
	hint  string
}

func pdfToolButtons() []pdfToolButton {
	return []pdfToolButton{
		{pdfToolSelect, icPDFCursor, msgs("Select text and annotations, fill in forms")[0]},
		{pdfToolHand, icPDFHand, msgs("Move the page")[0]},
		{pdfToolHighlight, icPDFHighlight, msgs("Highlight text")[0]},
		{pdfToolUnderline, icPDFUnderline, msgs("Underline text")[0]},
		{pdfToolStrike, icPDFStrike, msgs("Strike out text")[0]},
		{pdfToolInk, icPDFInk, msgs("Draw freehand")[0]},
		{pdfToolRect, icPDFRect, msgs("Rectangle")[0]},
		{pdfToolEllipse, icPDFEllipse, msgs("Ellipse")[0]},
		{pdfToolLine, icPDFLine, msgs("Line")[0]},
		{pdfToolArrow, icPDFArrow, msgs("Arrow")[0]},
		{pdfToolNote, icPDFNote, msgs("Sticky note")[0]},
		{pdfToolFreeText, icPDFFreeText, msgs("Text box annotation")[0]},
		{pdfToolText, icPDFText, msgs("Add text to the page")[0]},
		{pdfToolCover, icPDFCover, msgs("Cover with white: hides what is under it, which stays in the file")[0]},
		{pdfToolEditText, icPDFEditText, msgs("Replace a line of text: covers the old line and writes new words in the closest font")[0]},
	}
}

func (e *pdfEditor) optionsShown() bool {
	switch e.tool {
	case pdfToolHighlight, pdfToolUnderline, pdfToolStrike, pdfToolInk, pdfToolRect, pdfToolEllipse, pdfToolLine, pdfToolArrow, pdfToolNote, pdfToolFreeText, pdfToolText:
		return true
	}
	return e.sel != nil
}

func (e *pdfEditor) drawBar(r gfx.Rect) {
	a := e.a
	u := &a.u
	u.c.Fill(r, colBackground)
	u.c.Fill(gfx.Rect{X: r.X, Y: r.Bottom() - max(1, u.px(1)), W: r.W, H: max(1, u.px(1))}, colBorder)
	s := u.px(30)
	x := r.X + u.px(6)
	y := r.Y + (r.H-s)/2
	btn := func(id, glyph, hint string, on, enabled bool) bool {
		b := gfx.Rect{X: x, Y: y, W: s, H: s}
		x += s + u.px(1)
		return u.iconButton("office/pdf/"+id, b, glyph, hint, on, enabled)
	}
	sep := func() {
		u.c.Fill(gfx.Rect{X: x + u.px(3), Y: y + u.px(5), W: max(1, u.px(1)), H: s - u.px(10)}, colBorder)
		x += u.px(8)
	}
	if btn("thumbs", icPDFThumbs, u.t("Page thumbnails"), e.sideTab == 1, true) {
		e.sideTab = pdfToggle(e.sideTab, 1)
	}
	if btn("outline", icPDFOutline, u.t("Bookmarks"), e.sideTab == 2, true) {
		e.sideTab = pdfToggle(e.sideTab, 2)
	}
	sep()
	e.drawPageBox(gfx.Rect{X: x, Y: y, W: u.px(92), H: s})
	x += u.px(96)
	sep()
	cx, cy := e.area.X+e.area.W/2, e.area.Y+e.area.H/2
	if btn("zoom-out", icPDFZoomOut, u.t("Zoom out"), false, true) {
		e.setZoom(e.zoom/1.25, cx, cy)
	}
	zr := gfx.Rect{X: x, Y: y, W: u.px(46), H: s}
	x += zr.W
	u.label(u.face(12), zr, fmt.Sprintf("%d%%", int(math.Round(e.zoom*100))), colDim, 0.5)
	if btn("zoom-in", icPDFZoomIn, u.t("Zoom in"), false, true) {
		e.setZoom(e.zoom*1.25, cx, cy)
	}
	if btn("fit-width", icPDFFitWidth, u.t("Fit the width"), e.fit == pdfFitWidth, true) {
		e.fit = pdfFitWidth
	}
	if btn("fit-page", icPDFFitPage, u.t("Fit the page"), e.fit == pdfFitPage, true) {
		e.fit = pdfFitPage
	}
	if btn("turn", icPDFTurn, u.t("Turn the view"), e.view != 0, true) {
		e.view = (e.view + 90) % 360
	}
	sep()
	for _, t := range pdfToolButtons() {
		if btn("tool/"+strconv.Itoa(int(t.tool)), t.glyph, u.t(t.hint), e.tool == t.tool, true) {
			if pdfIsMarker(e.tool) != pdfIsMarker(t.tool) {
				e.color, e.altColor = e.altColor, e.color
			}
			e.tool = t.tool
			e.sel = nil
			if t.tool == pdfToolHighlight || t.tool == pdfToolUnderline || t.tool == pdfToolStrike {
				// A selection already made takes the mark at once.
				if e.selFrom != e.selTo {
					e.markSelection(map[pdfTool]pdf.Name{pdfToolHighlight: "Highlight", pdfToolUnderline: "Underline", pdfToolStrike: "StrikeOut"}[t.tool])
				}
			}
		}
	}
	if btn("image", icPDFImage, u.t("Add a picture"), false, true) {
		e.pickImage()
	}
	if btn("sign", icPDFSign, u.t("Sign with a drawn or typed picture: not a cryptographic signature"), e.sign != nil, true) {
		e.openSignPad()
	}
	if btn("print", icPrint, u.t("Print (Ctrl+P)"), false, e.doc != nil) {
		a.printOffice(e.od)
	}
	right := r.Right() - u.px(6)
	more := gfx.Rect{X: right - s, Y: y, W: s, H: s}
	if u.iconButton("office/pdf/more", more, icPDFMore, u.t("More"), a.menuOpen("office/pdf/more"), true) {
		a.openMenu("office/pdf/more", more, e.moreMenu())
	}
	find := gfx.Rect{X: more.X - s - u.px(1), Y: y, W: s, H: s}
	if u.iconButton("office/pdf/find", find, icPDFSearch, u.t("Find in the document (Ctrl+F)"), e.searchOpen, true) {
		if e.searchOpen {
			e.closeSearch()
		} else {
			e.searchOpen = true
			u.setFocus("office/pdf/search")
		}
	}
	pagesBtn := gfx.Rect{X: find.X - s - u.px(1), Y: y, W: s, H: s}
	if u.iconButton("office/pdf/pages", pagesBtn, icPDFPages, u.t("Pages"), a.menuOpen("office/pdf/pages"), true) {
		a.openMenu("office/pdf/pages", pagesBtn, e.pagesMenu(e.chosenPages()))
	}
}

func pdfIsMarker(t pdfTool) bool {
	return t == pdfToolHighlight || t == pdfToolSelect || t == pdfToolHand
}

func pdfToggle(cur, v int) int {
	if cur == v {
		return 0
	}
	return v
}

func (e *pdfEditor) drawPageBox(r gfx.Rect) {
	u := &e.a.u
	id := "office/pdf/pagebox"
	if u.focus != id {
		e.pageBox = strconv.Itoa(e.currentPage() + 1)
	}
	box := gfx.Rect{X: r.X, Y: r.Y + u.px(3), W: u.px(44), H: r.H - u.px(6)}
	_, done := u.editBox(id, box, &e.pageBox, "", false, false, boxStyle{size: 12, pad: 6})
	if done {
		if n, err := strconv.Atoi(strings.TrimSpace(e.pageBox)); err == nil {
			e.goTo(min(max(n, 1), len(e.pages))-1, 0, false)
		}
		u.focus = "office/pdf"
	}
	u.label(u.face(12), gfx.Rect{X: box.Right() + u.px(6), Y: r.Y, W: r.W - box.W, H: r.H}, "/ "+strconv.Itoa(len(e.pages)), colDim, 0)
}

// drawOptions is the row of a tool's settings: colours, and line width or text size.
func (e *pdfEditor) drawOptions(r gfx.Rect) {
	a := e.a
	u := &a.u
	u.c.Fill(r, colBackground)
	u.c.Fill(gfx.Rect{X: r.X, Y: r.Bottom() - max(1, u.px(1)), W: r.W, H: max(1, u.px(1))}, colBorder)
	e.oversNext = append(e.oversNext, r)
	x := r.X + u.px(10)
	y := r.Y + u.px(5)
	s := r.H - u.px(10)
	if e.sel != nil {
		u.label(u.face(12), gfx.Rect{X: x, Y: r.Y, W: u.px(100), H: r.H}, u.t(pdfAnnotName(e.sel.kind)), colDim, 0)
		x += u.px(106)
	}
	for i, c := range pdfSwatches {
		b := gfx.Rect{X: x, Y: y, W: s, H: s}
		u.c.Fill(b, pdfRGBA(c))
		if c == e.color && e.sel == nil {
			u.c.Outline(b.Inset(-u.px(2)), max(1, u.px(1.5)), colText)
		}
		id := "office/pdf/swatch/" + strconv.Itoa(i)
		if u.hover(id, b) {
			u.cursor = CursorHand
		}
		if u.clicked(id, b) {
			e.color = c
			e.textStyle.Color = pdfTextColor(c)
			e.recolour(c)
		}
		x += s + u.px(7)
	}
	x += u.px(8)
	switch e.tool {
	case pdfToolInk, pdfToolRect, pdfToolEllipse, pdfToolLine, pdfToolArrow:
		u.label(u.face(12), gfx.Rect{X: x, Y: r.Y, W: u.px(60), H: r.H}, u.t("Width"), colDim, 0)
		x += u.px(52)
		opts := []string{"0.5", "1", "1.5", "2", "3", "5"}
		sel := slices.Index(opts, strconv.FormatFloat(e.width, 'f', -1, 64))
		next := u.chips("office/pdf/width", gfx.Rect{X: x, Y: y, W: u.px(216), H: s}, opts, sel, true)
		if next != sel && next >= 0 {
			e.width, _ = strconv.ParseFloat(opts[next], 64)
		}
	case pdfToolFreeText, pdfToolText:
		fb := gfx.Rect{X: x, Y: y, W: u.px(150), H: s}
		if u.button("office/pdf/family", fb, "", e.textStyle.Family, plain, true) {
			a.openMenu("office/pdf/family", fb, e.familyMenu())
		}
		x = fb.Right() + u.px(8)
		sizes := []string{"9", "10", "12", "14", "18", "24", "36"}
		sel := slices.Index(sizes, strconv.FormatFloat(e.textStyle.Size, 'f', -1, 64))
		next := u.chips("office/pdf/size", gfx.Rect{X: x, Y: y, W: u.px(250), H: s}, sizes, sel, true)
		if next != sel && next >= 0 {
			e.textStyle.Size, _ = strconv.ParseFloat(sizes[next], 64)
		}
		x += u.px(258)
		if e.tool == pdfToolText {
			if u.iconButton("office/pdf/bold", gfx.Rect{X: x, Y: y, W: s, H: s}, "", u.t("Bold"), e.textStyle.Bold, true) {
				e.textStyle.Bold = !e.textStyle.Bold
			}
			x += s + u.px(2)
			if u.iconButton("office/pdf/italic", gfx.Rect{X: x, Y: y, W: s, H: s}, "", u.t("Italic"), e.textStyle.Italic, true) {
				e.textStyle.Italic = !e.textStyle.Italic
			}
		}
	}
	if e.sel != nil {
		bw := u.px(84)
		if u.button("office/pdf/annot-delete", gfx.Rect{X: r.Right() - bw - u.px(10), Y: y, W: bw, H: s}, "", u.t("Delete"), plain, true) {
			e.deleteChosen()
		}
	}
}

var pdfSwatches = [][3]float64{{1, 0.85, 0.1}, {0.95, 0.35, 0.3}, {0.3, 0.75, 0.4}, {0.25, 0.55, 1}, {0.75, 0.4, 0.9}, {1, 0.55, 0.15}, {0, 0, 0}}

// pdfTextColor keeps the marker yellow from being the colour of words: pale swatches make dark text.
func pdfTextColor(c [3]float64) [3]float64 {
	if 0.3*c[0]+0.59*c[1]+0.11*c[2] > 0.7 {
		return [3]float64{0.1, 0.1, 0.1}
	}
	return c
}

func pdfAnnotName(k pdf.Name) string {
	switch k {
	case "Highlight":
		return msgs("Highlight")[0]
	case "Underline":
		return msgs("Underline")[0]
	case "StrikeOut":
		return msgs("Strike out")[0]
	case "Ink":
		return msgs("Freehand")[0]
	case "Square":
		return msgs("Rectangle")[0]
	case "Circle":
		return msgs("Ellipse")[0]
	case "Line":
		return msgs("Line")[0]
	case "Text":
		return msgs("Sticky note")[0]
	case "FreeText":
		return msgs("Text box")[0]
	}
	return msgs("Annotation")[0]
}

func (e *pdfEditor) familyMenu() []menuItem {
	book := e.a.officeBook()
	var items []menuItem
	names := book.Families()
	slices.Sort(names)
	for _, n := range names {
		items = append(items, menuItem{label: pdfFamilyLabel(n), checked: strings.EqualFold(n, e.textStyle.Family), run: func() { e.textStyle.Family = n }})
	}
	return items
}

func pdfFamilyLabel(n string) string {
	words := strings.Fields(n)
	for i, w := range words {
		if w != "" && w[0] < 0x80 {
			words[i] = strings.ToUpper(w[:1]) + w[1:]
		}
	}
	return strings.Join(words, " ")
}

// chosenPages are the pages picked in the thumbnails, else the current one, in order.
func (e *pdfEditor) chosenPages() []int {
	var out []int
	for i := range e.pages {
		if e.picked[i] {
			out = append(out, i)
		}
	}
	if len(out) == 0 {
		out = []int{e.currentPage()}
	}
	return out
}

func (e *pdfEditor) pagesMenu(pages []int) []menuItem {
	u := &e.a.u
	turn := func(deg int) {
		e.edited(e.doc.RotatePages(pages, deg), pages...)
		e.layoutOf = [5]float64{}
	}
	at := e.currentPage() + 1
	if len(pages) > 0 {
		at = pages[len(pages)-1] + 1
	}
	return []menuItem{
		{label: u.t("Turn right"), run: func() { turn(90) }},
		{label: u.t("Turn left"), run: func() { turn(-90) }},
		menuSep,
		{label: u.t("Extract to a new PDF…"), run: func() { e.openSaveAs(pages, false) }},
		{label: u.t("Delete"), disabled: len(pages) >= len(e.pages), run: func() {
			if e.edited(e.doc.DeletePages(pages)) {
				e.rev++
				e.sel = nil
				e.clearSelection()
				e.a.toast(u.t("The next save rewrites the file, so that what the pages held leaves it."), false)
			}
		}},
		menuSep,
		{label: u.t("Insert a blank page"), run: func() {
			w, h := e.doc.ViewSize(e.pages[min(at, len(e.pages))-1], 0)
			e.edited(e.doc.InsertBlank(at, w, h))
		}},
		{label: u.t("Insert a PDF…"), run: func() {
			e.a.pickFiles(pickRequest{key: "office", title: u.t("Insert a PDF"), kinds: u.t("PDF files"), exts: []string{".pdf"}, multiple: true}, func(paths []string) {
				for _, p := range paths {
					e.insertPDF(p, at)
				}
			})
		}},
		{label: u.t("Insert pictures as pages…"), run: func() {
			e.a.pickFiles(pickRequest{key: "picture", title: u.t("Add a picture"), kinds: u.t("Pictures"), exts: pickPictures, multiple: true}, func(paths []string) {
				for i, p := range paths {
					img, data, err := pdfLoadPicture(p)
					if err == nil {
						err = e.doc.InsertImagePage(at+i, img, data)
					}
					e.edited(err)
				}
			})
		}},
	}
}

func (e *pdfEditor) moreMenu() []menuItem {
	u := &e.a.u
	return []menuItem{
		{label: u.t("Save a copy as…"), run: func() { e.openSaveAs(nil, false) }},
		{label: u.t("Save a compressed copy…"), run: func() { e.openSaveAs(nil, true) }},
		menuSep,
		{label: u.t("Copy"), key: "Ctrl+C", disabled: e.selectionText() == "", run: func() { u.setClipText(e.selectionText()) }},
		{label: u.t("Select all on this page"), key: "Ctrl+A", run: e.selectAll},
	}
}

// insertPDF brings in another file's pages, away from the window for reading it.
func (e *pdfEditor) insertPDF(path string, at int) {
	a := e.a
	data, err := os.ReadFile(path)
	if err != nil {
		a.toast(a.describe(err), true)
		return
	}
	src, err := pdf.Open(data, "")
	if err != nil {
		a.toast(a.officeError(path, err), true)
		return
	}
	if e.edited(e.doc.Merge(src, at)) {
		a.toast(a.u.tn(src.PageCount(), "%d page added", "%d pages added", src.PageCount()), false)
	}
}

// drawSide draws the panel left of the pages.
func (e *pdfEditor) drawSide(r gfx.Rect) {
	u := &e.a.u
	u.c.Fill(r, colBackground)
	u.c.Fill(gfx.Rect{X: r.Right() - max(1, u.px(1)), Y: r.Y, W: max(1, u.px(1)), H: r.H}, colBorder)
	u.c.Push(r)
	defer u.c.Pop()
	switch e.sideTab {
	case 1:
		e.drawThumbs(r)
	case 2:
		e.drawOutline(r)
	}
}

func (e *pdfEditor) drawThumbs(r gfx.Rect) {
	a := e.a
	u := &a.u
	id := "office/pdf/thumbs"
	cellW := r.W - u.px(16)
	maxW := float64(cellW - u.px(16))
	label := u.px(20)
	gap := u.px(8)
	// Heights vary with each page's shape, so places come from a walk down the column.
	y := u.px(8) - e.thumbScroll
	content := u.px(8)
	type cell struct {
		i int
		r gfx.Rect
	}
	var cells []cell
	var hit = -1
	for i, size := range e.sizes {
		w, h := size[0], size[1]
		k := min(maxW/w, float64(u.px(150))/h)
		th := float32(h * k)
		tw := float32(w * k)
		box := gfx.Rect{X: r.X + (r.W-tw)/2 - u.px(0), Y: r.Y + y, W: tw, H: th}
		if box.Bottom()+label >= r.Y && box.Y <= r.Bottom() {
			cells = append(cells, cell{i, box})
		}
		slot := gfx.Rect{X: r.X, Y: r.Y + y - gap/2, W: r.W, H: th + label + gap}
		if slot.Contains(u.mx, u.my) {
			hit = i
			if u.my > slot.Y+slot.H/2 {
				e.dropAt = i + 1
			} else {
				e.dropAt = i
			}
		}
		y += th + label + gap
		content += th + label + gap
	}
	maxScroll := max(content-r.H, 0)
	if u.wheel != 0 && u.hover(id, r) {
		e.thumbScroll -= u.wheel * u.px(0.8)
	}
	e.thumbScroll = min(max(e.thumbScroll, 0), maxScroll)
	f := u.face(11.5)
	cur := e.currentPage()
	for _, c := range cells {
		scale := float64(c.r.W) / e.sizes[c.i][0]
		bm := e.ensure(c.i, true, scale)
		edge := colBorderHi
		if e.picked[c.i] {
			edge = colAccent
		} else if c.i == cur {
			edge = colDim
		}
		u.c.Fill(c.r.Inset(-max(1, u.px(1))), edge)
		u.c.Fill(c.r, colWhite)
		if bm != nil {
			u.c.Image(bm.img, c.r)
		}
		u.label(f, gfx.Rect{X: r.X, Y: c.r.Bottom() + u.px(2), W: r.W, H: label - u.px(2)}, strconv.Itoa(c.i+1), colDim, 0.5)
	}
	if u.press(id, r) && hit >= 0 {
		u.focus = "office/pdf"
		e.dragThumb = false
		switch {
		case u.ctrl:
			e.picked[hit] = !e.picked[hit]
		case u.shift:
			lo, hi := min(e.anchor, hit), max(e.anchor, hit)
			e.picked = map[int]bool{}
			for k := lo; k <= hi; k++ {
				e.picked[k] = true
			}
		default:
			if !e.picked[hit] {
				e.picked = map[int]bool{hit: true}
			}
			e.goTo(hit, 0, false)
		}
		if !u.shift {
			e.anchor = hit
		}
	}
	if u.dragging(id) && u.drag.moved {
		e.dragThumb = true
		if e.dropAt >= 0 {
			// The line where the pages would land.
			at := e.dropAt
			yy := u.px(8) - e.thumbScroll
			for _, size := range e.sizes[:min(at, len(e.sizes))] {
				k := min(maxW/size[0], float64(u.px(150))/size[1])
				yy += float32(size[1]*k) + label + gap
			}
			u.c.Fill(gfx.Rect{X: r.X + u.px(10), Y: r.Y + yy - gap/2 - u.px(1), W: r.W - u.px(20), H: u.px(2)}, colAccent)
		}
	}
	if u.letGo(id) && e.dragThumb {
		e.dragThumb = false
		e.dropPages()
	}
	if u.rightAt != nil && r.Contains(u.rightAt.X, u.rightAt.Y) && hit >= 0 && !u.blocked {
		if !e.picked[hit] {
			e.picked = map[int]bool{hit: true}
		}
		a.openMenu("office/pdf/thumb", gfx.Rect{X: u.rightAt.X, Y: u.rightAt.Y}, e.pagesMenu(e.chosenPages()))
	}
}

// dropPages moves the picked pages to the line shown.
func (e *pdfEditor) dropPages() {
	pages := e.chosenPages()
	at := e.dropAt
	for _, p := range pages {
		if p < e.dropAt {
			at--
		}
	}
	if len(pages) == 1 && pages[0] == at {
		return
	}
	if e.edited(e.doc.MovePages(pages, at)) {
		e.picked = map[int]bool{}
		for i := range pages {
			e.picked[at+i] = true
		}
	}
}

func (e *pdfEditor) drawOutline(r gfx.Rect) {
	u := &e.a.u
	if v := e.doc.Version(); e.outlineItems == nil || v != e.outlineSeen {
		e.outlineItems, e.outlineSeen = e.doc.Outline(), v
		if e.outlineItems == nil {
			e.outlineItems = []pdf.OutlineItem{}
		}
	}
	if len(e.outlineItems) == 0 {
		lines := u.face(12.5).Wrap(u.t("This document has no bookmarks."), r.W-u.px(24))
		for i, l := range lines {
			u.label(u.face(12.5), gfx.Rect{X: r.X + u.px(12), Y: r.Y + u.px(14) + float32(i)*u.px(20), W: r.W - u.px(24), H: u.px(20)}, l, colDim, 0)
		}
		return
	}
	id := "office/pdf/outline"
	rowH := u.px(26)
	y := r.Y + u.px(4) - e.outlineView
	f := u.face(12.5)
	total := float32(0)
	var walk func(list []pdf.OutlineItem, depth int, prefix string)
	walk = func(list []pdf.OutlineItem, depth int, prefix string) {
		for i, it := range list {
			key := prefix + "/" + strconv.Itoa(i)
			total += rowH
			row := gfx.Rect{X: r.X, Y: y, W: r.W, H: rowH}
			if row.Bottom() >= r.Y && row.Y <= r.Bottom() {
				x := r.X + u.px(6) + float32(depth)*u.px(12)
				rid := id + key
				hot := u.hover(rid, row)
				if hot {
					u.c.Fill(row, colSurfaceHi)
					u.cursor = CursorHand
				}
				if len(it.Kids) > 0 {
					glyph := ""
					if e.outlineOpen[key] {
						glyph = ""
					}
					arrow := gfx.Rect{X: x, Y: y, W: u.px(18), H: rowH}
					u.icon(glyph, arrow, 14, colDim)
					if u.clicked(rid+"/open", arrow) {
						e.outlineOpen[key] = !e.outlineOpen[key]
					}
				}
				tx := x + u.px(20)
				u.label(f, gfx.Rect{X: tx, Y: y, W: r.Right() - tx - u.px(6), H: rowH}, f.Ellipsize(it.Title, r.Right()-tx-u.px(6)), colText, 0)
				u.hint(rid+"/tip", it.Title, row)
				if u.clicked(rid, row) && it.Page >= 0 {
					e.goTo(it.Page, it.Top, it.HasY)
				}
			}
			y += rowH
			if len(it.Kids) > 0 && e.outlineOpen[key] {
				walk(it.Kids, depth+1, key)
			}
		}
	}
	walk(e.outlineItems, 0, "")
	if u.wheel != 0 && r.Contains(u.mx, u.my) && !u.blocked {
		e.outlineView -= u.wheel * u.px(0.8)
	}
	e.outlineView = min(max(e.outlineView, 0), max(total+u.px(8)-r.H, 0))
}

func (e *pdfEditor) drawSignedBanner(r gfx.Rect) {
	u := &e.a.u
	u.c.Fill(r, gfx.Mix(colBackground, colWarning, 0.18))
	f := u.face(12)
	w := r.W - u.px(60)
	u.label(f, gfx.Rect{X: r.X + u.px(12), Y: r.Y, W: w, H: r.H},
		f.Ellipsize(u.t("This PDF is digitally signed. Edits are added after the signed part, so the signature stays valid for what was signed but does not cover them; deleting pages or compressing breaks it."), w), colWarning, 0)
	if u.iconButton("office/pdf/banner", gfx.Rect{X: r.Right() - u.px(34), Y: r.Y + u.px(2), W: u.px(26), H: r.H - u.px(4)}, icPDFClose, u.t("Close"), false, true) {
		e.banner = true
	}
}

// drawSearch is the find box, at the top right of the pages.
func (e *pdfEditor) drawSearch() {
	u := &e.a.u
	w := min(u.px(360), e.area.W-u.px(16))
	y0 := e.area.Y + u.px(8)
	if e.optionsShown() {
		y0 += u.px(34)
	}
	r := gfx.Rect{X: e.area.Right() - w - u.px(14), Y: y0, W: w, H: u.px(38)}
	e.oversNext = append(e.oversNext, r)
	u.c.Fill(r, colSurface)
	u.c.Outline(r, max(1, u.px(1)), colBorderHi)
	id := "office/pdf/search"
	box := gfx.Rect{X: r.X + u.px(5), Y: r.Y + u.px(5), W: r.W - u.px(168), H: r.H - u.px(10)}
	if changed, _ := u.textBox(id, box, &e.query, u.t("Find"), false); changed {
		e.search()
	}
	if u.focus == id {
		for _, k := range u.keys {
			switch k.Code {
			case keyEnter:
				if k.Shift {
					e.nextMatch(-1)
				} else {
					e.nextMatch(1)
				}
			case keyEscape:
				e.closeSearch()
				return
			}
		}
	}
	text := ""
	switch {
	case e.query == "":
	case len(e.matches) == 0 && e.searching != nil:
		text = u.t("Searching…")
	case len(e.matches) == 0:
		text = u.t("No matches")
	default:
		text = fmt.Sprintf("%d/%d", e.current+1, len(e.matches))
	}
	u.label(u.face(11.5), gfx.Rect{X: box.Right() + u.px(4), Y: r.Y, W: u.px(68), H: r.H}, text, colDim, 0.5)
	x := r.Right() - u.px(90)
	s := u.px(28)
	y := r.Y + u.px(5)
	if u.iconButton("office/pdf/search/prev", gfx.Rect{X: x, Y: y, W: s, H: s}, "", u.t("Previous"), false, len(e.matches) > 0) {
		e.nextMatch(-1)
	}
	if u.iconButton("office/pdf/search/next", gfx.Rect{X: x + s, Y: y, W: s, H: s}, "", u.t("Next"), false, len(e.matches) > 0) {
		e.nextMatch(1)
	}
	if u.iconButton("office/pdf/search/close", gfx.Rect{X: x + 2*s, Y: y, W: s, H: s}, icPDFClose, u.t("Close"), false, true) {
		e.closeSearch()
	}
}

func (e *pdfEditor) closeSearch() {
	e.searchOpen = false
	if e.searching != nil {
		e.searching()
		e.searching = nil
	}
	e.searchRun++
	e.matches, e.current, e.query = nil, 0, ""
	if e.a.u.focus == "office/pdf/search" {
		e.a.u.focus = "office/pdf"
	}
}

// search runs through the pages' text away from the window, and hits come in as they are found.
func (e *pdfEditor) search() {
	a := e.a
	if e.searching != nil {
		e.searching()
		e.searching = nil
	}
	e.searchRun++
	e.matches, e.current = nil, 0
	q := strings.TrimSpace(e.query)
	if q == "" {
		return
	}
	cctx, cancel := context.WithCancel(a.life)
	e.searching = cancel
	e.searchRun++
	run := e.searchRun
	pages := e.pages
	cache := e.cache
	start := e.currentPage()
	a.background(func(context.Context) {
		defer cancel()
		found := 0
		for k := range pages {
			if cctx.Err() != nil {
				return
			}
			i := (start + k) % len(pages) // the nearest hits first
			t, _ := cache.Text(cctx, pages[i])
			if t == nil {
				continue
			}
			var hits []pdfMatch
			for _, m := range t.Search(q) {
				hits = append(hits, pdfMatch{i, m.From, m.To})
			}
			if len(hits) == 0 {
				continue
			}
			found += len(hits)
			a.post(func() {
				if run != e.searchRun {
					return
				}
				first := len(e.matches) == 0
				if num := pages[i].Ref.Num; e.texts[num] == nil {
					e.texts[num] = &pdfText{t: t} // so hits can be drawn before the page's picture is ready; both come from one interpreter
				}
				e.matches = append(e.matches, hits...)
				if first {
					e.showMatch()
				}
			})
			if found >= pdfSearchMatches {
				break
			}
		}
		a.post(func() {
			if run == e.searchRun {
				e.searching = nil
				e.sortMatches()
				a.requestFrame()
			}
		})
	})
}

// sortMatches puts hits in document order, keeping the current one.
func (e *pdfEditor) sortMatches() {
	if len(e.matches) == 0 {
		return
	}
	cur := e.matches[min(e.current, len(e.matches)-1)]
	slices.SortStableFunc(e.matches, func(a, b pdfMatch) int {
		if a.page != b.page {
			return a.page - b.page
		}
		return a.from - b.from
	})
	e.current = max(slices.Index(e.matches, cur), 0)
}

func (e *pdfEditor) nextMatch(step int) {
	if n := len(e.matches); n > 0 {
		e.current = (e.current + step + n) % n
		e.showMatch()
	}
}

func (e *pdfEditor) showMatch() {
	m := e.matches[e.current]
	t := e.textOf(m.page)
	if t == nil {
		e.goTo(m.page, 0, false)
		return
	}
	if q := t.Quads(m.from, m.to); len(q) > 0 {
		e.goTo(m.page, q[0][1], true)
		e.scrollY -= e.area.H / 3
		e.clampScroll()
	}
}

// pdfSaveAs is the small dialog that takes a name and a folder.
type pdfSaveAs struct {
	name     string
	dir      string
	pages    []int // extract these; nil means the whole document
	compress bool
}

func (e *pdfEditor) openSaveAs(pages []int, compress bool) {
	u := &e.a.u
	base := strings.TrimSuffix(filepath.Base(e.od.path), filepath.Ext(e.od.path))
	name := base + " " + u.t("copy")
	switch {
	case compress:
		name = base + " " + u.t("compressed")
	case pages != nil:
		name = base + " " + u.t("pages")
	}
	e.saveAs = &pdfSaveAs{name: name, dir: filepath.Dir(e.od.path), pages: pages, compress: compress}
	u.setFocus("office/pdf/saveas")
}

func (e *pdfEditor) drawSaveAs() {
	a := e.a
	u := &a.u
	s := e.saveAs
	w := min(u.px(440), e.area.W-u.px(20))
	r := gfx.Rect{X: e.area.X + (e.area.W-w)/2, Y: e.area.Y + u.px(40), W: w, H: u.px(172)}
	u.c.Fill(gfx.Rect{X: r.X, Y: r.Y + u.px(3), W: r.W, H: r.H}, gfx.Alpha(colBlack, 0.4))
	u.c.Fill(r, colPanel)
	u.c.Outline(r, max(1, u.px(1)), colBorderHi)
	in := r.Inset(u.px(14))
	title := msgs("Save a copy")[0]
	if s.compress {
		title = msgs("Save a compressed copy")[0]
	} else if s.pages != nil {
		title = msgs("Extract pages to a new PDF")[0]
	}
	u.label(u.bold(14), gfx.Rect{X: in.X, Y: in.Y, W: in.W, H: u.px(22)}, u.t(title), colText, 0)
	y := in.Y + u.px(30)
	u.textBox("office/pdf/saveas", gfx.Rect{X: in.X, Y: y, W: in.W - u.px(46), H: u.px(32)}, &s.name, "", false)
	u.label(u.face(12.5), gfx.Rect{X: in.Right() - u.px(40), Y: y, W: u.px(40), H: u.px(32)}, ".pdf", colDim, 0)
	y += u.px(38)
	f := u.face(12)
	u.label(f, gfx.Rect{X: in.X, Y: y, W: in.W - u.px(100), H: u.px(28)}, f.Ellipsize(s.dir, in.W-u.px(100)), colDim, 0)
	if u.button("office/pdf/saveas/dir", gfx.Rect{X: in.Right() - u.px(92), Y: y, W: u.px(92), H: u.px(28)}, "", u.t("Folder…"), plain, true) {
		a.pickDir(pickRequest{key: "office", title: u.t("Choose a folder"), dir: s.dir}, func(d string) { s.dir = d })
	}
	y += u.px(38)
	bw := u.px(84)
	ok := strings.TrimSpace(s.name) != ""
	if u.button("office/pdf/saveas/ok", gfx.Rect{X: in.Right() - 2*bw - u.px(6), Y: y, W: bw, H: u.px(30)}, "", u.t("Save"), primary, ok) {
		e.finishSaveAs()
		return
	}
	if u.button("office/pdf/saveas/no", gfx.Rect{X: in.Right() - bw, Y: y, W: bw, H: u.px(30)}, "", u.t("Cancel"), plain, true) {
		e.saveAs = nil
		u.focus = "office/pdf"
	}
}

func (e *pdfEditor) finishSaveAs() {
	a := e.a
	u := &a.u
	s := e.saveAs
	e.saveAs = nil
	u.focus = "office/pdf"
	name := strings.TrimSpace(s.name)
	if !strings.HasSuffix(strings.ToLower(name), ".pdf") {
		name += ".pdf"
	}
	path := filepath.Join(s.dir, name)
	if _, err := os.Stat(path); err == nil || samePath(path, e.od.path) {
		a.toast(u.tf("%s already exists. Choose another name.", name), true)
		return
	}
	e.commitPending()
	e.commitField()
	snap := e.doc.Snapshot()
	doc := e.doc
	pages, compress := s.pages, s.compress
	a.background(func(context.Context) {
		var data []byte
		var err error
		switch {
		case pages != nil:
			data, err = doc.Extract(pages)
		case compress:
			data, err = snap.Rewrite(true)
		default:
			data, err = snap.Save()
		}
		if err == nil {
			err = writeFileWhole(path, data)
		}
		a.post(func() {
			if err != nil {
				a.toast(u.tf("The document could not be saved: %s", a.describe(err)), true)
				return
			}
			a.toast(u.tf("Saved %s (%s)", name, pdfSize(len(data))), false)
		})
	})
}

func pdfSize(n int) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%d KB", n>>10)
	}
	return fmt.Sprintf("%d B", n)
}
