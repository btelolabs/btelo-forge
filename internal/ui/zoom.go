package ui

import (
	"math"
	"slices"

	"github.com/btelolabs/btelo-forge/internal/gfx"
)

// The window's zoom, a browser's: Ctrl with = and - make the whole interface and what it shows larger and smaller in
// steps, and Ctrl+0 brings it back to its own size. It is kept between runs.

// zoomLevels are the zoom's steps, a browser's.
var zoomLevels = []float32{0.5, 0.67, 0.75, 0.8, 0.9, 1, 1.1, 1.25, 1.5, 1.75, 2, 2.5, 3}

// windowZoom is the window's zoom; 1 is its own size.
func (a *App) windowZoom() float32 {
	if z := a.settings.Zoom; z >= zoomLevels[0] && z <= zoomLevels[len(zoomLevels)-1] {
		return z
	}
	return 1
}

// stepWindowZoom goes to the next zoom step up (dir 1) or down (-1) from the zoom now.
func (a *App) stepWindowZoom(dir int) {
	i, exact := slices.BinarySearch(zoomLevels, a.windowZoom())
	switch {
	case dir > 0 && exact:
		i++
	case dir < 0:
		i--
	}
	a.setWindowZoom(zoomLevels[min(max(i, 0), len(zoomLevels)-1)])
}

// setWindowZoom zooms the window to z. The canvases grow and shrink with the interface about their centres, so
// their pictures keep their size beside it; a fitted one fits its new view.
func (a *App) setWindowZoom(z float32) {
	if old := a.windowZoom(); z != old {
		for _, d := range a.docs {
			if ad := d.art; ad != nil {
				ad.zoom = min(max(ad.zoom*float64(z/old), minZoom), maxZoom)
				ad.fitFor = gfx.Rect{}
			}
		}
		a.u.s *= z / old // this frame is drawn at the new size already
		a.settings.Zoom = z
		if z == 1 {
			a.settings.Zoom = 0 // its own size is the default, which is not written
		}
		a.saveSettings()
		a.requestFrame()
	}
	a.toastOnly("zoom", a.u.tf("Zoom %d%%", int(math.Round(float64(z)*100))))
}
