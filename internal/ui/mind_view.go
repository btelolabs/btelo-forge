package ui

import (
	"fmt"
	"image"
	"image/color"
	"math"
	"time"

	"github.com/btelolabs/btelo-forge/internal/gfx"
	"github.com/btelolabs/btelo-forge/internal/mind"
)

// The map tab: the board fills it, drawn from tiles (mind.Tiles), with what moves under the pointer — the chosen
// nodes' outlines, handles, a connector being drawn, words being typed, animations and a video playing — drawn over
// it every frame. A tool bar floats at the top, a style bar over the chosen node, and the zoom and an overview of
// the whole board at the bottom right.

// Zoom limits.
const (
	mindMinZoom = 0.01
	mindMaxZoom = 8
)

// mindTileBudget is how long a frame may spend drawing tiles; what is left is drawn in the next frames.
const mindTileBudget = 40 * time.Millisecond

// mindChrome is where the tab's floating bars are, which the board leaves alone.
type mindChrome struct {
	tools, style, zoom, overview, status gfx.Rect
}

func (c mindChrome) holds(x, y float32) bool {
	for _, r := range []gfx.Rect{c.tools, c.style, c.zoom, c.overview} {
		if r.W > 0 && r.Contains(x, y) {
			return true
		}
	}
	return false
}

// screenView is the view in window pixels, the board being at r.
func (md *mindDoc) screenView(r gfx.Rect) mind.View {
	return mind.View{Zoom: md.view.Zoom, OX: math.Round(float64(r.X)) + md.view.OX, OY: math.Round(float64(r.Y)) + md.view.OY}
}

// fitTo shows the board rectangle b whole in a board area w × h, at most at 100%.
func (md *mindDoc) fitTo(b mind.Rect, w, h float32) {
	if b.Empty() {
		md.view = mind.View{Zoom: 1, OX: math.Round(float64(w) / 2), OY: math.Round(float64(h) / 2)}
		return
	}
	z := math.Min(float64(w)*0.88/b.W(), float64(h)*0.82/b.H())
	z = math.Min(math.Max(z, mindMinZoom), 1)
	md.view = mind.View{Zoom: z, OX: math.Round(float64(w)/2 - b.CX()*z), OY: math.Round(float64(h)/2 - b.CY()*z)}
}

// zoomAt zooms to z keeping the board point under the window pixel x, y (relative to the board) in place.
func (md *mindDoc) zoomAt(x, y float32, z float64) {
	z = math.Min(math.Max(z, mindMinZoom), mindMaxZoom)
	bx := (float64(x) - md.view.OX) / md.view.Zoom
	by := (float64(y) - md.view.OY) / md.view.Zoom
	md.view = mind.View{Zoom: z, OX: math.Round(float64(x) - bx*z), OY: math.Round(float64(y) - by*z)}
}

// mindOptions are what the board is drawn with.
func (a *App) mindOptions(md *mindDoc) *mind.Options {
	return &mind.Options{Fonts: a.u.fonts, Pictures: boardPictures{a.mindPictures(), md.path}, Labels: a.mindLabels(), LiveAnimated: true}
}

// drawMind draws the map tab in r and handles what is done in it.
func (a *App) drawMind(r gfx.Rect) {
	u := &a.u
	md := a.board
	if md == nil {
		u.c.Fill(r, colPanel)
		return // the tab strip above closed this tab, or switched from it, in this frame
	}
	md.m.Layout()
	if md.fit {
		md.fitTo(md.m.Bounds(), r.W, r.H)
		md.fit = false
	}
	ch := a.mindChromeRects(md, r)
	a.mindInput(md, r, ch)
	md.m.Layout()
	regions, all := md.m.Changed()
	md.tiles.Invalidate(regions, all)

	v := md.screenView(r)
	area := r.Pixels()
	per := (area.Dx()/mind.TileSize + 2) * (area.Dy()/mind.TileSize + 2)
	md.tiles.SetLimit(per * 4)
	opts := a.mindOptions(md)
	md.whole = md.tiles.Draw(u.c, area, md.m, v, opts, mindTileBudget)
	if md.whole && md.act == nil && md.text == nil {
		md.whole = md.tiles.Prefetch(area, md.m, v, opts, 1, 6*time.Millisecond)
	}
	u.c.Push(r)
	a.drawMindLive(md, r, v)
	a.drawMindOverlay(md, r, v)
	u.c.Pop()

	ch = a.mindChromeRects(md, r) // the style bar follows the selection as it moves
	a.drawMindTools(md, ch.tools)
	a.drawMindStyle(md, ch.style)
	a.drawMindZoom(md, r, ch.zoom)
	a.drawMindOverview(md, r, ch.overview)
	a.drawMindStatus(md, ch.status)
	a.pumpMindPictures()
}

// mindWait is how soon the map tab wants to be drawn again: at once while tiles are missing or pictures are read,
// and when the next frame of an animation shown is due.
func (a *App) mindWait() time.Duration {
	md := a.board
	if md == nil {
		return 0
	}
	if !md.whole {
		return redrawNow
	}
	p := a.mindPics
	if p.pictureBusy() {
		return time.Second / 20
	}
	if p != nil {
		if due := p.animDue(); !due.IsZero() {
			return max(time.Until(due), redrawNow)
		}
	}
	return 0
}

// drawMindLive draws what plays on the board: the frames of animated pictures in view, and a video playing.
func (a *App) drawMindLive(md *mindDoc, r gfx.Rect, v mind.View) {
	u := &a.u
	p := a.mindPics
	if p == nil {
		return
	}
	view := v.BoardRect(r.Pixels())
	playing := false
	for _, ref := range p.animatedRefs() {
		for _, id := range md.m.Showing(ref) {
			n := md.m.Node(id)
			if n == nil || n.Kind != mind.Picture || md.m.Hidden(n) {
				continue
			}
			mr := md.m.MediaRect(n)
			if !mr.Overlaps(view) {
				continue
			}
			box := v.ScreenRect(mr)
			if box.W < 4 || box.H < 4 {
				continue
			}
			playing = true
			frame := a.animFrame(n.Ref)
			if frame == nil {
				continue
			}
			u.c.Push(box)
			u.c.Fill(box, gfx.Hex(0x2a2c33))
			u.c.Image(frame, fitRect(box, float32(frame.Rect.Dx()), float32(frame.Rect.Dy())))
			u.c.Pop()
		}
	}
	if !playing {
		clear(p.anims) // nothing animated in view: none plays, and none is woken for
	}
	if n := md.m.Node(md.video); n != nil && n.Kind == mind.Video && !md.m.Hidden(n) {
		box := v.ScreenRect(md.m.MediaRect(n))
		if box.Intersects(r) && box.W >= 48 && box.H >= 32 {
			a.drawFilePreview(mindVideoID(md), box, n.Ref)
		} else {
			md.video = 0
		}
	} else if md.video != 0 {
		md.video = 0
		if a.preview.id == mindVideoID(md) {
			a.closeFilePreview()
		}
	}
}

// mindVideoID names the place a map tab plays its video in.
func mindVideoID(md *mindDoc) string { return fmt.Sprintf("mind/video/%p", md) }

// drawMindOverlay draws over the tiles what follows the pointer: hover and selection outlines, handles, the drag's
// ghost and drop place, a connector being drawn, the marquee and the words being typed.
func (a *App) drawMindOverlay(md *mindDoc, r gfx.Rect, v mind.View) {
	u := &a.u
	m := md.m
	lw := max(1, u.px(2))
	// A chosen node is lit softly from behind its outline, as MindNode does: a tint, and a ring of the accent.
	outline := func(n *mind.Node, col color.RGBA, w float32) {
		b := v.ScreenRect(m.Box(n)).Inset(-u.px(5))
		radius := min(u.px(10)+u.px(5), b.H/2)
		if n.Kind == mind.Topic {
			u.c.RoundRect(b, radius, gfx.Alpha(col, 0.16)) // never over a picture
		}
		u.c.RoundOutline(b, radius, w, gfx.Alpha(col, 0.9))
	}
	if e := m.Edge(md.edge); e != nil {
		if path, label, ok := m.EdgePath(e); ok {
			for i := 1; i < len(path); i++ {
				x0, y0 := v.Screen(path[i-1])
				x1, y1 := v.Screen(path[i])
				u.c.Line(x0, y0, x1, y1, lw+u.px(2), gfx.Alpha(colAccent, 0.55))
			}
			lx, ly := v.Screen(label)
			u.c.Circle(lx, ly, u.px(4), colAccent)
		}
	}
	if act := md.act; act != nil && act.kind == actMove && act.moved {
		a.drawMindDrag(md, v)
	}
	if hov := a.mindHover(md, r, v); hov != nil && !md.sel[hov.ID] && md.act == nil {
		b := v.ScreenRect(m.Box(hov)).Inset(-u.px(5))
		u.c.RoundOutline(b, min(u.px(15), b.H/2), max(1, u.px(1)), gfx.Alpha(colAccent, 0.45))
	}
	for id := range md.sel {
		if n := m.Node(id); n != nil && !m.Hidden(n) {
			outline(n, colAccent, lw)
		}
	}
	if n := a.mindHandlesFor(md); n != nil {
		for _, h := range a.mindHandles(md, n, v) {
			hot := u.mx >= h.r.X && u.mx < h.r.Right() && u.my >= h.r.Y && u.my < h.r.Bottom()
			col := gfx.Alpha(colAccent, 0.8)
			if hot {
				col = colAccentHi
			}
			if h.side == 0 {
				u.c.Fill(h.r, colPanel)
				u.c.Outline(h.r, max(1, u.px(1.5)), col)
				continue
			}
			u.c.Circle(h.r.X+h.r.W/2, h.r.Y+h.r.H/2, h.r.W/2, colPanel)
			u.c.RoundOutline(h.r, h.r.W/2, max(1, u.px(1.5)), col)
			u.c.Circle(h.r.X+h.r.W/2, h.r.Y+h.r.H/2, h.r.W/2-u.px(3.5), col)
		}
	}
	if act := md.act; act != nil {
		switch act.kind {
		case actConnect:
			from := m.Node(act.id)
			if from == nil {
				break
			}
			p, _ := m.Anchor(from, act.side)
			x0, y0 := v.Screen(p)
			x1, y1 := u.mx, u.my
			if t := m.Node(act.target); t != nil {
				outline(t, colAccent, lw)
				q, _ := m.Anchor(t, nearestSide(m.Box(t), v.Board(float64(u.mx), float64(u.my))))
				x1, y1 = v.Screen(q)
			}
			u.c.Line(x0, y0, x1, y1, max(1, u.px(2)), colAccent)
			u.c.Circle(x1, y1, u.px(4), colAccent)
		case actMarquee:
			if act.moved {
				m0x, m0y := v.Screen(act.from)
				box := gfx.Rect{X: min(m0x, u.mx), Y: min(m0y, u.my), W: abs32(u.mx - m0x), H: abs32(u.my - m0y)}
				u.c.Fill(box, gfx.Alpha(colAccent, 0.12))
				u.c.Outline(box, max(1, u.px(1)), gfx.Alpha(colAccent, 0.8))
			}
		}
	}
	a.drawMindText(md, v)
	a.drawMindCell(md, v)
}

// drawMindDrag draws the nodes being moved into a tree where they would go, and the place they would take there.
func (a *App) drawMindDrag(md *mindDoc, v mind.View) {
	u := &a.u
	act := md.act
	m := md.m
	if !act.tree {
		return // free nodes move themselves
	}
	n := m.Node(act.id)
	if n == nil {
		return
	}
	// The ghost: the node drawn where the pointer holds it, its subtree left out by the clip.
	b := m.Box(n)
	dx := float64(u.mx) - float64(act.grab.X)
	dy := float64(u.my) - float64(act.grab.Y)
	gv := mind.View{Zoom: v.Zoom, OX: v.OX + dx, OY: v.OY + dy}
	ghost := gv.ScreenRect(b)
	u.c.RoundRect(ghost.Inset(-u.px(2)), u.px(6), gfx.Alpha(colAccent, 0.18))
	u.c.Push(ghost.Inset(-u.px(1)))
	mind.RenderNode(u.c, m, gv, &mind.Options{Fonts: u.fonts, Pictures: boardPictures{a.mindPictures(), md.path}, Labels: a.mindLabels(),
		Skip: map[mind.ID]bool{}}, n)
	u.c.Pop()
	u.c.RoundOutline(ghost.Inset(-u.px(2)), u.px(6), max(1, u.px(1.5)), colAccent)
	switch d := act.drop; {
	case d.parent != 0 && d.index < 0:
		if t := m.Node(d.parent); t != nil {
			tb := v.ScreenRect(m.Box(t))
			u.c.RoundRect(tb.Inset(-u.px(4)), u.px(8), gfx.Alpha(colAccent, 0.2))
			u.c.RoundOutline(tb.Inset(-u.px(4)), u.px(8), max(1, u.px(2)), colAccent)
		}
	case d.parent != 0:
		x0, y0 := v.Screen(d.line[0])
		x1, y1 := v.Screen(d.line[1])
		u.c.Line(x0, y0, x1, y1, max(2, u.px(3)), colAccent)
		u.c.Circle(x0, y0, u.px(4), colAccent)
	}
}

// mindChromeRects places the floating bars in the board r.
func (a *App) mindChromeRects(md *mindDoc, r gfx.Rect) mindChrome {
	u := &a.u
	var c mindChrome
	tw := u.px(36)*float32(len(mindToolList)) + u.px(36)*3 + u.px(12)
	c.tools = gfx.Rect{X: r.X + (r.W-tw)/2, Y: r.Y + u.px(12), W: tw, H: u.px(44)}
	zw := u.px(36*5 + 56 + 12)
	c.zoom = gfx.Rect{X: r.Right() - zw - u.px(14), Y: r.Bottom() - u.px(54), W: zw, H: u.px(40)}
	if !md.minimap {
		ow := min(u.px(220), r.W*0.3)
		c.overview = gfx.Rect{X: r.Right() - ow - u.px(14), Y: c.zoom.Y - u.px(8) - ow*0.66, W: ow, H: ow * 0.66}
	}
	c.status = gfx.Rect{X: r.X + u.px(14), Y: r.Bottom() - u.px(34), W: max(r.W-zw-u.px(48), 0), H: u.px(22)}
	if items := a.mindStyleItems(md); len(items) > 0 && md.text == nil && md.cell == nil && (md.act == nil || !md.act.moved) {
		w := u.px(8)
		for _, it := range items {
			w += it.width(u)
		}
		anchor := a.mindStyleAnchor(md, r)
		x := min(max(anchor.X-w/2, r.X+u.px(8)), r.Right()-w-u.px(8))
		y := anchor.Y - u.px(52)
		if y < c.tools.Bottom()+u.px(6) {
			y = anchor.Bottom() + u.px(12)
		}
		c.style = gfx.Rect{X: x, Y: y, W: w, H: u.px(40)}
	}
	return c
}

// drawMindTools draws the tool bar: what a click on the board adds, and undo and redo.
func (a *App) drawMindTools(md *mindDoc, r gfx.Rect) {
	u := &a.u
	floatPanel(u, r)
	x := r.X + u.px(6)
	for _, t := range mindToolList {
		b := gfx.Rect{X: x, Y: r.Y + u.px(4), W: u.px(36), H: u.px(36)}
		label, glyph := a.mindToolLabel(t)
		on := md.tool == t
		if u.iconButton(fmt.Sprintf("mind/tool/%d", t), b, glyph, label, on, true) {
			a.pickMindTool(md, t, b)
		}
		x += u.px(36)
	}
	x += u.px(6)
	u.c.Fill(gfx.Rect{X: x - u.px(3), Y: r.Y + u.px(10), W: max(1, u.px(1)), H: r.H - u.px(20)}, colBorder)
	undo := gfx.Rect{X: x, Y: r.Y + u.px(4), W: u.px(36), H: u.px(36)}
	if u.iconButton("mind/undo", undo, icUndo, withKey(u.t("Undo"), a.mindKeyHint("mind.undo")), false, md.m.CanUndo()) {
		a.mindUndo(md)
	}
	redo := gfx.Rect{X: undo.Right(), Y: undo.Y, W: undo.W, H: undo.H}
	if u.iconButton("mind/redo", redo, icRestore, withKey(u.t("Redo"), a.mindKeyHint("mind.redo")), false, md.m.CanRedo()) {
		a.mindRedo(md)
	}
	more := gfx.Rect{X: redo.Right(), Y: undo.Y, W: undo.W, H: undo.H}
	if u.iconButton("mind/more", more, icMore, u.t("More"), a.menuOpen("mind/more"), true) {
		a.openMenu("mind/more", more, a.mindMoreMenu(md))
	}
}

// floatPanel draws the background of a floating bar.
func floatPanel(u *ctx, r gfx.Rect) {
	u.c.RoundRect(r.Inset(-u.px(1)), u.px(10), gfx.Alpha(colBlack, 0.35))
	u.c.RoundRect(r, u.px(9), colSurface)
	u.c.RoundOutline(r, u.px(9), max(1, u.px(1)), colBorderHi)
}

// drawMindZoom draws the zoom bar: out, the zoom (a click fits the board), in, fit the choice, and the overview.
func (a *App) drawMindZoom(md *mindDoc, board, r gfx.Rect) {
	u := &a.u
	floatPanel(u, r)
	x := r.X + u.px(6)
	y := r.Y + u.px(2)
	btn := func(id, glyph, hint string, on bool) bool {
		b := gfx.Rect{X: x, Y: y, W: u.px(36), H: u.px(36)}
		x += u.px(36)
		return u.iconButton(id, b, glyph, hint, on, true)
	}
	centre := func(z float64) { md.zoomAt(board.W/2, board.H/2, z) }
	if btn("mind/zoom-out", icZoomOut, u.t("Zoom out"), false) {
		centre(md.view.Zoom / 1.25)
	}
	pct := gfx.Rect{X: x, Y: y, W: u.px(56), H: u.px(36)}
	x += pct.W
	if u.hover("mind/zoom-pct", pct) {
		u.c.Fill(pct, colSurfaceHi)
		u.cursor = CursorHand
	}
	u.label(u.face(12.5), pct, fmt.Sprintf("%d%%", int(math.Round(md.view.Zoom*100))), colText, 0.5)
	u.hint("mind/zoom-pct", withKey(u.t("Actual size"), a.mindKeyHint("mind.actual-size")), pct)
	if u.clicked("mind/zoom-pct", pct) {
		centre(1)
	}
	if btn("mind/zoom-in", icZoomIn, u.t("Zoom in"), false) {
		centre(md.view.Zoom * 1.25)
	}
	if btn("mind/fit", icFitBoard, withKey(u.t("Show the whole board"), a.mindKeyHint("mind.fit")), false) {
		md.fit = true
	}
	if btn("mind/fit-sel", icFocus, withKey(u.t("Zoom to the chosen nodes"), a.mindKeyHint("mind.fit-selection")), false) {
		a.mindZoomToSelection(md, board)
	}
	if btn("mind/overview", icMinimap, u.t("Show or hide the overview"), !md.minimap) {
		md.minimap = !md.minimap
	}
}

// mindOverview is the picture of the whole board the overview shows, kept until the board changes.
type mindOverview struct {
	img     *image.RGBA
	bounds  mind.Rect
	version uint64
	made    time.Time
	w, h    int
}

// drawMindOverview draws the whole board small with the part in view marked; a click or drag there moves the view.
func (a *App) drawMindOverview(md *mindDoc, board, r gfx.Rect) {
	if r.W == 0 {
		return
	}
	u := &a.u
	floatPanel(u, r)
	in := r.Inset(u.px(6))
	ov := &md.overview
	w, h := int(in.W), int(in.H)
	// Drawn again when the board changed: at once when nothing is being dragged or typed, else at most three times
	// a second.
	busy := md.act != nil || md.text != nil
	stale := ov.img == nil || ov.w != w || ov.h != h || ov.version != md.m.Version() && (!busy || time.Since(ov.made) > 300*time.Millisecond)
	if stale {
		b := md.m.Bounds()
		vb := md.screenView(board).BoardRect(board.Pixels())
		if !b.Empty() {
			b = b.Union(vb)
		} else {
			b = vb
		}
		ov.img = mind.RenderFit(md.m, w, h, &mind.Options{Fonts: u.fonts, Pictures: boardPictures{a.mindPictures(), md.path}, Labels: a.mindLabels()})
		ov.bounds, ov.version, ov.made, ov.w, ov.h = b, md.m.Version(), time.Now(), w, h
	} else if ov.version != md.m.Version() {
		a.requestFrame()
	}
	u.c.Blit(ov.img, int(in.X), int(in.Y), true)
	// The overview fits the board's bounds as RenderFit does: the same sums place the view on it.
	b := md.m.Bounds()
	if b.Empty() {
		return
	}
	z := math.Min(math.Min(float64(w)*0.92/b.W(), float64(h)*0.92/b.H()), 2)
	ovv := mind.View{Zoom: z, OX: float64(in.X) + float64(w)/2 - b.CX()*z, OY: float64(in.Y) + float64(h)/2 - b.CY()*z}
	vb := md.screenView(board).BoardRect(board.Pixels())
	vr := ovv.ScreenRect(vb)
	u.c.Push(in)
	u.c.Fill(vr, gfx.Alpha(colAccent, 0.12))
	u.c.Outline(vr, max(1, u.px(1.5)), colAccent)
	u.c.Pop()
	const id = "mind/overview"
	if u.hover(id, in) {
		u.cursor = CursorHand
	}
	u.press(id, in)
	if u.active == id && u.down {
		p := ovv.Board(float64(u.mx), float64(u.my))
		md.view.OX = math.Round(float64(board.W)/2 - p.X*md.view.Zoom)
		md.view.OY = math.Round(float64(board.H)/2 - p.Y*md.view.Zoom)
	}
	u.letGo(id)
}

// drawMindStatus draws, at the bottom left, how much the board holds and whether it is saved.
func (a *App) drawMindStatus(md *mindDoc, r gfx.Rect) {
	u := &a.u
	text := u.tf("%d nodes · %d connectors", md.m.Len(), md.m.EdgeCount())
	if len(md.sel) > 1 {
		text += " · " + u.tf("%d chosen", len(md.sel))
	}
	switch {
	case md.saveErr != nil:
		text += " · " + u.t("not saved")
	case md.saving || md.dirty:
		text += " · " + u.t("saving…")
	}
	f := u.face(11.5)
	text = f.Ellipsize(text, r.W)
	u.c.RoundRect(gfx.Rect{X: r.X - u.px(8), Y: r.Y, W: f.Measure(text) + u.px(16), H: r.H}, u.px(6), gfx.Alpha(colPanel, 0.85))
	u.label(f, r, text, colDim, 0)
}
