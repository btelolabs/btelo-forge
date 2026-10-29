package ui

import (
	"fmt"
	"image/color"
	"os"
	"path/filepath"
	"runtime"

	"github.com/btelolabs/btelo-forge/internal/assets"
	"github.com/btelolabs/btelo-forge/internal/files"
	"github.com/btelolabs/btelo-forge/internal/gfx"
)

// The video editor before it has a video: where the picture will play, a place to drop videos, a list of the
// folders and videos on this computer to pick one from, a button to choose them in the system's own dialog, and the
// timeline they will go on, empty. A new tab's Edit a video card opens it, so the editor is there first and the
// videos come into it, as in any editor. Ctrl+V there opens a video copied, or its path.

// newEditor makes the current tab, when it is empty, or a new one, the video editor waiting for videos.
func (a *App) newEditor() {
	if a.occupied() {
		a.newTab()
	}
	a.editing = true
}

// emptyEditor is where the editor with no video puts its parts.
type emptyEditor struct {
	main, zone, tile, choose gfx.Rect
	text                     gfx.Rect // the title and the line under it
	up, path, list           gfx.Rect // the folder list: its button up, the folder's parts, the cards
	status                   gfx.Rect // the tools' state, when there is one to say
}

// emptyEditorLayout places the drop zone in the picture's place: along its top the symbol, the words and the button
// for the system's dialog, under them the folder list, filling it.
func (a *App) emptyEditorLayout(l layout) emptyEditor {
	u := &a.u
	var e emptyEditor
	e.main = gfx.Rect{X: l.body.X, Y: l.body.Y, W: l.body.W, H: l.timeline.Y - l.body.Y}
	e.zone = e.main.Inset(u.px(28))
	in := e.zone.Inset(u.px(16))
	e.tile = gfx.Rect{X: in.X, Y: in.Y, W: u.px(48), H: u.px(48)}
	bw := u.buttonWidth(icFolder, u.t("Choose from files…")) + u.px(24)
	e.choose = gfx.Rect{X: in.Right() - bw, Y: in.Y + u.px(4), W: bw, H: u.px(40)}
	e.text = gfx.Rect{X: e.tile.Right() + u.px(14), Y: in.Y, W: max(e.choose.X-e.tile.Right()-u.px(28), 0), H: e.tile.H}
	bar := e.tile.Bottom() + u.px(14)
	e.up = gfx.Rect{X: in.X, Y: bar + u.px(5), W: u.px(30), H: u.px(30)}
	e.path = gfx.Rect{X: e.up.Right() + u.px(4), Y: bar, W: in.Right() - e.up.Right() - u.px(4), H: u.px(40)}
	bottom := in.Bottom()
	if status, _ := a.toolsStatus(); status != "" {
		e.status = gfx.Rect{X: in.X, Y: in.Bottom() - u.px(20), W: in.W, H: u.px(20)}
		bottom = e.status.Y - u.px(8)
	}
	e.list = gfx.Rect{X: in.X, Y: e.path.Bottom() + u.px(6), W: in.W, H: max(bottom-e.path.Bottom()-u.px(6), 0)}
	return e
}

// drawEmptyEditor draws the editor with no video: a place to drop videos where the picture plays, the folders and
// videos to pick one from, and the empty timeline under it.
func (a *App) drawEmptyEditor(l layout) {
	u := &a.u
	e := a.emptyEditorLayout(l)
	u.c.Fill(e.main, colPanel)
	a.drawEmptyTimeline(l.timeline)
	u.c.RoundRect(e.zone, u.px(14), gfx.Mix(colPanel, colSurface, 0.55))
	dashedOutline(u, e.zone, u.px(1.5), u.px(10), colBorderHi)

	u.c.RoundRect(e.tile, u.px(12), gfx.Mix(colSurface, gfx.Hex(0x5aa9ff), 0.18))
	u.icon(icVideo, e.tile, 22, gfx.Hex(0x5aa9ff))
	opening := a.opening != ""
	title := u.t("Drop videos here, or pick one")
	if opening {
		title = u.tf("Opening %s …", filepath.Base(a.opening))
	}
	u.label(u.bold(16), gfx.Rect{X: e.text.X, Y: e.text.Y + u.px(2), W: e.text.W, H: u.px(24)}, title, colText, 0)
	u.label(u.face(12.5), gfx.Rect{X: e.text.X, Y: e.text.Y + u.px(26), W: e.text.W, H: u.px(20)},
		u.t("Ctrl+V opens a video copied, or its path. Several at once go one after another on the timeline; sounds become music, pictures overlays."), colDim, 0)
	free := !opening && a.export == nil
	if u.button("editor/add", e.choose, icFolder, u.t("Choose from files…"), primary, free) {
		a.chooseVideo()
	}
	a.drawVideoList(e, free)
	if status, failed := a.toolsStatus(); status != "" {
		col := colDim
		if failed {
			col = colDangerText
		}
		u.label(u.face(12), e.status, status, col, 0.5)
	}
}

// videoListPane is the video editor's list of folders and videos, made the first time it is shown: in the folder it
// showed last, else where the app keeps its videos, else the videos folder, else home.
func (a *App) videoListPane() *filesPane {
	if a.videoList != nil {
		return a.videoList
	}
	dir := a.settings.Files.VideoDir
	if !isFolder(dir) {
		dir = ""
		home, _ := os.UserHomeDir()
		videos := filepath.Join(home, "Videos")
		if runtime.GOOS == "darwin" {
			videos = filepath.Join(home, "Movies")
		}
		mine, _ := a.uploadDir()
		for _, d := range []string{mine, videos, home} {
			if d != "" && isFolder(d) {
				dir = d
				break
			}
		}
	}
	p := &filesPane{dir: filepath.Clean(dir), marked: map[string]bool{}, grid: true, g: paneGrid{size: 120,
		filter: assetFilter{what: showKind, kind: assets.Video, folders: true}}}
	a.videoList = p
	a.readPane(p)
	return p
}

// videoListGo shows dir in the video editor's list, and keeps it for the next time.
func (a *App) videoListGo(p *filesPane, dir, land string) {
	a.goTo(p, dir, land)
	a.keepVideoDir(p)
}

// keepVideoDir keeps the folder the video editor's list shows, for the next time it is made.
func (a *App) keepVideoDir(p *filesPane) {
	if p.dir != thisPC && a.settings.Files.VideoDir != p.dir {
		a.settings.Files.VideoDir = p.dir
		a.saveSettings()
	}
}

// drawVideoList draws the folders and videos of a folder as cards, as the file manager's grid shows them, with the
// way to the folder above: a click on a folder goes into it, a click on a video edits it, while free says the
// editor can take one.
func (a *App) drawVideoList(e emptyEditor, free bool) {
	u := &a.u
	p := a.videoListPane()
	const id = "editor/videos"
	up := filepath.Dir(p.dir)
	canUp := !p.atDrives() && (up != p.dir || len(files.Drives()) > 0)
	if u.iconButton(id+"/up", e.up, icUp, u.t("The folder above"), false, canUp) {
		land := filepath.Base(p.dir)
		if up == p.dir {
			up, land = thisPC, filepath.VolumeName(p.dir) // a drive's top: the drives
		}
		a.videoListGo(p, up, land)
	}
	a.drawPathBar(p, id, e.path, false)
	a.keepVideoDir(p) // a folder above, clicked in the bar
	r := e.list
	u.c.Fill(gfx.Rect{X: r.X, Y: r.Y - u.px(3), W: r.W, H: max(1, u.px(1))}, colBorder)
	if p.g.arranged != a.filterKey(p) {
		a.arrange(p) // the filter's key follows the library's tags, which the list does not show
	}
	if len(p.rows) == 0 {
		msg := u.t("No folders or videos here")
		switch {
		case p.err != nil:
			msg = u.tf("This folder cannot be read: %v", p.err)
		case p.reading:
			msg = u.t("Reading…")
		}
		u.label(u.face(13), gfx.Rect{X: r.X, Y: r.Y + u.px(24), W: r.W, H: u.px(24)}, msg, colDim, 0.5)
		return
	}
	pad := u.px(8)
	inner := gfx.Rect{X: r.X + pad, Y: r.Y + pad, W: r.W - 2*pad - u.px(6), H: r.H - pad}
	g := a.layoutGrid(p, inner.W)
	if u.hover(id+"/grid", r) && u.wheel != 0 {
		p.g.scroll -= u.wheel * u.px(90)
		u.wheel = 0
	}
	p.g.scroll = min(max(p.g.scroll, 0), max(g.height-inner.H+pad, 0))
	cardAt := func(k int) gfx.Rect {
		cr := g.cards[k]
		return gfx.Rect{X: inner.X + cr.X, Y: inner.Y + cr.Y - p.g.scroll, W: cr.W, H: cr.H}
	}
	// The card under the pointer is the one ringed, as the file manager rings the cursor's.
	p.cursor = -1
	for k := range g.cards {
		if free && !u.blocked && cardAt(k).Contains(u.mx, u.my) && r.Contains(u.mx, u.my) {
			p.cursor = k
		}
	}
	// A click is acted on once the list is drawn: going into a folder empties the rows the cards are drawn from.
	clicked := -1
	u.c.Push(r)
	for k := range g.cards {
		card := cardAt(k)
		if card.Bottom() < r.Y || card.Y > r.Bottom() {
			continue
		}
		a.drawGridCard(p, k, card, true)
		if !free {
			continue
		}
		cid := fmt.Sprintf("%s/card/%s", id, p.entries[p.rows[k]].Name)
		top, bottom := max(card.Y, r.Y), min(card.Bottom(), r.Bottom())
		shown := gfx.Rect{X: card.X, Y: top, W: card.W, H: bottom - top} // the part not scrolled out of the list
		if u.hover(cid, shown) {
			u.cursor = CursorHand
		}
		if u.clicked(cid, shown) {
			clicked = k
		}
	}
	u.c.Pop()
	// Pictures just below the view are asked for too, so scrolling finds them made.
	for k := range g.cards {
		if y := cardAt(k).Y; y > r.Bottom() && y < r.Bottom()+r.H {
			a.thumbOf(assetEntry(p, &p.entries[p.rows[k]]))
		}
	}
	if g.height > inner.H {
		h := max(r.H*r.H/(g.height+pad), u.px(24))
		y := r.Y + (r.H-h)*p.g.scroll/max(g.height-inner.H+pad, 1)
		u.c.Fill(gfx.Rect{X: r.Right() - u.px(5), Y: y, W: u.px(3), H: h}, colBorderHi)
	}
	if clicked >= 0 {
		fe := &p.entries[p.rows[clicked]]
		if path := p.path(fe); fe.Dir {
			a.videoListGo(p, path, "")
		} else {
			a.openVideo(path, false)
		}
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
