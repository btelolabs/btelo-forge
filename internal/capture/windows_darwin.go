//go:build darwin

package capture

/*
#include "capture_darwin.h"
*/
import "C"

import "image"

func listWindows() []WindowRect {
	var raw [256]C.cap_window_info
	n := int(C.cap_list_windows(&raw[0], C.int(len(raw))))
	if n <= 0 {
		return nil
	}
	out := make([]WindowRect, 0, n)
	for i := range n {
		r := raw[i]
		rect := image.Rect(int(r.x), int(r.y), int(r.x)+int(r.w), int(r.y)+int(r.h))
		if rect.Empty() {
			continue
		}
		out = append(out, WindowRect{
			Rect:  rect,
			Title: C.GoString(&r.title[0]),
		})
	}
	return out
}
