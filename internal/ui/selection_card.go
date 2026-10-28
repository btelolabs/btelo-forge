package ui

import (
	"errors"
	"fmt"
	"math"

	"github.com/btelolabs/btelo-forge/internal/gfx"
	"github.com/btelolabs/btelo-forge/internal/project"
)

// selectionCard reports whether the panel shows the card that edits the selection: for several items at once, or for
// a single video clip, which has no tab of its own.
func (a *App) selectionCard() bool {
	return len(a.selectedIDs()) > 1 || a.sel.kind == selClip
}

// deleteItems removes several items in one undoable step.
func (a *App) deleteItems(ids []int) {
	before := a.p.Clone()
	n, err := a.p.DeleteItems(ids)
	if errors.Is(err, project.ErrMainTrackEmpty) {
		a.toast(a.u.t("At least one segment has to stay"), false)
		return
	}
	a.selectOnly(selection{})
	a.committed(before)
	a.toast(a.u.tf("Deleted %d items", n), false)
}

// drawSelectionCard edits every selected item together and returns the height it used.
func (a *App) drawSelectionCard(r gfx.Rect) float32 {
	u := &a.u
	ids := a.selectedIDs()
	clips, music, overlays := 0, 0, 0
	for _, id := range ids {
		switch {
		case a.hasClip(id):
			clips++
		case a.hasMusic(id):
			music++
		case a.hasOverlay(id):
			overlays++
		}
	}
	sound, picture := clips+music > 0, clips+overlays > 0
	h := u.px(64)
	if sound {
		h += u.px(34)
	}
	if picture {
		h += u.px(34)
	}
	card := gfx.Rect{X: r.X, Y: r.Y, W: r.W, H: h}
	u.c.Fill(card, colSurface)
	u.c.Outline(card, max(1, u.px(1)), colBorder)
	in := card.Inset(u.px(12))
	title := u.t("Video clip")
	if len(ids) > 1 {
		title = u.tf("%d selected", len(ids))
	}
	u.label(u.bold(13.5), gfx.Rect{X: in.X, Y: in.Y, W: in.W - u.px(80), H: u.px(20)}, title, colText, 0)
	if len(ids) > 1 && u.iconButton("sel/clear", gfx.Rect{X: in.Right() - u.px(64), Y: in.Y - u.px(5), W: u.px(30), H: u.px(30)}, icClose, u.t("Clear the selection (Esc)"), false, true) {
		a.selectOnly(selection{})
		return h + u.px(12)
	}
	if u.iconButton("sel/delete", gfx.Rect{X: in.Right() - u.px(30), Y: in.Y - u.px(5), W: u.px(30), H: u.px(30)}, icDelete, u.t("Delete the selection (Delete)"), false, true) {
		a.deleteSelection()
		return h + u.px(12)
	}
	u.label(u.face(11.5), gfx.Rect{X: in.X, Y: in.Y + u.px(22), W: in.W, H: u.px(18)},
		u.tf("%d video clips · %d music clips · %d overlays", clips, music, overlays), colDim, 0)
	y := in.Y + u.px(48)
	level := func(id, label string, value float64, hi float64, apply func(v float64)) {
		u.label(u.face(12.5), gfx.Rect{X: in.X, Y: y, W: u.px(64), H: u.px(28)}, label, colDim, 0)
		v := value
		sr := gfx.Rect{X: in.X + u.px(66), Y: y, W: in.W - u.px(120), H: u.px(28)}
		changed, done := u.slider(id, sr, &v, 0, hi, true)
		a.live(changed, done, func() { apply(v) })
		u.label(u.face(12), gfx.Rect{X: sr.Right() + u.px(6), Y: y, W: u.px(48), H: u.px(28)}, fmt.Sprintf("%d%%", int(math.Round(v*100))), colDim, 0)
		y += u.px(34)
	}
	if sound {
		v, _ := a.p.Volume(a.firstWith(ids, func(id int) bool { _, ok := a.p.Volume(id); return ok }))
		level("sel/volume", u.t("Volume"), v, 2, func(v float64) { a.p.SetVolumes(ids, v) })
	}
	if picture {
		v, _ := a.p.Opacity(a.firstWith(ids, func(id int) bool { _, ok := a.p.Opacity(id); return ok }))
		level("sel/opacity", u.t("Opacity"), v, 1, func(v float64) { a.p.SetOpacity(ids, v) })
	}
	return h + u.px(12)
}

// firstWith is the first id that has what ok asks for.
func (a *App) firstWith(ids []int, ok func(int) bool) int {
	for _, id := range ids {
		if ok(id) {
			return id
		}
	}
	return 0
}
