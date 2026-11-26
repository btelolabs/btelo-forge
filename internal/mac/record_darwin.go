package mac

/*
#include "window.h"
*/
import "C"

import "fmt"

// Hotkey is a key with modifiers, registered for the whole system via Carbon RegisterEventHotKey.
type Hotkey struct {
	Ctrl, Alt, Shift bool
	VK               uint16 // Windows virtual-key code; 0 is no hotkey
}

// Minimize sends the window to the Dock.
func (w *Window) Minimize() { C.forge_minimize() }

// Restore brings a minimized or hidden window back and in front.
func (w *Window) Restore() { C.forge_restore() }

// Hide takes the window off the screen; Restore brings it back.
func (w *Window) Hide() { C.forge_hide() }

// Shown reports whether the window is visible, not minimized, and active in front.
func (w *Window) Shown() bool { return C.forge_shown() != 0 }

// ExcludeFromCapture keeps the window out of screen recordings and screenshots (NSWindowSharingNone).
func (w *Window) ExcludeFromCapture(on bool) error {
	v := C.int(0)
	if on {
		v = 1
	}
	C.forge_exclude_capture(v)
	return nil
}

// SetHotkeys registers system-wide hotkeys via Carbon HIToolbox (does not require Accessibility permission),
// replacing any previously registered hotkeys. Each press appears in Input.Hotkeys as its index.
func (w *Window) SetHotkeys(keys []Hotkey) []error {
	C.forge_clear_hotkeys()
	w.hotkeys = keys
	errs := make([]error, len(keys))
	for i, k := range keys {
		if k.VK == 0 {
			continue
		}
		code, ok := macKeyCode(k.VK)
		if !ok {
			errs[i] = fmt.Errorf("hotkey key %#x is not supported on macOS", k.VK)
			continue
		}
		var ctrl, alt, shift C.int
		if k.Ctrl {
			ctrl = 1
		}
		if k.Alt {
			alt = 1
		}
		if k.Shift {
			shift = 1
		}
		if st := C.forge_register_hotkey(C.int(i+1), C.uint32_t(code), ctrl, alt, shift); st != 0 {
			errs[i] = fmt.Errorf("RegisterEventHotKey failed (OSStatus %d)", int(st))
		}
	}
	return errs
}
