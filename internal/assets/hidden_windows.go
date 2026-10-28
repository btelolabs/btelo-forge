package assets

import (
	"os"
	"syscall"
)

// hiddenFile reports whether Windows marks the file hidden or as part of the system.
func hiddenFile(_ string, info os.FileInfo) bool {
	const hidden, system = 0x2, 0x4
	if d, ok := info.Sys().(*syscall.Win32FileAttributeData); ok {
		return d.FileAttributes&(hidden|system) != 0
	}
	return false
}
