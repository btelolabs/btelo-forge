package ui

import (
	"image/color"
	"math"
	"testing"

	"github.com/btelolabs/btelo-forge/internal/gfx"

	"github.com/btelolabs/btelo-forge/internal/paint"
)

// clickCanvas clicks canvas point (cx, cy), a double-click when double.
func (x *harness) clickCanvas(ad *artDoc, cx, cy float64, double bool) {
	px, py := x.viewPoint(ad, cx, cy)
	x.mx, x.my = px, py
	x.frame(Input{})
	x.frame(Input{Down: true, Pointer: []Pointer{{X: px, Y: py, Down: true, Double: double}}})
	x.frame(Input{Pointer: []Pointer{{X: px, Y: py}}})
}

func TestCanvasPenAndNodeTool(t *testing.T) {
	x, ad := canvasHarness(t)
	a := x.a
	d := ad.d
	a.brush.Color = color.NRGBA{R: 255, A: 255}
	a.settings.Paint.ShapeWidth, a.settings.Paint.PenFill = 6, false

	// P picks the pen: two clicks and a drag, then a click on the first node closes the curve.
	x.key('P', false)
	if a.artTool != toolPen {
		t.Fatalf("P chose tool %d", a.artTool)
	}
	before := len(d.Layers())
	x.clickCanvas(ad, 60, 60, false)
	x.clickCanvas(ad, 200, 60, false)
	x.dragWith(ad, [2]float64{200, 200}, [2]float64{260, 200}, false, false) // a smooth node, pulled to the right
	if ad.pen == nil {
		t.Fatal("the pen's curve ended early")
	}
	x.frame(Input{})
	x.shot("canvas-pen-drawing")
	x.clickCanvas(ad, 60, 60, false)
	if ad.pen != nil || len(d.Layers()) != before+1 || d.UndoName() != "Pen" {
		t.Fatalf("closing: pen %v, %d layers, %q", ad.pen != nil, len(d.Layers()), d.UndoName())
	}
	v := d.Node(d.Active).Vector
	if v == nil || len(v.Path) != 1 || len(v.Path[0].Nodes) != 3 || !v.Path[0].Closed {
		t.Fatalf("the curve %+v", v)
	}
	if n := v.Path[0].Nodes[2]; !n.Smooth || math.Abs(n.Out.X-260) > 1 || math.Abs(n.In.X-140) > 1 {
		t.Errorf("the dragged node %+v", n)
	}
	// The stroke is drawn along the first segment, with the foreground colour.
	if c := ad.look(130, 60); c.R < 250 || c.G > 5 {
		t.Errorf("the curve's stroke %v", c)
	}
	// One undo step takes the whole curve away.
	d.Undo()
	if len(d.Layers()) != before {
		t.Fatalf("undo left %d layers", len(d.Layers()))
	}
	d.Redo()
	ad.changedAll()

	// Enter ends an open curve; one with a single node leaves nothing.
	x.clickCanvas(ad, 300, 250, false)
	x.key(keyEnter, false)
	if ad.pen != nil || len(d.Layers()) != before+1 || d.UndoName() != "Pen" {
		t.Fatalf("a single node: %d layers, %q", len(d.Layers()), d.UndoName())
	}
	curve := d.Active

	// A, the node tool: drag the corner at (200, 60) down by 30.
	x.key('A', false)
	if a.artTool != toolNode {
		t.Fatalf("A chose tool %d", a.artTool)
	}
	x.dragWith(ad, [2]float64{200, 60}, [2]float64{200, 90}, false, false)
	v = d.Node(curve).Vector
	if p := v.Path[0].Nodes[1].P; math.Abs(p.Y-90) > 0.5 || d.UndoName() != "Move node" {
		t.Fatalf("the node moved to %v (%q)", p, d.UndoName())
	}
	if len(ad.nodes.sel) != 1 || ad.nodes.sel[0] != (paint.NodeRef{Node: 1}) {
		t.Errorf("chosen nodes %v", ad.nodes.sel)
	}
	// The smooth node's out handle, dragged with Alt, breaks it; the in handle stays.
	x.clickCanvas(ad, 200, 200, false)
	in := d.Node(curve).Vector.Path[0].Nodes[2].In
	x.dragWith(ad, [2]float64{260, 200}, [2]float64{250, 250}, false, true)
	n := d.Node(curve).Vector.Path[0].Nodes[2]
	if n.Smooth || n.In != in || math.Abs(n.Out.Y-250) > 0.5 || d.UndoName() != "Move handle" {
		t.Errorf("after Alt-dragging the handle: %+v (%q)", n, d.UndoName())
	}
	// A double-click makes it smooth again.
	x.clickCanvas(ad, 200, 200, true)
	if n := d.Node(curve).Vector.Path[0].Nodes[2]; !n.Smooth || d.UndoName() != "Smooth node" {
		t.Errorf("a double-click left %+v (%q)", n, d.UndoName())
	}
	// A click on the outline adds a node there; Delete takes it out again.
	x.clickCanvas(ad, 130, 75, false) // on the segment from (60, 60) to (200, 90)
	if got := len(d.Node(curve).Vector.Path[0].Nodes); got != 4 || d.UndoName() != "Add node" {
		t.Fatalf("after a click on the outline: %d nodes (%q)", got, d.UndoName())
	}
	x.frame(Input{})
	x.shot("canvas-node-tool")
	x.key(keyDelete, false)
	if got := len(d.Node(curve).Vector.Path[0].Nodes); got != 3 || d.UndoName() != "Delete node" {
		t.Errorf("after Delete: %d nodes (%q)", got, d.UndoName())
	}
	// A box over nothing chooses the nodes in it; the arrows nudge them.
	x.dragWith(ad, [2]float64{40, 40}, [2]float64{220, 100}, false, false)
	if len(ad.nodes.sel) != 2 {
		t.Fatalf("the box chose %v", ad.nodes.sel)
	}
	x.frame(Input{Keys: []Key{{Code: keyRight, Shift: true}}})
	if p := d.Node(curve).Vector.Path[0].Nodes[0].P; math.Abs(p.X-70) > 0.01 {
		t.Errorf("Shift+Right moved the first node to %v", p)
	}

	// The node tool edits a rectangle from the shape tool too, which becomes a curve.
	x.key('U', false)
	a.settings.Paint.ShapeKind, a.settings.Paint.ShapeStroke = shapeRect, false
	x.dragWith(ad, [2]float64{300, 50}, [2]float64{380, 130}, false, false)
	rect := d.Active
	x.key('A', false)
	x.dragWith(ad, [2]float64{380, 130}, [2]float64{390, 160}, false, false)
	if v := d.Node(rect).Vector; v.Kind != paint.VectorCurve || len(v.Path[0].Nodes) != 4 {
		t.Fatalf("the edited rectangle %+v", v)
	}
	if c := ad.look(384, 145); c.R < 250 {
		t.Errorf("the pulled corner is not filled: %v", c)
	}
	// A click on another object chooses it.
	x.clickCanvas(ad, 130, 75, false)
	if d.Active != curve {
		t.Errorf("clicking the curve chose %d, want %d", d.Active, curve)
	}
	// The options bar's stroke: dashed, recorded as a stroke step.
	dash := d.Node(curve).Vector.Stroke
	dash.Dash = dashOf(1)
	next := *d.Node(curve).Vector
	next.Stroke = dash
	d.SetVector(curve, next, "Stroke")
	ad.changedAll()
	x.frame(Input{})
	x.shot("canvas-node-dashed")
}

func TestCanvasShapeShowsWhileDragged(t *testing.T) {
	x, ad := canvasHarness(t)
	a := x.a
	d := ad.d
	a.brush.Color = color.NRGBA{R: 255, A: 255}
	x.key('U', false)
	a.settings.Paint.ShapeKind, a.settings.Paint.ShapeFill, a.settings.Paint.ShapeStroke = shapeStar, true, false
	before := len(d.Layers())
	fx, fy := x.viewPoint(ad, 100, 50)
	tx, ty := x.viewPoint(ad, 300, 250)
	x.mx, x.my = fx, fy
	x.frame(Input{})
	x.frame(Input{Down: true, Pointer: []Pointer{{X: fx, Y: fy, Down: true}}})
	for i := 1; i <= 4; i++ {
		x.mx, x.my = fx+(tx-fx)*float32(i)/4, fy+(ty-fy)*float32(i)/4
		x.frame(Input{Down: true})
	}
	// Mid-drag the star is already drawn, on its own layer, though nothing is recorded yet.
	if len(d.Layers()) != before+1 || ad.look(200, 150).R < 250 {
		t.Fatalf("mid-drag: %d layers, centre %v", len(d.Layers()), ad.look(200, 150))
	}
	if steps, _ := d.History(); len(steps) != 0 {
		t.Errorf("recorded before the drag ended: %v", steps)
	}
	x.frame(Input{Down: true})
	x.shot("canvas-shape-dragging")
	x.frame(Input{Pointer: []Pointer{{X: tx, Y: ty}}})
	if steps, _ := d.History(); len(steps) != 1 || steps[0] != "Add shape" {
		t.Errorf("after the drag: %v", steps)
	}
	// A drag that comes back to where it began leaves nothing.
	x.dragWith(ad, [2]float64{50, 50}, [2]float64{50.5, 50}, false, false)
	if len(d.Layers()) != before+1 {
		t.Errorf("a click left a layer: %d", len(d.Layers()))
	}
}

// barArea is where the options bar puts the chosen tool's options, as drawArtBar lays it out.
func (x *harness) barArea() gfx.Rect {
	u := &x.a.u
	l := x.a.artLayout(x.a.layout().body)
	ax := l.bar.X + u.px(12) + u.px(30) + u.px(10) + u.px(12)
	return gfx.Rect{X: ax, Y: l.bar.Y + u.px(7), W: l.bar.Right() - ax - u.px(12), H: u.px(30)}
}

func TestCanvasBooleans(t *testing.T) {
	x, ad := canvasHarness(t)
	a := x.a
	d := ad.d
	a.brush.Color = color.NRGBA{R: 255, A: 255}
	x.key('U', false)
	a.settings.Paint.ShapeKind, a.settings.Paint.ShapeFill, a.settings.Paint.ShapeStroke = shapeRect, true, false
	x.dragWith(ad, [2]float64{60, 60}, [2]float64{200, 200}, false, false)
	a.settings.Paint.ShapeKind = shapeEllipse
	x.dragWith(ad, [2]float64{140, 140}, [2]float64{280, 280}, false, false)
	circle := d.Active
	before := len(d.Layers())
	// The node tool: a click chooses the square, Shift+click the circle besides it.
	x.key('A', false)
	x.clickCanvas(ad, 80, 80, false)
	square := d.Active
	if square == circle {
		t.Fatal("the click did not choose the square")
	}
	px, py := x.viewPoint(ad, 260, 210)
	x.clickWith(gfx.Rect{X: px - 1, Y: py - 1, W: 2, H: 2}, false, true)
	if len(ad.chosenVectors()) != 2 {
		t.Fatalf("chosen %v", ad.chosenVectors())
	}
	x.frame(Input{})
	x.shot("canvas-boolean-chosen")
	// The subtract button: the square, bottom, less the circle.
	bar := x.barArea()
	u := &a.u
	sub := gfx.Rect{X: bar.X + u.px(32*2+40) + u.px(32)*float32(paint.BoolSubtract), Y: bar.Y, W: u.px(30), H: bar.H}
	x.click(sub)
	if len(d.Layers()) != before-1 || d.UndoName() != "Boolean" || d.Node(circle) != nil {
		t.Fatalf("after subtracting: %d layers, %q", len(d.Layers()), d.UndoName())
	}
	if c := ad.look(100, 100); c.R < 250 {
		t.Errorf("the square's corner is gone: %v", c)
	}
	if c := ad.look(190, 190); c != (color.RGBA{255, 255, 255, 255}) {
		t.Errorf("the bite is still filled: %v", c)
	}
	x.frame(Input{})
	x.shot("canvas-boolean-subtracted")
}

func TestCanvasSnapping(t *testing.T) {
	x, ad := canvasHarness(t)
	a := x.a
	d := ad.d
	red := paint.VectorPaint{Kind: paint.PaintSolid, Color: color.NRGBA{R: 255, A: 255}}
	target := d.AddVector(paint.Vector{Kind: paint.VectorRect, Size: paint.Pt{X: 100, Y: 80}, M: paint.Translate(200, 100), Fill: red}, "Target")
	moving := d.AddVector(paint.Vector{Kind: paint.VectorRect, Size: paint.Pt{X: 40, Y: 40}, M: paint.Translate(40, 40), Fill: red}, "Moving")
	ad.changedAll()
	x.frame(Input{})
	// The node tool drags the moving square's corner to within a few pixels of the target's corner: it lands on it.
	x.key('A', false)
	x.dragWith(ad, [2]float64{80, 80}, [2]float64{198.4, 101.6}, false, false)
	at := func(id, node int) paint.Pt { v := d.Node(id).Vector; return v.M.Map(v.Path[0].Nodes[node].P) }
	if p := at(moving.ID, 2); p != (paint.Pt{X: 200, Y: 100}) {
		t.Errorf("the corner snapped to %v, want (200, 100)", p)
	}
	// Near only an edge, it snaps that way alone: x to the target's right edge, y left as dragged.
	x.dragWith(ad, [2]float64{40, 40}, [2]float64{302, 30}, false, false)
	if p := at(moving.ID, 0); p.X != 300 || math.Abs(p.Y-30) > 0.01 {
		t.Errorf("the corner snapped to %v, want x 300 and y 30", p)
	}
	// The pen's node lands on the target's centre.
	x.key('P', false)
	x.clickCanvas(ad, 251.5, 141, false)
	x.clickCanvas(ad, 380, 20, false)
	x.key(keyEscape, false)
	if ad.pen != nil {
		t.Fatal("Escape did not end the pen")
	}
	if p := at(d.Active, 0); p != (paint.Pt{X: 250, Y: 140}) {
		t.Errorf("the pen's node landed at %v, want the centre (250, 140)", p)
	}
	// Snapping off leaves points where they are dropped.
	a.settings.Paint.NoSnap = true
	x.key('A', false)
	d.Choose(target.ID)
	x.dragWith(ad, [2]float64{300, 180}, [2]float64{304.5, 183.5}, false, false)
	if v := d.Node(target.ID).Vector; v.Kind != paint.VectorCurve {
		t.Fatalf("the target was not edited: active %d, %+v", d.Active, ad.nodes)
	}
	if p := at(target.ID, 2); math.Abs(p.X-304.5) > 0.01 || math.Abs(p.Y-183.5) > 0.01 {
		t.Errorf("with snapping off the corner went to %v", p)
	}
}

func TestCanvasPencil(t *testing.T) {
	x, ad := canvasHarness(t)
	a := x.a
	d := ad.d
	a.brush.Color = color.NRGBA{B: 255, A: 255}
	a.settings.Paint.ShapeWidth = 4
	x.key('N', false)
	if a.artTool != toolPencil {
		t.Fatalf("N chose tool %d", a.artTool)
	}
	before := len(d.Layers())
	// A wavy line, drawn point by point with a slight tremor.
	var pts [][2]float64
	for i := 0; i <= 60; i++ {
		px := 40 + float64(i)*5
		pts = append(pts, [2]float64{px, 150 + 50*math.Sin(px/40) + 0.4*math.Sin(float64(i)*2.7)})
	}
	sx, sy := x.viewPoint(ad, pts[0][0], pts[0][1])
	x.mx, x.my = sx, sy
	x.frame(Input{})
	x.frame(Input{Down: true, Pointer: []Pointer{{X: sx, Y: sy, Down: true}}})
	for _, p := range pts[1:] {
		x.mx, x.my = x.viewPoint(ad, p[0], p[1])
		x.frame(Input{Down: true})
	}
	x.frame(Input{Down: true})
	x.shot("canvas-pencil-drawing")
	x.frame(Input{Pointer: []Pointer{{X: x.mx, Y: x.my}}})
	if len(d.Layers()) != before+1 || d.UndoName() != "Pencil" {
		t.Fatalf("after the pencil: %d layers, %q", len(d.Layers()), d.UndoName())
	}
	v := d.Node(d.Active).Vector
	if v == nil || v.Path[0].Closed || len(v.Path[0].Nodes) > 12 || v.Fill.Kind != paint.PaintNone {
		t.Fatalf("the pencil's curve %+v", v)
	}
	// The curve follows the line within the tremor and the fit.
	for _, p := range pts {
		if _, _, dist := v.NearestOnSegment(paint.Pt{X: p[0], Y: p[1]}); dist > 2 {
			t.Errorf("the curve is %.2f from %v", dist, p)
		}
	}
	x.frame(Input{})
	x.shot("canvas-pencil")
}
