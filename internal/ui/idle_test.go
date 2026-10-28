package ui

import (
	"image"
	"testing"
	"time"
)

// An app with nothing to do asks for no frames, so its window sleeps until input; work waiting on a timer asks
// to be woken when it is due, not before and not on a tick.
func TestIdleAppAsksForNoFrames(t *testing.T) {
	x := newHarness(t)
	a := x.a
	x.frame(Input{})
	if next := a.Frame(Input{Inactive: true}, x.c, x.scale); next != 0 {
		t.Errorf("an empty, idle window asks to be drawn in %v", next)
	}
	// A canvas with a change waits for its save, then asks for nothing more.
	a.settings.ExportDir = x.mediaDir()
	if err := a.newCanvas(); err != nil {
		t.Fatal(err)
	}
	x.frame(Input{})
	a.art.touched(image.Rect(0, 0, 64, 64))
	next := a.Frame(Input{Inactive: true}, x.c, x.scale)
	if next < artSaveDelay-200*time.Millisecond || next > artSaveDelay {
		t.Errorf("a changed canvas asks to be drawn in %v, want about %v", next, artSaveDelay)
	}
	a.art.changed = time.Now().Add(-artSaveDelay)
	x.frame(Input{Inactive: true}) // saves
	x.pump(5*time.Second, func() bool { return !a.art.saving })
	if next := a.Frame(Input{Inactive: true}, x.c, x.scale); next != 0 {
		t.Errorf("after saving the canvas still asks to be drawn in %v", next)
	}
	// A focused text box blinks its caret only while the window has the keyboard.
	a.openDialog(dialogImageSize)
	a.u.setFocus("dialog/a")
	if next := a.Frame(Input{}, x.c, x.scale); next == 0 || next > 300*time.Millisecond {
		t.Errorf("a caret in the focused window does not blink: %v", next)
	}
	if next := a.Frame(Input{Inactive: true}, x.c, x.scale); next != 0 {
		t.Errorf("a caret in an inactive window still wakes it in %v", next)
	}
}
