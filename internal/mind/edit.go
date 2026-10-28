package mind

import (
	"slices"
)

// Changes are made in steps, each undone and redone whole. A step keeps, for each node and connector it touched,
// what it was before (nil when it did not exist) and, once done, what it became: so a step costs what it changed,
// however large the map. Begin and Commit group several changes into one step; a change made outside them is a step
// of its own.

// historyLimit is how many steps can be undone.
const historyLimit = 500

type step struct {
	label       string
	before      map[ID]*Node
	after       map[ID]*Node
	edgesBefore map[EdgeID]*Edge
	edgesAfter  map[EdgeID]*Edge
	title       [2]string
	titled      bool
	theme       [2]string // the map's theme before and after
	themed      bool
}

type history struct {
	steps   []*step
	at      int // steps[:at] can be undone
	open    *step
	depth   int
	saved   int    // the step the map was saved at, -1 when that step is gone
	commits uint64 // steps made so far
}

// Begin starts a step named label; changes until the matching Commit are undone together.
func (m *Map) Begin(label string) {
	h := &m.hist
	if h.depth == 0 {
		h.open = &step{label: label, before: map[ID]*Node{}, edgesBefore: map[EdgeID]*Edge{}}
	}
	h.depth++
}

// Commit ends a step begun with Begin; a step that changed nothing is dropped.
func (m *Map) Commit() {
	h := &m.hist
	if h.depth == 0 {
		return
	}
	h.depth--
	if h.depth > 0 {
		return
	}
	s := h.open
	h.open = nil
	if len(s.before) == 0 && len(s.edgesBefore) == 0 && !s.titled && !s.themed {
		return
	}
	s.after = make(map[ID]*Node, len(s.before))
	for id := range s.before {
		if n := m.Node(id); n != nil {
			s.after[id] = n.saved()
		} else {
			s.after[id] = nil
		}
	}
	s.edgesAfter = make(map[EdgeID]*Edge, len(s.edgesBefore))
	for id := range s.edgesBefore {
		if e := m.Edge(id); e != nil {
			s.edgesAfter[id] = e.saved()
		} else {
			s.edgesAfter[id] = nil
		}
	}
	if h.saved > h.at {
		h.saved = -1 // the saved state was undone and is now replaced
	}
	h.commits++
	h.steps = append(h.steps[:h.at], s)
	if len(h.steps) > historyLimit {
		h.steps = h.steps[1:]
		h.saved--
	}
	h.at = len(h.steps)
}

// step runs fn as one step unless a step is open, which it then joins.
func (m *Map) step(label string, fn func() error) error {
	m.Begin(label)
	defer m.Commit()
	return fn()
}

// keep remembers what node id was before the open step changed it.
func (m *Map) keep(id ID) {
	s := m.hist.open
	if s == nil {
		panic("mind: a change outside a step")
	}
	if _, ok := s.before[id]; ok {
		return
	}
	if n := m.Node(id); n != nil {
		s.before[id] = n.saved()
	} else {
		s.before[id] = nil
	}
}

func (m *Map) keepEdge(id EdgeID) {
	s := m.hist.open
	if _, ok := s.edgesBefore[id]; ok {
		return
	}
	if e := m.Edge(id); e != nil {
		s.edgesBefore[id] = e.saved()
	} else {
		s.edgesBefore[id] = nil
	}
}

// CanUndo and CanRedo report whether there is a step to undo or redo.
func (m *Map) CanUndo() bool { return m.hist.at > 0 }
func (m *Map) CanRedo() bool { return m.hist.at < len(m.hist.steps) }

// UndoLabel and RedoLabel name the steps Undo and Redo would take back or again.
func (m *Map) UndoLabel() string {
	if !m.CanUndo() {
		return ""
	}
	return m.hist.steps[m.hist.at-1].label
}

func (m *Map) RedoLabel() string {
	if !m.CanRedo() {
		return ""
	}
	return m.hist.steps[m.hist.at].label
}

// Undo takes back the last step; it reports whether there was one.
func (m *Map) Undo() bool {
	h := &m.hist
	if h.open != nil || h.at == 0 {
		return false
	}
	h.at--
	s := h.steps[h.at]
	m.restore(s.before, s.edgesBefore)
	if s.titled {
		m.Title = s.title[0]
	}
	if s.themed {
		m.applyTheme(s.theme[0])
	}
	return true
}

// Redo makes the last undone step again.
func (m *Map) Redo() bool {
	h := &m.hist
	if h.open != nil || h.at == len(h.steps) {
		return false
	}
	s := h.steps[h.at]
	h.at++
	m.restore(s.after, s.edgesAfter)
	if s.titled {
		m.Title = s.title[1]
	}
	if s.themed {
		m.applyTheme(s.theme[1])
	}
	return true
}

// Saved notes that the map as it is now was saved; Unsaved reports whether it changed since.
func (m *Map) Saved()        { m.hist.saved = m.hist.at }
func (m *Map) Unsaved() bool { return m.hist.saved != m.hist.at }

// restore puts back the nodes and connectors as they are in the images.
func (m *Map) restore(nodes map[ID]*Node, edges map[EdgeID]*Edge) {
	for id, img := range nodes {
		if old := m.Node(id); old != nil {
			m.forgetArea(old)
			m.noteRef(old, false)
			if old.Parent == 0 {
				m.lay.gone[id] = true
			}
			if img == nil {
				m.nodes[id] = nil
				m.count--
				continue
			}
		} else if img == nil {
			continue
		} else {
			m.grow(id)
			m.count++
		}
		n := img.saved()
		m.nodes[id] = n
		m.noteRef(n, true)
	}
	// Every image is in place before any is laid out: a restored node's parent may be restored too.
	for id := range nodes {
		if n := m.Node(id); n != nil {
			m.touchLayout(n)
		}
	}
	for id, img := range edges {
		if old := m.Edge(id); old != nil {
			if old.geo.ok {
				m.lay.region(old.geo.box)
			}
			if img == nil {
				m.edges[id] = nil
				m.links--
				continue
			}
		} else if img == nil {
			continue
		} else {
			m.growEdges(id)
			m.links++
		}
		m.edges[id] = img.saved()
	}
	for id := range edges {
		m.lay.edgeDirty[id] = true
	}
	m.lay.edgesStale = true
	m.version++
}

func (m *Map) grow(id ID) {
	for int(id) >= len(m.nodes) {
		m.nodes = append(m.nodes, nil)
	}
}

func (m *Map) growEdges(id EdgeID) {
	for int(id) >= len(m.edges) {
		m.edges = append(m.edges, nil)
	}
}

// SetTitle renames the map, as a step.
func (m *Map) SetTitle(title string) {
	if title == m.Title {
		return
	}
	m.Begin("Rename")
	m.hist.open.title, m.hist.open.titled = [2]string{m.Title, title}, true
	m.Title = title
	m.version++
	m.Commit()
}

// Add puts a node on the board: under parent at index among its children (-1 for the end), or free when parent is
// 0, at n.X, n.Y. It returns the node's id.
func (m *Map) Add(n Node, parent ID, index int) (ID, error) {
	var id ID
	err := m.step("Add", func() error {
		var p *Node
		if parent != 0 {
			if p = m.Node(parent); p == nil {
				return ErrNoNode
			}
			m.keep(parent)
		}
		id = ID(len(m.nodes))
		m.keep(id)
		c := n.saved()
		c.ID, c.Parent, c.Children = id, parent, nil
		if c.Kind == Table && c.Cells == nil {
			c.Cells = &Grid{Rows: 1, Cols: 1, Cells: []string{""}}
		}
		m.nodes = append(m.nodes, c)
		m.count++
		m.noteRef(c, true)
		if p != nil {
			if index < 0 || index > len(p.Children) {
				index = len(p.Children)
			}
			p.Children = slices.Insert(p.Children, index, id)
			if p.Folded {
				p.Folded = false
			}
			m.touchLayout(p)
		}
		m.touchLayout(c)
		return nil
	})
	return id, err
}

// Update changes node id's saved fields with fn, as a step; its id, parent and children stay as they are (Move and
// Delete change those).
func (m *Map) Update(id ID, fn func(n *Node)) error {
	return m.step("Change", func() error {
		n := m.Node(id)
		if n == nil {
			return ErrNoNode
		}
		m.keep(id)
		m.noteRef(n, false)
		parent, children := n.Parent, n.Children
		fn(n)
		n.ID, n.Parent, n.Children = id, parent, children
		m.noteRef(n, true)
		m.touchLayout(n)
		return nil
	})
}

// Delete removes nodes with all their descendants, and the connectors that meet any of them.
func (m *Map) Delete(ids ...ID) error {
	return m.step("Delete", func() error {
		gone := map[ID]bool{}
		for _, id := range ids {
			n := m.Node(id)
			if n == nil {
				return ErrNoNode
			}
			if gone[id] {
				continue
			}
			m.forgetArea(n)
			if n.Parent != 0 {
				p := m.nodes[n.Parent]
				m.keep(p.ID)
				p.Children = slices.DeleteFunc(p.Children, func(c ID) bool { return c == id })
				m.touchLayout(p)
			} else {
				m.lay.gone[id] = true
			}
			m.eachUnder(n, func(d *Node) {
				gone[d.ID] = true
				m.keep(d.ID)
				m.noteRef(d, false)
				m.nodes[d.ID] = nil
				m.count--
			})
		}
		m.EachEdge(func(e *Edge) bool {
			if gone[e.From] || gone[e.To] {
				m.removeEdge(e)
			}
			return true
		})
		m.lay.edgesStale = true
		m.version++
		return nil
	})
}

// eachUnder calls fn for n and every node below it, folded or not, parents first.
func (m *Map) eachUnder(n *Node, fn func(*Node)) {
	stack := []*Node{n}
	for len(stack) > 0 {
		d := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		fn(d)
		for i := len(d.Children) - 1; i >= 0; i-- {
			if c := m.nodes[d.Children[i]]; c != nil {
				stack = append(stack, c)
			}
		}
	}
}

// Descendants counts the nodes below n.
func (m *Map) Descendants(n *Node) int {
	k := -1
	m.eachUnder(n, func(*Node) { k++ })
	return k
}

// Move puts node id under parent at index among its children (-1 for the end, and index counts the children as
// they are without the node), or makes it free at x, y when parent is 0.
func (m *Map) Move(id, parent ID, index int, x, y float64) error {
	return m.step("Move", func() error {
		n := m.Node(id)
		if n == nil {
			return ErrNoNode
		}
		var p *Node
		if parent != 0 {
			if p = m.Node(parent); p == nil {
				return ErrNoNode
			}
			if m.Under(p, n) {
				return ErrCycle
			}
		}
		m.forgetArea(n)
		m.keep(id)
		if n.Parent != 0 {
			old := m.nodes[n.Parent]
			m.keep(old.ID)
			old.Children = slices.DeleteFunc(old.Children, func(c ID) bool { return c == id })
			m.touchLayout(old)
		} else {
			m.lay.gone[id] = true
		}
		n.Parent = parent
		if p == nil {
			n.X, n.Y = x, y
			delete(m.lay.gone, id)
		} else {
			m.keep(parent)
			if index < 0 || index > len(p.Children) {
				index = len(p.Children)
			}
			p.Children = slices.Insert(p.Children, index, id)
			p.Folded = false
			n.lay.rel = Vec{}
			m.touchLayout(p)
		}
		m.eachUnder(n, func(d *Node) { d.lay.measured = false; d.lay.dirty = true })
		m.touchLayout(n)
		return nil
	})
}

// Place moves free node id to x, y.
func (m *Map) Place(id ID, x, y float64) error {
	return m.step("Move", func() error {
		n := m.Node(id)
		if n == nil {
			return ErrNoNode
		}
		if n.Parent != 0 {
			return m.Move(id, 0, -1, x, y)
		}
		if n.X == x && n.Y == y {
			return nil
		}
		m.keep(id)
		n.X, n.Y = x, y
		m.lay.roots[id] = true
		m.lay.edgesStale = true
		m.version++
		return nil
	})
}

// AddEdge connects two nodes; e's fields other than its id are kept.
func (m *Map) AddEdge(e Edge) (EdgeID, error) {
	var id EdgeID
	err := m.step("Connect", func() error {
		if m.Node(e.From) == nil || m.Node(e.To) == nil {
			return ErrNoNode
		}
		if e.From == e.To {
			return ErrSameNode
		}
		id = EdgeID(len(m.edges))
		m.keepEdge(id)
		c := e.saved()
		c.ID = id
		m.edges = append(m.edges, c)
		m.links++
		m.lay.edgeDirty[id] = true
		m.lay.edgesStale = true
		m.version++
		return nil
	})
	return id, err
}

// UpdateEdge changes connector id with fn; its id stays, and its ends must stay two different nodes.
func (m *Map) UpdateEdge(id EdgeID, fn func(e *Edge)) error {
	return m.step("Change", func() error {
		e := m.Edge(id)
		if e == nil {
			return ErrNoEdge
		}
		c := *e
		fn(&c)
		c.ID = id
		if m.Node(c.From) == nil || m.Node(c.To) == nil {
			return ErrNoNode
		}
		if c.From == c.To {
			return ErrSameNode
		}
		m.keepEdge(id)
		*e = c
		m.lay.edgeDirty[id] = true
		m.lay.edgesStale = true
		m.version++
		return nil
	})
}

// DeleteEdge removes connectors.
func (m *Map) DeleteEdge(ids ...EdgeID) error {
	return m.step("Delete", func() error {
		for _, id := range ids {
			e := m.Edge(id)
			if e == nil {
				return ErrNoEdge
			}
			m.removeEdge(e)
		}
		m.version++
		return nil
	})
}

func (m *Map) removeEdge(e *Edge) {
	m.keepEdge(e.ID)
	if e.geo.ok {
		m.lay.region(e.geo.box)
	}
	m.edges[e.ID] = nil
	m.links--
	m.lay.edgesStale = true
	m.lay.edgeDirty[e.ID] = true
}

// LiveEdge changes connector id with fn outside the history, while its middle is dragged; the change is then made
// again as a step with UpdateEdge, after LiveEdge puts it back.
func (m *Map) LiveEdge(id EdgeID, fn func(e *Edge)) {
	e := m.Edge(id)
	if e == nil {
		return
	}
	from, to := e.From, e.To
	fn(e)
	e.ID, e.From, e.To = id, from, to
	m.lay.edgeDirty[id] = true
	m.lay.edgesStale = true
	m.version++
}

// SetAspect sets the proportions of what node id shows, found once it is read; it is not a step of its own, as the
// file it shows decides it, and it is saved so the map is laid out the same before the file is read again.
func (m *Map) SetAspect(id ID, aspect float64) {
	n := m.Node(id)
	if n == nil || aspect <= 0 || n.Aspect == aspect {
		return
	}
	n.Aspect = aspect
	m.touchLayout(n)
	if s := m.hist.open; s != nil {
		if b := s.before[id]; b != nil {
			b.Aspect = aspect
		}
	}
	for _, s := range m.hist.steps {
		for _, img := range []map[ID]*Node{s.before, s.after} {
			if b := img[id]; b != nil {
				b.Aspect = aspect
			}
		}
	}
}

// Refresh lays out again the nodes that show ref, whose content changed (a table's formulas may read nothing else,
// but a nested map's caption or a picture's proportions follow the file).
func (m *Map) Refresh(ref string) {
	for _, id := range m.Showing(ref) {
		if n := m.Node(id); n != nil {
			m.touchLayout(n)
		}
	}
}

// Live changes node id with fn outside the history, for what follows the pointer or the keys before it is done (a
// drag, typing); the change is then made again as a step with Update or Place, after Live puts the node back.
func (m *Map) Live(id ID, fn func(n *Node)) {
	n := m.Node(id)
	if n == nil {
		return
	}
	m.noteRef(n, false)
	parent, children := n.Parent, n.Children
	fn(n)
	n.ID, n.Parent, n.Children = id, parent, children
	m.noteRef(n, true)
	if n.Parent == 0 {
		m.lay.roots[id] = true
	}
	m.touchLayout(n)
}

// Commits counts the steps made, for telling whether an operation made one.
func (m *Map) Commits() uint64 { return m.hist.commits }

// Discard takes back the last step made and forgets it, so it cannot be redone: for a batch of changes that failed
// part way.
func (m *Map) Discard() {
	h := &m.hist
	if h.open != nil || h.at == 0 || h.at != len(h.steps) {
		return
	}
	m.Undo()
	h.steps = h.steps[:h.at]
}

// SetTasks makes id and every node under it tasks (or not), as one step, as MindNode turns a branch into a task list.
func (m *Map) SetTasks(id ID, on bool) error {
	n := m.Node(id)
	if n == nil {
		return ErrNoNode
	}
	return m.step("Tasks", func() error {
		var err error
		m.eachUnder(n, func(d *Node) {
			if err == nil && d.Kind == Topic && d.Task != on {
				err = m.Update(d.ID, func(x *Node) { x.Task, x.Done = on, on && x.Done })
			}
		})
		return err
	})
}

// ToggleDone ticks task id, or takes the tick off, as one step: a task with tasks under it ticks or unticks them all,
// as it shows them all done or not.
func (m *Map) ToggleDone(id ID) error {
	n := m.Node(id)
	if n == nil {
		return ErrNoNode
	}
	m.Layout()
	done, tasks := n.lay.done, n.lay.tasks
	on := done < tasks
	return m.step("Done", func() error {
		var err error
		m.eachUnder(n, func(d *Node) {
			if err == nil && d.Task && d.Done != on {
				err = m.Update(d.ID, func(x *Node) { x.Done = on })
			}
		})
		return err
	})
}
