package ui

import (
	"fmt"

	"github.com/btelolabs/btelo-video-cut/internal/gfx"
)

// historyNames are the names the picture gives its steps, which the History panel shows translated;
// TestEveryHistoryStepIsNamed keeps the list complete.
var historyNames = msgsIn("history",
	"Add artboard", "Add layer mask", "Add node", "Add noise", "Add shape", "Adjustment", "Apply layer mask",
	"Artboard", "Blend mode", "Blur", "Boolean", "Brightness/Contrast", "Brush", "Burn", "Canvas size", "Clear",
	"Clone stamp", "Convert to curves", "Crop", "Curves", "Cut", "Delete artboard", "Delete layer", "Delete layer mask",
	"Delete node", "Desaturate", "Deselect", "Disable layer mask", "Distort", "Dodge", "Duplicate layer", "Edit text",
	"Eraser", "Feather", "Fill", "Flatten", "Flip", "Flip canvas", "Free transform", "Gaussian blur", "Gradient",
	"Group", "Hue/Saturation", "Image size", "Inverse", "Invert", "Layer opacity", "Layer style", "Levels",
	"Lock transparent pixels", "Merge down", "Merge group", "Mosaic", "Move", "Move artboard", "Move handle",
	"Move layer", "Move node", "New adjustment layer", "New group", "New layer", "Paint bucket", "Paste", "Pen",
	"Pencil", "Perspective", "Posterize", "Rasterize", "Rasterize type", "Rename layer", "Reselect", "Reveal all",
	"Rotate", "Rotate canvas", "Select", "Sharp node", "Sharpen", "Show or hide layer", "Skew", "Smooth node", "Smudge",
	"Sponge", "Stroke", "Text", "Threshold", "Ungroup", "Unsharp mask", "Warp")

// The side panel's lower pages, as Photoshop's Layers and History panels sharing a place.
const (
	sideLayers = iota
	sideHistory
)

// drawSideTabs is the titles of the side panel's lower pages, the shown one bright; it returns where the page
// starts.
func (a *App) drawSideTabs(r gfx.Rect) float32 {
	u := &a.u
	f := u.bold(12.5)
	x := r.X
	for i, name := range []string{u.t("Layers"), u.t("History")} {
		w := f.Measure(name) + u.px(16)
		tr := gfx.Rect{X: x, Y: r.Y, W: w, H: u.px(22)}
		id := fmt.Sprintf("art/side/%d", i)
		col := colDim
		switch {
		case a.sidePage == i:
			col = colText
			u.c.Fill(gfx.Rect{X: tr.X + u.px(4), Y: tr.Bottom() - u.px(2), W: tr.W - u.px(8), H: u.px(2)}, colAccent)
		case u.hover(id, tr):
			col = colText
			u.cursor = CursorHand
		}
		u.label(f, tr, name, col, 0.5)
		if u.clicked(id, tr) {
			a.sidePage = i
		}
		x += w + u.px(6)
	}
	return r.Y + u.px(26)
}

// drawHistory is the History panel: the picture as it opened, then each step; the steps after the current one,
// which redo would bring back, are dimmed. A click goes to that step.
func (a *App) drawHistory(ad *artDoc, r gfx.Rect) {
	u := &a.u
	steps, done := ad.d.History()
	rowH := u.px(28)
	area := gfx.Rect{X: r.X, Y: r.Y, W: r.W, H: r.H}
	u.c.RoundRect(area, u.px(8), colBackground)
	rows := len(steps) + 1
	// Keep the current step in view.
	visible := max(int(area.H/rowH), 1)
	first := max(0, min(done-visible/2, rows-visible))
	u.c.Push(area)
	defer u.c.Pop()
	for i := first; i < rows && float32(i-first)*rowH < area.H; i++ {
		row := gfx.Rect{X: area.X + u.px(4), Y: area.Y + u.px(4) + float32(i-first)*rowH, W: area.W - u.px(8), H: rowH - u.px(2)}
		label := a.artTitle(ad)
		if i > 0 {
			label = u.tc("history", steps[i-1])
		}
		id := fmt.Sprintf("art/history/%d", i)
		col := colText
		switch {
		case i == done:
			u.c.RoundRect(row, u.px(6), gfx.Mix(colSurface, colAccent, 0.45))
		case i > done:
			col = colFaint
			if u.hover(id, row) {
				u.c.RoundRect(row, u.px(6), colSurface)
			}
		case u.hover(id, row):
			u.c.RoundRect(row, u.px(6), colSurface)
		}
		if u.hover(id, row) {
			u.cursor = CursorHand
		}
		u.label(u.face(12.5), gfx.Rect{X: row.X + u.px(10), Y: row.Y, W: row.W - u.px(20), H: row.H}, label, col, 0)
		if u.clicked(id, row) && i != done {
			a.artRun(ad, func() error { ad.d.GoTo(i); return nil })
		}
	}
}
