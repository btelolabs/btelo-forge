// Package mind is the mind map: an infinite board on which every object is a node — a topic, a flowchart shape, a
// text, a table, a picture, a video, a smart object from a canvas or another mind map — and topics grow into trees
// laid out as mind maps, logic charts, org charts or outlines, while connectors join any two nodes as a flowchart's
// arrows do. Where a tree's nodes lie is worked out from the tree, never stored; only free nodes have a place.
//
// The board is built to hold hundreds of thousands of nodes: a node's place in a tree is kept relative to its
// parent, with the box around its whole subtree, so the trees themselves are the index that finds what is in view
// (see layout.go), and the free nodes are found through a grid (index.go). Pictures, videos, smart objects and nested
// maps are only referred to, by file or link id, so a saved map stays small (file.go).
package mind

import (
	"errors"
	"image/color"
	"slices"
	"strings"
)

// Ext is the extension of a saved mind map.
const Ext = ".btmind"

// ID names a node within its map; 0 is no node.
type ID uint32

// EdgeID names a connector within its map; 0 is no connector.
type EdgeID uint32

// Kind is what a node shows.
type Kind uint8

const (
	Topic   Kind = iota // words in a shape: a mind map's topic, a flowchart's box, or free text with no shape
	Table               // a grid of cells, with sums and other formulas as in a spreadsheet
	Picture             // a picture file, still or animated
	Video               // a video file, shown by a frame and played in place
	Smart               // a canvas's smart object, by its link id: it follows every change made to it
	Nested              // another mind map file, shown whole
)

// KindNames name the kinds for files and tools, in Kind's order.
var KindNames = []string{"topic", "table", "picture", "video", "smart", "map"}

// Shape is the outline of a topic.
type Shape uint8

const (
	ShapeAuto          Shape = iota // chosen by the topic's place: a tree's root, branches and leaves look as in XMind
	ShapeRounded                    // a rounded rectangle: a process
	ShapeRect                       // a rectangle
	ShapeCapsule                    // a pill: a flowchart's start or end
	ShapeEllipse                    // an ellipse
	ShapeDiamond                    // a diamond: a decision
	ShapeParallelogram              // input or output
	ShapeHexagon                    // a preparation
	ShapeUnderline                  // words over a line, as a mind map's leaves
	ShapeNone                       // words alone: free text
)

// ShapeNames name the shapes for files and tools, in Shape's order.
var ShapeNames = []string{"auto", "rounded", "rect", "capsule", "ellipse", "diamond", "parallelogram", "hexagon", "underline", "none"}

// Layout is how a tree's root lays out its descendants.
type Layout uint8

const (
	LayoutMap   Layout = iota // children on both sides of the root, balanced: a mind map
	LayoutRight               // all to the right: a logic chart
	LayoutLeft                // all to the left
	LayoutDown                // below, side by side: an org chart
	LayoutUp                  // above
	LayoutList                // below and indented: an outline, as a file tree
)

// LayoutNames name the layouts for files and tools, in Layout's order.
var LayoutNames = []string{"map", "right", "left", "down", "up", "list"}

// Route is the path a connector takes.
type Route uint8

const (
	RouteCurve    Route = iota // a smooth curve leaving and entering the sides at right angles
	RouteElbow                 // horizontal and vertical runs, as a flowchart's
	RouteStraight              // a straight line
)

// RouteNames name the routes, in Route's order.
var RouteNames = []string{"curve", "elbow", "straight"}

// Arrow is where a connector has arrowheads.
type Arrow uint8

const (
	ArrowEnd   Arrow = iota // at the node it goes to
	ArrowNone               // none
	ArrowBoth               // at both ends
	ArrowStart              // at the node it comes from
)

// ArrowNames name the arrowheads, in Arrow's order.
var ArrowNames = []string{"end", "none", "both", "start"}

// Side is a side of a node a connector meets.
type Side int8

const (
	SideAuto Side = iota // the side facing the other end
	SideTop
	SideRight
	SideBottom
	SideLeft
)

// SideNames name the sides, in Side's order.
var SideNames = []string{"auto", "top", "right", "bottom", "left"}

// Node is one object on the board. Its exported fields are what is saved; a tree's node has no place of its own,
// its parent's layout gives it one.
type Node struct {
	ID       ID
	Kind     Kind
	Parent   ID   // 0 for a free node, which may be the root of a tree
	Children []ID // in order: for a mind map's root, those on the right and those on the left are in one list
	X, Y     float64
	W, H     float64 // a size the user chose; 0 is the size the content needs (a picture's width is then a default)
	Text     string  // a topic's words, or a caption under anything else
	Shape    Shape
	Fill     color.RGBA // A == 0 is the theme's colour
	Line     color.RGBA
	Ink      color.RGBA
	Size     float32 // the words' size; 0 is the theme's for the node's place
	Bold     bool
	Layout   Layout // a root's
	Folded   bool   // its children are hidden
	Side     Side   // a mind map root's child: SideRight or SideLeft, or SideAuto to balance the two sides
	Ref      string // a picture's, video's or nested map's file, or a smart object's link id
	Aspect   float64
	Cells    *Grid // a table's
	Task     bool  // a task: a box to tick, or, with tasks under it, how much of them is done
	Done     bool  // a task ticked off

	lay place // worked out from the rest by the layout
}

// Grid is a table's cells, row after row.
type Grid struct {
	Rows, Cols int
	Cells      []string
	ColW       []float64 // each column's width; 0 fits its content
}

// Cell is the cell at row r, column c, "" outside the grid.
func (g *Grid) Cell(r, c int) string {
	if g == nil || r < 0 || c < 0 || r >= g.Rows || c >= g.Cols {
		return ""
	}
	return g.Cells[r*g.Cols+c]
}

// clone is a copy sharing nothing.
func (g *Grid) clone() *Grid {
	if g == nil {
		return nil
	}
	return &Grid{Rows: g.Rows, Cols: g.Cols, Cells: slices.Clone(g.Cells), ColW: slices.Clone(g.ColW)}
}

// Edge is a connector from one node to another.
type Edge struct {
	ID       EdgeID
	From, To ID
	FromSide Side
	ToSide   Side
	Route    Route
	Arrow    Arrow
	Dashed   bool
	Color    color.RGBA // A == 0 is the theme's
	Label    string
	Bend     Vec // how far the user moved the path's middle from where it would be: the route they shaped

	geo edgeGeo // worked out from the nodes' places
}

// Errors the map's operations return.
var (
	ErrNoNode    = errors.New("no such node")
	ErrNoEdge    = errors.New("no such connector")
	ErrCycle     = errors.New("a node cannot go under itself or one of its own descendants")
	ErrSameNode  = errors.New("a connector needs two different nodes")
	ErrNotATable = errors.New("the node is not a table")
)

// Map is a board with its nodes and connectors, its undo history and what its layout worked out. It is used from one
// goroutine; Freeze-free reads from others are safe only while it is not changed (the view's tiles are drawn so).
type Map struct {
	Title   string
	ThemeID string  // the theme it is drawn in; "" is the default
	nodes   []*Node // by id; nil where a node was deleted
	edges   []*Edge // by id
	count   int     // nodes
	links   int     // connectors

	measure  Measurer
	hist     history
	lay      layoutState
	grid     rootGrid
	edgeGrid spatial
	refs     map[string]map[ID]bool // the nodes that show each referred file or link id
	version  uint64                 // counts every change, for what is worked out from the map
}

// New is an empty map whose words are measured by measure.
func New(title string, measure Measurer) *Map {
	m := &Map{Title: title, measure: measure, nodes: []*Node{nil}, edges: []*Edge{nil}, refs: map[string]map[ID]bool{}}
	m.grid.init()
	m.edgeGrid.init()
	m.lay.init()
	return m
}

// Version changes whenever the map does.
func (m *Map) Version() uint64 { return m.version }

// Len is how many nodes the map has.
func (m *Map) Len() int { return m.count }

// Edges is how many connectors the map has.
func (m *Map) EdgeCount() int { return m.links }

// Node is the node with id, or nil.
func (m *Map) Node(id ID) *Node {
	if int(id) < len(m.nodes) {
		return m.nodes[id]
	}
	return nil
}

// Edge is the connector with id, or nil.
func (m *Map) Edge(id EdgeID) *Edge {
	if int(id) < len(m.edges) {
		return m.edges[id]
	}
	return nil
}

// Each calls fn for every node, in id order, until it returns false.
func (m *Map) Each(fn func(*Node) bool) {
	for _, n := range m.nodes[1:] {
		if n != nil && !fn(n) {
			return
		}
	}
}

// EachEdge calls fn for every connector, in id order, until it returns false.
func (m *Map) EachEdge(fn func(*Edge) bool) {
	for _, e := range m.edges[1:] {
		if e != nil && !fn(e) {
			return
		}
	}
}

// Roots lists the free nodes, in id order.
func (m *Map) Roots() []*Node {
	var out []*Node
	m.Each(func(n *Node) bool {
		if n.Parent == 0 {
			out = append(out, n)
		}
		return true
	})
	return out
}

// RootOf is the root of the tree n is in.
func (m *Map) RootOf(n *Node) *Node {
	for n.Parent != 0 {
		n = m.nodes[n.Parent]
	}
	return n
}

// Depth is how far n is below its root.
func (m *Map) Depth(n *Node) int {
	d := 0
	for n.Parent != 0 {
		n, d = m.nodes[n.Parent], d+1
	}
	return d
}

// Under reports whether n is a or lies under it.
func (m *Map) Under(n, a *Node) bool {
	for {
		if n == a {
			return true
		}
		if n.Parent == 0 {
			return false
		}
		n = m.nodes[n.Parent]
	}
}

// EdgesOf lists the connectors that meet n.
func (m *Map) EdgesOf(id ID) []*Edge {
	var out []*Edge
	m.EachEdge(func(e *Edge) bool {
		if e.From == id || e.To == id {
			out = append(out, e)
		}
		return true
	})
	return out
}

// Showing lists the nodes that show the file or link id ref.
func (m *Map) Showing(ref string) []ID {
	var out []ID
	for id := range m.refs[refKey(ref)] {
		out = append(out, id)
	}
	slices.Sort(out)
	return out
}

// Refs lists every file and link id the map's nodes show, of the kinds given (all when none are).
func (m *Map) Refs(kinds ...Kind) []string {
	seen := map[string]bool{}
	var out []string
	m.Each(func(n *Node) bool {
		if n.Ref != "" && (len(kinds) == 0 || slices.Contains(kinds, n.Kind)) && !seen[n.Ref] {
			seen[n.Ref] = true
			out = append(out, n.Ref)
		}
		return true
	})
	slices.Sort(out)
	return out
}

// refKey is how a reference is compared: file names without regard to letter case, as Windows does.
func refKey(ref string) string { return strings.ToLower(ref) }

func (m *Map) noteRef(n *Node, on bool) {
	if n.Ref == "" {
		return
	}
	k := refKey(n.Ref)
	if on {
		if m.refs[k] == nil {
			m.refs[k] = map[ID]bool{}
		}
		m.refs[k][n.ID] = true
		return
	}
	delete(m.refs[k], n.ID)
	if len(m.refs[k]) == 0 {
		delete(m.refs, k)
	}
}

// saved is a copy of n's saved fields, sharing nothing with it.
func (n *Node) saved() *Node {
	c := *n
	c.Children = slices.Clone(n.Children)
	c.Cells = n.Cells.clone()
	c.lay = place{}
	return &c
}

// savedEdge is a copy of e's saved fields.
func (e *Edge) saved() *Edge {
	c := *e
	c.geo = edgeGeo{}
	return &c
}

// Clone is a copy of the map with its nodes and connectors but no history, measured as m is.
func (m *Map) Clone() *Map {
	c := New(m.Title, m.measure)
	c.ThemeID = m.ThemeID
	for _, n := range m.nodes[1:] {
		if n == nil {
			c.nodes = append(c.nodes, nil)
			continue
		}
		c.nodes = append(c.nodes, n.saved())
	}
	for _, e := range m.edges[1:] {
		if e == nil {
			c.edges = append(c.edges, nil)
			continue
		}
		c.edges = append(c.edges, e.saved())
	}
	c.reindex()
	return c
}

// reindex works out everything kept beside the nodes after they were all replaced: the counts, the references and
// the layout of every tree.
func (m *Map) reindex() {
	m.count, m.links = 0, 0
	m.refs = map[string]map[ID]bool{}
	m.grid.init()
	m.edgeGrid.init()
	m.lay.init()
	for _, n := range m.nodes[1:] {
		if n != nil {
			m.count++
			m.noteRef(n, true)
			m.lay.dirtyNode(n.ID)
		}
	}
	for _, e := range m.edges[1:] {
		if e != nil {
			m.links++
		}
	}
	m.lay.edgesStale, m.lay.all = true, true
	m.version++
}

// Theme is the theme the map is drawn in.
func (m *Map) Theme() *Theme { return ThemeByID(m.ThemeID) }

// SetTheme draws the map in the theme named id, as a step; every node is measured again, as a theme changes their
// looks.
func (m *Map) SetTheme(id string) {
	if ThemeByID(id).ID != id || id == m.Theme().ID {
		return
	}
	m.Begin("Theme")
	s := m.hist.open
	if !s.themed {
		s.theme[0] = m.ThemeID
	}
	s.theme[1], s.themed = id, true
	m.applyTheme(id)
	m.Commit()
}

// applyTheme switches the theme and lays everything out again.
func (m *Map) applyTheme(id string) {
	m.ThemeID = id
	m.lay.all, m.lay.edgesStale = true, true
	m.lay.everything, m.lay.regions = true, nil
	m.Each(func(n *Node) bool {
		n.lay.measured = false
		return true
	})
	m.version++
}
