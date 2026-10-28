package win

import "fmt"

var (
	setDisplayAffinity = user32.NewProc("SetWindowDisplayAffinity")
	registerHotKey     = user32.NewProc("RegisterHotKey")
	unregisterHotKey   = user32.NewProc("UnregisterHotKey")
	isWindowVisible    = user32.NewProc("IsWindowVisible")
	getForeground      = user32.NewProc("GetForegroundWindow")
)

const wmHotkey = 0x0312

// Minimize sends the window to the taskbar.
func (w *Window) Minimize() {
	const showMinimized = 6
	showWindow.Call(w.Handle, showMinimized)
}

// Restore brings a minimized or hidden window back and in front, maximized again if it was; one already showing
// only comes to the front.
func (w *Window) Restore() {
	w.show()
	setForeground.Call(w.Handle)
}

// show makes the window visible as it was before it was hidden or minimized.
func (w *Window) show() {
	const showAsWas, restore = 5, 9 // SW_SHOW keeps a hidden window's maximized state; SW_RESTORE ends a minimized one
	if r, _, _ := isWindowVisible.Call(w.Handle); r == 0 {
		showWindow.Call(w.Handle, showAsWas)
	}
	if r, _, _ := isIconic.Call(w.Handle); r != 0 {
		showWindow.Call(w.Handle, restore)
	}
}

// Hide takes the window off the screen, the taskbar and Alt+Tab; Restore brings it back.
func (w *Window) Hide() {
	const hide = 0
	showWindow.Call(w.Handle, hide)
}

// Shown reports whether the window is on the screen and in front of the others, not minimized, hidden or behind
// another program's window.
func (w *Window) Shown() bool {
	if r, _, _ := isWindowVisible.Call(w.Handle); r == 0 {
		return false
	}
	if r, _, _ := isIconic.Call(w.Handle); r != 0 {
		return false
	}
	front, _, _ := getForeground.Call()
	return front == w.Handle
}

// ExcludeFromCapture keeps the window out of screen recordings and screenshots (the desktop behind it shows
// instead), or lets it be captured again. It needs Windows 10 version 2004 or later.
func (w *Window) ExcludeFromCapture(on bool) error {
	const none, exclude = 0x0, 0x11 // WDA_NONE, WDA_EXCLUDEFROMCAPTURE
	affinity := uintptr(none)
	if on {
		affinity = exclude
	}
	if r, _, e := setDisplayAffinity.Call(w.Handle, affinity); r == 0 {
		return fmt.Errorf("SetWindowDisplayAffinity: %w", e)
	}
	return nil
}

// Hotkey is a key with modifiers, registered for the whole system.
type Hotkey struct {
	Ctrl, Alt, Shift bool
	VK               uint16 // the virtual-key code; 0 is no hotkey
}

// SetHotkeys registers the hotkeys for the whole system, so they work while another program has the focus, in place
// of the ones registered before; each press shows in Input.Hotkeys as its index. A hotkey with no key is skipped.
// The error for each is nil, or says another program has taken it.
func (w *Window) SetHotkeys(keys []Hotkey) []error {
	for id := range w.hotkeys {
		unregisterHotKey.Call(w.Handle, uintptr(id+1))
	}
	w.hotkeys = keys
	errs := make([]error, len(keys))
	for i, k := range keys {
		if k.VK == 0 {
			continue
		}
		const alt, control, shift, noRepeat = 0x1, 0x2, 0x4, 0x4000
		mods := uintptr(noRepeat)
		if k.Alt {
			mods |= alt
		}
		if k.Ctrl {
			mods |= control
		}
		if k.Shift {
			mods |= shift
		}
		if r, _, e := registerHotKey.Call(w.Handle, uintptr(i+1), mods, uintptr(k.VK)); r == 0 {
			errs[i] = fmt.Errorf("RegisterHotKey: %w", e)
		}
	}
	return errs
}

func (w *Window) hotkey(msg uint32, wp uintptr) bool {
	if msg != wmHotkey || wp == 0 || int(wp) > len(w.hotkeys) {
		return false
	}
	w.input.Hotkeys = append(w.input.Hotkeys, int(wp)-1)
	return true
}
