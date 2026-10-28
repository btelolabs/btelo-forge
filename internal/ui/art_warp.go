package ui

import (
	"fmt"
	"math"

	"github.com/btelolabs/btelo-video-cut/internal/gfx"
	"github.com/btelolabs/btelo-video-cut/internal/paint"
)

// Skew, Distort, Perspective and Warp: Free Transform's other modes, as Photoshop's Edit › Transform has them. The
// first three move the corners of a quadrilateral (Skew slides a side along itself, Perspective moves the corner
// across an edge with it, Distort moves one freely); Warp moves the 16 points of a Bézier grid. A corner dragged
// with Ctrl in Free Transform distorts, as in Photoshop. Once the content is no longer a box, it stays in these
// modes until applied.

const (
	xfFree = iota
	xfSkew
	xfDistort
	xfPerspective
	xfWarp
)

var xfModeNames = msgs("Free transform", "Skew", "Distort", "Perspective", "Warp")

// xfSteps are the history's names for the modes.
var xfSteps = []string{"Free transform", "Skew", "Distort", "Perspective", "Warp"}

// warpCells is how finely the warp surface is drawn: enough that its curves look smooth.
const warpCells = 24

// quadOf is the transform's box as a quadrilateral: top left, top right, bottom right, bottom left.
func quadOf(b xbox) [4][2]float64 {
	return [4][2]float64{b.at(-1, -1), b.at(1, -1), b.at(1, 1), b.at(-1, 1)}
}

func ptOf(p [2]float64) paint.Pt { return paint.Pt{X: p[0], Y: p[1]} }

// setTransformMode starts Free Transform if it is not under way and goes to mode, carrying the shape over.
func (a *App) setTransformMode(ad *artDoc, mode int) {
	if ad.xf == nil {
		a.beginTransform(ad)
	}
	xf := ad.xf
	if xf == nil || xf.mode == mode || xf.mode == xfWarp {
		return // a warp stays a warp until it is applied
	}
	if xf.mode == xfFree {
		xf.quad = quadOf(xf.box)
	}
	if mode == xfWarp {
		// The grid's points a third of the way along the quadrilateral, each way.
		q := xf.quad
		for r := range 4 {
			for c := range 4 {
				u, v := float64(c)/3, float64(r)/3
				top := [2]float64{q[0][0] + (q[1][0]-q[0][0])*u, q[0][1] + (q[1][1]-q[0][1])*u}
				bottom := [2]float64{q[3][0] + (q[2][0]-q[3][0])*u, q[3][1] + (q[2][1]-q[3][1])*u}
				xf.warp[r*4+c] = [2]float64{top[0] + (bottom[0]-top[0])*v, top[1] + (bottom[1]-top[1])*v}
			}
		}
	}
	xf.mode, xf.pt = mode, -1
	a.showTransform(ad)
}

// meshOf is the mesh the transform's mode and points make.
func (xf *transformEdit) meshOf() paint.Mesh {
	if xf.mode == xfWarp {
		var g [16]paint.Pt
		for i, p := range xf.warp {
			g[i] = ptOf(p)
		}
		return paint.WarpMesh(g, warpCells)
	}
	return paint.QuadMesh([4]paint.Pt{ptOf(xf.quad[0]), ptOf(xf.quad[1]), ptOf(xf.quad[2]), ptOf(xf.quad[3])})
}

// meshPoints are the points the mode's handles are on: the corners and, for Skew, the sides' middles; or the warp
// grid's points.
func (xf *transformEdit) meshPoints() [][2]float64 {
	if xf.mode == xfWarp {
		return xf.warp[:]
	}
	pts := xf.quad[:]
	if xf.mode == xfSkew {
		mid := func(a, b [2]float64) [2]float64 { return [2]float64{(a[0] + b[0]) / 2, (a[1] + b[1]) / 2} }
		q := xf.quad
		pts = append(pts[:4:4], mid(q[0], q[1]), mid(q[1], q[2]), mid(q[2], q[3]), mid(q[3], q[0]))
	}
	return pts
}

// grabPoint is the handle under the pointer, or -1.
func (a *App) grabPoint(ad *artDoc, vp gfx.Rect) int {
	u := &a.u
	reach := u.px(8)
	for i, p := range ad.xf.meshPoints() {
		x, y := ad.toScreen(vp, p)
		if dx, dy := u.mx-x, u.my-y; dx*dx+dy*dy <= reach*reach {
			return i
		}
	}
	return -1
}

// meshPointer drags a mode's handles; a press away from them moves the whole shape, and a double click applies it.
func (a *App) meshPointer(ad *artDoc, vp gfx.Rect) {
	u := &a.u
	const id = "art/view"
	xf := ad.xf
	if u.hover(id, vp) {
		u.cursor = CursorMove
		if a.grabPoint(ad, vp) >= 0 {
			u.cursor = CursorCross
		}
	}
	if u.press(id, vp) {
		if u.double {
			u.active = ""
			a.applyTransform(ad)
			return
		}
		xf.pt, xf.from, xf.startQuad, xf.startWarp = a.grabPoint(ad, vp), ad.cursor, xf.quad, xf.warp
	}
	if u.dragging(id) {
		a.dragMeshPoint(ad, ad.cursor[0]-xf.from[0], ad.cursor[1]-xf.from[1])
	}
	if u.letGo(id) {
		xf.pt = -1
	}
	a.showTransform(ad)
}

// dragMeshPoint moves the held handle (or, with none, the whole shape) by (dx, dy) from where the drag began.
func (a *App) dragMeshPoint(ad *artDoc, dx, dy float64) {
	xf := ad.xf
	move := func(p [2]float64, x, y float64) [2]float64 { return [2]float64{p[0] + x, p[1] + y} }
	if xf.mode == xfWarp {
		xf.warp = xf.startWarp
		if xf.pt < 0 {
			for i := range xf.warp {
				xf.warp[i] = move(xf.warp[i], dx, dy)
			}
			return
		}
		xf.warp[xf.pt] = move(xf.warp[xf.pt], dx, dy)
		return
	}
	xf.quad = xf.startQuad
	q := &xf.quad
	switch {
	case xf.pt < 0:
		for i := range q {
			q[i] = move(q[i], dx, dy)
		}
	case xf.pt >= 4:
		// Skew: a side slides along itself, both its corners together.
		side := xf.pt - 4
		i, j := side, (side+1)%4
		ex, ey := q[j][0]-q[i][0], q[j][1]-q[i][1]
		l := math.Hypot(ex, ey)
		if l == 0 {
			return
		}
		along := (dx*ex + dy*ey) / l
		ox, oy := ex/l*along, ey/l*along
		q[i], q[j] = move(q[i], ox, oy), move(q[j], ox, oy)
	case xf.mode == xfPerspective:
		// The corner moves along one edge, and the corner across that edge moves the other way, keeping the shape
		// symmetrical as a view in perspective is.
		i := xf.pt
		if math.Abs(dx) >= math.Abs(dy) {
			partner := map[int]int{0: 1, 1: 0, 2: 3, 3: 2}[i] // the other end of the top or bottom edge
			q[i], q[partner] = move(q[i], dx, 0), move(q[partner], -dx, 0)
		} else {
			partner := map[int]int{0: 3, 3: 0, 1: 2, 2: 1}[i] // the other end of the left or right side
			q[i], q[partner] = move(q[i], 0, dy), move(q[partner], 0, -dy)
		}
	default:
		q[xf.pt] = move(q[xf.pt], dx, dy)
	}
}

// drawMeshOverlay outlines the mode's shape with its handles: the quadrilateral, or the warp surface's grid with
// its control points and the lines between them.
func (a *App) drawMeshOverlay(ad *artDoc, vp gfx.Rect) {
	u := &a.u
	xf := ad.xf
	w := max(1, u.px(1))
	line := func(p, q [2]float64) {
		x0, y0 := ad.toScreen(vp, p)
		x1, y1 := ad.toScreen(vp, q)
		u.c.Line(x0, y0, x1, y1, w+2, gfx.Alpha(colBlack, 0.5))
		u.c.Line(x0, y0, x1, y1, w, colWhite)
	}
	if xf.mode == xfWarp {
		m := xf.meshOf()
		at := func(i, j int) [2]float64 { p := m.Pts[j*(m.Cols+1)+i]; return [2]float64{p.X, p.Y} }
		// The surface's thirds, as Photoshop's warp grid shows them.
		for k := 0; k <= 3; k++ {
			idx := k * warpCells / 3
			for s := range warpCells {
				line(at(s, idx), at(s+1, idx))
				line(at(idx, s), at(idx, s+1))
			}
		}
		for r := range 4 {
			for c := range 4 {
				if c < 3 && (r == 0 || r == 3) {
					drawGuide(u, ad, vp, xf.warp[r*4+c], xf.warp[r*4+c+1])
				}
				if r < 3 && (c == 0 || c == 3) {
					drawGuide(u, ad, vp, xf.warp[r*4+c], xf.warp[(r+1)*4+c])
				}
			}
		}
	} else {
		for i := range 4 {
			line(xf.quad[i], xf.quad[(i+1)%4])
		}
	}
	s := u.px(4)
	for _, p := range xf.meshPoints() {
		x, y := ad.toScreen(vp, p)
		r := gfx.Rect{X: x - s, Y: y - s, W: 2 * s, H: 2 * s}
		u.c.Fill(r.Inset(-w), colBlack)
		u.c.Fill(r, colWhite)
	}
}

// drawGuide is a thin line from a warp point to the next, as the handles' lines are drawn.
func drawGuide(u *ctx, ad *artDoc, vp gfx.Rect, p, q [2]float64) {
	x0, y0 := ad.toScreen(vp, p)
	x1, y1 := ad.toScreen(vp, q)
	u.c.Line(x0, y0, x1, y1, max(1, u.px(1)), gfx.Alpha(colAccent, 0.7))
}

// drawTransformModes are the mode buttons at the start of Free Transform's options; it returns where the rest go.
func (a *App) drawTransformModes(ad *artDoc, x float32, r gfx.Rect) float32 {
	u := &a.u
	xf := ad.xf
	for i, name := range xfModeNames {
		label := u.t(name)
		w := u.buttonWidth("", label)
		br := gfx.Rect{X: x, Y: r.Y, W: w, H: r.H}
		// Free Transform cannot come back once the shape is no longer a box, nor a quadrilateral from a warp.
		enabled := i == xf.mode || i != xfFree && xf.mode != xfWarp
		kind := ghost
		if i == xf.mode {
			kind = plain
		}
		if u.button(fmt.Sprintf("art/xf/mode/%d", i), br, "", label, kind, enabled) && i != xf.mode {
			a.setTransformMode(ad, i)
		}
		x += w + u.px(4)
	}
	return x + u.px(8)
}
