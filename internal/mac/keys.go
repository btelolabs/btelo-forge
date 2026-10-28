package mac

import (
	"unicode"
	"unicode/utf8"
)

// specialKeys maps macOS virtual key codes to Windows virtual-key codes: the keys that type nothing, and the number
// pad's, which Windows tells from the main keys.
var specialKeys = map[int]uint16{
	36: 0x0d, 76: 0x0d, 48: 0x09, 49: 0x20, 51: 0x08, 53: 0x1b, 117: 0x2e, 115: 0x24, 119: 0x23,
	123: 0x25, 124: 0x27, 125: 0x28, 126: 0x26, 116: 0x21, 121: 0x22,
	82: 0x60, 83: 0x61, 84: 0x62, 85: 0x63, 86: 0x64, 87: 0x65, 88: 0x66, 89: 0x67, 91: 0x68, 92: 0x69,
	67: 0x6a, 69: 0x6b, 78: 0x6d, 65: 0x6e, 75: 0x6f,
}

// punctuationKeys maps the characters of the punctuation keys, with Shift or without as a US keyboard has them, to
// Windows's virtual-key codes for those keys.
var punctuationKeys = map[rune]uint16{
	';': 0xba, ':': 0xba, '=': 0xbb, '+': 0xbb, ',': 0xbc, '<': 0xbc, '-': 0xbd, '_': 0xbd, '.': 0xbe, '>': 0xbe,
	'/': 0xbf, '?': 0xbf, '`': 0xc0, '~': 0xc0, '[': 0xdb, '{': 0xdb, '\\': 0xdc, '|': 0xdc, ']': 0xdd, '}': 0xdd,
	'\'': 0xde, '"': 0xde,
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
	vk, ok := punctuationKeys[r]
	return vk, ok
}
