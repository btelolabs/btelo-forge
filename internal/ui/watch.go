package ui

import (
	"github.com/btelolabs/btelo-forge/internal/gfx"
)

// Player mode hides every tool of the video editor, the tabs, panels and timeline, and leaves the picture with a
// bar along the bottom to play, pause and seek: the edit watched as a video player shows it. Its key (Tab unless
// changed in Settings) or Esc turns it off again.

// watchState is player mode's own state.
type watchState struct {
	on     bool
	resume bool // playback was running when the seek bar was pressed, and goes on when it is let go
}

// watching reports whether player mode shows now: it applies to a video edit only.
func (a *App) watching() bool { return a.watch.on && a.p != nil && a.art == nil }

// setWatching turns player mode on or off.
func (a *App) setWatching(on bool) {
	if on && (a.p == nil || a.art != nil) {
		return
	}
	a.watch.on = on
	a.watch.resume = false
	a.menu = nil
	a.u.active, a.u.focus = "", ""
	a.requestFrame()
}

// watchCommands are the commands that still run in player mode: playing and moving through the video. Editing
// keys do nothing while the tools are hidden, so a stray key cannot change the edit unseen.
var watchCommands = map[string]bool{
	"video.play": true, "video.frame.back": true, "video.frame.forward": true, "video.second.back": true,
	"video.second.forward": true, "video.start": true, "video.end": true, "video.watch": true,
}

// drawWatching draws player mode over the whole window.
func (a *App) drawWatching(r gfx.Rect) {
	u := &a.u
	u.c.Fill(r, colBlack)
	barH := u.px(58)
	bar := gfx.Rect{X: r.X, Y: r.Bottom() - barH, W: r.W, H: barH}
	view := gfx.Rect{X: r.X, Y: r.Y, W: r.W, H: bar.Y - r.Y}
	a.drawPicture(view, 0, false)
	// The picture plays and pauses with a click, as players do; a double click leaves player mode, undoing what
	// its first click did.
	if u.press("watch/picture", view) {
		a.togglePlay()
		if u.double {
			a.togglePlay()
			a.setWatching(false)
			return
		}
	}
	u.letGo("watch/picture")
	a.drawWatchBar(bar)
}

// drawWatchBar draws the seek bar and the controls under it.
func (a *App) drawWatchBar(r gfx.Rect) {
	u := &a.u
	u.c.Fill(r, colPanel)
	length := a.p.Duration()

	// The seek bar runs the whole width along the top, thickening under the pointer.
	seek := gfx.Rect{X: r.X + u.px(14), Y: r.Y + u.px(4), W: r.W - u.px(28), H: u.px(16)}
	hot := u.hover("watch/seek", seek) || u.active == "watch/seek"
	thick := u.px(4)
	if hot {
		thick = u.px(6)
		u.cursor = CursorHand
	}
	track := gfx.Rect{X: seek.X, Y: seek.Y + (seek.H-thick)/2, W: seek.W, H: thick}
	u.c.Fill(track, colSurfaceHi)
	done := float32(0)
	if length > 0 {
		done = float32(min(max(a.playhead/length, 0), 1))
	}
	u.c.Fill(gfx.Rect{X: track.X, Y: track.Y, W: track.W * done, H: track.H}, colAccent)
	at := func(x float32) float64 { return float64(min(max((x-seek.X)/seek.W, 0), 1)) * length }
	if hot {
		knob := u.px(12)
		x := track.X + track.W*done
		u.c.Fill(gfx.Rect{X: x - knob/2, Y: track.Y + track.H/2 - knob/2, W: knob, H: knob}, colPlayhead)
		// The time under the pointer, above the bar.
		if u.active == "" {
			f := u.face(11.5)
			text := clock(at(u.mx))
			w := f.Measure(text) + u.px(12)
			tip := gfx.Rect{X: min(max(u.mx-w/2, r.X+u.px(4)), r.Right()-w-u.px(4)), Y: r.Y - u.px(26), W: w, H: u.px(22)}
			u.c.Fill(tip, gfx.Alpha(colBlack, 0.75))
			u.label(f, tip, text, colText, 0.5)
		}
	}
	if u.press("watch/seek", seek) {
		a.watch.resume = a.player.Playing()
		a.player.Stop()
	}
	if u.dragging("watch/seek") {
		a.playhead = at(u.mx)
		a.requestFrame()
	}
	if u.letGo("watch/seek") {
		a.playhead = at(u.mx)
		a.requestFrame()
		if a.watch.resume {
			a.watch.resume = false
			a.togglePlay()
		}
	}

	row := gfx.Rect{X: r.X + u.px(12), Y: seek.Bottom() + u.px(2), W: r.W - u.px(24), H: r.Bottom() - seek.Bottom() - u.px(6)}
	glyph, hint := icPlay, u.tf("Play (%s)", a.commandKey("video.play"))
	if a.player.Playing() {
		glyph, hint = icPause, u.tf("Pause (%s)", a.commandKey("video.play"))
	}
	pr := gfx.Rect{X: row.X, Y: row.Y + (row.H-u.px(32))/2, W: u.px(36), H: u.px(32)}
	if u.iconButton("watch/play", pr, glyph, hint, false, true) {
		a.togglePlay()
	}
	x := pr.Right() + u.px(10)
	f := u.bold(13)
	u.c.Text(f, x, row.Y+(row.H-f.Height())/2, clock(a.playhead), colText)
	x += f.Measure(clock(a.playhead)) + u.px(6)
	fd := u.face(12)
	u.c.Text(fd, x, row.Y+(row.H-fd.Height())/2+u.px(1), "/ "+clock(length), colDim)

	leave := gfx.Rect{X: row.Right() - u.px(36), Y: pr.Y, W: u.px(36), H: u.px(32)}
	if u.iconButton("watch/leave", leave, icFullscreenExit, u.tf("Leave player mode (%s or Esc)", a.commandKey("video.watch")), false, true) {
		a.setWatching(false)
	}
}

// commandKey is the first key of the general or video editing command id, for a hint.
func (a *App) commandKey(id string) string {
	for _, c := range a.appCommands() {
		if c.id == id {
			return a.keyHint(c.id, c.defaults)
		}
	}
	return ""
}
