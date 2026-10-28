package ui

import (
	"image"
	"math"

	"github.com/btelolabs/btelo-forge/internal/gfx"
	"github.com/btelolabs/btelo-forge/internal/paint"
)

// Snapping, as Affinity Designer's: a point being placed or dragged with the pen, the node tool, the shape tool or
// the artboard tool is drawn to what is near it on screen: first to points (other objects' nodes, the corners,
// middles of the sides and centres of objects, artboards and the frame), else each way on its own to those edges
// and centres. View › Snapping turns it off.

// snapReach is how near, in screen pixels, a point must come to what it snaps to.
const snapReach = 6

// snapTargets are what points snap to: points, and lines across and down.
type snapTargets struct {
	pts    []paint.Pt
	xs, ys []float64
}

// boxTargets adds a box's corners, side middles and centre, and its edges and middle lines.
func (s *snapTargets) boxTargets(r paint.Rect) {
	if r.Empty() {
		return
	}
	cx, cy := (r.Min.X+r.Max.X)/2, (r.Min.Y+r.Max.Y)/2
	for _, x := range []float64{r.Min.X, cx, r.Max.X} {
		for _, y := range []float64{r.Min.Y, cy, r.Max.Y} {
			s.pts = append(s.pts, paint.Pt{X: x, Y: y})
		}
	}
	s.xs = append(s.xs, r.Min.X, cx, r.Max.X)
	s.ys = append(s.ys, r.Min.Y, cy, r.Max.Y)
}

func rectOfImage(r image.Rectangle) paint.Rect {
	return paint.Rect{Min: paint.Pt{X: float64(r.Min.X), Y: float64(r.Min.Y)}, Max: paint.Pt{X: float64(r.Max.X), Y: float64(r.Max.Y)}}
}

// snapTargetsOf gathers what can be snapped to within the visible area: every shown vector layer's nodes and box
// but the layer skipped (the object being edited, whose own nodes are given separately), the artboards but the one
// skipped, and the frame.
func (ad *artDoc) snapTargetsOf(skip, skipBoard int, own []paint.Pt) snapTargets {
	var s snapTargets
	s.pts = append(s.pts, own...)
	view := ad.d.View
	for _, l := range ad.d.Layers() {
		if !l.Visible || l.Vector == nil || l.ID == skip || !l.Vector.PixelBounds().Overlaps(view) {
			continue
		}
		v := l.Vector
		for _, sp := range v.Outline() {
			for _, n := range sp.Nodes {
				s.pts = append(s.pts, v.M.Map(n.P))
			}
		}
		b := *v
		b.Stroke = paint.VectorStroke{} // the object's own outline, not its stroke's reach
		s.boxTargets(b.Bounds())
	}
	for _, b := range ad.d.Boards {
		if b.ID != skipBoard && b.Rect.Overlaps(view) {
			s.boxTargets(rectOfImage(b.Rect))
		}
	}
	if !ad.d.Frame.Empty() {
		s.boxTargets(rectOfImage(ad.d.Frame))
	}
	return s
}

// snapped is p drawn to the nearest target within snapReach screen pixels: a point, else a line each way. It
// reports whether it snapped, and notes where for the overlay to mark.
func (a *App) snapped(ad *artDoc, p paint.Pt, s snapTargets) (paint.Pt, bool) {
	ad.snapMark = nil
	if a.settings.Paint.NoSnap {
		return p, false
	}
	reach := float64(a.u.px(snapReach)) / ad.zoom
	best, bestD, found := p, reach, false
	for _, q := range s.pts {
		if d := q.Dist(p); d <= bestD {
			best, bestD, found = q, d, true
		}
	}
	if found {
		ad.snapMark = &best
		return best, true
	}
	out, hit := p, false
	if x, ok := nearest(p.X, s.xs, reach); ok {
		out.X, hit = x, true
	}
	if y, ok := nearest(p.Y, s.ys, reach); ok {
		out.Y, hit = y, true
	}
	if hit {
		ad.snapMark = &out
	}
	return out, hit
}

// nearest is the value in vs nearest v within reach.
func nearest(v float64, vs []float64, reach float64) (float64, bool) {
	best, bestD := v, math.Inf(1)
	for _, w := range vs {
		if d := math.Abs(w - v); d <= reach && d < bestD {
			best, bestD = w, d
		}
	}
	return best, !math.IsInf(bestD, 1)
}

// drawSnapMark marks where the point being dragged snapped, as a small cross in a ring.
func (a *App) drawSnapMark(ad *artDoc, vp gfx.Rect) {
	if ad.snapMark == nil || a.u.active == "" {
		return
	}
	u := &a.u
	x, y := ad.toScreen(vp, pt2(*ad.snapMark))
	s, w := u.px(5), max(1, u.px(1.5))
	u.c.Line(x-s, y-s, x+s, y+s, w, colWarning)
	u.c.Line(x-s, y+s, x+s, y-s, w, colWarning)
}
