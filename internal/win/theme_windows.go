package win

import (
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows/registry"
)

var (
	dwmapi                = syscall.NewLazyDLL("dwmapi.dll")
	dwmSetWindowAttribute = dwmapi.NewProc("DwmSetWindowAttribute")
)

// SystemDark reports whether Windows is set to dark mode for apps.
// On systems without the Personalize key it defaults to light (matching Windows's own default).
func SystemDark() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Themes\Personalize`, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	v, _, err := k.GetIntegerValue("AppsUseLightTheme")
	if err != nil {
		return false
	}
	return v == 0
}

// SetDark switches the window's native titlebar between dark and light mode.
func (w *Window) SetDark(dark bool) {
	if w.Handle == 0 || (w.darkSet && w.dark == dark) {
		return
	}
	w.dark, w.darkSet = dark, true
	var on uint32
	if dark {
		on = 1
	}
	const (
		dwmwaUseImmersiveDarkMode           = 20
		dwmwaUseImmersiveDarkModeBefore20H1 = 19
		swpNoSize                           = 0x0001
		swpNoMove                           = 0x0002
		swpNoZOrder                         = 0x0004
		swpNoActivate                       = 0x0010
		swpFrameChanged                     = 0x0020
	)
	if r, _, _ := dwmSetWindowAttribute.Call(w.Handle, dwmwaUseImmersiveDarkMode, uintptr(unsafe.Pointer(&on)), unsafe.Sizeof(on)); r != 0 {
		dwmSetWindowAttribute.Call(w.Handle, dwmwaUseImmersiveDarkModeBefore20H1, uintptr(unsafe.Pointer(&on)), unsafe.Sizeof(on))
	}
	setWindowPos.Call(w.Handle, 0, 0, 0, 0, 0, swpNoMove|swpNoSize|swpNoZOrder|swpNoActivate|swpFrameChanged)
}
