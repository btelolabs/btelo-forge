package ui

import (
	"fmt"

	"github.com/btelolabs/btelo-forge/internal/gfx"
)

// menuItem is a line of a popup menu: a command with its shortcut, a submenu, or a separator.
type menuItem struct {
	label    string
	key      string // the shortcut, shown on the right
	run      func()
	checked  bool
	disabled bool
	sep      bool       // a separator line; the other fields are ignored
	sub      []menuItem // a submenu, opened by pointing at the item
}

// menuSep separates groups of items.
var menuSep = menuItem{sep: true}

// popupMenu is the open menu, drawn over everything else; while it is open the rest of the window does not react.
type popupMenu struct {
	id     string
	anchor gfx.Rect // what it opened from: it opens below it, or above when there is no room
	items  []menuItem
	sub    int  // the item whose submenu is open, or -1
	opened bool // drawn once, so the click that opened it is not taken as a click outside
}

// openMenu shows a menu under anchor; opening the same one again closes it, as a menu bar's titles do.
func (a *App) openMenu(id string, anchor gfx.Rect, items []menuItem) {
	if a.menu != nil && a.menu.id == id {
		a.menu = nil
		return
	}
	a.menu = &popupMenu{id: id, anchor: anchor, items: items, sub: -1}
}

// menuOpen reports whether the menu with id is open.
func (a *App) menuOpen(id string) bool { return a.menu != nil && a.menu.id == id }

// Menu geometry, shared by drawing and the tests.
func (u *ctx) menuRowH() float32 { return u.px(30) }
func (u *ctx) menuSepH() float32 { return u.px(9) }
func (u *ctx) menuPad() float32  { return u.px(6) }

// menuSize is how large a list of items is drawn.
func (u *ctx) menuSize(items []menuItem) (w, h float32) {
	f := u.face(13)
	w, h = u.px(180), u.menuPad()*2
	for _, it := range items {
		if it.sep {
			h += u.menuSepH()
			continue
		}
		w = max(w, f.Measure(it.label)+u.face(12).Measure(it.key)+u.px(70))
		h += u.menuRowH()
	}
	return w, h
}

// menuBelow is where a menu opening from anchor goes: below it, or above when there is no room.
func (a *App) menuBelow(items []menuItem, anchor gfx.Rect) gfx.Rect {
	u := &a.u
	w, h := u.menuSize(items)
	x, y := anchor.X, anchor.Bottom()+u.px(2)
	if y+h > a.screen.H {
		y = anchor.Y - h - u.px(2)
	}
	x = min(x, a.screen.W-w-u.px(4))
	return gfx.Rect{X: max(x, u.px(4)), Y: max(y, u.px(4)), W: w, H: h}
}

// menuBeside is where a submenu opening from row goes: to its right, or its left when there is no room.
func (a *App) menuBeside(items []menuItem, row gfx.Rect) gfx.Rect {
	u := &a.u
	w, h := u.menuSize(items)
	x, y := row.Right()+u.px(4), row.Y-u.menuPad()
	if x+w > a.screen.W-u.px(4) {
		x = row.X - w - u.px(4)
	}
	y = min(y, a.screen.H-h-u.px(4))
	return gfx.Rect{X: max(x, u.px(4)), Y: max(y, u.px(4)), W: w, H: h}
}

// menuRows are where the items' rows are in a menu drawn in box; separators get an empty rectangle.
func (u *ctx) menuRows(items []menuItem, box gfx.Rect) []gfx.Rect {
	rows := make([]gfx.Rect, len(items))
	y := box.Y + u.menuPad()
	for i, it := range items {
		if it.sep {
			y += u.menuSepH()
			continue
		}
		rows[i] = gfx.Rect{X: box.X + u.px(4), Y: y, W: box.W - u.px(8), H: u.menuRowH()}
		y += u.menuRowH()
	}
	return rows
}

// drawMenuList draws a menu's items in box. It returns the item clicked, the item pointed at, and the rows.
func (a *App) drawMenuList(id string, items []menuItem, box gfx.Rect, open int) (clicked, pointed int, rows []gfx.Rect) {
	u := &a.u
	f := u.face(13)
	u.c.Fill(gfx.Rect{X: box.X, Y: box.Y + u.px(3), W: box.W, H: box.H}, gfx.Alpha(colBlack, 0.35))
	u.c.Fill(box, colSurface)
	u.c.Outline(box, max(1, u.px(1)), colBorder)
	rows = u.menuRows(items, box)
	clicked, pointed = -1, -1
	for i, it := range items {
		if it.sep {
			y := rows[i].Y
			if i > 0 {
				y = rows[i-1].Bottom()
			}
			u.c.Fill(gfx.Rect{X: box.X + u.px(10), Y: y + u.menuSepH()/2, W: box.W - u.px(20), H: max(1, u.px(1))}, colBorder)
			continue
		}
		row := rows[i]
		rid := fmt.Sprintf("menu/%s/%d", id, i)
		fg := colText
		switch {
		case it.disabled:
			fg = colFaint
		case u.hover(rid, row) || i == open:
			u.c.Fill(row, colSelected)
			u.cursor = CursorHand
		}
		if row.Contains(u.mx, u.my) {
			pointed = i
		}
		if it.checked {
			u.icon(icCheck, gfx.Rect{X: row.X + u.px(4), Y: row.Y, W: u.px(18), H: row.H}, 13, colText)
		}
		u.label(f, gfx.Rect{X: row.X + u.px(26), Y: row.Y, W: row.W - u.px(30), H: row.H}, it.label, fg, 0)
		switch {
		case it.sub != nil:
			u.icon(icClosed, gfx.Rect{X: row.Right() - u.px(24), Y: row.Y, W: u.px(20), H: row.H}, 15, colDim)
		case it.key != "":
			u.label(u.face(12), gfx.Rect{X: row.X, Y: row.Y, W: row.W - u.px(10), H: row.H}, it.key, colFaint, 1)
		}
		if !it.disabled && u.clicked(rid, row) {
			clicked = i
		}
	}
	return clicked, pointed, rows
}

// drawMenu draws the open menu, and its open submenu, last, and runs the item clicked.
func (a *App) drawMenu() {
	m := a.menu
	if m == nil {
		return
	}
	u := &a.u
	box := a.menuBelow(m.items, m.anchor)
	clicked, pointed, rows := a.drawMenuList(m.id, m.items, box, m.sub)
	// Pointing at an item opens its submenu, and at another item closes it, as menus do.
	if pointed >= 0 && !m.items[pointed].disabled {
		if m.items[pointed].sub != nil {
			m.sub = pointed
		} else {
			m.sub = -1
		}
	}
	var subBox gfx.Rect
	if m.sub >= 0 && m.sub < len(m.items) {
		sub := m.items[m.sub].sub
		subBox = a.menuBeside(sub, rows[m.sub])
		c, _, _ := a.drawMenuList(m.id+"/sub", sub, subBox, -1)
		if c >= 0 && sub[c].sub == nil {
			a.menu = nil
			if sub[c].run != nil {
				sub[c].run()
			}
			return
		}
	}
	if clicked >= 0 && m.items[clicked].sub == nil {
		a.menu = nil
		if run := m.items[clicked].run; run != nil {
			run()
		}
		return
	}
	// A press outside closes it, except on what opened it (whose own click toggles it).
	if m.opened && u.pressed && !box.Contains(u.pressX, u.pressY) && !subBox.Contains(u.pressX, u.pressY) &&
		!m.anchor.Contains(u.pressX, u.pressY) {
		a.menu = nil
		u.pressed = false
	}
	for _, k := range u.keys {
		if k.Code == keyEscape {
			a.menu = nil
		}
	}
	m.opened = true
}

// dropdown is a button showing the chosen option; it opens a menu of the options and calls pick with the choice.
func (a *App) dropdown(id string, r gfx.Rect, options []string, selected int, sepBefore map[int]bool, pick func(int)) {
	u := &a.u
	hot := u.hover(id, r)
	bg := colSurface
	if hot || a.menuOpen(id) {
		bg = colSurfaceHi
		u.cursor = CursorHand
	}
	u.c.Fill(r, bg)
	label := ""
	if selected >= 0 && selected < len(options) {
		label = options[selected]
	}
	u.label(u.face(12.5), gfx.Rect{X: r.X + u.px(10), Y: r.Y, W: r.W - u.px(30), H: r.H}, label, colText, 0)
	u.icon(icOpen, gfx.Rect{X: r.Right() - u.px(24), Y: r.Y, W: u.px(20), H: r.H}, 15, colDim)
	if u.clicked(id, r) {
		items := make([]menuItem, 0, len(options))
		for i, o := range options {
			if sepBefore[i] {
				items = append(items, menuSep)
			}
			items = append(items, menuItem{label: o, checked: i == selected, run: func() { pick(i) }})
		}
		a.openMenu(id, r, items)
	}
}
