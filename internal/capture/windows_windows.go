//go:build windows

package capture

import (
	"image"
	"syscall"
	"unsafe"
)

var (
	enumWindows           = user32.NewProc("EnumWindows")
	isWindowVisible       = user32.NewProc("IsWindowVisible")
	isIconicWin           = user32.NewProc("IsIconic")
	getWindowRect         = user32.NewProc("GetWindowRect")
	getWindowLongPtr      = user32.NewProc("GetWindowLongPtrW")
	getWindowDisplayAff   = user32.NewProc("GetWindowDisplayAffinity")
	getLayeredWindowAttrs = user32.NewProc("GetLayeredWindowAttributes")
	getClassNameW         = user32.NewProc("GetClassNameW")
	getWindowTextW        = user32.NewProc("GetWindowTextW")
	dwmGetWindowAttribute = dwmapi.NewProc("DwmGetWindowAttribute")
)

type winRect struct {
	Left, Top, Right, Bottom int32
}

func listWindows() []WindowRect {
	const perMonitorV2 = ^uintptr(3)
	if prev, _, _ := setThreadDpiContext.Call(perMonitorV2); prev != 0 {
		defer setThreadDpiContext.Call(prev)
	}
	var out []WindowRect
	cb := syscall.NewCallback(func(hwnd, _ uintptr) uintptr {
		if r, _, _ := isWindowVisible.Call(hwnd); r == 0 {
			return 1
		}
		if r, _, _ := isIconicWin.Call(hwnd); r != 0 {
			return 1
		}
		// Skip windows excluded from capture (e.g. our own overlays or excluded main window).
		const wdaExcludeFromCapture = 0x11
		var aff uint32
		if r, _, _ := getWindowDisplayAff.Call(hwnd, uintptr(unsafe.Pointer(&aff))); r != 0 && aff == wdaExcludeFromCapture {
			return 1
		}
		// Skip cloaked UWP/virtual-desktop/background windows.
		const dwmwaCloaked = 14
		var cloaked uint32
		if hr, _, _ := dwmGetWindowAttribute.Call(hwnd, dwmwaCloaked, uintptr(unsafe.Pointer(&cloaked)), unsafe.Sizeof(cloaked)); hr == 0 && cloaked != 0 {
			return 1
		}
		// Skip click-through or fully transparent layered windows.
		const (
			gwlExStyle  = ^uintptr(19) // -20
			wsExTransp  = 0x00000020
			wsExLayered = 0x00080000
			lwaAlpha    = 0x00000002
		)
		exStyle, _, _ := getWindowLongPtr.Call(hwnd, gwlExStyle)
		if exStyle&wsExTransp != 0 {
			return 1
		}
		if exStyle&wsExLayered != 0 {
			var alpha byte
			var flags uint32
			if r, _, _ := getLayeredWindowAttrs.Call(hwnd, 0, uintptr(unsafe.Pointer(&alpha)), uintptr(unsafe.Pointer(&flags))); r != 0 && flags&lwaAlpha != 0 && alpha == 0 {
				return 1
			}
		}
		// Skip shell desktop backdrop classes.
		var cls [64]uint16
		if n, _, _ := getClassNameW.Call(hwnd, uintptr(unsafe.Pointer(&cls[0])), uintptr(len(cls))); n > 0 {
			switch syscall.UTF16ToString(cls[:n]) {
			case "Progman", "WorkerW":
				return 1
			}
		}
		// Prefer DWMWA_EXTENDED_FRAME_BOUNDS (excludes invisible drop-shadow resize borders).
		const dwmwaExtendedFrameBounds = 9
		var rc winRect
		if hr, _, _ := dwmGetWindowAttribute.Call(hwnd, dwmwaExtendedFrameBounds, uintptr(unsafe.Pointer(&rc)), unsafe.Sizeof(rc)); hr != 0 {
			if r, _, _ := getWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&rc))); r == 0 {
				return 1
			}
		}
		rect := image.Rect(int(rc.Left), int(rc.Top), int(rc.Right), int(rc.Bottom))
		if rect.Dx() < 8 || rect.Dy() < 8 {
			return 1
		}
		var titleBuf [128]uint16
		title := ""
		if n, _, _ := getWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(&titleBuf[0])), uintptr(len(titleBuf))); n > 0 {
			title = syscall.UTF16ToString(titleBuf[:n])
		}
		out = append(out, WindowRect{Rect: rect, Title: title})
		return 1
	})
	enumWindows.Call(cb, 0)
	return out
}
