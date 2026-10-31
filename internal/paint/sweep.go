package paint

import (
	"image"
	"math"
)

// The vector brush draws with a brush's tip as a vector layer's shapes: the area the brush paints along the line the
// pointer draws becomes closed outlines, fitted with curves. Each stroke is a shape added to the layer's others, so a
// drawing made of many strokes is one layer.
//
// A drawn tip is convex, so that area is the sum of the line and the tip's outline, and is reckoned exactly (sweep). A
// picture's tip may be any shape: its dabs are laid as the brush lays them, and the area is what they cover half or
// more (sweepCover).

// sweepFit is how far a stroke's curves may stray from its outline.
const sweepFit = 0.25

// BrushSweep is the area a brush paints along a line drawn so far.
type BrushSweep struct {
	b     Brush
	line  []Pt
	tip   []Pt        // a drawn tip's outline
	cover *sweepCover // a picture tip's dabs
}

// NewBrushSweep starts a sweep of the brush at p.
func NewBrushSweep(b Brush, p Pt) *BrushSweep {
	s := &BrushSweep{b: b, line: []Pt{p}}
	if n := b.normalized(); n.Tip == TipShaped {
		s.b, s.cover = n, newSweepCover(n)
		s.cover.dab(p)
	} else {
		s.tip = tipOutline(b)
	}
	return s
}

// Last is where the line has reached.
func (s *BrushSweep) Last() Pt { return s.line[len(s.line)-1] }

// To continues the line to p.
func (s *BrushSweep) To(p Pt) {
	last := s.Last()
	if p == last {
		return
	}
	s.line = append(s.line, p)
	if s.cover != nil {
		s.cover.lineTo(last, p)
	}
}

// End takes the line to p and ends it there: a picture's dabs, laid by spacing, reach it.
func (s *BrushSweep) End(p Pt) {
	s.To(p)
	if s.cover != nil {
		s.cover.dab(p)
	}
}

// Outline is the area so far as closed outlines wound so that filling them by their winding fills the area: polygons
// that a drawn tip's may cross themselves or, fitted, curves that do not.
func (s *BrushSweep) Outline(fitted bool) []Subpath {
	if s.cover != nil {
		if rings := s.cover.rings(); len(rings) > 0 {
			out := make([]Subpath, len(rings))
			for i, r := range rings {
				if fitted {
					out[i] = FitPath(r, true, sweepFit)
				} else {
					out[i] = polyline(true, r...)
				}
			}
			return out
		}
	}
	tip := s.tip
	if tip == nil {
		tip = tipOutline(Brush{Size: s.b.Size, Round: 1, Tip: TipRound}) // too faint to cover half: a dot all the same
	}
	ring := polyline(true, sweep(s.line, tip)...)
	if fitted {
		v := Vector{Kind: VectorCurve, M: Identity, Path: []Subpath{ring}, Rule: NonZero}
		if out := combineFitted(BoolAdd, []Vector{v}, sweepFit); len(out) > 0 {
			return out
		}
	}
	return []Subpath{ring}
}

// tipOutline is the outline of a drawn tip's dab in canvas pixels about its centre, turned and squashed as the brush
// is: exactly where a hard dab covers half, half a pixel inside its size, which is the edge of what the brush paints
// and of its outline at the pointer. It is a convex polygon with its points in the order of a positive turn, within a
// fiftieth of a pixel. Chalk has the round tip's shape, its grain being texture. A brush too small to have that edge
// is half its size across, and one smaller than a pixel keeps its size, which only a shape can show.
func tipOutline(b Brush) []Pt {
	size := b.Size
	b = b.normalized()
	if size <= 0 {
		size = b.Size
	}
	r := max(size-1, size/2) / 2
	angle, round := b.turn()
	sn, cs := math.Sincos(angle * math.Pi / 180)
	turned := func(x, y float64) Pt {
		y *= round
		return Pt{x*cs - y*sn, x*sn + y*cs}
	}
	if b.Tip == TipSquare {
		return []Pt{turned(-r, -r), turned(r, -r), turned(r, r), turned(-r, r)}
	}
	n := min(max(int(math.Ceil(math.Pi/math.Acos(max(1-0.02/r, 0)))), 32), 1024)
	out := make([]Pt, n)
	for i := range n {
		y, x := math.Sincos(2 * math.Pi * float64(i) / float64(n))
		out[i] = turned(r*x, r*y)
	}
	return out
}

// supportIndex is the point of a convex outline farthest along direction d.
func supportIndex(outline []Pt, d Pt) int {
	best, bestV := 0, math.Inf(-1)
	for i, p := range outline {
		if v := p.Dot(d); v > bestV {
			best, bestV = i, v
		}
	}
	return best
}

// sweep is the area a convex tip outline, its points in the order of a positive turn, passes over as its centre goes
// along line. It is one closed ring, filled by its winding: along each segment the tip's point farthest to that side,
// and round the tip at each bend and end from one such point to the next. On the inside of a bend that way round goes
// backwards, and the ring crosses itself; the winding is one or more everywhere in the area and none outside it, as
// the ring is what the convex hulls of the segments' tips sum to less the tips at the bends they share.
func sweep(line []Pt, tip []Pt) []Pt {
	line = dedupe(line, false)
	if len(line) == 0 || len(tip) < 3 {
		return nil
	}
	if len(line) == 1 {
		out := make([]Pt, len(tip))
		for i, t := range tip {
			out[i] = line[0].Add(t)
		}
		return out
	}
	n := len(line) - 1
	dirs := make([]Pt, n)
	lefts, rights := make([]int, n), make([]int, n)
	for i := range n {
		d := line[i+1].Sub(line[i]).Unit()
		dirs[i] = d
		lefts[i] = supportIndex(tip, Pt{d.Y, -d.X}) // with y down, the left of going along d
		rights[i] = supportIndex(tip, Pt{-d.Y, d.X})
	}
	var ring []Pt
	// round adds the tip's points at p after from, up to to, going forward through the outline or backward.
	round := func(p Pt, from, to int, forward bool) {
		step := len(tip) - 1
		if forward {
			step = 1
		}
		for i := from; i != to; {
			i = (i + step) % len(tip)
			ring = append(ring, p.Add(tip[i]))
		}
	}
	ring = append(ring, line[0].Add(tip[lefts[0]]))
	for i := range n {
		ring = append(ring, line[i+1].Add(tip[lefts[i]]))
		if i+1 < n {
			round(line[i+1], lefts[i], lefts[i+1], dirs[i].Cross(dirs[i+1]) >= 0)
		}
	}
	round(line[n], lefts[n-1], rights[n-1], true)
	for i := n - 1; i >= 0; i-- {
		ring = append(ring, line[i].Add(tip[rights[i]]))
		if i > 0 {
			round(line[i], rights[i], rights[i-1], dirs[i-1].Cross(dirs[i]) < 0)
		}
	}
	round(line[0], rights[0], lefts[0], true)
	return ring
}

// sweepScale is how many samples of a picture tip's coverage there are to a canvas pixel, each way.
const sweepScale = 2

// sweepCover is how much a picture tip's dabs along a line cover, as a stroke keeps it, sampled in tiles so its size
// goes with the stroke's area.
type sweepCover struct {
	shape   dabShape
	reach   float64
	step    float64
	carry   float64
	tiles   map[image.Point][]uint16
	lastKey image.Point
	last    []uint16
}

func newSweepCover(b Brush) *sweepCover {
	return &sweepCover{shape: b.shape(), reach: b.reach(), step: max(b.Size*b.Spacing, 0.5), tiles: map[image.Point][]uint16{}}
}

// lineTo lays dabs by the spacing along the way from a to b, starting where the last way left off.
func (c *sweepCover) lineTo(a, b Pt) {
	dist := a.Dist(b)
	t := c.step - c.carry
	for ; t <= dist; t += c.step {
		c.dab(a.Lerp(b, t/dist))
	}
	c.carry = dist - (t - c.step)
}

// dab lays one dab centred at p, adding to what is there as a stroke's dabs do.
func (c *sweepCover) dab(p Pt) {
	x0, x1 := int(math.Floor((p.X-c.reach)*sweepScale)), int(math.Ceil((p.X+c.reach)*sweepScale))
	y0, y1 := int(math.Floor((p.Y-c.reach)*sweepScale)), int(math.Ceil((p.Y+c.reach)*sweepScale))
	for y := y0; y < y1; y++ {
		py := (float64(y) + 0.5) / sweepScale
		for x := x0; x < x1; x++ {
			v := c.shape.at((float64(x)+0.5)/sweepScale, py, p.X, p.Y)
			if v <= 0 {
				continue
			}
			k := image.Pt(x>>tileShift, y>>tileShift)
			t := c.tiles[k]
			if t == nil {
				t = make([]uint16, TileSize*TileSize)
				c.tiles[k] = t
				c.last = nil // it may have been looked up as missing
			}
			cell := &t[(y&tileMask)*TileSize+x&tileMask]
			m := float64(*cell) / 65535
			*cell = uint16((m+(1-m)*v)*65535 + 0.5)
		}
	}
}

// at is the coverage of sample (x, y).
func (c *sweepCover) at(x, y int) uint16 {
	k := image.Pt(x>>tileShift, y>>tileShift)
	if k != c.lastKey || c.last == nil {
		c.lastKey, c.last = k, c.tiles[k]
		if c.last == nil {
			return 0
		}
	}
	return c.last[(y&tileMask)*TileSize+x&tileMask]
}

// sweepHalf is the coverage that counts as covered, which is how the brush's outline at the pointer draws it too.
const sweepHalf = 32768

// rings are the outlines of what the dabs cover, in canvas pixels, with the covered side on their left: marching
// squares, each cell of four samples adding the edges that cross it, placed between its samples by their coverage.
func (c *sweepCover) rings() [][]Pt {
	// A cell's samples start at its own, so the row and column before a tile have cells that reach into it.
	cells := map[image.Point]bool{}
	for k := range c.tiles {
		for dy := -1; dy <= 0; dy++ {
			for dx := -1; dx <= 0; dx++ {
				cells[k.Add(image.Pt(dx, dy))] = true
			}
		}
	}
	var edges []boolEdge
	for k := range cells {
		x0, y0 := k.X<<tileShift, k.Y<<tileShift
		t := c.tiles[k]
		for y := range TileSize {
			for x := range TileSize {
				// Within a tile most cells are all covered or all clear, read without looking the tile up.
				if x < tileMask && y < tileMask {
					if t == nil {
						continue
					}
					i := y*TileSize + x
					in := t[i] >= sweepHalf
					if t[i+1] >= sweepHalf == in && t[i+TileSize] >= sweepHalf == in && t[i+TileSize+1] >= sweepHalf == in {
						continue
					}
				}
				edges = c.cell(edges, x0+x, y0+y)
			}
		}
	}
	return joinRings(edges)
}

// cell adds the edges that cross the cell at sample (x, y).
func (c *sweepCover) cell(edges []boolEdge, x, y int) []boolEdge {
	v := [4]uint16{c.at(x, y), c.at(x+1, y), c.at(x+1, y+1), c.at(x, y+1)}
	in := [4]bool{v[0] >= sweepHalf, v[1] >= sweepHalf, v[2] >= sweepHalf, v[3] >= sweepHalf}
	if in[0] == in[1] && in[1] == in[2] && in[2] == in[3] {
		return edges
	}
	// The corners' places, and where the cell's four sides are crossed, always read the same way along a side so
	// the cell beside it finds the same point.
	corner := func(i int) Pt {
		d := [4]Pt{{0, 0}, {1, 0}, {1, 1}, {0, 1}}[i]
		return Pt{(float64(x) + d.X + 0.5) / sweepScale, (float64(y) + d.Y + 0.5) / sweepScale}
	}
	cross := func(i, j int) Pt {
		return corner(i).Lerp(corner(j), (sweepHalf-float64(v[i]))/(float64(v[j])-float64(v[i])))
	}
	top, right, bottom, left := Pt{}, Pt{}, Pt{}, Pt{}
	if in[0] != in[1] {
		top = cross(0, 1)
	}
	if in[1] != in[2] {
		right = cross(1, 2)
	}
	if in[3] != in[2] {
		bottom = cross(3, 2)
	}
	if in[0] != in[3] {
		left = cross(0, 3)
	}
	// add joins two sides with an edge that has the corner near on its left going from p to q, turned when that
	// corner is the uncovered one.
	add := func(p, q Pt, near int) {
		if !in[near] {
			p, q = q, p
		}
		if p != q {
			edges = append(edges, boolEdge{a: p, b: q})
		}
	}
	sides := func(a, b int) bool { return in[a] != in[b] }
	switch {
	case in[0] == in[2] && in[1] == in[3]:
		// A saddle: the centre says which corners are joined.
		if (int(v[0])+int(v[1])+int(v[2])+int(v[3]))/4 >= sweepHalf == in[0] {
			add(right, top, 1)
			add(left, bottom, 3)
		} else {
			add(top, left, 0)
			add(bottom, right, 2)
		}
	case sides(0, 1) && sides(1, 2):
		add(right, top, 1)
	case sides(1, 2) && sides(2, 3):
		add(bottom, right, 2)
	case sides(2, 3) && sides(3, 0):
		add(left, bottom, 3)
	case sides(3, 0) && sides(0, 1):
		add(top, left, 0)
	case sides(0, 1):
		add(top, bottom, 0)
	default:
		add(right, left, 0)
	}
	return edges
}
