package keyboard

import (
	"encoding/hex"
	"errors"
	"slices"
	"strings"
	"testing"
)

func TestParseRemap(t *testing.T) {
	m, err := ParseRemap([]string{"# note", "", "CapsLock -> Esc", "Right-Alt -> Left-Ctrl", "ScrollLock -> None", "Ctrl -> Alt", "ScanE06A -> F1"})
	if err != nil {
		t.Fatal(err)
	}
	want := []Mapping{{0x3a, 0x01}, {0xe038, 0x1d}, {0x46, 0}, {0x1d, 0x38}, {0xe06a, 0x3b}}
	if !slices.Equal(m, want) {
		t.Fatalf("%#v", m)
	}
}

func TestRemapProblems(t *testing.T) {
	for _, c := range []struct {
		lines   []string
		line    int
		problem int
	}{
		{[]string{"nonsense"}, 1, BadRemap},
		{[]string{"A ->"}, 1, EmptyAction},
		{[]string{"Ctrl+A -> B"}, 1, NotOneKey},
		{[]string{"A -> Ctrl+B"}, 1, NotOneKey},
		{[]string{"A -> open \"x\""}, 1, NotOneKey},
		{[]string{"", "Pause -> A"}, 2, NotOneKey}, // its code has three bytes, which the table cannot hold
		{[]string{"A -> B", "a -> C"}, 2, DoubleTrigger},
	} {
		_, err := ParseRemap(c.lines)
		var re *RuleError
		if !errors.As(err, &re) || re.Line != c.line || re.Problem != c.problem {
			t.Errorf("%q: %v", c.lines, err)
		}
	}
}

// The value here is the one SharpKeys and Microsoft's documentation give for Caps Lock as Left Ctrl.
func TestScancodeMapFormat(t *testing.T) {
	data := encodeScancodeMap([]Mapping{{0x3a, 0x1d}})
	if got := hex.EncodeToString(data); got != "0000000000000000"+"02000000"+"1d003a00"+"00000000" {
		t.Fatal(got)
	}
	back, err := decodeScancodeMap(data)
	if err != nil || !slices.Equal(back, []Mapping{{0x3a, 0x1d}}) {
		t.Fatalf("%v %v", back, err)
	}
	if m, err := decodeScancodeMap(encodeScancodeMap(nil)); err != nil || m != nil {
		t.Fatalf("empty: %v %v", m, err)
	}
	if _, err := decodeScancodeMap(data[:len(data)-2]); err == nil {
		t.Fatal("a damaged value passed")
	}
}

func TestRemapReadsBack(t *testing.T) {
	table := []Mapping{{0x3a, 0x01}, {0xe038, 0x1d}, {0x46, 0}, {0xe06a, 0x3b}}
	m, err := ParseRemap(FormatRemap(table))
	if err != nil || !slices.Equal(m, table) {
		t.Fatalf("%v %v", m, err)
	}
	if !SameRemap(table, []Mapping{{0xe06a, 0x3b}, {0x46, 0}, {0x3a, 0x01}, {0xe038, 0x1d}}) || SameRemap(table, table[:1]) {
		t.Fatal("SameRemap")
	}
}

func TestEveryScanCodeIsOneKey(t *testing.T) {
	if len(keysByScan) != len(scanCodes) {
		t.Fatalf("%d codes for %d keys", len(keysByScan), len(scanCodes))
	}
	for c := uint16('A'); c <= 'Z'; c++ {
		if _, ok := scanCodes[c]; !ok {
			t.Errorf("no code for %c", c)
		}
	}
	for vk := uint16(0x70); vk <= 0x87; vk++ {
		if _, ok := scanCodes[vk]; !ok {
			t.Errorf("no code for %s", KeyName(vk))
		}
	}
}

func TestCompleteKeyNames(t *testing.T) {
	has := func(before string, remap bool, want string) {
		t.Helper()
		_, options := Complete(before, remap)
		if !slices.Contains(options, want) {
			t.Errorf("%q offered %v, not %s", before, options, want)
		}
	}
	has("Cap", false, "CapsLock")
	has("cap", false, "CapsLock")
	has("Right-", false, "Right-Alt")
	has("Ri", false, "Right-Alt")
	has("Ctrl+Alt+Pa", false, "PageUp")
	has("CapsLock -> Es", false, "Esc")
	has("CapsLock->Es", false, "Esc")
	has("CapsLock -> Ty", false, "type")
	has("CapsLock -> op", false, "open")
	has("CapsLock -> No", true, "None")
	has("F", false, "F12")
	if word, _ := Complete("Ctrl+Alt+Pa", false); word != "Pa" {
		t.Errorf("word %q", word)
	}
	for _, before := range []string{"", "  ", "# Cap", `F1 -> type "Cap`, ";mail => Cap", "CapsLock"} {
		if _, options := Complete(before, false); len(options) != 0 {
			t.Errorf("%q offered %v", before, options)
		}
	}
	if _, options := Complete("A -> op", true); len(options) != 0 {
		t.Errorf("remaps offered %v", options)
	}
	if _, options := Complete("Op", false); slices.Contains(options, "open") {
		t.Errorf("open before the arrow: %v", options)
	}
}

func TestNamesAreEnglishWithSides(t *testing.T) {
	for _, name := range []string{"Right-Alt", "Left-Ctrl", "Right-Shift", "Right-Win"} {
		c, ok := ParseCombo(name)
		if !ok || c.String() != name {
			t.Errorf("%s: %s", name, c)
		}
	}
	if c, ok := ParseCombo("ralt"); !ok || !strings.EqualFold(c.String(), "Right-Alt") {
		t.Errorf("the short name: %s", c)
	}
}

func TestHIDTableReadsBack(t *testing.T) {
	table := []Mapping{{0x3a, 0x01}, {0xe038, 0x1d}, {0x46, 0}}
	arg, err := hidArgument(table)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"UserKeyMapping":[{"HIDKeyboardModifierMappingSrc":0x700000039,"HIDKeyboardModifierMappingDst":0x700000029},` +
		`{"HIDKeyboardModifierMappingSrc":0x7000000E6,"HIDKeyboardModifierMappingDst":0x7000000E0},` +
		`{"HIDKeyboardModifierMappingSrc":0x700000047,"HIDKeyboardModifierMappingDst":0x0}]}`
	if arg != want {
		t.Fatal(arg)
	}
	if back := hidTable(hidAgent(arg)); !slices.Equal(back, table) {
		t.Fatalf("%v", back)
	}
	if arg, err := hidArgument(nil); err != nil || arg != `{"UserKeyMapping":[]}` {
		t.Fatalf("%s %v", arg, err)
	}
	if _, err := hidArgument([]Mapping{{0xe020, 0x01}}); err == nil {
		t.Fatal("a key hidutil cannot name passed") // Mute is not on the keyboard page
	}
}

func TestHIDUsagesAreDistinctAndCoverTheKeyboard(t *testing.T) {
	seen := map[uint64]string{}
	for vk, usage := range hidUsages {
		if other, ok := seen[usage]; ok {
			t.Errorf("usage %#x is both %s and %s", usage, other, KeyName(vk))
		}
		seen[usage] = KeyName(vk)
		if _, ok := scanCodes[vk]; !ok {
			t.Errorf("%s has a usage but no scan code", KeyName(vk))
		}
	}
	if len(hidUsages) < 100 {
		t.Fatalf("%d usages", len(hidUsages))
	}
}
