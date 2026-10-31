package keyboard

// macKeyCodes maps virtual-key codes to macOS key codes. Left out are the keys a Mac keyboard has no code for, which
// an adapter skips when sending. It stands in a file of no system so that it is tested on all of them.
var macKeyCodes = map[uint16]uint16{
	'A': 0, 'S': 1, 'D': 2, 'F': 3, 'H': 4, 'G': 5, 'Z': 6, 'X': 7, 'C': 8, 'V': 9, 'B': 11, 'Q': 12, 'W': 13, 'E': 14,
	'R': 15, 'Y': 16, 'T': 17, '1': 18, '2': 19, '3': 20, '4': 21, '6': 22, '5': 23, 0xbb: 24, '9': 25, '7': 26,
	0xbd: 27, '8': 28, '0': 29, 0xdd: 30, 'O': 31, 'U': 32, 0xdb: 33, 'I': 34, 'P': 35, 0x0d: 36, 'L': 37, 'J': 38,
	0xde: 39, 'K': 40, 0xba: 41, 0xdc: 42, 0xbc: 43, 0xbf: 44, 'N': 45, 'M': 46, 0xbe: 47, 0x09: 48, 0x20: 49,
	0xc0: 50, 0x08: 51, 0x1b: 53,
	vkRWin: 54, vkLWin: 55, vkLShift: 56, vkShift: 56, vkCapsLock: 57, vkLAlt: 58, vkAlt: 58, vkLCtrl: 59, vkCtrl: 59,
	vkRShift: 60, vkRAlt: 61, vkRCtrl: 62,
	0x70: 122, 0x71: 120, 0x72: 99, 0x73: 118, 0x74: 96, 0x75: 97, 0x76: 98, 0x77: 100, 0x78: 101, 0x79: 109, 0x7a: 103, 0x7b: 111,
	0x7c: 105, 0x7d: 107, 0x7e: 113, 0x7f: 106, 0x80: 64, 0x81: 79, 0x82: 80, 0x83: 90,
	0x60: 82, 0x61: 83, 0x62: 84, 0x63: 85, 0x64: 86, 0x65: 87, 0x66: 88, 0x67: 89, 0x68: 91, 0x69: 92,
	0x6a: 67, 0x6b: 69, 0x6d: 78, 0x6e: 65, 0x6f: 75,
	0x24: 115, 0x23: 119, 0x21: 116, 0x22: 121, 0x2e: 117, 0x25: 123, 0x27: 124, 0x28: 125, 0x26: 126,
}

// macVirtualKeys is macKeyCodes turned around; a modifier reads back as its side, not as the plain key.
var macVirtualKeys = func() map[uint16]uint16 {
	m := map[uint16]uint16{}
	for vk, code := range macKeyCodes {
		if vk != vkShift && vk != vkAlt && vk != vkCtrl {
			m[code] = vk
		}
	}
	return m
}()
