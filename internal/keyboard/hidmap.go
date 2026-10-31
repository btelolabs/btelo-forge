package keyboard

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// hidUsages are the keys' USB usage codes on the keyboard page, which macOS's hidutil remaps. They stand in a file of
// no system so that they are tested on all of them.
var hidUsages = func() map[uint16]uint64 {
	m := map[uint16]uint64{
		0x0d: 0x28, 0x1b: 0x29, 0x08: 0x2a, 0x09: 0x2b, 0x20: 0x2c, 0xbd: 0x2d, 0xbb: 0x2e, 0xdb: 0x2f, 0xdd: 0x30, 0xdc: 0x31,
		0xba: 0x33, 0xde: 0x34, 0xc0: 0x35, 0xbc: 0x36, 0xbe: 0x37, 0xbf: 0x38, vkCapsLock: 0x39, 0x2c: 0x46, 0x91: 0x47,
		0x2d: 0x49, 0x24: 0x4a, 0x21: 0x4b, 0x2e: 0x4c, 0x23: 0x4d, 0x22: 0x4e, 0x27: 0x4f, 0x25: 0x50, 0x28: 0x51, 0x26: 0x52,
		0x90: 0x53, 0x6f: 0x54, 0x6a: 0x55, 0x6d: 0x56, 0x6b: 0x57, 0x60: 0x62, 0x6e: 0x63, 0x5d: 0x65,
		vkLCtrl: 0xe0, vkLShift: 0xe1, vkLAlt: 0xe2, vkLWin: 0xe3, vkRCtrl: 0xe4, vkRShift: 0xe5, vkRAlt: 0xe6, vkRWin: 0xe7,
	}
	for c := uint16('A'); c <= 'Z'; c++ {
		m[c] = uint64(c-'A') + 0x04
	}
	for c := uint16('1'); c <= '9'; c++ {
		m[c] = uint64(c-'1') + 0x1e
	}
	m['0'] = 0x27
	for i := uint16(0); i < 12; i++ { // F1 to F12
		m[0x70+i] = uint64(i) + 0x3a
	}
	for i := uint16(0); i < 12; i++ { // F13 to F24
		m[0x7c+i] = uint64(i) + 0x68
	}
	for i := uint16(0); i < 9; i++ { // the number pad's 1 to 9
		m[0x61+i] = uint64(i) + 0x59
	}
	return m
}()

const hidKeyboardPage = 0x700000000

// hidCode is the code hidutil has for a key given by its scan code.
func hidCode(scan uint16) (uint64, error) {
	vk, ok := keysByScan[scan]
	if ok {
		if usage, found := hidUsages[vk]; found {
			return hidKeyboardPage | usage, nil
		}
	}
	return 0, fmt.Errorf("hidutil cannot remap %s", scanName(scan))
}

// hidArgument is the value of hidutil's UserKeyMapping property for a table. A key with no job maps to code 0.
func hidArgument(m []Mapping) (string, error) {
	entries := make([]string, len(m))
	for i, e := range m {
		from, err := hidCode(e.From)
		if err != nil {
			return "", err
		}
		var to uint64
		if e.To != 0 {
			if to, err = hidCode(e.To); err != nil {
				return "", err
			}
		}
		entries[i] = fmt.Sprintf(`{"HIDKeyboardModifierMappingSrc":0x%X,"HIDKeyboardModifierMappingDst":0x%X}`, from, to)
	}
	return `{"UserKeyMapping":[` + strings.Join(entries, ",") + `]}`, nil
}

var hidEntry = regexp.MustCompile(`"HIDKeyboardModifierMappingSrc":0x([0-9A-F]+),"HIDKeyboardModifierMappingDst":0x([0-9A-F]+)`)

// hidTable reads a table back from what hidArgument wrote.
func hidTable(argument string) []Mapping {
	scans := map[uint64]uint16{}
	for vk, usage := range hidUsages {
		if scan, ok := scanCodes[vk]; ok {
			scans[hidKeyboardPage|usage] = scan
		}
	}
	var out []Mapping
	for _, e := range hidEntry.FindAllStringSubmatch(argument, -1) {
		from, _ := strconv.ParseUint(e[1], 16, 64)
		to, _ := strconv.ParseUint(e[2], 16, 64)
		out = append(out, Mapping{From: scans[from], To: scans[to]})
	}
	return out
}

const hidAgentLabel = "com.btelolabs.forge.keymap"

// hidAgent is the login item that applies a table again at each sign-in, as hidutil's remaps last only until restart.
func hidAgent(argument string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>` + hidAgentLabel + `</string>
	<key>ProgramArguments</key>
	<array>
		<string>/usr/bin/hidutil</string>
		<string>property</string>
		<string>--set</string>
		<string>` + argument + `</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
</dict>
</plist>
`
}
