package mac

/*
#include <stdlib.h>
*/
import "C"

import "strings"

// The functions below are called by the Objective-C side while forge_poll dispatches events.

func applyMods(q *inputQueue, mods int) {
	q.next.Ctrl = mods&1 != 0
	q.next.Shift = mods&2 != 0
	q.next.Alt = mods&4 != 0
}

//export goModifiers
func goModifiers(mods C.int) {
	if w := current; w != nil {
		applyMods(&w.input, int(mods))
	}
}

//export goPointer
func goPointer(x, y C.double, button, down, twice, mods C.int) {
	w := current
	if w == nil {
		return
	}
	applyMods(&w.input, int(mods))
	w.input.next.X, w.input.next.Y = float32(x), float32(y)
	w.input.setButton(int(button), down != 0)
	w.input.next.Pointer = append(w.input.next.Pointer, PointerEvent{
		X: float32(x), Y: float32(y), Right: button == 1, Middle: button == 2, Down: down != 0, Double: twice != 0,
	})
}

//export goMove
func goMove(x, y C.double, mods C.int) {
	if w := current; w != nil {
		applyMods(&w.input, int(mods))
		w.input.next.X, w.input.next.Y = float32(x), float32(y)
	}
}

//export goMouseLeave
func goMouseLeave() {
	if w := current; w != nil && !w.input.anyDown() {
		w.input.next.X, w.input.next.Y = Outside, Outside
	}
}

//export goFocusChange
func goFocusChange(focused C.int) {
	if w := current; w != nil {
		if focused == 0 {
			w.input.releaseAll()
		}
		w.Wake()
	}
}

//export goWheel
func goWheel(dy C.double, mods C.int) {
	if w := current; w != nil {
		w.input.next.Wheel += float32(dy)
		applyMods(&w.input, int(mods))
	}
}

func (q *inputQueue) addKey(vk uint16, mods int) {
	applyMods(q, mods)
	q.next.Keys = append(q.next.Keys, KeyEvent{VK: vk, Ctrl: mods&1 != 0, Shift: mods&2 != 0, Alt: mods&4 != 0})
}

//export goKey
func goKey(code C.int, chars *C.char, mods C.int) {
	w := current
	if w == nil {
		return
	}
	if vk, ok := keyVK(int(code), C.GoString(chars)); ok {
		w.input.addKey(vk, int(mods))
	}
}

//export goKeyUp
func goKeyUp(code C.int, chars *C.char) {
	w := current
	if w == nil {
		return
	}
	if vk, ok := keyVK(int(code), C.GoString(chars)); ok {
		w.input.next.Up = append(w.input.next.Up, vk)
	}
}

//export goKeyCode
func goKeyCode(vk, mods C.int) {
	if w := current; w != nil {
		w.input.addKey(uint16(vk), int(mods))
	}
}

//export goTyped
func goTyped(text *C.char) {
	w := current
	if w == nil || !w.typing {
		return
	}
	for _, r := range C.GoString(text) {
		if r >= 0x20 && r != 0x7f {
			w.input.next.Typed = append(w.input.next.Typed, r)
		}
	}
}

//export goDrop
func goDrop(path *C.char) {
	if w := current; w != nil {
		if p := strings.TrimSpace(C.GoString(path)); p != "" {
			w.input.next.Dropped = append(w.input.next.Dropped, p)
		}
	}
}

//export goOpenFile
func goOpenFile(path *C.char) {
	if w := current; w != nil {
		if p := strings.TrimSpace(C.GoString(path)); p != "" {
			w.input.next.Opened = append(w.input.next.Opened, p)
			w.Wake()
		}
	}
}

//export goHotkey
func goHotkey(id C.int) {
	if w := current; w != nil && id > 0 {
		w.input.next.Hotkeys = append(w.input.next.Hotkeys, int(id)-1)
		w.Wake()
	}
}

//export goRefresh
func goRefresh() {
	if w := current; w != nil && w.refresh != nil {
		w.refresh()
	}
}

// ---- Overlay event callbacks (routed by overlay ID) ----

//export goOverlayPointer
func goOverlayPointer(id C.int, x, y C.double, button, down, twice, mods C.int) {
	o := overlayByID(int(id))
	if o == nil {
		return
	}
	applyMods(&o.input, int(mods))
	o.input.next.X, o.input.next.Y = float32(x), float32(y)
	o.input.setButton(int(button), down != 0)
	o.input.next.Pointer = append(o.input.next.Pointer, PointerEvent{
		X: float32(x), Y: float32(y), Right: button == 1, Middle: button == 2, Down: down != 0, Double: twice != 0,
	})
}

//export goOverlayMove
func goOverlayMove(id C.int, x, y C.double, mods C.int) {
	if o := overlayByID(int(id)); o != nil {
		applyMods(&o.input, int(mods))
		o.input.next.X, o.input.next.Y = float32(x), float32(y)
	}
}

//export goOverlayLeave
func goOverlayLeave(id C.int) {
	if o := overlayByID(int(id)); o != nil && !o.input.anyDown() {
		o.input.next.X, o.input.next.Y = Outside, Outside
	}
}

//export goOverlayWheel
func goOverlayWheel(id C.int, dy C.double, mods C.int) {
	if o := overlayByID(int(id)); o != nil {
		o.input.next.Wheel += float32(dy)
		applyMods(&o.input, int(mods))
	}
}

//export goOverlayKey
func goOverlayKey(id C.int, code C.int, chars *C.char, mods C.int) {
	o := overlayByID(int(id))
	if o == nil {
		return
	}
	if vk, ok := keyVK(int(code), C.GoString(chars)); ok {
		o.input.addKey(vk, int(mods))
	}
}

//export goOverlayKeyUp
func goOverlayKeyUp(id C.int, code C.int, chars *C.char) {
	o := overlayByID(int(id))
	if o == nil {
		return
	}
	if vk, ok := keyVK(int(code), C.GoString(chars)); ok {
		o.input.next.Up = append(o.input.next.Up, vk)
	}
}

//export goOverlayKeyCode
func goOverlayKeyCode(id C.int, vk, mods C.int) {
	if o := overlayByID(int(id)); o != nil {
		o.input.addKey(uint16(vk), int(mods))
	}
}

//export goOverlayTyped
func goOverlayTyped(id C.int, text *C.char) {
	o := overlayByID(int(id))
	if o == nil || !o.typing {
		return
	}
	for _, r := range C.GoString(text) {
		if r >= 0x20 && r != 0x7f {
			o.input.next.Typed = append(o.input.next.Typed, r)
		}
	}
}

//export goOverlayClosed
func goOverlayClosed(id C.int) {
	if o := overlayByID(int(id)); o != nil {
		o.closed = true
	}
}

//export goOverlayResign
func goOverlayResign(id C.int) {
	if o := overlayByID(int(id)); o != nil {
		o.input.releaseAll()
	}
}
