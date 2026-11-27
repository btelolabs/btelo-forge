package mac

import (
	"slices"
	"testing"
)

func TestEventsBetweenPollsReachTheNextFrame(t *testing.T) {
	var q inputQueue
	q.next.X, q.next.Down, q.next.Alt = 10, true, true
	q.next.Dropped = append(q.next.Dropped, "/tmp/a.mp4")
	q.next.Opened = append(q.next.Opened, "/tmp/open.mp4")
	q.next.Hotkeys = append(q.next.Hotkeys, 2)
	q.next.Keys = append(q.next.Keys, KeyEvent{VK: 0x41})
	q.next.Up = append(q.next.Up, 0x41)
	q.next.Wheel = 2
	q.flip()

	if !slices.Equal(q.last.Dropped, []string{"/tmp/a.mp4"}) || !slices.Equal(q.last.Opened, []string{"/tmp/open.mp4"}) || !slices.Equal(q.last.Hotkeys, []int{2}) || len(q.last.Keys) != 1 || len(q.last.Up) != 1 || q.last.Wheel != 2 || !q.last.Alt {
		t.Fatalf("the frame lost the collected events: %+v", q.last)
	}
	if len(q.next.Dropped) != 0 || len(q.next.Opened) != 0 || len(q.next.Hotkeys) != 0 || len(q.next.Keys) != 0 || len(q.next.Up) != 0 || q.next.Wheel != 0 {
		t.Fatalf("the events were handed over twice: %+v", q.next)
	}
	if q.next.X != 10 || !q.next.Down || !q.next.Alt {
		t.Fatalf("pointer/modifier state was not carried over: %+v", q.next)
	}

	q.next.Dropped = append(q.next.Dropped, "/tmp/b.mp4")
	if !slices.Equal(q.last.Dropped, []string{"/tmp/a.mp4"}) {
		t.Fatalf("collecting the next frame changed the current one: %q", q.last.Dropped)
	}
	q.flip()
	if !slices.Equal(q.last.Dropped, []string{"/tmp/b.mp4"}) {
		t.Fatalf("second frame got %q", q.last.Dropped)
	}
}

func TestModifierBitsSplitCmdAndControl(t *testing.T) {
	var q inputQueue
	q.addKey('3', 1|2|8)  // Cmd+Shift+3
	q.addKey('4', 1|2|16) // Control+Shift+4
	q.addKey('5', 1|4|8|16)
	q.flip()
	if len(q.last.Keys) != 3 {
		t.Fatalf("got %d keys, want 3", len(q.last.Keys))
	}
	k0, k1, k2 := q.last.Keys[0], q.last.Keys[1], q.last.Keys[2]
	if !k0.Ctrl || !k0.Shift || !k0.Cmd || k0.Control || k0.Alt {
		t.Fatalf("Cmd+Shift+3 key = %+v", k0)
	}
	if !k1.Ctrl || !k1.Shift || k1.Cmd || !k1.Control || k1.Alt {
		t.Fatalf("Control+Shift+4 key = %+v", k1)
	}
	if !k2.Ctrl || k2.Shift || !k2.Alt || !k2.Cmd || !k2.Control {
		t.Fatalf("Cmd+Control+Option+5 key = %+v", k2)
	}
}
