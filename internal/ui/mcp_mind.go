package ui

import (
	"bytes"
	"errors"
	"fmt"
	"image/color"
	"image/png"
	"math"
	"path/filepath"
	"slices"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/btelolabs/btelo-forge/internal/gfx"
	"github.com/btelolabs/btelo-forge/internal/mind"
	"github.com/btelolabs/btelo-forge/internal/paint"
)

// The mind map tools: they act on a map open in a tab — the active one, or the one whose file is named — as the
// window does, each call one step of its history. Building is done in batches: mind_add takes whole trees (or a
// Markdown outline), mind_update and mind_connect many nodes and connectors at once.

var errNoMind = errors.New("no mind map is open; call mind_new or mind_open first, or name one with path")

// mindTarget is the map named by path, opened in a tab when it is not open yet, or the active tab's map.
func (a *App) mindTarget(path string) (*mindDoc, error) {
	if path == "" {
		if a.board != nil {
			return a.board, nil
		}
		return nil, errNoMind
	}
	for i, d := range a.docs {
		if d.board != nil && mind.NestKey(d.board.path) == mind.NestKey(path) {
			a.switchTab(i)
			return d.board, nil
		}
	}
	m, err := readMindFile(path, a.mindMeasure())
	if err != nil {
		return nil, err
	}
	return a.openMindDoc(m, path), nil
}

// mindNow is the target map with what is being typed in it kept first, as a menu command keeps it.
func (a *App) mindNow(path string) (*mindDoc, error) {
	md, err := a.mindTarget(path)
	if err != nil {
		return nil, err
	}
	if md.act != nil {
		return nil, errors.New("the map is being dragged on with the mouse; try again in a moment")
	}
	a.commitMindText(md)
	a.commitMindCell(md)
	md.m.Layout()
	return md, nil
}

// mindNodeView is a node for the tools.
type mindNodeView struct {
	ID       mind.ID    `json:"id"`
	Kind     string     `json:"kind"`
	Text     string     `json:"text,omitempty"`
	Parent   mind.ID    `json:"parent,omitempty"`
	Children []mind.ID  `json:"children,omitempty"`
	Box      []float64  `json:"box"` // x, y, width, height on the board
	Shape    string     `json:"shape,omitempty"`
	Layout   string     `json:"layout,omitempty"`
	Folded   bool       `json:"folded,omitempty"`
	Side     string     `json:"side,omitempty"`
	Ref      string     `json:"ref,omitempty"`
	Cells    [][]string `json:"cells,omitempty"`  // a table's cells as typed
	Values   [][]string `json:"values,omitempty"` // and as shown, formulas worked out
	Fill     string     `json:"fill,omitempty"`
	Line     string     `json:"line,omitempty"`
	Ink      string     `json:"ink,omitempty"`
	Size     float32    `json:"size,omitempty"`
	Bold     bool       `json:"bold,omitempty"`
	Width    float64    `json:"width,omitempty"`
	Height   float64    `json:"height,omitempty"`
	Task     bool       `json:"task,omitempty"`
	Done     bool       `json:"done,omitempty"`
	Progress []int      `json:"progress,omitempty"` // a task's: how many of the tasks at the ends of its subtree are done, of how many
}

type mindEdgeView struct {
	ID       mind.EdgeID `json:"id"`
	From     mind.ID     `json:"from"`
	To       mind.ID     `json:"to"`
	Label    string      `json:"label,omitempty"`
	Route    string      `json:"route"`
	Arrow    string      `json:"arrow"`
	Dashed   bool        `json:"dashed,omitempty"`
	Color    string      `json:"color,omitempty"`
	FromSide string      `json:"from_side,omitempty"`
	ToSide   string      `json:"to_side,omitempty"`
	Bend     []float64   `json:"bend,omitempty"`
}

func rgbaHex(c color.RGBA) string {
	if c.A == 0 {
		return ""
	}
	return hexOf(color.NRGBA(c))
}

func (a *App) mindNodeOut(m *mind.Map, n *mind.Node) mindNodeView {
	b := m.Box(n)
	v := mindNodeView{ID: n.ID, Kind: mind.KindNames[n.Kind], Text: n.Text, Parent: n.Parent, Children: n.Children,
		Box: []float64{b.X0, b.Y0, b.W(), b.H()}, Folded: n.Folded, Ref: n.Ref, Fill: rgbaHex(n.Fill), Line: rgbaHex(n.Line),
		Ink: rgbaHex(n.Ink), Size: n.Size, Bold: n.Bold, Width: n.W, Height: n.H, Task: n.Task, Done: n.Done}
	if n.Task {
		done, tasks := m.Progress(n)
		v.Progress = []int{done, tasks}
	}
	if n.Kind == mind.Topic {
		v.Shape = mind.ShapeNames[n.Shape]
	}
	if n.Parent == 0 {
		v.Layout = mind.LayoutNames[n.Layout]
	}
	if n.Side != mind.SideAuto {
		v.Side = mind.SideNames[n.Side]
	}
	if g := n.Cells; n.Kind == mind.Table && g != nil {
		for r := range g.Rows {
			v.Cells = append(v.Cells, slices.Clone(g.Cells[r*g.Cols:(r+1)*g.Cols]))
			row := make([]string, g.Cols)
			for c := range g.Cols {
				row[c] = mind.CellValue(g, r, c)
			}
			v.Values = append(v.Values, row)
		}
	}
	return v
}

func mindEdgeOut(e *mind.Edge) mindEdgeView {
	v := mindEdgeView{ID: e.ID, From: e.From, To: e.To, Label: e.Label, Route: mind.RouteNames[e.Route], Arrow: mind.ArrowNames[e.Arrow],
		Dashed: e.Dashed, Color: rgbaHex(e.Color)}
	if e.FromSide != mind.SideAuto {
		v.FromSide = mind.SideNames[e.FromSide]
	}
	if e.ToSide != mind.SideAuto {
		v.ToSide = mind.SideNames[e.ToSide]
	}
	if e.Bend != (mind.Vec{}) {
		v.Bend = []float64{e.Bend.X, e.Bend.Y}
	}
	return v
}

// mindStateView is what mind_state answers.
type mindStateView struct {
	Path       string         `json:"path"`
	Title      string         `json:"title"`
	Theme      string         `json:"theme"`
	Nodes      int            `json:"node_count"`
	Connectors int            `json:"connector_count"`
	Bounds     []float64      `json:"bounds,omitempty"`
	Chosen     []mind.ID      `json:"chosen,omitempty"`
	List       []mindNodeView `json:"nodes"`
	More       bool           `json:"more,omitempty"` // the list stopped at the limit: ask again with offset
	Edges      []mindEdgeView `json:"connectors,omitempty"`
	CanUndo    bool           `json:"can_undo"`
	CanRedo    bool           `json:"can_redo"`
}

// mindIndex is a lookup of names given to nodes being added in one call, to their ids.
type mindIndex map[string]mind.ID

// mindNodeIn is a node as the tools give it: every field optional, a change leaving out what it does not name.
type mindNodeIn struct {
	ID       mind.ID    `json:"id,omitempty" jsonschema:"for mind_update: the node to change"`
	Key      string     `json:"key,omitempty" jsonschema:"for mind_add: a name for the new node, by which later nodes and connectors in the same call refer to it"`
	Parent   *mind.ID   `json:"parent,omitempty" jsonschema:"for mind_add: the id of the node to add it under (0 or left out: free on the board)"`
	ParentOf string     `json:"parent_key,omitempty" jsonschema:"for mind_add: the key of a node added earlier in the same call to add it under"`
	Index    *int       `json:"index,omitempty" jsonschema:"for mind_add: its place among the parent's children (left out: last)"`
	Kind     string     `json:"kind,omitempty" jsonschema:"topic (words in a shape; the default), table, picture (a picture, GIF, WebP or .btcanvas file), video (a video file), smart (a canvas smart object, ref is its link id) or map (another .btmind file shown inside)"`
	Text     *string    `json:"text,omitempty" jsonschema:"a topic's words, or a caption under anything else"`
	Shape    string     `json:"shape,omitempty" jsonschema:"auto (by the topic's place in its tree), rounded, rect, capsule, ellipse, diamond, parallelogram, hexagon, underline or none (free text)"`
	Layout   string     `json:"layout,omitempty" jsonschema:"a free node's tree layout: map (both sides), right, left, down (org chart), up or list (outline)"`
	X        *float64   `json:"x,omitempty" jsonschema:"a free node's left on the board"`
	Y        *float64   `json:"y,omitempty" jsonschema:"a free node's top on the board"`
	Width    *float64   `json:"width,omitempty" jsonschema:"a width to keep (words wrap in it), 0 to fit the content"`
	Height   *float64   `json:"height,omitempty" jsonschema:"a height to keep, 0 to fit the content"`
	Ref      *string    `json:"ref,omitempty" jsonschema:"the file a picture, video or map shows (an absolute path), or a smart object's link id"`
	Cells    [][]string `json:"cells,omitempty" jsonschema:"a table's cells, row by row; a cell starting with = is a formula, as =SUM(B2:B5) or =A1*2"`
	Fill     *string    `json:"fill,omitempty" jsonschema:"#rrggbb, or empty for the theme's"`
	Line     *string    `json:"line,omitempty" jsonschema:"#rrggbb outline or branch colour, or empty for the theme's"`
	Ink      *string    `json:"ink,omitempty" jsonschema:"#rrggbb words' colour, or empty for the theme's"`
	Size     *float32   `json:"size,omitempty" jsonschema:"the words' size in board units, 0 for the theme's"`
	Bold     *bool      `json:"bold,omitempty"`
	Folded   *bool      `json:"folded,omitempty"`
	Task     *bool      `json:"task,omitempty" jsonschema:"a task: a box to tick, or with tasks under it a ring of their progress; a topic added under a task is one unless this says false"`
	Done     *bool      `json:"done,omitempty" jsonschema:"a task ticked off"`
	TaskList *bool      `json:"task_list,omitempty" jsonschema:"for mind_update: make the node and everything under it tasks (true) or not (false), as MindNode's Convert to Task List"`
	Side     string     `json:"side,omitempty" jsonschema:"a mind map root's child: left, right or auto"`
}

// mindColor reads a colour given to a tool: empty is the theme's.
func mindColor(s string) (color.RGBA, error) {
	if s == "" {
		return color.RGBA{}, nil
	}
	c, err := nrgba(s)
	return color.RGBA{c.R, c.G, c.B, c.A}, err
}

func mindName(names []string, s, what string) (int, error) {
	if i := slices.Index(names, strings.ToLower(strings.TrimSpace(s))); i >= 0 {
		return i, nil
	}
	return 0, fmt.Errorf("%s %q is not one of %s", what, s, strings.Join(names, ", "))
}

// apply sets the fields in gives on n.
func (in mindNodeIn) apply(n *mind.Node) error {
	if in.Kind != "" {
		k, err := mindName(mind.KindNames, in.Kind, "kind")
		if err != nil {
			return err
		}
		n.Kind = mind.Kind(k)
	}
	if in.Text != nil {
		n.Text = *in.Text
	}
	if in.Shape != "" {
		s, err := mindName(mind.ShapeNames, in.Shape, "shape")
		if err != nil {
			return err
		}
		n.Shape = mind.Shape(s)
	}
	if in.Layout != "" {
		l, err := mindName(mind.LayoutNames, in.Layout, "layout")
		if err != nil {
			return err
		}
		n.Layout = mind.Layout(l)
	}
	if in.Side != "" {
		s, err := mindName(mind.SideNames, in.Side, "side")
		if err != nil {
			return err
		}
		n.Side = mind.Side(s)
	}
	if in.X != nil {
		n.X = *in.X
	}
	if in.Y != nil {
		n.Y = *in.Y
	}
	if in.Width != nil {
		n.W = math.Max(*in.Width, 0)
	}
	if in.Height != nil {
		n.H = math.Max(*in.Height, 0)
	}
	if in.Ref != nil {
		n.Ref = *in.Ref
		if n.Kind != mind.Smart && n.Ref != "" && !filepath.IsAbs(n.Ref) {
			return fmt.Errorf("ref %q is not an absolute path", n.Ref)
		}
		if n.Kind == mind.Smart {
			id, ok := paint.NormalLinkID(strings.TrimPrefix(strings.TrimSpace(n.Ref), "#"))
			if !ok {
				return fmt.Errorf("ref %q is not a smart object's link id", n.Ref)
			}
			n.Ref = id
		}
	}
	if in.Cells != nil {
		g := &mind.Grid{Rows: len(in.Cells)}
		for _, row := range in.Cells {
			g.Cols = max(g.Cols, len(row))
		}
		g.Cells = make([]string, g.Rows*g.Cols)
		g.ColW = make([]float64, g.Cols)
		for r, row := range in.Cells {
			copy(g.Cells[r*g.Cols:], row)
		}
		n.Cells = g
	}
	for _, c := range []struct {
		s   *string
		dst *color.RGBA
	}{{in.Fill, &n.Fill}, {in.Line, &n.Line}, {in.Ink, &n.Ink}} {
		if c.s != nil {
			col, err := mindColor(*c.s)
			if err != nil {
				return err
			}
			*c.dst = col
		}
	}
	if in.Size != nil {
		n.Size = max(*in.Size, 0)
	}
	if in.Bold != nil {
		n.Bold = *in.Bold
	}
	if in.Folded != nil {
		n.Folded = *in.Folded
	}
	if in.Task != nil {
		n.Task = *in.Task
	}
	if in.Done != nil {
		n.Done = *in.Done
	}
	return nil
}

// mindEdgeIn is a connector as the tools give it.
type mindEdgeIn struct {
	ID       mind.EdgeID `json:"id,omitempty" jsonschema:"for mind_update_connectors: the connector to change"`
	From     mind.ID     `json:"from,omitempty" jsonschema:"the node it comes from"`
	To       mind.ID     `json:"to,omitempty" jsonschema:"the node it goes to"`
	FromKey  string      `json:"from_key,omitempty" jsonschema:"in mind_add: the key of a node added in the same call it comes from"`
	ToKey    string      `json:"to_key,omitempty" jsonschema:"in mind_add: the key of a node added in the same call it goes to"`
	Label    *string     `json:"label,omitempty"`
	Route    string      `json:"route,omitempty" jsonschema:"curve (default), elbow (right angles, as a flowchart's) or straight"`
	Arrow    string      `json:"arrow,omitempty" jsonschema:"end (default), none, both or start"`
	Dashed   *bool       `json:"dashed,omitempty"`
	Color    *string     `json:"color,omitempty" jsonschema:"#rrggbb, or empty for grey"`
	FromSide string      `json:"from_side,omitempty" jsonschema:"auto (default), top, right, bottom or left"`
	ToSide   string      `json:"to_side,omitempty"`
	Bend     []float64   `json:"bend,omitempty" jsonschema:"[dx, dy]: move the path's middle by this much from where it would be, shaping its route as a drag of its middle does; [0, 0] lets it find its own way round the nodes again"`
}

func (in mindEdgeIn) apply(e *mind.Edge, keys mindIndex) error {
	if in.FromKey != "" {
		id, ok := keys[in.FromKey]
		if !ok {
			return fmt.Errorf("no node with key %q in this call", in.FromKey)
		}
		e.From = id
	} else if in.From != 0 {
		e.From = in.From
	}
	if in.ToKey != "" {
		id, ok := keys[in.ToKey]
		if !ok {
			return fmt.Errorf("no node with key %q in this call", in.ToKey)
		}
		e.To = id
	} else if in.To != 0 {
		e.To = in.To
	}
	if in.Label != nil {
		e.Label = *in.Label
	}
	for _, f := range []struct {
		s     string
		names []string
		what  string
		set   func(int)
	}{
		{in.Route, mind.RouteNames, "route", func(i int) { e.Route = mind.Route(i) }},
		{in.Arrow, mind.ArrowNames, "arrow", func(i int) { e.Arrow = mind.Arrow(i) }},
		{in.FromSide, mind.SideNames, "from_side", func(i int) { e.FromSide = mind.Side(i) }},
		{in.ToSide, mind.SideNames, "to_side", func(i int) { e.ToSide = mind.Side(i) }},
	} {
		if f.s == "" {
			continue
		}
		i, err := mindName(f.names, f.s, f.what)
		if err != nil {
			return err
		}
		f.set(i)
	}
	if in.Dashed != nil {
		e.Dashed = *in.Dashed
	}
	switch len(in.Bend) {
	case 0:
	case 2:
		e.Bend = mind.Vec{X: in.Bend[0], Y: in.Bend[1]}
	default:
		return errors.New("bend is [dx, dy]")
	}
	if in.Color != nil {
		c, err := mindColor(*in.Color)
		if err != nil {
			return err
		}
		e.Color = c
	}
	return nil
}

// mindStep runs fn as one step of md's history; when it fails, what it did is taken back and forgotten.
func (a *App) mindStep(md *mindDoc, label string, fn func() error) error {
	m := md.m
	before := m.Commits()
	m.Begin(label)
	err := fn()
	m.Commit()
	if err != nil && m.Commits() != before {
		m.Discard()
	}
	md.pruneSelection()
	a.requestFrame()
	return err
}

func (a *App) addMindTools(s *mcp.Server) {
	type pathIn struct {
		Path string `json:"path,omitempty" jsonschema:"a .btmind file to act on, opened in a tab if it is not open; left out: the map in the active tab"`
	}
	type newIn struct {
		Title string `json:"title,omitempty" jsonschema:"the map's name, and its file's (in the default folder); left out: a dated name"`
		Topic string `json:"topic,omitempty" jsonschema:"the central topic's words; left out: Central topic"`
	}
	tool(s, a, "mind_new", "Make a new mind map in a tab of its own, with one central topic, saved at once as a .btmind file. Returns its path and state.", func(in newIn) (any, error) {
		if err := a.newMindMap(); err != nil {
			return nil, err
		}
		md := a.board
		if in.Title != "" {
			if err := a.renameMindFile(md, safeFileName(in.Title)); err != nil {
				return nil, err
			}
		}
		if in.Topic != "" {
			md.m.Update(md.m.Roots()[0].ID, func(n *mind.Node) { n.Text = in.Topic })
		}
		return a.mindState(md, 0, 1, 0, 200, true), nil
	})
	type openIn struct {
		Path string `json:"path" jsonschema:"the .btmind file"`
	}
	tool(s, a, "mind_open", "Open a saved mind map (.btmind) in a tab, or show the tab that has it.", func(in openIn) (any, error) {
		md, err := a.mindTarget(in.Path)
		if err != nil {
			return nil, err
		}
		md.m.Layout()
		return a.mindState(md, 0, 1, 0, 200, true), nil
	})
	type stateIn struct {
		Path   string  `json:"path,omitempty"`
		Root   mind.ID `json:"root,omitempty" jsonschema:"list only this node's subtree; left out: the whole board"`
		Depth  int     `json:"depth,omitempty" jsonschema:"how many levels below the root (or below the free nodes) to list; 0: all"`
		Offset int     `json:"offset,omitempty" jsonschema:"skip this many nodes, to page through a large map"`
		Limit  int     `json:"limit,omitempty" jsonschema:"list at most this many nodes (default 500)"`
		Edges  *bool   `json:"connectors,omitempty" jsonschema:"list the connectors too (default true)"`
	}
	tool(s, a, "mind_state", "The map: its file, counts, the nodes (ids, kinds, words, parent and children, place on the board, shapes, tables' cells) tree by tree, and the connectors. Large maps are paged with offset and limit, or narrowed to a subtree with root and depth.",
		func(in stateIn) (any, error) {
			md, err := a.mindNow(in.Path)
			if err != nil {
				return nil, err
			}
			if in.Root != 0 && md.m.Node(in.Root) == nil {
				return nil, mind.ErrNoNode
			}
			edges := in.Edges == nil || *in.Edges
			limit := in.Limit
			if limit <= 0 {
				limit = 500
			}
			return a.mindState(md, in.Root, in.Depth, in.Offset, limit, edges), nil
		})
	type addIn struct {
		Path       string       `json:"path,omitempty"`
		Nodes      []mindNodeIn `json:"nodes,omitempty" jsonschema:"nodes to add, in order: a tree is a node with a key followed by nodes whose parent_key names it; free ones take x and y, or are placed where there is room"`
		Outline    string       `json:"outline,omitempty" jsonschema:"a Markdown outline (# headings, - bullets, indented) to add as topics, under parent or free"`
		Parent     mind.ID      `json:"parent,omitempty" jsonschema:"for outline: the node to add it under"`
		Connectors []mindEdgeIn `json:"connectors,omitempty" jsonschema:"connectors to add between nodes, by id or by the keys given to nodes in this call"`
	}
	tool(s, a, "mind_add", "Add nodes — whole trees at once, children naming their parent by key — and an outline, and connectors between them, as one step. Kinds: topic, table, picture, video, smart (a canvas's smart object, which follows its changes), map (another map shown inside). Returns the new ids by key.",
		func(in addIn) (any, error) {
			md, err := a.mindNow(in.Path)
			if err != nil {
				return nil, err
			}
			keys := mindIndex{}
			var added []mind.ID
			err = a.mindStep(md, "Add", func() error {
				add := func(list []mindNodeIn) error {
					for _, ni := range list {
						p := mind.ID(0)
						if ni.ParentOf != "" {
							id, ok := keys[ni.ParentOf]
							if !ok {
								return fmt.Errorf("no node with key %q added before it", ni.ParentOf)
							}
							p = id
						} else if ni.Parent != nil {
							p = *ni.Parent
						}
						var n mind.Node
						if err := ni.apply(&n); err != nil {
							return err
						}
						if n.Kind == mind.Table && n.Cells == nil {
							n.Cells = &mind.Grid{Rows: 2, Cols: 2, Cells: make([]string, 4), ColW: make([]float64, 2)}
						}
						if p == 0 && ni.X == nil && ni.Y == nil {
							c := a.mindViewCentre(md)
							n.X, n.Y = c.X, c.Y
						}
						if pn := md.m.Node(p); pn != nil && pn.Task && ni.Task == nil && n.Kind == mind.Topic {
							n.Task = true
						}
						index := -1
						if ni.Index != nil {
							index = *ni.Index
						}
						id, err := md.m.Add(n, p, index)
						if err != nil {
							return fmt.Errorf("adding %q: %w", n.Text, err)
						}
						if ni.Key != "" {
							keys[ni.Key] = id
						}
						added = append(added, id)
						if p == 0 && ni.X == nil && ni.Y == nil {
							a.mindMakeRoom(md, []mind.ID{id})
						}
					}
					return nil
				}
				if err := add(in.Nodes); err != nil {
					return err
				}
				if strings.TrimSpace(in.Outline) != "" {
					c := a.mindViewCentre(md)
					tops, err := md.m.AddOutline(in.Outline, in.Parent, -1, c.X, c.Y)
					if err != nil {
						return err
					}
					if in.Parent == 0 {
						a.mindMakeRoom(md, tops)
					}
					added = append(added, tops...)
				}
				for _, ei := range in.Connectors {
					var e mind.Edge
					if err := ei.apply(&e, keys); err != nil {
						return err
					}
					if _, err := md.m.AddEdge(e); err != nil {
						return fmt.Errorf("connecting %d to %d: %w", e.From, e.To, err)
					}
				}
				return nil
			})
			if err != nil {
				return nil, err
			}
			return map[string]any{"ids": keys, "added": added, "node_count": md.m.Len(), "connector_count": md.m.EdgeCount()}, nil
		})
	type updateIn struct {
		Path  string       `json:"path,omitempty"`
		Nodes []mindNodeIn `json:"nodes" jsonschema:"each with its id and the fields to change; children and parents are changed with mind_move"`
	}
	tool(s, a, "mind_update", "Change nodes — words, shapes, colours, sizes, layouts, folding, tables' cells, what they show — as one step.", func(in updateIn) (any, error) {
		md, err := a.mindNow(in.Path)
		if err != nil {
			return nil, err
		}
		err = a.mindStep(md, "Change", func() error {
			for _, ni := range in.Nodes {
				if md.m.Node(ni.ID) == nil {
					return fmt.Errorf("node %d: %w", ni.ID, mind.ErrNoNode)
				}
				var bad error
				if err := md.m.Update(ni.ID, func(n *mind.Node) { bad = ni.apply(n) }); err != nil {
					return err
				}
				if bad != nil {
					return fmt.Errorf("node %d: %w", ni.ID, bad)
				}
				if ni.TaskList != nil {
					if err := md.m.SetTasks(ni.ID, *ni.TaskList); err != nil {
						return fmt.Errorf("node %d: %w", ni.ID, err)
					}
				}
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
		var out []mindNodeView
		for _, ni := range in.Nodes {
			out = append(out, a.mindNodeOut(md.m, md.m.Node(ni.ID)))
		}
		return out, nil
	})
	type cellIn struct {
		ID    mind.ID `json:"id" jsonschema:"the table"`
		Row   int     `json:"row" jsonschema:"from 0"`
		Col   int     `json:"col" jsonschema:"from 0; the table grows to hold a cell past its end"`
		Value string  `json:"value" jsonschema:"words, a number, or a formula starting with ="`
	}
	type cellsIn struct {
		Path  string   `json:"path,omitempty"`
		Cells []cellIn `json:"cells"`
	}
	tool(s, a, "mind_set_cells", "Set tables' cells (words, numbers or formulas such as =SUM(B2:B9)), growing the tables as needed, as one step; returns the tables as shown.", func(in cellsIn) (any, error) {
		md, err := a.mindNow(in.Path)
		if err != nil {
			return nil, err
		}
		err = a.mindStep(md, "Change", func() error {
			for _, c := range in.Cells {
				if err := md.m.SetCell(c.ID, c.Row, c.Col, c.Value); err != nil {
					return fmt.Errorf("node %d cell %s: %w", c.ID, mind.CellName(c.Row, c.Col), err)
				}
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
		seen := map[mind.ID]bool{}
		var out []mindNodeView
		for _, c := range in.Cells {
			if !seen[c.ID] {
				seen[c.ID] = true
				out = append(out, a.mindNodeOut(md.m, md.m.Node(c.ID)))
			}
		}
		return out, nil
	})
	type deleteIn struct {
		Path       string        `json:"path,omitempty"`
		IDs        []mind.ID     `json:"ids,omitempty" jsonschema:"nodes to delete, each with its subtree and its connectors"`
		Connectors []mind.EdgeID `json:"connectors,omitempty" jsonschema:"connectors to delete"`
	}
	tool(s, a, "mind_delete", "Delete nodes (with their subtrees) and connectors, as one step.", func(in deleteIn) (any, error) {
		md, err := a.mindNow(in.Path)
		if err != nil {
			return nil, err
		}
		err = a.mindStep(md, "Delete", func() error {
			if len(in.Connectors) > 0 {
				if err := md.m.DeleteEdge(in.Connectors...); err != nil {
					return err
				}
			}
			if len(in.IDs) > 0 {
				return md.m.Delete(in.IDs...)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
		return map[string]int{"node_count": md.m.Len(), "connector_count": md.m.EdgeCount()}, nil
	})
	type moveIn struct {
		Path   string   `json:"path,omitempty"`
		ID     mind.ID  `json:"id"`
		Parent mind.ID  `json:"parent" jsonschema:"the node to put it under, or 0 to make it free"`
		Index  *int     `json:"index,omitempty" jsonschema:"its place among the new parent's children, counted without it (left out: last)"`
		X      *float64 `json:"x,omitempty" jsonschema:"when free: its left on the board (left out: where it is)"`
		Y      *float64 `json:"y,omitempty"`
	}
	type movesIn struct {
		Path  string   `json:"path,omitempty"`
		Moves []moveIn `json:"moves"`
	}
	tool(s, a, "mind_move", "Move nodes with their subtrees: under another node (at a place among its children), or free on the board at x, y; or move free nodes. One step.", func(in movesIn) (any, error) {
		md, err := a.mindNow(in.Path)
		if err != nil {
			return nil, err
		}
		err = a.mindStep(md, "Move", func() error {
			for _, mv := range in.Moves {
				n := md.m.Node(mv.ID)
				if n == nil {
					return fmt.Errorf("node %d: %w", mv.ID, mind.ErrNoNode)
				}
				md.m.Layout()
				pos := md.m.Pos(n)
				x, y := pos.X, pos.Y
				if mv.X != nil {
					x = *mv.X
				}
				if mv.Y != nil {
					y = *mv.Y
				}
				index := -1
				if mv.Index != nil {
					index = *mv.Index
				}
				var err error
				if mv.Parent == 0 && n.Parent == 0 {
					err = md.m.Place(mv.ID, x, y)
				} else {
					err = md.m.Move(mv.ID, mv.Parent, index, x, y)
				}
				if err != nil {
					return fmt.Errorf("node %d: %w", mv.ID, err)
				}
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
		var out []mindNodeView
		for _, mv := range in.Moves {
			out = append(out, a.mindNodeOut(md.m, md.m.Node(mv.ID)))
		}
		return out, nil
	})
	type connectIn struct {
		Path       string       `json:"path,omitempty"`
		Connectors []mindEdgeIn `json:"connectors"`
	}
	tool(s, a, "mind_connect", "Connect nodes with connectors (arrows), as in a flowchart: any two nodes, in trees or free; with a label, route, arrows, dashes and colour. One step.", func(in connectIn) (any, error) {
		md, err := a.mindNow(in.Path)
		if err != nil {
			return nil, err
		}
		var out []mindEdgeView
		err = a.mindStep(md, "Connect", func() error {
			for _, ei := range in.Connectors {
				var e mind.Edge
				if err := ei.apply(&e, nil); err != nil {
					return err
				}
				id, err := md.m.AddEdge(e)
				if err != nil {
					return fmt.Errorf("connecting %d to %d: %w", e.From, e.To, err)
				}
				out = append(out, mindEdgeOut(md.m.Edge(id)))
			}
			return nil
		})
		return out, err
	})
	tool(s, a, "mind_update_connectors", "Change connectors — label, route, arrows, dashes, colour, sides, ends — as one step.", func(in connectIn) (any, error) {
		md, err := a.mindNow(in.Path)
		if err != nil {
			return nil, err
		}
		var out []mindEdgeView
		err = a.mindStep(md, "Change", func() error {
			for _, ei := range in.Connectors {
				var bad error
				if err := md.m.UpdateEdge(ei.ID, func(e *mind.Edge) { bad = ei.apply(e, nil) }); err != nil {
					return fmt.Errorf("connector %d: %w", ei.ID, err)
				}
				if bad != nil {
					return fmt.Errorf("connector %d: %w", ei.ID, bad)
				}
				out = append(out, mindEdgeOut(md.m.Edge(ei.ID)))
			}
			return nil
		})
		return out, err
	})
	type findIn struct {
		Path  string `json:"path,omitempty"`
		Query string `json:"query" jsonschema:"words to find in nodes' words, captions, cells and files, ignoring letter case"`
		Limit int    `json:"limit,omitempty" jsonschema:"at most this many (default 100)"`
	}
	tool(s, a, "mind_find", "Find nodes whose words, captions, cells or files hold the query.", func(in findIn) (any, error) {
		md, err := a.mindNow(in.Path)
		if err != nil {
			return nil, err
		}
		q := strings.ToLower(strings.TrimSpace(in.Query))
		if q == "" {
			return nil, errors.New("query is empty")
		}
		limit := in.Limit
		if limit <= 0 {
			limit = 100
		}
		var out []mindNodeView
		md.m.Each(func(n *mind.Node) bool {
			hay := strings.ToLower(n.Text + "\n" + n.Ref)
			if n.Cells != nil {
				hay += "\n" + strings.ToLower(strings.Join(n.Cells.Cells, "\n"))
			}
			if strings.Contains(hay, q) {
				out = append(out, a.mindNodeOut(md.m, n))
			}
			return len(out) < limit
		})
		return out, nil
	})
	type outlineIn struct {
		Path string    `json:"path,omitempty"`
		IDs  []mind.ID `json:"ids,omitempty" jsonschema:"the subtrees to write; left out: every tree on the board"`
	}
	tool(s, a, "mind_outline", "The map's trees, or some subtrees, as a Markdown outline: a quick way to read a large map.", func(in outlineIn) (any, error) {
		md, err := a.mindNow(in.Path)
		if err != nil {
			return nil, err
		}
		ids := in.IDs
		if len(ids) == 0 {
			for _, n := range md.m.Roots() {
				ids = append(ids, n.ID)
			}
		}
		for _, id := range ids {
			if md.m.Node(id) == nil {
				return nil, fmt.Errorf("node %d: %w", id, mind.ErrNoNode)
			}
		}
		return map[string]string{"outline": md.m.Outline(ids...)}, nil
	})
	type renderIn struct {
		Path    string    `json:"path,omitempty"`
		IDs     []mind.ID `json:"ids,omitempty" jsonschema:"show these nodes with their subtrees; left out: the whole board"`
		Rect    []float64 `json:"rect,omitempty" jsonschema:"or a part of the board: [x, y, width, height]"`
		MaxSize int       `json:"max_size,omitempty" jsonschema:"the picture's longest side in pixels (default 1600, at most 4096)"`
	}
	mcp.AddTool(s, &mcp.Tool{Name: "mind_render", Description: "A PNG of the map, or of some nodes or a part of the board, as it looks, with its pictures, to check it."},
		func(ctx contextT, req *mcp.CallToolRequest, in renderIn) (*mcp.CallToolResult, any, error) {
			return imageResult(call(a, func() ([]byte, error) {
				md, err := a.mindNow(in.Path)
				if err != nil {
					return nil, err
				}
				return a.mindRenderPNG(md, in.IDs, in.Rect, in.MaxSize)
			}))
		})
	type showIn struct {
		Path string    `json:"path,omitempty"`
		IDs  []mind.ID `json:"ids,omitempty" jsonschema:"choose these nodes and bring them into view; left out: show the whole board"`
	}
	tool(s, a, "mind_show", "Show the user something in the window: choose nodes and move the view to them, or show the whole board.", func(in showIn) (any, error) {
		md, err := a.mindNow(in.Path)
		if err != nil {
			return nil, err
		}
		for _, id := range in.IDs {
			if md.m.Node(id) == nil {
				return nil, fmt.Errorf("node %d: %w", id, mind.ErrNoNode)
			}
		}
		clear(md.sel)
		for _, id := range in.IDs {
			md.sel[id] = true
			md.primary = id
		}
		if len(in.IDs) == 0 {
			md.fit = true
		} else {
			a.mindZoomToSelection(md, a.layout().body)
		}
		a.requestFrame()
		return map[string]any{"chosen": md.chosen()}, nil
	})
	type themeIn struct {
		Path  string `json:"path,omitempty"`
		Theme string `json:"theme" jsonschema:"midnight (dark, words over coloured lines, the default), paper (white), pastel (light, every topic a soft bubble), ocean (deep blue), sepia (warm parchment) or neon (dark, glowing bubbles)"`
	}
	tool(s, a, "mind_theme", "Draw the map in another theme: the board, its colours and how topics look. One step.", func(in themeIn) (any, error) {
		md, err := a.mindNow(in.Path)
		if err != nil {
			return nil, err
		}
		if mind.ThemeByID(in.Theme).ID != in.Theme {
			var ids []string
			for _, t := range mind.Themes {
				ids = append(ids, t.ID)
			}
			return nil, fmt.Errorf("theme %q is not one of %s", in.Theme, strings.Join(ids, ", "))
		}
		md.m.SetTheme(in.Theme)
		a.requestFrame()
		return map[string]string{"theme": md.m.Theme().ID}, nil
	})
	type historyIn struct {
		Path   string `json:"path,omitempty"`
		Action string `json:"action" jsonschema:"undo or redo"`
	}
	tool(s, a, "mind_history", "Undo or redo the map's last step.", func(in historyIn) (any, error) {
		md, err := a.mindNow(in.Path)
		if err != nil {
			return nil, err
		}
		switch in.Action {
		case "undo":
			if !md.m.Undo() {
				return nil, errors.New("nothing to undo")
			}
		case "redo":
			if !md.m.Redo() {
				return nil, errors.New("nothing to redo")
			}
		default:
			return nil, fmt.Errorf("action %q is not undo or redo", in.Action)
		}
		md.pruneSelection()
		a.requestFrame()
		return map[string]any{"node_count": md.m.Len(), "can_undo": md.m.CanUndo(), "can_redo": md.m.CanRedo()}, nil
	})
	tool(s, a, "mind_save", "Save the map to its .btmind file now (it is also saved by itself a moment after each change).", func(in pathIn) (any, error) {
		md, err := a.mindNow(in.Path)
		if err != nil {
			return nil, err
		}
		md.dirty = true
		if err := a.saveMind(md, true); err != nil {
			return nil, err
		}
		return map[string]string{"path": md.path}, nil
	})
}

// mindState is what mind_state answers: nodes tree by tree, parents first, from offset, at most limit.
func (a *App) mindState(md *mindDoc, root mind.ID, depth, offset, limit int, edges bool) mindStateView {
	m := md.m
	m.Layout()
	v := mindStateView{Path: md.path, Title: m.Title, Theme: m.Theme().ID, Nodes: m.Len(), Connectors: m.EdgeCount(), Chosen: md.chosen(),
		CanUndo: m.CanUndo(), CanRedo: m.CanRedo(), List: []mindNodeView{}}
	if b := m.Bounds(); !b.Empty() {
		v.Bounds = []float64{b.X0, b.Y0, b.W(), b.H()}
	}
	tops := []*mind.Node{}
	if root != 0 {
		tops = append(tops, m.Node(root))
	} else {
		tops = m.Roots()
	}
	seen := 0
	var walk func(n *mind.Node, level int) bool
	walk = func(n *mind.Node, level int) bool {
		if seen >= offset {
			if len(v.List) >= limit {
				v.More = true
				return false
			}
			v.List = append(v.List, a.mindNodeOut(m, n))
		}
		seen++
		if depth > 0 && level >= depth {
			return true
		}
		for _, id := range n.Children {
			if !walk(m.Node(id), level+1) {
				return false
			}
		}
		return true
	}
	for _, t := range tops {
		if !walk(t, 0) {
			break
		}
	}
	if edges {
		m.EachEdge(func(e *mind.Edge) bool {
			v.Edges = append(v.Edges, mindEdgeOut(e))
			return true
		})
	}
	return v
}

// mindRenderPNG draws the map, some nodes' subtrees or a part of the board as a PNG at most max pixels a side, its
// pictures read as it is drawn.
func (a *App) mindRenderPNG(md *mindDoc, ids []mind.ID, rect []float64, max int) ([]byte, error) {
	m := md.m
	m.Layout()
	var b mind.Rect
	switch {
	case len(ids) > 0:
		for i, id := range ids {
			n := m.Node(id)
			if n == nil {
				return nil, fmt.Errorf("node %d: %w", id, mind.ErrNoNode)
			}
			if i == 0 {
				b = m.SubBox(n)
			} else {
				b = b.Union(m.SubBox(n))
			}
		}
	case len(rect) == 4:
		b = mind.R(rect[0], rect[1], rect[2], rect[3])
	case len(rect) != 0:
		return nil, errors.New("rect is [x, y, width, height]")
	default:
		b = m.Bounds()
	}
	if b.Empty() {
		return nil, errors.New("there is nothing on the board there")
	}
	if max <= 0 {
		max = 1600
	}
	max = min(max, 4096)
	b = b.Grow(24)
	z := math.Min(float64(max)/math.Max(b.W(), b.H()), 2)
	w, h := int(math.Ceil(b.W()*z)), int(math.Ceil(b.H()*z))
	c := gfx.NewCanvas(w, h)
	mind.Render(c, m, mind.View{Zoom: z, OX: -b.X0 * z, OY: -b.Y0 * z}, &mind.Options{Fonts: a.u.fonts,
		Pictures: mindExportPictures{a, md.path}, Labels: a.mindLabels(), Paper: true})
	var buf bytes.Buffer
	if err := png.Encode(&buf, c.Img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
