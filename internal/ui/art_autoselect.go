package ui

import (
	"image"
	"slices"

	"github.com/btelolabs/btelo-forge/internal/gfx"
	"github.com/btelolabs/btelo-forge/internal/paint"
)

// The move tool's auto-select, on unless its options turn it off: a press on the canvas chooses what is there, the
// outermost of it first, so a drag moves a whole group, smart object or artboard. A click (without a drag) where
// the chosen group is goes into it, choosing its group or layer there, so each click goes a level deeper; with a
// layer or group inside a group chosen, a press chooses at the same depth, so moving the layers of a group one after
// another takes one press each. A press on a board's name chooses the board; nothing there leaves the choice as it
// was.

// autoSelect chooses what a press of the move tool at canvas pixel at lands on.
func (a *App) autoSelect(ad *artDoc, vp gfx.Rect, at image.Point) {
	d := ad.d
	ad.descend = 0
	chain := d.PickAt(at)
	if len(chain) == 0 {
		if b := a.boardLabelUnder(ad, vp); b != 0 {
			chain = []int{b}
		}
	}
	if len(chain) == 0 {
		return
	}
	if i := slices.Index(chain, d.Active); i >= 0 {
		// Already chosen: a drag moves it; a click goes into it.
		if i+1 < len(chain) {
			ad.descend = chain[i+1]
		}
		return
	}
	// At the depth of what is chosen, when what holds it holds what is pressed; else the outermost.
	pick := chain[0]
	if path := d.PathTo(d.Active); len(path) > 1 && len(chain) >= len(path) && slices.Equal(chain[:len(path)-1], path[:len(path)-1]) {
		pick = chain[len(path)-1]
	}
	d.Choose(pick)
	d.EditMask = false
	ad.picked = nil
}

// shiftPick adds what a Shift+press of the move tool lands on to the chosen layers, or takes it away when it is
// chosen, as in Photoshop, so layers to combine or group can be chosen on the canvas; it reports whether the press
// landed on anything.
func (a *App) shiftPick(ad *artDoc, vp gfx.Rect, at image.Point) bool {
	d := ad.d
	chain := d.PickAt(at)
	if len(chain) == 0 {
		if b := a.boardLabelUnder(ad, vp); b != 0 {
			chain = []int{b}
		}
	}
	if len(chain) == 0 {
		return false
	}
	// At the depth of what is chosen, as a plain press chooses.
	pick := chain[0]
	if path := d.PathTo(d.Active); len(path) > 1 && len(chain) >= len(path) && slices.Equal(chain[:len(path)-1], path[:len(path)-1]) {
		pick = chain[len(path)-1]
	}
	if pick == d.Active {
		if len(ad.picked) > 0 {
			next := ad.picked[0]
			ad.picked = slices.Delete(ad.picked, 0, 1)
			d.Choose(next)
		}
		return true
	}
	ad.togglePicked(pick)
	return true
}

// boardLabelUnder is the board whose name above it is under the pointer, or 0.
func (a *App) boardLabelUnder(ad *artDoc, vp gfx.Rect) int {
	u := &a.u
	boards := ad.d.Boards()
	for i := len(boards) - 1; i >= 0; i-- {
		if a.boardLabel(ad, vp, boards[i]).Contains(u.mx, u.my) {
			return boards[i].ID
		}
	}
	return 0
}

// drawMoveOptions is the move tool's options bar: auto-select, and what the tool does.
func (a *App) drawMoveOptions(ad *artDoc, r gfx.Rect) {
	u := &a.u
	p := &a.settings.Paint
	label := u.t("Auto-select")
	w := checkboxWidth(u, label)
	if on := u.checkbox("art/move/autoselect", gfx.Rect{X: r.X, Y: r.Y, W: w, H: r.H}, label, !p.NoAutoSelect); on == p.NoAutoSelect {
		p.NoAutoSelect = !on
		a.saveSettings()
	}
	u.hint("art/move/autoselect", u.t("A press chooses what is there, the outermost group or artboard first; a click on the chosen group goes into it"),
		gfx.Rect{X: r.X, Y: r.Y, W: w, H: r.H})
	x := a.drawCombineButtons(ad, r.X+w+u.px(16), &r)
	a.optHint(x, r, u.t("Drag to move the layer, or the selected pixels; the arrow keys nudge by a pixel, with Shift by ten"))
}

// Working on an artboard works on what is in it: a press with a painting or drawing tool on a board other than the
// one the chosen layer is in chooses that board's layer last chosen, or else its top layer, or makes it one; the
// tools that choose for themselves (moving, selecting, the eyedropper, the artboard and node tools) and the views
// are left alone.

// noteBoardLayer remembers the chosen layer of the board it is in.
func (ad *artDoc) noteBoardLayer() {
	if b := ad.d.BoardOf(ad.d.Active); b != 0 && b != ad.d.Active {
		if ad.boardLayer == nil {
			ad.boardLayer = map[int]int{}
		}
		ad.boardLayer[b] = ad.d.Active
	}
}

// targetsBoards reports whether the chosen tool works on the board it is pressed on.
func (a *App) targetsBoards() bool {
	switch a.artTool {
	case toolBrush, toolEraser, toolClone, toolSpot, toolHeal, toolPatch, toolDodge, toolBurn, toolSponge, toolBlur, toolSharpen, toolSmudge, toolLiquify,
		toolBucket, toolGradient, toolMaskGradient, toolShape, toolPen, toolPencil, toolText:
		return true
	}
	return false
}

// paintsPixels reports whether the chosen tool needs a layer with pixels to work on.
func (a *App) paintsPixels() bool {
	switch a.artTool {
	case toolShape, toolPen, toolPencil, toolText, toolMaskGradient:
		return false
	case toolGradient:
		return a.art == nil || a.art.paintedVector() == nil
	}
	return true
}

// targetBoardAt chooses what a press at canvas pixel at works on when it is on another board than the chosen
// layer's.
func (a *App) targetBoardAt(ad *artDoc, at image.Point) {
	d := ad.d
	b := d.BoardAt(at)
	if b == 0 || !a.targetsBoards() || d.BoardOf(d.Active) == b {
		return
	}
	pick := 0
	if id, ok := ad.boardLayer[b]; ok && d.BoardOf(id) == b && d.Node(id) != nil {
		pick = id
	} else if l := topLayerOf(d.Node(b)); l != nil {
		pick = l.ID
	}
	switch {
	case pick != 0:
		d.Choose(pick)
	case a.paintsPixels():
		// An empty board gets a layer to paint on.
		d.Choose(b)
		a.artRun(ad, func() error { d.AddLayer(a.layerName()); return nil })
	default:
		d.Choose(b) // what is drawn goes into it
	}
	d.EditMask = false
	ad.picked = nil
}

// topLayerOf is the topmost layer in g, or nil.
func topLayerOf(g *paint.Node) *paint.Node {
	for _, c := range g.Children {
		if !c.IsGroup() {
			return c
		}
		if l := topLayerOf(c); l != nil {
			return l
		}
	}
	return nil
}
