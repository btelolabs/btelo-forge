package mind

import (
	"math"
	"slices"
	"sort"
)

// Finding what is in a region. Free nodes, each with the box of its tree, and connectors are kept in a grid of
// cells in levels: an item goes in the level whose cells are at least as large as it, in the cell holding its
// top-left corner, so it is in one place and spans at most two cells each way. A query looks in each level only at
// the cells it covers, or through the level's items when they are fewer than its cells. From a tree's root the
// query descends the tree, at each node searching its stacked children by halves.

// gridBase is the size of the smallest level's cells.
const gridBase = 512.0

type cellKey struct {
	level int8
	x, y  int32
}

type spot struct {
	key cellKey
	box Rect
}

// spatial is a grid of items, named by number.
type spatial struct {
	cells  map[cellKey][]uint32
	where  map[uint32]spot
	levels [64]map[uint32]bool
}

func (g *spatial) init() {
	g.cells, g.where = map[cellKey][]uint32{}, map[uint32]spot{}
	g.levels = [64]map[uint32]bool{}
}

func cellOf(v, size float64) int32 {
	return int32(math.Max(math.Min(math.Floor(v/size), math.MaxInt32-1), math.MinInt32+1))
}

func levelOf(b Rect) int8 {
	s := math.Max(b.W(), b.H())
	l := int8(0)
	for size := gridBase; size < s && l < 62; size *= 2 {
		l++
	}
	return l
}

func (g *spatial) put(id uint32, b Rect) {
	if old, ok := g.where[id]; ok {
		if old.box == b {
			return
		}
		g.remove(id)
	}
	l := levelOf(b)
	size := gridBase * math.Pow(2, float64(l))
	k := cellKey{l, cellOf(b.X0, size), cellOf(b.Y0, size)}
	g.cells[k] = append(g.cells[k], id)
	g.where[id] = spot{k, b}
	if g.levels[l] == nil {
		g.levels[l] = map[uint32]bool{}
	}
	g.levels[l][id] = true
}

func (g *spatial) remove(id uint32) {
	s, ok := g.where[id]
	if !ok {
		return
	}
	list := g.cells[s.key]
	if i := slices.Index(list, id); i >= 0 {
		list[i] = list[len(list)-1]
		list = list[:len(list)-1]
	}
	if len(list) == 0 {
		delete(g.cells, s.key)
	} else {
		g.cells[s.key] = list
	}
	delete(g.where, id)
	delete(g.levels[s.key.level], id)
}

// query calls fn for every item whose box overlaps r, each once, in no order.
func (g *spatial) query(r Rect, fn func(id uint32, b Rect)) {
	for l := range g.levels {
		items := g.levels[l]
		if len(items) == 0 {
			continue
		}
		size := gridBase * math.Pow(2, float64(l))
		x0, x1 := int64(cellOf(r.X0-size, size)), int64(cellOf(r.X1, size))
		y0, y1 := int64(cellOf(r.Y0-size, size)), int64(cellOf(r.Y1, size))
		if (x1-x0+1)*(y1-y0+1) > int64(len(items)) {
			for id := range items {
				if b := g.where[id].box; b.Overlaps(r) {
					fn(id, b)
				}
			}
			continue
		}
		for y := y0; y <= y1; y++ {
			for x := x0; x <= x1; x++ {
				for _, id := range g.cells[cellKey{int8(l), int32(x), int32(y)}] {
					if b := g.where[id].box; b.Overlaps(r) {
						fn(id, b)
					}
				}
			}
		}
	}
}

func (g *spatial) each(fn func(id uint32, b Rect)) {
	for id, s := range g.where {
		fn(id, s.box)
	}
}

// rootGrid holds the free nodes by the boxes of their trees.
type rootGrid struct{ spatial }

func (g *rootGrid) put(id ID, b Rect) { g.spatial.put(uint32(id), b) }
func (g *rootGrid) remove(id ID)      { g.spatial.remove(uint32(id)) }
func (g *rootGrid) each(fn func(id ID, b Rect)) {
	g.spatial.each(func(id uint32, b Rect) { fn(ID(id), b) })
}

// RootsIn lists the free nodes whose trees overlap r, in id order: the order they are drawn in, later on top.
func (m *Map) RootsIn(r Rect) []*Node {
	m.Layout()
	var out []*Node
	m.grid.query(r, func(id uint32, b Rect) {
		if n := m.Node(ID(id)); n != nil {
			out = append(out, n)
		}
	})
	slices.SortFunc(out, func(a, b *Node) int { return int(a.ID) - int(b.ID) })
	return out
}

// alongAxis reports whether children growing in dir are stacked down the page (true) or across it.
func alongY(dir Layout) bool { return dir != LayoutDown && dir != LayoutUp }

// childRange is the part of a stack of children whose subtrees may overlap lo to hi along the stacking axis, in
// their parent's frame, found by halves: the children's subtrees follow one another along it.
func childRange(list []*Node, dir Layout, lo, hi float64) (int, int) {
	y := alongY(dir)
	end := func(c *Node) float64 {
		if y {
			return c.lay.rel.Y + c.lay.sub.Y1
		}
		return c.lay.rel.X + c.lay.sub.X1
	}
	start := func(c *Node) float64 {
		if y {
			return c.lay.rel.Y + c.lay.sub.Y0
		}
		return c.lay.rel.X + c.lay.sub.X0
	}
	i := sort.Search(len(list), func(k int) bool { return end(list[k]) >= lo })
	j := sort.Search(len(list), func(k int) bool { return start(list[k]) > hi })
	return i, max(i, j)
}

// stackDir is the way the children in n's side stack grow.
func stackDir(n *Node, side int) Layout {
	if side == 1 {
		return LayoutLeft
	}
	return n.lay.dir
}

// Visit calls fn, parents before children, for each node whose subtree's box overlaps r, with its box's top-left
// on the board, and descends into its children when fn returns true.
func (m *Map) Visit(r Rect, fn func(n *Node, at Vec) bool) {
	for _, root := range m.RootsIn(r) {
		m.visit(root, Vec{root.X, root.Y}, r, fn)
	}
}

func (m *Map) visit(n *Node, at Vec, r Rect, fn func(*Node, Vec) bool) {
	if !n.lay.sub.Add(at).Overlaps(r) || !fn(n, at) {
		return
	}
	for side, list := range n.lay.stacks {
		if len(list) == 0 {
			continue
		}
		dir := stackDir(n, side)
		lo, hi := r.Y0-at.Y, r.Y1-at.Y
		if !alongY(dir) {
			lo, hi = r.X0-at.X, r.X1-at.X
		}
		i, j := childRange(list, dir, lo, hi)
		for _, c := range list[i:j] {
			m.visit(c, at.Add(c.lay.rel), r, fn)
		}
	}
}

// At is the topmost node whose box holds the board point x, y, or nil; fold reports that the point is on its folded
// children's count.
func (m *Map) At(x, y float64) (n *Node, fold bool) {
	pt := Rect{x, y, x, y}
	roots := m.RootsIn(pt)
	for i := len(roots) - 1; i >= 0; i-- {
		var hit *Node
		m.visit(roots[i], Vec{roots[i].X, roots[i].Y}, pt, func(c *Node, at Vec) bool {
			if R(0, 0, c.lay.w, c.lay.h).Add(at).Contains(x, y) {
				hit = c
			} else if c.Folded && len(c.Children) > 0 && foldMark(c).Grow(3).Add(at).Contains(x, y) {
				hit, fold = c, true
			}
			return hit == nil
		})
		if hit != nil {
			return hit, fold
		}
	}
	return nil, false
}

// In lists the nodes whose boxes lie wholly inside r (touching it when touch is true), in the order they are drawn.
func (m *Map) In(r Rect, touch bool) []*Node {
	var out []*Node
	m.Visit(r, func(n *Node, at Vec) bool {
		b := R(0, 0, n.lay.w, n.lay.h).Add(at)
		if touch && b.Overlaps(r) || !touch && r.Inside(b) {
			out = append(out, n)
		}
		return true
	})
	return out
}

// Visible lists the nodes shown: those not under a folded node, in id order.
func (m *Map) Visible() []*Node {
	var out []*Node
	m.Each(func(n *Node) bool {
		if !m.Hidden(n) {
			out = append(out, n)
		}
		return true
	})
	return out
}

// spotMargin is the room FreeSpot keeps around what is placed.
const spotMargin = 24

// FreeSpot is the top-left of a w × h box at or near the board point at (its wished top-left) that overlaps
// nothing on the board with a margin: at itself when it is free, else the nearest free place going round it
// outwards. It is for what is put on the board without a place of its own: dropped files, pastes. The trees of
// the free nodes ignore are not in the way: they are what is being placed.
func (m *Map) FreeSpot(at Vec, w, h float64, ignore ...ID) Vec {
	m.Layout()
	free := func(p Vec) bool {
		r := R(p.X, p.Y, w, h).Grow(spotMargin)
		for _, n := range m.RootsIn(r) {
			if slices.Contains(ignore, n.ID) {
				continue
			}
			hit := false
			m.visit(n, Vec{n.X, n.Y}, r, func(c *Node, cat Vec) bool {
				if R(0, 0, c.lay.w, c.lay.h).Add(cat).Overlaps(r) {
					hit = true
				}
				return !hit
			})
			if hit {
				return false
			}
		}
		return true
	}
	if free(at) {
		return at
	}
	step := math.Max(40, math.Min(w, h)/2)
	for ring := 1; ring <= 60; ring++ {
		best, bestD := Vec{}, math.Inf(1)
		for i := -ring; i <= ring; i++ {
			for _, d := range [][2]int{{i, -ring}, {i, ring}, {-ring, i}, {ring, i}} {
				p := Vec{at.X + float64(d[0])*step, at.Y + float64(d[1])*step}
				if dist := math.Hypot(p.X-at.X, p.Y-at.Y); dist < bestD && free(p) {
					best, bestD = p, dist
				}
			}
		}
		if bestD < math.Inf(1) {
			return Vec{math.Round(best.X), math.Round(best.Y)}
		}
	}
	return at
}
