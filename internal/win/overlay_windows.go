package win

import (
	"errors"
	"fmt"
	"image"
	"sync"
	"sync/atomic"
	"syscall"
	"unsafe"
)

var (
	createCompatibleDC    = gdi32.NewProc("CreateCompatibleDC")
	createDIBSection      = gdi32.NewProc("CreateDIBSection")
	selectObject          = gdi32.NewProc("SelectObject")
	deleteObject          = gdi32.NewProc("DeleteObject")
	deleteDC              = gdi32.NewProc("DeleteDC")
	updateLayeredIndirect = user32.NewProc("UpdateLayeredWindowIndirect")
	getWindowLong         = user32.NewProc("GetWindowLongPtrW")
	setWindowLong         = user32.NewProc("SetWindowLongPtrW")
	setWindowPos          = user32.NewProc("SetWindowPos")
	isWindow              = user32.NewProc("IsWindow")
	getCursorPos          = user32.NewProc("GetCursorPos")
	overlayClassOnce      sync.Once
	overlayClassErr       error
	overlayClass, _       = syscall.UTF16PtrFromString("BteloForgeOverlay")
	mainWindow            atomic.Uintptr // the first window Open made, which overlays pass requests to close to
)

const (
	exLayered     = 0x00080000
	exTopmost     = 0x00000008
	exToolWindow  = 0x00000080
	exNoActivate  = 0x08000000
	exTransparent = 0x00000020
	stylePopup    = 0x80000000
	gwlExStyle    = ^uintptr(19) // -20
)

// Cross is the crosshair cursor, for picking a region or drawing on the screen.
const Cross Cursor = 32515

// OverlayOptions say how an overlay window behaves.
type OverlayOptions struct {
	Bounds       image.Rectangle // on the desktop, in physical pixels
	Activate     bool            // takes the keyboard, as picking a region does; otherwise it never takes the focus
	ClickThrough bool            // the mouse goes through to the windows below
	Excluded     bool            // left out of screen recordings and screenshots
}

// overlayState is what an overlay window has besides a normal window's input.
type overlayState struct {
	bounds       image.Rectangle
	memDC        uintptr
	bitmap       uintptr
	bits         []byte // the DIB section's premultiplied BGRA pixels
	size         image.Point
	clickThru    bool
	forwardClose bool    // a request to close goes to the main window
	keyboard     bool    // it takes the keyboard (TakeKeyboard)
	before       uintptr // the window in front before it took the keyboard
}

// OpenOverlay creates a borderless, always-on-top window whose pixels can be partly transparent: the region picker,
// the drawing over the screen and the floating toolbar. It belongs to the thread that opened the main window, whose
// Poll handles its messages; read its input with TakeInput.
func OpenOverlay(o OverlayOptions) (*Window, error) {
	if o.Bounds.Empty() {
		return nil, errors.New("overlay: empty bounds")
	}
	instance, _, _ := kernel32.NewProc("GetModuleHandleW").Call(0)
	overlayClassOnce.Do(func() {
		arrow, _, _ := user32.NewProc("LoadCursorW").Call(0, uintptr(Arrow))
		wc := &windowClass{Style: classDblClicks, Proc: windowCallback, Instance: instance, Cursor: arrow, Name: overlayClass}
		wc.Size = uint32(unsafe.Sizeof(*wc))
		if r, _, e := registerClass.Call(uintptr(unsafe.Pointer(wc))); r == 0 {
			overlayClassErr = fmt.Errorf("RegisterClassExW: %w", e)
		}
	})
	if overlayClassErr != nil {
		return nil, overlayClassErr
	}
	ex := uintptr(exLayered | exTopmost | exToolWindow)
	if !o.Activate {
		ex |= exNoActivate
	}
	if o.ClickThrough {
		ex |= exTransparent
	}
	b := o.Bounds
	hwnd, _, e := createWindow.Call(ex, uintptr(unsafe.Pointer(overlayClass)), 0, stylePopup,
		uintptr(b.Min.X), uintptr(b.Min.Y), uintptr(b.Dx()), uintptr(b.Dy()), 0, 0, instance, 0)
	if hwnd == 0 {
		return nil, fmt.Errorf("CreateWindowExW: %w", e)
	}
	w := &Window{Handle: hwnd, cursor: Arrow, overlay: &overlayState{bounds: b, clickThru: o.ClickThrough, forwardClose: !o.Activate}}
	windows.Store(hwnd, w)
	// Without an IME keys come as themselves, as the brush keys over a drawing need, until TextInput asks for it.
	if err := associateIME(hwnd, false); err != nil {
		w.Close()
		return nil, err
	}
	if o.Excluded {
		if err := w.ExcludeFromCapture(true); err != nil {
			w.Close()
			return nil, err
		}
	}
	// It shows on its first PresentLayered; until then a layered window has no pixels.
	if o.Activate {
		const show = 5
		showWindow.Call(hwnd, show)
		setForeground.Call(hwnd)
	} else {
		const showNoActivate = 4
		showWindow.Call(hwnd, showNoActivate)
	}
	return w, nil
}

// Bounds is where the overlay is on the desktop.
func (w *Window) Bounds() image.Rectangle {
	if w.overlay == nil {
		return image.Rectangle{}
	}
	return w.overlay.bounds
}

// PresentLayered shows a premultiplied RGBA image the overlay's size, updating the screen within dirty only
// (everything when dirty is empty).
func (w *Window) PresentLayered(rgba []byte, width, height int, dirty image.Rectangle) error {
	o := w.overlay
	if o == nil {
		return errors.New("present: not an overlay")
	}
	if width <= 0 || height <= 0 || len(rgba) < width*height*4 {
		return errors.New("present: bad image")
	}
	full := image.Rect(0, 0, width, height)
	if o.size != full.Size() {
		if err := o.allocate(width, height); err != nil {
			return err
		}
		dirty = full
	}
	if dirty.Empty() {
		dirty = full
	}
	dirty = dirty.Intersect(full)
	for y := dirty.Min.Y; y < dirty.Max.Y; y++ {
		i := (y*width + dirty.Min.X) * 4
		n := dirty.Dx() * 4
		src, dst := rgba[i:i+n], o.bits[i:i+n]
		for j := 0; j < n; j += 4 {
			dst[j], dst[j+1], dst[j+2], dst[j+3] = src[j+2], src[j+1], src[j], src[j+3]
		}
	}
	type point struct{ X, Y int32 }
	pos := point{int32(o.bounds.Min.X), int32(o.bounds.Min.Y)}
	size := point{int32(width), int32(height)}
	var src point
	blend := [4]byte{0, 0, 255, 1} // AC_SRC_OVER, no flags, opaque constant alpha, AC_SRC_ALPHA
	rc := rect{int32(dirty.Min.X), int32(dirty.Min.Y), int32(dirty.Max.X), int32(dirty.Max.Y)}
	info := struct {
		Size  uint32
		Dst   uintptr
		DstPt *point
		Sz    *point
		Src   uintptr
		SrcPt *point
		Key   uint32
		Blend *[4]byte
		Flags uint32
		Dirty *rect
	}{Dst: 0, DstPt: &pos, Sz: &size, Src: o.memDC, SrcPt: &src, Blend: &blend, Flags: 2, Dirty: &rc} // ULW_ALPHA
	info.Size = uint32(unsafe.Sizeof(info))
	if r, _, e := updateLayeredIndirect.Call(w.Handle, uintptr(unsafe.Pointer(&info))); r == 0 {
		return fmt.Errorf("UpdateLayeredWindowIndirect: %w", e)
	}
	return nil
}

// allocate makes the DIB section the overlay is drawn from.
func (o *overlayState) allocate(width, height int) error {
	o.release()
	dc, _, _ := createCompatibleDC.Call(0)
	if dc == 0 {
		return errors.New("CreateCompatibleDC failed")
	}
	header := struct {
		Size                   uint32
		Width, Height          int32
		Planes, Bits           uint16
		Compression, ImageSize uint32
		X, Y                   int32
		Used, Important        uint32
	}{Size: 40, Width: int32(width), Height: -int32(height), Planes: 1, Bits: 32}
	var bits unsafe.Pointer
	bmp, _, e := createDIBSection.Call(dc, uintptr(unsafe.Pointer(&header)), 0, uintptr(unsafe.Pointer(&bits)), 0, 0)
	if bmp == 0 {
		deleteDC.Call(dc)
		return fmt.Errorf("CreateDIBSection: %w", e)
	}
	selectObject.Call(dc, bmp)
	o.memDC, o.bitmap, o.size = dc, bmp, image.Pt(width, height)
	o.bits = unsafe.Slice((*byte)(bits), width*height*4)
	return nil
}

func (o *overlayState) release() {
	if o.memDC != 0 {
		deleteDC.Call(o.memDC)
		deleteObject.Call(o.bitmap)
		o.memDC, o.bitmap, o.bits, o.size = 0, 0, nil, image.Point{}
	}
}

// SetClickThrough lets the mouse go through the overlay to the windows below, or catches it.
func (w *Window) SetClickThrough(on bool) {
	o := w.overlay
	if o == nil || o.clickThru == on {
		return
	}
	o.clickThru = on
	ex, _, _ := getWindowLong.Call(w.Handle, gwlExStyle)
	if on {
		ex |= exTransparent
	} else {
		ex &^= exTransparent
	}
	setWindowLong.Call(w.Handle, gwlExStyle, ex)
}

// TakeKeyboard brings an overlay opened without Activate in front to take the keyboard, as drawing on the screen
// does, or gives the keyboard back to the window that had it. While it has the keyboard, a request to close (Alt+F4)
// closes the overlay rather than the app.
func (w *Window) TakeKeyboard(on bool) {
	o := w.overlay
	if o == nil || o.keyboard == on {
		return
	}
	o.keyboard, o.forwardClose = on, !on
	ex, _, _ := getWindowLong.Call(w.Handle, gwlExStyle)
	if on {
		o.before, _, _ = getForeground.Call()
		setWindowLong.Call(w.Handle, gwlExStyle, ex&^exNoActivate)
		setForeground.Call(w.Handle)
		return
	}
	setWindowLong.Call(w.Handle, gwlExStyle, ex|exNoActivate)
	if front, _, _ := getForeground.Call(); front == w.Handle && o.before != 0 && o.before != w.Handle {
		if alive, _, _ := isWindow.Call(o.before); alive != 0 {
			setForeground.Call(o.before)
		}
	}
	o.before = 0
}

// Raise brings the overlay back to the front of the always-on-top windows, without activating it; one that has taken
// the keyboard takes it again, which a press on another of the app's overlays lets it do even when Windows refused
// it at first, as it may when another program was in front.
func (w *Window) Raise() {
	o := w.overlay
	if o == nil {
		return
	}
	const noSize, noMove, noActivate = 0x0001, 0x0002, 0x0010
	topmost := ^uintptr(0) // HWND_TOPMOST
	setWindowPos.Call(w.Handle, topmost, 0, 0, 0, 0, noSize|noMove|noActivate)
	if o.keyboard {
		setForeground.Call(w.Handle)
	}
}

// MoveOverlay puts the overlay at r, such as a toolbar being dragged.
func (w *Window) MoveOverlay(r image.Rectangle) {
	o := w.overlay
	if o == nil || r == o.bounds {
		return
	}
	o.bounds = r
	const noZOrder, noActivate = 0x0004, 0x0010
	setWindowPos.Call(w.Handle, 0, uintptr(r.Min.X), uintptr(r.Min.Y), uintptr(r.Dx()), uintptr(r.Dy()), noZOrder|noActivate)
}

// TakeInput returns the overlay's input since the last call and starts collecting afresh.
func (w *Window) TakeInput() Input {
	in := w.input
	in.Pointer = append([]PointerEvent(nil), w.input.Pointer...)
	in.Keys = append([]KeyEvent(nil), w.input.Keys...)
	in.Up = append([]uint16(nil), w.input.Up...)
	in.Typed = append([]rune(nil), w.input.Typed...)
	in.Dropped, in.Hotkeys = nil, nil
	w.input.Pointer = w.input.Pointer[:0]
	w.input.Keys = w.input.Keys[:0]
	w.input.Up = w.input.Up[:0]
	w.input.Typed = w.input.Typed[:0]
	w.input.Wheel = 0
	in.Ctrl, in.Shift, in.Alt = held(0x11), held(0x10), held(0x12)
	return in
}

// PointerAt is where the mouse pointer is on the desktop, in physical pixels.
func PointerAt() image.Point {
	var p struct{ X, Y int32 }
	getCursorPos.Call(uintptr(unsafe.Pointer(&p)))
	return image.Pt(int(p.X), int(p.Y))
}

// Closed reports whether the overlay was asked to close, such as with Alt+F4.
func (w *Window) Closed() bool { return w.closed }
