package mac

/*
#include "window.h"
*/
import "C"

import (
	"errors"
	"image"
	"runtime"
	"sync"
	"unsafe"
)

// OverlayOptions say how a borderless always-on-top overlay window behaves.
type OverlayOptions struct {
	Bounds       image.Rectangle // on the desktop, in physical pixels (shared coordinate contract with capture.Display)
	Activate     bool            // takes the keyboard, as picking a region does; otherwise never takes focus
	ClickThrough bool            // mouse goes through to windows below
	Excluded     bool            // left out of screen recordings and screenshots (NSWindowSharingNone)
}

// Overlay is a transparent, borderless always-on-top NSPanel over the screen.
type Overlay struct {
	id         int
	handle     unsafe.Pointer
	bounds     image.Rectangle
	input      inputQueue
	typing     bool
	imeX, imeY float32
	imeH       float32
	cursor     int
	cursorSet  bool
	closed     bool
}

var (
	overlayMu     sync.Mutex
	overlayNextID int
	overlays      = map[int]*Overlay{}
)

func overlayByID(id int) *Overlay {
	overlayMu.Lock()
	o := overlays[id]
	overlayMu.Unlock()
	return o
}

// PointerAt returns where the mouse pointer is on the desktop, in physical pixels.
func PointerAt() image.Point {
	var x, y C.int
	C.forge_pointer_at(&x, &y)
	return image.Pt(int(x), int(y))
}

// OpenOverlay creates a transparent, borderless always-on-top panel over the screen.
// Must be called on the main Cocoa thread.
func OpenOverlay(o OverlayOptions) (*Overlay, error) {
	if o.Bounds.Empty() {
		return nil, errors.New("overlay: empty bounds")
	}
	overlayMu.Lock()
	overlayNextID++
	id := overlayNextID
	overlayMu.Unlock()

	var act, ct, ex C.int
	if o.Activate {
		act = 1
	}
	if o.ClickThrough {
		ct = 1
	}
	if o.Excluded {
		ex = 1
	}
	h := C.forge_overlay_open(C.int(id), C.int(o.Bounds.Min.X), C.int(o.Bounds.Min.Y), C.int(o.Bounds.Dx()), C.int(o.Bounds.Dy()), act, ct, ex)
	if h == nil {
		return nil, errors.New("overlay: could not create panel")
	}
	ov := &Overlay{id: id, handle: h, bounds: o.Bounds}
	overlayMu.Lock()
	overlays[id] = ov
	overlayMu.Unlock()
	return ov, nil
}

// Bounds is where the overlay sits on the desktop in physical pixels.
func (o *Overlay) Bounds() image.Rectangle { return o.bounds }

// Scale is the overlay's backing scale factor (pixels per point).
func (o *Overlay) Scale() float32 {
	if o.handle == nil {
		return 1
	}
	return float32(C.forge_overlay_scale(o.handle))
}

// PresentLayered shows a premultiplied RGBA image of the overlay's size.
// Thread-safe: may be called from background goroutines (such as camera capture) as well as the main thread.
func (o *Overlay) PresentLayered(rgba []byte, width, height int, _ image.Rectangle) error {
	if o.handle == nil || o.closed {
		return nil
	}
	if width <= 0 || height <= 0 || len(rgba) < width*height*4 {
		return errors.New("present: bad image")
	}
	C.forge_overlay_present(o.handle, unsafe.Pointer(&rgba[0]), C.int(width), C.int(height))
	runtime.KeepAlive(rgba)
	return nil
}

// SetClickThrough lets the mouse pass through the overlay or captures it.
func (o *Overlay) SetClickThrough(on bool) {
	if o.handle == nil {
		return
	}
	v := C.int(0)
	if on {
		v = 1
	}
	C.forge_overlay_set_click_through(o.handle, v)
}

// TakeKeyboard brings the overlay in front to take keyboard focus, or yields focus back.
func (o *Overlay) TakeKeyboard(on bool) {
	if o.handle == nil {
		return
	}
	v := C.int(0)
	if on {
		v = 1
	}
	C.forge_overlay_take_keyboard(o.handle, v)
}

// Raise brings the overlay to the front of always-on-top windows.
func (o *Overlay) Raise() {
	if o.handle != nil {
		C.forge_overlay_raise(o.handle)
	}
}

// MoveOverlay moves/resizes the overlay to r (in desktop physical pixels).
func (o *Overlay) MoveOverlay(r image.Rectangle) {
	if o.handle == nil || r == o.bounds || r.Empty() {
		return
	}
	o.bounds = r
	C.forge_overlay_move(o.handle, C.int(r.Min.X), C.int(r.Min.Y), C.int(r.Dx()), C.int(r.Dy()))
}

// SetCursor changes the cursor shown over the overlay.
func (o *Overlay) SetCursor(kind int) {
	if o.handle == nil || (o.cursorSet && o.cursor == kind) {
		return
	}
	o.cursor, o.cursorSet = kind, true
	C.forge_overlay_cursor(o.handle, C.int(kind))
}

// TextInput turns IME text input on or off for the overlay at (x, y) in overlay pixels.
func (o *Overlay) TextInput(on bool, x, y, height float32) {
	if o.handle == nil {
		return
	}
	if !on && !o.typing {
		return
	}
	if on && o.typing && o.imeX == x && o.imeY == y && o.imeH == height {
		return
	}
	v := C.int(0)
	if on {
		v = 1
	}
	o.typing, o.imeX, o.imeY, o.imeH = on, x, y, height
	C.forge_overlay_text_input(o.handle, v, C.double(x), C.double(y), C.double(height))
}

// TakeInput returns the overlay's input since the last call and starts collecting afresh.
func (o *Overlay) TakeInput() Input {
	o.input.flip()
	return o.input.last
}

// Closed reports whether the overlay was closed.
func (o *Overlay) Closed() bool { return o.closed }

// Close closes and releases the overlay panel.
func (o *Overlay) Close() {
	if o.handle == nil {
		return
	}
	o.closed = true
	overlayMu.Lock()
	delete(overlays, o.id)
	overlayMu.Unlock()
	h := o.handle
	o.handle = nil
	C.forge_overlay_close(h)
}
