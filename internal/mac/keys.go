package mac

import (
	"unicode"
	"unicode/utf8"
)

// specialKeys maps macOS virtual key codes to Windows virtual-key codes.
var specialKeys = map[int]uint16{
	36: 0x0d, 76: 0x0d, 48: 0x09, 49: 0x20, 51: 0x08, 53: 0x1b, 117: 0x2e, 115: 0x24, 119: 0x23,
	123: 0x25, 124: 0x27, 125: 0x28, 126: 0x26, 116: 0x21, 121: 0x22,
}

// keyVK translates a key press: special keys by code, letters and digits by the character they type.
func keyVK(code int, chars string) (uint16, bool) {
	if vk, ok := specialKeys[code]; ok {
		return vk, true
	}
	r, size := utf8.DecodeRuneInString(chars)
	if size == len(chars) && size > 0 && r < 128 && (unicode.IsLetter(r) || unicode.IsDigit(r)) {
		return uint16(unicode.ToUpper(r)), true
	}
	return 0, false
}
