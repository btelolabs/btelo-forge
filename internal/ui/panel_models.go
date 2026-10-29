package ui

import (
	"github.com/btelolabs/btelo-forge/internal/gfx"
	"github.com/btelolabs/btelo-forge/internal/transcribe"
)

// drawModelRows lists AI models to choose from, one row each with its size or whether it is downloaded, from the
// top of r. It returns the id of the model clicked this frame ("" for none) and the y below the list.
func (a *App) drawModelRows(id string, r gfx.Rect, models []transcribe.Model, chosen string, manager *transcribe.Manager) (string, float32) {
	u := &a.u
	lang := string(u.lang)
	y, picked := r.Y, ""
	nameW := u.px(60) // the names' column fits the longest name, leaving the rest to the notes
	for _, m := range models {
		nameW = max(nameW, u.bold(12.5).Measure(m.Name.In(lang)))
	}
	nameW = min(nameW, u.px(120))
	for _, m := range models {
		row := gfx.Rect{X: r.X, Y: y, W: r.W, H: u.px(34)}
		rowID := id + m.ID
		selected := m.ID == chosen
		bg := colSurface
		edge := colBorder
		if selected {
			bg, edge = colSelected, colBorderHi
		} else if u.hover(rowID, row) {
			bg = colSurfaceHi
			u.cursor = CursorHand
		}
		u.c.Fill(row, bg)
		u.c.Outline(row, max(1, u.px(1)), edge)
		dot := gfx.Rect{X: row.X + u.px(10), Y: row.Y + u.px(10), W: u.px(14), H: u.px(14)}
		if selected {
			u.c.Fill(dot, colPrimary)
			u.c.Fill(dot.Inset(u.px(4)), colOnPrimary)
		} else {
			u.c.Outline(dot, u.px(1.5), colFaint)
		}
		u.label(u.bold(12.5), gfx.Rect{X: row.X + u.px(32), Y: row.Y, W: nameW, H: row.H}, m.Name.In(lang), colText, 0)
		info := m.Note.In(lang)
		if m.Size > 0 {
			info = size(m.Size) + " · " + info
		}
		if _, ok := manager.ModelPath(m); ok {
			info = u.t("Downloaded") + " · " + m.Note.In(lang)
		}
		infoX := row.X + u.px(32+12) + nameW
		u.label(u.face(11), gfx.Rect{X: infoX, Y: row.Y, W: row.Right() - u.px(8) - infoX, H: row.H}, info, colDim, 0)
		if u.clicked(rowID, row) {
			picked = m.ID
		}
		y += u.px(38)
	}
	return picked, y
}

// drawEngineChoice shows where an AI model runs from the top of r: a chip for each engine when there are several,
// and the chosen engine's note. It returns the id of the engine picked this frame ("" for none) and the y below.
func (a *App) drawEngineChoice(id string, r gfx.Rect, engines []transcribe.Engine, engine transcribe.Engine) (string, float32) {
	u := &a.u
	lang := string(u.lang)
	y, picked := r.Y, ""
	if len(engines) > 1 {
		names, current := make([]string, len(engines)), 0
		for i, e := range engines {
			names[i] = e.Name.In(lang)
			if e.ID == engine.ID {
				current = i
			}
		}
		pick, h := u.flowChips(id, gfx.Rect{X: r.X, Y: y, W: r.W, H: u.px(28)}, names, current)
		if pick != current {
			picked = engines[pick].ID
		}
		y += h + u.px(4)
	} else {
		u.label(u.bold(12.5), gfx.Rect{X: r.X, Y: y, W: r.W, H: u.px(22)}, engine.Name.In(lang), colText, 0)
		y += u.px(26)
	}
	if note := engine.Note.In(lang); note != "" {
		f := u.face(11)
		for _, l := range f.Wrap(note, r.W) {
			u.c.Text(f, r.X, y, l, colFaint)
			y += f.Height() * 1.3
		}
	}
	return picked, y
}
