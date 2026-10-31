package ui

import (
	"image/color"
	"math"

	"github.com/btelolabs/btelo-forge/internal/gfx"
	s3 "github.com/btelolabs/btelo-forge/internal/scene3d"
)

// The handles over what is chosen: an arrow along each axis moves it, the small box on the arrow scales it, and the ring
// around the axis turns it. Their sizes are logical pixels, the same at any distance.
const (
	gizmoArrow = 96.0
	gizmoBox   = .68 // how far along the arrow the box is
	gizmoRing  = 1.32
	gizmoReach = 8.0 // how close the pointer has to be
	gizmoSteps = 48
	axesInset  = 44.0 // the corner's axes, from the corner of the view
	axesLength = 26.0
)

var gizmoColors = [3]color.RGBA{gfx.Hex(0xe5484d), gfx.Hex(0x6cbf43), gfx.Hex(0x4a90e2)}

// gizmoHandle is the part of the handles under the pointer.
type gizmoHandle struct {
	kind modalKind
	axis int
}

// gizmoPoints is the handles of axis on the screen, in the view's own pixels: the arrow's tip and the ring.
func (a *App) gizmoPoints(md *modelDoc, r gfx.Rect, axis int) (x0, y0, x1, y1 float32, ring [][2]float32, ok bool) {
	cam := md.cam
	depth := max(md.pivot.Sub(cam.Pos).Length(), 1e-4)
	size := 2 * float32(math.Tan(float64(cam.FovY)/2)) * depth / r.H * a.u.px(gizmoArrow)
	x0, y0, ok = cam.Project(md.pivot, r.W, r.H)
	if !ok {
		return
	}
	var dir, u, v s3.Vec3
	dir[axis], u[(axis+1)%3], v[(axis+2)%3] = 1, 1, 1
	x1, y1, ok = cam.Project(md.pivot.Add(dir.Scale(size)), r.W, r.H)
	if !ok {
		return
	}
	_, _, ahead := cam.Axes()
	for i := range gizmoSteps + 1 {
		s, c := math.Sincos(float64(i) / gizmoSteps * 2 * math.Pi)
		offset := u.Scale(float32(c)).Add(v.Scale(float32(s))).Scale(size * gizmoRing)
		// The far half would run through the near one.
		if offset.Dot(ahead) > 0 {
			ring = append(ring, [2]float32{-1, -1})
			continue
		}
		if px, py, on := cam.Project(md.pivot.Add(offset), r.W, r.H); on {
			ring = append(ring, [2]float32{px, py})
		}
	}
	return x0, y0, x1, y1, ring, true
}

func distanceToSegment(px, py, ax, ay, bx, by float32) float32 {
	dx, dy := bx-ax, by-ay
	t := float32(0)
	if l := dx*dx + dy*dy; l > 0 {
		t = min(max(((px-ax)*dx+(py-ay)*dy)/l, 0), 1)
	}
	return float32(math.Hypot(float64(px-ax-t*dx), float64(py-ay-t*dy)))
}

// gizmoAt finds the handle under the point (x, y) of the view r.
func (a *App) gizmoAt(md *modelDoc, r gfx.Rect, x, y float32) (gizmoHandle, bool) {
	reach := a.u.px(gizmoReach)
	best, found := reach, gizmoHandle{}
	any := false
	take := func(d float32, h gizmoHandle) {
		if d < best {
			best, found, any = d, h, true
		}
	}
	for axis := range 3 {
		x0, y0, x1, y1, ring, ok := a.gizmoPoints(md, r, axis)
		if !ok {
			continue
		}
		bx, by := x0+(x1-x0)*gizmoBox, y0+(y1-y0)*gizmoBox
		take(float32(math.Hypot(float64(x-bx), float64(y-by)))-reach/2, gizmoHandle{modalScale, axis})
		take(distanceToSegment(x, y, x0, y0, x1, y1), gizmoHandle{modalMove, axis})
		for i := 1; i < len(ring); i++ {
			if ring[i-1][0] >= 0 && ring[i][0] >= 0 {
				take(distanceToSegment(x, y, ring[i-1][0], ring[i-1][1], ring[i][0], ring[i][1]), gizmoHandle{modalRotate, axis})
			}
		}
	}
	return found, any
}

// gizmoShown reports whether the handles have something to work on.
func (a *App) gizmoShown(md *modelDoc) bool {
	return md.hasPivot && (md.clip < 0 || md.bone >= 0) && !md.tooling() && !a.modelHolding(md)
}

// gizmoPress starts the tool of the handle pressed, which goes on until the button is let go.
func (a *App) gizmoPress(md *modelDoc, r gfx.Rect) {
	u := &a.u
	if !u.pressed || !a.gizmoShown(md) || !r.Contains(u.pressX, u.pressY) {
		return
	}
	h, ok := a.gizmoAt(md, r, u.pressX-r.X, u.pressY-r.Y)
	if !ok {
		return
	}
	u.pressed = false
	if md.edit != nil {
		a.tool(md, h.kind)
		if m := md.edit.modal; m != nil {
			m.axis, m.held = h.axis, true
		}
		return
	}
	a.startObjectTool(md, h.kind)
	if t := md.objectTool; t != nil {
		t.axis, t.held = h.axis, true
	}
}

// drawGizmo draws the handles over the view r, and the world's axes in its corner.
func (a *App) drawGizmo(md *modelDoc, r gfx.Rect) {
	u := &a.u
	u.c.Push(r)
	defer u.c.Pop()
	a.drawViewAxes(md, r)
	if !a.gizmoShown(md) {
		return
	}
	over, hovering := gizmoHandle{}, false
	if !u.blocked && r.Contains(u.mx, u.my) {
		over, hovering = a.gizmoAt(md, r, u.mx-r.X, u.my-r.Y)
	}
	wide, thin := u.px(2.5), u.px(1.75)
	for axis := range 3 {
		x0, y0, x1, y1, ring, ok := a.gizmoPoints(md, r, axis)
		if !ok {
			continue
		}
		col := func(kind modalKind) color.RGBA {
			if hovering && over.axis == axis && over.kind == kind {
				return colWhite
			}
			return gizmoColors[axis]
		}
		for i := 1; i < len(ring); i++ {
			if ring[i-1][0] >= 0 && ring[i][0] >= 0 {
				u.c.Line(r.X+ring[i-1][0], r.Y+ring[i-1][1], r.X+ring[i][0], r.Y+ring[i][1], thin, col(modalRotate))
			}
		}
		ax, ay, bx, by := r.X+x0, r.Y+y0, r.X+x1, r.Y+y1
		u.c.Line(ax, ay, bx, by, wide, col(modalMove))
		dx, dy := bx-ax, by-ay
		length := float32(math.Hypot(float64(dx), float64(dy)))
		if length < 1 {
			continue
		}
		dx, dy = dx/length, dy/length
		head, side := u.px(13), u.px(5.5)
		u.c.Triangle(bx+dx*head, by+dy*head, bx-dy*side, by+dx*side, bx+dy*side, by-dx*side, col(modalMove))
		cx, cy := ax+(bx-ax)*gizmoBox, ay+(by-ay)*gizmoBox
		half := u.px(4.5)
		u.c.Fill(rectAt(cx-half, cy-half, half*2, half*2), col(modalScale))
	}
}

// drawViewAxes shows which way the world's axes lie, in the view's top right corner.
func (a *App) drawViewAxes(md *modelDoc, r gfx.Rect) {
	u := &a.u
	right, up, _ := md.cam.Axes()
	cx, cy := r.Right()-u.px(axesInset), r.Y+u.px(axesInset)
	f := u.bold(11)
	for axis, name := range []string{"X", "Y", "Z"} {
		var d s3.Vec3
		d[axis] = 1
		x, y := cx+d.Dot(right)*u.px(axesLength), cy-d.Dot(up)*u.px(axesLength)
		u.c.Line(cx, cy, x, y, u.px(2), gizmoColors[axis])
		u.c.Circle(x, y, u.px(7.5), gizmoColors[axis])
		u.label(f, rectAt(x-u.px(8), y-u.px(8), u.px(16), u.px(16)), name, colWhite, .5)
	}
}
