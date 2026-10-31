package docx

import (
	"time"

	"github.com/btelolabs/btelo-forge/internal/office/ox"
)

// History. Before a stretch of a parent's children changes, a copy of it is kept with the path to the parent. Undoing
// swaps the copy back in and keeps what it replaced, which is then the step to redo. Steps come undone strictly last
// first, so paths found when a step was made are good when it is undone, and a place in the text can be kept as
// numbers: the paragraph's position in its story and the offset.

const (
	maxSteps = 400
	maxNodes = 4_000_000 // what the kept copies may hold in all
)

// saved is a stretch of a parent's children as it was.
type saved struct {
	part  *Part
	path  []int // from the part's root to the parent
	at    int
	tail  int // how many children followed the stretch
	nodes []*ox.Node
	size  int
}

// selSpot is a selection as numbers.
type selSpot struct {
	story                    string // the part
	aFlat, aOff, cFlat, cOff int
}

type step struct {
	edits   []saved
	before  selSpot
	after   selSpot
	kind    string // what typing joins on to; "" for steps that stand alone
	covered map[*ox.Node]bool
	size    int
	at      time.Time // when it last changed
}

// burstGap is the pause that ends a burst of typing, and with it an undo step.
const burstGap = 1200 * time.Millisecond

type history struct {
	undo, redo []*step
	cur        *step
	nodes      int
	depth      int // begin calls open, so that an operation made of others is one step
}

// Begin opens a step of the kind; an edit made outside a step is one by itself. Typing joins the step before it of
// the same kind, unless the caret moved since.
func (d *Doc) Begin(kind string) {
	h := &d.hist
	h.depth++
	if h.depth > 1 {
		return
	}
	if kind != "" && len(h.undo) > 0 {
		last := h.undo[len(h.undo)-1]
		if last.kind == kind && last.after == d.spot() && time.Since(last.at) < burstGap {
			h.undo = h.undo[:len(h.undo)-1]
			h.nodes -= last.size
			h.cur = last
			return
		}
	}
	h.cur = &step{before: d.spot(), kind: kind, covered: map[*ox.Node]bool{}}
}

// Commit closes a step.
func (d *Doc) Commit() {
	h := &d.hist
	if h.depth == 0 {
		return
	}
	h.depth--
	if h.depth > 0 {
		return
	}
	st := h.cur
	h.cur = nil
	d.rebuild()
	if st == nil || len(st.edits) == 0 {
		return
	}
	st.after, st.at = d.spot(), time.Now()
	h.undo = append(h.undo, st)
	h.clearRedo()
	h.nodes += st.size
	for len(h.undo) > 1 && (len(h.undo) > maxSteps || h.nodes > maxNodes) {
		h.nodes -= h.undo[0].size
		h.undo = h.undo[1:]
	}
}

func (h *history) clearRedo() {
	for _, s := range h.redo {
		h.nodes -= s.size
	}
	h.redo = nil
}

func (d *Doc) spot() selSpot {
	s := d.Sel
	if s.Caret.B == nil {
		return selSpot{}
	}
	return selSpot{story: s.Caret.B.In.Story.Part.Name, aFlat: s.Anchor.B.Flat, aOff: s.Anchor.Off, cFlat: s.Caret.B.Flat, cOff: s.Caret.Off}
}

func (d *Doc) restoreSpot(s selSpot) {
	st := d.Body
	if s.story != d.Main.Name {
		if h := d.stories[s.story]; h != nil {
			st = h
		}
	}
	at := func(flat, off int) Pos {
		if len(st.flat) == 0 {
			return Pos{B: d.Body.first()}
		}
		b := st.flat[min(max(flat, 0), len(st.flat)-1)]
		return Pos{B: b, Off: min(off, b.para().Len)}
	}
	d.Sel = Selection{Anchor: at(s.aFlat, s.aOff), Caret: at(s.cFlat, s.cOff)}
}

func pathOf(n *ox.Node) []int {
	var out []int
	for n.Parent != nil {
		out = append(out, n.Parent.Index(n))
		n = n.Parent
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

func nodeAt(root *ox.Node, path []int) *ox.Node {
	n := root
	for _, i := range path {
		if i < 0 || i >= len(n.Children) {
			return nil
		}
		n = n.Children[i]
	}
	return n
}

func countNodes(n *ox.Node) int {
	c := 1
	for _, k := range n.Children {
		c += countNodes(k)
	}
	return c
}

// saveRange keeps n children of parent from at, before they change. It needs a step open; an edit makes sure of that.
func (d *Doc) saveRange(p *Part, parent *ox.Node, at, n int) {
	st := d.hist.cur
	if st == nil {
		return
	}
	s := saved{part: p, path: pathOf(parent), at: at, tail: len(parent.Children) - at - n}
	for _, c := range parent.Children[at : at+n] {
		clone := c.Clone()
		s.nodes = append(s.nodes, clone)
		s.size += countNodes(clone)
	}
	st.edits = append(st.edits, s)
	st.size += s.size + 8
}

// saveBlock keeps a paragraph or table, once a step.
func (d *Doc) saveBlock(b *Block) {
	d.saveNode(b.In.Story.Part, b.Node)
}

// saveSpan keeps several blocks of a container that will change together. Stretches of one parent must be one saved
// stretch, as undoing puts them back by position.
func (d *Doc) saveSpan(blocks []*Block) {
	st := d.hist.cur
	if st == nil || len(blocks) == 0 {
		return
	}
	part := blocks[0].In.Story.Part
	var parents []*ox.Node
	lo, hi := map[*ox.Node]int{}, map[*ox.Node]int{}
	for _, b := range blocks {
		p := b.Node.Parent
		i := p.Index(b.Node)
		if _, ok := lo[p]; !ok {
			parents = append(parents, p)
			lo[p], hi[p] = i, i
		}
		lo[p], hi[p] = min(lo[p], i), max(hi[p], i)
		st.covered[b.Node] = true
	}
	for _, p := range parents {
		d.saveRange(part, p, lo[p], hi[p]-lo[p]+1)
	}
}

func (d *Doc) saveNode(p *Part, n *ox.Node) {
	st := d.hist.cur
	if st == nil || st.covered[n] || n.Parent == nil {
		return
	}
	st.covered[n] = true
	d.saveRange(p, n.Parent, n.Parent.Index(n), 1)
}

// CanUndo reports whether there is a step to undo.
func (d *Doc) CanUndo() bool { return len(d.hist.undo) > 0 }

// CanRedo reports whether there is a step to redo.
func (d *Doc) CanRedo() bool { return len(d.hist.redo) > 0 }

// Undo takes back the last step.
func (d *Doc) Undo() bool {
	h := &d.hist
	if len(h.undo) == 0 {
		return false
	}
	st := h.undo[len(h.undo)-1]
	h.undo = h.undo[:len(h.undo)-1]
	d.apply(st, st.before)
	h.redo = append(h.redo, st)
	return true
}

// Redo does again the step last undone.
func (d *Doc) Redo() bool {
	h := &d.hist
	if len(h.redo) == 0 {
		return false
	}
	st := h.redo[len(h.redo)-1]
	h.redo = h.redo[:len(h.redo)-1]
	d.apply(st, st.after)
	h.undo = append(h.undo, st)
	return true
}

// apply swaps a step's kept stretches with what is in the tree. The list comes out reversed, ready for the way back.
func (d *Doc) apply(st *step, spot selSpot) {
	stories := map[*Story]bool{}
	styles, numbering := false, false
	var fresh []*ox.Node
	for i := len(st.edits) - 1; i >= 0; i-- {
		e := &st.edits[i]
		parent := nodeAt(e.part.X.Root, e.path)
		if parent == nil {
			continue
		}
		end := len(parent.Children) - e.tail
		current := append([]*ox.Node(nil), parent.Children[e.at:end]...)
		rest := append([]*ox.Node(nil), parent.Children[end:]...)
		parent.Children = parent.Children[:e.at]
		for _, n := range e.nodes {
			n.Parent = parent
			parent.Children = append(parent.Children, n)
		}
		parent.Children = append(parent.Children, rest...)
		fresh = append(fresh, e.nodes...)
		if len(e.nodes) == 0 && end < len(parent.Children) {
			fresh = append(fresh, parent)
		}
		for _, n := range current {
			n.Parent = nil
		}
		e.tail = len(parent.Children) - e.at - len(e.nodes)
		e.nodes = current
		d.touched(e.part)
		switch {
		case d.Styles.Part == e.part:
			styles = true
		case d.Num.Part == e.part:
			numbering = true
		}
		if s := d.storyOfPart(e.part); s != nil {
			stories[s] = true
			s.staleAround(parent)
		}
	}
	for i, j := 0, len(st.edits)-1; i < j; i, j = i+1, j-1 {
		st.edits[i], st.edits[j] = st.edits[j], st.edits[i]
	}
	if styles {
		d.Styles.reset()
	}
	if numbering {
		d.Num.read()
	}
	if styles || numbering {
		d.allChanged()
	}
	for _, n := range d.sects {
		if n.Node != nil && n.Node.Parent == nil {
			d.lay.sectsNew = true
		}
	}
	d.lay.sectsNew = true
	d.rebuild()
	for s := range stories {
		if s == d.Body {
			d.lay.restart()
		} else {
			s.head = nil
			d.lay.stories++
		}
	}
	_ = fresh
	d.restoreSpot(spot)
}

func (d *Doc) storyOfPart(p *Part) *Story {
	if p == d.Main {
		return d.Body
	}
	return d.stories[p.Name]
}

// staleAround marks the blocks around a node as changed.
func (s *Story) staleAround(n *ox.Node) {
	s.stale = true
	for ; n != nil; n = n.Parent {
		if b := s.byNode[n]; b != nil {
			b.markStale()
			if b.T != nil {
				s.d.lay.dirty(b.firstPara())
			}
		}
	}
}

// firstPara is a table's first paragraph, or the block itself.
func (b *Block) firstPara() *Block {
	for b.T != nil {
		if len(b.T.Rows) == 0 || len(b.T.Rows[0].Cells) == 0 || len(b.T.Rows[0].Cells[0].C.Blocks) == 0 {
			return b
		}
		b = b.T.Rows[0].Cells[0].C.Blocks[0]
	}
	return b
}
