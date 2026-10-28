package win

import "testing"

// Poll asks Windows whether the window has the keyboard focus, so a focus it was never told about (a window made
// foreground from outside after opening in the background) still lets the caret blink, and losing it stops it.
func TestPollReportsTheRealKeyboardFocus(t *testing.T) {
	w, err := Open("focus test", 100, 100, Background)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	setFocus := user32.NewProc("SetFocus")
	setFocus.Call(w.Handle)
	w.input.Inactive = true // as if WM_SETFOCUS had been missed
	w.Poll()
	if focus, _, _ := getFocus.Call(); focus != w.Handle {
		t.Fatalf("SetFocus left the focus on %#x, not the window %#x", focus, w.Handle)
	}
	if w.Input().Inactive {
		t.Error("the window has the keyboard focus, but its input says it is inactive")
	}
	setFocus.Call(0)
	w.Poll()
	if !w.Input().Inactive {
		t.Error("without the keyboard focus the window should be inactive")
	}
}
