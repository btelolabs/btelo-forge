//go:build darwin

package capture

/*
#include "capture_darwin.h"
*/
import "C"

import (
	"fmt"
	"strconv"
	"strings"
)

// Displays lists the active displays on macOS with backing-pixel dimensions and a shared top-down desktop coordinate
// space aligned with internal/mac overlays and pointer positions.
func Displays() ([]Display, error) {
	var raw [32]C.cap_display_info
	n := int(C.cap_list_displays(&raw[0], C.int(len(raw))))
	if n <= 0 {
		return nil, ErrNoDisplay
	}
	list := make([]Display, 0, n)
	for i := range n {
		r := raw[i]
		list = append(list, Display{
			Adapter: 0,
			Output:  int(r.display_id),
			Device:  fmt.Sprintf("CGDisplay:%d", uint32(r.display_id)),
			Name:    C.GoString(&r.name[0]),
			X:       int(r.x),
			Y:       int(r.y),
			Width:   int(r.width),
			Height:  int(r.height),
			Primary: r.primary != 0,
		})
	}
	return list, nil
}

// displayIDOf extracts the CGDirectDisplayID from a Display.
func displayIDOf(d Display) uint32 {
	if s, ok := strings.CutPrefix(d.Device, "CGDisplay:"); ok {
		if v, err := strconv.ParseUint(s, 10, 32); err == nil && v > 0 {
			return uint32(v)
		}
	}
	if d.Output > 0 {
		return uint32(d.Output)
	}
	return 0
}

// displaySourceRect converts a display-local pixel rectangle into ScreenCaptureKit's display-local point coordinates.
func displaySourceRect(d Display, area struct{ MinX, MinY, W, H int }) (x, y, w, h float64) {
	var raw [32]C.cap_display_info
	n := int(C.cap_list_displays(&raw[0], C.int(len(raw))))
	id := displayIDOf(d)
	for i := range n {
		r := raw[i]
		if uint32(r.display_id) == id || (id == 0 && r.primary != 0) {
			sx := float64(r.width) / float64(r.bounds_w)
			sy := float64(r.height) / float64(r.bounds_h)
			if sx <= 0 {
				sx = 1
			}
			if sy <= 0 {
				sy = 1
			}
			return float64(area.MinX) / sx, float64(area.MinY) / sy, float64(area.W) / sx, float64(area.H) / sy
		}
	}
	return float64(area.MinX), float64(area.MinY), float64(area.W), float64(area.H)
}
