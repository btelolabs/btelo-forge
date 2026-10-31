package ui

import (
	"fmt"
	"image"
	"math"

	"github.com/btelolabs/btelo-forge/internal/gfx"
	"github.com/btelolabs/btelo-forge/internal/paint"
)

// The vector brush draws with a brush's tip as vector shapes: the area its dabs cover along the line drawn
// (paint.BrushSweep) is a shape added to the chosen vector layer (or to a new one, when none is chosen), filled with
// the foreground colour, so a drawing of many strokes is one layer that stays sharp at any size. Its tip, size, angle
// and roundness are its own, round by default; D and F make it smaller and larger.

const icVectorBrush = ""

// vectorStroke is the vector brush's stroke under way: its layer's object before it, the area swept so far, and the
// canvas area the stroke last showed over.
type vectorStroke struct {
	s     *paint.VectorSession
	base  paint.Vector
	sweep *paint.BrushSweep
	shown image.Rectangle
}

// defaultVectorBrush is the vector brush as it starts: a round tip 6 pixels across.
func defaultVectorBrush() paint.Brush {
	return paint.Brush{Size: 6, Hardness: 1, Opacity: 1, Spacing: 0.1, Flow: 1, Round: 1, Tip: paint.TipRound}
}

// vectorBrush is the vector brush's tip as its settings keep it.
func (a *App) vectorBrush() paint.Brush {
	p := &a.settings.Paint
	b := defaultVectorBrush()
	if p.VBrushSize > 0 {
		b.Size = p.VBrushSize
	}
	b.Tip, b.Angle = paint.Tip(p.VBrushTip), p.VBrushAngle
	if p.VBrushRound > 0 {
		b.Round = p.VBrushRound
	}
	if b.Tip == paint.TipShaped {
		if b.Shape = a.tipShape(p.VBrushShape); b.Shape == nil {
			b.Tip = paint.TipRound // its picture is gone
		}
	}
	return b
}

// setVectorBrushSize sizes the vector brush, as its slider and D and F do.
func (a *App) setVectorBrushSize(size float64) {
	a.settings.Paint.VBrushSize = min(max(math.Round(size*10)/10, 0.5), 500)
	a.saveSettings()
}

// vectorBrushPointer is the vector brush on the canvas.
func (a *App) vectorBrushPointer(ad *artDoc, vp gfx.Rect) {
	u := &a.u
	const id = "art/view"
	cx, cy := ad.toCanvas(vp, u.mx, u.my)
	p := paint.Pt{X: cx, Y: cy}
	if u.hover(id, vp) {
		u.cursor = CursorCross
	}
	if u.press(id, vp) {
		a.beginVectorStroke(ad, p)
	}
	vs := ad.vstroke
	if vs == nil {
		return
	}
	if u.dragging(id) {
		// A point every couple of screen pixels: the hand's tremor finer than that is not drawn.
		if vs.sweep.Last().Dist(p)*ad.zoom >= float64(u.px(2)) {
			vs.sweep.To(p)
			a.showVectorStroke(ad, false)
		}
	}
	if u.letGo(id) {
		vs.sweep.End(p)
		a.showVectorStroke(ad, true)
		vs.s.End()
		ad.vstroke = nil
		ad.changedAll()
	}
}

// beginVectorStroke starts a stroke at p: on the chosen vector layer, or on a new one filled with the foreground
// colour.
func (a *App) beginVectorStroke(ad *artDoc, p paint.Pt) {
	u := &a.u
	vs := &vectorStroke{sweep: paint.NewBrushSweep(a.vectorBrush(), p)}
	var err error
	if n := ad.paintedVector(); n != nil {
		vs.base = *n.Vector
		vs.s, err = ad.d.BeginVector(n.ID, paint.Vector{}, "", "Vector brush")
	} else {
		vs.base = paint.Vector{Kind: paint.VectorCurve, M: paint.Identity, Fill: solidPaint(a.shapeColours()[0])}
		dot := vs.base.WithShape(vs.shape(false), paint.OpAdd)
		vs.s, err = ad.d.BeginVector(0, dot, a.vectorLayerName(), "Vector brush")
	}
	if err != nil {
		a.artFailed(ad, err)
		u.active = ""
		return
	}
	ad.vstroke = vs
	a.showVectorStroke(ad, false)
}

// shape is the stroke so far as a shape: as polygons while it is drawn, fitted with curves when it ends. A polygon
// may cross itself, which only a layer filled by its winding shows whole.
func (vs *vectorStroke) shape(fitted bool) paint.Vector {
	return paint.Vector{Kind: paint.VectorCurve, M: paint.Identity, Path: vs.sweep.Outline(fitted || vs.base.Rule == paint.EvenOdd)}
}

// showVectorStroke shows the stroke on its layer, redrawing only where it was and is.
func (a *App) showVectorStroke(ad *artDoc, fitted bool) {
	vs := ad.vstroke
	shape := vs.shape(fitted)
	vs.s.Set(vs.base.WithShape(shape, paint.OpAdd))
	r := shape.PixelBounds()
	ad.touched(r.Union(vs.shown))
	vs.shown = r
}

// drawVectorBrushOptions is the vector brush's options: its tip, the tips made from pictures, its size, angle and
// roundness, and what it does.
func (a *App) drawVectorBrushOptions(r gfx.Rect) {
	u := &a.u
	p := &a.settings.Paint
	b := a.vectorBrush()
	x := r.X
	choose := func(tip paint.Tip, shape string) {
		p.VBrushTip, p.VBrushShape = int(tip), shape
		a.saveSettings()
	}
	for _, tip := range paint.Tips {
		tr := gfx.Rect{X: x, Y: r.Y, W: u.px(30), H: r.H}
		id := fmt.Sprintf("art/vbrush/tip/%d", tip)
		bg := colSurface
		switch {
		case b.Tip == tip:
			bg = colSelected
		case u.hover(id, tr):
			bg = colSurfaceHi
		}
		u.c.RoundRect(tr, u.px(6), bg)
		drawTip(u, tip, tr, colText)
		u.hint(id, u.t(tipNames[tip]), tr)
		if u.clicked(id, tr) {
			choose(tip, "")
		}
		x += tr.W + u.px(4)
	}
	for _, name := range p.Tips {
		tip := a.tipShape(name)
		if tip == nil {
			continue
		}
		a.optRow(&x, &r, u.px(30))
		tr := gfx.Rect{X: x, Y: r.Y, W: u.px(30), H: r.H}
		if tr.Right() > r.Right()-u.px(400) && !a.over.flow.on {
			break
		}
		id := "art/vbrush/shape/" + name
		bg := colSurface
		switch {
		case b.Tip == paint.TipShaped && p.VBrushShape == name:
			bg = colSelected
		case u.hover(id, tr):
			bg = colSurfaceHi
		}
		u.c.RoundRect(tr, u.px(6), bg)
		drawTipShape(u, tip, tr.Inset(u.px(4)))
		u.hint(id, u.t("Brush tip made from a picture"), tr)
		if u.clicked(id, tr) {
			choose(paint.TipShaped, name)
		}
		x += tr.W + u.px(4)
	}
	x += u.px(10)
	size, angle, round := b.Size, b.Angle, b.Round
	saved := false
	var done bool
	a.optRow(&x, &r, u.px(190))
	x, done = a.labelledSlider("art/vbrush/size", x, r.Y, r.H, u.t("Brush size"), &size, 0.5, 200, func(v float64) string { return fmt.Sprintf("%.1f px", v) })
	saved = saved || done
	if size != b.Size {
		p.VBrushSize = math.Round(size*10) / 10
	}
	a.optRow(&x, &r, u.px(190))
	if x+u.px(190) < r.Right() {
		x, done = a.labelledSlider("art/vbrush/angle", x, r.Y, r.H, u.t("Angle"), &angle, -180, 180, func(v float64) string { return fmt.Sprintf("%.0f°", v) })
		saved = saved || done
		p.VBrushAngle = math.Round(angle)
	}
	a.optRow(&x, &r, u.px(190))
	if x+u.px(190) < r.Right() {
		x, done = a.labelledSlider("art/vbrush/round", x, r.Y, r.H, u.t("Roundness"), &round, 0.05, 1, percent)
		saved = saved || done
		p.VBrushRound = round
	}
	if saved {
		a.saveSettings()
	}
	if x+u.px(200) < r.Right() {
		a.optHint(x, r, u.t("Draw: each stroke is a vector shape added to the chosen vector layer; D and F size the brush"))
	}
}
