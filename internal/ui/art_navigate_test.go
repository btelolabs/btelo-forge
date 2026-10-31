package ui

import (
	"math"
	"testing"

	"github.com/btelolabs/btelo-forge/internal/gfx"
)

// The wheel zooms at the pointer, keeping the canvas point under it; Shift and Ctrl with it scroll.
func TestWheelZoomsTheCanvas(t *testing.T) {
	x, ad := canvasHarness(t)
	x.mx, x.my = x.viewPoint(ad, 100, 80)
	z := ad.zoom
	x.frame(Input{Wheel: 1})
	if math.Abs(ad.zoom-z*1.2) > 1e-9 {
		t.Fatalf("the wheel zoomed to %v from %v", ad.zoom, z)
	}
	if sx, sy := x.viewPoint(ad, 100, 80); math.Abs(float64(sx-x.mx)) > 0.01 || math.Abs(float64(sy-x.my)) > 0.01 {
		t.Errorf("the point under the pointer moved to %v,%v from %v,%v", sx, sy, x.mx, x.my)
	}
	z, py := ad.zoom, ad.panY
	x.frame(Input{Wheel: 1, Ctrl: true})
	if ad.zoom != z || ad.panY >= py {
		t.Errorf("Ctrl+wheel should scroll up: zoom %v pan %v", ad.zoom, ad.panY)
	}
	px := ad.panX
	x.frame(Input{Wheel: -1, Shift: true})
	if ad.zoom != z || ad.panX <= px {
		t.Errorf("Shift+wheel should scroll sideways: zoom %v pan %v", ad.zoom, ad.panX)
	}
}

// Holding Space drags the view with any tool, without painting; letting it go returns to the tool.
func TestSpaceDragsTheView(t *testing.T) {
	x, ad := canvasHarness(t)
	x.key('B', false)
	before := shown(ad)
	px, py := ad.panX, ad.panY
	x.frame(Input{Keys: []Key{{Code: keySpace}}, Up: []uint16{}})
	fx, fy := x.viewPoint(ad, 100, 100)
	x.drag(fx, fy, fx+50, fy+30)
	if got, want := ad.panX, px-50/ad.zoom; math.Abs(got-want) > 1e-6 {
		t.Errorf("pan x %v, want %v", got, want)
	}
	if got, want := ad.panY, py-30/ad.zoom; math.Abs(got-want) > 1e-6 {
		t.Errorf("pan y %v, want %v", got, want)
	}
	if string(shown(ad)) != string(before) {
		t.Error("dragging with Space held painted")
	}
	x.frame(Input{Up: []uint16{keySpace}})
	x.drag(fx, fy, fx+50, fy)
	if string(shown(ad)) == string(before) {
		t.Error("after Space is let go the brush should paint again")
	}
	if x.a.artTool != toolBrush {
		t.Errorf("the tool is %v", x.a.artTool)
	}
}

// Holding Z zooms by dragging sideways and returns to the tool; tapping Z chooses the zoom tool.
func TestHoldingZZooms(t *testing.T) {
	x, ad := canvasHarness(t)
	x.key('B', false)
	z := ad.zoom
	x.frame(Input{Keys: []Key{{Code: 'Z'}}, Up: []uint16{}})
	x.frame(Input{Keys: []Key{{Code: 'Z'}}, Up: []uint16{}}) // the keyboard repeating
	fx, fy := x.viewPoint(ad, 200, 150)
	x.drag(fx, fy, fx+x.a.u.px(zoomScrub), fy)
	if math.Abs(ad.zoom-2*z) > 1e-6 {
		t.Errorf("a drag of %v px zoomed to %v from %v", zoomScrub, ad.zoom, z)
	}
	x.frame(Input{Up: []uint16{'Z'}})
	if x.a.artTool != toolBrush {
		t.Errorf("after zooming with Z held the tool is %v", x.a.artTool)
	}
	x.key('Z', false)
	if x.a.artTool != toolZoom {
		t.Errorf("a tap of Z chose %v", x.a.artTool)
	}
	z = ad.zoom
	cx, cy := x.viewPoint(ad, 200, 150)
	x.click(gfx.Rect{X: cx - 1, Y: cy - 1, W: 2, H: 2})
	if math.Abs(ad.zoom-2*z) > 1e-6 {
		t.Errorf("a click with the zoom tool zoomed to %v from %v", ad.zoom, z)
	}
}

// Space and the zoom key work by the mouse alone, and the tool stays as it was.
func TestHeldKeysMoveTheCanvasWithoutAButton(t *testing.T) {
	x, ad := canvasHarness(t)
	x.key('B', false)
	before := shown(ad)
	fx, fy := x.viewPoint(ad, 100, 100)
	at := func(mx float32, in Input) {
		x.mx, x.my = mx, fy
		x.frame(in)
	}
	at(fx, Input{})
	panX := ad.panX
	at(fx, Input{Keys: []Key{{Code: keySpace}}, Up: []uint16{}})
	at(fx+60, Input{Up: []uint16{}})
	at(fx+60, Input{Up: []uint16{keySpace}})
	if got, want := ad.panX, panX-60/ad.zoom; math.Abs(got-want) > 1e-6 {
		t.Fatalf("pan x %v, want %v", got, want)
	}
	zoom := ad.zoom
	at(fx+60, Input{Keys: []Key{{Code: 'Z'}}, Up: []uint16{}})
	at(fx+60, Input{Up: []uint16{}}) // the key is seen after the pointer, so it counts from the next frame
	at(fx+60+zoomScrub, Input{Up: []uint16{}})
	at(fx+60+zoomScrub, Input{Up: []uint16{'Z'}})
	at(fx+60+zoomScrub, Input{})
	if math.Abs(ad.zoom-zoom*2) > 1e-6 {
		t.Fatalf("zoom went from %v to %v", zoom, ad.zoom)
	}
	if x.a.artTool != toolBrush {
		t.Fatalf("the tool is %v", x.a.artTool)
	}
	if string(shown(ad)) != string(before) {
		t.Fatal("moving the view painted")
	}
}
