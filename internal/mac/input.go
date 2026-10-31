package mac

// PointerEvent is a button transition.
type PointerEvent struct {
	X, Y   float32
	Right  bool
	Middle bool
	Down   bool
	Double bool
}

// KeyEvent is a key press translated to a Windows virtual-key code; Command counts as Control.
type KeyEvent struct {
	VK               uint16
	Ctrl, Shift, Alt bool
}

// Input holds the state at the last Poll and the events since the Poll before it.
type Input struct {
	X, Y    float32
	Down    bool
	Pointer []PointerEvent
	Wheel   float32
	Ctrl    bool
	Shift   bool
	Keys    []KeyEvent
	Up      []uint16 // the keys let go, as Windows virtual-key codes
	Typed   []rune
	Dropped []string
}

// inputQueue collects events into next and hands them over at each poll. Cocoa delivers some events, such as file
// drops, from the run loop while the editor waits between frames, so nothing may be cleared before it is handed over.
type inputQueue struct {
	last, next Input
}

// flip makes what was collected since the previous flip the current input, and starts collecting afresh with the
// pointer and modifier state carried over.
func (q *inputQueue) flip() {
	q.last, q.next = q.next, q.last
	n := &q.next
	n.X, n.Y, n.Down, n.Ctrl, n.Shift = q.last.X, q.last.Y, q.last.Down, q.last.Ctrl, q.last.Shift
	n.Pointer, n.Keys, n.Up, n.Typed, n.Dropped = n.Pointer[:0], n.Keys[:0], n.Up[:0], n.Typed[:0], n.Dropped[:0]
	n.Wheel = 0
}
