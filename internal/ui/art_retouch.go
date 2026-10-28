package ui

import (
	"fmt"

	"github.com/btelolabs/btelo-forge/internal/gfx"
	"github.com/btelolabs/btelo-forge/internal/paint"
)

// The retouching tools, in Photoshop's places: blur, sharpen and smudge share one, dodge, burn and sponge another.
// They paint with the brush's tip, size and hardness; each has its own strength, kept with the paint settings.

const (
	icDodge       = "\ue3a6"
	icBurn        = "\ue3a7"
	icSponge      = "\ue91c"
	icBlurTool    = "\ue3a5"
	icSharpenTool = "\ue3c8"
	icSmudge      = "\ue913"
)

// toneRangeNames and spongeModeNames name dodge's and burn's ranges, in paint.ToneRange's order, and the sponge's
// modes.
var (
	toneRangeNames  = msgs("Shadows", "Midtones", "Highlights")
	spongeModeNames = msgs("Desaturate", "Saturate")
)

// retouching reports whether the chosen tool is a retouching one.
func (a *App) retouching() bool {
	switch a.artTool {
	case toolDodge, toolBurn, toolSponge, toolBlur, toolSharpen, toolSmudge:
		return true
	}
	return false
}

// retouch is the chosen retouching tool's settings for the engine.
func (a *App) retouch() paint.Retouch {
	p := &a.settings.Paint
	rt := paint.Retouch{Range: paint.ToneRange(min(max(p.ToneRange, 0), 2)), Desaturate: !p.SpongeSaturate}
	switch a.artTool {
	case toolDodge:
		rt.Kind, rt.Strength = paint.RetouchDodge, p.Exposure
	case toolBurn:
		rt.Kind, rt.Strength = paint.RetouchBurn, p.Exposure
	case toolSponge:
		rt.Kind, rt.Strength = paint.RetouchSponge, p.SpongeFlow
	case toolBlur:
		rt.Kind, rt.Strength = paint.RetouchBlur, p.FocusStrength
	case toolSharpen:
		rt.Kind, rt.Strength = paint.RetouchSharpen, p.FocusStrength
	default:
		rt.Kind, rt.Strength = paint.RetouchSmudge, p.SmudgeStrength
	}
	return rt
}

// drawRetouchOptions is a retouching tool's options: the brush's size and hardness, then the tool's own.
func (a *App) drawRetouchOptions(r gfx.Rect) {
	u := &a.u
	p := &a.settings.Paint
	b := &a.brush
	x := r.X
	saved := false
	px := func(v float64) string { return fmt.Sprintf("%.0f px", v) }
	var done bool
	x, done = a.labelledSlider("art/retouch/size", x, r.Y, r.H, u.t("Brush size"), &b.Size, 1, 500, px)
	b.Size = float64(int(b.Size + 0.5))
	brushDone := done
	x, done = a.labelledSlider("art/retouch/hardness", x, r.Y, r.H, u.t("Hardness"), &b.Hardness, 0, 1, percent)
	brushDone = brushDone || done
	switch a.artTool {
	case toolDodge, toolBurn:
		lw := u.face(12).Measure(u.t("Range")) + u.px(6)
		u.label(u.face(12), gfx.Rect{X: x, Y: r.Y, W: lw, H: r.H}, u.t("Range"), colDim, 0)
		x += lw
		a.dropdown("art/retouch/range", gfx.Rect{X: x, Y: r.Y, W: u.px(120), H: r.H}, u.ts(toneRangeNames), min(max(p.ToneRange, 0), 2), nil, func(i int) {
			p.ToneRange = i
			a.saveSettings()
		})
		x += u.px(130)
		if x+u.px(190) < r.Right() {
			_, done = a.labelledSlider("art/retouch/exposure", x, r.Y, r.H, u.t("Exposure"), &p.Exposure, 0.01, 1, percent)
			saved = saved || done
		}
	case toolSponge:
		mode := 0
		if p.SpongeSaturate {
			mode = 1
		}
		lw := u.face(12).Measure(u.t("Mode")) + u.px(6)
		u.label(u.face(12), gfx.Rect{X: x, Y: r.Y, W: lw, H: r.H}, u.t("Mode"), colDim, 0)
		x += lw
		a.dropdown("art/retouch/mode", gfx.Rect{X: x, Y: r.Y, W: u.px(120), H: r.H}, u.ts(spongeModeNames), mode, nil, func(i int) {
			p.SpongeSaturate = i == 1
			a.saveSettings()
		})
		x += u.px(130)
		if x+u.px(190) < r.Right() {
			_, done = a.labelledSlider("art/retouch/flow", x, r.Y, r.H, u.t("Flow"), &p.SpongeFlow, 0.01, 1, percent)
			saved = saved || done
		}
	case toolBlur, toolSharpen:
		_, done = a.labelledSlider("art/retouch/strength", x, r.Y, r.H, u.t("Strength"), &p.FocusStrength, 0.01, 1, percent)
		saved = saved || done
	case toolSmudge:
		_, done = a.labelledSlider("art/retouch/smudge", x, r.Y, r.H, u.t("Strength"), &p.SmudgeStrength, 0.01, 1, percent)
		saved = saved || done
	}
	if brushDone {
		a.saveBrush()
	}
	if saved {
		a.saveSettings()
	}
}
