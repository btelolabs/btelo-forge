package ui

import (
	"fmt"
	"path/filepath"

	"github.com/btelolabs/btelo-video-cut/internal/gfx"
)

// drawTabs is the browser-like strip at the top: one tab per edit, a button for a new tab, and settings at the end.
func (a *App) drawTabs(r gfx.Rect) {
	u := &a.u
	u.c.Fill(r, colBackground)
	gear := gfx.Rect{X: r.Right() - u.px(40), Y: r.Y + u.px(5), W: u.px(32), H: u.px(30)}
	if u.iconButton("tabs/settings", gear, icSettings, u.t("Settings"), a.settingsOpen, a.export == nil) {
		a.settingsOpen = true
	}
	plusW := u.px(32)
	space := gear.X - u.px(8) - r.X - u.px(8) - plusW - u.px(4)
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

// drawEmpty is a new tab: a drop zone for the first video.
func (a *App) drawEmpty(r gfx.Rect) {
	u := &a.u
	u.c.Fill(r, colPanel)
	w, h := min(u.px(700), r.W-u.px(48)), min(u.px(320), r.H-u.px(48))
	box := gfx.Rect{X: r.X + (r.W-w)/2, Y: r.Y + (r.H-h)/2, W: w, H: h}
	hot := u.hover("empty/drop", box)
	bg := colSurface
	if hot {
		bg = gfx.Mix(colSurface, colAccent, 0.06)
	}
	u.c.RoundRect(box, u.px(18), bg)
	u.c.RoundOutline(box, u.px(18), u.px(1.5), gfx.Mix(colBorder, colAccent, 0.4))
	circle := gfx.Rect{X: box.X + box.W/2 - u.px(34), Y: box.Y + u.px(36), W: u.px(68), H: u.px(68)}
	drawLogo(u, circle)
	title := u.t("Drop a video here to start editing")
	if a.opening != "" {
		title = u.tf("Opening %s …", filepath.Base(a.opening))
	}
	u.label(u.bold(18), gfx.Rect{X: box.X, Y: circle.Bottom() + u.px(18), W: box.W, H: u.px(28)}, title, colText, 0.5)
	sf := u.face(12.5)
	u.label(sf, gfx.Rect{X: box.X + u.px(20), Y: circle.Bottom() + u.px(52), W: box.W - u.px(40), H: u.px(20)},
		u.t("mp4, mov, mkv, avi, webm and other common formats · cut, change speed, add music and text, compress"), colDim, 0.5)
	u.label(sf, gfx.Rect{X: box.X + u.px(20), Y: circle.Bottom() + u.px(74), W: box.W - u.px(40), H: u.px(20)},
		u.t("Drop more videos later to add them to the end; open another tab for a separate edit"), colDim, 0.5)
	br := gfx.Rect{X: box.X + box.W/2 - u.px(80), Y: box.Bottom() - u.px(60), W: u.px(160), H: u.px(38)}
	if u.button("empty/open", br, icFolder, u.t("Choose a video…"), primary, a.opening == "") {
		a.chooseVideo()
	}
}
