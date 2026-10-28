package files

import (
	"os"
	"path/filepath"
	"syscall"
	"unsafe"
)

var multiByteToWideChar = kernel32.NewProc("MultiByteToWideChar")

// decodeLegacyName reads a zip entry's name written in the system's OEM code page, as Explorer reads it: GBK on a
// Chinese Windows.
func decodeLegacyName(name string) string {
	if name == "" {
		return name
	}
	const cpOEM = 1
	b := []byte(name)
	n, _, _ := multiByteToWideChar.Call(cpOEM, 0, uintptr(unsafe.Pointer(&b[0])), uintptr(len(b)), 0, 0)
	if n == 0 {
		return name
	}
	u := make([]uint16, n)
	multiByteToWideChar.Call(cpOEM, 0, uintptr(unsafe.Pointer(&b[0])), uintptr(len(b)), uintptr(unsafe.Pointer(&u[0])), n)
	return syscall.UTF16ToString(u)
}

// systemTar is Windows' own tar, libarchive, not one another program put on the path.
func systemTar() string {
	return filepath.Join(os.Getenv("SystemRoot"), "System32", "tar.exe")
}
