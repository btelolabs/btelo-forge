package ui

import (
	"image"
	"image/color"
	"sync"

	"github.com/btelolabs/btelo-forge/internal/gfx"
	"github.com/btelolabs/btelo-forge/internal/paint"
)

// Brush presets, as Photoshop's brush preset picker: the first button of a brush's options bar opens a panel of the
// ready-made brushes (paint.Presets), each shown by a stroke it paints. Choosing one takes its tip, size, spacing,
// flow and shape dynamics, keeping the colour; the tip and its dynamics can then be changed as any brush's.

// brushPresetWords are the presets' names, for translation.
var brushPresetWords = msgs("Pencil", "Charcoal", "Soft pastel", "Ink pen", "Ink brush", "Flat bristle brush",
	"Round bristle brush", "Dry fan brush", "Sponge", "Spatter", "Grunge", "Oval pen")

// choosePreset makes the brush the preset's, in the brush's colour, and in its size too while every brush has the same
// size; otherwise in the size last used with the preset, at first its own.
func (a *App) choosePreset(p paint.Preset) {
	b := p.Brush
	b.Color = a.brush.Color
	size := a.brush.Size
	a.brush = b
	a.settings.Paint.ShapeTip = ""
	a.chosenBrush(size)
	a.saveBrush()
}

// brushKey names the brush for the sizes kept for each: the preset whose tip it has, the picture tip, or the plain
// round and square tips, which are one brush.
func (a *App) brushKey() string {
	if p, ok := a.chosenPreset(); ok {
		return "preset:" + p.ID
	}
	if a.brush.Tip == paint.TipShaped && a.settings.Paint.ShapeTip != "" {
		return "tip:" + a.settings.Paint.ShapeTip
	}
	return ""
}

// chosenBrush sizes a brush just chosen, which was size before: the same while every brush has the same size, else
// the size kept for it, when there is one.
func (a *App) chosenBrush(size float64) {
	p := &a.settings.Paint
	if p.SameSize {
		a.brush.Size = size
	} else if s, ok := p.Sizes[a.brushKey()]; ok && s > 0 {
		a.brush.Size = s
	}
}

// drawSameSize is the options bar's switch giving every brush the same size, at x; it returns where it ends.
func (a *App) drawSameSize(x float32, r *gfx.Rect) float32 {
	u := &a.u
	p := &a.settings.Paint
	label := u.t("Same size for all brushes")
	a.optRow(&x, r, checkboxWidth(u, label))
	box := gfx.Rect{X: x, Y: r.Y, W: checkboxWidth(u, label), H: r.H}
	if on := u.checkbox("art/samesize", box, label, p.SameSize); on != p.SameSize {
		p.SameSize = on
		a.saveBrush() // turned off, the brush keeps its size as its own
	}
	u.hint("art/samesize", u.t("Choosing another preset or picture tip keeps the brush's size; off, each brush keeps its own size"), box)
	return box.Right()
}

// brushPresetIDs are the presets' ids, as the MCP tools name them.
func brushPresetIDs() []string {
	ps := paint.Presets()
	ids := make([]string, len(ps))
	for i, p := range ps {
		ids[i] = p.ID
	}
	return ids
}

// chosenPreset is the preset whose tip the brush has.
func (a *App) chosenPreset() (paint.Preset, bool) {
	if a.brush.Tip != paint.TipShaped {
		return paint.Preset{}, false
	}
	return paint.PresetOf(a.brush.Shape)
}

// drawPresetButton is the button opening the presets panel, at r: the chosen preset's tip, or a brush.
func (a *App) drawPresetButton(r gfx.Rect) {
	u := &a.u
	const id = "art/presets"
	open := a.menuOpen(id)
	bg := colSurface
	switch {
	case open:
		bg = colSelected
	case u.hover(id, r):
		bg = colSurfaceHi
	}
	u.c.RoundRect(r, u.px(6), bg)
	glyph := gfx.Rect{X: r.X, Y: r.Y, W: r.H, H: r.H}
	if p, ok := a.chosenPreset(); ok {
		drawTipShape(u, p.Brush.Shape, glyph.Inset(u.px(5)))
	} else {
		u.icon(icBrush, glyph, 17, colText)
	}
	u.icon("", gfx.Rect{X: glyph.Right() - u.px(4), Y: r.Y, W: r.W - glyph.W + u.px(4), H: r.H}, 16, colDim)
	u.hint(id, u.t("Brush presets"), r)
	if u.clicked(id, r) {
		if open {
			a.menu = nil
		} else {
			a.openMenu(id, r, nil) // drawn by drawPresets: a panel rather than a list
		}
	}
}

// drawPresets is the presets panel opened from the options bar: each preset's stroke and name; a click chooses one.
func (a *App) drawPresets() {
	m := a.menu
	if m == nil || m.id != "art/presets" {
		return
	}
	u := &a.u
	ps := paint.Presets()
	box, cells := presetsLayout(u, m.anchor, len(ps))
	pad := u.px(12)
	u.c.Fill(gfx.Rect{X: box.X, Y: box.Y + u.px(3), W: box.W, H: box.H}, gfx.Alpha(colBlack, 0.35))
	u.c.Fill(box, colSurface)
	u.c.Outline(box, max(1, u.px(1)), colBorder)
	u.heading(gfx.Rect{X: box.X + pad, Y: box.Y + pad, W: box.W - 2*pad, H: u.px(20)}, u.t("Brush presets"))
	chosen, _ := a.chosenPreset()
	for i, p := range ps {
		cell := cells[i]
		id := "art/preset/" + p.ID
		bg := colBackground
		switch {
		case p.ID == chosen.ID:
			bg = colSelected
		case u.hover(id, cell):
			bg = colSurfaceHi
		}
		u.c.RoundRect(cell, u.px(6), bg)
		sr := gfx.Rect{X: cell.X + u.px(6), Y: cell.Y + u.px(4), W: cell.W - u.px(12), H: cell.H - u.px(24)}
		drawCoverage(u, presetSample(p, int(sr.W), int(sr.H), u.s), sr)
		u.label(u.face(11.5), gfx.Rect{X: cell.X + u.px(4), Y: cell.Bottom() - u.px(20), W: cell.W - u.px(8), H: u.px(18)},
			u.t(p.Name), colText, 0.5)
		if u.clicked(id, cell) {
			a.choosePreset(p)
			a.menu = nil
			return
		}
	}
	// Clicks inside the panel stay with it.
	if u.pressed && !box.Contains(u.pressX, u.pressY) && !m.anchor.Contains(u.pressX, u.pressY) && m.opened {
		a.menu = nil
		u.pressed = false
	}
	m.opened = true
}

// presetsLayout places the presets panel under the button at anchor, within the window, and its n cells in rows of
// three under its heading.
func presetsLayout(u *ctx, anchor gfx.Rect, n int) (gfx.Rect, []gfx.Rect) {
	const cols = 3
	rows := (n + cols - 1) / cols
	cw, ch, gap, pad, head := u.px(150), u.px(70), u.px(6), u.px(12), u.px(28)
	box := gfx.Rect{X: anchor.X, Y: anchor.Bottom() + u.px(4),
		W: 2*pad + cols*cw + (cols-1)*gap, H: 2*pad + head + float32(rows)*ch + float32(rows-1)*gap}
	box.X = max(min(box.X, float32(u.c.Img.Rect.Max.X)-box.W-u.px(4)), 0)
	cells := make([]gfx.Rect, n)
	for i := range cells {
		cells[i] = gfx.Rect{X: box.X + pad + float32(i%cols)*(cw+gap), Y: box.Y + pad + head + float32(i/cols)*(ch+gap), W: cw, H: ch}
	}
	return box, cells
}

// presetSamples are the presets' strokes as the panel shows them, painted once for each size.
var presetSamples = struct {
	sync.Mutex
	m map[presetSampleKey]*image.Alpha
}{m: map[presetSampleKey]*image.Alpha{}}

type presetSampleKey struct {
	id    string
	w, h  int
	scale float32
}

// presetSample is a stroke of the preset in a w×h picture, at its own size scaled to the screen but no taller than
// half the picture, so a fine pencil shows as a fine line and a sponge's texture can be seen.
func presetSample(p paint.Preset, w, h int, scale float32) *image.Alpha {
	k := presetSampleKey{p.ID, w, h, scale}
	presetSamples.Lock()
	defer presetSamples.Unlock()
	if s, ok := presetSamples.m[k]; ok {
		return s
	}
	b := p.Brush
	b.Size = min(max(b.Size*float64(scale), 3), float64(h)/2)
	s := paint.Sample(b, w, h)
	presetSamples.m[k] = s
	return s
}

// drawCoverage draws cover at r's corner, light where it covers.
func drawCoverage(u *ctx, cover *image.Alpha, r gfx.Rect) {
	img := u.c.Img
	ox, oy := int(r.X), int(r.Y)
	for y := range cover.Rect.Dy() {
		for x := range cover.Rect.Dx() {
			k := float32(cover.Pix[y*cover.Stride+x]) / 255
			if k == 0 || !(image.Point{ox + x, oy + y}).In(img.Rect) {
				continue
			}
			old := img.RGBAAt(ox+x, oy+y)
			mix := func(o uint8) uint8 { return uint8(float32(o)*(1-k) + 235*k) }
			img.SetRGBA(ox+x, oy+y, color.RGBA{mix(old.R), mix(old.G), mix(old.B), 255})
		}
	}
}
