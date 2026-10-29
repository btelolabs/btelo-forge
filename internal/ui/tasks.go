package ui

import (
	"fmt"
	"math"
	"slices"
	"time"

	"github.com/btelolabs/btelo-forge/internal/gfx"
)

// task is work running in the background that a card in the window's bottom right corner shows until it ends,
// whichever page is in front: what it is, how far it has got, a way to stop it and a way back to where it began.
type task struct {
	id       string // stays the same while the work runs, for the card's buttons
	glyph    string
	title    string
	status   string
	fraction float64 // how far the work has got, 0 to 1; below 0 when that cannot be told
	stopping bool    // cancelled, and ending
	cancel   func()
	open     func() // shows the page the work was started from; nil when there is none any more
}

// tasks lists the work running in the background, oldest kind first.
func (a *App) tasks() []task {
	var out []task
	if j := a.subtitles.job; j != nil {
		status, fraction := a.subtitleStage(j)
		out = append(out, task{id: "subtitles", glyph: icSubtitle, title: a.taskTitle(a.u.t("Generating subtitles"), j.doc),
			status: status, fraction: fraction, stopping: j.stopping, cancel: a.subtitles.cancel, open: a.openPanelOf(j.doc, tabSubtitles)})
	}
	if j := a.speech.job; j != nil {
		status, fraction := a.speechStage(j)
		out = append(out, task{id: "speech", glyph: icVoice, title: a.taskTitle(a.u.t("Generating speech"), j.doc),
			status: status, fraction: fraction, stopping: j.stopping, cancel: a.speech.cancel, open: a.openPanelOf(j.doc, tabSpeech)})
	}
	for _, m := range a.music.addingInOrder() {
		status, fraction := a.u.t("Waiting for the download"), -1.0
		if read, total, ok := a.lib.Progress(m.track); ok && total > 0 {
			fraction = float64(read) / float64(total)
			status = a.u.t("Downloading")
		} else if ok {
			status = a.u.tf("Downloading %.1f MB", float64(read)/1e6)
		}
		out = append(out, task{id: "music/" + m.track.Key(), glyph: icMusic, title: a.taskTitle(a.u.tf("Adding %s", m.track.Title), m.doc),
			status: status, fraction: fraction, stopping: m.stopping, cancel: m.cancel, open: a.openPanelOf(m.doc, tabMusic)})
	}
	if t, ok := a.updateTask(); ok {
		out = append(out, t)
	}
	return out
}

// taskTitle names work done for an edit, adding the edit's tab when another tab is in front.
func (a *App) taskTitle(title string, d *doc) string {
	if d != a.doc && slices.Contains(a.docs, d) {
		return title + " · " + a.tabName(d)
	}
	return title
}

// openPanelOf returns what brings the edit d forward with the side panel's tab showing; it is nil once d's tab
// is closed or no longer holds an edit.
func (a *App) openPanelOf(d *doc, tab int) func() {
	i := slices.Index(a.docs, d)
	if i < 0 || d.p == nil {
		return nil
	}
	return func() {
		a.switchTab(i)
		a.showPanel(tab)
	}
}

// taskBoxes places n task cards in the window's bottom right corner, the first at the bottom, and the cancel
// button on each.
func (a *App) taskBoxes(n int) (cards, cancels []gfx.Rect) {
	u := &a.u
	margin, h := u.px(16), u.px(64)
	w := min(u.px(320), a.screen.W-2*margin)
	y := a.screen.H - margin
	for range n {
		y -= h
		r := gfx.Rect{X: a.screen.W - margin - w, Y: y, W: w, H: h}
		cards = append(cards, r)
		cancels = append(cancels, gfx.Rect{X: r.Right() - u.px(34), Y: r.Y + u.px(6), W: u.px(28), H: u.px(28)})
		y -= u.px(8)
	}
	return cards, cancels
}

// preTasks lets the task cards take a mouse press before the widgets drawn under them see it; it does nothing
// while a dialog or menu holds the window.
func (a *App) preTasks() {
	u := &a.u
	if u.blocked || !u.pressed {
		return
	}
	tasks := a.tasks()
	cards, cancels := a.taskBoxes(len(tasks))
	for i, t := range tasks {
		if u.press("task/cancel/"+t.id, cancels[i]) || u.press("task/open/"+t.id, cards[i]) {
			return
		}
	}
}

// drawTasks draws a card for each piece of work running in the background; live is false while a dialog or menu
// holds the window, and the cards then only show.
func (a *App) drawTasks(live bool) {
	u := &a.u
	tasks := a.tasks()
	cards, cancels := a.taskBoxes(len(tasks))
	for i, t := range tasks {
		r, x := cards[i], cancels[i]
		openID, cancelID := "task/open/"+t.id, "task/cancel/"+t.id
		hotX := live && !t.stopping && u.hover(cancelID, x)
		hot := live && t.open != nil && !hotX && u.hover(openID, r)
		if u.letGo(cancelID) && x.Contains(u.mx, u.my) && !t.stopping {
			t.cancel()
		}
		if u.letGo(openID) && r.Contains(u.mx, u.my) && t.open != nil {
			t.open()
		}
		u.c.Fill(gfx.Rect{X: r.X, Y: r.Y + u.px(3), W: r.W, H: r.H}, gfx.Alpha(colBlack, 0.45))
		bg := colSurfaceHi
		if hot {
			bg = gfx.Mix(colSurfaceHi, colText, 0.05)
			u.cursor = CursorHand
		}
		u.c.Fill(r, bg)
		u.c.Outline(r, max(1, u.px(1)), colBorderHi)
		u.icon(t.glyph, gfx.Rect{X: r.X + u.px(10), Y: r.Y + u.px(8), W: u.px(22), H: u.px(24)}, 15, colAccent)
		textX := r.X + u.px(38)
		u.label(u.bold(12.5), gfx.Rect{X: textX, Y: r.Y + u.px(8), W: x.X - textX - u.px(4), H: u.px(24)}, t.title, colText, 0)
		status := t.status
		if t.stopping {
			status = u.t("Cancelling…")
		}
		line := gfx.Rect{X: textX, Y: r.Y + u.px(30), W: r.Right() - u.px(12) - textX, H: u.px(16)}
		percent := ""
		if t.fraction >= 0 && !t.stopping {
			percent = fmt.Sprintf("%d%%", int(t.fraction*100))
			line.W -= u.face(11.5).Measure("100%") + u.px(8)
		}
		u.label(u.face(11.5), line, status, colDim, 0)
		if percent != "" {
			u.label(u.face(11.5), gfx.Rect{X: line.Right(), Y: line.Y, W: r.Right() - u.px(12) - line.Right(), H: line.H}, percent, colDim, 1)
		}
		u.progressBar(gfx.Rect{X: textX, Y: r.Bottom() - u.px(12), W: r.Right() - u.px(12) - textX, H: u.px(4)}, t.fraction)
		fg := colDim
		if hotX {
			u.c.Fill(x, colSelected)
			fg = colText
			u.cursor = CursorHand
		}
		if t.stopping {
			fg = colFaint
		}
		u.icon(icClose, x, 14, fg)
		if live && !t.stopping {
			u.hint(cancelID, u.t("Cancel"), x)
			if t.open != nil && !hotX {
				u.hint(openID, u.t("Go to its panel"), r)
			}
		}
	}
}

// progressBar draws how far work has got, from 0 to 1; below 0, when that cannot be told, a block sweeps along it.
func (u *ctx) progressBar(r gfx.Rect, fraction float64) {
	u.c.Fill(r, colSurface)
	if fraction >= 0 {
		u.c.Fill(gfx.Rect{X: r.X, Y: r.Y, W: max(r.W*float32(min(fraction, 1)), r.H), H: r.H}, colAccent)
		return
	}
	const period = 1.6
	phase := math.Mod(float64(time.Now().UnixNano())/1e9, period) / period
	w := r.W / 4
	x := r.X - w + (r.W+w)*float32(phase)
	if left, right := max(x, r.X), min(x+w, r.Right()); right > left {
		u.c.Fill(gfx.Rect{X: left, Y: r.Y, W: right - left, H: r.H}, colAccent)
	}
}
