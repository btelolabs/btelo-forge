package ui

import (
	"fmt"
	"image/color"
	"math"
	"time"

	"github.com/btelolabs/btelo-forge/internal/gfx"
	"github.com/btelolabs/btelo-forge/internal/mind"
)

// The map tab: the board fills it, drawn from tiles (mind.Tiles), with what moves under the pointer — the chosen
// nodes' outlines, handles, a connector being drawn, words being typed, animations and a video playing — drawn over
// it every frame. Nothing sits over the top of the board: a style bar floats over the chosen node, and at the bottom
// right the zoom bar, with the map's theme and its other commands, under an overview of the whole board. What a
// tool bar would add is on the board's right-click menu.

// Zoom limits.
const (
	mindMinZoom = 0.01
	mindMaxZoom = 8
)

// mindTileBudget is how long a frame may spend drawing tiles; what is left is drawn in the next frames.
const mindTileBudget = 40 * time.Millisecond

// mindChrome is where the tab's floating bars are, which the board leaves alone.
type mindChrome struct {
	style, zoom, overview, status, themes gfx.Rect
}

func (c mindChrome) holds(x, y float32) bool {
	for _, r := range []gfx.Rect{c.style, c.zoom, c.overview, c.themes} {
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

// fitTo shows the board rectangle b whole in area, a part of the board's view relative to its top-left, at most at
// 100%.
func (md *mindDoc) fitTo(b mind.Rect, area gfx.Rect) {
	cx, cy := float64(area.X+area.W/2), float64(area.Y+area.H/2)
	if b.Empty() {
		md.view = mind.View{Zoom: 1, OX: math.Round(cx), OY: math.Round(cy)}
		return
	}
	z := fitZoom(b, area)
	md.view = mind.View{Zoom: z, OX: math.Round(cx - b.CX()*z), OY: math.Round(cy - b.CY()*z)}
}

// fitZoom is the zoom that shows b whole in area, with a margin, at most at 100%.
func fitZoom(b mind.Rect, area gfx.Rect) float64 {
	z := math.Min(float64(area.W)*0.94/b.W(), float64(area.H)*0.94/b.H())
	return math.Min(math.Max(z, mindMinZoom), 1)
}

// mindFitArea is the part of the board r, relative to its top-left, that showing b whole fits it in: clear of the
// zoom bar, the overview and the status line, beside them or above them, whichever shows b the larger.
func (a *App) mindFitArea(md *mindDoc, r gfx.Rect, b mind.Rect) gfx.Rect {
	u := &a.u
	c := a.mindChromeRects(md, r)
	top := u.px(8)
	corner := c.zoom // its top-left, with the overview's
	if c.overview.W > 0 {
		corner.X, corner.Y = min(corner.X, c.overview.X), min(corner.Y, c.overview.Y)
	}
	above := gfx.Rect{Y: top, W: r.W, H: corner.Y - u.px(8) - r.Y - top}
	beside := gfx.Rect{Y: top, W: corner.X - u.px(8) - r.X, H: c.status.Y - u.px(4) - r.Y - top}
	if beside.W <= 0 || beside.H <= 0 || b.Empty() || fitZoom(b, above) >= fitZoom(b, beside) {
		return above
	}
	return beside
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
		b := md.m.Bounds()
		md.fitTo(b, a.mindFitArea(md, r, b))
		md.fit = false
	}
	ch := a.mindChromeRects(md, r)
	a.mindInput(md, r, ch)
	md.m.Layout()
	regions, all := md.m.Changed()
	md.tiles.Invalidate(regions, all)
	md.overviewLater(regions, all)

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
	a.drawMindStyle(md, ch.style)
	a.drawMindZoom(md, r, ch.zoom)
	if ch.themes.W == 0 {
		a.drawMindOverview(md, r, ch.overview) // the theme panel opens over it, hiding it whole
	}
	a.drawMindThemes(md, ch.themes)
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
	if !md.whole || md.m.Routing() {
		return redrawNow
	}
	if due := md.overviewDue(); !due.IsZero() && !md.minimap { // a hidden overview catches up once shown
		return max(time.Until(due), redrawNow)
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
			u.c.Fill(box, md.m.Theme().Media)
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
			// The handle that reshapes it: a ring, filled while it is under the pointer or dragged.
			lx, ly := v.Screen(label)
			hot := float32(math.Hypot(float64(u.mx-lx), float64(u.my-ly))) <= u.px(10) || md.act != nil && md.act.kind == actBend
			if hot {
				u.cursor = CursorMove
			}
			u.c.Circle(lx, ly, u.px(7), colPanel)
			u.c.RoundOutline(gfx.Rect{X: lx - u.px(7), Y: ly - u.px(7), W: u.px(14), H: u.px(14)}, u.px(7), max(1, u.px(2)), colAccent)
			if hot {
				u.c.Circle(lx, ly, u.px(3.5), colAccent)
			}
			u.hint("mind/bend", u.t("Drag to reshape the connector; double-click for the route it finds itself"),
				gfx.Rect{X: lx - u.px(10), Y: ly - u.px(10), W: u.px(20), H: u.px(20)})
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
	zw := u.px(36*7 + 56 + 12 + 6)
	c.zoom = gfx.Rect{X: r.Right() - zw - u.px(14), Y: r.Bottom() - u.px(54), W: zw, H: u.px(40)}
	if !md.minimap {
		ow := min(u.px(220), r.W*0.3)
		c.overview = gfx.Rect{X: r.Right() - ow - u.px(14), Y: c.zoom.Y - u.px(8) - ow*0.66, W: ow, H: ow * 0.66}
	}
	c.themes = a.mindThemePanel(md, r, c.zoom)
	c.status = gfx.Rect{X: r.X + u.px(14), Y: r.Bottom() - u.px(34), W: max(r.W-zw-u.px(48), 0), H: u.px(22)}
	if items := a.mindStyleItems(md); len(items) > 0 && md.text == nil && md.cell == nil && (md.act == nil || !md.act.moved) {
		w := u.px(8)
		for _, it := range items {
			w += it.width(u)
		}
		anchor := a.mindStyleAnchor(md, r)
		x := min(max(anchor.X-w/2, r.X+u.px(8)), r.Right()-w-u.px(8))
		// Just above what is chosen, else just below, else a step or two further off, wherever the bar hides the
		// fewest of the other nodes: the nearest of those.
		best, hidden := gfx.Rect{}, -1
		for step := range 3 {
			off := u.px(44) * float32(step)
			for _, y := range []float32{anchor.Y - u.px(52) - off, anchor.Bottom() + u.px(12) + off} {
				sr := gfx.Rect{X: x, Y: y, W: w, H: u.px(40)}
				if sr.Y < r.Y+u.px(6) || sr.Bottom() > c.status.Y-u.px(4) {
					continue
				}
				if n := a.mindCovered(md, r, sr); hidden < 0 || n < hidden {
					best, hidden = sr, n
				}
			}
		}
		if hidden < 0 { // no room anywhere: over the top of the board
			best = gfx.Rect{X: x, Y: r.Y + u.px(6), W: w, H: u.px(40)}
		}
		c.style = best
	}
	return c
}

// mindCovered counts the nodes, other than those chosen, that the window rectangle sr would hide on the board r.
func (a *App) mindCovered(md *mindDoc, r, sr gfx.Rect) int {
	if md.view.Zoom == 0 {
		return 0
	}
	n := 0
	for _, node := range md.m.In(md.screenView(r).BoardRect(sr.Pixels()), true) {
		if !md.sel[node.ID] {
			n++
		}
	}
	return n
}

// floatPanel draws the background of a floating bar.
func floatPanel(u *ctx, r gfx.Rect) {
	u.c.RoundRect(r.Inset(-u.px(1)), u.px(10), gfx.Alpha(colBlack, 0.35))
	u.c.RoundRect(r, u.px(9), colSurface)
	u.c.RoundOutline(r, u.px(9), max(1, u.px(1)), colBorderHi)
}

// drawMindZoom draws the zoom bar: out, the zoom (a click fits the board), in, fit the choice, the overview, and after
// a rule the map's theme and its other commands.
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
	x += u.px(6)
	u.c.Fill(gfx.Rect{X: x - u.px(3), Y: r.Y + u.px(10), W: max(1, u.px(1)), H: r.H - u.px(20)}, colBorder)
	if btn("mind/themes", icTheme, u.t("Theme"), md.themes) {
		md.themes = !md.themes
	}
	more := gfx.Rect{X: x, Y: y, W: u.px(36), H: u.px(36)}
	if btn("mind/more", icMore, u.t("More"), a.menuOpen("mind/more")) {
		a.openMenu("mind/more", more, a.mindMoreMenu(md))
	}
}

// drawMindOverview draws the whole board small with the part in view marked; a click or drag there moves the view.
// It is drawn from tiles of its own, as the board is, so a change draws again only the small part of it that
// changed, however large the map.
func (a *App) drawMindOverview(md *mindDoc, board, r gfx.Rect) {
	if r.W == 0 {
		return
	}
	u := &a.u
	floatPanel(u, r)
	in := r.Inset(u.px(6))
	w, h := float64(in.W), float64(in.H)
	b := md.m.Bounds()
	if b.Empty() {
		return
	}
	// The fit's zoom is rounded to a few steps an octave, so a board that grows a little keeps its tiles.
	z := math.Min(math.Min(w*0.92/b.W(), h*0.92/b.H()), 2)
	z = math.Pow(2, math.Floor(math.Log2(z)*8)/8)
	ovv := mind.View{Zoom: z, OX: math.Round(float64(in.X) + w/2 - b.CX()*z), OY: math.Round(float64(in.Y) + h/2 - b.CY()*z)}
	md.overviewNow()
	opts := a.mindOptions(md)
	opts.LiveAnimated = false
	if !md.overview.Draw(u.c, in.Pixels(), md.m, ovv, opts, 8*time.Millisecond) {
		a.requestFrame()
	}
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
	text := u.tn(md.m.Len(), "%d node", "%d nodes", md.m.Len()) + " · " + u.tn(md.m.EdgeCount(), "%d connector", "%d connectors", md.m.EdgeCount())
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
