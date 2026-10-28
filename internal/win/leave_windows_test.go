package win

import "testing"

// Once the pointer leaves the window it is nowhere over it: what it pointed at is hovered no longer, and its tip goes.
// While a button is held, as in a drag carried outside, it stays where it was last seen.
func TestThePointerLeavingTheWindowIsNowhereOverIt(t *testing.T) {
	w, err := Open("leave test", 300, 200, Background)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	send := user32.NewProc("SendMessageW")
	at := uintptr(60<<16 | 40)
	send.Call(w.Handle, 0x0200, 0, at) // WM_MOUSEMOVE at 40, 60
	if in := w.Input(); in.X != 40 || in.Y != 60 {
		t.Fatalf("the pointer is at %v, %v", in.X, in.Y)
	}
	if !w.leaving {
		t.Fatal("Windows was not asked to tell when the pointer leaves")
	}
	send.Call(w.Handle, 0x02a3, 0, 0) // WM_MOUSELEAVE
	if in := w.Input(); in.X != Outside || in.Y != Outside {
		t.Fatalf("after leaving, the pointer is at %v, %v", in.X, in.Y)
	}

	send.Call(w.Handle, 0x0200, 0, at)
	w.input.Down[Left] = true // a drag under way
	send.Call(w.Handle, 0x02a3, 0, 0)
	if in := w.Input(); in.X != 40 || in.Y != 60 {
		t.Fatalf("leaving during a drag moved the pointer to %v, %v", in.X, in.Y)
	}
	w.input.Down[Left] = false
}
