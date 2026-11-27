package mac

import (
	"unicode"
	"unicode/utf8"
)

// specialKeys maps macOS virtual key codes to Windows virtual-key codes: the keys that type nothing, function keys,
// and the number pad's, which Windows tells from the main keys.
var specialKeys = map[int]uint16{
	36: 0x0d, 76: 0x0d, 48: 0x09, 49: 0x20, 51: 0x08, 53: 0x1b, 114: 0x2d, 117: 0x2e, 115: 0x24, 119: 0x23,
	123: 0x25, 124: 0x27, 125: 0x28, 126: 0x26, 116: 0x21, 121: 0x22,
	// Numpad
	82: 0x60, 83: 0x61, 84: 0x62, 85: 0x63, 86: 0x64, 87: 0x65, 88: 0x66, 89: 0x67, 91: 0x68, 92: 0x69,
	67: 0x6a, 69: 0x6b, 78: 0x6d, 65: 0x6e, 75: 0x6f,
	// Function keys F1–F20
	122: 0x70, 120: 0x71, 99: 0x72, 118: 0x73, 96: 0x74, 97: 0x75, 98: 0x76, 100: 0x77,
	101: 0x78, 109: 0x79, 103: 0x7a, 111: 0x7b, 105: 0x7c, 107: 0x7d, 113: 0x7e, 106: 0x7f,
	64: 0x80, 79: 0x81, 80: 0x82, 90: 0x83,
}

// punctuationKeys maps the characters of the punctuation keys, with Shift or without as a US keyboard has them, to
// Windows's virtual-key codes for those keys.
var punctuationKeys = map[rune]uint16{
	';': 0xba, ':': 0xba, '=': 0xbb, '+': 0xbb, ',': 0xbc, '<': 0xbc, '-': 0xbd, '_': 0xbd, '.': 0xbe, '>': 0xbe,
	'/': 0xbf, '?': 0xbf, '`': 0xc0, '~': 0xc0, '[': 0xdb, '{': 0xdb, '\\': 0xdc, '|': 0xdc, ']': 0xdd, '}': 0xdd,
	'\'': 0xde, '"': 0xde,
}

// digitKeys maps the macOS ANSI number-row key codes to Windows digit virtual-key codes.
// NSEvent.charactersIgnoringModifiers still applies Shift (sending "!", "@", "#", "$" for Shift+1..4),
// so falling back to the hardware key code lets Cmd+Shift+1..0 / Ctrl+Shift+1..0 record as '1'..'0'.
var digitKeys = map[int]uint16{
	18: '1', 19: '2', 20: '3', 21: '4', 23: '5', 22: '6', 26: '7', 28: '8', 25: '9', 29: '0',
}

// vkToMacCode maps Windows virtual-key codes back to macOS ANSI virtual key codes for RegisterEventHotKey.
var vkToMacCode = map[uint16]uint32{
	// Letters
	'A': 0, 'S': 1, 'D': 2, 'F': 3, 'H': 4, 'G': 5, 'Z': 6, 'X': 7, 'C': 8, 'V': 9, 'B': 11,
	'Q': 12, 'W': 13, 'E': 14, 'R': 15, 'Y': 16, 'T': 17, 'O': 31, 'U': 32, 'I': 34, 'P': 35,
	'L': 37, 'J': 38, 'K': 40, 'N': 45, 'M': 46,
	// Digits
	'1': 18, '2': 19, '3': 20, '4': 21, '6': 22, '5': 23, '9': 25, '7': 26, '8': 28, '0': 29,
	// Punctuation
	0xbb: 24, 0xbd: 27, 0xdd: 30, 0xdb: 33, 0xde: 39, 0xba: 41, 0xdc: 42, 0xbc: 43, 0xbf: 44, 0xbe: 47, 0xc0: 50,
	// Special & navigation
	0x0d: 36, 0x09: 48, 0x20: 49, 0x08: 51, 0x1b: 53, 0x2d: 114, 0x2e: 117, 0x24: 115, 0x23: 119,
	0x25: 123, 0x27: 124, 0x28: 125, 0x26: 126, 0x21: 116, 0x22: 121,
	// Numpad
	0x60: 82, 0x61: 83, 0x62: 84, 0x63: 85, 0x64: 86, 0x65: 87, 0x66: 88, 0x67: 89, 0x68: 91, 0x69: 92,
	0x6a: 67, 0x6b: 69, 0x6d: 78, 0x6e: 65, 0x6f: 75,
	// Function keys F1–F20
	0x70: 122, 0x71: 120, 0x72: 99, 0x73: 118, 0x74: 96, 0x75: 97, 0x76: 98, 0x77: 100,
	0x78: 101, 0x79: 109, 0x7a: 103, 0x7b: 111, 0x7c: 105, 0x7d: 107, 0x7e: 113, 0x7f: 106,
	0x80: 64, 0x81: 79, 0x82: 80, 0x83: 90,
}

// keyVK translates a key press: special keys by code, letters, digits and punctuation by the character they type.
func keyVK(code int, chars string) (uint16, bool) {
	if vk, ok := specialKeys[code]; ok {
		return vk, true
	}
	r, size := utf8.DecodeRuneInString(chars)
	if size != len(chars) || size == 0 || r >= 128 {
		return 0, false
	}
	if unicode.IsLetter(r) || unicode.IsDigit(r) {
		return uint16(unicode.ToUpper(r)), true
	}
	if vk, ok := punctuationKeys[r]; ok {
		return vk, true
	}
	if vk, ok := digitKeys[code]; ok {
		return vk, true
	}
	return 0, false
}

// macKeyCode translates a Windows virtual-key code back to a macOS key code for global hotkey registration.
func macKeyCode(vk uint16) (uint32, bool) {
	c, ok := vkToMacCode[vk]
	return c, ok
}
