package chart

import (
	"image"
	"image/color"
	"math"
	"strconv"
	"strings"

	"github.com/btelolabs/btelo-forge/internal/gfx"
	"github.com/btelolabs/btelo-forge/internal/office/formula"
)

// Faces gives the face of a family at a size in pixels.
type Faces func(family string, bold, italic bool, size float32) *gfx.Face

// SeriesName is what a series with no name of its own is called, from one. The window sets it to the language in use.
var SeriesName = func(n int) string { return "Series" + strconv.Itoa(n) }

// formats keeps number formats parsed.
type formats struct {
	date1904 bool
	parsed   map[string]*formula.Format
}

func (f *formats) number(code string, v float64) string {
	if code == "" || strings.EqualFold(code, "General") {
		return formula.General(v, 0)
	}
	if f.parsed == nil {
		f.parsed = map[string]*formula.Format{}
	}
	p := f.parsed[code]
	if p == nil {
		p = formula.ParseFormat(code)
		f.parsed[code] = p
	}
	return strings.TrimSpace(p.Render(formula.Num(v), f.date1904).Text)
}

// painter draws one chart; lengths are points inside the chart, k pixels to the point.
type painter struct {
	c     *gfx.Canvas
	ch    *Chart
	box   gfx.Rect
	k     float32
	w, h  float32
	faces Faces
	fmt   formats
	lines map[int64]*axisView
	axes  []*axisView // the same, in the order the plots name them
}

// Draw draws the chart in r, which is at k pixels to the chart's point. A canvas of pixels and a recording one get
// the same drawing.
func Draw(c *gfx.Canvas, r gfx.Rect, k float32, ch *Chart, faces Faces) {
	if r.W < 2 || r.H < 2 || k <= 0 {
		return
	}
	p := &painter{c: c, ch: ch, box: r, k: k, w: r.W / k, h: r.H / k, faces: faces, fmt: formats{date1904: ch.Date1904}, lines: map[int64]*axisView{}}
	c.Push(r)
	defer c.Pop()
	p.frame()
	p.layout()
}

// FacesOf is the faces of a set of fonts; a chart that names no family gets Office's usual one.
func FacesOf(fonts interface {
	Font(family string, bold, italic bool) *gfx.Font
}) Faces {
	return func(family string, bold, italic bool, size float32) *gfx.Face {
		if family == "" {
			family = "Calibri"
		}
		return fonts.Font(family, bold, italic).Face(size)
	}
}

// Cache keeps the last picture of a chart, so that a frame costs a copy. A recording canvas is drawn on directly.
type Cache struct {
	img  *image.RGBA
	ch   *Chart
	w, h int
}

// Draw draws the chart in r through the cache.
func (ca *Cache) Draw(c *gfx.Canvas, r gfx.Rect, k float32, ch *Chart, faces Faces) {
	if c.Recorder() != nil {
		Draw(c, r, k, ch, faces)
		return
	}
	w, h := int(math.Ceil(float64(r.W))), int(math.Ceil(float64(r.H)))
	if w < 2 || h < 2 || w > 8192 || h > 8192 {
		return
	}
	if ca.img == nil || ca.ch != ch || ca.w != w || ca.h != h {
		off := gfx.NewCanvas(w, h)
		Draw(off, gfx.Rect{W: r.W, H: r.H}, k, ch, faces)
		ca.img, ca.ch, ca.w, ca.h = off.Img, ch, w, h
	}
	c.Blit(ca.img, int(math.Round(float64(r.X))), int(math.Round(float64(r.Y))), false)
}

func (p *painter) x(v float32) float32 { return p.box.X + v*p.k }
func (p *painter) y(v float32) float32 { return p.box.Y + v*p.k }

func (p *painter) rect(r gfx.Rect) gfx.Rect {
	return gfx.Rect{X: p.x(r.X), Y: p.y(r.Y), W: r.W * p.k, H: r.H * p.k}
}

func (p *painter) face(t *Text) *gfx.Face {
	size := t.Size
	if size <= 0 {
		size = 10
	}
	return p.faces(t.Family, t.Bold, t.Italic, max(size*p.k, 1))
}

func ink(t *Text) color.RGBA {
	if t.HasColor {
		return t.Color
	}
	return color.RGBA{89, 89, 89, 255}
}

func (p *painter) measure(t *Text, s string) float32 { return p.face(t).Measure(s) / p.k }

func (p *painter) lineHeight(t *Text) float32 { return p.face(t).Height() / p.k }

// text writes s with its top-left at x, y.
func (p *painter) text(t *Text, x, y float32, s string) {
	p.c.Text(p.face(t), p.x(x), p.y(y), s, ink(t))
}

// centred writes s around x, y.
func (p *painter) centred(t *Text, x, y float32, s string) {
	p.text(t, x-p.measure(t, s)/2, y-p.lineHeight(t)/2, s)
}

// turned writes s around x, y, turned clockwise by degrees.
func (p *painter) turned(t *Text, x, y float32, s string, deg float64) {
	a := deg * math.Pi / 180
	sin, cos := math.Sincos(a)
	w, h := float64(p.measure(t, s)), float64(p.lineHeight(t))
	ox := float64(x) - (w/2*cos - h/2*sin)
	oy := float64(y) - (w/2*sin + h/2*cos)
	p.c.TextTurned(p.face(t), p.x(float32(ox)), p.y(float32(oy)), s, ink(t), a)
}

func (p *painter) frame() {
	ch := p.ch
	r := gfx.Rect{W: p.w, H: p.h}
	fill, line := ch.Fill, ch.Line
	if !ch.Plain {
		if !fill.Set {
			fill = Fill{Color: color.RGBA{255, 255, 255, 255}, Set: true}
		}
		if !line.Set {
			line = Stroke{Color: color.RGBA{217, 217, 217, 255}, Width: 0.75, Set: true}
		}
	}
	radius := float32(0)
	if ch.Rounded {
		radius = 8
	}
	if fill.Set && !fill.None {
		if radius > 0 {
			p.c.RoundRect(p.rect(r), radius*p.k, fill.Color)
		} else {
			p.c.Fill(p.rect(r), fill.Color)
		}
	}
	if line.Set && !line.None {
		w := max(line.Width, 0.5) * p.k
		if radius > 0 {
			p.c.RoundOutline(p.rect(r), radius*p.k, w, line.Color)
		} else {
			p.c.Outline(p.rect(r), w, line.Color)
		}
	}
}

// layout places the title, the legend and the axes, and draws them and the plots.
func (p *painter) layout() {
	ch := p.ch
	const pad = 7
	room := gfx.Rect{X: pad, Y: pad, W: p.w - 2*pad, H: p.h - 2*pad}
	if t := ch.Title; t != nil && strings.TrimSpace(t.Words) != "" {
		used := p.title(t, room)
		if !t.Overlay {
			room.Y += used
			room.H -= used
		}
	}
	if l := ch.Legend; l != nil {
		room = p.legend(l, room)
	}
	if room.W < 8 || room.H < 8 {
		return
	}
	p.buildAxes()
	inner := p.innerArea(room)
	round := false
	for _, pl := range ch.Plots {
		round = round || pl.Kind == Pie || pl.Kind == Doughnut || pl.Kind == Radar
	}
	if ch.PlotFill.Set && !ch.PlotFill.None && !round {
		p.c.Fill(p.rect(inner), ch.PlotFill.Color)
	}
	p.grids(inner)
	if ch.PlotLine.Set && !ch.PlotLine.None && !round {
		p.c.Outline(p.rect(inner), max(ch.PlotLine.Width, 0.5)*p.k, ch.PlotLine.Color)
	}
	p.c.Push(p.rect(gfx.Rect{X: inner.X - 1, Y: inner.Y - 1, W: inner.W + 2, H: inner.H + 2}))
	for _, pl := range ch.Plots {
		if pl.Kind == Area {
			p.drawPlot(pl, inner)
		}
	}
	for _, pl := range ch.Plots {
		if pl.Kind == Bar {
			p.drawPlot(pl, inner)
		}
	}
	for _, pl := range ch.Plots {
		if pl.Kind != Area && pl.Kind != Bar {
			p.drawPlot(pl, inner)
		}
	}
	p.c.Pop()
	p.drawAxes(inner)
	for _, pl := range ch.Plots {
		p.plotLabels(pl, inner)
	}
}

// title draws the chart's title and says how much room it took.
func (p *painter) title(t *Title, room gfx.Rect) float32 {
	f := p.face(&t.Text)
	var lines []string
	for _, l := range strings.Split(t.Words, "\n") {
		lines = append(lines, f.Wrap(l, room.W*p.k)...)
	}
	lh := p.lineHeight(&t.Text)
	y := room.Y
	if t.Layout.Set {
		y = float32(t.Layout.Y) * p.h
	}
	for _, l := range lines {
		x := room.X + (room.W-p.measure(&t.Text, l))/2
		if t.Layout.Set {
			x = float32(t.Layout.X) * p.w
		}
		p.text(&t.Text, x, y, l)
		y += lh
	}
	return float32(len(lines))*lh + 6
}

// legendItem is one line of the legend.
type legendItem struct {
	name  string
	plot  *Plot
	s     *Series
	point int // a point of a varied series, or -1
}

func (p *painter) legendItems(l *Legend) []legendItem {
	var out []legendItem
	at := 0
	for _, pl := range p.ch.Plots {
		vary := pl.Vary && len(pl.Series) == 1 || pl.Kind == Pie || pl.Kind == Doughnut
		for _, s := range pl.Series {
			if vary {
				for i := range s.Vals {
					if !l.Hidden[at] {
						name := ""
						if i < len(s.Cats) {
							name = s.Cats[i]
						}
						if name == "" {
							name = strconv.Itoa(i + 1)
						}
						out = append(out, legendItem{name, pl, s, i})
					}
					at++
				}
				break
			}
			if !l.Hidden[at] {
				name := s.Name
				if name == "" {
					name = SeriesName(s.Index + 1)
				}
				out = append(out, legendItem{name, pl, s, -1})
			}
			at++
		}
	}
	return out
}

// legend draws the legend and gives the room left beside it.
func (p *painter) legend(l *Legend, room gfx.Rect) gfx.Rect {
	items := p.legendItems(l)
	if len(items) == 0 {
		return room
	}
	lh := p.lineHeight(&l.Text) + 3
	key := max(lh*0.62, 6)
	gap := float32(4)
	wide := make([]float32, len(items))
	longest := float32(0)
	for i, it := range items {
		wide[i] = key + gap + p.measure(&l.Text, it.name)
		longest = max(longest, wide[i])
	}
	side := l.Pos == "r" || l.Pos == "l" || l.Pos == "tr"
	var rows [][]int
	var box gfx.Rect
	if side {
		longest = min(longest+2, room.W*0.4)
		for i := range items {
			rows = append(rows, []int{i})
		}
		rows = rows[:min(len(rows), max(int(room.H/lh), 1))]
		box = gfx.Rect{W: longest, H: float32(len(rows)) * lh}
		box.Y = room.Y + (room.H-box.H)/2
		if l.Pos == "l" {
			box.X = room.X
		} else {
			box.X = room.Right() - box.W
		}
		if l.Pos == "tr" {
			box.Y = room.Y
		}
	} else {
		var row []int
		used := float32(0)
		for i := range items {
			w := wide[i] + 10
			if len(row) > 0 && used+w > room.W {
				rows = append(rows, row)
				row, used = nil, 0
			}
			row = append(row, i)
			used += w
		}
		rows = append(rows, row)
		rows = rows[:min(len(rows), max(int(room.H/3/lh), 1))]
		box = gfx.Rect{X: room.X, W: room.W, H: float32(len(rows)) * lh}
		box.Y = room.Bottom() - box.H
		if l.Pos == "t" {
			box.Y = room.Y
		}
	}
	if l.Layout.Set {
		box.X, box.Y = float32(l.Layout.X)*p.w, float32(l.Layout.Y)*p.h
		if l.Layout.Sized {
			box.W, box.H = float32(l.Layout.W)*p.w, float32(l.Layout.H)*p.h
		}
	}
	if l.Fill.Set && !l.Fill.None {
		p.c.Fill(p.rect(box), l.Fill.Color)
	}
	if l.Line.Set && !l.Line.None {
		p.c.Outline(p.rect(box), max(l.Line.Width, 0.5)*p.k, l.Line.Color)
	}
	for r, row := range rows {
		y := box.Y + float32(r)*lh
		x := box.X
		if !side {
			total := float32(0)
			for _, i := range row {
				total += wide[i] + 10
			}
			x += (box.W - total + 10) / 2
		}
		for _, i := range row {
			it := items[i]
			p.legendKey(it, gfx.Rect{X: x, Y: y + (lh-key)/2, W: key, H: key})
			room := box.Right() - x - key - gap
			name := it.name
			if side {
				name = p.face(&l.Text).Ellipsize(name, room*p.k)
			}
			p.text(&l.Text, x+key+gap, y+(lh-p.lineHeight(&l.Text))/2, name)
			x += wide[i] + 10
		}
	}
	if l.Overlay {
		return room
	}
	switch {
	case side && l.Pos == "l":
		room.X += box.W + 8
		room.W -= box.W + 8
	case side:
		room.W -= box.W + 8
	case l.Pos == "t":
		room.Y += box.H + 6
		room.H -= box.H + 6
	default:
		room.H -= box.H + 6
	}
	return room
}

func (p *painter) legendKey(it legendItem, r gfx.Rect) {
	pl, s := it.plot, it.s
	fill := p.seriesFill(pl, s, it.point)
	if pl.Kind == Line || pl.Kind == Scatter || pl.Kind == Radar && !pl.Filled {
		line := p.seriesLine(pl, s)
		y := r.Y + r.H/2
		if !line.None {
			p.segment(r.X-1, y, r.Right()+1, y, line)
		}
		m := p.seriesMarker(pl, s, it.point)
		if m.Symbol != "none" && m.Symbol != "" {
			m.Size = min(m.Size, r.H)
			p.marker(r.X+r.W/2, y, m)
		}
		return
	}
	if !fill.None {
		p.c.Fill(p.rect(r), fill.Color)
	}
	if s.Line.Set && !s.Line.None {
		p.c.Outline(p.rect(r), max(s.Line.Width, 0.5)*p.k, s.Line.Color)
	}
}

// accent is the theme colour a series or point gets by its place: the six accents, then darker and lighter again.
func (p *painter) accent(i int) color.RGBA {
	if i < 0 {
		i = 0
	}
	c, ok := p.ch.colors("accent" + strconv.Itoa(i%6+1))
	if !ok {
		c = color.RGBA{68, 114, 196, 255}
	}
	switch (i / 6) % 4 {
	case 1:
		return gfx.Mix(c, color.RGBA{0, 0, 0, 255}, 0.4)
	case 2:
		return gfx.Mix(c, color.RGBA{255, 255, 255, 255}, 0.4)
	case 3:
		return gfx.Mix(c, color.RGBA{0, 0, 0, 255}, 0.2)
	}
	return c
}

func (p *painter) seriesFill(pl *Plot, s *Series, point int) Fill {
	if point >= 0 {
		if st := s.Points[point]; st != nil && st.Fill.Set {
			return st.Fill
		}
		if pl.Vary {
			return Fill{Color: p.accent(point), Set: true}
		}
	}
	if s.Fill.Set {
		return s.Fill
	}
	return Fill{Color: p.accent(s.Index), Set: true}
}

func (p *painter) seriesLine(pl *Plot, s *Series) Stroke {
	l := s.Line
	if !l.Set {
		l.Color, l.Set = p.accent(s.Index), true
		// An older file's line with no ln has a colour from its fill.
		if s.Fill.Set && !s.Fill.None {
			l.Color = s.Fill.Color
		}
	}
	if l.Width <= 0 {
		l.Width = 2.25
	}
	return l
}

var markerCycle = []string{"diamond", "square", "triangle", "x", "star", "circle", "plus", "dash"}

func (p *painter) seriesMarker(pl *Plot, s *Series, point int) Marker {
	m := s.Marker
	if point >= 0 {
		if st := s.Points[point]; st != nil && st.Marker != nil {
			m = *st.Marker
		}
	}
	if m.Symbol == "" || m.Symbol == "auto" {
		if !pl.Markers {
			m.Symbol = "none"
			return m
		}
		m.Symbol = markerCycle[max(s.Index, 0)%len(markerCycle)]
	}
	if m.Size == 0 {
		m.Size = 5
	}
	if !m.Fill.Set {
		m.Fill = Fill{Color: p.accent(s.Index), Set: true}
		if s.Line.Set && !s.Line.None {
			m.Fill.Color = s.Line.Color
		}
	}
	if !m.Line.Set {
		m.Line = Stroke{Color: m.Fill.Color, Width: 0.75, Set: true, None: m.Fill.None}
	}
	return m
}

// segment draws a straight line, dashed as it asks.
func (p *painter) segment(x0, y0, x1, y1 float32, s Stroke) {
	w := max(s.Width, 0.25) * p.k
	if len(s.Dash) == 0 {
		p.c.Line(p.x(x0), p.y(y0), p.x(x1), p.y(y1), w, s.Color)
		return
	}
	total := float32(math.Hypot(float64(x1-x0), float64(y1-y0)))
	if total <= 0 {
		return
	}
	dx, dy := (x1-x0)/total, (y1-y0)/total
	unit := max(s.Width, 0.5)
	at, i, on := float32(0), 0, true
	for steps := 0; at < total && steps < 4096; steps++ {
		n := s.Dash[i%len(s.Dash)] * unit
		end := min(at+n, total)
		if on {
			p.c.Line(p.x(x0+dx*at), p.y(y0+dy*at), p.x(x0+dx*end), p.y(y0+dy*end), w, s.Color)
		}
		at, on = end, !on
		i++
	}
}

// polyline draws joined segments.
func (p *painter) polyline(pts [][2]float32, s Stroke) {
	for i := 0; i+1 < len(pts); i++ {
		p.segment(pts[i][0], pts[i][1], pts[i+1][0], pts[i+1][1], s)
	}
}

// polygon fills points given in the chart's points.
func (p *painter) polygon(pts [][2]float32, col color.RGBA) {
	out := make([][2]float32, len(pts))
	for i, q := range pts {
		out[i] = [2]float32{p.x(q[0]), p.y(q[1])}
	}
	p.c.Polygon([][][2]float32{out}, col)
}

// marker draws a symbol centred on x, y.
func (p *painter) marker(x, y float32, m Marker) {
	r := m.Size / 2
	fill := func(pts [][2]float32) {
		if !m.Fill.None {
			p.polygon(pts, m.Fill.Color)
		}
		if !m.Line.None {
			p.polyline(append(pts, pts[0]), m.Line)
		}
	}
	switch m.Symbol {
	case "none", "":
	case "circle", "dot":
		if !m.Fill.None {
			p.c.Circle(p.x(x), p.y(y), r*p.k, m.Fill.Color)
		}
		if !m.Line.None {
			p.c.RoundOutline(p.rect(gfx.Rect{X: x - r, Y: y - r, W: 2 * r, H: 2 * r}), r*p.k, max(m.Line.Width, 0.5)*p.k, m.Line.Color)
		}
	case "square", "picture":
		fill([][2]float32{{x - r, y - r}, {x + r, y - r}, {x + r, y + r}, {x - r, y + r}})
	case "diamond":
		fill([][2]float32{{x, y - r}, {x + r, y}, {x, y + r}, {x - r, y}})
	case "triangle":
		fill([][2]float32{{x, y - r}, {x + r, y + r}, {x - r, y + r}})
	case "x", "star", "plus", "dash":
		line := m.Line
		if line.None {
			line = Stroke{Color: m.Fill.Color, Width: 0.75}
		}
		if m.Symbol == "x" || m.Symbol == "star" {
			p.segment(x-r, y-r, x+r, y+r, line)
			p.segment(x-r, y+r, x+r, y-r, line)
		}
		if m.Symbol == "plus" || m.Symbol == "star" {
			p.segment(x, y-r, x, y+r, line)
		}
		if m.Symbol != "x" {
			p.segment(x-r, y, x+r, y, line)
		}
	}
}
