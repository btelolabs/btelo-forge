package ui

import (
	"time"

	"github.com/btelolabs/btelo-video-cut/internal/gfx"
)

type toastAction struct {
	label string
	run   func()
}

type toast struct {
	text    string
	err     bool
	actions []toastAction
	until   time.Time
}

func (a *App) toast(text string, err bool) { a.toastActions(text, err, nil) }

func (a *App) toastActions(text string, err bool, actions []toastAction) {
	life := 4 * time.Second
	if err || len(actions) > 0 {
		life = 9 * time.Second
	}
	a.toasts = append(a.toasts, toast{text: text, err: err, actions: actions, until: time.Now().Add(life)})
	if len(a.toasts) > 4 {
		a.toasts = a.toasts[len(a.toasts)-4:]
	}
}

// toastLayout places each toast under the top bar, with its action buttons on the right.
func (a *App) toastLayout() ([]gfx.Rect, [][]gfx.Rect) {
	u := &a.u
	var boxes []gfx.Rect
	var buttons [][]gfx.Rect
	y := u.px(64)
	f := u.face(13)
	for _, t := range a.toasts {
		bw := float32(0)
		for _, act := range t.actions {
			bw += f.Measure(act.label) + u.px(20) + u.px(6)
		}
		w := min(u.px(38)+f.Measure(t.text)+u.px(16)+bw+u.px(4), a.screen.W-u.px(40))
		r := gfx.Rect{X: (a.screen.W - w) / 2, Y: y, W: w, H: u.px(42)}
		var bs []gfx.Rect
		x := r.Right() - u.px(4)
		for k := len(t.actions) - 1; k >= 0; k-- {
			wb := f.Measure(t.actions[k].label) + u.px(20)
			x -= wb + u.px(6)
			bs = append([]gfx.Rect{{X: x, Y: r.Y + u.px(6), W: wb, H: u.px(30)}}, bs...)
		}
		boxes = append(boxes, r)
		buttons = append(buttons, bs)
		y += r.H + u.px(8)
	}
	return boxes, buttons
}

// preToasts lets toasts take mouse presses before the widgets under them see them.
func (a *App) preToasts() {
	now := time.Now()
	kept := a.toasts[:0]
	for _, t := range a.toasts {
		if now.Before(t.until) {
			kept = append(kept, t)
		}
	}
	a.toasts = kept
	u := &a.u
	boxes, buttons := a.toastLayout()
	for i := len(boxes) - 1; i >= 0; i-- {
		if !u.pressed || !boxes[i].Contains(u.pressX, u.pressY) {
			continue
		}
		u.pressed = false
		for k, b := range buttons[i] {
			if b.Contains(u.pressX, u.pressY) {
				a.toasts[i].actions[k].run()
			}
		}
		a.toasts = append(a.toasts[:i], a.toasts[i+1:]...)
		return
	}
}

func (a *App) drawToasts() {
	u := &a.u
	boxes, buttons := a.toastLayout()
	f := u.face(13)
	for i, r := range boxes {
		t := a.toasts[i]
		u.c.RoundRect(gfx.Rect{X: r.X, Y: r.Y + u.px(3), W: r.W, H: r.H}, u.px(10), gfx.Alpha(colBlack, 0.45))
		bg := gfx.Hex(0x2b3040)
		if t.err {
			bg = gfx.Mix(bg, colDanger, 0.35)
		}
		u.c.RoundRect(r, u.px(10), bg)
		glyph, col := icCheck, colSuccess
		if t.err {
			glyph, col = icClose, gfx.Hex(0xffb4b2)
		} else if len(t.actions) == 0 {
			glyph, col = icSettings, colDim
		}
		u.icon(glyph, gfx.Rect{X: r.X + u.px(10), Y: r.Y, W: u.px(22), H: r.H}, 13, col)
		textW := r.W - u.px(46)
		if len(buttons[i]) > 0 {
			textW = buttons[i][0].X - r.X - u.px(46)
		}
		u.label(f, gfx.Rect{X: r.X + u.px(38), Y: r.Y, W: textW, H: r.H}, t.text, colText, 0)
		for k, b := range buttons[i] {
			hot := b.Contains(u.mx, u.my)
			bgc := colSurfaceHi
			if hot {
				bgc = colAccent
				u.cursor = CursorHand
			}
			u.c.RoundRect(b, u.px(6), bgc)
			u.label(f, b, t.actions[k].label, colText, 0.5)
		}
	}
}
