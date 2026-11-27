//go:build darwin

package capture

/*
#include <stdlib.h>
#include "capture_darwin.h"
*/
import "C"

import (
	"context"
	"errors"
	"fmt"
	"image"
	"runtime"
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

// grabDisplays captures all displays concurrently using one SCShareableContent lookup and parallel SCScreenshotManager
// requests + SIMD vImage BGRA→RGBA conversion.
func grabDisplays(_ context.Context, displays []Display, cursor, flush bool) ([]*image.RGBA, error) {
	if !isAppBundle() && !hasScreenPermission() {
		return nil, ErrScreenPermission
	}
	if len(displays) == 0 {
		return nil, ErrNoDisplay
	}
	imgs := make([]*image.RGBA, len(displays))
	// Allocate C array with C.calloc so Go pointers inside struct fields never violate cgo pointer-passing rules.
	// Wait: cgo forbids storing Go pointers (img.Pix) inside C-allocated memory too! Instead, for single display
	// call cap_grab_screenshot directly, or pass pinned buffers / per-display C buffers and copy, OR use runtime.Pinner!
	// runtime.Pinner (Go 1.21+) cleanly pins img.Pix so C can write directly into img.Pix with zero extra copy!
	var pinner runtime.Pinner
	defer pinner.Unpin()

	cShots := (*C.cap_display_shot)(C.calloc(C.size_t(len(displays)), C.size_t(unsafe.Sizeof(C.cap_display_shot{}))))
	if cShots == nil {
		return nil, errors.New("screenshot: out of memory")
	}
	defer C.free(unsafe.Pointer(cShots))
	shotSlice := unsafe.Slice(cShots, len(displays))

	for i, d := range displays {
		if d.Width <= 0 || d.Height <= 0 {
			return nil, errors.New("empty display bounds")
		}
		img := image.NewRGBA(image.Rect(0, 0, d.Width, d.Height))
		pinner.Pin(&img.Pix[0])
		imgs[i] = img
		shotSlice[i] = C.cap_display_shot{
			display_id: C.uint32_t(displayIDOf(d)),
			out_w:      C.int(d.Width),
			out_h:      C.int(d.Height),
			rgba_out:   (*C.uint8_t)(unsafe.Pointer(&img.Pix[0])),
		}
	}

	var errBuf [256]C.char
	cur, fl := C.int(0), C.int(0)
	if cursor {
		cur = 1
	}
	if flush {
		fl = 1
	}
	rc := C.cap_grab_displays(cShots, C.int(len(displays)), cur, fl, &errBuf[0], C.int(len(errBuf)))
	switch rc {
	case C.CAP_OK:
		return imgs, nil
	case C.CAP_ERR_PERMISSION:
		return nil, ErrScreenPermission
	case C.CAP_ERR_UNSUPPORTED:
		return nil, fmt.Errorf("%w: %s", ErrUnsupported, C.GoString(&errBuf[0]))
	default:
		return nil, fmt.Errorf("screenshot: %s", C.GoString(&errBuf[0]))
	}
}
