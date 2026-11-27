package capture

import "image"

// WindowRect is a visible window, panel, menu, or popup on the desktop in front-to-back Z-order,
// using the same desktop physical pixel coordinate space as Display.
type WindowRect struct {
	Rect  image.Rectangle
	Title string
}

// Windows returns visible on-screen windows and popups in front-to-back Z-order, excluding desktop backdrops,
// invisible or cloaked windows, and windows excluded from screen capture.
func Windows() []WindowRect {
	return listWindows()
}
