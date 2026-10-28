//go:build !windows

package files

import (
	"runtime"
	"strings"
)

// decodeLegacyName reads a zip entry's name that is not UTF-8 as well as can be without knowing its code page.
func decodeLegacyName(name string) string { return strings.ToValidUTF8(name, "?") }

// systemTar is the system's tar: libarchive's on macOS, which reads 7-Zip and RAR.
func systemTar() string {
	if runtime.GOOS == "darwin" {
		return "/usr/bin/tar"
	}
	return "tar"
}
