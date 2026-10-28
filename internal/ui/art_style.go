package ui

import (
	"fmt"
	"image/color"
	"math"
	"reflect"

	"github.com/btelolabs/btelo-forge/internal/gfx"
	"github.com/btelolabs/btelo-forge/internal/paint"
)

// The Layer Style dialog, as Photoshop's: the effects listed on the left, each turned on with its box, and the
// chosen one's settings on the right. The picture shows the effects as they change; OK keeps them as one step.

const icEffects = "\ue3e4"

// The effects, in the dialog's order.
const (
	effectDropShadow = iota
	effectInnerShadow
	effectOuterGlow
	effectInnerGlow
	effectStroke
	effectOverlay
	effectCount
)

var effectNames = msgs("Drop shadow", "Inner shadow", "Outer glow", "Inner glow", "Stroke", "Color overlay")

// styleDialog is a layer's effects being edited.
type styleDialog struct {
	ad    *artDoc
	s     *paint.StyleSession
	st    paint.LayerStyle // as edited
	shown paint.LayerStyle // as the picture shows it
	page  int
	// Each effect's settings while it is off, so turning it on again brings them back.
	shadows [2]paint.Shadow
	glows   [2]paint.Glow
	stroke  paint.Outline
	overlay paint.Overlay
}

// cloneStyle copies a style and the effects it points to.
func cloneStyle(s paint.LayerStyle) paint.LayerStyle {
	c := s
	if s.DropShadow != nil {
		v := *s.DropShadow
		c.DropShadow = &v
	}
	if s.InnerShadow != nil {
		v := *s.InnerShadow
		c.InnerShadow = &v
	}
	if s.OuterGlow != nil {
		v := *s.OuterGlow
		c.OuterGlow = &v
	}
	if s.InnerGlow != nil {
		v := *s.InnerGlow
		c.InnerGlow = &v
	}
	if s.Stroke != nil {
		v := *s.Stroke
		c.Stroke = &v
	}
	if s.ColorOverlay != nil {
		v := *s.ColorOverlay
		c.ColorOverlay = &v
	}
	return c
}

// openStyle opens the Layer Style dialog for the chosen layer.
func (a *App) openStyle(ad *artDoc) {
	if ad.stroke != nil || ad.move != nil {
		return
	}
	a.applyTransform(ad)
	a.commitText(ad)
	a.finishPen(ad)
	s, err := ad.d.BeginStyle(ad.d.Active)
	if err != nil {
		a.toast(a.describe(err), true)
		return
	}
	f := &styleDialog{ad: ad, s: s, st: cloneStyle(s.Style()),
		shadows: [2]paint.Shadow{paint.DefaultDropShadow(), paint.DefaultInnerShadow()},
		glows:   [2]paint.Glow{paint.DefaultOuterGlow(), paint.DefaultInnerGlow()},
		stroke:  paint.DefaultOutline(), overlay: paint.DefaultOverlay()}
	f.shown = cloneStyle(f.st)
	// The first effect that is on, or the drop shadow.
	for i := range effectCount {
		if f.on(i) {
			f.page = i
			break
		}
	}
	a.style = f
}

// on reports whether effect i is on.
func (f *styleDialog) on(i int) bool {
	st := &f.st
	return [effectCount]bool{st.DropShadow != nil, st.InnerShadow != nil, st.OuterGlow != nil, st.InnerGlow != nil,
		st.Stroke != nil, st.ColorOverlay != nil}[i]
}

// toggle turns effect i on, with its last settings, or off, keeping them.
func (f *styleDialog) toggle(i int, on bool) {
	st := &f.st
	switch i {
	case effectDropShadow, effectInnerShadow:
		p := &st.DropShadow
		if i == effectInnerShadow {
			p = &st.InnerShadow
		}
		if *p != nil {
			f.shadows[i-effectDropShadow] = **p
		}
		*p = nil
		if on {
			v := f.shadows[i-effectDropShadow]
			*p = &v
		}
	case effectOuterGlow, effectInnerGlow:
		p := &st.OuterGlow
		if i == effectInnerGlow {
			p = &st.InnerGlow
		}
		if *p != nil {
			f.glows[i-effectOuterGlow] = **p
		}
		*p = nil
		if on {
			v := f.glows[i-effectOuterGlow]
			*p = &v
		}
	case effectStroke:
		if st.Stroke != nil {
			f.stroke = *st.Stroke
		}
		st.Stroke = nil
		if on {
			v := f.stroke
			st.Stroke = &v
		}
	case effectOverlay:
		if st.ColorOverlay != nil {
			f.overlay = *st.ColorOverlay
		}
		st.ColorOverlay = nil
		if on {
			v := f.overlay
			st.ColorOverlay = &v
		}
	}
}

// closeStyle keeps the effects as one step, or puts them back.
func (a *App) closeStyle(apply bool) {
	f := a.style
	a.style = nil
	if f == nil {
		return
	}
	if apply {
		f.s.Set(&f.st)
		f.s.End()
	} else {
		f.s.Cancel()
	}
	f.ad.changedAll()
}

// drawStyleDialog is the Layer Style dialog.
func (a *App) drawStyleDialog() {
	f := a.style
	if f == nil {
		return
	}
	if a.art != f.ad {
		a.closeStyle(false)
		return
	}
	u := &a.u
	box := rectCentered(a.screen, u.px(600), u.px(430))
	box.X = min(a.screen.W-box.W-u.px(330), box.X+u.px(160))
	panel(u, box)
	in := box.Inset(u.px(22))
	u.label(u.bold(16), gfx.Rect{X: in.X, Y: in.Y, W: in.W, H: u.px(24)}, u.t("Layer style"), colText, 0)
	list := gfx.Rect{X: in.X, Y: in.Y + u.px(40), W: u.px(180), H: in.H - u.px(90)}
	u.c.RoundRect(list, u.px(8), colBackground)
	for i, name := range effectNames {
		row := gfx.Rect{X: list.X + u.px(4), Y: list.Y + u.px(4) + float32(i)*u.px(36), W: list.W - u.px(8), H: u.px(32)}
		if i == f.page {
			u.c.RoundRect(row, u.px(6), gfx.Mix(colSurface, colAccent, 0.35))
		}
		box := gfx.Rect{X: row.X + u.px(6), Y: row.Y, W: u.px(26), H: row.H}
		if on := u.checkbox(fmt.Sprintf("style/on/%d", i), box, "", f.on(i)); on != f.on(i) {
			f.toggle(i, on)
			f.page = i
		}
		label := gfx.Rect{X: box.Right(), Y: row.Y, W: row.Right() - box.Right(), H: row.H}
		id := fmt.Sprintf("style/page/%d", i)
		if u.hover(id, label) {
			u.cursor = CursorHand
		}
		u.label(u.face(12.5), label.Inset(u.px(4)), u.t(name), colText, 0)
		if u.clicked(id, label) {
			f.page = i
		}
	}
	page := gfx.Rect{X: list.Right() + u.px(20), Y: list.Y, W: in.Right() - list.Right() - u.px(20), H: list.H}
	a.drawEffectPage(f, page)
	foot := in.Bottom() - u.px(34)
	cancel := u.button("style/cancel", gfx.Rect{X: in.Right() - u.px(210), Y: foot, W: u.px(100), H: u.px(34)}, "", u.t("Cancel"), ghost, true)
	ok := u.button("style/ok", gfx.Rect{X: in.Right() - u.px(100), Y: foot, W: u.px(100), H: u.px(34)}, "", u.t("OK"), primary, true)
	for _, k := range u.keys {
		switch k.Code {
		case keyEscape:
			cancel = true
		case keyEnter:
			ok = true
		}
	}
	u.keys = nil
	switch {
	case ok:
		a.closeStyle(true)
	case cancel:
		a.closeStyle(false)
	case !reflect.DeepEqual(f.st, f.shown):
		if f.s.Set(&f.st) == nil {
			f.shown = cloneStyle(f.st)
			f.ad.changedAll()
		}
	}
}

// drawEffectPage is the chosen effect's settings; those of an effect that is off are kept for when it is turned on.
func (a *App) drawEffectPage(f *styleDialog, r gfx.Rect) {
	u := &a.u
	title := u.t(effectNames[f.page])
	if !f.on(f.page) {
		title = u.tf("%s (off: tick it to use it)", title)
	}
	u.label(u.bold(13.5), gfx.Rect{X: r.X, Y: r.Y, W: r.W, H: u.px(24)}, title, colText, 0)
	y := r.Y + u.px(36)
	st := &f.st
	row := func() gfx.Rect { rr := gfx.Rect{X: r.X, Y: y, W: r.W, H: u.px(28)}; y += u.px(38); return rr }
	slider := func(id, label string, v *float64, lo, hi float64, format func(float64) string) {
		rr := row()
		lw := u.px(100)
		u.label(u.face(12.5), gfx.Rect{X: rr.X, Y: rr.Y, W: lw, H: rr.H}, label, colDim, 0)
		sr := gfx.Rect{X: rr.X + lw, Y: rr.Y + u.px(11), W: rr.W - lw - u.px(70), H: u.px(6)}
		u.slider("style/"+id, sr, v, lo, hi, true)
		u.label(u.face(12.5), gfx.Rect{X: sr.Right() + u.px(10), Y: rr.Y, W: u.px(60), H: rr.H}, format(*v), colText, 0)
	}
	px := func(v float64) string { return fmt.Sprintf("%.0f px", v) }
	deg := func(v float64) string { return fmt.Sprintf("%.0f°", v) }
	colour := func(c *color.NRGBA) {
		rr := row()
		u.label(u.face(12.5), gfx.Rect{X: rr.X, Y: rr.Y, W: u.px(100), H: rr.H}, u.t("Color"), colDim, 0)
		sw := gfx.Rect{X: rr.X + u.px(100), Y: rr.Y + u.px(2), W: u.px(44), H: rr.H - u.px(4)}
		u.c.Fill(sw.Inset(-max(1, u.px(1))), colBorder)
		u.c.Fill(sw, color.RGBA{c.R, c.G, c.B, 255})
		u.hint("style/colour", u.tf("%s: click to use the foreground colour", u.t("Color")), sw)
		if u.hover("style/colour", sw) {
			u.cursor = CursorHand
		}
		if u.clicked("style/colour", sw) {
			*c = a.brush.Color
		}
	}
	blend := func(b *paint.Blend) {
		rr := row()
		u.label(u.face(12.5), gfx.Rect{X: rr.X, Y: rr.Y, W: u.px(100), H: rr.H}, u.t("Blend mode"), colDim, 0)
		at := 0
		for i, m := range paint.Blends {
			if m == *b {
				at = i
			}
		}
		a.dropdown("style/blend", gfx.Rect{X: rr.X + u.px(100), Y: rr.Y, W: u.px(180), H: rr.H}, u.tcs("blend", blendNames), at, nil, func(i int) {
			*b = paint.Blends[i]
		})
	}
	opacity := func(v *float64) { slider("opacity", u.t("Opacity"), v, 0, 1, percent) }
	switch f.page {
	case effectDropShadow, effectInnerShadow:
		sh := st.DropShadow
		if f.page == effectInnerShadow {
			sh = st.InnerShadow
		}
		if sh == nil {
			sh = &f.shadows[f.page-effectDropShadow]
		}
		blend(&sh.Blend)
		colour(&sh.Color)
		opacity(&sh.Opacity)
		slider("angle", u.t("Angle"), &sh.Angle, -180, 180, deg)
		slider("distance", u.t("Distance"), &sh.Distance, 0, 250, px)
		slider("spread", u.t("Spread"), &sh.Spread, 0, 1, percent)
		slider("size", u.t("Size"), &sh.Size, 0, 250, px)
		sh.Angle, sh.Distance, sh.Size = math.Round(sh.Angle), math.Round(sh.Distance), math.Round(sh.Size)
	case effectOuterGlow, effectInnerGlow:
		g := st.OuterGlow
		if f.page == effectInnerGlow {
			g = st.InnerGlow
		}
		if g == nil {
			g = &f.glows[f.page-effectOuterGlow]
		}
		blend(&g.Blend)
		colour(&g.Color)
		opacity(&g.Opacity)
		slider("spread", u.t("Spread"), &g.Spread, 0, 1, percent)
		slider("size", u.t("Size"), &g.Size, 0, 250, px)
		g.Size = math.Round(g.Size)
	case effectStroke:
		o := st.Stroke
		if o == nil {
			o = &f.stroke
		}
		slider("size", u.t("Size"), &o.Size, 1, 250, px)
		o.Size = math.Round(o.Size)
		rr := row()
		u.label(u.face(12.5), gfx.Rect{X: rr.X, Y: rr.Y, W: u.px(100), H: rr.H}, u.t("Position"), colDim, 0)
		a.dropdown("style/position", gfx.Rect{X: rr.X + u.px(100), Y: rr.Y, W: u.px(180), H: rr.H}, u.ts(strokePositions), int(o.Position), nil, func(i int) {
			o.Position = paint.StrokePosition(i)
		})
		colour(&o.Color)
		opacity(&o.Opacity)
	case effectOverlay:
		o := st.ColorOverlay
		if o == nil {
			o = &f.overlay
		}
		blend(&o.Blend)
		colour(&o.Color)
		opacity(&o.Opacity)
	}
}

// strokePositions name a style's stroke positions, in paint.StrokePosition's order.
var strokePositions = msgs("Outside", "Inside", "Center")
