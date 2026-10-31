package keyboard

import (
	_ "embed"
	"encoding/binary"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// Remapping keys below the programs needs no hook and nothing running. Windows reads a table from the registry when a
// user signs in, which is the method of SharpKeys; macOS has hidutil, which works at once and lasts until restart.

// DefaultRemapJSON is the system remaps with only examples, as comments.
//
//go:embed remap.json
var DefaultRemapJSON []byte

// RemapGuide explains the system remaps to whoever edits them, a person or an AI.
const RemapGuide = `These remaps change a key below the programs, with nothing running: in the Windows registry, as SharpKeys does, or
with hidutil on macOS. The JSON has version 1 and "rules", a list of text lines; a blank line or one starting with # is
skipped. Each line takes one key and gives it another's job, or none:

  CapsLock -> Esc
  Right-Alt -> Left-Ctrl
  ScrollLock -> None              the key does nothing

These are single keys: combinations, opening programs and typing text belong to the keyboard rules. Saving keeps the lines;
only the user can apply them, with the button in the Keyboard tab. On Windows that needs an administrator's approval and
takes effect after restarting the computer, for all its users. On macOS it takes effect at once and lasts after a restart,
for this user.`

// ErrCancelled says the user turned down the system's request for approval.
var ErrCancelled = errors.New("the approval was turned down")

// ErrNotWritten says the system took the request and the table did not change.
var ErrNotWritten = errors.New("the remaps did not reach the system")

// Mapping gives the key with scan code From the job of the key with scan code To, 0 for none.
type Mapping struct {
	From, To uint16
}

// MapKind is how a system remaps keys below the programs.
type MapKind int

const (
	NoMap       MapKind = iota
	RegistryMap         // the Windows registry: after the next sign-in, for every user, with approval
	HIDMap              // macOS's hidutil: at once, for this user
)

// SystemMap is what a system offers for remapping keys below the programs.
type SystemMap interface {
	Kind() MapKind
	// Read is the table in effect at the next sign-in.
	Read() ([]Mapping, error)
	// Write replaces the table, asking for the approval the system needs. It waits for the user, so it is not for
	// the interface's goroutine.
	Write(m []Mapping) error
}

// scanCodes are the PC scan codes of the keys; those with E0 in the high byte have the extended prefix.
var scanCodes = func() map[uint16]uint16 {
	m := map[uint16]uint16{
		0x1b: 0x01, '1': 0x02, '2': 0x03, '3': 0x04, '4': 0x05, '5': 0x06, '6': 0x07, '7': 0x08, '8': 0x09, '9': 0x0a, '0': 0x0b,
		0xbd: 0x0c, 0xbb: 0x0d, 0x08: 0x0e, 0x09: 0x0f, 'Q': 0x10, 'W': 0x11, 'E': 0x12, 'R': 0x13, 'T': 0x14, 'Y': 0x15, 'U': 0x16,
		'I': 0x17, 'O': 0x18, 'P': 0x19, 0xdb: 0x1a, 0xdd: 0x1b, 0x0d: 0x1c, vkLCtrl: 0x1d, 'A': 0x1e, 'S': 0x1f, 'D': 0x20,
		'F': 0x21, 'G': 0x22, 'H': 0x23, 'J': 0x24, 'K': 0x25, 'L': 0x26, 0xba: 0x27, 0xde: 0x28, 0xc0: 0x29, vkLShift: 0x2a,
		0xdc: 0x2b, 'Z': 0x2c, 'X': 0x2d, 'C': 0x2e, 'V': 0x2f, 'B': 0x30, 'N': 0x31, 'M': 0x32, 0xbc: 0x33, 0xbe: 0x34, 0xbf: 0x35,
		vkRShift: 0x36, 0x6a: 0x37, vkLAlt: 0x38, 0x20: 0x39, vkCapsLock: 0x3a, 0x90: 0x45, 0x91: 0x46,
		0x67: 0x47, 0x68: 0x48, 0x69: 0x49, 0x6d: 0x4a, 0x64: 0x4b, 0x65: 0x4c, 0x66: 0x4d, 0x6b: 0x4e, 0x61: 0x4f, 0x62: 0x50,
		0x63: 0x51, 0x60: 0x52, 0x6e: 0x53, 0x7a: 0x57, 0x7b: 0x58, 0x87: 0x76,
		vkRCtrl: 0xe01d, 0x6f: 0xe035, 0x2c: 0xe037, vkRAlt: 0xe038, 0x24: 0xe047, 0x26: 0xe048, 0x21: 0xe049, 0x25: 0xe04b,
		0x27: 0xe04d, 0x23: 0xe04f, 0x28: 0xe050, 0x22: 0xe051, 0x2d: 0xe052, 0x2e: 0xe053, vkLWin: 0xe05b, vkRWin: 0xe05c,
		0x5d: 0xe05d, 0xad: 0xe020, 0xae: 0xe02e, 0xaf: 0xe030, 0xb0: 0xe019, 0xb1: 0xe010, 0xb3: 0xe022,
	}
	for i := uint16(0); i < 10; i++ { // F1 to F10
		m[0x70+i] = 0x3b + i
	}
	for i := uint16(0); i < 11; i++ { // F13 to F23
		m[0x7c+i] = 0x64 + i
	}
	return m
}()

var keysByScan = func() map[uint16]uint16 {
	m := map[uint16]uint16{}
	for vk, scan := range scanCodes {
		m[scan] = vk
	}
	return m
}()

// scanOf finds a key's scan code; the plain modifiers are the left ones.
func scanOf(vk uint16) (uint16, bool) {
	switch vk {
	case vkCtrl:
		vk = vkLCtrl
	case vkAlt:
		vk = vkLAlt
	case vkShift:
		vk = vkLShift
	}
	scan, ok := scanCodes[vk]
	return scan, ok
}

// remapKey reads a key of a remap: a name, or Scan and the hexadecimal code of a key that has no name.
func remapKey(text string) (uint16, bool) {
	if hex, ok := strings.CutPrefix(text, "Scan"); ok {
		n, err := strconv.ParseUint(hex, 16, 16)
		return uint16(n), err == nil && n != 0
	}
	c, ok := ParseCombo(text)
	if !ok || !c.plain() {
		return 0, false
	}
	return scanOf(c.Key)
}

// ParseRemap reads the lines of the system remaps.
func ParseRemap(lines []string) ([]Mapping, error) {
	var out []Mapping
	seen := map[uint16]int{}
	for i, line := range lines {
		n := i + 1
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		left, right, ok := cutArrow(line, "->")
		if !ok {
			return nil, &RuleError{Line: n, Problem: BadRemap}
		}
		left, right = strings.TrimSpace(left), strings.TrimSpace(right)
		if right == "" {
			return nil, &RuleError{Line: n, Problem: EmptyAction}
		}
		from, ok := remapKey(left)
		if !ok {
			return nil, &RuleError{Line: n, Problem: NotOneKey, Text: left}
		}
		to := uint16(0)
		if right != "None" {
			if to, ok = remapKey(right); !ok {
				return nil, &RuleError{Line: n, Problem: NotOneKey, Text: right}
			}
		}
		if other, ok := seen[from]; ok {
			return nil, &RuleError{Line: n, Problem: DoubleTrigger, Text: left, Other: other}
		}
		seen[from] = n
		out = append(out, Mapping{from, to})
	}
	return out, nil
}

func scanName(scan uint16) string {
	if vk, ok := keysByScan[scan]; ok {
		return KeyName(vk)
	}
	return fmt.Sprintf("Scan%04X", scan)
}

// FormatRemap writes a table as the lines ParseRemap reads, for remaps made before this app, as with SharpKeys.
func FormatRemap(m []Mapping) []string {
	lines := make([]string, len(m))
	for i, e := range m {
		to := "None"
		if e.To != 0 {
			to = scanName(e.To)
		}
		lines[i] = scanName(e.From) + " -> " + to
	}
	return lines
}

// SameRemap reports whether two tables do the same, in whatever order.
func SameRemap(a, b []Mapping) bool {
	order := func(x, y Mapping) int { return int(x.From) - int(y.From) }
	a, b = slices.Clone(a), slices.Clone(b)
	slices.SortFunc(a, order)
	slices.SortFunc(b, order)
	return slices.Equal(a, b)
}

// encodeScancodeMap is the registry value: eight zero bytes, the number of entries with the closing one, each entry
// as the new code then the old one, and a closing entry of zeros.
func encodeScancodeMap(m []Mapping) []byte {
	out := make([]byte, 8, 16+4*(len(m)+1))
	out = binary.LittleEndian.AppendUint32(out, uint32(len(m)+1))
	for _, e := range m {
		out = binary.LittleEndian.AppendUint16(out, e.To)
		out = binary.LittleEndian.AppendUint16(out, e.From)
	}
	return binary.LittleEndian.AppendUint32(out, 0)
}

func decodeScancodeMap(data []byte) ([]Mapping, error) {
	if len(data) == 0 {
		return nil, nil
	}
	if len(data) < 16 {
		return nil, errors.New("the Scancode Map value is too short")
	}
	count := int(binary.LittleEndian.Uint32(data[8:]))
	if count < 1 || 12+4*count != len(data) {
		return nil, errors.New("the Scancode Map value is damaged")
	}
	var out []Mapping
	for i := range count - 1 {
		at := 12 + 4*i
		out = append(out, Mapping{From: binary.LittleEndian.Uint16(data[at+2:]), To: binary.LittleEndian.Uint16(data[at:])})
	}
	return out, nil
}

// Complete offers what may finish the word before the caret on a line of rules, or of remaps. word is the part
// already typed, which an offer replaces.
func Complete(before string, remap bool) (word string, options []string) {
	trimmed := strings.TrimLeft(before, " \t")
	if strings.HasPrefix(trimmed, "#") || strings.Contains(before, "=>") || strings.Count(before, `"`)%2 == 1 {
		return "", nil
	}
	start := len(before)
	for start > 0 {
		c := before[start-1]
		if c != '-' && (c < '0' || c > '9') && (c < 'A' || c > 'Z') && (c < 'a' || c > 'z') {
			break
		}
		start--
	}
	word = before[start:]
	if rest, ok := strings.CutPrefix(word, "->"); ok {
		word = rest
	}
	if word == "" {
		return "", nil
	}
	lead := strings.TrimSpace(strings.TrimSuffix(before, word))
	arrow := strings.Contains(lead, "->")
	if strings.HasSuffix(lead, "open") || strings.HasSuffix(lead, "type") {
		return "", nil
	}
	lower := strings.ToLower(word)
	offer := func(name string) {
		if len(name) > 1 && strings.HasPrefix(strings.ToLower(name), lower) && !strings.EqualFold(name, word) {
			options = append(options, name)
		}
	}
	if arrow && !remap && !strings.HasSuffix(lead, "+") {
		offer("open")
		offer("type")
	}
	if arrow && remap {
		offer("None")
	}
	for _, k := range keyNames {
		offer(k.names[0])
	}
	for i := 1; i <= 24; i++ {
		offer(fmt.Sprintf("F%d", i))
	}
	return word, options
}
