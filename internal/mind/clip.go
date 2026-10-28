package mind

import "slices"

// Copying and pasting: the chosen nodes' subtrees, with the connectors between them, are taken out as a map of
// their own, and put into a map with ids of their own.

// Tops are the nodes of ids none of whose ancestors are among them, in id order: what a copy or a move takes, each
// with its subtree.
func (m *Map) Tops(ids []ID) []ID {
	set := map[ID]bool{}
	for _, id := range ids {
		if m.Node(id) != nil {
			set[id] = true
		}
	}
	var out []ID
	for id := range set {
		top := true
		for n := m.Node(id); n.Parent != 0; {
			n = m.nodes[n.Parent]
			if set[n.ID] {
				top = false
				break
			}
		}
		if top {
			out = append(out, id)
		}
	}
	slices.Sort(out)
	return out
}

// Extract is a map holding copies of the subtrees of ids (their tops free, where they are on the board) and the
// connectors whose two ends are both among them.
func (m *Map) Extract(ids []ID) *Map {
	m.Layout()
	c := New(m.Title, m.measure)
	old := map[ID]ID{}
	c.Begin("Copy")
	for _, top := range m.Tops(ids) {
		n := m.Node(top)
		pos := m.Pos(n)
		m.eachUnder(n, func(d *Node) {
			cp := *d.saved()
			parent := old[d.Parent]
			if d == n {
				parent, cp.X, cp.Y = 0, pos.X, pos.Y
				cp.Side = SideAuto
			}
			id, _ := c.Add(cp, parent, -1)
			old[d.ID] = id
		})
	}
	m.EachEdge(func(e *Edge) bool {
		from, okF := old[e.From]
		to, okT := old[e.To]
		if okF && okT {
			cp := *e.saved()
			cp.From, cp.To = from, to
			c.AddEdge(cp)
		}
		return true
	})
	c.Commit()
	c.hist = history{}
	return c
}

// Insert puts copies of src's trees into the map as one step: under parent at index (each after the one before),
// or free with the top-left of them all at at when parent is 0. It returns the ids of the copies of src's roots.
func (m *Map) Insert(src *Map, parent ID, index int, at Vec) ([]ID, error) {
	src.Layout()
	var tops []ID
	err := m.step("Paste", func() error {
		if parent != 0 && m.Node(parent) == nil {
			return ErrNoNode
		}
		b := src.Bounds()
		ids := map[ID]ID{}
		for _, root := range src.Roots() {
			var err error
			src.eachUnder(root, func(d *Node) {
				if err != nil {
					return
				}
				cp := *d.saved()
				p, i := ids[d.Parent], -1
				if d == root {
					if parent != 0 {
						p, i = parent, index
						if index >= 0 {
							index++
						}
					} else {
						p = 0
						cp.X, cp.Y = at.X+d.X-b.X0, at.Y+d.Y-b.Y0
					}
				}
				var id ID
				id, err = m.Add(cp, p, i)
				ids[d.ID] = id
				if d == root {
					tops = append(tops, id)
				}
			})
			if err != nil {
				return err
			}
		}
		var err error
		src.EachEdge(func(e *Edge) bool {
			cp := *e.saved()
			cp.From, cp.To = ids[e.From], ids[e.To]
			_, err = m.AddEdge(cp)
			return err == nil
		})
		return err
	})
	return tops, err
}
