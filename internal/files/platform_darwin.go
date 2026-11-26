//go:build darwin

package files

import (
	"io/fs"
	"strings"
	"syscall"
)

// ufHidden is macOS BSD UF_HIDDEN (chflags hidden, ~/Library, /Volumes, /usr, etc.).
const ufHidden = 0x8000

// hidden reports whether Finder hides a file: its name starts with a dot, it is Finder's custom icon file ("Icon\r"),
// or it carries the BSD UF_HIDDEN flag.
func hidden(name string, info fs.FileInfo) bool {
	if strings.HasPrefix(name, ".") || name == "Icon\r" {
		return true
	}
	if info != nil {
		if st, ok := info.Sys().(*syscall.Stat_t); ok {
			return st.Flags&ufHidden != 0
		}
	}
	return false
}

// samePath compares two cleaned paths as APFS/HFS+ usually does: without regard to case.
func samePath(a, b string) bool { return strings.EqualFold(a, b) }

// nameKey is a name as samePath compares it, to find it by.
func nameKey(name string) string { return foldKey(name) }
