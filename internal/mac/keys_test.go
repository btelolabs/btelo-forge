package mac

import "testing"

func TestKeyVKTranslatesMacKeys(t *testing.T) {
	for _, c := range []struct {
		code  int
		chars string
		vk    uint16
		ok    bool
	}{{51, "", 0x08, true}, {36, "", 0x0d, true}, {0, "a", 'A', true}, {1, "s", 'S', true}, {18, "1", '1', true}, {123, "", 0x25, true}, {27, "-", 0, false}, {0, "å", 0, false}} {
		if vk, ok := keyVK(c.code, c.chars); vk != c.vk || ok != c.ok {
			t.Errorf("keyVK(%d, %q) = %#x %v, want %#x %v", c.code, c.chars, vk, ok, c.vk, c.ok)
		}
	}
}
