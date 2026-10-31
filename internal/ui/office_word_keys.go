package ui

import (
	"strings"
	"time"

	"github.com/btelolabs/btelo-forge/internal/gfx"
	"github.com/btelolabs/btelo-forge/internal/office/docx"
	"github.com/btelolabs/btelo-forge/internal/office/flow"
)

// The keys and the mouse in the pages.

// keys takes the frame's keys and typing while the caret is in the document.
func (e *wordEditor) keys() {
	a := e.a
	u := &a.u
	if u.blocked {
		return
	}
	// Find and replace open whatever has the keyboard; the find bar's box takes the rest.
	keep := u.keys[:0:0]
	for _, k := range u.keys {
		switch {
		case k.Ctrl && !k.Alt && k.Code == 'F':
			e.openFind(false)
		case k.Ctrl && !k.Alt && k.Code == 'H':
			e.openFind(true)
		default:
			keep = append(keep, k)
		}
	}
	u.keys = keep
	if u.focus != wordFocus {
		return
	}
	u.focusDrawn = true
	if len(u.keys) > 0 || len(u.typed) > 0 {
		keys := u.keys
		u.keys = nil
		for _, k := range keys {
			e.key(k)
		}
		if len(u.typed) > 0 {
			typed := string(u.typed)
			u.typed = nil
			e.d.Type(typed)
			e.goalX, e.follow = -1, true
		}
	}
}

// key does what a key means in the document.
func (e *wordEditor) key(k Key) {
	a := e.a
	u := &a.u
	d := e.d
	const moved = 1
	touched := 0
	ctrl, shift := k.Ctrl && !k.Alt, k.Shift
	keepGoal := false
	switch {
	case k.Code == keyLeft:
		if ctrl {
			d.MoveWord(false, shift)
		} else {
			d.MoveChar(false, shift)
		}
		touched = moved
	case k.Code == keyRight:
		if ctrl {
			d.MoveWord(true, shift)
		} else {
			d.MoveChar(true, shift)
		}
		touched = moved
	case k.Code == keyUp, k.Code == keyDown:
		e.goalX = d.MoveVert(k.Code == keyDown, shift, e.goalX)
		keepGoal = true
		touched = moved
	case k.Code == keyHome:
		if ctrl {
			d.MoveDoc(false, shift)
		} else {
			d.MoveLine(false, shift)
		}
		touched = moved
	case k.Code == keyEnd:
		if ctrl {
			d.MoveDoc(true, shift)
		} else {
			d.MoveLine(true, shift)
		}
		touched = moved
	case k.Code == keyPageUp, k.Code == keyPageDn:
		e.pageKey(k.Code == keyPageDn, shift)
		touched = moved
	case k.Code == keyBack:
		d.Backspace(ctrl)
		touched = moved
	case k.Code == keyDelete:
		d.Delete(ctrl)
		touched = moved
	case k.Code == keyEnter:
		switch {
		case ctrl:
			d.PageBreak()
		case shift:
			d.LineBreak()
		default:
			d.Enter()
		}
		touched = moved
	case k.Code == keyTab && !ctrl:
		e.tabKey(shift)
		touched = moved
	case k.Code == keyEscape:
		e.leaveHeader()
		e.link = nil
	case ctrl && k.Code == 'A':
		d.SelectAll()
	case ctrl && k.Code == 'C':
		if s := d.Copy(); s != "" {
			u.setClipText(s)
		}
	case ctrl && k.Code == 'X':
		if s := d.Copy(); s != "" {
			u.setClipText(s)
			d.DeleteSelection()
			touched = moved
		}
	case ctrl && k.Code == 'V':
		e.paste()
		touched = moved
	case ctrl && k.Code == 'B':
		e.toggle(func(c *docx.RunChange, v *bool) { c.Bold = v }, d.CaretRun().Bold)
	case ctrl && k.Code == 'I':
		e.toggle(func(c *docx.RunChange, v *bool) { c.Italic = v }, d.CaretRun().Italic)
	case ctrl && k.Code == 'U':
		e.toggle(func(c *docx.RunChange, v *bool) { c.Underline = v }, d.CaretRun().Under != "")
	case ctrl && k.Code == 'L':
		d.SetAlign(flow.Left)
	case ctrl && k.Code == 'E':
		d.SetAlign(flow.Center)
	case ctrl && k.Code == 'R':
		d.SetAlign(flow.Right)
	case ctrl && k.Code == 'J':
		d.SetAlign(flow.Justify)
	case ctrl && k.Code == 'K':
		e.openLink()
	case ctrl && (k.Code == keyEquals || k.Code == keyNumPlus):
		e.setZoom(e.zoom * 1.1)
	case ctrl && (k.Code == keyMinus || k.Code == keyNumMinus):
		e.setZoom(e.zoom / 1.1)
	}
	if touched != 0 {
		e.follow = true
		if !keepGoal {
			e.goalX = -1
		}
	}
}

// toggle turns a character format on or off for the selection or what is typed next.
func (e *wordEditor) toggle(set func(*docx.RunChange, *bool), now bool) {
	v := !now
	var c docx.RunChange
	set(&c, &v)
	e.d.FormatRuns(c)
}

// tabKey is Tab: to the next cell in a table, a list level at the start of an item, otherwise a tab.
func (e *wordEditor) tabKey(shift bool) {
	d := e.d
	switch {
	case d.CaretInTable():
		d.NextCell(shift)
	case d.AtListStart():
		d.Indent(!shift)
	case !shift:
		d.Type("\t")
	}
}

// pageKey moves the caret a view's height.
func (e *wordEditor) pageKey(down, extend bool) {
	d := e.d
	s, ok := d.Reach(d.Sel.Caret)
	if !ok {
		return
	}
	h := e.view.H * 0.9
	y := e.tops[s.Page] + s.Y*e.scale
	if !down {
		h = -h
	}
	y += h
	e.scroll += h
	page := e.pageAt(y - e.scroll + e.view.Y)
	if page < 0 {
		return
	}
	d.MoveTo(page, s.X, (y-e.tops[page])/e.scale, extend)
}

// paste puts what the clipboard has at the caret: a picture, files that are pictures, or words.
func (e *wordEditor) paste() {
	a := e.a
	img, files, text := a.host.Paste()
	switch {
	case img != nil:
		e.pasteImage(img)
	case len(files) > 0:
		e.dropped(files)
	case text != "":
		e.d.Paste(text)
	default:
		if s := a.u.clipText(); s != "" {
			e.d.Paste(s)
		}
	}
}

// mouse handles what is done with the mouse on the pages.
func (e *wordEditor) mouse(view gfx.Rect) {
	a := e.a
	u := &a.u
	d := e.d
	id := "office/word/pages"
	scrollbar := gfx.Rect{X: view.Right() - u.px(10), Y: view.Y, W: u.px(10), H: view.H}
	area := view
	if e.height > view.H {
		area.W -= scrollbar.W
	}
	if u.hover(id, area) {
		u.cursor = CursorText
		if u.ctrl {
			if p, _, ok := e.hitAt(u.mx, u.my); ok && d.LinkAt(p) != "" {
				u.cursor = CursorHand
			}
		}
	}
	if u.rightAt != nil && area.Contains(u.rightAt.X, u.rightAt.Y) && !u.blocked {
		e.rightClick(*u.rightAt)
	}
	if u.press(id, area) {
		u.setFocus(wordFocus)
		e.pressed()
	}
	if u.dragging(id) {
		if u.drag.moved && e.drag {
			e.dragTo()
		}
		// Past the edge of the view the pages go by.
		switch {
		case u.my < view.Y+u.px(8):
			e.scroll -= u.px(14)
		case u.my > view.Bottom()-u.px(8):
			e.scroll += u.px(14)
		}
	}
	if u.letGo(id) {
		e.drag = false
		e.goalX = -1
	}
}

// hitAt is the place under a window point, the page and whether it has one.
func (e *wordEditor) hitAt(x, y float32) (docx.Pos, int, bool) {
	page := e.pageAt(y)
	if page < 0 {
		return docx.Pos{}, 0, false
	}
	pg := e.d.Pages()[page]
	px, py := e.pageXY(page)
	if e.inHeader() && page == e.d.EditPage {
		st := e.d.Sel.Caret.B.In.Story
		p, ok := e.d.HitStory(pg, st, (x-px)/e.scale, (y-py)/e.scale)
		return p, page, ok
	}
	p, ok := e.d.Hit(pg, (x-px)/e.scale, (y-py)/e.scale)
	return p, page, ok
}

// pressed is a press on the pages: it counts clicks, puts the caret, and enters or leaves a header.
func (e *wordEditor) pressed() {
	a := e.a
	u := &a.u
	d := e.d
	now := time.Now()
	near := abs32(u.pressX-e.pressXY[0]) < u.px(4) && abs32(u.pressY-e.pressXY[1]) < u.px(4)
	if u.double || near && now.Sub(e.pressAt) < 450*time.Millisecond {
		e.pressN = min(e.pressN+1, 3)
	} else {
		e.pressN = 1
	}
	e.pressAt, e.pressXY = now, [2]float32{u.pressX, u.pressY}
	page := e.pageAt(u.pressY)
	if page < 0 {
		return
	}
	pg := d.Pages()[page]
	px, py := e.pageXY(page)
	x, y := (u.pressX-px)/e.scale, (u.pressY-py)/e.scale
	inside := x >= 0 && x <= pg.W && y >= 0 && y <= pg.H
	switch {
	case e.inHeader():
		if st := d.Sel.Caret.B.In.Story; page != d.EditPage || !d.InHeaderBand(pg, st, y) {
			e.leaveHeader()
		}
	case e.pressN == 2 && inside && y < pg.Top:
		e.enterHeader(page, "header")
		return
	case e.pressN == 2 && inside && y > pg.Bottom:
		e.enterHeader(page, "footer")
		return
	}
	p, _, ok := e.hitAt(u.pressX, u.pressY)
	if !ok {
		return
	}
	if u.ctrl {
		if url := d.LinkAt(p); url != "" {
			if safeLink(url) {
				a.host.LaunchURL(url)
			}
			return
		}
	}
	e.goalX = -1
	e.drag = true
	switch {
	case e.pressN == 2:
		d.SelectWord(p)
		e.dragBy = 1
	case e.pressN >= 3:
		d.SelectParagraph(p)
		e.dragBy = 2
	default:
		d.SetCaret(p, u.shift)
		e.dragBy = 0
	}
	e.dragFrom = d.Sel
}

// safeLink allows the addresses a browser or a mail program should open.
func safeLink(url string) bool {
	low := strings.ToLower(url)
	return strings.HasPrefix(low, "http://") || strings.HasPrefix(low, "https://") || strings.HasPrefix(low, "mailto:")
}

// dragTo stretches the selection to the pointer, by the unit the press chose.
func (e *wordEditor) dragTo() {
	a := e.a
	u := &a.u
	d := e.d
	p, _, ok := e.hitAt(u.mx, u.my)
	if !ok || p.B.In.Story != d.Sel.Caret.B.In.Story {
		return
	}
	if e.dragBy == 0 {
		d.SetCaret(p, true)
		return
	}
	from0, from1 := e.dragFrom.Range()
	var to0, to1 docx.Pos
	if e.dragBy == 1 {
		x, y := docx.WordAround(p.B, p.Off)
		to0, to1 = docx.Pos{B: p.B, Off: x}, docx.Pos{B: p.B, Off: y}
	} else {
		to0, to1 = docx.Pos{B: p.B}, docx.Pos{B: p.B, Off: d.ParaLen(p.B)}
	}
	if d.Before(to0, from0) {
		d.Sel = docx.Selection{Anchor: from1, Caret: to0}
	} else {
		d.Sel = docx.Selection{Anchor: from0, Caret: to1}
	}
}

// rightClick opens the menu of the pages.
func (e *wordEditor) rightClick(at Pointer) {
	a := e.a
	u := &a.u
	d := e.d
	p, _, ok := e.hitAt(at.X, at.Y)
	inSel := false
	if ok && !d.Sel.Empty() {
		x, y := d.Sel.Range()
		inSel = !d.Before(p, x) && !d.Before(y, p)
	}
	if ok && !inSel {
		d.SetCaret(p, false)
	}
	u.setFocus(wordFocus)
	var items []menuItem
	has := !d.Sel.Empty()
	items = append(items,
		menuItem{label: u.t("Cut"), key: "Ctrl+X", disabled: !has, run: func() { e.key(Key{Code: 'X', Ctrl: true}) }},
		menuItem{label: u.t("Copy"), key: "Ctrl+C", disabled: !has, run: func() { e.key(Key{Code: 'C', Ctrl: true}) }},
		menuItem{label: u.t("Paste"), key: "Ctrl+V", run: func() { e.key(Key{Code: 'V', Ctrl: true}) }},
		menuSep,
		menuItem{label: u.t("Link…"), key: "Ctrl+K", run: e.openLink})
	if d.CaretInTable() {
		items = append(items, menuSep)
		items = append(items, e.tableItems()...)
	}
	a.openMenu("office/word/context", gfx.Rect{X: at.X, Y: at.Y, W: 0, H: 0}, items)
}

// tableItems are the commands on the table around the caret.
func (e *wordEditor) tableItems() []menuItem {
	a := e.a
	u := &a.u
	d := e.d
	run := func(fn func() error) func() {
		return func() {
			if err := fn(); err != nil {
				a.toast(a.describe(err), true)
			}
			e.follow = true
		}
	}
	return []menuItem{
		{label: u.t("Insert row above"), run: run(func() error { return d.AddRow(true) })},
		{label: u.t("Insert row below"), run: run(func() error { return d.AddRow(false) })},
		{label: u.t("Insert column left"), run: run(func() error { return d.AddColumn(true) })},
		{label: u.t("Insert column right"), run: run(func() error { return d.AddColumn(false) })},
		menuSep,
		{label: u.t("Merge cells"), run: run(d.MergeCells)},
		menuSep,
		{label: u.t("Delete row"), run: run(d.DeleteRow)},
		{label: u.t("Delete column"), run: run(d.DeleteColumn)},
		{label: u.t("Delete table"), run: run(d.DeleteTable)},
	}
}
