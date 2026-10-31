package pptx

import (
	"math"
	"strconv"
	"strings"

	"github.com/btelolabs/btelo-forge/internal/office/ox"
)

// Path is a shape's outline in its own points: moves, lines, cubic curves and closes, with what the DrawingML path
// asks of it.
type Path struct {
	Ops    []PathOp
	Fill   string // "none", "norm", "lighten", "lightenLess", "darken" or "darkenLess"
	Stroke bool
}

// PathOp is a command of a Path. Code is 'M', 'L', 'C' or 'Z'.
type PathOp struct {
	Code byte
	X    [3]float64
	Y    [3]float64
}

// pen builds a shape's paths: the presets call its methods, and custom geometry's commands do too.
type pen struct {
	w, h  float64
	ss    float64 // the shorter side
	adj   map[string]float64
	paths []Path
	cur   *Path
	x, y  float64 // where the path is
	sx    float64 // where the subpath started
	sy    float64
	text  [4]float64 // the text rectangle: left, top, right, bottom
}

func newPen(w, h float64, adj map[string]float64) *pen {
	return &pen{w: w, h: h, ss: min(w, h), adj: adj, text: [4]float64{0, 0, w, h}}
}

// a is an adjust value with its default.
func (p *pen) a(name string, def float64) float64 {
	if v, ok := p.adj[name]; ok {
		return v
	}
	return def
}

// pin is an adjust value kept in a range.
func (p *pen) pin(name string, def, lo, hi float64) float64 { return clampF(p.a(name, def), lo, hi) }

// begin starts a path of the kind.
func (p *pen) begin(fill string, stroke bool) *pen {
	p.paths = append(p.paths, Path{Fill: fill, Stroke: stroke})
	p.cur = &p.paths[len(p.paths)-1]
	return p
}

func (p *pen) ensure() {
	if p.cur == nil {
		p.begin("norm", true)
	}
}

func (p *pen) m(x, y float64) *pen {
	p.ensure()
	p.cur.Ops = append(p.cur.Ops, PathOp{Code: 'M', X: [3]float64{x}, Y: [3]float64{y}})
	p.x, p.y, p.sx, p.sy = x, y, x, y
	return p
}

func (p *pen) l(x, y float64) *pen {
	p.ensure()
	p.cur.Ops = append(p.cur.Ops, PathOp{Code: 'L', X: [3]float64{x}, Y: [3]float64{y}})
	p.x, p.y = x, y
	return p
}

// poly is a closed outline through the points, given as x, y pairs.
func (p *pen) poly(pts ...float64) *pen {
	for i := 0; i+1 < len(pts); i += 2 {
		if i == 0 {
			p.m(pts[0], pts[1])
		} else {
			p.l(pts[i], pts[i+1])
		}
	}
	return p.z()
}

// line is an open polyline through the points.
func (p *pen) line(pts ...float64) *pen {
	for i := 0; i+1 < len(pts); i += 2 {
		if i == 0 {
			p.m(pts[0], pts[1])
		} else {
			p.l(pts[i], pts[i+1])
		}
	}
	return p
}

func (p *pen) c(x1, y1, x2, y2, x3, y3 float64) *pen {
	p.ensure()
	p.cur.Ops = append(p.cur.Ops, PathOp{Code: 'C', X: [3]float64{x1, x2, x3}, Y: [3]float64{y1, y2, y3}})
	p.x, p.y = x3, y3
	return p
}

func (p *pen) q(x1, y1, x2, y2 float64) *pen {
	return p.c(p.x+2.0/3*(x1-p.x), p.y+2.0/3*(y1-p.y), x2+2.0/3*(x1-x2), y2+2.0/3*(y1-y2), x2, y2)
}

func (p *pen) z() *pen {
	p.ensure()
	p.cur.Ops = append(p.cur.Ops, PathOp{Code: 'Z'})
	p.x, p.y = p.sx, p.sy
	return p
}

// arc is DrawingML's arcTo: the path is on an ellipse of radii wr and hr at the angle st, and goes sw round it; the
// angles are in degrees clockwise from the x axis, as seen on the ellipse's own circle.
func (p *pen) arc(wr, hr, st, sw float64) *pen {
	p.ensure()
	if sw == 0 {
		return p
	}
	sw = clampF(sw, -360, 360)
	// DrawingML's angles are visual: the point on the ellipse lies in that direction, so the parameter differs.
	param := func(a float64) float64 {
		r := a * math.Pi / 180
		if wr == 0 || hr == 0 {
			return r
		}
		s, c := math.Sincos(r)
		t := math.Atan2(s/hr, c/wr)
		// keep the same turn as a
		for t < r-math.Pi {
			t += 2 * math.Pi
		}
		for t > r+math.Pi {
			t -= 2 * math.Pi
		}
		return t
	}
	t0 := param(st)
	t1 := param(st + sw)
	if math.Abs(sw) >= 360 {
		t1 = t0 + math.Copysign(2*math.Pi, sw)
	}
	cx, cy := p.x-wr*math.Cos(t0), p.y-hr*math.Sin(t0)
	n := int(math.Ceil(math.Abs(t1-t0) / (math.Pi / 2)))
	n = max(n, 1)
	step := (t1 - t0) / float64(n)
	k := 4.0 / 3 * math.Tan(step/4)
	for i := 0; i < n; i++ {
		a0 := t0 + float64(i)*step
		a1 := a0 + step
		s0, c0 := math.Sincos(a0)
		s1, c1 := math.Sincos(a1)
		p.c(cx+wr*(c0-k*s0), cy+hr*(s0+k*c0), cx+wr*(c1+k*s1), cy+hr*(s1-k*c1), cx+wr*c1, cy+hr*s1)
	}
	return p
}

// ellipseAt is a whole ellipse in the box.
func (p *pen) ellipse(x, y, w, h float64) *pen {
	return p.m(x, y+h/2).arc(w/2, h/2, 180, 360).z()
}

func (p *pen) rect(x, y, w, h float64) *pen { return p.poly(x, y, x+w, y, x+w, y+h, x, y+h) }

// box sets the text rectangle.
func (p *pen) box(l, t, r, b float64) { p.text = [4]float64{l, t, r, b} }

// star draws a star of n points, the inner radius a of the outer in 50000ths of the half size.
func (p *pen) star(n int, a float64) {
	hc, vc := p.w/2, p.h/2
	for i := 0; i < 2*n; i++ {
		ang := -math.Pi/2 + float64(i)*math.Pi/float64(n)
		rx, ry := hc, vc
		if i%2 == 1 {
			rx, ry = hc*a/50000, vc*a/50000
		}
		x, y := hc+rx*math.Cos(ang), vc+ry*math.Sin(ang)
		if i == 0 {
			p.m(x, y)
		} else {
			p.l(x, y)
		}
	}
	p.z()
	r := a / 50000
	p.box(hc*(1-r), vc*(1-r), hc*(1+r), vc*(1+r))
}

// regular draws a polygon of n sides with a vertex up, stretched by the factors of the preset.
func (p *pen) regular(n int) {
	hc, vc := p.w/2, p.h/2
	for i := 0; i < n; i++ {
		ang := -math.Pi/2 + float64(i)*2*math.Pi/float64(n)
		x, y := hc+hc*math.Cos(ang), vc+vc*math.Sin(ang)
		if i == 0 {
			p.m(x, y)
		} else {
			p.l(x, y)
		}
	}
	p.z()
}

// presets draw the preset shapes, following the formulas of ECMA-376's presetShapeDefinitions.
var presets map[string]func(*pen)

func init() {
	presets = map[string]func(*pen){
		"rect":      func(p *pen) { p.rect(0, 0, p.w, p.h) },
		"roundRect": func(p *pen) { presetRoundRect(p) },
		"snip1Rect": func(p *pen) {
			x := p.ss * p.pin("adj", 16667, 0, 50000) / 100000
			p.poly(0, 0, p.w-x, 0, p.w, x, p.w, p.h, 0, p.h)
			p.box(0, x/2, p.w-x/2, p.h)
		},
		"snip2SameRect": func(p *pen) {
			a, b := p.ss*p.pin("adj1", 16667, 0, 50000)/100000, p.ss*p.pin("adj2", 0, 0, 50000)/100000
			p.poly(a, 0, p.w-a, 0, p.w, a, p.w, p.h-b, p.w-b, p.h, b, p.h, 0, p.h-b, 0, a)
		},
		"snip2DiagRect": func(p *pen) {
			a, b := p.ss*p.pin("adj1", 0, 0, 50000)/100000, p.ss*p.pin("adj2", 16667, 0, 50000)/100000
			p.poly(a, 0, p.w-b, 0, p.w, b, p.w, p.h-a, p.w-a, p.h, b, p.h, 0, p.h-b, 0, a)
		},
		"round1Rect": func(p *pen) {
			r := p.ss * p.pin("adj", 16667, 0, 50000) / 100000
			p.m(0, 0).l(p.w-r, 0).arc(r, r, 270, 90).l(p.w, p.h).l(0, p.h).z()
			p.box(0, 0, p.w-r*0.29289, p.h)
		},
		"round2SameRect": func(p *pen) {
			a, b := p.ss*p.pin("adj1", 16667, 0, 50000)/100000, p.ss*p.pin("adj2", 0, 0, 50000)/100000
			p.m(a, 0).l(p.w-a, 0).arc(a, a, 270, 90).l(p.w, p.h-b).arc(b, b, 0, 90).l(b, p.h).arc(b, b, 90, 90).l(0, a).arc(a, a, 180, 90).z()
		},
		"round2DiagRect": func(p *pen) {
			a, b := p.ss*p.pin("adj1", 16667, 0, 50000)/100000, p.ss*p.pin("adj2", 0, 0, 50000)/100000
			p.m(a, 0).l(p.w-b, 0).arc(b, b, 270, 90).l(p.w, p.h-a).arc(a, a, 0, 90).l(b, p.h).arc(b, b, 90, 90).l(0, a).arc(a, a, 180, 90).z()
		},
		"ellipse": func(p *pen) {
			p.ellipse(0, 0, p.w, p.h)
			dx, dy := p.w/2*(1-math.Sqrt2/2), p.h/2*(1-math.Sqrt2/2)
			p.box(dx, dy, p.w-dx, p.h-dy)
		},
		"triangle": func(p *pen) {
			x := p.w * p.pin("adj", 50000, 0, 100000) / 100000
			p.poly(0, p.h, x, 0, p.w, p.h)
			p.box(x/2, p.h/2, (x+p.w)/2, p.h)
		},
		"rtTriangle": func(p *pen) {
			p.poly(0, p.h, 0, 0, p.w, p.h)
			p.box(p.w/12, p.h*7/12, p.w*7/12, p.h*11/12)
		},
		"diamond": func(p *pen) {
			p.poly(0, p.h/2, p.w/2, 0, p.w, p.h/2, p.w/2, p.h)
			p.box(p.w/4, p.h/4, p.w*3/4, p.h*3/4)
		},
		"parallelogram": func(p *pen) {
			maxAdj := 100000 * p.w / p.ss
			x := p.ss * p.pin("adj", 25000, 0, maxAdj) / 100000
			p.poly(0, p.h, x, 0, p.w, 0, p.w-x, p.h)
			p.box(x/2, 0, p.w-x/2, p.h)
		},
		"trapezoid": func(p *pen) {
			maxAdj := 50000 * p.w / p.ss
			x := p.ss * p.pin("adj", 25000, 0, maxAdj) / 100000
			p.poly(0, p.h, x, 0, p.w-x, 0, p.w, p.h)
			p.box(x/3, x/3*p.h/p.w, p.w-x/3, p.h)
		},
		"pentagon": func(p *pen) {
			hf, vf := p.a("hf", 105146)/100000, p.a("vf", 110557)/100000
			sw, sh, svc := p.w/2*hf, p.h/2*vf, p.h/2*vf
			c1, s1 := cosd(18), sind(18)
			c2, s2 := cosd(306), sind(306)
			x1, x2, x3, x4 := p.w/2-sw*c1, p.w/2-sw*c2, p.w/2+sw*c2, p.w/2+sw*c1
			y1, y2 := svc-sh*s1, svc-sh*s2
			p.poly(x1, y1, p.w/2, 0, x4, y1, x3, y2, x2, y2)
			p.box(x2, y1, x3, y2)
		},
		"hexagon": func(p *pen) {
			maxAdj := 50000 * p.w / p.ss
			x1 := p.ss * p.pin("adj", 25000, 0, maxAdj) / 100000
			dy := p.h / 2 * p.a("vf", 115470) / 100000 * sind(60)
			vc := p.h / 2
			p.poly(0, vc, x1, vc-dy, p.w-x1, vc-dy, p.w, vc, p.w-x1, vc+dy, x1, vc+dy)
			p.box(x1/2, vc-dy/2, p.w-x1/2, vc+dy/2)
		},
		"heptagon": func(p *pen) { p.regular(7) },
		"octagon": func(p *pen) {
			x := p.ss * p.pin("adj", 29289, 0, 50000) / 100000
			p.poly(0, x, x, 0, p.w-x, 0, p.w, x, p.w, p.h-x, p.w-x, p.h, x, p.h, 0, p.h-x)
			p.box(x/2, x/2, p.w-x/2, p.h-x/2)
		},
		"decagon":   func(p *pen) { p.regular(10) },
		"dodecagon": func(p *pen) { p.regular(12) },
		"star4":     func(p *pen) { p.star(4, p.pin("adj", 12500, 0, 50000)) },
		"star5": func(p *pen) {
			// The preset's five points are scaled to touch the box.
			a := p.pin("adj", 19098, 0, 50000)
			hf, vf := p.a("hf", 105146)/100000, p.a("vf", 110557)/100000
			sw, sh := p.w/2*hf, p.h/2*vf
			svc := sh
			hc := p.w / 2
			for i := 0; i < 10; i++ {
				ang := -90 + float64(i)*36
				rx, ry := sw, sh
				if i%2 == 1 {
					rx, ry = sw*a/50000, sh*a/50000
				}
				x, y := hc+rx*cosd(ang), svc+ry*sind(ang)
				if i == 0 {
					p.m(x, y)
				} else {
					p.l(x, y)
				}
			}
			p.z()
			p.box(hc-sw*a/50000, svc-sh*a/50000, hc+sw*a/50000, svc+sh*a/50000)
		},
		"star6":  func(p *pen) { p.star(6, p.pin("adj", 28868, 0, 50000)) },
		"star7":  func(p *pen) { p.star(7, p.pin("adj", 34601, 0, 50000)) },
		"star8":  func(p *pen) { p.star(8, p.pin("adj", 37500, 0, 50000)) },
		"star10": func(p *pen) { p.star(10, p.pin("adj", 42533, 0, 50000)) },
		"star12": func(p *pen) { p.star(12, p.pin("adj", 37500, 0, 50000)) },
		"star16": func(p *pen) { p.star(16, p.pin("adj", 37500, 0, 50000)) },
		"star24": func(p *pen) { p.star(24, p.pin("adj", 37500, 0, 50000)) },
		"star32": func(p *pen) { p.star(32, p.pin("adj", 37500, 0, 50000)) },
		"rightArrow": func(p *pen) {
			maxA2 := 100000 * p.w / p.ss
			a1, a2 := p.pin("adj1", 50000, 0, 100000), p.pin("adj2", 50000, 0, maxA2)
			dx := p.ss * a2 / 100000
			x1 := p.w - dx
			dy := p.h * a1 / 200000
			vc := p.h / 2
			p.poly(0, vc-dy, x1, vc-dy, x1, 0, p.w, vc, x1, p.h, x1, vc+dy, 0, vc+dy)
			p.box(0, vc-dy, x1+dy*dx/(p.h/2), vc+dy)
		},
		"leftArrow": func(p *pen) {
			maxA2 := 100000 * p.w / p.ss
			a1, a2 := p.pin("adj1", 50000, 0, 100000), p.pin("adj2", 50000, 0, maxA2)
			dx := p.ss * a2 / 100000
			dy := p.h * a1 / 200000
			vc := p.h / 2
			p.poly(0, vc, dx, 0, dx, vc-dy, p.w, vc-dy, p.w, vc+dy, dx, vc+dy, dx, p.h)
			p.box(dx-dy*dx/(p.h/2), vc-dy, p.w, vc+dy)
		},
		"upArrow": func(p *pen) {
			maxA2 := 100000 * p.h / p.ss
			a1, a2 := p.pin("adj1", 50000, 0, 100000), p.pin("adj2", 50000, 0, maxA2)
			dy := p.ss * a2 / 100000
			dx := p.w * a1 / 200000
			hc := p.w / 2
			p.poly(0, dy, hc, 0, p.w, dy, hc+dx, dy, hc+dx, p.h, hc-dx, p.h, hc-dx, dy)
			p.box(hc-dx, dy-dx*dy/(p.w/2), hc+dx, p.h)
		},
		"downArrow": func(p *pen) {
			maxA2 := 100000 * p.h / p.ss
			a1, a2 := p.pin("adj1", 50000, 0, 100000), p.pin("adj2", 50000, 0, maxA2)
			dy := p.ss * a2 / 100000
			dx := p.w * a1 / 200000
			hc := p.w / 2
			y1 := p.h - dy
			p.poly(hc-dx, 0, hc+dx, 0, hc+dx, y1, p.w, y1, hc, p.h, 0, y1, hc-dx, y1)
			p.box(hc-dx, 0, hc+dx, y1+dx*dy/(p.w/2))
		},
		"leftRightArrow": func(p *pen) {
			maxA2 := 50000 * p.w / p.ss
			a1, a2 := p.pin("adj1", 50000, 0, 100000), p.pin("adj2", 50000, 0, maxA2)
			dx := p.ss * a2 / 100000
			dy := p.h * a1 / 200000
			vc := p.h / 2
			p.poly(0, vc, dx, 0, dx, vc-dy, p.w-dx, vc-dy, p.w-dx, 0, p.w, vc, p.w-dx, p.h, p.w-dx, vc+dy, dx, vc+dy, dx, p.h)
			p.box(dx/2, vc-dy, p.w-dx/2, vc+dy)
		},
		"upDownArrow": func(p *pen) {
			maxA2 := 50000 * p.h / p.ss
			a1, a2 := p.pin("adj1", 50000, 0, 100000), p.pin("adj2", 50000, 0, maxA2)
			dy := p.ss * a2 / 100000
			dx := p.w * a1 / 200000
			hc := p.w / 2
			p.poly(hc, 0, p.w, dy, hc+dx, dy, hc+dx, p.h-dy, p.w, p.h-dy, hc, p.h, 0, p.h-dy, hc-dx, p.h-dy, hc-dx, dy, 0, dy)
			p.box(hc-dx, dy/2, hc+dx, p.h-dy/2)
		},
		"quadArrow": func(p *pen) {
			a2 := p.pin("adj2", 22500, 0, 50000)
			a1 := p.pin("adj1", 22500, 0, 2*a2)
			a3 := p.pin("adj3", 22500, 0, 50000-a2)
			x := p.ss * a3 / 100000
			d2 := p.ss * a2 / 100000
			d1 := p.ss * a1 / 200000
			hc, vc := p.w/2, p.h/2
			p.poly(0, vc, x, vc-d2, x, vc-d1, hc-d1, vc-d1, hc-d1, x, hc-d2, x, hc, 0, hc+d2, x, hc+d1, x, hc+d1, vc-d1,
				p.w-x, vc-d1, p.w-x, vc-d2, p.w, vc, p.w-x, vc+d2, p.w-x, vc+d1, hc+d1, vc+d1, hc+d1, p.h-x, hc+d2, p.h-x,
				hc, p.h, hc-d2, p.h-x, hc-d1, p.h-x, hc-d1, vc+d1, x, vc+d1, x, vc+d2)
		},
		"notchedRightArrow": func(p *pen) {
			maxA2 := 100000 * p.w / p.ss
			a1, a2 := p.pin("adj1", 50000, 0, 100000), p.pin("adj2", 50000, 0, maxA2)
			dx := p.ss * a2 / 100000
			x2 := p.w - dx
			dy := p.h * a1 / 200000
			vc := p.h / 2
			x1 := dy * dx / (p.h / 2)
			p.poly(0, vc-dy, x2, vc-dy, x2, 0, p.w, vc, x2, p.h, x2, vc+dy, 0, vc+dy, x1, vc)
		},
		"stripedRightArrow": func(p *pen) {
			maxA2 := 84375 * p.w / p.ss
			a1, a2 := p.pin("adj1", 50000, 0, 100000), p.pin("adj2", 50000, 0, maxA2)
			x4 := p.ss * a2 / 100000
			dy := p.h * a1 / 200000
			vc := p.h / 2
			p.rect(0, vc-dy, p.ss*3125/100000, 2*dy)
			p.rect(p.ss*6250/100000, vc-dy, p.ss*6250/100000, 2*dy)
			x := p.ss * 15625 / 100000
			p.poly(x, vc-dy, p.w-x4, vc-dy, p.w-x4, 0, p.w, vc, p.w-x4, p.h, p.w-x4, vc+dy, x, vc+dy)
		},
		"bentArrow": func(p *pen) {
			a2 := p.pin("adj2", 25000, 0, 50000)
			a1 := p.pin("adj1", 25000, 0, 2*a2)
			a3 := p.pin("adj3", 25000, 0, 50000)
			th := p.ss * a1 / 100000
			aw2 := p.ss * a2 / 100000
			th2 := th / 2
			dh2 := aw2 - th2
			ah := p.ss * a3 / 100000
			bw := p.w - ah
			bh := p.h - dh2
			bs := min(bw, bh)
			a4 := p.pin("adj4", 43750, 0, 100000*bs/p.ss)
			bd := p.ss * a4 / 100000
			bd2 := max(bd-th, 0)
			y3 := aw2 + aw2
			p.m(0, p.h).l(0, dh2+bd).arc(bd, bd, 180, 90).l(bw, dh2).l(bw, 0).l(p.w, aw2).l(bw, y3).l(bw, dh2+th).
				l(th+bd2, dh2+th).arc(bd2, bd2, 270, -90).l(th, p.h).z()
		},
		"uturnArrow": func(p *pen) {
			a2 := p.pin("adj2", 25000, 0, 25000)
			a1 := p.pin("adj1", 25000, 0, 2*a2)
			a3 := p.pin("adj3", 25000, 0, 50000)
			a4 := p.pin("adj4", 43750, 0, 50000)
			th := p.ss * a1 / 100000
			aw2 := p.ss * a2 / 100000
			ah := p.ss * a3 / 100000
			bd := p.ss * a4 / 100000
			x8 := p.w
			x6 := x8 - aw2
			x4 := x6 - th/2
			x5 := x6 + th/2
			y5 := p.h * p.a("adj5", 75000) / 100000
			y4 := y5 - ah
			p.m(0, p.h).l(0, bd).arc(bd, bd, 180, 90).l(x4+th/2-bd+th/2, 0).arc(bd, bd, 270, 90).l(x5, y4).l(x8, y4).l(x6, y5).
				l(x4-aw2+th/2, y4).l(x4, y4).l(x4, max(bd, th)).arc(max(bd-th, 0), max(bd-th, 0), 0, -90).l(th+max(bd-th, 0), th).
				arc(max(bd-th, 0), max(bd-th, 0), 270, -90).l(th, p.h).z()
		},
		"chevron": func(p *pen) {
			maxAdj := 100000 * p.w / p.ss
			x := p.ss * p.pin("adj", 50000, 0, maxAdj) / 100000
			x2 := p.w - x
			p.poly(0, 0, x2, 0, p.w, p.h/2, x2, p.h, 0, p.h, x, p.h/2)
			p.box(min(x, x2), 0, max(x, x2), p.h)
		},
		"homePlate": func(p *pen) {
			maxAdj := 100000 * p.w / p.ss
			x := p.ss * p.pin("adj", 50000, 0, maxAdj) / 100000
			x1 := p.w - x
			p.poly(0, 0, x1, 0, p.w, p.h/2, x1, p.h, 0, p.h)
			p.box(0, 0, (x1+p.w)/2, p.h)
		},
		"wedgeRectCallout":      func(p *pen) { presetRectCallout(p, 0) },
		"wedgeRoundRectCallout": func(p *pen) { presetRectCallout(p, p.pin("adj3", 16667, 0, 50000)) },
		"wedgeEllipseCallout": func(p *pen) {
			dxPos, dyPos := p.w*p.a("adj1", -20833)/100000, p.h*p.a("adj2", 62500)/100000
			hc, vc := p.w/2, p.h/2
			xPos, yPos := hc+dxPos, vc+dyPos
			ang := math.Atan2(dyPos*p.w/p.h, dxPos)
			const spread = 11.0 * math.Pi / 180
			a0 := ang + spread
			pt := func(a float64) (float64, float64) { return hc + hc*math.Cos(a), vc + vc*math.Sin(a) }
			x0, y0 := pt(a0)
			p.m(x0, y0).arc(hc, vc, a0DrawingML(p, a0), 360-(2*spread*180/math.Pi)).l(xPos, yPos).z()
			dx, dy := p.w/2*(1-math.Sqrt2/2), p.h/2*(1-math.Sqrt2/2)
			p.box(dx, dy, p.w-dx, p.h-dy)
		},
		"cloudCallout": func(p *pen) {
			presetCloud(p)
			hc, vc := p.w/2, p.h/2
			tx, ty := hc+p.w*p.a("adj1", -20833)/100000, vc+p.h*p.a("adj2", 62500)/100000
			g := math.Hypot(tx-hc, ty-vc)
			if g > 0 {
				r := p.ss / 12
				p.begin("norm", true)
				for i, f := range []float64{1, 0.8, 0.62} {
					cx, cy := tx+(hc-tx)*float64(i)*0.14*f, ty+(vc-ty)*float64(i)*0.14*f
					rr := r * (0.35 + 0.3*float64(i))
					p.ellipse(cx-rr, cy-rr, 2*rr, 2*rr)
				}
			}
		},
		"borderCallout1": func(p *pen) {
			p.rect(0, 0, p.w, p.h)
			y1, x1 := p.h*p.a("adj1", 18750)/100000, p.w*p.a("adj2", -8333)/100000
			y2, x2 := p.h*p.a("adj3", 112500)/100000, p.w*p.a("adj4", -38333)/100000
			p.begin("none", true).m(x1, y1).l(x2, y2)
		},
		"line":               func(p *pen) { p.begin("none", true).m(0, 0).l(p.w, p.h) },
		"lineInv":            func(p *pen) { p.begin("none", true).m(0, p.h).l(p.w, 0) },
		"straightConnector1": func(p *pen) { p.begin("none", true).m(0, 0).l(p.w, p.h) },
		"bentConnector2":     func(p *pen) { p.begin("none", true).line(0, 0, p.w, 0, p.w, p.h) },
		"bentConnector3": func(p *pen) {
			x := p.w * p.a("adj1", 50000) / 100000
			p.begin("none", true).line(0, 0, x, 0, x, p.h, p.w, p.h)
		},
		"bentConnector4": func(p *pen) {
			x, y := p.w*p.a("adj1", 50000)/100000, p.h*p.a("adj2", 50000)/100000
			p.begin("none", true).line(0, 0, x, 0, x, y, p.w, y, p.w, p.h)
		},
		"bentConnector5": func(p *pen) {
			x1, y, x2 := p.w*p.a("adj1", 50000)/100000, p.h*p.a("adj2", 50000)/100000, p.w*p.a("adj3", 50000)/100000
			p.begin("none", true).line(0, 0, x1, 0, x1, y, x2, y, x2, p.h, p.w, p.h)
		},
		"curvedConnector2": func(p *pen) { p.begin("none", true).m(0, 0).c(p.w/2, 0, p.w, p.h/2, p.w, p.h) },
		"curvedConnector3": func(p *pen) {
			x := p.w * p.a("adj1", 50000) / 100000
			p.begin("none", true).m(0, 0).c(x/2, 0, x, p.h/4, x, p.h/2).c(x, p.h*3/4, (x+p.w)/2, p.h, p.w, p.h)
		},
		"curvedConnector4": func(p *pen) {
			x := p.w * p.a("adj1", 50000) / 100000
			y := p.h * p.a("adj2", 50000) / 100000
			p.begin("none", true).m(0, 0).c(x/2, 0, x, y/2, x, y).c(x, y+(p.h-y)/2, (x+p.w)/2, p.h, p.w, p.h)
		},
		"curvedConnector5": func(p *pen) {
			x := p.w * p.a("adj1", 50000) / 100000
			p.begin("none", true).m(0, 0).c(x/2, 0, x, p.h/4, x, p.h/2).c(x, p.h*3/4, (x+p.w)/2, p.h, p.w, p.h)
		},
		"plus": func(p *pen) {
			x := p.ss * p.pin("adj", 25000, 0, 50000) / 100000
			p.poly(0, x, x, x, x, 0, p.w-x, 0, p.w-x, x, p.w, x, p.w, p.h-x, p.w-x, p.h-x, p.w-x, p.h, x, p.h, x, p.h-x, 0, p.h-x)
			p.box(x, x, p.w-x, p.h-x)
		},
		"mathPlus": func(p *pen) {
			t := p.h * p.pin("adj1", 23520, 0, 73490) / 200000
			hc, vc := p.w/2, p.h/2
			dx, dy := p.w*73490/200000, p.h*73490/200000
			p.poly(hc-dx, vc-t, hc-t, vc-t, hc-t, vc-dy, hc+t, vc-dy, hc+t, vc-t, hc+dx, vc-t, hc+dx, vc+t, hc+t, vc+t, hc+t, vc+dy, hc-t, vc+dy, hc-t, vc+t, hc-dx, vc+t)
		},
		"mathMinus": func(p *pen) {
			t := p.h * p.pin("adj1", 23520, 0, 100000) / 200000
			dx := p.w * 73490 / 200000
			p.rect(p.w/2-dx, p.h/2-t, 2*dx, 2*t)
		},
		"mathEqual": func(p *pen) {
			t := p.h * p.pin("adj1", 23520, 0, 36745) / 100000
			g := p.h * p.pin("adj2", 11760, 0, 100000) / 100000
			dx := p.w * 73490 / 200000
			p.rect(p.w/2-dx, p.h/2-g/2-t, 2*dx, t)
			p.rect(p.w/2-dx, p.h/2+g/2, 2*dx, t)
		},
		"donut": func(p *pen) {
			d := p.ss * p.pin("adj", 25000, 0, 50000) / 100000
			p.ellipse(0, 0, p.w, p.h)
			// The hole winds the other way.
			p.m(d, p.h/2).arc(p.w/2-d, p.h/2-d, 180, -360).z()
		},
		"noSmoking": func(p *pen) {
			d := p.ss * p.pin("adj", 18750, 0, 50000) / 100000
			hc, vc := p.w/2, p.h/2
			p.ellipse(0, 0, p.w, p.h)
			// The two holes are the inner ellipse either side of a bar along the diagonal; they wind the other way.
			rx, ry := hc-d, vc-d
			ang := math.Atan2(p.h, p.w)
			t := math.Asin(clampF(d/2/math.Hypot(rx*math.Sin(ang), ry*math.Cos(ang)), -1, 1))
			hole := func(from, to float64) {
				p.m(hc+rx*math.Cos(from), vc+ry*math.Sin(from))
				p.arcToward(rx, ry, hc, vc, from, to)
				p.z()
			}
			hole(ang-t+2*math.Pi, ang+math.Pi+t)
			hole(ang+math.Pi-t, ang+t)
		},
		"blockArc": func(p *pen) {
			st, sw := p.a("adj1", 10800000)/60000, p.a("adj2", 0)/60000
			d := p.ss * p.pin("adj3", 25000, 0, 50000) / 100000
			end := sw
			span := math.Mod(end-st+720, 360)
			if span == 0 {
				span = 360
			}
			arcPoint := func(rx, ry, a float64) (float64, float64) {
				t := visualParam(rx, ry, a)
				return p.w/2 + rx*math.Cos(t), p.h/2 + ry*math.Sin(t)
			}
			rx, ry := p.w/2, p.h/2
			x0, y0 := arcPoint(rx, ry, st)
			p.m(x0, y0).arc(rx, ry, st, span)
			x1, y1 := arcPoint(rx-d, ry-d, end)
			p.l(x1, y1).arc(rx-d, ry-d, end, -span).z()
		},
		"pie": func(p *pen) {
			st, end := p.a("adj1", 0)/60000, p.a("adj2", 16200000)/60000
			span := math.Mod(end-st+720, 360)
			if span == 0 {
				span = 360
			}
			rx, ry := p.w/2, p.h/2
			t := visualParam(rx, ry, st)
			p.m(rx+rx*math.Cos(t), ry+ry*math.Sin(t)).arc(rx, ry, st, span).l(rx, ry).z()
		},
		"chord": func(p *pen) {
			st, end := p.a("adj1", 2700000)/60000, p.a("adj2", 16200000)/60000
			span := math.Mod(end-st+720, 360)
			rx, ry := p.w/2, p.h/2
			t := visualParam(rx, ry, st)
			p.m(rx+rx*math.Cos(t), ry+ry*math.Sin(t)).arc(rx, ry, st, span).z()
		},
		"arc": func(p *pen) {
			st, end := p.a("adj1", 16200000)/60000, p.a("adj2", 0)/60000
			span := math.Mod(end-st+720, 360)
			rx, ry := p.w/2, p.h/2
			t := visualParam(rx, ry, st)
			x, y := rx+rx*math.Cos(t), ry+ry*math.Sin(t)
			p.begin("norm", false).m(x, y).arc(rx, ry, st, span).l(rx, ry).z()
			p.begin("none", true).m(x, y).arc(rx, ry, st, span)
		},
		"can": func(p *pen) {
			maxAdj := 50000 * p.h / p.ss
			y1 := p.ss * p.pin("adj", 25000, 0, maxAdj) / 200000
			rx := p.w / 2
			p.begin("norm", false).m(0, y1).arc(rx, y1, 180, -180).l(p.w, p.h-y1).arc(rx, y1, 0, 180).z()
			p.begin("lighten", false).m(0, y1).arc(rx, y1, 180, 180).arc(rx, y1, 0, 180).z()
			p.begin("none", true).m(0, y1).arc(rx, y1, 180, 180).arc(rx, y1, 0, 180).l(p.w, p.h-y1).arc(rx, y1, 0, 180).z()
			p.box(0, 2*y1, p.w, p.h-y1)
		},
		"cube": func(p *pen) {
			y1 := p.ss * p.pin("adj", 25000, 0, 100000) / 100000
			x4 := p.w - y1
			y4 := p.h - y1
			p.begin("norm", false).poly(0, y1, x4, y1, x4, p.h, 0, p.h)
			p.begin("darkenLess", false).poly(x4, y1, p.w, 0, p.w, y4, x4, p.h)
			p.begin("lightenLess", false).poly(0, y1, y1, 0, p.w, 0, x4, y1)
			p.begin("none", true).poly(0, y1, y1, 0, p.w, 0, p.w, y4, x4, p.h, 0, p.h).line(0, y1, x4, y1, p.w, 0).m(x4, y1).l(x4, p.h)
			p.box(0, y1, x4, p.h)
		},
		"bevel": func(p *pen) {
			x := p.ss * p.pin("adj", 12500, 0, 50000) / 100000
			r, b := p.w-x, p.h-x
			p.begin("norm", false).rect(x, x, r-x, b-x)
			p.begin("lightenLess", false).poly(0, 0, p.w, 0, r, x, x, x)
			p.begin("darkenLess", false).poly(0, p.h, x, b, r, b, p.w, p.h)
			p.begin("lighten", false).poly(0, 0, x, x, x, b, 0, p.h)
			p.begin("darken", false).poly(p.w, 0, p.w, p.h, r, b, r, x)
			p.begin("none", true).rect(0, 0, p.w, p.h).rect(x, x, r-x, b-x).line(0, 0, x, x).line(p.w, 0, r, x).line(p.w, p.h, r, b).line(0, p.h, x, b)
			p.box(x, x, r, b)
		},
		"frame": func(p *pen) {
			x := p.ss * p.pin("adj1", 12500, 0, 50000) / 100000
			p.rect(0, 0, p.w, p.h)
			p.poly(x, x, x, p.h-x, p.w-x, p.h-x, p.w-x, x)
			p.box(x, x, p.w-x, p.h-x)
		},
		"halfFrame": func(p *pen) {
			x := p.ss * p.pin("adj2", 33333, 0, 100000) / 100000
			y := p.ss * p.pin("adj1", 33333, 0, 100000) / 100000
			p.poly(0, 0, p.w, 0, p.w-y*p.w/p.h, y, x, y, x, p.h-x*p.h/p.w, 0, p.h)
		},
		"corner": func(p *pen) {
			y := p.ss * p.pin("adj1", 50000, 0, 100000) / 100000
			x := p.ss * p.pin("adj2", 50000, 0, 100000) / 100000
			p.poly(0, 0, x, 0, x, p.h-y, p.w, p.h-y, p.w, p.h, 0, p.h)
		},
		"diagStripe": func(p *pen) {
			f := p.pin("adj", 50000, 0, 100000) / 100000
			p.poly(0, p.h*f, p.w*f, 0, p.w, 0, 0, p.h)
		},
		"foldedCorner": func(p *pen) {
			d := p.ss * p.pin("adj", 16667, 0, 50000) / 100000
			y := p.h - d
			x := p.w - d
			p.begin("norm", false).poly(0, 0, p.w, 0, p.w, y, x, p.h, 0, p.h)
			p.begin("darkenLess", false).poly(x+d/5, y+d/5, p.w, y, x, p.h)
			p.begin("none", true).poly(0, 0, p.w, 0, p.w, y, x, p.h, 0, p.h).line(x, p.h, x+d/5, y+d/5, p.w, y)
			p.box(0, 0, p.w, y)
		},
		"plaque": func(p *pen) {
			x := p.ss * p.pin("adj", 16667, 0, 50000) / 100000
			p.m(0, x).arc(x, x, 90, -90).l(p.w-x, 0).arc(x, x, 180, -90).l(p.w, p.h-x).arc(x, x, 270, -90).l(x, p.h).arc(x, x, 0, -90).z()
			p.box(x*0.29289, x*0.29289, p.w-x*0.29289, p.h-x*0.29289)
		},
		"teardrop": func(p *pen) {
			f := p.pin("adj", 100000, 0, 200000) / 100000
			hc, vc := p.w/2, p.h/2
			x, y := hc+hc*f, vc-vc*f
			p.m(0, vc).c(0, vc/2, hc/2, 0, hc, 0).q(hc+(x-hc)/2+hc/4, 0, x, y).q(p.w, vc/2+y/2, p.w, vc).c(p.w, vc*1.5, hc*1.5, p.h, hc, p.h).c(hc/2, p.h, 0, vc*1.5, 0, vc).z()
		},
		"sun": func(p *pen) {
			f := p.pin("adj", 25000, 12500, 46875) / 100000
			hc, vc := p.w/2, p.h/2
			for i := 0; i < 8; i++ {
				a := float64(i) * math.Pi / 4
				s, c := math.Sincos(a)
				ax, ay := 0.62, 0.11
				tip := [3][2]float64{{1, 0}, {ax, ay}, {ax, -ay}}
				for k, t := range tip {
					x, y := t[0]*c-t[1]*s, t[0]*s+t[1]*c
					if k == 0 {
						p.m(hc+x*hc, vc+y*vc)
					} else {
						p.l(hc+x*hc, vc+y*vc)
					}
				}
				p.z()
			}
			r := 0.5 - f + 0.05
			rx, ry := p.w*r, p.h*r
			p.ellipse(hc-rx, vc-ry, 2*rx, 2*ry)
		},
		"moon": func(p *pen) {
			x := p.w * p.pin("adj", 50000, 0, 87500) / 100000
			p.m(p.w, p.h).arc(p.w, p.h/2, 90, 180).arc(p.w-x, p.h/2, 270, -180).z()
			p.box(x/2, p.h/4, x, p.h*3/4)
		},
		"heart": func(p *pen) {
			hc := p.w / 2
			dx1, dx2 := p.w*49/48, p.w*10/48
			y1 := -p.h / 3
			p.m(hc, p.h/4).c(hc+dx2, y1, hc+dx1, p.h/4, hc, p.h).c(hc-dx1, p.h/4, hc-dx2, y1, hc, p.h/4).z()
			p.box(p.w/4, p.h/8, p.w*3/4, p.h*5/8)
		},
		"lightningBolt": func(p *pen) {
			u := func(x, y float64) (float64, float64) { return p.w * x / 21600, p.h * y / 21600 }
			pts := [][2]float64{{8472, 0}, {12860, 6080}, {11050, 6797}, {16577, 12007}, {14767, 12877}, {21600, 21600}, {10012, 14915}, {12222, 13987}, {5022, 9705}, {7602, 8382}, {0, 3890}}
			for i, q := range pts {
				x, y := u(q[0], q[1])
				if i == 0 {
					p.m(x, y)
				} else {
					p.l(x, y)
				}
			}
			p.z()
			p.box(p.w*0.38, p.h*0.3, p.w*0.72, p.h*0.68)
		},
		"cloud": presetCloud,
		"smileyFace": func(p *pen) {
			p.begin("norm", true).ellipse(0, 0, p.w, p.h)
			r := p.ss * 0.06
			p.begin("darkenLess", true).ellipse(p.w*0.35-r, p.h*0.36-r, 2*r, 2*r).ellipse(p.w*0.65-r, p.h*0.36-r, 2*r, 2*r)
			smile := p.pin("adj", 4653, -4653, 4653) / 100000
			p.begin("none", true).m(p.w*0.25, p.h*(0.74-smile)).c(p.w*0.4, p.h*(0.74+2*smile*4), p.w*0.6, p.h*(0.74+2*smile*4), p.w*0.75, p.h*(0.74-smile))
		},
		"bracketPair": func(p *pen) {
			x := p.ss * p.pin("adj", 16667, 0, 50000) / 100000
			p.begin("norm", false).m(0, x).arc(x, x, 180, 90).l(p.w-x, 0).arc(x, x, 270, 90).l(p.w, p.h-x).arc(x, x, 0, 90).l(x, p.h).arc(x, x, 90, 90).z()
			p.begin("none", true).m(x, p.h).arc(x, x, 90, 90).l(0, x).arc(x, x, 180, 90)
			p.m(p.w-x, 0).arc(x, x, 270, 90).l(p.w, p.h-x).arc(x, x, 0, 90)
		},
		"bracePair": func(p *pen) {
			x := p.ss * p.pin("adj", 8333, 0, 25000) / 100000
			vc := p.h / 2
			p.begin("none", true).m(2*x, p.h).arc(x, x, 90, 90).l(x, vc+x).arc(x, x, 0, -90).arc(x, x, 90, -90).l(x, x).arc(x, x, 180, 90)
			p.m(p.w-2*x, 0).arc(x, x, 270, 90).l(p.w-x, vc-x).arc(x, x, 180, -90).arc(x, x, 270, -90).l(p.w-x, p.h-x).arc(x, x, 0, 90)
		},
		"leftBracket": func(p *pen) {
			y := p.ss * p.pin("adj", 8333, 0, 50000) / 100000
			p.begin("none", true).m(p.w, p.h).arc(p.w, y, 90, 90).l(0, y).arc(p.w, y, 180, 90)
		},
		"rightBracket": func(p *pen) {
			y := p.ss * p.pin("adj", 8333, 0, 50000) / 100000
			p.begin("none", true).m(0, 0).arc(p.w, y, 270, 90).l(p.w, p.h-y).arc(p.w, y, 0, 90)
		},
		"leftBrace": func(p *pen) {
			y := p.ss * p.pin("adj1", 8333, 0, 25000) / 100000
			mid := p.h * p.pin("adj2", 50000, 0, 100000) / 100000
			hw := p.w / 2
			p.begin("none", true).m(p.w, p.h).arc(hw, y, 90, 90).l(hw, mid+y).arc(hw, y, 0, -90).arc(hw, y, 90, -90).l(hw, y).arc(hw, y, 180, 90)
		},
		"rightBrace": func(p *pen) {
			y := p.ss * p.pin("adj1", 8333, 0, 25000) / 100000
			mid := p.h * p.pin("adj2", 50000, 0, 100000) / 100000
			hw := p.w / 2
			p.begin("none", true).m(0, 0).arc(hw, y, 270, 90).l(hw, mid-y).arc(hw, y, 180, -90).arc(hw, y, 270, -90).l(hw, p.h-y).arc(hw, y, 0, 90)
		},
		"flowChartProcess": func(p *pen) { p.rect(0, 0, p.w, p.h) },
		"flowChartDecision": func(p *pen) {
			p.poly(0, p.h/2, p.w/2, 0, p.w, p.h/2, p.w/2, p.h)
			p.box(p.w/4, p.h/4, p.w*3/4, p.h*3/4)
		},
		"flowChartConnector": func(p *pen) { p.ellipse(0, 0, p.w, p.h) },
		"flowChartTerminator": func(p *pen) {
			rx, ry := p.w*3475/21600, p.h*10800/21600
			p.m(rx, 0).l(p.w-rx, 0).arc(rx, ry, 270, 180).l(rx, p.h).arc(rx, ry, 90, 180).z()
			p.box(p.w*1018/21600, p.h*3163/21600, p.w*20582/21600, p.h*18437/21600)
		},
		"flowChartAlternateProcess": func(p *pen) {
			x := p.ss / 6
			p.m(0, x).arc(x, x, 180, 90).l(p.w-x, 0).arc(x, x, 270, 90).l(p.w, p.h-x).arc(x, x, 0, 90).l(x, p.h).arc(x, x, 90, 90).z()
		},
		"flowChartInputOutput": func(p *pen) {
			x := p.w / 5
			p.poly(x, 0, p.w, 0, p.w-x, p.h, 0, p.h)
			p.box(x, 0, p.w-x, p.h)
		},
		"flowChartPredefinedProcess": func(p *pen) {
			x := p.w / 8
			p.begin("norm", false).rect(0, 0, p.w, p.h)
			p.begin("none", true).rect(0, 0, p.w, p.h).line(x, 0, x, p.h).line(p.w-x, 0, p.w-x, p.h)
			p.box(x, 0, p.w-x, p.h)
		},
		"flowChartInternalStorage": func(p *pen) {
			x := p.w / 8
			y := p.h / 8
			p.begin("norm", false).rect(0, 0, p.w, p.h)
			p.begin("none", true).rect(0, 0, p.w, p.h).line(x, 0, x, p.h).line(0, y, p.w, y)
			p.box(x, y, p.w, p.h)
		},
		"flowChartDocument": func(p *pen) {
			f := func(v float64) float64 { return p.h * v / 21600 }
			p.m(0, 0).l(p.w, 0).l(p.w, f(17322)).c(p.w*0.5, f(17322), p.w*0.5, f(23922), 0, f(20172)).z()
			p.box(0, 0, p.w, f(17322))
		},
		"flowChartPreparation": func(p *pen) {
			x := p.w / 5
			p.poly(0, p.h/2, x, 0, p.w-x, 0, p.w, p.h/2, p.w-x, p.h, x, p.h)
			p.box(x, 0, p.w-x, p.h)
		},
		"flowChartManualInput": func(p *pen) {
			p.poly(0, p.h/5, p.w, 0, p.w, p.h, 0, p.h)
			p.box(0, p.h/5, p.w, p.h)
		},
		"flowChartManualOperation": func(p *pen) {
			p.poly(0, 0, p.w, 0, p.w*4/5, p.h, p.w/5, p.h)
			p.box(p.w/5, 0, p.w*4/5, p.h)
		},
		"flowChartMerge":   func(p *pen) { p.poly(0, 0, p.w, 0, p.w/2, p.h); p.box(p.w/4, 0, p.w*3/4, p.h/2) },
		"flowChartExtract": func(p *pen) { p.poly(0, p.h, p.w/2, 0, p.w, p.h); p.box(p.w/4, p.h/2, p.w*3/4, p.h) },
		"flowChartOffpageConnector": func(p *pen) {
			p.poly(0, 0, p.w, 0, p.w, p.h*4/5, p.w/2, p.h, 0, p.h*4/5)
			p.box(0, 0, p.w, p.h*4/5)
		},
	}
}

func cosd(a float64) float64 { return math.Cos(a * math.Pi / 180) }
func sind(a float64) float64 { return math.Sin(a * math.Pi / 180) }

// visualParam is the parameter on an ellipse of the point seen at the angle a, in degrees.
func visualParam(wr, hr, a float64) float64 {
	r := a * math.Pi / 180
	if wr == 0 || hr == 0 {
		return r
	}
	s, c := math.Sincos(r)
	return math.Atan2(s/hr, c/wr)
}

// a0DrawingML is the visual angle, in degrees, of the point at parameter t on the shape's ellipse.
func a0DrawingML(p *pen, t float64) float64 {
	return math.Atan2(p.h/2*math.Sin(t), p.w/2*math.Cos(t)) * 180 / math.Pi
}

// arcToward continues a path along the ellipse at (cx, cy) between two parameters, in radians.
func (p *pen) arcToward(rx, ry, cx, cy, t0, t1 float64) {
	n := max(int(math.Ceil(math.Abs(t1-t0)/(math.Pi/2))), 1)
	step := (t1 - t0) / float64(n)
	k := 4.0 / 3 * math.Tan(step/4)
	for i := 0; i < n; i++ {
		a0 := t0 + float64(i)*step
		a1 := a0 + step
		s0, c0 := math.Sincos(a0)
		s1, c1 := math.Sincos(a1)
		p.c(cx+rx*(c0-k*s0), cy+ry*(s0+k*c0), cx+rx*(c1+k*s1), cy+ry*(s1-k*c1), cx+rx*c1, cy+ry*s1)
	}
}

func presetRoundRect(p *pen) {
	r := p.ss * p.pin("adj", 16667, 0, 50000) / 100000
	p.m(0, r).arc(r, r, 180, 90).l(p.w-r, 0).arc(r, r, 270, 90).l(p.w, p.h-r).arc(r, r, 0, 90).l(r, p.h).arc(r, r, 90, 90).z()
	i := r * 0.29289
	p.box(i, i, p.w-i, p.h-i)
}

// presetRectCallout draws the rectangle callouts: a box, rounded by adj of the shorter side, with a pointed tail.
func presetRectCallout(p *pen, adj float64) {
	dx, dy := p.w*p.a("adj1", -20833)/100000, p.h*p.a("adj2", 62500)/100000
	hc, vc := p.w/2, p.h/2
	tx, ty := hc+dx, vc+dy
	r := p.ss * adj / 100000
	// The tail leaves the side the point is beyond, at the side's first or last stretch, as the preset's formulas do.
	inX, inY := tx >= 0 && tx <= p.w, ty >= 0 && ty <= p.h
	side := 0 // 0 inside, 1 top, 2 right, 3 bottom, 4 left
	if !(inX && inY) {
		if math.Abs(dy*p.w) > math.Abs(dx*p.h) {
			if dy < 0 {
				side = 1
			} else {
				side = 3
			}
		} else if dx > 0 {
			side = 2
		} else {
			side = 4
		}
	}
	w7, w5 := p.w*7/12, p.w*5/12
	h7, h5 := p.h*7/12, p.h*5/12
	x0, x1 := p.w*2/12, w5
	if dx > 0 {
		x0, x1 = w7, p.w*10/12
	}
	y0, y1 := p.h*2/12, h5
	if dy > 0 {
		y0, y1 = h7, p.h*10/12
	}
	p.m(0, r)
	if r > 0 {
		p.arc(r, r, 180, 90)
	}
	if side == 1 {
		p.l(x0, 0).l(tx, ty).l(x1, 0)
	}
	p.l(p.w-r, 0)
	if r > 0 {
		p.arc(r, r, 270, 90)
	}
	if side == 2 {
		p.l(p.w, y0).l(tx, ty).l(p.w, y1)
	}
	p.l(p.w, p.h-r)
	if r > 0 {
		p.arc(r, r, 0, 90)
	}
	if side == 3 {
		p.l(x1, p.h).l(tx, ty).l(x0, p.h)
	}
	p.l(r, p.h)
	if r > 0 {
		p.arc(r, r, 90, 90)
	}
	if side == 4 {
		p.l(0, y1).l(tx, ty).l(0, y0)
	}
	p.z()
	i := r * 0.29289
	p.box(i, i, p.w-i, p.h-i)
}

// presetCloud is the cloud: bumps round an ellipse, each a curve bulging out between two points of it. The preset's
// own outline is a list of eleven arcs; this comes close to it at any size.
func presetCloud(p *pen) {
	hc, vc := p.w/2, p.h/2
	// The points between bumps, as angles, with the bumps' sizes varied as a cloud's are.
	angles := []float64{190, 225, 262, 300, 335, 15, 55, 95, 140}
	bulge := []float64{0.30, 0.30, 0.34, 0.28, 0.30, 0.34, 0.32, 0.34, 0.30}
	at := func(a float64) (float64, float64) { return hc + hc*0.78*cosd(a), vc + vc*0.74*sind(a) }
	x0, y0 := at(angles[0])
	p.begin("norm", true).m(x0, y0)
	for i := range angles {
		a, b := angles[i], angles[(i+1)%len(angles)]
		if b < a {
			b += 360
		}
		x1, y1 := at(b)
		mid := (a + b) / 2
		ox, oy := hc*bulge[i]*cosd(mid), vc*bulge[i]*sind(mid)
		xa, ya := at(a)
		p.c(xa+ox+(xa-x1)*0.1, ya+oy+(ya-y1)*0.1, x1+ox+(x1-xa)*0.1, y1+oy+(y1-ya)*0.1, x1, y1)
	}
	p.z()
	p.box(p.w*0.15, p.h*0.2, p.w*0.85, p.h*0.8)
}

// HasPreset reports that the name is a preset this package draws.
func HasPreset(name string) bool { _, ok := presets[name]; return ok }

// Geometry is a shape's outline at a size.
type Geometry struct {
	Paths []Path
	Text  [4]float64 // the text rectangle: left, top, right, bottom
}

// presetGeometry draws a preset at a size. What is not a known preset is a rectangle, which at least holds the place.
func presetGeometry(name string, w, h float64, adj map[string]float64) Geometry {
	fn := presets[name]
	if fn == nil {
		fn = presets["rect"]
	}
	p := newPen(w, h, adj)
	fn(p)
	return Geometry{Paths: p.paths, Text: p.text}
}

// adjustsOf reads an a:avLst's values, written as "val 12345".
func adjustsOf(av *ox.Node) map[string]float64 {
	if av == nil {
		return nil
	}
	out := map[string]float64{}
	for _, g := range av.Elements("a:gd") {
		f := strings.Fields(g.Attr("fmla"))
		if len(f) == 2 && f[0] == "val" {
			if v, err := strconv.ParseFloat(f[1], 64); err == nil {
				out[g.Attr("name")] = v
			}
		}
	}
	return out
}

// maxGuides and maxPathOps bound what custom geometry may ask for.
const (
	maxGuides  = 4096
	maxPathOps = 1 << 20
)

// guides evaluates a custom geometry's guides.
type guides struct {
	vals map[string]float64
}

func newGuides(w, h float64) *guides {
	ss, ls := min(w, h), max(w, h)
	g := &guides{vals: map[string]float64{
		"w": w, "h": h, "l": 0, "t": 0, "r": w, "b": h, "hc": w / 2, "vc": h / 2, "ss": ss, "ls": ls,
		"wd2": w / 2, "wd3": w / 3, "wd4": w / 4, "wd5": w / 5, "wd6": w / 6, "wd8": w / 8, "wd10": w / 10, "wd12": w / 12, "wd16": w / 16, "wd32": w / 32,
		"hd2": h / 2, "hd3": h / 3, "hd4": h / 4, "hd5": h / 5, "hd6": h / 6, "hd8": h / 8, "hd10": h / 10, "hd12": h / 12, "hd16": h / 16, "hd32": h / 32,
		"ssd2": ss / 2, "ssd4": ss / 4, "ssd6": ss / 6, "ssd8": ss / 8, "ssd16": ss / 16, "ssd32": ss / 32,
		"cd2": 10800000, "cd4": 5400000, "cd8": 2700000, "3cd4": 16200000, "3cd8": 8100000, "5cd8": 13500000, "7cd8": 18900000,
	}}
	return g
}

func (g *guides) get(s string) float64 {
	if v, ok := g.vals[s]; ok {
		return v
	}
	v, _ := strconv.ParseFloat(s, 64)
	return v
}

func (g *guides) eval(fmla string) float64 {
	f := strings.Fields(fmla)
	if len(f) == 0 {
		return 0
	}
	arg := func(i int) float64 {
		if i < len(f) {
			return g.get(f[i])
		}
		return 0
	}
	x, y, z := arg(1), arg(2), arg(3)
	const toRad = math.Pi / 180 / 60000
	switch f[0] {
	case "val":
		return x
	case "*/":
		if z == 0 {
			return 0
		}
		return x * y / z
	case "+-":
		return x + y - z
	case "+/":
		if z == 0 {
			return 0
		}
		return (x + y) / z
	case "?:":
		if x > 0 {
			return y
		}
		return z
	case "abs":
		return math.Abs(x)
	case "at2":
		return math.Atan2(y, x) / toRad
	case "cat2":
		return x * math.Cos(math.Atan2(z, y))
	case "sat2":
		return x * math.Sin(math.Atan2(z, y))
	case "cos":
		return x * math.Cos(y*toRad)
	case "sin":
		return x * math.Sin(y*toRad)
	case "tan":
		return x * math.Tan(y*toRad)
	case "max":
		return max(x, y)
	case "min":
		return min(x, y)
	case "mod":
		return math.Sqrt(x*x + y*y + z*z)
	case "pin":
		if y < x {
			return x
		}
		if y > z {
			return z
		}
		return y
	case "sqrt":
		return math.Sqrt(math.Abs(x))
	}
	return 0
}

// customGeometry draws an a:custGeom at a size. A path's own w and h scale its coordinates to the box.
func customGeometry(cg *ox.Node, w, h float64) Geometry {
	// Guides work in the file's units, EMU.
	g := newGuides(w*emuPerPt, h*emuPerPt)
	n := 0
	load := func(list *ox.Node) {
		for _, e := range list.Elements("a:gd") {
			if n++; n > maxGuides {
				return
			}
			g.vals[e.Attr("name")] = g.eval(e.Attr("fmla"))
		}
	}
	load(cg.Child("a:avLst"))
	load(cg.Child("a:gdLst"))
	out := Geometry{Text: [4]float64{0, 0, w, h}}
	if r := cg.Child("a:rect"); r != nil {
		out.Text = [4]float64{g.get(r.Attr("l")) / emuPerPt, g.get(r.Attr("t")) / emuPerPt, g.get(r.Attr("r")) / emuPerPt, g.get(r.Attr("b")) / emuPerPt}
	}
	p := newPen(w, h, nil)
	ops := 0
	for _, path := range cg.Child("a:pathLst").Elements("a:path") {
		fill := path.Attr("fill")
		if fill == "" {
			fill = "norm"
		}
		p.begin(fill, attrBool(path, "stroke", true))
		sx, sy := 1.0/emuPerPt, 1.0/emuPerPt
		if pw := attrFloat(path, "w", 0); pw > 0 {
			sx = w / pw
		}
		if ph := attrFloat(path, "h", 0); ph > 0 {
			sy = h / ph
		}
		pt := func(n *ox.Node) (float64, float64) { return g.get(n.Attr("x")) * sx, g.get(n.Attr("y")) * sy }
		for _, c := range path.Elements() {
			if ops++; ops > maxPathOps {
				break
			}
			pts := c.Elements("a:pt")
			switch c.Name {
			case "a:moveTo":
				if len(pts) > 0 {
					x, y := pt(pts[0])
					p.m(x, y)
				}
			case "a:lnTo":
				if len(pts) > 0 {
					x, y := pt(pts[0])
					p.l(x, y)
				}
			case "a:quadBezTo":
				if len(pts) > 1 {
					x1, y1 := pt(pts[0])
					x2, y2 := pt(pts[1])
					p.q(x1, y1, x2, y2)
				}
			case "a:cubicBezTo":
				if len(pts) > 2 {
					x1, y1 := pt(pts[0])
					x2, y2 := pt(pts[1])
					x3, y3 := pt(pts[2])
					p.c(x1, y1, x2, y2, x3, y3)
				}
			case "a:arcTo":
				p.arc(g.get(c.Attr("wR"))*sx, g.get(c.Attr("hR"))*sy, g.get(c.Attr("stAng"))/60000, g.get(c.Attr("swAng"))/60000)
			case "a:close":
				p.z()
			}
		}
	}
	out.Paths = p.paths
	return out
}
