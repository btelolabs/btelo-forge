package ui

import (
	"fmt"
	"image/color"
	"math"
	"slices"

	"github.com/btelolabs/btelo-forge/internal/gfx"
	"github.com/btelolabs/btelo-forge/internal/paint"
)

// The pen and the node tool, in the manner of Affinity Designer's: the pen draws a curve node by node, the node tool
// edits any vector layer's nodes and handles, and both share a context bar that edits the object's fill and stroke.

const (
	icPen      = "\ue746"
	icNodeTool = "\ue25e"
	icPencil   = "\ue150"
)

// grabReach is how near, in screen pixels, a press must be to a node, handle or outline to take it.
const grabReach = 7

// penEdit is a curve being drawn with the pen: its layer is in the picture, recorded as one step when it ends.
type penEdit struct {
	s       *paint.VectorSession
	node    int  // the node the drag shapes: the newest, or the first when closing
	closing bool // the press landed on the first node: the release closes the curve
}

// What a node tool drag holds.
const (
	nodeGrabNone = iota
	nodeGrabNodes
	nodeGrabHandle
	nodeGrabBox
)

// nodeEdit is the node tool's state: the nodes chosen on the chosen vector layer and the drag under way.
type nodeEdit struct {
	layer   int // the layer the chosen nodes belong to
	sel     []paint.NodeRef
	grab    int
	handle  paint.NodeRef // the node whose handle is held
	out     bool          // the out handle, else the in
	start   paint.Vector  // the object when the drag began
	from    paint.Pt      // where the drag began, in the object's own space
	name    string        // the step the drag will be recorded as
	anchor  paint.Pt      // the held node or handle where the drag began, in canvas pixels, for snapping
	press   paint.Pt      // the pointer where the drag began, in canvas pixels
	s       *paint.VectorSession
	box     [2][2]float64 // a drag over nothing: the box of nodes to choose, in canvas pixels
	snapFor int           // the layer, plus one, the snap targets were gathered for in this drag
}

// penVector is the curve the pen starts with a node at p: stroked with the foreground colour and, as its option
// says, filled with the background colour.
func (a *App) penVector(p paint.Pt) paint.Vector {
	s := &a.settings.Paint
	v := paint.Vector{Kind: paint.VectorCurve, M: paint.Identity, Path: []paint.Subpath{{Nodes: []paint.PathNode{sharpNode(p)}}},
		Stroke: paint.VectorStroke{Paint: solidPaint(a.brush.Color), Width: max(s.ShapeWidth, 1), Join: paint.JoinRound, Cap: paint.CapRound}}
	if s.PenFill {
		v.Fill = solidPaint(a.backgroundColor())
	}
	return v
}

func solidPaint(c color.NRGBA) paint.VectorPaint {
	return paint.VectorPaint{Kind: paint.PaintSolid, Color: c}
}

func sharpNode(p paint.Pt) paint.PathNode { return paint.PathNode{P: p, In: p, Out: p} }

// near reports whether canvas point p is within grabReach of the pointer on screen.
func (a *App) near(ad *artDoc, vp gfx.Rect, p paint.Pt) bool {
	x, y := ad.toScreen(vp, [2]float64{p.X, p.Y})
	dx, dy := a.u.mx-x, a.u.my-y
	r := a.u.px(grabReach)
	return dx*dx+dy*dy <= r*r
}

// penPointer is the pen: a click adds a sharp node, a drag pulls out a smooth one's handles, a click on the first
// node closes the curve; Enter, Escape or another tool ends it.
func (a *App) penPointer(ad *artDoc, vp gfx.Rect) {
	u := &a.u
	const id = "art/view"
	cx, cy := ad.toCanvas(vp, u.mx, u.my)
	p := paint.Pt{X: cx, Y: cy}
	if u.hover(id, vp) {
		u.cursor = CursorCross
	}
	if u.press(id, vp) {
		var own []paint.Pt
		skip := 0
		if ad.pen != nil {
			skip = ad.pen.s.Layer()
			for _, n := range ad.pen.s.Vector().Path[0].Nodes {
				own = append(own, n.P)
			}
		}
		ad.snapTo = ad.snapTargetsOf(skip, 0, own)
		p, _ = a.snapped(ad, p, ad.snapTo)
		if ad.pen == nil {
			s, err := ad.d.BeginVector(0, a.penVector(p), u.tf("Curve %d", len(ad.d.Layers())+1), "Pen")
			if err != nil {
				a.toast(a.describe(err), true)
				u.active = ""
				return
			}
			ad.pen = &penEdit{s: s}
			ad.touched(s.Set(s.Vector()))
		} else {
			pe := ad.pen
			v := pe.s.Vector()
			nodes := v.Path[0].Nodes
			if len(nodes) >= 2 && a.near(ad, vp, nodes[0].P) {
				pe.node, pe.closing = 0, true
				v = v.Close(0)
			} else {
				pe.node = len(nodes)
				v = v.AppendNode(0, sharpNode(p))
			}
			ad.touched(pe.s.Set(v))
		}
	}
	if u.dragging(id) && ad.pen != nil && u.drag.moved {
		pe := ad.pen
		v := pe.s.Vector()
		r := paint.NodeRef{Node: pe.node}
		n := v.Path[0].Nodes[pe.node]
		// The handles pull out in line, the drag's way, as a smooth node's do.
		n.Out, n.In, n.Smooth = p, n.P.Mul(2).Sub(p), true
		ad.touched(pe.s.Set(v.WithNode(r, n)))
	}
	if u.letGo(id) && ad.pen != nil && ad.pen.closing {
		a.finishPen(ad)
	}
}

// pencilPointer is the pencil, as Affinity Designer's: a freehand line, fitted with a smooth curve when it is let go,
// and closed when it ends where it began.
func (a *App) pencilPointer(ad *artDoc, vp gfx.Rect) {
	u := &a.u
	const id = "art/view"
	cx, cy := ad.toCanvas(vp, u.mx, u.my)
	if u.hover(id, vp) {
		u.cursor = CursorCross
	}
	if u.press(id, vp) {
		ad.pencil = [][2]float64{{cx, cy}}
	}
	if u.dragging(id) && len(ad.pencil) > 0 {
		last := ad.pencil[len(ad.pencil)-1]
		if math.Hypot(cx-last[0], cy-last[1])*ad.zoom >= float64(u.px(2)) {
			ad.pencil = append(ad.pencil, [2]float64{cx, cy})
		}
	}
	if u.letGo(id) && len(ad.pencil) > 0 {
		line := ad.pencil
		ad.pencil = nil
		a.finishPencil(ad, append(line, [2]float64{cx, cy}))
	}
}

// finishPencil makes the pencil's line a curve on a layer of its own, as one step.
func (a *App) finishPencil(ad *artDoc, line [][2]float64) {
	pts := make([]paint.Pt, len(line))
	for i, p := range line {
		pts[i] = paint.Pt{X: p[0], Y: p[1]}
	}
	reach := float64(a.u.px(grabReach)) / ad.zoom
	length := 0.0
	for i := 1; i < len(pts); i++ {
		length += pts[i].Dist(pts[i-1])
	}
	if length < reach {
		return // a click, or barely a line, draws nothing
	}
	// Ending where it began, after going somewhere, closes it.
	closed := pts[0].Dist(pts[len(pts)-1]) <= reach && length > 4*reach
	if closed {
		pts = pts[:len(pts)-1]
	}
	// Fitted to within about a screen pixel and a half, which smooths the hand's tremor away.
	path := paint.FitPath(pts, closed, float64(a.u.px(1.5))/ad.zoom)
	v := a.penVector(pts[0])
	v.Path = []paint.Subpath{path}
	if !closed {
		v.Fill = paint.VectorPaint{}
	}
	a.artRun(ad, func() error {
		s, err := ad.d.BeginVector(0, v, a.u.tf("Curve %d", len(ad.d.Layers())+1), "Pencil")
		if err != nil {
			return err
		}
		s.End()
		return nil
	})
}

// finishPen ends the curve being drawn, keeping it when it has a segment.
func (a *App) finishPen(ad *artDoc) {
	pe := ad.pen
	if pe == nil {
		return
	}
	ad.pen = nil
	if v := pe.s.Vector(); len(v.Path) > 0 && len(v.Path[0].Nodes) < 2 {
		ad.touched(pe.s.Set(v.DeleteNodes([]paint.NodeRef{{}})))
	}
	pe.s.End()
	ad.changedAll()
}

// editedVector is the chosen layer's object when it is a vector layer.
func (ad *artDoc) editedVector() (*paint.Node, bool) {
	n := ad.d.Node(ad.d.Active)
	return n, n != nil && n.Vector != nil
}

// nodeState is the node tool's state for the chosen layer: chosen nodes belong to one layer.
func (ad *artDoc) nodeState() *nodeEdit {
	if ad.nodes == nil {
		ad.nodes = &nodeEdit{}
	}
	if ad.nodes.layer != ad.d.Active && ad.nodes.s == nil {
		ad.nodes.layer, ad.nodes.sel = ad.d.Active, nil
	}
	return ad.nodes
}

// canvasOf maps a point of an object's own space to the canvas.
func canvasOf(v *paint.Vector, p paint.Pt) paint.Pt { return v.M.Map(p) }

// nodePointer is the node tool: a press takes a chosen node's handle, a node (Shift adds it to the chosen ones,
// a double-click makes it smooth or sharp), the outline (adding a node there) or another object; a drag over
// nothing chooses the nodes in a box. Alt while dragging a handle breaks a smooth node.
func (a *App) nodePointer(ad *artDoc, vp gfx.Rect) {
	u := &a.u
	const id = "art/view"
	cx, cy := ad.toCanvas(vp, u.mx, u.my)
	p := paint.Pt{X: cx, Y: cy}
	ne := ad.nodeState()
	n, ok := ad.editedVector()
	var v paint.Vector
	var inv paint.Affine
	if ok {
		v = n.Vector.ToCurve()
		if inv, ok = v.M.Inverse(); !ok {
			n = nil
		}
	}
	grabbedNode := func() (paint.NodeRef, bool) {
		for si, s := range v.Path {
			for ni := range s.Nodes {
				if a.near(ad, vp, canvasOf(&v, s.Nodes[ni].P)) {
					return paint.NodeRef{Sub: si, Node: ni}, true
				}
			}
		}
		return paint.NodeRef{}, false
	}
	grabbedHandle := func() (paint.NodeRef, bool, bool) {
		for _, r := range ne.sel {
			nd, ok := nodeOf(v, r)
			if !ok {
				continue
			}
			for _, out := range []bool{true, false} {
				h := nd.In
				if out {
					h = nd.Out
				}
				if h != nd.P && a.near(ad, vp, canvasOf(&v, h)) {
					return r, out, true
				}
			}
		}
		return paint.NodeRef{}, false, false
	}
	if u.hover(id, vp) {
		u.cursor = CursorArrow
		if n != nil {
			if _, ok := grabbedNode(); ok {
				u.cursor = CursorMove
			}
		}
	}
	slack := float64(u.px(grabReach)) / ad.zoom
	if u.press(id, vp) {
		ne.grab, ne.s, ne.start = nodeGrabNone, nil, v
		ne.press, ne.anchor = p, p
		if n != nil {
			ne.from = inv.Map(p)
		}
		var hr paint.NodeRef
		var hout, onHandle bool
		var nr paint.NodeRef
		var onNode bool
		if n != nil {
			hr, hout, onHandle = grabbedHandle()
			nr, onNode = grabbedNode()
		}
		switch {
		case onHandle:
			ne.grab, ne.handle, ne.out, ne.name = nodeGrabHandle, hr, hout, "Move handle"
			if nd, ok := nodeOf(v, hr); ok {
				ne.anchor = canvasOf(&v, nd.In)
				if hout {
					ne.anchor = canvasOf(&v, nd.Out)
				}
			}
		case onNode && u.double:
			nd, _ := nodeOf(v, nr)
			name := "Smooth node"
			if nd.Smooth {
				name = "Sharp node"
			}
			a.artRun(ad, func() error {
				ad.d.SetVector(n.ID, v.SetSmooth(nr, !nd.Smooth), name)
				return nil
			})
			ne.sel = []paint.NodeRef{nr}
		case onNode:
			chosen := slices.Contains(ne.sel, nr)
			switch {
			case u.shift && chosen:
				ne.sel = slices.DeleteFunc(ne.sel, func(r paint.NodeRef) bool { return r == nr })
			case u.shift:
				ne.sel = append(ne.sel, nr)
			case !chosen:
				ne.sel = []paint.NodeRef{nr}
			}
			if slices.Contains(ne.sel, nr) {
				ne.grab, ne.name = nodeGrabNodes, "Move node"
				nd, _ := nodeOf(v, nr)
				ne.anchor = canvasOf(&v, nd.P)
			}
		case n != nil && a.onOutline(ad, vp, v, inv.Map(p)):
			r, t, _ := v.NearestOnSegment(inv.Map(p))
			added, at := v.InsertNode(r, t)
			ne.start, ne.sel, ne.grab, ne.name = added, []paint.NodeRef{at}, nodeGrabNodes, "Add node"
			if nd, ok := nodeOf(added, at); ok {
				ne.anchor = canvasOf(&added, nd.P)
			}
			s, err := ad.d.BeginVector(n.ID, paint.Vector{}, "", "Add node")
			if err == nil {
				ne.s = s
				ad.touched(s.Set(added))
			}
		default:
			if other := ad.d.VectorAt(p, slack); other != 0 && (n == nil || other != n.ID) {
				// Another object: chosen instead, or with Shift besides, as booleans need.
				if u.shift && n != nil {
					ad.togglePicked(other)
				} else {
					ad.d.Choose(other)
					ad.picked = nil
					ad.nodeState()
				}
				break
			}
			if !u.shift {
				ne.sel, ad.picked = nil, nil
			}
			ne.grab, ne.box = nodeGrabBox, [2][2]float64{{cx, cy}, {cx, cy}}
		}
	}
	if u.dragging(id) && u.drag.moved && n != nil {
		switch ne.grab {
		case nodeGrabNodes, nodeGrabHandle:
			if ne.s == nil {
				s, err := ad.d.BeginVector(n.ID, paint.Vector{}, "", ne.name)
				if err != nil {
					a.toast(a.describe(err), true)
					ne.grab = nodeGrabNone
					break
				}
				ne.s = s
			}
			if ne.snapFor != n.ID+1 {
				// The object's own nodes that stay put are targets too.
				var own []paint.Pt
				for si, sp := range v.Path {
					for ni, nd := range sp.Nodes {
						if !slices.Contains(ne.sel, paint.NodeRef{Sub: si, Node: ni}) {
							own = append(own, canvasOf(&v, nd.P))
						}
					}
				}
				ad.snapTo, ne.snapFor = ad.snapTargetsOf(n.ID, 0, own), n.ID+1
			}
			// Where the held node or handle would go, snapped; the drag moves by what that takes.
			at, _ := a.snapped(ad, ne.anchor.Add(p.Sub(ne.press)), ad.snapTo)
			q := inv.Map(at)
			next := ne.start.MoveNodes(ne.sel, q.Sub(inv.Map(ne.anchor)))
			if ne.grab == nodeGrabHandle {
				next = ne.start.MoveHandle(ne.handle, ne.out, q, u.alt)
			}
			ad.touched(ne.s.Set(next))
		}
	}
	if u.dragging(id) && ne.grab == nodeGrabBox {
		ne.box[1] = [2]float64{cx, cy}
	}
	if u.letGo(id) {
		ne.snapFor = 0
		switch {
		case ne.s != nil:
			ne.s.End()
			ne.s = nil
			ad.changedAll()
		case ne.grab == nodeGrabBox && n != nil:
			x0, x1 := min(ne.box[0][0], ne.box[1][0]), max(ne.box[0][0], ne.box[1][0])
			y0, y1 := min(ne.box[0][1], ne.box[1][1]), max(ne.box[0][1], ne.box[1][1])
			for si, s := range v.Path {
				for ni, nd := range s.Nodes {
					c := canvasOf(&v, nd.P)
					r := paint.NodeRef{Sub: si, Node: ni}
					if c.X >= x0 && c.X <= x1 && c.Y >= y0 && c.Y <= y1 && !slices.Contains(ne.sel, r) {
						ne.sel = append(ne.sel, r)
					}
				}
			}
		}
		ne.grab = nodeGrabNone
	}
}

// nodeOf is node r of a curve, and whether there is one.
func nodeOf(v paint.Vector, r paint.NodeRef) (paint.PathNode, bool) {
	if r.Sub < 0 || r.Sub >= len(v.Path) || r.Node < 0 || r.Node >= len(v.Path[r.Sub].Nodes) {
		return paint.PathNode{}, false
	}
	return v.Path[r.Sub].Nodes[r.Node], true
}

// onOutline reports whether the object's outline passes within grabReach of q, a point in its own space.
func (a *App) onOutline(ad *artDoc, vp gfx.Rect, v paint.Vector, q paint.Pt) bool {
	r, t, dist := v.NearestOnSegment(q)
	return !math.IsInf(dist, 0) && a.near(ad, vp, canvasOf(&v, v.PointAt(r, t)))
}

// nodeKeys are the node tool's and the pen's keys: Delete removes the chosen nodes, the arrows nudge them (Shift by
// ten), Escape lets them go; Enter and Escape end the pen's curve. It reports whether it used the key.
func (a *App) nodeKeys(ad *artDoc, k Key) bool {
	if a.artTool == toolPen && ad.pen != nil && (k.Code == keyEnter || k.Code == keyEscape) {
		a.finishPen(ad)
		return true
	}
	if a.artTool != toolNode {
		return false
	}
	ne := ad.nodeState()
	n, ok := ad.editedVector()
	if !ok || len(ne.sel) == 0 {
		return false
	}
	v := n.Vector.ToCurve()
	step := 1.0
	if k.Shift {
		step = 10
	}
	var d paint.Pt
	switch k.Code {
	case keyEscape:
		ne.sel = nil
		return true
	case keyDelete, keyBack:
		sel := ne.sel
		ne.sel = nil
		a.artRun(ad, func() error {
			s, err := ad.d.BeginVector(n.ID, paint.Vector{}, "", "Delete node")
			if err != nil {
				return err
			}
			s.Set(v.DeleteNodes(sel))
			s.End()
			return nil
		})
		return true
	case keyLeft:
		d.X = -step
	case keyRight:
		d.X = step
	case keyUp:
		d.Y = -step
	case keyDown:
		d.Y = step
	default:
		return false
	}
	// A nudge is in canvas pixels, whatever the object's map.
	inv, ok := v.M.Inverse()
	if !ok {
		return true
	}
	o := inv.Map(paint.Pt{})
	a.artRun(ad, func() error {
		ad.d.SetVector(n.ID, v.MoveNodes(ne.sel, inv.Map(d).Sub(o)), "Move node")
		return nil
	})
	return true
}

// drawVectorOverlay draws the pen's curve and the next segment it would add, or the node tool's object with its
// nodes, the chosen ones' handles and the box being dragged.
func (a *App) drawVectorOverlay(ad *artDoc, vp gfx.Rect) {
	u := &a.u
	switch {
	case a.artTool == toolPen && ad.pen != nil:
		v := ad.pen.s.Vector()
		a.drawOutline(ad, vp, v)
		nodes := v.Path[0].Nodes
		if last := nodes[len(nodes)-1]; u.active == "" && ad.over && !v.Path[0].Closed {
			// The segment the next click would add, bent by the last node's handle.
			p := paint.Pt{X: ad.cursor[0], Y: ad.cursor[1]}
			next := paint.Vector{Kind: paint.VectorCurve, M: paint.Identity, Path: []paint.Subpath{{Nodes: []paint.PathNode{last, sharpNode(p)}}}}
			a.drawOutline(ad, vp, next)
		}
		a.drawNodes(ad, vp, v, nil, true)
	case a.artTool == toolPencil && len(ad.pencil) > 1:
		w := max(1, u.px(1.5))
		for i := 1; i < len(ad.pencil); i++ {
			x0, y0 := ad.toScreen(vp, ad.pencil[i-1])
			x1, y1 := ad.toScreen(vp, ad.pencil[i])
			u.c.Line(x0, y0, x1, y1, w, colAccent)
		}
	case a.artTool == toolNode:
		n, ok := ad.editedVector()
		if !ok {
			return
		}
		ne := ad.nodeState()
		for _, id := range ad.chosenVectors()[1:] { // the others chosen, outlined without their nodes
			a.drawOutline(ad, vp, ad.d.Node(id).Vector.ToCurve())
		}
		v := n.Vector.ToCurve()
		a.drawOutline(ad, vp, v)
		a.drawNodes(ad, vp, v, ne.sel, false)
		if ne.grab == nodeGrabBox {
			x0, y0 := ad.toScreen(vp, ne.box[0])
			x1, y1 := ad.toScreen(vp, ne.box[1])
			box := gfx.Rect{X: min(x0, x1), Y: min(y0, y1), W: max(x0, x1) - min(x0, x1), H: max(y0, y1) - min(y0, y1)}
			u.c.Fill(box, gfx.Alpha(colAccent, 0.12))
			drawAnts(u, box)
		}
	}
}

// drawOutline draws an object's outline as a thin line in the accent colour, over a darker one so it shows on
// any picture.
func (a *App) drawOutline(ad *artDoc, vp gfx.Rect, v paint.Vector) {
	u := &a.u
	w := max(1, u.px(1))
	for si, s := range v.Path {
		for i := range s.Segments() {
			r := paint.NodeRef{Sub: si, Node: i}
			// Enough steps that each is a few screen pixels long.
			a0, b0 := ad.toScreen(vp, pt2(canvasOf(&v, v.PointAt(r, 0))))
			a1, b1 := ad.toScreen(vp, pt2(canvasOf(&v, v.PointAt(r, 1))))
			steps := min(max(int(math.Hypot(float64(a1-a0), float64(b1-b0))/4), 8), 200)
			px, py := a0, b0
			for k := 1; k <= steps; k++ {
				x, y := ad.toScreen(vp, pt2(canvasOf(&v, v.PointAt(r, float64(k)/float64(steps)))))
				u.c.Line(px, py, x, y, w+2, gfx.Alpha(colBlack, 0.35))
				u.c.Line(px, py, x, y, w, colAccent)
				px, py = x, y
			}
		}
	}
}

func pt2(p paint.Pt) [2]float64 { return [2]float64{p.X, p.Y} }

// drawNodes draws a curve's nodes: squares for sharp ones, circles for smooth, filled when chosen, with the chosen
// ones' handles (every node's, for the pen's curve).
func (a *App) drawNodes(ad *artDoc, vp gfx.Rect, v paint.Vector, sel []paint.NodeRef, handles bool) {
	u := &a.u
	w := max(1, u.px(1))
	s := u.px(3.5)
	screen := func(p paint.Pt) (float32, float32) { return ad.toScreen(vp, pt2(canvasOf(&v, p))) }
	for si, sp := range v.Path {
		for ni, nd := range sp.Nodes {
			chosen := slices.Contains(sel, paint.NodeRef{Sub: si, Node: ni})
			if chosen || handles {
				x, y := screen(nd.P)
				for _, h := range []paint.Pt{nd.In, nd.Out} {
					if h == nd.P {
						continue
					}
					hx, hy := screen(h)
					u.c.Line(x, y, hx, hy, w, gfx.Alpha(colAccent, 0.9))
					u.c.Circle(hx, hy, s*0.8+w, colAccent)
					u.c.Circle(hx, hy, s*0.8, colWhite)
				}
			}
		}
	}
	for si, sp := range v.Path {
		for ni, nd := range sp.Nodes {
			x, y := screen(nd.P)
			fill := colWhite
			if slices.Contains(sel, paint.NodeRef{Sub: si, Node: ni}) {
				fill = colAccent
			}
			if nd.Smooth {
				u.c.Circle(x, y, s+w, colAccent)
				u.c.Circle(x, y, s, fill)
				continue
			}
			r := gfx.Rect{X: x - s, Y: y - s, W: 2 * s, H: 2 * s}
			u.c.Fill(r.Inset(-w), colAccent)
			u.c.Fill(r, fill)
		}
	}
}

// drawPenOptions is the pen's and the pencil's options: the stroke's width and whether it fills, with a hint.
func (a *App) drawPenOptions(r gfx.Rect, hint string) {
	u := &a.u
	p := &a.settings.Paint
	x := r.X
	done := false
	if on := u.checkbox("art/pen/fill", gfx.Rect{X: x, Y: r.Y, W: u.px(70), H: r.H}, u.t("Fill"), p.PenFill); on != p.PenFill {
		p.PenFill, done = on, true
	}
	x += u.px(78)
	var d bool
	x, d = a.labelledSlider("art/pen/width", x, r.Y, r.H, u.t("Stroke width"), &p.ShapeWidth, 1, 100, func(v float64) string { return fmt.Sprintf("%.0f px", v) })
	p.ShapeWidth = math.Round(p.ShapeWidth)
	done = done || d
	if x+u.px(200) < r.Right() {
		u.label(u.face(12), gfx.Rect{X: x, Y: r.Y, W: r.Right() - x, H: r.H}, hint, colDim, 0)
	}
	if done {
		a.saveSettings()
	}
}

// drawNodeOptions is the node tool's options: making the chosen nodes sharp or smooth, deleting them, and the
// object's fill and stroke.
func (a *App) drawNodeOptions(ad *artDoc, r gfx.Rect) {
	u := &a.u
	n, ok := ad.editedVector()
	if !ok {
		u.label(u.face(12), r, u.t("Click a vector layer's outline to edit its nodes; the pen and the shape tool make them"), colDim, 0)
		return
	}
	ne := ad.nodeState()
	v := n.Vector.ToCurve()
	x := r.X
	for _, b := range []struct {
		id, hint string
		smooth   bool
	}{
		{"art/node/sharp", u.t("Sharp: the chosen nodes make corners"), false},
		{"art/node/smooth", u.t("Smooth: the chosen nodes' handles stay in line"), true},
	} {
		br := gfx.Rect{X: x, Y: r.Y, W: u.px(30), H: r.H}
		pressed := u.iconButton(b.id, br, "", b.hint, false, len(ne.sel) > 0)
		drawNodeGlyph(u, br, b.smooth, len(ne.sel) > 0)
		if pressed {
			next := v
			for _, ref := range ne.sel {
				next = next.SetSmooth(ref, b.smooth)
			}
			name := "Sharp node"
			if b.smooth {
				name = "Smooth node"
			}
			a.artRun(ad, func() error { ad.d.SetVector(n.ID, next, name); return nil })
		}
		x += u.px(32)
	}
	if u.iconButton("art/node/delete", gfx.Rect{X: x, Y: r.Y, W: u.px(30), H: r.H}, "\ue872", withKey(u.t("Delete the chosen nodes"), "Delete"), false, len(ne.sel) > 0) {
		a.nodeKeys(ad, Key{Code: keyDelete})
	}
	x += u.px(40)
	x = a.drawBoolButtons(ad, x, r)
	a.drawVectorProperties(ad, n, gfx.Rect{X: x, Y: r.Y, W: r.Right() - x, H: r.H})
}

// drawNodeGlyph draws the icon of a sharp node, two straight segments meeting at a square, or of a smooth one, an
// arc through a round node with its handles along it.
func drawNodeGlyph(u *ctx, r gfx.Rect, smooth, enabled bool) {
	col := colText
	if !enabled {
		col = colFaint
	}
	cx, cy, s := r.X+r.W/2, r.Y+r.H/2, u.px(1)
	w := max(1, u.px(1.3))
	if smooth {
		// An arc over the node, flattened, and the handle line tangent to it.
		const steps = 12
		px, py := cx-8*s, cy+5*s
		for i := 1; i <= steps; i++ {
			a := math.Pi * (1 - float64(i)/steps)
			x, y := cx+8*s*float32(math.Cos(a)), cy+5*s-9*s*float32(math.Sin(a))
			u.c.Line(px, py, x, y, w, col)
			px, py = x, y
		}
		u.c.Line(cx-6*s, cy-4*s, cx+6*s, cy-4*s, max(1, u.px(1)), col)
		u.c.Circle(cx-6*s, cy-4*s, 1.6*s, col)
		u.c.Circle(cx+6*s, cy-4*s, 1.6*s, col)
		u.c.Circle(cx, cy-4*s, 2.6*s, col)
		return
	}
	u.c.Line(cx-8*s, cy+5*s, cx, cy-5*s, w, col)
	u.c.Line(cx, cy-5*s, cx+8*s, cy+5*s, w, col)
	u.c.Fill(gfx.Rect{X: cx - 2.5*s, Y: cy - 7.5*s, W: 5 * s, H: 5 * s}, col)
}

// strokeAligns, strokeCaps, strokeJoins and strokeDashes name a stroke's choices, in paint's order.
var (
	strokeAligns = msgs("Centre", "Inside", "Outside")
	strokeCaps   = msgs("Butt cap", "Round cap", "Square cap")
	strokeJoins  = msgs("Miter join", "Round join", "Bevel join")
	strokeDashes = msgs("Solid", "Dashed", "Dotted")
)

// dashOf is a stroke's dash pattern, in stroke widths, by its choice in strokeDashes: dots are dashes too short to
// see, drawn by their round ends.
func dashOf(choice int) []float64 {
	switch choice {
	case 1:
		return []float64{3, 2}
	case 2:
		return []float64{0.01, 2}
	}
	return nil
}

// dashChoice is which of strokeDashes a pattern is.
func dashChoice(d []float64) int {
	switch {
	case len(d) == 0:
		return 0
	case d[0] < 0.1:
		return 2
	}
	return 1
}

// drawVectorProperties edits a vector layer's fill and stroke in the options bar: a swatch takes the foreground
// colour, a box turns the paint on or off, and the stroke's width, alignment, ends, corners and dashes.
func (a *App) drawVectorProperties(ad *artDoc, n *paint.Node, r gfx.Rect) {
	u := &a.u
	v := *n.Vector
	x := r.X
	set := func(next paint.Vector, name string) {
		a.artRun(ad, func() error { ad.d.SetVector(n.ID, next, name); return nil })
	}
	for _, part := range []struct {
		id, label string
		paint     *paint.VectorPaint
		name      string
	}{
		{"fill", u.t("Fill"), &v.Fill, "Fill"},
		{"stroke", u.t("Stroke"), &v.Stroke.Paint, "Stroke"},
	} {
		on := part.paint.Kind != paint.PaintNone
		cw := checkboxWidth(u, part.label)
		if now := u.checkbox("art/vec/"+part.id, gfx.Rect{X: x, Y: r.Y, W: cw, H: r.H}, part.label, on); now != on {
			next := v
			pp := &next.Fill
			if part.id == "stroke" {
				pp = &next.Stroke.Paint
				if next.Stroke.Width == 0 {
					next.Stroke.Width = max(a.settings.Paint.ShapeWidth, 1)
				}
			}
			*pp = paint.VectorPaint{}
			if now {
				*pp = solidPaint(a.brush.Color)
			}
			set(next, part.name)
		}
		x += cw + u.px(4)
		sw := gfx.Rect{X: x, Y: r.Y + u.px(5), W: u.px(20), H: r.H - u.px(10)}
		swid := "art/vec/" + part.id + "/swatch"
		col := part.paint.Color
		if part.paint.Kind != paint.PaintSolid && len(part.paint.Stops) > 0 {
			col = part.paint.Stops[0].Color
		}
		if on {
			u.c.Fill(sw.Inset(-max(1, u.px(1))), colBorder)
			u.c.Fill(sw, color.RGBA{col.R, col.G, col.B, 255})
		} else {
			u.c.RoundOutline(sw, u.px(2), max(1, u.px(1)), colBorder)
			u.c.Line(sw.X, sw.Bottom(), sw.Right(), sw.Y, max(1, u.px(1)), colDanger)
		}
		u.hint(swid, u.tf("%s: click to use the foreground colour", part.label), sw)
		if u.hover(swid, sw) {
			u.cursor = CursorHand
		}
		if u.clicked(swid, sw) {
			next := v
			pp := &next.Fill
			if part.id == "stroke" {
				pp = &next.Stroke.Paint
				if next.Stroke.Width == 0 {
					next.Stroke.Width = max(a.settings.Paint.ShapeWidth, 1)
				}
			}
			*pp = solidPaint(a.brush.Color)
			set(next, part.name)
		}
		x += u.px(30)
	}
	if v.Stroke.Paint.Kind == paint.PaintNone || x+u.px(200) > r.Right() {
		return
	}
	// The width drags live and is recorded as one step when it is let go.
	width := v.Stroke.Width
	x, done := a.labelledSlider("art/vec/width", x, r.Y, r.H, u.t("Stroke width"), &width, 0.5, 200, func(v float64) string { return fmt.Sprintf("%.1f px", v) })
	if width != v.Stroke.Width {
		if ad.props == nil {
			s, err := ad.d.BeginVector(n.ID, paint.Vector{}, "", "Stroke")
			if err != nil {
				a.toast(a.describe(err), true)
				return
			}
			ad.props = s
		}
		next := v
		next.Stroke.Width = width
		ad.touched(ad.props.Set(next))
	}
	if done && ad.props != nil {
		ad.props.End()
		ad.props = nil
		ad.changedAll()
	}
	for _, dd := range []struct {
		id      string
		options []string
		chosen  int
		apply   func(*paint.Vector, int)
	}{
		{"align", strokeAligns, int(v.Stroke.Align), func(n *paint.Vector, i int) { n.Stroke.Align = paint.StrokeAlign(i) }},
		{"cap", strokeCaps, int(v.Stroke.Cap), func(n *paint.Vector, i int) { n.Stroke.Cap = paint.LineCap(i) }},
		{"join", strokeJoins, int(v.Stroke.Join), func(n *paint.Vector, i int) { n.Stroke.Join = paint.LineJoin(i) }},
		{"dash", strokeDashes, dashChoice(v.Stroke.Dash), func(n *paint.Vector, i int) {
			n.Stroke.Dash = dashOf(i)
			if i == 2 {
				n.Stroke.Cap = paint.CapRound
			}
		}},
	} {
		w := u.px(108)
		if x+w > r.Right() {
			break
		}
		a.dropdown("art/vec/"+dd.id, gfx.Rect{X: x, Y: r.Y, W: w, H: r.H}, u.ts(dd.options), dd.chosen, nil, func(i int) {
			next := v
			dd.apply(&next, i)
			set(next, "Stroke")
		})
		x += w + u.px(6)
	}
}

// convertToCurves makes the chosen smart shape a curve, as Layer › Convert to Curves does.
func (a *App) convertToCurves(ad *artDoc) {
	n, ok := ad.editedVector()
	if !ok {
		a.toast(a.describe(paint.ErrNotVector), true)
		return
	}
	if n.Vector.Kind == paint.VectorCurve {
		return
	}
	a.artRun(ad, func() error { ad.d.SetVector(n.ID, n.Vector.ToCurve(), "Convert to curves"); return nil })
}

// togglePicked chooses layer id besides the active one, or lets it go; the active layer stays chosen.
func (ad *artDoc) togglePicked(id int) {
	if id == ad.d.Active {
		return
	}
	if i := slices.Index(ad.picked, id); i >= 0 {
		ad.picked = slices.Delete(ad.picked, i, i+1)
		return
	}
	ad.picked = append(ad.picked, id)
}

// chosenVectors are the chosen vector layers: the active one and those picked besides it that are still there.
func (ad *artDoc) chosenVectors() []int {
	var out []int
	for _, id := range append([]int{ad.d.Active}, ad.picked...) {
		if n := ad.d.Node(id); n != nil && n.Vector != nil && !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	return out
}

// boolNames name the booleans, by paint.BoolOp.
var boolNames = msgsIn("boolean", "Add", "Subtract", "Intersect", "Exclude")

// runBoolean combines the chosen vector layers.
func (a *App) runBoolean(ad *artDoc, op paint.BoolOp) {
	ids := ad.chosenVectors()
	a.artRun(ad, func() error {
		_, err := ad.d.Boolean(op, ids)
		return err
	})
	ad.picked = nil
}

// drawBoolGlyph draws a boolean's icon: two overlapping squares, the back one at the top left, with the area the
// result covers filled.
func drawBoolGlyph(u *ctx, r gfx.Rect, op paint.BoolOp, enabled bool) {
	col := colText
	if !enabled {
		col = colFaint
	}
	s := u.px(1)
	back := gfx.Rect{X: r.X + r.W/2 - 8*s, Y: r.Y + r.H/2 - 8*s, W: 11 * s, H: 11 * s}
	front := gfx.Rect{X: back.X + 5*s, Y: back.Y + 5*s, W: 11 * s, H: 11 * s}
	both := gfx.Rect{X: front.X, Y: front.Y, W: back.Right() - front.X, H: back.Bottom() - front.Y}
	switch op {
	case paint.BoolAdd:
		u.c.Fill(back, col)
		u.c.Fill(front, col)
	case paint.BoolSubtract:
		u.c.Fill(back, col)
		u.c.Fill(both, colPanel)
	case paint.BoolIntersect:
		u.c.Fill(both, col)
	case paint.BoolExclude:
		u.c.Fill(back, col)
		u.c.Fill(front, col)
		u.c.Fill(both, colPanel)
	}
	w := max(1, u.px(1))
	u.c.RoundOutline(back, 0, w, col)
	u.c.RoundOutline(front, 0, w, col)
}

// drawBoolButtons are the booleans' buttons, enabled when two or more vector layers are chosen; it returns where
// the next control goes.
func (a *App) drawBoolButtons(ad *artDoc, x float32, r gfx.Rect) float32 {
	u := &a.u
	on := len(ad.chosenVectors()) >= 2
	for i, name := range boolNames {
		br := gfx.Rect{X: x, Y: r.Y, W: u.px(30), H: r.H}
		hint := u.tf("%s: combine the chosen vector layers (Ctrl+click layers to choose more)", u.tc("boolean", name))
		pressed := u.iconButton(fmt.Sprintf("art/bool/%d", i), br, "", hint, false, on)
		drawBoolGlyph(u, br, paint.BoolOp(i), on)
		if pressed {
			a.runBoolean(ad, paint.BoolOp(i))
		}
		x += u.px(32)
	}
	return x + u.px(8)
}
