package mind

import (
	"math"
	"path/filepath"
	"strings"
	"time"
)

// The layout. Each node's box is measured from its content and look; a tree node's place is an offset from its
// parent's box, and every node keeps the box around its whole subtree, relative to its own. Children are stacked
// along the direction the tree grows, so their subtrees follow one another along that axis: what lies in any region
// is found by descending from the roots and, at each node, searching its children by halves (see Visit), and a
// change moves only offsets — measuring again the nodes that changed and restacking each ancestor's children — never
// every node below. The areas whose drawing changed are gathered for the view to draw again (Changed).

// Spacing between a parent and its children, and between siblings, in board units.
const (
	gapRoot   = 72 // from a root to its children, across
	gapBranch = 44 // deeper
	gapMain   = 26 // between a root's children
	gapLeaf   = 10 // between deeper siblings
	gapDown   = 56 // from a parent to its children in an org chart
	gapSide   = 22 // between siblings side by side
	indent    = 34 // an outline's step in
	gapList   = 8  // between an outline's rows
	foldR     = 9  // the radius of a folded node's count
)

// Default sizes of what shows a picture.
const (
	mediaW     = 240
	nestedW    = 300
	captionPad = 6
	cellPadX   = 8
	cellPadY   = 5
	minCellW   = 56
	maxCellW   = 260
)

// place is what the layout worked out for a node.
type place struct {
	measured bool
	dirty    bool // its content or children changed
	path     bool // a descendant's did

	w, h    float64
	lk      look
	lines   []string // the words, wrapped
	lineH   float64
	text    Rect // where the words are, in the box
	media   Rect // where a picture or table is, in the box
	caption Rect // where a caption is, under it
	cols    []float64
	rows    []float64
	cells   []string   // a table's cells as shown: formulas worked out
	cellLn  [][]string // and wrapped

	task   Rect // where a task's box or ring is, in the box
	tasks  int  // the tasks at the ends of its subtree (itself, when it has none under it) and how many are done
	done   int
	under  bool   // tasks are counted under it: it shows a ring of their progress rather than a box
	rel    Vec    // the box's top-left from its parent's
	sub    Rect   // the subtree's box from its own top-left
	dir    Layout // which way its children grow
	depth  int    // below its root
	branch int    // the main branch it is on
	stacks [2][]*Node
}

// layoutState is what the layout has yet to do.
type layoutState struct {
	roots      map[ID]bool // trees to lay out again
	gone       map[ID]bool // nodes no longer free, or deleted: out of the grid
	all        bool        // everything changed
	edgesStale bool
	regions    []Rect // areas whose drawing changed, on the board
	everything bool   // too many to list: all of it
	wrap       wrapCache
	budget     time.Duration // how long a frame may spend routing connectors round the nodes; 0 is no bound
	spent      time.Duration // spent so far in this frame, by every layout in it
	routing    bool          // connectors are still being routed
	// The connectors to look at again besides those running through the areas that changed: those changed
	// themselves, those not shown (an end hidden), whose ends may show again, and those still being routed.
	edgeDirty   map[EdgeID]bool
	edgeOff     map[EdgeID]bool
	edgePending map[EdgeID]bool
}

func (l *layoutState) init() {
	l.roots, l.gone = map[ID]bool{}, map[ID]bool{}
	l.edgeDirty, l.edgeOff, l.edgePending = map[EdgeID]bool{}, map[EdgeID]bool{}, map[EdgeID]bool{}
	l.regions, l.everything = nil, true
}

// dirtyNode notes that the node with id changed (or was deleted); its ancestors' stacking follows at the next layout.
func (l *layoutState) dirtyNode(id ID) { l.roots[id] = true }

// regionLimit is how many changed areas are listed before the whole board is said to have changed.
const regionLimit = 512

func (l *layoutState) region(r Rect) {
	if l.everything || r.Empty() || math.IsInf(r.X0, 0) {
		return
	}
	if len(l.regions) >= regionLimit {
		l.everything, l.regions = true, nil
		return
	}
	l.regions = append(l.regions, r)
}

// touch notes that n changed and must be measured again, and marks the way up to its root.
func (m *Map) touchLayout(n *Node) {
	n.lay.dirty = true
	for p := n; ; {
		if p.Parent == 0 {
			m.lay.roots[p.ID] = true
			break
		}
		p = m.nodes[p.Parent]
		if p.lay.path {
			break
		}
		p.lay.path = true
	}
	m.lay.edgesStale = true
	m.version++
}

// forgetArea notes that what n's subtree showed is gone from where it was.
func (m *Map) forgetArea(n *Node) {
	if n.lay.measured {
		m.lay.region(m.SubBox(n))
	}
}

// Changed returns the areas of the board whose drawing changed since it was last called, or all = true when the
// whole board may have; it lays the map out first.
func (m *Map) Changed() (regions []Rect, all bool) {
	m.lay.spent = 0 // a frame begins: its routing budget is whole again
	m.Layout()
	regions, all = m.lay.regions, m.lay.everything
	m.lay.regions, m.lay.everything = nil, false
	return regions, all
}

// Layout works out the places of everything changed since it last ran.
func (m *Map) Layout() {
	l := &m.lay
	if len(l.roots) == 0 && len(l.gone) == 0 && !l.all && !l.edgesStale {
		return
	}
	start := len(l.regions)
	for id := range l.gone {
		m.grid.remove(id)
	}
	clear(l.gone)
	todo := l.roots
	if l.all {
		todo = map[ID]bool{}
		m.Each(func(n *Node) bool {
			if n.Parent == 0 {
				todo[n.ID] = true
			}
			return true
		})
		l.all = false
	}
	for id := range todo {
		n := m.Node(id)
		if n == nil {
			m.grid.remove(id)
			continue
		}
		if n.Parent != 0 {
			m.grid.remove(id) // it was free and is in a tree now
			n = m.RootOf(n)
			if n.lay.measured && !n.lay.dirty && !n.lay.path {
				continue // its tree was laid out already
			}
		}
		old := emptyRect
		had := n.lay.measured
		if had {
			old = n.lay.sub.Add(Vec{n.X, n.Y})
		}
		dir := n.Layout
		if dir == LayoutMap {
			dir = LayoutRight
		}
		m.layNode(n, 0, 0, dir, Vec{n.X, n.Y})
		now := n.lay.sub.Add(Vec{n.X, n.Y})
		m.grid.put(n.ID, now)
		if !had || old != now {
			l.region(old.Union(now))
		}
	}
	l.roots = map[ID]bool{}
	if l.everything {
		m.layEdges(nil, true)
	} else {
		m.layEdges(l.regions[start:], false)
	}
}

// layNode lays out n's subtree, n being at depth on main branch branch, its children growing in direction dir, its
// box's top-left at at on the board (for the areas that change); it does nothing to a subtree that did not change.
func (m *Map) layNode(n *Node, depth, branch int, dir Layout, at Vec) {
	p := &n.lay
	moved := !p.measured || p.depth != depth || p.branch != branch || p.dir != dir
	if !moved && !p.dirty && !p.path {
		return
	}
	p.depth, p.branch, p.dir = depth, branch, dir
	if moved || p.dirty {
		old := R(0, 0, p.w, p.h)
		m.measureNode(n)
		if p.measured {
			m.lay.region(old.Add(at))
		}
		m.lay.region(R(0, 0, p.w, p.h).Add(at))
	}
	m.stack(n, at)
	p.measured, p.dirty, p.path = true, false, false
}

// childDir is the direction a child's children grow, the child being on the given side of a map's root.
func childDir(parent Layout, left bool) Layout {
	if parent == LayoutMap || parent == LayoutRight && left {
		if left {
			return LayoutLeft
		}
		return LayoutRight
	}
	return parent
}

// stack lays out n's children and places them along the way n's tree grows, noting the areas that changed.
func (m *Map) stack(n *Node, at Vec) {
	p := &n.lay
	p.stacks[0], p.stacks[1] = p.stacks[0][:0], p.stacks[1][:0]
	own := R(0, 0, p.w, p.h)
	defer m.countTasks(n, at)
	if n.Folded || len(n.Children) == 0 {
		p.sub = own
		if n.Folded && len(n.Children) > 0 {
			p.sub = p.sub.Union(foldMark(n).Grow(1))
		}
		return
	}
	mapRoot := p.depth == 0 && n.Layout == LayoutMap
	// A map's root shares its children between its sides: those that chose one keep it, the others balance them.
	if mapRoot {
		want := (len(n.Children) + 1) / 2
		right := 0
		for _, id := range n.Children {
			if m.nodes[id].Side == SideRight {
				right++
			}
		}
		for _, id := range n.Children {
			c := m.nodes[id]
			left := c.Side == SideLeft || c.Side == SideAuto && right >= want
			if c.Side == SideAuto && !left {
				right++
			}
			if left {
				p.stacks[1] = append(p.stacks[1], c)
			} else {
				p.stacks[0] = append(p.stacks[0], c)
			}
		}
	} else {
		for _, id := range n.Children {
			p.stacks[0] = append(p.stacks[0], m.nodes[id])
		}
	}
	sub := own
	for side, list := range p.stacks {
		if len(list) == 0 {
			continue
		}
		dir := p.dir
		if mapRoot && side == 1 {
			dir = LayoutLeft
		}
		changed, olds := make([]bool, len(list)), make([]Rect, len(list))
		for i, c := range list {
			br := p.branch
			if p.depth == 0 {
				br = i
				if side == 1 {
					br = len(p.stacks[0]) + i
				}
			}
			cd := childDir(dir, false)
			changed[i] = !c.lay.measured || c.lay.depth != p.depth+1 || c.lay.branch != br || c.lay.dir != cd
			olds[i] = c.lay.sub
			m.layNode(c, p.depth+1, br, cd, at.Add(c.lay.rel))
			changed[i] = changed[i] || olds[i] != c.lay.sub
		}
		sub = sub.Union(m.place(n, list, changed, olds, dir, at))
	}
	p.sub = sub
}

// place stacks the laid out children list of n in direction dir, setting their offsets, and returns the box around
// them; it notes the area of each child that moved or changed, olds being their subtrees' boxes before.
func (m *Map) place(n *Node, list []*Node, changed []bool, olds []Rect, dir Layout, at Vec) Rect {
	p := &n.lay
	across, along := float64(gapBranch), float64(gapLeaf)
	if p.depth == 0 {
		across, along = gapRoot, gapMain
	}
	switch dir {
	case LayoutDown, LayoutUp:
		along = gapSide
	case LayoutList:
		along = gapList
	}
	box := emptyRect
	cursor := 0.0
	if dir == LayoutList {
		cursor = p.h + gapList
	}
	rels := make([]Vec, len(list))
	for i, c := range list {
		s := c.lay.sub
		var rel Vec
		switch dir {
		case LayoutRight:
			rel = Vec{p.w + across - s.X0, cursor - s.Y0}
			cursor += s.H() + along
		case LayoutLeft:
			rel = Vec{-across - s.X1, cursor - s.Y0}
			cursor += s.H() + along
		case LayoutDown:
			rel = Vec{cursor - s.X0, p.h + gapDown - s.Y0}
			cursor += s.W() + along
		case LayoutUp:
			rel = Vec{cursor - s.X0, -gapDown - s.Y1}
			cursor += s.W() + along
		case LayoutList:
			rel = Vec{indent - s.X0, cursor - s.Y0}
			cursor += s.H() + along
		}
		rels[i] = rel
	}
	// The children are stacked about the point their branches leave n from, as MindNode places them: the middle of
	// the first and the last child's anchors lies across from it, so a lone child's line runs straight on from
	// n's.
	if len(list) > 0 && dir != LayoutList {
		out := outAnchor(n, dir)
		first := rels[0].Add(inAnchor(list[0], dir))
		last := rels[len(list)-1].Add(inAnchor(list[len(list)-1], dir))
		for i := range rels {
			if dir == LayoutDown || dir == LayoutUp {
				rels[i].X += out.X - (first.X+last.X)/2
			} else {
				rels[i].Y += out.Y - (first.Y+last.Y)/2
			}
		}
	}
	for i, c := range list {
		s := c.lay.sub
		rel := Vec{math.Round(rels[i].X*4) / 4, math.Round(rels[i].Y*4) / 4}
		if c.lay.rel != rel || changed[i] {
			// Where it was and where it is now, with the branch to it from n's box.
			own := R(0, 0, p.w, p.h).Add(at)
			if olds[i] != (Rect{}) {
				m.lay.region(olds[i].Add(at.Add(c.lay.rel)).Union(own))
			}
			c.lay.rel = rel
			m.lay.region(s.Add(at.Add(rel)).Union(own))
		}
		box = box.Union(s.Add(rel))
	}
	return box
}

// foldMark is where a folded node shows how many children it hides, in its box's frame.
func foldMark(n *Node) Rect {
	p := &n.lay
	a := outAnchor(n, p.dir)
	switch p.dir {
	case LayoutLeft:
		return R(a.X-2*foldR-2, a.Y-foldR, 2*foldR, 2*foldR)
	case LayoutDown, LayoutList:
		return R(a.X-foldR, a.Y+2, 2*foldR, 2*foldR)
	case LayoutUp:
		return R(a.X-foldR, a.Y-2*foldR-2, 2*foldR, 2*foldR)
	}
	return R(a.X+2, a.Y-foldR, 2*foldR, 2*foldR)
}

// underY is the middle of an underlined topic's line, in its box's frame.
func underY(n *Node) float64 { return n.lay.h - n.lay.lk.lineW/2 }

// outAnchor is where branches leave n towards its children growing in direction dir, in its box's frame: the
// end of its line for an underlined topic, else the middle of its side.
func outAnchor(n *Node, dir Layout) Vec {
	p := &n.lay
	under := p.lk.shape == ShapeUnderline
	switch dir {
	case LayoutLeft:
		if under {
			return Vec{0, underY(n)}
		}
		return Vec{0, p.h / 2}
	case LayoutDown:
		return Vec{p.w / 2, p.h}
	case LayoutUp:
		return Vec{p.w / 2, 0}
	case LayoutList:
		return Vec{math.Min(indent/2, p.w/2), p.h}
	}
	if under {
		return Vec{p.w, underY(n)}
	}
	return Vec{p.w, p.h / 2}
}

// inAnchor is where the branch from its parent meets n, growing in direction dir, in its box's frame.
func inAnchor(n *Node, dir Layout) Vec {
	p := &n.lay
	under := p.lk.shape == ShapeUnderline
	switch dir {
	case LayoutLeft:
		if under {
			return Vec{p.w, underY(n)}
		}
		return Vec{p.w, p.h / 2}
	case LayoutDown:
		return Vec{p.w / 2, 0}
	case LayoutUp:
		return Vec{p.w / 2, p.h}
	}
	if under {
		return Vec{0, underY(n)}
	}
	return Vec{0, p.h / 2}
}

// measureNode works out n's look and the size of its box.
func (m *Map) measureNode(n *Node) {
	p := &n.lay
	lk := lookOf(n, p.depth, p.branch, p.dir, m.Theme())
	p.lk = lk
	p.lineH = m.measure.LineHeight(lk.size)
	p.text, p.media, p.caption = Rect{}, Rect{}, Rect{}
	switch n.Kind {
	case Topic:
		m.measureTopic(n)
	case Table:
		m.measureTable(n)
	default:
		m.measureMedia(n)
	}
	p.w, p.h = math.Ceil(p.w), math.Ceil(p.h)
}

func (m *Map) wrap(text string, size float32, bold bool, width float64) []string {
	return m.lay.wrap.wrap(m.measure, wrapKey{text, size, bold, width})
}

// widest is the width of the widest line.
func (m *Map) widest(lines []string, size float32, bold bool) float64 {
	w := 0.0
	for _, l := range lines {
		w = math.Max(w, m.measure.Width(l, size, bold))
	}
	return w
}

// shapeRoom is how much bigger than its words a shape's box is, across and down, so the words sit inside it.
func shapeRoom(s Shape, tw, th float64) (w, h float64) {
	switch s {
	case ShapeEllipse:
		return tw * 1.3, th * 1.4
	case ShapeDiamond:
		return tw*1.6 + th, th * 2
	case ShapeHexagon, ShapeParallelogram:
		return tw + th*0.9, th
	case ShapeCapsule:
		return tw + th*0.6, th
	}
	return tw, th
}

func (m *Map) measureTopic(n *Node) {
	p := &n.lay
	lk := p.lk
	limit := lk.maxTextW
	if n.W > 0 {
		cw, _ := shapeRoom(lk.shape, 100, 10)
		limit = math.Max(16, (n.W-2*lk.padX)*100/cw)
	}
	// A task's box or ring stands before its words.
	icon, gap := 0.0, 0.0
	if n.Task {
		icon, gap = math.Round(float64(lk.size)*1.05), math.Round(float64(lk.size)*0.45)
		if n.W > 0 {
			limit = math.Max(16, limit-icon-gap)
		}
	}
	p.lines = m.wrap(n.Text, lk.size, lk.bold, limit)
	tw := m.widest(p.lines, lk.size, lk.bold)
	th := float64(max(len(p.lines), 1)) * p.lineH
	if n.W > 0 {
		tw = limit
	} else {
		tw = math.Max(tw, float64(lk.size)*2)
	}
	th = math.Max(th, icon)
	tw += icon + gap
	sw, sh := shapeRoom(lk.shape, tw+2*lk.padX, th+2*lk.padY)
	p.w, p.h = sw, sh
	if n.W > 0 {
		p.w = n.W
	}
	if n.H > 0 {
		p.h = math.Max(n.H, th+2)
	}
	left := (p.w - tw) / 2
	if lk.shape == ShapeUnderline || lk.shape == ShapeNone {
		left = lk.padX
		if n.W > 0 {
			left = (p.w - tw) / 2
		}
	}
	p.text = R(left, (p.h-th)/2, tw, th)
	if lk.shape == ShapeUnderline {
		p.text = R(left, lk.padY, tw, th)
		p.h = th + 2*lk.padY + lk.lineW
	}
	p.task = Rect{}
	if n.Task {
		// The box sits by the first line; the words move along to make room for it.
		first := math.Min(p.lineH, p.text.H())
		p.task = R(p.text.X0, p.text.Y0+(first-icon)/2, icon, icon)
		p.text.X0 += icon + gap
	}
}

// Caption is what is written under a node that shows something: its text, or a file's name for a video or a
// nested map that has none.
func (n *Node) Caption() string {
	if n.Text != "" || n.Ref == "" {
		return n.Text
	}
	switch n.Kind {
	case Video, Nested:
		return strings.TrimSuffix(filepath.Base(n.Ref), filepath.Ext(n.Ref))
	}
	return ""
}

// DefaultAspect is the proportions a node showing something has before what it shows is known.
func DefaultAspect(k Kind) float64 {
	if k == Video {
		return 16.0 / 9
	}
	return 4.0 / 3
}

func (m *Map) measureMedia(n *Node) {
	p := &n.lay
	w := float64(mediaW)
	if n.Kind == Nested {
		w = nestedW
	}
	if n.W > 0 {
		w = n.W
	}
	aspect := n.Aspect
	if aspect <= 0 {
		aspect = DefaultAspect(n.Kind)
	}
	h := w / aspect
	if n.H > 0 {
		h = n.H
	}
	p.w, p.media = w, R(0, 0, w, h)
	m.measureCaption(n, h)
}

// measureCaption puts n's caption under what it shows, which is top tall.
func (m *Map) measureCaption(n *Node, top float64) {
	p := &n.lay
	p.h = top
	text := n.Caption()
	if text == "" {
		p.lines = nil
		return
	}
	p.lines = m.wrap(text, p.lk.size, p.lk.bold, p.w-2*captionPad)
	th := float64(len(p.lines)) * p.lineH
	p.caption = R(captionPad, top+captionPad, p.w-2*captionPad, th)
	p.text = p.caption
	p.h = top + th + 2*captionPad
}

func (m *Map) measureTable(n *Node) {
	p := &n.lay
	g := n.Cells
	if g == nil || g.Rows == 0 || g.Cols == 0 {
		g = &Grid{Rows: 1, Cols: 1, Cells: []string{""}}
	}
	size, bold := p.lk.size, p.lk.bold
	p.cells = evalGrid(g)
	p.cols = make([]float64, g.Cols)
	for c := range g.Cols {
		if c < len(g.ColW) && g.ColW[c] > 0 {
			p.cols[c] = g.ColW[c]
			continue
		}
		w := 0.0
		for r := range g.Rows {
			for _, line := range strings.Split(p.cells[r*g.Cols+c], "\n") {
				w = math.Max(w, m.measure.Width(line, size, bold))
			}
		}
		p.cols[c] = math.Min(math.Max(math.Ceil(w)+2*cellPadX, minCellW), maxCellW)
	}
	p.rows = make([]float64, g.Rows)
	p.cellLn = make([][]string, len(p.cells))
	tw := 0.0
	for _, w := range p.cols {
		tw += w
	}
	th := 0.0
	for r := range g.Rows {
		lines := 1
		for c := range g.Cols {
			i := r*g.Cols + c
			p.cellLn[i] = m.wrap(p.cells[i], size, bold, p.cols[c]-2*cellPadX)
			lines = max(lines, len(p.cellLn[i]))
		}
		p.rows[r] = math.Ceil(float64(lines)*p.lineH + 2*cellPadY)
		th += p.rows[r]
	}
	p.w, p.media = tw, R(0, 0, tw, th)
	m.measureCaption(n, th)
}

// Pos is the top-left of n's box on the board.
func (m *Map) Pos(n *Node) Vec {
	v := Vec{}
	for n.Parent != 0 {
		v = v.Add(n.lay.rel)
		n = m.nodes[n.Parent]
	}
	return v.Add(Vec{n.X, n.Y})
}

// Box is n's box on the board.
func (m *Map) Box(n *Node) Rect { return R(0, 0, n.lay.w, n.lay.h).Add(m.Pos(n)) }

// SubBox is the box around n's whole subtree on the board.
func (m *Map) SubBox(n *Node) Rect { return n.lay.sub.Add(m.Pos(n)) }

// Hidden reports whether n is under a folded ancestor.
func (m *Map) Hidden(n *Node) bool {
	for n.Parent != 0 {
		n = m.nodes[n.Parent]
		if n.Folded {
			return true
		}
	}
	return false
}

// Bounds is the box around everything on the board, empty for an empty board.
func (m *Map) Bounds() Rect {
	m.Layout()
	r := emptyRect
	m.grid.each(func(id ID, b Rect) { r = r.Union(b) })
	m.EachEdge(func(e *Edge) bool {
		if e.geo.ok {
			r = r.Union(e.geo.box)
		}
		return true
	})
	if math.IsInf(r.X0, 0) {
		return Rect{}
	}
	return r
}

// TextRect is where n's words are on the board, and the size and boldness they are written at.
func (m *Map) TextRect(n *Node) (r Rect, size float32, bold bool) {
	m.Layout()
	return n.lay.text.Add(m.Pos(n)), n.lay.lk.size, n.lay.lk.bold
}

// MediaRect is where n shows its picture, video, map or table on the board.
func (m *Map) MediaRect(n *Node) Rect {
	m.Layout()
	return n.lay.media.Add(m.Pos(n))
}

// CellAt is the cell of table n at the board point x, y, and ok when there is one.
func (m *Map) CellAt(n *Node, x, y float64) (row, col int, ok bool) {
	m.Layout()
	if n.Kind != Table {
		return 0, 0, false
	}
	pos := m.Pos(n)
	x, y = x-pos.X, y-pos.Y
	row, col = -1, -1
	for r, h := range n.lay.rows {
		if y >= 0 && y < h {
			row = r
			break
		}
		y -= h
	}
	for c, w := range n.lay.cols {
		if x >= 0 && x < w {
			col = c
			break
		}
		x -= w
	}
	return row, col, row >= 0 && col >= 0
}

// CellRect is the box of a table's cell on the board.
func (m *Map) CellRect(n *Node, row, col int) Rect {
	m.Layout()
	pos := m.Pos(n)
	x, y := pos.X, pos.Y
	for c := 0; c < col && c < len(n.lay.cols); c++ {
		x += n.lay.cols[c]
	}
	for r := 0; r < row && r < len(n.lay.rows); r++ {
		y += n.lay.rows[r]
	}
	if row >= len(n.lay.rows) || col >= len(n.lay.cols) {
		return Rect{}
	}
	return R(x, y, n.lay.cols[col], n.lay.rows[row])
}

// Lines are n's words as laid out, wrapped.
func (m *Map) Lines(n *Node) []string {
	m.Layout()
	return n.lay.lines
}

// Dir is the way n's children grow.
func (m *Map) Dir(n *Node) Layout {
	m.Layout()
	return n.lay.dir
}

// Look reports the shape, colours and words' size n is drawn with.
func (m *Map) Look(n *Node) (shape Shape, fill, line, ink [4]uint8, size float32) {
	m.Layout()
	lk := n.lay.lk
	c := func(v [4]uint8) [4]uint8 { return v }
	return lk.shape, c([4]uint8{lk.fill.R, lk.fill.G, lk.fill.B, lk.fill.A}), c([4]uint8{lk.line.R, lk.line.G, lk.line.B, lk.line.A}),
		c([4]uint8{lk.ink.R, lk.ink.G, lk.ink.B, lk.ink.A}), lk.size
}

// countTasks works out how many tasks lie at the ends of n's subtree and how many of them are done, from its
// children's counts, which are laid out before it (a folded node's are counted afresh); when they changed, n's box,
// which shows them, is drawn again.
func (m *Map) countTasks(n *Node, at Vec) {
	p := &n.lay
	tasks, done := 0, 0
	for _, id := range n.Children {
		c := m.nodes[id]
		if n.Folded {
			t, d := m.subtreeTasks(c)
			tasks, done = tasks+t, done+d
			continue
		}
		tasks, done = tasks+c.lay.tasks, done+c.lay.done
	}
	under := tasks > 0
	if tasks == 0 && n.Task {
		tasks = 1
		if n.Done {
			done = 1
		}
	}
	if tasks != p.tasks || done != p.done || under != p.under {
		p.tasks, p.done, p.under = tasks, done, under
		if n.Task {
			m.lay.region(R(0, 0, p.w, p.h).Add(at))
		}
	}
}

// subtreeTasks counts the tasks at the ends of n's subtree and how many are done, reading every node in it.
func (m *Map) subtreeTasks(n *Node) (tasks, done int) {
	for _, id := range n.Children {
		t, d := m.subtreeTasks(m.nodes[id])
		tasks, done = tasks+t, done+d
	}
	if tasks == 0 && n.Task {
		tasks = 1
		if n.Done {
			done = 1
		}
	}
	return tasks, done
}

// Progress is how many of the tasks at the ends of n's subtree are done, and how many there are: for a task with
// none under it, 1 of 1 or 0 of 1.
func (m *Map) Progress(n *Node) (done, tasks int) {
	m.Layout()
	return n.lay.done, n.lay.tasks
}

// TaskBox is where a task's box or ring is on the board, and ok when n is a task.
func (m *Map) TaskBox(n *Node) (Rect, bool) {
	m.Layout()
	if !n.Task || n.Kind != Topic {
		return Rect{}, false
	}
	return n.lay.task.Add(m.Pos(n)), true
}
