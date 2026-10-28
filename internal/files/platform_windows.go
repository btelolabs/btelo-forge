package files

import (
	"io/fs"
	"strings"
	"syscall"
)

// hidden reports whether Explorer hides a file: it has the hidden attribute, or its name starts with a dot as the
// tools that come from Unix name their settings.
func hidden(name string, info fs.FileInfo) bool {
	if strings.HasPrefix(name, ".") {
		return true
	}
	if d, ok := info.Sys().(*syscall.Win32FileAttributeData); ok {
		return d.FileAttributes&syscall.FILE_ATTRIBUTE_HIDDEN != 0
	}
	return false
}

// samePath compares two cleaned paths as NTFS does, without regard to case.
func samePath(a, b string) bool { return strings.EqualFold(a, b) }
