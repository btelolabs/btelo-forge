package capture

import (
	"context"
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

// grabDisplays captures all displays in one DWM flush + one desktop DC acquisition, converting BGRA→RGBA in parallel
// across displays for instant multi-monitor freeze-frame screenshots.
func grabDisplays(ctx context.Context, displays []Display, cursor, flush bool) ([]*image.RGBA, error) {
	const perMonitorV2 = ^uintptr(3)
	if prev, _, _ := setThreadDpiContext.Call(perMonitorV2); prev != 0 {
		defer setThreadDpiContext.Call(prev)
	}
	if flush {
		dwmFlush.Call()
	}
	screenDC, _, _ := getDC.Call(0)
	if screenDC == 0 {
		// Fallback per display if GetDC fails.
		out := make([]*image.RGBA, len(displays))
		for i, d := range displays {
			img, err := GrabScreenshot(ctx, d, image.Rectangle{}, cursor, false)
			if err != nil {
				return nil, err
			}
			out[i] = img
		}
		return out, nil
	}
	defer releaseDC.Call(0, screenDC)

	type grabbed struct {
		memDC uintptr
		bmp   uintptr
		old   uintptr
		bits  []byte
		w, h  int
	}
	raw := make([]grabbed, len(displays))
	defer func() {
		for _, g := range raw {
			if g.memDC != 0 {
				if g.old != 0 {
					selectObject.Call(g.memDC, g.old)
				}
				if g.bmp != 0 {
					deleteObject.Call(g.bmp)
				}
				deleteDC.Call(g.memDC)
			}
		}
	}()

	const srccopy = 0x00CC0020
	for i, d := range displays {
		w, h := d.Width, d.Height
		if w <= 0 || h <= 0 {
			return nil, errors.New("empty display bounds")
		}
		memDC, _, _ := createCompatibleDC.Call(0)
		if memDC == 0 {
			return nil, errors.New("CreateCompatibleDC failed")
		}
		bmi := bitmapInfoHeader{Size: 40, Width: int32(w), Height: -int32(h), Planes: 1, BitCount: 32}
		var bits unsafe.Pointer
		bmp, _, err := createDIBSection.Call(memDC, uintptr(unsafe.Pointer(&bmi)), 0, uintptr(unsafe.Pointer(&bits)), 0, 0)
		if bmp == 0 || bits == nil {
			deleteDC.Call(memDC)
			return nil, fmt.Errorf("CreateDIBSection: %w", err)
		}
		old, _, _ := selectObject.Call(memDC, bmp)
		raw[i] = grabbed{memDC: memDC, bmp: bmp, old: old, bits: unsafe.Slice((*byte)(bits), w*h*4), w: w, h: h}
		if r, _, err := bitBlt.Call(memDC, 0, 0, uintptr(w), uintptr(h), screenDC, uintptr(d.X), uintptr(d.Y), srccopy); r == 0 {
			return nil, fmt.Errorf("BitBlt: %w", err)
		}
		if cursor {
			drawCursorOn(memDC, d.X, d.Y, w, h)
		}
	}
	gdiFlush.Call()

	out := make([]*image.RGBA, len(displays))
	var wg sync.WaitGroup
	for i := range raw {
		g := raw[i]
		img := image.NewRGBA(image.Rect(0, 0, g.w, g.h))
		out[i] = img
		wg.Add(1)
		go func() {
			defer wg.Done()
			bgraToRGBA(img.Pix, g.bits)
		}()
	}
	wg.Wait()
	return out, nil
}
