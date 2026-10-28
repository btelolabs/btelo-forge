package win

import (
	"os"
	"testing"
	"time"
)

// TestHideAndRestoreKeepTheWindowAsItWas hides a maximized window and brings it back. It shows a window and takes
// the focus, so it runs only when FORGE_WINDOW_TEST is set.
func TestHideAndRestoreKeepTheWindowAsItWas(t *testing.T) {
	if os.Getenv("FORGE_WINDOW_TEST") == "" {
		t.Skip("set FORGE_WINDOW_TEST to let the test show windows and take the focus")
	}
	EnableDPI()
	w, err := Open("hide test", 300, 200, Foreground)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	pump(w, 300*time.Millisecond)
	if !w.Shown() {
		t.Fatal("a window just opened in front is not shown")
	}
	const showMaximized = 3
	showWindow.Call(w.Handle, showMaximized)
	pump(w, 300*time.Millisecond)
	maxW, maxH := w.Size()

	w.Hide()
	pump(w, 200*time.Millisecond)
	if w.Shown() {
		t.Error("a hidden window is shown")
	}
	if width, height := w.Size(); width != 0 || height != 0 {
		t.Errorf("a hidden window has a size to draw: %d×%d", width, height)
	}

	w.Restore()
	pump(w, 300*time.Millisecond)
	if !w.Shown() {
		t.Error("the window is not back in front")
	}
	if r, _, _ := isZoomed.Call(w.Handle); r == 0 {
		t.Error("the window came back no longer maximized")
	}
	if width, height := w.Size(); width != maxW || height != maxH {
		t.Errorf("came back %d×%d, was %d×%d", width, height, maxW, maxH)
	}

	w.Minimize()
	pump(w, 300*time.Millisecond)
	if w.Shown() {
		t.Error("a minimized window is shown")
	}
	w.Restore()
	pump(w, 300*time.Millisecond)
	if !w.Shown() {
		t.Error("the window is not back from the taskbar")
	}
	if r, _, _ := isZoomed.Call(w.Handle); r == 0 {
		t.Error("the window came back from the taskbar no longer maximized")
	}
}

var isZoomed = user32.NewProc("IsZoomed")
