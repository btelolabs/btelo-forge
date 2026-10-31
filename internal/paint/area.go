package paint

import (
	"image"
	"slices"
)

// On the unbounded canvas an operation never works over a rectangle that merely spans what it touches: two dots
// far apart span an area that would not fit in memory. It works over tiles instead, the pieces where something can
// change.

// workArea is where an operation on a layer acts when nothing else says: the tiles of the selection; or else of the
// frame; or else of what is on screen and of own, the layer's (or mask's) own tiles, grown by reach pixels for
// operations that spread, such as a blur. An inverted selection, selected everywhere beyond its tiles, acts on its
// tiles as well as on that.
func (d *Doc) workArea(own *plane, reach int) []image.Rectangle {
	keys := map[image.Point]bool{}
	add := func(r image.Rectangle) {
		if r.Empty() {
			return
		}
		k0, k1 := keysOver(r)
		for ky := k0.Y; ky <= k1.Y; ky++ {
			for kx := k0.X; kx <= k1.X; kx++ {
				keys[image.Pt(kx, ky)] = true
			}
		}
	}
	sel := d.Selection
	switch {
	case sel != nil && sel.Fill() == 0:
		for k := range sel.p.tiles {
			add(tileRect(k).Add(sel.p.off))
		}
	case !d.Frame.Empty():
		add(d.Frame)
	default:
		add(d.View)
		if own != nil {
			for k := range own.tiles {
				add(tileRect(k).Add(own.off).Inset(-reach))
			}
		}
	}
	if sel != nil && sel.Fill() != 0 {
		for k := range sel.p.tiles {
			add(tileRect(k).Add(sel.p.off))
		}
	}
	out := make([]image.Rectangle, 0, len(keys))
	for k := range keys {
		r := tileRect(k)
		if !d.Frame.Empty() {
			r = r.Intersect(d.Frame)
		}
		if !r.Empty() {
			out = append(out, r)
		}
	}
	slices.SortFunc(out, func(a, b image.Rectangle) int {
		if a.Min.Y != b.Min.Y {
			return a.Min.Y - b.Min.Y
		}
		return a.Min.X - b.Min.X
	})
	return out
}

// areaOf is where an operation on the chosen node's pixels, or on its mask when toMask, acts (see workArea and
// maskArea).
func (d *Doc) areaOf(n *Node, toMask bool, reach int) []image.Rectangle {
	if toMask {
		return d.maskArea(n, reach)
	}
	return d.workArea(&n.Pixels.p, reach)
}

// maskArea is where an operation on n's mask acts: over what n shows, grown by reach, within the selection and the
// frame. Beyond what its layer shows a mask hides nothing, so the work follows the layer's size and never the
// canvas's. An adjustment layer's mask hides its effect on everything under it, so it acts as on pixels.
func (d *Doc) maskArea(n *Node, reach int) []image.Rectangle {
	limit, bounded := contentReach(n)
	if !bounded {
		return d.workArea(&n.Mask.p, reach)
	}
	if limit.Empty() {
		return nil
	}
	limit = limit.Inset(-reach)
	if !d.Frame.Empty() {
		limit = limit.Intersect(d.Frame)
	}
	if sel := d.Selection; sel != nil && sel.Fill() == 0 {
		limit = limit.Intersect(sel.TileBounds())
	}
	if limit.Empty() {
		return nil
	}
	var out []image.Rectangle
	k0, k1 := keysOver(limit)
	for ky := k0.Y; ky <= k1.Y; ky++ {
		for kx := k0.X; kx <= k1.X; kx++ {
			if r := tileRect(image.Pt(kx, ky)).Intersect(limit); !r.Empty() {
				out = append(out, r)
			}
		}
	}
	return out
}

// contentReach is a quick bound of where n shows anything: a layer's reach, a group's children's. It is unbounded
// for an adjustment layer, which changes everything under it, and so for a pass-through group holding one; in any
// other group an adjustment changes only what the group's own layers show.
func contentReach(n *Node) (image.Rectangle, bool) {
	switch {
	case n.Board != nil:
		return n.Board.Rect, true // what is in a board is cut to it, and its background fills it
	case n.Adjust != nil:
		return image.Rectangle{}, false
	case !n.IsGroup():
		return reachOfNode(n), true
	}
	var r image.Rectangle
	for _, c := range n.Children {
		cr, bounded := contentReach(c)
		if !bounded {
			if n.Blend == BlendPassThrough {
				return image.Rectangle{}, false
			}
			continue
		}
		r = r.Union(cr)
	}
	if e := n.Filters.reach(); e > 0 && !r.Empty() {
		r = r.Inset(-e)
	}
	return r, true
}

// spanOf is the rectangle that spans pieces, for reporting what changed.
func spanOf(pieces []image.Rectangle) image.Rectangle {
	var r image.Rectangle
	for _, p := range pieces {
		r = r.Union(p)
	}
	return r
}
