package ui

import (
	"math"
	"slices"
	"time"

	"github.com/btelolabs/btelo-forge/internal/gfx"
	"github.com/btelolabs/btelo-forge/internal/paint"
)

// Layers are put in order by dragging them in the layers panel, as in Photoshop: a press on a row (or one of its
// thumbnails) that moves becomes a drag of that layer, or of all the chosen layers when it is one of them. A line
// shows where they will go, above or below the row under the pointer, or a frame round a group they will go into;
// near the list's ends it scrolls.

// layerDrag is layers being dragged in the layers panel.
type layerDrag struct {
	ids    []int     // what moves: the chosen layers, or the one pressed
	drop   layerDrop // where they would go
	ok     bool      // whether they can go there
	ticked time.Time // the frame before, for scrolling at a pace the frame rate does not change
}

// layerDrop is where dragged layers would go, next to or into target, and how the list shows it: a line under or
// over row starting depth levels in, or a frame round row.
type layerDrop struct {
	target int
	where  paint.Place
	row    int
	mark   dropMark
	depth  int
}

// dropMark is how the layers panel shows where dragged layers would go.
type dropMark int

const (
	markAbove dropMark = iota // a line over the row
	markBelow                 // a line under it
	markFrame                 // a frame round it, a group they go into
)

// startLayerDrag begins dragging n once a press on its row has moved: the chosen layers when n is one of them,
// else n alone.
func (ad *artDoc) startLayerDrag(n *paint.Node) {
	ids := []int{n.ID}
	if n.ID == ad.d.Active || slices.Contains(ad.picked, n.ID) {
		ids = append([]int{ad.d.Active}, ad.picked...)
	}
	ad.layerDrag = &layerDrag{ids: ids, ticked: time.Now()}
	ad.layerHeld = 0
}

// layerDropAt is where the dragged layers would go with the pointer at y over rows laid out from top, rowH high:
// above or below a layer by the half of its row the pointer is in; above a group in the top quarter of its row,
// into it in the middle, below it (into it, at the top, when it is open) in the bottom quarter. Past the last row
// they go to the very bottom. Artboards stay at the top level, so they go only next to a row there.
func (ad *artDoc) layerDropAt(rows []layerRow, top, rowH, y float32) (layerDrop, bool) {
	if len(rows) == 0 {
		return layerDrop{}, false
	}
	d := ad.d
	boards := slices.ContainsFunc(ad.layerDrag.ids, func(id int) bool { n := d.Node(id); return n != nil && n.IsBoard() })
	i := int(math.Floor(float64((y - top) / rowH)))
	var drop layerDrop
	switch {
	case i >= len(rows):
		last := d.Root.Children[len(d.Root.Children)-1]
		drop = layerDrop{target: last.ID, where: paint.PlaceBelow, row: len(rows) - 1, mark: markBelow}
	default:
		i = max(i, 0)
		f := (y - top - float32(i)*rowH) / rowH
		n := rows[i].n
		drop = layerDrop{target: n.ID, where: paint.PlaceAbove, row: i, mark: markAbove, depth: rows[i].depth}
		below := layerDrop{target: n.ID, where: paint.PlaceBelow, row: i, mark: markBelow, depth: rows[i].depth}
		switch {
		case boards:
			if !slices.Contains(d.Root.Children, n) {
				return layerDrop{}, false
			}
			if f >= 0.5 {
				drop = below
			}
		case n.IsGroup() && f >= 0.75 && n.Open && len(n.Children) > 0:
			// The line under an open group is over its first layer: they go into it, at the top.
			drop.where, drop.mark, drop.depth = paint.PlaceInto, markBelow, rows[i].depth+1
		case n.IsGroup() && f >= 0.75:
			drop = below
		case n.IsGroup() && f >= 0.25:
			drop.where, drop.mark = paint.PlaceInto, markFrame
		case !n.IsGroup() && f >= 0.5:
			drop = below
		}
	}
	return drop, d.CanMoveNodes(ad.layerDrag.ids, drop.target, drop.where) == nil
}

// dragLayers follows a drag in the layers panel's list, before its rows are drawn: it scrolls near the list's ends
// and finds where the layers would go, and moves them there when the mouse is let go.
func (a *App) dragLayers(ad *artDoc, list gfx.Rect, rows []layerRow, rowH, maxScroll float32) {
	u := &a.u
	g := ad.layerDrag
	if !u.down {
		ad.layerDrag = nil
		if g.ok {
			a.artRun(ad, func() error { return ad.d.MoveNodes(g.ids, g.drop.target, g.drop.where) })
		}
		return
	}
	// Within half a row of an end, or past it, the list scrolls: eight rows a second, and three more for each row
	// beyond.
	now := time.Now()
	dt := float32(min(now.Sub(g.ticked).Seconds(), 0.1))
	g.ticked = now
	var beyond float32
	switch {
	case u.my < list.Y+rowH/2:
		beyond = (u.my - list.Y - rowH/2) / rowH
	case u.my > list.Bottom()-rowH/2:
		beyond = (u.my - list.Bottom() + rowH/2) / rowH
	}
	if beyond != 0 {
		speed := 8 + 3*float32(math.Abs(float64(beyond)))
		if beyond < 0 {
			speed = -speed
		}
		a.layersScroll = min(max(a.layersScroll+dt*speed*rowH, 0), maxScroll)
	}
	top := list.Y - a.layersScroll
	g.drop, g.ok = ad.layerDropAt(rows, top, rowH, min(max(u.my, list.Y), list.Bottom()-1))
}

// drawLayerDrop shows, over the rows, where the dragged layers would go.
func (a *App) drawLayerDrop(ad *artDoc, list gfx.Rect, rowH float32) {
	u := &a.u
	g := ad.layerDrag
	if g == nil {
		return
	}
	u.cursor = CursorMove
	if !g.ok {
		return
	}
	rr := gfx.Rect{X: list.X, Y: list.Y - a.layersScroll + float32(g.drop.row)*rowH, W: list.W, H: rowH}
	if g.drop.mark == markFrame {
		u.c.Outline(rr.Inset(u.px(1)), u.px(2), colAccent)
	} else {
		// The line starts where the names of the layers at its depth start, past the eye, with a dot.
		y := rr.Y
		if g.drop.mark == markBelow {
			y = rr.Bottom()
		}
		x := rr.X + u.px(4) + float32(g.drop.depth)*u.px(16) + u.px(24)
		u.c.Fill(gfx.Rect{X: x, Y: y - u.px(1), W: rr.Right() - x - u.px(4), H: u.px(2)}, colAccent)
		u.c.Fill(gfx.Rect{X: x - u.px(3), Y: y - u.px(3), W: u.px(6), H: u.px(6)}, colAccent)
	}
}
