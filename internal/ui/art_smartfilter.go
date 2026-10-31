package ui

import (
	"fmt"
	"math/rand/v2"
	"slices"

	"github.com/btelolabs/btelo-forge/internal/gfx"
	"github.com/btelolabs/btelo-forge/internal/paint"
)

// Smart filters: with a smart object chosen, the Filter menu and Image › Adjustments add a filter it holds instead of
// changing pixels, in the same dialogs; Layer › Smart filters lists them, to change, turn off, order or take away.

// smartKinds are the dialogs' filters as a smart object holds them.
var smartKinds = map[int]string{filterBlur: paint.SmartBlur, filterSharpen: paint.SmartUnsharp, filterNoise: paint.SmartNoise,
	filterMosaic: paint.SmartMosaic, adjustShadowsHighlights: paint.SmartTones, filterDevelop: paint.SmartDevelop}

// smartFilterOf is the dialog's settings as a filter of a smart object.
func smartFilterOf(f *adjustDialog) paint.SmartFilter {
	if kind, ok := smartKinds[f.kind]; ok {
		return paint.SmartFilter{Kind: kind, Values: slices.Clone(f.values), On: f.on, Seed: f.seed}
	}
	adj := adjustmentOf(f.kind, f.values, f.on, f.curve)
	return paint.SmartFilter{Kind: paint.SmartAdjust, Adjust: &adj}
}

// dialogKindOf is the dialog of a smart object's filter; -1 for one without settings.
func dialogKindOf(sf paint.SmartFilter) int {
	if sf.Kind != paint.SmartAdjust {
		for k, name := range smartKinds {
			if name == sf.Kind {
				return k
			}
		}
		return -1
	}
	for k, pk := range layerKinds {
		if sf.Adjust != nil && pk == sf.Adjust.Kind {
			return k
		}
	}
	return -1
}

// openSmartFilter starts adding a filter of the dialog's kind to the chosen smart object.
func (a *App) openSmartFilter(ad *artDoc, kind int) {
	k := a.adjustKinds()[kind]
	f := &adjustDialog{kind: kind, ad: ad, preview: true, grab: -1, on: k.initial, seed: rand.Uint64(), values: k.startValues()}
	if k.curve {
		f.curve = []paint.CurvePoint{{In: 0, Out: 0}, {In: 255, Out: 255}}
	}
	s, err := ad.d.BeginSmartFilter(ad.d.Active, smartFilterOf(f))
	if err != nil {
		a.artFailed(ad, err)
		return
	}
	f.smart, f.smartInitial = s, s.Filter()
	f.shown = ""
	a.adjust = f
	ad.changedAll()
}

// editSmartFilter opens the settings of the filter at place i.
func (a *App) editSmartFilter(ad *artDoc, i int) {
	s, err := ad.d.EditSmartFilter(ad.d.Active, i)
	if err != nil {
		a.artFailed(ad, err)
		return
	}
	sf := s.Filter()
	kind := dialogKindOf(sf)
	if kind < 0 {
		s.Cancel()
		return
	}
	k := a.adjustKinds()[kind]
	f := &adjustDialog{kind: kind, ad: ad, preview: true, grab: -1, on: sf.On, seed: sf.Seed, smart: s, smartInitial: sf, values: slices.Clone(sf.Values)}
	if sf.Adjust != nil {
		f.on, f.values, f.curve = sf.Adjust.On, slices.Clone(sf.Adjust.Values), slices.Clone(sf.Adjust.Curve)
	}
	if len(f.values) == 0 && !k.curve {
		f.values = k.startValues()
	}
	a.adjust = f
	ad.changedAll()
}

// smartFilterName names a filter in the list.
func (a *App) smartFilterName(sf paint.SmartFilter) string {
	if kind := dialogKindOf(sf); kind >= 0 {
		return a.adjustKinds()[kind].title
	}
	if sf.Adjust != nil && sf.Adjust.Kind == paint.AdjustInvert {
		return a.u.t("Invert")
	}
	return a.u.t("Black & White")
}

// drawSmartFilters is the list of the chosen smart object's filters, first applied at the top.
func (a *App) drawSmartFilters() {
	ad := a.smartList
	if ad == nil {
		return
	}
	n := ad.d.Node(ad.d.Active)
	if a.art != ad || n == nil || n.Filters == nil {
		a.smartList = nil
		return
	}
	u := &a.u
	const rowH = 40
	box := rectCentered(a.screen, u.px(460), u.px(130)+float32(len(n.Filters.List))*u.px(rowH))
	box.X = min(a.screen.W-box.W-u.px(330), box.X+u.px(200))
	panel(u, box)
	in := box.Inset(u.px(22))
	u.label(u.bold(16), gfx.Rect{X: in.X, Y: in.Y, W: in.W, H: u.px(24)}, u.t("Smart filters"), colText, 0)
	y := in.Y + u.px(40)
	act := func(fn func() error) { a.artRun(ad, fn); ad.changedAll() }
	for i, sf := range n.Filters.List {
		id := fmt.Sprintf("smartfilter/%d", i)
		row := gfx.Rect{X: in.X, Y: y, W: in.W, H: u.px(32)}
		if on := u.checkbox(id+"/on", gfx.Rect{X: row.X, Y: row.Y, W: u.px(220), H: row.H}, a.smartFilterName(sf), !sf.Off); on == sf.Off {
			act(func() error { return ad.d.ToggleSmartFilter(n.ID, i) })
		}
		x := row.Right() - u.px(190)
		if dialogKindOf(sf) >= 0 && u.button(id+"/edit", gfx.Rect{X: x, Y: row.Y, W: u.px(70), H: row.H}, "", u.t("Change"), ghost, true) {
			a.smartList = nil
			a.editSmartFilter(ad, i)
			return
		}
		x += u.px(74)
		if u.button(id+"/up", gfx.Rect{X: x, Y: row.Y, W: u.px(36), H: row.H}, "", "↑", ghost, i > 0) {
			act(func() error { return ad.d.MoveSmartFilter(n.ID, i, -1) })
		}
		x += u.px(38)
		if u.button(id+"/down", gfx.Rect{X: x, Y: row.Y, W: u.px(36), H: row.H}, "", "↓", ghost, i < len(n.Filters.List)-1) {
			act(func() error { return ad.d.MoveSmartFilter(n.ID, i, 1) })
		}
		x += u.px(38)
		if u.button(id+"/delete", gfx.Rect{X: x, Y: row.Y, W: u.px(36), H: row.H}, "", "×", ghost, true) {
			act(func() error { return ad.d.RemoveSmartFilter(n.ID, i) })
			return // the list changed under this loop
		}
		y += u.px(rowH)
	}
	done := u.button("smartfilter/close", gfx.Rect{X: in.Right() - u.px(100), Y: in.Bottom() - u.px(34), W: u.px(100), H: u.px(34)}, "", u.t("Close"), primary, true)
	for _, k := range u.keys {
		done = done || k.Code == keyEscape || k.Code == keyEnter
	}
	u.keys = nil
	if done {
		a.smartList = nil
	}
}
