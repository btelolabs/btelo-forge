// Package win owns the native Windows window, its event thread and GDI presentation, without cgo.
package win

import (
	"errors"
	"fmt"
	"runtime"
	"sync"
	"syscall"
	"unsafe"
)

var (
	user32           = syscall.NewLazyDLL("user32.dll")
	kernel32         = syscall.NewLazyDLL("kernel32.dll")
	gdi32            = syscall.NewLazyDLL("gdi32.dll")
	registerClass    = user32.NewProc("RegisterClassExW")
	createWindow     = user32.NewProc("CreateWindowExW")
	destroyWindow    = user32.NewProc("DestroyWindow")
	defWindowProc    = user32.NewProc("DefWindowProcW")
	peekMessage      = user32.NewProc("PeekMessageW")
	getFocus         = user32.NewProc("GetFocus")
	postMessage      = user32.NewProc("PostMessageW")
	translateMessage = user32.NewProc("TranslateMessage")
	dispatchMessage  = user32.NewProc("DispatchMessageW")
	getClientRect    = user32.NewProc("GetClientRect")
	setWindowText    = user32.NewProc("SetWindowTextW")
	getDpiForWindow  = user32.NewProc("GetDpiForWindow")
	setCapture       = user32.NewProc("SetCapture")
	releaseCapture   = user32.NewProc("ReleaseCapture")
	getKeyState      = user32.NewProc("GetKeyState")
	showWindow       = user32.NewProc("ShowWindow")
	setForeground    = user32.NewProc("SetForegroundWindow")
	validateRect     = user32.NewProc("ValidateRect")
	msgWait          = user32.NewProc("MsgWaitForMultipleObjectsEx")
	setDpiContext    = user32.NewProc("SetProcessDpiAwarenessContext")
	moveMemory       = kernel32.NewProc("RtlMoveMemory")
	getDC            = user32.NewProc("GetDC")
	releaseDC        = user32.NewProc("ReleaseDC")
	setDIBits        = gdi32.NewProc("SetDIBitsToDevice")
	windows          sync.Map
	classOnce        sync.Once
	classErr         error
	className, _     = syscall.UTF16PtrFromString("BteloForgeWindow")
	windowCallback   = syscall.NewCallback(windowProc)
)

const (
	wmApp           = 0x8000
	wmSize          = 0x0005
	wmPaint         = 0x000f
	wmClose         = 0x0010
	wmQuit          = 0x0012
	wmEraseBkgnd    = 0x0014
	wmSetCursor     = 0x0020
	wmGetMinMaxInfo = 0x0024
	styleWindow     = 0x00CF0000 // WS_OVERLAPPEDWINDOW
	classDblClicks  = 0x0008
)

type rect struct{ Left, Top, Right, Bottom int32 }

type message struct {
	Window         uintptr
	ID             uint32
	WParam, LParam uintptr
	Time           uint32
	X, Y           int32
	Private        uint32
}

type windowClass struct {
	Size, Style                        uint32
	Proc                               uintptr
	ClassExtra, WindowExtra            int32
	Instance, Icon, Cursor, Background uintptr
	Menu, Name                         *uint16
	SmallIcon                          uintptr
}

type minMaxInfo struct {
	Reserved, MaxSize, MaxPosition, MinTrackSize, MaxTrackSize [2]int32
}

// Window must be used and closed by the goroutine that called Open.
type Window struct {
	Handle  uintptr
	closed  bool
	input   Input
	msg     message
	bounds  rect
	typing  bool
	high    uint16
	cursor  Cursor
	minSize [2]int32
	bgra    []byte
	hotkeys []Hotkey      // registered with SetHotkeys, by id less one
	overlay *overlayState // set for a window made by OpenOverlay
	// Refresh runs on WM_SIZE and WM_PAINT, so the window keeps drawing while Windows runs its modal resize loop.
	Refresh func()
}

// Placement chooses where Open puts the window.
type Placement int

const (
	// Foreground opens on the primary display and takes the keyboard focus.
	Foreground Placement = iota
	// Background opens on a secondary display when there is one and never takes the focus.
	Background
)

// EnableDPI makes the process per-monitor DPI aware; call it before Open.
func EnableDPI() {
	const perMonitorV2 = ^uintptr(3)
	setDpiContext.Call(perMonitorV2)
}

// Open creates a window whose client area is width×height logical pixels, scaled by the target display's DPI.
func Open(title string, width, height int, at Placement) (*Window, error) {
	t, err := syscall.UTF16PtrFromString(title)
	if err != nil {
		return nil, err
	}
	runtime.LockOSThread()
	instance, _, _ := kernel32.NewProc("GetModuleHandleW").Call(0)
	classOnce.Do(func() {
		arrow, _, _ := user32.NewProc("LoadCursorW").Call(0, uintptr(Arrow))
		icon, _, _ := user32.NewProc("LoadIconW").Call(instance, 1)
		wc := &windowClass{Style: classDblClicks, Proc: windowCallback, Instance: instance, Cursor: arrow, Icon: icon, SmallIcon: icon, Name: className}
		wc.Size = uint32(unsafe.Sizeof(*wc))
		if r, _, e := registerClass.Call(uintptr(unsafe.Pointer(wc))); r == 0 {
			classErr = fmt.Errorf("RegisterClassExW: %w", e)
		}
	})
	if classErr != nil {
		runtime.UnlockOSThread()
		return nil, classErr
	}
	m := chooseMonitor(at == Background)
	scale := float64(m.dpi) / 96
	cw, ch := int32(float64(width)*scale), int32(float64(height)*scale)
	frame := rect{0, 0, cw, ch}
	user32.NewProc("AdjustWindowRectExForDpi").Call(uintptr(unsafe.Pointer(&frame)), styleWindow, 0, 0, uintptr(m.dpi))
	fw, fh := frame.Right-frame.Left, frame.Bottom-frame.Top
	work := m.work
	fw, fh = min(fw, work.Right-work.Left), min(fh, work.Bottom-work.Top)
	x := work.Left + (work.Right-work.Left-fw)/2
	y := work.Top + (work.Bottom-work.Top-fh)/2
	hwnd, _, e := createWindow.Call(0, uintptr(unsafe.Pointer(className)), uintptr(unsafe.Pointer(t)), styleWindow,
		uintptr(x), uintptr(y), uintptr(fw), uintptr(fh), 0, 0, instance, 0)
	if hwnd == 0 {
		runtime.UnlockOSThread()
		return nil, fmt.Errorf("CreateWindowExW: %w", e)
	}
	w := &Window{Handle: hwnd, cursor: Arrow, minSize: [2]int32{int32(960 * scale), int32(600 * scale)}}
	w.input.Inactive = true // until the window gets the keyboard focus
	mainWindow.CompareAndSwap(0, hwnd)
	windows.Store(hwnd, w)
	if err := associateIME(hwnd, false); err != nil {
		w.Close()
		return nil, err
	}
	if err := acceptFiles(hwnd); err != nil {
		w.Close()
		return nil, err
	}
	if at == Background {
		const showNoActivate = 4
		showWindow.Call(hwnd, showNoActivate)
	} else {
		const showNormal = 1
		showWindow.Call(hwnd, showNormal)
		setForeground.Call(hwnd)
	}
	return w, nil
}

func windowProc(hwnd uintptr, msg uint32, wp, lp uintptr) uintptr {
	if value, ok := windows.Load(hwnd); ok {
		w := value.(*Window)
		switch msg {
		case wmGetMinMaxInfo:
			var info minMaxInfo
			moveMemory.Call(uintptr(unsafe.Pointer(&info)), lp, unsafe.Sizeof(info))
			info.MinTrackSize = w.minSize
			moveMemory.Call(lp, uintptr(unsafe.Pointer(&info)), unsafe.Sizeof(info))
			return 0
		case wmSetCursor:
			if uint16(lp) == 1 {
				w.applyCursor()
				return 1
			}
		case wmSize, wmPaint:
			if w.Refresh != nil {
				w.Refresh()
			}
			if msg == wmPaint {
				validateRect.Call(hwnd, 0)
				return 0
			}
		case wmClose:
			// Closing the app from outside (taskkill, an installer) may reach an overlay first; the toolbar and the
			// drawing pass it on to the main window, while the region picker takes it as cancelling.
			if o := w.overlay; o != nil && o.forwardClose && mainWindow.Load() != 0 {
				postMessage.Call(mainWindow.Load(), wmClose, 0, 0)
				return 0
			}
			w.closed = true
			return 0
		case wmApp:
			w.input.Woken = true
			return 0
		case wmCopyData:
			if w.takeForwarded(lp) {
				return 1
			}
		}
		if w.mouse(msg, wp, lp) || w.key(msg, wp) || w.char(msg, wp) || w.drop(msg, wp) || w.hotkey(msg, wp) {
			return 0
		}
	}
	if msg == wmEraseBkgnd {
		return 1
	}
	r, _, _ := syscall.SyscallN(defWindowProc.Addr(), hwnd, uintptr(msg), wp, lp)
	return r
}

// Poll clears the previous frame's events and collects new ones; it returns false once the window is closed.
func (w *Window) Poll() bool {
	w.input.Pointer = w.input.Pointer[:0]
	w.input.Keys = w.input.Keys[:0]
	w.input.Up = w.input.Up[:0]
	w.input.Typed = w.input.Typed[:0]
	w.input.Dropped = w.input.Dropped[:0]
	w.input.Opened = w.input.Opened[:0]
	w.input.Wheel = 0
	w.input.Hotkeys = w.input.Hotkeys[:0]
	w.input.Woken = false
	for {
		r, _, _ := syscall.SyscallN(peekMessage.Addr(), uintptr(unsafe.Pointer(&w.msg)), 0, 0, 0, 1)
		if r == 0 {
			break
		}
		if w.msg.ID == wmQuit {
			w.closed = true
		}
		syscall.SyscallN(translateMessage.Addr(), uintptr(unsafe.Pointer(&w.msg)))
		syscall.SyscallN(dispatchMessage.Addr(), uintptr(unsafe.Pointer(&w.msg)))
	}
	w.input.Ctrl, w.input.Shift, w.input.Alt = held(0x11), held(0x10), held(0x12)
	// Whether the window has the keyboard focus, asked of Windows rather than kept from WM_SETFOCUS and
	// WM_KILLFOCUS: a window made foreground from outside, as -background leaves it to be, can have the focus
	// without being told, and a caret would then never blink.
	focus, _, _ := getFocus.Call()
	w.input.Inactive = focus != w.Handle
	return !w.closed
}

// Wait blocks until input arrives, Wake is called, or timeoutMillis passes.
// Forever is a Wait timeout that waits for input or Wake only.
const Forever = 0xffffffff

func (w *Window) Wait(timeoutMillis uint32) {
	const allInput, inputAvailable = 0x04ff, 0x0004
	msgWait.Call(0, 0, uintptr(timeoutMillis), allInput, inputAvailable)
}

// Wake makes a pending Wait return; it is safe to call from any goroutine.
func (w *Window) Wake() { postMessage.Call(w.Handle, wmApp, 0, 0) }

// Input returns the events collected by the last Poll.
func (w *Window) Input() *Input { return &w.input }

// Scale is the window's display DPI relative to 96.
func (w *Window) Scale() float32 {
	dpi, _, _ := getDpiForWindow.Call(w.Handle)
	if dpi == 0 {
		return 1
	}
	return float32(dpi) / 96
}

// Size is the client area in physical pixels; it is 0×0 while minimized or hidden, when there is nothing to draw.
func (w *Window) Size() (int, int) {
	if r, _, _ := isWindowVisible.Call(w.Handle); r == 0 {
		return 0, 0
	}
	if r, _, _ := getClientRect.Call(w.Handle, uintptr(unsafe.Pointer(&w.bounds))); r == 0 {
		return 0, 0
	}
	return int(w.bounds.Right), int(w.bounds.Bottom)
}

// Present copies an RGBA image of the client size onto the window.
func (w *Window) Present(rgba []byte, width, height int) error {
	if width <= 0 || height <= 0 {
		return nil
	}
	if len(rgba) < width*height*4 {
		return errors.New("present: image smaller than its size")
	}
	n := width * height * 4
	if cap(w.bgra) < n {
		w.bgra = make([]byte, n)
	}
	b := w.bgra[:n]
	toBGRA(b, rgba[:n])
	header := struct {
		Size                   uint32
		Width, Height          int32
		Planes, Bits           uint16
		Compression, ImageSize uint32
		X, Y                   int32
		Used, Important        uint32
	}{Size: 40, Width: int32(width), Height: -int32(height), Planes: 1, Bits: 32}
	dc, _, _ := getDC.Call(w.Handle)
	if dc == 0 {
		return errors.New("present: GetDC failed")
	}
	defer releaseDC.Call(w.Handle, dc)
	const dibRGB = 0
	r, _, e := setDIBits.Call(dc, 0, 0, uintptr(width), uintptr(height), 0, 0, 0, uintptr(height),
		uintptr(unsafe.Pointer(&b[0])), uintptr(unsafe.Pointer(&header)), dibRGB)
	runtime.KeepAlive(b)
	if r == 0 {
		return fmt.Errorf("SetDIBitsToDevice: %w", e)
	}
	return nil
}

// toBGRA swaps red and blue and makes every pixel opaque, four bytes at a time, in parallel bands.
func toBGRA(dst, src []byte) {
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

func (w *Window) SetTitle(title string) {
	if t, err := syscall.UTF16PtrFromString(title); err == nil {
		setWindowText.Call(w.Handle, uintptr(unsafe.Pointer(t)))
	}
}

// Close destroys the window and releases the OS thread locked by Open.
func (w *Window) Close() {
	if w.Handle == 0 {
		return
	}
	destroyWindow.Call(w.Handle)
	windows.Delete(w.Handle)
	w.Handle = 0
	if w.overlay != nil {
		w.overlay.release() // an overlay shares the main window's thread, which stays locked
		return
	}
	runtime.UnlockOSThread()
}

func held(vk uintptr) bool {
	state, _, _ := getKeyState.Call(vk)
	return int16(state) < 0
}
