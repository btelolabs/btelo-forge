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

// spanOf is the rectangle that spans pieces, for reporting what changed.
func spanOf(pieces []image.Rectangle) image.Rectangle {
	var r image.Rectangle
	for _, p := range pieces {
		r = r.Union(p)
	}
	return r
}
