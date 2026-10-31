package paint

import (
	"image"
)

// state is the whole picture at one moment: its frame and paper, the layer tree with snapshots of every raster and
// mask, the chosen node and the selection. Snapshots share their tiles, so a state costs only a copy of the tile
// maps, and two states share every tile neither changed.
type state struct {
	frame    image.Rectangle
	paper    bool
	root     *Node
	active   int
	sel      *Mask
	editMask bool
}

type entry struct {
	name          string
	before, after state
	bytes         int // the pixels this step alone holds: the tiles it made
}

// history keeps the most recent changes within a count and a memory budget.
type history struct {
	done, undone []entry
}

const (
	maxHistory      = 100
	maxHistoryBytes = 768 << 20
)

// snapshotTree copies a tree with a snapshot of every raster and mask, which freezes their tiles: the live picture
// copies a tile before writing to it, so the copy keeps what it had.
func snapshotTree(n *Node) *Node {
	c := *n
	if n.Pixels != nil {
		c.Pixels = n.Pixels.Snapshot()
	}
	if n.Mask != nil {
		c.Mask = n.Mask.Snapshot()
	}
	if n.Children != nil {
		c.Children = make([]*Node, len(n.Children))
		for i, ch := range n.Children {
			c.Children[i] = snapshotTree(ch)
		}
	}
	return &c
}

// copyTree is a copy of a tree sharing its pixels, as snapshotTree's, that keeps the halved levels made so far: for
// copies that are shown, as a smart object's copies are, so that drawing them small does not halve them again. The
// history's snapshots leave the levels out, never drawn.
func copyTree(n *Node) *Node {
	c := *n
	if n.Pixels != nil {
		c.Pixels = n.Pixels.Moved(0, 0)
	}
	if n.Mask != nil {
		c.Mask = n.Mask.Moved(0, 0)
	}
	if n.Children != nil {
		c.Children = make([]*Node, len(n.Children))
		for i, ch := range n.Children {
			c.Children[i] = copyTree(ch)
		}
	}
	return &c
}

// Mark is the state before a change, for Commit.
type Mark struct{ s state }

// Mark notes the state before a change the caller makes directly, such as dragging a layer's opacity.
func (d *Doc) Mark() Mark { return Mark{d.state()} }

func (d *Doc) state() state {
	s := state{frame: d.Frame, paper: d.Paper, root: snapshotTree(d.Root), active: d.Active, editMask: d.EditMask}
	if d.Selection != nil {
		s.sel = d.Selection.Snapshot()
	}
	return s
}

// Commit records the change since m as one step called name; nothing is recorded when nothing changed.
func (d *Doc) Commit(name string, m Mark) {
	d.syncSmart() // a change in a smart object's layers is made in its copies, in the same step
	after := d.state()
	if sameState(m.s, after) {
		return
	}
	h := &d.history
	h.done = append(h.done, entry{name: name, before: m.s, after: after, bytes: newTileBytes(m.s, after)})
	h.undone = nil
	total := 0
	for _, e := range h.done {
		total += e.bytes
	}
	for len(h.done) > 1 && (len(h.done) > maxHistory || total > maxHistoryBytes) {
		total -= h.done[0].bytes
		h.done[0] = entry{}
		h.done = h.done[1:]
	}
}

// newTileBytes is the size of the tiles after has that before has not: what keeping the step costs.
func newTileBytes(before, after state) int {
	old := map[*tile]bool{}
	var collect func(n *Node)
	collect = func(n *Node) {
		if n.Pixels != nil {
			for _, t := range n.Pixels.p.tiles {
				old[t] = true
			}
		}
		if n.Mask != nil {
			for _, t := range n.Mask.p.tiles {
				old[t] = true
			}
		}
		for _, c := range n.Children {
			collect(c)
		}
	}
	collect(before.root)
	if before.sel != nil {
		for _, t := range before.sel.p.tiles {
			old[t] = true
		}
	}
	n := 0
	var count func(p *plane)
	count = func(p *plane) {
		for _, t := range p.tiles {
			if !old[t] {
				n += len(t.pix)
			}
		}
	}
	var walk func(nd *Node)
	walk = func(nd *Node) {
		if nd.Pixels != nil {
			count(&nd.Pixels.p)
		}
		if nd.Mask != nil {
			count(&nd.Mask.p)
		}
		for _, c := range nd.Children {
			walk(c)
		}
	}
	walk(after.root)
	if after.sel != nil {
		count(&after.sel.p)
	}
	return n
}

func sameState(a, b state) bool {
	return a.frame == b.frame && a.paper == b.paper && a.active == b.active && a.editMask == b.editMask &&
		(a.sel == nil) == (b.sel == nil) && (a.sel == nil || a.sel.Same(b.sel)) && sameTree(a.root, b.root)
}

func sameTree(a, b *Node) bool {
	if a.ID != b.ID || a.Name != b.Name || a.Visible != b.Visible || a.Opacity != b.Opacity ||
		(a.Pixels == nil) != (b.Pixels == nil) || a.Pixels != nil && !a.Pixels.Same(b.Pixels) ||
		(a.Mask == nil) != (b.Mask == nil) || a.Mask != nil && !a.Mask.Same(b.Mask) ||
		a.Blend != b.Blend || a.LockAlpha != b.LockAlpha || a.MaskOff != b.MaskOff || a.Open != b.Open || a.Text != b.Text || a.Vector != b.Vector || a.Adjust != b.Adjust || a.Style != b.Style || a.Clip != b.Clip || a.Board != b.Board || a.Smart != b.Smart || a.Filters != b.Filters ||
		len(a.Children) != len(b.Children) {
		return false
	}
	for i := range a.Children {
		if !sameTree(a.Children[i], b.Children[i]) {
			return false
		}
	}
	return true
}

// CanUndo and CanRedo report whether there is a step to take back or to do again; UndoName and RedoName name it.
func (d *Doc) CanUndo() bool { return len(d.history.done) > 0 }
func (d *Doc) CanRedo() bool { return len(d.history.undone) > 0 }

func (d *Doc) UndoName() string {
	if !d.CanUndo() {
		return ""
	}
	return d.history.done[len(d.history.done)-1].name
}

func (d *Doc) RedoName() string {
	if !d.CanRedo() {
		return ""
	}
	return d.history.undone[len(d.history.undone)-1].name
}

// History names the steps that can be undone, oldest first, then those that can be done again, next first; the
// picture is after done of them, as Photoshop's History panel shows.
func (d *Doc) History() (steps []string, done int) {
	h := &d.history
	for _, e := range h.done {
		steps = append(steps, e.name)
	}
	for i := len(h.undone) - 1; i >= 0; i-- {
		steps = append(steps, h.undone[i].name)
	}
	return steps, len(h.done)
}

// GoTo undoes or redoes until done steps are done, as a click in the History panel does.
func (d *Doc) GoTo(done int) {
	if d.stroke != nil {
		return // Undo and Redo wait for the stroke to end
	}
	for len(d.history.done) > done && d.CanUndo() {
		d.Undo()
	}
	for len(d.history.done) < done && d.CanRedo() {
		d.Redo()
	}
}

// Undo takes back the last step.
func (d *Doc) Undo() {
	h := &d.history
	if len(h.done) == 0 || d.stroke != nil {
		return
	}
	e := h.done[len(h.done)-1]
	h.done = h.done[:len(h.done)-1]
	d.restore(e.before)
	h.undone = append(h.undone, e)
	d.syncSmart()
}

// Redo does the last undone step again.
func (d *Doc) Redo() {
	h := &d.history
	if len(h.undone) == 0 || d.stroke != nil {
		return
	}
	e := h.undone[len(h.undone)-1]
	h.undone = h.undone[:len(h.undone)-1]
	d.restore(e.after)
	h.done = append(h.done, e)
	d.syncSmart()
}

// restore puts a kept state back as a fresh snapshot, so later changes do not alter the history.
func (d *Doc) restore(s state) {
	d.Frame, d.Paper = s.frame, s.paper
	d.Root, d.Active, d.EditMask = snapshotTree(s.root), s.active, s.editMask
	d.Selection = nil
	if s.sel != nil {
		d.Selection = s.sel.Snapshot()
	}
}
