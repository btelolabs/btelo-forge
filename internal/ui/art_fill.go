package ui

import (
	"fmt"
	"image"
	"image/color"
	"math"

	"github.com/btelolabs/btelo-forge/internal/gfx"
	"github.com/btelolabs/btelo-forge/internal/paint"
)

const (
	icClone       = "\ue982"
	icGradient    = "\ue3e9"
	icShapes      = "\ue574"
	icShapeRect   = "\ue3c1"
	icShapeCircle = "\ue40c"
	icShapeLine   = "\ue15b"
	icPolygon     = "\ue86b"
	icStar        = "\ue838"
)

// The shape tool's shapes, in the order of its options; each makes a vector layer, as Affinity Designer's shape
// tools do.
const (
	shapeRect = iota
	shapeEllipse
	shapeLine
	shapePolygon
	shapeStar
)

var (
	shapeGlyphs = []string{icShapeRect, icShapeCircle, icShapeLine, icPolygon, icStar}
	shapeNames  = msgs("Rectangle", "Ellipse", "Line", "Polygon", "Star")
)

// shapeKind is the chosen shape, kept within those there are.
func (a *App) shapeKind() int { return min(max(a.settings.Paint.ShapeKind, 0), len(shapeNames)-1) }

// gradientNames name the gradient shapes, by paint.GradientKind; gradientPresets are its colours.
var (
	gradientNames   = msgs("Linear gradient", "Radial gradient", "Angle gradient", "Reflected gradient", "Diamond gradient")
	gradientPresets = msgs("Foreground to background", "Foreground to transparent", "Black to white")
)

// gradientColours are the start and end colours of the chosen preset.
func (a *App) gradientColours() (color.NRGBA, color.NRGBA) {
	fg := a.brush.Color
	switch a.settings.Paint.GradientColours {
	case 1:
		end := fg
		end.A = 0
		return fg, end
	case 2:
		return color.NRGBA{A: 255}, color.NRGBA{R: 255, G: 255, B: 255, A: 255}
	}
	return fg, a.backgroundColor()
}

// snap45 keeps a line from (x0, y0) to (x1, y1) to a multiple of 45°, as Shift does in Photoshop.
func snap45(x0, y0, x1, y1 float64) (float64, float64) {
	dx, dy := x1-x0, y1-y0
	angle := math.Round(math.Atan2(dy, dx)/(math.Pi/4)) * (math.Pi / 4)
	length := math.Hypot(dx, dy)
	return x0 + length*math.Cos(angle), y0 + length*math.Sin(angle)
}

// drawEnd is where a gradient or shape being dragged ends: Shift keeps lines to 45° and boxes square, Alt draws
// a box from its middle.
func (a *App) drawEnd(ad *artDoc) (from, to [2]float64) {
	u := &a.u
	from, to = ad.drawFrom, ad.drawTo
	line := a.artTool == toolGradient || a.shapeKind() == shapeLine
	switch {
	case line && u.shift:
		to[0], to[1] = snap45(from[0], from[1], to[0], to[1])
	case !line && u.shift:
		d := max(math.Abs(to[0]-from[0]), math.Abs(to[1]-from[1]))
		to = [2]float64{from[0] + math.Copysign(d, to[0]-from[0]), from[1] + math.Copysign(d, to[1]-from[1])}
	}
	if !line && u.alt {
		from = [2]float64{2*from[0] - to[0], 2*from[1] - to[1]}
	}
	return from, to
}

// finishDrawing lays the gradient, or adds the shape, dragged on the canvas.
func (a *App) finishDrawing(ad *artDoc) {
	p := &a.settings.Paint
	from, to := a.drawEnd(ad)
	ad.drawing = false
	if a.artTool == toolGradient {
		start, end := a.gradientColours()
		g := paint.Gradient{Kind: paint.GradientKind(p.GradientKind), From: from, To: to, Start: start, End: end,
			Opacity: p.GradientOpacity, Reverse: p.GradientReverse, Dither: p.GradientDither}
		a.artRun(ad, func() error { return ad.d.DrawGradient(g) })
		return
	}
	s := ad.shape
	ad.shape = nil
	if s == nil {
		return
	}
	if !a.shapeShows(from, to) {
		ad.touched(s.Cancel()) // a click, or a shape with neither fill nor stroke, draws nothing
		return
	}
	s.End()
	ad.changedAll()
}

// shapeShows reports whether the shape tool's drag from one corner to the other draws anything.
func (a *App) shapeShows(from, to [2]float64) bool {
	p := &a.settings.Paint
	return math.Hypot(to[0]-from[0], to[1]-from[1]) >= 2 && (p.ShapeFill || p.ShapeStroke && a.shapeKind() != shapeLine)
}

// showShape shows the shape being dragged as the object it will be, on its own layer, as Affinity Designer draws a
// shape as it is dragged; the layer is made once the drag draws something.
func (a *App) showShape(ad *artDoc) {
	from, to := a.drawEnd(ad)
	kind := a.shapeKind()
	v := a.newShape(kind, from, to)
	if ad.shape == nil {
		if !a.shapeShows(from, to) {
			return
		}
		s, err := ad.d.BeginVector(0, v, fmt.Sprintf("%s %d", a.u.t(shapeNames[kind]), len(ad.d.Layers())+1), "Add shape")
		if err != nil {
			a.toast(a.describe(err), true)
			return
		}
		ad.shape = s
	}
	ad.touched(ad.shape.Set(v))
}

// newShape is the vector object the shape tool draws from one corner to the other (a line's ends), filled with the
// foreground colour and stroked with the background colour as its options say; a line is stroked with the
// foreground colour.
func (a *App) newShape(kind int, from, to [2]float64) paint.Vector {
	p := &a.settings.Paint
	if kind == shapeLine {
		path := []paint.Subpath{{Nodes: []paint.PathNode{sharpNode(paint.Pt{X: from[0], Y: from[1]}), sharpNode(paint.Pt{X: to[0], Y: to[1]})}}}
		return paint.Vector{Kind: paint.VectorCurve, Path: path, M: paint.Identity,
			Stroke: paint.VectorStroke{Paint: solidPaint(a.brush.Color), Width: max(p.ShapeWidth, 1), Cap: paint.CapRound}}
	}
	x0, y0 := min(from[0], to[0]), min(from[1], to[1])
	v := paint.Vector{Size: paint.Pt{X: math.Abs(to[0] - from[0]), Y: math.Abs(to[1] - from[1])}, M: paint.Translate(x0, y0),
		Radius: p.ShapeRadius, Sides: max(p.ShapeSides, 3), Inner: p.ShapeInner}
	switch kind {
	case shapeEllipse:
		v.Kind = paint.VectorEllipse
	case shapePolygon:
		v.Kind = paint.VectorPolygon
	case shapeStar:
		v.Kind = paint.VectorStar
	default:
		v.Kind = paint.VectorRect
	}
	if p.ShapeFill {
		v.Fill = solidPaint(a.brush.Color)
	}
	if p.ShapeStroke {
		v.Stroke = paint.VectorStroke{Paint: solidPaint(a.backgroundColor()), Width: max(p.ShapeWidth, 0)}
	}
	return v
}

// beginClone starts a clone stamp stroke at the pointer; Alt+click sets where it clones from instead. Aligned, the
// source keeps its distance from the brush from one stroke to the next, as in Photoshop.
func (a *App) beginClone(ad *artDoc, cx, cy float64) {
	u := &a.u
	p := &a.settings.Paint
	if u.alt {
		ad.cloneFrom, ad.cloneSet, ad.cloneAligned = [2]float64{cx, cy}, true, false
		u.active = ""
		return
	}
	if !ad.cloneSet {
		a.toast(u.t("Alt+click where to clone from first"), false)
		u.active = ""
		return
	}
	if !p.CloneAligned || !ad.cloneAligned {
		ad.cloneOffset = image.Pt(int(math.Round(ad.cloneFrom[0]-cx)), int(math.Round(ad.cloneFrom[1]-cy)))
		ad.cloneAligned = true
	}
	s, changed, err := ad.d.BeginClone(a.brush, ad.cloneOffset.X, ad.cloneOffset.Y, p.CloneAllLayers, cx, cy)
	if err != nil {
		a.toast(a.describe(err), true)
		u.active = ""
		return
	}
	ad.stroke = s
	ad.touched(changed)
}

// drawFillOverlay shows the gradient's line or the shape being dragged, and where the clone stamp takes from.
func (a *App) drawFillOverlay(ad *artDoc, vp gfx.Rect) {
	u := &a.u
	w := max(1, u.px(1))
	if ad.drawing && ad.shape != nil {
		a.drawOutline(ad, vp, ad.shape.Vector().ToCurve())
		return
	}
	if ad.drawing {
		from, to := a.drawEnd(ad)
		x0, y0 := ad.toScreen(vp, from)
		x1, y1 := ad.toScreen(vp, to)
		if a.artTool == toolGradient || a.shapeKind() == shapeLine {
			u.c.Line(x0, y0, x1, y1, w+2, gfx.Alpha(colBlack, 0.5))
			u.c.Line(x0, y0, x1, y1, w, colWhite)
			for _, p := range [][2]float32{{x0, y0}, {x1, y1}} {
				u.c.Circle(p[0], p[1], u.px(4), colWhite)
				u.c.Circle(p[0], p[1], u.px(2.5), colBlack)
			}
			return
		}
		box := gfx.Rect{X: min(x0, x1), Y: min(y0, y1), W: max(x0, x1) - min(x0, x1), H: max(y0, y1) - min(y0, y1)}
		if a.shapeKind() == shapeEllipse {
			u.c.RoundOutline(box, min(box.W, box.H)/2, w, colWhite)
		}
		drawAnts(u, box)
		return
	}
	if a.artTool != toolClone || !ad.cloneSet {
		return
	}
	// The source: where it was set, or under the brush's distance while painting and once aligned.
	src := ad.cloneFrom
	if ad.stroke != nil || ad.cloneAligned && a.settings.Paint.CloneAligned && ad.over {
		src = [2]float64{ad.cursor[0] + float64(ad.cloneOffset.X), ad.cursor[1] + float64(ad.cloneOffset.Y)}
	}
	x, y := ad.toScreen(vp, src)
	s := u.px(7)
	for _, c := range []struct {
		col   color.RGBA
		width float32
	}{{gfx.Alpha(colBlack, 0.6), w + 2}, {colWhite, w}} {
		u.c.Line(x-s, y, x+s, y, c.width, c.col)
		u.c.Line(x, y-s, x, y+s, c.width, c.col)
	}
}

// drawBucketOptions is the paint bucket's options bar.
func (a *App) drawBucketOptions(r gfx.Rect) {
	u := &a.u
	p := &a.settings.Paint
	x, done := a.labelledSlider("art/fill/opacity", r.X, r.Y, r.H, u.t("Opacity"), &p.FillOpacity, 0.01, 1, percent)
	tol := float64(p.FillTolerance)
	x, done2 := a.labelledSlider("art/fill/tolerance", x, r.Y, r.H, u.t("Tolerance"), &tol, 0, 255, func(v float64) string { return fmt.Sprintf("%.0f", v) })
	p.FillTolerance = int(math.Round(tol))
	done = done || done2
	if on := u.checkbox("art/fill/contiguous", gfx.Rect{X: x, Y: r.Y, W: u.px(110), H: r.H}, u.t("Contiguous"), p.FillContiguous); on != p.FillContiguous {
		p.FillContiguous, done = on, true
	}
	x += u.px(116)
	if on := u.checkbox("art/fill/all", gfx.Rect{X: x, Y: r.Y, W: u.px(170), H: r.H}, u.t("Sample all layers"), p.FillAllLayers); on != p.FillAllLayers {
		p.FillAllLayers, done = on, true
	}
	if done {
		a.saveSettings()
	}
}

// drawGradientOptions is the gradient tool's options bar: colours, shape, opacity, reverse and dither.
func (a *App) drawGradientOptions(r gfx.Rect) {
	u := &a.u
	p := &a.settings.Paint
	x := r.X
	a.dropdown("art/gradient/colours", gfx.Rect{X: x, Y: r.Y, W: u.px(200), H: r.H}, u.ts(gradientPresets), p.GradientColours, nil, func(i int) {
		p.GradientColours = i
		a.saveSettings()
	})
	x += u.px(210)
	for i := range paint.GradientKinds {
		b := gfx.Rect{X: x, Y: r.Y, W: u.px(30), H: r.H}
		id := fmt.Sprintf("art/gradient/kind/%d", i)
		bg := colSurface
		switch {
		case p.GradientKind == i:
			bg = gfx.Mix(colSurface, colAccent, 0.45)
		case u.hover(id, b):
			bg = colSurfaceHi
		}
		u.c.RoundRect(b, u.px(6), bg)
		a.drawGradientIcon(b.Inset(u.px(5)), paint.GradientKind(i))
		u.hint(id, u.t(gradientNames[i]), b)
		if u.clicked(id, b) {
			p.GradientKind = i
			a.saveSettings()
		}
		x += u.px(32)
	}
	x += u.px(10)
	x, done := a.labelledSlider("art/gradient/opacity", x, r.Y, r.H, u.t("Opacity"), &p.GradientOpacity, 0.01, 1, percent)
	if on := u.checkbox("art/gradient/reverse", gfx.Rect{X: x, Y: r.Y, W: u.px(90), H: r.H}, u.t("Reverse"), p.GradientReverse); on != p.GradientReverse {
		p.GradientReverse, done = on, true
	}
	x += u.px(96)
	if on := u.checkbox("art/gradient/dither", gfx.Rect{X: x, Y: r.Y, W: u.px(90), H: r.H}, u.t("Dither"), p.GradientDither); on != p.GradientDither {
		p.GradientDither, done = on, true
	}
	if done {
		a.saveSettings()
	}
}

// drawGradientIcon draws a small picture of a gradient shape, black to white.
func (a *App) drawGradientIcon(r gfx.Rect, kind paint.GradientKind) {
	img := a.u.c.Img
	g := paint.Gradient{Kind: kind, From: [2]float64{float64(r.X), float64(r.Y + r.H/2)}, To: [2]float64{float64(r.Right()), float64(r.Y + r.H/2)}}
	if kind != paint.GradientLinear {
		g.From = [2]float64{float64(r.X + r.W/2), float64(r.Y + r.H/2)}
	}
	for y := int(r.Y); y < int(r.Bottom()); y++ {
		for x := int(r.X); x < int(r.Right()); x++ {
			if !(image.Point{x, y}).In(img.Rect) {
				continue
			}
			v := uint8(40 + 200*g.At(float64(x)+0.5, float64(y)+0.5))
			img.SetRGBA(x, y, color.RGBA{v, v, v, 255})
		}
	}
}

// drawCloneOptions is the clone stamp's options bar: aligned, the source, and the brush.
func (a *App) drawCloneOptions(r gfx.Rect) {
	u := &a.u
	p := &a.settings.Paint
	x := r.X
	done := false
	if on := u.checkbox("art/clone/aligned", gfx.Rect{X: x, Y: r.Y, W: u.px(96), H: r.H}, u.t("Aligned"), p.CloneAligned); on != p.CloneAligned {
		p.CloneAligned, done = on, true
	}
	x += u.px(100)
	if on := u.checkbox("art/clone/all", gfx.Rect{X: x, Y: r.Y, W: u.px(170), H: r.H}, u.t("Sample all layers"), p.CloneAllLayers); on != p.CloneAllLayers {
		p.CloneAllLayers, done = on, true
	}
	x += u.px(176)
	if done {
		a.saveSettings()
	}
	a.drawBrushOptions(gfx.Rect{X: x, Y: r.Y, W: r.Right() - x, H: r.H})
}

// drawShapeOptions is the shape tool's options bar: the shape, its fill (the foreground colour) and stroke (the
// background colour), the stroke's width and a rectangle's corners.
func (a *App) drawShapeOptions(r gfx.Rect) {
	u := &a.u
	p := &a.settings.Paint
	x := r.X
	done := false
	for i, glyph := range shapeGlyphs {
		if u.iconButton(fmt.Sprintf("art/shape/%d", i), gfx.Rect{X: x, Y: r.Y, W: u.px(30), H: r.H}, glyph, u.t(shapeNames[i]), p.ShapeKind == i, true) {
			p.ShapeKind, done = i, true
		}
		x += u.px(32)
	}
	x += u.px(12)
	kind := a.shapeKind()
	line := kind == shapeLine
	if !line {
		if on := u.checkbox("art/shape/fill", gfx.Rect{X: x, Y: r.Y, W: u.px(70), H: r.H}, u.t("Fill"), p.ShapeFill); on != p.ShapeFill {
			p.ShapeFill, done = on, true
		}
		x += u.px(74)
		if on := u.checkbox("art/shape/stroke", gfx.Rect{X: x, Y: r.Y, W: u.px(80), H: r.H}, u.t("Stroke"), p.ShapeStroke); on != p.ShapeStroke {
			p.ShapeStroke, done = on, true
		}
		x += u.px(84)
	}
	label := u.t("Stroke width")
	if line {
		label = u.t("Weight")
	}
	px := func(v float64) string { return fmt.Sprintf("%.0f px", v) }
	var d bool
	x, d = a.labelledSlider("art/shape/width", x, r.Y, r.H, label, &p.ShapeWidth, 1, 100, px)
	p.ShapeWidth = math.Round(p.ShapeWidth)
	done = done || d
	switch {
	case kind == shapeRect && x+u.px(190) < r.Right():
		_, d = a.labelledSlider("art/shape/radius", x, r.Y, r.H, u.t("Corners"), &p.ShapeRadius, 0, 200, px)
		p.ShapeRadius = math.Round(p.ShapeRadius)
		done = done || d
	case (kind == shapePolygon || kind == shapeStar) && x+u.px(190) < r.Right():
		sides := float64(max(p.ShapeSides, 3))
		x, d = a.labelledSlider("art/shape/sides", x, r.Y, r.H, u.t("Points"), &sides, 3, 32, func(v float64) string { return fmt.Sprintf("%.0f", v) })
		p.ShapeSides = int(math.Round(sides))
		done = done || d
		if kind == shapeStar && x+u.px(190) < r.Right() {
			_, d = a.labelledSlider("art/shape/inner", x, r.Y, r.H, u.t("Inner radius"), &p.ShapeInner, 0.05, 1, percent)
			done = done || d
		}
	}
	if done {
		a.saveSettings()
	}
}
