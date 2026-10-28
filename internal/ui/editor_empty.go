package ui

import (
	"image/color"
	"path/filepath"

	"github.com/btelolabs/btelo-forge/internal/gfx"
)

// The video editor before it has a video: where the picture will play, a place to drop videos or to add them
// with a button, and the timeline they will go on, empty. A new tab's Edit a video card opens it, so the editor is
// there first and the videos come into it, as in any editor.

// newEditor makes the current tab, when it is empty, or a new one, the video editor waiting for videos.
func (a *App) newEditor() {
	if a.occupied() {
		a.newTab()
	}
	a.editing = true
}

// emptyEditor is where the editor with no video puts its parts.
type emptyEditor struct {
	main, zone, tile, add gfx.Rect
	title, text           float32 // the tops of the title and the line under it
}

// emptyEditorLayout places the drop zone in the picture's place, its symbol, words and button centred in it.
func (a *App) emptyEditorLayout(l layout) emptyEditor {
	u := &a.u
	var e emptyEditor
	e.main = gfx.Rect{X: l.body.X, Y: l.body.Y, W: l.body.W, H: l.timeline.Y - l.body.Y}
	e.zone = e.main.Inset(u.px(28))
	blockH := u.px(64 + 18 + 40 + 48 + 40)
	y := e.zone.Y + max((e.zone.H-blockH)/2, u.px(16))
	e.tile = gfx.Rect{X: e.zone.X + e.zone.W/2 - u.px(32), Y: y, W: u.px(64), H: u.px(64)}
	e.title = e.tile.Bottom() + u.px(18)
	e.text = e.title + u.px(40)
	bw := u.buttonWidth(icAdd, u.t("Add videos…")) + u.px(24)
	e.add = gfx.Rect{X: e.zone.X + (e.zone.W-bw)/2, Y: e.text + u.px(48), W: bw, H: u.px(40)}
	return e
}

// drawEmptyEditor draws the editor with no video: a place to drop or add videos where the picture plays, and the
// empty timeline under it.
func (a *App) drawEmptyEditor(l layout) {
	u := &a.u
	e := a.emptyEditorLayout(l)
	u.c.Fill(e.main, colPanel)
	a.drawEmptyTimeline(l.timeline)
	u.c.RoundRect(e.zone, u.px(14), gfx.Mix(colPanel, colSurface, 0.55))
	dashedOutline(u, e.zone, u.px(1.5), u.px(10), colBorderHi)

	u.c.RoundRect(e.tile, u.px(14), gfx.Mix(colSurface, gfx.Hex(0x5aa9ff), 0.18))
	u.icon(icVideo, e.tile, 28, gfx.Hex(0x5aa9ff))
	opening := a.opening != ""
	title := u.t("Drop videos here")
	if opening {
		title = u.tf("Opening %s …", filepath.Base(a.opening))
	}
	u.label(u.bold(18), gfx.Rect{X: e.zone.X, Y: e.title, W: e.zone.W, H: u.px(30)}, title, colText, 0.5)
	u.label(u.face(13), gfx.Rect{X: e.zone.X + u.px(24), Y: e.text, W: e.zone.W - u.px(48), H: u.px(22)},
		u.t("Several at once go one after another on the timeline; sounds become music, pictures overlays."), colDim, 0.5)
	if u.button("editor/add", e.add, icAdd, u.t("Add videos…"), primary, !opening && a.export == nil) {
		a.chooseVideo()
	}
	if status, failed := a.toolsStatus(); status != "" {
		col := colDim
		if failed {
			col = colDangerText
		}
		u.label(u.face(12), gfx.Rect{X: e.zone.X, Y: e.add.Bottom() + u.px(16), W: e.zone.W, H: u.px(20)}, status, col, 0.5)
	}
}

// drawEmptyTimeline draws the timeline with nothing on it: its ruler and an empty video track.
func (a *App) drawEmptyTimeline(r gfx.Rect) {
	u := &a.u
	u.c.Fill(r, colBackground)
	u.c.Fill(gfx.Rect{X: r.X, Y: r.Y, W: r.W, H: max(1, u.px(1))}, colBorder)
	ruler := gfx.Rect{X: r.X, Y: r.Y, W: r.W, H: u.px(28)}
	f := u.face(10.5)
	for i := 0; ; i++ {
		x := r.X + u.px(96) + float32(i)*u.px(120)
		if x > r.Right() {
			break
		}
		u.c.Fill(gfx.Rect{X: x, Y: ruler.Bottom() - u.px(8), W: max(1, u.px(1)), H: u.px(8)}, colBorderHi)
		u.c.Text(f, x+u.px(4), ruler.Y+u.px(6), shortClock(float64(i*5)), colFaint)
	}
	lane := gfx.Rect{X: r.X + u.px(96), Y: ruler.Bottom() + u.px(14), W: r.W - u.px(112), H: u.px(64)}
	u.label(u.face(12), gfx.Rect{X: r.X + u.px(12), Y: lane.Y, W: u.px(80), H: lane.H}, u.t("Video"), colDim, 0)
	u.c.RoundRect(lane, u.px(6), colSurface)
	dashedOutline(u, lane, max(1, u.px(1)), u.px(8), colBorder)
	u.label(u.face(12), lane, u.t("The videos you add go here"), colFaint, 0.5)
}

// dashedOutline draws r's edge as dashes of length dash.
func dashedOutline(u *ctx, r gfx.Rect, width, dash float32, col color.RGBA) {
	step := dash * 1.8
	for x := r.X + dash; x+dash < r.Right()-dash; x += step {
		u.c.Fill(gfx.Rect{X: x, Y: r.Y, W: dash, H: width}, col)
		u.c.Fill(gfx.Rect{X: x, Y: r.Bottom() - width, W: dash, H: width}, col)
	}
	for y := r.Y + dash; y+dash < r.Bottom()-dash; y += step {
		u.c.Fill(gfx.Rect{X: r.X, Y: y, W: width, H: dash}, col)
		u.c.Fill(gfx.Rect{X: r.Right() - width, Y: y, W: width, H: dash}, col)
	}
}
