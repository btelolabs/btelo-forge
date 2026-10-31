// Package keyboard remaps keys, runs actions from key combinations and expands abbreviations, in every program.
//
// Rules are lines of plain text (rules.go), matched by a pure state machine (engine.go). Everything that depends on
// the system sits behind Adapter (adapter.go), one implementation for each. Keys are named by Windows virtual-key
// codes on every system, as in the rest of the app; an adapter translates them.
package keyboard

import (
	"fmt"
	"strings"
)

// The virtual-key codes the package itself needs.
const (
	vkBackspace = 0x08
	vkShift     = 0x10
	vkCtrl      = 0x11
	vkAlt       = 0x12
	vkLWin      = 0x5b
	vkRWin      = 0x5c
	vkLShift    = 0xa0
	vkRShift    = 0xa1
	vkLCtrl     = 0xa2
	vkRCtrl     = 0xa3
	vkLAlt      = 0xa4
	vkRAlt      = 0xa5
	vkCapsLock  = 0x14

	// vkMask is an unassigned key. Tapped while Alt or Win is held, it keeps their release from opening the menu bar
	// or the Start menu when the key pressed with them was swallowed.
	vkMask = 0xe8
)

// keyNames are the names of the keys that are not letters, digits or F1 to F24; the first of each is how the key is
// written back. Left and right modifiers have names of their own, and the plain one matches either.
var keyNames = []struct {
	vk    uint16
	names []string
}{
	{0x08, []string{"Backspace"}}, {0x09, []string{"Tab"}}, {0x0d, []string{"Enter", "Return"}},
	{0x13, []string{"Pause"}}, {0x14, []string{"CapsLock", "Caps"}}, {0x1b, []string{"Esc", "Escape"}},
	{0x20, []string{"Space"}}, {0x21, []string{"PageUp", "PgUp"}}, {0x22, []string{"PageDown", "PgDn"}},
	{0x23, []string{"End"}}, {0x24, []string{"Home"}}, {0x25, []string{"Left"}}, {0x26, []string{"Up"}},
	{0x27, []string{"Right"}}, {0x28, []string{"Down"}}, {0x2c, []string{"PrintScreen"}},
	{0x2d, []string{"Insert", "Ins"}}, {0x2e, []string{"Delete", "Del"}}, {0x5d, []string{"Menu"}},
	{0x6a, []string{"Num*"}}, {0x6b, []string{"Num+"}}, {0x6d, []string{"Num-"}}, {0x6e, []string{"Num."}}, {0x6f, []string{"Num/"}},
	{0x90, []string{"NumLock"}}, {0x91, []string{"ScrollLock"}},
	{vkShift, []string{"Shift"}}, {vkCtrl, []string{"Ctrl", "Control"}}, {vkAlt, []string{"Alt", "Option"}},
	{vkLWin, []string{"Win", "Cmd", "Command", "Left-Win", "LWin"}}, {vkRWin, []string{"Right-Win", "RWin", "RCmd"}},
	{vkLShift, []string{"Left-Shift", "LShift"}}, {vkRShift, []string{"Right-Shift", "RShift"}},
	{vkLCtrl, []string{"Left-Ctrl", "LCtrl"}}, {vkRCtrl, []string{"Right-Ctrl", "RCtrl"}},
	{vkLAlt, []string{"Left-Alt", "LAlt"}}, {vkRAlt, []string{"Right-Alt", "RAlt"}},
	{0xad, []string{"Mute"}}, {0xae, []string{"VolumeDown"}}, {0xaf, []string{"VolumeUp"}},
	{0xb0, []string{"NextTrack"}}, {0xb1, []string{"PreviousTrack"}}, {0xb3, []string{"PlayPause"}},
	{0xba, []string{";"}}, {0xbb, []string{"="}}, {0xbc, []string{","}}, {0xbd, []string{"-"}}, {0xbe, []string{"."}},
	{0xbf, []string{"/"}}, {0xc0, []string{"`"}}, {0xdb, []string{"["}}, {0xdc, []string{`\`}}, {0xdd, []string{"]"}}, {0xde, []string{"'"}},
}

var keysByName = func() map[string]uint16 {
	m := map[string]uint16{}
	for _, k := range keyNames {
		for _, n := range k.names {
			m[strings.ToLower(n)] = k.vk
		}
	}
	return m
}()

// KeyName is how a key is written in a rule.
func KeyName(vk uint16) string {
	switch {
	case vk >= 'A' && vk <= 'Z', vk >= '0' && vk <= '9':
		return string(rune(vk))
	case vk >= 0x70 && vk <= 0x87:
		return fmt.Sprintf("F%d", vk-0x6f)
	case vk >= 0x60 && vk <= 0x69:
		return fmt.Sprintf("Num%d", vk-0x60)
	}
	for _, k := range keyNames {
		if k.vk == vk {
			return k.names[0]
		}
	}
	return fmt.Sprintf("0x%02X", vk)
}

// keyCode finds a key by name, in either case, or by the code a key without a name is written with.
func keyCode(name string) (uint16, bool) {
	lower := strings.ToLower(name)
	if vk, ok := keysByName[lower]; ok {
		return vk, true
	}
	var n int
	if len(lower) == 1 && (lower[0] >= 'a' && lower[0] <= 'z' || lower[0] >= '0' && lower[0] <= '9') {
		return uint16(strings.ToUpper(lower)[0]), true
	}
	if _, err := fmt.Sscanf(lower, "f%d", &n); err == nil && n >= 1 && n <= 24 && fmt.Sprintf("f%d", n) == lower {
		return uint16(0x6f + n), true
	}
	if _, err := fmt.Sscanf(lower, "num%d", &n); err == nil && n >= 0 && n <= 9 && fmt.Sprintf("num%d", n) == lower {
		return uint16(0x60 + n), true
	}
	if _, err := fmt.Sscanf(lower, "0x%x", &n); err == nil && n > 0 && n < 0xff {
		return uint16(n), true
	}
	return 0, false
}

// Combo is a key with the modifiers held with it. A modifier on its own is a Combo with that Key and no flags.
type Combo struct {
	Ctrl, Alt, Shift, Win bool
	Key                   uint16
}

// String is the combination as a rule writes it.
func (c Combo) String() string {
	var parts []string
	for _, m := range []struct {
		on   bool
		name string
	}{{c.Ctrl, "Ctrl"}, {c.Alt, "Alt"}, {c.Shift, "Shift"}, {c.Win, "Win"}} {
		if m.on {
			parts = append(parts, m.name)
		}
	}
	return strings.Join(append(parts, KeyName(c.Key)), "+")
}

// ParseCombo reads a combination such as Ctrl+Alt+T or Right-Alt.
func ParseCombo(text string) (Combo, bool) {
	var c Combo
	parts := strings.Split(strings.TrimSpace(text), "+")
	if strings.HasSuffix(text, "Num+") || strings.HasSuffix(text, "num+") { // the number pad's plus is the one key with + in its name
		parts = append(parts[:len(parts)-2], "Num+")
	}
	for i, p := range parts {
		vk, ok := keyCode(strings.TrimSpace(p))
		if !ok {
			return Combo{}, false
		}
		if i == len(parts)-1 {
			c.Key = vk
			return c, true
		}
		switch generic(vk) {
		case vkCtrl:
			c.Ctrl = true
		case vkAlt:
			c.Alt = true
		case vkShift:
			c.Shift = true
		case vkLWin:
			c.Win = true
		default:
			return Combo{}, false
		}
	}
	return Combo{}, false
}

// generic is the plain modifier of a left or right one, and any other key itself.
func generic(vk uint16) uint16 {
	switch vk {
	case vkLShift, vkRShift:
		return vkShift
	case vkLCtrl, vkRCtrl:
		return vkCtrl
	case vkLAlt, vkRAlt:
		return vkAlt
	case vkRWin:
		return vkLWin
	}
	return vk
}

// isModifier reports whether a key is a modifier, on either side or plain.
func isModifier(vk uint16) bool {
	switch generic(vk) {
	case vkShift, vkCtrl, vkAlt, vkLWin:
		return true
	}
	return false
}

// usKeys are the characters of the keys that type one on a US keyboard, without Shift and with it. Abbreviations are
// matched by keys, so that they work whatever input method is on.
var usKeys = func() map[uint16][2]rune {
	m := map[uint16][2]rune{
		0xba: {';', ':'}, 0xbb: {'=', '+'}, 0xbc: {',', '<'}, 0xbd: {'-', '_'}, 0xbe: {'.', '>'}, 0xbf: {'/', '?'},
		0xc0: {'`', '~'}, 0xdb: {'[', '{'}, 0xdc: {'\\', '|'}, 0xdd: {']', '}'}, 0xde: {'\'', '"'}, 0x20: {' ', ' '},
	}
	for c := 'A'; c <= 'Z'; c++ {
		m[uint16(c)] = [2]rune{c + 'a' - 'A', c}
	}
	for i, shifted := range []rune(")!@#$%^&*(") {
		m[uint16('0'+i)] = [2]rune{rune('0' + i), shifted}
	}
	return m
}()

// typable reports whether a keyboard can type c, so an abbreviation can use it.
func typable(c rune) bool {
	for _, k := range usKeys {
		if k[0] == c || k[1] == c {
			return true
		}
	}
	return false
}
