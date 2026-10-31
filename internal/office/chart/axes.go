package chart

import (
	"image/color"
	"math"
	"strconv"
	"strings"

	"github.com/btelolabs/btelo-forge/internal/gfx"
)

// axisView is an axis as drawn: its scale, and which way it runs.
type axisView struct {
	a       *Axis
	across  bool // it runs left to right
	cat     bool // it counts categories
	lo, hi  float64
	major   float64
	ticks   []float64
	cats    []string
	between bool
	format  string
	side    string // which edge its labels are at
	rot     float64
	step    int
	thick   float32 // what its labels and title take beside the plot
	percent bool    // it shows shares of a whole
}

// nice rounds up to 1, 2 or 5 times a power of ten.
func nice(v float64) float64 {
	if v <= 0 || math.IsInf(v, 0) || math.IsNaN(v) {
		return 1
	}
	e := math.Floor(math.Log10(v))
	f := v / math.Pow(10, e)
	switch {
	case f <= 1:
		f = 1
	case f <= 2:
		f = 2
	case f <= 5:
		f = 5
	default:
		f = 10
	}
	return f * math.Pow(10, e)
}

// scale chooses the range and the ticks of a value axis from the data's lo and hi, and how many ticks fit.
func (v *axisView) scale(lo, hi float64, most int, bars bool) {
	a := v.a
	if lo > hi {
		lo, hi = 0, 1
	}
	if a.Log > 0 {
		v.logScale(lo, hi)
		return
	}
	if v.percent {
		lo, hi = min(lo, 0), max(hi, 0)
		if a.Max == nil {
			hi = 1
		}
		if a.Min == nil && lo < 0 {
			lo = -1
		}
		if a.Major <= 0 {
			major := 0.1
			if most < 8 {
				major = 0.2
			}
			v.ticksFrom(lo, hi, major)
			return
		}
	}
	if a.Min == nil && lo > 0 && (hi-lo)/hi > 1.0/6 {
		lo = 0
	}
	if a.Max == nil && hi < 0 && (hi-lo)/-lo > 1.0/6 {
		hi = 0
	}
	if bars {
		lo, hi = min(lo, 0), max(hi, 0)
	}
	if a.Min != nil {
		lo = *a.Min
	}
	if a.Max != nil {
		hi = *a.Max
	}
	if hi <= lo {
		if a.Min != nil && a.Max == nil {
			hi = lo + 1
		} else if a.Max != nil && a.Min == nil {
			lo = hi - 1
		} else {
			lo, hi = lo-1, lo+1
		}
	}
	major := a.Major
	if major <= 0 {
		major = nice((hi - lo) / float64(max(most, 2)))
	}
	if (hi-lo)/major > 400 {
		major = nice((hi - lo) / 400)
	}
	if a.Min == nil {
		lo = math.Floor(lo/major) * major
	}
	if a.Max == nil {
		// Room over the highest value, as Excel leaves it.
		top := hi + (hi-lo)*0.05
		hi = math.Ceil(top/major-1e-9) * major
	}
	v.ticksFrom(lo, hi, major)
}

// ticksFrom sets the range, with a tick at each multiple of major, or from a minimum set by hand.
func (v *axisView) ticksFrom(lo, hi, major float64) {
	a := v.a
	v.lo, v.hi, v.major = lo, hi, major
	v.ticks = v.ticks[:0]
	start := math.Ceil(lo/major-1e-9) * major
	if a.Min != nil {
		start = lo
	}
	for i := 0; i < 402; i++ {
		t := start + float64(i)*major
		if t > hi+major*1e-9 {
			break
		}
		if math.Abs(t) < major*1e-9 {
			t = 0
		}
		v.ticks = append(v.ticks, t)
	}
}

func (v *axisView) logScale(lo, hi float64) {
	a := v.a
	b := a.Log
	if lo <= 0 {
		lo = hi / 1000
	}
	if hi <= 0 {
		lo, hi = 1, 10
	}
	first := math.Floor(math.Log(lo)/math.Log(b) + 1e-9)
	last := math.Ceil(math.Log(hi)/math.Log(b) - 1e-9)
	if a.Min != nil && *a.Min > 0 {
		first = math.Log(*a.Min) / math.Log(b)
	}
	if a.Max != nil && *a.Max > 0 {
		last = math.Log(*a.Max) / math.Log(b)
	}
	if last <= first {
		last = first + 1
	}
	last = min(last, first+300)
	v.lo, v.hi, v.major = math.Pow(b, first), math.Pow(b, last), b
	v.ticks = v.ticks[:0]
	for e := first; e <= last+1e-9; e++ {
		v.ticks = append(v.ticks, math.Pow(b, e))
	}
}

// share is where a value falls along the axis, from 0 to 1, low end first.
func (v *axisView) share(x float64) float64 {
	var t float64
	if v.a.Log > 0 {
		if x <= 0 {
			x = v.lo
		}
		t = (math.Log(x) - math.Log(v.lo)) / (math.Log(v.hi) - math.Log(v.lo))
	} else {
		t = (x - v.lo) / (v.hi - v.lo)
	}
	if v.a.Reverse {
		t = 1 - t
	}
	return t
}

// at is where a value falls in the area r.
func (v *axisView) at(x float64, r gfx.Rect) float32 {
	if v.across {
		return r.X + float32(v.share(x))*r.W
	}
	return r.Bottom() - float32(v.share(x))*r.H
}

// slot is where category i's middle falls.
func (v *axisView) slot(i int) float64 {
	n := max(len(v.cats), 1)
	if v.between {
		return float64(i) + 0.5
	}
	if n == 1 {
		return 0.5
	}
	return float64(i) * float64(v.hi) / float64(n-1)
}

func (p *painter) axisOf(id int64, role string, across bool) *axisView {
	if v := p.lines[id]; v != nil {
		return v
	}
	a := p.ch.Axes[id]
	if a == nil {
		// A chart may be drawn from several goroutines, so what stands in for a missing axis stays out of it.
		a = &Axis{ID: id, Role: role, Labels: "nextTo", Text: p.ch.Base, Divide: 1, Hidden: true, Linked: true, Crosses: "autoZero"}
	}
	// Which way it runs comes from the plot, as a bar chart's direction turns both its axes.
	v := &axisView{a: a, across: across}
	p.lines[id] = v
	p.axes = append(p.axes, v)
	return v
}

// cartesian reports a plot with two axes.
func (pl *Plot) cartesian() bool {
	return pl.Kind == Bar || pl.Kind == Line || pl.Kind == Area || pl.Kind == Scatter || pl.Kind == Bubble
}

// buildAxes joins each plot to its axes and works out their ranges in terms of the data.
func (p *painter) buildAxes() {
	for _, pl := range p.ch.Plots {
		if !pl.cartesian() {
			continue
		}
		role := "cat"
		if pl.Kind == Scatter || pl.Kind == Bubble {
			role = "val"
		}
		x := p.axisOf(pl.Axes[0], role, !pl.Horizontal)
		y := p.axisOf(pl.Axes[1], "val", pl.Horizontal)
		if p.ch.Axes[x.a.ID] == nil {
			x.a.Cross = y.a.ID
		}
		if p.ch.Axes[y.a.ID] == nil {
			y.a.Cross = x.a.ID
		}
		if role == "cat" {
			x.cat = true
			for _, s := range pl.Series {
				n := max(len(s.Vals), len(s.Cats))
				if n > len(x.cats) {
					cats := make([]string, n)
					copy(cats, s.Cats)
					copy(cats, x.cats)
					x.cats = cats
				}
				for i, c := range s.Cats {
					if i < len(x.cats) && x.cats[i] == "" {
						x.cats[i] = c
					}
				}
			}
			for i, c := range x.cats {
				if c == "" {
					x.cats[i] = strconv.Itoa(i + 1)
				}
			}
			x.between = y.a.Between
			if pl.Kind == Bar {
				x.between = true
			}
		}
		if y.format == "" && len(pl.Series) > 0 {
			y.format = pl.Series[0].Format
		}
		y.percent = y.percent || pl.Grouping == "percentStacked"
	}
}

// extent is the range of values a plot gives its value axis.
func extent(pl *Plot) (lo, hi float64) {
	lo, hi = math.Inf(1), math.Inf(-1)
	stacked := strings.HasSuffix(pl.Grouping, "tacked")
	if stacked && (pl.Kind == Bar || pl.Kind == Line || pl.Kind == Area) {
		n := 0
		for _, s := range pl.Series {
			n = max(n, len(s.Vals))
		}
		for i := range n {
			pos, neg, total := 0.0, 0.0, 0.0
			for _, s := range pl.Series {
				if i < len(s.Vals) && !math.IsNaN(s.Vals[i]) {
					total += math.Abs(s.Vals[i])
					if s.Vals[i] > 0 {
						pos += s.Vals[i]
					} else {
						neg += s.Vals[i]
					}
				}
			}
			if pl.Grouping == "percentStacked" && total > 0 {
				pos, neg = pos/total, neg/total
			}
			lo, hi = min(lo, neg), max(hi, pos)
		}
		return lo, hi
	}
	for _, s := range pl.Series {
		for _, v := range s.Vals {
			if !math.IsNaN(v) {
				lo, hi = min(lo, v), max(hi, v)
			}
		}
	}
	return lo, hi
}

// measureAxes scales the axes for an area and works out what they take beside it.
func (p *painter) measureAxes(r gfx.Rect) {
	ranges := map[int64]*[3]float64{} // lo, hi, whether bars stand on it
	add := func(id int64, lo, hi float64, bars bool) {
		if math.IsInf(lo, 0) {
			return
		}
		e := ranges[id]
		if e == nil {
			e = &[3]float64{lo, hi, 0}
			ranges[id] = e
		}
		e[0], e[1] = min(e[0], lo), max(e[1], hi)
		if bars {
			e[2] = 1
		}
	}
	for _, pl := range p.ch.Plots {
		if !pl.cartesian() {
			continue
		}
		lo, hi := extent(pl)
		add(pl.Axes[1], lo, hi, pl.Kind == Bar || pl.Kind == Area)
		if pl.Kind == Scatter || pl.Kind == Bubble {
			xl, xh := math.Inf(1), math.Inf(-1)
			for _, s := range pl.Series {
				for _, x := range s.Xs {
					if !math.IsNaN(x) {
						xl, xh = min(xl, x), max(xh, x)
					}
				}
			}
			add(pl.Axes[0], xl, xh, false)
		}
	}
	for _, v := range p.axes {
		a := v.a
		if v.cat {
			n := max(len(v.cats), 1)
			v.lo, v.hi = 0, float64(n)
			if !v.between {
				v.hi = float64(max(n-1, 1))
			}
			continue
		}
		length := r.H
		if v.across {
			length = r.W
		}
		size := p.lineHeight(&a.Text)
		most := int(length / (size * 1.7))
		if v.across {
			most = int(length / max(size*2.6, 1))
		}
		e := ranges[a.ID]
		if e == nil {
			e = &[3]float64{0, 1, 0}
		}
		v.scale(e[0], e[1], max(most, 2), e[2] != 0)
	}
	for _, v := range p.axes {
		p.sideOf(v)
		p.thickness(v, r)
	}
}

// crossing is the value on this axis's partner where this axis stands.
func (p *painter) crossing(v *axisView) (other *axisView, at float64) {
	other = p.lines[v.a.Cross]
	if other == nil {
		return nil, 0
	}
	a := v.a
	switch {
	case a.CrossesAt != nil:
		at = *a.CrossesAt
	case a.Crosses == "min":
		at = other.lo
	case a.Crosses == "max":
		at = other.hi
	case other.cat:
		at = other.lo
	default:
		at = 0
		if other.a.Log > 0 {
			at = other.lo
		}
	}
	if other.cat && a.CrossesAt != nil {
		at-- // categories count from one here
	}
	return other, min(max(at, other.lo), other.hi)
}

func (p *painter) sideOf(v *axisView) {
	other, at := p.crossing(v)
	low := true
	if other != nil {
		low = other.share(at) < 0.5
	}
	if !v.across {
		// A vertical axis on the low side of a left-to-right axis is at the left.
		v.side = "l"
		if !low {
			v.side = "r"
		}
	} else {
		v.side = "b"
		if !low {
			v.side = "t"
		}
	}
	switch v.a.Labels {
	case "high", "low":
		high := v.a.Labels == "high"
		if other != nil && other.a.Reverse {
			high = !high
		}
		switch {
		case !v.across && high:
			v.side = "r"
		case !v.across:
			v.side = "l"
		case high:
			v.side = "t"
		default:
			v.side = "b"
		}
	}
}

// labels are what an axis writes along itself, with where each goes.
func (p *painter) labels(v *axisView) (at []float64, words []string) {
	a := v.a
	if v.cat {
		step := max(v.step, 1)
		for i := 0; i < len(v.cats); i += step {
			at = append(at, v.slot(i))
			words = append(words, v.cats[i])
		}
		return at, words
	}
	code := a.Format
	if a.Linked && v.format != "" {
		code = v.format
	}
	if v.percent && (a.Linked || code == "" || strings.EqualFold(code, "General")) {
		code = "0%"
	}
	for _, t := range v.ticks {
		at = append(at, t)
		words = append(words, p.fmt.number(code, t/a.Divide))
	}
	return at, words
}

// thickness works out how a category axis fits its labels, and what any axis takes beside the plot.
func (p *painter) thickness(v *axisView, r gfx.Rect) {
	a := v.a
	v.thick, v.rot = 0, 0
	if a.Hidden {
		return
	}
	lh := p.lineHeight(&a.Text)
	if a.Labels != "none" {
		if a.Text.HasRotation {
			v.rot = a.Text.Rotation
		}
		v.step = max(a.Skip, 1)
		if v.cat && len(v.cats) > 0 {
			length := r.H
			if v.across {
				length = r.W
			}
			slot := length / float32(len(v.cats))
			longest := float32(0)
			for _, c := range v.cats {
				longest = max(longest, p.measure(&a.Text, c))
			}
			if a.Skip == 0 {
				need := lh * 1.1
				if v.across && v.rot == 0 {
					need = longest + 6
					if need > slot && !a.Text.HasRotation {
						v.rot = -45
						need = lh * 1.3
					}
				} else if v.across {
					need = lh * 1.3
				}
				v.step = max(int(math.Ceil(float64(need/max(slot, 0.01)))), 1)
			}
		}
		_, words := p.labels(v)
		size := float32(0)
		for _, w := range words {
			size = max(size, p.measure(&a.Text, w))
		}
		sin, cos := math.Sincos(v.rot * math.Pi / 180)
		if v.across {
			v.thick = float32(math.Abs(sin))*size + float32(math.Abs(cos))*lh
		} else {
			v.thick = float32(math.Abs(cos))*size + float32(math.Abs(sin))*lh
		}
		limit := r.H * 0.45
		if !v.across {
			limit = r.W * 0.4
		}
		v.thick = min(v.thick, limit) + 5
	}
	if t := a.Title; t != nil && t.Words != "" {
		v.thick += p.lineHeight(&t.Text) + 4
	}
}

// innerArea is where the plots go, in the room left.
func (p *painter) innerArea(room gfx.Rect) gfx.Rect {
	if l := p.ch.PlotLayout; l.Set && l.Sized {
		r := gfx.Rect{X: float32(l.X) * p.w, Y: float32(l.Y) * p.h, W: float32(l.W) * p.w, H: float32(l.H) * p.h}
		p.measureAxes(r)
		if l.Inner {
			return r
		}
		room = r
	}
	inner := room
	for range 3 {
		p.measureAxes(inner)
		var l, t, r, b float32
		for _, v := range p.axes {
			switch v.side {
			case "l":
				l = max(l, v.thick)
			case "r":
				r = max(r, v.thick)
			case "t":
				t = max(t, v.thick)
			case "b":
				b = max(b, v.thick)
			}
		}
		for _, v := range p.axes {
			if v.across && !v.a.Hidden && !v.cat && v.rot == 0 && v.a.Labels != "none" {
				if _, words := p.labels(v); len(words) > 0 {
					half := p.measure(&v.a.Text, words[len(words)-1]) / 2
					if v.a.Reverse {
						l = max(l, half)
					} else {
						r = max(r, half)
					}
				}
			}
		}
		inner = gfx.Rect{X: room.X + l, Y: room.Y + t, W: max(room.W-l-r, 4), H: max(room.H-t-b, 4)}
	}
	p.measureAxes(inner)
	return inner
}

// gridStroke is an axis's gridline as it will be drawn.
func gridStroke(s *Stroke) Stroke {
	out := *s
	if !out.Set {
		out.Color, out.Set = color.RGBA{217, 217, 217, 255}, true
	}
	if out.Width <= 0 {
		out.Width = 0.75
	}
	return out
}

func (p *painter) grids(r gfx.Rect) {
	for _, v := range p.axes {
		if v.a.MinorGrid != nil && !v.a.MinorGrid.None && !v.cat && v.a.Log == 0 {
			s := gridStroke(v.a.MinorGrid)
			for i := 0; i+1 < len(v.ticks); i++ {
				for k := 1; k < 5; k++ {
					p.rule(v, v.ticks[i]+(v.ticks[i+1]-v.ticks[i])*float64(k)/5, r, s)
				}
			}
		}
		if v.a.Grid == nil || v.a.Grid.None {
			continue
		}
		s := gridStroke(v.a.Grid)
		if v.cat {
			for i := 0; i <= len(v.cats); i++ {
				if t := float64(i); t <= v.hi {
					p.rule(v, t, r, s)
				}
			}
			continue
		}
		for _, t := range v.ticks {
			p.rule(v, t, r, s)
		}
	}
}

// rule draws a line across the plot at a value of an axis.
func (p *painter) rule(v *axisView, t float64, r gfx.Rect, s Stroke) {
	at := v.at(t, r)
	if v.across {
		p.segment(at, r.Y, at, r.Bottom(), s)
	} else {
		p.segment(r.X, at, r.Right(), at, s)
	}
}

func (p *painter) drawAxes(r gfx.Rect) {
	for _, v := range p.axes {
		if !v.a.Hidden {
			p.drawAxis(v, r)
		}
	}
}

func (p *painter) drawAxis(v *axisView, r gfx.Rect) {
	a := v.a
	// Where the axis's line stands.
	pos := map[string]float32{"l": r.X, "r": r.Right(), "t": r.Y, "b": r.Bottom()}[v.side]
	if other, at := p.crossing(v); other != nil {
		pos = other.at(at, r)
	}
	line := a.Line
	if !line.Set {
		line = Stroke{Color: color.RGBA{134, 134, 134, 255}, Width: 0.75, Set: true}
	}
	if line.Width <= 0 {
		line.Width = 0.75
	}
	if !line.None {
		if v.across {
			p.segment(r.X, pos, r.Right(), pos, line)
		} else {
			p.segment(pos, r.Y, pos, r.Bottom(), line)
		}
	}
	ticks := func(t float64) {
		if line.None || a.MajorTicks == "none" {
			return
		}
		c := v.at(t, r)
		in, out := float32(0), float32(3)
		switch a.MajorTicks {
		case "in":
			in, out = 3, 0
		case "cross":
			in = 3
		}
		sign := float32(1)
		if v.side == "l" || v.side == "t" {
			sign = -1
		}
		if v.across {
			p.segment(c, pos-in*sign, c, pos+out*sign, line)
		} else {
			p.segment(pos-in*sign, c, pos+out*sign, c, line)
		}
	}
	if v.cat {
		for i := 0; i <= len(v.cats); i++ {
			if t := float64(i); t <= v.hi {
				ticks(t)
			}
		}
	} else {
		for _, t := range v.ticks {
			ticks(t)
		}
	}
	// Labels next to the axis stand at its line; asked for at an end, at the plot's edge.
	edge := pos
	if a.Labels == "low" || a.Labels == "high" {
		edge = map[string]float32{"l": r.X, "r": r.Right(), "t": r.Y, "b": r.Bottom()}[v.side]
	}
	if a.Labels != "none" {
		at, words := p.labels(v)
		lh := p.lineHeight(&a.Text)
		for i, w := range words {
			c := v.at(at[i], r)
			switch {
			case v.rot != 0:
				p.turnedLabel(v, w, c, edge, lh)
			case v.across && v.side == "b":
				p.centred(&a.Text, c, edge+4+lh/2, w)
			case v.across:
				p.centred(&a.Text, c, edge-4-lh/2, w)
			case v.side == "l":
				room := v.thick
				if a.Title != nil {
					room -= p.lineHeight(&a.Title.Text) + 4
				}
				w = p.face(&a.Text).Ellipsize(w, max(room-4, 8)*p.k)
				p.text(&a.Text, edge-4-p.measure(&a.Text, w), c-lh/2, w)
			default:
				p.text(&a.Text, edge+4, c-lh/2, w)
			}
		}
	}
	if t := a.Title; t != nil && t.Words != "" {
		mid := float32(0)
		if v.across {
			mid = r.X + r.W/2
		} else {
			mid = r.Y + r.H/2
		}
		lh := p.lineHeight(&t.Text)
		rot := t.Text.Rotation
		if !t.Text.HasRotation && !v.across {
			rot = -90
		}
		switch v.side {
		case "l":
			p.turned(&t.Text, r.X-v.thick+lh/2, mid, t.Words, rot)
		case "r":
			p.turned(&t.Text, r.Right()+v.thick-lh/2, mid, t.Words, rot)
		case "t":
			p.turned(&t.Text, mid, r.Y-v.thick+lh/2, t.Words, rot)
		default:
			p.turned(&t.Text, mid, r.Bottom()+v.thick-lh/2, t.Words, rot)
		}
	}
}

// turnedLabel writes a slanted label with its near end at the tick, as Office does.
func (p *painter) turnedLabel(v *axisView, s string, c, edge, lh float32) {
	a := v.a
	s = p.face(&a.Text).Ellipsize(s, 0.45*p.h*p.k/float32(max(math.Abs(math.Sin(v.rot*math.Pi/180)), 0.2)))
	w := float64(p.measure(&a.Text, s))
	sin, cos := math.Sincos(v.rot * math.Pi / 180)
	cx, cy := float64(c), float64(edge)
	half := w / 2
	if v.across {
		// The end nearest the axis goes at the tick.
		dir := 1.0
		if v.side == "t" {
			dir = -1
		}
		if sin*dir < 0 {
			cx -= half * cos
			cy += dir * half * math.Abs(sin)
		} else {
			cx += half * cos
			cy += dir * half * math.Abs(sin)
		}
		cy += dir * float64(4+lh/2*float32(math.Abs(cos)))
	} else {
		dir := 1.0
		if v.side == "l" {
			dir = -1
		}
		cx += dir * (half*math.Abs(cos) + 4)
		cy += half * sin * -dir
	}
	p.turned(&a.Text, float32(cx), float32(cy), s, v.rot)
}
