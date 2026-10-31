package keyboard

import "errors"

// ErrUnsupported says this system has no adapter.
var ErrUnsupported = errors.New("keyboard rules are not available on this system")

// ErrPermission says the system has not yet let the app watch the keyboard; it asked the user when Start failed.
var ErrPermission = errors.New("the system has not allowed this app to watch the keyboard")

// Adapter is what the keyboard rules need from a system. Keys are virtual-key codes; an adapter translates them.
type Adapter interface {
	// Start watches every key in the system. handle is called for each key going down, repeating or up, except those
	// sent by programs, and returns whether to keep the key from the programs. It must return quickly: the
	// whole system's typing waits for it. Start returns once the watch is on, or why it could not begin.
	Start(handle func(vk uint16, down bool) bool) error
	// Stop ends the watch and returns when no handle call is running.
	Stop()
	// Send presses and releases keys, in order. It may be called from handle.
	Send(keys []Stroke) error
	// Type enters text whatever the keyboard layout. It may take a while, so it is not called from handle.
	Type(text string) error
	// Open opens a program, file or web address, with arguments for a program.
	Open(target, args string) error
	// KeyDown reports whether a key is down now, as the system sees it. handle calls it, through the engine.
	KeyDown(vk uint16) bool
	// CanRemap reports whether the system lets a key be taken as the source of a rule.
	CanRemap(vk uint16) bool
}
