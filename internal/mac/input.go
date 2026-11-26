package mac

// Outside is where the pointer is said to be once it has left the window: far from anything drawn there.
const Outside = -1 << 20

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
	Alt     bool
	Keys    []KeyEvent
	Up      []uint16 // the keys let go, as Windows virtual-key codes
	Typed   []rune
	Dropped []string
	Opened  []string // files opened from Finder / Launch Services while the app runs
	Hotkeys []int    // indexes, in the list given to SetHotkeys, of global hotkeys pressed
}

// inputQueue collects events into next and hands them over at each poll. Cocoa delivers some events, such as file
// drops, from the run loop while the editor waits between frames, so nothing may be cleared before it is handed over.
type inputQueue struct {
	last, next Input
	buttons    [3]bool // left, right, middle held state
}

func (q *inputQueue) anyDown() bool {
	return q.buttons[0] || q.buttons[1] || q.buttons[2] || q.next.Down
}

func (q *inputQueue) setButton(button int, down bool) {
	if button >= 0 && button < 3 {
		q.buttons[button] = down
	}
	if button == 0 {
		q.next.Down = down
	}
}

func (q *inputQueue) releaseAll() {
	for b := 0; b < 3; b++ {
		if q.buttons[b] {
			q.buttons[b] = false
			q.next.Pointer = append(q.next.Pointer, PointerEvent{
				X: q.next.X, Y: q.next.Y, Right: b == 1, Middle: b == 2, Down: false,
			})
		}
	}
	q.next.Down = false
}

// flip makes what was collected since the previous flip the current input, and starts collecting afresh with the
// pointer and modifier state carried over.
func (q *inputQueue) flip() {
	q.last, q.next = q.next, q.last
	n := &q.next
	n.X, n.Y, n.Down, n.Ctrl, n.Shift, n.Alt = q.last.X, q.last.Y, q.last.Down, q.last.Ctrl, q.last.Shift, q.last.Alt
	n.Pointer = n.Pointer[:0]
	n.Keys = n.Keys[:0]
	n.Up = n.Up[:0]
	n.Typed = n.Typed[:0]
	n.Dropped = n.Dropped[:0]
	n.Opened = n.Opened[:0]
	n.Hotkeys = n.Hotkeys[:0]
	n.Wheel = 0
}
