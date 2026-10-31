package ui

import (
	"fmt"
	"image/color"
	"math"
	"slices"

	"github.com/btelolabs/btelo-forge/internal/gfx"
	"github.com/btelolabs/btelo-forge/internal/paint"
)

// The gradient tool on a vector layer, as Affinity Designer's fill tool: a drag across the object lays a gradient
// over its fill, or its stroke while the stroke is being coloured (X switches), from where the drag begins to where
// it ends; the gradient's handles stay on the object, to be dragged: its ends, a radial gradient's edge across, and
// its stops, which a click chooses for the colour panel to colour, a double-click on the line adds and Delete
// removes. The options bar chooses the paint: none, a solid colour, or a linear, elliptical or conical gradient.

// What a gradient handle drag holds.
const (
	gradGrabNone = iota
	gradGrabNew
	gradGrabFrom
	gradGrabTo
	gradGrabAcross
	gradGrabStop
)

// gradDrag is a drag of the vector gradient tool.
type gradDrag struct {
	grab  int
	stop  int          // the stop held, by its place as the drag began
	start paint.Vector // the object as the drag began
	press paint.Pt     // where the drag began, on the canvas
}

// paintKinds are the paints the options bar offers, in its order, named by paintKindNames.
var (
	paintKinds     = []paint.PaintKind{paint.PaintNone, paint.PaintSolid, paint.PaintLinear, paint.PaintRadial, paint.PaintConical}
	paintKindNames = msgs("No paint", "Solid colour", "Linear gradient", "Elliptical gradient", "Conical gradient")
)

// gradientEdited is the chosen vector layer and its paint the gradient tool edits, when it is on a vector layer.
func (a *App) gradientEdited(ad *artDoc) (*paint.Node, *paint.VectorPaint, bool) {
	n := ad.paintedVector()
	if a.artTool != toolGradient || n == nil {
		return nil, nil, false
	}
	v := *n.Vector
	p := *targetPaint(&v, a.pickBackground)
	return n, &p, true
}

// gradientHandle is a handle of a gradient on the screen: what dragging it holds, its stop, and where it is.
type gradientHandle struct {
	grab int
	stop int
	at   paint.Pt // on the canvas
}

// gradientHandles are a gradient's handles on the canvas, stops last so they are taken before the ends they sit on.
func gradientHandles(v *paint.Vector, p paint.VectorPaint) []gradientHandle {
	if !p.Gradient() {
		return nil
	}
	from, to := v.M.Map(p.From), v.M.Map(p.To)
	hs := []gradientHandle{{grab: gradGrabFrom, at: from}, {grab: gradGrabTo, at: to}}
	if p.Kind == paint.PaintRadial {
		hs = append(hs, gradientHandle{grab: gradGrabAcross, at: v.M.Map(p.Across())})
	}
	for i, s := range p.Stops {
		hs = append(hs, gradientHandle{grab: gradGrabStop, stop: i, at: from.Lerp(to, s.At)})
	}
	return hs
}

// handleUnder is the gradient handle under the pointer, nearest first among stops and ends, and whether there is one.
func (a *App) handleUnder(ad *artDoc, vp gfx.Rect, hs []gradientHandle) (gradientHandle, bool) {
	for i := len(hs) - 1; i >= 0; i-- {
		if a.near(ad, vp, hs[i].at) {
			return hs[i], true
		}
	}
	return gradientHandle{}, false
}

// defaultGradient is a gradient of kind across the object's own box: from its colour to white (or black, from
// white), left to right through the middle, or from the middle for a round one.
func (ad *artDoc) defaultGradient(v *paint.Vector, p paint.VectorPaint, kind paint.PaintKind) paint.VectorPaint {
	if p.Gradient() {
		p.Kind = kind
		return p
	}
	c := ad.paintColour(p)
	end := color.NRGBA{R: 255, G: 255, B: 255, A: 255}
	if c.R > 240 && c.G > 240 && c.B > 240 {
		end = color.NRGBA{A: 255}
	}
	b := *v
	b.M, b.Stroke = paint.Identity, paint.VectorStroke{}
	box := b.Bounds()
	mid := box.Min.Add(box.Max).Mul(0.5)
	from := paint.Pt{X: box.Min.X, Y: mid.Y}
	if kind != paint.PaintLinear {
		from = mid
	}
	return paint.VectorPaint{Kind: kind, From: from, To: paint.Pt{X: box.Max.X, Y: mid.Y},
		Stops: []paint.Stop{{At: 0, Color: c}, {At: 1, Color: end}}}
}

// vectorGradientPointer is the gradient tool on a vector layer.
func (a *App) vectorGradientPointer(ad *artDoc, vp gfx.Rect, n *paint.Node, p paint.VectorPaint) {
	u := &a.u
	const id = "art/view"
	v := n.Vector
	inv, ok := v.M.Inverse()
	if !ok {
		return
	}
	hs := gradientHandles(v, p)
	under, onHandle := a.handleUnder(ad, vp, hs)
	if u.hover(id, vp) {
		u.cursor = CursorCross
		if onHandle {
			u.cursor = CursorMove
		}
	}
	cur := paint.Pt{X: ad.cursor[0], Y: ad.cursor[1]}
	if u.press(id, vp) {
		g := &gradDrag{grab: gradGrabNew, start: *v, press: cur}
		switch {
		case onHandle:
			g.grab, g.stop = under.grab, under.stop
			if under.grab == gradGrabStop {
				ad.gradStop = under.stop
				a.picker.hex = ""
				// A stop at an end is chosen for the colours, and a drag takes the end it sits on.
				switch at := p.Stops[under.stop].At; {
				case at <= 0:
					g.grab = gradGrabFrom
				case at >= 1:
					g.grab = gradGrabTo
				}
			}
		case p.Gradient() && u.double:
			// A double-click on the line adds a stop there, coloured as the gradient is there.
			from, to := v.M.Map(p.From), v.M.Map(p.To)
			if t, d := nearestOnLine(from, to, cur); d*ad.zoom <= float64(u.px(grabReach)) {
				next := *v
				q := targetPaint(&next, a.pickBackground)
				q.Stops = append(slices.Clone(q.Stops), paint.Stop{At: t, Color: p.ColorAt(p.From.Lerp(p.To, t))})
				slices.SortStableFunc(q.Stops, func(x, y paint.Stop) int { return cmpFloat(x.At, y.At) })
				ad.gradStop = slices.IndexFunc(q.Stops, func(s paint.Stop) bool { return s.At == t })
				a.picker.hex = ""
				a.liveVector(ad, n, next, "Gradient")
				g.grab = gradGrabNone
			}
		}
		ad.gradDrag = g
	}
	g := ad.gradDrag
	if g == nil || !u.dragging(id) && !u.letGo(id) {
		return
	}
	if u.dragging(id) && u.drag.moved && g.grab != gradGrabNone {
		next := g.start
		q := targetPaint(&next, a.pickBackground)
		at := cur
		switch g.grab {
		case gradGrabNew:
			if u.shift {
				at.X, at.Y = snap45(g.press.X, g.press.Y, cur.X, cur.Y)
			}
			kind := q.Kind
			if !q.Gradient() {
				kind = paint.PaintLinear
			}
			*q = ad.defaultGradient(&g.start, *q, kind)
			q.From, q.To = inv.Map(g.press), inv.Map(at)
		case gradGrabFrom:
			if u.shift {
				e := g.start.M.Map(q.To)
				at.X, at.Y = snap45(e.X, e.Y, cur.X, cur.Y)
			}
			q.From = inv.Map(at)
		case gradGrabTo:
			if u.shift {
				f := g.start.M.Map(q.From)
				at.X, at.Y = snap45(f.X, f.Y, cur.X, cur.Y)
			}
			q.To = inv.Map(at)
		case gradGrabAcross:
			line := q.To.Sub(q.From)
			if l := line.Len(); l > 0 {
				q.Aspect = max(math.Abs(inv.Map(at).Sub(q.From).Dot(line.Perp().Unit()))/l, 0.01)
			}
		case gradGrabStop:
			if i := g.stop; i < len(q.Stops) {
				t, _ := nearestOnLine(next.M.Map(q.From), next.M.Map(q.To), cur)
				q.Stops = slices.Clone(q.Stops)
				moved := q.Stops[i]
				moved.At = t
				q.Stops[i] = moved
				slices.SortStableFunc(q.Stops, func(x, y paint.Stop) int { return cmpFloat(x.At, y.At) })
				ad.gradStop = slices.Index(q.Stops, moved)
			}
		}
		a.liveVector(ad, n, next, "Gradient")
	}
	if u.letGo(id) {
		ad.gradDrag = nil
	}
}

// nearestOnLine is where along the line from a to b (0 to 1) the point p is nearest, and how far from it.
func nearestOnLine(a, b, p paint.Pt) (t, dist float64) {
	d := b.Sub(a)
	if l2 := d.Dot(d); l2 > 0 {
		t = min(max(p.Sub(a).Dot(d)/l2, 0), 1)
	}
	return t, a.Lerp(b, t).Dist(p)
}

func cmpFloat(a, b float64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// gradientKeys are the vector gradient tool's keys: Delete removes the chosen stop while two others are left.
func (a *App) gradientKeys(ad *artDoc, k Key) bool {
	n, p, ok := a.gradientEdited(ad)
	if !ok || !p.Gradient() || k.Code != keyDelete && k.Code != keyBack {
		return false
	}
	if len(p.Stops) <= 2 {
		return true // a gradient keeps two stops
	}
	next := *n.Vector
	q := targetPaint(&next, a.pickBackground)
	q.Stops = slices.Delete(slices.Clone(q.Stops), ad.stopOf(*q), ad.stopOf(*q)+1)
	ad.gradStop = max(ad.gradStop-1, 0)
	a.picker.hex = ""
	a.artRun(ad, func() error { ad.d.SetVector(n.ID, next, "Gradient"); return nil })
	return true
}

// setPaintKind gives the paint the gradient tool edits another kind, keeping its colours.
func (a *App) setPaintKind(ad *artDoc, n *paint.Node, kind paint.PaintKind) {
	next := *n.Vector
	q := targetPaint(&next, a.pickBackground)
	switch kind {
	case paint.PaintNone:
		*q = paint.VectorPaint{}
	case paint.PaintSolid:
		*q = solidPaint(ad.paintColour(*q))
	default:
		*q = ad.defaultGradient(n.Vector, *q, kind)
	}
	if a.pickBackground && q.Kind != paint.PaintNone && next.Stroke.Width <= 0 {
		next.Stroke.Width = max(a.settings.Paint.ShapeWidth, 1)
	}
	a.artRun(ad, func() error { ad.d.SetVector(n.ID, next, "Gradient"); return nil })
}

// drawVectorGradientOptions is the gradient tool's options bar on a vector layer: the fill or the stroke, the kind
// of paint, reversing a gradient, and a hint.
func (a *App) drawVectorGradientOptions(ad *artDoc, n *paint.Node, p paint.VectorPaint, r gfx.Rect) {
	u := &a.u
	x := r.X
	for i, label := range []string{u.t("Fill"), u.t("Stroke")} {
		w := u.buttonWidth("", label)
		style := ghost
		if a.pickBackground == (i == 1) {
			style = plain
		}
		if u.button(fmt.Sprintf("art/vgrad/target/%d", i), gfx.Rect{X: x, Y: r.Y, W: w, H: r.H}, "", label, style, true) {
			a.pickBackground = i == 1
			a.picker.hex = ""
		}
		x += w + u.px(4)
	}
	x += u.px(10)
	for i, kind := range paintKinds {
		a.optRow(&x, &r, u.px(32))
		b := gfx.Rect{X: x, Y: r.Y, W: u.px(30), H: r.H}
		id := fmt.Sprintf("art/vgrad/kind/%d", i)
		bg := colSurface
		switch {
		case p.Kind == kind:
			bg = colSelected
		case u.hover(id, b):
			bg = colSurfaceHi
		}
		u.c.RoundRect(b, u.px(6), bg)
		drawPaintKindIcon(u, b.Inset(u.px(7)), kind)
		u.hint(id, u.t(paintKindNames[i]), b)
		if u.clicked(id, b) && p.Kind != kind {
			a.setPaintKind(ad, n, kind)
		}
		x += u.px(32)
	}
	x += u.px(10)
	if p.Gradient() {
		a.optRow(&x, &r, u.px(220))
		x = a.drawStopsBar(ad, n, p, gfx.Rect{X: x, Y: r.Y, W: u.px(220), H: r.H}) + u.px(10)
		label := u.t("Reverse")
		w := u.buttonWidth("", label)
		a.optRow(&x, &r, w)
		if u.button("art/vgrad/reverse", gfx.Rect{X: x, Y: r.Y, W: w, H: r.H}, "", label, ghost, true) {
			next := *n.Vector
			q := targetPaint(&next, a.pickBackground)
			q.Stops = slices.Clone(q.Stops)
			for i := range q.Stops {
				q.Stops[i].At = 1 - q.Stops[i].At
			}
			slices.Reverse(q.Stops)
			a.artRun(ad, func() error { ad.d.SetVector(n.ID, next, "Gradient"); return nil })
		}
		x += w + u.px(10)
	}
	a.optHint(x, r, u.t("Drag across the shape for a gradient; drag its handles, click a stop to colour it, double-click the line to add one"))
}

// stopsDrag is a stop being dragged along the options bar's gradient: the stops as the drag began, and which.
type stopsDrag struct {
	start []paint.Stop
	stop  int
}

// drawStopsBar is the gradient's stops in the options bar, as Photoshop's gradient editor has them: the gradient
// over a checkerboard with a marker under it for each stop. A click on the bar adds a stop there, in the colour the
// gradient has there; a click on a marker chooses it, for the colour panel; a drag moves it along; a right-click,
// or Delete, removes it while two others are left. It returns where it ends.
func (a *App) drawStopsBar(ad *artDoc, n *paint.Node, p paint.VectorPaint, r gfx.Rect) float32 {
	u := &a.u
	bar := gfx.Rect{X: r.X, Y: r.Y + u.px(3), W: r.W, H: r.H - u.px(14)}
	drawTransparency(u, bar)
	steps := max(int(bar.W), 1)
	line := paint.VectorPaint{Kind: paint.PaintLinear, Stops: p.Stops, To: paint.Pt{X: 1}}
	for i := range steps {
		c := line.ColorAt(paint.Pt{X: (float64(i) + 0.5) / float64(steps)})
		u.c.Fill(gfx.Rect{X: bar.X + float32(i)*bar.W/float32(steps), Y: bar.Y, W: bar.W/float32(steps) + 1, H: bar.H}, color.RGBA{c.R, c.G, c.B, c.A})
	}
	u.c.Outline(bar, max(1, u.px(1)), colBorder)
	at := func(t float64) float32 { return bar.X + float32(t)*bar.W }
	tOf := func(x float32) float64 { return min(max(float64((x-bar.X)/bar.W), 0), 1) }
	set := func(stops []paint.Stop, live bool) {
		next := *n.Vector
		q := targetPaint(&next, a.pickBackground)
		q.Stops = stops
		if live {
			a.liveVector(ad, n, next, "Gradient")
			return
		}
		a.artRun(ad, func() error { ad.d.SetVector(n.ID, next, "Gradient"); return nil })
	}
	chosen := ad.stopOf(p)
	hitStop := -1
	for i, s := range p.Stops {
		mr := gfx.Rect{X: at(s.At) - u.px(5), Y: bar.Bottom() + u.px(1), W: u.px(10), H: u.px(10)}
		edge := colBorder
		if i == chosen {
			edge = colAccent
		}
		u.c.Fill(mr.Inset(-max(1, u.px(1.5))), edge)
		drawTransparency(u, mr)
		u.c.Fill(mr, color.RGBA{s.Color.R, s.Color.G, s.Color.B, s.Color.A})
		id := fmt.Sprintf("art/vgrad/stop/%d", i)
		u.hint(id, u.t("A stop: click to colour it, drag to move it, right-click to remove it"), mr)
		if mr.Contains(u.mx, u.my) {
			hitStop = i
		}
		if u.rightAt != nil && mr.Contains(u.rightAt.X, u.rightAt.Y) && len(p.Stops) > 2 {
			u.rightAt = nil
			ad.gradStop = i
			a.gradientKeys(ad, Key{Code: keyDelete})
			return r.Right()
		}
	}
	const id = "art/vgrad/stops"
	area := gfx.Rect{X: bar.X - u.px(5), Y: bar.Y, W: bar.W + u.px(10), H: r.Bottom() - bar.Y}
	if u.hover(id, area) {
		u.cursor = CursorHand
	}
	if u.press(id, area) {
		if hitStop >= 0 {
			ad.gradStop, ad.stopsDrag = hitStop, &stopsDrag{start: p.Stops, stop: hitStop}
		} else {
			// A new stop where the bar was pressed, in the colour the gradient has there.
			t := tOf(u.mx)
			stops := append(slices.Clone(p.Stops), paint.Stop{At: t, Color: line.ColorAt(paint.Pt{X: t})})
			slices.SortStableFunc(stops, func(x, y paint.Stop) int { return cmpFloat(x.At, y.At) })
			ad.gradStop = slices.IndexFunc(stops, func(s paint.Stop) bool { return s.At == t })
			set(stops, false)
		}
		a.picker.hex = ""
	}
	if g := ad.stopsDrag; g != nil && u.dragging(id) && u.drag.moved && g.stop < len(g.start) {
		stops := slices.Clone(g.start)
		moved := stops[g.stop]
		moved.At = tOf(u.mx)
		stops[g.stop] = moved
		slices.SortStableFunc(stops, func(x, y paint.Stop) int { return cmpFloat(x.At, y.At) })
		ad.gradStop = slices.Index(stops, moved)
		set(stops, true)
	}
	if u.letGo(id) {
		ad.stopsDrag = nil
	}
	return r.Right()
}

// drawPaintKindIcon draws a small picture of a kind of paint.
func drawPaintKindIcon(u *ctx, r gfx.Rect, kind paint.PaintKind) {
	u.c.Fill(r, colWhite)
	if kind == paint.PaintNone {
		u.c.Line(r.X, r.Bottom(), r.Right(), r.Y, max(1, u.px(1.5)), colDanger)
		return
	}
	if kind == paint.PaintSolid {
		u.c.Fill(r, colText)
		return
	}
	g := paint.VectorPaint{Kind: kind, From: paint.Pt{X: 0, Y: 0.5}, To: paint.Pt{X: 1, Y: 0.5}, Aspect: 0.6,
		Stops: []paint.Stop{{At: 0, Color: color.NRGBA{R: 30, G: 30, B: 34, A: 255}}, {At: 1, Color: color.NRGBA{R: 240, G: 240, B: 240, A: 255}}}}
	if kind != paint.PaintLinear {
		g.From = paint.Pt{X: 0.5, Y: 0.5}
	}
	steps := max(int(r.W), 1)
	for j := range steps {
		for i := range steps {
			c := g.ColorAt(paint.Pt{X: (float64(i) + 0.5) / float64(steps), Y: (float64(j) + 0.5) / float64(steps)})
			u.c.Fill(gfx.Rect{X: r.X + float32(i)*r.W/float32(steps), Y: r.Y + float32(j)*r.H/float32(steps), W: r.W / float32(steps), H: r.H / float32(steps)},
				color.RGBA{c.R, c.G, c.B, 255})
		}
	}
}

// drawVectorGradientOverlay draws the gradient's handles on the object: its line and ends, a radial gradient's edge
// across, and its stops in their colours, the chosen one ringed.
func (a *App) drawVectorGradientOverlay(ad *artDoc, vp gfx.Rect) {
	n, p, ok := a.gradientEdited(ad)
	if !ok || !p.Gradient() {
		return
	}
	u := &a.u
	w := max(1, u.px(1))
	v := n.Vector
	fx, fy := ad.toScreen(vp, pt2(v.M.Map(p.From)))
	tx, ty := ad.toScreen(vp, pt2(v.M.Map(p.To)))
	line := func(x0, y0, x1, y1 float32) {
		u.c.Line(x0, y0, x1, y1, w+2, gfx.Alpha(colBlack, 0.5))
		u.c.Line(x0, y0, x1, y1, w, colWhite)
	}
	line(fx, fy, tx, ty)
	if p.Kind == paint.PaintRadial {
		ax, ay := ad.toScreen(vp, pt2(v.M.Map(p.Across())))
		line(fx, fy, ax, ay)
		u.c.Circle(ax, ay, u.px(4.5), colBlack)
		u.c.Circle(ax, ay, u.px(3.5), colAccent)
	}
	for _, e := range [][2]float32{{fx, fy}, {tx, ty}} {
		u.c.Circle(e[0], e[1], u.px(5.5), colBlack)
		u.c.Circle(e[0], e[1], u.px(4.5), colWhite)
	}
	chosen := ad.stopOf(*p)
	for i, s := range p.Stops {
		x, y := fx+(tx-fx)*float32(s.At), fy+(ty-fy)*float32(s.At)
		r := gfx.Rect{X: x - u.px(4.5), Y: y - u.px(4.5), W: u.px(9), H: u.px(9)}
		edge := colBlack
		if i == chosen {
			edge = colAccent
			u.c.Fill(r.Inset(-u.px(2.5)), edge)
		}
		u.c.Fill(r.Inset(-2*w), gfx.Alpha(colBlack, 0.7)) // dark, then light: seen on any colour
		u.c.Fill(r.Inset(-w), colWhite)
		u.c.Fill(r, color.RGBA{s.Color.R, s.Color.G, s.Color.B, 255})
	}
}
