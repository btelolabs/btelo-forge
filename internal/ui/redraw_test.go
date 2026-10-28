package ui

import (
	"testing"
	"time"
)

func TestInputIsShownWithoutWaitingForMoreInput(t *testing.T) {
	x := newHarness(t)
	x.open(x.fixture())
	x.settle(10 * time.Second)

	if next := x.a.Frame(Input{X: 10, Y: 10}, x.c, x.scale); next == redrawNow {
		t.Fatal("moving the pointer alone should not ask for another frame")
	}
	l := x.a.layout()
	gear := l.tabs.Right() - x.a.u.px(40)
	end := gear
	if x.a.recorder() != nil {
		record, _ := x.a.recordButtons(gear, 0, 0) // the recording buttons sit between them
		end = record.X
	}
	toggle := end - x.a.u.px(36) + x.a.u.px(16)
	y := l.tabs.Y + x.a.u.px(20)
	x.a.Frame(Input{X: toggle, Y: y, Down: true, Pointer: []Pointer{{X: toggle, Y: y, Down: true}}}, x.c, x.scale)
	next := x.a.Frame(Input{X: toggle, Y: y, Pointer: []Pointer{{X: toggle, Y: y}}}, x.c, x.scale)
	if !x.a.settings.PanelHidden {
		t.Fatal("the click did not reach the panel button")
	}
	if next != redrawNow {
		t.Fatalf("after a click the next frame waits %v (0 means until more input) instead of drawing at once", next)
	}
}
