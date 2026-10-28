package ui

import (
	"fmt"
	"image"
	"image/color"
	"math"
	"strings"

	"github.com/btelolabs/btelo-video-cut/internal/gfx"
	"github.com/btelolabs/btelo-video-cut/internal/paint"
)

const (
	icText       = "\ue264"
	icAlignLeft  = "\ue236"
	icAlignMid   = "\ue234"
	icAlignRight = "\ue237"
	icBold       = "\ue238"
)

// textFocus is the keyboard focus while a text layer is typed on the canvas.
const textFocus = "art/text"

// textEdit is a text layer being typed with the text tool.
type textEdit struct {
	s *paint.TextSession
}

// textFace is the face a text is drawn with.
func (a *App) textFace(t paint.Text) *gfx.Face {
	f := a.u.fonts.Regular
	if t.Bold {
		f = a.u.fonts.Bold
	}
	return f.Face(float32(t.Size))
}

// lineX is where a line of t starts, by its alignment.
func (a *App) lineX(t paint.Text, f *gfx.Face, line string) float64 {
	w := float64(f.Measure(line))
	switch t.Align {
	case paint.AlignCenter:
		return t.X - w/2
	case paint.AlignRight:
		return t.X - w
	}
	return t.X
}

// renderText draws a text layer's pixels, placed on the picture where the text goes.
func (a *App) renderText(t paint.Text) *image.RGBA {
	f := a.textFace(t)
	pad := math.Ceil(t.Size / 4) // room for glyphs that reach past their advance
	var r image.Rectangle
	for i, line := range t.Lines() {
		if line == "" {
			continue
		}
		x, y := a.lineX(t, f, line), t.Y+float64(i)*t.LineHeight()
		lr := image.Rect(int(math.Floor(x-pad)), int(math.Floor(y-pad)), int(math.Ceil(x+float64(f.Measure(line))+pad)),
			int(math.Ceil(y+float64(f.Height())+pad)))
		r = r.Union(lr)
	}
	img := image.NewRGBA(r)
	if r.Empty() {
		return img
	}
	c := gfx.Wrap(img)
	col := color.RGBA{t.Color.R, t.Color.G, t.Color.B, 255}
	for i, line := range t.Lines() {
		c.Text(f, float32(a.lineX(t, f, line)), float32(t.Y+float64(i)*t.LineHeight()), line, col)
	}
	if t.Color.A < 255 {
		img = gfx.Fade(img, float64(t.Color.A)/255)
	}
	return img
}

// textSettings are the text tool's options as a text, with the words and anchor given.
func (a *App) textSettings(words string, x, y float64) paint.Text {
	p := &a.settings.Paint
	return paint.Text{Text: words, X: x, Y: y, Size: min(max(p.TextSize, 1), 1000), Bold: p.TextBold, Color: a.brush.Color,
		Align: paint.TextAlign(min(max(p.TextAlign, 0), 2))}
}

// beginText starts typing: on the text layer under the pointer, or a new one whose first line sits on the point
// clicked, as Photoshop's type tool does.
func (a *App) beginText(ad *artDoc, cx, cy float64) {
	u := &a.u
	p := &a.settings.Paint
	id := 0
	var words string
	var x, y float64
	if l := ad.d.TextAt(int(math.Floor(cx)), int(math.Floor(cy))); l != nil {
		t := *l.Text
		id, words, x, y = l.ID, t.Text, t.X, t.Y
		// The options take the layer's own, as Photoshop's do.
		p.TextSize, p.TextBold, p.TextAlign = t.Size, t.Bold, int(t.Align)
		a.setBrushColor(t.Color)
	} else {
		x, y = cx, cy-float64(a.textFace(a.textSettings("", 0, 0)).Ascent)
	}
	s, err := ad.d.BeginText(id)
	if err != nil {
		a.toast(a.describe(err), true)
		return
	}
	ad.text = &textEdit{s: s}
	t := a.textSettings(words, x, y)
	ad.touched(s.Set(t, a.renderText(t)))
	u.focus, u.focusDrawn = textFocus, true
	caret := len([]rune(words))
	if id != 0 {
		caret = a.textCaretAt(t, cx, cy) // a click on a text puts the caret where it was clicked
	}
	u.edit = editor{text: []rune(words), multiline: true, caret: caret, anchor: caret}
}

// commitText records what was typed; cancelText drops it.
func (a *App) commitText(ad *artDoc) {
	if ad.text == nil {
		return
	}
	ad.touched(ad.text.s.Commit())
	a.endText(ad)
}

func (a *App) cancelText(ad *artDoc) {
	if ad.text == nil {
		return
	}
	ad.touched(ad.text.s.Cancel())
	a.endText(ad)
}

func (a *App) endText(ad *artDoc) {
	ad.text = nil
	if a.u.focus == textFocus {
		a.u.focus = ""
	}
	ad.changedAll()
}

// textTick types into the text being edited, keeps its pixels in step with the text and the options, and keeps
// the keyboard (and the input method) on it.
func (a *App) textTick(ad *artDoc) {
	u := &a.u
	te := ad.text
	if te == nil {
		return
	}
	if u.focus != textFocus {
		a.commitText(ad) // the keyboard went elsewhere: what was typed stays
		return
	}
	u.focusDrawn = true
	keys := u.keys
	u.keys = nil
	for _, k := range keys {
		switch {
		case k.Code == keyEscape:
			a.cancelText(ad)
			return
		case k.Code == keyEnter && k.Ctrl:
			a.commitText(ad)
			return
		default:
			u.edit.key(k, u.paste)
		}
	}
	if len(u.typed) > 0 {
		u.edit.insert(string(u.typed))
		u.typed = nil
	}
	cur := te.s.Layer().Text
	want := a.textSettings(string(u.edit.text), cur.X, cur.Y)
	if want != *cur {
		ad.touched(te.s.Set(want, a.renderText(want)))
	}
}

// textCaretAt is the caret position for a picture point inside the text.
func (a *App) textCaretAt(t paint.Text, px, py float64) int {
	f := a.textFace(t)
	lines := t.Lines()
	i := min(max(int(math.Floor((py-t.Y)/t.LineHeight())), 0), len(lines)-1)
	index := 0
	for _, l := range lines[:i] {
		index += len([]rune(l)) + 1
	}
	x := a.lineX(t, f, lines[i])
	rs := []rune(lines[i])
	col := 0
	for col < len(rs) {
		w := float64(f.Measure(string(rs[col : col+1])))
		if px < x+w/2 {
			break
		}
		x += w
		col++
	}
	return index + col
}

// textBounds is the box a text takes on the picture, at least a caret's width.
func (a *App) textBounds(t paint.Text) (x0, y0, x1, y1 float64) {
	f := a.textFace(t)
	x0, x1 = math.Inf(1), math.Inf(-1)
	for _, line := range t.Lines() {
		x := a.lineX(t, f, line)
		x0, x1 = min(x0, x), max(x1, x+float64(f.Measure(line)))
	}
	y0, y1 = t.Y, t.Y+float64(len(t.Lines())-1)*t.LineHeight()+float64(f.Height())
	return x0, y0, max(x1, x0+1), y1
}

// textPointer handles the text tool on the canvas: a press in the text being typed moves the caret (a drag
// selects), elsewhere it ends the typing, or starts typing where nothing is being typed.
func (a *App) textPointer(ad *artDoc, vp gfx.Rect) {
	u := &a.u
	const id = "art/view"
	cx, cy := ad.cursor[0], ad.cursor[1]
	inside := func() bool {
		if ad.text == nil {
			return false
		}
		x0, y0, x1, y1 := a.textBounds(*ad.text.s.Layer().Text)
		return cx >= x0-2 && cx <= x1+2 && cy >= y0-2 && cy <= y1+2
	}
	if u.hover(id, vp) {
		u.cursor = CursorText
	}
	if u.press(id, vp) {
		switch {
		case inside():
			u.edit.caret = a.textCaretAt(*ad.text.s.Layer().Text, cx, cy)
			if !u.shift {
				u.edit.anchor = u.edit.caret
			}
			u.focus, u.focusDrawn = textFocus, true
		case ad.text != nil:
			a.commitText(ad)
			u.active = ""
		default:
			a.beginText(ad, cx, cy)
			u.active = ""
		}
	}
	if u.dragging(id) && ad.text != nil {
		u.edit.caret = a.textCaretAt(*ad.text.s.Layer().Text, cx, cy)
	}
	u.letGo(id)
}

// drawTextOverlay outlines the text being typed and shows its caret and selection.
func (a *App) drawTextOverlay(ad *artDoc, vp gfx.Rect) {
	u := &a.u
	if ad.text == nil {
		return
	}
	t := *ad.text.s.Layer().Text
	f := a.textFace(t)
	x0, y0, x1, y1 := a.textBounds(t)
	sx0, sy0 := ad.toScreen(vp, [2]float64{x0 - 3, y0 - 3})
	sx1, sy1 := ad.toScreen(vp, [2]float64{x1 + 3, y1 + 3})
	drawAnts(u, gfx.Rect{X: sx0, Y: sy0, W: sx1 - sx0, H: sy1 - sy0})
	// The selection, line by line, then the caret.
	lines := t.Lines()
	a0, b0 := u.edit.selection()
	start := 0
	for i, line := range lines {
		rs := []rune(line)
		lx, ly := a.lineX(t, f, line), t.Y+float64(i)*t.LineHeight()
		sa, sb := min(max(a0-start, 0), len(rs)), min(max(b0-start, 0), len(rs))
		if sa < sb {
			p0x, p0y := ad.toScreen(vp, [2]float64{lx + float64(f.Measure(string(rs[:sa]))), ly})
			p1x, p1y := ad.toScreen(vp, [2]float64{lx + float64(f.Measure(string(rs[:sb]))), ly + float64(f.Height())})
			u.c.Fill(gfx.Rect{X: p0x, Y: p0y, W: p1x - p0x, H: p1y - p0y}, gfx.Alpha(colAccent, 0.4))
		}
		if c := u.edit.caret - start; c >= 0 && c <= len(rs) {
			cx, cy := ad.toScreen(vp, [2]float64{lx + float64(f.Measure(string(rs[:c]))), ly})
			h := float32(float64(f.Height()) * ad.zoom)
			u.c.Fill(gfx.Rect{X: cx - 1, Y: cy, W: max(1, u.px(1.5)) + 1, H: h}, gfx.Alpha(colBlack, 0.5))
			u.c.Fill(gfx.Rect{X: cx, Y: cy, W: max(1, u.px(1.5)), H: h}, colWhite)
			u.imeAt = [3]float32{cx, cy, h}
		}
		start += len(rs) + 1
	}
}

// drawTextOptions is the text tool's options bar: the size, bold, the alignment, and cancel and commit while
// typing.
func (a *App) drawTextOptions(ad *artDoc, r gfx.Rect) {
	u := &a.u
	p := &a.settings.Paint
	x, done := a.labelledSlider("art/text/size", r.X, r.Y, r.H, u.t("Font size"), &p.TextSize, 4, 400, func(v float64) string { return fmt.Sprintf("%.0f px", v) })
	p.TextSize = math.Round(p.TextSize)
	if u.iconButton("art/text/bold", gfx.Rect{X: x, Y: r.Y, W: u.px(30), H: r.H}, icBold, u.t("Bold"), p.TextBold, true) {
		p.TextBold, done = !p.TextBold, true
	}
	x += u.px(42)
	for i, c := range []struct{ glyph, hint string }{
		{icAlignLeft, u.t("Align left")}, {icAlignMid, u.t("Centre")}, {icAlignRight, u.t("Align right")},
	} {
		if u.iconButton(fmt.Sprintf("art/text/align/%d", i), gfx.Rect{X: x, Y: r.Y, W: u.px(30), H: r.H}, c.glyph, c.hint, p.TextAlign == i, true) {
			p.TextAlign, done = i, true
		}
		x += u.px(32)
	}
	x += u.px(10)
	u.label(u.face(12), gfx.Rect{X: x, Y: r.Y, W: r.Right() - x - u.px(80), H: r.H},
		u.t("Click to type, or click a text layer to edit it; Ctrl+Enter commits, Esc cancels"), colDim, 0)
	if done {
		a.saveSettings()
	}
	if ad.text == nil {
		return
	}
	if u.iconButton("art/text/cancel", gfx.Rect{X: r.Right() - u.px(66), Y: r.Y, W: u.px(30), H: r.H}, icCancel, withKey(u.t("Cancel the text"), "Esc"), false, true) {
		a.cancelText(ad)
		return
	}
	if u.iconButton("art/text/commit", gfx.Rect{X: r.Right() - u.px(32), Y: r.Y, W: u.px(30), H: r.H}, icCheck, withKey(u.t("Commit the text"), "Ctrl+Enter"), true, true) {
		a.commitText(ad)
	}
}

// layerLabel is how the layers panel names a layer: a text layer still being named shows what it says.
func layerLabel(n *paint.Node) string {
	if n.Name == "" && n.Text != nil {
		first, _, _ := strings.Cut(n.Text.Text, "\n")
		return first
	}
	return n.Name
}
