package pptx

import (
	"image"
	"image/color"
	"math"

	"github.com/btelolabs/btelo-forge/internal/gfx"
	"github.com/btelolabs/btelo-forge/internal/printing"
)

// Printing a slide. The same walk over the shapes as drawing one, but what comes out is the display list: outlines
// keep their curves, gradients stay gradients, pictures keep all their pixels and their crop, and words stay outlines
// even when turned, through a matrix rather than the offscreen picture the screen uses. Pixels are made only for what
// a page description has no word for: blurred shadows, and gradients that are themselves see-through.

// printResolution is the pixels a point of what has to be pixels: about 300 to the inch.
const printResolution = 4

// PrintSlide adds a slide to a page the slide's size, in points.
func (r *Renderer) PrintSlide(pg *printing.Page, s *Slide) {
	x := r.D.slideScope(s)
	number := s.number
	if number == 0 {
		number = r.D.Number(s)
	}
	c := printing.NewCanvas(pg, printing.Identity, r.D.W, r.D.H)
	d := &drawer{r: r, c: c, view: Translate(0, 0), scale: 1, x: x, l: s.Layout, number: number, page: pg}
	pg.ClipRect(0, 0, r.D.W, r.D.H)
	defer pg.Add(&printing.Unclip{})
	root := s.tree().Root
	layout, master := s.Layout, s.Layout.Master
	d.background(root, x, layout, master)
	showLayout := attrBool(root, "showMasterSp", true)
	if showLayout && attrBool(layout.Doc.Root, "showMasterSp", true) {
		d.withScope(masterScope(master), func() { d.decor(master.shapes) })
	}
	if showLayout {
		d.withScope(layoutScope(layout), func() { d.decor(layout.shapes) })
	}
	d.shapes(r.D.Shapes(s))
}

func nrgba(c color.RGBA) color.NRGBA { return color.NRGBA{c.R, c.G, c.B, c.A} }

// pathOf is a shape's path through m, its curves still curves.
func pathOf(ops []PathOp, m Matrix) *printing.Path {
	p := &printing.Path{}
	for i := range ops {
		op := &ops[i]
		switch op.Code {
		case 'M':
			x, y := m.Apply(op.X[0], op.Y[0])
			p.MoveTo(x, y)
		case 'L':
			x, y := m.Apply(op.X[0], op.Y[0])
			p.LineTo(x, y)
		case 'C':
			x1, y1 := m.Apply(op.X[0], op.Y[0])
			x2, y2 := m.Apply(op.X[1], op.Y[1])
			x3, y3 := m.Apply(op.X[2], op.Y[2])
			p.CurveTo(x1, y1, x2, y2, x3, y3)
		case 'Z':
			p.Close()
		}
	}
	return p
}

// polyPath is polylines as a path.
func polyPath(lines []polyline) *printing.Path {
	p := &printing.Path{}
	for _, l := range lines {
		for i, q := range l.pts {
			if i == 0 {
				p.MoveTo(q.x, q.y)
			} else {
				p.LineTo(q.x, q.y)
			}
		}
		if l.closed {
			p.Close()
		}
	}
	return p
}

// fineLines flattens a path much finer than the page's points, for what works on polylines.
func fineLines(ops []PathOp, m Matrix) []polyline {
	const k = 16
	lines := flatten(ops, Scale(k, k).Mul(m))
	for i := range lines {
		for j := range lines[i].pts {
			lines[i].pts[j].x /= k
			lines[i].pts[j].y /= k
		}
	}
	return lines
}

// fillLines lays a fill over polylines, on the canvas or on the page.
func (d *drawer) fillLines(lines []polyline, f *Fill, b Box, m Matrix) {
	if d.page != nil {
		d.printFill(polyPath(lines), f, b, m)
		return
	}
	d.fill(fillMask(lines, d.c.Clip()), f, b, m)
}

// strokeLines lays a fill along polylines, on the canvas or on the page.
func (d *drawer) strokeLines(lines []polyline, st *strokeStyle, f *Fill, b Box, m Matrix) {
	if d.page != nil {
		d.printStroke(polyPath(lines), st, f, b, m)
		return
	}
	d.fill(strokeMask(lines, st, d.c.Clip()), f, b, m)
}

func (d *drawer) printFill(p *printing.Path, f *Fill, b Box, m Matrix) {
	if f == nil || f.None || p.Empty() {
		return
	}
	switch {
	case f.Pic != "":
		if !d.printPicture(p, f, b, m) {
			d.page.Add(&printing.Fill{Path: p, Color: color.NRGBA{204, 204, 204, 255}})
		}
	case len(f.Stops) > 1:
		d.printGradient(p, f, b, m)
	default:
		d.page.Add(&printing.Fill{Path: p, Color: nrgba(f.Color)})
	}
}

func (d *drawer) printStroke(p *printing.Path, st *strokeStyle, f *Fill, b Box, m Matrix) {
	if f == nil || f.None || p.Empty() {
		return
	}
	s := &printing.Stroke{Path: p, Width: st.width, Miter: st.miter, Dash: st.dash}
	switch st.cap {
	case "rnd":
		s.Cap = printing.CapRound
	case "sq":
		s.Cap = printing.CapSquare
	}
	switch st.join {
	case "round":
		s.Join = printing.JoinRound
	case "bevel":
		s.Join = printing.JoinBevel
	}
	if f.Pic == "" && len(f.Stops) < 2 {
		s.Color = nrgba(f.Color)
		d.page.Add(s)
		return
	}
	d.printFill(printing.StrokeOutline(s, printResolution), f, b, m)
}

// printPicture lays a picture through a shape: the whole picture at its own resolution, with the shape as its clip.
func (d *drawer) printPicture(p *printing.Path, f *Fill, b Box, m Matrix) bool {
	pics := d.r.D.pics
	base := pics.base(f.Pic)
	if base == nil {
		return false
	}
	cropW, cropH := 1-f.Crop[0]-f.Crop[2], 1-f.Crop[1]-f.Crop[3]
	if cropW <= 0 || cropH <= 0 {
		return false
	}
	ax, ay := f.Stretch[0]*b.W, f.Stretch[1]*b.H
	aw, ah := b.W*(1-f.Stretch[0]-f.Stretch[2]), b.H*(1-f.Stretch[1]-f.Stretch[3])
	sw, sh := float64(base.Rect.Dx()), float64(base.Rect.Dy())
	if f.Tile != nil {
		aw, ah = sw*0.75*f.Tile.SX, sh*0.75*f.Tile.SY
		ax, ay = f.Tile.X, f.Tile.Y
	}
	if aw <= 0 || ah <= 0 {
		return false
	}
	pic := printing.Picture{Img: base, Alpha: f.Alpha}
	pics.mu.Lock()
	if data := pics.blobs[f.Pic]; len(data) > 2 && data[0] == 0xff && data[1] == 0xd8 && max(sw, sh) < maxPicSide {
		pic.JPEG, pic.W, pic.H = data, base.Rect.Dx(), base.Rect.Dy()
	}
	pics.mu.Unlock()
	d.page.Add(&printing.Clip{Path: p})
	defer d.page.Add(&printing.Unclip{})
	// The picture's whole square, of which the crop fills the area.
	fullW, fullH := aw/cropW, ah/cropH
	place := func(x, y float64) {
		c := pic
		c.M = printing.Matrix(m.Mul(Translate(x-f.Crop[0]*fullW, y-f.Crop[1]*fullH)).Mul(Scale(fullW, fullH)))
		d.page.Add(&c)
	}
	if f.Tile == nil {
		place(ax, ay)
		return true
	}
	// Tiles, over the box the shape fills; a tile too small to be worth its copies is left to the bound.
	const maxTiles = 2500
	x0 := ax - math.Ceil(ax/aw)*aw
	y0 := ay - math.Ceil(ay/ah)*ah
	n := 0
	for y := y0; y < b.H && n < maxTiles; y += ah {
		for x := x0; x < b.W && n < maxTiles; x += aw {
			place(x, y)
			n++
		}
	}
	return true
}

// gradientStops are a fill's stops for the page.
func gradientStops(f *Fill) ([]printing.Stop, bool) {
	out := make([]printing.Stop, len(f.Stops))
	opaque := true
	for i, s := range f.Stops {
		out[i] = printing.Stop{At: s.Pos, Color: nrgba(s.Color)}
		opaque = opaque && s.Color.A == 255
	}
	return out, opaque
}

func (d *drawer) printGradient(p *printing.Path, f *Fill, b Box, m Matrix) {
	stops, opaque := gradientStops(f)
	if !opaque {
		d.printPixels(p, f, b, m)
		return
	}
	if !f.Rotates {
		k := b
		k.Rot, k.FlipH, k.FlipV = 0, false, false
		m = d.view.Mul(k.Local())
	}
	w, h := max(b.W, 1e-6), max(b.H, 1e-6)
	if f.Path == "" {
		ang := f.Angle
		if f.Scaled {
			s, c := math.Sincos(ang * math.Pi / 180)
			ang = math.Atan2(s*h, c*w) * 180 / math.Pi
		}
		s, c := math.Sincos(ang * math.Pi / 180)
		span := math.Abs(w*c) + math.Abs(h*s)
		cx, cy := w/2, h/2
		d.page.Add(&printing.Shade{Path: p, Stops: stops, Extend: [2]bool{true, true}, M: printing.Matrix(m),
			X0: cx - c*span/2, Y0: cy - s*span/2, X1: cx + c*span/2, Y1: cy + s*span/2})
		return
	}
	// A path gradient runs out from a focus rectangle, in contours no gradient of a page description has: they are
	// laid as nested shapes of plain colour, outermost first.
	fx0, fy0 := f.Focus[0]*w, f.Focus[1]*h
	fx1, fy1 := w-f.Focus[2]*w, h-f.Focus[3]*h
	circle := f.Path == "circle"
	const steps = 96
	col := func(t float64) color.NRGBA { return stopColor(stops, t) }
	d.page.Add(&printing.Clip{Path: p})
	defer d.page.Add(&printing.Unclip{})
	d.page.Add(&printing.Fill{Path: printing.RectPath(0, 0, w, h).Transformed(printing.Matrix(m)), Color: col(1)})
	for i := steps; i >= 0; i-- {
		t := float64(i) / steps
		var c *printing.Path
		if circle {
			c = focusEllipse(fx0, fy0, fx1, fy1, w, h, t*math.Sqrt2)
		} else {
			x0, y0 := fx0*(1-t), fy0*(1-t)
			x1, y1 := fx1+(w-fx1)*t, fy1+(h-fy1)*t
			if x1 <= x0 || y1 <= y0 {
				continue
			}
			c = printing.RectPath(x0, y0, x1-x0, y1-y0)
		}
		d.page.Add(&printing.Fill{Path: c.Transformed(printing.Matrix(m)), Color: col(max(t-0.5/steps, 0))})
	}
}

func stopColor(stops []printing.Stop, t float64) color.NRGBA {
	if t <= stops[0].At {
		return stops[0].Color
	}
	for i := 1; i < len(stops); i++ {
		if t < stops[i].At {
			a, b := stops[i-1], stops[i]
			u := (t - a.At) / max(b.At-a.At, 1e-9)
			l := func(x, y uint8) uint8 { return uint8(float64(x)*(1-u) + float64(y)*u + 0.5) }
			return color.NRGBA{l(a.Color.R, b.Color.R), l(a.Color.G, b.Color.G), l(a.Color.B, b.Color.B), l(a.Color.A, b.Color.A)}
		}
	}
	return stops[len(stops)-1].Color
}

// focusEllipse is the contour of a circle gradient: quarter ellipses round the corners of the focus rectangle, each
// reaching the share s of the way to the box's edges.
func focusEllipse(fx0, fy0, fx1, fy1, w, h, s float64) *printing.Path {
	l, r, t, b := fx0*s, (w-fx1)*s, fy0*s, (h-fy1)*s
	const k = 0.5522847498
	p := &printing.Path{}
	p.MoveTo(fx0, fy0-t)
	p.LineTo(fx1, fy0-t)
	p.CurveTo(fx1+r*k, fy0-t, fx1+r, fy0-t*k, fx1+r, fy0)
	p.LineTo(fx1+r, fy1)
	p.CurveTo(fx1+r, fy1+b*k, fx1+r*k, fy1+b, fx1, fy1+b)
	p.LineTo(fx0, fy1+b)
	p.CurveTo(fx0-l*k, fy1+b, fx0-l, fy1+b*k, fx0-l, fy1)
	p.LineTo(fx0-l, fy0)
	p.CurveTo(fx0-l, fy0-t*k, fx0-l*k, fy0-t, fx0, fy0-t)
	p.Close()
	return p
}

// printPixels draws a fill as pixels through its shape, for a see-through gradient.
func (d *drawer) printPixels(p *printing.Path, f *Fill, b Box, m Matrix) {
	box := p.Bounds()
	const k = printResolution
	w, h := int(math.Ceil((box.X1-box.X0)*k))+2, int(math.Ceil((box.Y1-box.Y0)*k))+2
	if w <= 0 || h <= 0 || w*h > 1<<26 {
		return
	}
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	to := Scale(k, k).Mul(Translate(-box.X0, -box.Y0))
	mask := fillMask(flatten(rectOps(1, 1), Matrix{float64(w), 0, 0, float64(h), 0, 0}), img.Rect)
	sub := &drawer{r: d.r, c: gfx.Wrap(img), dst: img, view: to.Mul(d.view), scale: k, x: d.x, l: d.l}
	sub.fill(mask, f, b, to.Mul(m))
	d.page.Add(&printing.Clip{Path: p})
	d.page.Add(&printing.Picture{Img: img, M: printing.Matrix(Translate(box.X0, box.Y0).Mul(Scale(float64(w)/k, float64(h)/k)))})
	d.page.Add(&printing.Unclip{})
}

// printBody draws a shape's fill, shadow and line.
func (d *drawer) printBody(sh *Shape, g Geometry, look *resolved) {
	m := d.view.Mul(sh.Box.Local())
	st, stroke := d.strokeOf(&look.line)
	if look.shadow != nil && (!look.fill.None || stroke) {
		var filled, all []polyline
		for i := range g.Paths {
			p := &g.Paths[i]
			lines := fineLines(p.Ops, m)
			on := p.Fill != "none" && !look.fill.None
			if on {
				filled = append(filled, lines...)
			}
			if on || p.Fill == "none" || p.Stroke {
				all = append(all, lines...)
			}
		}
		d.printShadow(filled, all, &st, stroke, look.shadow)
	}
	for i := range g.Paths {
		p := &g.Paths[i]
		if p.Fill == "none" || look.fill.None {
			continue
		}
		fl := *look.fill
		if p.Fill != "norm" && len(fl.Stops) < 2 && fl.Pic == "" {
			fl.Color = shadeOf(fl.Color, p.Fill)
		}
		d.printFill(pathOf(p.Ops, m), &fl, sh.Box, m)
	}
	if !stroke {
		return
	}
	arrows := look.line.Head.Kind != "" || look.line.Tail.Kind != ""
	for i := range g.Paths {
		p := &g.Paths[i]
		if !p.Stroke {
			continue
		}
		if arrows {
			d.stroke(fineLines(p.Ops, m), &st, &look.line, sh.Box, m)
		} else {
			d.printStroke(pathOf(p.Ops, m), &st, look.line.Fill, sh.Box, m)
		}
	}
}

// printShadow lays a shape's shadow: a shape of its own when it is sharp, a picture with soft edges when it is not.
func (d *drawer) printShadow(filled, stroked []polyline, st *strokeStyle, hasStroke bool, s *Shadow) {
	sin, cos := math.Sincos(s.Dir * math.Pi / 180)
	dx, dy := s.Dist*cos, s.Dist*sin
	if s.Blur <= 0 {
		p := polyPath(filled).Transformed(printing.Translate(dx, dy))
		d.page.Add(&printing.Fill{Path: p, Color: nrgba(s.Color)})
		return
	}
	all := append(append([]polyline(nil), filled...), stroked...)
	x0, y0, x1, y1 := math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)
	for _, l := range all {
		for _, q := range l.pts {
			x0, y0, x1, y1 = min(x0, q.x), min(y0, q.y), max(x1, q.x), max(y1, q.y)
		}
	}
	if x0 > x1 {
		return
	}
	pad := s.Blur*2 + 3 + st.width
	x0, y0, x1, y1 = min(x0, x0+dx)-pad, min(y0, y0+dy)-pad, max(x1, x1+dx)+pad, max(y1, y1+dy)+pad
	k := float64(printResolution)
	for (x1-x0)*k*(y1-y0)*k > 1<<26 && k > 1 {
		k /= 2
	}
	w, h := int(math.Ceil((x1-x0)*k)), int(math.Ceil((y1-y0)*k))
	if w <= 0 || h <= 0 {
		return
	}
	move := func(in []polyline) []polyline {
		out := make([]polyline, len(in))
		for i, l := range in {
			out[i] = polyline{make([]pt, len(l.pts)), l.closed}
			for j, q := range l.pts {
				out[i].pts[j] = pt{(q.x - x0) * k, (q.y - y0) * k}
			}
		}
		return out
	}
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	sub := &drawer{r: d.r, c: gfx.Wrap(img), dst: img, scale: k}
	scaled := *st
	scaled.width *= k
	sub.shadow(move(filled), move(stroked), &scaled, hasStroke, s)
	d.page.Add(&printing.Picture{Img: img, M: printing.Matrix(Translate(x0, y0).Mul(Scale(float64(w)/k, float64(h)/k)))})
}

// printText adds a frame's words. The canvas's pixels are placed well inside its bounds, as a frame's words can start
// above or left of its origin, and the matrix turns them with the shape.
func (d *drawer) printText(f *TextFrame, m Matrix, fg color.RGBA) {
	const px, margin = printing.Pixels, 40000.0
	to := printing.Matrix(m).Mul(printing.Scale(1.0/px, 1.0/px)).Mul(printing.Translate(-margin, -margin))
	c := printing.NewCanvas(d.page, to, 2*margin, 2*margin)
	f.draw(c, Translate(margin, margin).Mul(Scale(px, px)), px, d.r, fg)
}

// printPictureShape draws a picture shape.
func (d *drawer) printPictureShape(sh *Shape, g Geometry, look *resolved, blip *Fill) {
	m := d.view.Mul(sh.Box.Local())
	st, stroke := d.strokeOf(&look.line)
	p := &printing.Path{}
	var lines []polyline
	for i := range g.Paths {
		p.Append(pathOf(g.Paths[i].Ops, m))
		if look.shadow != nil {
			lines = append(lines, fineLines(g.Paths[i].Ops, m)...)
		}
	}
	if look.shadow != nil {
		d.printShadow(lines, lines, &st, stroke, look.shadow)
	}
	if blip == nil || blip.Pic == "" || !d.printPicture(p, blip, sh.Box, m) {
		d.missing(sh, "picture")
	}
	if stroke {
		d.printStroke(p, &st, look.line.Fill, sh.Box, m)
	}
}

// printBackground fills the page with the slide's background.
func (d *drawer) printBackground(f *Fill, w, h float64) {
	d.page.Add(&printing.Fill{Path: printing.RectPath(0, 0, w, h), Color: color.NRGBA{255, 255, 255, 255}})
	if f != nil && !f.None {
		d.printFill(printing.RectPath(0, 0, w, h), f, Box{W: w, H: h}, d.view)
	}
}
