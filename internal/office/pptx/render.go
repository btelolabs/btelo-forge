package pptx

import (
	"image"
	"image/color"
	"math"
	"strings"

	"github.com/btelolabs/btelo-forge/internal/gfx"
	"github.com/btelolabs/btelo-forge/internal/office/chart"
	"github.com/btelolabs/btelo-forge/internal/office/flow"
	"github.com/btelolabs/btelo-forge/internal/office/opc"
	"github.com/btelolabs/btelo-forge/internal/office/ox"
	"github.com/btelolabs/btelo-forge/internal/printing"
)

// Renderer draws slides; the editor, the thumbnails, the slideshow and the render tool all come through here. It is
// safe to use on any goroutine with a frozen slide (Slide.Freeze), as the masters and layouts are never edited.
type Renderer struct {
	D     *Doc
	Fonts flow.Fonts
	// Prompt is what an empty placeholder says in the editor.
	Prompt func(ph *Ph) string
	// Label names a chart, diagram or object for its placeholder.
	Label func(kind string) string
}

// View places a slide on a canvas: pixels are points times Scale, plus the offset.
type View struct {
	Scale, X, Y float64
}

// Matrix is the view as a matrix from points to pixels.
func (v View) Matrix() Matrix { return Translate(v.X, v.Y).Mul(Scale(v.Scale, v.Scale)) }

// Options say what a drawing is for.
type Options struct {
	Prompts bool // show empty placeholders' prompts, as the editor does, never the slideshow
	NoText  int  // the id of a shape whose words the caller draws itself, 0 for none
}

// drawer is one drawing under way.
type drawer struct {
	r      *Renderer
	c      *gfx.Canvas
	dst    *image.RGBA
	view   Matrix
	scale  float64
	x      *scope
	l      *Layout
	number int
	opt    Options
	page   *printing.Page // when set, what is drawn goes here and dst is unused
}

// Slide draws a slide. The canvas's clip bounds what is touched.
func (r *Renderer) Slide(c *gfx.Canvas, s *Slide, v View, o Options) {
	x := r.D.slideScope(s)
	number := s.number
	if number == 0 {
		number = r.D.Number(s)
	}
	d := &drawer{r: r, c: c, dst: c.Img, view: v.Matrix(), scale: v.Scale, x: x, l: s.Layout, number: number, opt: o}
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

// Layout draws a layout as a picker shows it: its decoration, with its placeholders as boxes of their prompts.
func (r *Renderer) Layout(c *gfx.Canvas, l *Layout, v View) {
	x := layoutScope(l)
	d := &drawer{r: r, c: c, dst: c.Img, view: v.Matrix(), scale: v.Scale, x: x, l: l, opt: Options{Prompts: true}}
	d.background(l.Doc.Root, x, nil, l.Master)
	if attrBool(l.Doc.Root, "showMasterSp", true) {
		d.withScope(masterScope(l.Master), func() { d.decor(l.Master.shapes) })
	}
	d.decor(l.shapes)
	for _, s := range l.shapes {
		if s.Ph != nil && family(s.Ph.Type) != "dt" && family(s.Ph.Type) != "ftr" && family(s.Ph.Type) != "sldNum" {
			d.outline(s.Box, color.RGBA{170, 170, 170, 255})
		}
	}
}

func (d *drawer) withScope(x *scope, fn func()) {
	old := d.x
	d.x = x
	fn()
	d.x = old
}

// decor draws a master's or layout's shapes that show on its slides: everything but its placeholders.
func (d *drawer) decor(list []*Shape) {
	for _, s := range list {
		if s.Ph == nil && !s.Hidden {
			d.shape(s)
		}
	}
}

func (d *drawer) shapes(list []*Shape) {
	for _, s := range list {
		if !s.Hidden {
			d.shape(s)
		}
	}
}

// outline draws the thin frame of a shape that has nothing to show.
func (d *drawer) outline(b Box, col color.RGBA) {
	ops := rectOps(b.W, b.H)
	lines := flatten(ops, d.view.Mul(b.Local()))
	d.strokeLines(lines, &strokeStyle{width: 1, dash: []float64{4, 3}, cap: "flat", join: "miter", miter: 4}, &Fill{Color: col}, Box{}, Identity)
}

func rectOps(w, h float64) []PathOp {
	return []PathOp{{Code: 'M'}, {Code: 'L', X: [3]float64{w}}, {Code: 'L', X: [3]float64{w}, Y: [3]float64{h}}, {Code: 'L', Y: [3]float64{h}}, {Code: 'Z'}}
}

// background fills the slide with the first background found on the way up to the master.
func (d *drawer) background(root *ox.Node, x *scope, l *Layout, m *Master) {
	type level struct {
		n *ox.Node
		x *scope
	}
	levels := []level{{root, x}}
	if l != nil {
		levels = append(levels, level{l.Doc.Root, layoutScope(l)})
	}
	levels = append(levels, level{m.Doc.Root, masterScope(m)})
	var f *Fill
	for _, lv := range levels {
		bg := lv.n.Path("p:cSld", "p:bg")
		if bg == nil {
			continue
		}
		if pr := bg.Child("p:bgPr"); pr != nil {
			f = lv.x.readFill(fillChild(pr))
		} else if ref := bg.Child("p:bgRef"); ref != nil {
			idx := int(attrInt(ref, "idx", 0)) - 1000
			theme := lv.x.col.theme
			if idx >= 1 && idx <= len(theme.Backgrounds) {
				k := *lv.x
				if c, ok := lv.x.col.resolve(first(ref)); ok {
					k.col = lv.x.col.withPh(c)
				}
				k.rels, k.part = theme.Rels, theme.Part
				f = k.readFill(theme.Backgrounds[idx-1])
			} else if c, ok := lv.x.col.resolve(first(ref)); ok {
				f = &Fill{Color: c}
			}
		}
		if f != nil {
			break
		}
	}
	w, h := d.r.D.W, d.r.D.H
	if d.page != nil {
		d.printBackground(f, w, h)
		return
	}
	lines := flatten(rectOps(w, h), d.view)
	mask := fillMask(lines, d.c.Clip())
	composite(d.dst, mask, color.RGBA{255, 255, 255, 255}, nil, 1)
	if f != nil && !f.None {
		d.fill(mask, f, Box{W: w, H: h}, d.view)
	}
}

// fill lays a fill through a mask; m maps the box's own coordinates to pixels.
func (d *drawer) fill(mask maskArea, f *Fill, b Box, m Matrix) {
	if f == nil || f.None || mask.img == nil {
		return
	}
	switch {
	case f.Pic != "":
		fn, ok := d.pictureFn(f, b, m)
		if !ok {
			composite(d.dst, mask, color.RGBA{204, 204, 204, 255}, nil, 1)
			return
		}
		composite(d.dst, mask, color.RGBA{}, fn, f.Alpha)
	case len(f.Stops) > 1:
		composite(d.dst, mask, color.RGBA{}, gradientFn(f, b, m, d.view), 1)
	default:
		composite(d.dst, mask, f.Color, nil, 1)
	}
}

// gradientFn is a paint for a gradient across the box.
func gradientFn(f *Fill, b Box, m, view Matrix) paintFn {
	var lut [256]color.RGBA
	stops := f.Stops
	for i := range lut {
		t := float64(i) / 255
		c := stops[len(stops)-1].Color
		for k := range stops {
			if t < stops[k].Pos {
				if k == 0 {
					c = stops[0].Color
				} else {
					a, e := stops[k-1], stops[k]
					u := (t - a.Pos) / max(e.Pos-a.Pos, 1e-9)
					c = gfx.Mix(a.Color, e.Color, float32(u))
				}
				break
			}
		}
		ca := uint32(c.A)
		lut[i] = color.RGBA{uint8(uint32(c.R) * ca / 255), uint8(uint32(c.G) * ca / 255), uint8(uint32(c.B) * ca / 255), c.A}
	}
	if !f.Rotates {
		k := b
		k.Rot, k.FlipH, k.FlipV = 0, false, false
		m = view.Mul(k.Local())
	}
	inv := m.Inverse()
	w, h := max(b.W, 1e-6), max(b.H, 1e-6)
	if f.Path == "" {
		ang := f.Angle
		if f.Scaled {
			// Scaled angles are measured in a square box stretched to the shape.
			s, c := math.Sincos(ang * math.Pi / 180)
			ang = math.Atan2(s*h, c*w) * 180 / math.Pi
		}
		s, c := math.Sincos(ang * math.Pi / 180)
		span := math.Abs(w*c) + math.Abs(h*s)
		cx, cy := w/2, h/2
		return func(x, y int) color.RGBA {
			lx := inv[0]*(float64(x)+0.5) + inv[2]*(float64(y)+0.5) + inv[4]
			ly := inv[1]*(float64(x)+0.5) + inv[3]*(float64(y)+0.5) + inv[5]
			t := ((lx-cx)*c+(ly-cy)*s)/span + 0.5
			return lut[int(clampF(t, 0, 1)*255+0.5)]
		}
	}
	// A path gradient runs from the focus rectangle out to the box's edge.
	fx0, fy0 := f.Focus[0]*w, f.Focus[1]*h
	fx1, fy1 := w-f.Focus[2]*w, h-f.Focus[3]*h
	circle := f.Path == "circle"
	return func(x, y int) color.RGBA {
		lx := inv[0]*(float64(x)+0.5) + inv[2]*(float64(y)+0.5) + inv[4]
		ly := inv[1]*(float64(x)+0.5) + inv[3]*(float64(y)+0.5) + inv[5]
		dx := outside(lx, fx0, fx1, w)
		dy := outside(ly, fy0, fy1, h)
		var t float64
		if circle {
			t = math.Hypot(dx, dy) / math.Sqrt2 // the circle reaches the box's corners
		} else {
			t = max(dx, dy)
		}
		return lut[int(clampF(t, 0, 1)*255+0.5)]
	}
}

// outside is how far v is beyond the range lo..hi, as a fraction of the way to the edge at 0 or end.
func outside(v, lo, hi, end float64) float64 {
	switch {
	case v < lo:
		if lo <= 0 {
			return 0
		}
		return (lo - v) / lo
	case v > hi:
		if hi >= end {
			return 0
		}
		return (v - hi) / (end - hi)
	}
	return 0
}

// pictureFn is a paint for a picture across the box.
func (d *drawer) pictureFn(f *Fill, b Box, m Matrix) (paintFn, bool) {
	pics := d.r.D.pics
	base := pics.base(f.Pic)
	if base == nil {
		return nil, false
	}
	sw, sh := float64(base.Rect.Dx()), float64(base.Rect.Dy())
	cropW := sw * (1 - f.Crop[0] - f.Crop[2])
	cropH := sh * (1 - f.Crop[1] - f.Crop[3])
	if cropW <= 0 || cropH <= 0 {
		return nil, false
	}
	// The area the picture is stretched into, in the box's coordinates.
	ax, ay := f.Stretch[0]*b.W, f.Stretch[1]*b.H
	aw, ah := b.W*(1-f.Stretch[0]-f.Stretch[2]), b.H*(1-f.Stretch[1]-f.Stretch[3])
	if f.Tile != nil {
		// Sized as printed at 96 dots to the inch.
		aw, ah = sw*0.75*f.Tile.SX, sh*0.75*f.Tile.SY
		ax, ay = f.Tile.X, f.Tile.Y
	}
	if aw <= 0 || ah <= 0 {
		return nil, false
	}
	// A level of the picture near the size shown, so that shrinking does not alias.
	px := math.Hypot(m[0], m[1]) * aw
	py := math.Hypot(m[2], m[3]) * ah
	shrink := min(cropW/max(px, 1), cropH/max(py, 1))
	level := 0
	for shrink >= 2 && level < 8 {
		shrink /= 2
		level++
	}
	img := pics.level(f.Pic, level)
	if img == nil {
		return nil, false
	}
	lw, lh := float64(img.Rect.Dx()), float64(img.Rect.Dy())
	fx, fy := lw/sw, lh/sh
	// From pixels on the canvas to pixels of the level.
	imgFromBox := Translate(f.Crop[0]*sw*fx, f.Crop[1]*sh*fy).Mul(Scale(cropW*fx/aw, cropH*fy/ah)).Mul(Translate(-ax, -ay))
	return samplerFn(img, imgFromBox.Mul(m.Inverse()), f.Tile != nil), true
}

// samplerFn paints a premultiplied picture through a matrix from canvas pixels to picture pixels, with bilinear
// filtering. Outside the picture it is clear, unless it tiles.
func samplerFn(img *image.RGBA, inv Matrix, tile bool) paintFn {
	w, h := img.Rect.Dx(), img.Rect.Dy()
	fw, fh := float64(w), float64(h)
	return func(x, y int) color.RGBA {
		px := inv[0]*(float64(x)+0.5) + inv[2]*(float64(y)+0.5) + inv[4]
		py := inv[1]*(float64(x)+0.5) + inv[3]*(float64(y)+0.5) + inv[5]
		if tile {
			px = math.Mod(math.Mod(px, fw)+fw, fw)
			py = math.Mod(math.Mod(py, fh)+fh, fh)
		} else if px < 0 || py < 0 || px >= fw || py >= fh {
			return color.RGBA{}
		}
		px -= 0.5
		py -= 0.5
		x0, y0 := int(math.Floor(px)), int(math.Floor(py))
		tx := uint32((px - float64(x0)) * 256)
		ty := uint32((py - float64(y0)) * 256)
		x1, y1 := min(x0+1, w-1), min(y0+1, h-1)
		x0, y0 = max(x0, 0), max(y0, 0)
		s := img.Stride
		a := img.Pix[y0*s+x0*4:]
		b := img.Pix[y0*s+x1*4:]
		c := img.Pix[y1*s+x0*4:]
		e := img.Pix[y1*s+x1*4:]
		mix := func(k int) uint8 {
			top := uint32(a[k])*(256-tx) + uint32(b[k])*tx
			bot := uint32(c[k])*(256-tx) + uint32(e[k])*tx
			return uint8((top*(256-ty) + bot*ty) >> 16)
		}
		return color.RGBA{mix(0), mix(1), mix(2), mix(3)}
	}
}

// strokeOf is a line's style in pixels, and false when it draws nothing.
func (d *drawer) strokeOf(l *Line) (strokeStyle, bool) {
	if l.Fill == nil || l.Fill.None {
		return strokeStyle{}, false
	}
	w := max(l.Width*d.scale, 0.75)
	st := strokeStyle{width: w, cap: l.Cap, join: l.Join, miter: l.Miter}
	if st.cap == "" {
		st.cap = "flat"
	}
	if st.join == "" {
		st.join = "round"
	}
	if st.miter == 0 {
		st.miter = 8
	}
	for _, v := range l.Dash {
		st.dash = append(st.dash, v*max(l.Width*d.scale, 1))
	}
	return st, true
}

// geometryOf is the shape's outline.
func geometryOf(sh *Shape) Geometry {
	pr := spPr(sh)
	w, h := sh.Box.W, sh.Box.H
	if cg := pr.Child("a:custGeom"); cg != nil {
		return customGeometry(cg, w, h)
	}
	if pg := pr.Child("a:prstGeom"); pg != nil {
		return presetGeometry(pg.Attr("prst"), w, h, adjustsOf(pg.Child("a:avLst")))
	}
	for _, k := range sh.Chain {
		kp := spPr(k)
		if cg := kp.Child("a:custGeom"); cg != nil {
			return customGeometry(cg, w, h)
		}
		if pg := kp.Child("a:prstGeom"); pg != nil {
			return presetGeometry(pg.Attr("prst"), w, h, adjustsOf(pg.Child("a:avLst")))
		}
	}
	return presetGeometry("rect", w, h, nil)
}

func (d *drawer) shape(sh *Shape) {
	switch sh.Kind {
	case KindGroup:
		d.shapes(sh.Kids)
	case KindShape, KindConnector:
		d.plain(sh)
	case KindPicture:
		d.picture(sh)
	case KindTable:
		d.table(sh)
	case KindGraphic:
		d.graphic(sh)
	}
}

// visible reports that the shape's box touches the clip; a shape wholly outside costs nothing.
func (d *drawer) visible(b Box, pad float64) bool {
	x, y, w, h := b.Bounds()
	x0, y0 := d.view.Apply(x, y)
	x1, y1 := d.view.Apply(x+w, y+h)
	clip := d.c.Clip()
	p := pad * d.scale
	return x1+p >= float64(clip.Min.X) && y1+p >= float64(clip.Min.Y) && x0-p <= float64(clip.Max.X) && y0-p <= float64(clip.Max.Y)
}

func shadeOf(c color.RGBA, mode string) color.RGBA {
	switch mode {
	case "lighten":
		return gfx.Mix(c, color.RGBA{255, 255, 255, c.A}, 0.4)
	case "lightenLess":
		return gfx.Mix(c, color.RGBA{255, 255, 255, c.A}, 0.2)
	case "darken":
		return gfx.Mix(c, color.RGBA{0, 0, 0, c.A}, 0.4)
	case "darkenLess":
		return gfx.Mix(c, color.RGBA{0, 0, 0, c.A}, 0.2)
	}
	return c
}

func (d *drawer) plain(sh *Shape) {
	if !d.visible(sh.Box, 40) {
		return
	}
	look := d.r.D.look(sh, d.x, d.l)
	g := geometryOf(sh)
	d.body(sh, g, &look)
	d.words(sh, g)
}

// body draws a shape's fill, shadow and line.
func (d *drawer) body(sh *Shape, g Geometry, look *resolved) {
	if d.page != nil {
		d.printBody(sh, g, look)
		return
	}
	m := d.view.Mul(sh.Box.Local())
	clip := d.c.Clip()
	st, stroke := d.strokeOf(&look.line)
	type flat struct {
		lines  []polyline
		path   *Path
		filled bool
	}
	var flats []flat
	var all []polyline
	for i := range g.Paths {
		p := &g.Paths[i]
		lines := flatten(p.Ops, m)
		f := flat{lines: lines, path: p, filled: p.Fill != "none" && !look.fill.None}
		flats = append(flats, f)
		if f.filled || p.Fill == "none" || p.Stroke {
			all = append(all, lines...)
		}
	}
	if look.shadow != nil && (!look.fill.None || stroke) {
		var src []polyline
		for _, f := range flats {
			if f.filled {
				src = append(src, f.lines...)
			}
		}
		d.shadow(src, all, &st, stroke, look.shadow)
	}
	for _, f := range flats {
		if !f.filled {
			continue
		}
		mask := fillMask(f.lines, clip)
		fl := *look.fill
		if f.path.Fill != "norm" && len(fl.Stops) < 2 && fl.Pic == "" {
			fl.Color = shadeOf(fl.Color, f.path.Fill)
		}
		d.fill(mask, &fl, sh.Box, m)
	}
	if !stroke {
		return
	}
	for _, f := range flats {
		if !f.path.Stroke {
			continue
		}
		d.stroke(f.lines, &st, &look.line, sh.Box, m)
	}
}

// stroke draws lines with their arrowheads.
func (d *drawer) stroke(lines []polyline, st *strokeStyle, l *Line, b Box, m Matrix) {
	if l.Head.Kind != "" || l.Tail.Kind != "" {
		lines = append([]polyline(nil), lines...)
		for i := range lines {
			if !lines[i].closed {
				lines[i].pts = append([]pt(nil), lines[i].pts...)
			}
		}
		for i := range lines {
			if lines[i].closed || len(lines[i].pts) < 2 {
				continue
			}
			if i == 0 && l.Head.Kind != "" {
				d.arrow(&lines[i], true, l.Head, st, l)
			}
			if i == len(lines)-1 && l.Tail.Kind != "" {
				d.arrow(&lines[i], false, l.Tail, st, l)
			}
		}
	}
	d.strokeLines(lines, st, l.Fill, b, m)
}

// arrow draws an arrowhead at an end of a polyline, and pulls the line back under it.
func (d *drawer) arrow(line *polyline, atStart bool, a Arrow, st *strokeStyle, l *Line) {
	pts := line.pts
	end, prev := pts[len(pts)-1], pts[len(pts)-2]
	if atStart {
		end, prev = pts[0], pts[1]
	}
	w := max(st.width, 2)
	hl, hw := a.L*w, a.W*w
	dx, dy := end.x-prev.x, end.y-prev.y
	s := math.Hypot(dx, dy)
	if s == 0 {
		return
	}
	dx, dy = dx/s, dy/s
	nx, ny := -dy, dx
	base := pt{end.x - dx*hl, end.y - dy*hl}
	var poly []pt
	pull := hl * 0.9
	switch a.Kind {
	case "triangle":
		poly = []pt{end, {base.x + nx*hw/2, base.y + ny*hw/2}, {base.x - nx*hw/2, base.y - ny*hw/2}}
	case "stealth":
		mid := pt{end.x - dx*hl*0.65, end.y - dy*hl*0.65}
		poly = []pt{end, {base.x + nx*hw/2, base.y + ny*hw/2}, mid, {base.x - nx*hw/2, base.y - ny*hw/2}}
		pull = hl * 0.6
	case "diamond":
		poly = []pt{{end.x + dx*hl/2, end.y + dy*hl/2}, {end.x + nx*hw/2, end.y + ny*hw/2}, {end.x - dx*hl/2, end.y - dy*hl/2}, {end.x - nx*hw/2, end.y - ny*hw/2}}
		pull = 0
	case "oval":
		n := 24
		for i := 0; i < n; i++ {
			t := float64(i) * 2 * math.Pi / float64(n)
			c, sn := math.Cos(t)*hl/2, math.Sin(t)*hw/2
			poly = append(poly, pt{end.x + dx*c + nx*sn, end.y + dy*c + ny*sn})
		}
		pull = 0
	case "arrow":
		// Two strokes rather than a solid head.
		left := pt{base.x + nx*hw/2, base.y + ny*hw/2}
		right := pt{base.x - nx*hw/2, base.y - ny*hw/2}
		k := strokeStyle{width: st.width, cap: "rnd", join: "round", miter: 4}
		d.strokeLines([]polyline{{pts: []pt{left, end, right}}}, &k, l.Fill, Box{}, Identity)
		return
	default:
		return
	}
	d.fillLines([]polyline{{pts: poly, closed: true}}, l.Fill, Box{}, Identity)
	if pull > 0 {
		p := pt{end.x - dx*pull, end.y - dy*pull}
		if atStart {
			line.pts[0] = p
		} else {
			line.pts[len(pts)-1] = p
		}
	}
}

// shadow draws an outer shadow of what is filled and stroked.
func (d *drawer) shadow(filled, stroked []polyline, st *strokeStyle, hasStroke bool, s *Shadow) {
	sin, cos := math.Sincos(s.Dir * math.Pi / 180)
	dx, dy := s.Dist*cos*d.scale, s.Dist*sin*d.scale
	blurPx := s.Blur * d.scale
	clip := d.c.Clip()
	// The source may be just outside the canvas and still cast onto it.
	area := clip.Add(image.Pt(int(-dx), int(-dy))).Inset(-int(blurPx*2) - 2)
	all := append(append([]polyline(nil), filled...), stroked...)
	pad := blurPx*2 + 3 + st.width
	b := pixelBounds(all, pad, area)
	if b.Empty() || b.Dx()*b.Dy() > 1<<26 {
		return
	}
	full := image.NewAlpha(b)
	lay := func(m maskArea) {
		if m.img == nil {
			return
		}
		r := m.img.Rect.Add(m.at).Intersect(b)
		for y := r.Min.Y; y < r.Max.Y; y++ {
			src := m.img.Pix[(y-m.at.Y)*m.img.Stride+(r.Min.X-m.at.X):]
			out := full.Pix[(y-b.Min.Y)*full.Stride+(r.Min.X-b.Min.X):]
			for i := 0; i < r.Dx(); i++ {
				out[i] = max(out[i], src[i])
			}
		}
	}
	lay(fillMask(filled, b))
	if hasStroke {
		lay(strokeMask(stroked, st, b))
	}
	full.Rect = image.Rect(0, 0, b.Dx(), b.Dy())
	blur(full, blurPx)
	composite(d.dst, maskArea{full, b.Min.Add(image.Pt(int(math.Round(dx)), int(math.Round(dy))))}, s.Color, nil, 1)
}

// words draws a shape's words, or in the editor the prompt of an empty placeholder.
func (d *drawer) words(sh *Shape, g Geometry) {
	if sh.body() == nil || sh.ID == d.opt.NoText {
		return
	}
	var prompt string
	empty := sh.EmptyPlaceholder()
	if empty {
		if !d.opt.Prompts {
			return
		}
		prompt = d.r.promptFor(sh)
		if prompt == "" {
			return
		}
	} else if !sh.HasText() {
		return
	}
	src := d.r.source(sh, d.x, d.l, d.number)
	if empty {
		tx := sh.body().Clone()
		for _, p := range tx.Elements("a:p") {
			p.Remove()
		}
		p := ox.New("a:p")
		if orig := sh.body().Child("a:p"); orig != nil && orig.Child("a:pPr") != nil {
			p.Append(orig.Child("a:pPr").Clone())
		}
		p.Append(ox.New("a:r").Append(ox.New("a:t").Append(ox.NewText(prompt))))
		tx.Append(p)
		src.tx = tx
		src.extra = &RunProps{Color: color.RGBA{128, 128, 128, 255}, HasColor: true}
	}
	f := d.r.frame(sh, g, src)
	d.text(f, d.defaultInk())
}

func (d *drawer) defaultInk() color.RGBA {
	c, ok := d.x.col.scheme("tx1")
	if !ok {
		return color.RGBA{0, 0, 0, 255}
	}
	return c
}

// source gathers what the shape's words inherit.
func (r *Renderer) source(sh *Shape, x *scope, l *Layout, number int) *textSource {
	src := &textSource{tx: sh.body(), lists: r.D.textChain(sh, x, l), x: x, number: number}
	src.own = listRef{sh.body().Child("a:lstStyle"), x}
	src.font = sh.Node.Path("p:style", "a:fontRef")
	src.major = sh.Ph != nil && family(sh.Ph.Type) == "title"
	return src
}

// bodyChain lists the a:bodyPr elements that apply, the shape's own first.
func bodyChain(sh *Shape) bodyProps {
	b := bodyProps{chain: []*ox.Node{sh.body().Child("a:bodyPr")}}
	for _, k := range sh.Chain {
		b.chain = append(b.chain, k.Node.Path("p:txBody", "a:bodyPr"))
	}
	return b
}

func (r *Renderer) frame(sh *Shape, g Geometry, src *textSource) *TextFrame {
	t := g.Text
	return r.layoutFrame(src, bodyChain(sh), frameArgs{t[0], t[1], t[2] - t[0], t[3] - t[1], sh.Box.Upright()})
}

// Frame is the layout of a shape's words on a slide, for the editor. It is nil for a shape that holds none.
func (r *Renderer) Frame(s *Slide, sh *Shape) *TextFrame {
	if sh.body() == nil {
		return nil
	}
	return r.frame(sh, geometryOf(sh), r.source(sh, r.D.slideScope(s), s.Layout, r.D.Number(s)))
}

func (r *Renderer) promptFor(sh *Shape) string {
	for _, k := range sh.Chain {
		if attrBool(k.Node.Path("p:nvSpPr", "p:nvPr", "p:ph"), "hasCustomPrompt", false) {
			if t := paraText(k.body().Child("a:p")); t != "" {
				return t
			}
		}
	}
	if r.Prompt != nil {
		return r.Prompt(sh.Ph)
	}
	return ""
}

func paraText(p *ox.Node) string {
	var b strings.Builder
	for _, t := range p.All("a:t") {
		b.WriteString(t.InnerText())
	}
	return b.String()
}

// text paints a frame. Upright words go straight on the canvas; turned ones are drawn upright at twice the size, off
// the canvas, and turned with bilinear filtering, which stays sharp where nearest-pixel glyphs would crawl.
func (d *drawer) text(f *TextFrame, fg color.RGBA) {
	m := d.view.Mul(f.Matrix)
	if d.page != nil {
		d.printText(f, m, fg)
		return
	}
	if math.Abs(m[1]) < 1e-6 && math.Abs(m[2]) < 1e-6 && m[0] > 0 && m[3] > 0 {
		f.draw(d.c, m, d.scale, d.r, fg)
		return
	}
	x0, y0, x1, y1 := f.Rect[0], f.Rect[1], f.Rect[0]+f.Rect[2], f.Rect[1]+f.Rect[3]
	for i := range f.Paras {
		p := &f.Paras[i]
		for _, s := range p.Spans {
			y0, y1 = min(y0, s.Y), max(y1, s.Y+float64(p.Laid.Height))
			x0 = min(x0, s.X)
			if p.Laid.Marker != nil {
				x0 = min(x0, s.X+float64(p.Laid.Marker.X))
			}
			for _, l := range p.Laid.Lines {
				x1 = max(x1, s.X+float64(l.Right))
			}
		}
	}
	x0, y0, x1, y1 = x0-2, y0-2, x1+2, y1+2
	ss := d.scale * 2
	for (x1-x0)*ss > 8192 || (y1-y0)*ss > 8192 {
		ss /= 2
		if ss < d.scale/8 {
			return
		}
	}
	w, h := int(math.Ceil((x1-x0)*ss)), int(math.Ceil((y1-y0)*ss))
	if w <= 0 || h <= 0 {
		return
	}
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	f.draw(gfx.Wrap(img), Scale(ss, ss).Mul(Translate(-x0, -y0)), ss, d.r, fg)
	toCanvas := m.Mul(Translate(x0, y0)).Mul(Scale(1/ss, 1/ss))
	mask := fillMask(flatten(rectOps(float64(w), float64(h)), toCanvas), d.c.Clip())
	composite(d.dst, mask, color.RGBA{}, samplerFn(img, toCanvas.Inverse(), false), 1)
}

func (d *drawer) picture(sh *Shape) {
	if !d.visible(sh.Box, 40) {
		return
	}
	look := d.r.D.look(sh, d.x, d.l)
	g := geometryOf(sh)
	blip := d.x.readFill(sh.Node.Child("p:blipFill"))
	if d.page != nil {
		d.printPictureShape(sh, g, &look, blip)
		return
	}
	m := d.view.Mul(sh.Box.Local())
	var lines []polyline
	for i := range g.Paths {
		lines = append(lines, flatten(g.Paths[i].Ops, m)...)
	}
	st, stroke := d.strokeOf(&look.line)
	if look.shadow != nil {
		d.shadow(lines, lines, &st, stroke, look.shadow)
	}
	mask := fillMask(lines, d.c.Clip())
	shown := false
	if blip != nil && blip.Pic != "" {
		if fn, ok := d.pictureFn(blip, sh.Box, m); ok {
			composite(d.dst, mask, color.RGBA{}, fn, blip.Alpha)
			shown = true
		}
	}
	if !shown {
		d.missing(sh, "picture")
	}
	if stroke {
		d.stroke(lines, &st, &look.line, sh.Box, m)
	}
}

// missing draws the box of something that cannot be shown here but stays in the file.
func (d *drawer) missing(sh *Shape, kind string) {
	m := d.view.Mul(sh.Box.Local())
	lines := flatten(rectOps(sh.Box.W, sh.Box.H), m)
	d.fillLines(lines, &Fill{Color: color.RGBA{236, 238, 242, 255}}, Box{}, Identity)
	d.outline(sh.Box, color.RGBA{150, 156, 170, 255})
	if d.r.Label == nil {
		return
	}
	text := d.r.Label(kind)
	if text == "" {
		return
	}
	size := float32(min(max(sh.Box.H*0.09, 9), 18))
	para := flow.Para{Align: flow.Center, Runs: []flow.Run{{Text: text, Style: flow.Style{Size: size, Color: color.RGBA{96, 102, 116, 255}}}}}
	laid := flow.Layout(&para, float32(sh.Box.W), d.r.Fonts)
	x, y := float64(0), (sh.Box.H-float64(laid.Height))/2
	f := &TextFrame{Rect: [4]float64{0, y, sh.Box.W, float64(laid.Height)}, Matrix: sh.Box.Upright(),
		Paras: []ParaFrame{{Laid: laid, Spans: []Span{{0, len(laid.Lines), x, y}}}}}
	d.text(f, color.RGBA{96, 102, 116, 255})
}

// graphicKind names what a frame holds.
func graphicKind(sh *Shape) string {
	data := sh.Node.Path("a:graphic", "a:graphicData")
	uri := data.Attr("uri")
	switch {
	case strings.Contains(uri, "chart"):
		return "chart"
	case strings.Contains(uri, "diagram"):
		return "diagram"
	case strings.Contains(uri, "ole"):
		return "object"
	}
	return "graphic"
}

// diagramDrawingRel is the relationship from a slide to the drawing Office keeps of a SmartArt diagram.
const diagramDrawingRel = "http://schemas.microsoft.com/office/2007/relationships/diagramDrawing"

// graphic draws a frame's picture stand-in where the file has one, and otherwise says what is there.
func (d *drawer) graphic(sh *Shape) {
	if !d.visible(sh.Box, 40) {
		return
	}
	data := sh.Node.Path("a:graphic", "a:graphicData")
	// An embedded object keeps a picture of itself.
	for _, ole := range data.All("p:oleObj") {
		if pic := ole.Child("p:pic"); pic != nil {
			s := *sh
			s.Node, s.Kind, s.Chain = pic, KindPicture, nil
			d.picture(&s)
			return
		}
	}
	if graphicKind(sh) == "diagram" && d.diagram(sh) {
		return
	}
	if graphicKind(sh) == "chart" && d.chart(sh) {
		return
	}
	d.missing(sh, graphicKind(sh))
}

// chart draws the chart a frame names, and reports that it could. On paper it goes through a recording canvas, finer
// than a point, as words are measured better there; the shapes it records are the same.
func (d *drawer) chart(sh *Shape) bool {
	ref := sh.Node.Path("a:graphic", "a:graphicData").All("c:chart")
	if len(ref) == 0 {
		return false
	}
	rel, ok := opc.RelByID(d.x.rels, ref[0].Attr("r:id"))
	if !ok || rel.External {
		return false
	}
	name := opc.Resolve(d.x.part, rel.Target)
	got, ok := d.r.D.charts.Load(name)
	if !ok {
		pics := d.r.D.pics
		pics.mu.Lock()
		data := pics.blobs[name]
		pics.mu.Unlock()
		var ch *chart.Chart
		if parsed, err := chart.Parse(data, chart.Source{Colors: d.x.col.scheme, Plain: true}); err == nil {
			ch = parsed
		}
		got, _ = d.r.D.charts.LoadOrStore(name, ch)
	}
	ch := got.(*chart.Chart)
	if ch == nil {
		return false
	}
	faces := chart.FacesOf(d.r.Fonts)
	b := sh.Box
	if d.page != nil {
		const k = printing.Pixels
		c := printing.NewCanvas(d.page, printing.Scale(1.0/k, 1.0/k), d.r.D.W*k, d.r.D.H*k)
		chart.Draw(c, gfx.Rect{X: float32(b.X * k), Y: float32(b.Y * k), W: float32(b.W * k), H: float32(b.H * k)}, k, ch, faces)
		return true
	}
	x0, y0 := d.view.Apply(b.X, b.Y)
	chart.Draw(d.c, gfx.Rect{X: float32(x0), Y: float32(y0), W: float32(b.W * d.scale), H: float32(b.H * d.scale)}, float32(d.scale), ch, faces)
	return true
}

// diagram draws the shapes Office saved of a SmartArt diagram, whose markup is a slide tree's in another namespace.
func (d *drawer) diagram(sh *Shape) bool {
	pics := d.r.D.pics
	for _, r := range d.x.rels {
		if r.Type != diagramDrawingRel {
			continue
		}
		name := opc.Resolve(d.x.part, r.Target)
		pics.mu.Lock()
		data, relData := pics.blobs[name], pics.blobs[relsPart(name)]
		pics.mu.Unlock()
		doc, err := parseBytes(name, data)
		if err != nil {
			return false
		}
		tree := doc.Root.Child("dsp:spTree")
		if tree == nil {
			return false
		}
		tree.Walk(func(n *ox.Node) bool {
			if strings.HasPrefix(n.Name, "dsp:") {
				n.Name = "p:" + strings.TrimPrefix(n.Name, "dsp:")
			}
			return true
		})
		// The drawing's own pictures are relationships of its part.
		x := *d.x
		x.part = name
		x.rels = parseRels(relData)
		list := readTree(tree, nil, groupFrame{sx: 1, sy: 1, box: Box{X: sh.Box.X, Y: sh.Box.Y, W: sh.Box.W, H: sh.Box.H}, centered: true}, nil, 0)
		d.withScope(&x, func() { d.shapes(list) })
		return true
	}
	return false
}

func parseRels(data []byte) []opc.Rel {
	if data == nil {
		return nil
	}
	doc, err := ox.Parse(data)
	if err != nil {
		return nil
	}
	var out []opc.Rel
	for _, e := range doc.Root.Elements("Relationship") {
		out = append(out, opc.Rel{ID: e.Attr("Id"), Type: e.Attr("Type"), Target: e.Attr("Target"), External: e.Attr("TargetMode") == "External"})
	}
	return out
}

// PresetIcon is a small picture of a preset, drawn in a colour on nothing.
func PresetIcon(name string, size int, col color.RGBA) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	s := float64(size)
	g := presetGeometry(name, s-2, s-2, nil)
	m := Translate(1, 1)
	st := strokeStyle{width: 1.2, cap: "flat", join: "round", miter: 4}
	for i := range g.Paths {
		p := &g.Paths[i]
		lines := flatten(p.Ops, m)
		if p.Fill != "none" {
			composite(img, fillMask(lines, img.Rect), color.RGBA{col.R, col.G, col.B, 110}, nil, 1)
		}
		if p.Stroke {
			composite(img, strokeMask(lines, &st, img.Rect), col, nil, 1)
		}
	}
	return img
}

// HasTransition reports that the file gives the slide a transition, which the slideshow plays as a fade.
func (s *Slide) HasTransition() bool {
	root := s.tree().Root
	if root.Child("p:transition") != nil {
		return true
	}
	for _, alt := range root.Elements("mc:AlternateContent") {
		if len(alt.All("p:transition")) > 0 {
			return true
		}
	}
	return false
}
