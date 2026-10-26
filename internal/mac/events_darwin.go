package mac

/*
#include <stdlib.h>
*/
import "C"

import "strings"

// The functions below are called by the Objective-C side while bvc_poll dispatches events.

//export goPointer
func goPointer(x, y C.double, right, down, double C.int) {
	w := current
	if w == nil {
		return
	}
	w.input.X, w.input.Y = float32(x), float32(y)
	if right == 0 {
		w.input.Down = down != 0
	}
	w.input.Pointer = append(w.input.Pointer, PointerEvent{X: float32(x), Y: float32(y), Right: right != 0, Down: down != 0, Double: double != 0})
}

//export goMove
func goMove(x, y C.double) {
	if w := current; w != nil {
		w.input.X, w.input.Y = float32(x), float32(y)
	}
}

//export goWheel
func goWheel(dy C.double, mods C.int) {
	if w := current; w != nil {
		w.input.Wheel += float32(dy)
		w.input.Ctrl, w.input.Shift = mods&1 != 0, mods&2 != 0
	}
}

func (w *Window) key(vk uint16, mods int) {
	w.input.Ctrl, w.input.Shift = mods&1 != 0, mods&2 != 0
	w.input.Keys = append(w.input.Keys, KeyEvent{VK: vk, Ctrl: mods&1 != 0, Shift: mods&2 != 0, Alt: mods&4 != 0})
}

//export goKey
func goKey(code C.int, chars *C.char, mods C.int) {
	w := current
	if w == nil {
		return
	}
	if vk, ok := keyVK(int(code), C.GoString(chars)); ok {
		w.key(vk, int(mods))
	}
}

//export goKeyCode
func goKeyCode(vk, mods C.int) {
	if w := current; w != nil {
		w.key(uint16(vk), int(mods))
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
			w.input.Typed = append(w.input.Typed, r)
		}
	}
}

//export goDrop
func goDrop(path *C.char) {
	if w := current; w != nil {
		if p := strings.TrimSpace(C.GoString(path)); p != "" {
			w.input.Dropped = append(w.input.Dropped, p)
		}
	}
}

//export goRefresh
func goRefresh() {
	if w := current; w != nil && w.refresh != nil {
		w.refresh()
	}
}
