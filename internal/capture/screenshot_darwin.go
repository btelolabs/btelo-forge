//go:build darwin

package capture

/*
#include "capture_darwin.h"
*/
import "C"

import (
	"errors"
	"fmt"
	"image"
	"unsafe"
)

// grabScreen captures the display or region using ScreenCaptureKit's SCScreenshotManager (macOS 14+), honoring cursor
// visibility and NSWindowSharingNone window exclusion.
func grabScreen(d Display, area image.Rectangle, cursor, flush bool) (*image.RGBA, error) {
	w, h := area.Dx(), area.Dy()
	if w <= 0 || h <= 0 {
		return nil, errors.New("empty screenshot region")
	}
	if !isAppBundle() && !hasScreenPermission() {
		return nil, ErrScreenPermission
	}
	sx, sy, sw, sh := displaySourceRect(d, struct{ MinX, MinY, W, H int }{area.Min.X, area.Min.Y, w, h})
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	var errBuf [256]C.char
	cur, fl := C.int(0), C.int(0)
	if cursor {
		cur = 1
	}
	if flush {
		fl = 1
	}
	rc := C.cap_grab_screenshot(
		C.uint32_t(displayIDOf(d)),
		C.double(sx), C.double(sy), C.double(sw), C.double(sh),
		C.int(w), C.int(h), cur, fl,
		(*C.uint8_t)(unsafe.Pointer(&img.Pix[0])),
		&errBuf[0], C.int(len(errBuf)),
	)
	switch rc {
	case C.CAP_OK:
		return img, nil
	case C.CAP_ERR_PERMISSION:
		return nil, ErrScreenPermission
	case C.CAP_ERR_UNSUPPORTED:
		return nil, fmt.Errorf("%w: %s", ErrUnsupported, C.GoString(&errBuf[0]))
	default:
		return nil, fmt.Errorf("screenshot: %s", C.GoString(&errBuf[0]))
	}
}
