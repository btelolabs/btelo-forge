package ui

import (
	"image/color"

	"github.com/btelolabs/btelo-forge/internal/gfx"
	"github.com/btelolabs/btelo-forge/internal/paint"
)

// While a vector layer is chosen, the foreground and background colours are its fill and its stroke, as Affinity
// Designer's colour panel is: the colour panel's swatches show them, the colour panel and the eyedropper change the
// one being edited (the fill, or the stroke while the background swatch is chosen), X switches between them and
// Shift+X swaps them. A gradient's colour is that of its chosen stop.

// paintedVector is the chosen vector layer whose fill and stroke the colours edit, or nil.
func (ad *artDoc) paintedVector() *paint.Node {
	n := ad.d.Node(ad.d.Active)
	if n == nil || n.Vector == nil || ad.d.EditMask {
		return nil
	}
	return n
}

// paintedVector is the canvas's chosen vector layer, when a canvas is open.
func (a *App) paintedVector() (*artDoc, *paint.Node) {
	if a.art == nil {
		return nil, nil
	}
	return a.art, a.art.paintedVector()
}

// targetPaint is the fill, or with stroke the stroke's paint, of v.
func targetPaint(v *paint.Vector, stroke bool) *paint.VectorPaint {
	if stroke {
		return &v.Stroke.Paint
	}
	return &v.Fill
}

// stopOf is the stop of a gradient the colours edit: the chosen one, kept within the stops.
func (ad *artDoc) stopOf(p paint.VectorPaint) int {
	return min(max(ad.gradStop, 0), max(len(p.Stops)-1, 0))
}

// paintColour is the colour a paint shows in the colour panel: its own, or its chosen stop's.
func (ad *artDoc) paintColour(p paint.VectorPaint) color.NRGBA {
	if p.Gradient() && len(p.Stops) > 0 {
		return p.Stops[ad.stopOf(p)].Color
	}
	if p.Kind == paint.PaintNone {
		return color.NRGBA{R: 255, G: 255, B: 255, A: 255}
	}
	return p.Color
}

// withColour is p coloured c: a gradient's chosen stop, or else a solid colour, as choosing a colour for no paint
// turns it on.
func (ad *artDoc) withColour(p paint.VectorPaint, c color.NRGBA) paint.VectorPaint {
	if p.Gradient() && len(p.Stops) > 0 {
		p.Stops = append([]paint.Stop(nil), p.Stops...)
		p.Stops[ad.stopOf(p)].Color = c
		return p
	}
	return solidPaint(c)
}

// vectorColour is the colour of the chosen vector layer's fill, or with stroke its stroke.
func (ad *artDoc) vectorColour(n *paint.Node, stroke bool) color.NRGBA {
	return ad.paintColour(*targetPaint(n.Vector, stroke))
}

// setVectorColour colours the chosen vector layer's fill, or with stroke its stroke, shown at once and recorded as
// one step when the mouse is let go (see endLiveVector).
func (a *App) setVectorColour(ad *artDoc, n *paint.Node, stroke bool, c color.NRGBA) {
	next := *n.Vector
	p := targetPaint(&next, stroke)
	*p = ad.withColour(*p, c)
	if stroke && next.Stroke.Width <= 0 {
		next.Stroke.Width = max(a.settings.Paint.ShapeWidth, 1)
	}
	name := "Fill"
	if stroke {
		name = "Stroke"
	}
	a.liveVector(ad, n, next, name)
}

// liveVector shows next as the chosen vector layer's object at once; the change is recorded as one step called name
// when the mouse is let go, so a drag through colours is one step.
func (a *App) liveVector(ad *artDoc, n *paint.Node, next paint.Vector, name string) {
	if ad.props == nil {
		s, err := ad.d.BeginVector(n.ID, paint.Vector{}, "", name)
		if err != nil {
			a.artFailed(ad, err)
			return
		}
		ad.props = s
	}
	ad.touched(ad.props.Set(next))
}

// endLiveVector records a live change to a vector layer once the mouse is let go.
func (a *App) endLiveVector(ad *artDoc) {
	if ad.props != nil && !a.u.down {
		ad.props.End()
		ad.props = nil
		ad.changedAll()
	}
}

// swapFillAndStroke exchanges the chosen vector layer's fill and stroke paints, as Shift+X does.
func (a *App) swapFillAndStroke(ad *artDoc, n *paint.Node) {
	next := *n.Vector
	next.Fill, next.Stroke.Paint = next.Stroke.Paint, next.Fill
	if next.Stroke.Paint.Kind != paint.PaintNone && next.Stroke.Width <= 0 {
		next.Stroke.Width = max(a.settings.Paint.ShapeWidth, 1)
	}
	a.artRun(ad, func() error { ad.d.SetVector(n.ID, next, "Swap fill and stroke"); return nil })
}

// colourSwatch is one of the colour panel's two overlapping colours: the background's (or stroke's) or the
// foreground's (or fill's).
type colourSwatch struct {
	id     string
	r      gfx.Rect
	target bool // it is the background, or the stroke
	hint   string
	draw   func(r gfx.Rect)
}

// drawSwatchPair draws the colour panel's two colours, the one the colours edit in front; a click takes the one in
// front where they overlap.
func (a *App) drawSwatchPair(pair []colourSwatch) {
	u := &a.u
	if pair[0].target == a.pickBackground {
		pair[0], pair[1] = pair[1], pair[0] // the chosen one last: on top
	}
	for _, s := range pair {
		s.draw(s.r)
	}
	for _, s := range []colourSwatch{pair[1], pair[0]} {
		u.hint(s.id, s.hint, s.r)
		if u.clicked(s.id, s.r) {
			a.pickBackground = s.target
			a.picker.hex = ""
		}
	}
}

// drawColourSwatch draws a colour as a swatch, over a checkerboard where it is transparent.
func drawColourSwatch(u *ctx, r gfx.Rect, c color.NRGBA) {
	u.c.Fill(r.Inset(-max(1, u.px(1))), colWhite)
	drawTransparency(u, r)
	u.c.Fill(r, color.RGBA{c.R, c.G, c.B, c.A})
}

// drawTransparency draws a small checkerboard, which a colour with transparency shows over.
func drawTransparency(u *ctx, r gfx.Rect) {
	u.c.Fill(r, color.RGBA{255, 255, 255, 255})
	cell := max(u.px(4), 2)
	for j, y := 0, r.Y; y < r.Bottom(); j, y = j+1, y+cell {
		for i, x := 0, r.X; x < r.Right(); i, x = i+1, x+cell {
			if (i+j)%2 == 1 {
				u.c.Fill(gfx.Rect{X: x, Y: y, W: min(cell, r.Right()-x), H: min(cell, r.Bottom()-y)}, color.RGBA{200, 200, 200, 255})
			}
		}
	}
}

// drawPaintSwatch draws a paint as a swatch: its colour, a gradient's band, or white struck through for none; a
// stroke's swatch is a frame, its middle the panel's.
func drawPaintSwatch(u *ctx, r gfx.Rect, p paint.VectorPaint, stroke bool) {
	u.c.Fill(r.Inset(-max(1, u.px(1))), colWhite)
	switch {
	case p.Kind == paint.PaintNone:
		u.c.Fill(r, colWhite)
	case p.Gradient() && len(p.Stops) > 0:
		// The stops, left to right, a column at a time.
		steps := max(int(r.W), 1)
		for i := range steps {
			t := (float64(i) + 0.5) / float64(steps)
			c := paint.VectorPaint{Kind: paint.PaintLinear, Stops: p.Stops, To: paint.Pt{X: 1}}.ColorAt(paint.Pt{X: t})
			k := float32(c.A) / 255
			col := color.RGBA{uint8(float32(c.R)*k + 255*(1-k)), uint8(float32(c.G)*k + 255*(1-k)), uint8(float32(c.B)*k + 255*(1-k)), 255}
			u.c.Fill(gfx.Rect{X: r.X + float32(i)*r.W/float32(steps), Y: r.Y, W: r.W/float32(steps) + 1, H: r.H}, col)
		}
	default:
		u.c.Fill(r, color.RGBA{p.Color.R, p.Color.G, p.Color.B, 255})
	}
	if stroke {
		u.c.Fill(r.Inset(r.W*0.3), colBackground)
	}
	if p.Kind == paint.PaintNone {
		u.c.Line(r.X, r.Bottom(), r.Right(), r.Y, max(1, u.px(1.5)), colDanger)
	}
}

// drawColourSwatches draws the foreground colour over the background one in r, overlapping as in Photoshop, with the
// swap button at the top right; while a vector layer is chosen they are its fill over its stroke. A click on either
// chooses the one the colour panel edits.
func (a *App) drawColourSwatches(r gfx.Rect) {
	u := &a.u
	side := u.px(26)
	fg := gfx.Rect{X: r.X + u.px(1), Y: r.Y + u.px(1), W: side, H: side}
	bg := gfx.Rect{X: r.X + u.px(16), Y: r.Y + u.px(13), W: side, H: side}
	swap := gfx.Rect{X: r.Right() - u.px(15), Y: r.Y - u.px(2), W: u.px(15), H: u.px(15)}
	if ad, n := a.paintedVector(); n != nil {
		v := n.Vector
		a.drawSwatchPair([]colourSwatch{
			{"art/stroke", bg, true, u.t("Stroke: click to colour the stroke (X switches)"), func(r gfx.Rect) { drawPaintSwatch(u, r, v.Stroke.Paint, true) }},
			{"art/fill", fg, false, u.t("Fill: click to colour the fill (X switches)"), func(r gfx.Rect) { drawPaintSwatch(u, r, v.Fill, false) }},
		})
		if u.iconButton("art/swap", swap, icSwap, withKey(u.t("Swap the fill and the stroke"), "Shift+X"), false, true) {
			a.swapFillAndStroke(ad, n)
		}
		return
	}
	a.drawSwatchPair([]colourSwatch{
		{"art/bg", bg, true, u.t("Background colour: click to choose it"), func(r gfx.Rect) { drawColourSwatch(u, r, a.backgroundColor()) }},
		{"art/fg", fg, false, u.t("Foreground colour: click to choose it"), func(r gfx.Rect) { drawColourSwatch(u, r, a.brush.Color) }},
	})
	if u.iconButton("art/swap", swap, icSwap, withKey(u.t("Swap the colours"), "X"), false, true) {
		a.swapColors()
	}
}

// shapeColours are the foreground and background colours a new object takes: the brush's, or while a vector layer
// is chosen the fill and stroke colours the swatches show, so a new shape looks like the one chosen; a paint that
// is off gives the brush's colour in its place.
func (a *App) shapeColours() [2]color.NRGBA {
	out := [2]color.NRGBA{a.brush.Color, a.backgroundColor()}
	if ad, n := a.paintedVector(); n != nil {
		for i, p := range []paint.VectorPaint{n.Vector.Fill, n.Vector.Stroke.Paint} {
			if p.Kind != paint.PaintNone {
				out[i] = ad.paintColour(p)
				out[i].A = 255
			}
		}
	}
	return out
}
