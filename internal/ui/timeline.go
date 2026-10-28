package ui

import (
	"fmt"
	"math"
	"slices"

	"github.com/btelolabs/btelo-forge/internal/gfx"
)

// timelineView is the visible part of the timeline: pixels per second (0 fits the whole edit), the time at the left
// edge and how far the tracks are scrolled down.
type timelineView struct {
	pps     float64
	scroll  float64
	vscroll float32
}

// lane is one track row: its group and index, the clip area and the header on the left.
type lane struct {
	group  group
	track  int
	rect   gfx.Rect
	header gfx.Rect
}

// tracks are the rectangles of one timeline frame.
type tracks struct {
	area, ruler, view, bar gfx.Rect
	headerW                float32
	lanes                  []lane
	contentH               float32
	x0, pps                float64
}

func (t tracks) x(at float64) float32 { return float32(float64(t.area.X) + (at-t.x0)*t.pps) }
func (t tracks) at(x float32) float64 { return t.x0 + float64(x-t.area.X)/t.pps }
func (t tracks) seconds() float64     { return float64(t.area.W) / t.pps }

// lane returns the row of a track.
func (t tracks) lane(g group, track int) (lane, bool) {
	for _, l := range t.lanes {
		if l.group == g && l.track == track {
			return l, true
		}
	}
	return lane{}, false
}

// target maps a height to a track of group g while dragging: an existing track, or one past the last (a new track)
// when the pointer goes beyond the group's outer edge (above for overlays and video, below for music).
func (t tracks) target(g group, y float32, count int) int {
	var first, last lane
	found := false
	for _, l := range t.lanes {
		if l.group != g {
			continue
		}
		if l.rect.Y <= y && y < l.rect.Bottom()+4 {
			return l.track
		}
		if !found {
			first, found = l, true
		}
		last = l
	}
	if !found {
		return 0
	}
	if g == groupMusic {
		if y >= last.rect.Bottom() {
			return count
		}
		return last.track
	}
	if y < first.rect.Y {
		return count
	}
	return last.track
}

func (a *App) laneHeight(g group, track int) float32 {
	u := &a.u
	switch {
	case g == groupOverlay:
		return u.px(34)
	case g == groupMusic:
		return u.px(44)
	case track == 0:
		return u.px(72)
	}
	return u.px(54)
}

// fitScale is the pixels per second that show the whole edit in width w.
func (a *App) fitScale(w float32) float64 {
	return float64(w-a.u.px(24)) / math.Max(a.p.ContentEnd(), 0.5)
}

// scrollEnd is the last time the view may scroll to, leaving the same margin as the fitted view.
func (a *App) scrollEnd(t tracks) float64 { return a.p.ContentEnd() + float64(a.u.px(24))/t.pps }

func (a *App) tracks(r gfx.Rect) tracks {
	u := &a.u
	var t tracks
	t.headerW = u.px(140)
	top := r.Y + u.px(38)
	t.ruler = gfx.Rect{X: r.X + t.headerW, Y: top, W: r.W - t.headerW - u.px(12), H: u.px(22)}
	t.bar = gfx.Rect{X: t.ruler.X, Y: r.Bottom() - u.px(12), W: t.ruler.W, H: u.px(7)}
	t.view = gfx.Rect{X: r.X, Y: t.ruler.Bottom() + u.px(4), W: r.W, H: t.bar.Y - u.px(4) - t.ruler.Bottom() - u.px(4)}
	t.area = gfx.Rect{X: t.ruler.X, Y: t.view.Y, W: t.ruler.W, H: t.view.H}
	t.pps = a.fitScale(t.area.W)
	if a.view.pps > 0 {
		t.pps = a.view.pps * float64(u.s)
	} else {
		a.view.scroll = 0
	}
	a.view.scroll = math.Min(math.Max(a.view.scroll, 0), math.Max(0, a.scrollEnd(t)-t.seconds()))
	t.x0 = a.view.scroll

	gap := u.px(4)
	order, h := a.laneOrder()
	t.contentH = h
	a.view.vscroll = min(max(a.view.vscroll, 0), max(0, h-t.view.H))
	y := t.view.Y - a.view.vscroll
	prev := group(-1)
	for i, l := range order {
		if i > 0 && l.group != prev {
			y += u.px(8)
		}
		prev = l.group
		lh := a.laneHeight(l.group, l.track)
		l.rect = gfx.Rect{X: t.area.X, Y: y, W: t.area.W, H: lh}
		l.header = gfx.Rect{X: r.X + u.px(8), Y: y, W: t.headerW - u.px(14), H: lh}
		t.lanes = append(t.lanes, l)
		y += lh + gap
	}
	return t
}

// laneOrder lists the lanes from top to bottom (overlays, video, music) and the height they need together.
func (a *App) laneOrder() ([]lane, float32) {
	u := &a.u
	var order []lane
	for k := len(a.p.Overlays) - 1; k >= 0; k-- {
		order = append(order, lane{group: groupOverlay, track: k})
	}
	for k := len(a.p.Video) - 1; k >= 0; k-- {
		order = append(order, lane{group: groupVideo, track: k})
	}
	for k := range a.p.Music {
		order = append(order, lane{group: groupMusic, track: k})
	}
	h := float32(0)
	prev := group(-1)
	for i := range order {
		if i > 0 && order[i].group != prev {
			h += u.px(8)
		}
		prev = order[i].group
		h += a.laneHeight(order[i].group, order[i].track) + u.px(4)
	}
	return order, h
}

// timelineChrome is the height of the timeline around its lanes: toolbar, ruler and scroll bar.
func (a *App) timelineChrome() float32 { return a.u.px(38 + 22 + 4 + 4 + 4 + 12) }

// zoom multiplies the scale around output time pivot, keeping it under the same pixel.
func (a *App) zoom(t tracks, factor float64, pivot float64) {
	px := float64(a.u.s)
	fit := a.fitScale(t.area.W) / px
	cur := t.pps / px
	next := math.Min(math.Max(cur*factor, fit), 600)
	if next <= fit*1.0001 {
		a.view.pps = 0
		return
	}
	offset := (pivot - t.x0) * cur
	a.view.pps = next
	a.view.scroll = pivot - offset/next
}

func (a *App) drawTimeline(r gfx.Rect) {
	u := &a.u
	u.c.Fill(r, colPanel)
	u.c.Fill(gfx.Rect{X: r.X, Y: r.Y, W: r.W, H: max(1, u.px(1))}, colBorder)
	t := a.tracks(r)
	a.timelineToolbar(r, t)
	if u.hover("tl/wheel", gfx.Rect{X: r.X, Y: t.ruler.Y, W: r.W, H: r.Bottom() - t.ruler.Y}) && u.wheel != 0 {
		switch {
		case u.ctrl:
			a.zoom(t, math.Pow(1.25, float64(u.wheel)), t.at(u.mx))
		case u.shift || t.contentH <= t.view.H:
			if a.view.pps == 0 {
				a.view.pps = t.pps / float64(u.s)
			}
			a.view.scroll -= float64(u.wheel) * float64(u.px(80)) / t.pps
		default:
			a.view.vscroll -= u.wheel * u.px(60)
		}
		u.wheel = 0
		t = a.tracks(r)
	}
	if a.player.Playing() {
		if x := t.x(a.playhead); x > t.area.Right()-u.px(40) || x < t.area.X {
			a.view.scroll = a.playhead - t.seconds()*0.15
			if a.view.pps == 0 {
				a.view.pps = t.pps / float64(u.s)
			}
			t = a.tracks(r)
		}
	}
	u.c.Push(t.view)
	for _, l := range t.lanes {
		u.c.RoundRect(l.rect, u.px(6), gfx.Mix(colPanel, colBackground, 0.55))
		a.drawLaneHeader(l)
	}
	u.c.Pop()
	u.c.Push(gfx.Rect{X: t.area.X - u.px(2), Y: t.view.Y, W: t.area.W + u.px(4), H: t.view.H})
	a.drawVideoClips(t)
	a.drawOverlayItems(t)
	a.drawMusicClips(t)
	a.trackBackground(t)
	u.c.Pop()
	u.c.Push(gfx.Rect{X: t.area.X - u.px(8), Y: t.ruler.Y, W: t.area.W + u.px(16), H: r.Bottom() - t.ruler.Y})
	a.drawRuler(t)
	a.drawPlayhead(t)
	u.c.Pop()
	a.drawScrollbars(t)
}

func (a *App) timelineToolbar(r gfx.Rect, t tracks) {
	u := &a.u
	y := r.Y + u.px(6)
	h := u.px(28)
	x := r.X + u.px(12)
	for _, b := range []struct {
		id, glyph, label string
		run              func()
	}{
		{"tl/addtext", icFont, u.t("Text"), a.addText},
		{"tl/addimage", icImage, u.t("Image"), a.chooseImage},
		{"tl/addvideo", icVideo, u.t("Video"), a.chooseVideo},
		{"tl/addmusic", icMusic, u.t("Music"), func() { a.showPanel(tabMusic) }},
	} {
		w := u.face(13).Measure(b.label) + u.px(44)
		if u.button(b.id, gfx.Rect{X: x, Y: y, W: w, H: h}, b.glyph, b.label, ghost, true) {
			b.run()
		}
		x += w + u.px(4)
	}
	x += u.px(12)
	u.label(u.face(11.5), gfx.Rect{X: x, Y: y, W: r.W - x - u.px(120), H: h},
		u.t("Drag clips between tracks · drag ends to trim · wheel scrolls tracks, Shift+wheel scrolls time, Ctrl+wheel zooms"), colFaint, 0)
	right := r.Right() - u.px(12)
	fit := gfx.Rect{X: right - u.px(28), Y: y, W: u.px(28), H: h}
	if u.iconButton("tl/fit", fit, icFit, u.t("Fit to window"), a.view.pps == 0, true) {
		a.view.pps = 0
	}
	in := gfx.Rect{X: fit.X - u.px(32), Y: y, W: u.px(28), H: h}
	if u.iconButton("tl/zoomin", in, icZoomIn, u.t("Zoom in (Ctrl+wheel)"), false, true) {
		a.zoom(t, 1.6, a.playhead)
	}
	out := gfx.Rect{X: in.X - u.px(32), Y: y, W: u.px(28), H: h}
	if u.iconButton("tl/zoomout", out, icZoomOut, u.t("Zoom out"), false, a.view.pps != 0) {
		a.zoom(t, 1/1.6, a.playhead)
	}
}

// trackName labels a lane.
func (a *App) trackName(l lane) string {
	switch {
	case l.group == groupOverlay:
		return a.u.tf("Overlay %d", l.track+1)
	case l.group == groupMusic:
		return a.u.tf("Music %d", l.track+1)
	case l.track == 0:
		return a.u.t("Main video")
	}
	return a.u.tf("Video %d", l.track+1)
}

// drawLaneHeader shows a track's name and switches; a selected video or music track also gets its volume slider.
func (a *App) drawLaneHeader(l lane) {
	u := &a.u
	r := l.header
	selected := a.sel.kind == selTrack && a.sel.group == l.group && a.sel.track == l.track
	id := fmt.Sprintf("hdr/%d/%d", l.group, l.track)
	bg := colSurface
	if selected {
		bg = gfx.Mix(colSurface, colAccent, 0.2)
	} else if u.hover(id, r) {
		bg = colSurfaceHi
	}
	u.c.RoundRect(r, u.px(6), bg)
	glyph := map[group]string{groupVideo: icVideo, groupOverlay: icFont, groupMusic: icMusic}[l.group]
	top := gfx.Rect{X: r.X, Y: r.Y, W: r.W, H: min(r.H, u.px(26))}
	u.icon(glyph, gfx.Rect{X: r.X + u.px(6), Y: top.Y, W: u.px(16), H: top.H}, 11, colDim)
	u.label(u.face(11.5), gfx.Rect{X: r.X + u.px(26), Y: top.Y, W: r.W - u.px(56), H: top.H}, a.trackName(l), colText, 0)
	sw := gfx.Rect{X: r.Right() - u.px(28), Y: top.Y + (top.H-u.px(22))/2, W: u.px(24), H: u.px(22)}
	switch l.group {
	case groupOverlay:
		tr := a.p.Overlays[l.track]
		glyph := icEye
		if tr.Hidden {
			glyph = icHide
		}
		if u.iconButton(id+"/hide", sw, glyph, u.t("Show / hide this track"), tr.Hidden, true) {
			a.change(func() { a.p.Overlays[l.track].Hidden = !tr.Hidden })
		}
	default:
		music := l.group == groupMusic
		volume, muted := a.trackVolume(music, l.track)
		glyph := icVolume
		if muted {
			glyph = icMute
		}
		if u.iconButton(id+"/mute", sw, glyph, u.t("Mute / unmute this track"), muted, true) {
			a.change(func() { a.p.SetTrackVolume(music, l.track, volume, !muted) })
		}
		if selected && r.H >= u.px(40) {
			sr := gfx.Rect{X: r.X + u.px(4), Y: top.Bottom() - u.px(4), W: r.W - u.px(42), H: u.px(20)}
			v := volume
			changed, done := u.slider(id+"/vol", sr, &v, 0, 2, !muted)
			a.live(changed, done, func() { a.p.SetTrackVolume(music, l.track, v, muted) })
			u.label(u.face(10.5), gfx.Rect{X: sr.Right(), Y: sr.Y, W: u.px(38), H: sr.H}, fmt.Sprintf("%d%%", int(math.Round(volume*100))), colDim, 0)
			u.hint(id+"/vol", u.t("Volume of the whole track"), sr)
		}
	}
	u.hint(id, u.t("Click to select the track and set its volume"), top)
	if u.clicked(id, r) {
		a.sel = selection{kind: selTrack, group: l.group, track: l.track}
	}
}

func (a *App) trackVolume(music bool, track int) (float64, bool) {
	if music {
		return a.p.Music[track].Volume, a.p.Music[track].Muted
	}
	return a.p.Video[track].Volume, a.p.Video[track].Muted
}

// tickStep picks a ruler interval whose labels are at least minPx apart.
func tickStep(pps float64, minPx float64) float64 {
	for _, s := range []float64{0.1, 0.2, 0.5, 1, 2, 5, 10, 15, 30, 60, 120, 300, 600, 1800, 3600} {
		if s*pps >= minPx {
			return s
		}
	}
	return 7200
}

// shortClock formats whole seconds as m:ss (h:mm:ss past an hour).
func shortClock(t float64) string {
	s := int(math.Round(t))
	if s >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", s/3600, s/60%60, s%60)
	}
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}

// rulerLabel formats a tick: m:ss for whole-second steps, seconds with a decimal for finer ones.
func rulerLabel(t, step float64) string {
	if step >= 1 {
		return shortClock(t)
	}
	if t >= 60 {
		return fmt.Sprintf("%d:%04.1f", int(t)/60, math.Mod(t, 60))
	}
	return fmt.Sprintf("%.1fs", t)
}

func (a *App) drawRuler(t tracks) {
	u := &a.u
	step := tickStep(t.pps, float64(u.px(70)))
	minor := step / 5
	f := u.face(10.5)
	first := math.Floor(t.x0/minor) * minor
	for at := first; at <= t.x0+t.seconds()+minor; at += minor {
		x := t.x(at)
		major := math.Abs(math.Remainder(at, step)) < minor/2
		h := u.px(4)
		if major {
			h = u.px(8)
			u.c.Text(f, x+u.px(4), t.ruler.Y+u.px(1), rulerLabel(math.Round(at/minor)*minor, step), colFaint)
		}
		u.c.Fill(gfx.Rect{X: x, Y: t.ruler.Bottom() - h, W: max(1, u.px(1)), H: h}, colBorder)
	}
	if end := t.x(a.p.Duration()); end < t.area.Right() {
		u.c.Fill(gfx.Rect{X: end, Y: t.ruler.Y, W: max(1, u.px(1)), H: t.ruler.H}, gfx.Alpha(colAccent, 0.6))
	}
	if u.press("tl/ruler", t.ruler) {
		a.player.Stop()
	}
	if u.dragging("tl/ruler") || u.letGo("tl/ruler") {
		a.playhead = min(max(t.at(u.mx), 0), a.p.Duration())
		a.requestFrame()
	}
	if u.hover("tl/ruler", t.ruler) {
		u.cursor = CursorHand
	}
}

func (a *App) drawPlayhead(t tracks) {
	u := &a.u
	x := t.x(a.playhead)
	bottom := min(t.view.Bottom(), t.view.Y+t.contentH)
	u.c.Fill(gfx.Rect{X: x - u.px(1), Y: t.ruler.Y + u.px(8), W: u.px(2), H: bottom - t.ruler.Y - u.px(8)}, colPlayhead)
	u.c.Triangle(x-u.px(6), t.ruler.Y+u.px(3), x+u.px(6), t.ruler.Y+u.px(3), x, t.ruler.Y+u.px(12), colPlayhead)
}

// marquee is a selection box being drawn on empty timeline space; it starts from the items already selected when
// Ctrl/⌘ or Shift was held.
type marquee struct {
	x, y float64 // where it started, in seconds and timeline content pixels, so it follows scrolling
	keep []int
}

// trackBackground lets a click on empty track space seek there and clear the selection, and a drag draw a box that
// selects every item it touches.
func (a *App) trackBackground(t tracks) {
	u := &a.u
	if u.press("tl/empty", t.area) {
		a.player.Stop()
		var keep []int
		if u.ctrl || u.shift {
			keep = a.selectedIDs()
		} else {
			a.selectOnly(selection{})
		}
		a.marquee = &marquee{x: t.at(u.pressX), y: float64(u.pressY - t.view.Y + a.view.vscroll), keep: keep}
	}
	m := a.marquee
	if m == nil {
		return
	}
	if u.dragging("tl/empty") && u.drag.moved {
		box := a.marqueeRect(t, m)
		ids := append(slices.Clone(m.keep), a.itemsIn(t, box)...)
		a.selectOnly(selection{})
		for _, id := range ids {
			a.addPick(id)
		}
		u.c.Fill(box, gfx.Alpha(colAccent, 0.12))
		u.c.RoundOutline(box, u.px(2), max(1, u.px(1)), colAccentHi)
	}
	if u.letGo("tl/empty") {
		if !u.drag.moved {
			a.playhead = min(max(t.at(u.mx), 0), a.p.Duration())
			a.requestFrame()
		}
		a.marquee = nil
	} else if u.active != "tl/empty" {
		a.marquee = nil
	}
}

// marqueeRect is the box from where the marquee started to the pointer, on screen.
func (a *App) marqueeRect(t tracks, m *marquee) gfx.Rect {
	x0, y0 := t.x(m.x), t.view.Y+float32(m.y)-a.view.vscroll
	x1, y1 := a.u.mx, a.u.my
	return gfx.Rect{X: min(x0, x1), Y: min(y0, y1), W: max(x0, x1) - min(x0, x1), H: max(y0, y1) - min(y0, y1)}
}

func (a *App) drawScrollbars(t tracks) {
	u := &a.u
	total := math.Max(a.scrollEnd(t), t.seconds())
	if t.seconds() < total-1e-6 {
		u.c.RoundRect(t.bar, t.bar.H/2, colSurface)
		w := max(t.bar.W*float32(t.seconds()/total), u.px(30))
		x := t.bar.X + (t.bar.W-w)*float32(t.x0/(total-t.seconds()))
		thumb := gfx.Rect{X: x, Y: t.bar.Y, W: w, H: t.bar.H}
		col := colSurfaceHi
		if u.hover("tl/bar", thumb) || u.active == "tl/bar" {
			col = colFaint
		}
		u.c.RoundRect(thumb, t.bar.H/2, col)
		if u.press("tl/bar", thumb.Inset(-u.px(4))) {
			u.drag.values[0] = a.view.scroll
			if a.view.pps == 0 {
				a.view.pps = t.pps / float64(u.s)
			}
		}
		if u.dragging("tl/bar") {
			a.view.scroll = u.drag.values[0] + float64(u.mx-u.drag.x)/float64(t.bar.W-w)*(total-t.seconds())
		}
		u.letGo("tl/bar")
	}
	if t.contentH > t.view.H {
		bar := gfx.Rect{X: t.view.Right() - u.px(8), Y: t.view.Y, W: u.px(5), H: t.view.H}
		h := max(bar.H*t.view.H/t.contentH, u.px(24))
		y := bar.Y + (bar.H-h)*a.view.vscroll/(t.contentH-t.view.H)
		thumb := gfx.Rect{X: bar.X, Y: y, W: bar.W, H: h}
		col := colSurfaceHi
		if u.hover("tl/vbar", thumb.Inset(-u.px(3))) || u.active == "tl/vbar" {
			col = colFaint
		}
		u.c.RoundRect(thumb, bar.W/2, col)
		if u.press("tl/vbar", thumb.Inset(-u.px(4))) {
			u.drag.values[0] = float64(a.view.vscroll)
		}
		if u.dragging("tl/vbar") {
			a.view.vscroll = float32(u.drag.values[0]) + (u.my-u.drag.y)/(bar.H-h)*(t.contentH-t.view.H)
		}
		u.letGo("tl/vbar")
	}
}
