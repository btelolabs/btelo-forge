package ui

import (
	"math"
	"slices"
	"strings"
	"unicode"

	"github.com/btelolabs/btelo-forge/internal/gfx"
	"github.com/btelolabs/btelo-forge/internal/mind"
)

// Working on the board with the pointer, as in XMind and Whimsical together: a click chooses a node (Shift or Ctrl
// adds to the choice), a drag on the board moves the view and Shift+drag draws a marquee, a free node is moved where
// it is dragged, and a tree's node is dragged into another place in a tree — onto a node to go under it, beside
// one to go before or after it — or out onto the board to be free. The dots at a node's sides draw connectors: to
// another node, or to a new one where the pointer is let go. The square at its corner sizes it. A double-click
// types into a topic, a table's cell or a connector, opens a nested map or a smart object, plays a video, and on
// the board adds a topic.

// mindTool is what the board's menu adds.
type mindTool int

const (
	mtTopic mindTool = iota
	mtShape
	mtText
	mtTable
	mtPicture
	mtVideo
	mtNested
	mtMap
)

// What a pointer gesture is doing.
const (
	actPan = iota
	actMarquee
	actMove
	actConnect
	actResize
	actTextSelect
	actBend // a connector's middle dragged, reshaping its route
)

// mindAction is a gesture under way.
type mindAction struct {
	kind   int
	id     mind.ID
	side   mind.Side
	target mind.ID
	from   mind.Vec // the board point pressed
	grab   gfx.Rect // the window point pressed, as a rectangle's corner
	moved  bool
	tree   bool // a tree's node being dragged to another place
	drop   mindDrop
	starts map[mind.ID]mind.Vec // the free nodes' places before the move
	view   mind.View            // the view when the board was pressed
	before map[mind.ID]bool     // the choice before the marquee
	size   [2]float64           // the node's chosen size before resizing (0 for none)
	attach mind.ID              // a free node let go over this node goes under it
	edge   mind.EdgeID          // the connector being reshaped
	bend   mind.Vec             // its bend before
	sides  [2]mind.Side         // and the sides it left and entered by
	shown  [2]mind.Side         // the sides it showed, which the reshaped route keeps
	shownB mind.Vec             // and the bend it showed
}

// mindDrop is where a tree's node being dragged would go: under parent at index (-1 for the end, after the node
// under the pointer), drawn as line; or free.
type mindDrop struct {
	parent mind.ID
	index  int
	line   [2]mind.Vec
	free   bool
}

// mindText is the words being typed into a node: what they were, so the change is one step.
type mindText struct {
	id   mind.ID
	orig string
}

// mindCell is a table's cell being typed into, or a connector's label.
type mindCell struct {
	id       mind.ID
	edge     mind.EdgeID
	row, col int
	value    string
}

// selection helpers.
func (md *mindDoc) selectOnly(id mind.ID) {
	clear(md.sel)
	md.edge = 0
	if id != 0 {
		md.sel[id] = true
	}
	md.primary = id
}

func (md *mindDoc) chosen() []mind.ID {
	out := make([]mind.ID, 0, len(md.sel))
	for id := range md.sel {
		if md.m.Node(id) != nil {
			out = append(out, id)
		}
	}
	slices.Sort(out)
	return out
}

// pruneSelection drops chosen nodes that are gone or hidden, after undo or a delete.
func (md *mindDoc) pruneSelection() {
	for id := range md.sel {
		if n := md.m.Node(id); n == nil || md.m.Hidden(n) {
			delete(md.sel, id)
		}
	}
	if !md.sel[md.primary] {
		md.primary = 0
		for id := range md.sel {
			md.primary = max(md.primary, id)
		}
	}
	if md.m.Edge(md.edge) == nil {
		md.edge = 0
	}
	if md.text != nil && md.m.Node(md.text.id) == nil {
		md.text = nil
	}
}

// board point of the pointer.
func (md *mindDoc) pointer(u *ctx, r gfx.Rect) mind.Vec {
	return md.screenView(r).Board(float64(u.mx), float64(u.my))
}

// mindHover is the node under the pointer, when the board has it.
func (a *App) mindHover(md *mindDoc, r gfx.Rect, v mind.View) *mind.Node {
	u := &a.u
	if !u.hover("mind/board", r) || a.mindChromeRects(md, r).holds(u.mx, u.my) {
		return nil
	}
	n, _ := md.m.At(v.Board(float64(u.mx), float64(u.my)).X, v.Board(float64(u.mx), float64(u.my)).Y)
	return n
}

// mindHandle is a dot at a node's side that draws a connector, or (side 0) the square at its corner that sizes it.
type mindHandle struct {
	r    gfx.Rect
	side mind.Side
}

// mindHandlesFor is the node whose handles show: the one chosen alone, or the one under the pointer.
func (a *App) mindHandlesFor(md *mindDoc) *mind.Node {
	if md.text != nil || md.cell != nil || md.act != nil && md.act.kind != actConnect {
		return nil
	}
	if md.act != nil {
		return md.m.Node(md.act.id)
	}
	if len(md.sel) == 1 {
		if n := md.m.Node(md.primary); n != nil && !md.m.Hidden(n) {
			return n
		}
	}
	return nil
}

// mindHandles are n's handles on the screen. The sides a tree's branches leave or reach have none: there the dot
// would lie on a branch.
func (a *App) mindHandles(md *mindDoc, n *mind.Node, v mind.View) []mindHandle {
	u := &a.u
	m := md.m
	b := v.ScreenRect(m.Box(n))
	d := u.px(13)
	off := u.px(16)
	busy := map[mind.Side]bool{}
	if len(n.Children) > 0 && !n.Folded {
		busy[outSide(m.Dir(n))] = true
		if n.Parent == 0 && n.Layout == mind.LayoutMap {
			busy[mind.SideLeft], busy[mind.SideRight] = true, true
		}
	}
	if n.Parent != 0 {
		dir := m.Dir(n)
		if p := m.Node(n.Parent); p.Parent == 0 && p.Layout == mind.LayoutMap {
			dir = mind.LayoutRight
			if m.Box(n).X1 <= m.Box(p).X0 {
				dir = mind.LayoutLeft
			}
		}
		busy[inSide(dir)] = true
	}
	var out []mindHandle
	for _, h := range []mindHandle{
		{gfx.Rect{X: b.X + b.W/2 - d/2, Y: b.Y - off - d/2, W: d, H: d}, mind.SideTop},
		{gfx.Rect{X: b.Right() + off - d/2, Y: b.Y + b.H/2 - d/2, W: d, H: d}, mind.SideRight},
		{gfx.Rect{X: b.X + b.W/2 - d/2, Y: b.Bottom() + off - d/2, W: d, H: d}, mind.SideBottom},
		{gfx.Rect{X: b.X - off - d/2, Y: b.Y + b.H/2 - d/2, W: d, H: d}, mind.SideLeft},
	} {
		if !busy[h.side] {
			out = append(out, h)
		}
	}
	if n.Kind != mind.Table {
		s := u.px(9)
		out = append(out, mindHandle{gfx.Rect{X: b.Right() + u.px(3) - s/2, Y: b.Bottom() + u.px(3) - s/2, W: s, H: s}, 0})
	}
	return out
}

// outSide is the side of a node its children's branches leave from, growing in direction dir.
func outSide(dir mind.Layout) mind.Side {
	switch dir {
	case mind.LayoutLeft:
		return mind.SideLeft
	case mind.LayoutDown, mind.LayoutList:
		return mind.SideBottom
	case mind.LayoutUp:
		return mind.SideTop
	}
	return mind.SideRight
}

// inSide is the side of a node its parent's branch reaches, the node growing in direction dir.
func inSide(dir mind.Layout) mind.Side {
	switch dir {
	case mind.LayoutLeft:
		return mind.SideRight
	case mind.LayoutDown:
		return mind.SideTop
	case mind.LayoutUp:
		return mind.SideBottom
	}
	return mind.SideLeft
}

// nearestSide is the side of b nearest p.
func nearestSide(b mind.Rect, p mind.Vec) mind.Side {
	best, side := math.Inf(1), mind.SideRight
	for _, s := range []mind.Side{mind.SideTop, mind.SideRight, mind.SideBottom, mind.SideLeft} {
		var q mind.Vec
		switch s {
		case mind.SideTop:
			q = mind.Vec{X: b.CX(), Y: b.Y0}
		case mind.SideRight:
			q = mind.Vec{X: b.X1, Y: b.CY()}
		case mind.SideBottom:
			q = mind.Vec{X: b.CX(), Y: b.Y1}
		default:
			q = mind.Vec{X: b.X0, Y: b.CY()}
		}
		if d := math.Hypot(p.X-q.X, p.Y-q.Y); d < best {
			best, side = d, s
		}
	}
	return side
}

// mindInput handles the keys, the wheel and the pointer on the board r, outside the floating bars ch.
func (a *App) mindInput(md *mindDoc, r gfx.Rect, ch mindChrome) {
	u := &a.u
	md.pruneSelection()
	if md.text != nil {
		a.mindTextKeys(md)
	} else if md.cell == nil && u.focus == "" && !u.blocked {
		a.mindKeys(md, r)
	}
	v := md.screenView(r)
	const id = "mind/board"
	inBoard := u.hover(id, r) && !ch.holds(u.mx, u.my)
	if inBoard && u.wheel != 0 {
		local := gfx.Rect{X: u.mx - float32(math.Round(float64(r.X))), Y: u.my - float32(math.Round(float64(r.Y)))}
		switch {
		case u.shift:
			md.view.OX = math.Round(md.view.OX + float64(u.wheel)*60)
		case u.ctrl:
			md.view.OY = math.Round(md.view.OY + float64(u.wheel)*60)
		default:
			md.zoomAt(local.X, local.Y, md.view.Zoom*math.Pow(1.2, float64(u.wheel)))
		}
		u.wheel = 0
		v = md.screenView(r)
	}
	md.panning = u.isHeld(keySpace) && md.text == nil && md.cell == nil
	if u.rightAt != nil && r.Contains(u.rightAt.X, u.rightAt.Y) && !ch.holds(u.rightAt.X, u.rightAt.Y) && !u.blocked && md.act == nil {
		a.mindContext(md, r, v, u.rightAt.X, u.rightAt.Y)
	}
	if u.pressed && u.active == "" && r.Contains(u.pressX, u.pressY) && !ch.holds(u.pressX, u.pressY) && !u.blocked {
		a.mindPress(md, r, v)
	}
	if u.active == id && md.act != nil {
		if u.down {
			a.mindDrag(md, r, v)
		}
		if u.letGo(id) {
			a.mindRelease(md, r, v)
		}
	} else if u.active != id && md.act != nil {
		md.act = nil // the gesture was taken over (a menu opened)
	}
	a.mindCursor(md, r, v, inBoard)
}

// mindCursor shows what a press would do.
func (a *App) mindCursor(md *mindDoc, r gfx.Rect, v mind.View, inBoard bool) {
	u := &a.u
	switch {
	case md.act != nil && md.act.kind == actPan:
		u.cursor = CursorMove
	case md.act != nil && md.act.kind == actMove && md.act.moved:
		u.cursor = CursorMove
	case md.act != nil && md.act.kind == actResize:
		u.cursor = CursorResizeCorner
	case !inBoard:
	case md.panning:
		u.cursor = CursorMove
	default:
		if n := a.mindHandlesFor(md); n != nil {
			for _, h := range a.mindHandles(md, n, v) {
				if h.r.Inset(-u.px(3)).Contains(u.mx, u.my) {
					u.cursor = CursorHand
					if h.side == 0 {
						u.cursor = CursorResizeCorner
					}
					return
				}
			}
		}
		if md.text != nil {
			if n := md.m.Node(md.text.id); n != nil {
				tr, _, _ := md.m.TextRect(n)
				if v.ScreenRect(tr.Grow(4)).Contains(u.mx, u.my) {
					u.cursor = CursorText
				}
			}
		}
	}
}

// mindPress starts what a press on the board does.
func (a *App) mindPress(md *mindDoc, r gfx.Rect, v mind.View) {
	u := &a.u
	const id = "mind/board"
	p := v.Board(float64(u.pressX), float64(u.pressY))
	m := md.m
	md.themes = false // a press on the board closes the theme panel
	// Typing goes on only when the press is in the words being typed, which places the caret.
	if md.text != nil {
		if n := m.Node(md.text.id); n != nil {
			tr, _, _ := m.TextRect(n)
			if tr.Grow(4).Contains(p.X, p.Y) || m.Box(n).Contains(p.X, p.Y) {
				u.press(id, r)
				md.act = &mindAction{kind: actTextSelect, id: n.ID}
				a.mindCaretTo(md, v, u.pressX, u.pressY, !u.shift)
				if u.double {
					u.edit.anchor, u.edit.caret = wordAround(u.edit.text, u.edit.caret)
				}
				return
			}
		}
		a.commitMindText(md)
	}
	if md.cell != nil {
		a.commitMindCell(md)
	}
	handles := a.mindHandlesFor(md) // before the gesture starts, which hides them
	u.press(id, r)
	act := &mindAction{from: p, grab: gfx.Rect{X: u.pressX, Y: u.pressY}, view: md.view}
	md.act = act
	if md.panning {
		act.kind = actPan
		return
	}
	if e := m.Edge(md.edge); e != nil {
		if hx, hy, ok := a.mindBendHandle(md, e, v); ok && float32(math.Hypot(float64(u.pressX-hx), float64(u.pressY-hy))) <= u.px(10) {
			if u.double {
				md.act = nil
				a.mindResetBend(md, e)
				return
			}
			a.mindStartBend(md, act, e)
			return
		}
	}
	if n := handles; n != nil {
		for _, h := range a.mindHandles(md, n, v) {
			if !h.r.Inset(-u.px(3)).Contains(u.pressX, u.pressY) {
				continue
			}
			if h.side == 0 {
				if u.double {
					a.mindResetSize(md, n)
					md.act = nil
					return
				}
				act.kind, act.id, act.size = actResize, n.ID, [2]float64{n.W, n.H}
				return
			}
			act.kind, act.id, act.side = actConnect, n.ID, h.side
			return
		}
	}
	n, fold := m.At(p.X, p.Y)
	if n != nil {
		if tb, ok := m.TaskBox(n); ok && tb.Grow(3).Contains(p.X, p.Y) && !u.shift && !u.ctrl {
			md.act = nil
			md.selectOnly(n.ID)
			a.mindDo(md, m.ToggleDone(n.ID))
			return
		}
		if fold {
			m.Update(n.ID, func(x *mind.Node) { x.Folded = !x.Folded })
			md.act = nil
			return
		}
		if u.double {
			md.act = nil
			a.mindOpenNode(md, n, p)
			return
		}
		switch {
		case u.ctrl || u.shift:
			if md.sel[n.ID] {
				delete(md.sel, n.ID)
			} else {
				md.sel[n.ID] = true
				md.primary = n.ID
			}
			md.edge = 0
		case !md.sel[n.ID]:
			md.selectOnly(n.ID)
		default:
			md.primary = n.ID
		}
		act.kind, act.id = actMove, n.ID
		return
	}
	if e := m.EdgeAt(p.X, p.Y, 6/v.Zoom); e != nil {
		clear(md.sel)
		md.primary, md.edge = 0, e.ID
		if u.double {
			md.act = nil
			a.editEdgeLabel(md, e)
			return
		}
		// Dragging the line itself reshapes it, as dragging its middle does.
		a.mindStartBend(md, act, e)
		return
	}
	if u.double {
		md.act = nil
		a.mindPlace(md, mtTopic, p)
		return
	}
	if u.shift || u.ctrl {
		act.kind = actMarquee
		act.before = map[mind.ID]bool{}
		for id := range md.sel {
			act.before[id] = true
		}
		return
	}
	act.kind = actPan
}

// mindDragStarts reports whether the pointer moved far enough from the press for a drag.
func (a *App) mindDragStarts(act *mindAction) bool {
	u := &a.u
	if !act.moved && math.Hypot(float64(u.mx-act.grab.X), float64(u.my-act.grab.Y)) > float64(u.px(4)) {
		act.moved = true
	}
	return act.moved
}

// mindDrag carries a gesture on as the pointer moves.
func (a *App) mindDrag(md *mindDoc, r gfx.Rect, v mind.View) {
	u := &a.u
	act := md.act
	m := md.m
	p := v.Board(float64(u.mx), float64(u.my))
	switch act.kind {
	case actTextSelect:
		a.mindCaretTo(md, v, u.mx, u.my, false)
	case actBend:
		if !a.mindDragStarts(act) {
			return
		}
		bend := act.shownB.Add(p.Sub(act.from))
		m.LiveEdge(act.edge, func(e *mind.Edge) {
			e.FromSide, e.ToSide = act.shown[0], act.shown[1]
			e.Bend = mind.Vec{X: math.Round(bend.X), Y: math.Round(bend.Y)}
		})
	case actPan:
		if a.mindDragStarts(act) {
			md.view.OX = math.Round(act.view.OX + float64(u.mx-act.grab.X))
			md.view.OY = math.Round(act.view.OY + float64(u.my-act.grab.Y))
		}
	case actMarquee:
		if !a.mindDragStarts(act) {
			return
		}
		box := mind.Rect{X0: math.Min(act.from.X, p.X), Y0: math.Min(act.from.Y, p.Y), X1: math.Max(act.from.X, p.X), Y1: math.Max(act.from.Y, p.Y)}
		clear(md.sel)
		for id := range act.before {
			md.sel[id] = true
		}
		for _, n := range m.In(box, false) {
			md.sel[n.ID] = true
			md.primary = n.ID
		}
		md.edge = 0
	case actMove:
		if !a.mindDragStarts(act) {
			return
		}
		a.mindMoveDrag(md, p)
	case actConnect:
		a.mindDragStarts(act)
		act.target = 0
		if t, _ := m.At(p.X, p.Y); t != nil && t.ID != act.id {
			act.target = t.ID
		}
	case actResize:
		if !a.mindDragStarts(act) {
			return
		}
		n := m.Node(act.id)
		if n == nil {
			return
		}
		b := m.Box(n)
		w := math.Max(40, math.Round(p.X-b.X0))
		h := math.Max(24, math.Round(p.Y-b.Y0))
		m.Live(n.ID, func(x *mind.Node) {
			x.W = w
			if n.Kind == mind.Topic && boxShape(n) {
				x.H = h
			}
		})
	}
	a.mindEdgeScroll(md, r)
}

// boxShape reports whether a topic's outline encloses it, so it has a height to size.
func boxShape(n *mind.Node) bool {
	return n.Shape != mind.ShapeAuto && n.Shape != mind.ShapeUnderline && n.Shape != mind.ShapeNone
}

// mindEdgeScroll moves the view while a drag holds the pointer near the board's edge.
func (a *App) mindEdgeScroll(md *mindDoc, r gfx.Rect) {
	u := &a.u
	act := md.act
	if act.kind == actPan || act.kind == actTextSelect || !act.moved {
		return
	}
	edge := u.px(28)
	step := float64(u.px(14))
	moved := false
	if u.mx < r.X+edge {
		md.view.OX += step
		moved = true
	} else if u.mx > r.Right()-edge {
		md.view.OX -= step
		moved = true
	}
	if u.my < r.Y+edge {
		md.view.OY += step
		moved = true
	} else if u.my > r.Bottom()-edge {
		md.view.OY -= step
		moved = true
	}
	if moved {
		md.view.OX, md.view.OY = math.Round(md.view.OX), math.Round(md.view.OY)
		a.requestFrame()
	}
}

// mindMoveDrag moves the chosen free nodes with the pointer, or finds where the tree's node dragged would go.
func (a *App) mindMoveDrag(md *mindDoc, p mind.Vec) {
	m := md.m
	act := md.act
	n := m.Node(act.id)
	if n == nil {
		md.act = nil
		return
	}
	if act.starts == nil && !act.tree {
		if n.Parent != 0 {
			act.tree = true
		} else {
			act.starts = map[mind.ID]mind.Vec{}
			for _, id := range m.Tops(md.chosen()) {
				if c := m.Node(id); c != nil && c.Parent == 0 {
					act.starts[id] = mind.Vec{X: c.X, Y: c.Y}
				}
			}
		}
	}
	if act.tree {
		act.drop = a.mindDropAt(md, n, p)
		return
	}
	dx, dy := p.X-act.from.X, p.Y-act.from.Y
	for id, s := range act.starts {
		m.Live(id, func(x *mind.Node) { x.X, x.Y = math.Round(s.X+dx), math.Round(s.Y+dy) })
	}
	// A free node alone, without children, dropped onto a topic goes under it.
	act.attach = 0
	if len(act.starts) == 1 && len(n.Children) == 0 {
		for _, c := range m.In(mind.Rect{X0: p.X, Y0: p.Y, X1: p.X, Y1: p.Y}, true) {
			if c.ID != n.ID && !m.Under(c, n) {
				act.attach = c.ID
			}
		}
	}
}

// mindDropAt is where node n of a tree, dragged to p, would go: under the node at p, before or after a sibling
// beside p, or free.
func (a *App) mindDropAt(md *mindDoc, n *mind.Node, p mind.Vec) mindDrop {
	m := md.m
	reach := 70.0
	near := m.In(mind.Rect{X0: p.X - reach, Y0: p.Y - reach, X1: p.X + reach, Y1: p.Y + reach}, true)
	for _, c := range near {
		if m.Under(c, n) {
			continue
		}
		if m.Box(c).Contains(p.X, p.Y) {
			return mindDrop{parent: c.ID, index: -1}
		}
	}
	best := math.Inf(1)
	var drop mindDrop
	for _, c := range near {
		if c.Parent == 0 || m.Under(c, n) {
			continue
		}
		parent := m.Node(c.Parent)
		b := m.Box(c)
		dir := m.Dir(parent)
		across := dir == mind.LayoutDown || dir == mind.LayoutUp
		var d float64
		var before bool
		var line [2]mind.Vec
		if across {
			if p.Y < b.Y0-20 || p.Y > b.Y1+20 {
				continue
			}
			d = math.Abs(p.X - b.CX())
			before = p.X < b.CX()
			x := b.X1 + 6
			if before {
				x = b.X0 - 6
			}
			line = [2]mind.Vec{{X: x, Y: b.Y0}, {X: x, Y: b.Y1}}
		} else {
			if p.X < b.X0-30 || p.X > b.X1+30 {
				continue
			}
			d = math.Abs(p.Y - b.CY())
			before = p.Y < b.CY()
			y := b.Y1 + 4
			if before {
				y = b.Y0 - 4
			}
			line = [2]mind.Vec{{X: b.X0, Y: y}, {X: b.X1, Y: y}}
		}
		if d >= best {
			continue
		}
		best = d
		index := slices.Index(parent.Children, c.ID)
		if !before {
			index++
		}
		if n.Parent == parent.ID {
			if at := slices.Index(parent.Children, n.ID); at < index {
				index-- // counted without the node itself
			}
		}
		drop = mindDrop{parent: parent.ID, index: index, line: line}
	}
	if best < math.Inf(1) {
		return drop
	}
	return mindDrop{free: true}
}

// mindRelease finishes a gesture.
func (a *App) mindRelease(md *mindDoc, r gfx.Rect, v mind.View) {
	u := &a.u
	act := md.act
	md.act = nil
	m := md.m
	p := v.Board(float64(u.mx), float64(u.my))
	switch act.kind {
	case actPan:
		if !act.moved {
			md.selectOnly(0)
		}
	case actMove:
		if !act.moved {
			if !u.ctrl && !u.shift {
				md.selectOnly(act.id)
			}
			return
		}
		if act.tree {
			n := m.Node(act.id)
			if n == nil {
				return
			}
			d := act.drop
			switch {
			case d.parent != 0:
				if d.parent == n.Parent && d.index == slices.Index(m.Node(n.Parent).Children, n.ID) {
					return
				}
				a.mindDo(md, m.Move(n.ID, d.parent, d.index, 0, 0))
			case d.free:
				pos := m.Pos(n)
				a.mindDo(md, m.Move(n.ID, 0, -1, math.Round(pos.X+p.X-act.from.X), math.Round(pos.Y+p.Y-act.from.Y)))
			}
			return
		}
		m.Begin(u.t("Move"))
		final := map[mind.ID]mind.Vec{}
		for id := range act.starts {
			if n := m.Node(id); n != nil {
				final[id] = mind.Vec{X: n.X, Y: n.Y}
			}
		}
		for id, s := range act.starts {
			m.Live(id, func(x *mind.Node) { x.X, x.Y = s.X, s.Y })
		}
		if act.attach != 0 && len(final) == 1 {
			for id := range final {
				a.mindDo(md, m.Move(id, act.attach, -1, 0, 0))
			}
		} else {
			for id, f := range final {
				a.mindDo(md, m.Place(id, f.X, f.Y))
			}
		}
		m.Commit()
	case actConnect:
		from := m.Node(act.id)
		if from == nil {
			return
		}
		if act.target != 0 {
			eid, err := m.AddEdge(mind.Edge{From: from.ID, To: act.target})
			if a.mindDo(md, err) {
				clear(md.sel)
				md.primary, md.edge = 0, eid
			}
			return
		}
		a.mindGrowFrom(md, from, act.side, p, act.moved)
	case actBend:
		e := m.Edge(act.edge)
		if e == nil || !act.moved {
			return
		}
		from, to, bend := e.FromSide, e.ToSide, e.Bend
		m.LiveEdge(e.ID, func(e *mind.Edge) { e.FromSide, e.ToSide, e.Bend = act.sides[0], act.sides[1], act.bend })
		a.mindDo(md, m.UpdateEdge(e.ID, func(e *mind.Edge) { e.FromSide, e.ToSide, e.Bend = from, to, bend }))
	case actResize:
		n := m.Node(act.id)
		if n == nil || !act.moved {
			return
		}
		w, h := n.W, n.H
		m.Live(n.ID, func(x *mind.Node) { x.W, x.H = act.size[0], act.size[1] })
		a.mindDo(md, m.Update(n.ID, func(x *mind.Node) { x.W, x.H = w, h }))
	}
}

// mindDo reports whether an operation worked, telling the user why when it did not.
func (a *App) mindDo(md *mindDoc, err error) bool {
	if err != nil {
		a.toast(a.describe(err), true)
		return false
	}
	md.pruneSelection()
	return true
}

// mindResetSize gives a node back the size its content needs.
func (a *App) mindResetSize(md *mindDoc, n *mind.Node) {
	if n.W != 0 || n.H != 0 {
		a.mindDo(md, md.m.Update(n.ID, func(x *mind.Node) { x.W, x.H = 0, 0 }))
	}
}

// mindGrowFrom adds a node connected from n: where the pointer let go of a connector drawn from n's side, or, for a
// click on the dot, beside n on that side. The new node is a box like n, and its words are typed at once.
func (a *App) mindGrowFrom(md *mindDoc, n *mind.Node, side mind.Side, p mind.Vec, dragged bool) {
	m := md.m
	b := m.Box(n)
	shape := mind.ShapeRounded
	if n.Kind == mind.Topic && boxShape(n) && n.Shape != mind.ShapeDiamond {
		shape = n.Shape
	}
	at := p
	if !dragged {
		gap := 90.0
		switch side {
		case mind.SideTop:
			at = mind.Vec{X: b.CX(), Y: b.Y0 - gap - 20}
		case mind.SideBottom:
			at = mind.Vec{X: b.CX(), Y: b.Y1 + gap + 20}
		case mind.SideLeft:
			at = mind.Vec{X: b.X0 - gap - 60, Y: b.CY()}
		default:
			at = mind.Vec{X: b.X1 + gap + 60, Y: b.CY()}
		}
	}
	m.Begin(a.u.t("Add"))
	id, err := a.mindAddCentred(md, mind.Node{Shape: shape}, at)
	if err == nil {
		_, err = m.AddEdge(mind.Edge{From: n.ID, To: id})
	}
	m.Commit()
	if a.mindDo(md, err) {
		md.selectOnly(id)
		a.editMindText(md, id, "", false)
	}
}

// mindAddCentred adds a free node centred on the board point at, in the step open or one of its own.
func (a *App) mindAddCentred(md *mindDoc, n mind.Node, at mind.Vec) (mind.ID, error) {
	m := md.m
	m.Begin(a.u.t("Add"))
	defer m.Commit()
	id, err := m.Add(n, 0, -1)
	if err != nil {
		return 0, err
	}
	m.Layout()
	b := m.Box(m.Node(id))
	return id, m.Place(id, math.Round(at.X-b.W()/2), math.Round(at.Y-b.H()/2))
}

// mindMakeRoom moves the free trees ids, just put on the board, together to the nearest place where they overlap
// nothing, in the step open.
func (a *App) mindMakeRoom(md *mindDoc, ids []mind.ID) {
	m := md.m
	m.Layout()
	var box mind.Rect
	first := true
	for _, id := range ids {
		n := m.Node(id)
		if n == nil || n.Parent != 0 {
			continue
		}
		if first {
			box, first = m.SubBox(n), false
		} else {
			box = box.Union(m.SubBox(n))
		}
	}
	if first {
		return
	}
	to := m.FreeSpot(mind.Vec{X: box.X0, Y: box.Y0}, box.W(), box.H(), ids...)
	dx, dy := to.X-box.X0, to.Y-box.Y0
	if dx == 0 && dy == 0 {
		return
	}
	for _, id := range ids {
		if n := m.Node(id); n != nil && n.Parent == 0 {
			m.Place(id, math.Round(n.X+dx), math.Round(n.Y+dy))
		}
	}
}

// mindPlace adds what tool adds, centred at the board point p, and starts typing into it when it has words.
func (a *App) mindPlace(md *mindDoc, tool mindTool, p mind.Vec) {
	u := &a.u
	var n mind.Node
	switch tool {
	case mtTopic:
		n.Shape = mind.ShapeRounded
	case mtMap:
		n.Text = u.t("Central topic")
	case mtShape:
		n.Shape = md.shape
		if n.Shape == mind.ShapeAuto {
			n.Shape = mind.ShapeRounded
		}
	case mtText:
		n.Shape = mind.ShapeNone
	case mtTable:
		n.Kind = mind.Table
		n.Cells = &mind.Grid{Rows: 3, Cols: 3, Cells: make([]string, 9), ColW: make([]float64, 3)}
	}
	id, err := a.mindAddCentred(md, n, p)
	if !a.mindDo(md, err) {
		return
	}
	md.selectOnly(id)
	switch tool {
	case mtTable:
		a.editMindCell(md, id, 0, 0)
	case mtMap:
		a.editMindText(md, id, "", true)
	default:
		a.editMindText(md, id, "", false)
	}
}

// mindOpenNode does what a double-click on n does: types into a topic, a table's cell or a picture's caption,
// opens a nested map or a smart object in its own tab, and plays or stops a video.
func (a *App) mindOpenNode(md *mindDoc, n *mind.Node, p mind.Vec) {
	md.selectOnly(n.ID)
	switch n.Kind {
	case mind.Table:
		if row, col, ok := md.m.CellAt(n, p.X, p.Y); ok {
			a.editMindCell(md, n.ID, row, col)
			return
		}
		a.editMindText(md, n.ID, "", true)
	case mind.Video:
		if md.video == n.ID {
			md.video = 0
			a.closeFilePreview()
			return
		}
		md.video = n.ID
	case mind.Nested:
		a.openMindFile(n.Ref)
	case mind.Smart:
		a.openSmartByID(n.Ref)
	case mind.Picture:
		a.editMindText(md, n.ID, "", true)
	default:
		a.editMindText(md, n.ID, "", true)
	}
}

// editMindText starts typing into node id's words (a caption for what is not a topic): with typed in place of
// them when typed is not empty, the caret at the end, or all of them chosen when all is true.
func (a *App) editMindText(md *mindDoc, id mind.ID, typed string, all bool) {
	u := &a.u
	n := md.m.Node(id)
	if n == nil {
		return
	}
	a.commitMindCell(md)
	md.text = &mindText{id: id, orig: n.Text}
	text := n.Text
	if typed != "" {
		text = typed
	}
	end := len([]rune(text))
	anchor := end
	if all {
		anchor = 0
	}
	u.beginEdit("mind/text", text, true, anchor, end)
	if typed != "" {
		md.m.Live(id, func(x *mind.Node) { x.Text = typed })
	}
}

// commitMindText ends typing, keeping what was typed as one step.
func (a *App) commitMindText(md *mindDoc) {
	t := md.text
	if t == nil {
		return
	}
	md.text = nil
	if a.u.focus == "mind/text" {
		a.u.focus = ""
	}
	n := md.m.Node(t.id)
	if n == nil {
		return
	}
	text := strings.TrimRight(n.Text, " \t\n")
	md.m.Live(t.id, func(x *mind.Node) { x.Text = t.orig })
	if text != t.orig {
		a.mindDo(md, md.m.Update(t.id, func(x *mind.Node) { x.Text = text }))
	}
}

// mindTextKeys types into the node: Enter ends (Shift+Enter starts a new line), Tab ends and adds a child, Escape
// ends; the rest edits as in any text box.
func (a *App) mindTextKeys(md *mindDoc) {
	u := &a.u
	t := md.text
	u.focusDrawn = true
	if !u.editing("mind/text") {
		// The keyboard or the editor went elsewhere and came back: typing goes on from the node's words.
		words := ""
		if n := md.m.Node(t.id); n != nil {
			words = n.Text
		}
		u.beginEdit("mind/text", words, true, len([]rune(words)), len([]rune(words)))
	}
	changed := false
	keys := u.keys
	u.keys = nil
	for i, k := range keys {
		switch {
		case k.Code == keyEnter && !k.Shift:
			a.commitMindText(md)
			u.keys = keys[i+1:]
			return
		case k.Code == keyEscape:
			a.commitMindText(md)
			return
		case k.Code == keyTab && !k.Ctrl:
			a.commitMindText(md)
			if k.Shift {
				a.mindAddSibling(md, false)
			} else {
				a.mindAddChild(md)
			}
			return
		case k.Code == keyUp || k.Code == keyDown:
			a.mindCaretLine(md, k)
		default:
			rev := u.edit.rev
			u.edit.key(k, u)
			changed = changed || u.edit.rev != rev
		}
	}
	if len(u.typed) > 0 {
		u.edit.typeText(string(u.typed))
		u.typed = nil
		changed = true
	}
	if changed {
		text := string(u.edit.text)
		md.m.Live(t.id, func(x *mind.Node) { x.Text = text })
	}
}

// mindLines are the lines a node's words are laid out in, each with the index of its first character in text.
func mindLines(text []rune, lines []string) (starts []int) {
	pos := 0
	for _, l := range lines {
		lr := []rune(l)
		for pos < len(text) && !hasAt(text, pos, lr) && unicode.IsSpace(text[pos]) {
			pos++
		}
		starts = append(starts, pos)
		pos += len(lr)
	}
	return starts
}

func hasAt(text []rune, pos int, s []rune) bool {
	if pos+len(s) > len(text) {
		return false
	}
	for i, r := range s {
		if text[pos+i] != r {
			return false
		}
	}
	return true
}

// mindTextLayout is the node's words as typed, laid out: its lines, where each starts and its box on the board.
type mindTextLayout struct {
	lines  []string
	starts []int
	rect   mind.Rect
	size   float32
	bold   bool
	lineH  float64
	centre bool
}

func (a *App) mindTextLayout(md *mindDoc, n *mind.Node) mindTextLayout {
	rect, size, bold := md.m.TextRect(n)
	lines := md.m.Lines(n)
	if len(lines) == 0 {
		lines = []string{""}
	}
	shape, _, _, _, _ := md.m.Look(n)
	return mindTextLayout{lines: lines, starts: mindLines(a.u.edit.text, lines), rect: rect, size: size, bold: bold,
		lineH: a.mindMeasure().LineHeight(size), centre: n.Kind != mind.Topic || shape != mind.ShapeUnderline && shape != mind.ShapeNone || n.W > 0}
}

// lineX is where a line starts across, on the board.
func (a *App) mindLineX(tl mindTextLayout, line string) float64 {
	if !tl.centre {
		return tl.rect.X0
	}
	return tl.rect.X0 + (tl.rect.W()-a.mindMeasure().Width(line, tl.size, tl.bold))/2
}

// mindCaretTo puts the caret at the window point x, y in the words being typed, keeping the other end of the
// choice unless reset is true.
func (a *App) mindCaretTo(md *mindDoc, v mind.View, x, y float32, reset bool) {
	u := &a.u
	n := md.m.Node(md.text.id)
	if n == nil {
		return
	}
	tl := a.mindTextLayout(md, n)
	p := v.Board(float64(x), float64(y))
	line := min(max(int((p.Y-tl.rect.Y0)/tl.lineH), 0), len(tl.lines)-1)
	rs := []rune(tl.lines[line])
	lx := a.mindLineX(tl, tl.lines[line])
	ms := a.mindMeasure()
	best := len(rs)
	for i := range rs {
		mid := (ms.Width(string(rs[:i]), tl.size, tl.bold) + ms.Width(string(rs[:i+1]), tl.size, tl.bold)) / 2
		if p.X-lx < mid {
			best = i
			break
		}
	}
	u.edit.caret = min(tl.starts[line]+best, len(u.edit.text))
	if reset {
		u.edit.anchor = u.edit.caret
	}
}

// mindCaretLine moves the caret a line up or down in the words being typed.
func (a *App) mindCaretLine(md *mindDoc, k Key) {
	u := &a.u
	n := md.m.Node(md.text.id)
	if n == nil {
		return
	}
	tl := a.mindTextLayout(md, n)
	line, col := caretLineCol(tl, u.edit.caret)
	to := line - 1
	if k.Code == keyDown {
		to = line + 1
	}
	if to < 0 || to >= len(tl.lines) {
		return
	}
	u.edit.caret = tl.starts[to] + min(col, len([]rune(tl.lines[to])))
	if !k.Shift {
		u.edit.anchor = u.edit.caret
	}
}

// caretLineCol is the line the caret is on and how far into it.
func caretLineCol(tl mindTextLayout, caret int) (int, int) {
	line := 0
	for i, s := range tl.starts {
		if caret >= s {
			line = i
		}
	}
	return line, min(caret-tl.starts[line], len([]rune(tl.lines[line])))
}

// drawMindText draws the caret and the chosen words over the node being typed into.
func (a *App) drawMindText(md *mindDoc, v mind.View) {
	u := &a.u
	if md.text == nil {
		return
	}
	n := md.m.Node(md.text.id)
	if n == nil {
		return
	}
	u.focusDrawn = true
	tl := a.mindTextLayout(md, n)
	ms := a.mindMeasure()
	box := v.ScreenRect(tl.rect)
	u.c.RoundOutline(box.Inset(-u.px(4)), u.px(4), max(1, u.px(1)), gfx.Alpha(colAccent, 0.6))
	lh := float32(tl.lineH * v.Zoom)
	s, e := u.edit.selection()
	for i, l := range tl.lines {
		start := tl.starts[i]
		rs := []rune(l)
		a0, b0 := min(max(s-start, 0), len(rs)), min(max(e-start, 0), len(rs))
		if a0 < b0 {
			x0 := a.mindLineX(tl, l) + ms.Width(string(rs[:a0]), tl.size, tl.bold)
			x1 := a.mindLineX(tl, l) + ms.Width(string(rs[:b0]), tl.size, tl.bold)
			sx0, sy := v.Screen(mind.Vec{X: x0, Y: tl.rect.Y0 + float64(i)*tl.lineH})
			sx1, _ := v.Screen(mind.Vec{X: x1})
			u.c.Fill(gfx.Rect{X: sx0, Y: sy, W: max(sx1-sx0, u.px(3)), H: lh}, gfx.Alpha(colAccent, 0.4))
		}
	}
	line, col := caretLineCol(tl, u.edit.caret)
	rs := []rune(tl.lines[line])
	cx := a.mindLineX(tl, tl.lines[line]) + ms.Width(string(rs[:col]), tl.size, tl.bold)
	x, y := v.Screen(mind.Vec{X: cx, Y: tl.rect.Y0 + float64(line)*tl.lineH})
	u.drawCaret(x, y, lh, true) // readable on light boards and dark
}

// editMindCell starts typing into a table's cell.
func (a *App) editMindCell(md *mindDoc, id mind.ID, row, col int) {
	u := &a.u
	n := md.m.Node(id)
	if n == nil || n.Kind != mind.Table {
		return
	}
	a.commitMindText(md)
	a.commitMindCell(md)
	value := n.Cells.Cell(row, col)
	md.cell = &mindCell{id: id, row: row, col: col, value: value}
	md.tableAt = mindCellAt{id, row, col}
	u.focusEdit("mind/cell", value, false)
}

// editEdgeLabel starts typing a connector's label.
func (a *App) editEdgeLabel(md *mindDoc, e *mind.Edge) {
	u := &a.u
	a.commitMindText(md)
	a.commitMindCell(md)
	md.cell = &mindCell{edge: e.ID, value: e.Label}
	u.focusEdit("mind/cell", e.Label, false)
}

// commitMindCell keeps what was typed into a cell or label, as one step.
func (a *App) commitMindCell(md *mindDoc) {
	c := md.cell
	if c == nil {
		return
	}
	md.cell = nil
	if a.u.focus == "mind/cell" {
		a.u.focus = ""
	}
	if c.edge != 0 {
		if e := md.m.Edge(c.edge); e != nil && e.Label != c.value {
			a.mindDo(md, md.m.UpdateEdge(c.edge, func(e *mind.Edge) { e.Label = strings.TrimSpace(c.value) }))
		}
		return
	}
	if n := md.m.Node(c.id); n != nil && n.Cells.Cell(c.row, c.col) != c.value {
		a.mindDo(md, md.m.SetCell(c.id, c.row, c.col, c.value))
	}
}

// drawMindCell draws the box a cell or label is typed in, over it; Enter goes down a row and Tab to the next
// cell, adding them past the table's end, and Escape leaves the cell as it was.
func (a *App) drawMindCell(md *mindDoc, v mind.View) {
	u := &a.u
	c := md.cell
	if c == nil {
		return
	}
	var box gfx.Rect
	if c.edge != 0 {
		e := md.m.Edge(c.edge)
		if e == nil {
			md.cell = nil
			return
		}
		_, label, ok := md.m.EdgePath(e)
		if !ok {
			md.cell = nil
			return
		}
		x, y := v.Screen(label)
		box = gfx.Rect{X: x - u.px(90), Y: y - u.px(16), W: u.px(180), H: u.px(32)}
	} else {
		n := md.m.Node(c.id)
		if n == nil {
			md.cell = nil
			return
		}
		box = v.ScreenRect(md.m.CellRect(n, c.row, c.col))
		box.W, box.H = max(box.W, u.px(140)), max(box.H, u.px(30))
	}
	// Keys the box does not keep: Escape drops what was typed, Enter and Tab move on.
	var next Key
	for _, k := range u.keys {
		if k.Code == keyEscape {
			md.cell = nil
			u.focus = ""
			u.keys = nil
			return
		}
		if k.Code == keyEnter || k.Code == keyTab {
			next = k
		}
	}
	_, done := u.textBox("mind/cell", box, &c.value, "", false)
	if done && md.cell == c {
		a.commitMindCell(md)
		if c.edge == 0 && next.Code != 0 {
			row, col := c.row+1, c.col
			if next.Code == keyTab {
				row, col = c.row, c.col+1
				if next.Shift {
					col = max(c.col-1, 0)
				}
			} else if next.Shift {
				row = max(c.row-1, 0)
			}
			a.editMindCell(md, c.id, row, col)
		}
	}
}

// mindBendHandle is where the chosen connector's middle handle is in the window.
func (a *App) mindBendHandle(md *mindDoc, e *mind.Edge, v mind.View) (float32, float32, bool) {
	_, mid, ok := md.m.EdgePath(e)
	if !ok {
		return 0, 0, false
	}
	x, y := v.Screen(mid)
	return x, y, true
}

// mindStartBend starts reshaping connector e from the route it shows, found round the nodes or given before.
func (a *App) mindStartBend(md *mindDoc, act *mindAction, e *mind.Edge) {
	act.kind, act.edge, act.bend, act.sides = actBend, e.ID, e.Bend, [2]mind.Side{e.FromSide, e.ToSide}
	from, to, bend := md.m.EdgeShape(e)
	act.shown, act.shownB = [2]mind.Side{from, to}, bend
}

// mindResetBend gives a connector back the route it finds itself: no bend, and the sides it chooses.
func (a *App) mindResetBend(md *mindDoc, e *mind.Edge) {
	if e.Bend != (mind.Vec{}) || e.FromSide != mind.SideAuto || e.ToSide != mind.SideAuto {
		a.mindDo(md, md.m.UpdateEdge(e.ID, func(e *mind.Edge) { e.Bend, e.FromSide, e.ToSide = mind.Vec{}, mind.SideAuto, mind.SideAuto }))
	}
}
