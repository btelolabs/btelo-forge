package ui

import (
	"fmt"
	"math"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/btelolabs/btelo-forge/internal/gfx"
	"github.com/btelolabs/btelo-forge/internal/project"
)

var panelTabs = msgs("Music", "Overlays", "Subtitles", "Voice", "Export")

// timeFields hold what the user is typing into the start and end boxes until it is committed.
type timeFields struct {
	id         int
	start, end string
}

func baseName(path string) string { return filepath.Base(path) }

func (a *App) drawPanel(r gfx.Rect) {
	u := &a.u
	u.c.Fill(r, colPanel)
	u.c.Fill(gfx.Rect{X: r.X, Y: r.Y, W: max(1, u.px(1)), H: r.H}, colBorder)
	in := gfx.Rect{X: r.X + u.px(16), Y: r.Y + u.px(12), W: r.W - u.px(32), H: r.H - u.px(24)}
	if pick := u.tabs("panel/tabs", gfx.Rect{X: in.X, Y: in.Y, W: in.W, H: u.px(32)}, u.ts(panelTabs), a.tab); pick != a.tab {
		a.tab = pick
	}
	body := gfx.Rect{X: in.X, Y: in.Y + u.px(44), W: in.W, H: in.H - u.px(44)}
	if a.p != nil && a.selectionCard() {
		used := a.drawSelectionCard(body)
		body.Y, body.H = body.Y+used, body.H-used
	}
	switch a.tab {
	case tabMusic:
		a.drawMusicPanel(body)
	case tabOverlays:
		a.scrolled(body, a.drawOverlayPanel)
	case tabSubtitles:
		a.scrolled(body, a.drawSubtitlePanel)
	case tabSpeech:
		a.scrolled(body, a.drawSpeechPanel)
	case tabExport:
		a.drawExportTab(body)
	}
}

// scrolled draws the side panel's content, which may be taller than r.
func (a *App) scrolled(r gfx.Rect, draw func(gfx.Rect) float32) {
	a.scrolledBy("panel/scroll", r, &a.panelScroll[a.tab], &a.panelContent[a.tab], draw)
}

// scrolledBy draws content that may be taller than r, scrolled by the wheel and clipped to r, with a scroll bar;
// offset is how far it is scrolled and content its height at the last frame.
func (a *App) scrolledBy(id string, r gfx.Rect, offset, contentH *float32, draw func(gfx.Rect) float32) {
	u := &a.u
	// Clamp to the content height of the last frame first, so shorter content never shows an empty view.
	if last := *contentH; last > 0 {
		*offset = min(max(*offset, 0), max(last-r.H, 0))
	}
	u.c.Push(r)
	bottom := draw(gfx.Rect{X: r.X, Y: r.Y - *offset, W: r.W - u.px(8), H: r.H + *offset})
	u.c.Pop()
	content := bottom + *offset - r.Y + u.px(12)
	*contentH = content
	if u.hover(id, r) && u.wheel != 0 {
		*offset -= u.wheel * u.px(60)
		u.wheel = 0
	}
	*offset = min(max(*offset, 0), max(content-r.H, 0))
	if content > r.H {
		h := max(r.H*r.H/content, u.px(24))
		y := r.Y + (r.H-h)*(*offset)/(content-r.H)
		u.c.Fill(gfx.Rect{X: r.Right() - u.px(4), Y: y, W: u.px(3), H: h}, colSurfaceHi)
	}
}

// drawOverlayPanel draws the overlays tab and returns the bottom of its content.
func (a *App) drawOverlayPanel(r gfx.Rect) float32 {
	u := &a.u
	// Two rows of two, so every label fits the panel's width.
	bw := (r.W - u.px(8)) / 2
	for i, b := range []struct {
		id, glyph, label, hint string
		run                    func()
	}{
		{"ov/addtext", icFont, u.t("Text"), u.t("Add text at the playhead (T)"), a.addText},
		{"ov/addimage", icImage, u.t("Image"), u.t("Add an image file at the playhead"), a.chooseImage},
		{"ov/adddrawing", icBrush, u.t("Drawing"), u.t("Draw over the video at the playhead, with brushes and layers"), a.addDrawing},
		{"ov/paste", icPaste, u.t("Paste"), u.t("Paste an image or text from the clipboard (Ctrl+V)"), a.paste},
	} {
		br := gfx.Rect{X: r.X + float32(i%2)*(bw+u.px(8)), Y: r.Y + float32(i/2)*u.px(42), W: bw, H: u.px(34)}
		kind := plain
		if i == 0 {
			kind = primary
		}
		if u.button(b.id, br, b.glyph, b.label, kind, true) {
			b.run()
		}
		u.hint(b.id, b.hint, br)
	}
	y := r.Y + u.px(88)
	if a.sel.kind == selOverlay && !a.selectionCard() {
		if o, ok := a.p.Overlay(a.sel.id); ok {
			return a.drawOverlayEditor(gfx.Rect{X: r.X, Y: y, W: r.W, H: r.Bottom() - y}, o)
		}
	}
	var all []project.Overlay
	for _, tr := range a.p.Overlays {
		all = append(all, tr.Items...)
	}
	if len(all) == 0 {
		f := u.face(12.5)
		for k, l := range f.Wrap(u.t("No overlays yet. Text and images appear at the playhead; drag their ends on the timeline to change how long they show, and drag them in the picture to move them."), r.W) {
			u.c.Text(f, r.X, y+float32(k)*f.Height()*1.3, l, colDim)
		}
		return y + u.px(80)
	}
	u.heading(gfx.Rect{X: r.X, Y: y, W: r.W, H: u.px(22)}, u.tf("All overlays (%d)", len(all)))
	y += u.px(28)
	sort.SliceStable(all, func(i, j int) bool { return all[i].Start < all[j].Start })
	for _, o := range all {
		row := gfx.Rect{X: r.X, Y: y, W: r.W, H: u.px(42)}
		id := fmt.Sprintf("ov/row/%d", o.ID)
		bg := colSurface
		if u.hover(id, row) {
			bg = colSurfaceHi
			u.cursor = CursorHand
		}
		u.c.Fill(row, bg)
		sw := gfx.Rect{X: row.X + u.px(10), Y: row.Y + u.px(11), W: u.px(20), H: u.px(20)}
		glyph, label := overlayLook(o)
		if o.Kind == project.ImageOverlay {
			u.c.Fill(sw, colImage)
			u.icon(glyph, sw, 10, colText)
		} else {
			u.c.Fill(sw, gfx.RGBA(o.Color))
		}
		u.label(u.face(12.5), gfx.Rect{X: row.X + u.px(40), Y: row.Y, W: row.W - u.px(150), H: row.H}, label, colText, 0)
		u.label(u.face(11), gfx.Rect{X: row.Right() - u.px(110), Y: row.Y, W: u.px(100), H: row.H}, clock(o.Start)+" – "+clock(o.End), colDim, 1)
		if u.clicked(id, row) {
			a.sel = selection{kind: selOverlay, id: o.ID}
			a.seek(o.Start)
		}
		y += u.px(48)
	}
	return y
}

// drawOverlayEditor edits the selected text or image.
func (a *App) drawOverlayEditor(r gfx.Rect, o project.Overlay) float32 {
	u := &a.u
	y := r.Y
	title := u.t("Edit the selected text")
	if o.Kind == project.ImageOverlay {
		title = u.t("Edit the selected image")
	}
	if o.Canvas != "" {
		title = u.t("Edit the selected drawing")
	}
	u.heading(gfx.Rect{X: r.X, Y: y, W: r.W - u.px(80), H: u.px(22)}, title)
	if u.button("ov/done", gfx.Rect{X: r.Right() - u.px(70), Y: y - u.px(3), W: u.px(70), H: u.px(28)}, "", u.t("Done"), ghost, true) {
		a.sel = selection{}
		return y
	}
	y += u.px(28)
	update := func(change func(*project.Overlay)) { a.change(func() { a.p.UpdateOverlay(o.ID, change) }) }
	if o.Kind == project.TextOverlay {
		body := o.Body
		if u.focus == "overlay/body" && a.bodyBefore == nil {
			a.bodyBefore = a.p.Clone()
		}
		if changed, _ := u.textBox("overlay/body", gfx.Rect{X: r.X, Y: y, W: r.W, H: u.px(74)}, &body, u.t("Type your text; Enter starts a new line"), true); changed {
			a.p.UpdateOverlay(o.ID, func(x *project.Overlay) { x.Body = body })
			a.dirty = true
		}
		y += u.px(84)
	} else if o.Canvas != "" {
		if u.button("ov/drawing", gfx.Rect{X: r.X, Y: y, W: r.W, H: u.px(34)}, icBrush, u.t("Draw in it"), primary, true) {
			a.editDrawing(o)
		}
		y += u.px(44)
	} else {
		u.label(u.face(12), gfx.Rect{X: r.X, Y: y, W: r.W, H: u.px(20)}, o.Image, colDim, 0)
		y += u.px(28)
	}

	label := u.t("Size")
	lo, hi := 0.03, 0.2
	if o.Kind == project.ImageOverlay {
		label, lo, hi = u.t("Height"), 0.05, 1
	}
	u.label(u.face(12.5), gfx.Rect{X: r.X, Y: y, W: u.px(62), H: u.px(28)}, label, colDim, 0)
	size := o.Size
	sr := gfx.Rect{X: r.X + u.px(64), Y: y, W: r.W - u.px(112), H: u.px(28)}
	sc, sd := u.slider("ov/size", sr, &size, lo, hi, true)
	a.live(sc, sd, func() { a.p.UpdateOverlay(o.ID, func(x *project.Overlay) { x.Size = size }) })
	u.label(u.face(12), gfx.Rect{X: sr.Right() + u.px(4), Y: y, W: u.px(44), H: u.px(28)}, fmt.Sprintf("%d%%", int(math.Round(size*100))), colDim, 1)
	y += u.px(36)
	u.label(u.face(12.5), gfx.Rect{X: r.X, Y: y, W: u.px(62), H: u.px(28)}, u.t("Opacity"), colDim, 0)
	opacity := o.Opacity
	or := gfx.Rect{X: r.X + u.px(64), Y: y, W: r.W - u.px(112), H: u.px(28)}
	oc, od := u.slider("ov/opacity", or, &opacity, 0, 1, true)
	a.live(oc, od, func() { a.p.SetOpacity([]int{o.ID}, opacity) })
	u.label(u.face(12), gfx.Rect{X: or.Right() + u.px(4), Y: y, W: u.px(44), H: u.px(28)}, fmt.Sprintf("%d%%", int(math.Round(opacity*100))), colDim, 1)
	y += u.px(36)

	if o.Kind == project.TextOverlay {
		sw := u.px(26)
		for k, c := range textColors {
			cr := gfx.Rect{X: r.X + float32(k)*(sw+u.px(8)), Y: y, W: sw, H: sw}
			id := fmt.Sprintf("ov/color/%d", k)
			u.c.Fill(cr, gfx.RGBA(c))
			if o.Color == c {
				u.c.Outline(cr.Inset(-u.px(3)), u.px(2), colText)
			} else if u.hover(id, cr) {
				u.c.Outline(cr.Inset(-u.px(2)), u.px(1), colText)
				u.cursor = CursorHand
			}
			if u.clicked(id, cr) {
				update(func(x *project.Overlay) { x.Color = c })
			}
		}
		y += sw + u.px(12)
		if v := u.checkbox("ov/outline", gfx.Rect{X: r.X, Y: y, W: u.px(100), H: u.px(26)}, u.t("Outline"), o.Outline); v != o.Outline {
			update(func(x *project.Overlay) { x.Outline = v })
		}
		if v := u.checkbox("ov/box", gfx.Rect{X: r.X + u.px(110), Y: y, W: u.px(110), H: u.px(26)}, u.t("Background"), o.Box); v != o.Box {
			update(func(x *project.Overlay) { x.Box = v })
		}
		y += u.px(36)
	}

	u.label(u.face(12.5), gfx.Rect{X: r.X, Y: y, W: u.px(62), H: u.px(28)}, u.t("Position"), colDim, 0)
	spots := u.ts(msgs("Top", "Middle", "Bottom"))
	heights := []float64{0.12, 0.5, 0.86}
	current := -1
	for k, h := range heights {
		if math.Abs(o.Y-h) < 0.01 && math.Abs(o.X-0.5) < 0.01 {
			current = k
		}
	}
	if pick := u.chips("ov/spot", gfx.Rect{X: r.X + u.px(64), Y: y, W: r.W - u.px(64), H: u.px(28)}, spots, current, true); pick != current {
		update(func(x *project.Overlay) { x.X, x.Y = 0.5, heights[pick] })
	}
	y += u.px(40)

	if a.times.id != o.ID || u.focus != "ov/start" && u.focus != "ov/end" {
		a.times = timeFields{id: o.ID, start: clock(o.Start), end: clock(o.End)}
	}
	for k, field := range []struct {
		name, id string
		value    *string
	}{{u.t("Start"), "ov/start", &a.times.start}, {u.t("End"), "ov/end", &a.times.end}} {
		// The button is as wide as its words in every language; the time field takes the rest.
		here := u.t("Use playhead")
		hw := max(u.px(100), u.buttonWidth("", here))
		u.label(u.face(12.5), gfx.Rect{X: r.X, Y: y, W: u.px(62), H: u.px(32)}, field.name, colDim, 0)
		_, done := u.textBox(field.id, gfx.Rect{X: r.X + u.px(64), Y: y, W: r.W - u.px(72) - hw, H: u.px(32)}, field.value, "0:00.00", false)
		if done {
			if v, ok := parseClock(*field.value); ok {
				a.change(func() {
					if k == 0 {
						a.p.SetOverlayRange(o.ID, v, math.Max(o.End, v+project.MinLength))
					} else {
						a.p.SetOverlayRange(o.ID, math.Min(o.Start, v-project.MinLength), v)
					}
				})
			}
			a.times.id = -1
		}
		if u.button(field.id+"/here", gfx.Rect{X: r.Right() - hw, Y: y, W: hw, H: u.px(32)}, "", here, plain, true) {
			a.change(func() {
				if k == 0 {
					a.p.SetOverlayRange(o.ID, a.playhead, math.Max(o.End, a.playhead+project.MinLength))
				} else {
					a.p.SetOverlayRange(o.ID, math.Min(o.Start, math.Max(a.playhead-project.MinLength, 0)), math.Max(a.playhead, project.MinLength))
				}
			})
			a.times.id = -1
		}
		y += u.px(40)
	}
	del := u.t("Delete this text")
	switch {
	case o.Canvas != "":
		del = u.t("Delete this drawing")
	case o.Kind == project.ImageOverlay:
		del = u.t("Delete this image")
	}
	if u.button("ov/delete", gfx.Rect{X: r.X, Y: y, W: r.W, H: u.px(34)}, icDelete, del, danger, true) {
		a.deleteSelection()
	}
	return y + u.px(34)
}

// parseClock reads "m:ss.cc", "h:mm:ss" or plain seconds.
func parseClock(s string) (float64, bool) {
	s = strings.TrimSpace(strings.ReplaceAll(s, "：", ":"))
	if s == "" {
		return 0, false
	}
	total := 0.0
	for _, part := range strings.Split(s, ":") {
		v, err := strconv.ParseFloat(strings.TrimSpace(part), 64)
		if err != nil || v < 0 {
			return 0, false
		}
		total = total*60 + v
	}
	return total, true
}
