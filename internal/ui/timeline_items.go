package ui

import (
	"fmt"
	"image"
	"image/color"
	"math"

	"github.com/btelolabs/btelo-video-cut/internal/gfx"
	"github.com/btelolabs/btelo-video-cut/internal/project"
)

// snap moves at to the nearest edit point within a few pixels: the playhead, the ends of the edit, or the ends of any
// clip or item other than skip.
func (a *App) snap(t tracks, at float64, skip int) float64 {
	points := []float64{0, a.playhead, a.p.Duration()}
	for _, tr := range a.p.Video {
		for _, c := range tr.Clips {
			if c.ID != skip {
				points = append(points, c.Start, c.End())
			}
		}
	}
	for _, tr := range a.p.Music {
		for _, m := range tr.Clips {
			if m.ID != skip {
				points = append(points, m.Start, m.End())
			}
		}
	}
	for _, tr := range a.p.Overlays {
		for _, o := range tr.Items {
			if o.ID != skip {
				points = append(points, o.Start, o.End)
			}
		}
	}
	best, dist := at, float64(a.u.px(8))/t.pps
	for _, p := range points {
		if d := math.Abs(p - at); d < dist {
			best, dist = p, d
		}
	}
	return best
}

func (a *App) edgeRects(r gfx.Rect) (gfx.Rect, gfx.Rect) {
	w := min(a.u.px(7), r.W/3)
	return gfx.Rect{X: r.X, Y: r.Y, W: w, H: r.H}, gfx.Rect{X: r.Right() - w, Y: r.Y, W: w, H: r.H}
}

func (a *App) clipRect(t tracks, l lane, start, end float64) gfx.Rect {
	x0, x1 := t.x(start), t.x(end)
	return gfx.Rect{X: x0, Y: l.rect.Y + a.u.px(2), W: max(x1-x0, a.u.px(4)), H: l.rect.H - a.u.px(4)}
}

func (a *App) outline(r gfx.Rect, selected, hot bool) {
	u := &a.u
	switch {
	case selected:
		u.c.RoundOutline(r, u.px(6), u.px(2), colWhite)
	case hot:
		u.c.RoundOutline(r, u.px(6), u.px(1), gfx.Alpha(colText, 0.5))
	}
}

func (a *App) beginDrag(id int, values ...float64) {
	a.u.drag.id = id
	copy(a.u.drag.values[:], values)
	a.dragBefore = a.p.Clone()
	a.player.Stop()
}

func (a *App) endDrag() {
	a.committed(a.dragBefore)
	a.dragBefore = nil
}

func waveColumn(c *gfx.Canvas, x, mid, half, peak float32, col color.RGBA) {
	h := max(peak*half, 0.5)
	c.Fill(gfx.Rect{X: x, Y: mid - h, W: 1, H: 2 * h}, col)
}

// itemDrag runs the move and trim gestures shared by every kind of clip. Edge 0 is the body, 1 the left end and
// 2 the right end; apply gets the time moved and, for the body, the height of the pointer.
func (a *App) itemDrag(t tracks, r gfx.Rect, prefix string, id int, sel selection, tab int, begin func(), apply func(dt float64, edge int, y float32)) {
	u := &a.u
	bid, lid, rid := fmt.Sprintf("%s/%d", prefix, id), fmt.Sprintf("%sL/%d", prefix, id), fmt.Sprintf("%sR/%d", prefix, id)
	left, right := a.edgeRects(r)
	if u.hover(lid, left) || u.hover(rid, right) || u.active == lid || u.active == rid {
		u.cursor = CursorResize
	} else if u.hover(bid, r) || u.active == bid {
		u.cursor = CursorMove
	}
	switch {
	case (u.ctrl || u.shift) && u.press(bid, r):
		// Ctrl/⌘-click toggles the item in the selection, Shift-click adds it; neither moves anything.
		if u.ctrl {
			a.togglePick(id)
		} else {
			a.addPick(id)
		}
		u.active = ""
		return
	case u.press(lid, left), u.press(rid, right):
		a.selectOnly(sel)
		begin()
	case u.press(bid, r):
		if !a.isPicked(id) {
			a.selectOnly(sel)
		}
		a.sel, a.tab = sel, tab
		begin()
	}
	for edge, key := range []string{bid, lid, rid} {
		if u.dragging(key) && u.drag.moved {
			if len(a.picked) > 0 {
				a.selectOnly(sel)
			}
			apply(float64(u.mx-u.drag.x)/t.pps, edge, u.my)
		}
		if u.letGo(key) {
			if !u.drag.moved && edge == 0 {
				a.selectOnly(sel)
				a.playhead = min(max(t.at(u.mx), 0), a.p.Duration())
				a.requestFrame()
			}
			a.endDrag()
		}
	}
}

func (a *App) drawVideoClips(t tracks) {
	u := &a.u
	for k := range a.p.Video {
		l, ok := t.lane(groupVideo, k)
		if !ok {
			continue
		}
		for _, c := range a.p.Video[k].Clips {
			r := a.clipRect(t, l, c.Start, c.End())
			if r.Right() >= t.area.X && r.X <= t.area.Right() && r.Bottom() >= t.view.Y && r.Y <= t.view.Bottom() {
				a.drawClip(t, k, c, r)
			}
			id := c.ID
			a.itemDrag(t, r, "clip", id, selection{kind: selClip, id: id}, a.tab,
				func() { a.beginDrag(id, c.In, c.Out, c.Start, t.at(u.mx)-c.Start) },
				func(dt float64, edge int, y float32) {
					cur, ok := a.p.Clip(id)
					if !ok {
						return
					}
					switch edge {
					case 0:
						track := t.target(groupVideo, y, len(a.p.Video))
						start := a.snap(t, u.drag.values[2]+dt, id)
						if track == 0 {
							start = t.at(u.mx)
						}
						a.p.MoveClip(id, track, start)
					case 1:
						a.p.SetIn(id, u.drag.values[0]+dt*cur.Speed)
					case 2:
						a.p.SetOut(id, u.drag.values[1]+dt*cur.Speed)
					}
					a.requestFrame()
				})
			u.hint(fmt.Sprintf("clip/%d", id), u.t("Drag to move or onto another track; drag the ends to trim; S splits at the playhead"), r)
		}
	}
}

func (a *App) drawClip(t tracks, track int, c project.Clip, r gfx.Rect) {
	u := &a.u
	selected := a.isPicked(c.ID)
	bg := colVideo
	if track > 0 {
		bg = colVideoUpper
	}
	u.c.RoundRect(r, u.px(6), bg)
	u.c.Push(r.Inset(u.px(1)))
	src := a.p.SourceOf(c)
	sm := a.media.sources[src.Path]
	waveH := min(u.px(20), r.H*0.32)
	strip := gfx.Rect{X: r.X, Y: r.Y, W: r.W, H: r.H - waveH}
	if sm != nil && sm.thumbStep > 0 && len(sm.thumbs) > 0 {
		th := int(math.Round(float64(strip.H)))
		tw := max(1, int(math.Round(float64(th)*float64(sm.thumbW)/float64(sm.thumbH))))
		left, right := int(math.Floor(float64(r.X))), int(math.Ceil(float64(min(r.Right(), t.area.Right()))))
		first := left + max(0, (int(t.area.X)-tw-left)/tw)*tw
		for x := first; x < right; x += tw {
			mid := math.Min(c.Start+(float64(x-left)+float64(tw)/2)/t.pps, c.End()-1e-3)
			at := c.In + (mid-c.Start)*c.Speed
			k := min(max(int(at/sm.thumbStep), 0), len(sm.thumbs)-1)
			if img := nearestThumb(sm, k); img != nil {
				u.c.Blit(a.tile(img, tw, th), x, int(math.Round(float64(strip.Y))), true)
			}
		}
	}
	wave := gfx.Rect{X: r.X, Y: r.Bottom() - waveH, W: r.W, H: waveH}
	u.c.Fill(wave, gfx.Hex(0x18211f))
	tr := a.p.Video[track]
	if sm != nil && len(sm.peaks) > 0 {
		gain := float32(c.Volume * tr.Volume)
		col := gfx.Hex(0x6fd3a3)
		if tr.Muted {
			col, gain = colFaint, 0.25
		}
		x0, x1 := max(int(wave.X), int(t.area.X)), min(int(wave.Right()), int(t.area.Right()))
		for x := x0; x < x1; x++ {
			a0 := c.Start + float64(float32(x)-r.X)/t.pps
			s0 := (c.In + (a0-c.Start)*c.Speed) * peaksPerSecond
			s1 := s0 + c.Speed*peaksPerSecond/t.pps
			peak := float32(0)
			for k := int(s0); k <= int(s1) && k < len(sm.peaks); k++ {
				if k >= 0 {
					peak = max(peak, sm.peaks[k])
				}
			}
			waveColumn(u.c, float32(x), wave.Y+wave.H/2, wave.H/2-u.px(2), min(peak*gain, 1), col)
		}
	} else if !src.HasAudio {
		u.label(u.face(10), wave.Inset(u.px(4)), u.t("No sound"), colFaint, 0)
	}
	if math.Abs(c.Speed-1) > 1e-6 {
		label := fmt.Sprintf("%g×", c.Speed)
		f := u.bold(10.5)
		bw := f.Measure(label) + u.px(26)
		badge := gfx.Rect{X: max(r.X, t.area.X) + u.px(5), Y: r.Y + u.px(4), W: bw, H: u.px(17)}
		u.c.RoundRect(badge, u.px(8), gfx.Alpha(colBlack, 0.7))
		u.icon(icSpeed, gfx.Rect{X: badge.X + u.px(4), Y: badge.Y, W: u.px(14), H: badge.H}, 9, colPlayhead)
		u.label(f, gfx.Rect{X: badge.X + u.px(18), Y: badge.Y, W: bw - u.px(20), H: badge.H}, label, colPlayhead, 0)
	}
	u.c.Pop()
	a.outline(r, selected, u.hover(fmt.Sprintf("clip/%d", c.ID), r))
	handle := colFaint
	if selected {
		handle = colWhite
	}
	left, right := a.edgeRects(r)
	for _, e := range []gfx.Rect{left, right} {
		u.c.RoundRect(gfx.Rect{X: e.X + e.W/2 - u.px(1.5), Y: e.Y + e.H/2 - u.px(8), W: u.px(3), H: u.px(16)}, u.px(1.5), handle)
	}
}

// tile is a thumbnail resized to the strip once and kept until the strip size changes.
func (a *App) tile(img *image.RGBA, w, h int) *image.RGBA {
	sc, ok := a.media.tiles[img]
	if !ok {
		sc = &gfx.Scaler{}
		a.media.tiles[img] = sc
	}
	return sc.Scaled(img, w, h)
}

// nearestThumb falls back to the closest decoded thumbnail while the strip is still filling in.
func nearestThumb(sm *sourceMedia, k int) *image.RGBA {
	for d := 0; d < len(sm.thumbs); d++ {
		if k-d >= 0 && sm.thumbs[k-d] != nil {
			return sm.thumbs[k-d]
		}
		if k+d < len(sm.thumbs) && sm.thumbs[k+d] != nil {
			return sm.thumbs[k+d]
		}
	}
	return nil
}

func (a *App) drawMusicClips(t tracks) {
	u := &a.u
	empty := true
	for k := range a.p.Music {
		l, ok := t.lane(groupMusic, k)
		if !ok {
			continue
		}
		for _, m := range a.p.Music[k].Clips {
			empty = false
			r := a.clipRect(t, l, m.Start, m.End())
			id := m.ID
			selected := a.isPicked(id)
			hot := u.hover(fmt.Sprintf("music/%d", id), r)
			bg := colMusic
			if hot || selected {
				bg = colMusicHi
			}
			u.c.RoundRect(r, u.px(6), bg)
			u.c.Push(r.Inset(u.px(1)))
			if peaks := a.musicPeaks(m.Path); len(peaks) > 0 {
				mid, half := r.Y+r.H*0.62, r.H*0.3
				gain := float32(m.Volume * a.p.Music[k].Volume)
				x0, x1 := max(int(r.X), int(t.area.X)), min(int(r.Right()), int(t.area.Right()))
				for x := x0; x < x1; x++ {
					f0 := (m.Offset + float64(float32(x)-r.X)/t.pps) * peaksPerSecond
					f1 := f0 + peaksPerSecond/t.pps
					peak := float32(0)
					for i := int(f0); i <= int(f1) && i < len(peaks); i++ {
						if i >= 0 {
							peak = max(peak, peaks[i])
						}
					}
					waveColumn(u.c, float32(x), mid, half, min(peak*gain, 1), gfx.Alpha(gfx.Hex(0xbff0da), 0.55))
				}
			}
			title := m.Title
			if math.Abs(m.Volume-1) > 0.005 {
				title += fmt.Sprintf("  · %d%%", int(math.Round(m.Volume*100)))
			}
			u.label(u.face(11), gfx.Rect{X: max(r.X, t.area.X) + u.px(8), Y: r.Y + u.px(2), W: r.W - u.px(12), H: u.px(17)}, title, colText, 0)
			u.c.Pop()
			a.outline(r, selected, hot)
			a.itemDrag(t, r, "music", id, selection{kind: selMusic, id: id}, tabMusic,
				func() { a.beginDrag(id, m.Start, m.End()) },
				func(dt float64, edge int, y float32) {
					switch edge {
					case 0:
						start := a.snap(t, u.drag.values[0]+dt, id)
						if end := a.snap(t, u.drag.values[1]+dt, id); end != u.drag.values[1]+dt {
							start = end - (u.drag.values[1] - u.drag.values[0])
						}
						a.p.MoveMusic(id, t.target(groupMusic, y, len(a.p.Music)), start)
					case 1:
						a.p.TrimMusicStart(id, a.snap(t, u.drag.values[0]+dt, id))
					case 2:
						a.p.TrimMusicEnd(id, a.snap(t, u.drag.values[1]+dt, id))
					}
				})
			u.hint(fmt.Sprintf("music/%d", id), u.t("Drag to move or onto another track, drag the ends to trim"), r)
		}
	}
	if empty {
		if l, ok := t.lane(groupMusic, 0); ok {
			u.label(u.face(11.5), l.rect.Inset(u.px(10)), u.t("Add music with + in the Music panel on the right, or drop sound files on the window"), colFaint, 0)
		}
	}
}

func (a *App) drawOverlayItems(t tracks) {
	u := &a.u
	empty := true
	for k := range a.p.Overlays {
		l, ok := t.lane(groupOverlay, k)
		if !ok {
			continue
		}
		for _, o := range a.p.Overlays[k].Items {
			empty = false
			r := a.clipRect(t, l, o.Start, o.End)
			id := o.ID
			selected := a.isPicked(id)
			hot := u.hover(fmt.Sprintf("ov/%d", id), r)
			bg, hi := colCaption, colCaptionHi
			if o.Kind == project.ImageOverlay {
				bg, hi = colImage, colImageHi
			}
			glyph, label := overlayLook(o)
			if hot || selected {
				bg = hi
			}
			if a.p.Overlays[k].Hidden {
				bg = gfx.Mix(bg, colPanel, 0.6)
			}
			u.c.RoundRect(r, u.px(6), bg)
			u.c.Push(r.Inset(u.px(1)))
			x := max(r.X, t.area.X)
			u.icon(glyph, gfx.Rect{X: x + u.px(4), Y: r.Y, W: u.px(16), H: r.H}, 10, colText)
			u.label(u.face(11), gfx.Rect{X: x + u.px(22), Y: r.Y, W: r.W - u.px(26), H: r.H}, label, colText, 0)
			u.c.Pop()
			a.outline(r, selected, hot)
			if u.double && u.pressed && r.Contains(u.pressX, u.pressY) && o.Kind == project.TextOverlay {
				a.selectOnly(selection{kind: selOverlay, id: id})
				a.focusBody(o.Body)
			}
			a.itemDrag(t, r, "ov", id, selection{kind: selOverlay, id: id}, tabOverlays,
				func() { a.beginDrag(id, o.Start, o.End) },
				func(dt float64, edge int, y float32) {
					s0, e0 := u.drag.values[0], u.drag.values[1]
					switch edge {
					case 0:
						start := a.snap(t, s0+dt, id)
						if end := a.snap(t, e0+dt, id); end != e0+dt {
							start = end - (e0 - s0)
						}
						a.p.MoveOverlay(id, t.target(groupOverlay, y, len(a.p.Overlays)), start)
					case 1:
						if cur, ok := a.p.Overlay(id); ok {
							a.p.SetOverlayRange(id, math.Min(a.snap(t, s0+dt, id), cur.End-project.MinLength), cur.End)
						}
					case 2:
						if cur, ok := a.p.Overlay(id); ok {
							a.p.SetOverlayRange(id, cur.Start, a.snap(t, e0+dt, id))
						}
					}
				})
			u.hint(fmt.Sprintf("ov/%d", id), u.t("Drag to move or onto another track, drag the ends to change how long it shows, double-click to edit"), r)
		}
	}
	if empty {
		if l, ok := t.lane(groupOverlay, 0); ok {
			u.label(u.face(11.5), l.rect.Inset(u.px(10)), u.t("Text and images go here: press T, paste an image with Ctrl+V or drop one on the window"), colFaint, 0)
		}
	}
}

func firstLine(s string) string {
	for i, r := range s {
		if r == '\n' {
			return s[:i] + " …"
		}
	}
	return s
}
