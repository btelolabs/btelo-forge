package ui

import (
	"context"
	"fmt"
	"image"
	"math"

	"github.com/btelolabs/btelo-video-cut/internal/caption"
	"github.com/btelolabs/btelo-video-cut/internal/gfx"
	"github.com/btelolabs/btelo-video-cut/internal/media"
	"github.com/btelolabs/btelo-video-cut/internal/project"
)

// shownFrame is the picture in the preview. While paused it is the top clip's source frame, fetched one at a time
// with the newest request winning; while playing it is the player's canvas-sized frame.
type shownFrame struct {
	img    *image.RGBA
	key    frameKey
	full   bool // img covers the whole canvas (a player frame) rather than one source fitted into it
	busy   bool
	stale  bool
	failed frameKey
	canvas image.Point
	scaler gfx.Scaler
}

// frameKey names a source picture: which file, at what time and size.
type frameKey struct {
	path string
	at   float64
	size image.Point
}

// maxPreviewWidth caps the decoded preview; ffmpeg scales to the displayed size so frames are copied, not resampled.
const maxPreviewWidth = 2560

func (a *App) requestFrame() {
	if a.doc != nil {
		a.shown.stale = true
	}
}

// fitRect places a w×h picture inside r keeping its shape.
func fitRect(r gfx.Rect, w, h float32) gfx.Rect {
	scale := min(r.W/w, r.H/h)
	fw, fh := w*scale, h*scale
	return gfx.Rect{X: r.X + (r.W-fw)/2, Y: r.Y + (r.H-fh)/2, W: fw, H: fh}
}

func (a *App) canvasRect(r gfx.Rect) gfx.Rect {
	return fitRect(r, float32(a.p.Canvas.Width), float32(a.p.Canvas.Height))
}

// evenSize is the even-sized picture the decoders produce for a rectangle.
func evenSize(r gfx.Rect) image.Point {
	w := min(math.Round(float64(r.W)), maxPreviewWidth)
	h := w * float64(r.H) / float64(r.W)
	return image.Pt(max(2, int(w/2)*2), max(2, int(h/2)*2))
}

// wantedFrame is the source picture the paused preview should show, or false over black.
func (a *App) wantedFrame(v gfx.Rect) (frameKey, bool) {
	c, at, ok := a.p.Top(a.playhead)
	if !ok {
		return frameKey{}, false
	}
	src := a.p.SourceOf(c)
	fit := fitRect(v, float32(src.Width), float32(src.Height))
	half := 0.5 / max(src.FPS, 1)
	at = math.Min(at, src.Duration-1/max(src.FPS, 1))
	return frameKey{path: src.Path, at: math.Round(math.Max(at, 0)/half) * half, size: evenSize(fit)}, true
}

func (a *App) fetchFrame(v gfx.Rect) {
	if a.player.Playing() || a.shown.busy {
		return
	}
	want, ok := a.wantedFrame(v)
	if !ok {
		if a.shown.img != nil {
			a.shown.img, a.shown.key = nil, frameKey{}
		}
		return
	}
	if (want == a.shown.key && !a.shown.full || want == a.shown.failed) && !a.shown.stale {
		return
	}
	a.shown.busy, a.shown.stale = true, false
	d := a.doc
	a.background(func(ctx context.Context) {
		img, err := media.Frame(ctx, want.path, want.at, want.size.X, want.size.Y)
		a.post(func() {
			d.shown.busy = false
			if err != nil {
				d.shown.failed = want
				return
			}
			d.shown.img, d.shown.key, d.shown.full, d.shown.failed = img, want, false, frameKey{}
		})
	})
}

func (a *App) play(from float64) {
	a.audition.Stop()
	a.player.Start(a.p, from, a.shown.canvas.X, a.shown.canvas.Y)
}

func (a *App) togglePlay() {
	if a.player.Playing() {
		a.player.Stop()
		a.requestFrame()
		return
	}
	if a.playhead >= a.p.Duration()-0.05 {
		a.playhead = 0
	}
	a.play(a.playhead)
}

func (a *App) syncPlayback() {
	if err := a.player.Err(); err != nil {
		a.toast(a.u.tf("Playback failed: %s", a.describe(err)), true)
	}
	if err := a.audition.Err(); err != nil {
		a.toast(a.describe(err), true)
	}
	if a.p == nil {
		return
	}
	wasPlaying := a.player.Playing()
	if t, ok := a.player.Position(); ok {
		a.playhead = t
	} else if wasPlaying {
		a.playhead = t
		a.requestFrame()
	}
	if f, ok := a.player.Frame(); ok && a.player.Playing() {
		a.shown.img, a.shown.full = f.Image, true
	}
}

func (a *App) drawPreview(r gfx.Rect) {
	u := &a.u
	u.c.Fill(r, gfx.Hex(0x0a0b0c))
	v := a.canvasRect(r.Inset(u.px(14)))
	a.shown.canvas = evenSize(v)
	a.fetchFrame(v)
	u.c.Fill(v, colBlack)
	if img := a.shown.img; img != nil {
		target := v
		if !a.shown.full {
			if c, _, ok := a.p.Top(a.playhead); ok {
				src := a.p.SourceOf(c)
				target = fitRect(v, float32(src.Width), float32(src.Height))
			}
		}
		pr := target.Pixels()
		if dw, dh := img.Rect.Dx()-pr.Dx(), img.Rect.Dy()-pr.Dy(); dw < -2 || dw > 2 || dh < -2 || dh > 2 {
			img = a.shown.scaler.Scaled(img, pr.Dx(), pr.Dy())
		}
		u.c.Blit(img, pr.Min.X+(pr.Dx()-img.Rect.Dx())/2, pr.Min.Y+(pr.Dy()-img.Rect.Dy())/2, true)
	} else if a.shown.busy {
		u.label(u.face(13), v, u.t("Loading the picture…"), colFaint, 0.5)
	}
	u.c.Push(v)
	defer u.c.Pop()
	a.drawOverlays(v)
}

// overlayImage draws an overlay for a w×h frame, with ok false when there is nothing to show.
func (a *App) overlayImage(o project.Overlay, w, h int) (caption.Image, bool) {
	if o.Kind == project.ImageOverlay {
		src, err := a.media.image(o.Image)
		if err != nil {
			return caption.Image{}, false
		}
		return a.captions.Picture(o, src, w, h), true
	}
	img := a.captions.Draw(o, w, h)
	return img, img.Bounds().Dx() > 0
}

// drawOverlays shows the text and images visible at the playhead and lets them be selected and dragged into place.
func (a *App) drawOverlays(v gfx.Rect) {
	u := &a.u
	vw, vh := int(math.Round(float64(v.W))), int(math.Round(float64(v.H)))
	var rects []gfx.Rect
	var items []project.Overlay
	for _, o := range a.p.Visible(a.playhead) {
		img, ok := a.overlayImage(o, vw, vh)
		if !ok {
			continue
		}
		rect := gfx.Rect{X: v.X + float32(img.At.X), Y: v.Y + float32(img.At.Y), W: float32(img.Bounds().Dx()), H: float32(img.Bounds().Dy())}
		u.c.Image(img.RGBA, rect)
		rects = append(rects, rect)
		items = append(items, o)
	}
	hit := -1
	for i := len(rects) - 1; i >= 0; i-- {
		if rects[i].Contains(u.mx, u.my) {
			hit = i
			break
		}
	}
	for i, rect := range rects {
		box := rect.Inset(-u.px(4))
		if a.sel.kind == selOverlay && a.sel.id == items[i].ID {
			u.c.RoundOutline(box, u.px(4), u.px(2), colAccent)
		} else if i == hit && u.active == "" {
			u.c.RoundOutline(box, u.px(4), u.px(1), gfx.Alpha(colText, 0.6))
		}
	}
	if hit >= 0 && u.active == "" && !u.blocked {
		u.cursor = CursorMove
		hint := u.t("Drag to move, double-click to edit")
		if items[hit].Kind == project.ImageOverlay {
			hint = u.t("Drag to move; set its size in the Overlays panel")
		}
		u.hint("overlay", hint, rects[hit])
	}
	key := "overlay/drag"
	if hit >= 0 && u.press(key, rects[hit]) {
		o := items[hit]
		a.sel, a.tab = selection{kind: selOverlay, id: o.ID}, tabOverlays
		u.drag.id = o.ID
		u.drag.values[0], u.drag.values[1] = o.X, o.Y
		a.dragBefore = a.p.Clone()
		if u.double && o.Kind == project.TextOverlay {
			a.focusBody(o.Body)
		}
	}
	if u.dragging(key) && u.drag.moved {
		u.cursor = CursorMove
		x := u.drag.values[0] + float64((u.mx-u.drag.x)/v.W)
		y := u.drag.values[1] + float64((u.my-u.drag.y)/v.H)
		snap := float64(u.px(6) / v.W)
		if math.Abs(x-0.5) < snap {
			x = 0.5
			u.c.Fill(gfx.Rect{X: v.X + v.W/2, Y: v.Y, W: max(1, u.px(1)), H: v.H}, gfx.Alpha(colPlayhead, 0.8))
		}
		a.p.UpdateOverlay(u.drag.id, func(o *project.Overlay) { o.X, o.Y = x, y })
	}
	if u.letGo(key) {
		a.committed(a.dragBefore)
		a.dragBefore = nil
	}
	if hit < 0 && u.press("preview/empty", v) && a.sel.kind == selOverlay {
		a.sel = selection{}
	}
	u.letGo("preview/empty")
}

// live applies one step of a continuous edit such as a slider drag; the whole drag becomes a single undo step.
func (a *App) live(changed, done bool, apply func()) {
	if changed {
		if a.dragBefore == nil {
			a.dragBefore = a.p.Clone()
		}
		apply()
		if !done {
			a.requestFrame()
		}
	}
	if done {
		a.committed(a.dragBefore)
		a.dragBefore = nil
	}
}

// clock formats seconds as m:ss.cc, with hours when needed.
func clock(t float64) string {
	t = math.Max(t, 0)
	cs := int(math.Round(t * 100))
	h, m, s, c := cs/360000, cs/6000%60, cs/100%60, cs%100
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d.%02d", h, m, s, c)
	}
	return fmt.Sprintf("%d:%02d.%02d", m, s, c)
}

var speedLabels = []string{"0.5×", "0.75×", "1×", "1.25×", "1.5×", "2×", "3×", "4×"}
var speedValues = []float64{0.5, 0.75, 1, 1.25, 1.5, 2, 3, 4}

func (a *App) drawTransport(r gfx.Rect) {
	u := &a.u
	u.c.Fill(r, colPanel)
	y := r.Y + (r.H-u.px(32))/2
	x := r.X + u.px(12)
	glyph, hint := icPlay, u.t("Play (Space)")
	if a.player.Playing() {
		glyph, hint = icPause, u.t("Pause (Space)")
	}
	pr := gfx.Rect{X: x, Y: y, W: u.px(38), H: u.px(32)}
	hot := u.hover("play", pr)
	bg := colAccent
	if hot {
		bg = colAccentHi
		u.cursor = CursorHand
	}
	u.c.RoundRect(pr, u.px(16), bg)
	u.icon(glyph, pr, 14, colOnAccent)
	u.hint("play", hint, pr)
	if u.clicked("play", pr) {
		a.togglePlay()
	}
	x += pr.W + u.px(12)
	f := u.bold(13.5)
	u.c.Text(f, x, r.Y+(r.H-f.Height())/2, clock(a.playhead), colText)
	x += f.Measure("00:00.00") + u.px(6)
	fd := u.face(12)
	u.c.Text(fd, x, r.Y+(r.H-fd.Height())/2+u.px(1), "/ "+clock(a.p.Duration()), colDim)
	x += fd.Measure("/ 00:00.00") + u.px(14)

	for _, b := range []struct {
		id, glyph, hint string
		enabled         bool
		run             func()
	}{
		{"undo", icUndo, u.t("Undo (Ctrl+Z)"), a.hist.CanUndo(), a.undo},
		{"redo", icRedo, u.t("Redo (Ctrl+Y)"), a.hist.CanRedo(), a.redo},
		{"split", icCut, u.t("Split at the playhead (S)"), true, a.split},
		{"delete", icDelete, u.t("Delete the selection (Delete)"), a.sel.kind != selNone && a.sel.kind != selTrack, a.deleteSelection},
	} {
		br := gfx.Rect{X: x, Y: y, W: u.px(32), H: u.px(32)}
		if u.iconButton("tr/"+b.id, br, b.glyph, b.hint, false, b.enabled) {
			b.run()
		}
		x += u.px(36)
	}
	x += u.px(10)

	c := a.targetClip()
	right := r.Right() - u.px(12)
	volW := u.px(200)
	chipsW := min(right-volW-x-u.px(60), u.px(8*44))
	if chipsW > u.px(180) {
		u.label(u.face(12), gfx.Rect{X: x, Y: r.Y, W: u.px(44), H: r.H}, u.t("Speed"), colDim, 0)
		x += u.px(44)
		current := -1
		for i, s := range speedValues {
			if math.Abs(s-c.Speed) < 1e-6 {
				current = i
			}
		}
		if pick := u.chips("speed", gfx.Rect{X: x, Y: y + u.px(3), W: chipsW, H: u.px(26)}, speedLabels, current, true); pick != current {
			a.change(func() {
				if !a.p.SetSpeed(c.ID, speedValues[pick]) {
					a.toast(a.u.t("No room on this track for the slower clip"), false)
				}
			})
		}
		u.hint("speed", u.t("Speed of the selected clip"), gfx.Rect{X: x, Y: y, W: chipsW, H: u.px(32)})
	}
	vx := right - volW
	has := a.p.SourceOf(c).HasAudio
	u.icon(icVolume, gfx.Rect{X: vx, Y: y, W: u.px(24), H: u.px(32)}, 13, colDim)
	sr := gfx.Rect{X: vx + u.px(26), Y: y, W: volW - u.px(70), H: u.px(32)}
	vol := c.Volume
	changed, done := u.slider("clipvol", sr, &vol, 0, 2, has)
	a.live(changed, done, func() { a.p.SetClipVolume(c.ID, vol) })
	tip := u.t("Volume of the selected clip's own sound")
	if !has {
		tip = u.t("This video has no sound")
	}
	u.hint("clipvol", tip, sr)
	u.label(u.face(11.5), gfx.Rect{X: sr.Right() + u.px(4), Y: r.Y, W: u.px(40), H: r.H}, fmt.Sprintf("%d%%", int(math.Round(c.Volume*100))), colDim, 0)
}
