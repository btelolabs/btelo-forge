package render

import (
	"image"
	"math"

	"github.com/btelolabs/btelo-forge/internal/office/pdf"
)

// patternSource is the paint of a pattern colour over box of device pixels.
func (r *ctx) patternSource(p *paint, box image.Rectangle) source {
	d := r.doc
	dict := d.Dict(p.pattern)
	if dict == nil {
		return nil
	}
	m := d.MatrixOf(dict["Matrix"]).Mul(r.base)
	switch d.Int(dict["PatternType"], 0) {
	case 1:
		s, _ := d.Resolve(p.pattern).(*pdf.Stream)
		if s != nil {
			return r.tilingSource(s, m, p, box)
		}
	case 2:
		sh := r.shadingSource(dict["Shading"], m, box, true)
		if sh != nil {
			return sh
		}
	}
	return nil
}

func (r *ctx) paintShading(o pdf.Object) {
	if !r.drawing() {
		return
	}
	if r.vecOn() {
		defer r.vecShading(o)
	}
	gs := r.gs
	box := gs.clip.r.Intersect(r.layer.r)
	if box.Empty() {
		return
	}
	src := r.shadingSource(o, gs.ctm, box, false)
	if src == nil {
		return
	}
	m := &mask{r: box, pix: make([]uint8, box.Dx()*box.Dy())}
	for i := range m.pix {
		m.pix[i] = 255
	}
	r.composite(m, [3]uint8{}, src, gs.ca)
}

type lutSource struct {
	inv      pdf.Matrix
	bg       *[3]uint8
	lut      [][3]uint8
	t0, t1   float64
	ext0     bool
	ext1     bool
	bbox     *pdf.Rect
	axial    bool
	x0, y0   float64
	dx, dy   float64
	denom    float64
	r0, r1   float64
	dr, a    float64
	function func(x, y float64) ([3]uint8, bool) // for type 1
}

func (s *lutSource) at(x, y int) (uint8, uint8, uint8, uint8) {
	px, py := s.inv.Apply(float64(x)+0.5, float64(y)+0.5)
	if s.bbox != nil && !s.bbox.Contains(px, py) {
		return 0, 0, 0, 0
	}
	if s.function != nil {
		if c, ok := s.function(px, py); ok {
			return c[0], c[1], c[2], 255
		}
		return s.none()
	}
	var t float64
	ok := false
	if s.axial {
		t, ok = 0, true
		if s.denom != 0 {
			t = ((px-s.x0)*s.dx + (py-s.y0)*s.dy) / s.denom
		}
		if t < 0 {
			if !s.ext0 {
				ok = false
			}
			t = 0
		} else if t > 1 {
			if !s.ext1 {
				ok = false
			}
			t = 1
		}
	} else {
		t, ok = s.radial(px, py)
	}
	if !ok {
		return s.none()
	}
	c := s.lut[int(t*float64(len(s.lut)-1)+0.5)]
	return c[0], c[1], c[2], 255
}

func (s *lutSource) none() (uint8, uint8, uint8, uint8) {
	if s.bg != nil {
		return s.bg[0], s.bg[1], s.bg[2], 255
	}
	return 0, 0, 0, 0
}

// radial finds the largest t whose circle passes through the point.
func (s *lutSource) radial(px, py float64) (float64, bool) {
	cx, cy := px-s.x0, py-s.y0
	b := cx*s.dx + cy*s.dy + s.r0*s.dr
	c := cx*cx + cy*cy - s.r0*s.r0
	var roots [2]float64
	n := 0
	if s.a == 0 {
		if b == 0 {
			return 0, false
		}
		roots[0] = c / (2 * b)
		n = 1
	} else {
		disc := b*b - s.a*c
		if disc < 0 {
			return 0, false
		}
		q := math.Sqrt(disc)
		roots[0], roots[1] = (b+q)/s.a, (b-q)/s.a
		if roots[0] < roots[1] {
			roots[0], roots[1] = roots[1], roots[0]
		}
		n = 2
	}
	for _, t := range roots[:n] {
		if s.r0+t*s.dr < 0 {
			continue
		}
		switch {
		case t >= 0 && t <= 1:
			return t, true
		case t < 0 && s.ext0:
			return 0, true
		case t > 1 && s.ext1:
			return 1, true
		}
	}
	return 0, false
}

func (r *ctx) shadingSource(o pdf.Object, m pdf.Matrix, box image.Rectangle, background bool) source {
	d := r.doc
	dict := d.Dict(o)
	if dict == nil {
		return nil
	}
	inv, ok := m.Invert()
	if !ok {
		return nil
	}
	cs := r.parseColorSpace(dict["ColorSpace"], r.res, 0)
	if cs.comps() == 0 {
		return nil
	}
	s := &lutSource{inv: inv}
	if v := d.Floats(dict["Background"]); background && len(v) >= cs.comps() {
		c := toBytes(cs.rgb(v))
		s.bg = &c
	}
	if b, ok := d.RectOf(dict["BBox"]); ok {
		s.bbox = &b
	}
	var fn function
	if dict["Function"] != nil {
		fn = parseFunction(d, dict["Function"], 0)
	}
	colorAt := func(in []float64) [3]uint8 {
		v := fn.eval(in)
		want := cs.comps()
		for len(v) < want {
			v = append(v, 0)
		}
		return toBytes(cs.rgb(v[:want]))
	}
	kind := d.Int(dict["ShadingType"], 0)
	switch kind {
	case 1:
		if fn == nil {
			return nil
		}
		domain := []float64{0, 1, 0, 1}
		if v := d.Floats(dict["Domain"]); len(v) >= 4 {
			domain = v
		}
		toDomain, ok := d.MatrixOf(dict["Matrix"]).Invert()
		if !ok {
			return nil
		}
		s.function = func(x, y float64) ([3]uint8, bool) {
			u, v := toDomain.Apply(x, y)
			if u < domain[0] || u > domain[1] || v < domain[2] || v > domain[3] {
				return [3]uint8{}, false
			}
			return colorAt([]float64{u, v}), true
		}
		return s
	case 2, 3:
		if fn == nil {
			return nil
		}
		coords := d.Floats(dict["Coords"])
		domain := []float64{0, 1}
		if v := d.Floats(dict["Domain"]); len(v) >= 2 {
			domain = v
		}
		if e := d.Array(dict["Extend"]); len(e) >= 2 {
			s.ext0, s.ext1 = d.Bool(e[0], false), d.Bool(e[1], false)
		}
		s.lut = make([][3]uint8, 512)
		for i := range s.lut {
			t := domain[0] + float64(i)/float64(len(s.lut)-1)*(domain[1]-domain[0])
			s.lut[i] = colorAt([]float64{t})
		}
		if kind == 2 {
			if len(coords) < 4 {
				return nil
			}
			s.axial = true
			s.x0, s.y0 = coords[0], coords[1]
			s.dx, s.dy = coords[2]-coords[0], coords[3]-coords[1]
			s.denom = s.dx*s.dx + s.dy*s.dy
			return s
		}
		if len(coords) < 6 {
			return nil
		}
		s.x0, s.y0, s.r0 = coords[0], coords[1], coords[2]
		s.dx, s.dy, s.r1 = coords[3]-coords[0], coords[4]-coords[1], coords[5]
		s.dr = s.r1 - s.r0
		s.a = s.dx*s.dx + s.dy*s.dy - s.dr*s.dr
		return s
	case 4, 5, 6, 7:
		st, _ := d.Resolve(o).(*pdf.Stream)
		if st == nil {
			return nil
		}
		return r.meshSource(st, kind, cs, fn, m, box, s.bg)
	}
	return nil
}

// meshSource draws a mesh to a layer once, as triangles of colour.
type meshPic struct {
	l  *layer
	bg *[3]uint8
}

func (s *meshPic) at(x, y int) (uint8, uint8, uint8, uint8) {
	if (image.Point{x, y}).In(s.l.r) {
		i := s.l.offset(x, y)
		if a := s.l.pix[i+3]; a != 0 {
			return s.l.pix[i], s.l.pix[i+1], s.l.pix[i+2], 255
		}
	}
	if s.bg != nil {
		return s.bg[0], s.bg[1], s.bg[2], 255
	}
	return 0, 0, 0, 0
}

type meshVertex struct {
	x, y float64
	c    [3]float64
}

type bitReader struct {
	b   []byte
	pos int
}

func (b *bitReader) read(n int) (uint64, bool) {
	if b.pos+n > len(b.b)*8 {
		return 0, false
	}
	var v uint64
	for range n {
		v = v<<1 | uint64(b.b[b.pos>>3]>>(7-b.pos&7))&1
		b.pos++
	}
	return v, true
}

func (b *bitReader) align() { b.pos = (b.pos + 7) &^ 7 }

func (r *ctx) meshSource(st *pdf.Stream, kind int, cs colorSpace, fn function, m pdf.Matrix, box image.Rectangle, bg *[3]uint8) source {
	d := r.doc
	dict := st.Dict
	data, err := d.Decode(st)
	if err != nil && len(data) == 0 {
		return nil
	}
	bpcoord := d.Int(dict["BitsPerCoordinate"], 16)
	bpcomp := d.Int(dict["BitsPerComponent"], 8)
	bpflag := d.Int(dict["BitsPerFlag"], 8)
	dec := d.Floats(dict["Decode"])
	n := cs.comps()
	if fn != nil {
		n = 1
	}
	if len(dec) < 4+2*n || bpcoord < 1 || bpcoord > 32 || bpcomp < 1 || bpcomp > 16 || bpflag > 8 {
		return nil
	}
	in := &bitReader{b: data}
	coordTop := math.Pow(2, float64(bpcoord)) - 1
	compTop := math.Pow(2, float64(bpcomp)) - 1
	readPoint := func() (point, bool) {
		x, ok1 := in.read(bpcoord)
		y, ok2 := in.read(bpcoord)
		if !ok1 || !ok2 {
			return point{}, false
		}
		px := dec[0] + float64(x)/coordTop*(dec[1]-dec[0])
		py := dec[2] + float64(y)/coordTop*(dec[3]-dec[2])
		a, b := m.Apply(px, py)
		return point{a, b}, true
	}
	readColor := func() ([3]float64, bool) {
		vals := make([]float64, n)
		for i := range n {
			v, ok := in.read(bpcomp)
			if !ok {
				return [3]float64{}, false
			}
			vals[i] = dec[4+2*i] + float64(v)/compTop*(dec[5+2*i]-dec[4+2*i])
		}
		if fn != nil {
			out := fn.eval(vals)
			want := cs.comps()
			for len(out) < want {
				out = append(out, 0)
			}
			return cs.rgb(out[:want]), true
		}
		return cs.rgb(vals), true
	}
	l := newLayer(box)
	tri := func(a, b, c meshVertex) { fillTriangle(l, a, b, c) }
	switch kind {
	case 4:
		var va, vb, vc meshVertex
		for r.op() {
			flag, ok := in.read(bpflag)
			if !ok {
				break
			}
			p, ok1 := readPoint()
			col, ok2 := readColor()
			if !ok1 || !ok2 {
				break
			}
			in.align()
			v := meshVertex{p.x, p.y, col}
			switch flag & 3 {
			case 0:
				va = v
				for i := range 2 {
					if _, ok := in.read(bpflag); !ok {
						break
					}
					q, ok1 := readPoint()
					cc, ok2 := readColor()
					if !ok1 || !ok2 {
						break
					}
					in.align()
					if i == 0 {
						vb = meshVertex{q.x, q.y, cc}
					} else {
						vc = meshVertex{q.x, q.y, cc}
					}
				}
			case 1:
				va, vb, vc = vb, vc, v
			case 2:
				vb, vc = vc, v
			}
			tri(va, vb, vc)
		}
	case 5:
		cols := d.Int(dict["VerticesPerRow"], 0)
		if cols < 2 || cols > 1<<16 {
			return nil
		}
		var prev []meshVertex
		for r.op() {
			row := make([]meshVertex, 0, cols)
			for range cols {
				p, ok1 := readPoint()
				col, ok2 := readColor()
				if !ok1 || !ok2 {
					break
				}
				in.align()
				row = append(row, meshVertex{p.x, p.y, col})
			}
			if len(row) < cols {
				break
			}
			for i := 0; prev != nil && i+1 < cols; i++ {
				tri(prev[i], prev[i+1], row[i])
				tri(prev[i+1], row[i+1], row[i])
			}
			prev = row
		}
	default:
		r.patches(in, kind, bpflag, readPoint, readColor, tri)
	}
	return &meshPic{l: l, bg: bg}
}

// patches reads Coons (6) and tensor (7) patches. A patch is held as its 4×4 control net.
func (r *ctx) patches(in *bitReader, kind, bpflag int, readPoint func() (point, bool), readColor func() ([3]float64, bool), tri func(a, b, c meshVertex)) {
	var net [4][4]point
	var col [4][3]float64 // at the corners (0,0), (0,3), (3,3), (3,0)
	for count := 0; count < 1<<20 && r.op(); count++ {
		flag, ok := in.read(bpflag)
		if !ok {
			return
		}
		flag &= 3
		total := 12
		if kind == 7 {
			total = 16
		}
		var pts [16]point
		start := 0
		var keep [4]point
		var keepC [2][3]float64
		if flag != 0 {
			// The new patch shares an edge of the last.
			switch flag {
			case 1:
				keep = [4]point{net[0][3], net[1][3], net[2][3], net[3][3]}
				keepC = [2][3]float64{col[1], col[2]}
			case 2:
				keep = [4]point{net[3][3], net[3][2], net[3][1], net[3][0]}
				keepC = [2][3]float64{col[2], col[3]}
			case 3:
				keep = [4]point{net[3][0], net[2][0], net[1][0], net[0][0]}
				keepC = [2][3]float64{col[3], col[0]}
			}
			copy(pts[:4], keep[:])
			start = 4
		}
		for i := start; i < total; i++ {
			p, ok := readPoint()
			if !ok {
				return
			}
			pts[i] = p
		}
		if flag != 0 {
			col[0], col[1] = keepC[0], keepC[1]
			for i := 2; i < 4; i++ {
				c, ok := readColor()
				if !ok {
					return
				}
				col[i] = c
			}
		} else {
			for i := range 4 {
				c, ok := readColor()
				if !ok {
					return
				}
				col[i] = c
			}
		}
		in.align()
		// The stream walks round the patch's edge.
		net[0][0], net[0][1], net[0][2], net[0][3] = pts[0], pts[1], pts[2], pts[3]
		net[1][3], net[2][3], net[3][3] = pts[4], pts[5], pts[6]
		net[3][2], net[3][1], net[3][0] = pts[7], pts[8], pts[9]
		net[2][0], net[1][0] = pts[10], pts[11]
		if kind == 7 {
			net[1][1], net[1][2], net[2][2], net[2][1] = pts[12], pts[13], pts[14], pts[15]
		} else {
			coonsInterior(&net)
		}
		drawPatch(&net, &col, tri)
	}
}

// coonsInterior works out the four inner control points of a Coons patch.
func coonsInterior(p *[4][4]point) {
	f := func(a, b, c, d, e, f2, g, h point) point {
		return point{
			(-4*a.x + 6*(b.x+c.x) - 2*(d.x+e.x) + 3*(f2.x+g.x) - h.x) / 9,
			(-4*a.y + 6*(b.y+c.y) - 2*(d.y+e.y) + 3*(f2.y+g.y) - h.y) / 9,
		}
	}
	p[1][1] = f(p[0][0], p[0][1], p[1][0], p[0][3], p[3][0], p[3][1], p[1][3], p[3][3])
	p[1][2] = f(p[0][3], p[0][2], p[1][3], p[0][0], p[3][3], p[3][2], p[1][0], p[3][0])
	p[2][1] = f(p[3][0], p[3][1], p[2][0], p[3][3], p[0][0], p[0][1], p[2][3], p[0][3])
	p[2][2] = f(p[3][3], p[3][2], p[2][3], p[3][0], p[0][3], p[0][2], p[2][0], p[0][0])
}

func drawPatch(p *[4][4]point, col *[4][3]float64, tri func(a, b, c meshVertex)) {
	const steps = 12
	bern := func(t float64) [4]float64 {
		s := 1 - t
		return [4]float64{s * s * s, 3 * s * s * t, 3 * s * t * t, t * t * t}
	}
	var grid [steps + 1][steps + 1]meshVertex
	for i := 0; i <= steps; i++ {
		u := float64(i) / steps
		bu := bern(u)
		for j := 0; j <= steps; j++ {
			v := float64(j) / steps
			bv := bern(v)
			var q point
			for a := range 4 {
				for b := range 4 {
					w := bu[a] * bv[b]
					q.x += w * p[a][b].x
					q.y += w * p[a][b].y
				}
			}
			var c [3]float64
			for k := range 3 {
				c[k] = (1-u)*((1-v)*col[0][k]+v*col[1][k]) + u*((1-v)*col[3][k]+v*col[2][k])
			}
			grid[i][j] = meshVertex{q.x, q.y, c}
		}
	}
	for i := range steps {
		for j := range steps {
			tri(grid[i][j], grid[i+1][j], grid[i][j+1])
			tri(grid[i+1][j], grid[i+1][j+1], grid[i][j+1])
		}
	}
}

func fillTriangle(l *layer, a, b, c meshVertex) {
	x0 := max(int(math.Floor(min(a.x, b.x, c.x))), l.r.Min.X)
	x1 := min(int(math.Ceil(max(a.x, b.x, c.x))), l.r.Max.X-1)
	y0 := max(int(math.Floor(min(a.y, b.y, c.y))), l.r.Min.Y)
	y1 := min(int(math.Ceil(max(a.y, b.y, c.y))), l.r.Max.Y-1)
	area := (b.x-a.x)*(c.y-a.y) - (b.y-a.y)*(c.x-a.x)
	if area == 0 || x0 > x1 || y0 > y1 {
		return
	}
	// A little over the edge, so neighbouring triangles leave no gap.
	const edge = -0.02
	for y := y0; y <= y1; y++ {
		for x := x0; x <= x1; x++ {
			px, py := float64(x)+0.5, float64(y)+0.5
			w1 := ((px-a.x)*(c.y-a.y) - (py-a.y)*(c.x-a.x)) / area
			w2 := ((b.x-a.x)*(py-a.y) - (b.y-a.y)*(px-a.x)) / area
			w0 := 1 - w1 - w2
			if w0 < edge || w1 < edge || w2 < edge {
				continue
			}
			i := l.offset(x, y)
			for k := range 3 {
				l.pix[i+k] = uint8(clamp1(w0*a.c[k]+w1*b.c[k]+w2*c.c[k])*255 + 0.5)
			}
			l.pix[i+3] = 255
		}
	}
}

type tileKey struct {
	num int
	m   pdf.Matrix
	rgb [3]uint8
}

// tileSource repeats a pattern cell.
type tileSource struct {
	inv          pdf.Matrix
	l            *layer
	x0, y0       float64
	xstep, ystep float64
}

func (s *tileSource) at(x, y int) (uint8, uint8, uint8, uint8) {
	px, py := s.inv.Apply(float64(x)+0.5, float64(y)+0.5)
	u := math.Mod(px-s.x0, s.xstep)
	if u < 0 {
		u += s.xstep
	}
	v := math.Mod(py-s.y0, s.ystep)
	if v < 0 {
		v += s.ystep
	}
	w, h := s.l.r.Dx(), s.l.r.Dy()
	i := min(int(u/s.xstep*float64(w)), w-1)
	j := min(int(v/s.ystep*float64(h)), h-1)
	q := s.l.pix[(j*w+i)*4 : (j*w+i)*4+4 : (j*w+i)*4+4]
	a := q[3]
	if a == 0 {
		return 0, 0, 0, 0
	}
	if a == 255 {
		return q[0], q[1], q[2], 255
	}
	return uint8(min(int(q[0])*255/int(a), 255)), uint8(min(int(q[1])*255/int(a), 255)), uint8(min(int(q[2])*255/int(a), 255)), a
}

func (r *ctx) tilingSource(s *pdf.Stream, m pdf.Matrix, p *paint, box image.Rectangle) source {
	if r.vec != nil {
		r.vec.off++
		defer func() { r.vec.off-- }()
	}
	d := r.doc
	if r.depth >= maxFormDepth {
		return nil
	}
	dict := s.Dict
	bbox, ok := d.RectOf(dict["BBox"])
	xstep, ystep := math.Abs(d.Float(dict["XStep"], 0)), math.Abs(d.Float(dict["YStep"], 0))
	inv, invertible := m.Invert()
	if !ok || xstep == 0 || ystep == 0 || !invertible || math.IsNaN(xstep+ystep) {
		return nil
	}
	sx, sy := math.Hypot(m[0], m[1]), math.Hypot(m[2], m[3])
	w := min(max(int(math.Ceil(xstep*sx)), 1), 4096)
	h := min(max(int(math.Ceil(ystep*sy)), 1), 4096)
	key := tileKey{m: m}
	colored := d.Int(dict["PaintType"], 1) == 1
	if !colored {
		key.rgb = p.rgb
	}
	if ref, ok := p.pattern.(pdf.Ref); ok {
		key.num = ref.Num
		if t, ok := r.tiles[key]; ok {
			return &tileSource{inv: inv, l: t, x0: bbox.X0, y0: bbox.Y0, xstep: xstep, ystep: ystep}
		}
	}
	body, err := d.Decode(s)
	if err != nil && len(body) == 0 {
		return nil
	}
	l := newLayer(image.Rect(0, 0, w, h))
	ax, ay := float64(w)/xstep, float64(h)/ystep
	saveLayer, saveGS, saveStack, savePath, saveBase, saveUn := r.layer, r.gs, r.stack, r.path, r.base, r.uncolored
	saveHidden, saveTm, saveTlm, saveOut := r.hidden, r.tm, r.tlm, r.out
	r.layer, r.stack, r.hidden, r.out = l, nil, 0, nil
	r.depth++
	res := d.Dict(dict["Resources"])
	// A cell's content may reach past one step; draw it from each neighbour that could reach in.
	nx := min(int(math.Ceil(bbox.W()/xstep)), 4)
	ny := min(int(math.Ceil(bbox.H()/ystep)), 4)
	for i := 1 - nx; i <= 0; i++ {
		for j := 1 - ny; j <= 0; j++ {
			base := pdf.Matrix{ax, 0, 0, ay, (-bbox.X0 + float64(i)*xstep) * ax, (-bbox.Y0 + float64(j)*ystep) * ay}
			r.gs = newGState(base, l.r)
			r.base = base
			r.path = path{}
			r.uncolored = false
			if !colored {
				r.uncolored = true
				r.gs.fill.rgb, r.gs.stroke.rgb = p.rgb, p.rgb
			}
			r.interpret(body, res)
		}
	}
	r.depth--
	r.layer, r.gs, r.stack, r.path, r.base, r.uncolored = saveLayer, saveGS, saveStack, savePath, saveBase, saveUn
	r.hidden, r.tm, r.tlm, r.out = saveHidden, saveTm, saveTlm, saveOut
	if key.num != 0 {
		if r.tiles == nil {
			r.tiles = map[tileKey]*layer{}
		}
		r.tiles[key] = l
	}
	return &tileSource{inv: inv, l: l, x0: bbox.X0, y0: bbox.Y0, xstep: xstep, ystep: ystep}
}
