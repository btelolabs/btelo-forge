package mac

import "testing"

func TestKeyVKTranslatesMacKeys(t *testing.T) {
	for _, c := range []struct {
		code  int
		chars string
		vk    uint16
		ok    bool
	}{{51, "", 0x08, true}, {36, "", 0x0d, true}, {0, "a", 'A', true}, {1, "s", 'S', true}, {18, "1", '1', true}, {123, "", 0x25, true},
		{27, "-", 0xbd, true}, {24, "=", 0xbb, true}, {24, "+", 0xbb, true}, {33, "[", 0xdb, true}, {42, "\\", 0xdc, true},
		{83, "1", 0x61, true}, {82, "0", 0x60, true}, {69, "+", 0x6b, true}, {78, "-", 0x6d, true},
		{0, "å", 0, false}, {10, "§", 0, false}} {
		if vk, ok := keyVK(c.code, c.chars); vk != c.vk || ok != c.ok {
			t.Errorf("keyVK(%d, %q) = %#x %v, want %#x %v", c.code, c.chars, vk, ok, c.vk, c.ok)
		}
	}
}
