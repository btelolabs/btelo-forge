package ui

import (
	"math"
	"time"

	"github.com/btelolabs/btelo-forge/internal/gfx"
)

// Moving around the canvas whatever tool is chosen, as Photoshop allows: the wheel zooms at the pointer (Shift
// with it scrolls sideways, Ctrl up and down), holding Space drags the view, and holding the zoom tool's key zooms
// by dragging sideways or by clicking (Alt+click zooms out). Letting the zoom key go returns to the tool in use;
// tapping it without zooming chooses the zoom tool, as a tool key does.

// What a navigation drag is doing.
const (
	navNone = iota
	navPan
	navZoom
)

// zoomScrub is how far, in logical pixels, a sideways drag goes to double the zoom.
const zoomScrub = 120

// zoomHold is the zoom tool's key held down for a moment's zoom.
type zoomHold struct {
	code uint16
	used bool // it zoomed: letting it go keeps the tool in use rather than choosing the zoom tool
}

// navigate handles the wheel, and the pointer while Space or the zoom key is held or a drag they began goes on; it
// reports whether it took the pointer, which the chosen tool then leaves alone.
func (a *App) navigate(ad *artDoc, vp gfx.Rect) bool {
	u := &a.u
	const id = "art/view"
	if u.hover(id, vp) && u.wheel != 0 {
		switch {
		case u.shift:
			ad.panX -= float64(u.wheel) * 60 / ad.zoom
			ad.fit = false
		case u.ctrl:
			ad.panY -= float64(u.wheel) * 60 / ad.zoom
			ad.fit = false
		default:
			ad.zoomAt(vp, u.mx, u.my, ad.zoom*math.Pow(1.2, float64(u.wheel)))
		}
		u.wheel = 0
	}
	kind := ad.nav
	if kind == navNone {
		switch {
		case u.active != "" || ad.text != nil:
			return false // a drag of the tool's own goes on, or Space types
		case u.isHeld(keySpace) || a.artTool == toolHand && ad.xf == nil:
			kind = navPan
		case a.zoomHeld != nil || a.artTool == toolZoom && ad.xf == nil:
			kind = navZoom
		default:
			return false
		}
	}
	if u.hover(id, vp) || ad.nav != navNone {
		u.cursor = CursorMove
		if kind == navZoom {
			u.cursor = CursorCross
		}
	}
	if u.press(id, vp) {
		ad.nav = kind
		ad.panFrom = [4]float64{float64(u.mx), float64(u.my), ad.panX, ad.panY}
		ad.navZoom = ad.zoom
		if kind == navZoom && a.zoomHeld != nil {
			a.zoomHeld.used = true
		}
	}
	if u.dragging(id) && ad.nav != navNone {
		switch ad.nav {
		case navPan:
			ad.panX = ad.panFrom[2] - (float64(u.mx)-ad.panFrom[0])/ad.zoom
			ad.panY = ad.panFrom[3] - (float64(u.my)-ad.panFrom[1])/ad.zoom
			ad.fit = false
		case navZoom:
			if u.drag.moved {
				// Zoomed about where the drag began, so that point stays under it.
				dx := (float64(u.mx) - ad.panFrom[0]) / float64(u.px(zoomScrub))
				ad.zoomKeeping(vp, ad.panFrom, ad.navZoom*math.Pow(2, dx))
			}
		}
	}
	if u.letGo(id) && ad.nav != navNone {
		if ad.nav == navZoom && !u.drag.moved {
			z := ad.zoom * 2
			if u.alt {
				z = ad.zoom / 2
			}
			ad.zoomAt(vp, u.mx, u.my, z)
		}
		ad.nav = navNone
	}
	return true
}

// zoomKeeping zooms so the canvas point that was under the screen point (from[0], from[1]) when the pan was
// (from[2], from[3]) at zoom ad.navZoom stays under it.
func (ad *artDoc) zoomKeeping(vp gfx.Rect, from [4]float64, zoom float64) {
	zoom = min(max(zoom, minZoom), maxZoom)
	mx, my := from[0]-float64(vp.X+vp.W/2), from[1]-float64(vp.Y+vp.H/2)
	cx, cy := from[2]+mx/ad.navZoom, from[3]+my/ad.navZoom
	ad.zoom, ad.fit = zoom, false
	ad.panX, ad.panY = cx-mx/zoom, cy-my/zoom
}

// navigationKey takes the presses that hold a navigation key: Space, whose presses do nothing else on the canvas,
// and the zoom tool's key, which zooms while held; it reports whether it took k.
func (a *App) navigationKey(k Key) bool {
	u := &a.u
	if k.Ctrl || k.Alt {
		return false
	}
	if k.Code == keySpace && !k.Shift {
		return true
	}
	t, ok := a.toolFor(k)
	if !ok || t != toolZoom || k.Shift || a.artTool == toolZoom {
		return false
	}
	if !u.repeated(k) && a.zoomHeld == nil {
		a.zoomHeld = &zoomHold{code: k.Code}
	}
	return true
}

// releaseNavigation ends a zoom key let go: one that did not zoom chooses the zoom tool, as tapping a tool's key
// does.
func (a *App) releaseNavigation() {
	h := a.zoomHeld
	if h == nil || a.u.isHeld(h.code) {
		return
	}
	a.zoomHeld = nil
	if !h.used {
		a.setArtTool(toolZoom)
	}
}

// opacityTyping is the digit just typed for an opacity, so a second one soon after makes two-digit percent, as
// typing 4 then 5 gives 45% in Photoshop.
type opacityTyping struct {
	digit int
	at    time.Time
}

// opacityDigits is how soon a second digit must follow the first to make a two-digit percent.
const opacityDigits = 700 * time.Millisecond

// opacityKey sets an opacity from a digit, as Photoshop's number keys do: with a painting tool the brush's, else the
// chosen layer's or group's; 1 is 10%, 0 is 100%, and two digits typed quickly are the percent itself. It reports
// whether k was a digit.
func (a *App) opacityKey(ad *artDoc, k Key) bool {
	if k.Ctrl || k.Alt || k.Shift {
		return false
	}
	var digit int
	switch {
	case k.Code >= '0' && k.Code <= '9':
		digit = int(k.Code - '0')
	case k.Code >= keyNum0 && k.Code <= keyNum0+9:
		digit = int(k.Code - keyNum0)
	default:
		return false
	}
	pct := digit * 10
	if digit == 0 {
		pct = 100
	}
	now := time.Now()
	if t := a.typedOpacity; t != nil && now.Sub(t.at) <= opacityDigits {
		pct = t.digit*10 + digit // the second of two digits: 4 then 5 is 45%, 0 then 5 is 5%
		a.typedOpacity = nil
	} else {
		a.typedOpacity = &opacityTyping{digit: digit, at: now}
	}
	if pct == 0 {
		pct = 100 // 0 then 0 is a hundred, as in Photoshop
	}
	v := float64(pct) / 100
	u := &a.u
	if a.painting() {
		a.brush.Opacity = v
		a.saveBrush()
		a.toast(u.tf("Brush opacity %d%%", pct), false)
		return true
	}
	n := ad.d.Node(ad.d.Active)
	if n == nil {
		return true
	}
	a.artRun(ad, func() error { ad.d.SetOpacity(n.ID, v); return nil })
	a.toast(u.tf("Opacity of “%s”: %d%%", n.Name, pct), false)
	return true
}
