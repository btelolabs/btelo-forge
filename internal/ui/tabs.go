package ui

import (
	"fmt"
	"image/color"
	"path/filepath"

	"github.com/btelolabs/btelo-forge/internal/gfx"
)

// drawTabs is the browser-like strip at the top: one tab per edit, a button for a new tab, and settings at the end.
func (a *App) drawTabs(r gfx.Rect) {
	u := &a.u
	u.c.Fill(r, colBackground)
	gear := gfx.Rect{X: r.Right() - u.px(40), Y: r.Y + u.px(5), W: u.px(32), H: u.px(30)}
	if u.iconButton("tabs/settings", gear, icSettings, u.t("Settings"), a.settingsOpen, a.export == nil) {
		a.settingsOpen = true
	}
	end := a.drawRecordControl(gear.X, gear.Y, gear.H)
	if a.p != nil {
		toggle := gfx.Rect{X: end - u.px(36), Y: gear.Y, W: gear.W, H: gear.H}
		glyph, hint := icPanelOut, u.t("Hide the side panel")
		if a.settings.PanelHidden {
			glyph, hint = icPanelIn, u.t("Show the side panel")
		}
		if u.iconButton("tabs/panel", toggle, glyph, hint, false, true) {
			a.setPanelHidden(!a.settings.PanelHidden)
		}
		end = toggle.X
	}
	plusW := u.px(32)
	space := end - u.px(8) - r.X - u.px(8) - plusW - u.px(4)
	w := min(u.px(220), space/float32(len(a.docs)))
	x := r.X + u.px(8)
	f := u.face(12.5)
	closeAt := -1
	for i, d := range a.docs {
		tr := gfx.Rect{X: x, Y: r.Y + u.px(6), W: w - u.px(2), H: r.H - u.px(6)}
		id := fmt.Sprintf("tabs/%d", i)
		active := d == a.doc
		hot := u.hover(id, tr)
		bg := colBackground
		switch {
		case active:
			bg = colPanel
		case hot:
			bg = colSurface
		}
		u.c.RoundRect(gfx.Rect{X: tr.X, Y: tr.Y, W: tr.W, H: tr.H + u.px(10)}, u.px(8), bg)
		if active {
			u.c.Fill(gfx.Rect{X: tr.X + u.px(10), Y: tr.Y, W: tr.W - u.px(20), H: max(1, u.px(2))}, colAccent)
		}
		glyph := icVideo
		if d.p == nil && d.opening == "" {
			glyph = icAdd
		}
		fg := colDim
		if active {
			fg = colText
		}
		u.icon(glyph, gfx.Rect{X: tr.X + u.px(8), Y: tr.Y, W: u.px(18), H: tr.H}, 12, fg)
		cb := gfx.Rect{X: tr.Right() - u.px(26), Y: tr.Y + (tr.H-u.px(20))/2, W: u.px(20), H: u.px(20)}
		u.label(f, gfx.Rect{X: tr.X + u.px(30), Y: tr.Y, W: cb.X - tr.X - u.px(34), H: tr.H}, d.title(a), fg, 0)
		if hot || active {
			ch := u.hover(id+"/close", cb)
			if ch {
				u.c.RoundRect(cb, u.px(10), colSurfaceHi)
				u.cursor = CursorHand
			}
			u.icon(icClose, cb, 9, fg)
			if u.clicked(id+"/close", cb) {
				closeAt = i
			}
		}
		if d.p != nil {
			u.hint(id, d.p.Sources[0].Path, tr)
		}
		if u.clicked(id, tr) {
			a.switchTab(i)
		}
		x += w
	}
	plus := gfx.Rect{X: x + u.px(4), Y: r.Y + u.px(6), W: plusW, H: u.px(28)}
	if u.iconButton("tabs/new", plus, icAdd, u.t("New tab (Ctrl+T)"), false, true) {
		a.newTab()
	}
	if closeAt >= 0 {
		a.closeTab(closeAt)
	}
	u.c.Fill(gfx.Rect{X: r.X, Y: r.Bottom() - max(1, u.px(1)), W: r.W, H: max(1, u.px(1))}, colPanel)
}

// startCard is one of the three kinds of work a new tab offers, side by side and on an equal footing.
type startCard struct {
	id, glyph, title, text, action string
	hue                            color.RGBA
	enabled                        bool
	hint                           string
	run                            func()
}

// startCards are the new tab's three kinds of work.
func (a *App) startCards() []startCard {
	u := &a.u
	recordHint := ""
	if a.recorder() == nil {
		recordHint = u.t("Screen recording works on Windows")
	} else if reason, _ := a.recordBlocker(); reason != "" {
		recordHint = u.tf("Screen recording is not available: %s", reason)
	}
	return []startCard{
		{id: "empty/video", glyph: icVideo, hue: gfx.Hex(0x5aa9ff), title: u.t("Edit a video"),
			text:   u.t("Cut, change speed, add music, text and subtitles, and export at the size you need."),
			action: u.t("Choose a video…"), enabled: a.opening == "", run: a.chooseVideo},
		{id: "empty/canvas", glyph: icBrush, hue: gfx.Hex(0xf2994a), title: u.t("Paint and draw"),
			text:   u.t("Brushes and layers as in Photoshop, vectors as in Affinity Designer, on an infinite canvas."),
			action: u.t("New canvas"), enabled: true, run: a.newCanvasNow},
		{id: "empty/record", glyph: icRecord, hue: gfx.Hex(0xf0605d), title: u.t("Record the screen"),
			text:   u.t("Record the screen with the computer's sound and your microphone, or take a screenshot to draw on."),
			action: u.t("Record…"), enabled: a.recorder() != nil && !a.recording() && a.export == nil, hint: recordHint,
			run: a.openRecorder},
	}
}

// startPage is where the new tab puts its parts.
type startPage struct {
	head    gfx.Rect   // the logo, the name and the line under it
	cards   []gfx.Rect // one per start card
	stacked bool       // the cards are rows, one above another, in a narrow window
	lineH   float32    // the cards' description lines
	foot    gfx.Rect   // the row with Open a file
}

// startLayout places the logo, the cards and the footer, side by side where the cards fit and one above another
// otherwise, centred in r; every card is as tall as the one with the most text.
func (a *App) startLayout(r gfx.Rect, cards []startCard) startPage {
	u := &a.u
	var p startPage
	gap := u.px(16)
	w := min(u.px(940), r.W-u.px(48))
	cw := (w - float32(len(cards)-1)*gap) / float32(len(cards))
	p.stacked = cw < u.px(220)
	if p.stacked {
		cw = w
	}
	tf := u.face(12.5)
	p.lineH = u.px(19)
	lines := 0
	for _, c := range cards {
		textW := cw - u.px(44)
		if p.stacked {
			textW = cw - u.px(3*22+52+18) - startActionWidth(u, c)
		}
		lines = max(lines, len(tf.Wrap(c.text, textW)))
	}
	ch := u.px(22+52+18+28+14+38+22) + float32(lines)*p.lineH
	cardsH := ch
	if p.stacked {
		ch = max(u.px(96), u.px(44+28)+float32(lines)*p.lineH)
		cardsH = float32(len(cards))*ch + float32(len(cards)-1)*u.px(10)
	}
	headH, footH := u.px(56+14+30+24+32), u.px(28+32)
	if status, _ := a.toolsStatus(); status != "" {
		footH += u.px(70)
	}
	top := r.Y + max(u.px(24), (r.H-headH-cardsH-footH)/2)
	left := r.X + (r.W-w)/2
	p.head = gfx.Rect{X: left, Y: top, W: w, H: headH}
	for i := range cards {
		if p.stacked {
			p.cards = append(p.cards, gfx.Rect{X: left, Y: top + headH + float32(i)*(ch+u.px(10)), W: cw, H: ch})
		} else {
			p.cards = append(p.cards, gfx.Rect{X: left + float32(i)*(cw+gap), Y: top + headH, W: cw, H: ch})
		}
	}
	p.foot = gfx.Rect{X: left, Y: top + headH + cardsH + u.px(28), W: w, H: u.px(32)}
	return p
}

// startActionWidth is how wide a stacked card's action is.
func startActionWidth(u *ctx, c startCard) float32 {
	return max(u.px(150), u.buttonWidth("", c.action))
}

// drawEmpty is a new tab: the three kinds of work — editing a video, painting and drawing, recording the screen — as
// equal cards, with a way to open any file and a reminder that files can be dropped anywhere.
func (a *App) drawEmpty(r gfx.Rect) {
	u := &a.u
	u.c.Fill(r, colPanel)
	cards := a.startCards()
	p := a.startLayout(r, cards)

	logo := gfx.Rect{X: p.head.X + p.head.W/2 - u.px(28), Y: p.head.Y, W: u.px(56), H: u.px(56)}
	drawLogo(u, logo)
	u.label(u.bold(22), gfx.Rect{X: p.head.X, Y: logo.Bottom() + u.px(14), W: p.head.W, H: u.px(30)}, "Btelo Forge", colText, 0.5)
	sub := u.t("What would you like to make?")
	if a.opening != "" {
		sub = u.tf("Opening %s …", filepath.Base(a.opening))
	}
	u.label(u.face(13.5), gfx.Rect{X: p.head.X, Y: logo.Bottom() + u.px(44), W: p.head.W, H: u.px(24)}, sub, colDim, 0.5)

	for i, c := range cards {
		a.drawStartCard(c, p.cards[i], p.stacked, p.lineH)
	}

	// Open a file, and beside it, where there is room, a reminder that files can simply be dropped.
	open := u.t("Open a file…")
	ow := u.buttonWidth(icFolder, open)
	drop := u.t("Or drop videos, pictures and canvases anywhere in this window")
	df := u.face(12)
	dw := df.Measure(drop)
	rowW := ow + u.px(12) + dw
	fits := rowW <= p.foot.W
	bx := p.foot.X + (p.foot.W-ow)/2
	if fits {
		bx = p.foot.X + (p.foot.W-rowW)/2
	}
	if u.button("empty/open", gfx.Rect{X: bx, Y: p.foot.Y, W: ow, H: p.foot.H}, icFolder, open, ghost, a.opening == "" && a.export == nil) {
		a.chooseAnyFile()
	}
	if fits {
		u.label(df, gfx.Rect{X: bx + ow + u.px(12), Y: p.foot.Y, W: dw + u.px(2), H: p.foot.H}, drop, colFaint, 0)
	}

	if status, failed := a.toolsStatus(); status != "" {
		col := colDim
		if failed {
			col = gfx.Hex(0xffb4b2)
		}
		line := gfx.Rect{X: p.foot.X, Y: p.foot.Bottom() + u.px(18), W: p.foot.W, H: u.px(20)}
		u.label(u.face(12), line, status, col, 0.5)
		if a.tools.downloading {
			bar := gfx.Rect{X: line.X + line.W/4, Y: line.Bottom() + u.px(8), W: line.W / 2, H: u.px(4)}
			u.c.RoundRect(bar, u.px(2), colSurface)
			u.c.RoundRect(gfx.Rect{X: bar.X, Y: bar.Y, W: max(bar.W*float32(a.tools.fraction), bar.H), H: bar.H}, u.px(2), colAccent)
		}
		if failed && u.button("empty/retry", gfx.Rect{X: r.X + r.W/2 - u.px(60), Y: line.Bottom() + u.px(10), W: u.px(120), H: u.px(32)}, "", u.t("Retry"), plain, true) {
			a.prepareTools()
		}
	}
}

// drawStartCard draws one kind of work as a card the whole of which can be clicked: its symbol in its own colour,
// what it is, what it does, and the action. Stacked, the card is a row with the action on the right.
func (a *App) drawStartCard(c startCard, box gfx.Rect, stacked bool, lineH float32) {
	u := &a.u
	hot := c.enabled && u.hover(c.id, box)
	bg, edge := colSurface, colBorder
	if hot {
		bg, edge = gfx.Mix(colSurface, c.hue, 0.06), gfx.Mix(colBorder, c.hue, 0.6)
		u.cursor = CursorHand
	}
	if u.active == c.id && c.enabled {
		bg = gfx.Mix(bg, colBlack, 0.12)
	}
	u.c.RoundRect(box, u.px(14), bg)
	u.c.RoundOutline(box, u.px(14), u.px(1), edge)
	hue, fg := c.hue, colText
	if !c.enabled {
		hue, fg = gfx.Mix(colSurface, c.hue, 0.45), colFaint
	}

	pad := u.px(22)
	tile := gfx.Rect{X: box.X + pad, Y: box.Y + pad, W: u.px(52), H: u.px(52)}
	if stacked {
		tile = gfx.Rect{X: box.X + pad, Y: box.Y + (box.H-u.px(52))/2, W: u.px(52), H: u.px(52)}
	}
	u.c.RoundRect(tile, u.px(12), gfx.Mix(colSurface, c.hue, 0.16))
	u.icon(c.glyph, tile, 26, hue)

	tx, ty, tw := box.X+pad, tile.Bottom()+u.px(18), box.W-2*pad
	bw := startActionWidth(u, c)
	if stacked {
		tx = tile.Right() + u.px(18)
		tw = box.Right() - 2*pad - bw - tx
		lines := float32(len(u.face(12.5).Wrap(c.text, tw)))
		ty = box.Y + (box.H-u.px(28)-lines*lineH)/2
	}
	u.label(u.bold(16), gfx.Rect{X: tx, Y: ty, W: tw, H: u.px(24)}, c.title, fg, 0)
	tf := u.face(12.5)
	for i, line := range tf.Wrap(c.text, tw) {
		u.c.Text(tf, tx, ty+u.px(28)+float32(i)*lineH+(lineH-tf.Height())/2, line, colDim)
	}

	// The action looks like a button, coloured by its card, and lights with it.
	btn := gfx.Rect{X: box.X + pad, Y: box.Bottom() - pad - u.px(38), W: box.W - 2*pad, H: u.px(38)}
	if stacked {
		btn = gfx.Rect{X: box.Right() - pad - bw, Y: box.Y + (box.H-u.px(38))/2, W: bw, H: u.px(38)}
	}
	pill := gfx.Mix(colSurface, c.hue, 0.2)
	if hot {
		pill = gfx.Mix(colSurface, c.hue, 0.34)
	}
	u.c.RoundRect(btn, u.px(8), pill)
	u.label(u.face(13), btn, c.action, fg, 0.5)

	u.hint(c.id, c.hint, box)
	if c.enabled && u.clicked(c.id, box) {
		c.run()
	}
}
