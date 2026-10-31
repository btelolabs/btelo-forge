package chart

import (
	"image/color"
	"math"
	"strings"

	"github.com/btelolabs/btelo-forge/internal/gfx"
)

func (p *painter) drawPlot(pl *Plot, r gfx.Rect) {
	switch pl.Kind {
	case Bar:
		p.bars(pl, r)
	case Line:
		p.lineOf(pl, r)
	case Area:
		p.areas(pl, r)
	case Pie, Doughnut:
		p.pie(pl, r)
	case Scatter:
		p.scatter(pl, r)
	case Bubble:
		p.bubbles(pl, r)
	case Radar:
		p.radar(pl, r)
	}
}

// pointAt is a place in the plot from a category or X axis's value and a value axis's.
func pointAt(x, y *axisView, a, b float64, r gfx.Rect) (float32, float32) {
	if x.across {
		return x.at(a, r), y.at(b, r)
	}
	return y.at(b, r), x.at(a, r)
}

// stackedValue is a series's top and bottom at a point in a stacked plot.
func stackedValue(pl *Plot, upto, i int) (from, to float64, ok bool) {
	s := pl.Series[upto]
	if i >= len(s.Vals) || math.IsNaN(s.Vals[i]) {
		return 0, 0, false
	}
	pos, neg, total := 0.0, 0.0, 0.0
	for k, o := range pl.Series {
		if i >= len(o.Vals) || math.IsNaN(o.Vals[i]) {
			continue
		}
		total += math.Abs(o.Vals[i])
		if k < upto {
			if o.Vals[i] > 0 {
				pos += o.Vals[i]
			} else {
				neg += o.Vals[i]
			}
		}
	}
	base := pos
	if s.Vals[i] < 0 {
		base = neg
	}
	from, to = base, base+s.Vals[i]
	if pl.Grouping == "percentStacked" && total > 0 {
		from, to = from/total, to/total
	}
	return from, to, true
}

func (pl *Plot) stacked() bool { return strings.HasSuffix(pl.Grouping, "tacked") }

// barBox is the rectangle of a series at a category.
func (p *painter) barBox(pl *Plot, x, y *axisView, si, i int, r gfx.Rect) (gfx.Rect, bool) {
	s := pl.Series[si]
	var from, to float64
	if pl.stacked() {
		var ok bool
		if from, to, ok = stackedValue(pl, si, i); !ok {
			return gfx.Rect{}, false
		}
	} else {
		if i >= len(s.Vals) || math.IsNaN(s.Vals[i]) {
			return gfx.Rect{}, false
		}
		_, base := p.crossing(x)
		from, to = base, s.Vals[i]
	}
	n := float64(len(pl.Series))
	if pl.stacked() {
		n = 1
		si = 0
	}
	overlap := min(max(pl.Overlap, -100), 100) / 100
	gap := min(max(pl.GapWidth, 0), 500) / 100
	width := 1 / (n - (n-1)*overlap + gap)
	start := float64(i) + (gap*width)/2 + float64(si)*width*(1-overlap)
	a0, b0 := pointAt(x, y, start, from, r)
	a1, b1 := pointAt(x, y, start+width, to, r)
	box := gfx.Rect{X: min(a0, a1), Y: min(b0, b1), W: float32(math.Abs(float64(a1 - a0))), H: float32(math.Abs(float64(b1 - b0)))}
	return box, true
}

func (p *painter) bars(pl *Plot, r gfx.Rect) {
	x, y := p.lines[pl.Axes[0]], p.lines[pl.Axes[1]]
	for si, s := range pl.Series {
		for i := range s.Vals {
			box, ok := p.barBox(pl, x, y, si, i, r)
			if !ok {
				continue
			}
			fill := p.seriesFill(pl, s, i)
			if !fill.None {
				p.c.Fill(p.rect(box), fill.Color)
			}
			line := s.Line
			if st := s.Points[i]; st != nil && st.Line.Set {
				line = st.Line
			}
			if line.Set && !line.None {
				p.c.Outline(p.rect(box), max(line.Width, 0.5)*p.k, line.Color)
			}
		}
	}
}

// curve is a line through the points, smoothed when asked, with breaks for what is missing.
func (p *painter) curve(pts [][2]float32, smooth bool) [][2]float32 {
	if !smooth || len(pts) < 3 {
		return pts
	}
	out := make([][2]float32, 0, len(pts)*8)
	for i := 0; i+1 < len(pts); i++ {
		a, b, c, d := pts[max(i-1, 0)], pts[i], pts[i+1], pts[min(i+2, len(pts)-1)]
		for k := range 8 {
			t := float32(k) / 8
			t2, t3 := t*t, t*t*t
			f := func(j int) float32 {
				return 0.5 * (2*b[j] + (c[j]-a[j])*t + (2*a[j]-5*b[j]+4*c[j]-d[j])*t2 + (3*b[j]-a[j]-3*c[j]+d[j])*t3)
			}
			out = append(out, [2]float32{f(0), f(1)})
		}
	}
	return append(out, pts[len(pts)-1])
}

// valueAt is a series's value at a point as a line or area plots it, ok false for a gap.
func (p *painter) valueAt(pl *Plot, si, i int) (float64, bool) {
	s := pl.Series[si]
	if pl.stacked() {
		_, to, ok := stackedValue(pl, si, i)
		if !ok && p.ch.Blanks == "zero" {
			return 0, true
		}
		return to, ok
	}
	if i < len(s.Vals) && !math.IsNaN(s.Vals[i]) {
		return s.Vals[i], true
	}
	if p.ch.Blanks == "zero" {
		return 0, true
	}
	return 0, false
}

func (p *painter) lineOf(pl *Plot, r gfx.Rect) {
	x, y := p.lines[pl.Axes[0]], p.lines[pl.Axes[1]]
	for si, s := range pl.Series {
		line := p.seriesLine(pl, s)
		var run [][2]float32
		flush := func() {
			if !line.None {
				p.polyline(p.curve(run, s.Smooth), line)
			}
			run = run[:0]
		}
		for i := range s.Vals {
			v, ok := p.valueAt(pl, si, i)
			if !ok {
				if p.ch.Blanks != "span" {
					flush()
				}
				continue
			}
			a, b := pointAt(x, y, x.slot(i), v, r)
			run = append(run, [2]float32{a, b})
		}
		flush()
		for i := range s.Vals {
			if v, ok := p.valueAt(pl, si, i); ok {
				a, b := pointAt(x, y, x.slot(i), v, r)
				p.marker(a, b, p.seriesMarker(pl, s, i))
			}
		}
	}
	if pl.Stock {
		p.stock(pl, x, y, r)
	}
}

// stock joins each category's highest and lowest value.
func (p *painter) stock(pl *Plot, x, y *axisView, r gfx.Rect) {
	if len(pl.Series) < 2 {
		return
	}
	line := Stroke{Color: color.RGBA{89, 89, 89, 255}, Width: 0.75}
	for i := range pl.Series[0].Vals {
		lo, hi := math.Inf(1), math.Inf(-1)
		for _, s := range pl.Series {
			if i < len(s.Vals) && !math.IsNaN(s.Vals[i]) {
				lo, hi = min(lo, s.Vals[i]), max(hi, s.Vals[i])
			}
		}
		if lo < hi {
			a0, b0 := pointAt(x, y, x.slot(i), lo, r)
			a1, b1 := pointAt(x, y, x.slot(i), hi, r)
			p.segment(a0, b0, a1, b1, line)
		}
	}
}

func (p *painter) areas(pl *Plot, r gfx.Rect) {
	x, y := p.lines[pl.Axes[0]], p.lines[pl.Axes[1]]
	_, base := p.crossing(x)
	for si, s := range pl.Series {
		fill := p.seriesFill(pl, s, -1)
		if fill.None || len(s.Vals) == 0 {
			continue
		}
		var top, bottom [][2]float32
		for i := range s.Vals {
			v, ok := p.valueAt(pl, si, i)
			if !ok {
				v = 0
			}
			from := base
			if pl.stacked() {
				from = 0
				if si > 0 {
					from, _ = p.valueAt(pl, si-1, i)
				}
			}
			a, b := pointAt(x, y, x.slot(i), v, r)
			top = append(top, [2]float32{a, b})
			a, b = pointAt(x, y, x.slot(i), from, r)
			bottom = append(bottom, [2]float32{a, b})
		}
		for i := len(bottom) - 1; i >= 0; i-- {
			top = append(top, bottom[i])
		}
		p.polygon(top, fill.Color)
		if s.Line.Set && !s.Line.None {
			top = append(top, top[0])
			p.polyline(top, s.Line)
		}
	}
}

func (p *painter) scatter(pl *Plot, r gfx.Rect) {
	x, y := p.lines[pl.Axes[0]], p.lines[pl.Axes[1]]
	for _, s := range pl.Series {
		line := p.seriesLine(pl, s)
		var run [][2]float32
		for i := range s.Vals {
			if math.IsNaN(s.Vals[i]) || math.IsNaN(s.Xs[i]) {
				if !line.None {
					p.polyline(p.curve(run, s.Smooth), line)
				}
				run = run[:0]
				continue
			}
			a, b := pointAt(x, y, s.Xs[i], s.Vals[i], r)
			run = append(run, [2]float32{a, b})
		}
		if !line.None {
			p.polyline(p.curve(run, s.Smooth), line)
		}
		for i := range s.Vals {
			if !math.IsNaN(s.Vals[i]) && !math.IsNaN(s.Xs[i]) {
				a, b := pointAt(x, y, s.Xs[i], s.Vals[i], r)
				p.marker(a, b, p.seriesMarker(pl, s, i))
			}
		}
	}
}

func (p *painter) bubbles(pl *Plot, r gfx.Rect) {
	x, y := p.lines[pl.Axes[0]], p.lines[pl.Axes[1]]
	top := 0.0
	for _, s := range pl.Series {
		for _, v := range s.Sizes {
			if !math.IsNaN(v) {
				top = max(top, math.Abs(v))
			}
		}
	}
	limit := float64(min(r.W, r.H)) / 8 * pl.BubbleSize / 100
	for _, s := range pl.Series {
		fill := p.seriesFill(pl, s, -1)
		for i := range s.Vals {
			if math.IsNaN(s.Vals[i]) || math.IsNaN(s.Xs[i]) {
				continue
			}
			size := 1.0
			if top > 0 && i < len(s.Sizes) && !math.IsNaN(s.Sizes[i]) {
				size = math.Sqrt(math.Abs(s.Sizes[i]) / top)
			}
			a, b := pointAt(x, y, s.Xs[i], s.Vals[i], r)
			rad := float32(size * limit)
			f := fill
			if st := s.Points[i]; st != nil && st.Fill.Set {
				f = st.Fill
			}
			if !f.None {
				p.c.Circle(p.x(a), p.y(b), rad*p.k, f.Color)
			}
		}
	}
}

// slice is a piece of a pie or a ring.
type slice struct {
	from, to      float64 // radians clockwise from twelve o'clock
	value, share  float64
	index         int
	cx, cy        float32
	inner, radius float32
}

func arcPoints(cx, cy, radius float32, from, to float64, out [][2]float32) [][2]float32 {
	n := max(int(math.Abs(to-from)*max(float64(radius), 8)/3), 3)
	n = min(n, 256)
	for i := 0; i <= n; i++ {
		a := from + (to-from)*float64(i)/float64(n)
		s, c := math.Sincos(a)
		out = append(out, [2]float32{cx + radius*float32(s), cy - radius*float32(c)})
	}
	return out
}

// pieSlices lays out a series round a circle in r.
func (p *painter) pieSlices(pl *Plot, s *Series, inner, radius, cx, cy float32) []slice {
	total := 0.0
	for _, v := range s.Vals {
		if !math.IsNaN(v) {
			total += math.Abs(v)
		}
	}
	if total <= 0 {
		return nil
	}
	var out []slice
	at := pl.FirstAngle * math.Pi / 180
	for i, v := range s.Vals {
		if math.IsNaN(v) || v == 0 {
			continue
		}
		span := math.Abs(v) / total * 2 * math.Pi
		k := slice{from: at, to: at + span, value: v, share: math.Abs(v) / total, index: i, cx: cx, cy: cy, inner: inner, radius: radius}
		exp := s.Explosion
		if st := s.Points[i]; st != nil && st.Explosion >= 0 {
			exp = st.Explosion
		}
		if exp > 0 {
			sin, cos := math.Sincos(at + span/2)
			d := radius * float32(min(exp, 400)/100)
			k.cx += d * float32(sin)
			k.cy -= d * float32(cos)
		}
		out = append(out, k)
		at += span
	}
	return out
}

// pieGeometry is where a pie or a ring goes in r, room left for labels written outside it.
func (p *painter) pieGeometry(pl *Plot, r gfx.Rect) (cx, cy, radius float32) {
	cx, cy = r.X+r.W/2, r.Y+r.H/2
	radius = min(r.W, r.H) / 2
	outside := false
	explode := 0.0
	for _, s := range pl.Series {
		explode = max(explode, s.Explosion)
		for _, st := range s.Points {
			explode = max(explode, st.Explosion)
		}
		if s.Labels.any() && (s.Labels.Position == "outEnd" || s.Labels.Position == "bestFit" || s.Labels.Position == "") {
			outside = pl.Kind == Pie
		}
	}
	if outside {
		radius *= 0.74
	}
	return cx, cy, max(radius/(1+float32(min(explode, 400)/100)), 2)
}

func (p *painter) pie(pl *Plot, r gfx.Rect) {
	cx, cy, radius := p.pieGeometry(pl, r)
	rings := len(pl.Series)
	if pl.Kind == Pie {
		rings = 1
	}
	for n := 0; n < rings; n++ {
		s := pl.Series[n]
		inner, outer := float32(0), radius
		if pl.Kind == Doughnut {
			band := radius * (1 - float32(pl.Hole)) / float32(rings)
			inner = radius*float32(pl.Hole) + band*float32(n)
			outer = inner + band
		}
		for _, k := range p.pieSlices(pl, s, inner, outer, cx, cy) {
			fill := p.seriesFill(pl, s, k.index)
			var pts [][2]float32
			if k.inner > 0 {
				pts = arcPoints(k.cx, k.cy, k.radius, k.from, k.to, pts)
				pts = arcPoints(k.cx, k.cy, k.inner, k.to, k.from, pts)
			} else {
				pts = append(pts, [2]float32{k.cx, k.cy})
				pts = arcPoints(k.cx, k.cy, k.radius, k.from, k.to, pts)
			}
			if !fill.None {
				p.polygon(pts, fill.Color)
			}
			line := s.Line
			if st := s.Points[k.index]; st != nil && st.Line.Set {
				line = st.Line
			}
			if !line.Set {
				line = Stroke{Color: color.RGBA{255, 255, 255, 255}, Width: 0.75, Set: true}
			}
			if !line.None {
				p.polyline(append(pts, pts[0]), line)
			}
		}
	}
}

// radarFrame is the middle and size of a radar, and its categories.
func (p *painter) radarFrame(pl *Plot, r gfx.Rect) (cx, cy, radius float32, cats []string, lo, hi float64, ticks []float64, v *axisView) {
	cx, cy = r.X+r.W/2, r.Y+r.H/2
	radius = min(r.W, r.H)/2 - p.lineHeight(&p.ch.Base)*1.6
	radius = max(radius, 4)
	n := 0
	for _, s := range pl.Series {
		n = max(n, len(s.Vals))
		if len(s.Cats) > len(cats) {
			cats = s.Cats
		}
	}
	a := p.ch.Axes[pl.Axes[1]]
	if a == nil {
		a = &Axis{Labels: "nextTo", Text: p.ch.Base, Divide: 1, Linked: true}
	}
	v = &axisView{a: a}
	l, h := extent(pl)
	v.scale(l, h, 6, true)
	if len(cats) < n {
		c := make([]string, n)
		copy(c, cats)
		cats = c
	}
	return cx, cy, radius, cats, v.lo, v.hi, v.ticks, v
}

func (p *painter) radar(pl *Plot, r gfx.Rect) {
	cx, cy, radius, cats, lo, hi, ticks, axis := p.radarFrame(pl, r)
	n := len(cats)
	if n < 3 {
		return
	}
	spoke := func(i int, f float32) [2]float32 {
		s, c := math.Sincos(2 * math.Pi * float64(i) / float64(n))
		return [2]float32{cx + radius*f*float32(s), cy - radius*f*float32(c)}
	}
	grid := Stroke{Color: color.RGBA{217, 217, 217, 255}, Width: 0.75, Set: true}
	if axis.a.Grid != nil {
		grid = gridStroke(axis.a.Grid)
	}
	for _, t := range ticks {
		f := float32((t - lo) / (hi - lo))
		for i := range n {
			a, b := spoke(i, f), spoke(i+1, f)
			p.segment(a[0], a[1], b[0], b[1], grid)
		}
	}
	for i := range n {
		a := spoke(i, 1)
		p.segment(cx, cy, a[0], a[1], grid)
		if cats[i] != "" {
			q := spoke(i, 1.08)
			p.centred(&p.ch.Base, q[0], q[1], cats[i])
		}
	}
	for _, s := range pl.Series {
		var pts [][2]float32
		for i := range n {
			v := lo
			if i < len(s.Vals) && !math.IsNaN(s.Vals[i]) {
				v = s.Vals[i]
			}
			pts = append(pts, spoke(i, float32(min(max((v-lo)/(hi-lo), 0), 1))))
		}
		if pl.Filled {
			fill := p.seriesFill(pl, s, -1)
			c := fill.Color
			if c.A == 255 {
				c.A = 160
			}
			if !fill.None {
				p.polygon(pts, c)
			}
		}
		line := p.seriesLine(pl, s)
		if !line.None {
			p.polyline(append(pts, pts[0]), line)
		}
		if !pl.Filled {
			for i, q := range pts {
				p.marker(q[0], q[1], p.seriesMarker(pl, s, i))
			}
		}
	}
	if !axis.a.Hidden && axis.a.Labels != "none" {
		for _, t := range ticks {
			f := float32((t - lo) / (hi - lo))
			w := p.fmt.number(axis.a.Format, t/axis.a.Divide)
			p.text(&axis.a.Text, cx-p.measure(&axis.a.Text, w)-3, cy-radius*f-p.lineHeight(&axis.a.Text)/2, w)
		}
	}
}

// labelWords is what a point's label says.
func (p *painter) labelWords(s *Series, l *Labels, i int, share float64) string {
	var parts []string
	if l.Series {
		name := s.Name
		if name == "" {
			name = SeriesName(s.Index + 1)
		}
		parts = append(parts, name)
	}
	if l.Category && i < len(s.Cats) {
		parts = append(parts, s.Cats[i])
	}
	if l.Value && i < len(s.Vals) {
		code := l.Format
		if code == "" {
			code = s.Format
		}
		parts = append(parts, p.fmt.number(code, s.Vals[i]))
	}
	if l.Percent {
		code := l.Format
		if code == "" || !strings.Contains(code, "%") {
			code = "0%"
		}
		parts = append(parts, p.fmt.number(code, share))
	}
	return strings.Join(parts, l.Separator)
}

// plotLabels writes the data labels over everything else.
func (p *painter) plotLabels(pl *Plot, r gfx.Rect) {
	for si, s := range pl.Series {
		l := s.Labels
		if !l.any() {
			continue
		}
		switch pl.Kind {
		case Pie, Doughnut:
			if pl.Kind == Pie && si > 0 {
				return
			}
			p.pieLabels(pl, si, s, l, r)
		case Bar:
			p.barLabels(pl, si, s, l, r)
		case Line, Area, Scatter, Bubble:
			p.pointLabels(pl, si, s, l, r)
		}
	}
}

func (p *painter) barLabels(pl *Plot, si int, s *Series, l *Labels, r gfx.Rect) {
	x, y := p.lines[pl.Axes[0]], p.lines[pl.Axes[1]]
	for i := range s.Vals {
		if s.Hidden[i] {
			continue
		}
		box, ok := p.barBox(pl, x, y, si, i, r)
		if !ok {
			continue
		}
		w := p.labelWords(s, l, i, 0)
		tw, lh := p.measure(&l.Text, w), p.lineHeight(&l.Text)
		pos := l.Position
		if pos == "" {
			pos = "outEnd"
			if pl.stacked() {
				pos = "ctr"
			}
		}
		cx, cy := box.X+box.W/2, box.Y+box.H/2
		up := s.Vals[i] >= 0 != y.a.Reverse
		if pl.Horizontal {
			end, start := box.Right(), box.X
			if !up {
				end, start = start, end
			}
			sign := float32(1)
			if !up {
				sign = -1
			}
			switch pos {
			case "outEnd":
				cx = end + sign*(tw/2+3)
			case "inEnd":
				cx = end - sign*(tw/2+3)
			case "inBase":
				cx = start + sign*(tw/2+3)
			}
		} else {
			end, start := box.Y, box.Bottom()
			if !up {
				end, start = start, end
			}
			sign := float32(-1)
			if !up {
				sign = 1
			}
			switch pos {
			case "outEnd":
				cy = end + sign*(lh/2+2)
			case "inEnd":
				cy = end - sign*(lh/2+2)
			case "inBase":
				cy = start - sign*-(lh/2+2)
			}
		}
		p.centred(&l.Text, cx, cy, w)
	}
}

func (p *painter) pointLabels(pl *Plot, si int, s *Series, l *Labels, r gfx.Rect) {
	x, y := p.lines[pl.Axes[0]], p.lines[pl.Axes[1]]
	for i := range s.Vals {
		if s.Hidden[i] {
			continue
		}
		var a, b float32
		if pl.Kind == Scatter || pl.Kind == Bubble {
			if math.IsNaN(s.Vals[i]) || math.IsNaN(s.Xs[i]) {
				continue
			}
			a, b = pointAt(x, y, s.Xs[i], s.Vals[i], r)
		} else {
			v, ok := p.valueAt(pl, si, i)
			if !ok {
				continue
			}
			a, b = pointAt(x, y, x.slot(i), v, r)
		}
		w := p.labelWords(s, l, i, 0)
		tw, lh := p.measure(&l.Text, w), p.lineHeight(&l.Text)
		switch l.Position {
		case "b":
			b += lh/2 + 4
		case "l":
			a -= tw/2 + 4
		case "r":
			a += tw/2 + 4
		case "ctr":
		default:
			b -= lh/2 + 4
		}
		p.centred(&l.Text, a, b, w)
	}
}

func (p *painter) pieLabels(pl *Plot, si int, s *Series, l *Labels, r gfx.Rect) {
	cx, cy, radius := p.pieGeometry(pl, r)
	inner, outer := float32(0), radius
	if pl.Kind == Doughnut {
		band := radius * (1 - float32(pl.Hole)) / float32(len(pl.Series))
		inner = radius*float32(pl.Hole) + band*float32(si)
		outer = inner + band
	}
	for _, k := range p.pieSlices(pl, s, inner, outer, cx, cy) {
		if s.Hidden[k.index] {
			continue
		}
		w := p.labelWords(s, l, k.index, k.share)
		sin, cos := math.Sincos((k.from + k.to) / 2)
		tw, lh := p.measure(&l.Text, w), p.lineHeight(&l.Text)
		pos := l.Position
		if pos == "" {
			pos = "bestFit"
		}
		var at float32
		switch {
		case pl.Kind == Doughnut || pos == "ctr":
			at = (k.inner + k.radius) / 2
		case pos == "inEnd":
			at = k.radius * 0.75
		default:
			// Beside the slice, the label's own size clear of the edge.
			at = k.radius + 4 + float32(math.Abs(sin))*tw/2 + float32(math.Abs(cos))*lh/2
		}
		p.centred(&l.Text, k.cx+at*float32(sin), k.cy-at*float32(cos), w)
	}
}
