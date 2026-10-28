package mind

import (
	"image"
	"image/color"
	"math"
	"strconv"

	"github.com/btelolabs/btelo-forge/internal/gfx"
)

// Drawing the board. What is drawn costs what shows on the screen, not what the map holds: only what lies in the
// clip is visited, a subtree smaller than a few pixels is a dot of its branch's colour, words too small to read are
// grey bars, and boxes too small to tell apart are filled rectangles. Render only reads the map, so several can draw
// parts of one map at once once it is laid out; nothing else may change it meanwhile.

// View is how the board is shown: a board point x, y is at pixel x·Zoom+OX, y·Zoom+OY.
type View struct{ Zoom, OX, OY float64 }

// Screen is where a board point is shown.
func (v View) Screen(p Vec) (float32, float32) {
	return float32(p.X*v.Zoom + v.OX), float32(p.Y*v.Zoom + v.OY)
}

// ScreenRect is where a board rectangle is shown.
func (v View) ScreenRect(r Rect) gfx.Rect {
	x0, y0 := v.Screen(Vec{r.X0, r.Y0})
	x1, y1 := v.Screen(Vec{r.X1, r.Y1})
	return gfx.Rect{X: x0, Y: y0, W: x1 - x0, H: y1 - y0}
}

// Board is the board point shown at pixel x, y.
func (v View) Board(x, y float64) Vec { return Vec{(x - v.OX) / v.Zoom, (y - v.OY) / v.Zoom} }

// BoardRect is the part of the board shown in a rectangle of pixels.
func (v View) BoardRect(r image.Rectangle) Rect {
	a, b := v.Board(float64(r.Min.X), float64(r.Min.Y)), v.Board(float64(r.Max.X), float64(r.Max.Y))
	return Rect{a.X, a.Y, b.X, b.Y}
}

// PictureState is how far a node's picture is: what is drawn in its place until it is ready.
type PictureState uint8

const (
	PicLoading PictureState = iota
	PicReady
	PicMissing // the file is gone, or cannot be read
	PicCycle   // a nested map that holds, at some depth, the map it is shown in
	PicTooDeep // a nested map past the depth maps are shown to
)

// Pictures gives the pictures nodes show, at about px pixels wide; it is asked from several goroutines at once and
// must not wait for a picture: one not ready yet is fetched in the background and the view drawn again then.
type Pictures interface {
	Picture(n *Node, px int) (img *image.RGBA, state PictureState, animated bool)
}

// Labels are the words drawn in the board's own placeholders, in the user's language.
type Labels struct {
	Missing string // a file that cannot be found
	Cycle   string // a nested map that holds itself
	TooDeep string // a nested map too deep to show
}

// Options say what Render draws and with what.
type Options struct {
	Fonts    gfx.Fonts
	Pictures Pictures // nil draws every picture as loading
	Labels   Labels
	// Skip leaves out these nodes with their subtrees, and SkipEdges these connectors: the window draws them over the
	// rest while they are dragged.
	Skip      map[ID]bool
	SkipEdges map[EdgeID]bool
	// LiveAnimated leaves the frames of animated pictures to the window, which draws them as they play.
	LiveAnimated bool
	Paper        bool // fill the board's colour and its dots first
}

// Glyphs of the icon font drawn on the board.
const (
	glyphPicture = "\ue410"
	glyphVideo   = "\ue04b"
	glyphBroken  = "\ue3ad"
	glyphWarning = "\ue002"
	glyphMap     = "\ue97a"
	glyphSmart   = "\ue3e0"
	glyphPlay    = "\ue037"
)

type renderer struct {
	c    *gfx.Canvas
	m    *Map
	v    View
	o    *Options
	clip gfx.Rect
	z    float32
}

// Render draws the part of the map within the canvas's clip as seen through v. The map must be laid out.
func Render(c *gfx.Canvas, m *Map, v View, o *Options) {
	cl := c.Clip()
	if cl.Empty() {
		return
	}
	r := &renderer{c: c, m: m, v: v, o: o, z: float32(v.Zoom),
		clip: gfx.Rect{X: float32(cl.Min.X), Y: float32(cl.Min.Y), W: float32(cl.Dx()), H: float32(cl.Dy())}}
	if o.Paper {
		r.paper()
	}
	area := v.BoardRect(cl).Grow(4 / v.Zoom)
	var edges []*Edge
	m.edgeGrid.query(area, func(id uint32, _ Rect) {
		if e := m.Edge(EdgeID(id)); e != nil && e.geo.ok && !o.SkipEdges[e.ID] {
			edges = append(edges, e)
		}
	})
	sortEdges(edges)
	for _, e := range edges {
		r.edge(e)
	}
	for _, root := range m.rootsIn(area) {
		if !o.Skip[root.ID] {
			r.tree(root, Vec{root.X, root.Y}, area)
		}
	}
}

// RenderNode draws one node with its subtree at its place, as Render would, over what is drawn already.
func RenderNode(c *gfx.Canvas, m *Map, v View, o *Options, n *Node) {
	cl := c.Clip()
	if cl.Empty() || n == nil {
		return
	}
	r := &renderer{c: c, m: m, v: v, o: o, z: float32(v.Zoom),
		clip: gfx.Rect{X: float32(cl.Min.X), Y: float32(cl.Min.Y), W: float32(cl.Dx()), H: float32(cl.Dy())}}
	r.tree(n, m.Pos(n), v.BoardRect(cl).Grow(4/v.Zoom))
}

// RenderEdge draws one connector over what is drawn already.
func RenderEdge(c *gfx.Canvas, m *Map, v View, o *Options, e *Edge) {
	cl := c.Clip()
	if cl.Empty() || e == nil || !e.geo.ok {
		return
	}
	r := &renderer{c: c, m: m, v: v, o: o, z: float32(v.Zoom),
		clip: gfx.Rect{X: float32(cl.Min.X), Y: float32(cl.Min.Y), W: float32(cl.Dx()), H: float32(cl.Dy())}}
	r.edge(e)
}

// rootsIn is RootsIn without laying out, for drawing.
func (m *Map) rootsIn(r Rect) []*Node {
	var out []*Node
	m.grid.query(r, func(id uint32, b Rect) {
		if n := m.Node(ID(id)); n != nil {
			out = append(out, n)
		}
	})
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].ID < out[j-1].ID; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// paper fills the clip with the board's colour, plain as MindNode's.
func (r *renderer) paper() { r.c.Fill(r.clip, Paper) }

// tree draws n's subtree, n's box being at at on the board.
func (r *renderer) tree(n *Node, at Vec, area Rect) {
	p := &n.lay
	sub := r.v.ScreenRect(p.sub.Add(at))
	if !sub.Intersects(r.clip) {
		return
	}
	if sub.W < 3 && sub.H < 3 {
		col := BranchColor(p.branch)
		if p.depth == 0 && n.Kind == Topic {
			col = p.lk.fill
		}
		r.c.Fill(gfx.Rect{X: sub.X, Y: sub.Y, W: max(sub.W, 1), H: max(sub.H, 1)}, col)
		return
	}
	for side, list := range p.stacks {
		if len(list) == 0 {
			continue
		}
		dir := stackDir(n, side)
		lo, hi := area.Y0-at.Y, area.Y1-at.Y
		if !alongY(dir) {
			lo, hi = area.X0-at.X, area.X1-at.X
		}
		i, j := childRange(list, dir, lo, hi)
		r.branches(n, at, list, dir, i, j)
		for _, c := range list[i:j] {
			if !r.o.Skip[c.ID] {
				r.tree(c, at.Add(c.lay.rel), area)
			}
		}
	}
	r.node(n, at)
}

// curveLimit is the most children a root's branches are drawn to as curves; more are drawn from a trunk.
const curveLimit = 48

// branches draws the lines from n to its children list, those from i to j being in view. As in MindNode each is a
// smooth curve that tapers from the parent's line to the child's and flows into it; a node with very many children
// sends them from a trunk instead, which costs only the children in view.
func (r *renderer) branches(n *Node, at Vec, list []*Node, dir Layout, i, j int) {
	p := &n.lay
	out := at.Add(outAnchor(n, dir))
	if len(list) == 0 {
		return
	}
	w0 := float32(branchWidth(p.depth)) * r.z
	if p.depth == 0 {
		w0 = float32(branchWidth(0)) * r.z
	} else if p.lk.shape == ShapeUnderline {
		w0 = float32(p.lk.lineW) * r.z
	}
	col := func(c *Node) color.RGBA {
		if c.Line.A != 0 {
			return c.Line
		}
		return BranchColor(c.lay.branch)
	}
	childW := func(c *Node) float32 {
		if c.lay.lk.shape == ShapeUnderline {
			return float32(c.lay.lk.lineW) * r.z
		}
		return float32(branchWidth(c.lay.depth)) * r.z
	}
	in := func(c *Node) Vec { return at.Add(c.lay.rel).Add(inAnchor(c, dir)) }
	sx, sy := r.v.Screen(out)
	// From a box the branches start a little inside it, so they leave it as one shape.
	if p.lk.shape != ShapeUnderline && p.lk.fill.A != 0 {
		switch dir {
		case LayoutRight:
			sx -= 6 * r.z
		case LayoutLeft:
			sx += 6 * r.z
		case LayoutDown:
			sy -= 6 * r.z
		case LayoutUp:
			sy += 6 * r.z
		}
	}
	if dir != LayoutList && len(list) <= curveLimit {
		for _, c := range list {
			if r.o.Skip[c.ID] {
				continue
			}
			ex, ey := r.v.Screen(in(c))
			var cp [4]pt
			if dir == LayoutDown || dir == LayoutUp {
				my := (sy + ey) / 2
				cp = [4]pt{{sx, sy}, {sx, my}, {ex, my}, {ex, ey}}
			} else {
				mx := sx + (ex-sx)*0.5
				cp = [4]pt{{sx, sy}, {mx, sy}, {mx, ey}, {ex, ey}}
			}
			r.ribbon(cp, max(w0, childW(c)), childW(c), col(c))
		}
		return
	}
	trunkCol := BranchColor(p.branch)
	if p.depth == 0 {
		trunkCol = edgeColor
	}
	w := max(1, min(w0, 3*r.z))
	first, last := in(list[0]), in(list[len(list)-1])
	stub := func(a, b pt, c *Node) {
		r.ribbon([4]pt{a, a, b, b}, max(1, childW(c)), max(1, childW(c)), col(c))
	}
	switch dir {
	case LayoutRight, LayoutLeft:
		gap := float64(gapBranch)
		if p.depth == 0 {
			gap = gapRoot
		}
		if dir == LayoutLeft {
			gap = -gap
		}
		mx, _ := r.v.Screen(Vec{out.X + gap/2, 0})
		_, fy := r.v.Screen(first)
		_, ly := r.v.Screen(last)
		segment(r.c, pt{sx, sy}, pt{mx, sy}, w, trunkCol)
		segment(r.c, pt{mx, min(fy, sy)}, pt{mx, max(ly, sy)}, w, trunkCol)
		for _, c := range list[i:j] {
			if !r.o.Skip[c.ID] {
				ex, ey := r.v.Screen(in(c))
				stub(pt{mx, ey}, pt{ex, ey}, c)
			}
		}
	case LayoutDown, LayoutUp:
		gap := float64(gapDown)
		if dir == LayoutUp {
			gap = -gap
		}
		_, my := r.v.Screen(Vec{0, out.Y + gap/2})
		fx, _ := r.v.Screen(first)
		lx, _ := r.v.Screen(last)
		segment(r.c, pt{sx, sy}, pt{sx, my}, w, trunkCol)
		segment(r.c, pt{min(fx, sx), my}, pt{max(lx, sx), my}, w, trunkCol)
		for _, c := range list[i:j] {
			if !r.o.Skip[c.ID] {
				ex, ey := r.v.Screen(in(c))
				stub(pt{ex, my}, pt{ex, ey}, c)
			}
		}
	case LayoutList:
		// An outline's rows hang from a line down from the parent, each reached by a rounded corner.
		_, ly := r.v.Screen(last)
		radius := 10 * r.z
		segment(r.c, pt{sx, sy}, pt{sx, ly - radius}, w, trunkCol)
		for _, c := range list[i:j] {
			if r.o.Skip[c.ID] {
				continue
			}
			ex, ey := r.v.Screen(in(c))
			r.ribbon([4]pt{{sx, ey - radius}, {sx, ey}, {sx, ey}, {sx + radius, ey}}, w, w, trunkCol)
			stub(pt{sx + radius, ey}, pt{ex, ey}, c)
		}
	}
}

// ribbon draws a cubic curve through four control points as one smooth band that narrows from w0 to w1 pixels,
// with round ends. The curve is divided finely enough for its length on the screen that no corner shows.
func (r *renderer) ribbon(cp [4]pt, w0, w1 float32, col color.RGBA) {
	m := max(w0, w1)
	minX, minY, maxX, maxY := cp[0].x, cp[0].y, cp[0].x, cp[0].y
	for _, q := range cp[1:] {
		minX, minY, maxX, maxY = min(minX, q.x), min(minY, q.y), max(maxX, q.x), max(maxY, q.y)
	}
	if !(gfx.Rect{X: minX - m, Y: minY - m, W: maxX - minX + 2*m, H: maxY - minY + 2*m}).Intersects(r.clip) {
		return // most bands lie outside a tile
	}
	d := math.Hypot(float64(cp[3].x-cp[0].x), float64(cp[3].y-cp[0].y))
	steps := int(min(max(d/4, 4), 160))
	pts := make([]pt, steps+1)
	for i := range pts {
		t := float32(i) / float32(steps)
		u := 1 - t
		a, b, c, e := u*u*u, 3*u*u*t, 3*u*t*t, t*t*t
		pts[i] = pt{a*cp[0].x + b*cp[1].x + c*cp[2].x + e*cp[3].x, a*cp[0].y + b*cp[1].y + c*cp[2].y + e*cp[3].y}
	}
	// The width eases: it stays near the parent's a little and settles into the child's. Below a pixel the band is
	// kept a pixel wide and faded by what is missing, so thin branches stay smooth rather than break up.
	if m < 1 {
		col = gfx.Alpha(col, max(m, 0.25))
		w0, w1 = 1, 1
	}
	fillPoly(r.c, band(pts, func(t float32) float32 {
		t = t * t * (3 - 2*t)
		return w0 + (w1-w0)*t
	}), col)
}

// node draws n's box and content, its box being at at on the board.
func (r *renderer) node(n *Node, at Vec) {
	p := &n.lay
	box := r.v.ScreenRect(R(0, 0, p.w, p.h).Add(at))
	if !box.Intersects(r.clip) {
		return
	}
	lk := p.lk
	if box.H < 3 || box.W < 3 {
		col := lk.fill
		if col.A == 0 {
			col = lk.line
		}
		if col.A == 0 {
			col = lk.ink
		}
		r.c.Fill(gfx.Rect{X: box.X, Y: box.Y, W: max(box.W, 1), H: max(box.H, 1)}, col)
		return
	}
	switch n.Kind {
	case Topic:
		r.shape(lk, box)
		r.words(p.lines, p.text.Add(at), lk, lk.ink, lk.shape != ShapeUnderline && lk.shape != ShapeNone || n.W > 0)
	case Table:
		r.table(n, at)
		r.words(p.lines, p.caption.Add(at), lk, inkDim, true)
	default:
		r.media(n, at)
		r.words(p.lines, p.caption.Add(at), lk, inkDim, true)
	}
	if n.Folded && len(n.Children) > 0 {
		r.fold(n, at)
	}
}

// shadow draws a soft shadow under a box with corners of radius, as if it floated a little over the board.
func (r *renderer) shadow(box gfx.Rect, radius float32) {
	if box.W < 8 || box.H < 8 {
		return
	}
	s := max(1, 2.5*r.z)
	for i := 4; i >= 1; i-- {
		d := float32(i) * s
		sh := gfx.Rect{X: box.X - d/2, Y: box.Y + s*1.2 - d/2, W: box.W + d, H: box.H + d}
		r.c.RoundRect(sh, radius+d/2, color.RGBA{0, 0, 0, 22})
	}
}

// shape draws a topic's outline, filled.
func (r *renderer) shape(lk look, box gfx.Rect) {
	lw := float32(lk.lineW) * r.z
	if lw > 0 && lw < 1 {
		lw = 1
	}
	switch lk.shape {
	case ShapeNone:
	case ShapeUnderline:
		// The line under the words, as thick as the branch that flows into it, with round ends.
		r.c.RoundRect(gfx.Rect{X: box.X - lw/2, Y: box.Y + box.H - lw, W: box.W + lw, H: lw}, lw/2, lk.line)
	case ShapeRounded, ShapeRect, ShapeCapsule:
		radius := float32(lk.radius) * r.z
		if lk.shape == ShapeCapsule {
			radius = box.H / 2
		}
		radius = min(radius, box.H/2)
		if lk.shadow {
			r.shadow(box, radius)
		}
		r.c.RoundRect(box, radius, lk.fill)
		if lw > 0 && lk.line.A != 0 {
			r.c.RoundOutline(box, radius, lw, lk.line)
		}
	default:
		poly := shapePoly(lk.shape, box)
		if lk.shadow {
			shifted := make([]pt, len(poly))
			for i, q := range poly {
				shifted[i] = pt{q.x, q.y + 2.5*r.z}
			}
			fillPoly(r.c, shifted, color.RGBA{0, 0, 0, 60})
		}
		fillPoly(r.c, poly, lk.fill)
		if lw > 0 && lk.line.A != 0 {
			strokeClosed(r.c, poly, lw, lk.line)
		}
	}
}

// face is the font for words of size board units at the view's zoom; above twelve pixels sizes are whole pixels,
// so zooming does not make a face for every step.
func (r *renderer) face(size float32, bold bool) *gfx.Face {
	px := size * r.z
	if px >= 12 {
		px = float32(math.Round(float64(px)))
	}
	f := r.o.Fonts.Regular
	if bold && r.o.Fonts.Bold != nil {
		f = r.o.Fonts.Bold
	}
	return f.Face(px)
}

// greekBelow is the size in pixels below which words are drawn as bars.
const greekBelow = 4.5

// words draws wrapped lines in the board rectangle rect, centred across or from its left.
func (r *renderer) words(lines []string, rect Rect, lk look, ink color.RGBA, centre bool) {
	if len(lines) == 0 || rect.Empty() {
		return
	}
	box := r.v.ScreenRect(rect)
	if !box.Intersects(r.clip) {
		return
	}
	px := lk.size * r.z
	lineH := float32(r.m.measure.LineHeight(lk.size)) * r.z
	if px < greekBelow || r.o.Fonts.Regular == nil {
		if lineH < 1.2 {
			return
		}
		bar := max(1, px*0.5)
		for i, l := range lines {
			w := float32(r.m.measure.Width(l, lk.size, lk.bold)) * r.z
			x := box.X
			if centre {
				x = box.X + (box.W-w)/2
			}
			r.c.Fill(gfx.Rect{X: x, Y: box.Y + float32(i)*lineH + (lineH-bar)/2, W: max(w, 1), H: bar}, gfx.Alpha(ink, 0.4))
		}
		return
	}
	f := r.face(lk.size, lk.bold)
	r.c.Push(gfx.Rect{X: box.X - 2, Y: box.Y - 2, W: box.W + 4, H: box.H + 4})
	for i, l := range lines {
		y := box.Y + float32(i)*lineH + (lineH-f.Height())/2
		if y > r.clip.Y+r.clip.H || y+lineH < r.clip.Y {
			continue
		}
		x := box.X
		if centre {
			x = box.X + (box.W-f.Measure(l))/2
		}
		r.c.Text(f, x, y, l, ink)
	}
	r.c.Pop()
}

// icon draws a glyph of the icon font centred in box, size pixels tall.
func (r *renderer) icon(glyph string, box gfx.Rect, size float32, col color.RGBA) {
	if r.o.Fonts.Icons == nil || size < 6 {
		return
	}
	f := r.o.Fonts.Icons.Face(float32(math.Round(float64(size))))
	w := f.Measure(glyph)
	r.c.Text(f, box.X+(box.W-w)/2, box.Y+(box.H-f.Height())/2, glyph, col)
}

// label draws one line of words centred in box, size pixels tall, cut short to fit.
func (r *renderer) label(text string, box gfx.Rect, size float32, col color.RGBA) {
	if text == "" || size < greekBelow || r.o.Fonts.Regular == nil {
		return
	}
	f := r.o.Fonts.Regular.Face(float32(math.Round(float64(size))))
	text = f.Ellipsize(text, box.W-4)
	r.c.Text(f, box.X+(box.W-f.Measure(text))/2, box.Y+(box.H-f.Height())/2, text, col)
}

// media draws what a picture, video, smart object or nested map shows.
func (r *renderer) media(n *Node, at Vec) {
	p := &n.lay
	box := r.v.ScreenRect(R(0, 0, p.w, p.h).Add(at))
	mr := r.v.ScreenRect(p.media.Add(at))
	radius := float32(p.lk.radius) * r.z
	r.shadow(box, radius)
	r.c.RoundRect(box, radius, p.lk.fill)
	r.c.Push(box) // the picture keeps to the card's corners
	defer r.c.Pop()
	var img *image.RGBA
	state, animated := PicLoading, false
	if r.o.Pictures != nil {
		img, state, animated = r.o.Pictures.Picture(n, int(math.Ceil(float64(mr.W))))
	}
	small := min(mr.W, mr.H)
	glyph := glyphPicture
	switch n.Kind {
	case Video:
		glyph = glyphVideo
	case Nested:
		glyph = glyphMap
	case Smart:
		glyph = glyphSmart
	}
	switch {
	case state == PicReady && img != nil:
		if !(animated && r.o.LiveAnimated) {
			r.c.Push(mr)
			r.picture(img, fitIn(img.Rect.Dx(), img.Rect.Dy(), mr), radius)
			r.c.Pop()
		}
		if n.Kind == Video && small >= 24 {
			d := min(small*0.3, 56)
			c := gfx.Rect{X: mr.X + (mr.W-d)/2, Y: mr.Y + (mr.H-d)/2, W: d, H: d}
			r.c.RoundRect(c, d/2, color.RGBA{0, 0, 0, 150})
			r.icon(glyphPlay, c, d*0.7, color.RGBA{255, 255, 255, 235})
		}
	case state == PicCycle || state == PicTooDeep:
		r.c.RoundRect(mr, radius, warnFill)
		r.c.RoundOutline(mr, radius, max(1, 1.5*r.z), warnLine)
		text := r.o.Labels.Cycle
		if state == PicTooDeep {
			text = r.o.Labels.TooDeep
		}
		ic := min(small*0.38, 64)
		r.icon(glyphWarning, gfx.Rect{X: mr.X, Y: mr.Y + mr.H*0.18, W: mr.W, H: ic}, ic, warnLine)
		r.label(text, gfx.Rect{X: mr.X, Y: mr.Y + mr.H*0.18 + ic, W: mr.W, H: mr.H * 0.3}, min(14*r.z, mr.H*0.12), warnLine)
		return
	case state == PicMissing:
		ic := min(small*0.34, 56)
		r.icon(glyphBroken, gfx.Rect{X: mr.X, Y: mr.Y + mr.H*0.22, W: mr.W, H: ic}, ic, inkDim)
		r.label(r.o.Labels.Missing, gfx.Rect{X: mr.X, Y: mr.Y + mr.H*0.22 + ic, W: mr.W, H: mr.H * 0.25}, min(13*r.z, mr.H*0.11), inkDim)
	default:
		r.icon(glyph, mr, min(small*0.34, 56), gfx.Alpha(inkDim, 0.6))
	}
	if (n.Kind == Smart || n.Kind == Nested) && small >= 40 {
		d := min(22*r.z, small*0.2)
		chip := gfx.Rect{X: mr.X + 6*r.z, Y: mr.Y + 6*r.z, W: d, H: d}
		r.c.RoundRect(chip, d*0.25, color.RGBA{0, 0, 0, 160})
		r.icon(glyph, chip, d*0.72, color.RGBA{255, 255, 255, 230})
	}
	if p.lk.line.A != 0 {
		r.c.RoundOutline(box, radius, max(1, float32(p.lk.lineW)*r.z), p.lk.line)
	}
}

// picture draws img scaled into dst with its corners rounded to radius pixels, as the card around it is.
func (r *renderer) picture(img *image.RGBA, dst gfx.Rect, radius float32) {
	px := dst.Pixels()
	if radius < 2 || px.Dx() < 8 || px.Dy() < 8 || px.Dx()*px.Dy() > 4<<20 {
		r.c.Image(img, dst)
		return
	}
	if px.Intersect(r.c.Clip()).Empty() {
		return
	}
	tmp := image.NewRGBA(image.Rectangle{Max: px.Size()})
	gfx.Wrap(tmp).Image(img, gfx.Rect{W: float32(px.Dx()), H: float32(px.Dy())})
	gfx.RoundCorners(tmp, radius)
	r.c.Blit(tmp, px.Min.X, px.Min.Y, false)
}

// fitIn is the largest rectangle of w × h proportions centred in box.
func fitIn(w, h int, box gfx.Rect) gfx.Rect {
	if w <= 0 || h <= 0 {
		return box
	}
	s := min(box.W/float32(w), box.H/float32(h))
	fw, fh := float32(w)*s, float32(h)*s
	return gfx.Rect{X: box.X + (box.W-fw)/2, Y: box.Y + (box.H-fh)/2, W: fw, H: fh}
}

// table draws a table's grid and cells.
func (r *renderer) table(n *Node, at Vec) {
	p := &n.lay
	mr := r.v.ScreenRect(p.media.Add(at))
	r.shadow(mr, 6*r.z)
	r.c.RoundRect(mr, 6*r.z, boxFill)
	g := n.Cells
	if g == nil || len(p.cols) == 0 || len(p.rows) == 0 {
		return
	}
	line := gfx.Alpha(boxLine, 0.9)
	head := r.v.ScreenRect(R(0, 0, p.media.W(), p.rows[0]).Add(at))
	r.c.Fill(head, gfx.Mix(boxFill, boxLine, 0.35))
	px := p.lk.size * r.z
	lineH := float32(p.lineH) * r.z
	var f *gfx.Face
	if px >= greekBelow && r.o.Fonts.Regular != nil {
		f = r.face(p.lk.size, false)
	}
	y := 0.0
	for row, h := range p.rows {
		x := 0.0
		for col, w := range p.cols {
			cell := r.v.ScreenRect(R(x, y, w, h).Add(at))
			if cell.Intersects(r.clip) {
				i := row*g.Cols + col
				text := p.cellLn[i]
				_, numErr := strconv.ParseFloat(p.cells[i], 64)
				right := numErr == nil
				pad := float32(cellPadX) * r.z
				inner := gfx.Rect{X: cell.X + pad, Y: cell.Y + float32(cellPadY)*r.z, W: cell.W - 2*pad, H: cell.H}
				for k, l := range text {
					ly := inner.Y + float32(k)*lineH
					if f == nil {
						if lineH >= 1.2 {
							lw := float32(r.m.measure.Width(l, p.lk.size, false)) * r.z
							bx := inner.X
							if right {
								bx = inner.X + inner.W - lw
							}
							r.c.Fill(gfx.Rect{X: bx, Y: ly + lineH*0.3, W: max(lw, 1), H: max(1, px*0.5)}, gfx.Alpha(inkLight, 0.35))
						}
						continue
					}
					lx := inner.X
					if right {
						lx = inner.X + inner.W - f.Measure(l)
					}
					r.c.Push(cell)
					r.c.Text(f, lx, ly+(lineH-f.Height())/2, l, inkLight)
					r.c.Pop()
				}
			}
			x += w
		}
		y += h
	}
	lw := max(1, r.z)
	x := 0.0
	for _, w := range p.cols[:len(p.cols)-1] {
		x += w
		sx, _ := r.v.Screen(at.Add(Vec{x, 0}))
		r.c.Fill(gfx.Rect{X: sx, Y: mr.Y, W: lw, H: mr.H}, line)
	}
	y = 0
	for _, h := range p.rows[:len(p.rows)-1] {
		y += h
		_, sy := r.v.Screen(at.Add(Vec{0, y}))
		r.c.Fill(gfx.Rect{X: mr.X, Y: sy, W: mr.W, H: lw}, line)
	}
	r.c.RoundOutline(mr, 6*r.z, lw, boxLine)
}

// fold draws the count of a folded node's children where its branches would leave: a dot of its branch's colour.
func (r *renderer) fold(n *Node, at Vec) {
	b := r.v.ScreenRect(foldMark(n).Add(at))
	col := BranchColor(n.lay.branch)
	if n.lay.depth == 0 {
		col = edgeColor
	}
	r.c.Circle(b.X+b.W/2, b.Y+b.H/2, b.H/2, col)
	r.label(strconv.Itoa(len(n.Children)), b, b.H*0.6, Paper)
}

// edge draws a connector with its arrowheads and label.
func (r *renderer) edge(e *Edge) {
	g := &e.geo
	box := r.v.ScreenRect(g.box)
	if !box.Intersects(r.clip) {
		return
	}
	col := e.Color
	if col.A == 0 {
		col = edgeColor
	}
	w := max(1, 1.75*r.z)
	size := max(5, arrowLen*r.z)
	var line []pt
	if e.Route == RouteCurve && len(g.pts) == 4 {
		a, b := r.v.Screen(g.pts[0])
		c, d := r.v.Screen(g.pts[3])
		steps := int(min(max(math.Hypot(float64(c-a), float64(d-b))/8, 8), 96))
		for _, v := range polyline(RouteCurve, g.pts, steps) {
			x, y := r.v.Screen(v)
			line = append(line, pt{x, y})
		}
	} else {
		for _, v := range g.pts {
			x, y := r.v.Screen(v)
			line = append(line, pt{x, y})
		}
	}
	if len(line) < 2 {
		return
	}
	head := func(end bool) {
		tip, from := line[len(line)-1], line[len(line)-2]
		if !end {
			tip, from = line[0], line[1]
		}
		dx, dy := tip.x-from.x, tip.y-from.y
		d := float32(math.Hypot(float64(dx), float64(dy)))
		if d == 0 {
			return
		}
		dx, dy = dx/d, dy/d
		arrowHead(r.c, tip, dx, dy, size, col)
		// The line stops inside the head, so its end does not show past the tip.
		back := pt{tip.x - dx*size*0.6, tip.y - dy*size*0.6}
		if end {
			line[len(line)-1] = back
		} else {
			line[0] = back
		}
	}
	if e.Arrow == ArrowEnd || e.Arrow == ArrowBoth {
		head(true)
	}
	if e.Arrow == ArrowStart || e.Arrow == ArrowBoth {
		head(false)
	}
	if e.Dashed {
		dashed(r.c, line, w, col, max(4, 7*r.z), max(3, 5*r.z))
	} else {
		strokeLine(r.c, line, w, col)
	}
	if e.Label != "" {
		px := float32(13) * r.z
		if px < greekBelow || r.o.Fonts.Regular == nil {
			return
		}
		f := r.face(13, false)
		tw := f.Measure(e.Label)
		x, y := r.v.Screen(g.label)
		lb := gfx.Rect{X: x - tw/2 - 6*r.z, Y: y - f.Height()/2 - 3*r.z, W: tw + 12*r.z, H: f.Height() + 6*r.z}
		r.c.RoundRect(lb, 4*r.z, Paper)
		r.c.RoundOutline(lb, 4*r.z, max(1, r.z), gfx.Alpha(col, 0.6))
		r.c.Text(f, x-tw/2, y-f.Height()/2, e.Label, inkLight)
	}
}

// RenderFit draws the whole map into a w × h picture, fitted with a margin, and returns it; an empty map is the
// board's colour alone. The map is laid out first.
func RenderFit(m *Map, w, h int, o *Options) *image.RGBA {
	m.Layout()
	c := gfx.NewCanvas(w, h)
	c.Clear(Paper)
	b := m.Bounds()
	if b.Empty() {
		return c.Img
	}
	z := math.Min(float64(w)*0.92/b.W(), float64(h)*0.92/b.H())
	z = math.Min(z, 2)
	v := View{Zoom: z, OX: float64(w)/2 - b.CX()*z, OY: float64(h)/2 - b.CY()*z}
	opts := *o
	opts.Paper = false
	Render(c, m, v, &opts)
	return c.Img
}
