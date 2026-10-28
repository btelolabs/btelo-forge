package mac

/*
#include <stdlib.h>
*/
import "C"

import "strings"

// The functions below are called by the Objective-C side while forge_poll dispatches events.

//export goPointer
func goPointer(x, y C.double, right, down, twice C.int) {
	w := current
	if w == nil {
		return
	}
	w.input.next.X, w.input.next.Y = float32(x), float32(y)
	if right == 0 {
		w.input.next.Down = down != 0
	}
	w.input.next.Pointer = append(w.input.next.Pointer, PointerEvent{X: float32(x), Y: float32(y), Right: right != 0, Down: down != 0, Double: twice != 0})
}

//export goMove
func goMove(x, y C.double) {
	if w := current; w != nil {
		w.input.next.X, w.input.next.Y = float32(x), float32(y)
	}
}

//export goWheel
func goWheel(dy C.double, mods C.int) {
	if w := current; w != nil {
		w.input.next.Wheel += float32(dy)
		w.input.next.Ctrl, w.input.next.Shift = mods&1 != 0, mods&2 != 0
	}
}

func (w *Window) key(vk uint16, mods int) {
	w.input.next.Ctrl, w.input.next.Shift = mods&1 != 0, mods&2 != 0
	w.input.next.Keys = append(w.input.next.Keys, KeyEvent{VK: vk, Ctrl: mods&1 != 0, Shift: mods&2 != 0, Alt: mods&4 != 0})
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

//export goRefresh
func goRefresh() {
	if w := current; w != nil && w.refresh != nil {
		w.refresh()
	}
}
