package mac

import (
	"slices"
	"testing"
)

func TestEventsBetweenPollsReachTheNextFrame(t *testing.T) {
	var q inputQueue
	q.next.X, q.next.Down = 10, true
	q.next.Dropped = append(q.next.Dropped, "/tmp/a.mp4")
	q.next.Keys = append(q.next.Keys, KeyEvent{VK: 0x41})
	q.next.Wheel = 2
	q.flip()

	if !slices.Equal(q.last.Dropped, []string{"/tmp/a.mp4"}) || len(q.last.Keys) != 1 || q.last.Wheel != 2 {
		t.Fatalf("the frame lost the collected events: %+v", q.last)
	}
	if len(q.next.Dropped) != 0 || len(q.next.Keys) != 0 || q.next.Wheel != 0 {
		t.Fatalf("the events were handed over twice: %+v", q.next)
	}
	if q.next.X != 10 || !q.next.Down {
		t.Fatalf("pointer state was not carried over: %+v", q.next)
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
