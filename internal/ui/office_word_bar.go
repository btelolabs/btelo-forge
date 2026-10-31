package ui

import (
	"image/color"
	"strconv"
	"strings"

	"github.com/btelolabs/btelo-forge/internal/gfx"
	"github.com/btelolabs/btelo-forge/internal/office/docx"
	"github.com/btelolabs/btelo-forge/internal/office/flow"
)

// The toolbar, the find bar, the link bar and the small popups. Everything on the toolbar shows the caret's state.

// Symbols of the toolbar, from Material Icons.
const (
	icWBold      = ""
	icWItalic    = ""
	icWUnder     = ""
	icWStrike    = ""
	icWSuper     = ""
	icWSub       = ""
	icWColor     = ""
	icWHighlight = ""
	icWClear     = ""
	icWLeft      = ""
	icWCenter    = ""
	icWRight     = ""
	icWJustify   = ""
	icWIndentIn  = ""
	icWIndentOut = ""
	icWLines     = ""
	icWBullets   = ""
	icWNumbers   = ""
	icWTable     = ""
	icWPicture   = ""
	icWBreak     = ""
	icWLink      = ""
	icWHeader    = ""
	icWOutline   = ""
	icWSearch    = ""
	icWClose     = ""
	icWUp        = ""
	icWDown      = ""
	icWNumber    = ""
)

// wordFonts are the families offered; a document's own show too.
var wordFonts = []string{"Calibri", "Arial", "Times New Roman", "Georgia", "Verdana", "Tahoma", "Consolas", "Courier New", "微软雅黑", "等线", "宋体", "黑体", "楷体", "仿宋"}

var wordSizes = []float32{8, 9, 10, 10.5, 11, 12, 14, 16, 18, 20, 22, 24, 26, 28, 36, 48, 72}

// wordSwatches are the palette of the colour and highlight popups.
var wordSwatches = []uint32{0x000000, 0x434343, 0x7f7f7f, 0xbfbfbf, 0xffffff, 0xc00000, 0xff0000, 0xffc000, 0xffff00, 0x92d050, 0x00b050, 0x00b0f0, 0x0070c0, 0x002060, 0x7030a0}

// wordHighlights are the highlights Word has names for, in the order shown.
var wordHighlights = []string{"yellow", "green", "cyan", "magenta", "blue", "red", "darkBlue", "darkCyan", "darkGreen", "darkMagenta", "darkRed", "darkYellow", "darkGray", "lightGray", "black"}

// styleLabel is a style's name for people: the built-in ones in the user's language.
func styleLabel(u *ctx, st *docx.Style) string {
	switch strings.ToLower(st.Name) {
	case "normal":
		return u.tc("word", "Normal")
	case "heading 1":
		return u.t("Heading 1")
	case "heading 2":
		return u.t("Heading 2")
	case "heading 3":
		return u.t("Heading 3")
	case "heading 4":
		return u.t("Heading 4")
	case "title":
		return u.t("Title")
	case "subtitle":
		return u.t("Subtitle")
	case "quote":
		return u.t("Quote")
	case "header":
		return u.t("Header")
	case "footer":
		return u.t("Footer")
	case "list paragraph":
		return u.t("List paragraph")
	}
	return st.Name
}

// wordStyleMenu is how many styles the style menu lists.
const wordStyleMenu = 22

// builtinStyles are the styles always offered, added to a document that lacks them when picked.
var builtinStyles = []string{"Normal", "Title", "Subtitle", "Heading1", "Heading2", "Heading3", "Quote"}

// drawBar draws the toolbar in the top of r and returns the rectangle it took: its buttons wrap onto a second row in
// a narrow window.
func (e *wordEditor) drawBar(r gfx.Rect) gfx.Rect {
	a := e.a
	u := &a.u
	d := e.d
	rowH := u.px(38)
	side := u.px(30)
	pad := u.px(8)
	bar := gfx.Rect{X: r.X, Y: r.Y, W: r.W, H: e.bar.H}
	if bar.H == 0 {
		bar.H = rowH
	}
	u.c.Fill(bar, colSurface)
	u.c.Fill(gfx.Rect{X: r.X, Y: bar.Bottom() - max(1, u.px(1)), W: r.W, H: max(1, u.px(1))}, colBorder)
	x, y := r.X+pad, r.Y+(rowH-side)/2
	place := func(w float32) gfx.Rect {
		if x+w > r.Right()-pad {
			x, y = r.X+pad, y+rowH
		}
		out := gfx.Rect{X: x, Y: y, W: w, H: side}
		x += w + u.px(2)
		return out
	}
	gap := func() {
		x += u.px(6)
		if x < r.Right()-pad {
			u.c.Fill(gfx.Rect{X: x - u.px(4), Y: y + u.px(6), W: max(1, u.px(1)), H: side - u.px(12)}, colBorder)
		}
	}
	button := func(id, glyph, hint string, on bool, run func()) {
		if u.iconButton("office/word/"+id, place(side), glyph, hint, on, true) {
			run()
			u.setFocus(wordFocus)
		}
	}
	button("print", icPrint, u.t("Print (Ctrl+P)"), false, func() { a.printOffice(e.od) })
	gap()
	rp := d.CaretRun()
	para := d.CaretPara()
	e.styleBox(place(u.px(130)), para)
	e.fontBox(place(u.px(124)), &rp)
	e.sizeBox(place(u.px(62)), &rp)
	gap()
	button("bold", icWBold, u.tc("word", "Bold")+" (Ctrl+B)", rp.Bold, func() { e.toggle(func(c *docx.RunChange, v *bool) { c.Bold = v }, rp.Bold) })
	button("italic", icWItalic, u.t("Italic")+" (Ctrl+I)", rp.Italic, func() { e.toggle(func(c *docx.RunChange, v *bool) { c.Italic = v }, rp.Italic) })
	button("under", icWUnder, u.t("Underline")+" (Ctrl+U)", rp.Under != "", func() { e.toggle(func(c *docx.RunChange, v *bool) { c.Underline = v }, rp.Under != "") })
	button("strike", icWStrike, u.t("Strikethrough"), rp.Strike, func() { e.toggle(func(c *docx.RunChange, v *bool) { c.Strike = v }, rp.Strike) })
	vert := func(want int8) func() {
		return func() {
			v := want
			if rp.Vert == want {
				v = 0
			}
			d.FormatRuns(docx.RunChange{Vert: &v})
		}
	}
	button("super", icWSuper, u.t("Superscript"), rp.Vert == 1, vert(1))
	button("sub", icWSub, u.t("Subscript"), rp.Vert == -1, vert(-1))
	colorButton := func(id, glyph, hint string, col color.RGBA) {
		box := place(side)
		if u.iconButton("office/word/"+id, box, glyph, hint, e.pop == id, true) {
			e.openPop(id, box)
		}
		if col.A != 0 {
			u.c.Fill(gfx.Rect{X: box.X + u.px(7), Y: box.Bottom() - u.px(6), W: box.W - u.px(14), H: u.px(3)}, col)
		}
	}
	colorButton("color", icWColor, u.t("Text colour"), rp.Color)
	colorButton("highlight", icWHighlight, u.t("Highlight"), rp.Highlight)
	button("clear", icWClear, u.t("Clear formatting"), false, func() { d.ClearFormatting() })
	gap()
	align := flow.Left
	if para != nil {
		align = para.PP.Align
	}
	for _, al := range []struct {
		id, glyph, hint string
		v               flow.Align
	}{{"left", icWLeft, u.tc("word", "Align left"), flow.Left}, {"center", icWCenter, u.tc("word", "Centre"), flow.Center}, {"right", icWRight, u.tc("word", "Align right"), flow.Right}, {"justify", icWJustify, u.t("Justify"), flow.Justify}} {
		button(al.id, al.glyph, al.hint, align == al.v, func() { d.SetAlign(al.v) })
	}
	button("outdent", icWIndentOut, u.t("Decrease indent"), false, func() { d.Indent(false) })
	button("indent", icWIndentIn, u.t("Increase indent"), false, func() { d.Indent(true) })
	lines := place(side)
	if u.iconButton("office/word/lines", lines, icWLines, u.t("Line spacing"), false, true) {
		e.lineMenu(lines)
	}
	kind, listed := d.CaretList()
	button("bullets", icWBullets, u.t("Bullets"), listed && kind == docx.ListBullet, func() { d.ToggleList(docx.ListBullet) })
	numbers := place(side)
	if u.iconButton("office/word/numbers", numbers, icWNumbers, u.t("Numbering"), listed && kind != docx.ListBullet, true) {
		e.listMenu(numbers)
	}
	gap()
	table := place(side)
	if u.iconButton("office/word/table", table, icWTable, u.t("Table"), e.pop == "table", true) {
		if d.CaretInTable() {
			a.openMenu("office/word/tablemenu", table, e.tableItems())
		} else {
			e.openPop("table", table)
		}
	}
	button("picture", icWPicture, u.tc("word", "Picture"), false, e.choosePicture)
	button("break", icWBreak, u.t("Page break")+" (Ctrl+Enter)", false, func() { d.PageBreak() })
	pageText := u.t("Page setup")
	pageBox := place(u.buttonWidth("", pageText))
	if u.button("office/word/page", pageBox, "", pageText, ghost, true) {
		e.openPop("page", pageBox)
		e.loadPagePanel()
	}
	button("link", icWLink, u.t("Link")+" (Ctrl+K)", d.LinkAt(d.Sel.Caret) != "", e.openLink)
	hf := place(side)
	if u.iconButton("office/word/header", hf, icWHeader, u.t("Header and footer"), e.inHeader(), true) {
		e.headerMenu(hf)
	}
	if e.inHeader() {
		button("pagenum", icWNumber, u.t("Page number"), false, func() { d.InsertField("PAGE", "1") })
	}
	gap()
	button("outline", icWOutline, u.t("Headings"), e.outline, func() { e.outline = !e.outline })
	button("find", icWSearch, u.t("Find and replace")+" (Ctrl+F)", e.find != nil, func() { e.openFind(false) })
	e.bar = gfx.Rect{X: r.X, Y: r.Y, W: r.W, H: y + side + (rowH-side)/2 - r.Y}
	return e.bar
}

// styleBox is the style picker.
func (e *wordEditor) styleBox(r gfx.Rect, para *docx.Para) {
	a := e.a
	u := &a.u
	d := e.d
	var names []string
	var ids []string
	// A document can have hundreds of styles: the menu holds those marked for the gallery, and the one in use.
	for _, st := range d.Styles.StyleList() {
		if len(ids) < wordStyleMenu && st.Quick || para != nil && st.ID == para.PP.Style {
			names = append(names, styleLabel(u, st))
			ids = append(ids, st.ID)
		}
	}
	for _, id := range builtinStyles {
		if d.Styles.ByID[id] == nil {
			names = append(names, styleLabel(u, &docx.Style{Name: strings.ReplaceAll(strings.ReplaceAll(id, "Heading", "heading "), "  ", " ")}))
			ids = append(ids, id)
		}
	}
	at := -1
	if para != nil {
		for i, id := range ids {
			if id == para.PP.Style {
				at = i
			}
		}
	}
	a.dropdown("office/word/style", r, names, at, nil, func(i int) {
		d.SetStyle(ids[i])
		u.setFocus(wordFocus)
	})
}

// fontBox is the font picker.
func (e *wordEditor) fontBox(r gfx.Rect, rp *docx.RP) {
	a := e.a
	u := &a.u
	d := e.d
	current := rp.HAnsi
	if rp.East != "" && d.CaretIsEastAsian() {
		current = rp.East
	}
	options := append([]string(nil), wordFonts...)
	at := -1
	for i, f := range options {
		if strings.EqualFold(f, current) {
			at = i
		}
	}
	if at < 0 && current != "" {
		options = append(options, current)
		at = len(options) - 1
	}
	a.dropdown("office/word/font", r, options, at, map[int]bool{8: true}, func(i int) {
		f := options[i]
		d.FormatRuns(docx.RunChange{Family: &f})
		u.setFocus(wordFocus)
	})
}

// sizeBox is the size picker.
func (e *wordEditor) sizeBox(r gfx.Rect, rp *docx.RP) {
	a := e.a
	u := &a.u
	options := make([]string, len(wordSizes))
	at := -1
	for i, s := range wordSizes {
		options[i] = wordSize(s)
		if s == rp.Size {
			at = i
		}
	}
	if at < 0 {
		options = append(options, wordSize(rp.Size))
		at = len(options) - 1
	}
	a.dropdown("office/word/size", r, options, at, nil, func(i int) {
		if i < len(wordSizes) {
			s := wordSizes[i]
			e.d.FormatRuns(docx.RunChange{Size: &s})
		}
		u.setFocus(wordFocus)
	})
}

func wordSize(s float32) string {
	return strconv.FormatFloat(float64(s), 'f', -1, 32)
}

// lineMenu offers the usual line spacings.
func (e *wordEditor) lineMenu(anchor gfx.Rect) {
	var items []menuItem
	para := e.d.CaretPara()
	for _, m := range []float32{1, 1.15, 1.5, 2, 2.5, 3} {
		items = append(items, menuItem{label: wordSize(m), checked: para != nil && para.PP.Rule == flow.Multiple && para.PP.Line == m, run: func() { e.d.SetLineSpacing(m) }})
	}
	e.a.openMenu("office/word/lines", anchor, items)
}

// listMenu offers the kinds of numbering.
func (e *wordEditor) listMenu(anchor gfx.Rect) {
	u := &e.a.u
	kind, listed := e.d.CaretList()
	item := func(label string, k docx.ListKind) menuItem {
		return menuItem{label: label, checked: listed && kind == k, run: func() { e.d.ToggleList(k) }}
	}
	e.a.openMenu("office/word/numbers", anchor, []menuItem{
		item(u.t("1. 2. 3."), docx.ListNumber),
		item(u.t("1 1.1 1.1.1"), docx.ListMulti),
		item(u.t("一、二、三、"), docx.ListChinese),
	})
}

// headerMenu goes to the header or footer of the caret's page.
func (e *wordEditor) headerMenu(anchor gfx.Rect) {
	u := &e.a.u
	page := 0
	if s, ok := e.d.Locate(e.d.Sel.Caret); ok {
		page = s.Page
	}
	items := []menuItem{
		{label: u.t("Edit the header"), run: func() { e.enterHeader(page, "header") }},
		{label: u.t("Edit the footer"), run: func() { e.enterHeader(page, "footer") }},
	}
	if e.inHeader() {
		items = append(items, menuSep, menuItem{label: u.t("Back to the document"), key: "Esc", run: e.leaveHeader})
	}
	e.a.openMenu("office/word/headermenu", anchor, items)
}

func (e *wordEditor) openPop(kind string, at gfx.Rect) {
	if e.pop == kind {
		e.pop = ""
		return
	}
	e.pop, e.popAt = kind, at
	e.tableR, e.tableC = 0, 0
}

// drawPop draws the open popup over everything, and closes it on a press outside or Esc.
func (e *wordEditor) drawPop() {
	a := e.a
	u := &a.u
	cell := u.px(22)
	pad := u.px(8)
	var box gfx.Rect
	switch e.pop {
	case "table":
		box = gfx.Rect{X: e.popAt.X, Y: e.popAt.Bottom() + u.px(2), W: cell*10 + pad*2, H: cell*8 + pad*2 + u.px(22)}
	case "page":
		box = e.pagePanelBox()
	default:
		box = gfx.Rect{X: e.popAt.X, Y: e.popAt.Bottom() + u.px(2), W: cell*5 + pad*2, H: cell*4 + pad*2}
	}
	box.X = min(box.X, a.screen.W-box.W-u.px(4))
	u.c.Fill(gfx.Rect{X: box.X, Y: box.Y + u.px(3), W: box.W, H: box.H}, gfx.Alpha(colBlack, 0.35))
	u.c.Fill(box, colSurface)
	u.c.Outline(box, max(1, u.px(1)), colBorder)
	if e.pop == "table" {
		e.drawTablePop(box, cell, pad)
	} else if e.pop == "page" {
		e.drawPagePanel(box)
	} else {
		e.drawColorPop(box, cell, pad)
	}
	if e.pop != "" && u.pressed && !box.Contains(u.pressX, u.pressY) {
		e.pop = ""
		u.pressed = false
	}
	for _, k := range u.keys {
		if k.Code == keyEscape {
			e.pop = ""
		}
	}
}

// drawTablePop is the grid a table's size is pointed out on.
func (e *wordEditor) drawTablePop(box gfx.Rect, cell, pad float32) {
	u := &e.a.u
	if box.Contains(u.mx, u.my) {
		e.tableC = min(max(int((u.mx-box.X-pad)/cell)+1, 1), 10)
		e.tableR = min(max(int((u.my-box.Y-pad)/cell)+1, 1), 8)
	}
	for r := 0; r < 8; r++ {
		for c := 0; c < 10; c++ {
			sq := gfx.Rect{X: box.X + pad + float32(c)*cell, Y: box.Y + pad + float32(r)*cell, W: cell - u.px(2), H: cell - u.px(2)}
			if c < e.tableC && r < e.tableR {
				u.c.Fill(sq, colAccent)
			} else {
				u.c.Fill(sq, colSurfaceHi)
			}
			u.c.Outline(sq, max(1, u.px(1)), colBorderHi)
		}
	}
	label := u.t("Insert a table")
	if e.tableR > 0 {
		label = u.tf("%d × %d table", e.tableR, e.tableC)
	}
	u.label(u.face(12), gfx.Rect{X: box.X, Y: box.Bottom() - u.px(26), W: box.W, H: u.px(22)}, label, colDim, 0.5)
	if u.clicked("office/word/tablepop", box) && e.tableR > 0 {
		rows, cols := e.tableR, e.tableC
		e.pop = ""
		if err := e.d.InsertTable(rows, cols); err != nil {
			e.a.toast(e.a.describe(err), true)
		}
		e.follow = true
		u.setFocus(wordFocus)
	}
}

// drawColorPop is the palette of text colours or highlights; its first square is automatic, or none.
func (e *wordEditor) drawColorPop(box gfx.Rect, cell, pad float32) {
	u := &e.a.u
	highlight := e.pop == "highlight"
	count := len(wordSwatches)
	id := func(i int) string { return "office/word/swatch/" + strconv.Itoa(i) }
	for i := 0; i <= count; i++ {
		sq := gfx.Rect{X: box.X + pad + float32(i%5)*cell, Y: box.Y + pad + float32(i/5)*cell, W: cell - u.px(2), H: cell - u.px(2)}
		if i == 0 {
			u.c.Fill(sq, colSurfaceHi)
			u.icon(icWClose, sq, 13, colDim)
			u.hint(id(i), u.tc("word", "None"), sq)
		} else if highlight {
			u.c.Fill(sq, docx.HighlightColor(wordHighlights[i-1]))
		} else {
			u.c.Fill(sq, gfx.Hex(wordSwatches[i-1]))
		}
		u.c.Outline(sq, max(1, u.px(1)), colBorderHi)
		if u.hover(id(i), sq) {
			u.cursor = CursorHand
			u.c.Outline(sq.Inset(-max(1, u.px(1))), max(1, u.px(1)), colAccent)
		}
		if !u.clicked(id(i), sq) {
			continue
		}
		v := ""
		if highlight {
			if i > 0 {
				v = wordHighlights[i-1]
			}
			e.d.FormatRuns(docx.RunChange{Highlight: &v})
		} else {
			if i > 0 {
				v = strings.ToUpper(hex6(wordSwatches[i-1]))
			}
			e.d.FormatRuns(docx.RunChange{Color: &v})
		}
		e.pop = ""
		u.setFocus(wordFocus)
	}
}

func hex6(v uint32) string {
	s := strconv.FormatUint(uint64(v), 16)
	return strings.Repeat("0", 6-len(s)) + s
}

// wordFind is the find bar's state.
type wordFind struct {
	query, with string
	replace     bool
	matchCase   bool
	hits        []docx.Match
	at          int
	rev         uint64
	asked       string
	wasCase     bool
}

// openFind shows the find bar with the words chosen in it.
func (e *wordEditor) openFind(replace bool) {
	u := &e.a.u
	if e.find == nil {
		e.find = &wordFind{}
	}
	e.find.replace = e.find.replace || replace
	if s := e.d.SelectedText(); s != "" && !strings.ContainsAny(s, "\n\t") {
		e.find.query = s
	}
	e.find.asked = "\x00"
	u.focusEdit("office/word/findbox", e.find.query, false)
}

// search brings the hits up to date.
func (e *wordEditor) search() {
	f := e.find
	if f.query == f.asked && f.matchCase == f.wasCase && f.rev == e.d.Version() {
		return
	}
	f.hits = e.d.FindAll(f.query, f.matchCase)
	f.asked, f.wasCase, f.rev = f.query, f.matchCase, e.d.Version()
	f.at = min(f.at, max(len(f.hits)-1, 0))
}

// jump selects hit i.
func (e *wordEditor) jump(i int) {
	f := e.find
	if len(f.hits) == 0 {
		return
	}
	f.at = (i + len(f.hits)) % len(f.hits)
	m := f.hits[f.at]
	e.d.Sel = docx.Selection{Anchor: docx.Pos{B: m.B, Off: m.A}, Caret: docx.Pos{B: m.B, Off: m.C}}
	e.follow = true
}

// nextFrom is the first hit at or after the caret.
func (e *wordEditor) nextFrom() int {
	c, _ := e.d.Sel.Range()
	for i, m := range e.find.hits {
		if !e.d.Before(docx.Pos{B: m.B, Off: m.A}, c) {
			return i
		}
	}
	return 0
}

// drawFind draws the find bar and returns its height.
func (e *wordEditor) drawFind(r gfx.Rect) float32 {
	a := e.a
	u := &a.u
	f := e.find
	rowH := u.px(38)
	h := rowH
	if f.replace {
		h += rowH - u.px(6)
	}
	box := gfx.Rect{X: r.X, Y: r.Y, W: r.W, H: h}
	u.c.Fill(box, colSurface)
	u.c.Fill(gfx.Rect{X: r.X, Y: box.Bottom() - max(1, u.px(1)), W: r.W, H: max(1, u.px(1))}, colBorder)
	x := r.X + u.px(10)
	y := r.Y + u.px(5)
	field := gfx.Rect{X: x, Y: y, W: u.px(260), H: u.px(28)}
	held := u.keys
	changed, done := u.textBox("office/word/findbox", field, &f.query, u.t("Find"), false)
	e.search()
	if changed {
		f.at = e.nextFrom()
		e.jump(f.at)
	}
	if done {
		e.findKeys(held)
	}
	x += field.W + u.px(8)
	label := u.t("No results")
	if f.query == "" {
		label = ""
	} else if len(f.hits) > 0 {
		label = u.tf("%d of %d", f.at+1, len(f.hits))
	}
	u.label(u.face(12), gfx.Rect{X: x, Y: y, W: u.px(116), H: field.H}, label, colDim, 0)
	x += u.px(120)
	side := u.px(28)
	if u.iconButton("office/word/findprev", gfx.Rect{X: x, Y: y, W: side, H: side}, icWUp, u.t("Previous"), false, len(f.hits) > 0) {
		e.jump(f.at - 1)
	}
	x += side + u.px(2)
	if u.iconButton("office/word/findnext", gfx.Rect{X: x, Y: y, W: side, H: side}, icWDown, u.t("Next"), false, len(f.hits) > 0) {
		e.jump(f.at + 1)
	}
	x += side + u.px(10)
	w := checkboxWidth(u, u.t("Match case"))
	f.matchCase = u.checkbox("office/word/findcase", gfx.Rect{X: x, Y: y, W: w, H: side}, u.t("Match case"), f.matchCase)
	x += w + u.px(8)
	w = u.buttonWidth("", u.t("Replace…"))
	if !f.replace && u.button("office/word/findmore", gfx.Rect{X: x, Y: y, W: w, H: side}, "", u.t("Replace…"), ghost, true) {
		f.replace = true
	}
	if u.iconButton("office/word/findclose", gfx.Rect{X: r.Right() - u.px(36), Y: y, W: side, H: side}, icWClose, u.t("Close"), false, true) {
		e.closeFind()
		return h
	}
	if f.replace {
		y += rowH - u.px(6)
		x = r.X + u.px(10)
		field.Y = y
		u.textBox("office/word/replacebox", field, &f.with, u.t("Replace with"), false)
		x += field.W + u.px(8)
		w = u.buttonWidth("", u.t("Replace"))
		if u.button("office/word/replaceone", gfx.Rect{X: x, Y: y, W: w, H: side}, "", u.t("Replace"), plain, len(f.hits) > 0) {
			e.replaceOne()
		}
		x += w + u.px(6)
		w = u.buttonWidth("", u.t("Replace all"))
		if u.button("office/word/replaceall", gfx.Rect{X: x, Y: y, W: w, H: side}, "", u.t("Replace all"), plain, len(f.hits) > 0) {
			n := e.d.ReplaceAll(f.query, f.with, f.matchCase)
			a.toast(u.tf("%d replaced", n), false)
		}
	}
	return h
}

// findKeys is what ended the find box's typing: Enter goes to the next hit and keeps it open, Esc closes.
func (e *wordEditor) findKeys(keys []Key) {
	u := &e.a.u
	f := e.find
	for _, k := range keys {
		switch k.Code {
		case keyEnter:
			if k.Shift {
				e.jump(f.at - 1)
			} else {
				e.jump(f.at + 1)
			}
			u.focusEdit("office/word/findbox", f.query, false)
			u.edit.anchor = u.edit.caret
		case keyEscape:
			e.closeFind()
		}
	}
}

func (e *wordEditor) closeFind() {
	e.find = nil
	e.a.u.setFocus(wordFocus)
}

// replaceOne replaces the current hit when it is what is chosen, then goes to the next.
func (e *wordEditor) replaceOne() {
	f := e.find
	if len(f.hits) == 0 {
		return
	}
	m := f.hits[f.at]
	a, c := e.d.Sel.Range()
	if a.B == m.B && a.Off == m.A && c.Off == m.C {
		e.d.ReplaceMatch(m, f.with)
		e.search()
		if len(f.hits) > 0 {
			e.jump(e.nextFrom())
		}
		return
	}
	e.jump(f.at)
}

// wordLink is the link bar's state.
type wordLink struct{ url, text string }

// openLink shows the link bar for the selection, or the link at the caret.
func (e *wordEditor) openLink() {
	u := &e.a.u
	e.link = &wordLink{url: e.d.LinkAt(e.d.Sel.Caret)}
	u.focusEdit("office/word/linkbox", e.link.url, false)
}

// drawLink draws the link bar and returns its height.
func (e *wordEditor) drawLink(r gfx.Rect) float32 {
	a := e.a
	u := &a.u
	l := e.link
	h := u.px(38)
	box := gfx.Rect{X: r.X, Y: r.Y, W: r.W, H: h}
	u.c.Fill(box, colSurface)
	u.c.Fill(gfx.Rect{X: r.X, Y: box.Bottom() - max(1, u.px(1)), W: r.W, H: max(1, u.px(1))}, colBorder)
	x := r.X + u.px(10)
	y := r.Y + u.px(5)
	field := gfx.Rect{X: x, Y: y, W: u.px(420), H: u.px(28)}
	held := u.keys
	_, done := u.textBox("office/word/linkbox", field, &l.url, u.t("Address, such as https://example.com"), false)
	x += field.W + u.px(8)
	apply := func(url string) {
		if err := e.d.SetLink(url, ""); err != nil {
			a.toast(a.describe(err), true)
		}
		e.link = nil
		u.setFocus(wordFocus)
	}
	enter := false
	if done {
		for _, k := range held {
			enter = enter || k.Code == keyEnter
		}
		if !enter {
			e.link = nil
			u.setFocus(wordFocus)
			return h
		}
	}
	w := u.buttonWidth("", u.t("Apply"))
	if u.button("office/word/linkok", gfx.Rect{X: x, Y: y, W: w, H: u.px(28)}, "", u.t("Apply"), primary, strings.TrimSpace(l.url) != "") || enter && strings.TrimSpace(l.url) != "" {
		apply(strings.TrimSpace(l.url))
		return h
	}
	x += w + u.px(6)
	w = u.buttonWidth("", u.t("Remove link"))
	if u.button("office/word/linkoff", gfx.Rect{X: x, Y: y, W: w, H: u.px(28)}, "", u.t("Remove link"), plain, e.d.LinkAt(e.d.Sel.Caret) != "") {
		apply("")
		return h
	}
	if u.iconButton("office/word/linkclose", gfx.Rect{X: r.Right() - u.px(36), Y: y, W: u.px(28), H: u.px(28)}, icWClose, u.t("Close"), false, true) {
		e.link = nil
		u.setFocus(wordFocus)
	}
	return h
}
