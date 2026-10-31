package formula

import (
	"math/bits"
	"sort"
	"strings"
	"time"
)

// Sizes of the two segment trees of the dependency index.
const (
	colLeaves = 1 << 14
	rowLeaves = 1 << 20
)

// maxDemand bounds the on-demand evaluations of dynamic references that can nest.
const maxDemand = 200

// maxCircular caps what Circular reports.
const maxCircular = 1000

// How a formula stands in the pass under way.
const (
	stClean uint8 = iota
	stDirty
	stRunning
	stEarly // evaluated on demand; its dependents still wait for it in the queue
)

const seenBit uint8 = 0x80 // in st while the dirty list is being deduplicated

// Flags of a formula record.
const (
	flagArray uint8 = 1 << iota
	flagVolatile
	flagFrozen // Known is false: a value that never changes
	flagDead   // replaced; its entries in the index are dropped as they are met
)

// formula is one registered cell. Many records share one Expr.
type formula struct {
	x      *Expr
	cell   Cell
	dr, dc int32
	flags  uint8
}

// rowNode holds the row tree of one column-tree node.
type rowNode struct {
	rows   map[int32][]int32
	levels uint32 // heights of the row tree that hold anything
}

// sheetDeps is a sparse 2-D segment tree: column tree over row trees, formula ids at the leaves of the decomposition.
type sheetDeps struct {
	cols    map[int32]*rowNode
	present [(2 * colLeaves) / 64]uint64 // which column nodes exist, so a query skips the map
}

func (s *sheetDeps) node(i int32) *rowNode {
	n := s.cols[i]
	if n == nil {
		n = &rowNode{rows: map[int32][]int32{}}
		s.cols[i] = n
		s.present[i>>6] |= 1 << (uint(i) & 63)
	}
	return n
}

func (n *rowNode) add(r1, r2, id int32) {
	put := func(i int32) {
		n.rows[i] = append(n.rows[i], id)
		n.levels |= 1 << uint(21-bits.Len32(uint32(i)))
	}
	for l, r := r1+rowLeaves, r2+rowLeaves+1; l < r; l, r = l>>1, r>>1 {
		if l&1 == 1 {
			put(l)
			l++
		}
		if r&1 == 1 {
			r--
			put(r)
		}
	}
}

func (s *sheetDeps) add(a Area, id int32) {
	for l, r := a.C1+colLeaves, a.C2+colLeaves+1; l < r; l, r = l>>1, r>>1 {
		if l&1 == 1 {
			s.node(l).add(a.R1, a.R2, id)
			l++
		}
		if r&1 == 1 {
			r--
			s.node(r).add(a.R1, a.R2, id)
		}
	}
}

// colIndex is the exact-match index of one column range.
type colIndex struct {
	nums  map[float64]int32
	texts map[string]int32
	bools [2]int32
}

// Engine keeps the dependency graph of a workbook's formulas and recalculates them incrementally.
type Engine struct {
	b     Book
	fs    []formula
	st    []uint8
	cells map[Cell]int32
	live  int
	dead  int

	exact  map[Cell][]int32 // single-cell precedents
	sheets map[int32]*sheetDeps

	volatile     []int32
	spill        map[int32]Area // the last extent of array formulas with more than one cell
	spillNext    Area
	spillHas     bool
	hasSubtotals bool

	touched  []Cell
	touchAll bool
	refresh  bool
	open     bool // a pass is under way

	queue []int32 // ids whose dependents are still to mark
	qi    int

	dirty     int
	dirtyList []int32
	stale     bool // indeg and ready no longer describe the dirty set
	buildAt   int  // how far the current build got; -1 when none is running
	built     bool
	indeg     []int32
	ready     []int32

	cycleSeen map[int32]bool // cells found circular while this pass runs
	cycleList []Cell
	circular  []Cell

	names map[string]*Expr

	indexes map[Area]*colIndex
	byCol   map[int64][]Area

	buf []Area
}

// NewEngine makes an engine over a book.
func NewEngine(b Book) *Engine {
	e := &Engine{b: b}
	e.Reset()
	return e
}

// Reset forgets every formula.
func (e *Engine) Reset() {
	*e = Engine{
		b:         e.b,
		cells:     map[Cell]int32{},
		exact:     map[Cell][]int32{},
		sheets:    map[int32]*sheetDeps{},
		spill:     map[int32]Area{},
		cycleSeen: map[int32]bool{},
		names:     map[string]*Expr{},
		indexes:   map[Area]*colIndex{},
		byCol:     map[int64][]Area{},
		buildAt:   -1,
	}
}

// Count says how many formulas are registered.
func (e *Engine) Count() int { return e.live }

// parsed is Parse with a memory, for defined names and INDIRECT texts. A text that does not parse gives nil.
func (e *Engine) parsed(text string) *Expr {
	if x, ok := e.names[text]; ok {
		return x
	}
	x, err := Parse(text)
	if err != nil {
		x = nil
	}
	if len(e.names) >= 1<<16 {
		clear(e.names)
	}
	e.names[text] = x
	return x
}

func (e *Engine) register(id int32) {
	f := &e.fs[id]
	e.buf = f.x.refsInto(e.buf[:0], int(f.cell.Sheet), int(f.cell.Row), int(f.cell.Col), int(f.dr), int(f.dc), e.b, 0, e)
	for _, a := range e.buf {
		if a.R1 == a.R2 && a.C1 == a.C2 {
			c := Cell{Sheet: a.Sheet, Row: a.R1, Col: a.C1}
			e.exact[c] = append(e.exact[c], id)
			continue
		}
		s := e.sheets[a.Sheet]
		if s == nil {
			s = &sheetDeps{cols: map[int32]*rowNode{}}
			e.sheets[a.Sheet] = s
		}
		s.add(a, id)
	}
}

// Set registers the formula at c, replacing any there; a nil x removes it. dr, dc and array are as in Eval. A formula
// that is not Known stays in the graph as a value that never changes. Set does not mark anything dirty.
func (e *Engine) Set(c Cell, x *Expr, dr, dc int, array bool) {
	if id, ok := e.cells[c]; ok {
		e.kill(id)
		delete(e.cells, c)
	}
	if x == nil {
		return
	}
	var flags uint8
	if array {
		flags |= flagArray
	}
	if x.volatile {
		flags |= flagVolatile
	}
	if !x.known {
		flags |= flagFrozen
		flags &^= flagVolatile
	}
	id := int32(len(e.fs))
	e.fs = append(e.fs, formula{x: x, cell: c, dr: int32(dr), dc: int32(dc), flags: flags})
	e.st = append(e.st, stClean)
	e.cells[c] = id
	e.live++
	if flags&flagFrozen == 0 {
		e.register(id)
	}
	if flags&flagVolatile != 0 {
		e.volatile = append(e.volatile, id)
	}
	e.hasSubtotals = e.hasSubtotals || x.subtotal
	if e.dead > e.live+1<<16 {
		e.compact()
	}
}

func (e *Engine) kill(id int32) {
	e.fs[id].flags |= flagDead
	e.fs[id].x = nil
	e.live--
	e.dead++
	if e.st[id] != stClean {
		e.st[id] = stClean
		e.dirty--
		e.stale = true
		e.buildAt = -1
	}
	delete(e.spill, id)
}

// compact renumbers the formulas without the dead ones and rebuilds the index.
func (e *Engine) compact() {
	remap := make([]int32, len(e.fs))
	fs := make([]formula, 0, e.live)
	st := make([]uint8, 0, e.live)
	for i := range e.fs {
		if e.fs[i].flags&flagDead != 0 {
			remap[i] = -1
			continue
		}
		remap[i] = int32(len(fs))
		fs = append(fs, e.fs[i])
		st = append(st, e.st[i])
	}
	move := func(ids []int32) []int32 {
		out := ids[:0]
		for _, id := range ids {
			if n := remap[id]; n >= 0 {
				out = append(out, n)
			}
		}
		return out
	}
	e.fs, e.st = fs, st
	e.volatile = move(e.volatile)
	e.queue = move(e.queue[e.qi:])
	e.qi = 0
	e.dirtyList = move(e.dirtyList)
	e.ready = e.ready[:0]
	e.indeg = nil
	e.stale = true
	e.buildAt = -1
	e.built = false
	spill := make(map[int32]Area, len(e.spill))
	for id, a := range e.spill {
		if n := remap[id]; n >= 0 {
			spill[n] = a
		}
	}
	e.spill = spill
	cycle := make(map[int32]bool, len(e.cycleSeen))
	for id := range e.cycleSeen {
		if n := remap[id]; n >= 0 {
			cycle[n] = true
		}
	}
	e.cycleSeen = cycle
	e.exact = map[Cell][]int32{}
	e.sheets = map[int32]*sheetDeps{}
	e.dead = 0
	clear(e.cells)
	for id := range e.fs {
		e.cells[e.fs[id].cell] = int32(id)
		if e.fs[id].flags&flagFrozen == 0 {
			e.register(int32(id))
		}
	}
}

// walk calls fn for the live formulas in a list and drops the dead ones from it.
func (e *Engine) walk(list []int32, fn func(int32)) []int32 {
	n := 0
	for _, id := range list {
		if e.fs[id].flags&flagDead != 0 {
			continue
		}
		list[n] = id
		n++
		fn(id)
	}
	return list[:n]
}

// dependents calls fn for each formula that reads the cell; a formula that reads it twice comes twice.
func (e *Engine) dependents(c Cell, fn func(int32)) {
	if list, ok := e.exact[c]; ok {
		kept := e.walk(list, fn)
		if len(kept) != len(list) {
			if len(kept) == 0 {
				delete(e.exact, c)
			} else {
				e.exact[c] = kept
			}
		}
	}
	s := e.sheets[c.Sheet]
	if s == nil {
		return
	}
	for i := c.Col + colLeaves; i >= 1; i >>= 1 {
		if s.present[i>>6]&(1<<(uint(i)&63)) == 0 {
			continue
		}
		n := s.cols[i]
		leaf := c.Row + rowLeaves
		for m := n.levels; m != 0; m &= m - 1 {
			node := leaf >> uint(bits.TrailingZeros32(m))
			list, ok := n.rows[node]
			if !ok {
				continue
			}
			kept := e.walk(list, fn)
			if len(kept) != len(list) {
				n.rows[node] = kept
			}
		}
	}
}

// successors calls fn for everything that reads a formula's cell or its spilled cells.
func (e *Engine) successors(id int32, fn func(int32)) {
	f := &e.fs[id]
	e.dependents(f.cell, fn)
	if a, ok := e.spill[id]; ok {
		for r := a.R1; r <= a.R2; r++ {
			for k := a.C1; k <= a.C2; k++ {
				c := Cell{Sheet: a.Sheet, Row: r, Col: k}
				if c != f.cell {
					e.dependents(c, fn)
				}
			}
		}
	}
}

// Touch says c's value changed, or its formula: c, if it is a formula, and everything depending on it need
// recalculating.
func (e *Engine) Touch(c Cell) {
	e.touched = append(e.touched, c)
	e.dropIndexes(c.Sheet, c.Col)
}

// TouchAll says everything is dirty.
func (e *Engine) TouchAll() {
	e.touchAll = true
	e.dropAllIndexes()
}

// Refresh starts a pass that recalculates volatile formulas.
func (e *Engine) Refresh() { e.refresh = true }

// Pending says there is dirty work.
func (e *Engine) Pending() bool {
	return e.open || e.refresh || e.touchAll || len(e.touched) > 0 || e.dirty > 0
}

// Circular gives the cells found in a cycle by the last finished pass, sorted, capped at 1000.
func (e *Engine) Circular() []Cell { return e.circular }

func (e *Engine) mark(id int32) {
	if e.st[id] != stClean && e.st[id] != stEarly {
		return
	}
	f := &e.fs[id]
	if f.flags&(flagFrozen|flagDead) != 0 {
		return
	}
	if e.st[id] == stClean {
		e.dirty++
	}
	e.st[id] = stDirty
	e.dirtyList = append(e.dirtyList, id)
	e.queue = append(e.queue, id)
	e.stale = true
	e.buildAt = -1
}

// stopwatch keeps Recalc to its budget, reading the clock every sixteenth call.
type stopwatch struct {
	deadline time.Time
	limited  bool
	n        int
}

func (w *stopwatch) expired() bool {
	if !w.limited {
		return false
	}
	w.n++
	if w.n&15 != 0 {
		return false
	}
	return time.Now().After(w.deadline)
}

// absorb turns touched cells into the dirty set, closed over dependents. It reports false when out of time.
func (e *Engine) absorb(w *stopwatch) bool {
	if e.touchAll {
		e.touchAll = false
		e.touched = e.touched[:0]
		e.queue = e.queue[:0]
		e.qi = 0
		for id := range e.fs {
			if e.st[id] == stClean && e.fs[id].flags&(flagFrozen|flagDead) == 0 {
				e.st[id] = stDirty
				e.dirty++
				e.dirtyList = append(e.dirtyList, int32(id))
			} else if e.st[id] == stEarly {
				e.st[id] = stDirty
				e.dirtyList = append(e.dirtyList, int32(id))
			}
		}
		e.stale = true
	}
	for len(e.touched) > 0 {
		c := e.touched[len(e.touched)-1]
		e.touched = e.touched[:len(e.touched)-1]
		if id, ok := e.cells[c]; ok {
			e.mark(id)
		}
		e.dependents(c, e.mark)
		if w.expired() {
			return false
		}
	}
	for e.qi < len(e.queue) {
		id := e.queue[e.qi]
		e.qi++
		e.successors(id, e.mark)
		if w.expired() {
			return false
		}
	}
	e.queue = e.queue[:0]
	e.qi = 0
	return true
}

// build works out how many dirty precedents each dirty formula has. It reports false when out of time.
func (e *Engine) build(w *stopwatch) bool {
	if e.buildAt < 0 {
		// Compact the dirty list: drop finished ids and duplicates.
		list := e.dirtyList[:0]
		for _, id := range e.dirtyList {
			if e.st[id] == stClean || e.st[id]&seenBit != 0 {
				continue
			}
			e.st[id] |= seenBit
			list = append(list, id)
		}
		e.dirtyList = list
		if len(e.indeg) < len(e.fs) {
			e.indeg = make([]int32, len(e.fs))
		}
		for _, id := range list {
			e.st[id] &^= seenBit
			e.indeg[id] = 0
		}
		e.buildAt = 0
		e.ready = e.ready[:0]
	}
	count := func(g int32) {
		if e.st[g] != stClean {
			e.indeg[g]++
		}
	}
	for e.buildAt < len(e.dirtyList) {
		e.successors(e.dirtyList[e.buildAt], count)
		e.buildAt++
		if w.expired() {
			return false
		}
	}
	for _, id := range e.dirtyList {
		if e.indeg[id] == 0 {
			e.ready = append(e.ready, id)
		}
	}
	e.buildAt = -1
	e.stale = false
	e.built = true
	return true
}

// run evaluates one formula and writes its result.
func (e *Engine) run(id int32, depth int) {
	f := &e.fs[id]
	e.st[id] = stRunning
	c := ctx{b: e.b, e: e, sheet: int(f.cell.Sheet), row: int(f.cell.Row), col: int(f.cell.Col),
		dr: int(f.dr), dc: int(f.dc), array: f.flags&flagArray != 0, depth: depth}
	v := c.top(f.x)
	if e.cycleSeen[id] {
		v = Num(0)
	}
	e.write(id, v)
	e.st[id] = stEarly
	if f.flags&flagArray != 0 {
		e.noteSpill(id, v, depth)
	}
}

func (e *Engine) write(id int32, v Value) {
	c := e.fs[id].cell
	e.b.SetResult(c, v)
	if len(e.byCol) > 0 {
		e.dropIndexes(c.Sheet, c.Col)
		if v.Kind == Array {
			for k := int32(1); k < int32(v.Arr.Cols); k++ {
				e.dropIndexes(c.Sheet, c.Col+k)
			}
		}
	}
}

// noteSpill records the cells an array formula now covers. The dependents of cells that came or went are touched.
func (e *Engine) noteSpill(id int32, v Value, depth int) {
	c := e.fs[id].cell
	next := Area{Sheet: c.Sheet, R1: c.Row, C1: c.Col, R2: c.Row, C2: c.Col}
	if v.Kind == Array {
		next.R2 = min(c.Row+int32(v.Arr.Rows)-1, MaxRows-1)
		next.C2 = min(c.Col+int32(v.Arr.Cols)-1, MaxCols-1)
	}
	old, had := e.spill[id]
	if !had {
		old = Area{Sheet: c.Sheet, R1: c.Row, C1: c.Col, R2: c.Row, C2: c.Col}
	}
	if old == next {
		return
	}
	if depth > 0 {
		e.applySpill(id, next)
		e.stale = true
		return
	}
	e.spillNext, e.spillHas = next, true
}

// applySpill stores a new extent and touches the dependents of every cell in the old and the new.
func (e *Engine) applySpill(id int32, next Area) {
	c := e.fs[id].cell
	old, had := e.spill[id]
	if next.R1 == next.R2 && next.C1 == next.C2 {
		delete(e.spill, id)
	} else {
		e.spill[id] = next
	}
	add := func(a Area) {
		for r := a.R1; r <= a.R2; r++ {
			for k := a.C1; k <= a.C2; k++ {
				if p := (Cell{Sheet: a.Sheet, Row: r, Col: k}); p != c {
					e.touched = append(e.touched, p)
				}
			}
		}
	}
	if had {
		add(old)
	}
	add(next)
}

// settle evaluates the dirty formulas inside an area, for a reference the graph could not know about.
func (e *Engine) settle(a Area, depth int) {
	if e.dirty == 0 || depth >= maxDemand {
		return
	}
	if areaCells(a) <= float64(len(e.dirtyList)) {
		for r := a.R1; r <= a.R2; r++ {
			for k := a.C1; k <= a.C2; k++ {
				if id, ok := e.cells[Cell{Sheet: a.Sheet, Row: r, Col: k}]; ok {
					e.demand(id, depth)
				}
			}
		}
		return
	}
	for _, id := range e.dirtyList {
		if e.st[id] != stDirty && e.st[id] != stRunning {
			continue
		}
		c := e.fs[id].cell
		if c.Sheet == a.Sheet && c.Row >= a.R1 && c.Row <= a.R2 && c.Col >= a.C1 && c.Col <= a.C2 {
			e.demand(id, depth)
		}
	}
}

func (e *Engine) demand(id int32, depth int) {
	switch e.st[id] {
	case stRunning:
		// Reading a cell whose evaluation led here: a cycle.
		if !e.cycleSeen[id] {
			e.noteCycle(id)
		}
		e.b.SetResult(e.fs[id].cell, Num(0))
	case stDirty:
		e.run(id, depth+1)
	}
}

func (e *Engine) noteCycle(id int32) {
	e.cycleSeen[id] = true
	if len(e.cycleList) < maxCircular {
		e.cycleList = append(e.cycleList, e.fs[id].cell)
	}
}

// release lets the dependents of a finished formula go ahead.
func (e *Engine) release(id int32) {
	e.successors(id, func(g int32) {
		if e.st[g] != stClean {
			e.indeg[g]--
			if e.indeg[g] == 0 {
				e.ready = append(e.ready, g)
			}
		}
	})
}

// drain evaluates what is ready. It reports false when out of time.
func (e *Engine) drain(w *stopwatch) bool {
	for len(e.ready) > 0 {
		id := e.ready[len(e.ready)-1]
		e.ready = e.ready[:len(e.ready)-1]
		if e.st[id] == stClean {
			continue
		}
		if e.st[id] == stDirty {
			e.run(id, 0)
		}
		e.st[id] = stClean
		e.dirty--
		e.release(id)
		if e.spillHas {
			e.spillHas = false
			e.applySpill(id, e.spillNext)
		}
		if e.stale || len(e.touched) > 0 {
			return true
		}
		if w.expired() {
			return false
		}
	}
	return true
}

// tarjanFrame is one level of the iterative depth-first search.
type tarjanFrame struct {
	id       int32
	from, at int32 // this node's successors in the arena
}

// breakCycles runs when evaluation stalls: whatever is left waits on a cycle. Tarjan's algorithm, iteratively, finds
// the strongly connected components of what is left; their members become 0, and what is downstream carries on.
func (e *Engine) breakCycles() {
	order := make([]int32, len(e.fs)) // visit number, 0 for unvisited
	low := make([]int32, len(e.fs))
	onStack := make([]bool, len(e.fs))
	self := map[int32]bool{}
	var stack []int32
	var frames []tarjanFrame
	var arena []int32
	var cyclic []int32
	next := int32(0)

	visit := func(id int32) {
		next++
		order[id], low[id] = next, next
		stack = append(stack, id)
		onStack[id] = true
		from := int32(len(arena))
		e.successors(id, func(g int32) {
			if e.st[g] == stClean {
				return
			}
			if g == id {
				self[id] = true
				return
			}
			arena = append(arena, g)
		})
		frames = append(frames, tarjanFrame{id: id, from: from, at: from})
	}
	for _, root := range e.dirtyList {
		if e.st[root] == stClean || order[root] != 0 {
			continue
		}
		visit(root)
		for len(frames) > 0 {
			top := &frames[len(frames)-1]
			// The successors of the top frame run to the end of the arena.
			if int(top.at) < len(arena) {
				g := arena[top.at]
				top.at++
				if order[g] == 0 {
					visit(g)
				} else if onStack[g] {
					low[top.id] = min(low[top.id], order[g])
				}
				continue
			}
			id := top.id
			arena = arena[:top.from]
			frames = frames[:len(frames)-1]
			if len(frames) > 0 {
				parent := frames[len(frames)-1].id
				low[parent] = min(low[parent], low[id])
			}
			if low[id] != order[id] {
				continue
			}
			first := len(stack) - 1
			for stack[first] != id {
				first--
			}
			scc := stack[first:]
			if len(scc) > 1 || self[id] {
				cyclic = append(cyclic, scc...)
			}
			for _, m := range scc {
				onStack[m] = false
			}
			stack = stack[:first]
		}
	}
	for _, id := range cyclic {
		if !e.cycleSeen[id] {
			e.noteCycle(id)
		}
		e.st[id] = stClean
		e.dirty--
		e.write(id, Num(0))
	}
	for _, id := range cyclic {
		e.release(id)
	}
}

// Recalc works through dirty formulas in dependency order, writing through Book.SetResult, for at most budget (0 is
// until finished). It returns true when finished. A later Recalc carries on, and Touch in between is merged.
func (e *Engine) Recalc(budget time.Duration) (done bool) {
	w := stopwatch{limited: budget > 0}
	if w.limited {
		w.deadline = time.Now().Add(budget)
	}
	for {
		if !e.open {
			if !e.Pending() {
				return true
			}
			e.open = true
			e.refresh = false
			clear(e.cycleSeen)
			e.cycleList = e.cycleList[:0]
			e.dropAllIndexes()
			for _, id := range e.volatile {
				if e.fs[id].flags&flagDead == 0 {
					e.mark(id)
				}
			}
			e.volatile = e.trimVolatile()
		}
		if e.touchAll || len(e.touched) > 0 || e.qi < len(e.queue) {
			if !e.absorb(&w) {
				return false
			}
		}
		if e.dirty == 0 {
			e.finish()
			continue
		}
		if e.stale || !e.built {
			if !e.build(&w) {
				return false
			}
		}
		if !e.drain(&w) {
			return false
		}
		if e.stale || len(e.touched) > 0 {
			continue
		}
		if e.dirty > 0 {
			e.breakCycles()
		}
	}
}

func (e *Engine) trimVolatile() []int32 {
	out := e.volatile[:0]
	for _, id := range e.volatile {
		if e.fs[id].flags&flagDead == 0 {
			out = append(out, id)
		}
	}
	return out
}

// finish closes a pass.
func (e *Engine) finish() {
	e.open = false
	e.built = false
	e.stale = false
	e.dirtyList = e.dirtyList[:0]
	e.ready = e.ready[:0]
	e.buildAt = -1
	e.dropAllIndexes()
	list := make([]Cell, len(e.cycleList))
	copy(list, e.cycleList)
	sort.Slice(list, func(i, j int) bool {
		a, b := list[i], list[j]
		if a.Sheet != b.Sheet {
			return a.Sheet < b.Sheet
		}
		if a.Row != b.Row {
			return a.Row < b.Row
		}
		return a.Col < b.Col
	})
	e.circular = list
}

func (e *Engine) isSubtotal(sheet, row, col int) bool {
	id, ok := e.cells[Cell{Sheet: int32(sheet), Row: int32(row), Col: int32(col)}]
	return ok && e.fs[id].x != nil && e.fs[id].x.subtotal
}

func colKey(sheet, col int32) int64 { return int64(sheet)<<32 | int64(uint32(col)) }

func (e *Engine) dropIndexes(sheet, col int32) {
	if len(e.byCol) == 0 {
		return
	}
	k := colKey(sheet, col)
	for _, a := range e.byCol[k] {
		delete(e.indexes, a)
	}
	delete(e.byCol, k)
}

func (e *Engine) dropAllIndexes() {
	if len(e.indexes) > 0 {
		clear(e.indexes)
	}
	if len(e.byCol) > 0 {
		clear(e.byCol)
	}
}

// findExact looks a value up in a column through a hash index built on first use in the pass.
func (e *Engine) findExact(c *ctx, a Area, want Value) int {
	ix := e.indexes[a]
	if ix == nil {
		ix = &colIndex{nums: map[float64]int32{}, texts: map[string]int32{}, bools: [2]int32{-1, -1}}
		base := a.R1
		c.b.Each(int(a.Sheet), int(a.R1), int(a.C1), int(a.R2), int(a.C1), func(r, _ int, v Value) bool {
			at := int32(r) - base
			switch v.Kind {
			case Number:
				if _, ok := ix.nums[v.Num]; !ok {
					ix.nums[v.Num] = at
				}
			case Text:
				k := strings.ToLower(v.Str)
				if _, ok := ix.texts[k]; !ok {
					ix.texts[k] = at
				}
			case Bool:
				if ix.bools[int(v.Num)] < 0 {
					ix.bools[int(v.Num)] = at
				}
			}
			return true
		})
		e.indexes[a] = ix
		k := colKey(a.Sheet, a.C1)
		e.byCol[k] = append(e.byCol[k], a)
	}
	switch want.Kind {
	case Number:
		if i, ok := ix.nums[want.Num]; ok {
			return int(i)
		}
	case Text:
		if i, ok := ix.texts[strings.ToLower(want.Str)]; ok {
			return int(i)
		}
	case Bool:
		return int(ix.bools[int(want.Num)])
	}
	return -1
}

// Refs gives the static precedents of the formula placed at (sheet,row,col) with offset dr,dc: every reference and
// range, whole columns as R1=0,R2=1048575 and whole rows as C1=0,C2=16383. Sheet qualifiers are resolved through b,
// 3-D references expanded, references through defined names followed, and unknown sheets dropped.
func (x *Expr) Refs(sheet, row, col, dr, dc int, b Book) []Area {
	return x.refsInto(nil, sheet, row, col, dr, dc, b, 0, nil)
}

func (x *Expr) refsInto(out []Area, sheet, row, col, dr, dc int, b Book, depth int, e *Engine) []Area {
	return x.collect(out, sheet, row, col, dr, dc, false, b, depth, e)
}

func (x *Expr) collect(out []Area, sheet, row, col, dr, dc int, wrap bool, b Book, depth int, e *Engine) []Area {
	for i := range x.refs {
		r := &x.refs[i]
		if r.external || r.bad {
			continue
		}
		s := sheet
		if r.hasSheet {
			if s = b.SheetIndex(r.sheet1); s < 0 {
				continue
			}
		}
		if r.isName {
			if depth >= maxNames {
				continue
			}
			text := strings.TrimPrefix(strings.TrimSpace(b.Name(r.name, s)), "=")
			if text == "" {
				continue
			}
			var sub *Expr
			if e != nil {
				sub = e.parsed(text)
			} else if p, err := Parse(text); err == nil {
				sub = p
			}
			if sub != nil {
				out = sub.collect(out, s, row, col, row, col, true, b, depth+1, e)
			}
			continue
		}
		r1, c1, r2, c2, ok := r.shift(dr, dc, wrap)
		if !ok {
			continue
		}
		a := normArea(Area{R1: r1, C1: c1, R2: r2, C2: c2})
		last := s
		if r.sheet2 != "" {
			if last = b.SheetIndex(r.sheet2); last < 0 {
				continue
			}
			if s > last {
				s, last = last, s
			}
		}
		for i := s; i <= last; i++ {
			a.Sheet = int32(i)
			out = append(out, a)
		}
	}
	if x.hull {
		return x.root.hulls(out, sheet, dr, dc, wrap, b)
	}
	return out
}

// hulls adds the bounding box of a ':' written between two separate references, which the flat list cannot show.
func (n *node) hulls(out []Area, sheet, dr, dc int, wrap bool, b Book) []Area {
	if n == nil {
		return out
	}
	// The left spine of a long chain is walked in a loop, so a long sum does not recurse.
	for {
		if n.kind == nRange && n.kids[0].kind == nRef && n.kids[1].kind == nRef {
			p, q := n.kids[0].r, n.kids[1].r
			if !p.bad && !q.bad && !p.external && !q.external && p.sheet2 == "" && !q.hasSheet {
				r1, c1, r2, c2, ok := p.shift(dr, dc, wrap)
				x1, y1, x2, y2, ok2 := q.shift(dr, dc, wrap)
				s := sheet
				if p.hasSheet {
					s = b.SheetIndex(p.sheet1)
				}
				if ok && ok2 && s >= 0 {
					a := normArea(Area{R1: r1, C1: c1, R2: r2, C2: c2})
					o := normArea(Area{R1: x1, C1: y1, R2: x2, C2: y2})
					out = append(out, Area{Sheet: int32(s), R1: min(a.R1, o.R1), C1: min(a.C1, o.C1),
						R2: max(a.R2, o.R2), C2: max(a.C2, o.C2)})
				}
			}
			return out
		}
		if len(n.kids) == 0 {
			return out
		}
		for _, k := range n.kids[1:] {
			out = k.hulls(out, sheet, dr, dc, wrap, b)
		}
		n = n.kids[0]
	}
}
