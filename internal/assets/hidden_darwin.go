//go:build darwin

package assets

import (
	"os"
	"syscall"
)

// ufHidden is macOS BSD UF_HIDDEN (Finder hidden flag, e.g. ~/Library, /Volumes, chflags hidden).
const ufHidden = 0x8000

// hiddenFile reports whether macOS marks the file hidden via BSD UF_HIDDEN or Finder's custom icon file ("Icon\r").
func hiddenFile(_ string, info os.FileInfo) bool {
	if info.Name() == "Icon\r" {
		return true
	}
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return st.Flags&ufHidden != 0
	}
	return false
}
