package ui

import (
	"fmt"
	"image/color"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/btelolabs/btelo-forge/internal/gfx"
	"github.com/btelolabs/btelo-forge/internal/office/formula"
	"github.com/btelolabs/btelo-forge/internal/office/xlsx"
)

// The bars around the grid: formats on top, then the formula bar; sheet tabs, the selection's sum and the zoom below.
// Pop-ups for colours, filter lists and sizes float over them.

// Symbols of the tool bar, by their Material Icons code points.
const (
	icSheetBold      = ""
	icSheetItalic    = ""
	icSheetUnder     = ""
	icSheetStrike    = ""
	icSheetInk       = "" // format_color_text
	icSheetFill      = ""
	icSheetBorder    = ""
	icSheetTop       = ""
	icSheetMiddle    = ""
	icSheetBottom    = ""
	icSheetWrap      = ""
	icSheetMerge     = "" // call_merge
	icSheetFilter    = ""
	icSheetFreeze    = "" // ac_unit
	icSheetFind      = "" // find_replace
	icSheetFunction  = ""
	icSheetDown      = "" // arrow_drop_down
	icSheetSortAZ    = ""
	icSheetChecked   = ""
	icSheetUnchecked = ""
	icSheetMoney     = ""
)

// sheetFind is the find bar.
type sheetFind struct {
	open      bool
	replace   bool
	what      string
	with      string
	matchCase bool
	matches   []xlsx.Match
	at        int
	bar       gfx.Rect
}

// What a pop-up is for.
type sheetPopKind int

const (
	sheetPopInk sheetPopKind = iota
	sheetPopFill
	sheetPopTab
	sheetPopFilter
	sheetPopSize
)

// sheetPop is the floating panel.
type sheetPop struct {
	kind    sheetPopKind
	at      gfx.Rect
	opened  bool
	col     int // a filter's column
	values  []string
	chosen  map[string]bool
	blanks  bool
	hasNone bool
	scroll  float32
	cols    bool // a size pop-up is for columns
	text    string
	sheet   *xlsx.Sheet
}

func (ed *sheetEd) popRect() gfx.Rect {
	u := &ed.a.u
	p := ed.pop
	w, h := u.px(238), u.px(214)
	switch p.kind {
	case sheetPopFilter:
		w, h = u.px(240), u.px(326)
	case sheetPopSize:
		w, h = u.px(220), u.px(84)
	}
	x := min(p.at.X, ed.a.screen.W-w-u.px(4))
	y := p.at.Bottom() + u.px(2)
	if y+h > ed.a.screen.H {
		y = max(p.at.Y-h-u.px(2), u.px(4))
	}
	return gfx.Rect{X: x, Y: y, W: w, H: h}
}

// sheetFormats are the number formats on offer.
var sheetFormats = []struct{ label, code string }{
	{msgs("General")[0], "General"},
	{msgs("Number")[0], "0.00"},
	{msgs("Thousands")[0], "#,##0.00"},
	{msgs("Currency")[0], `"$"#,##0.00`},
	{msgs("Yuan")[0], `"¥"#,##0.00`},
	{msgs("Accounting")[0], `_("$"* #,##0.00_);_("$"* \(#,##0.00\);_("$"* "-"??_);_(@_)`},
	{msgs("Percent")[0], "0.00%"},
	{msgs("Scientific")[0], "0.00E+00"},
	{msgs("Fraction")[0], "# ?/?"},
	{msgs("Date")[0], "m/d/yyyy"},
	{msgs("Long date")[0], "yyyy年m月d日"},
	{msgs("Time")[0], "h:mm:ss"},
	{msgs("Date and time")[0], "m/d/yyyy h:mm"},
	{msgs("Text")[0], "@"},
}

var sheetFonts = []string{"Calibri", "Arial", "Times New Roman", "Cambria", "Consolas", "Courier New", "Georgia", "Tahoma", "Verdana", "等线", "微软雅黑", "宋体", "黑体", "楷体"}

var sheetSizes = []float64{8, 9, 10, 10.5, 11, 12, 14, 16, 18, 20, 24, 28, 36, 48, 72}

// flowButton lays one tool bar icon.
func (ed *sheetEd) tool(x *float32, id, glyph, hint string, on bool) bool {
	u := &ed.a.u
	b := ed.body.tools
	s := u.px(28)
	r := gfx.Rect{X: *x, Y: b.Y + (b.H-s)/2, W: s, H: s}
	*x += s + u.px(1)
	return u.iconButton("sheet/tool/"+id, r, glyph, hint, on, true)
}

// dropTool is a button showing a value, which opens a menu.
func (ed *sheetEd) dropTool(x *float32, id, text, hint string, w float32, items func() []menuItem) {
	a := ed.a
	u := &a.u
	b := ed.body.tools
	r := gfx.Rect{X: *x, Y: b.Y + (b.H-u.px(28))/2, W: u.px(w), H: u.px(28)}
	*x += r.W + u.px(4)
	mid := "sheet/menu/" + id
	hot := u.hover(mid, r)
	bg := colSurface
	if hot || a.menuOpen(mid) {
		bg = colSurfaceHi
		u.cursor = CursorHand
	}
	u.c.Fill(r, bg)
	u.label(u.face(12.5), gfx.Rect{X: r.X + u.px(8), Y: r.Y, W: r.W - u.px(26), H: r.H}, text, colText, 0)
	u.icon(icSheetDown, gfx.Rect{X: r.Right() - u.px(22), Y: r.Y, W: u.px(20), H: r.H}, 16, colDim)
	u.hint(mid, hint, r)
	if u.clicked(mid, r) {
		a.openMenu(mid, r, items())
	}
}

func (ed *sheetEd) sep(x *float32) {
	u := &ed.a.u
	b := ed.body.tools
	*x += u.px(4)
	u.c.Fill(gfx.Rect{X: *x, Y: b.Y + u.px(9), W: max(1, u.px(1)), H: b.H - u.px(18)}, colBorderHi)
	*x += u.px(5)
}

func (ed *sheetEd) drawBars(v *sheetView) {
	a := ed.a
	u := &a.u
	b := &ed.body
	u.c.Fill(b.tools, colSurface)
	u.c.Push(b.tools)
	st := ed.activeStyle(v)
	x := b.tools.X + u.px(8)
	if ed.tool(&x, "print", icPrint, u.t("Print (Ctrl+P)"), false) {
		ed.a.printOffice(ed.od)
	}
	x += u.px(6)
	ed.dropTool(&x, "font", st.Font, u.t("Font"), 130, func() []menuItem {
		items := make([]menuItem, len(sheetFonts))
		for i, f := range sheetFonts {
			items[i] = menuItem{label: f, checked: f == st.Font, run: func() { ed.format(&xlsx.Change{Font: &f}) }}
		}
		return items
	})
	ed.dropTool(&x, "size", strconv.FormatFloat(st.Size, 'f', -1, 64), u.t("Font size"), 58, func() []menuItem {
		items := make([]menuItem, len(sheetSizes))
		for i, s := range sheetSizes {
			items[i] = menuItem{label: strconv.FormatFloat(s, 'f', -1, 64), checked: s == st.Size, run: func() { ed.format(&xlsx.Change{Size: &s}) }}
		}
		return items
	})
	flag := func(id, glyph, hint string, on bool, set func(*xlsx.Change, *bool)) {
		if ed.tool(&x, id, glyph, hint, on) {
			next := !on
			var ch xlsx.Change
			set(&ch, &next)
			ed.format(&ch)
		}
	}
	flag("bold", icSheetBold, u.t("Bold")+" (Ctrl+B)", st.Bold, func(c *xlsx.Change, b *bool) { c.Bold = b })
	flag("italic", icSheetItalic, u.t("Italic")+" (Ctrl+I)", st.Italic, func(c *xlsx.Change, b *bool) { c.Italic = b })
	flag("under", icSheetUnder, u.t("Underline")+" (Ctrl+U)", st.Underline, func(c *xlsx.Change, b *bool) { c.Underline = b })
	flag("strike", icSheetStrike, u.t("Strikethrough"), st.Strike, func(c *xlsx.Change, b *bool) { c.Strike = b })
	popAt := func() gfx.Rect { return gfx.Rect{X: x - u.px(29), Y: b.tools.Y, W: u.px(28), H: b.tools.H} }
	if ed.tool(&x, "ink", icSheetInk, u.t("Text colour"), false) {
		ed.togglePop(&sheetPop{kind: sheetPopInk, at: popAt()})
	}
	ed.swatch(x-u.px(29), hexRGBA(st.Color, colBlack))
	if ed.tool(&x, "fill", icSheetFill, u.t("Fill colour"), false) {
		ed.togglePop(&sheetPop{kind: sheetPopFill, at: popAt()})
	}
	ed.swatch(x-u.px(29), hexRGBA(st.Fill, colWhite))
	if ed.tool(&x, "border", icSheetBorder, u.t("Borders"), false) {
		a.openMenu("sheet/borders", popAt(), ed.borderItems(v))
	}
	ed.sep(&x)
	align := func(id, glyph, hint, value string, cur string, vertical bool) {
		if ed.tool(&x, id, glyph, hint, cur == value) {
			next := value
			if cur == value {
				next = ""
			}
			if vertical {
				ed.format(&xlsx.Change{VAlign: &next})
			} else {
				ed.format(&xlsx.Change{HAlign: &next})
			}
		}
	}
	align("left", icAlignLeft, u.t("Align left"), "left", st.HAlign, false)
	align("center", icAlignMid, u.t("Centre"), "center", st.HAlign, false)
	align("right", icAlignRight, u.t("Align right"), "right", st.HAlign, false)
	align("top", icSheetTop, u.t("Align top"), "top", st.VAlign, true)
	align("middle", icSheetMiddle, u.t("Align middle"), "center", st.VAlign, true)
	align("bottom", icSheetBottom, u.t("Align bottom"), "bottom", st.VAlign, true)
	if ed.tool(&x, "wrap", icSheetWrap, u.t("Wrap text"), st.Wrap) {
		next := !st.Wrap
		ed.format(&xlsx.Change{Wrap: &next})
	}
	merged := false
	if _, ok := v.s.MergeAt(v.active[0], v.active[1]); ok {
		merged = true
	}
	if ed.tool(&x, "merge", icSheetMerge, u.t("Merge and centre"), merged) {
		ed.toggleMerge(v, merged)
	}
	ed.sep(&x)
	label := st.NumFmt
	for _, f := range sheetFormats {
		if f.code == st.NumFmt {
			label = u.t(f.label)
		}
	}
	ed.dropTool(&x, "numfmt", label, u.t("Number format"), 112, func() []menuItem {
		items := make([]menuItem, len(sheetFormats))
		for i, f := range sheetFormats {
			items[i] = menuItem{label: u.t(f.label), checked: f.code == st.NumFmt, run: func() { ed.format(&xlsx.Change{NumFmt: &f.code}) }}
		}
		return items
	})
	if ed.tool(&x, "money", icSheetMoney, u.t("Currency"), false) {
		code := `"$"#,##0.00`
		ed.format(&xlsx.Change{NumFmt: &code})
	}
	if ed.textTool(&x, "percent", "%", u.t("Percent")) {
		code := "0%"
		ed.format(&xlsx.Change{NumFmt: &code})
	}
	if ed.textTool(&x, "more", ".0+", u.t("More decimals")) {
		code := moreDecimals(st.NumFmt, 1)
		ed.format(&xlsx.Change{NumFmt: &code})
	}
	if ed.textTool(&x, "less", ".0-", u.t("Fewer decimals")) {
		code := moreDecimals(st.NumFmt, -1)
		ed.format(&xlsx.Change{NumFmt: &code})
	}
	ed.sep(&x)
	if ed.tool(&x, "sort", icSheetSortAZ, u.t("Sort"), false) {
		a.openMenu("sheet/sort", popAt(), []menuItem{
			{label: u.t("Sort A to Z"), run: func() { ed.sortBy(v, false) }},
			{label: u.t("Sort Z to A"), run: func() { ed.sortBy(v, true) }},
		})
	}
	_, filtered := v.s.AutoFilter()
	if ed.tool(&x, "filter", icSheetFilter, u.t("Filter"), filtered) {
		ed.toggleFilter(v)
	}
	if ed.tool(&x, "freeze", icSheetFreeze, u.t("Freeze panes"), v.frozenC > 0 || v.frozenR > 0) {
		r, c := v.active[0], v.active[1]
		a.openMenu("sheet/freeze", popAt(), []menuItem{
			{label: u.tf("Freeze at %s", formula.CellName(r, c)), run: func() { ed.freezeAt(v, r, c) }},
			{label: u.t("Freeze top row"), run: func() { ed.freezeAt(v, 1, 0) }},
			{label: u.t("Freeze first column"), run: func() { ed.freezeAt(v, 0, 1) }},
			{label: u.t("Unfreeze"), disabled: v.frozenC == 0 && v.frozenR == 0, run: func() { ed.freezeAt(v, 0, 0) }},
		})
	}
	if ed.tool(&x, "find", icSheetFind, u.t("Find and replace")+" (Ctrl+F)", ed.find.open) {
		if ed.find.open {
			ed.find.open = false
		} else {
			ed.openFind(false)
		}
	}
	u.c.Pop()
	if ed.find.open {
		ed.drawFind(v)
	}
	ed.drawFormulaBar(v)
}

// swatch underlines a colour tool with its colour.
func (ed *sheetEd) swatch(x float32, col color.RGBA) {
	u := &ed.a.u
	b := ed.body.tools
	u.c.Fill(gfx.Rect{X: x + u.px(6), Y: b.Y + b.H/2 + u.px(9), W: u.px(16), H: u.px(3)}, col)
}

func (ed *sheetEd) textTool(x *float32, id, text, hint string) bool {
	u := &ed.a.u
	b := ed.body.tools
	f := u.face(12.5)
	r := gfx.Rect{X: *x, Y: b.Y + (b.H-u.px(28))/2, W: f.Measure(text) + u.px(12), H: u.px(28)}
	*x += r.W + u.px(1)
	tid := "sheet/tool/" + id
	hot := u.hover(tid, r)
	if hot {
		u.c.Fill(r, colSurfaceHi)
		u.cursor = CursorHand
	}
	col := colDim
	if hot {
		col = colText
	}
	u.label(f, r, text, col, 0.5)
	u.hint(tid, hint, r)
	return u.clicked(tid, r)
}

var reDecimals = regexp.MustCompile(`[0#?](\.[0#?]*)?`)

// moreDecimals is a number format with d more digits after the point in each section with digits.
func moreDecimals(code string, d int) string {
	if code == "General" || code == "" {
		if d > 0 {
			return "0.0"
		}
		return "0"
	}
	if code == "@" {
		return code
	}
	sections := splitSections(code)
	for i, s := range sections {
		// The last run of digit places is the one with the point.
		spots := reDecimals.FindAllStringIndex(s, -1)
		if spots == nil {
			continue
		}
		last := spots[len(spots)-1]
		digits := s[last[0]:last[1]]
		whole, frac, _ := strings.Cut(digits, ".")
		n := max(len(frac)+d, 0)
		next := whole
		if n > 0 {
			next += "." + strings.Repeat("0", n)
		}
		sections[i] = s[:last[0]] + next + s[last[1]:]
	}
	return strings.Join(sections, ";")
}

// splitSections cuts a format at its semicolons, outside quotes.
func splitSections(code string) []string {
	var out []string
	start, quoted := 0, false
	for i := 0; i < len(code); i++ {
		switch code[i] {
		case '"':
			quoted = !quoted
		case '\\':
			i++
		case ';':
			if !quoted {
				out = append(out, code[start:i])
				start = i + 1
			}
		}
	}
	return append(out, code[start:])
}

func (ed *sheetEd) borderItems(v *sheetView) []menuItem {
	u := &ed.a.u
	line := xlsx.Edge{Style: "thin"}
	thick := xlsx.Edge{Style: "medium"}
	none := xlsx.Edge{}
	all := func() {
		ed.format(&xlsx.Change{Left: &line, Right: &line, Top: &line, Bottom: &line})
	}
	outer := func(e xlsx.Edge) func() {
		return func() {
			ed.finishEdit()
			ed.wb.Begin()
			for _, a := range v.sel {
				v.s.ApplyStyle([]formula.Area{{R1: a.R1, C1: a.C1, R2: a.R1, C2: a.C2}}, &xlsx.Change{Top: &e})
				v.s.ApplyStyle([]formula.Area{{R1: a.R2, C1: a.C1, R2: a.R2, C2: a.C2}}, &xlsx.Change{Bottom: &e})
				v.s.ApplyStyle([]formula.Area{{R1: a.R1, C1: a.C1, R2: a.R2, C2: a.C1}}, &xlsx.Change{Left: &e})
				v.s.ApplyStyle([]formula.Area{{R1: a.R1, C1: a.C2, R2: a.R2, C2: a.C2}}, &xlsx.Change{Right: &e})
			}
		}
	}
	side := func(set func(*xlsx.Change)) func() {
		return func() {
			var ch xlsx.Change
			set(&ch)
			ed.format(&ch)
		}
	}
	return []menuItem{
		{label: u.t("All borders"), run: all},
		{label: u.t("Outside borders"), run: outer(line)},
		{label: u.t("Thick outside borders"), run: outer(thick)},
		menuSep,
		{label: u.t("Top border"), run: side(func(c *xlsx.Change) { c.Top = &line })},
		{label: u.t("Bottom border"), run: side(func(c *xlsx.Change) { c.Bottom = &line })},
		{label: u.t("Left border"), run: side(func(c *xlsx.Change) { c.Left = &line })},
		{label: u.t("Right border"), run: side(func(c *xlsx.Change) { c.Right = &line })},
		menuSep,
		{label: u.t("No borders"), run: side(func(c *xlsx.Change) { c.Left, c.Right, c.Top, c.Bottom = &none, &none, &none, &none })},
	}
}

func (ed *sheetEd) toggleMerge(v *sheetView, merged bool) {
	ed.finishEdit()
	ed.wb.Begin()
	if merged {
		for _, a := range v.sel {
			v.s.Unmerge(a)
		}
		return
	}
	center := "center"
	for _, a := range v.sel {
		if err := v.s.Merge(a); err != nil {
			ed.a.toast(err.Error(), true)
			return
		}
	}
	v.s.ApplyStyle(v.sel[:len(v.sel):len(v.sel)], &xlsx.Change{HAlign: &center})
	ed.noteAfterEdit()
}

// drawFormulaBar draws the name box and the words of the active cell.
func (ed *sheetEd) drawFormulaBar(v *sheetView) {
	a := ed.a
	u := &a.u
	b := &ed.body
	r := b.bar
	u.c.Fill(r, colPanel)
	u.c.Fill(gfx.Rect{X: r.X, Y: r.Bottom() - 1, W: r.W, H: 1}, colBorder)
	name := gfx.Rect{X: r.X + u.px(6), Y: r.Y + u.px(3), W: u.px(96), H: r.H - u.px(6)}
	if u.focus == "sheet/name" {
		if _, done := u.inlineBox("sheet/name", gfx.Rect{X: name.X + u.px(8), Y: name.Y, W: name.W - u.px(8), H: name.H}, &ed.nameBox, 12.5, u.px(8)); done {
			ed.goTo(v, ed.nameBox)
		}
	} else {
		u.c.Fill(name, colSurface)
		text := formula.CellName(v.active[0], v.active[1])
		if a := v.area(); a.R1 != a.R2 || a.C1 != a.C2 {
			text = formula.AreaName(a)
		}
		u.label(u.face(12.5), gfx.Rect{X: name.X + u.px(8), Y: name.Y, W: name.W - u.px(12), H: name.H}, text, colText, 0)
		if u.hover("sheet/namebox", name) {
			u.cursor = CursorText
		}
		if u.clicked("sheet/namebox", name) {
			ed.finishEdit()
			ed.nameBox = text
			u.focusEdit("sheet/name", text, false)
		}
	}
	u.icon(icSheetFunction, gfx.Rect{X: name.Right() + u.px(4), Y: r.Y, W: u.px(24), H: r.H}, 16, colDim)
	box := gfx.Rect{X: name.Right() + u.px(32), Y: r.Y + u.px(3), W: r.Right() - name.Right() - u.px(38), H: r.H - u.px(6)}
	u.c.Fill(box, colSurface)
	f := u.face(13)
	x0 := box.X + u.px(8)
	y0 := box.Y + (box.H-f.Height())/2
	u.c.Push(box)
	defer u.c.Pop()
	if !ed.editing {
		text := strings.ReplaceAll(v.s.InputText(v.active[0], v.active[1]), "\n", " ")
		u.c.Text(f, x0, y0, text, colText)
		if u.hover("sheet/formula", box) {
			u.cursor = CursorText
		}
		if u.press("sheet/formula", box) {
			ed.startEdit(v, v.active[0], v.active[1], v.s.InputText(v.active[0], v.active[1]), false)
			ed.inBar = true
			u.edit.caret = barCaret(f, []rune(string(u.edit.text)), u.pressX-x0)
			u.edit.anchor = u.edit.caret
		}
		u.letGo("sheet/formula")
		return
	}
	// The same words as the cell's box, one line, scrolled to the caret.
	text := strings.ReplaceAll(string(u.edit.text), "\n", " ")
	rs := []rune(text)
	cx := f.Measure(string(rs[:min(u.edit.caret, len(rs))]))
	room := box.W - u.px(16)
	if cx > room {
		x0 -= cx - room
	}
	if !ed.inBar && u.press("sheet/formula", box) {
		ed.inBar = true
		ed.typing = false
		u.edit.caret = barCaret(f, rs, u.pressX-x0)
		u.edit.anchor = u.edit.caret
	}
	if ed.inBar && u.press("sheet/formula", box) {
		u.edit.caret = barCaret(f, rs, u.pressX-x0)
		u.edit.anchor = u.edit.caret
	}
	u.letGo("sheet/formula")
	if sa, sb := u.edit.selection(); sa < sb {
		x1, x2 := x0+f.Measure(string(rs[:sa])), x0+f.Measure(string(rs[:min(sb, len(rs))]))
		u.c.Fill(gfx.Rect{X: x1, Y: y0, W: x2 - x1, H: f.Height()}, gfx.Alpha(colAccent, 0.45))
	}
	u.c.Text(f, x0, y0, text, colText)
	if ed.inBar {
		u.drawCaret(x0+cx, y0, f.Height(), false)
	}
}

func barCaret(f *gfx.Face, rs []rune, dx float32) int {
	for i := range rs {
		if dx < (f.Measure(string(rs[:i]))+f.Measure(string(rs[:i+1])))/2 {
			return i
		}
	}
	return len(rs)
}

// goTo selects what is typed in the name box: an address or range, with a sheet or a defined name.
func (ed *sheetEd) goTo(v *sheetView, text string) {
	u := &ed.a.u
	u.focus = sheetFocus
	text = strings.TrimSpace(text)
	if t := ed.wb.Name(text, ed.cur); t != "" {
		text = t
	}
	if sheet, rest, ok := strings.Cut(text, "!"); ok {
		if i := ed.wb.SheetIndex(strings.Trim(sheet, "'")); i >= 0 {
			ed.cur = i
			v = ed.view()
			text = rest
		}
	}
	a, ok := formula.ParseArea(strings.ReplaceAll(text, "$", ""))
	if !ok {
		ed.a.toast(u.tf("%s is not a cell or a name.", text), true)
		return
	}
	v.selectArea(a)
	ed.reveal(v, int(a.R1), int(a.C1))
}

func (ed *sheetEd) openFind(replace bool) {
	u := &ed.a.u
	ed.find.open, ed.find.replace = true, replace
	u.focusEdit("sheet/find/what", ed.find.what, false)
}

func (ed *sheetEd) drawFind(v *sheetView) {
	a := ed.a
	u := &a.u
	fd := &ed.find
	r := fd.bar
	u.c.Fill(r, colSurface)
	u.c.Fill(gfx.Rect{X: r.X, Y: r.Y, W: r.W, H: 1}, colBorder)
	x := r.X + u.px(8)
	h := u.px(26)
	y := r.Y + (r.H-h)/2
	box := gfx.Rect{X: x, Y: y, W: u.px(200), H: h}
	cancel := slices.ContainsFunc(u.keys, func(k Key) bool { return k.Code == keyEscape })
	changed, done := u.textBox("sheet/find/what", box, &fd.what, u.t("Find"), false)
	if changed {
		fd.matches, fd.at = nil, 0
	}
	x = box.Right() + u.px(6)
	switch {
	case done && cancel:
		fd.open = false
		u.focus = sheetFocus
		return
	case done && !u.down:
		// Enter finds, and stays here to find again.
		ed.findNext(v, 1)
		u.setFocus("sheet/find/what")
	case done:
		u.focus = sheetFocus
	}
	if fd.replace {
		box = gfx.Rect{X: x, Y: y, W: u.px(200), H: h}
		u.textBox("sheet/find/with", box, &fd.with, u.t("Replace with"), false)
		x = box.Right() + u.px(6)
	}
	btn := func(id, text string, kind buttonKind) bool {
		w := u.buttonWidth("", text)
		rr := gfx.Rect{X: x, Y: y, W: w, H: h}
		x += w + u.px(4)
		return u.button("sheet/find/"+id, rr, "", text, kind, true)
	}
	if btn("next", u.t("Find next"), plain) {
		ed.findNext(v, 1)
	}
	if btn("prev", u.t("Previous"), plain) {
		ed.findNext(v, -1)
	}
	if fd.replace {
		if btn("one", u.t("Replace"), plain) {
			ed.replaceOne(v)
		}
		if btn("all", u.t("Replace all"), plain) {
			ed.replaceAll(v)
		}
	}
	fd.matchCase = u.checkbox("sheet/find/case", gfx.Rect{X: x + u.px(4), Y: y, W: checkboxWidth(u, u.t("Match case")), H: h}, u.t("Match case"), fd.matchCase)
	x += checkboxWidth(u, u.t("Match case")) + u.px(10)
	if !fd.replace {
		if btn("more", u.t("Replace…"), ghost) {
			fd.replace = true
		}
	}
	if len(fd.matches) > 0 {
		u.label(u.face(12), gfx.Rect{X: x, Y: y, W: u.px(120), H: h}, u.tf("%d of %d", fd.at+1, len(fd.matches)), colDim, 0)
	}
	if u.iconButton("sheet/find/close", gfx.Rect{X: r.Right() - u.px(32), Y: y, W: h, H: h}, icClose, u.t("Close"), false, true) {
		fd.open = false
		u.focus = sheetFocus
	}
}

const sheetFindLimit = 100000

func (ed *sheetEd) searchAll(v *sheetView) {
	fd := &ed.find
	fd.matches = v.s.Find(fd.what, formula.Area{R2: xlsx.MaxRows - 1, C2: xlsx.MaxCols - 1}, fd.matchCase, false, sheetFindLimit)
}

func (ed *sheetEd) findNext(v *sheetView, dir int) {
	fd := &ed.find
	if fd.what == "" {
		return
	}
	if fd.matches == nil {
		ed.searchAll(v)
		fd.at = -dir
		// From the active cell on, as searching does.
		for i, m := range fd.matches {
			if m.Row > v.active[0] || m.Row == v.active[0] && m.Col >= v.active[1] {
				fd.at = i - dir
				break
			}
		}
	}
	if len(fd.matches) == 0 {
		ed.a.toast(ed.a.u.tf("%s was not found.", fd.what), false)
		return
	}
	fd.at = (fd.at + dir + len(fd.matches)) % len(fd.matches)
	m := fd.matches[fd.at]
	v.moveTo(m.Row, m.Col)
	ed.reveal(v, m.Row, m.Col)
}

func (ed *sheetEd) replaceOne(v *sheetView) {
	fd := &ed.find
	if len(fd.matches) == 0 || fd.at < 0 {
		ed.findNext(v, 1)
		return
	}
	m := fd.matches[fd.at]
	ed.wb.Begin()
	if _, err := v.s.ReplaceIn(m.Row, m.Col, fd.what, fd.with, fd.matchCase); err != nil {
		ed.a.toast(err.Error(), true)
	}
	ed.noteAfterEdit()
	fd.matches = nil
	ed.findNext(v, 1)
}

func (ed *sheetEd) replaceAll(v *sheetView) {
	fd := &ed.find
	if fd.what == "" {
		return
	}
	ed.searchAll(v)
	ed.wb.Begin()
	n := 0
	for _, m := range fd.matches {
		if ok, err := v.s.ReplaceIn(m.Row, m.Col, fd.what, fd.with, fd.matchCase); err == nil && ok {
			n++
		}
	}
	fd.matches = nil
	ed.noteAfterEdit()
	ed.a.toast(ed.a.u.tf("%d cells changed.", n), false)
}

// drawTabs draws the sheet tabs.
func (ed *sheetEd) drawTabs() {
	a := ed.a
	u := &a.u
	r := ed.body.tabs
	u.c.Fill(r, colSurface)
	w := r.W * 0.58
	strip := gfx.Rect{X: r.X, Y: r.Y, W: w, H: r.H}
	u.c.Push(strip)
	defer u.c.Pop()
	x := r.X + u.px(6)
	f := u.face(12.5)
	for i, s := range ed.wb.Sheets {
		if s.Hidden() {
			continue
		}
		tw := f.Measure(s.Name) + u.px(24)
		box := gfx.Rect{X: x, Y: r.Y + u.px(2), W: tw, H: r.H - u.px(4)}
		x += tw + u.px(2)
		id := "sheet/tab/" + s.Name
		bg := colSurface
		switch {
		case i == ed.cur:
			bg = colPanel
		case u.hover(id, box):
			bg = colSurfaceHi
			u.cursor = CursorHand
		}
		u.c.Fill(box, bg)
		if t := s.TabColor(); t != "" {
			u.c.Fill(gfx.Rect{X: box.X, Y: box.Bottom() - u.px(3), W: box.W, H: u.px(3)}, hexRGBA(t, colAccent))
		}
		if ed.rename == i {
			if _, done := u.inlineBox("sheet/rename", gfx.Rect{X: box.X + u.px(8), Y: box.Y, W: box.W - u.px(10), H: box.H}, &ed.renameTo, 12.5, u.px(4)); done {
				ed.rename = -1
				u.focus = sheetFocus
				ed.structure(func() error { return ed.wb.RenameSheet(s, strings.TrimSpace(ed.renameTo)) })
			}
			continue
		}
		col := colDim
		if i == ed.cur {
			col = colText
		}
		u.label(f, box, s.Name, col, 0.5)
		if at := u.rightAt; at != nil && box.Contains(at.X, at.Y) && !u.blocked {
			ed.tabMenu(i, s, box)
		}
		if u.press(id, box) {
			if !ed.pointing() {
				ed.finishEdit()
			}
			if u.double {
				ed.startRename(i, s)
			}
			ed.cur = i
			ed.tabDrag = i
		}
		if u.active == id {
			if u.dragging(id) && u.drag.moved {
				u.cursor = CursorMove
				if at := ed.tabAt(u.mx); at >= 0 && at != ed.tabDrag {
					ed.tabDrag = at
				}
			}
			if u.letGo(id) && ed.tabDrag != i {
				to := ed.tabDrag
				ed.structure(func() error {
					ed.wb.MoveSheet(s, to)
					ed.cur = to
					return nil
				})
			}
		}
	}
	add := gfx.Rect{X: x + u.px(2), Y: r.Y + u.px(3), W: r.H - u.px(6), H: r.H - u.px(6)}
	if u.iconButton("sheet/tab/add", add, icAdd, u.t("New sheet"), false, true) {
		ed.addSheet(ed.cur + 1)
	}
}

// tabAt is the sheet whose tab is under x, -1 if none.
func (ed *sheetEd) tabAt(mx float32) int {
	u := &ed.a.u
	x := ed.body.tabs.X + u.px(6)
	f := u.face(12.5)
	for i, s := range ed.wb.Sheets {
		if s.Hidden() {
			continue
		}
		tw := f.Measure(s.Name) + u.px(24)
		if mx >= x && mx < x+tw {
			return i
		}
		x += tw + u.px(2)
	}
	return -1
}

func (ed *sheetEd) startRename(i int, s *xlsx.Sheet) {
	u := &ed.a.u
	ed.finishEdit()
	ed.rename, ed.renameTo = i, s.Name
	u.focusEdit("sheet/rename", s.Name, false)
}

func (ed *sheetEd) addSheet(at int) {
	ed.structure(func() error {
		_, err := ed.wb.AddSheet("Sheet", at)
		if err == nil {
			ed.cur = at
		}
		return err
	})
}

func (ed *sheetEd) tabMenu(i int, s *xlsx.Sheet, box gfx.Rect) {
	a := ed.a
	u := &a.u
	wb := ed.wb
	a.openMenu("sheet/tabmenu", box, []menuItem{
		{label: u.t("New sheet"), run: func() { ed.addSheet(i + 1) }},
		{label: u.t("Rename"), run: func() { ed.startRename(i, s) }},
		{label: u.t("Tab colour…"), run: func() { ed.togglePop(&sheetPop{kind: sheetPopTab, at: box, sheet: s}) }},
		menuSep,
		{label: u.t("Move left"), disabled: i == 0, run: func() {
			ed.structure(func() error { wb.MoveSheet(s, i-1); ed.cur = i - 1; return nil })
		}},
		{label: u.t("Move right"), disabled: i == len(wb.Sheets)-1, run: func() {
			ed.structure(func() error { wb.MoveSheet(s, i+1); ed.cur = i + 1; return nil })
		}},
		menuSep,
		{label: u.t("Delete sheet"), run: func() { ed.structure(func() error { return wb.DeleteSheet(s) }) }},
	})
}

// drawStatus draws the selection's numbers and the zoom.
func (ed *sheetEd) drawStatus(v *sheetView) {
	a := ed.a
	u := &a.u
	r := ed.body.tabs
	f := u.face(12)
	x := r.Right() - u.px(8)
	// The zoom, right to left.
	pct := gfx.Rect{X: x - u.px(44), Y: r.Y, W: u.px(44), H: r.H}
	x = pct.X
	if u.clicked("sheet/zoom/reset", pct) {
		ed.setZoom(1)
	}
	u.label(f, pct, strconv.Itoa(int(ed.zoom*100+0.5))+"%", colDim, 1)
	slide := gfx.Rect{X: x - u.px(116), Y: r.Y, W: u.px(110), H: r.H}
	z := ed.zoom
	if ch, _ := u.slider("sheet/zoom", slide, &z, sheetMinZoom, 2, true); ch {
		ed.setZoom(z)
	}
	x = slide.X - u.px(12)
	st := ed.statsFor(v)
	text := ""
	switch {
	case ed.circular != "":
		text = u.tf("Circular reference: %s", ed.circular)
	case ed.wb.Pending():
		text = u.t("Calculating…")
	case st.numbers > 1 || st.numbers == 1 && st.count > 1:
		text = u.tf("Sum %s    Average %s    Count %d", formula.General(st.sum, 12), formula.General(st.sum/float64(st.numbers), 12), st.count)
	case st.count > 1:
		text = u.tf("Count %d", st.count)
	case ed.plain != nil:
		text = u.t("A text file keeps the first sheet's values, not its formats")
	}
	if text != "" {
		col := colDim
		if ed.circular != "" {
			col = colWarning
		}
		u.label(f, gfx.Rect{X: r.X + r.W*0.58, Y: r.Y, W: x - (r.X + r.W*0.58), H: r.H}, text, col, 1)
	}
}

func (ed *sheetEd) togglePop(p *sheetPop) {
	if ed.pop != nil && ed.pop.kind == p.kind {
		ed.pop = nil
		return
	}
	ed.pop = p
}

// drawPop draws the floating panel, last, and closes it on a press elsewhere.
func (ed *sheetEd) drawPop() {
	a := ed.a
	u := &a.u
	p := ed.pop
	box := ed.popRect()
	u.c.Fill(gfx.Rect{X: box.X, Y: box.Y + u.px(3), W: box.W, H: box.H}, gfx.Alpha(colBlack, 0.35))
	u.c.Fill(box, colSurface)
	u.c.Outline(box, max(1, u.px(1)), colBorder)
	switch p.kind {
	case sheetPopFilter:
		ed.drawFilterPop(box)
	case sheetPopSize:
		ed.drawSizePop(box)
	default:
		ed.drawColourPop(box)
	}
	if ed.pop == nil {
		return
	}
	if p.opened && u.pressed && !box.Contains(u.pressX, u.pressY) {
		ed.pop = nil
		u.pressed = false
	}
	for _, k := range u.keys {
		if k.Code == keyEscape {
			ed.pop = nil
		}
	}
	p.opened = true
}

// sheetStandard are the standard colours under the theme's.
var sheetStandard = []string{"C00000", "FF0000", "FFC000", "FFFF00", "92D050", "00B050", "00B0F0", "0070C0", "002060", "7030A0"}

func (ed *sheetEd) drawColourPop(box gfx.Rect) {
	a := ed.a
	u := &a.u
	p := ed.pop
	pad := u.px(10)
	s := u.px(20)
	gap := u.px(2)
	pick := func(hex string) {
		ed.pop = nil
		switch p.kind {
		case sheetPopInk:
			ed.format(&xlsx.Change{Color: &hex})
		case sheetPopFill:
			ed.format(&xlsx.Change{Fill: &hex})
		case sheetPopTab:
			ed.structure(func() error { p.sheet.SetTabColor(hex); return nil })
		}
	}
	y := box.Y + pad
	label := u.t("No colour")
	if p.kind == sheetPopInk {
		label = u.t("Automatic")
	}
	if u.button("sheet/pop/none", gfx.Rect{X: box.X + pad, Y: y, W: box.W - 2*pad, H: u.px(26)}, "", label, plain, true) {
		pick("")
		return
	}
	y += u.px(32)
	cell := func(hex string, i, row int) {
		r := gfx.Rect{X: box.X + pad + float32(i)*(s+gap), Y: y + float32(row)*(s+gap), W: s, H: s}
		id := fmt.Sprintf("sheet/pop/%s", hex)
		u.c.Fill(r, hexRGBA(hex, colWhite))
		if u.hover(id, r) {
			u.c.Outline(r, 2, colText)
			u.cursor = CursorHand
		} else {
			u.c.Outline(r, 1, gfx.Alpha(colBlack, 0.3))
		}
		if u.clicked(id, r) {
			pick(hex)
		}
	}
	// The theme's ten colours, then tints of them, as Office shows them.
	order := []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9}
	tints := []float64{0, 0.8, 0.6, 0.4, -0.25, -0.5}
	st := ed.wb.Styles
	for row, t := range tints {
		for i, th := range order {
			tint := t
			if th == 0 && t > 0 {
				tint = -t / 2 // white has nothing lighter
			}
			cell(st.ThemeColor(th, tint), i, row)
			if ed.pop == nil {
				return
			}
		}
	}
	y += float32(len(tints))*(s+gap) + u.px(6)
	for i, hex := range sheetStandard {
		cell(hex, i, 0)
		if ed.pop == nil {
			return
		}
	}
}

// openFilter lists a filter column's values.
func (ed *sheetEd) openFilter(v *sheetView, c int, af formula.Area) {
	seen := map[string]bool{}
	var values []string
	blanks := false
	for r := int(af.R1) + 1; r <= int(af.R2); r++ {
		t := v.s.Shown(r, c).Text
		if t == "" {
			blanks = true
			continue
		}
		if !seen[t] {
			seen[t] = true
			values = append(values, t)
		}
	}
	slices.Sort(values)
	chosen := map[string]bool{}
	old, showBlanks, has := v.s.FilterValues(c)
	for _, t := range values {
		chosen[t] = !has || slices.Contains(old, t)
	}
	box := ed.filterButton(v, int(af.R1), c)
	ed.pop = &sheetPop{kind: sheetPopFilter, at: box, col: c, values: values, chosen: chosen, blanks: !has || showBlanks, hasNone: blanks}
}

func (ed *sheetEd) drawFilterPop(box gfx.Rect) {
	a := ed.a
	u := &a.u
	p := ed.pop
	v := ed.view()
	pad := u.px(8)
	x := box.X + pad
	y := box.Y + pad
	w := box.W - 2*pad
	if u.button("sheet/filter/az", gfx.Rect{X: x, Y: y, W: w/2 - u.px(2), H: u.px(26)}, "", u.t("Sort A to Z"), plain, true) {
		ed.pop = nil
		ed.sortFiltered(v, p.col, false)
		return
	}
	if u.button("sheet/filter/za", gfx.Rect{X: x + w/2 + u.px(2), Y: y, W: w/2 - u.px(2), H: u.px(26)}, "", u.t("Sort Z to A"), plain, true) {
		ed.pop = nil
		ed.sortFiltered(v, p.col, true)
		return
	}
	y += u.px(32)
	list := gfx.Rect{X: x, Y: y, W: w, H: box.Bottom() - pad - u.px(32) - y}
	u.c.Fill(list, colPanel)
	rowH := u.px(24)
	n := len(p.values)
	if p.hasNone {
		n++
	}
	total := float32(n+1) * rowH
	if u.hover("sheet/filter/list", list) && u.wheel != 0 {
		p.scroll -= u.wheel * rowH * 2
		u.wheel = 0
	}
	p.scroll = min(max(p.scroll, 0), max(total-list.H, 0))
	u.c.Push(list)
	row := func(i int, text string, on bool) bool {
		r := gfx.Rect{X: list.X + u.px(4), Y: list.Y + float32(i)*rowH - p.scroll, W: list.W - u.px(8), H: rowH}
		if r.Bottom() < list.Y || r.Y > list.Bottom() {
			return on
		}
		return u.checkbox(fmt.Sprintf("sheet/filter/%d", i), r, text, on)
	}
	all := row(0, u.t("(Select all)"), !slices.ContainsFunc(p.values, func(s string) bool { return !p.chosen[s] }))
	if all != !slices.ContainsFunc(p.values, func(s string) bool { return !p.chosen[s] }) {
		for _, s := range p.values {
			p.chosen[s] = all
		}
		p.blanks = all
	}
	i := 1
	if p.hasNone {
		p.blanks = row(i, u.t("(Blanks)"), p.blanks)
		i++
	}
	for _, s := range p.values {
		if i*int(rowH) < int(p.scroll+list.H)+int(rowH) {
			p.chosen[s] = row(i, s, p.chosen[s])
		}
		i++
	}
	u.c.Pop()
	bw := u.px(76)
	by := box.Bottom() - pad - u.px(26)
	if u.button("sheet/filter/cancel", gfx.Rect{X: box.Right() - pad - bw, Y: by, W: bw, H: u.px(26)}, "", u.t("Cancel"), plain, true) {
		ed.pop = nil
		return
	}
	if u.button("sheet/filter/ok", gfx.Rect{X: box.Right() - pad - 2*bw - u.px(4), Y: by, W: bw, H: u.px(26)}, "", u.t("OK"), primary, true) {
		var picked []string
		for _, s := range p.values {
			if p.chosen[s] {
				picked = append(picked, s)
			}
		}
		col, blanks := p.col, p.blanks && p.hasNone
		everything := len(picked) == len(p.values) && (blanks || !p.hasNone)
		ed.pop = nil
		ed.structure(func() error {
			if everything {
				return v.s.SetFilter(col, nil, false)
			}
			if picked == nil {
				picked = []string{}
			}
			return v.s.SetFilter(col, picked, blanks)
		})
	}
}

func (ed *sheetEd) sortFiltered(v *sheetView, col int, desc bool) {
	af, ok := v.s.AutoFilter()
	if !ok {
		return
	}
	af.R1++
	ed.structure(func() error { return v.s.Sort(af, []xlsx.SortKey{{Col: col, Desc: desc}}) })
}

// openSize asks for a column's width or a row's height.
func (ed *sheetEd) openSize(v *sheetView, cols bool) {
	u := &ed.a.u
	text := strconv.FormatFloat(v.s.ColInfo(v.active[1]).Width, 'f', 2, 64)
	if !cols {
		text = strconv.FormatFloat(v.s.RowPoints(v.active[0]), 'f', 2, 64)
	}
	r := ed.cellBox(v, cellArea(v.active[0], v.active[1]))
	ed.pop = &sheetPop{kind: sheetPopSize, at: gfx.Rect{X: min(r.X, ed.body.panes.Right()-u.px(240)), Y: ed.body.panes.Y + u.px(30)}, cols: cols, text: text}
	u.focusEdit("sheet/size", text, false)
}

func (ed *sheetEd) drawSizePop(box gfx.Rect) {
	u := &ed.a.u
	p := ed.pop
	pad := u.px(10)
	label := u.t("Row height (points)")
	if p.cols {
		label = u.t("Column width (characters)")
	}
	u.label(u.face(12.5), gfx.Rect{X: box.X + pad, Y: box.Y + u.px(6), W: box.W - 2*pad, H: u.px(22)}, label, colDim, 0)
	_, done := u.textBox("sheet/size", gfx.Rect{X: box.X + pad, Y: box.Y + u.px(34), W: box.W - 2*pad, H: u.px(34)}, &p.text, "", false)
	if !done {
		return
	}
	u.focus = sheetFocus
	ed.pop = nil
	n, err := strconv.ParseFloat(strings.TrimSpace(p.text), 64)
	if err != nil || n < 0 {
		return
	}
	v := ed.view()
	ed.structure(func() error {
		for _, a := range v.sel {
			if p.cols {
				v.s.SetColWidth(int(a.C1), int(a.C2), n)
			} else if !wholeCols(a) {
				v.s.SetRowHeight(int(a.R1), int(a.R2), n)
			}
		}
		return nil
	})
}
