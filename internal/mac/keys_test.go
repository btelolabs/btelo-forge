package mac

import "testing"

func TestKeyVKTranslatesMacKeys(t *testing.T) {
	for _, c := range []struct {
		code  int
		chars string
		vk    uint16
		ok    bool
	}{{51, " ", 0x08, true}, {36, "\r", 0x0d, true}, {0, "a", 'A', true}, {1, "s", 'S', true}, {18, "1", '1', true}, {123, "", 0x25, true},
		{27, "-", 0xbd, true}, {24, "=", 0xbb, true}, {24, "+", 0xbb, true}, {33, "[", 0xdb, true}, {42, "\\", 0xdc, true},
		{83, "1", 0x61, true}, {82, "0", 0x60, true}, {69, "+", 0x6b, true}, {78, "-", 0x6d, true},
		// Function keys & Insert
		{122, "\uf704", 0x70, true}, {120, "\uf705", 0x71, true}, {96, "\uf708", 0x74, true},
		{103, "\uf70e", 0x7a, true}, {111, "\uf70f", 0x7b, true}, {114, "", 0x2d, true},
		// Shifted top-row digits (charactersIgnoringModifiers still applies Shift on macOS)
		{18, "!", '1', true}, {19, "@", '2', true}, {20, "#", '3', true}, {21, "$", '4', true}, {29, ")", '0', true},
		{0, "å", 0, false}, {10, "§", 0, false}} {
		if vk, ok := keyVK(c.code, c.chars); vk != c.vk || ok != c.ok {
			t.Errorf("keyVK(%d, %q) = %#x %v, want %#x %v", c.code, c.chars, vk, ok, c.vk, c.ok)
		}
	}
}

func TestMacKeyCodeRoundTrip(t *testing.T) {
	for _, vk := range []uint16{'A', 'Z', '0', '9', 0x70, 0x71, 0x74, 0x7b, 0x1b, 0x20, 0x0d, 0x25, 0x2d, 0xbd, 0xbb} {
		code, ok := macKeyCode(vk)
		if !ok {
			t.Errorf("macKeyCode(%#x) not found", vk)
			continue
		}
		if vk >= 0x70 && vk <= 0x83 || vk == 0x1b || vk == 0x20 || vk == 0x0d || vk == 0x25 || vk == 0x2d {
			got, ok := keyVK(int(code), "")
			if !ok || got != vk {
				t.Errorf("round-trip %#x -> %d -> %#x (%v)", vk, code, got, ok)
			}
		}
	}
}
