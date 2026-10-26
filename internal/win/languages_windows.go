package win

import (
	"syscall"
	"unsafe"
)

var preferredLanguages = kernel32.NewProc("GetUserPreferredUILanguages")

// Languages lists the user's preferred display languages as BCP 47 tags, most preferred first.
func Languages() []string {
	const byName = 0x8 // MUI_LANGUAGE_NAME
	var count, size uint32
	if r, _, _ := preferredLanguages.Call(byName, uintptr(unsafe.Pointer(&count)), 0, uintptr(unsafe.Pointer(&size))); r == 0 || size == 0 {
		return nil
	}
	buf := make([]uint16, size)
	if r, _, _ := preferredLanguages.Call(byName, uintptr(unsafe.Pointer(&count)), uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size))); r == 0 {
		return nil
	}
	return splitMultiString(buf)
}

// splitMultiString reads a list of NUL-terminated strings that ends with an empty one.
func splitMultiString(buf []uint16) []string {
	var out []string
	start := 0
	for i, c := range buf {
		if c != 0 {
			continue
		}
		if i == start {
			break
		}
		out = append(out, syscall.UTF16ToString(buf[start:i]))
		start = i + 1
	}
	return out
}
