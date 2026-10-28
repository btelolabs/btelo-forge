package ui

import (
	"image/color"
	"math"
	"time"

	"github.com/btelolabs/btelo-forge/internal/gfx"
)

type buttonKind int

const (
	plain buttonKind = iota
	primary
	ghost
	danger
)

// tip is the tooltip candidate: what the mouse rests on and since when.
type tip struct {
	id    string
	text  string
	at    gfx.Rect
	since time.Time
}

func (u *ctx) hint(id, text string, r gfx.Rect) {
	if text == "" || !u.hover(id, r) || u.active != "" {
		return
	}
	if u.tip.id != id {
		u.tip = tip{id: id, text: text, at: r, since: time.Now()}
	}
	u.tipSeen = true
}

// buttonWidth is how wide button needs to be to show its icon and text in full.
func (u *ctx) buttonWidth(glyph, text string) float32 {
	w := u.face(13).Measure(text) + u.px(16)
	if glyph != "" {
		w += u.px(16)
		if text != "" {
			w += u.px(6)
		}
	}
	return w + u.px(4)
}

// button draws a labelled button with an optional icon and reports a click.
func (u *ctx) button(id string, r gfx.Rect, glyph, text string, kind buttonKind, enabled bool) bool {
	hot := enabled && u.hover(id, r)
	bg, fg, edge := colSurface, colText, colBorder
	switch kind {
	case primary:
		bg, fg, edge = colPrimary, colOnPrimary, color.RGBA{}
		if hot {
			bg = colPrimaryHi
		}
	case danger:
		bg, edge = gfx.Mix(colSurface, colDanger, 0.22), gfx.Mix(colBorder, colDanger, 0.35)
		if hot {
			bg = gfx.Mix(colSurface, colDanger, 0.4)
		}
	case ghost:
		bg, edge = color.RGBA{}, color.RGBA{}
		if hot {
			bg = colSurfaceHi
		}
	default:
		if hot {
			bg, edge = colSurfaceHi, colBorderHi
		}
	}
	if !enabled {
		fg = colFaint
		if kind == primary {
			bg = gfx.Mix(colSurface, colPrimary, 0.12)
		}
	}
	if u.active == id && enabled {
		bg = gfx.Mix(bg, colBlack, 0.15)
	}
	u.c.Fill(r, bg)
	if edge.A > 0 {
		u.c.Outline(r, max(1, u.px(1)), edge)
	}
	if hot {
		u.cursor = CursorHand
	}
	f := u.face(13)
	iw := float32(0)
	if glyph != "" {
		iw = u.px(16)
		if text != "" {
			iw += u.px(6)
		}
	}
	tw := f.Ellipsize(text, r.W-iw-u.px(16))
	total := iw + f.Measure(tw)
	x := r.X + (r.W-total)/2
	if glyph != "" {
		u.icon(glyph, gfx.Rect{X: x, Y: r.Y, W: u.px(16), H: r.H}, 14, fg)
	}
	if text != "" {
		u.c.Text(f, x+iw, r.Y+(r.H-f.Height())/2, tw, fg)
	}
	if !enabled {
		return false
	}
	return u.clicked(id, r)
}

// iconButton is a square symbol button with a tooltip.
func (u *ctx) iconButton(id string, r gfx.Rect, glyph, hint string, on, enabled bool) bool {
	hot := enabled && u.hover(id, r)
	fg := colDim
	if hot || on {
		fg = colText
	}
	if on {
		u.c.Fill(r, colSelected)
	} else if hot {
		u.c.Fill(r, colSurfaceHi)
	}
	if !enabled {
		fg = colFaint
	}
	if hot {
		u.cursor = CursorHand
	}
	u.icon(glyph, r, 16, fg)
	u.hint(id, hint, r)
	return enabled && u.clicked(id, r)
}

// chips is a row of mutually exclusive options; it returns the chosen index.
func (u *ctx) chips(id string, r gfx.Rect, options []string, selected int, enabled bool) int {
	n := float32(len(options))
	gap := u.px(4)
	w := (r.W - gap*(n-1)) / n
	f := u.face(12.5)
	for i, o := range options {
		cr := gfx.Rect{X: r.X + float32(i)*(w+gap), Y: r.Y, W: w, H: r.H}
		cid := id + "/" + o
		hot := enabled && u.hover(cid, cr)
		bg, fg, edge := colSurface, colDim, colBorder
		if i == selected {
			bg, fg, edge = colSelected, colText, colBorderHi
		} else if hot {
			bg, fg = colSurfaceHi, colText
		}
		if !enabled {
			fg = colFaint
		}
		u.c.Fill(cr, bg)
		u.c.Outline(cr, max(1, u.px(1)), edge)
		u.label(f, cr, o, fg, 0.5)
		if hot {
			u.cursor = CursorHand
		}
		if enabled && u.clicked(cid, cr) {
			selected = i
		}
	}
	return selected
}

// tabs is a row of page names over a rule, the open one underlined; it returns the chosen index. Unlike chips it
// reads as navigation rather than as buttons that do something.
func (u *ctx) tabs(id string, r gfx.Rect, names []string, selected int) int {
	u.c.Fill(gfx.Rect{X: r.X, Y: r.Bottom() - max(1, u.px(1)), W: r.W, H: max(1, u.px(1))}, colBorder)
	w := r.W / float32(len(names))
	for i, name := range names {
		tr := gfx.Rect{X: r.X + float32(i)*w, Y: r.Y, W: w, H: r.H}
		tid := id + "/" + name
		hot := u.hover(tid, tr)
		f, fg := u.face(13), colDim
		switch {
		case i == selected:
			f, fg = u.bold(13), colText
			u.c.Fill(gfx.Rect{X: tr.X + u.px(12), Y: tr.Bottom() - u.px(2), W: tr.W - u.px(24), H: u.px(2)}, colText)
		case hot:
			fg = colText
			u.cursor = CursorHand
		}
		u.label(f, gfx.Rect{X: tr.X, Y: tr.Y, W: tr.W, H: tr.H - u.px(3)}, name, fg, 0.5)
		if u.clicked(tid, tr) {
			selected = i
		}
	}
	return selected
}

// slider edits value in [lo, hi]; changed is true while it moves, done once when the drag ends.
func (u *ctx) slider(id string, r gfx.Rect, value *float64, lo, hi float64, enabled bool) (changed, done bool) {
	track := gfx.Rect{X: r.X + u.px(7), Y: r.Y + r.H/2 - u.px(2), W: r.W - u.px(14), H: u.px(4)}
	t := float32((*value - lo) / (hi - lo))
	t = min(max(t, 0), 1)
	u.c.Fill(track, colSurfaceHi)
	fill := gfx.Mix(colDim, colText, 0.6)
	if !enabled {
		fill = colFaint
	}
	u.c.Fill(gfx.Rect{X: track.X, Y: track.Y, W: track.W * t, H: track.H}, fill)
	knob := track.X + track.W*t
	hot := enabled && (u.hover(id, r) || u.active == id)
	half := u.px(5)
	if hot {
		half = u.px(6)
		u.cursor = CursorHand
	}
	k := gfx.Rect{X: knob - half, Y: r.Y + r.H/2 - half, W: 2 * half, H: 2 * half}
	u.c.Fill(k.Inset(-max(1, u.px(1))), colBackground)
	u.c.Fill(k, colWhite)
	if !enabled {
		return false, false
	}
	u.press(id, r)
	if u.dragging(id) || (u.active == id && !u.down) {
		v := lo + (hi-lo)*float64(min(max((u.mx-track.X)/track.W, 0), 1))
		if math.Abs(v-*value) > 1e-9 {
			*value, changed = v, true
		}
	}
	if u.letGo(id) {
		done = true
	}
	return changed, done
}

// checkboxWidth is how wide a checkbox needs to be to show its box and text in full.
func checkboxWidth(u *ctx, text string) float32 { return u.px(18+8+6) + u.face(13).Measure(text) }

// checkbox is a small toggle with a label; it returns the new state.
func (u *ctx) checkbox(id string, r gfx.Rect, text string, on bool) bool {
	hot := u.hover(id, r)
	box := gfx.Rect{X: r.X, Y: r.Y + (r.H-u.px(18))/2, W: u.px(18), H: u.px(18)}
	if on {
		u.c.Fill(box, colPrimary)
		u.icon(icCheck, box, 12, colOnPrimary)
	} else {
		u.c.Fill(box, colSurface)
		u.c.Outline(box, max(1, u.px(1)), colBorderHi)
	}
	col := colDim
	if hot {
		col = colText
		u.cursor = CursorHand
	}
	u.label(u.face(13), gfx.Rect{X: box.Right() + u.px(8), Y: r.Y, W: r.W - box.W - u.px(8), H: r.H}, text, col, 0)
	if u.clicked(id, r) {
		return !on
	}
	return on
}

// panelTitle draws a small section heading.
func (u *ctx) heading(r gfx.Rect, text string) {
	u.label(u.bold(12.5), r, text, colDim, 0)
}

// drawTip shows the tooltip of whatever the mouse rested on for half a second; it returns how long until it shows.
func (u *ctx) drawTip(screen gfx.Rect) time.Duration {
	if !u.tipSeen {
		u.tip = tip{}
		return 0
	}
	wait := 500*time.Millisecond - time.Since(u.tip.since)
	if wait > 0 {
		return wait
	}
	f := u.face(12)
	w := f.Measure(u.tip.text) + u.px(16)
	h := u.px(26)
	x := min(max(u.tip.at.X+u.tip.at.W/2-w/2, screen.X+u.px(4)), screen.Right()-w-u.px(4))
	y := u.tip.at.Bottom() + u.px(6)
	if y+h > screen.Bottom() {
		y = u.tip.at.Y - h - u.px(6)
	}
	r := gfx.Rect{X: x, Y: y, W: w, H: h}
	u.c.Fill(gfx.Rect{X: r.X, Y: r.Y + u.px(2), W: r.W, H: r.H}, gfx.Alpha(colBlack, 0.4))
	u.c.Fill(r, colSelected)
	u.c.Outline(r, max(1, u.px(1)), colBorderHi)
	u.label(f, r, u.tip.text, colText, 0.5)
	return 0
}

// flowChips lays out options of their natural width, wrapping onto more rows; it returns the choice and the height.
func (u *ctx) flowChips(id string, r gfx.Rect, options []string, selected int) (int, float32) {
	return u.flowChipsSome(id, r, options, selected, nil)
}

// flowChipsSome is flowChips where options whose entry in enabled is false are shown but cannot be picked; a nil
// enabled allows every option.
func (u *ctx) flowChipsSome(id string, r gfx.Rect, options []string, selected int, enabled []bool) (int, float32) {
	f := u.face(12.5)
	gap := u.px(4)
	x, y := r.X, r.Y
	for i, o := range options {
		on := enabled == nil || enabled[i]
		w := f.Measure(o) + u.px(18)
		if x > r.X && x+w > r.Right() {
			x, y = r.X, y+r.H+gap
		}
		cr := gfx.Rect{X: x, Y: y, W: w, H: r.H}
		cid := id + "/" + o
		hot := on && u.hover(cid, cr)
		bg, fg, edge := colSurface, colDim, colBorder
		switch {
		case i == selected:
			bg, fg, edge = colSelected, colText, colBorderHi
		case hot:
			bg, fg = colSurfaceHi, colText
		case !on:
			fg = colFaint
		}
		u.c.Fill(cr, bg)
		u.c.Outline(cr, max(1, u.px(1)), edge)
		u.label(f, cr, o, fg, 0.5)
		if hot {
			u.cursor = CursorHand
		}
		if on && u.clicked(cid, cr) {
			selected = i
		}
		x += w + gap
	}
	return selected, y + r.H - r.Y
}
