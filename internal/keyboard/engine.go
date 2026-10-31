package keyboard

import (
	"strings"
	"time"
)

// Stroke is a key going down or up.
type Stroke struct {
	VK   uint16
	Down bool
}

// world is what the engine asks the system while it decides.
type world interface {
	KeyDown(vk uint16) bool
}

// Effect is what a key event comes to.
type Effect struct {
	Swallow bool     // the event does not reach the programs
	Keys    []Stroke // sent at once, in order
	Type    string   // then typed
	Open    string   // then opened
	Args    string
}

// Engine decides what each key event does. It keeps what a press began until the release, so it is not safe for
// concurrent use; Service serializes it.
type Engine struct {
	byKey map[uint16][]*Rule // several rules may share a key, with different modifiers
	abbrs []*Rule
	held  map[uint16]*Rule // the source keys that are down and took a rule
	typed []rune           // the keys typed lately, for abbreviations
	now   func() time.Time
}

// NewEngine prepares rules for matching.
func NewEngine(rules []Rule) *Engine {
	e := &Engine{byKey: map[uint16][]*Rule{}, held: map[uint16]*Rule{}, now: time.Now}
	for i := range rules {
		if r := &rules[i]; r.Abbr != "" {
			e.abbrs = append(e.abbrs, r)
		} else {
			e.byKey[r.From.Key] = append(e.byKey[r.From.Key], r)
		}
	}
	return e
}

// Release lets go of everything the engine holds down, as when the rules change or the hook ends.
func (e *Engine) Release() []Stroke {
	var keys []Stroke
	for vk, r := range e.held {
		if r.hold() {
			keys = append(keys, Stroke{r.To.Key, false})
		}
		delete(e.held, vk)
	}
	return keys
}

// Key takes a key going down or up, or repeating while held, which arrives as another down.
func (e *Engine) Key(vk uint16, down bool, w world) Effect {
	if !down {
		r, ok := e.held[vk]
		if !ok {
			return Effect{}
		}
		delete(e.held, vk)
		out := Effect{Swallow: true}
		if r.hold() {
			out.Keys = []Stroke{{r.To.Key, false}}
		}
		return out
	}
	if r, ok := e.held[vk]; ok {
		if r.Kind != SendKeys {
			return Effect{Swallow: true}
		}
		return e.fire(r, w)
	}
	mods := modifiers(w)
	if r := e.match(vk, mods); r != nil {
		e.held[vk] = r
		e.typed = e.typed[:0]
		return e.fire(r, w)
	}
	return e.abbreviate(vk, mods, w)
}

// fire is a rule's action, for its first press or a repeat.
func (e *Engine) fire(r *Rule, w world) Effect {
	out := Effect{Swallow: true}
	switch r.Kind {
	case SendKeys:
		if r.hold() {
			out.Keys = []Stroke{{r.To.Key, true}}
		} else {
			out.Keys = tap(r.To, w)
		}
	case TypeText:
		// The modifiers of the trigger would change what is typed. They stay up until pressed again, as they are
		// let go by then and pressing them back could leave them stuck.
		out.Keys = masked(w)
		for _, vk := range modifierKeys {
			if w.KeyDown(vk) {
				out.Keys = append(out.Keys, Stroke{vk, false})
			}
		}
		out.Type = e.expand(r.Text)
	case OpenItem:
		out.Keys, out.Open, out.Args = masked(w), r.Text, r.Args
	}
	return out
}

var modifierKeys = []uint16{vkLCtrl, vkRCtrl, vkLAlt, vkRAlt, vkLShift, vkRShift, vkLWin, vkRWin}

// modifiers are the modifiers down. Those a rule holds count too: its strokes are real to the system.
func modifiers(w world) Combo {
	var c Combo
	for _, vk := range modifierKeys {
		if w.KeyDown(vk) {
			switch generic(vk) {
			case vkCtrl:
				c.Ctrl = true
			case vkAlt:
				c.Alt = true
			case vkShift:
				c.Shift = true
			case vkLWin:
				c.Win = true
			}
		}
	}
	return c
}

// match finds the rule for a press, by the key itself and then, for a side of a modifier, by the plain modifier.
func (e *Engine) match(vk uint16, mods Combo) *Rule {
	for _, key := range []uint16{vk, generic(vk)} {
		for _, r := range e.byKey[key] {
			from := r.From
			from.Key = 0
			if from == mods {
				return r
			}
		}
	}
	return nil
}

// tap presses a combination once, with only its own modifiers down, and leaves the keyboard as it found it.
func tap(c Combo, w world) []Stroke {
	wanted := func(vk uint16) bool {
		switch generic(vk) {
		case vkCtrl:
			return c.Ctrl
		case vkAlt:
			return c.Alt
		case vkShift:
			return c.Shift
		}
		return c.Win
	}
	keys := masked(w)
	var lifted []uint16
	for _, vk := range modifierKeys {
		if w.KeyDown(vk) && !wanted(vk) {
			keys = append(keys, Stroke{vk, false})
			lifted = append(lifted, vk)
		}
	}
	var added []uint16
	for _, m := range []struct {
		on   bool
		l, r uint16
	}{{c.Ctrl, vkLCtrl, vkRCtrl}, {c.Alt, vkLAlt, vkRAlt}, {c.Shift, vkLShift, vkRShift}, {c.Win, vkLWin, vkRWin}} {
		if m.on && !w.KeyDown(m.l) && !w.KeyDown(m.r) {
			keys = append(keys, Stroke{m.l, true})
			added = append(added, m.l)
		}
	}
	keys = append(keys, Stroke{c.Key, true}, Stroke{c.Key, false})
	for _, vk := range added {
		keys = append(keys, Stroke{vk, false})
	}
	for _, vk := range lifted {
		keys = append(keys, Stroke{vk, true})
	}
	return keys
}

// masked is the tap that keeps Alt or Win, held with a key that was swallowed, from acting alone when let go.
func masked(w world) []Stroke {
	for _, vk := range []uint16{vkLAlt, vkRAlt, vkLWin, vkRWin} {
		if w.KeyDown(vk) {
			return []Stroke{{vkMask, true}, {vkMask, false}}
		}
	}
	return nil
}

const typedMemory = 64

// abbreviate follows typing, and replaces an abbreviation when its last key arrives.
func (e *Engine) abbreviate(vk uint16, mods Combo, w world) Effect {
	if len(e.abbrs) == 0 || isModifier(vk) {
		return Effect{}
	}
	if vk == vkBackspace {
		if len(e.typed) > 0 {
			e.typed = e.typed[:len(e.typed)-1]
		}
		return Effect{}
	}
	key, ok := usKeys[vk]
	if !ok || mods.Ctrl || mods.Alt || mods.Win {
		e.typed = e.typed[:0]
		return Effect{}
	}
	c := key[0]
	if mods.Shift {
		c = key[1]
	}
	e.typed = append(e.typed, c)
	if len(e.typed) > typedMemory {
		e.typed = append(e.typed[:0], e.typed[len(e.typed)-typedMemory:]...)
	}
	text := string(e.typed)
	for _, r := range e.abbrs {
		if !strings.HasSuffix(text, r.Abbr) {
			continue
		}
		e.typed = e.typed[:0]
		out := Effect{Swallow: true, Type: e.expand(r.Text)}
		for range len([]rune(r.Abbr)) - 1 { // the last key has not reached the program
			out.Keys = append(out.Keys, Stroke{vkBackspace, true}, Stroke{vkBackspace, false})
		}
		return out
	}
	return Effect{}
}

// expand fills in the date and time.
func (e *Engine) expand(text string) string {
	if !strings.Contains(text, "{") {
		return text
	}
	now := e.now()
	return strings.NewReplacer("{date}", now.Format("2006-01-02"), "{time}", now.Format("15:04")).Replace(text)
}
