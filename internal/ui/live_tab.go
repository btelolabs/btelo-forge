package ui

import (
	"errors"
	"image/color"
	"strings"
	"time"

	"github.com/btelolabs/btelo-forge/internal/capture"
	"github.com/btelolabs/btelo-forge/internal/gfx"
)

// The live stream has a tab of its own, as a canvas does: the stream's picture as the platforms get it, how it is
// going, the buttons that start and end it and the recording it shows, and beside them the destinations and how the
// stream is made. There is one such tab at most; opening it again brings it forward.

// openLive brings the stream's tab forward, opening it in the tab in front when that is empty, or in a new one.
func (a *App) openLive() {
	if !capture.Supported() {
		return
	}
	for i, d := range a.docs {
		if d.liveTab {
			a.switchTab(i)
			return
		}
	}
	if a.occupied() {
		a.newTab()
	}
	a.liveTab = true
	a.checkRecording() // the microphones are listed with the screens
	a.requestFrame()
}

// drawLiveTab is the stream's tab: the picture and its controls, with the destinations and choices beside them.
func (a *App) drawLiveTab(r gfx.Rect) {
	u := &a.u
	u.c.Fill(r, colPanel)
	side := min(max(u.px(400), r.W*0.32), u.px(520))
	if r.W-side < u.px(420) {
		side = max(r.W*0.45, u.px(300))
	}
	stage := rectAt(r.X, r.Y, r.W-side, r.H)
	panelR := rectAt(stage.Right(), r.Y, side, r.H)
	a.drawLiveStage(stage)
	u.c.Fill(panelR, colBackground)
	u.c.Fill(rectAt(panelR.X, panelR.Y, max(1, u.px(1)), panelR.H), colBorder)
	a.drawLiveSide(panelR)
	for _, k := range u.keys {
		if k.Code == keyEscape && u.focus == "" && a.onAir.edit != nil {
			a.onAir.edit = nil
		}
	}
}

// drawLiveStage is the stream's picture, with its state above it and the buttons and figures below.
func (a *App) drawLiveStage(r gfx.Rect) {
	u := &a.u
	l := &a.onAir
	in := r.Inset(u.px(24))
	var st capture.StreamStatus
	if l.stream != nil {
		st = l.stream.Status()
	}

	// The state: live, starting or ending, or not live.
	title := rectAt(in.X, in.Y, in.W, u.px(30))
	u.label(u.bold(18), rectAt(title.X, title.Y, in.W/2, title.H), u.t("Live stream"), colText, 0)
	badgeText, badgeCol := u.t("Not live"), colSurfaceHi
	switch {
	case l.stopping:
		badgeText, badgeCol = u.t("Ending…"), colSurfaceHi
	case l.starting:
		badgeText, badgeCol = u.t("Starting…"), colSurfaceHi
	case l.stream != nil:
		badgeText, badgeCol = u.t("LIVE"), colDanger
	}
	bf := u.bold(11)
	bw := bf.Measure(badgeText) + u.px(20)
	badge := rectAt(in.Right()-bw, title.Y+u.px(4), bw, u.px(22))
	u.c.Fill(badge, badgeCol)
	u.label(bf, badge, badgeText, colWhite, 0.5)
	if l.stream != nil {
		u.label(u.bold(15), rectAt(badge.X-u.px(90), title.Y, u.px(82), title.H), shortClock(st.Elapsed.Seconds()), colText, 1)
	}

	// The picture, as large as fits, in the canvas's shape.
	bar := rectAt(in.X, in.Bottom()-u.px(92), in.W, u.px(92))
	area := rectAt(in.X, title.Bottom()+u.px(16), in.W, bar.Y-u.px(16)-title.Bottom()-u.px(16))
	canvas := a.settings.Live.canvas()
	if l.stream != nil {
		canvas = l.stream.Options().Size
	}
	pw := min(area.W, area.H*float32(canvas.X)/float32(canvas.Y))
	ph := pw * float32(canvas.Y) / float32(canvas.X)
	pic := rectAt(area.X+(area.W-pw)/2, area.Y+(area.H-ph)/2, pw, ph)
	u.c.Fill(pic, colBlack)
	u.c.Outline(pic.Inset(-u.px(1)), max(1, u.px(1)), colBorder)
	caption := ""
	switch {
	case l.stream != nil:
		if l.preview == nil || time.Since(l.previewAt) > 90*time.Millisecond {
			l.preview, l.previewAt = l.stream.Preview(int(pic.W)), time.Now()
		}
		u.c.Image(l.preview, pic)
		if !st.Screen {
			caption = u.t("Nothing is being recorded, so the stream shows black. Record the screen to show it; the stream goes on meanwhile.")
		}
	default:
		l.preview = nil
		caption = u.t("Once live, the stream shows here as the platforms get it: the screen while it is recorded, black otherwise.")
	}
	if caption != "" {
		box := rectAt(pic.X+pic.W*0.15, pic.Y+pic.H/2-u.px(40), pic.W*0.7, u.px(80))
		a.paragraphCentred(box, caption, 13, colDim)
	}

	// The figures, and the buttons.
	y := bar.Y + u.px(4)
	if l.stream != nil {
		live := 0
		for _, t := range st.Targets {
			if t.State == capture.TargetLive {
				live++
			}
		}
		col := colSuccess
		if live < len(st.Targets) {
			col = colWarning
		}
		if live == 0 {
			col = colDangerText
		}
		type figure struct {
			text string
			col  color.RGBA
		}
		figures := []figure{
			{u.tf("%d of %d destinations live", live, len(st.Targets)), col},
			{u.tf("Encoding %.1f Mb/s", st.Kbps/1000), colDim},
			{u.t("Showing the screen being recorded"), colDim},
		}
		if !st.Screen {
			figures[2] = figure{u.t("Showing black"), colWarning}
		}
		if time.Since(l.lateAt) >= lateWindow {
			l.lateRecent, l.lateBefore, l.lateAt = st.FrameDrops-l.lateBefore, st.FrameDrops, time.Now()
		}
		if fps := l.stream.Options().FPS; l.lateRecent > fps/2 {
			figures = append(figures, figure{u.tf("%d frames late in the last few seconds: the computer cannot keep up", l.lateRecent), colWarning})
		}
		x := in.X
		for _, f := range figures {
			w := u.face(12.5).Measure(f.text)
			u.label(u.face(12.5), rectAt(x, y, w+u.px(2), u.px(20)), f.text, f.col, 0)
			x += w + u.px(22)
		}
	} else if reason := a.liveBlocker(); reason != "" && !l.starting {
		u.label(u.face(12.5), rectAt(in.X, y, in.W, u.px(20)), reason, colFaint, 0)
	}
	btnY := bar.Bottom() - u.px(40)
	right := in.Right()
	if l.stream != nil || l.starting {
		stop := rectAt(right-u.px(180), btnY, u.px(180), u.px(40))
		if u.button("live/stop", stop, icStop, u.t("End the stream"), danger, l.stream != nil && !l.stopping) {
			a.stopLive(nil)
		}
		right = stop.X - u.px(10)
	} else {
		start := rectAt(right-u.px(200), btnY, u.px(200), u.px(40))
		if u.button("live/start", start, icLive, u.t("Start streaming"), primary, a.liveBlocker() == "" && a.export == nil) {
			a.startLive(nil)
		}
		right = start.X - u.px(10)
	}
	if a.recorder() != nil {
		label, glyph := u.t("Record the screen"), icRecord
		if a.rec.rec != nil {
			label, glyph = u.t("Stop recording"), icStop
		}
		w := max(u.px(170), u.buttonWidth(glyph, label))
		rec := rectAt(right-w, btnY, w, u.px(40))
		if u.button("live/record", rec, glyph, label, plain, !a.rec.starting && !a.rec.stopping && a.export == nil) {
			if a.rec.rec != nil {
				a.stopRecording(nil)
			} else {
				a.startRecording(func(_ string, err error) {
					if err != nil && !errors.Is(err, errRegionCancelled) {
						a.toast(a.u.tf("Recording could not start: %s", a.describe(err)), true)
					}
				})
			}
		}
		u.hint("live/record", u.t("The stream shows what is recorded; stopping the recording leaves the stream on, black"), rec)
	}
}

// drawLiveSide is the destinations and how the stream is made, or the destination being changed.
func (a *App) drawLiveSide(r gfx.Rect) {
	u := &a.u
	l := &a.onAir
	in := r.Inset(u.px(20))
	draw := a.liveBody
	foot := float32(0)
	if l.edit != nil {
		draw, foot = a.liveEditor, u.px(54)
	}
	a.scrolledBy("live/scroll", rectAt(in.X, in.Y, in.W+u.px(8), in.H-foot), &l.scroll, &l.content, draw)
	if e := l.edit; e != nil {
		y := in.Bottom() - u.px(40)
		e.d.Key = sealedText(strings.TrimSpace(e.key))
		if u.button("live/edit/cancel", rectAt(in.X, y, u.px(110), u.px(40)), "", u.t("Cancel"), ghost, true) {
			l.edit = nil
		}
		if u.button("live/edit/save", rectAt(in.Right()-u.px(200), y, u.px(200), u.px(40)), icCheck, u.t("Save destination"), primary, a.destinationProblem(e.d) == "") {
			if err := a.saveDestination(e.index, e.d); err != nil {
				a.toast(a.describe(err), true)
			} else {
				l.edit = nil
			}
		}
	}
}

// lateWindow is how often the frames taken late are counted.
const lateWindow = 5 * time.Second

// paragraphCentred draws wrapped text centred in r, both ways.
func (a *App) paragraphCentred(r gfx.Rect, text string, size float32, col color.RGBA) {
	u := &a.u
	f := u.face(size)
	lines := f.Wrap(text, r.W)
	lineH := u.px(size * 1.6)
	y := r.Y + (r.H-float32(len(lines))*lineH)/2
	for _, line := range lines {
		u.label(f, rectAt(r.X, y, r.W, lineH), line, col, 0.5)
		y += lineH
	}
}
