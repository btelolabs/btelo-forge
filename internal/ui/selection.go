package ui

import (
	"slices"

	"github.com/btelolabs/btelo-video-cut/internal/gfx"
)

// The selection is a.sel, the item last picked (or a whole track), plus a.picked: further items picked with
// Ctrl/⌘-click, Shift-click or a box drawn on empty timeline space.

// itemSelection is the selection of one item by id.
func (a *App) itemSelection(id int) (selection, bool) {
	switch {
	case a.hasClip(id):
		return selection{kind: selClip, id: id}, true
	case a.hasMusic(id):
		return selection{kind: selMusic, id: id}, true
	case a.hasOverlay(id):
		return selection{kind: selOverlay, id: id}, true
	}
	return selection{}, false
}

func (a *App) hasClip(id int) bool    { _, _, ok := a.p.FindClip(id); return ok }
func (a *App) hasMusic(id int) bool   { _, _, ok := a.p.FindMusic(id); return ok }
func (a *App) hasOverlay(id int) bool { _, _, ok := a.p.FindOverlay(id); return ok }

// isPicked reports whether item id is part of the selection.
func (a *App) isPicked(id int) bool {
	return a.picked[id] || a.sel.kind != selNone && a.sel.kind != selTrack && a.sel.id == id
}

// selectedIDs lists every selected item, the last picked one first.
func (a *App) selectedIDs() []int {
	var ids []int
	if a.sel.kind != selNone && a.sel.kind != selTrack {
		ids = append(ids, a.sel.id)
	}
	rest := make([]int, 0, len(a.picked))
	for id := range a.picked {
		if id != a.sel.id {
			rest = append(rest, id)
		}
	}
	slices.Sort(rest)
	return append(ids, rest...)
}

// selectOnly makes s the whole selection.
func (a *App) selectOnly(s selection) {
	a.sel = s
	clear(a.picked)
}

// addPick adds item id to the selection and makes it the last picked.
func (a *App) addPick(id int) {
	s, ok := a.itemSelection(id)
	if !ok {
		return
	}
	if a.sel.kind != selNone && a.sel.kind != selTrack {
		a.pick(a.sel.id)
	}
	a.sel = s
	delete(a.picked, id)
}

// togglePick adds or removes item id, as Ctrl/⌘-click does.
func (a *App) togglePick(id int) {
	if !a.isPicked(id) {
		a.addPick(id)
		return
	}
	delete(a.picked, id)
	if a.sel.id == id {
		a.sel = selection{}
		if ids := a.selectedIDs(); len(ids) > 0 {
			a.sel, _ = a.itemSelection(ids[0])
			delete(a.picked, ids[0])
		}
	}
}

func (a *App) pick(id int) {
	if a.picked == nil {
		a.picked = map[int]bool{}
	}
	a.picked[id] = true
}

// selectAll selects every item of the edit.
func (a *App) selectAll() {
	a.selectOnly(selection{})
	for _, id := range a.allItems() {
		a.addPick(id)
	}
}

func (a *App) allItems() []int {
	var ids []int
	for _, t := range a.p.Video {
		for _, c := range t.Clips {
			ids = append(ids, c.ID)
		}
	}
	for _, t := range a.p.Overlays {
		for _, o := range t.Items {
			ids = append(ids, o.ID)
		}
	}
	for _, t := range a.p.Music {
		for _, m := range t.Clips {
			ids = append(ids, m.ID)
		}
	}
	return ids
}

// pruneSelection forgets picked items that no longer exist.
func (a *App) pruneSelection() {
	for id := range a.picked {
		if !a.p.Has(id) {
			delete(a.picked, id)
		}
	}
}

// itemsIn lists the items whose timeline rectangles touch box.
func (a *App) itemsIn(t tracks, box gfx.Rect) []int {
	var ids []int
	hit := func(g group, k int, id int, start, end float64) {
		if l, ok := t.lane(g, k); ok && a.clipRect(t, l, start, end).Intersects(box) {
			ids = append(ids, id)
		}
	}
	for k, tr := range a.p.Video {
		for _, c := range tr.Clips {
			hit(groupVideo, k, c.ID, c.Start, c.End())
		}
	}
	for k, tr := range a.p.Overlays {
		for _, o := range tr.Items {
			hit(groupOverlay, k, o.ID, o.Start, o.End)
		}
	}
	for k, tr := range a.p.Music {
		for _, m := range tr.Clips {
			hit(groupMusic, k, m.ID, m.Start, m.End())
		}
	}
	return ids
}
