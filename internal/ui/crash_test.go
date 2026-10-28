package ui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A failure in a frame or in background work is reported, everything open is saved, the user is told, and the app
// goes on.
func TestFailuresAreSurvived(t *testing.T) {
	x, ad := canvasHarness(t)
	a := x.a
	ad.d.AddLayer("Unsaved")
	ad.dirty = true
	a.post(func() { panic("a frame's failure") })
	if next := a.Frame(Input{}, x.c, x.scale); next != redrawNow {
		t.Errorf("after a failure the next frame waits %v", next)
	}
	reports, _ := filepath.Glob(filepath.Join(a.crashDir(), "crash-*.txt"))
	if len(reports) != 1 {
		t.Fatalf("reports %v", reports)
	}
	data, _ := os.ReadFile(reports[0])
	if !strings.Contains(string(data), "a frame's failure") || !strings.Contains(string(data), "goroutine") {
		t.Errorf("the report:\n%s", data)
	}
	if ad.dirty {
		t.Error("the canvas should be saved at once")
	}
	if len(a.toasts) == 0 || !a.toasts[len(a.toasts)-1].err {
		t.Error("the user should be told")
	}
	// Work away from the window.
	a.background(func(context.Context) { panic("background failure") })
	x.pump(10*time.Second, func() bool {
		r, _ := filepath.Glob(filepath.Join(a.crashDir(), "crash-*.txt"))
		return len(r) == 2
	})
	// The app goes on: a stroke paints.
	x.key('B', false)
	x.pressAt(ad, 100, 100)
	x.moveTo(ad, 150, 100, true)
	x.frame(Input{Pointer: []Pointer{{X: x.mx, Y: x.my}}})
	if c := ad.look(125, 100); c.A == 0 || c == ad.look(10, 290) {
		t.Errorf("after the failures the brush does not paint: %v", c)
	}
}
