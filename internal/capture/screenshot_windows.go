package capture

import (
	"errors"
	"fmt"
	"image"
	"sync"
	"syscall"
	"unsafe"
)

var (
	gdi32               = syscall.NewLazyDLL("gdi32.dll")
	dwmapi              = syscall.NewLazyDLL("dwmapi.dll")
	getDC               = user32.NewProc("GetDC")
	releaseDC           = user32.NewProc("ReleaseDC")
	getCursorInfo       = user32.NewProc("GetCursorInfo")
	getIconInfo         = user32.NewProc("GetIconInfo")
	drawIconEx          = user32.NewProc("DrawIconEx")
	setThreadDpiContext = user32.NewProc("SetThreadDpiAwarenessContext")
	createCompatibleDC  = gdi32.NewProc("CreateCompatibleDC")
	deleteDC            = gdi32.NewProc("DeleteDC")
	createDIBSection    = gdi32.NewProc("CreateDIBSection")
	selectObject        = gdi32.NewProc("SelectObject")
	deleteObject        = gdi32.NewProc("DeleteObject")
	bitBlt              = gdi32.NewProc("BitBlt")
	gdiFlush            = gdi32.NewProc("GdiFlush")
	dwmFlush            = dwmapi.NewProc("DwmFlush")
)

type bitmapInfoHeader struct {
	Size          uint32
	Width         int32
	Height        int32
	Planes        uint16
	BitCount      uint16
	Compression   uint32
	SizeImage     uint32
	XPelsPerMeter int32
	YPelsPerMeter int32
	ClrUsed       uint32
	ClrImportant  uint32
}

type cursorInfo struct {
	Size      uint32
	Flags     uint32
	Cursor    uintptr
	ScreenPos struct{ X, Y int32 }
}

type iconInfo struct {
	Icon     int32
	XHotspot uint32
	YHotspot uint32
	Mask     uintptr
	Color    uintptr
}

// grabScreen reads the display region directly from DWM's composited desktop, which leaves out windows marked with
// WDA_EXCLUDEFROMCAPTURE. When flush is set, it waits one DWM frame first so a window just marked excluded is gone.
func grabScreen(d Display, area image.Rectangle, cursor, flush bool) (*image.RGBA, error) {
	w, h := area.Dx(), area.Dy()
	if w <= 0 || h <= 0 {
		return nil, errors.New("empty screenshot region")
	}
	const perMonitorV2 = ^uintptr(3)
	if prev, _, _ := setThreadDpiContext.Call(perMonitorV2); prev != 0 {
		defer setThreadDpiContext.Call(prev)
	}
	if flush {
		dwmFlush.Call()
	}
	screenDC, _, _ := getDC.Call(0)
	if screenDC == 0 {
		return nil, errors.New("GetDC failed")
	}
	defer releaseDC.Call(0, screenDC)

	memDC, _, _ := createCompatibleDC.Call(0)
	if memDC == 0 {
		return nil, errors.New("CreateCompatibleDC failed")
	}
	defer deleteDC.Call(memDC)

	bmi := bitmapInfoHeader{
		Size:     40,
		Width:    int32(w),
		Height:   -int32(h), // top-down rows
		Planes:   1,
		BitCount: 32,
	}
	var bits unsafe.Pointer
	bmp, _, err := createDIBSection.Call(memDC, uintptr(unsafe.Pointer(&bmi)), 0, uintptr(unsafe.Pointer(&bits)), 0, 0)
	if bmp == 0 || bits == nil {
		return nil, fmt.Errorf("CreateDIBSection: %w", err)
	}
	defer deleteObject.Call(bmp)

	old, _, _ := selectObject.Call(memDC, bmp)
	defer selectObject.Call(memDC, old)

	sx, sy := d.X+area.Min.X, d.Y+area.Min.Y
	const srccopy = 0x00CC0020
	if r, _, err := bitBlt.Call(memDC, 0, 0, uintptr(w), uintptr(h), screenDC, uintptr(sx), uintptr(sy), srccopy); r == 0 {
		return nil, fmt.Errorf("BitBlt: %w", err)
	}
	if cursor {
		drawCursorOn(memDC, sx, sy, w, h)
	}
	gdiFlush.Call()

	out := image.NewRGBA(image.Rect(0, 0, w, h))
	bgraToRGBA(out.Pix, unsafe.Slice((*byte)(bits), w*h*4))
	return out, nil
}

// drawCursorOn draws the visible system cursor onto memDC when it lies near the captured rectangle.
func drawCursorOn(memDC uintptr, sx, sy, w, h int) {
	var ci cursorInfo
	ci.Size = uint32(unsafe.Sizeof(ci))
	const cursorShowing = 0x00000001
	if r, _, _ := getCursorInfo.Call(uintptr(unsafe.Pointer(&ci))); r == 0 || ci.Flags&cursorShowing == 0 || ci.Cursor == 0 {
		return
	}
	var ii iconInfo
	if r, _, _ := getIconInfo.Call(ci.Cursor, uintptr(unsafe.Pointer(&ii))); r != 0 {
		if ii.Mask != 0 {
			deleteObject.Call(ii.Mask)
		}
		if ii.Color != 0 {
			deleteObject.Call(ii.Color)
		}
	}
	cx := int(ci.ScreenPos.X) - int(ii.XHotspot) - sx
	cy := int(ci.ScreenPos.Y) - int(ii.YHotspot) - sy
	if cx < -128 || cy < -128 || cx > w+128 || cy > h+128 {
		return
	}
	const diNormal = 0x0003
	drawIconEx.Call(memDC, uintptr(cx), uintptr(cy), ci.Cursor, 0, 0, 0, 0, diNormal)
}

// bgraToRGBA swaps blue and red and sets every pixel opaque, four bytes at a time, in parallel bands.
func bgraToRGBA(dst, src []byte) {
	d := unsafe.Slice((*uint32)(unsafe.Pointer(unsafe.SliceData(dst))), len(dst)/4)
	s := unsafe.Slice((*uint32)(unsafe.Pointer(unsafe.SliceData(src))), len(src)/4)
	swap := func(d, s []uint32) {
		for i, v := range s {
			d[i] = v&0x0000ff00 | v>>16&0xff | v&0xff<<16 | 0xff000000
		}
	}
	const band = 1 << 18
	if len(s) <= band {
		swap(d, s)
		return
	}
	var wg sync.WaitGroup
	for from := 0; from < len(s); from += band {
		to := min(from+band, len(s))
		wg.Add(1)
		go func() {
			defer wg.Done()
			swap(d[from:to], s[from:to])
		}()
	}
	wg.Wait()
}
